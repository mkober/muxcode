package daemon

import (
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// TestCheckPollHealth_StaleTaskRecoversInOnePoll is the MUX-192 11:06:56
// sequence inverted: for a codex review role with an answered-elsewhere
// in-flight task and a stale pending request, the receipt-gap backstop used to
// call the same unforced SendWakeUp that refused, logging delivery-gap-skip
// every poll until the 600s task timeout. It must now recover through a forced
// ForceDeliver on the first poll and log no skip. The pending request is
// force-sent, as edit's was, or the in-flight dedup guard drops it.
func TestCheckPollHealth_StaleTaskRecoversInOnePoll(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	t.Setenv(bus.RoleCLIEnvVar("review"), "codex")
	d.agentAlive = allAlive
	var forces []bool
	d.forceDeliver = func(_, role string, force bool) (bus.DeliverResult, error) {
		forces = append(forces, force)
		return bus.DeliverResult{Role: role, Delivered: 1}, nil
	}

	stale := bus.NewMessage("edit", "review", "request", "review", "review the branch", "")
	stale.TS = time.Now().Unix() - 300
	if err := bus.CreateTask(session, stale, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	reply := bus.NewMessage("review", "edit", "response", "review", "LGTM", "1790347737-test-chain")
	if err := bus.Send(session, reply); err != nil {
		t.Fatalf("Send reply: %v", err)
	}
	pending := bus.NewMessage("edit", "review", "request", "review", "review the fix", "")
	pending.TS = time.Now().Unix() - (pollHealthGapSecs + 30)
	if err := bus.SendForce(session, pending); err != nil {
		t.Fatalf("SendForce pending: %v", err)
	}

	for i := 0; i < 4; i++ {
		d.lastPollHealthCheck = 0
		d.checkPollHealth()
	}

	if len(forces) != 1 || !forces[0] {
		t.Fatalf("expected exactly one forced re-drive on the first poll, got %v", forces)
	}
	if !d.pollGapRecovered["review"] {
		t.Error("a delivered re-drive must close the recovery for this episode")
	}
	if rows := lifecycleDetails(t, session, "delivery-gap-skip"); len(rows) != 0 {
		t.Errorf("a stale task must no longer produce delivery-gap-skip rows, got %v", rows)
	}
}

// TestCheckTrackedTasks_AnsweredElsewhereLeavesInFlight replays the MUX-192
// correlation: the chain (test → review) and edit both asked review for the
// same review, and review answered edit with "review-complete" replying to the
// chain's id. Edit's task must be completed with that answer instead of held
// in flight to the 600s timeout. Negative control: a response to an unrelated
// action leaves it in flight.
func TestCheckTrackedTasks_AnsweredElsewhereLeavesInFlight(t *testing.T) {
	for _, tc := range []struct {
		action   string
		complete bool
	}{
		{"review-complete", true},
		{"plan", false},
	} {
		t.Run(tc.action, func(t *testing.T) {
			session := testSession(t)
			d := New(session, 5, 8)

			chain := bus.NewMessage("test", "review", "request", "review", "Tests passed — review the changes", "")
			if err := bus.SendNoCC(session, chain); err != nil {
				t.Fatalf("SendNoCC chain: %v", err)
			}
			tracked := bus.NewMessage("edit", "review", "request", "review", "review the MUX-154 changes", "")
			if err := bus.SendForce(session, tracked); err != nil {
				t.Fatalf("SendForce tracked: %v", err)
			}
			if err := bus.CreateTask(session, tracked, 600); err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			unrelated := bus.NewMessage("edit", "review", "request", "security-review", "audit the scrubber", "")
			if err := bus.Send(session, unrelated); err != nil {
				t.Fatalf("Send unrelated: %v", err)
			}
			reply := bus.NewMessage("review", "edit", "response", tc.action, "1 must-fix", chain.ID)
			if err := bus.Send(session, reply); err != nil {
				t.Fatalf("Send reply: %v", err)
			}

			d.lastTrackedTaskCheck = 0
			d.checkTrackedTasks()

			task, err := bus.ReadTask(session, tracked.ID)
			if err != nil {
				t.Fatalf("ReadTask: %v", err)
			}
			rows := lifecycleDetails(t, session, "task-answered-elsewhere")
			pending := pendingIDs(t, session, "review")
			if !pending[unrelated.ID] {
				t.Errorf("an unrelated pending request must stay in review's inbox, pending=%v", pending)
			}
			if tc.complete {
				if task.Status != bus.TaskCompleted || task.ResponseID != reply.ID {
					t.Errorf("edit's task must complete with the chain reply, got status=%s response=%s", task.Status, task.ResponseID)
				}
				if pending[tracked.ID] {
					t.Error("the answered request must be drained from review's inbox, or it is dispatched again")
				}
				if ds, err := bus.ReadDeliveryStatus(session, tracked.ID); err != nil || ds.Status != bus.StatusResponded || ds.ResponseID != reply.ID {
					t.Errorf("the answered request's delivery status must be responded to %s, got %+v (%v)", reply.ID, ds, err)
				}
				if len(rows) != 1 {
					t.Errorf("expected one task-answered-elsewhere row, got %v", rows)
				}
				return
			}
			if task.Status != bus.TaskInFlight {
				t.Errorf("a reply to an unrelated action must leave the task in flight, got %s", task.Status)
			}
			if !pending[tracked.ID] {
				t.Error("an unanswered request must stay pending in review's inbox")
			}
			if len(rows) != 0 {
				t.Errorf("no task-answered-elsewhere row expected, got %v", rows)
			}
		})
	}
}

// pendingIDs returns the ids of the messages pending in role's inbox.
func pendingIDs(t *testing.T, session, role string) map[string]bool {
	t.Helper()
	msgs, err := bus.Peek(session, role)
	if err != nil {
		t.Fatalf("Peek %s: %v", role, err)
	}
	ids := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		ids[m.ID] = true
	}
	return ids
}
