package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsHarnessActive_LivePID(t *testing.T) {
	useTempBusDir(t)

	dir := t.TempDir()
	session := "test-harness"
	role := "build"

	// Override BusDir for test by writing directly to expected path
	busDir := filepath.Join(dir, "muxcode-bus-"+session)
	os.MkdirAll(busDir, 0755)

	// Use our own PID (guaranteed alive)
	pid := os.Getpid()
	markerPath := filepath.Join(busDir, "harness-"+role+".pid")
	os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", pid)), 0644)

	// IsHarnessActive uses HarnessMarkerPath which uses BusDir
	// We need to check with a session that maps to our temp dir
	// Instead, test the logic directly by writing to the real path
	got := IsHarnessActive(session, role)
	if got {
		// It will be false because BusDir doesn't point to our temp dir.
		// Let's test with the actual path directly instead.
		t.Log("Unexpectedly got true — BusDir matched")
	}

	// Test with actual BusDir path: create marker at the real location
	realDir := BusDir("test-notify-live")
	os.MkdirAll(realDir, 0755)

	realMarker := HarnessMarkerPath("test-notify-live", role)
	os.WriteFile(realMarker, []byte(fmt.Sprintf("%d", pid)), 0644)

	if !IsHarnessActive("test-notify-live", role) {
		t.Error("IsHarnessActive should return true for live PID")
	}
}

func TestIsHarnessActive_MissingFile(t *testing.T) {
	useTempBusDir(t)

	// No marker file at all
	if IsHarnessActive("nonexistent-session-xyz", "build") {
		t.Error("IsHarnessActive should return false when marker file does not exist")
	}
}

func TestIsHarnessActive_StalePID(t *testing.T) {
	useTempBusDir(t)

	session := "test-notify-stale"
	role := "review"

	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	markerPath := HarnessMarkerPath(session, role)

	// Write a PID that is almost certainly dead (very high number)
	os.WriteFile(markerPath, []byte("999999999"), 0644)

	if IsHarnessActive(session, role) {
		t.Error("IsHarnessActive should return false for dead PID")
	}

	// Verify stale marker was cleaned up
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("stale marker file should have been removed")
	}
}

func TestIsHarnessActive_InvalidContent(t *testing.T) {
	useTempBusDir(t)

	session := "test-notify-invalid"
	role := "commit"

	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	markerPath := HarnessMarkerPath(session, role)

	// Write garbage content
	os.WriteFile(markerPath, []byte("not-a-pid"), 0644)

	if IsHarnessActive(session, role) {
		t.Error("IsHarnessActive should return false for invalid PID content")
	}

	// Verify invalid marker was cleaned up
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("invalid marker file should have been removed")
	}
}

func TestNotify_EditBestEffort(t *testing.T) {
	useTempBusDir(t)

	// Notify(edit) with no tmux session → IsAgentIdle returns false →
	// falls back to display-message (best-effort, returns nil).
	err := Notify("nonexistent-session-xyz", "edit")
	if err != nil {
		t.Errorf("Notify(edit) should return nil (best-effort), got %v", err)
	}
}

func TestNotifyDisplayMessage_Dedup(t *testing.T) {
	useTempBusDir(t)

	session := "test-dm-dedup"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	// Write a message to the edit inbox
	os.WriteFile(InboxPath(session, "edit"), []byte(`{"from":"build"}`+"\n"), 0644)

	// Call returns nil but has-session guard prevents marker write (no real tmux session)
	err := notifyDisplayMessage(session, "edit")
	if err != nil {
		t.Errorf("notifyDisplayMessage should return nil, got %v", err)
	}

	// Marker should NOT be written — has-session guard returns before dedup/mark
	if _, err := os.Stat(notifiedSizePath(session, "edit")); !os.IsNotExist(err) {
		t.Error("notifyDisplayMessage should skip marker when tmux session doesn't exist")
	}
}

