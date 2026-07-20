package bus

import (
	"testing"
	"time"
)

func TestWriteReceipt_Ack(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "plan", "request", "update-docs", "write spec", "")
	if err := CreateDeliveryStatus(session, msg); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}

	WriteReceipt(session, msg.ID, "plan", ReceiptKindAck)

	ds, acked := ReadReceipt(session, msg.ID)
	if !acked {
		t.Fatal("ReadReceipt reports no receipt after WriteReceipt")
	}
	if ds.ReceiptKind != ReceiptKindAck {
		t.Errorf("ReceiptKind = %q, want %q", ds.ReceiptKind, ReceiptKindAck)
	}
	if ds.AckedBy != "plan" {
		t.Errorf("AckedBy = %q, want %q", ds.AckedBy, "plan")
	}
	if ds.AckedAt == 0 {
		t.Error("AckedAt not set")
	}
	if ds.Status != StatusAcked {
		t.Errorf("Status = %q, want %q (a true ack advances status)", ds.Status, StatusAcked)
	}
}

func TestWriteReceipt_DeliveredIsWeakerThanAck(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "review", "request", "review", "review it", "")
	_ = CreateDeliveryStatus(session, msg)

	WriteReceipt(session, msg.ID, "review", ReceiptKindDelivered)

	ds, acked := ReadReceipt(session, msg.ID)
	if !acked {
		t.Fatal("delivered receipt should still register as acked=true (AckedAt set)")
	}
	if ds.ReceiptKind != ReceiptKindDelivered {
		t.Errorf("ReceiptKind = %q, want %q", ds.ReceiptKind, ReceiptKindDelivered)
	}
	if ds.Status != StatusDelivered {
		t.Errorf("Status = %q, want %q (verified-inject is delivered, not acked)", ds.Status, StatusDelivered)
	}
}

func TestWriteReceipt_DoesNotRegressResponded(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "build", "build it", "")
	_ = CreateDeliveryStatus(session, msg)
	MarkResponded(session, msg.ID, "resp-1")

	// A late receipt must not clobber the recorded response.
	WriteReceipt(session, msg.ID, "build", ReceiptKindAck)

	ds, _ := ReadReceipt(session, msg.ID)
	if ds.Status != StatusResponded {
		t.Errorf("Status = %q, want %q (receipt must not regress responded)", ds.Status, StatusResponded)
	}
	if ds.ResponseID != "resp-1" {
		t.Errorf("ResponseID = %q, want resp-1", ds.ResponseID)
	}
	if ds.AckedAt == 0 {
		t.Error("receipt fields should still be recorded alongside responded")
	}
}

func TestWriteReceipt_NoPriorStatus(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// No CreateDeliveryStatus first — message predates tracking / status GC'd.
	WriteReceipt(session, "1700000000-edit-deadbeef", "plan", ReceiptKindAck)

	ds, acked := ReadReceipt(session, "1700000000-edit-deadbeef")
	if !acked {
		t.Fatal("receipt must be durable even with no prior status file")
	}
	if ds.Status != StatusAcked {
		t.Errorf("Status = %q, want %q", ds.Status, StatusAcked)
	}
}

func TestReadReceipt_NoReceipt(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "test", "request", "test", "test it", "")
	_ = CreateDeliveryStatus(session, msg)

	if _, acked := ReadReceipt(session, msg.ID); acked {
		t.Error("a sent-but-unconsumed message must report acked=false")
	}
}

func TestReceiptGap_ReturnsStaleUnreceipted(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Stale, un-receipted message — the poll-loop-is-dead signal.
	stale := NewMessage("edit", "plan", "request", "update-docs", "old", "")
	stale.TS = time.Now().Unix() - 300
	if err := Send(session, stale); err != nil {
		t.Fatalf("Send stale: %v", err)
	}

	// Fresh message — too new to count as stuck.
	fresh := NewMessage("edit", "plan", "request", "update-docs", "new", "")
	if err := Send(session, fresh); err != nil {
		t.Fatalf("Send fresh: %v", err)
	}

	gap := ReceiptGap(session, "plan", 60*time.Second)
	if len(gap) != 1 {
		t.Fatalf("gap = %d messages, want 1 (only the stale one)", len(gap))
	}
	if gap[0].ID != stale.ID {
		t.Errorf("gap contains %q, want the stale message %q", gap[0].ID, stale.ID)
	}

	// Once receipted, the stale message drops out of the gap.
	WriteReceipt(session, stale.ID, "plan", ReceiptKindAck)
	if g := ReceiptGap(session, "plan", 60*time.Second); len(g) != 0 {
		t.Errorf("gap = %d after receipt, want 0", len(g))
	}
}

func TestReceiptGap_IgnoresSelfSends(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// A self-addressed message would never be consumed; it must not register as
	// a permanent gap. Send drops self-sends, so append directly to the inbox.
	self := NewMessage("plan", "plan", "request", "noop", "self", "")
	self.TS = time.Now().Unix() - 300
	if err := AppendToInbox(session, "plan", self); err != nil {
		t.Fatalf("AppendToInbox: %v", err)
	}

	if g := ReceiptGap(session, "plan", 60*time.Second); len(g) != 0 {
		t.Errorf("gap = %d, want 0 (self-sends must be ignored)", len(g))
	}
}

func TestReceive_WritesConsumeReceipt(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "build", "build it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The agent's own consume must write a true ack receipt.
	if _, err := Receive(session, "build"); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	ds, acked := ReadReceipt(session, msg.ID)
	if !acked {
		t.Fatal("Receive did not write a consume-receipt")
	}
	if ds.ReceiptKind != ReceiptKindAck {
		t.Errorf("ReceiptKind = %q, want %q", ds.ReceiptKind, ReceiptKindAck)
	}
	if ds.AckedBy != "build" {
		t.Errorf("AckedBy = %q, want build", ds.AckedBy)
	}
}

// TestReceive_ClearsReceiptGap proves the harness/AgentLoop delivery guarantee
// end-to-end: an agent that consumes its own inbox in-process (bus/agent.go's
// AgentLoop calls Receive, and the standalone harness consumes via the same
// `muxcode inbox` -> Receive path) writes a receipt that removes the message
// from ReceiptGap — the positive-signal detector the Phase 5 daemon backstop
// reads instead of pane-scraping. Before the consume the stale message is a
// gap (looks stuck); after it, the gap is clear.
func TestReceive_ClearsReceiptGap(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// A stale message the local agent has not yet consumed registers as a gap.
	msg := NewMessage("edit", "build", "request", "build", "build it", "")
	msg.TS = time.Now().Unix() - 300
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if g := ReceiptGap(session, "build", 60*time.Second); len(g) != 1 {
		t.Fatalf("pre-consume gap = %d, want 1 (message looks stuck)", len(g))
	}

	// The agent's own in-process consume writes the receipt at the choke point.
	if _, err := Receive(session, "build"); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if g := ReceiptGap(session, "build", 60*time.Second); len(g) != 0 {
		t.Errorf("post-consume gap = %d, want 0 (consume must clear the gap)", len(g))
	}
}
