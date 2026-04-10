package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OpenCodeProvider implements the Provider interface for OpenCode CLI
// in TUI mode. Launches the bare `opencode` binary and relies on
// pane-based detection and display-message notifications.
// Server mode is deferred to Phase 5.
type OpenCodeProvider struct{}

// --- Provider interface ---

func (p *OpenCodeProvider) Name() string { return "opencode" }

// ConfigureLaunch populates OpenCode-specific fields in the LaunchConfig.
// Resolves agent definition file and shared prompt for WriteAgentConfig.
func (p *OpenCodeProvider) ConfigureLaunch(cfg *LaunchConfig, role string) {
	// Resolve agent file — OpenCode agents use the same source definitions
	agentName := AgentFileName(role)
	cfg.Agent = agentName
	if agentName != "" {
		installDir := resolveInstallDir()
		agentFile, _ := ResolveAgentFile(agentName, installDir)
		cfg.AgentFile = agentFile
	}

	// Shared prompt (used as agent markdown body in WriteAgentConfig)
	cfg.SharedPrompt = BuildSharedPrompt(role)
}

// BuildExecArgs constructs the OpenCode launch command.
// All roles launch the bare TUI: ("opencode", []).
func (p *OpenCodeProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) {
	return cfg.CLI, nil
}

// IsIdle always returns false for TUI mode.
// The TUI has no stable prompt character that can be matched via pane capture.
func (p *OpenCodeProvider) IsIdle(session, role string) bool {
	return false
}

// IsAlive checks whether the OpenCode TUI is running via pane capture.
// Looks for "opencode" text or box-drawing characters (TUI frame).
// If the pane shows a bare shell prompt, the agent is dead.
func (p *OpenCodeProvider) IsAlive(session, role string) bool {
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-5")
	out, err := cmd.Output()
	if err != nil {
		return true // indeterminate -> assume alive
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "opencode") {
			return true
		}
		// Box-drawing characters indicate TUI is rendered
		for _, ch := range []string{"─", "│", "╭", "╰", "┌", "└"} {
			if strings.Contains(trimmed, ch) {
				return true
			}
		}
	}
	return !isShellPrompt(lines)
}

// ClassifyPane determines the startup state of an OpenCode pane.
// Box-drawing characters indicate the TUI has rendered and is ready.
func (p *OpenCodeProvider) ClassifyPane(content string) PaneState {
	// TUI mode: box-drawing characters indicate the TUI has rendered
	for _, ch := range []string{"─", "│", "╭", "╰", "┌", "└"} {
		if strings.Contains(content, ch) {
			return PaneIdle
		}
	}
	// Check for common error states
	if strings.Contains(content, "Error") || strings.Contains(content, "FATAL") {
		return PaneNotReady
	}
	return PaneNotReady
}

// AcceptStartup handles OpenCode TUI startup — no action needed once
// the TUI has rendered (ClassifyPane returns PaneIdle).
func (p *OpenCodeProvider) AcceptStartup(session, pane string, state PaneState) bool {
	return state == PaneIdle
}

// SendWakeUp reads the latest pending message from the inbox and injects
// it as text into the OpenCode TUI input via tmux send-keys. Since OpenCode
// has no hooks or inbox polling, the message content must be typed directly
// into the prompt. Text and Enter are sent as separate send-keys calls with
// a brief delay to avoid the TUI dropping the Enter key.
func (p *OpenCodeProvider) SendWakeUp(session, role string) error {
	target := PaneTarget(session, role)

	// Read pending messages to build the prompt text
	msgs, err := Peek(session, role)
	if err != nil || len(msgs) == 0 {
		return nil // nothing to inject
	}

	// Use the latest message's payload as the prompt
	last := msgs[len(msgs)-1]
	prompt := last.Payload
	if prompt == "" {
		prompt = fmt.Sprintf("You have a new %s request from %s", last.Action, last.From)
	}

	// Consume the inbox so the message isn't re-injected on next wake-up
	_, _ = Receive(session, role)

	// Send text first
	cmd := exec.Command("tmux", "send-keys", "-t", target, prompt)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s/%s failed: %v\n", role, "opencode", err)
		return err
	}
	// Brief delay so the TUI registers the text before Enter
	time.Sleep(150 * time.Millisecond)
	// Send Enter
	cmd = exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s/%s failed: %v\n", role, "opencode", err)
		return err
	}
	return nil
}

