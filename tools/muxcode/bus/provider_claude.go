package bus

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ClaudeCodeProvider implements the Provider interface for Claude Code CLI.
type ClaudeCodeProvider struct{}

func (p *ClaudeCodeProvider) Name() string { return "claude" }

// ConfigureLaunch populates Claude Code-specific fields in the LaunchConfig:
// agent file resolution, model flags, permission flags, tool profiles, shared prompt.
func (p *ClaudeCodeProvider) ConfigureLaunch(cfg *LaunchConfig, role string) {
	// Agent file resolution
	agentName := AgentFileName(role)
	cfg.Agent = agentName
	installDir := resolveInstallDir()

	if agentName != "" {
		agentFile, tier := ResolveAgentFile(agentName, installDir)
		cfg.AgentFile = agentFile
		if tier == 1 {
			cfg.AgentName = agentName
		} else if tier >= 2 && agentFile != "" {
			data, err := os.ReadFile(agentFile)
			if err == nil {
				fm, body := ExtractFrontmatter(string(data))
				desc := fm.Description
				if desc == "" {
					desc = agentName
				}
				agentJSON, jsonErr := BuildAgentsJSON(agentName, desc, body)
				if jsonErr == nil {
					cfg.AgentName = agentName
					cfg.AgentJSON = agentJSON
				}
			}
		}
	}

	// Claude model selection
	model := resolveClaudeModel(role)
	if model != "" {
		cfg.ModelFlags = []string{"--model", model}
	}

	// Permission mode
	cfg.PermFlags = []string{"--dangerously-skip-permissions"}

	// Tool profiles
	tools := ResolveTools(role)
	for _, tool := range tools {
		cfg.ToolFlags = append(cfg.ToolFlags, "--allowedTools", tool)
	}

	// Shared prompt
	cfg.SharedPrompt = BuildSharedPrompt(role)
}

// BuildExecArgs constructs Claude Code CLI arguments.
func (p *ClaudeCodeProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) {
	var args []string

	// Agent selection
	if cfg.AgentName != "" {
		args = append(args, "--agent", cfg.AgentName)
		if cfg.AgentJSON != "" {
			args = append(args, "--agents", cfg.AgentJSON)
		}
	}

	// Model flags
	args = append(args, cfg.ModelFlags...)

	// Permission flags
	args = append(args, cfg.PermFlags...)

	// Tool flags
	args = append(args, cfg.ToolFlags...)

	// Shared prompt
	if cfg.SharedPrompt != "" {
		args = append(args, "--append-system-prompt", cfg.SharedPrompt)
	}

	// If no agent file found, use inline fallback prompt
	if cfg.AgentName == "" {
		prompt := InlineFallbackPrompt(cfg.Role)
		if prompt != "" {
			args = append(args, "--append-system-prompt", prompt)
		}
	}

	return cfg.CLI, args
}

// IsIdle returns true if the agent's tmux pane shows the Claude Code idle prompt (❯).
// Scans the last 8 lines because Claude Code renders decorative UI elements
// (borders, "? for shortcuts") below the ❯ prompt.
func (p *ClaudeCodeProvider) IsIdle(session, role string) bool {
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-8")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	lines := strings.Split(string(out), "\n")
	// Scan all lines — the ❯ prompt may not be the last non-empty line
	// due to Claude Code's decorative footer (borders, help text).
	// Accept lines that are exactly ❯ OR start with "❯ " (prompt with
	// stale text in the input buffer). An agent at "❯ push it" is still
	// idle — it's at the prompt with leftover text, not actively executing.
	// When the agent is truly active, ❯ appears mid-line in tool output
	// or status text, not as the line prefix after trimming.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == idlePromptChar || strings.HasPrefix(trimmed, idlePromptChar+" ") {
			return true
		}
	}
	return false
}

// IsAlive checks whether the agent's tmux pane is running Claude Code.
//
// Detection heuristic (in order):
//  1. IsIdle sees ❯ → alive (idle Claude Code)
//  2. Last non-empty line ends with shell prompt ($, %, ->, >) and no ❯ → dead
//  3. "muxcode agent launch" or "claude" or "opencode" in capture → alive (starting up)
//  4. Default: assume alive if indeterminate
//
// Shell prompt check (2) must come before startup text check (3) because
// Claude Code's exit message contains "claude" in "Resume this session
// with: claude --resume ..." which would false-positive the startup check.
func (p *ClaudeCodeProvider) IsAlive(session, role string) bool {
	// 1. Idle check (❯ present = Claude Code is running)
	if p.IsIdle(session, role) {
		return true
	}

	// Capture pane content for heuristic checks
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-5")
	out, err := cmd.Output()
	if err != nil {
		// Can't reach pane — assume alive (indeterminate)
		return true
	}

	lines := strings.Split(string(out), "\n")

	// 2. Shell prompt check — bare shell prompt with no ❯ → dead.
	// This must come BEFORE the startup text check because Claude Code's
	// exit message ("Resume this session with: claude --resume ...") contains
	// the word "claude", which would false-positive the startup check.
	if isShellPrompt(lines) {
		return false
	}

	// 3. Startup check — look for agent launcher or CLI text
	// Only reached if we're NOT at a shell prompt (agent is mid-startup).
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "muxcode agent launch") ||
			strings.Contains(trimmed, "claude") ||
			strings.Contains(trimmed, "opencode") {
			return true
		}
	}

	// 4. Default: assume alive
	return true
}

