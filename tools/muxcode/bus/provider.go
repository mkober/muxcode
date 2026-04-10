package bus

import (
	"os"
	"os/exec"
	"strings"
)

// Provider abstracts the AI CLI backend used by an agent role.
// Each provider implements CLI-specific behavior for launching,
// idle detection, notifications, and lifecycle management.
type Provider interface {
	// Name returns the provider identifier ("claude", "opencode", "local").
	Name() string

	// ConfigureLaunch populates CLI-specific fields in the LaunchConfig.
	// Called by ResolveLaunchConfig after generic fields are set.
	ConfigureLaunch(cfg *LaunchConfig, role string)

	// BuildExecArgs constructs (binary, args) for launching the agent.
	BuildExecArgs(cfg *LaunchConfig) (string, []string)

	// IsIdle returns true if the agent pane shows an idle prompt.
	IsIdle(session, role string) bool

	// IsAlive returns true if the agent process is running in the pane.
	IsAlive(session, role string) bool

	// ClassifyPane determines the startup state of a pane from captured content.
	ClassifyPane(content string) PaneState

	// AcceptStartup handles a detected startup prompt in the pane.
	// Returns true if all startup prompts have been handled.
	AcceptStartup(session, pane string, state PaneState) bool

	// SendWakeUp injects a wake-up message into an idle agent's pane.
	SendWakeUp(session, role string) error

	// Compact triggers context compaction for the agent.
	Compact(session, role, target string) error

	// SupportsHooks returns true if the provider supports PreToolUse/PostToolUse hooks.
	SupportsHooks() bool

	// IdlePromptChar returns the character used to detect idle state.
	IdlePromptChar() string

	// WriteAgentConfig writes provider-specific agent configuration files.
	// For Claude Code: no-op (agent files discovered from .claude/agents/).
	// For OpenCode: writes .opencode/agents/<role>.md with frontmatter.
	WriteAgentConfig(role string) error
}

// ResolveProvider returns the appropriate Provider for a role based on
// environment variable configuration. Resolution order:
//  1. MUXCODE_{ROLE}_CLI per-role env var
//  2. MUXCODE_AGENT_CLI session-wide default
//  3. "claude" (built-in default)
func ResolveProvider(role string) Provider {
	cli := ResolveProviderCLI(role)
	switch cli {
	case "opencode":
		return &OpenCodeProvider{}
	case "local":
		return &LocalProvider{}
	default:
		return &ClaudeCodeProvider{}
	}
}

// ResolveProviderCLI returns the CLI identifier for a role without
// constructing a full Provider. Used for logging and configuration.
func ResolveProviderCLI(role string) string {
	cli := os.Getenv(RoleCLIEnvVar(role))

	// Beta role defaults to opencode when no per-role override is set
	if cli == "" && role == "beta" {
		return "opencode"
	}

	if cli == "" {
		cli = os.Getenv("MUXCODE_AGENT_CLI")
	}
	if cli == "" {
		cli = "claude"
	}
	return cli
}

// --- OpenCodeProvider (stub — full implementation in Phase 2) ---

// OpenCodeProvider implements the Provider interface for OpenCode CLI.
// Phase 1: minimal stub that launches the bare binary.
// Phase 2: server mode with HTTP API for messaging, idle detection, etc.
type OpenCodeProvider struct{}

func (p *OpenCodeProvider) Name() string                              { return "opencode" }
func (p *OpenCodeProvider) ConfigureLaunch(_ *LaunchConfig, _ string) {}
func (p *OpenCodeProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) {
	return cfg.CLI, nil
}
func (p *OpenCodeProvider) IsIdle(_, _ string) bool { return false }
func (p *OpenCodeProvider) IsAlive(session, role string) bool {
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-5")
	out, err := cmd.Output()
	if err != nil {
		return true // indeterminate → assume alive
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if strings.Contains(strings.TrimSpace(line), "opencode") {
			return true
		}
	}
	return !isShellPrompt(lines)
}
func (p *OpenCodeProvider) ClassifyPane(_ string) PaneState             { return PaneNotReady }
func (p *OpenCodeProvider) AcceptStartup(_, _ string, _ PaneState) bool { return false }
func (p *OpenCodeProvider) SendWakeUp(_, _ string) error                { return nil }
func (p *OpenCodeProvider) Compact(_, _, _ string) error                { return nil }
func (p *OpenCodeProvider) SupportsHooks() bool                         { return false }
func (p *OpenCodeProvider) IdlePromptChar() string                      { return "" }
func (p *OpenCodeProvider) WriteAgentConfig(_ string) error             { return nil }

// --- LocalProvider ---

// LocalProvider implements the Provider interface for local LLM harness.
type LocalProvider struct{}

func (p *LocalProvider) Name() string { return "local" }
func (p *LocalProvider) ConfigureLaunch(cfg *LaunchConfig, role string) {
	cfg.IsLocal = true
	cfg.HarnessArgs = buildHarnessArgs(role)
}
func (p *LocalProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) {
	binary := "muxcode"
	args := []string{"agent"}
	args = append(args, cfg.HarnessArgs...)
	if _, err := lookPath("muxcode-llm-harness"); err == nil {
		binary = "muxcode-llm-harness"
		args = cfg.HarnessArgs
	}
	return binary, args
}
func (p *LocalProvider) IsIdle(_, _ string) bool                     { return false }
func (p *LocalProvider) IsAlive(session, role string) bool           { return IsHarnessActive(session, role) }
func (p *LocalProvider) ClassifyPane(_ string) PaneState             { return PaneNotReady }
func (p *LocalProvider) AcceptStartup(_, _ string, _ PaneState) bool { return false }
func (p *LocalProvider) SendWakeUp(_, _ string) error                { return nil }
func (p *LocalProvider) Compact(_, _, _ string) error                { return nil }
func (p *LocalProvider) SupportsHooks() bool                         { return false }
func (p *LocalProvider) IdlePromptChar() string                      { return "" }
func (p *LocalProvider) WriteAgentConfig(_ string) error             { return nil }
