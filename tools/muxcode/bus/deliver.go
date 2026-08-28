package bus

import (
	"fmt"
	"os"
	"strconv"
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
		// With force, a role can be wedged on work it already CONSUMED: the
		// inbox row is gone (receipt recorded), an in-flight task remains,
		// and the pane sits at a prompt with parked input — the turn never
		// started (live 2026-08-27: plan consumed a graph verify-spec node,
		// froze for 9m, diagnose reported clean). Re-drive those tasks'
		// requests as a fresh injection so the work restarts — graph runs
		// take delivery priority and must not wait out the task timeout.
		if force {
			if n := redriveInFlightTasks(session, role, provider); n > 0 {
				res.Delivered = n
				return res, nil
			}
		}
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

// redriveInFlightTasks re-injects the requests behind a role's live
// in-flight tasks — the recovery for consumed-but-never-started work,
// which no inbox-based path can reach (the rows are already drained).
// Returns how many requests were re-driven.
func redriveInFlightTasks(session, role string, provider Provider) int {
	tasks, err := ListTasks(session, TaskInFlight)
	if err != nil {
		return 0
	}
	msgs := redriveMessages(tasks, role, time.Now().Unix())
	if len(msgs) == 0 {
		return 0
	}
	if HasPendingInput(session, role) && !IsWindowFocused(session, role) {
		if err := TmuxClearInput(PaneTarget(session, role)); err == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	text := "Re-drive (consumed but never completed): " + BuildCombinedNotification(msgs)
	if err := SendWakeUpWithText(session, role, provider, text, true); err != nil {
		return 0
	}
	LogLifecycle(session, "info", "deliver", "force-redrive",
		fmt.Sprintf("%s: %d in-flight task(s)", role, len(msgs)))
	return len(msgs)
}

// RedriveTask re-injects ONE consumed-but-never-started task's request
// into its role's pane — the graph executor's per-dispatch redrive.
// ForceDeliver's force path redrives EVERY in-flight task for the role,
// which would duplicate unrelated work the agent legitimately holds
// (review must-fix 2026-08-28); a graph node owns only its own dispatch.
func RedriveTask(session string, t Task) bool {
	role := WindowForRole(t.To)
	provider := ResolveProvider(role)
	msgs := redriveMessages([]Task{t}, role, time.Now().Unix())
	if len(msgs) == 0 {
		return false
	}
	if HasPendingInput(session, role) && !IsWindowFocused(session, role) {
		if err := TmuxClearInput(PaneTarget(session, role)); err == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	text := "Re-drive (consumed but never completed): " + BuildCombinedNotification(msgs)
	if err := SendWakeUpWithText(session, role, provider, text, true); err != nil {
		return false
	}
	LogLifecycle(session, "info", "deliver", "force-redrive",
		fmt.Sprintf("%s: task %s (targeted)", role, t.ID))
	return true
}

// TaskStallSecs is how long an in-flight task may sit un-responded
// before the daemon's stall watchdog suspects a consumed-but-never-
// started turn. Graph-dispatched tasks (From == "daemon") use half this
// — graph runs take delivery priority (user rule, 2026-08-27).
func TaskStallSecs() int {
	if v := os.Getenv("MUXCODE_TASK_STALL_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 90
}

// TaskStallDisabled reports the stall watchdog opt-out.
func TaskStallDisabled() bool {
	return os.Getenv("MUXCODE_TASK_STALL_DISABLE") == "1"
}

// TaskStalled reports whether an in-flight task is old enough to suspect
// a stall. Expired tasks are the timeout path's business, not a stall's;
// graph-dispatched tasks stall at half the threshold (priority rule).
func TaskStalled(t Task, now int64, stallSecs int) bool {
	if t.Status != TaskInFlight || TaskExpired(t, now) {
		return false
	}
	if t.From == "daemon" {
		// Clamp: integer halving of a small configured threshold can hit
		// 0, which would make every graph task stall instantly
		// (Copilot review catch, PR #40).
		if stallSecs /= 2; stallSecs < 1 {
			stallSecs = 1
		}
	}
	return now-t.SentAt >= int64(stallSecs)
}

// redriveMessages maps a role's live in-flight tasks back to injectable
// request messages — the pure seam under redriveInFlightTasks.
func redriveMessages(tasks []Task, role string, now int64) []Message {
	var msgs []Message
	for _, t := range tasks {
		if WindowForRole(t.To) != role || TaskExpired(t, now) {
			continue
		}
		msgs = append(msgs, Message{
			ID: t.ID, TS: t.SentAt, From: t.From, To: t.To,
			Type: "request", Action: t.Action, Payload: t.Payload,
		})
	}
	return msgs
}
