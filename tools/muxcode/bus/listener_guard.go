package bus

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Inbox-listener guard: `muxcode inbox --poll`/`--loop` must be the whole Bash
// call — bare, in the foreground, its stdout unredirected — so the agent
// harness backgrounds it (run_in_background) and hands each message back as
// the call's output. Any other shape consumes messages into output nobody
// reads while the bus records them acked by the role: on 2026-09-22 watch ran
// `nohup muxcode inbox --poll --loop > /tmp/watch-listener.log 2>&1 & disown`,
// which reparented to launchd within a second. MUX-156 covers orphans from any
// cause; this refuses the one an agent makes on purpose. It binds every role,
// since none has a use for a detached listener.
//
// Detection follows command position: env assignments, grouping words and
// wrapper commands (listenerWrappers) are peeled to find the word a statement
// executes, so a wrapped listener is found but never bare. The string `eval`
// or a shell's `-c` runs is searched as a command too. Every other argument is
// data — echo's, grep's, a bus message about this very defect — and a
// listener named there starts nothing.

// CheckListenerGuard denies a call that starts the inbox listener in any shape
// but bare and alone in its call. Nil when the call starts no listener or
// starts it bare. The role is not consulted.
func CheckListenerGuard(_, command string) *GuardDecision {
	stmts, seps := dropLeadingCd(parseShellStatements(command))
	for i, stmt := range stmts {
		found, bare := listenerInvocation(stmt)
		if !found {
			continue
		}
		if !bare || seps[i] == "&" || len(stmts) > 1 {
			return &GuardDecision{Blocked: true, Reason: listenerDetachedReason(stmt)}
		}
	}
	return nil
}

// listenerWrappers run the command that follows them, so a listener behind
// one is found but not bare. `(` and `{` open a subshell or group.
var listenerWrappers = map[string]bool{
	"nohup": true, "setsid": true, "env": true, "timeout": true, "gtimeout": true,
	"nice": true, "command": true, "exec": true, "time": true, "caffeinate": true,
	"stdbuf": true, "sudo": true, "(": true, "{": true,
}

// listenerShells are the interpreters whose `-c` string is a command.
var listenerShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// listenerInvocation reports whether stmt runs the inbox listener, and whether
// it runs it bare: at command position with no wrapper, stdout not redirected.
// A listener run by `eval` or a shell's `-c` string is never bare.
func listenerInvocation(stmt string) (found, bare bool) {
	words := shellWords(stmt)
	k, wrapped := commandStart(words)
	if listenerAt(words, k) {
		return true, !wrapped && !redirectsStdout(words)
	}
	if k >= len(words) {
		return false, false
	}
	switch cmd := filepath.Base(words[k]); {
	case cmd == "eval":
		args := make([]string, 0, len(words)-k-1)
		for _, w := range words[k+1:] {
			args = append(args, unquote(w))
		}
		return runsListener(strings.Join(args, " ")), false
	case listenerShells[cmd]:
		for j := k + 1; j+1 < len(words); j++ {
			if w := words[j]; strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.ContainsRune(w[1:], 'c') {
				return runsListener(unquote(words[j+1])), false
			}
		}
	}
	return false, false
}

// commandStart returns the index of the word a statement executes, skipping
// env assignments, then any wrappers together with the option and numeric
// words they take (`timeout 600`, `nice -n 10`); wrapped reports whether a
// wrapper was skipped.
func commandStart(words []string) (k int, wrapped bool) {
	for ; k < len(words); k++ {
		w := words[k]
		switch {
		case isEnvAssignment(w):
		case listenerWrappers[w]:
			wrapped = true
		case wrapped && (strings.HasPrefix(w, "-") || isDurationArg(w)):
		default:
			return k, wrapped
		}
	}
	return k, wrapped
}

// isDurationArg reports whether w is a number with an optional s/m/h/d unit.
func isDurationArg(w string) bool {
	n := strings.TrimRight(w, "smhd")
	return n != "" && strings.Trim(n, "0123456789.") == ""
}

// listenerAt reports whether words[k] starts `muxcode inbox` (any path to the
// binary) carrying a --poll or --loop flag in either dash form.
func listenerAt(words []string, k int) bool {
	if k+1 >= len(words) || filepath.Base(words[k]) != "muxcode" || words[k+1] != "inbox" {
		return false
	}
	for _, a := range words[k+2:] {
		flag := strings.TrimLeft(a, "-")
		if flag == a {
			continue
		}
		if name, _, _ := strings.Cut(flag, "="); name == "poll" || name == "loop" {
			return true
		}
	}
	return false
}

// runsListener reports whether any statement of a command string runs the
// listener, for the string a `sh -c` or `eval` executes.
func runsListener(command string) bool {
	stmts, _ := parseShellStatements(command)
	for _, s := range stmts {
		if found, _ := listenerInvocation(s); found {
			return true
		}
	}
	return false
}

// shellWords splits a statement into words. Quoted spans stay inside their
// word with the quotes kept; each parenthesis and each redirection operator,
// with its fd prefix, is a word of its own — so `--poll)` cannot hide a flag
// and `2>err>log` cannot hide a stdout redirect behind a stderr one.
func shellWords(s string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' && c == '"' {
					j++
				}
				j++
			}
			if j >= len(s) {
				j = len(s) - 1
			}
			cur.WriteString(s[i : j+1])
			i = j
		case c == '\\' && i+1 < len(s):
			cur.WriteString(s[i : i+2])
			i++
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		case c == '(' || c == ')':
			flush()
			words = append(words, string(c))
		case c == '>' || c == '<':
			fd := cur.String()
			if fd != "&" && strings.Trim(fd, "0123456789") != "" {
				flush()
				fd = ""
			}
			cur.Reset()
			op := fd + string(c)
			for i+1 < len(s) && strings.IndexByte(">&|", s[i+1]) >= 0 {
				i++
				op += string(s[i])
			}
			words = append(words, op)
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return words
}

// redirectsStdout reports whether any redirection operator among words sends
// stdout away from the call's own output: `>`, `>>`, `1>`, `&>`, `>|`, `>&`.
// Stderr alone (`2>file`, `2>&1`) does not.
func redirectsStdout(words []string) bool {
	for _, w := range words {
		i := strings.IndexByte(w, '>')
		if i < 0 || strings.Trim(w, "0123456789&<>|") != "" {
			continue
		}
		if fd := w[:i]; fd == "" || fd == "1" || fd == "&" {
			return true
		}
	}
	return false
}

// isEnvAssignment reports whether a word is a `NAME=value` prefix assignment.
func isEnvAssignment(w string) bool {
	i := strings.IndexByte(w, '=')
	return i > 0 && isEnvVarName(w[:i])
}

// unquote strips one pair of matching outer quotes.
func unquote(w string) string {
	if len(w) >= 2 && (w[0] == '\'' || w[0] == '"') && w[len(w)-1] == w[0] {
		return w[1 : len(w)-1]
	}
	return w
}

func listenerDetachedReason(stmt string) string {
	return fmt.Sprintf("BLOCKED: inbox listener — `%s` detaches the listener, wraps it, redirects its output, or bundles it with other statements. "+
		"The messages it consumes then land where you never read them, while the bus records them as acked by your role and stops trying to deliver them. "+
		"Run exactly `muxcode inbox --poll --loop` as its own Bash call with run_in_background=true — no nohup, subshell, `&`, disown, `>` or pipe. "+
		"The harness returns each message to you when it arrives.",
		shortStatement(stmt))
}
