package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// validationRoles verify the work and never author it (MUX-157; the user's
// decision of 2026-11-24 names build, test and review — watch and analyze are
// not among them). On 2026-09-08 the test agent authored source for an hour
// against a prose rule, a halt and a reload, certifying its own changes.
var validationRoles = map[string]bool{"build": true, "test": true, "review": true}

// IsValidationRole reports whether role is held to the never-author guard.
func IsValidationRole(role string) bool {
	return validationRoles[role]
}

// roleOutputDirs are the checkout directories a validation role's own work
// produces and may write: build's bin/ (the Phase 1 inventory). The one list
// both CheckSourceWriteGuard and OpenCode's edit rules read.
func roleOutputDirs(role string) []string {
	if role == "build" {
		return []string{"bin"}
	}
	return nil
}

// sourceWriteDenyTools carries the never-author shell shapes to OpenCode,
// where no PreToolUse guard runs, as bash deny patterns in the validation
// roles' profiles. A glob cannot read a writer's operands, so on OpenCode
// these refuse the shape wherever it writes, /tmp included, and a redirect
// into the tree — which has no glob form — is not refused; the file tools
// are path-aware there (openCodeSourceEditRules).
var sourceWriteDenyTools = []string{
	"sed -i*", "sed --in-place*", "perl -i*", "perl -pi*",
	"gofmt -w*", "goimports -w*", "gofumpt -w*",
	"* --fix*", "* --write*",
}

// openCodeSourceEditRules is the never-author rule as OpenCode's edit
// permission, path-aware like CheckSourceWriteGuard. OpenCode turns the
// legacy `edit:` map into one rule per glob, in order, and the last match
// wins (its converter, read in opencode v2.4.1). Its docs give an in-project
// resource as a project-relative path and an external one as a canonical
// absolute path — documented for read and inferred for edit. So "*" denies
// the checkout, "/*" re-allows everything outside it, and each roleOutputDirs
// entry re-allows the role's own output.
func openCodeSourceEditRules(role string) string {
	var b strings.Builder
	b.WriteString("  edit:\n    \"*\": deny\n    \"/*\": allow\n")
	for _, dir := range roleOutputDirs(role) {
		fmt.Fprintf(&b, "    \"%s/*\": allow\n", dir)
	}
	return b.String()
}

// RoleWriteRefusedEvent is the lifecycle event hookGuard writes for every
// never-author refusal, naming role, tool and path.
const RoleWriteRefusedEvent = "role-write-refused"

func sourceWriteReason(role, what string) string {
	return fmt.Sprintf("BLOCKED: the %s agent validates and never authors — %s. Report what needs changing to the edit agent instead: "+
		`muxcode send edit <action> "<what to change and why>". Writes outside the checkout (/tmp, caches) stay allowed.`, role, what)
}

// CheckSourceWriteGuard refuses a validation role's write to a source path:
// any path inside the session checkout except the role's own output
// (roleOutputDirs). Paths outside the checkout (/tmp, /dev, caches, home)
// pass — the Phase 1 inventory found that everything else build and test
// produce is written by child processes (go, make, install), which no
// PreToolUse guard sees. The path is taken literally — a file tool's
// `$source.go`, `~/x.go` or `"x.go"` names a file of exactly that name;
// shell quoting and expansion belong to the shell caller (shellTargetPath).
// The decision carries the path for the role-write-refused row.
func CheckSourceWriteGuard(role, p string) *GuardDecision {
	if !validationRoles[role] || p == "" {
		return nil
	}
	rel, inside := checkoutRelative(p)
	if !inside {
		return nil
	}
	for _, dir := range roleOutputDirs(role) {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return nil
		}
	}
	return &GuardDecision{Blocked: true, Path: p,
		Reason: sourceWriteReason(role, p+" is source in this checkout")}
}

// CheckSourceShellWriteGuard applies the never-author rule to a shell
// command: every file an in-place writer rewrites (shellWriteTargets) and
// every redirect and tee target goes through CheckSourceWriteGuard, so a
// write to /tmp or build's bin/ passes and one into the tree is refused,
// whichever shape makes it. A shell-variable target ($tmpfile — the
// reviewer's findings file) cannot be resolved here and passes, as does a
// write the shapes do not recognise: the command-shape limit MUX-197 records.
// Quoted payloads and heredoc bodies are not read as commands.
func CheckSourceShellWriteGuard(role, command string) *GuardDecision {
	if !validationRoles[role] {
		return nil
	}
	stripped := stripQuotedSegments(stripHeredocBodies(command))
	targets := append(shellWriteTargets(command), redirectTargets(stripped)...)
	for _, t := range append(targets, teeTargets(stripped)...) {
		if p, ok := shellTargetPath(t); ok {
			if d := CheckSourceWriteGuard(role, p); d != nil {
				return d
			}
		}
	}
	return nil
}