// Compact is a no-op for TUI mode — the TUI manages its own context
// and auto-compacts at 95%.
func (p *OpenCodeProvider) Compact(session, role, target string) error {
	return nil
}

func (p *OpenCodeProvider) SupportsHooks() bool    { return false }
func (p *OpenCodeProvider) IdlePromptChar() string { return "" }

// WriteAgentConfig generates the OpenCode agent definition file at
// .opencode/agents/<role>.md with YAML frontmatter containing
// permissions translated from muxcode tool profiles.
func (p *OpenCodeProvider) WriteAgentConfig(role string) error {
	return writeOpenCodeAgentConfig(role)
}

// --- Agent config generation ---

// writeOpenCodeAgentConfig generates .opencode/agents/<role>.md with
// YAML frontmatter containing permissions translated from tool profiles.
func writeOpenCodeAgentConfig(role string) error {
	// Ensure .opencode/agents/ directory exists
	dir := filepath.Join(".opencode", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// Build agent content
	agentName := AgentFileName(role)
	description := role + " agent"
	var agentBody string

	// Read the source agent definition if available
	if agentName != "" {
		installDir := resolveInstallDir()
		agentFile, _ := ResolveAgentFile(agentName, installDir)
		if agentFile != "" {
			data, err := os.ReadFile(agentFile)
			if err == nil {
				fm, body := ExtractFrontmatter(string(data))
				if fm.Description != "" {
					description = fm.Description
				}
				agentBody = body
			}
		}
	}

	// Build permission block from tool profiles
	permissions := translateToolProfile(role)

	// Build shared prompt
	sharedPrompt := BuildSharedPrompt(role)

	// Compose the markdown file
	var buf strings.Builder
	buf.WriteString("---\n")
	buf.WriteString(fmt.Sprintf("description: %s\n", description))
	buf.WriteString("mode: primary\n")

	// Model selection
	model := resolveOpenCodeModel(role)
	if model != "" {
		buf.WriteString(fmt.Sprintf("model: %s\n", model))
	}

	// Permission block
	if permissions != "" {
		buf.WriteString(permissions)
	}

	buf.WriteString("---\n\n")

	// Agent body from definition file — adapt hook references for non-hook provider
	if agentBody != "" {
		agentBody = adaptBodyForNonHookProvider(agentBody, role)
		buf.WriteString(agentBody)
		buf.WriteString("\n\n")
	}

	// Shared prompt
	if sharedPrompt != "" {
		buf.WriteString(sharedPrompt)
		buf.WriteString("\n")
	}

	// Write the file
	outPath := filepath.Join(dir, role+".md")
	return os.WriteFile(outPath, []byte(buf.String()), 0o644)
}

// translateToolProfile converts muxcode tool profiles to OpenCode permission YAML.
// Returns a YAML fragment suitable for embedding in frontmatter.
func translateToolProfile(role string) string {
	tools := ResolveTools(role)
	if len(tools) == 0 {
		return ""
	}

	bashAllow := []string{}
	bashDeny := []string{}
	editAllow := false

	for _, tool := range tools {
		switch {
		case tool == "Write" || tool == "Edit":
			editAllow = true
		case strings.HasPrefix(tool, "Bash(") && strings.HasSuffix(tool, ")"):
			// Extract the pattern from Bash(pattern)
			pattern := tool[5 : len(tool)-1]
			bashAllow = append(bashAllow, pattern)
		case strings.HasPrefix(tool, "!Bash(") && strings.HasSuffix(tool, ")"):
			// Deny pattern: !Bash(pattern)
			pattern := tool[6 : len(tool)-1]
			bashDeny = append(bashDeny, pattern)
			// Read, Grep, Glob — implicitly allowed in OpenCode, no permission needed
		}
	}

	if len(bashAllow) == 0 && len(bashDeny) == 0 && !editAllow {
		return ""
	}

	var buf strings.Builder
	buf.WriteString("permission:\n")

	if len(bashAllow) > 0 || len(bashDeny) > 0 {
		buf.WriteString("  bash:\n")
		for _, pattern := range bashAllow {
			buf.WriteString(fmt.Sprintf("    \"%s\": allow\n", pattern))
		}
		for _, pattern := range bashDeny {
			buf.WriteString(fmt.Sprintf("    \"%s\": deny\n", pattern))
		}
	}

	if editAllow {
		buf.WriteString("  edit: allow\n")
	}

	return buf.String()
}

// resolveOpenCodeModel returns the OpenCode model string for a role.
// Maps Claude model names to OpenCode provider/model format.
func resolveOpenCodeModel(role string) string {
	// Check for explicit OpenCode model env var
	envKey := "MUXCODE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_")) + "_MODEL"
	if model := os.Getenv(envKey); model != "" {
		return model
	}

	// Map Claude model defaults to Anthropic provider format
	claudeModel := resolveClaudeModel(role)
	if claudeModel == "" {
		claudeModel = RoleClaudeModelDefault(role)
	}
	if claudeModel == "" {
		return ""
	}

	// Translate: claude-sonnet-4-5 -> anthropic/claude-sonnet-4-5
	if !strings.Contains(claudeModel, "/") {
		return "anthropic/" + claudeModel
	}
	return claudeModel
}

