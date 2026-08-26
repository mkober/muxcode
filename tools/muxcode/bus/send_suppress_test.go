package bus

import (
	"errors"
	"os"
	"testing"
	"time"
)

func suppressTestSetup(t *testing.T, session string) {
	t.Helper()
	t.Setenv("BUS_SESSION", session)
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(BusDir(session)) })
}

// A suppressed request must return ErrSendSuppressed, never nil — the
// nil return let the CLI print "Sent", create a task for an undelivered
// message, and jam every retry on its own phantom (2026-08-26).
func TestSend_InFlightSuppressionReturnsSentinel(t *testing.T) {
	session := "send-suppress-test"
	suppressTestSetup(t, session)

	aged := Message{
		ID: "task-msg-1", From: "edit", To: "run", Type: "request",
		Action: "run", Payload: "original work", TS: time.Now().Unix() - 60,
	}
	if err := CreateTask(session, aged, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	dup := NewMessage("edit", "run", "request", "run", "retry of the same work", "")
	err := Send(session, dup)
	if !errors.Is(err, ErrSendSuppressed) {
		t.Fatalf("expected ErrSendSuppressed, got %v", err)
	}
	if msgs, _ := Peek(session, "run"); len(msgs) != 0 {
		t.Errorf("a suppressed message must not reach the inbox, found %d", len(msgs))
	}
}

// SendForce bypasses the guard — the message lands despite the in-flight
// task, so a stuck request can never block its own unsticking.
func TestSendForce_BypassesInFlightGuard(t *testing.T) {
	session := "send-force-test"
	suppressTestSetup(t, session)

	aged := Message{
		ID: "task-msg-2", From: "edit", To: "run", Type: "request",
		Action: "run", Payload: "original work", TS: time.Now().Unix() - 60,
	}
	if err := CreateTask(session, aged, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	fresh := NewMessage("edit", "run", "request", "run", "the fix has landed - rerun", "")
	if err := SendForce(session, fresh); err != nil {
		t.Fatalf("SendForce must bypass the guard, got %v", err)
	}
	msgs, _ := Peek(session, "run")
	found := false
	for _, m := range msgs {
		if m.Payload == "the fix has landed - rerun" {
			found = true
		}
	}
	if !found {
		t.Errorf("forced message must reach the inbox, got %d messages", len(msgs))
	}
}

// The inbox-duplicate branch carries the same sentinel.
func TestSend_InboxDuplicateReturnsSentinel(t *testing.T) {
	session := "send-inboxdup-test"
	suppressTestSetup(t, session)

	first := NewMessage("edit", "run", "request", "run", "same payload", "")
	if err := Send(session, first); err != nil {
		t.Fatalf("first send must succeed, got %v", err)
	}
	second := NewMessage("edit", "run", "request", "run", "same payload", "")
	if err := Send(session, second); !errors.Is(err, ErrSendSuppressed) {
		t.Errorf("expected ErrSendSuppressed on the inbox duplicate, got %v", err)
	}
}
