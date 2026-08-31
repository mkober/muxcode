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
// For mode-cycled agents (active or inactive): targets the hold
// window's agent pane, resolved by identity.
//
// For standard (non-mode-cycled) agents: uses PaneTarget.
func ReloadTarget(session, role string) string {
	// A mode-cycled agent lives in its hold window whether or not that mode is
	// showing. modeSwitchTo swaps with swap-window, which exchanges window
	// *indices* while every agent keeps its own panes — the reason bus targeting
	// is by window name. Resolving an active mode role to the host window
	// instead therefore addresses whichever agent occupies the host, not the one
	// being reloaded: reloading research sent /exit to plan, killing plan and
	// leaving research untouched, and the reload then timed out waiting for a
	// process it never signalled ("did not exit after 12 seconds").
	//
	// Hold window rather than PaneTarget(role) because the two can differ — a
	// mode agent may declare a hold window that is not its role name.
	for _, window := range []string{"edit", "plan"} {
		state, err := ReadModeCycleState(session, window)
		if err != nil {
			continue
		}
		for _, agent := range state.Agents {
			if agent.Role == role && agent.HoldWindow != "" {
				return PaneTargetForWindow(session, agent.HoldWindow, PaneTagAgent)
			}
		}
	}
	// Host-mode roles (no hold window) and standard agents live in the window
	// that carries their own name.
	return PaneTarget(session, role)
}

// paneAwaitingExitConfirmation reports whether the pane is currently showing
// Claude Code's exit-confirmation dialog. A capture failure reads as "no
// dialog": guessing yes would fire a blind Enter into a pane we cannot see.
func paneAwaitingExitConfirmation(target string) bool {
	out, err := TmuxOutput("capture-pane", "-t", target, "-p", "-S", "-12")
	if err != nil {
		return false
	}
	return PaneShowsExitConfirmation(out)
}

// pairedInterrupt sends C-c twice in quick succession.
//
// One press is not an exit. OpenCode's TUI treats the first C-c as "press
// again to exit" and expires that prompt after a few seconds, so a lone press
// only cancels the current turn. Sending the second press ~10s later — as the
// stop sequence used to, via its fallback — lands after the prompt has expired
// and merely opens a new confirmation, so the agent never exits and every
// reload of an OpenCode agent failed with "did not exit after 12 seconds".
//
// The two presses are kept in one helper so a caller cannot fix the pairing at
// one call site while the other quietly regrows the bug.
func pairedInterrupt(target string) {
	exec.Command("tmux", "send-keys", "-t", target, "C-c").Run()
	time.Sleep(interruptPairDelay)
	exec.Command("tmux", "send-keys", "-t", target, "C-c").Run()
}

// interruptPairDelay is short enough to land inside OpenCode's confirmation
// window and long enough that the TUI processes the presses as two keys.
const interruptPairDelay = 300 * time.Millisecond

