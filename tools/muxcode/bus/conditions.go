package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ChainContext holds the evaluation context for condition expressions.
// Built by ProcessBashHook() from ToolEvent fields, or by the CLI from flags.
// When nil is passed to ResolveChain(), conditions are skipped (backward compatible).
type ChainContext struct {
	ChangedFiles []string // cached from git diff --name-only HEAD
	Branch       string   // current branch (git rev-parse --abbrev-ref HEAD)
	ExitCode     int      // numeric exit code from ToolEvent
	Command      string   // command string from ToolEvent.ToolInput.Command
	Output       string   // stdout/stderr from ToolEvent.ToolResult (for output_contains)
}

// ConditionResult records per-condition evaluation details for --verbose output.
type ConditionResult struct {
	Type    string // e.g. "files_match", "branch_match"
	Pattern string // the pattern or value being tested
	Passed  bool
	Detail  string // human-readable explanation
}

// knownConditionTypes lists all recognized condition keys for validation.
var knownConditionTypes = map[string]bool{
	"files_match":      true,
	"files_not_match":  true,
	"branch_match":     true,
	"branch_not_match": true,
	"env_set":          true,
	"env_equals":       true,
	"output_contains":  true,
	"exit_code":        true,
}

// IsKnownCondition returns true if the condition type is recognized.
func IsKnownCondition(condType string) bool {
	return knownConditionTypes[condType]
}

// EvaluateConditions checks all conditions against the given context.
// Returns (allPassed, results). If conditions is nil or empty, returns (true, nil).
func EvaluateConditions(conditions map[string]any, ctx *ChainContext) (bool, []ConditionResult) {
	if len(conditions) == 0 {
		return true, nil
	}
	if ctx == nil {
		// No context — skip condition evaluation (backward compatible)
		return true, nil
	}

	// Populate git info lazily — only when conditions reference git state
	for condType := range conditions {
		if condType == "files_match" || condType == "files_not_match" || condType == "branch_match" {
			ctx.PopulateGitInfo()
			break
		}
	}

	var results []ConditionResult
	allPassed := true

	for condType, value := range conditions {
		result := evaluateCondition(condType, value, ctx)
		results = append(results, result)
		if !result.Passed {
			allPassed = false
		}
	}

	return allPassed, results
}

// evaluateCondition dispatches to the appropriate evaluator.
func evaluateCondition(condType string, value any, ctx *ChainContext) ConditionResult {
	switch condType {
	case "files_match":
		return evalFilesMatch(value, ctx)
	case "files_not_match":
		return evalFilesNotMatch(value, ctx)
	case "branch_match":
		return evalBranchMatch(value, ctx)
	case "branch_not_match":
		return evalBranchNotMatch(value, ctx)
	case "env_set":
		return evalEnvSet(value)
	case "env_equals":
		return evalEnvEquals(value)
	case "output_contains":
		return evalOutputContains(value, ctx)
	case "exit_code":
		return evalExitCode(value, ctx)
	default:
		return ConditionResult{
			Type:   condType,
			Passed: false,
			Detail: "unknown condition type",
		}
	}
}

// evalFilesMatch checks if at least one changed file matches the glob pattern.
func evalFilesMatch(value any, ctx *ChainContext) ConditionResult {
	pattern, ok := value.(string)
	if !ok {
		return ConditionResult{Type: "files_match", Passed: false, Detail: "pattern must be a string"}
	}
	result := ConditionResult{Type: "files_match", Pattern: pattern}

	for _, f := range ctx.ChangedFiles {
		if fileGlobMatch(pattern, f) {
			result.Passed = true
			result.Detail = fmt.Sprintf("matched: %s", f)
			return result
		}
	}
	result.Detail = fmt.Sprintf("no files matched (checked %d files)", len(ctx.ChangedFiles))
	return result
}

// evalFilesNotMatch checks that no changed file matches the glob pattern.
func evalFilesNotMatch(value any, ctx *ChainContext) ConditionResult {
	pattern, ok := value.(string)
	if !ok {
		return ConditionResult{Type: "files_not_match", Passed: false, Detail: "pattern must be a string"}
	}
	result := ConditionResult{Type: "files_not_match", Pattern: pattern, Passed: true}

	for _, f := range ctx.ChangedFiles {
		if fileGlobMatch(pattern, f) {
			result.Passed = false
			result.Detail = fmt.Sprintf("file matched: %s", f)
			return result
		}
	}
	result.Detail = fmt.Sprintf("no files matched (checked %d files)", len(ctx.ChangedFiles))
	return result
}

