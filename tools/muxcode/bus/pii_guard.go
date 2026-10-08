package bus

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PII pipe guard (MUX-203): on a PII-sensitive role, a command known to print
// secrets — an environment dump (`env`, `printenv`, `ps` with its environment
// flag) or a read of a muxcode config file — is denied unless it pipes
// straight into `muxcode pii-scrub`. The history row is scrubbed either way
// (MUX-179); the agent's own conversation is not, and on Claude no hook can
// rewrite a result that exited non-zero (MUX-203 Phase 1), so refusing the
// bare form before it runs is the floor beneath every provider road. It runs
// wherever the PreToolUse guard does — Claude and the Codex hook road.
//
// The scrubber matches label=value, so it must see the NAME= labels: it has to
// be the very next stage (`env | cut -d= -f2 | muxcode pii-scrub` hands it
// bare values), and `printenv NAME`, which prints bare values, is denied even
// piped. Detection is by command position after env assignments and simple
// wrappers: a floor for habits, not a sandbox.

const (
	leakEnvironment = "process environment variables"
	leakConfig      = "a muxcode config file"
	leakValues      = "bare variable values with no NAME= label, which the scrubber cannot recognise as secrets"
)

// CheckPIIPipeGuard denies a leaky statement on a PII-sensitive role — a spawn
// worker judged by its base role — unless it pipes straight into a bare
// `muxcode pii-scrub` and prints labelled lines. Nil for any other role or call.
func CheckPIIPipeGuard(role, command string) *GuardDecision {
	base := SpawnBaseRole(BusSession(), role)
	if !IsPIISensitiveRole(base) {
		return nil
	}
	stmts, seps := parseShellStatements(command)
	for i, stmt := range stmts {
		what := leakyStatement(stmt)
		if what == "" || (what != leakValues && scrubbedNext(stmts, seps, i)) {
			continue
		}
		return &GuardDecision{Blocked: true, Reason: piiPipeReason(base, what, stmt)}
	}
	return nil
}

// leakyStatement names what a statement prints — leakEnvironment, leakConfig
// or leakValues — or "" when it prints none of them.
func leakyStatement(stmt string) string {
	words, k, envDump := leakCommand(stmt)
	if envDump {
		return leakEnvironment
	}
	if k >= len(words) {
		return ""
	}
	args := words[k+1:]
	switch filepath.Base(words[k]) {
	case "printenv":
		if len(printenvNames(args)) > 0 {
			return leakValues
		}
		return leakEnvironment
	case "ps":
		if psShowsEnvironment(args) {
			return leakEnvironment
		}
	case "cat":
		for _, a := range args {
			if isMuxcodeConfigPath(a) {
				return leakConfig
			}
		}
	}
	return ""
}

// leakCommand returns a statement's words, without grouping or redirections,
// and the index k of the word it executes — past env assignments, wrappers
// and any env that runs a command (`env FOO=1 cmd` is judged as cmd).
// envDump reports an env that runs none and so prints the environment.
func leakCommand(stmt string) (words []string, k int, envDump bool) {
	words = leakWords(stmt)
	k = skipLeakPrefix(words)
	for k < len(words) && filepath.Base(words[k]) == "env" {
		if k = envCommandIndex(words, k+1); k >= len(words) {
			return words, k, true
		}
	}
	return words, k, false
}

// leakWords is shellWords without grouping tokens or redirections, so
// `( env )` and `env > /tmp/x` read as a bare `env`.
func leakWords(stmt string) []string {
	var out []string
	words := shellWords(stmt)
	for i := 0; i < len(words); i++ {
		switch w := words[i]; {
		case w == "(" || w == ")" || w == "{" || w == "}":
		case strings.ContainsAny(w, "<>") && strings.Trim(w, "0123456789&<>|") == "":
			i++
		default:
			out = append(out, w)
		}
	}
	return out
}

// leakPrefixWrappers run the command that follows them. `command` is left out:
// `command -v env` names env without running it.
var leakPrefixWrappers = map[string]bool{"sudo": true, "exec": true, "nohup": true, "time": true}

