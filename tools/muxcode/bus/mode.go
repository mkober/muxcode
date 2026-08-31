package bus

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ModeAgent represents a registered agent in a window's mode cycle.
type ModeAgent struct {
	Index      int    `json:"index"`
	Mode       string `json:"mode"`
	Role       string `json:"role"`
	HoldWindow string `json:"hold_window"`
}

// ModeCycleState holds the current cycle state for a window's modes.
type ModeCycleState struct {
	Window  string      `json:"window"`
	Current int         `json:"current"`
	Agents  []ModeAgent `json:"agents"`
}

// ModeCyclePath returns the path to the mode cycle state file for a window.
func ModeCyclePath(session, window string) string {
	return filepath.Join(BusDir(session), "mode-cycle-"+window+".json")
}

// DefaultModeCycleState returns the initial cycle state for the edit window
// with edit and auto modes registered.
func DefaultModeCycleState() *ModeCycleState {
	return &ModeCycleState{
		Window:  "edit",
		Current: 0,
		Agents: []ModeAgent{
			{Index: 0, Mode: "edit", Role: "edit", HoldWindow: ""},
			{Index: 1, Mode: "auto", Role: "auto", HoldWindow: "auto"},
		},
	}
}

// DefaultPlanModeCycleState returns the initial cycle state for the plan window
// with plan and research modes registered.
func DefaultPlanModeCycleState() *ModeCycleState {
	return &ModeCycleState{
		Window:  "plan",
		Current: 0,
		Agents: []ModeAgent{
			{Index: 0, Mode: "plan", Role: "plan", HoldWindow: ""},
			{Index: 1, Mode: "research", Role: "research", HoldWindow: "research"},
		},
	}
}

// ReadModeCycleState reads the cycle state from disk.
// Returns the default state if the file doesn't exist and window is "edit" or "plan".
func ReadModeCycleState(session, window string) (*ModeCycleState, error) {
	data, err := os.ReadFile(ModeCyclePath(session, window))
	if err != nil {
		if os.IsNotExist(err) {
			switch window {
			case "edit":
				return DefaultModeCycleState(), nil
			case "plan":
				return DefaultPlanModeCycleState(), nil
			default:
				return nil, fmt.Errorf("no mode cycle for window %q", window)
			}
		}
		return nil, fmt.Errorf("read mode cycle state: %w", err)
	}
	var state ModeCycleState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse mode cycle state: %w", err)
	}
	if len(state.Agents) == 0 {
		switch window {
		case "edit":
			return DefaultModeCycleState(), nil
		case "plan":
			return DefaultPlanModeCycleState(), nil
		default:
			return nil, fmt.Errorf("empty mode cycle for window %q", window)
		}
	}
	return &state, nil
}

// WriteModeCycleState writes the cycle state to disk.
func WriteModeCycleState(session string, state *ModeCycleState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mode cycle state: %w", err)
	}
	dir := filepath.Dir(ModeCyclePath(session, state.Window))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	return os.WriteFile(ModeCyclePath(session, state.Window), append(data, '\n'), 0o644)
}

// NextModeIndex computes the next cycle index (wraps around).
func NextModeIndex(current, count int) int {
	if count <= 0 {
		return 0
	}
	return (current + 1) % count
}

// FindModeAgent finds an agent by mode name. Returns nil if not found.
func FindModeAgent(state *ModeCycleState, mode string) *ModeAgent {
	for i := range state.Agents {
		if state.Agents[i].Mode == mode {
			return &state.Agents[i]
		}
	}
	return nil
}

// CurrentModeAgent returns the currently active agent.
func CurrentModeAgent(state *ModeCycleState) *ModeAgent {
	if state.Current >= 0 && state.Current < len(state.Agents) {
		return &state.Agents[state.Current]
	}
	return nil
}

// ActiveModeRole returns the currently active role for a window.
// Returns the role name (e.g. "edit", "auto", "plan", "research").
func ActiveModeRole(session, window string) (string, error) {
	state, err := ReadModeCycleState(session, window)
	if err != nil {
		return "", err
	}
	agent := CurrentModeAgent(state)
	if agent == nil {
		return "", fmt.Errorf("no active agent for window %q", window)
	}
	return agent.Role, nil
}

// ModeCycle performs the pane swap cycle to the next registered agent.
// It swaps panes between the host window and holding windows using tmux.
func ModeCycle(session, window string) error {
	state, err := ReadModeCycleState(session, window)
	if err != nil {
		return err
	}
	if len(state.Agents) < 2 {
		return fmt.Errorf("need at least 2 modes to cycle (have %d)", len(state.Agents))
	}

	nextIdx := NextModeIndex(state.Current, len(state.Agents))
	return modeSwitchTo(session, state, nextIdx)
}