// evalBranchMatch checks if the current branch matches a regex pattern.
func evalBranchMatch(value any, ctx *ChainContext) ConditionResult {
	pattern, ok := value.(string)
	if !ok {
		return ConditionResult{Type: "branch_match", Passed: false, Detail: "pattern must be a string"}
	}
	result := ConditionResult{Type: "branch_match", Pattern: pattern}

	re, err := regexp.Compile(pattern)
	if err != nil {
		result.Detail = fmt.Sprintf("invalid regex: %v", err)
		return result
	}

	result.Passed = re.MatchString(ctx.Branch)
	if result.Passed {
		result.Detail = fmt.Sprintf("branch %q matches", ctx.Branch)
	} else {
		result.Detail = fmt.Sprintf("branch %q does not match", ctx.Branch)
	}
	return result
}

// evalBranchNotMatch checks if the current branch does NOT match a regex pattern.
func evalBranchNotMatch(value any, ctx *ChainContext) ConditionResult {
	pattern, ok := value.(string)
	if !ok {
		return ConditionResult{Type: "branch_not_match", Passed: false, Detail: "pattern must be a string"}
	}
	result := ConditionResult{Type: "branch_not_match", Pattern: pattern}

	re, err := regexp.Compile(pattern)
	if err != nil {
		result.Detail = fmt.Sprintf("invalid regex: %v", err)
		return result
	}

	result.Passed = !re.MatchString(ctx.Branch)
	if result.Passed {
		result.Detail = fmt.Sprintf("branch %q does not match", ctx.Branch)
	} else {
		result.Detail = fmt.Sprintf("branch %q matches", ctx.Branch)
	}
	return result
}

// evalEnvSet checks if an environment variable is set and non-empty.
func evalEnvSet(value any) ConditionResult {
	name, ok := value.(string)
	if !ok {
		return ConditionResult{Type: "env_set", Passed: false, Detail: "name must be a string"}
	}
	result := ConditionResult{Type: "env_set", Pattern: name}

	v := os.Getenv(name)
	result.Passed = v != ""
	if result.Passed {
		result.Detail = fmt.Sprintf("%s is set", name)
	} else {
		result.Detail = fmt.Sprintf("%s is not set or empty", name)
	}
	return result
}

// evalEnvEquals checks if an environment variable equals a specific value.
func evalEnvEquals(value any) ConditionResult {
	result := ConditionResult{Type: "env_equals"}

	m, ok := value.(map[string]any)
	if !ok {
		result.Detail = "value must be {name, value} object"
		return result
	}
	name, _ := m["name"].(string)
	expected, _ := m["value"].(string)
	if name == "" {
		result.Detail = "missing 'name' field"
		return result
	}
	result.Pattern = fmt.Sprintf("%s=%s", name, expected)

	actual := os.Getenv(name)
	result.Passed = actual == expected
	if result.Passed {
		result.Detail = fmt.Sprintf("%s equals %q", name, expected)
	} else {
		result.Detail = fmt.Sprintf("%s is %q, want %q", name, actual, expected)
	}
	return result
}

// evalOutputContains checks if command output contains a substring.
func evalOutputContains(value any, ctx *ChainContext) ConditionResult {
	substr, ok := value.(string)
	if !ok {
		return ConditionResult{Type: "output_contains", Passed: false, Detail: "value must be a string"}
	}
	result := ConditionResult{Type: "output_contains", Pattern: substr}

	result.Passed = strings.Contains(ctx.Output, substr)
	if result.Passed {
		result.Detail = fmt.Sprintf("output contains %q", substr)
	} else {
		result.Detail = fmt.Sprintf("output does not contain %q", substr)
	}
	return result
}

// evalExitCode checks if the numeric exit code matches.
func evalExitCode(value any, ctx *ChainContext) ConditionResult {
	result := ConditionResult{Type: "exit_code"}

	var expected int
	switch v := value.(type) {
	case float64:
		expected = int(v) // JSON numbers decode as float64
	case int:
		expected = v
	case string:
		var err error
		expected, err = strconv.Atoi(v)
		if err != nil {
			result.Detail = fmt.Sprintf("invalid exit code: %q", v)
			return result
		}
	default:
		result.Detail = fmt.Sprintf("exit_code must be a number, got %T", value)
		return result
	}
	result.Pattern = strconv.Itoa(expected)

	result.Passed = ctx.ExitCode == expected
	if result.Passed {
		result.Detail = fmt.Sprintf("exit code %d matches", ctx.ExitCode)
	} else {
		result.Detail = fmt.Sprintf("exit code %d, want %d", ctx.ExitCode, expected)
	}
	return result
}

