package bus

import (
	"fmt"
	"time"
)

// DeliverResult describes the outcome of a ForceDeliver call.
type DeliverResult struct {
	Role      string // resolved (host) role the messages were delivered to
	Delivered int    // number of messages included in the wake-up
	Skipped   string // non-empty when nothing was delivered, explaining why
}

// ForceDeliver pushes an agent's pending inbox messages into its pane via the
// robust wake-up path (text → delay → Enter → verify for hook providers, or the
// provider's SendWakeUp for non-hook), bypassing the daemon's idle-detection.
//
// It exists because pane-scrape idle detection (ClaudeCodeProvider.IsIdle) can
// transiently misread a finished-but-at-prompt agent as "active" — the daemon
// then never delivers, and the inbox piles up un-notified ("active-with-stale-
// messages"). This is the user-facing escape hatch the backlog called for.
//
// Behavior:
//   - Resolves hosted roles to their host (docs/research/pr-read → host pane).
//   - Gathers unnotified messages. With force, if everything is already marked
//     notified but the inbox still has actionable messages (a dropped send-keys
//     left them stuck), the notified markers are cleared so they re-deliver.
//   - Without force, requires the pane to actually show the idle prompt in a
//     wide (200-line) capture — guards against injecting into a genuinely busy
//     agent. With force, the idle check is skipped entirely.
//   - Clears stale parked input in an unfocused pane before injecting.
func ForceDeliver(session, role string, force bool) (DeliverResult, error) {
	role = WindowForRole(role)
	res := DeliverResult{Role: role}

	if !IsKnownRole(role) {
		return res, fmt.Errorf("unknown role %q", role)
	}
	if !TmuxHasSession(session) {
		return res, fmt.Errorf("session %q not found", session)
	}

	provider := ResolveProvider(role)

	// Gather messages to deliver.
	unnotified := UnnotifiedMessages(session, role)
	if len(unnotified) == 0 && force && HasActionableMessages(session, role) {
		// All messages are marked notified but the agent never processed them
		// (a prior send-keys/Enter was dropped). Clear markers and retry.
		ClearNotifiedIDs(session, role)
		unnotified = UnnotifiedMessages(session, role)
	}
	if len(unnotified) == 0 {
		res.Skipped = "no pending messages"
		return res, nil
	}

	// Idle gate (skipped with --force). A wide capture tolerates the ❯ prompt
	// having scrolled past the 8-line window the daemon's IsIdle uses.
	if !force {
		target := PaneTarget(session, role)
		content, err := TmuxCapturePaneLines(target, 200)
		if err != nil {
			return res, fmt.Errorf("capture pane for %s: %w", role, err)
		}
		if !PaneHasIdlePrompt(content) {
			return res, fmt.Errorf("agent %s does not show an idle prompt — re-run with --force to deliver anyway", role)
		}
	}

	// Clear stale parked input in an unfocused pane so the injection lands clean.
	if HasPendingInput(session, role) && !IsWindowFocused(session, role) {
		target := PaneTarget(session, role)
		if err := TmuxClearInput(target); err == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}

	text := BuildCombinedNotification(unnotified)
	ids := make([]string, 0, len(unnotified))
	for _, m := range unnotified {
		ids = append(ids, m.ID)
	}
	AddNotifiedIDs(session, role, ids)
	if err := SendWakeUpWithText(session, role, provider, text, force); err != nil {
		// Roll back the notified markers so a later attempt can retry.
		ClearNotifiedIDs(session, role)
		return res, fmt.Errorf("deliver to %s: %w", role, err)
	}

	LogLifecycle(session, "info", "deliver", "force-deliver",
		fmt.Sprintf("%s: %d messages", role, len(unnotified)))
	res.Delivered = len(unnotified)
	return res, nil
}
