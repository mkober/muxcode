package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// agentHealthExcludedRoles lists roles that should never be auto-restarted.
// edit: user's interactive session.
// webhook: managed separately, not a tmux-based agent.
var agentHealthExcludedRoles = map[string]bool{
	"edit":    true,
	"webhook": true,
}

// AgentStoppedPath returns the marker file path that suppresses auto-restart
// for a role. Written by "agent-health --stop", cleared by "--start".
func AgentStoppedPath(session, role string) string {
	return filepath.Join(BusDir(session), "lock", role+".stopped")
}

// MarkAgentStopped writes a stopped marker to prevent auto-restart.
func MarkAgentStopped(session, role string) error {
	return os.WriteFile(AgentStoppedPath(session, role), []byte("stopped"), 0644)
}

// ClearAgentStopped removes the stopped marker, allowing auto-restart.
func ClearAgentStopped(session, role string) {
	_ = os.Remove(AgentStoppedPath(session, role))
}

// IsAgentStopped returns true if a stopped marker exists for the role.
func IsAgentStopped(session, role string) bool {
	_, err := os.Stat(AgentStoppedPath(session, role))
	return err == nil
}

// IsAgentHealthExcluded returns true if a role should be excluded from
// automatic health monitoring (never auto-restarted).
// Also returns true while a reload is in progress (reload marker exists),
// since the agent is intentionally down during the reload cycle.
func IsAgentHealthExcluded(session, role string) bool {
	if IsReloading(session, role) {
		return true
	}
	return agentHealthExcludedRoles[role]
}

// RoleHasWindow reports whether the tmux window backing a role appears in
// names, as returned by TmuxListWindowNames.
//
// This is the DEFINITE-liveness counterpart to IsAgentAlive. IsAgentAlive
// fail-safes to "alive" when a pane cannot be captured, which is indeterminate
// for a role that was never launched: a session without an "auto" window makes
// IsAgentAlive("auto") report alive even though no such agent exists. Callers
// that must not act on a phantom role (sending it work, alarming that it never
// consumed) need a signal that can actually say "no", which is this.
//
// Hosted and mode roles resolve to their host window via WindowForRole.
//
// Taking the window list as a parameter lets a caller sweeping many roles pay
// for one tmux call instead of one per role. A caller that could not read the
// list must treat that as indeterminate rather than "no windows exist" — see
// Daemon.roleHasWindow.
func RoleHasWindow(names []string, role string) bool {
	want := WindowForRole(role)
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// IsAgentAlive checks whether an agent's tmux pane is running an AI CLI
// session or a local LLM harness, as opposed to having crashed back to a
// bare shell prompt.
//
// Detection strategy:
//  1. Harness marker PID alive → alive (provider-independent catch-all)
//  2. Delegate to provider for CLI-specific alive detection
func IsAgentAlive(session, role string) bool {
	// 1. Harness check — provider-independent catch-all for local LLM agents
	if IsHarnessActive(session, role) {
		return true
	}

	// 2. Delegate to provider for CLI-specific alive detection
	provider := ResolveProvider(role)
	return provider.IsAlive(session, role)
}

// isShellPrompt returns true if the captured pane lines indicate a bare shell
// prompt (agent has exited). Checks that the last non-empty line ends with
// a known prompt suffix ('$', '%', '>', '->') and that no ❯ appears anywhere.
func isShellPrompt(lines []string) bool {
	hasPromptChar := false

	// Find last non-empty line
	lastNonEmpty := ""
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != "" {
			lastNonEmpty = trimmed
			break
		}
	}

	if lastNonEmpty == "" {
		return false
	}

	// Check for ❯ anywhere — if present, agent is alive
	for _, line := range lines {
		if strings.Contains(line, idlePromptChar) {
			return false
		}
	}

	// Check if last non-empty line ends with a known shell prompt suffix.
	// Common prompts: bash ($), zsh (%), custom arrow (->).
	// Also check for standalone ">" but only for short lines (≤10 chars)
	// to avoid false positives from command output ending with ">".
	if strings.HasSuffix(lastNonEmpty, "$") || strings.HasSuffix(lastNonEmpty, "%") {
		hasPromptChar = true
	} else if strings.HasSuffix(lastNonEmpty, "->") {
		hasPromptChar = true
	} else if strings.HasSuffix(lastNonEmpty, ">") && len(lastNonEmpty) <= 10 {
		hasPromptChar = true
	}

	return hasPromptChar
}

// FormatAgentHealthAlert formats an agent health alert message.
func FormatAgentHealthAlert(status, role, message string) string {
	var b strings.Builder
	switch status {
	case "down":
		b.WriteString(fmt.Sprintf("⚠ AGENT DOWN: %s\n", role))
	case "restarting":
		b.WriteString(fmt.Sprintf("🔄 AGENT RESTARTING: %s\n", role))
	case "recovered":
		b.WriteString(fmt.Sprintf("✅ AGENT RECOVERED: %s\n", role))
	default:
		b.WriteString(fmt.Sprintf("ℹ AGENT %s: %s\n", strings.ToUpper(status), role))
	}
	if message != "" {
		b.WriteString(fmt.Sprintf("  %s\n", message))
	}
	return b.String()
}

// AgentHealthAlertKey returns a dedup key for an agent health alert.
func AgentHealthAlertKey(role, status string) string {
	return fmt.Sprintf("agent:%s:%s", role, status)
}
