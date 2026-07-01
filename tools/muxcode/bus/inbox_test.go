package bus

import (
	"fmt"
	"math/rand"
	"testing"
)

// testSession returns a unique session name and registers cleanup.
func testSession(t *testing.T) string {
	t.Helper()
	session := fmt.Sprintf("test-%d", rand.Int())
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = Cleanup(session) })
	return session
}

func TestSendDropsSelfAddressed(t *testing.T) {
	session := testSession(t)
	// A self-addressed message (from == to) must never be delivered.
	if err := Send(session, NewMessage("deploy", "deploy", "request", "verify", "self ping", "")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if msgs, _ := Peek(session, "deploy"); len(msgs) != 0 {
		t.Errorf("self-send should be dropped, inbox has %d message(s)", len(msgs))
	}
}

func TestStartupSelfSendStillDelivered(t *testing.T) {
	session := testSession(t)
	// The startup bootstrap is a LEGITIMATE self-addressed request — it must be
	// delivered and remain actionable so the daemon re-wakes the agent until it
	// consumes the message and restores context (see PreLaunchSetup). Use
	// SendNoCC to mirror PreLaunchSetup and avoid touching the package-global
	// auto-CC rate-limiter (which would pollute other tests in the same run).
	if err := SendNoCC(session, NewMessage("build", "build", "request", "startup", "Session started", "")); err != nil {
		t.Fatalf("SendNoCC: %v", err)
	}
	if msgs, _ := Peek(session, "build"); len(msgs) != 1 {
		t.Fatalf("startup self-send should be delivered, got %d message(s)", len(msgs))
	}
	if !HasActionableMessages(session, "build") {
		t.Error("startup self-send must remain actionable")
	}
}

func TestSelfAddressedFilteredFromActionableAndUnnotified(t *testing.T) {
	session := testSession(t)
	// Simulate a self-message already in the inbox (e.g. queued before the fix).
	self := NewMessage("deploy", "deploy", "request", "verify", "stale self request", "")
	if err := AppendToInbox(session, "deploy", self); err != nil {
		t.Fatalf("AppendToInbox: %v", err)
	}
	if HasActionableMessages(session, "deploy") {
		t.Error("self-addressed message must not count as actionable")
	}
	if msgs := UnnotifiedMessages(session, "deploy"); len(msgs) != 0 {
		t.Errorf("self-addressed message must not be unnotified, got %d", len(msgs))
	}
}

func TestCCdRequestNotActionableForEdit(t *testing.T) {
	session := testSession(t)
	// Auto-CC copies a request addressed to another agent verbatim into edit's
	// inbox (To stays the original recipient). That informational copy must NOT
	// count as actionable for edit, otherwise the daemon re-wakes edit forever
	// for a request it can never complete.
	cc := NewMessage("test", "review", "request", "review", "review the changes", "")
	if err := AppendToInbox(session, "edit", cc); err != nil {
		t.Fatalf("AppendToInbox: %v", err)
	}
	if HasActionableMessages(session, "edit") {
		t.Error("CC'd request addressed to review must not be actionable for edit")
	}

	// A request genuinely addressed to edit IS actionable.
	direct := NewMessage("build", "edit", "request", "check", "please check", "")
	if err := AppendToInbox(session, "edit", direct); err != nil {
		t.Fatalf("AppendToInbox: %v", err)
	}
	if !HasActionableMessages(session, "edit") {
		t.Error("request addressed to edit must be actionable for edit")
	}
}

func TestSendAndReceive(t *testing.T) {
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "compile", "build it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs, err := Receive(session, "build")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].From != "edit" || msgs[0].Action != "compile" || msgs[0].Payload != "build it" {
		t.Errorf("message mismatch: %+v", msgs[0])
	}

	// Inbox should be empty after receive
	msgs, err = Receive(session, "build")
	if err != nil {
		t.Fatalf("second Receive: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("inbox not empty after receive: got %d messages", len(msgs))
	}
}

func TestReceive_EmptyInbox(t *testing.T) {
	session := testSession(t)

	msgs, err := Receive(session, "build")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d messages", len(msgs))
	}
}

