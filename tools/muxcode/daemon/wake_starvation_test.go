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
