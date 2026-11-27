package bus

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
//     wide (200-line) capture. With force, that idle REQUIREMENT is skipped.
//   - Force never buys a pass into a WORKING pane, though: AgentIsWorking is
//     positive evidence of a live turn (Claude's spinner, OpenCode's ▸), and
//     every typed wake-up opens with an Escape, which mid-turn is Claude's
//     interrupt key rather than an overlay dismissal. The check precedes the
//     message gathering because both roads out of here type — the unnotified
//     batch and the in-flight re-drive — and only the re-drive was guarded
//     when this refusal was first added (MUX-171 review). "Cannot read the
//     pane" is not "is working", so a capture failure still delivers: this is
//     the manual escape hatch, and AgentIsWorking answers false when it
//     cannot see.
//   - Clears stale parked input in an unfocused pane before injecting.
func ForceDeliver(session, role string, force bool) (DeliverResult, error) {
	return ForceDeliverFor(session, role, force, "")
}

// ForceDeliverFor is ForceDeliver whose in-flight re-drive puts the task
// firstTaskID first, so a bounded re-drive always holds it. The stall
// watchdog spends a stalled task's retry before it calls here, and a
// role-wide re-drive that cut that task from the tail would spend its
// retries on the leading requests and leave it unanswerable (MUX-232 review).
func ForceDeliverFor(session, role string, force bool, firstTaskID string) (DeliverResult, error) {
	role = WindowForRole(role)
	res := DeliverResult{Role: role}

	if !IsKnownRole(role) {
		return res, fmt.Errorf("unknown role %q", role)
	}
	if !TmuxHasSession(session) {
		return res, fmt.Errorf("session %q not found", session)
	}

	provider := ResolveProvider(role)

	if AgentIsWorking(session, role) {
		LogLifecycle(session, "info", "deliver", "deliver-skipped-busy", role)
		res.Skipped = "agent is mid-turn — nothing is typed into a running tool call"
		return res, nil
	}

	// Gather messages to deliver.
	unnotified := UnnotifiedMessages(session, role)
	if len(unnotified) == 0 && force && (HasActionableMessages(session, role) || HasOwedReply(session, role)) {
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
			if n := redriveInFlightTasks(session, role, provider, firstTaskID); n > 0 {
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
// Returns how many requests the typed text held — not how many were in flight:
// the bound can leave a tail out. firstTaskID, when set, leads the batch.
// ForceDeliverFor, its only caller, has already refused a working pane. Tasks
// holding a guard denial are left out: the guard road owns their re-drive.
func redriveInFlightTasks(session, role string, provider Provider, firstTaskID string) int {
	tasks, err := ListTasks(session, TaskInFlight)
	if err != nil {
		return 0
	}
	tasks = withoutGuardOwned(session, tasks)
	msgs := redriveFirst(redriveMessages(tasks, role, time.Now().Unix()), firstTaskID)
	if len(msgs) == 0 {
		return 0
	}
	if HasPendingInput(session, role) && !IsWindowFocused(session, role) {
		if err := TmuxClearInput(PaneTarget(session, role)); err == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	text, shown := redriveNotification(session, msgs)
	if err := redriveText(session, role, provider, text); err != nil {
		return 0
	}
	LogLifecycle(session, "info", "deliver", "force-redrive",
		fmt.Sprintf("%s: %d of %d in-flight task(s)", role, shown, len(msgs)))
	return shown
}

// redriveText delivers a redrive's explicit text and reports whether it
// actually went out.
//
// SendWakeUpWithText discards explicit text for a provider that does not poll
// its own inbox: that road rebuilds the payload by reading the inbox. A redrive
// is the one case where the row is ALREADY CONSUMED, so the provider Peeks an
// empty inbox, returns nil, and the caller reads nil as delivered — logging
// force-redrive and a positive count for an injection that never happened.
// Recovery for those providers needs a road that carries the text itself, so
// until one exists this refuses loudly rather than reporting a false success;
// the task timeout remains the backstop.
func redriveText(session, role string, provider Provider, text string) error {
	if !provider.SelfPollsInbox() {
		LogLifecycle(session, "warn", "deliver", "redrive-undeliverable",
			fmt.Sprintf("%s: %s rebuilds payloads from the inbox, and the redriven row is already consumed", role, provider.Name()))
		return fmt.Errorf("%s: redrive text cannot reach a %s pane", role, provider.Name())
	}
	return SendWakeUpWithText(session, role, provider, text, true)
}

// RedriveTask re-injects ONE consumed-but-never-started task's request
// into its role's pane — the graph executor's per-dispatch redrive.
// ForceDeliver's force path redrives every in-flight task for the role,
// which would duplicate unrelated work the agent legitimately holds; a
// graph node owns only its own dispatch. A task holding a guard denial is
// refused: the guard road owns its re-drive (GuardDenialOwns, MUX-190).
func RedriveTask(session string, t Task) bool {
	if GuardDenialOwns(session, t.ID) {
		LogLifecycle(session, "info", "deliver", "redrive-withheld-guard-denied",
			fmt.Sprintf("%s: task %s — the guard road owns its re-drive", WindowForRole(t.To), t.ID))
		return false
	}
	return redriveTask(session, t)
}

// redriveTask is RedriveTask without the guard-ownership refusal — the guard
// road's own re-drive.
func redriveTask(session string, t Task) bool {
	role := WindowForRole(t.To)
	provider := ResolveProvider(role)
	msgs := redriveMessages([]Task{t}, role, time.Now().Unix())
	if len(msgs) == 0 {
		return false
	}
	if AgentIsWorking(session, role) {
		LogLifecycle(session, "info", "deliver", "redrive-skipped-busy",
			fmt.Sprintf("%s: working — task %s left alone", role, t.ID))
		return false
	}
	if HasPendingInput(session, role) && !IsWindowFocused(session, role) {
		if err := TmuxClearInput(PaneTarget(session, role)); err == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	text, _ := redriveNotification(session, msgs)
	if err := redriveText(session, role, provider, text); err != nil {
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

// redriveNotification is the text both re-drive roads type into the pane: one
// line holding, per request, its id, sender, full payload and the reply
// command to its reply target (redriveBlock), bounded like a wake-up batch —
// at most wakeUpMaxMessages requests and wakeUpMaxBytes, cut between whole
// requests with a count of those left out, the first always included. The
// row it re-drives is already consumed, so `muxcode inbox` returns nothing:
// before MUX-232 this was the wake notification — the payload cut at 80
// runes, no id, no reply route — and on 2026-11-17 the commit agent could do
// the work and not answer it, failing two graph runs at 600 s each.
//
// It returns how many of msgs the text holds — the leading ones — so a caller
// counts only what it typed: a request cut off by the bound was not re-driven.
func redriveNotification(session string, msgs []Message) (text string, shown int) {
	var b strings.Builder
	b.WriteString("Re-drive (consumed but never completed) — this request was already read from your inbox, so `muxcode inbox` will not show it; do it and reply with its command:")
	for i, m := range msgs {
		block := redriveBlock(session, m)
		if i > 0 && (i >= wakeUpMaxMessages || b.Len()+len(block) > wakeUpMaxBytes) {
			break
		}
		b.WriteString(block)
		shown++
	}
	if left := len(msgs) - shown; left > 0 {
		fmt.Fprintf(&b, " | [%d more re-driven request(s) not shown]", left)
	}
	return b.String(), shown
}

// redriveFirst moves the request with id to the front of msgs, so a bounded
// re-drive always holds it: the first request is never cut.
func redriveFirst(msgs []Message, id string) []Message {
	for i, m := range msgs {
		if m.ID == id && i > 0 {
			out := append([]Message{m}, msgs[:i]...)
			return append(out, msgs[i+1:]...)
		}
	}
	return msgs
}

// redriveNewlines flattens a payload to one line: a newline typed into the
// pane would submit the prompt before the reply command.
var redriveNewlines = strings.NewReplacer("\r\n", " ⏎ ", "\n", " ⏎ ", "\r", " ⏎ ")

// redriveBlock renders one re-driven request. Its reply goes to the request's
// ReplyTarget — read from its log line (replyTargetFor), so a `daemon`
// dispatch answers edit and a chain request its reply_role — never the raw
// sender, which may be no bus role at all. A request the guard refused a
// command on carries the guard's reason, so the agent is told what to change
// (MUX-190) — on every re-drive road, the stall watchdog's included.
func redriveBlock(session string, m Message) string {
	target := replyTargetFor(session, Task{ID: m.ID, From: m.From})
	if target == "" || !IsKnownRole(target) {
		target = "edit"
	}
	return fmt.Sprintf(" | [%s] from %s [%s:%s]: %s%s — To reply: %s",
		m.ID, m.From, m.Type, m.Action, redriveNewlines.Replace(m.Payload),
		guardDenialRedriveText(session, m.ID), buildReplyCommand(target, m.ID))
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
