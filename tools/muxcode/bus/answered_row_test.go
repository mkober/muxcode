package bus

import (
	"os"
	"testing"
	"time"
)

// TestReply_ClearsReceiptGap is the regression test for the answered-but-
// un-receipted row. An agent that ANSWERS a request without consuming its inbox
// row used to leave that row un-receipted forever: MarkResponded records the
// reply but never sets AckedAt, and ReadReceipt defined "receipted" as AckedAt>0.
// ReceiptGap therefore counted the finished request permanently, so the daemon's
// checkPollHealth backstop re-drove delivery and alerted `delivery-gap` for work
// that was already done. Observed live as ~21h of repeated re-drives and 4+
// duplicate LGTM echoes from one review request.
func TestReply_ClearsReceiptGap(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	req := NewMessage("test", "review", "request", "review", "review the branch", "")
	req.TS = time.Now().Unix() - 300
	if err := Send(session, req); err != nil {
		t.Fatalf("Send request: %v", err)
	}
	if g := ReceiptGap(session, "review", 60*time.Second); len(g) != 1 {
		t.Fatalf("pre-reply gap = %d, want 1 (request looks stuck)", len(g))
	}

	// The agent answers WITHOUT ever consuming its inbox — the exact path that
	// produced the echo loop.
	reply := NewMessage("review", "test", "response", "review", "LGTM", req.ID)
	if err := Send(session, reply); err != nil {
		t.Fatalf("Send reply: %v", err)
	}

	if g := ReceiptGap(session, "review", 60*time.Second); len(g) != 0 {
		t.Errorf("post-reply gap = %d, want 0 (a reply proves receipt)", len(g))
	}
}

// TestReply_DrainsAnsweredRequestRow covers the second half: a reply must also
// remove the request row, otherwise it stays actionable, the daemon keeps waking
// the agent for finished work, and the agent answers again.
func TestReply_DrainsAnsweredRequestRow(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	req := NewMessage("test", "review", "request", "review", "review the branch", "")
	if err := Send(session, req); err != nil {
		t.Fatalf("Send request: %v", err)
	}
	if !HasActionableMessages(session, "review") {
		t.Fatal("request must be actionable before the reply")
	}

	reply := NewMessage("review", "test", "response", "review", "LGTM", req.ID)
	if err := Send(session, reply); err != nil {
		t.Fatalf("Send reply: %v", err)
	}

	if HasActionableMessages(session, "review") {
		t.Error("answered request still actionable — it would re-wake the agent forever")
	}
	msgs, err := Peek(session, "review")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	for _, m := range msgs {
		if m.ID == req.ID {
			t.Error("answered request row was not drained from the inbox")
		}
	}
}

// TestReply_DrainsFromHostInboxForHostedRole pins the routing: a hosted role
// (pr-read) has no inbox of its own — its requests land in the host's (commit).
// The drain must use the same WindowForRole routing Send uses for delivery, or
// it would target a phantom inbox and silently leave the row behind.
func TestReply_DrainsFromHostInboxForHostedRole(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	const hosted = "pr-read"
	host := WindowForRole(hosted)
	if host == hosted {
		t.Skipf("%q is not a hosted role in this build", hosted)
	}

	req := NewMessage("edit", hosted, "request", "pr-read", "read PR 1", "")
	if err := Send(session, req); err != nil {
		t.Fatalf("Send request: %v", err)
	}
	if !HasActionableMessages(session, host) {
		t.Fatalf("request to %q must be actionable in host inbox %q", hosted, host)
	}

	reply := NewMessage(hosted, "edit", "response", "pr-read", "CI green", req.ID)
	if err := Send(session, reply); err != nil {
		t.Fatalf("Send reply: %v", err)
	}

	if HasActionableMessages(session, host) {
		t.Errorf("answered request still actionable in host inbox %q", host)
	}
}