// ModeSwitch switches directly to a named mode within a window.
func ModeSwitch(session, window, mode string) error {
	state, err := ReadModeCycleState(session, window)
	if err != nil {
		return err
	}

	target := FindModeAgent(state, mode)
	if target == nil {
		return fmt.Errorf("unknown mode: %s", mode)
	}
	if target.Index == state.Current {
		return nil // already active
	}
	return modeSwitchTo(session, state, target.Index)
}

// modeSwitchTo performs a window swap from current to targetIdx.
// Uses swap-window instead of swap-pane so each window keeps its own panes
// intact. This preserves bus targeting (panes are resolved within their
// window by identity) and avoids layout/size mismatches between windows.
//
// The swap and focus selection are batched into a single tmux command chain
// (one server event loop iteration, one redraw). Per-window format overrides
// are applied as separate commands afterward — tmux's chain parser mishandles
// empty-string values and the -u (unset) flag when mixed with ";" separators.
func modeSwitchTo(session string, state *ModeCycleState, targetIdx int) error {
	current := &state.Agents[state.Current]
	target := &state.Agents[targetIdx]
	hostWindow := state.Window

	// Guard: if the state says we're on a non-default agent but its hold window
	// is missing (e.g. stale state from a previous session), auto-correct to
	// index 0. The host window is at the host index when no hold windows exist.
	if current.HoldWindow != "" && !tmuxWindowExists(session, current.HoldWindow) {
		state.Current = 0
		current = &state.Agents[0]
		// If we were already targeting the corrected index, nothing to do.
		if targetIdx == 0 {
			return WriteModeCycleState(session, state)
		}
	}

	// Ensure the target's holding window exists.
	// On first switch to a non-default agent, create the holding window
	// and launch the agent.
	if target.HoldWindow != "" {
		if !tmuxWindowExists(session, target.HoldWindow) {
			if err := modeCreateAgent(session, target); err != nil {
				return fmt.Errorf("create agent %s: %w", target.Mode, err)
			}
		}
	}

	// Determine which hold window to swap with the host.
	// Switching TO a non-default agent: use target's hold window.
	// Switching FROM a non-default agent (back to default): use current's hold window.
	// Either way it's the same swap-window command — they just exchange indices.
	holdWindow := ""
	if target.HoldWindow != "" {
		holdWindow = target.HoldWindow
	} else if current.HoldWindow != "" {
		holdWindow = current.HoldWindow
	}

	if holdWindow != "" {
		// Batch swap + select into one tmux call (single redraw, no flashing).
		args := modeSwapArgs(session, hostWindow, holdWindow, target)
		if err := tmuxRunErr(args...); err != nil {
			return fmt.Errorf("mode swap %s <-> %s: %w", hostWindow, holdWindow, err)
		}

		// Update per-window format overrides to hide index 0.
		// Done as separate calls — tmux's chain parser mishandles empty-string
		// values and -u (unset) when batched with ";" separators.
		modeUpdateFormats(session, hostWindow, holdWindow, target)
	}

	state.Current = targetIdx
	return WriteModeCycleState(session, state)
}

// modeSwapArgs builds a batched tmux command that performs the window swap
// and focus selection in a single execution.
func modeSwapArgs(session, hostWindow, holdWindow string, target *ModeAgent) []string {
	src := session + ":" + hostWindow
	dst := session + ":" + holdWindow

	// After swap-window, tmux keeps the client focused on the same window
	// object (not the same index). Select the target so the user sees the
	// right window at the host position.
	selectWindow := hostWindow
	if target.HoldWindow != "" {
		selectWindow = target.HoldWindow
	}

	return []string{
		"swap-window", "-s", src, "-t", dst, ";",
		"select-window", "-t", session + ":" + selectWindow,
	}
}

// modeUpdateFormats applies per-window format overrides after a swap.
// The window displaced to index 0 gets an empty format (hidden), while the
// window coming from index 0 has its per-window override removed (falling
// back to the global format, making it visible).
//
// Per-window overrides follow the window object across swap-window — they
// are keyed on the window, not the index position.
func modeUpdateFormats(session, hostWindow, holdWindow string, target *ModeAgent) {
	// Forward (switching to non-default mode): hostWindow displaces to 0.
	// Reverse (switching back to default): holdWindow returns to 0.
	var toHide, toShow string
	if target.HoldWindow != "" {
		toHide = hostWindow
		toShow = holdWindow
	} else {
		toHide = holdWindow
		toShow = hostWindow
	}

	// Hide the window now at index 0 (empty format = no tab shown).
	tmuxRun("set-option", "-w", "-t", session+":"+toHide,
		"window-status-format", "")
	tmuxRun("set-option", "-w", "-t", session+":"+toHide,
		"window-status-current-format", "")

	// Restore the window coming from index 0 — remove the per-window
	// override so it falls back to the global format.
	tmuxRun("set-option", "-w", "-u", "-t", session+":"+toShow,
		"window-status-format")
	tmuxRun("set-option", "-w", "-u", "-t", session+":"+toShow,
		"window-status-current-format")
}