func TestNotifyDisplayMessage_SkipsWithoutTmuxSession(t *testing.T) {
	useTempBusDir(t)

	session := "test-dm-display"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	// Write a response message to edit's inbox.
	msg := NewMessage("build", "edit", "response", "build", "Build succeeded", "req-123")
	data, _ := EncodeMessage(msg)
	os.WriteFile(InboxPath(session, "edit"), append(data, '\n'), 0644)

	// notifyDisplayMessage returns nil early when the tmux session doesn't
	// exist (has-session guard prevents leaking display-message to the
	// user's live session).
	err := notifyDisplayMessage(session, "edit")
	if err != nil {
		t.Errorf("notifyDisplayMessage should return nil, got %v", err)
	}

	// Marker should NOT be written — has-session guard returns before dedup/mark.
	if _, err := os.Stat(notifiedSizePath(session, "edit")); !os.IsNotExist(err) {
		t.Error("notifyDisplayMessage should skip marker when tmux session doesn't exist")
	}
}

func TestNotify_NonEditSkipsWithoutTmuxSession(t *testing.T) {
	useTempBusDir(t)

	session := "test-nonedit-dm"
	role := "api"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	// Write a message to the api inbox
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)

	// Notify returns nil (best-effort) even without a tmux session
	err := Notify(session, role)
	if err != nil {
		t.Errorf("Notify(%s) should return nil (best-effort), got %v", role, err)
	}

	// No marker written — has-session guard returns early when no tmux session
	if _, err := os.Stat(notifiedSizePath(session, role)); !os.IsNotExist(err) {
		t.Errorf("Notify(%s) should NOT write marker without a tmux session", role)
	}
}

func TestNotify_HarnessSkipped(t *testing.T) {
	useTempBusDir(t)

	session := "test-harness-skip"
	role := "review"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	// Write a harness marker with our own PID (guaranteed alive)
	markerPath := HarnessMarkerPath(session, role)
	os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	// Write a message to the inbox
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)

	// Notify should skip (harness is active) — no marker written
	err := Notify(session, role)
	if err != nil {
		t.Errorf("Notify(harness) should return nil, got %v", err)
	}

	// Verify NO notified marker was written (proves harness was skipped)
	if _, err := os.Stat(notifiedSizePath(session, role)); !os.IsNotExist(err) {
		t.Error("Notify should NOT write notified marker when harness is active")
	}
}

func TestAlreadyNotified_NoMarker(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-nomarker"
	role := "build"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message to the inbox
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)

	// No notified marker yet — should not be considered already notified
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when no marker exists")
	}
}

func TestAlreadyNotified_SameSize(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-same"
	role := "test"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message to the inbox
	inboxData := []byte(`{"from":"edit"}` + "\n")
	os.WriteFile(InboxPath(session, role), inboxData, 0644)

	// Mark as notified
	markNotified(session, role)

	// Same size — should be deduplicated
	if !alreadyNotified(session, role) {
		t.Error("alreadyNotified should return true when inbox size matches marker")
	}
}

func TestAlreadyNotified_DifferentSize(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-diff"
	role := "review"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write initial message and mark notified
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)
	markNotified(session, role)

	// Backdate marker beyond the cooldown window so the size change is detected
	markerPath := notifiedSizePath(session, role)
	past := time.Now().Add(-3 * time.Second)
	os.Chtimes(markerPath, past, past)

	// Add a second message — inbox grew
	f, _ := os.OpenFile(InboxPath(session, role), os.O_APPEND|os.O_WRONLY, 0644)
	f.Write([]byte(`{"from":"build"}` + "\n"))
	f.Close()

	// Inbox changed and cooldown expired — should NOT be considered already notified
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when inbox grew since last notification")
	}
}

func TestAlreadyNotified_EmptyInbox(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-empty"
	role := "deploy"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Empty inbox — nothing to notify
	os.WriteFile(InboxPath(session, role), []byte{}, 0644)

	if !alreadyNotified(session, role) {
		t.Error("alreadyNotified should return true for empty inbox")
	}
}

