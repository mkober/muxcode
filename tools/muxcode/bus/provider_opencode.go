package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// SendWakeUp sends a display-message notification to the agent's pane.
// The TUI's input model doesn't support programmatic text injection,
// so display-message is the best-effort approach (user-visible flash).
func (p *OpenCodeProvider) SendWakeUp(session, role string) error {
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "display-message", "-t", target, "You have new messages")
	return cmd.Run()
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

	// Agent body from definition file
	if agentBody != "" {
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
