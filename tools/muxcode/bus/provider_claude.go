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
	// When the agent is active, ❯ only appears as part of a longer line
	// (e.g. "❯ You have new messages") which won't match the exact check.
	for _, line := range lines {
		if strings.TrimSpace(line) == idlePromptChar {
			return true
		}
	}
	return false
}

// IsAlive checks whether the agent's tmux pane is running Claude Code.
//
// Detection heuristic (in order):
//  1. IsIdle sees ❯ → alive (idle Claude Code)
//  2. "muxcode-agent" or "claude" or "opencode" in capture → alive (starting up)
//  3. Last non-empty line ends with $ or % and no ❯ → dead (bare shell)
//  4. Default: assume alive if indeterminate
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

	// 2. Startup check — look for agent launcher or claude text
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "muxcode-agent") ||
			strings.Contains(trimmed, "claude") ||
			strings.Contains(trimmed, "opencode") {
			return true
		}
	}

	// 3. Shell prompt check — bare $ or % with no ❯ anywhere
	if isShellPrompt(lines) {
		return false
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
// via send-keys. Text and Enter are sent as separate tmux send-keys calls
// with a brief delay to avoid Claude Code's TUI dropping the Enter key.
func (p *ClaudeCodeProvider) SendWakeUp(session, role string) error {
	target := PaneTarget(session, role)
	// Send text first
	cmd := exec.Command("tmux", "send-keys", "-t", target, "You have new messages")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s failed: %v\n", role, err)
		return err
	}
	// Brief delay so Claude Code's TUI registers the text before Enter
	time.Sleep(100 * time.Millisecond)
	// Send Enter
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