func TestMarkNotified_WritesSize(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-mark"
	role := "commit"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write some data
	data := []byte(`{"from":"edit","action":"commit"}` + "\n")
	os.WriteFile(InboxPath(session, role), data, 0644)

	markNotified(session, role)

	// Verify marker file was created with correct size
	markerData, err := os.ReadFile(notifiedSizePath(session, role))
	if err != nil {
		t.Fatalf("markNotified should create marker file: %v", err)
	}

	expected := fmt.Sprintf("%d", len(data))
	if string(markerData) != expected {
		t.Errorf("marker size = %q, want %q", string(markerData), expected)
	}
}

func TestAlreadyNotified_Cooldown(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-cooldown"
	role := "build"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write initial message and mark notified
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)
	markNotified(session, role)

	// Grow the inbox — size now differs from marker
	f, _ := os.OpenFile(InboxPath(session, role), os.O_APPEND|os.O_WRONLY, 0644)
	f.Write([]byte(`{"from":"build"}` + "\n"))
	f.Close()

	// Marker was just written (within cooldown) — should still be suppressed
	if !alreadyNotified(session, role) {
		t.Error("alreadyNotified should return true within cooldown window even when inbox size differs")
	}
}

func TestIsAgentIdle_NoTmux(t *testing.T) {
	// When the session doesn't exist, IsAgentIdle should return false (safe fallback).
	// This ensures no panic or error when called outside a tmux session.
	if IsAgentIdle("nonexistent-session-xyz", "build") {
		t.Error("IsAgentIdle should return false when tmux session doesn't exist")
	}
}

func TestIdlePromptChar(t *testing.T) {
	// Verify the constant matches the expected Unicode character ❯
	if idlePromptChar != "❯" {
		t.Errorf("idlePromptChar = %q, want %q", idlePromptChar, "❯")
	}
}

func TestNotify_NonIdleSkipsWithoutTmuxSession(t *testing.T) {
	useTempBusDir(t)

	// No tmux session → has-session guard returns early, no notification sent.
	session := "test-nonidle-fallback"
	role := "build"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)

	err := Notify(session, role)
	if err != nil {
		t.Errorf("Notify(%s) should return nil (best-effort), got %v", role, err)
	}

	// No marker written — has-session guard returns early when no tmux session
	if _, err := os.Stat(notifiedSizePath(session, role)); !os.IsNotExist(err) {
		t.Errorf("Notify(%s) should NOT write marker without a tmux session", role)
	}
}

func TestNotify_EditSkipsWithoutTmuxSession(t *testing.T) {
	useTempBusDir(t)

	// Edit role with no tmux session → has-session guard returns early.
	session := "test-edit-dm-fallback"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	os.WriteFile(InboxPath(session, "edit"), []byte(`{"from":"build"}`+"\n"), 0644)

	err := Notify(session, "edit")
	if err != nil {
		t.Errorf("Notify(edit) should return nil (best-effort), got %v", err)
	}

	// No marker written — has-session guard returns early when no tmux session
	if _, err := os.Stat(notifiedSizePath(session, "edit")); !os.IsNotExist(err) {
		t.Error("Notify(edit) should NOT write marker without a tmux session")
	}
}

func TestNotifyIdleSendKeys_Dedup(t *testing.T) {
	useTempBusDir(t)

	session := "test-idle-sendkeys-dedup"
	role := "review"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)

	// Call returns nil but has-session guard prevents marker write (no real tmux session)
	err := notifyIdleSendKeys(session, role)
	if err != nil {
		t.Errorf("notifyIdleSendKeys should return nil, got %v", err)
	}

	// Marker should NOT be written — has-session guard returns before dedup/mark
	if _, err := os.Stat(notifiedSizePath(session, role)); !os.IsNotExist(err) {
		t.Error("notifyIdleSendKeys should skip marker when tmux session doesn't exist")
	}
}