// TestReadReceipt_RespondedCountsAsReceipted isolates the Half 1 read-path rule
// itself: MarkResponded records a reply WITHOUT setting AckedAt, and that must
// still read as receipted. Asserting AckedAt is still zero proves the Status
// clause of hasReceipt is doing the work — not an incidental ack written
// elsewhere — so a future refactor that drops that clause fails here.
func TestReadReceipt_RespondedCountsAsReceipted(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Deliberately NOT Sent: only a delivery status exists, so the message is in
	// neither the log nor an inbox. MarkResponded therefore finds nothing to
	// drain and writes no ack receipt, leaving AckedAt at zero — which is what
	// lets this assert that the Status clause alone makes it read as receipted.
	req := NewMessage("edit", "build", "request", "build", "build it", "")
	if err := CreateDeliveryStatus(session, req); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}
	if _, acked := ReadReceipt(session, req.ID); acked {
		t.Fatal("a sent-but-unanswered message must not read as receipted")
	}

	MarkResponded(session, req.ID, "response-id-1")

	ds, acked := ReadReceipt(session, req.ID)
	if !acked {
		t.Error("a responded message must read as receipted (a reply implies receipt)")
	}
	if ds.AckedAt != 0 {
		t.Errorf("AckedAt = %d, want 0 — the Status clause must be what makes this receipted", ds.AckedAt)
	}
}

