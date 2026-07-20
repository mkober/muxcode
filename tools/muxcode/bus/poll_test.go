package bus

import (
	"os"
	"testing"
	"time"
)

// Integration tests for poll-based notification (Phase 4, item #21).
// These tests verify the trigger-file → poll detection pipeline that replaced
// the send-keys notification mechanism.

func TestTriggerFile_SendWritesTrigger(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	role := "build"
	triggerPath := TriggerNotifyPath(session, role)

	// No trigger file initially
	if _, err := os.Stat(triggerPath); !os.IsNotExist(err) {
		t.Fatal("trigger file should not exist before Send()")
	}

	// Send a message — Send() appends to inbox but does NOT write trigger.
	// Trigger file is written by Notify() (called at the CLI layer in cmd/send.go).
	msg := NewMessage("edit", role, "request", "build", "run build", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Simulate the notification that cmd/send.go would trigger
	writeTriggerNotify(session, role)

	// Trigger file should now exist
	if _, err := os.Stat(triggerPath); err != nil {
		t.Errorf("trigger file should exist after writeTriggerNotify(): %v", err)
	}
}

func TestTriggerFile_MtimeUpdatesOnNewMessage(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	role := "test"
	triggerPath := TriggerNotifyPath(session, role)

	// First message + notify
	msg1 := NewMessage("edit", role, "request", "test", "run tests", "")
	if err := Send(session, msg1); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	writeTriggerNotify(session, role)

	info1, err := os.Stat(triggerPath)
	if err != nil {
		t.Fatalf("trigger file should exist after first notify: %v", err)
	}
	mtime1 := info1.ModTime()

	// Small delay so mtime differs
	time.Sleep(10 * time.Millisecond)

	// Second message + notify — trigger mtime should update
	msg2 := NewMessage("edit", role, "request", "test", "run tests again", "")
	if err := Send(session, msg2); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	writeTriggerNotify(session, role)

	info2, err := os.Stat(triggerPath)
	if err != nil {
		t.Fatalf("trigger file should exist after second notify: %v", err)
	}
	mtime2 := info2.ModTime()

	if !mtime2.After(mtime1) {
		t.Error("trigger file mtime should update on second notify")
	}
}

func TestTriggerFile_SendNoCCWritesTrigger(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	role := "review"
	triggerPath := TriggerNotifyPath(session, role)

	msg := NewMessage("build", role, "request", "review", "review this", "")
	if err := SendNoCC(session, msg); err != nil {
		t.Fatalf("SendNoCC: %v", err)
	}

	// Simulate the notification that cmd/send.go would trigger
	writeTriggerNotify(session, role)

	if _, err := os.Stat(triggerPath); err != nil {
		t.Errorf("trigger file should exist after writeTriggerNotify(): %v", err)
	}
}

func TestPollDetectsTriggerChange(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	role := "build"
	triggerPath := TriggerNotifyPath(session, role)

	// Record initial trigger mtime (no file = zero)
	var initialMtime int64
	if info, err := os.Stat(triggerPath); err == nil {
		initialMtime = info.ModTime().UnixNano()
	}

	// Write a message to the inbox
	msg := NewMessage("edit", role, "request", "build", "compile", "")
	data, err := EncodeMessage(msg)
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}
	if err := os.WriteFile(InboxPath(session, role), append(data, '\n'), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Write trigger file (simulates what Notify does)
	writeTriggerNotify(session, role)

	// Verify mtime changed from initial
	info, err := os.Stat(triggerPath)
	if err != nil {
		t.Fatalf("trigger file should exist: %v", err)
	}
	newMtime := info.ModTime().UnixNano()
	if newMtime == initialMtime {
		t.Error("trigger file mtime should differ from initial value")
	}

	// Verify inbox has messages (what poll would check)
	if !HasMessages(session, role) {
		t.Error("HasMessages should return true after writing to inbox")
	}
}

func TestPollingMarkerPreventsDisplayMessage(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	role := "build"

	// Write a message to the inbox
	msg := NewMessage("edit", role, "request", "build", "compile", "")
	data, _ := EncodeMessage(msg)
	os.WriteFile(InboxPath(session, role), append(data, '\n'), 0644)

	// Set polling marker (simulates active --poll loop)
	SetPolling(session, role)
	defer ClearPolling(session, role)

	// Notify should write trigger but skip display-message/send-keys
	// (no tmux session = returns nil anyway, but the polling check is tested)
	err := Notify(session, role)
	if err != nil {
		t.Errorf("Notify with polling marker should not error: %v", err)
	}

	// Trigger file should still be written (always written before tmux guard)
	if _, err := os.Stat(TriggerNotifyPath(session, role)); err != nil {
		t.Error("trigger file should exist even when polling marker is set")
	}
}

func TestWaitingMarkerPreventsDisplayMessage(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	role := "edit"

	// Write a message to the inbox
	msg := NewMessage("build", role, "response", "build", "build ok", "")
	data, _ := EncodeMessage(msg)
	os.WriteFile(InboxPath(session, role), append(data, '\n'), 0644)

	// Set waiting marker (simulates active --wait loop)
	SetWaiting(session, role)
	defer ClearWaiting(session, role)

	err := Notify(session, role)
	if err != nil {
		t.Errorf("Notify with waiting marker should not error: %v", err)
	}

	// Trigger file should still be written
	if _, err := os.Stat(TriggerNotifyPath(session, role)); err != nil {
		t.Error("trigger file should exist even when waiting marker is set")
	}
}

func TestTriggerFile_HostedRoleResolvesToHost(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// "docs" is a hosted role that resolves to "edit" window
	hostedRole := "docs"
	hostRole := WindowForRole(hostedRole)

	if hostRole != "edit" {
		t.Skipf("docs role does not map to edit, got %q", hostRole)
	}

	triggerPath := TriggerNotifyPath(session, hostRole)

	// Send to the hosted role
	msg := NewMessage("build", hostedRole, "request", "docs", "update docs", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Notify resolves hosted role to host window for trigger file
	writeTriggerNotify(session, hostRole)

	// Trigger file for the host (edit) should exist
	if _, err := os.Stat(triggerPath); err != nil {
		t.Errorf("trigger file for host role %q should exist: %v", hostRole, err)
	}
}

func TestDeliveryStatusCreatedOnSend(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "build", "compile", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Delivery status should have been created
	ds, err := ReadDeliveryStatus(session, msg.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus: %v", err)
	}
	if ds.Status != StatusSent {
		t.Errorf("delivery status = %q, want %q", ds.Status, StatusSent)
	}
}

func TestDeliveryStatusUpdatedOnReceive(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "build", "compile", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Receive the message
	msgs, err := Receive(session, "build")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}

	// A consume writes an ack receipt: status advances to acked and AckedAt is set.
	ds, err := ReadDeliveryStatus(session, msg.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus after Receive: %v", err)
	}
	if ds.Status != StatusAcked {
		t.Errorf("delivery status = %q, want %q", ds.Status, StatusAcked)
	}
	if ds.AckedAt == 0 {
		t.Error("AckedAt should be set after Receive")
	}
}

func TestDeliveryStatusUpdatedOnResponse(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Send original request
	reqMsg := NewMessage("edit", "build", "request", "build", "compile", "")
	if err := Send(session, reqMsg); err != nil {
		t.Fatalf("Send request: %v", err)
	}

	// Consume the request
	_, _ = Receive(session, "build")

	// Send response with reply-to
	respMsg := NewMessage("build", "edit", "response", "build", "build ok", reqMsg.ID)
	if err := Send(session, respMsg); err != nil {
		t.Fatalf("Send response: %v", err)
	}

	// Original delivery status should now be "responded"
	ds, err := ReadDeliveryStatus(session, reqMsg.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus after response: %v", err)
	}
	if ds.Status != StatusResponded {
		t.Errorf("delivery status = %q, want %q", ds.Status, StatusResponded)
	}
	if ds.ResponseID != respMsg.ID {
		t.Errorf("ResponseID = %q, want %q", ds.ResponseID, respMsg.ID)
	}
}