// modeCreateAgent creates the holding window and launches the agent for first use.
func modeCreateAgent(session string, agent *ModeAgent) error {
	// Get the project directory from an existing pane so the new window
	// starts in the correct working directory for agent launch.
	projectDir := modeProjectDir(session)

	// Create the holding window at index 0 (before the F1-F10 range).
	// This keeps the hold window hidden from the F-key status bar when
	// swap-window displaces the host window to index 0.
	// Fall back to next available index if 0 is occupied.
	newArgs := []string{"new-window", "-d", "-t", session + ":0", "-n", agent.HoldWindow}
	if projectDir != "" {
		newArgs = append(newArgs, "-c", projectDir)
	}
	if err := tmuxRunErr(newArgs...); err != nil {
		// Index 0 occupied — let tmux pick the next available index.
		fallbackArgs := []string{"new-window", "-d", "-t", session, "-n", agent.HoldWindow}
		if projectDir != "" {
			fallbackArgs = append(fallbackArgs, "-c", projectDir)
		}
		tmuxRun(fallbackArgs...)
	}

	// Set display name for the status bar (used by #{@display-name} format).
	tmuxRun("set-option", "-w", "-t", session+":"+agent.HoldWindow,
		"@display-name", CapitalizeWindow(agent.Mode))

	// Hide the hold window in the status bar — it sits at index 0, before
	// the F1-F10 range. Per-window format overrides follow the window object
	// across swap-window operations. modeSwapArgs manages these during cycling.
	tmuxRun("set-option", "-w", "-t", session+":"+agent.HoldWindow,
		"window-status-format", "")
	tmuxRun("set-option", "-w", "-t", session+":"+agent.HoldWindow,
		"window-status-current-format", "")

	// Console viewer — pre-split the window has one pane, so the bare window target addresses it.
	tmuxRun("send-keys", "-t", session+":"+agent.HoldWindow,
		fmt.Sprintf("muxcode console %s", agent.Role), "Enter")

	// Split horizontally for agent pane (pane 1).
	splitArgs := []string{"split-window", "-h", "-t", session + ":" + agent.HoldWindow}
	if projectDir != "" {
		splitArgs = append(splitArgs, "-c", projectDir)
	}
	tmuxRun(splitArgs...)

	// Stamp pane identity while creation-order indices still hold (MUX-117).
	if terr := TagWindowPanes(session, agent.HoldWindow); terr != nil && !errors.Is(terr, ErrPaneTagUnsupported) {
		fmt.Fprintf(os.Stderr, "Warning: pane tagging failed for %s — window marked broken, deliveries error rather than risk index misdelivery: %v\n", agent.HoldWindow, terr)
	}

	// Creation-instant: launch survives tag failure — see CreationPaneTarget.
	agentPane := CreationPaneTarget(session, agent.HoldWindow, PaneTagAgent)
	tmuxRun("send-keys", "-t", agentPane,
		fmt.Sprintf("muxcode agent launch %s", agent.Role), "Enter")

	// Start background auto-accept + wake-up for the new agent.
	// Hold-window agents are not in the session's cfg.Windows list, so the
	// main AutoAccept() process never sees them. Without this, the agent
	// launches, reaches its ❯ prompt, but never gets woken to check inbox
	// — even though PreLaunchSetup() wrote a startup message.
	go modeAutoAcceptAndWake(session, agent)

	return nil
}

// modeProjectDir resolves the project directory from an existing tmux pane.
// Queries the edit window first (most reliable), then falls back to the
// session's active pane.
func modeProjectDir(session string) string {
	// Try the edit window's Neovim pane (always in the project dir).
	out, err := exec.Command("tmux", "display-message",
		"-t", PaneTargetForWindow(session, "edit", PaneTagLeft), "-p", "#{pane_current_path}").Output()
	if err == nil {
		if dir := strings.TrimSpace(string(out)); dir != "" {
			return dir
		}
	}
	// Fallback: active pane in the session.
	out, err = exec.Command("tmux", "display-message",
		"-t", session+":", "-p", "#{pane_current_path}").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// FormatModeStatus returns a formatted status string for the mode cycle.
func FormatModeStatus(state *ModeCycleState) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Mode Cycle: %s ===\n", state.Window))
	current := CurrentModeAgent(state)
	if current != nil {
		sb.WriteString(fmt.Sprintf("Active: %s (index %d)\n", current.Mode, current.Index))
	}
	sb.WriteString(fmt.Sprintf("Total modes: %d\n\n", len(state.Agents)))
	for _, a := range state.Agents {
		marker := "  "
		if a.Index == state.Current {
			marker = "→ "
		}
		hold := ""
		if a.HoldWindow != "" {
			hold = fmt.Sprintf(" (hold: %s)", a.HoldWindow)
		}
		sb.WriteString(fmt.Sprintf("%s[%d] %-10s role=%-10s%s\n", marker, a.Index, a.Mode, a.Role, hold))
	}
	return sb.String()
}

