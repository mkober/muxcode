package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// TestCheckPollHealth_StaleTaskRefusesEveryPoll_Pin pins the MUX-192 11:06:56
// sequence as it stands: for a codex review role with an answered-elsewhere
// in-flight task and a stale pending request, the receipt-gap backstop calls
// the same unforced SendWakeUp that refuses, so every poll logs another
// delivery-gap-skip and the request is never delivered. Phase 2 inverts it —
// delivered within one backstop interval, at most one skip row. The pending
// request is force-sent, as edit's was, or the in-flight dedup guard drops it.
func TestCheckPollHealth_StaleTaskRefusesEveryPoll_Pin(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	t.Setenv(bus.RoleCLIEnvVar("review"), "codex")
	d.agentAlive = allAlive

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

	const polls = 4
	for i := 1; i <= polls; i++ {
		d.lastPollHealthCheck = 0
		d.checkPollHealth()
		if rows := lifecycleDetails(t, session, "delivery-gap-skip"); len(rows) != i {
			t.Fatalf("pin: poll %d expected %d delivery-gap-skip rows (one per poll), got %d: %v", i, i, len(rows), rows)
		}
	}
	for _, row := range lifecycleDetails(t, session, "delivery-gap-skip") {
		if !strings.Contains(row, "in-flight task") {
			t.Errorf("pin: each skip should name the in-flight task, got %q", row)
		}
	}
	if d.pollGapRecovered["review"] {
		t.Error("pin: a refused wake leaves the episode un-recovered, so it retries identically")
	}
	if len(bus.ReceiptGap(session, "review", pollHealthGapSecs*time.Second)) != 1 {
		t.Error("pin: the pending request must still be un-receipted after every poll")
	}
}
