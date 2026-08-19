package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// ToolEvent represents the JSON event received from Claude Code hooks.
type ToolEvent struct {
	// ToolName is the tool being invoked (e.g. "Bash", "Edit",
	// "mcp__claude_ai_Atlassian__editJiraIssue"). Needed to gate MCP tools,
	// which carry no bash command to inspect.
	ToolName     string          `json:"tool_name,omitempty"`
	ToolInput    ToolInput       `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
	ToolResult   json.RawMessage `json:"tool_result,omitempty"`
	RawExitCode  interface{}     `json:"exit_code,omitempty"`
	// StopHookActive is set on Stop-hook events: true means a Stop hook already
	// blocked once this turn and the agent is being asked to stop again (a
	// re-entrant Stop). It is the loop guard for the self-poll re-launch hook.
	StopHookActive bool `json:"stop_hook_active,omitempty"`
}

// ToolInput holds the input fields of a tool event.
type ToolInput struct {
	Command      string `json:"command,omitempty"`
	Description  string `json:"description,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	NotebookPath string `json:"notebook_path,omitempty"`
	NewString    string `json:"new_string,omitempty"`
}

// ParseToolEvent parses a JSON tool event from raw bytes.
func ParseToolEvent(data []byte) (*ToolEvent, error) {
	var ev ToolEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// GetExitCode extracts the exit code from a tool event.
// Checks top-level exit_code, then tool_response/tool_result exit_code,
// then interrupted flag, then stderr prefix.
func (ev *ToolEvent) GetExitCode() string {
	// Check top-level exit_code
	if code := interfaceToString(ev.RawExitCode); code != "" {
		return code
	}

	// Check tool_response/tool_result
	for _, raw := range []json.RawMessage{ev.ToolResponse, ev.ToolResult} {
		if len(raw) == 0 {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			continue
		}
		if code := interfaceToString(obj["exit_code"]); code != "" {
			return code
		}
		if interrupted, ok := obj["interrupted"].(bool); ok && interrupted {
			return "1"
		}
		if stderr, ok := obj["stderr"].(string); ok && strings.HasPrefix(stderr, "Error:") {
			return "1"
		}
	}
	return "0"
}

// GetOutput extracts the command output from a tool event's response.
// Returns the last maxLines lines with ANSI codes stripped, truncated to maxChars.
func (ev *ToolEvent) GetOutput(maxLines, maxChars int) string {
	raw := ev.responseText()
	if raw == "" {
		return ""
	}

	// Strip ANSI escape codes (reuses StripANSI from console.go)
	raw = StripANSI(raw)

	// Take last N lines
	lines := strings.Split(raw, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := strings.Join(lines, "\n")

	// Truncate
	if len(out) > maxChars {
		out = out[:maxChars-3] + "..."
	}

	// Replace HOME with ~
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = strings.ReplaceAll(out, home, "~")
	}

	return out
}

// responseText extracts the text content from tool_response or tool_result.
func (ev *ToolEvent) responseText() string {
	for _, raw := range []json.RawMessage{ev.ToolResponse, ev.ToolResult} {
		if len(raw) == 0 {
			continue
		}
		// Try as string
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
		// Try as object with stdout/content
		var obj map[string]interface{}
		if json.Unmarshal(raw, &obj) == nil {
			if v, ok := obj["stdout"].(string); ok && v != "" {
				return v
			}
			if v, ok := obj["content"].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

// HookOutcome returns "success", "failure", or "unknown" based on the exit code.
// An absent exit code is "unknown", never "success" — a command that reported
// no status is not a command that reported success.
func HookOutcome(exitCode string) string {
	switch {
	case exitCode == "":
		return OutcomeUnknown
	case exitCode == "0":
		return OutcomeSuccess
	default:
		return OutcomeFailure
	}
}

// CommandType represents the classification of a bash command.
type CommandType int

const (
	CmdUnknown CommandType = iota
	CmdBuild
	CmdTest
	CmdDeploy
	CmdDeployApply
	CmdGit
	CmdBus
)

// DefaultBuildPatterns are the default patterns for detecting build commands.
var DefaultBuildPatterns = []string{
	"./build.sh", "pnpm*build", "go*build", "make", "cargo*build", "cdk*synth", "tsc",
}

// DefaultTestPatterns are the default patterns for detecting test commands.
var DefaultTestPatterns = []string{
	"./test.sh", "jest", "pnpm*test", "pytest", "go*test", "go*vet", "cargo*test", "vitest",
}

// DefaultDeployPatterns are the default patterns for detecting deploy commands.
var DefaultDeployPatterns = []string{
	"cdk*diff", "cdk*deploy", "cdk*destroy", "cdk*synth*--all",
	"terraform*plan", "terraform*apply", "terraform*destroy",
	"pulumi*up", "pulumi*destroy", "pulumi*preview",
	"sam*deploy", "sam*package",
	"cloudformation*deploy", "cloudformation*create-stack", "cloudformation*update-stack",
}

// DefaultDeployApplyPatterns are the patterns for deploy commands that trigger verify chains.
var DefaultDeployApplyPatterns = []string{
	"cdk*deploy", "cdk*destroy",
	"terraform*apply", "terraform*destroy",
	"pulumi*up", "pulumi*destroy",
	"sam*deploy",
	"cloudformation*deploy", "cloudformation*create-stack", "cloudformation*update-stack",
}

// DefaultGitPatterns are the default patterns for detecting git commands.
var DefaultGitPatterns = []string{
	"git*commit", "git*push", "git*merge", "git*rebase", "git*tag", "git*cherry-pick",
	"gh*pr*create", "gh*pr*merge", "gh*pr*close", "gh*release*create",
}

// ClassifyCommand detects the type of a bash command.
// Returns the most specific match (deploy-apply > deploy, etc).
func ClassifyCommand(command string) CommandType {
	// Skip bus commands
	if strings.HasPrefix(command, "muxcode") || strings.HasPrefix(command, "agent-bus") {
		return CmdBus
	}

	// Strip cd prefix and env vars to get the first real command
	firstCmd := stripCommandPrefix(command)

	patterns := loadPatterns()

	if matchPatterns(firstCmd, patterns.build, true) {
		return CmdBuild
	}
	if matchPatterns(firstCmd, patterns.test, true) {
		return CmdTest
	}
	if matchPatterns(firstCmd, patterns.deploy, true) {
		if matchPatterns(firstCmd, patterns.deployApply, true) {
			return CmdDeployApply
		}
		return CmdDeploy
	}
	if matchPatterns(firstCmd, patterns.git, false) {
		return CmdGit
	}
	return CmdUnknown
}

// commandPatterns holds all loaded command patterns.
type commandPatterns struct {
	build       []string
	test        []string
	deploy      []string
	deployApply []string
	git         []string
}

// loadPatterns loads command patterns from env vars or defaults.
func loadPatterns() commandPatterns {
	return commandPatterns{
		build:       envOrDefault("MUXCODE_BUILD_PATTERNS", DefaultBuildPatterns),
		test:        envOrDefault("MUXCODE_TEST_PATTERNS", DefaultTestPatterns),
		deploy:      envOrDefault("MUXCODE_DEPLOY_PATTERNS", DefaultDeployPatterns),
		deployApply: envOrDefault("MUXCODE_DEPLOY_APPLY_PATTERNS", DefaultDeployApplyPatterns),
		git:         envOrDefault("MUXCODE_GIT_PATTERNS", DefaultGitPatterns),
	}
}

// envOrDefault returns patterns from a pipe-delimited env var, or defaults.
func envOrDefault(envVar string, defaults []string) []string {
	if v := os.Getenv(envVar); v != "" {
		return strings.Split(v, "|")
	}
	return defaults
}

// stripCommandPrefix strips cd prefixes and env var assignments from a command.
func stripCommandPrefix(cmd string) string {
	// Strip cd prefix (e.g. "cd /foo && actual-cmd")
	if idx := strings.Index(cmd, "&&"); idx >= 0 {
		prefix := strings.TrimSpace(cmd[:idx])
		if strings.HasPrefix(prefix, "cd ") {
			cmd = strings.TrimSpace(cmd[idx+2:])
		}
	}
	// Strip env var assignments (e.g. "FOO=bar actual-cmd")
	for {
		trimmed := strings.TrimSpace(cmd)
		if len(trimmed) == 0 {
			break
		}
		eqIdx := strings.Index(trimmed, "=")
		spIdx := strings.Index(trimmed, " ")
		if eqIdx > 0 && (spIdx < 0 || eqIdx < spIdx) {
			key := trimmed[:eqIdx]
			if isEnvVarName(key) && spIdx > 0 {
				cmd = strings.TrimSpace(trimmed[spIdx+1:])
				continue
			}
		}
		break
	}
	return cmd
}

// isEnvVarName returns true if s looks like an env var name (letters, digits, _).
// Accepts both uppercase (FOO_BAR) and camelCase (envName) for CDK compatibility.
func isEnvVarName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// matchPatterns checks if a command matches any of the glob-style patterns.
// If withWrappers is true, also matches bash/sh/npx wrapper prefixes.
// Uses globMatch from tools.go for pattern matching.
func matchPatterns(cmd string, patterns []string, withWrappers bool) bool {
	for _, pat := range patterns {
		if globMatch(pat+"*", cmd) {
			return true
		}
		if withWrappers {
			base := pat
			if idx := strings.LastIndex(pat, "/"); idx >= 0 {
				base = pat[idx+1:]
			}
			if globMatch("bash*"+base+"*", cmd) || globMatch("sh*"+base+"*", cmd) {
				return true
			}
			if globMatch("npx*"+base+"*", cmd) {
				return true
			}
		}
	}
	return false
}

// errorPatterns matches error-relevant lines in command output.
var errorPatterns = regexp.MustCompile(
	`(?i)(error[:\[ (]|ERR!|failed|fatal|cannot |unable to |not found|no such|undefined|unresolved|exception|panic|FAIL[: ]|segfault|permission denied|syntax error|type.?error|reference.?error|assert|expect)`,
)

// ExtractErrors extracts error-relevant lines from output.
// Returns at most maxLines lines, truncated to maxChars.
func ExtractErrors(output string, maxLines, maxChars int) string {
	if output == "" {
		return ""
	}
	var matches []string
	for _, line := range strings.Split(output, "\n") {
		if errorPatterns.MatchString(line) && !strings.HasPrefix(line, "Exit code:") {
			matches = append(matches, line)
			if len(matches) >= maxLines {
				break
			}
		}
	}
	result := strings.Join(matches, "\n")
	if len(result) > maxChars {
		result = result[:maxChars-3] + "..."
	}
	return result
}

// ExtractGitSummary extracts a short summary from git command output.
func ExtractGitSummary(firstCmd, output string) string {
	switch {
	case strings.Contains(firstCmd, "commit"):
		re := regexp.MustCompile(`\[[^ ]+ [a-f0-9]+\] .+`)
		if m := re.FindString(output); m != "" {
			return m
		}
		re = regexp.MustCompile(`[a-f0-9]{7,}`)
		if m := re.FindString(output); m != "" {
			return m
		}
	case strings.Contains(firstCmd, "push"):
		re := regexp.MustCompile(`[a-z]+/[^ ]+\.\.[a-f0-9]+`)
		if m := re.FindString(output); m != "" {
			return m
		}
		return "push"
	case strings.Contains(firstCmd, "pr") && strings.Contains(firstCmd, "create"):
		re := regexp.MustCompile(`https://github\.com/[^ ]+/pull/[0-9]+`)
		if m := re.FindString(output); m != "" {
			return m
		}
		return "pr create"
	case strings.Contains(firstCmd, "pr") && strings.Contains(firstCmd, "merge"):
		return "pr merge"
	case strings.Contains(firstCmd, "pr") && strings.Contains(firstCmd, "close"):
		return "pr close"
	case strings.Contains(firstCmd, "release") && strings.Contains(firstCmd, "create"):
		return "release create"
	}
	return ""
}

// ExtractBuildChanges captures a short change summary from git.
func ExtractBuildChanges() string {
	if err := exec.Command("git", "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return ""
	}

	var files []string
	out, err := exec.Command("git", "diff", "--name-only", "HEAD").Output()
	if err == nil {
		files = nonEmptyLines(string(out))
	}
	if len(files) == 0 {
		out, err = exec.Command("git", "diff", "--name-only", "--cached", "HEAD").Output()
		if err == nil {
			files = nonEmptyLines(string(out))
		}
	}
	if len(files) == 0 {
		return ""
	}

	count := len(files)
	maxShow := 3
	if maxShow > count {
		maxShow = count
	}
	var names []string
	for _, f := range files[:maxShow] {
		names = append(names, filepath.Base(f))
	}
	result := fmt.Sprintf("%d files: %s", count, strings.Join(names, ", "))
	if count > 3 {
		result += fmt.Sprintf(", +%d more", count-3)
	}
	return result
}

// nonEmptyLines splits a string into non-empty trimmed lines.
func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// HookHistoryEntry represents a JSONL history entry written by the bash hook.
// Extends the base HistoryEntry from guard.go with additional hook-specific fields.
type HookHistoryEntry struct {
	TS          int64  `json:"ts"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	ExitCode    string `json:"exit_code"`
	Outcome     string `json:"outcome"`
	Output      string `json:"output,omitempty"`
	Errors      string `json:"errors,omitempty"`
	Changes     string `json:"changes,omitempty"`
	Summary     string `json:"summary,omitempty"`
	// Action carries the bus action for synthesized entries. It exists so those
	// entries can identify themselves without borrowing Command, which is
	// reserved for shell commands that actually ran.
	Action string `json:"action,omitempty"`
	// Source is the entry's provenance — empty for the authoritative hook /
	// self-logged path, SourceBusResponse for entries synthesized from a bus
	// response payload. See history_provenance.go.
	Source string `json:"source,omitempty"`
}

// WriteHookHistory appends a history entry to a JSONL file with file-level locking
// and rotation to keep the last maxEntries entries.
func WriteHookHistory(path string, entry HookHistoryEntry, maxEntries int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	// File-level locking (non-blocking, best-effort)
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)

	_, writeErr := f.Write(append(data, '\n'))
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()

	if writeErr != nil {
		return writeErr
	}

	rotateHookHistory(path, maxEntries)
	return nil
}

// rotateHookHistory truncates a JSONL file to keep only the last maxEntries lines.
func rotateHookHistory(path string, maxEntries int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) && len(data[start:]) > 0 {
		lines = append(lines, data[start:])
	}

	if len(lines) <= maxEntries {
		return
	}

	keep := lines[len(lines)-maxEntries:]
	var out []byte
	for _, line := range keep {
		out = append(out, line...)
		out = append(out, '\n')
	}
	_ = os.WriteFile(path, out, 0644)
}

// HookBashResult holds the result of processing a bash tool event.
type HookBashResult struct {
	CommandType CommandType
	Logged      bool
	Chained     bool
}

// ProcessBashHook processes a PostToolUse Bash event: classifies the command,
// writes history, and triggers chains. This is the core logic of muxcode-bash-hook.sh.
func ProcessBashHook(session, role string, ev *ToolEvent) HookBashResult {
	command := ev.ToolInput.Command
	if command == "" {
		return HookBashResult{}
	}

	cmdType := ClassifyCommand(command)
	if cmdType == CmdBus {
		return HookBashResult{}
	}

	exitCode := ev.GetExitCode()
	outcome := HookOutcome(exitCode)
	firstCmd := stripCommandPrefix(command)
	ts := time.Now().Unix()
	maxHistory := 100

	// Use larger capture limits for deploy commands (long output)
	maxLines, maxChars := 15, 1000
	switch cmdType {
	case CmdDeploy, CmdDeployApply:
		maxLines, maxChars = 50, 4000
	}
	output := ev.GetOutput(maxLines, maxChars)

	result := HookBashResult{CommandType: cmdType}

	// Workflow: transition on command detection
	switch cmdType {
	case CmdBuild:
		TransitionWorkflow(session, StateBuilding, "hook:bash:build")
	case CmdTest:
		TransitionWorkflow(session, StateTesting, "hook:bash:test")
	case CmdDeployApply:
		TransitionWorkflow(session, StateDeploying, "hook:bash:deploy")
	}

	switch cmdType {
	case CmdBuild:
		errors := ExtractErrors(output, 20, 1000)
		changes := ExtractBuildChanges()
		entry := HookHistoryEntry{
			TS:          ts,
			Command:     command,
			Description: ev.ToolInput.Description,
			ExitCode:    exitCode,
			Outcome:     outcome,
			Output:      output,
			Errors:      errors,
			Changes:     changes,
		}
		_ = WriteHookHistory(filepath.Join(BusDir(session), "build-history.jsonl"), entry, maxHistory)
		result.Logged = true

	case CmdTest:
		errors := ExtractErrors(output, 20, 1000)
		entry := HookHistoryEntry{
			TS:          ts,
			Command:     command,
			Description: ev.ToolInput.Description,
			ExitCode:    exitCode,
			Outcome:     outcome,
			Output:      output,
			Errors:      errors,
		}
		_ = WriteHookHistory(filepath.Join(BusDir(session), "test-history.jsonl"), entry, maxHistory)
		result.Logged = true

	case CmdDeploy, CmdDeployApply:
		errors := ExtractErrors(output, 20, 2000)
		entry := HookHistoryEntry{
			TS:          ts,
			Command:     command,
			Description: ev.ToolInput.Description,
			ExitCode:    exitCode,
			Outcome:     outcome,
			Output:      output,
			Errors:      errors,
		}
		_ = WriteHookHistory(filepath.Join(BusDir(session), "deploy-history.jsonl"), entry, maxHistory)
		result.Logged = true

	case CmdGit:
		summary := ExtractGitSummary(firstCmd, output)
		entry := HookHistoryEntry{
			TS:          ts,
			Command:     command,
			Description: ev.ToolInput.Description,
			ExitCode:    exitCode,
			Outcome:     outcome,
			Output:      output,
			Summary:     summary,
		}
		_ = WriteHookHistory(filepath.Join(BusDir(session), "commit-history.jsonl"), entry, maxHistory)
		result.Logged = true

	case CmdUnknown:
		if role == "run" || role == "runner" || role == "watch" {
			entry := HookHistoryEntry{
				TS:          ts,
				Command:     command,
				Description: ev.ToolInput.Description,
				ExitCode:    exitCode,
				Outcome:     outcome,
				Output:      output,
			}
			_ = WriteHookHistory(filepath.Join(BusDir(session), role+"-history.jsonl"), entry, maxHistory)
			result.Logged = true
		}
	}

	return result
}

// GuardDecision represents the result of an edit guard check.
type GuardDecision struct {
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason,omitempty"`
}

// guardRule maps a command pattern to a block reason.
type guardRule struct {
	prefixes []string
	reason   string
}

// editGuardRules defines all prohibited commands for the edit window.
var editGuardRules = []guardRule{
	{
		prefixes: []string{"gh pr view", "gh pr checks", "gh pr diff", "gh pr status", "gh pr list", "gh api repos/"},
		reason:   `BLOCKED: PR read commands are prohibited in the edit window. Delegate to the commit agent. Run this command: muxcode send commit pr-read "Read the PR on the current branch and report review feedback, CI failures, and suggested fixes" --wait`,
	},
	{
		prefixes: []string{"gh pr create", "gh pr merge", "gh pr close", "gh pr reopen", "gh pr edit", "gh release"},
		reason:   `BLOCKED: PR/release mutations are prohibited in the edit window. Delegate to the commit agent. Run: muxcode send commit commit "<describe the PR/release operation>" --wait`,
	},
	{
		prefixes: []string{"gh "},
		reason:   `BLOCKED: gh commands are prohibited in the edit window. Delegate to the commit agent. Run: muxcode send commit commit "<describe the operation>" --wait`,
	},
	{
		prefixes: []string{"git "},
		reason:   `BLOCKED: All git commands are prohibited in the edit window. Delegate to the commit agent. Run: muxcode send commit commit "<describe the git operation>" --wait`,
	},
	{
		prefixes: []string{"./build.sh", "pnpm build", "pnpm run build", "npm run build", "go build", "cargo build", "tsc "},
		reason:   `BLOCKED: Build commands are prohibited in the edit window. Delegate to the build agent. Run: muxcode send build build "Run ./build.sh and report results" --wait`,
	},
	{
		prefixes: []string{"make ", "make"},
		reason:   `BLOCKED: Build commands are prohibited in the edit window. Delegate to the build agent. Run: muxcode send build build "Run ./build.sh and report results" --wait`,
	},
	{
		prefixes: []string{
			"./test.sh", "pnpm test", "pnpm run test", "npm test", "npm run test",
			"jest", "npx jest", "npx vitest", "pytest", "python -m pytest",
			"go test", "cargo test",
		},
		reason: `BLOCKED: Test commands are prohibited in the edit window. Delegate to the test agent. Run: muxcode send test test "Run tests and report results" --wait`,
	},
	{
		prefixes: []string{"cdk ", "npx cdk ", "terraform ", "pulumi ", "sam "},
		reason:   `BLOCKED: Deploy commands are prohibited in the edit window. Delegate to the deploy agent. Run: muxcode send deploy deploy "<describe the deploy operation>" --wait`,
	},
	{
		prefixes: []string{"aws logs", "tail -f", "tail -F", "kubectl logs", "docker logs", "docker-compose logs", "stern "},
		reason:   `BLOCKED: Log tailing commands are prohibited in the edit window. Delegate to the watch agent. Run: muxcode send watch watch "<describe what logs to tail>" --wait`,
	},
	{
		prefixes: []string{"aws s3 ", "aws s3api ", "aws lambda ", "aws stepfunctions "},
		reason:   `BLOCKED: AWS commands are prohibited in the edit window. Delegate to the run agent. Run: muxcode send run run "<describe the AWS operation>" --wait`,
	},
	{
		prefixes: []string{"until ", "while true", "while :"},
		reason:   `BLOCKED: Polling loops are prohibited in the edit window. Use --wait for delegation: muxcode send <target> <action> "<message>" --wait`,
	},
}

// planGuardRules defines prohibited commands for the plan window.
// The plan agent has read-only git access — mutations must be delegated.
// Read-only git commands (status, log, diff, show, rev-parse) are allowed.
var planGuardRules = []guardRule{
	{
		prefixes: []string{
			"git commit", "git push", "git pull", "git rebase",
			"git checkout", "git switch",
			"git branch", "git merge", "git stash", "git tag",
			"git reset", "git clean", "git revert", "git cherry-pick",
			"git am", "git apply", "git mv", "git rm",
			"git add",
		},
		reason: `BLOCKED: Git mutations are prohibited in the plan window. Delegate to the commit agent. Run: muxcode send commit commit "<describe the git operation>" --force --wait`,
	},
	{
		prefixes: []string{"gh "},
		reason:   `BLOCKED: GitHub CLI commands are prohibited in the plan window. Delegate to the commit agent. Run: muxcode send commit commit "<describe the operation>" --force --wait`,
	},
	{
		prefixes: []string{"./build.sh", "pnpm build", "pnpm run build", "npm run build", "go build", "cargo build", "tsc "},
		reason:   `BLOCKED: Build commands are prohibited in the plan window. Delegate to the build agent. Run: muxcode send build build "Run ./build.sh and report results" --wait`,
	},
	{
		prefixes: []string{"make ", "make"},
		reason:   `BLOCKED: Build commands are prohibited in the plan window. Delegate to the build agent. Run: muxcode send build build "Run ./build.sh and report results" --wait`,
	},
	{
		prefixes: []string{
			"./test.sh", "pnpm test", "pnpm run test", "npm test", "npm run test",
			"jest", "npx jest", "npx vitest", "pytest", "python -m pytest",
			"go test", "cargo test",
		},
		reason: `BLOCKED: Test commands are prohibited in the plan window. Delegate to the test agent. Run: muxcode send test test "Run tests and report results" --wait`,
	},
	{
		prefixes: []string{"cdk ", "npx cdk ", "terraform ", "pulumi ", "sam "},
		reason:   `BLOCKED: Deploy commands are prohibited in the plan window. Delegate to the deploy agent. Run: muxcode send deploy deploy "<describe the deploy operation>" --wait`,
	},
	{
		prefixes: []string{"aws logs", "tail -f", "tail -F", "kubectl logs", "docker logs", "docker-compose logs", "stern "},
		reason:   `BLOCKED: Log tailing commands are prohibited in the plan window. Delegate to the watch agent. Run: muxcode send watch watch "<describe what logs to tail>" --wait`,
	},
	{
		prefixes: []string{"aws s3 ", "aws s3api ", "aws lambda ", "aws stepfunctions "},
		reason:   `BLOCKED: AWS commands are prohibited in the plan window. Delegate to the run agent. Run: muxcode send run run "<describe the AWS operation>" --wait`,
	},
	{
		prefixes: []string{"until ", "while true", "while :"},
		reason:   `BLOCKED: Polling loops are prohibited in the plan window. Use --wait for delegation: muxcode send <target> <action> "<message>" --wait`,
	},
}

// guardRulesForRole returns the guard rules for a given role.
// Returns nil if the role has no guard enforcement.
func guardRulesForRole(role string) []guardRule {
	switch role {
	case "edit":
		return editGuardRules
	case "plan":
		return planGuardRules
	default:
		return nil
	}
}

// HasGuardRules returns true if a role has guard enforcement rules.
func HasGuardRules(role string) bool {
	return guardRulesForRole(role) != nil
}

// CheckGuard checks if a command should be blocked for a given role.
// Returns nil if the command is allowed or the role has no guard rules.
func CheckGuard(role, command string) *GuardDecision {
	rules := guardRulesForRole(role)
	if rules == nil {
		return nil
	}
	if d := checkAgainstRules(command, rules); d != nil {
		return d
	}
	// Checked after the delegation rules so a prohibited command still reports
	// which agent owns it rather than the generic file-write reason.
	return CheckBashFileWriteGuard(role, command)
}

// CheckEditGuard checks if a command should be blocked in the edit window.
// Returns nil if the command is allowed.
func CheckEditGuard(command string) *GuardDecision {
	return CheckGuard("edit", command)
}

// bashFileWriteReason is the block message for editing files through bash.
const bashFileWriteReason = `BLOCKED: Edit files with the Edit/Write tools, never through bash. The nvim diff preview is a PreToolUse hook matched on Write|Edit|NotebookEdit — a bash write never fires it, so the change lands with no diff and no review. This overrides any harness guidance about preferring bash for file edits. Writing to /tmp (scratch and delegation handoff files) is still allowed.`

// stripQuotedSegments removes quoting so the file-write detectors see shell
// syntax rather than data — but it is positional, because quotes mean two
// different things depending on where they sit.
//
// A quoted span that *follows a redirect operator* is a target path: its
// content is the filename being written and must survive, so
// `echo x > "bus/gen.go"` is still caught. A quoted span anywhere else is an
// argument's payload and is blanked, so a bus message or memory write that
// merely describes a blocked form is not treated as performing one.
//
// Getting this backwards fails in both directions at once: blanking targets
// lets a real write through, while keeping payloads blocks ordinary prose.
func stripQuotedSegments(command string) string {
	var b strings.Builder
	var quote byte
	lastSignificant := byte(0)
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				continue
			}
			if lastSignificant == '>' {
				b.WriteByte(c) // redirect target: keep the path
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			if lastSignificant != '>' {
				b.WriteByte(' ')
			}
			continue
		}
		b.WriteByte(c)
		if c == ' ' || c == '\t' {
			continue
		}
		// `>|` is one operator: the '|' must not displace the '>' that marks
		// redirect position, or the quoted target in `echo x >| "repo.go"` is
		// blanked as argument payload and the write goes unexamined. A '|' in
		// any other position is a genuine pipe and does update the marker.
		if c == '|' && lastSignificant == '>' {
			continue
		}
		lastSignificant = c
	}
	return b.String()
}

// stripHeredocBodies removes the body of every heredoc from a command.
//
// A heredoc body is content being written, not shell syntax to police: a file
// whose text happens to contain "sed -i" or "> out" must not trip the guard.
// The `cat > file <<EOF` redirect that precedes the body is on the command line
// and still detected — only the payload between the delimiter line and its
// terminator is dropped.
func stripHeredocBodies(command string) string {
	for {
		idx := strings.Index(command, "<<")
		if idx < 0 {
			return command
		}
		rest := command[idx+2:]
		rest = strings.TrimPrefix(rest, "-")
		nl := strings.Index(rest, "\n")
		if nl < 0 {
			// No body present (single-line command); keep what precedes it.
			return command[:idx]
		}
		// Only the FIRST token is the delimiter. Taking the whole line broke the
		// `cat <<EOF > out` ordering: the delimiter became "EOF > out", no body
		// line ever matched it, and every command after the real terminator was
		// swallowed as body — so a trailing `sed -i` on a repo file passed.
		var delim string
		if f := strings.Fields(rest[:nl]); len(f) > 0 {
			delim = strings.Trim(f[0], `"'`)
		}
		head := command[:idx] + rest[:nl]
		if delim == "" {
			return head
		}
		body := rest[nl+1:]
		end := len(body)
		// Running offset, not strings.Index: searching for the terminator by
		// content matched the delimiter as a *substring* of an earlier body line
		// ("mentions EOF here"), cutting mid-body and leaking the remainder back
		// into the command to be scanned as syntax.
		off := 0
		for _, line := range strings.SplitAfter(body, "\n") {
			if strings.TrimSpace(line) == delim {
				end = off + len(line)
				break
			}
			off += len(line)
		}
		// Rejoin with a newline: the terminator line was consumed, so splicing
		// directly would fuse the last token of the head onto the first token of
		// the next command ("/tmp/plan.md" + "sed" -> "/tmp/plan.mdsed"). That
		// single missing byte defeated both detectors at once — the fused path
		// still looked /tmp-scratch, and sed was no longer segment-initial.
		command = head + "\n" + body[end:]
	}
}

// commandSegments splits a command on shell separators so each detector sees
// one simple command at a time.
//
// Without this, flag scanning bleeds across a pipeline: `sed -n '1p' f | grep -i x`
// matched grep's -i and blocked a read as though it were an in-place edit.
// Splitting on single '&' covers '&&' for free and also catches backgrounding
// (`sed ... & grep -i x`), which is the same bleed in a rarer shape. Segments
// feed only the flag and tee scanners — never redirect extraction — so tearing
// "2>&1" into "2>" and "1" here is harmless.
func commandSegments(command string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(command, func(r rune) bool {
		return r == '|' || r == ';' || r == '\n' || r == '&'
	}) {
		if s := strings.TrimSpace(f); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// isScratchPath reports whether a redirect target is throwaway output rather
// than a file anyone reviews: temp dirs and the /dev pseudo-files. Writes there
// bypass no review, so they stay allowed.
func isScratchPath(p string) bool {
	p = strings.Trim(p, `"'`)
	// An empty target is a parse artifact, not a filename. Treating it as a
	// real path made a bare quote block the command that contained it.
	if p == "" {
		return true
	}
	switch {
	case strings.HasPrefix(p, "/dev/"):
		return true
	case strings.HasPrefix(p, "/tmp/"), strings.HasPrefix(p, "/private/tmp/"):
		return true
	case strings.HasPrefix(p, "/var/folders/"): // macOS mktemp
		return true
	case strings.HasPrefix(p, "$TMPDIR"), strings.HasPrefix(p, "${TMPDIR}"):
		return true
	}
	return false
}

// redirectTargets extracts the file paths a command redirects into with > or >>.
//
// File-descriptor duplications (2>&1, >&2) are skipped: they redirect between
// streams and never create a file. Both spaced and attached forms are handled
// (`> f`, `>f`, `>>f`), because a heredoc write — the shape that motivated this
// guard — arrives as `cat > path <<EOF`.
func redirectTargets(command string) []string {
	var targets []string
	for i := 0; i < len(command); i++ {
		if command[i] != '>' {
			continue
		}
		j := i + 1
		if j < len(command) && command[j] == '>' {
			j++
		}
		if j < len(command) && command[j] == '&' {
			continue // fd duplication, not a file
		}
		// `>|` is the noclobber override — still a file redirect. Without this
		// the '|' lands in the terminator set, the target scan advances zero
		// bytes, and no target is extracted at all: `echo x >| repo.go` wrote a
		// repo file and the guard saw nothing to check.
		if j < len(command) && command[j] == '|' {
			j++
		}
		for j < len(command) && (command[j] == ' ' || command[j] == '\t') {
			j++
		}
		start := j
		for j < len(command) && !strings.ContainsRune(" \t;|&\n", rune(command[j])) {
			j++
		}
		if start < j {
			targets = append(targets, command[start:j])
		}
	}
	return targets
}

// hasInPlaceEdit reports whether a command edits a file in place via sed or
// perl. These have no non-mutating use, so they are blocked outright.
func hasInPlaceEdit(command string) bool {
	for _, seg := range commandSegments(command) {
		fields := strings.Fields(seg)
		for i, f := range fields {
			base := filepath.Base(f)
			if base != "sed" && base != "perl" {
				continue
			}
			// Only this segment's own arguments — a later command's flags in a
			// pipeline are not sed's.
			for _, arg := range fields[i+1:] {
				if strings.HasPrefix(arg, "--in-place") || arg == "-i" || strings.HasPrefix(arg, "-i.") {
					return true
				}
				// Clustered short flags (perl -pi, sed -ni). Bounded length so a
				// long word starting with '-' that merely contains an 'i' — an
				// operand, not a flag cluster — does not match.
				if len(arg) > 1 && len(arg) <= 5 && arg[0] == '-' && arg[1] != '-' && strings.Contains(arg, "i") {
					return true
				}
			}
			break // the segment's command is resolved; don't rescan its operands
		}
	}
	return false
}

// CheckBashFileWriteGuard blocks editing repository files through bash in roles
// that own an editor pane.
//
// The rule exists because the nvim diff split is a PreToolUse hook matched on
// Write|Edit|NotebookEdit. A bash write — `sed -i`, a `cat > file <<EOF`
// heredoc — matches no such tool, so the hook never fires and the edit reaches
// disk with no diff shown and no chance to review it. Claude Code's
// bypass-permissions mode actively suggests exactly those bash forms, so
// instruction alone has already proven insufficient: this is the enforcement
// that makes the preference stick.
//
// Scratch writes under /tmp stay allowed — the file-handoff delegation pattern
// depends on them, and nobody reviews a handoff file.
//
// KNOWN GAP: only writes visible as shell syntax are detected — redirects, tee,
// and in-place sed/perl. A write performed *inside* an interpreter, such as
// `python3 -c 'open("x.go","w").write(...)'`, carries no such syntax and passes.
// Detecting that would mean interpreting the script, so the agent-definition
// ban in agents/code-editor.md is the only cover there. Stated explicitly
// because a guard whose comment promises more than it enforces is worse than a
// narrow one: it invites reliance the code cannot support.
func CheckBashFileWriteGuard(role, command string) *GuardDecision {
	if !HasGuardRules(role) {
		return nil
	}
	// Detection runs on shell syntax only, never on quoted data. A bus message,
	// memory write, or commit message that merely *describes* a blocked form
	// ("never use sed -i") must not be blocked as though it performed one —
	// observed immediately on this guard's first real use, where
	// `muxcode memory write "... sed -i ..."` was rejected.
	// Order matters: drop heredoc bodies before anything else, or an unbalanced
	// quote inside a written file would desynchronise the quote scanner and
	// corrupt every check downstream.
	command = stripQuotedSegments(stripHeredocBodies(command))
	if hasInPlaceEdit(command) {
		return &GuardDecision{Blocked: true, Reason: bashFileWriteReason}
	}
	for _, t := range redirectTargets(command) {
		if !isScratchPath(t) {
			return &GuardDecision{Blocked: true, Reason: bashFileWriteReason}
		}
	}
	// tee writes its file arguments without any '>', so redirectTargets misses
	// it entirely — a hole big enough to drive the whole guard through.
	for _, t := range teeTargets(command) {
		if !isScratchPath(t) {
			return &GuardDecision{Blocked: true, Reason: bashFileWriteReason}
		}
	}
	return nil
}

// teeTargets returns every file argument passed to a tee invocation.
//
// All of them, not just the first: `tee /tmp/a.log config/settings.json` writes
// both, so stopping at the first scratch path would wave the repo file through.
// Scanning is per-segment, so the `|` in `echo x | tee | wc -l` is a separator
// rather than a filename.
func teeTargets(command string) []string {
	var targets []string
	for _, seg := range commandSegments(command) {
		fields := strings.Fields(seg)
		for i, f := range fields {
			if filepath.Base(f) != "tee" {
				continue
			}
			for _, arg := range fields[i+1:] {
				// Stop at the redirect region: `tee /tmp/c.log > /dev/null`
				// otherwise collects ">" itself as a filename, which is not a
				// scratch path and blocked a legitimate scratch write.
				// redirectTargets already polices what follows.
				if strings.ContainsRune(arg, '>') {
					break
				}
				if strings.HasPrefix(arg, "-") {
					continue
				}
				targets = append(targets, arg)
			}
			break
		}
	}
	return targets
}

// isDocsMarkdown reports whether filePath is a Markdown file under a docs/
// directory (relative or absolute). CLAUDE.md and README.md at the repo root
// are NOT docs/ files and therefore not matched.
func isDocsMarkdown(filePath string) bool {
	p := filepath.ToSlash(filePath)
	if !strings.HasSuffix(strings.ToLower(p), ".md") {
		return false
	}
	return strings.HasPrefix(p, "docs/") || strings.Contains(p, "/docs/")
}

// CheckDocFileGuard blocks a role from directly writing documentation that must
// be authored by the plan agent instead. All Markdown under docs/ — specs,
// requirements, architecture — is delegated: the plan agent owns the docs tree.
// The plan agent is exempt (it is the delegation target). Returns nil when the
// write is allowed (the plan agent, or any non-docs/*.md file). Only roles with
// guard enforcement reach this via hookGuard, so in practice this gates edit.
func CheckDocFileGuard(role, filePath string) *GuardDecision {
	if role == "plan" {
		return nil
	}
	if !isDocsMarkdown(filePath) {
		return nil
	}
	return &GuardDecision{
		Blocked: true,
		Reason:  `BLOCKED: Documentation under docs/ (specs, requirements, architecture) must be authored by the plan agent, not edited directly in the edit window. Delegate to the plan agent. Run: muxcode send plan update-docs "<describe the doc change>" --wait`,
	}
}

// atlassianCommandTarget extracts the (service, action) pair from a bash command
// that invokes `muxcode atlassian ...`, returning ("", "") when the command is
// not an Atlassian call.
//
// Scans tokens rather than matching a prefix, so it survives the shapes these
// commands actually arrive in: `cd /repo && muxcode atlassian jira update ...`,
// `MUXCODE_CONFIG=x muxcode atlassian ...`, and `./bin/muxcode atlassian ...`.
// A prefix match would miss all three.
func atlassianCommandTarget(command string) (string, string) {
	fields := strings.Fields(command)
	for i := 1; i+2 < len(fields); i++ {
		if fields[i] != "atlassian" {
			continue
		}
		if filepath.Base(fields[i-1]) != "muxcode" {
			continue
		}
		return fields[i+1], fields[i+2]
	}
	return "", ""
}

// CheckAtlassianCommandGuard blocks an unauthorized role from running a
// mutating `muxcode atlassian` command, before the command executes.
//
// This is defence in depth, not the enforcement itself: CheckAtlassianAuthority
// in cmd/atlassian.go is the load-bearing gate and covers every provider,
// including the OpenCode and Codex agents that never run PreToolUse hooks at
// all. The value of blocking here too is that the agent is stopped at the tool
// layer with an actionable reason instead of discovering a non-zero exit, and
// the tool profile cannot be relied on for this — the "bus" include group grants
// `Bash(muxcode *)`, which already covers every atlassian subcommand for every
// role that includes it.
//
// Deliberately delegates the mutating/read-only decision to
// IsAtlassianMutatingAction rather than restating it as guard-rule prefixes.
// A second copy of that list is how this incident happened: the agent
// definition and the edit agent's definition each described a different owner
// for Jira, and the model followed whichever it read last.
func CheckAtlassianCommandGuard(role, command string) *GuardDecision {
	service, action := atlassianCommandTarget(command)
	if service == "" {
		return nil
	}
	deny := CheckAtlassianAuthority(role, service, action)
	if deny == "" {
		return nil
	}
	return &GuardDecision{Blocked: true, Reason: "BLOCKED: " + deny}
}

// checkAgainstRules normalizes a command and checks it against a set of guard rules.
func checkAgainstRules(command string, rules []guardRule) *GuardDecision {
	cmd := strings.TrimSpace(command)
	if idx := strings.Index(cmd, "&&"); idx >= 0 {
		prefix := strings.TrimSpace(cmd[:idx])
		if strings.HasPrefix(prefix, "cd ") {
			cmd = strings.TrimSpace(cmd[idx+2:])
		}
	}

	// Handle env var prefixes (e.g. "envName=prod cdk diff")
	stripped := cmd
	for {
		eqIdx := strings.Index(stripped, "=")
		spIdx := strings.Index(stripped, " ")
		if eqIdx > 0 && (spIdx < 0 || eqIdx < spIdx) {
			key := stripped[:eqIdx]
			if isEnvVarName(key) && spIdx > 0 {
				stripped = strings.TrimSpace(stripped[spIdx+1:])
				continue
			}
		}
		break
	}

	for _, rule := range rules {
		for _, prefix := range rule.prefixes {
			if strings.HasPrefix(cmd, prefix) || strings.HasPrefix(stripped, prefix) {
				return &GuardDecision{Blocked: true, Reason: rule.reason}
			}
			if prefix == "make" && cmd == "make" {
				return &GuardDecision{Blocked: true, Reason: rule.reason}
			}
		}
	}
	return nil
}

// FormatGuardBlock returns the JSON block decision for a hook response.
func FormatGuardBlock(reason string) string {
	result := map[string]string{
		"decision": "block",
		"reason":   reason,
	}
	data, _ := json.Marshal(result)
	return string(data)
}

// FormatStopBlock returns the JSON a Stop hook prints (exit 0) to prevent the
// agent from stopping and feed reason back into its context. Shares the
// {"decision":"block","reason":...} shape with FormatGuardBlock today, but is
// named separately because a Stop-block and a tool-guard-block are distinct
// hook responses that may diverge (e.g. hookSpecificOutput) later.
func FormatStopBlock(reason string) string {
	return FormatGuardBlock(reason)
}

// StopHookPollReason is the instruction fed back to a Claude agent whose
// background inbox listener has died, telling it to re-launch the self-poll so
// it keeps receiving delegated work. Kept as a single line: it is echoed into
// the agent's context, and the poll command must be copy-pasteable.
const StopHookPollReason = "Your background inbox listener is not running. " +
	"Re-launch it now so you keep receiving delegated work: run `muxcode inbox --poll --loop` " +
	"as a background Bash command (run_in_background=true). It blocks until a bus message " +
	"arrives, then returns it for you to process. Start it before ending your turn."

// StopHookAction is the decision computed by the Stop-hook self-poll logic.
type StopHookAction struct {
	Block  bool
	Reason string
}

// DecideStopHook computes whether a Claude Stop hook should block the agent
// from stopping in order to re-launch its self-poll listener.
//
// It blocks — forcing the agent to continue and restart the poll — only when a
// listener is genuinely absent AND we are not already inside a stop-hook
// continuation (stopHookActive). Gating on stopHookActive guarantees at most one
// block per real stop, so the hook can never loop even if the re-launch keeps
// failing. When a poll/wait listener is already alive, or the delivery-ack
// feature is disabled via kill switch, it allows the stop.
//
// inboxPending is the escape from a treadmill. A listener blocks until a message
// arrives, so on a quiet session it just sits there — and a runtime that reclaims
// idle background tasks will eventually kill it. The agent then stops, this hook
// finds no listener, blocks to demand a relaunch, the replacement sits idle and
// is reclaimed in turn: a loop that burns a turn per cycle and delivers nothing,
// because there was never a message to deliver. Blocking is only worth its cost
// when something is actually waiting, or when a message could arrive and find
// nobody home — and the latter is covered: the daemon's checkIdleAgents keeps
// waking self-poll-capable agents that have no listener (see needsIdleFallback),
// so an agent that stops empty-handed still gets its next message promptly and
// relaunches the listener then.
//
// Pure and side-effect-free for testability; callers supply the observed state.
func DecideStopHook(listenerAlive, stopHookActive, disabled, inboxPending bool) StopHookAction {
	if disabled {
		return StopHookAction{Block: false}
	}
	if stopHookActive {
		return StopHookAction{Block: false} // loop guard: already blocked this turn
	}
	if listenerAlive {
		return StopHookAction{Block: false} // a poll/wait listener is running
	}
	if !inboxPending {
		// Nothing to deliver: let the agent rest. The daemon's idle wake covers
		// the window until it launches a listener again.
		return StopHookAction{Block: false}
	}
	return StopHookAction{Block: true, Reason: StopHookPollReason}
}

// ShouldPollInbox checks if a command is a bus send that warrants inbox polling.
// Returns false for sends to self, fire-and-forget types, and non-send commands.
func ShouldPollInbox(command string) bool {
	if !strings.HasPrefix(command, "muxcode send ") && !strings.HasPrefix(command, "agent-bus send ") {
		return false
	}
	if strings.Contains(command, " send edit ") {
		return false
	}
	if strings.Contains(command, "--type event") ||
		strings.Contains(command, "--type notification") ||
		strings.Contains(command, "--no-notify") {
		return false
	}
	return true
}

// PollInbox polls the edit inbox every pollInterval until messages arrive or timeout.
// Returns consumed messages or a timeout message.
func PollInbox(session string, timeout, pollInterval time.Duration) string {
	SetWaiting(session, "edit")
	defer ClearWaiting(session, "edit")

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		if !HasMessages(session, "edit") {
			continue
		}

		msgs, err := Receive(session, "edit")
		if err != nil || len(msgs) == 0 {
			continue
		}

		var lines []string
		for _, m := range msgs {
			lines = append(lines, FormatMessage(m))
		}
		return strings.Join(lines, "\n")
	}

	return fmt.Sprintf("No response received within %ds. Check manually: muxcode inbox --peek", int(timeout.Seconds()))
}

// AnalyzeHookResult holds the results of processing an analyze (Write/Edit) hook event.
type AnalyzeHookResult struct {
	FilePath       string
	TriggerWritten bool
	DiffCleaned    bool
}

// ProcessAnalyzeHook handles a PostToolUse Write/Edit event: writes the trigger file,
// cleans up nvim diff preview, and routes file-change events.
func ProcessAnalyzeHook(session, windowName string, ev *ToolEvent) AnalyzeHookResult {
	result := AnalyzeHookResult{}

	filePath := ev.ToolInput.FilePath
	if filePath == "" {
		filePath = ev.ToolInput.NotebookPath
	}
	if filePath == "" {
		return result
	}
	result.FilePath = filePath

	// Skip agent state files
	if strings.Contains(filePath, "/.claude/") || strings.Contains(filePath, "/.muxcode/") {
		return result
	}

	// Write trigger file for the daemon
	triggerPath := TriggerFile(session)
	triggerLine := fmt.Sprintf("%d %s\n", time.Now().Unix(), filePath)
	if f, err := os.OpenFile(triggerPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		f.WriteString(triggerLine)
		f.Close()
		result.TriggerWritten = true
	}

	// Workflow: file edit regresses state to editing
	TransitionWorkflow(session, StateEditing, "hook:analyze:edit",
		WithFiles([]string{filePath}))

	// Clean up nvim diff preview (edit window only)
	if windowName == "edit" {
		cleanupDiffPreview(session, ev, filePath)
		result.DiffCleaned = true
	}

	// File-change routing goes exclusively to the analyze agent via the
	// trigger file + daemon's routeTrigger(). Build, test, and deploy
	// agents are only triggered by explicit bus messages, not file changes.

	return result
}

// cleanupDiffPreview sends tmux commands to clean up the nvim diff preview.
func cleanupDiffPreview(session string, ev *ToolEvent, filePath string) {
	tempFile := fmt.Sprintf("/tmp/muxcode-preview-%s.tmp", session)
	paneTarget := session + ":edit.0"

	line := "1"
	if needle := firstNonEmptyLine(ev.ToolInput.NewString); needle != "" {
		if out, err := exec.Command("grep", "-nF", "--", needle, filePath).Output(); err == nil {
			parts := strings.SplitN(string(out), "\n", 2)
			if len(parts) > 0 {
				if cols := strings.SplitN(parts[0], ":", 2); len(cols) > 0 && cols[0] != "" {
					line = cols[0]
				}
			}
		}
	}

	escapedPath := strings.ReplaceAll(filePath, " ", "\\ ")

	// Wait for async PreToolUse preview hook to finish setting up the diff
	time.Sleep(1 * time.Second)

	// Send Escape to ensure normal mode
	exec.Command("tmux", "send-keys", "-t", paneTarget, "Escape", "Escape").Run()
	time.Sleep(100 * time.Millisecond)

	if _, err := os.Stat(tempFile); err == nil {
		vimCmd := fmt.Sprintf(":sil! set shortmess+=AF | sil! exe 'b!'.get(g:,'_mux_buf',bufnr()) | sil! diffoff! | sil! only | sil! exe 'e! +%s %s' | sil! set number | sil! setlocal foldlevel=99", line, escapedPath)
		exec.Command("tmux", "send-keys", "-t", paneTarget, vimCmd, "Enter").Run()
		os.Remove(tempFile)
	} else {
		vimCmd := fmt.Sprintf(":sil! set shortmess+=AF | sil! exe 'e! +%s %s' | sil! setlocal foldlevel=99 | sil! set number | sil! filetype detect", line, escapedPath)
		exec.Command("tmux", "send-keys", "-t", paneTarget, vimCmd, "Enter").Run()
	}
}

// firstNonEmptyLine returns the first non-blank line from a string.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// interfaceToString converts an interface{} to a string representation.
func interfaceToString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%d", int(val))
	case int:
		return fmt.Sprintf("%d", val)
	case json.Number:
		return val.String()
	default:
		return fmt.Sprintf("%v", val)
	}
}