// FormatModeList returns a concise list of registered modes.
func FormatModeList(state *ModeCycleState) string {
	var sb strings.Builder
	for _, a := range state.Agents {
		marker := " "
		if a.Index == state.Current {
			marker = "*"
		}
		sb.WriteString(fmt.Sprintf("[%s] %s (%s)\n", marker, a.Mode, a.Role))
	}
	return sb.String()
}

// tmuxWindowExists checks if a tmux window exists.
func tmuxWindowExists(session, window string) bool {
	cmd := exec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == window {
			return true
		}
	}
	return false
}

// tmuxRun runs a tmux command, ignoring errors.
func tmuxRun(args ...string) {
	exec.Command("tmux", args...).Run()
}

// tmuxRunErr runs a tmux command and returns any error.
func tmuxRunErr(args ...string) error {
	return exec.Command("tmux", args...).Run()
}

// modeAutoAcceptAndWake polls a hold-window agent pane for startup prompts
// and wakes the agent once it reaches the idle prompt. Mirrors the behavior
// of AutoAccept() for regular session windows.
//
// Hold-window agents (auto, research) are created on-demand via modeCreateAgent
// and are NOT in the session's cfg.Windows list, so AutoAccept never covers
// them. Without this, the agent launches, reaches ❯, and sits idle — never
// waking to process the startup message that PreLaunchSetup wrote to its inbox.
func modeAutoAcceptAndWake(session string, agent *ModeAgent) {
	pane := PaneTargetForWindow(session, agent.HoldWindow, PaneTagAgent)
	provider := ResolveProvider(agent.Role)

	for attempt := 0; attempt < 30; attempt++ {
		time.Sleep(2 * time.Second)

		content, err := TmuxCapturePaneLines(pane, 50)
		if err != nil {
			continue
		}

		state := provider.ClassifyPane(content)

		switch state {
		case PaneTrustPrompt:
			provider.AcceptStartup(session, pane, state)
			LogLifecycle(session, "info", "mode-accept", "trust-prompt", agent.Role)

		case PaneBypassPrompt:
			provider.AcceptStartup(session, pane, state)
			LogLifecycle(session, "info", "mode-accept", "bypass-prompt", agent.Role)
			// Fall through to check for idle on next iteration

		case PaneIdle:
			LogLifecycle(session, "info", "mode-accept", "agent-ready", agent.Role)

			if !NeedsWakeUp(session, agent.Role) {
				return
			}

			// Stabilization delay — let the agent fully initialize
			time.Sleep(1 * time.Second)

			// Clear any stale notification markers so Notify() doesn't
			// suppress the wake-up (the agent just reached idle for the
			// first time — any markers are from a previous lifecycle).
			ClearNotifiedIDs(session, agent.Role)

			if !provider.SupportsHooks() {
				if err := provider.SendWakeUp(session, agent.Role, false); err != nil {
					LogLifecycle(session, "warn", "mode-accept", "wake-failed",
						agent.Role+": "+err.Error())
				} else {
					LogLifecycle(session, "info", "mode-accept", "wake-provider", agent.Role)
				}
			} else {
				// Claude Code agents: inject "You have new messages"
				// Re-capture to check for existing wake text
				freshContent, err := TmuxCapturePaneLines(pane, 50)
				if err != nil {
					return
				}

				if strings.Contains(freshContent, "You have new messages") {
					TmuxSendEnter(pane)
					LogLifecycle(session, "info", "mode-accept", "wake-enter", agent.Role)
				} else {
					TmuxSendKeys(pane, "You have new messages")
					// Poll for text to appear
					for poll := 0; poll < 10; poll++ {
						time.Sleep(100 * time.Millisecond)
						cap, err := TmuxCapturePaneLines(pane, 3)
						if err != nil {
							break
						}
						if strings.Contains(cap, "You have new messages") {
							break
						}
					}
					TmuxSendEnter(pane)
					LogLifecycle(session, "info", "mode-accept", "wake-full", agent.Role)
				}
			}
			return

		default:
			// PaneNotReady — keep polling
		}
	}
	// Timed out (60s) without reaching idle — log and exit.
	// The daemon's checkIdleAgents will eventually catch it.
	LogLifecycle(session, "warn", "mode-accept", "timeout", agent.Role)
}
