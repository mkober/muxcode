package bus

import (
	"fmt"
	"strings"
	"testing"
)

// Regression cover for the non-hook wake-up wedge: an unbounded inbox was
// concatenated into one send-keys argv until the exec failed, and because the
// inbox is only consumed after a confirmed inject, every retry rebuilt the same
// oversized argv. The agent became permanently undeliverable.

func msgsWithPayloads(payloads ...string) []Message {
	msgs := make([]Message, 0, len(payloads))
	for i, p := range payloads {
		msgs = append(msgs, Message{ID: fmt.Sprintf("id-%d", i), Payload: p})
	}
	return msgs
}

func TestBoundWakeUpBatchReturnsAllWhenWithinLimits(t *testing.T) {
	msgs := msgsWithPayloads("a", "b", "c")
	if got := BoundWakeUpBatch(msgs); len(got) != 3 {
		t.Errorf("len = %d, want 3 (small inbox must not be split)", len(got))
	}
}

func TestBoundWakeUpBatchCapsMessageCount(t *testing.T) {
	payloads := make([]string, 50)
	for i := range payloads {
		payloads[i] = "short"
	}
	got := BoundWakeUpBatch(msgsWithPayloads(payloads...))
	if len(got) != wakeUpMaxMessages {
		t.Errorf("len = %d, want %d", len(got), wakeUpMaxMessages)
	}
}

func TestBoundWakeUpBatchCapsTotalBytes(t *testing.T) {
	// Three payloads of 1800 bytes: the first two fit in the 4000-byte budget,
	// the third must be deferred even though the count cap is nowhere near hit.
	big := strings.Repeat("x", 1800)
	got := BoundWakeUpBatch(msgsWithPayloads(big, big, big))
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (byte budget must bound before the count cap)", len(got))
	}
}

func TestBoundWakeUpBatchAlwaysIncludesFirstMessage(t *testing.T) {
	// A single payload larger than the whole budget must still be delivered.
	// Returning an empty batch would consume nothing and re-inject forever —
	// the same closed loop the bound exists to break.
	huge := strings.Repeat("x", wakeUpMaxBytes*3)
	got := BoundWakeUpBatch(msgsWithPayloads(huge, "next"))
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (oversized head must still make progress)", len(got))
	}
	if got[0].Payload != huge {
		t.Error("first message must be the oversized one, not a later message")
	}
}

func TestBoundWakeUpBatchEmptyInbox(t *testing.T) {
	if got := BoundWakeUpBatch(nil); len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestReceiveDeliveredIDsLeavesUndeliveredMessages(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	var ids []string
	for i := 0; i < 3; i++ {
		m := NewMessage("daemon", "review", "event", "compact-recommended",
			fmt.Sprintf("alert %d", i), "")
		if err := Send(session, m); err != nil {
			t.Fatalf("Send: %v", err)
		}
		ids = append(ids, m.ID)
	}

	// Consume only the first two — the batch the wake-up actually injected.
	got, err := ReceiveDeliveredIDs(session, "review", map[string]bool{
		ids[0]: true, ids[1]: true,
	})
	if err != nil {
		t.Fatalf("ReceiveDeliveredIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("consumed %d, want 2", len(got))
	}

	rest, err := Peek(session, "review")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if len(rest) != 1 || rest[0].ID != ids[2] {
		t.Fatalf("remainder = %d msgs, want exactly the undelivered third", len(rest))
	}
}

func TestReceiveDeliveredIDsWritesDeliveredReceipts(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	m := NewMessage("edit", "review", "request", "review", "review it", "")
	if err := Send(session, m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, err := ReceiveDeliveredIDs(session, "review", map[string]bool{m.ID: true}); err != nil {
		t.Fatalf("ReceiveDeliveredIDs: %v", err)
	}

	// A pane injection is NOT an in-process read: the receipt must say
	// delivered, never acked, or the delivery-gap backstop is misled.
	ds, ok := ReadReceipt(session, m.ID)
	if !ok {
		t.Fatal("expected a receipt")
	}
	if ds.ReceiptKind != ReceiptKindDelivered {
		t.Errorf("ReceiptKind = %q, want %q", ds.ReceiptKind, ReceiptKindDelivered)
	}
}

func TestReceiveDeliveredIDsEmptySetConsumesNothing(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	m := NewMessage("edit", "review", "request", "review", "review it", "")
	if err := Send(session, m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, err := ReceiveDeliveredIDs(session, "review", nil); err != nil {
		t.Fatalf("ReceiveDeliveredIDs: %v", err)
	}
	if !HasMessages(session, "review") {
		t.Error("an empty batch must not drain the inbox")
	}
}

func TestHasPendingActionIgnoresPayload(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// The advisory embeds a live byte count, so consecutive copies never have
	// equal payloads — which is precisely why the payload-comparing
	// HasPendingInboxRequest failed to suppress them.
	m := NewMessage("daemon", "build", "event", "compact-recommended", "total=51 KB", "")
	if err := Send(session, m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if !HasPendingAction(session, "build", "daemon", "compact-recommended") {
		t.Error("HasPendingAction must match on (from, action) regardless of payload")
	}
	if HasPendingInboxRequest(session, "build", "daemon", "compact-recommended", "total=97 KB") {
		t.Error("HasPendingInboxRequest is payload-sensitive — this documents why it cannot bound the advisory")
	}
}

func TestHasPendingActionFalseWhenInboxEmpty(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	if HasPendingAction(session, "build", "daemon", "compact-recommended") {
		t.Error("empty inbox must report no pending action")
	}
}

func TestHasPendingActionDistinguishesSenderAndAction(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	m := NewMessage("daemon", "build", "event", "compact-recommended", "ctx high", "")
	if err := Send(session, m); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if HasPendingAction(session, "build", "daemon", "disk-pressure") {
		t.Error("a different action must not match")
	}
	if HasPendingAction(session, "build", "edit", "compact-recommended") {
		t.Error("a different sender must not match")
	}
}