// TestMarkResponded_DrainsWithoutAReplyMessage covers the path the live
// integration test caught: cmd/send.go's --wait fallback correlates a response
// that carried NO ReplyTo back to the request it was waiting on, calling
// MarkResponded directly. There is no reply message to hang a drain off, so a
// drain wired only into Send() left the row actionable forever. Found in a real
// session as a stranded run.jsonl row whose "response" predated its request.
func TestMarkResponded_DrainsWithoutAReplyMessage(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	req := NewMessage("edit", "run", "request", "run", "run the script", "")
	if err := Send(session, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !HasActionableMessages(session, "run") {
		t.Fatal("request must be actionable before correlation")
	}

	// No reply is Sent — the --wait fallback correlates an unrelated response.
	MarkResponded(session, req.ID, "some-uncorrelated-response-id")

	if HasActionableMessages(session, "run") {
		t.Error("request marked responded is still actionable — drain did not fire")
	}
	if _, acked := ReadReceipt(session, req.ID); !acked {
		t.Error("request marked responded must read as receipted")
	}
}

// TestMarkResponded_SurvivesMissingStatusFile pins that a GC'd or absent delivery
// status does not skip the drain — the status write and the drain are
// independent best-effort steps, and an undrained row is the harmful outcome.
func TestMarkResponded_SurvivesMissingStatusFile(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	req := NewMessage("edit", "build", "request", "build", "build it", "")
	if err := Send(session, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Simulate the status file having been cleaned up.
	if err := os.Remove(DeliveryPath(session, req.ID)); err != nil {
		t.Fatalf("removing status file: %v", err)
	}

	MarkResponded(session, req.ID, "response-id")

	if HasActionableMessages(session, "build") {
		t.Error("drain must still fire when the delivery status file is missing")
	}
}

// TestReply_LeavesAutoCCCopyInEditInbox pins the blast-radius limit on the drain.
// Auto-CC copies a request addressed to another agent verbatim into edit's inbox
// — the copy carries the SAME message ID. Draining on reply must remove only the
// responder's row, never the CC copy living in a different inbox.
func TestReply_LeavesAutoCCCopyInEditInbox(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// autoCCLastSent is a PACKAGE-LEVEL rate limiter (one CC per role per 60s),
	// not per-session, so exercising the real auto-CC path here would silently
	// consume build's budget and make a later build→test CC test fail. Give this
	// test a fresh limiter and put the old one back.
	savedAutoCC := autoCCLastSent
	autoCCLastSent = make(map[string]int64)
	t.Cleanup(func() { autoCCLastSent = savedAutoCC })

	// build is an auto-CC role, so this genuinely copies the request into edit's
	// inbox with the SAME message ID — the real collision the drain must survive.
	req := NewMessage("build", "test", "request", "test", "run tests", "")
	if err := Send(session, req); err != nil {
		t.Fatalf("Send request: %v", err)
	}
	var ccPresent bool
	for _, m := range mustPeek(t, session, "edit") {
		if m.ID == req.ID {
			ccPresent = true
		}
	}
	if !ccPresent {
		t.Fatal("auto-CC copy did not land in edit's inbox — test cannot prove anything")
	}

	reply := NewMessage("test", "build", "response", "test", "all green", req.ID)
	if err := Send(session, reply); err != nil {
		t.Fatalf("Send reply: %v", err)
	}

	// Responder's row is gone.
	for _, m := range mustPeek(t, session, "test") {
		if m.ID == req.ID {
			t.Error("responder's request row was not drained")
		}
	}
	// edit's CC copy survives.
	var ccFound bool
	for _, m := range mustPeek(t, session, "edit") {
		if m.ID == req.ID {
			ccFound = true
		}
	}
	if !ccFound {
		t.Error("auto-CC copy in edit's inbox was collaterally drained")
	}
}

func mustPeek(t *testing.T, session, role string) []Message {
	t.Helper()
	msgs, err := Peek(session, role)
	if err != nil {
		t.Fatalf("Peek %s: %v", role, err)
	}
	return msgs
}

// TestConsumeByID_LeavesOtherMessages proves the removal is targeted. An auto-CC
// copy of a request addressed to another agent carries the same From/Action, so
// draining must key on message ID alone or it would collaterally remove unrelated
// work from the inbox.
func TestConsumeByID_LeavesOtherMessages(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	first := NewMessage("edit", "build", "request", "build", "build one", "")
	second := NewMessage("edit", "build", "request", "build", "build two", "")
	for _, m := range []Message{first, second} {
		if err := AppendToInbox(session, "build", m); err != nil {
			t.Fatalf("AppendToInbox: %v", err)
		}
	}

	found, err := ConsumeByID(session, "build", first.ID)
	if err != nil {
		t.Fatalf("ConsumeByID: %v", err)
	}
	if !found {
		t.Fatal("ConsumeByID reported not-found for a present message")
	}

	msgs, err := Peek(session, "build")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("inbox has %d messages, want 1 (only the untargeted one survives)", len(msgs))
	}
	if msgs[0].ID != second.ID {
		t.Errorf("surviving message = %q, want %q", msgs[0].ID, second.ID)
	}

	// The drained message carries a consume-ack receipt.
	if _, acked := ReadReceipt(session, first.ID); !acked {
		t.Error("drained message must carry a receipt")
	}
}

// TestConsumeByID_NoopWhenAbsent pins the no-op semantics the Send() reply path
// depends on: the normal case is that the agent already consumed the row, so a
// missing message (and a missing inbox) must be an ordinary false, not an error.
func TestConsumeByID_NoopWhenAbsent(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Missing inbox entirely.
	found, err := ConsumeByID(session, "deploy", "nope-1")
	if err != nil {
		t.Fatalf("ConsumeByID on missing inbox: %v", err)
	}
	if found {
		t.Error("found = true for a missing inbox")
	}

	// Present inbox, absent message — the surrounding messages must survive.
	keep := NewMessage("edit", "deploy", "request", "deploy", "diff it", "")
	if err := AppendToInbox(session, "deploy", keep); err != nil {
		t.Fatalf("AppendToInbox: %v", err)
	}
	found, err = ConsumeByID(session, "deploy", "nope-2")
	if err != nil {
		t.Fatalf("ConsumeByID on absent message: %v", err)
	}
	if found {
		t.Error("found = true for an absent message")
	}
	msgs, err := Peek(session, "deploy")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != keep.ID {
		t.Errorf("a no-op drain disturbed the inbox: %d messages", len(msgs))
	}

	// Empty ID is also a no-op (Send passes ReplyTo verbatim).
	if found, err := ConsumeByID(session, "deploy", ""); err != nil || found {
		t.Errorf("empty msgID: found=%v err=%v, want false/nil", found, err)
	}
}