// shellTargetPath is the one shell normalization of a write target: quotes
// removed and a leading ~ expanded, as the shell would. A $-variable target
// cannot be resolved here and reports ok=false (MUX-197). File-tool paths
// never pass through it — they are literal.
func shellTargetPath(t string) (string, bool) {
	p := strings.Trim(t, `"'`)
	if p == "" || strings.HasPrefix(p, "$") {
		return "", false
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, true
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p, true
}

// fixerRunners are the launchers a --fix/--write tool is run through.
var fixerRunners = map[string]bool{"npx": true, "pnpm": true, "yarn": true, "npm": true, "bunx": true, "bun": true,
	"exec": true, "run": true, "dlx": true, "uv": true, "uvx": true, "poetry": true, "python": true, "python3": true, "-m": true}

// shellWriteTargets returns the files the in-place writers in command
// rewrite, statement by statement: sed or perl with an in-place flag
// (script skipped — the first operand unless -e/-f gave one, and BSD's
// empty backup suffix after -i), gofmt/goimports/gofumpt -w, and any tool given
// --fix or --write. A writer naming no file rewrites the working directory,
// reported as ".".
func shellWriteTargets(command string) []string {
	var out []string
	stmts, _ := parseShellStatements(stripHeredocBodies(command))
	for _, stmt := range stmts {
		out = append(out, statementWriteTargets(shellWords(stmt))...)
	}
	return out
}

func statementWriteTargets(words []string) []string {
	var args []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "(" || w == ")":
		case isRedirectWord(w):
			i++ // the operator's target, read by redirectTargets
		case len(args) == 0 && isEnvAssignment(w):
		default:
			args = append(args, strings.Trim(w, `"'`))
		}
	}
	if len(args) == 0 {
		return nil
	}
	operands := func(from int, skipFlagValue map[string]bool) []string {
		var ops []string
		for j := from; j < len(args); j++ {
			a := args[j]
			switch {
			case skipFlagValue[a]:
				j++
			case strings.HasPrefix(a, "-"):
			default:
				ops = append(ops, a)
			}
		}
		return ops
	}
	orCwd := func(ops []string) []string {
		if len(ops) == 0 {
			return []string{"."}
		}
		return ops
	}

	switch filepath.Base(args[0]) {
	case "sed", "perl":
		inPlace, scriptGiven, bsdSuffix := false, false, false
		for j, a := range args[1:] {
			switch {
			case a == "-i" && j+2 < len(args) && args[j+2] == "":
				inPlace, bsdSuffix = true, true
			case a == "-i" || strings.HasPrefix(a, "--in-place") || strings.HasPrefix(a, "-i."):
				inPlace = true
			case len(a) > 1 && len(a) <= 5 && a[0] == '-' && a[1] != '-' && strings.Contains(a, "i"):
				inPlace = true
			}
			if a == "-e" || a == "-f" || a == "--expression" || a == "--file" || strings.HasPrefix(a, "--expression=") {
				scriptGiven = true
			}
		}
		if !inPlace {
			return nil
		}
		ops := operands(1, map[string]bool{"-e": true, "-f": true, "--expression": true, "--file": true})
		if bsdSuffix && len(ops) > 0 && ops[0] == "" {
			ops = ops[1:]
		}
		if !scriptGiven && len(ops) > 0 {
			ops = ops[1:]
		}
		return orCwd(ops)
	case "gofmt", "goimports", "gofumpt":
		for _, a := range args[1:] {
			if a == "-w" || strings.HasPrefix(a, "-w=") {
				return orCwd(operands(1, nil))
			}
		}
		return nil
	}
	for _, a := range args {
		if a == "--fix" || strings.HasPrefix(a, "--fix=") || a == "--write" || strings.HasPrefix(a, "--write=") {
			tool := 0
			for tool < len(args)-1 && fixerRunners[filepath.Base(args[tool])] {
				tool++
			}
			return orCwd(operands(tool+1, nil))
		}
	}
	return nil
}

// isRedirectWord reports a redirect operator as shellWords emits it: `>`,
// `>>`, `2>`, `&>`, `<`, `>|` and the like — digits, & and | around < or >.
func isRedirectWord(w string) bool {
	return strings.ContainsAny(w, "<>") && strings.Trim(w, "0123456789&<>|") == ""
}

// checkoutRelative resolves the literal path p against the working directory
// — where the hook runs, the agent's checkout — and reports it relative to
// the checkout root (the nearest directory above holding .git, else the
// working directory). Both sides are compared with symlinks resolved, since
// /tmp and macOS's /var are links. A path outside the root reports
// inside=false. An unreadable working directory reads as inside: the guard
// fails closed.
func checkoutRelative(p string) (rel string, inside bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return p, true
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	root := resolveExisting(checkoutRoot(cwd))
	r, err := filepath.Rel(root, resolveExisting(filepath.Clean(p)))
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(r), true
}

// resolveExisting resolves the symlinks of p's deepest existing ancestor and
// rejoins the rest — the file a write names usually does not exist yet.
func resolveExisting(p string) string {
	rest := ""
	for d := p; ; d = filepath.Dir(d) {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(r, rest)
		}
		if filepath.Dir(d) == d {
			return p
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
}

func checkoutRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}
