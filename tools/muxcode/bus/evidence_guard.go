package bus

import (
	"fmt"
	"strings"
)

// Hook-road evidence guard: a build, test or deploy statement must be the
// whole Bash call, in the foreground. The PostToolUse hook classifies a call
// by its first statement and records the exit code of its last, so a bundled
// build writes no authoritative row — the 2026-09-09 spec-to-pr hold, where a
// codex build agent put three `muxcode send` acks, `./build.sh` and a
// hand-typed result in one call — and a piped or backgrounded one records
// another status as the build's. Only a leading `cd … &&` and env
// assignments are exempt, as in stripCommandPrefix. Scoped to the roles whose
// classified calls become evidence; edit and plan are denied those commands
// outright, and run's verdict is the whole call's by design.

// evidenceGuardRoles are the roles whose build, test and deploy calls the
// hook turns into authoritative history rows.
var evidenceGuardRoles = map[string]bool{"build": true, "test": true, "deploy": true}

// HasEvidenceGuard reports whether the hook-road evidence rule applies to a
// role; hookGuard reads it so a role with no delegation rules still has its
// events examined.
func HasEvidenceGuard(role string) bool {
	return evidenceGuardRoles[role]
}

// CheckEvidenceGuard denies a call that bundles a build, test or deploy
// statement with any other statement, or backgrounds it. Nil when the role
// is out of scope, the call carries no such statement, or the statement
// stands alone in the foreground.
func CheckEvidenceGuard(role, command string) *GuardDecision {
	if !HasEvidenceGuard(role) {
		return nil
	}
	stmts, seps := parseShellStatements(command)
	if len(stmts) > 1 && seps[0] == "&&" && (stmts[0] == "cd" || strings.HasPrefix(stmts[0], "cd ")) {
		stmts, seps = stmts[1:], seps[1:]
	}
	for i, stmt := range stmts {
		kind := evidenceStatementKind(stmt)
		if kind == "" {
			continue
		}
		if seps[i] == "&" {
			return &GuardDecision{Blocked: true, Reason: evidenceBackgroundReason(kind, stmt)}
		}
		if len(stmts) > 1 {
			return &GuardDecision{Blocked: true, Reason: evidenceBundledReason(kind, stmt, len(stmts)-1)}
		}
	}
	return nil
}

// evidenceStatementKind names the chain a statement would feed — "build",
// "test" or "deploy" — using the patterns ClassifyCommand applies. A test
// precheck is "test": its failure is the stage's evidence and bundling
// would lose it.
func evidenceStatementKind(stmt string) string {
	cmd := stripCommandPrefix(strings.TrimSpace(stmt))
	p := loadPatterns()
	switch {
	case matchPatterns(cmd, p.build, true):
		return "build"
	case matchPatterns(cmd, p.test, true), matchPatterns(cmd, p.testPrecheck, true):
		return "test"
	case matchPatterns(cmd, p.deploy, true):
		return "deploy"
	}
	return ""
}

func evidenceBundledReason(kind, stmt string, others int) string {
	shown := shortStatement(stmt)
	return fmt.Sprintf("BLOCKED: hook-road evidence — the %s command must be the only statement in its call, but `%s` is bundled with %d other statement(s). "+
		"The PostToolUse hook classifies a call by its first statement and records the exit code of its last, so bundled like this it records nothing (or another command's status), the chain does not fire, and a graph run parks on an unverified hold. "+
		"Run `%s` alone — a leading `cd … &&` or env assignment is fine, a pipe or `;`/`&&` chain is not — and send acks or reports in a separate call.",
		kind, shown, others, shown)
}

func evidenceBackgroundReason(kind, stmt string) string {
	shown := shortStatement(stmt)
	return fmt.Sprintf("BLOCKED: hook-road evidence — `%s &` runs the %s command in the background, so the call returns before it finishes and the PostToolUse hook records the launch, not the result. "+
		"Run `%s` in the foreground, alone in its call.", shown, kind, shown)
}

func shortStatement(stmt string) string {
	if len(stmt) > 80 {
		return stmt[:77] + "..."
	}
	return stmt
}

// parseShellStatements splits a command at top-level `;`, newline, `&&`,
// `||`, `|` and `&`, returning each statement with the operator that ended
// it ("" for the last). Quoted spans, backticks and parenthesised groups stay
// intact; `2>&1`, `0<&0`, `&>` and `>|` are redirections, not separators,
// and a backslash-newline continuation is one statement.
func parseShellStatements(command string) (stmts, seps []string) {
	var cur strings.Builder
	flush := func(sep string) {
		if s := strings.TrimSpace(cur.String()); s != "" {
			stmts = append(stmts, s)
			seps = append(seps, sep)
		}
		cur.Reset()
	}
	var quote byte
	depth := 0
	n := len(command)
	for i := 0; i < n; i++ {
		c := command[i]
		switch {
		case quote != 0:
			cur.WriteByte(c)
			if c == '\\' && quote == '"' && i+1 < n {
				i++
				cur.WriteByte(command[i])
			} else if c == quote {
				quote = 0
			}
		case c == '\\' && i+1 < n:
			cur.WriteByte(c)
			i++
			cur.WriteByte(command[i])
		case c == '\'' || c == '"' || c == '`':
			quote = c
			cur.WriteByte(c)
		case c == '(':
			depth++
			cur.WriteByte(c)
		case c == ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case depth > 0:
			cur.WriteByte(c)
		case c == ';' || c == '\n':
			flush(string(c))
		case c == '|':
			if i > 0 && command[i-1] == '>' {
				cur.WriteByte(c)
				continue
			}
			if i+1 < n && command[i+1] == '|' {
				i++
				flush("||")
			} else {
				flush("|")
			}
		case c == '&':
			if (i > 0 && (command[i-1] == '>' || command[i-1] == '<')) || (i+1 < n && command[i+1] == '>') {
				cur.WriteByte(c)
				continue
			}
			if i+1 < n && command[i+1] == '&' {
				i++
				flush("&&")
			} else {
				flush("&")
			}
		default:
			cur.WriteByte(c)
		}
	}
	flush("")
	return stmts, seps
}
