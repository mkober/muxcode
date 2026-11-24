package bus

import (
	"path/filepath"
	"regexp"
	"strings"
)

// History-file guard (MUX-185 D3): a role's `<role>-history.jsonl` is written
// only by muxcode's own writers — `hook bash` observing a call, `muxcode log`
// self-reporting one — never by an agent's Bash tool. Appending a line with
// "source":"hook" by hand mints the evidence a graph node's verdict rests on.
//
// This is a cost, not a boundary. It refuses the cheap forms the guard can see
// as shell syntax: a redirection, tee, sed/perl -i, cp/mv/install/ln/dd onto
// the file, an interpreter whose own argv names the file with write intent,
// and `muxcode hook bash`, which writes an honest-looking row from any payload
// piped into it. A computed filename, a heredoc script fed to an interpreter
// or any write the shell syntax does not show passes; the transcript witness
// is what corroborates a hook row past that. The target is matched by its
// `*-history.jsonl` basename, never a resolved directory, because the guard
// sees it as written (`$B/test-history.jsonl`, `$(muxcode bus-dir)/…`). It
// binds every role. Reads — cat, jq, tail, grep — are untouched.

// CheckHistoryWriteGuard denies a Bash call that writes to a history file in a
// form the guard can see. Nil when the call writes none. The role is not
// consulted.
func CheckHistoryWriteGuard(_, command string) *GuardDecision {
	if writesHistory(stripHeredocBodies(command)) {
		return &GuardDecision{Blocked: true, Reason: historyWriteReason}
	}
	return nil
}

const historyWriteReason = "BLOCKED: `*-history.jsonl` files are written only by muxcode's hooks and `muxcode log` — never by redirection, tee, sed -i, cp/mv/dd, an interpreter or `muxcode hook bash` from an agent's shell (MUX-185). A row written by hand forges the evidence a graph node's verdict rests on. Read them freely with cat, jq or tail; record your own result with `muxcode log <role> \"summary\" --exit-code N`, which is filed as self-reported."

// historyCopyCommands take their destination as the last operand.
var historyCopyCommands = map[string]bool{"cp": true, "mv": true, "install": true, "ln": true, "rsync": true}

// historyInterpreters are commands whose argv may carry a script that opens a
// file itself.
var historyInterpreters = map[string]bool{
	"python": true, "python3": true, "node": true, "ruby": true, "perl": true,
	"awk": true, "gawk": true, "php": true, "deno": true, "bun": true,
}

// historyWriteTarget matches an interpreter script that writes a literal
// history path, never one that merely mentions it beside unrelated write
// vocabulary (`sys.stdout.write(open(f).read())` is a read). In order: an open
// with a write/append mode (python, node fs.open, ruby File.open); a write or
// append call taking the path first (node fs.*File*, createWriteStream, ruby
// File.write); pathlib's write_text/write_bytes/open(mode); a perl mode
// spliced into the path (`">>f"`) or given as its own argument; and awk's
// `print > "f"`. Quotes may arrive backslash-escaped inside a `-c` string.
var historyWriteTarget = func() *regexp.Regexp {
	lit := `\\?['"][^'"]*-history\.jsonl\\?['"]`
	mode := `\\?['"][^'"\\]*[wax+][^'"\\]*\\?['"]`
	return regexp.MustCompile(`(?i)` + strings.Join([]string{
		`open(sync)?\(\s*` + lit + `\s*,\s*(mode\s*=\s*)?` + mode,
		`(write|append)\w*\(\s*` + lit,
		lit + `\s*\)\s*\.\s*(write_text|write_bytes|open\(\s*` + mode + `)`,
		`\\?['"]\s*(\+?>>?|\+<)\s*[^'"\s]*-history\.jsonl`,
		`\\?['"](\+?>>?|\+<)\\?['"]\s*,\s*` + lit,
		`>>?\s*` + lit,
	}, "|"))
}()

func writesHistory(command string) bool {
	stmts, _ := parseShellStatements(command)
	for _, stmt := range stmts {
		if statementWritesHistory(stmt) {
			return true
		}
	}
	return false
}

