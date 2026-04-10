package bus

import (
	"fmt"
	"os"
	"os/exec"
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
func IsAgentHealthExcluded(role string) bool {
	return agentHealthExcludedRoles[role]
}

// IsAgentAlive checks whether an agent's tmux pane is running a Claude Code
// session or a local LLM harness, as opposed to having crashed back to a
// bare shell prompt.
//
// Detection heuristic (in order):
//  1. Harness marker PID alive → alive
//  2. IsAgentIdle sees ❯ → alive (idle Claude Code)
//  3. Last non-empty line ends with $ or % and no ❯ → dead (bare shell)
//  4. "muxcode-agent" or "claude" in capture → alive (starting up)
//  5. Default: assume alive if indeterminate
func IsAgentAlive(session, role string) bool {
	// 1. Harness check
	if IsHarnessActive(session, role) {
		return true
	}

	// 2. Idle check (❯ present = Claude Code is running)
	if IsAgentIdle(session, role) {
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

	// 4. Startup check — look for agent launcher or claude text
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "muxcode-agent") || strings.Contains(trimmed, "claude") || strings.Contains(trimmed, "opencode") {
			return true
		}
	}

	// 3. Shell prompt check — bare $ or % with no ❯ anywhere
	if isShellPrompt(lines) {
		return false
	}

	// 5. Default: assume alive
	return true
}

// isShellPrompt returns true if the captured pane lines indicate a bare shell
// prompt (agent has exited). Checks that the last non-empty line ends with
// '$' or '%' and that no ❯ appears anywhere.
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

	// Check if last non-empty line ends with $ or %
	if strings.HasSuffix(lastNonEmpty, "$") || strings.HasSuffix(lastNonEmpty, "%") {
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