// DetectTaskCompletion analyzes captured pane content from the OpenCode TUI
// to determine if the agent has finished processing a task.
//
// Completion signals (from OpenCode Zen mode output):
//   - Stop marker "▣" followed by role/model/timing (e.g. "▣  Build · Kimi K2.5 · 12.9s")
//   - Status bar at bottom with ctrl+p indicator
//
// Active signals (task still running):
//   - Running marker "▸" (spinning indicator)
//   - "Thinking:" blocks in output
//
// Error signals:
//   - Lines containing "error", "Error", "FATAL", "failed" (case-insensitive check)
//   - "permission denied", "command not found"
func (p *OpenCodeProvider) DetectTaskCompletion(session, role, paneContent string) (completed bool, errored bool, summary string) {
	if paneContent == "" {
		return false, false, ""
	}

	lines := strings.Split(paneContent, "\n")

	// Check for active signals first — if the agent is still working, don't report
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "▸") {
			return false, false, "" // still running
		}
	}

	// Look for the stop marker "▣" which indicates task completion
	var stopLine string
	hasStop := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "▣") {
			hasStop = true
			stopLine = trimmed
		}
	}

	if !hasStop {
		return false, false, "" // no completion signal
	}

	// Check for error indicators in the output above the stop marker
	// Error detection uses phrase patterns to reduce false positives.
	// "failed" alone would match "0 tests failed" (a success message).
	errorPatterns := []string{
		"error:", "error!", "ERROR:",
		"fatal:", "FATAL",
		"build failed", "compilation failed", "test failed",
		"permission denied", "command not found",
		"exit code 1", "exit code 2", "non-zero exit",
		"panic:", "segfault",
	}
	// Negative patterns: skip lines that look like success summaries
	successPatterns := []string{
		"0 failed", "0 errors", "no errors", "no issues",
		"all clean", "all passed", "succeeded",
	}
	var errorLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip the stop/status lines themselves
		if strings.Contains(trimmed, "▣") || strings.Contains(trimmed, "ctrl+p") {
			continue
		}
		lower := strings.ToLower(trimmed)
		// Skip lines that match success patterns
		isSuccess := false
		for _, sp := range successPatterns {
			if strings.Contains(lower, sp) {
				isSuccess = true
				break
			}
		}
		if isSuccess {
			continue
		}
		for _, pat := range errorPatterns {
			if strings.Contains(lower, pat) {
				errorLines = append(errorLines, trimmed)
				break
			}
		}
	}

	// Build summary from stop line (contains role, model, timing)
	summary = stopLine
	if summary == "" {
		summary = "Task completed"
	}

	// Extract the content between the injected prompt and the stop marker
	// to build a more useful summary for the requester
	var contentLines []string
	pastPrompt := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "▣") {
			break // stop at completion marker
		}
		// Skip empty lines at the start
		if !pastPrompt && trimmed == "" {
			continue
		}
		pastPrompt = true
		// Skip box-drawing / UI chrome
		if isUIChrome(trimmed) {
			continue
		}
		if trimmed != "" {
			contentLines = append(contentLines, trimmed)
		}
	}

	// Build a useful summary: last few content lines + stop line
	if len(contentLines) > 10 {
		contentLines = contentLines[len(contentLines)-10:]
	}
	var sb strings.Builder
	for _, cl := range contentLines {
		sb.WriteString(cl)
		sb.WriteString("\n")
	}
	sb.WriteString(stopLine)
	summary = sb.String()

	if len(errorLines) > 0 {
		return true, true, summary
	}
	return true, false, summary
}

