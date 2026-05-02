package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ReloadMarkerPath returns the path to the reload marker file for a role.
// Written during reload to suppress daemon health checks; removed after
// successful launch verification.
func ReloadMarkerPath(session, role string) string {
	return filepath.Join(BusDir(session), "lock", role+".reloading")
}

// IsReloading returns true if a reload marker exists for the role.
func IsReloading(session, role string) bool {
	_, err := os.Stat(ReloadMarkerPath(session, role))
	return err == nil
}

// writeReloadMarker creates a reload marker file.
func writeReloadMarker(session, role string) error {
	dir := filepath.Join(BusDir(session), "lock")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	return os.WriteFile(ReloadMarkerPath(session, role), []byte("reloading"), 0644)
}

// clearReloadMarker removes the reload marker file.
func clearReloadMarker(session, role string) {
	_ = os.Remove(ReloadMarkerPath(session, role))
}

// ReloadTarget resolves the correct tmux pane target for a role,
// accounting for mode-cycled agents (edit↔auto on F2, plan↔research on F1).
//
// For active mode agents: targets the host window's agent pane
// (e.g. "session:edit.1" for the active edit mode).
//
// For inactive mode agents: targets the holding window's agent pane
// (e.g. "session:auto.1" for the inactive auto mode).
//
// For standard (non-mode-cycled) agents: uses PaneTarget.
func ReloadTarget(session, role string) string {
	// Check if role is in a mode cycle window (edit or plan)
	for _, window := range []string{"edit", "plan"} {
		state, err := ReadModeCycleState(session, window)
		if err != nil {
			continue
		}
		for _, agent := range state.Agents {
			if agent.Role == role {
				if agent.Index == state.Current {
					// Active — target the host window's agent pane
					return PaneTarget(session, window)
				}
				if agent.HoldWindow != "" {
					// Inactive — target the holding window's agent pane
					return session + ":" + agent.HoldWindow + ".1"
				}
			}
		}
	}
	// Standard agent — use normal pane target
	return PaneTarget(session, role)
}

// GracefulStop stops an agent process gracefully:
//  1. Optionally triggers context compaction before stopping (--compact flag)
//  2. Sends C-c to interrupt
//  3. Polls for process exit (500ms intervals, max 5s)
//  4. Force kills (second C-c) if still running
//
// Returns an error if the agent does not exit after ~6 seconds total.
func GracefulStop(session, role string, compact bool) error {
	target := ReloadTarget(session, role)
	provider := ResolveProvider(role)

	// Optional: trigger compact before stopping
	if compact {
		_ = provider.Compact(session, role, target)
		time.Sleep(2 * time.Second)
	}

	// Send C-c to interrupt
	exec.Command("tmux", "send-keys", "-t", target, "C-c").Run()

	// Poll for process exit (max 5s)
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if !IsAgentAlive(session, role) {
			return nil
		}
	}

	// Force kill if still running
	exec.Command("tmux", "send-keys", "-t", target, "C-c").Run()
	time.Sleep(1 * time.Second)

	if IsAgentAlive(session, role) {
		return fmt.Errorf("agent %s did not exit after 6 seconds", role)
	}
	return nil
}

// IsReloadMarkerStale returns true if a reload marker exists and is older
// than 60 seconds. Stale markers indicate a reload that crashed or timed out
// without cleaning up. The daemon calls this to auto-clean stale markers.
func IsReloadMarkerStale(session, role string) bool {
	info, err := os.Stat(ReloadMarkerPath(session, role))
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > 60*time.Second
}

// CleanStaleReloadMarkers removes reload marker files older than 60 seconds.
// Returns the number of markers cleaned.
func CleanStaleReloadMarkers(session string) int {
	cleaned := 0
	for _, role := range KnownRoles {
		if IsReloadMarkerStale(session, role) {
			clearReloadMarker(session, role)
			cleaned++
		}
	}
	return cleaned
}

// ReloadAll reloads every active agent sequentially with a 5-second gap
// between each. Skips:
//   - edit (interactive orchestrator — require explicit `muxcode reload edit`)
//   - hosted roles that share a window with their host (e.g. docs, pr-read)
//   - agents that are not alive (no point reloading a dead agent)
//
// Returns the count of successfully reloaded agents and any errors encountered.
func ReloadAll(session string, compact bool) (int, []error) {
	var errs []error
	reloaded := 0

	for _, role := range KnownRoles {
		// Skip edit — interactive session, require explicit reload
		if role == "edit" {
			continue
		}
		// Skip hosted roles that share a window
		if WindowForRole(role) != role {
			continue
		}
		// Skip dead agents
		if !IsAgentAlive(session, role) {
			continue
		}

		fmt.Printf("Reloading %s...\n", role)
		if err := ReloadAgent(session, role, "", "", compact); err != nil {
			fmt.Printf("  ✗ %s: %v\n", role, err)
			errs = append(errs, fmt.Errorf("%s: %w", role, err))
		} else {
			newCLI := ResolveProviderCLI(role)
			fmt.Printf("  ✓ %s reloaded (CLI: %s)\n", role, newCLI)
			reloaded++
		}

		// 5-second gap between reloads to avoid overwhelming the system
		time.Sleep(5 * time.Second)
	}

	return reloaded, errs
}