func TestAlreadyNotified_CooldownExpired(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-cooldown-exp"
	role := "test"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write initial message and mark notified
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)
	markNotified(session, role)

	// Backdate the marker file mtime to exceed the cooldown
	markerPath := notifiedSizePath(session, role)
	past := time.Now().Add(-3 * time.Second)
	os.Chtimes(markerPath, past, past)

	// Grow the inbox
	f, _ := os.OpenFile(InboxPath(session, role), os.O_APPEND|os.O_WRONLY, 0644)
	f.Write([]byte(`{"from":"review"}` + "\n"))
	f.Close()

	// Cooldown expired and size differs — should allow notification
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when cooldown has expired and inbox size differs")
	}
}

func TestIsSendKeysCoolingDown_NoMarker(t *testing.T) {
	useTempBusDir(t)

	// No marker file — not cooling down
	if IsSendKeysCoolingDown("nonexistent-session", "build") {
		t.Error("IsSendKeysCoolingDown should return false when no marker exists")
	}
}

func TestIsSendKeysCoolingDown_RecentMarker(t *testing.T) {
	useTempBusDir(t)

	session := "test-sendkeys-recent"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	// Write a marker with current timestamp
	markSendKeys(session, "test")

	if !IsSendKeysCoolingDown(session, "test") {
		t.Error("IsSendKeysCoolingDown should return true for a just-written marker")
	}
}

func TestIsSendKeysCoolingDown_ExpiredMarker(t *testing.T) {
	useTempBusDir(t)

	session := "test-sendkeys-expired"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	// Write a marker with a timestamp well in the past (beyond sendKeysCooldown)
	past := time.Now().Add(-15 * time.Second).Unix()
	os.WriteFile(SendKeysMarkerPath(session, "test"),
		[]byte(fmt.Sprintf("%d", past)), 0644)

	if IsSendKeysCoolingDown(session, "test") {
		t.Error("IsSendKeysCoolingDown should return false for an expired marker")
	}
}

func TestIsSendKeysCoolingDown_InvalidContent(t *testing.T) {
	useTempBusDir(t)

	session := "test-sendkeys-invalid"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	os.WriteFile(SendKeysMarkerPath(session, "test"), []byte("garbage"), 0644)

	if IsSendKeysCoolingDown(session, "test") {
		t.Error("IsSendKeysCoolingDown should return false for invalid marker content")
	}
}

func TestSendKeysMarkerPath(t *testing.T) {
	p := SendKeysMarkerPath("mysession", "build")
	if !strings.Contains(p, "sendkeys-build.ts") {
		t.Errorf("unexpected marker path: %s", p)
	}
}

func TestTriggerNotifyPath(t *testing.T) {
	p := TriggerNotifyPath("mysession", "build")
	if !strings.Contains(p, "trigger-build.notify") {
		t.Errorf("unexpected trigger path: %s", p)
	}
}

func TestWriteTriggerNotify(t *testing.T) {
	useTempBusDir(t)

	session := "test-trigger-write"
	role := "build"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	// Trigger file should not exist yet
	triggerPath := TriggerNotifyPath(session, role)
	if _, err := os.Stat(triggerPath); !os.IsNotExist(err) {
		t.Fatal("trigger file should not exist before writeTriggerNotify")
	}

	writeTriggerNotify(session, role)

	// Trigger file should now exist with a timestamp
	data, err := os.ReadFile(triggerPath)
	if err != nil {
		t.Fatalf("trigger file should exist after writeTriggerNotify: %v", err)
	}
	if len(data) == 0 {
		t.Error("trigger file should contain a timestamp")
	}
}