// isUIChrome returns true if the line is OpenCode TUI decoration.
func isUIChrome(line string) bool {
	for _, ch := range []string{"─", "╭", "╰", "┌", "└", "╹", "╻"} {
		if strings.HasPrefix(line, ch) {
			return true
		}
	}
	// Status bar lines
	if strings.Contains(line, "ctrl+p") && strings.Contains(line, "commands") {
		return true
	}
	return false
}

// adaptBodyForNonHookProvider rewrites agent definition body text to replace
// Claude Code hook chain references with manual bus messaging instructions.
// The source agent definitions assume hooks auto-chain (build→test→review),
// but OpenCode and local LLM providers don't support hooks.
func adaptBodyForNonHookProvider(body, role string) string {
	replacements := map[string]map[string]string{
		"build": {
			// Replace "don't send test — hooks handle it" with "send test manually"
			"The bash hook automatically chains to the test agent — do NOT send a test request yourself.": "Your CLI does not support automatic hooks. After a successful build, send the test request manually:\n`muxcode send test test \"Build succeeded, run tests\" --type request`",
			"**Do NOT send a test request — the bash hook auto-chains build->test on success.**":          "**After a successful build, send a test request manually** (no auto-chain):\n`muxcode send test test \"Build succeeded, run tests\" --type request`",
			"the bash hook auto-chains build->test on success":                                            "send a test request manually after a successful build",
		},
		"test": {
			// Replace "don't send review — hooks handle it" with "send review manually"
			"**Do NOT send a review request — the bash hook auto-chains test->review on success.**": "**After tests pass, send a review request manually** (no auto-chain):\n`muxcode send review review \"Tests passed, review changes\" --type request`",
			"the bash hook auto-chains test->review on success":                                     "send a review request manually after tests pass",
		},
	}

	roleReplacements, ok := replacements[role]
	if !ok {
		return body
	}

	for old, new := range roleReplacements {
		body = strings.ReplaceAll(body, old, new)
	}
	return body
}

// --- Helpers ---

// roleFromPane extracts the role name from a tmux pane target string.
// Format: "session:window.pane" -> returns "window" (which is the role).
func roleFromPane(pane string) string {
	// Strip session prefix
	if idx := strings.Index(pane, ":"); idx >= 0 {
		pane = pane[idx+1:]
	}
	// Strip pane suffix
	if idx := strings.Index(pane, "."); idx >= 0 {
		pane = pane[:idx]
	}
	return pane
}
