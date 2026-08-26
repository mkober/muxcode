package bus

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrInjectionSkipped reports that a wake-up injection was intentionally
// not performed — e.g. an in-flight task suggests the message was already
// injected on a prior wake-up. It exists because a skip that returns nil
// is indistinguishable from a delivery: the receipt-gap backstop once
// recorded phantom re-drives against a wedged agent and left it stuck for
// ~20 minutes (the 2026-08-26 incident behind MUX-105). Callers must
// never read a skip as success; match with errors.Is.
var ErrInjectionSkipped = errors.New("wake-up injection skipped")

// shortID abbreviates an id for log lines without panicking on ids
// shorter than the display width.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

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
	// force bypasses provider-side suppression guards (the non-hook
	// in-flight-task skip) — recovery paths use it so a stuck request can
	// never block its own re-delivery (MUX-105); routine wake-ups pass
	// false. A skipped injection returns ErrInjectionSkipped, never nil.
	SendWakeUp(session, role string, force bool) error

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

	// DetectTaskCompletion analyzes captured pane content to determine if
	// the agent has finished processing a task. Returns:
	//   completed: true if the agent appears idle after working
	//   errored:   true if error indicators were detected
	//   summary:   human-readable description of what was detected
	// Only meaningful for non-hook providers (OpenCode, local LLM).
	// Hook providers return (false, false, "") since they report via hooks.
	DetectTaskCompletion(session, role, paneContent string) (completed bool, errored bool, summary string)
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
	case "codex":
		return &CodexProvider{}
	case "local":
		return &LocalProvider{}
	default:
		return &ClaudeCodeProvider{}
	}
}

// ResolveProviderCLI returns the CLI identifier for a role without
// constructing a full Provider. Used for logging and configuration.
// Resolution: runtime override → per-role env → global env → role default.
// Command-execution roles (build, test, deploy, run, watch, commit)
// default to "opencode" (MiniMax M2.5 Free via OpenCode Zen).
//
// This function reads runtime overrides without mutating process env vars
// (no os.Setenv). Callers that need env-side-effects for downstream model
// resolution (ResolveLaunchConfig, ReloadAgent) call LoadRuntimeOverrides
// separately.
func ResolveProviderCLI(role string) string {
	// Check runtime overrides first (highest priority).
	// Read the override file directly — don't call os.Setenv, which would
	// pollute the process environment and break concurrent test isolation.
	cliKey := RoleCLIEnvVar(role)
	if session := BusSession(); session != "" {
		if overrides, err := ReadRuntimeOverrides(session, role); err == nil && overrides != nil {
			if v, ok := overrides[cliKey]; ok && v != "" {
				return v
			}
		}
	}

	cli := os.Getenv(cliKey)

	if cli == "" {
		cli = os.Getenv("MUXCODE_AGENT_CLI")
	}
	if cli == "" {
		cli = roleDefaultCLI(role)
	}
	return cli
}

// roleDefaultCLI returns the built-in default CLI for a role.
// Command-execution roles default to OpenCode (MiniMax M2.5 Free).
// All other roles default to Claude Code (hook support, orchestration).
func roleDefaultCLI(role string) string {
	switch role {
	case "build", "test", "deploy", "run", "runner", "watch", "commit", "git", "research", "serve":
		return "opencode"
	default:
		return "claude"
	}
}

// chainInstructionForRole returns an additional SendWakeUp prompt suffix
// for roles that participate in event chains (e.g. build→test→review).
// Non-hook providers (OpenCode, Codex) don't have PostToolUse bash hooks,
// so the chain must be triggered explicitly via the injected prompt.
// Delegates to buildChainInstruction() using the global config.
func chainInstructionForRole(role string) string {
	return buildChainInstruction(role, Config())
}

// buildChainInstruction generates a natural-language chain instruction for
// a role by reading EventChains config. Returns "" if the role has no chain
// responsibilities. The role is the event source (e.g. "build" owns the
// "build" event chain).
func buildChainInstruction(role string, cfg *MuxcodeConfig) string {
	if cfg == nil {
		return ""
	}
	chain, ok := cfg.EventChains[role]
	if !ok {
		return ""
	}

	var parts []string

	if inst := describeOutcome("SUCCESS", chain.OnSuccess); inst != "" {
		parts = append(parts, inst)
	}
	if inst := describeOutcome("FAILURE", chain.OnFailure); inst != "" {
		parts = append(parts, inst)
	}

	if len(parts) == 0 {
		return ""
	}

	return " — ALSO after your task completes, you MUST trigger the chain: " + strings.Join(parts, "; ")
}

