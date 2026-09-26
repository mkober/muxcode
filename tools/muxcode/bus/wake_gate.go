package bus

import (
	"fmt"
	"os"
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
		LogLifecycle(session, "info", "notify", "wake-skipped", role+": agent is mid-turn")
		return fmt.Errorf("%s: agent is mid-turn: %w", role, ErrInjectionSkipped)
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

// taskAnswered reports whether t's request has been answered: its delivery
// status is responded, or the log holds a response from t.To to t.From with
// t's action sent at or after the request — an answer correlated to another
// request id for the same work. The (from, to, action) match is deliberately
// narrow so a foreign answer is never adopted (MUX-170).
func taskAnswered(session string, t Task) bool {
	if ds, err := ReadDeliveryStatus(session, t.ID); err == nil && ds.Status == StatusResponded {
		return true
	}
	for _, m := range readLogForRole(session, t.To, 200) {
		if m.Type == "response" && m.From == t.To && m.To == t.From && m.Action == t.Action && m.TS >= t.SentAt {
			return true
		}
	}
	return false
}