// GracefulStop stops an agent process gracefully:
//  1. Optionally triggers context compaction before stopping (--compact flag)
//  2. Sends provider-specific exit sequence:
//     - Claude Code: Escape (cancel input) → /exit + Enter (clean exit command)
//     - OpenCode/Codex/Local: C-c to interrupt
//  3. Polls for process exit (500ms intervals, max 10s), answering Claude
//     Code's "background shells are still running" confirmation dialog with
//     Enter whenever it is on screen — without this, an agent running the
//     `muxcode inbox --poll --loop` self-poll listener can never exit
//  4. Falls back to C-c if provider-specific exit didn't work
//  5. Force kills (second C-c) if still running
//
// Returns an error if the agent does not exit after ~12 seconds total.
func GracefulStop(session, role string, compact bool) error {
	target := ReloadTarget(session, role)
	provider := ResolveProvider(role)

	// Optional: trigger compact before stopping
	if compact {
		_ = provider.Compact(session, role, target)
		time.Sleep(2 * time.Second)
	}

	// Provider-specific exit sequence
	if provider.SupportsHooks() {
		// Claude Code: /exit is the clean exit command.
		// First Escape to cancel any pending input, then /exit + Enter.
		// send-keys text and Enter must be separate calls with a delay
		// to avoid Claude Code's TUI dropping the Enter key.
		exec.Command("tmux", "send-keys", "-t", target, "Escape").Run()
		time.Sleep(200 * time.Millisecond)
		exec.Command("tmux", "send-keys", "-t", target, "C-u").Run()
		time.Sleep(100 * time.Millisecond)
		exec.Command("tmux", "send-keys", "-t", target, "/exit").Run()
		time.Sleep(200 * time.Millisecond)
		exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
	} else {
		// OpenCode, Codex CLI, Local LLM: C-c interrupts and exits.
		pairedInterrupt(target)
	}

	// Poll for process exit (max 10s)
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		if !IsAgentAlive(session, role) {
			return nil
		}
		// Claude Code refuses to exit silently while background shells are
		// running — it raises a confirmation dialog listing them. Since
		// receipt-based delivery went default-ON every Claude agent runs
		// `muxcode inbox --poll --loop`, so this dialog appears on EVERY
		// reload and nothing was answering it.
		//
		// Re-detecting each iteration is the guard against sending a stray
		// Enter: the key goes out only while the dialog is actually on screen,
		// which also retries for free if the first Enter is dropped (Claude
		// Code's TUI does drop keys that arrive in the same pty write as
		// preceding text — the reason /exit and Enter are separate calls
		// above).
		if provider.SupportsHooks() && paneAwaitingExitConfirmation(target) {
			exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
		}
	}

	// Fallback: interrupt again in case the provider-specific exit didn't work.
	pairedInterrupt(target)
	time.Sleep(1 * time.Second)

	if !IsAgentAlive(session, role) {
		return nil
	}

	// Last resort: interrupt once more before giving up.
	pairedInterrupt(target)
	time.Sleep(1 * time.Second)

	if IsAgentAlive(session, role) {
		return fmt.Errorf("agent %s did not exit after 12 seconds", role)
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

// ReloadAll reloads every active agent sequentially with a gap between each.
// Accepts optional CLI/model overrides and a provider filter.
//
// Parameters:
//   - cli, model: override the provider/model for each reloaded agent (empty = keep current)
//   - providerFilter: only reload agents currently on this CLI (empty = reload all)
//
// Skips:
//   - edit/auto (interactive orchestrator — require explicit reload)
//   - hosted roles that share a window with their host (e.g. docs, pr-read)
//   - agents that are not alive (no point reloading a dead agent)
//
// Returns the count of successfully reloaded agents and any errors encountered.
func ReloadAll(session, cli, model, providerFilter string, compact bool) (int, []error) {
	var roles []string
	for _, role := range ReloadableRoles() {
		// Skip orchestrator roles — require explicit reload
		if role == "edit" || role == "auto" {
			continue
		}
		// Skip dead agents
		if !IsAgentAlive(session, role) {
			continue
		}
		// Apply provider filter
		if providerFilter != "" && ResolveProviderCLI(role) != providerFilter {
			continue
		}
		roles = append(roles, role)
	}

	results := ReloadBatch(session, roles, cli, model, compact, func(i int, r ReloadResult) {
		if r.Success {
			fmt.Printf("  ✓ %-10s %s → %s  (%s)\n", r.Role, r.OldCLI, r.NewCLI, r.Duration.Round(time.Second))
		} else {
			fmt.Printf("  ✗ %-10s %v\n", r.Role, r.Error)
		}
	})

	var errs []error
	reloaded := 0
	for _, r := range results {
		if r.Success {
			reloaded++
		} else {
			errs = append(errs, fmt.Errorf("%s: %w", r.Role, r.Error))
		}
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

	// 10a. Reset notified IDs marker so the daemon re-notifies the new agent
	// about any pending inbox messages. Without this, the marker retains the
	// pre-reload notification state and alreadyNotified() suppresses wake-up.
	ClearNotifiedIDs(session, role)

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

	// 13. Wake new agent if it has pending inbox messages.
	// Must wait for the agent to reach the idle prompt (❯) before injecting
	// the wake-up — mirrors the startup wake-up logic in LaunchSession(). Calling
	// Notify() while the agent is still initializing takes the displayMessage
	// path (status bar flash, invisible to the agent) which calls markNotified(),
	// poisoning the dedup state so the daemon's checkIdleAgents → notifySendKeys
	// is suppressed for 30s (notifyRetryInterval).
	if HasActionableMessages(session, role) {
		wakeAfterReload(session, role)
	}

	return nil
}

// wakeAfterReload waits for an agent to reach the idle prompt after a hot
// reload, then injects a wake-up via send-keys. This mirrors the startup
// wake-up logic in LaunchSession() (which waits for ❯ then sends "You have
// new messages") but runs from within the Go reload path.
//
// The naive approach — calling Notify() right after the agent is alive —
// fails because the agent is not yet idle (still initializing). Notify falls
// to notifyDisplayMessage() (status bar flash, invisible to the agent) which
// calls markNotified(), poisoning the dedup state. The daemon's subsequent
// checkIdleAgents → Notify → notifySendKeys is then suppressed by
// alreadyNotified() for 30 seconds (notifyRetryInterval).
//
// Polls for idle every 500ms for up to 15 seconds using both the standard
// 8-line IsAgentIdle check AND a wider full-pane capture via PaneHasIdlePrompt.
// The wider capture catches the ❯ prompt when Claude Code's status bar overlay
// (e.g. "⏵⏵ bypass permissions on") sits below the prompt, pushing it beyond
// the 8-line window.
//
// Always sends the wake-up after polling (detected idle or timeout). If the
// agent isn't at ❯ yet, the text buffers in the PTY and gets processed when
// the agent reaches the prompt.
func wakeAfterReload(session, role string) {
	provider := ResolveProvider(role)

	// Non-hook providers (OpenCode, Codex) can't be reliably detected as idle.
	// Send wake-up immediately — their SendWakeUp handles injection directly.
	if !provider.SupportsHooks() {
		time.Sleep(2 * time.Second)
		ClearNotifiedIDs(session, role)
		_ = provider.SendWakeUp(session, role, false)
		return
	}

	// Hook providers (Claude Code): wait for idle prompt before injecting.
	// Use both standard 8-line check and wider full-pane capture.
	target := PaneTarget(session, role)
	for i := 0; i < 30; i++ { // 15 seconds at 500ms intervals
		time.Sleep(500 * time.Millisecond)
		// Fast path: standard 8-line idle detection
		if IsAgentIdle(session, role) {
			break
		}
		// Fallback: wider capture catches ❯ behind status bar overlays
		if content, err := TmuxCapturePaneLines(target, 200); err == nil {
			if PaneHasIdlePrompt(content) {
				break
			}
		}
	}

	// Always send wake-up — handles both detected-idle and timeout.
	// On timeout, the text buffers in the PTY and gets processed when the
	// agent reaches ❯. Harmless if agent already processed its inbox.
	ClearNotifiedIDs(session, role)
	unnotified := UnnotifiedMessages(session, role)
	text := BuildCombinedNotification(unnotified)
	if len(unnotified) > 0 {
		ids := make([]string, 0, len(unnotified))
		for _, m := range unnotified {
			ids = append(ids, m.ID)
		}
		AddNotifiedIDs(session, role, ids)
	}
	// Clear any stale input before injecting
	if HasPendingInput(session, role) {
		_ = TmuxClearInput(target)
		time.Sleep(100 * time.Millisecond)
	}
	_ = SendWakeUpWithText(session, role, provider, text, false)
}

// restartConsole kills the existing console process in the left pane of a
// split-left window and relaunches it. This ensures a clean console state
// after an agent reload (e.g. provider change may affect rendering).
func restartConsole(session, window string) {
	leftPane := PaneTargetForWindow(session, window, PaneTagLeft)
	// Send C-c to stop the current console process
	exec.Command("tmux", "send-keys", "-t", leftPane, "C-c").Run()
	time.Sleep(500 * time.Millisecond)
	// Relaunch the console
	consoleCmd := fmt.Sprintf("muxcode console %s", window)
	exec.Command("tmux", "send-keys", "-t", leftPane, consoleCmd, "Enter").Run()
}
