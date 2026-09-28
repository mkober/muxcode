package bus

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// wakeSendGraceSecs is how long an unanswered in-flight task withholds an
// unforced wake: the window after an injection in which the agent has its
// work but its pane may not yet show a working marker.
const wakeSendGraceSecs = 5

// unforcedWakeGate is the single refusal both listenerless providers' unforced
// SendWakeUp applies: a fresh unanswered task (wakeBlockedByTask) or a mid-turn
// pane (AgentIsWorking) withholds the wake with ErrInjectionSkipped. Every
// refusal writes a wake-skipped lifecycle row naming role, task and age — the
// stderr line alone went unread through both MUX-192 incidents.
func unforcedWakeGate(session, role string) error {
	if t, age, ok := wakeBlockedByTask(session, role); ok {
		fmt.Fprintf(os.Stderr, "  [wakeup] skipping %s injection — in-flight task %s:%s exists (%ds old)\n",
			role, t.Action, shortID(t.ID), age)
		LogLifecycle(session, "info", "notify", "wake-skipped",
			fmt.Sprintf("%s: in-flight task %s (%s, %ds old)", role, t.ID, t.Action, age))
		return fmt.Errorf("%s: in-flight task %s (%ds old): %w", role, shortID(t.ID), age, ErrInjectionSkipped)
	}
	if AgentIsWorking(session, role) {
		task := oldestInFlightTaskTo(session, role)
		LogLifecycle(session, "info", "notify", "wake-skipped", role+": agent is mid-turn"+task)
		return fmt.Errorf("%s: agent is mid-turn%s: %w", role, task, ErrInjectionSkipped)
	}
	return nil
}

// wakeBlockedByTask returns the in-flight task to role that withholds an
// unforced wake, and its age in seconds.
//
// A task blocks only while it is younger than wakeSendGraceSecs, unanswered
// (taskAnswered), and not still pending in role's inbox — a pending request is
// what the wake delivers, not evidence the agent is working on it. Past the
// grace a task says only that a request was sent: on 2026-09-25 (MUX-192) a
// codex review agent sat idle for minutes behind a task already answered under
// the chain's request id, and the receipt-gap backstop was refused by the same
// rule every poll. From the grace on, AgentIsWorking decides busy.
func wakeBlockedByTask(session, role string) (Task, int64, bool) {
	tasks, _ := ListTasks(session, TaskInFlight)
	if len(tasks) == 0 {
		return Task{}, 0, false
	}
	pending := map[string]bool{}
	if msgs, err := Peek(session, role); err == nil {
		for _, m := range msgs {
			pending[m.ID] = true
		}
	}
	now := time.Now().Unix()
	for _, t := range tasks {
		age := now - t.SentAt
		if t.To != role || age >= wakeSendGraceSecs || pending[t.ID] || taskAnswered(session, t) {
			continue
		}
		return t, age, true
	}
	return Task{}, 0, false
}

// oldestInFlightTaskTo describes role's oldest in-flight task as
// " (in-flight task <id>, <action>, <age>s old)", or "" when there is none —
// the task a mid-turn wake-skipped row names.
func oldestInFlightTaskTo(session, role string) string {
	tasks, _ := ListTasks(session, TaskInFlight)
	var oldest *Task
	for i := range tasks {
		if tasks[i].To == role && (oldest == nil || tasks[i].SentAt < oldest.SentAt) {
			oldest = &tasks[i]
		}
	}
	if oldest == nil {
		return ""
	}
	return fmt.Sprintf(" (in-flight task %s, %s, %ds old)", oldest.ID, oldest.Action, time.Now().Unix()-oldest.SentAt)
}

// taskAnswered reports whether t's request has been answered: its delivery
// status is responded, or TaskAnsweredElsewhere finds the answer under
// another request id.
func taskAnswered(session string, t Task) bool {
	if ds, err := ReadDeliveryStatus(session, t.ID); err == nil && ds.Status == StatusResponded {
		return true
	}
	_, ok := TaskAnsweredElsewhere(session, t)
	return ok
}

// TaskAnsweredElsewhere returns the id of a logged response that answers t
// under another request id — the MUX-192 shape, where review answered the
// chain's review request and edit's tracked one stayed in flight to the
// timeout. The response must come from t.To to t.From with t's action — or
// that action with a suffix, as review answers "review" with
// "review-complete" (the incident reply 1790347804-review-af895fd9) — at or
// after t was sent, and must not reply to another request t.From itself sent:
// the (from, to, action) match plus that exclusion keep a sibling's or a
// foreign answer from being adopted (MUX-170).
//
// "After t was sent" is decided by log order: only responses logged after t's
// own request line qualify. Timestamps are second-resolution, so an inclusive
// compare adopted a same-second reply that preceded the request (PR #93
// review); when the request has left the read window, a response must be
// strictly later than t.SentAt.
func TaskAnsweredElsewhere(session string, t Task) (string, bool) {
	msgs := readLogForRole(session, t.To, 200)
	start, requestLogged := 0, false
	for i, m := range msgs {
		if m.ID == t.ID {
			start, requestLogged = i+1, true
			break
		}
	}
	for _, m := range msgs[start:] {
		if m.Type != "response" || m.From != t.To || m.To != t.From || !answersAction(m.Action, t.Action) || m.ReplyTo == t.ID {
			continue
		}
		if !requestLogged && m.TS <= t.SentAt {
			continue
		}
		if m.ReplyTo != "" {
			if orig, ok := FindMessageByID(session, m.ReplyTo); ok && orig.From == t.From {
				continue
			}
		}
		return m.ID, true
	}
	return "", false
}

// answersAction reports whether a response action answers a request action:
// equal, or the request action followed by a hyphenated suffix.
func answersAction(responseAction, requestAction string) bool {
	return responseAction == requestAction || strings.HasPrefix(responseAction, requestAction+"-")
}