// describeOutcome generates a natural-language instruction for one outcome's
// action list. Returns "" if there are no actions to describe.
func describeOutcome(outcome string, actions ChainActions) string {
	if len(actions) == 0 {
		return ""
	}

	// Filter out actions that just notify edit (events) — those are handled
	// by hooks/CC, not by the agent prompt.
	meaningful := filterMeaningfulActions(actions)
	if len(meaningful) == 0 {
		return ""
	}

	// Single unconditional action — simple instruction
	if len(meaningful) == 1 && len(meaningful[0].Conditions) == 0 {
		a := meaningful[0]
		return fmt.Sprintf("on %s, send: muxcode send %s %s %q --type %s",
			outcome, a.SendTo, a.Action, a.Message, actionType(a))
	}

	// Multiple actions or conditional actions — describe in order
	var descs []string
	for i, a := range meaningful {
		desc := describeAction(a, i == len(meaningful)-1)
		descs = append(descs, desc)
	}

	return fmt.Sprintf("on %s: %s", outcome, strings.Join(descs, "; "))
}

// describeAction generates a description of a single chain action,
// including its conditions if any.
func describeAction(a ChainAction, isLast bool) string {
	cmd := fmt.Sprintf("muxcode send %s %s %q --type %s",
		a.SendTo, a.Action, a.Message, actionType(a))

	if len(a.Conditions) == 0 {
		if isLast {
			return fmt.Sprintf("otherwise send: %s", cmd)
		}
		return fmt.Sprintf("send: %s", cmd)
	}

	condDesc := describeConditions(a.Conditions)
	return fmt.Sprintf("if %s, send: %s", condDesc, cmd)
}

// describeConditions generates a natural-language description of a conditions map.
func describeConditions(conditions map[string]any) string {
	var parts []string
	for key, val := range conditions {
		switch key {
		case "files_match":
			parts = append(parts, fmt.Sprintf("changed files match %q", val))
		case "files_not_match":
			parts = append(parts, fmt.Sprintf("no changed files match %q", val))
		case "branch_match":
			parts = append(parts, fmt.Sprintf("branch matches %q", val))
		case "branch_not_match":
			parts = append(parts, fmt.Sprintf("branch does not match %q", val))
		case "env_set":
			parts = append(parts, fmt.Sprintf("env var %v is set", val))
		case "env_equals":
			if m, ok := val.(map[string]any); ok {
				parts = append(parts, fmt.Sprintf("env var %v equals %q", m["name"], m["value"]))
			}
		case "output_contains":
			parts = append(parts, fmt.Sprintf("output contains %q", val))
		case "exit_code":
			parts = append(parts, fmt.Sprintf("exit code is %v", val))
		default:
			parts = append(parts, fmt.Sprintf("%s=%v", key, val))
		}
	}
	return strings.Join(parts, " AND ")
}

// filterMeaningfulActions returns actions that are not just edit notifications
// (event-type messages to edit are handled by hooks/CC, not agent prompts).
func filterMeaningfulActions(actions ChainActions) []ChainAction {
	var result []ChainAction
	for _, a := range actions {
		// Skip event notifications to edit — those are for the hook system
		if a.SendTo == "edit" && a.Type == "event" {
			continue
		}
		result = append(result, a)
	}
	return result
}

// actionType returns the action type, defaulting to "request" if empty.
func actionType(a ChainAction) string {
	if a.Type != "" {
		return a.Type
	}
	return "request"
}

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
func (p *LocalProvider) SendWakeUp(_, _ string, _ bool) error        { return nil }
func (p *LocalProvider) Compact(_, _, _ string) error                { return nil }
func (p *LocalProvider) SupportsHooks() bool                         { return false }
func (p *LocalProvider) IdlePromptChar() string                      { return "" }
func (p *LocalProvider) WriteAgentConfig(_ string) error             { return nil }
func (p *LocalProvider) DetectTaskCompletion(_, _, _ string) (bool, bool, string) {
	return false, false, ""
}