// skipLeakPrefix returns the index of the word a statement executes, past env
// assignments and wrappers with their option words.
func skipLeakPrefix(words []string) int {
	k, wrapped := 0, false
	for ; k < len(words); k++ {
		w := words[k]
		switch {
		case isEnvAssignment(w):
		case leakPrefixWrappers[w]:
			wrapped = true
		case wrapped && strings.HasPrefix(w, "-"):
		default:
			return k
		}
	}
	return k
}

// envArgFlags are env's options that take the next word as their argument.
var envArgFlags = map[string]bool{
	"-u": true, "-C": true, "-P": true, "-S": true,
	"--unset": true, "--chdir": true, "--split-string": true,
}

// envCommandIndex returns the index of the command env runs, starting after
// the `env` word at k, or len(words) when it runs none and so prints the
// environment.
func envCommandIndex(words []string, k int) int {
	for ; k < len(words); k++ {
		w := words[k]
		switch {
		case w == "--":
			return k + 1
		case envArgFlags[w]:
			k++
		case strings.HasPrefix(w, "-"), isEnvAssignment(w):
		default:
			return k
		}
	}
	return k
}

// printenvNames returns the variable names printenv was asked for.
func printenvNames(args []string) []string {
	var names []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			names = append(names, a)
		}
	}
	return names
}

// psShowsEnvironment reports whether ps's arguments print each process's
// environment: a dashless BSD option word carrying `e` (ps eww, ps auxe) that
// is not another option's argument, or a dashed word carrying `E`. A dashed
// `-e` selects every process in macOS's default mode and on Linux, so
// `ps -ef` stays allowed.
func psShowsEnvironment(args []string) bool {
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			if strings.ContainsRune(a, 'E') {
				return true
			}
		case isLetters(a) && strings.ContainsRune(a, 'e') && (i == 0 || !strings.HasPrefix(args[i-1], "-")):
			return true
		}
	}
	return false
}

func isLetters(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return s != ""
}

// isMuxcodeConfigPath reports whether a word names a muxcode config file:
// $MUXCODE_CONFIG, or a file named config directly under a muxcode or
// .muxcode directory — ~/.config/muxcode/config and .muxcode/config under any
// prefix.
func isMuxcodeConfigPath(word string) bool {
	p := unquote(word)
	if p == "$MUXCODE_CONFIG" || p == "${MUXCODE_CONFIG}" {
		return true
	}
	if filepath.Base(p) != "config" {
		return false
	}
	parent := filepath.Base(filepath.Dir(p))
	return parent == "muxcode" || parent == ".muxcode"
}

// scrubbedNext reports whether statement i pipes straight into a bare `muxcode
// pii-scrub`. A stage in between does not count: it could strip the labels.
// Nor does an argument: `--role build` echoes its input byte-for-byte.
func scrubbedNext(stmts, seps []string, i int) bool {
	if i+1 >= len(stmts) || seps[i] != "|" {
		return false
	}
	words, k, _ := leakCommand(stmts[i+1])
	return len(words) == k+2 && filepath.Base(words[k]) == "muxcode" && words[k+1] == "pii-scrub"
}

// piiRemedy is the compliant form of a leaky statement, which itself passes
// the guard: the statement without redirections or grouping (`env > f | …`
// would pipe nothing) piped straight into the scrubber, or — for printenv
// NAME — the labelled dump scrubbed and then narrowed to those names.
func piiRemedy(stmt string) string {
	words, k, _ := leakCommand(stmt)
	if k < len(words) && filepath.Base(words[k]) == "printenv" {
		if names := printenvNames(words[k+1:]); len(names) > 0 {
			return "printenv | muxcode pii-scrub | grep -E '^(" + strings.Join(names, "|") + ")='"
		}
	}
	return strings.Join(words, " ") + " | muxcode pii-scrub"
}

func piiPipeReason(role, what, stmt string) string {
	return fmt.Sprintf("BLOCKED: PII scrub — `%s` prints %s, and %s is a PII-sensitive role: only its history row is redacted, "+
		"so the output would reach your own conversation and your provider's transcript unredacted. "+
		"Run `%s` instead. The scrubber matches NAME=value, so it must come directly after the command — "+
		"narrow with grep after the scrub, never before it.",
		shortStatement(stmt), what, role, piiRemedy(stmt))
}