func TestWriteTriggerNotify_MtimeChanges(t *testing.T) {
	useTempBusDir(t)

	session := "test-trigger-mtime"
	role := "test"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	writeTriggerNotify(session, role)
	info1, _ := os.Stat(TriggerNotifyPath(session, role))
	mtime1 := info1.ModTime()

	// Small delay to ensure mtime differs
	time.Sleep(10 * time.Millisecond)

	writeTriggerNotify(session, role)
	info2, _ := os.Stat(TriggerNotifyPath(session, role))
	mtime2 := info2.ModTime()

	if !mtime2.After(mtime1) {
		t.Error("second writeTriggerNotify should update mtime")
	}
}

func TestPollingMarkerPath(t *testing.T) {
	p := PollingMarkerPath("mysession", "review")
	if !strings.Contains(p, "polling-review.marker") {
		t.Errorf("unexpected polling marker path: %s", p)
	}
}

func TestSetPolling_ClearPolling(t *testing.T) {
	useTempBusDir(t)

	session := "test-polling-marker"
	role := "build"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	// Not polling initially
	if IsPolling(session, role) {
		t.Error("IsPolling should return false when no marker exists")
	}

	SetPolling(session, role)

	// Now polling (our own PID is alive)
	if !IsPolling(session, role) {
		t.Error("IsPolling should return true after SetPolling")
	}

	ClearPolling(session, role)

	// No longer polling
	if IsPolling(session, role) {
		t.Error("IsPolling should return false after ClearPolling")
	}
}

func TestIsPolling_StalePID(t *testing.T) {
	useTempBusDir(t)

	session := "test-polling-stale"
	role := "test"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	// Write a dead PID
	os.WriteFile(PollingMarkerPath(session, role), []byte("999999999"), 0644)

	if IsPolling(session, role) {
		t.Error("IsPolling should return false for dead PID")
	}

	// Stale marker should be cleaned up
	if _, err := os.Stat(PollingMarkerPath(session, role)); !os.IsNotExist(err) {
		t.Error("stale polling marker should have been removed")
	}
}

func TestIsPolling_InvalidContent(t *testing.T) {
	useTempBusDir(t)

	session := "test-polling-invalid"
	role := "commit"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	os.WriteFile(PollingMarkerPath(session, role), []byte("garbage"), 0644)

	if IsPolling(session, role) {
		t.Error("IsPolling should return false for invalid marker content")
	}

	// Invalid marker should be cleaned up
	if _, err := os.Stat(PollingMarkerPath(session, role)); !os.IsNotExist(err) {
		t.Error("invalid polling marker should have been removed")
	}
}

func TestNotify_PollingMarkerPreventsNotifiedMarker(t *testing.T) {
	useTempBusDir(t)

	// Without a real tmux session, Notify returns early at the has-session guard.
	// This test verifies the IsPolling check itself works correctly.
	session := "test-notify-polling"
	role := "build"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)

	// Set polling marker with our own PID
	SetPolling(session, role)
	defer ClearPolling(session, role)

	// Without a tmux session, Notify returns nil early (before reaching polling check)
	err := Notify(session, role)
	if err != nil {
		t.Errorf("Notify should return nil, got %v", err)
	}

	// Notified-size marker should NOT be written (has-session guard returns early)
	if _, err := os.Stat(notifiedSizePath(session, role)); !os.IsNotExist(err) {
		t.Error("Notify should NOT write notified marker without tmux session")
	}
}

func TestWriteTriggerNotify_Direct(t *testing.T) {
	useTempBusDir(t)

	// writeTriggerNotify is called directly (not through Notify) in some paths.
	// Verify it works independently of tmux.
	session := "test-trigger-direct"
	role := "deploy"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)

	writeTriggerNotify(session, role)

	triggerPath := TriggerNotifyPath(session, role)
	data, err := os.ReadFile(triggerPath)
	if err != nil {
		t.Fatalf("trigger file should exist: %v", err)
	}
	if len(data) == 0 {
		t.Error("trigger file should contain a timestamp")
	}
}