// ClassifyPane determines the startup state of a Claude Code agent pane.
func (p *ClaudeCodeProvider) ClassifyPane(content string) PaneState {
	if strings.Contains(content, "trust this folder") {
		return PaneTrustPrompt
	}
	if strings.Contains(content, "Bypass Permissions") {
		return PaneBypassPrompt
	}
	if strings.Contains(content, "❯") {
		return PaneIdle
	}
	return PaneNotReady
}

// AcceptStartup handles Claude Code startup prompts (trust folder, bypass permissions).
// Returns true if all startup prompts have been handled.
func (p *ClaudeCodeProvider) AcceptStartup(session, pane string, state PaneState) bool {
	switch state {
	case PaneTrustPrompt:
		// Trust prompt — default selection is correct, just confirm
		TmuxSendEnter(pane)
		return false // bypass prompt may follow
	case PaneBypassPrompt:
		// Bypass permissions — move to "Yes, I accept" and confirm
		TmuxSendKeys(pane, "Down")
		time.Sleep(200 * time.Millisecond)
		TmuxSendEnter(pane)
		return true
	case PaneIdle:
		return true
	default:
		return false
	}
}

// SendWakeUp injects "You have new messages" into the agent's tmux pane
// via send-keys. Uses -l (literal) flag for the text to avoid tmux
// interpreting special characters. Text and Enter are sent as separate
// calls with a 200ms delay to give the TUI time to register the text.
//
// No Escape/C-u preamble — stale buffer text is handled by
// verifySendKeysDelivery() retry. The multi-command preamble was the
// primary cause of dropped injections during TUI redraws.
func (p *ClaudeCodeProvider) SendWakeUp(session, role string) error {
	target := PaneTarget(session, role)
	// Send text with -l (literal) to avoid tmux key interpretation
	cmd := exec.Command("tmux", "send-keys", "-t", target, "-l", "You have new messages")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s failed: %v\n", role, err)
		return err
	}
	// 200ms delay gives Claude Code's TUI time to register the text
	// before the Enter keypress (increased from 100ms for reliability)
	time.Sleep(200 * time.Millisecond)
	// Send Enter separately (not literal — Enter is a tmux key name)
	cmd = exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s failed: %v\n", role, err)
		return err
	}
	return nil
}

// Compact triggers context compaction by injecting /compact into the pane.
// Waits for the agent to become idle first (up to 30 seconds).
func (p *ClaudeCodeProvider) Compact(session, role, target string) error {
	// Wait for agent to reach idle (❯ prompt), max 30 seconds
	idle := false
	for i := 0; i < 30; i++ {
		if p.IsIdle(session, role) {
			idle = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !idle {
		// Agent never became idle — skip silently
		return nil
	}

	// Clear any residual input
	_ = exec.Command("tmux", "send-keys", "-t", target, "Escape").Run()
	time.Sleep(100 * time.Millisecond)
	_ = exec.Command("tmux", "send-keys", "-t", target, "C-u").Run()
	time.Sleep(100 * time.Millisecond)

	// Inject /compact + Enter (separate calls per tmux send-keys convention)
	if err := exec.Command("tmux", "send-keys", "-t", target, "/compact").Run(); err != nil {
		return fmt.Errorf("send /compact: %w", err)
	}
	time.Sleep(200 * time.Millisecond)
	_ = exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
	return nil
}

func (p *ClaudeCodeProvider) SupportsHooks() bool             { return true }
func (p *ClaudeCodeProvider) IdlePromptChar() string          { return idlePromptChar }
func (p *ClaudeCodeProvider) WriteAgentConfig(_ string) error { return nil }

// DetectTaskCompletion is a no-op for Claude Code — hooks handle completion.
func (p *ClaudeCodeProvider) DetectTaskCompletion(_, _, _ string) (bool, bool, string) {
	return false, false, ""
}