// ReloadAgent orchestrates the full stop→reconfigure→relaunch cycle:
//  1. Validate role exists
//  2. Write reload marker (suppresses daemon health checks)
//  3. Gracefully stop the agent (BEFORE writing overrides — so IsAgentAlive
//     resolves the correct provider for the still-running agent)
//  4. Write runtime overrides if --cli or --model specified
//  5. Load runtime overrides (sets env vars for provider resolution)
//  6. Regenerate provider config (WriteAgentConfig)
//  7. Restart console in left pane (if split-left window)
//  8. Relaunch agent via tmux send-keys
//  9. Poll for launch verification (500ms intervals, max 15s)
//  10. Clear reload marker
//  11. Log lifecycle event
//  12. Notify edit agent
//
// Note: stale reload markers (>60s) are cleaned up by the daemon in Phase 4.
func ReloadAgent(session, role, cli, model string, compact bool) error {
	// 1. Validate role exists
	if !IsKnownRole(role) {
		return fmt.Errorf("unknown role: %s", role)
	}

	// Capture old CLI for logging (before writing overrides)
	oldCLI := ResolveProviderCLI(role)

	// 2. Write reload marker
	if err := writeReloadMarker(session, role); err != nil {
		return fmt.Errorf("write reload marker: %w", err)
	}

	// 3. Gracefully stop the agent BEFORE writing overrides.
	// This is critical: GracefulStop → IsAgentAlive → ResolveProvider reads
	// the override file. If we wrote the new CLI override first, alive
	// detection would use the wrong provider (e.g. ClaudeCodeProvider checking
	// an OpenCode pane), causing incorrect results and failed stops.
	if err := GracefulStop(session, role, compact); err != nil {
		clearReloadMarker(session, role)
		return fmt.Errorf("stop agent: %w", err)
	}

	// 4. Write runtime overrides if specified (now safe — agent is stopped)
	if cli != "" {
		envKey := RoleCLIEnvVar(role)
		if err := WriteRuntimeOverride(session, role, envKey, cli); err != nil {
			clearReloadMarker(session, role)
			return fmt.Errorf("write CLI override: %w", err)
		}
	}
	if model != "" {
		// Use the generic MUXCODE_{ROLE}_MODEL env var. All provider model
		// resolution functions (resolveClaudeModel, resolveOpenCodeModel,
		// resolveCodexModel, RoleModel) check this generic var first before
		// falling through to provider-specific vars.
		envKey := RoleModelEnvVar(role)
		if err := WriteRuntimeOverride(session, role, envKey, model); err != nil {
			clearReloadMarker(session, role)
			return fmt.Errorf("write model override: %w", err)
		}
	}

	// 5. Load runtime overrides (sets env vars so provider/model resolution
	//    sees the new values, e.g. WriteAgentConfig regenerates for the new CLI)
	_ = LoadRuntimeOverrides(session, role)

	// 6. Regenerate provider config (.opencode/agents/ or .codex/AGENTS.md)
	provider := ResolveProvider(role)
	if err := provider.WriteAgentConfig(role); err != nil {
		clearReloadMarker(session, role)
		return fmt.Errorf("write agent config: %w", err)
	}

	// 7. Restart console in left pane if this is a split-left window.
	// The console process may have stale state or the provider change
	// may affect console rendering.
	window := WindowForRole(role)
	if IsSplitLeft(window) && HasConsoleView(window) {
		restartConsole(session, window)
	}

	// 8. Relaunch agent via tmux send-keys
	target := ReloadTarget(session, role)
	launchCmd := fmt.Sprintf("muxcode agent launch %s", role)
	if err := exec.Command("tmux", "send-keys", "-t", target, launchCmd, "Enter").Run(); err != nil {
		clearReloadMarker(session, role)
		return fmt.Errorf("relaunch agent: %w", err)
	}

	// 9. Poll for launch verification (max 15s)
	alive := false
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		if IsAgentAlive(session, role) {
			alive = true
			break
		}
	}

	// Extra grace period for the agent to fully initialize
	if alive {
		time.Sleep(1 * time.Second)
	}

	// 10. Clear reload marker
	clearReloadMarker(session, role)

	if !alive {
		return fmt.Errorf("agent %s did not come alive within 15 seconds", role)
	}

	// 11. Log lifecycle event
	newCLI := ResolveProviderCLI(role)
	LogLifecycle(session, "info", "user", "agent-reload",
		fmt.Sprintf("%s: %s→%s, model: %s", role, oldCLI, newCLI, model))

	// 12. Notify edit agent
	msg := NewMessage("daemon", "edit", "event", "agent-reloaded",
		fmt.Sprintf("Agent %s reloaded: CLI=%s, Model=%s", role, newCLI, model), "")
	_ = Send(session, msg)

	return nil
}

// restartConsole kills the existing console process in the left pane of a
// split-left window and relaunches it. This ensures a clean console state
// after an agent reload (e.g. provider change may affect rendering).
func restartConsole(session, window string) {
	leftPane := session + ":" + window + ".0"
	// Send C-c to stop the current console process
	exec.Command("tmux", "send-keys", "-t", leftPane, "C-c").Run()
	time.Sleep(500 * time.Millisecond)
	// Relaunch the console
	consoleCmd := fmt.Sprintf("muxcode console %s", window)
	exec.Command("tmux", "send-keys", "-t", leftPane, consoleCmd, "Enter").Run()
}
