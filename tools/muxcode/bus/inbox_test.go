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

	for i := 0; i < 3; i++ {
		msg := NewMessage("edit", "build", "request", "compile", fmt.Sprintf("msg-%d", i), "")
		if err := Send(session, msg); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	msgs, err := Receive(session, "build")
	if err != nil {
		t.Fatalf("Receive: %v", err)
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

	for i := 0; i < 3; i++ {
		msg := NewMessage("edit", "build", "request", "compile", fmt.Sprintf("msg-%d", i), "")
		if err := Send(session, msg); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	if got := InboxCount(session, "build"); got != 3 {
		t.Errorf("count after 3 sends = %d, want 3", got)
	}
}