func statementWritesHistory(stmt string) bool {
	for _, t := range redirectWriteTargets(stmt) {
		if isHistoryFile(t) {
			return true
		}
	}
	words := shellWords(stmt)
	for i, w := range words {
		words[i] = unquote(w)
	}
	k, _ := commandStart(words)
	if k >= len(words) {
		return false
	}
	args := operands(words[k+1:])
	switch cmd := filepath.Base(unquote(words[k])); {
	case cmd == "muxcode":
		return len(args) >= 2 && args[0] == "hook" && args[1] == "bash"
	case cmd == "tee":
		return anyHistoryFile(args)
	case cmd == "sed" || cmd == "perl":
		if hasInPlaceEdit(stmt) && anyHistoryFile(args) {
			return true
		}
		return cmd == "perl" && interpreterWritesHistory(stmt)
	case historyCopyCommands[cmd]:
		return len(args) > 0 && isHistoryFile(args[len(args)-1])
	case cmd == "dd":
		for _, a := range args {
			if dest, ok := strings.CutPrefix(unquote(a), "of="); ok && isHistoryFile(dest) {
				return true
			}
		}
	case historyInterpreters[cmd]:
		return interpreterWritesHistory(stmt)
	case cmd == "eval":
		inner := make([]string, 0, len(args))
		for _, a := range args {
			inner = append(inner, unquote(a))
		}
		return writesHistory(strings.Join(inner, " "))
	case listenerShells[cmd]:
		for j := k + 1; j+1 < len(words); j++ {
			if w := words[j]; strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && strings.ContainsRune(w[1:], 'c') {
				return writesHistory(unquote(words[j+1]))
			}
		}
	}
	return false
}

// interpreterWritesHistory reports a script that writes a literal history
// path — see historyWriteTarget. A handle built at run time is out of reach.
func interpreterWritesHistory(stmt string) bool {
	return historyWriteTarget.MatchString(stmt)
}

// operands returns words that are neither options nor redirections, with a
// redirection's own target dropped too.
func operands(words []string) []string {
	var out []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case strings.ContainsAny(w, "<>") && strings.Trim(w, "0123456789&<>|") == "":
			i++
		case w == "(" || w == ")":
		case strings.HasPrefix(w, "-") && w != "-":
		default:
			out = append(out, w)
		}
	}
	return out
}

func anyHistoryFile(words []string) bool {
	for _, w := range words {
		if isHistoryFile(w) {
			return true
		}
	}
	return false
}

// isHistoryFile reports whether a path, as written, names a role history file.
func isHistoryFile(p string) bool {
	return strings.HasSuffix(filepath.Base(strings.Trim(p, `"'`)), "-history.jsonl")
}

// redirectWriteTargets returns the target of every output redirection in one
// statement, read as the shell would: through quotes and a `$(…)` with spaces
// inside, so `>> "$(muxcode bus-dir)/test-history.jsonl"` yields the whole
// path. An fd duplication (`2>&1`, `>&2`) is not a file and yields nothing.
func redirectWriteTargets(stmt string) []string {
	var targets []string
	for i := 0; i < len(stmt); i++ {
		switch c := stmt[i]; {
		case c == '\'' || c == '"':
			i = skipQuoted(stmt, i)
			continue
		case c != '>':
			continue
		}
		j := i + 1
		for j < len(stmt) && (stmt[j] == '>' || stmt[j] == '|') {
			j++
		}
		if j < len(stmt) && stmt[j] == '&' {
			i = j
			continue
		}
		for j < len(stmt) && (stmt[j] == ' ' || stmt[j] == '\t') {
			j++
		}
		start, depth := j, 0
	scan:
		for ; j < len(stmt); j++ {
			switch stmt[j] {
			case '\'', '"':
				j = skipQuoted(stmt, j)
			case '(':
				depth++
			case ')':
				if depth == 0 {
					break scan
				}
				depth--
			case ' ', '\t', '\n', ';', '|', '&', '<', '>':
				if depth == 0 {
					break scan
				}
			}
		}
		if start < j {
			targets = append(targets, stmt[start:j])
		}
		i = j - 1
	}
	return targets
}

// skipQuoted returns the index of the quote closing the span opened at i, or
// the last index when it never closes.
func skipQuoted(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' && q == '"' {
			j++
			continue
		}
		if s[j] == q {
			return j
		}
	}
	return len(s) - 1
}