// fileGlobMatch matches a file path against a glob pattern using filepath.Match
// semantics with ** support. Unlike globMatch() in tools.go (which treats * as
// crossing directory separators for tool permission patterns), this function uses
// standard file glob semantics: * matches within a single directory, ** crosses
// directory boundaries. Converts the glob pattern to a regex for reliable matching.
func fileGlobMatch(pattern, path string) bool {
	// No ** — use filepath.Match directly (fast path)
	if !strings.Contains(pattern, "**") {
		matched, err := filepath.Match(pattern, path)
		return err == nil && matched
	}

	// Convert glob pattern to regex:
	//   **  → match any path segments (including empty)
	//   *   → match within a single segment (no /)
	//   ?   → match single non-/ char
	//   .   → literal dot
	var re strings.Builder
	re.WriteString("^")

	i := 0
	for i < len(pattern) {
		switch {
		case i+1 < len(pattern) && pattern[i] == '*' && pattern[i+1] == '*':
			// ** — match anything including /
			i += 2
			if i >= len(pattern) {
				// ** at end of pattern — match everything remaining
				re.WriteString(".*")
			} else {
				// consume optional separator after **
				if pattern[i] == '/' {
					i++
				}
				re.WriteString("(?:.+/)?")
			}
		case pattern[i] == '*':
			re.WriteString("[^/]*")
			i++
		case pattern[i] == '?':
			re.WriteString("[^/]")
			i++
		case pattern[i] == '.':
			re.WriteString(`\.`)
			i++
		default:
			// Escape other regex metacharacters
			c := pattern[i]
			if strings.ContainsRune(`+{}()[]|^$\`, rune(c)) {
				re.WriteByte('\\')
			}
			re.WriteByte(c)
			i++
		}
	}

	re.WriteString("$")
	rx, err := regexp.Compile(re.String())
	if err != nil {
		return false
	}
	return rx.MatchString(path)
}

// changedFiles returns the list of changed files from git diff.
// Returns nil on error (git unavailable, not a repo, etc.).
func changedFiles() []string {
	out, err := exec.Command("git", "diff", "--name-only", "HEAD").Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

// branchName returns the current git branch name.
// Returns "" on error.
func branchName() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BuildChainContext creates a ChainContext from ToolEvent fields.
// Git info (branch, changed files) is populated lazily via PopulateGitInfo()
// and only called when conditions actually reference them.
func BuildChainContext(ev *ToolEvent) *ChainContext {
	exitCode := 0
	if s := ev.GetExitCode(); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			exitCode = n
		}
	}
	return &ChainContext{
		ExitCode: exitCode,
		Command:  ev.ToolInput.Command,
		Output:   ev.GetOutput(50, 4000),
	}
}

// PopulateGitInfo fills in git-derived fields (ChangedFiles, Branch).
// Called only when condition evaluation needs them, avoiding unnecessary
// git subprocess calls when no conditions reference git state.
func (ctx *ChainContext) PopulateGitInfo() {
	if ctx == nil {
		return
	}
	if ctx.ChangedFiles == nil {
		ctx.ChangedFiles = changedFiles()
	}
	if ctx.Branch == "" {
		ctx.Branch = branchName()
	}
}

// BuildChainContextFromFlags creates a ChainContext from CLI flags for testing.
func BuildChainContextFromFlags(files, branch, output, exitCode string) *ChainContext {
	ctx := &ChainContext{}

	if files != "" {
		for _, f := range strings.Split(files, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				ctx.ChangedFiles = append(ctx.ChangedFiles, f)
			}
		}
	} else {
		ctx.ChangedFiles = changedFiles()
	}

	if branch != "" {
		ctx.Branch = branch
	} else {
		ctx.Branch = branchName()
	}

	if output != "" {
		ctx.Output = output
	}

	if exitCode != "" {
		if n, err := strconv.Atoi(exitCode); err == nil {
			ctx.ExitCode = n
		}
	}

	return ctx
}

// FormatConditionResults formats ConditionResult slice for human-readable output.
func FormatConditionResults(results []ConditionResult) string {
	if len(results) == 0 {
		return "  (no conditions)"
	}
	var b strings.Builder
	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		if r.Pattern != "" {
			fmt.Fprintf(&b, "  [%s] %s %q: %s\n", status, r.Type, r.Pattern, r.Detail)
		} else {
			fmt.Fprintf(&b, "  [%s] %s: %s\n", status, r.Type, r.Detail)
		}
	}
	return b.String()
}

// ValidateConditions checks condition keys against known types.
// Returns warnings for unknown keys (not errors — forward compatibility).
func ValidateConditions(conditions map[string]any) []string {
	var warnings []string
	for key := range conditions {
		if !knownConditionTypes[key] {
			warnings = append(warnings, fmt.Sprintf("unknown condition type: %q", key))
		}
	}
	return warnings
}
