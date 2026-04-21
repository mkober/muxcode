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
	ToolInput    ToolInput       `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
	ToolResult   json.RawMessage `json:"tool_result,omitempty"`
	RawExitCode  interface{}     `json:"exit_code,omitempty"`
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
func HookOutcome(exitCode string) string {
	switch {
	case exitCode == "":
		return "unknown"
	case exitCode == "0":
		return "success"
	default:
		return "failure"
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
}

// CheckEditGuard checks if a command should be blocked in the edit window.
// Returns nil if the command is allowed.
func CheckEditGuard(command string) *GuardDecision {
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

	for _, rule := range editGuardRules {
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