func TestPeek_DoesNotConsume(t *testing.T) {
	session := testSession(t)

	msg := NewMessage("edit", "test", "event", "notify", "check", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Peek should return the message
	msgs, err := Peek(session, "test")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Peek got %d messages, want 1", len(msgs))
	}

	// Peek again — still there
	msgs, err = Peek(session, "test")
	if err != nil {
		t.Fatalf("second Peek: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("second Peek got %d messages, want 1", len(msgs))
	}

	// Receive consumes it
	msgs, err = Receive(session, "test")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Receive got %d messages, want 1", len(msgs))
	}

	// Now peek returns empty
	msgs, err = Peek(session, "test")
	if err != nil {
		t.Fatalf("Peek after receive: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("Peek after receive got %d messages, want 0", len(msgs))
	}
}

func TestSendMultiple_OrderPreserved(t *testing.T) {
	session := testSession(t)

	// Collect messages across multiple send-receive cycles.
	// Each Receive clears the inbox so HasPendingInboxRequest returns false
	// (allowing the next send to bypass the inbox dedup check).
	var msgs []Message
	for i := 0; i < 3; i++ {
		msg := NewMessage("edit", "build", "request", "compile", fmt.Sprintf("msg-%d", i), "")
		if err := Send(session, msg); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		received, err := Receive(session, "build")
		if err != nil {
			t.Fatalf("Receive %d: %v", i, err)
		}
		msgs = append(msgs, received...)
	}

	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	for i, m := range msgs {
		want := fmt.Sprintf("msg-%d", i)
		if m.Payload != want {
			t.Errorf("message %d: payload=%q, want %q", i, m.Payload, want)
		}
	}
}

func TestHasMessages(t *testing.T) {
	session := testSession(t)

	if HasMessages(session, "build") {
		t.Error("expected no messages initially")
	}

	msg := NewMessage("edit", "build", "request", "compile", "go", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if !HasMessages(session, "build") {
		t.Error("expected messages after send")
	}

	if _, err := Receive(session, "build"); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	if HasMessages(session, "build") {
		t.Error("expected no messages after receive")
	}
}

func TestSendNoCC_SkipsAutoCC(t *testing.T) {
	session := testSession(t)
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// build is an auto-CC role — SendNoCC should NOT copy to edit
	msg := NewMessage("build", "test", "event", "build-complete", "done", "")
	if err := SendNoCC(session, msg); err != nil {
		t.Fatalf("SendNoCC: %v", err)
	}

	// test should have the message
	msgs, err := Receive(session, "test")
	if err != nil {
		t.Fatalf("Receive test: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("test inbox: got %d messages, want 1", len(msgs))
	}

	// edit should NOT have the message (no auto-CC)
	editMsgs, err := Receive(session, "edit")
	if err != nil {
		t.Fatalf("Receive edit: %v", err)
	}
	if len(editMsgs) != 0 {
		t.Errorf("edit inbox should be empty with SendNoCC, got %d messages", len(editMsgs))
	}
}

func TestSend_StillCCs(t *testing.T) {
	session := testSession(t)
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// build is an auto-CC role — Send should copy to edit
	msg := NewMessage("build", "test", "event", "build-complete", "done", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// test should have the message
	msgs, err := Receive(session, "test")
	if err != nil {
		t.Fatalf("Receive test: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("test inbox: got %d messages, want 1", len(msgs))
	}

	// edit should also have the message (auto-CC)
	editMsgs, err := Receive(session, "edit")
	if err != nil {
		t.Fatalf("Receive edit: %v", err)
	}
	if len(editMsgs) != 1 {
		t.Errorf("edit inbox should have 1 auto-CC message, got %d", len(editMsgs))
	}
}

func TestInboxCount(t *testing.T) {
	session := testSession(t)

	if got := InboxCount(session, "build"); got != 0 {
		t.Errorf("initial count = %d, want 0", got)
	}

	// Use unique actions to avoid inbox dedup suppression between sends.
	// Each Receive clears the inbox so the next send is not suppressed.
	actions := []string{"compile", "build", "link"}
	var msgs []Message
	for i := 0; i < 3; i++ {
		msg := NewMessage("edit", "build", "request", actions[i], fmt.Sprintf("msg-%d", i), "")
		if err := Send(session, msg); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		received, err := Receive(session, "build")
		if err != nil {
			t.Fatalf("Receive %d: %v", i, err)
		}
		msgs = append(msgs, received...)
	}

	if got := InboxCount(session, "build"); got != 0 {
		t.Errorf("count after full consume = %d, want 0", got)
	}
	if len(msgs) != 3 {
		t.Errorf("got %d received messages, want 3", len(msgs))
	}
	for i, m := range msgs {
		want := fmt.Sprintf("msg-%d", i)
		if m.Payload != want {
			t.Errorf("message %d: payload=%q, want %q", i, m.Payload, want)
		}
	}
}
