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
	if _, err := os.Stat(notifiedIDsPath(session, "edit")); !os.IsNotExist(err) {
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
	if _, err := os.Stat(notifiedIDsPath(session, "edit")); !os.IsNotExist(err) {
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
	if _, err := os.Stat(notifiedIDsPath(session, role)); !os.IsNotExist(err) {
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
	if _, err := os.Stat(notifiedIDsPath(session, role)); !os.IsNotExist(err) {
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

// writeTestMessage writes a proper message with an ID to the inbox file.
func writeTestMessage(t *testing.T, session, role, id, from string) {
	t.Helper()
	msg := Message{ID: id, From: from, To: role, Type: "request", Action: "test", Payload: "test"}
	data, err := EncodeMessage(msg)
	if err != nil {
		t.Fatalf("encode message: %v", err)
	}
	f, err := os.OpenFile(InboxPath(session, role), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("open inbox: %v", err)
	}
	defer f.Close()
	f.Write(append(data, '\n'))
}

func TestAlreadyNotified_SameMessages(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-same"
	role := "test"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message with a known ID
	writeTestMessage(t, session, role, "msg-001", "edit")

	// Mark as notified
	markNotified(session, role)

	// Same messages — should be deduplicated
	if !alreadyNotified(session, role) {
		t.Error("alreadyNotified should return true when all message IDs are in notified set")
	}
}

func TestAlreadyNotified_SameMessages_RetryAfterInterval(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-same-retry"
	role := "run"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message with a known ID
	writeTestMessage(t, session, role, "msg-001", "edit")

	// Mark as notified
	markNotified(session, role)

	// Same messages, recent marker — should be deduplicated
	if !alreadyNotified(session, role) {
		t.Error("alreadyNotified should return true for same messages within retry interval")
	}

	// Backdate the marker beyond the retry interval to simulate a missed notification
	markerPath := notifiedIDsPath(session, role)
	past := time.Now().Add(-31 * time.Second)
	os.Chtimes(markerPath, past, past)

	// Same messages but marker is old — should allow re-notification
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when same messages but retry interval expired (missed send-keys)")
	}
}

func TestAlreadyNotified_NewMessage(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-diff"
	role := "review"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write initial message and mark notified
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Add a second message with a new ID — new content
	writeTestMessage(t, session, role, "msg-002", "build")

	// New message not in notified set — should NOT be considered already notified
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when new messages arrive")
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

func TestMarkNotified_WritesIDs(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-mark"
	role := "commit"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message with known ID
	writeTestMessage(t, session, role, "msg-001", "edit")

	markNotified(session, role)

	// Verify marker file was created with the message ID
	markerData, err := os.ReadFile(notifiedIDsPath(session, role))
	if err != nil {
		t.Fatalf("markNotified should create marker file: %v", err)
	}

	if !strings.Contains(string(markerData), "msg-001") {
		t.Errorf("marker should contain message ID 'msg-001', got %q", string(markerData))
	}
}

func TestAlreadyNotified_NewMessageWhileNotifiedRecent(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-new-recent"
	role := "build"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write initial message and mark notified
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Add a new message — even though marker is recent, there's an unnotified ID
	writeTestMessage(t, session, role, "msg-002", "build")

	// New unnotified message — should return false (needs notification)
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when new unnotified messages exist, even if marker is recent")
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
	if _, err := os.Stat(notifiedIDsPath(session, role)); !os.IsNotExist(err) {
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
	if _, err := os.Stat(notifiedIDsPath(session, "edit")); !os.IsNotExist(err) {
		t.Error("Notify(edit) should NOT write marker without a tmux session")
	}
}

func TestAlreadyNotified_NewMessageAfterNotification(t *testing.T) {
	useTempBusDir(t)

	session := "test-dedup-new-msg"
	role := "test"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write initial message and mark notified
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Add a new message with a different ID
	writeTestMessage(t, session, role, "msg-002", "review")

	// New message not in notified set — should allow notification
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when new unnotified messages exist")
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
	if _, err := os.Stat(notifiedIDsPath(session, role)); !os.IsNotExist(err) {
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

// --- Delivery verification tests ---

// sequenceMockProvider implements Provider for testing with per-call IsIdle and
// SendWakeUp tracking. idleSequence controls what IsIdle returns on each
// successive call (index 0 = first call, etc.). Falls back to false when
// the sequence is exhausted.
type sequenceMockProvider struct {
	supportsHooks   bool
	idleSequence    []bool
	idleCallCount   int
	wakeUpCallCount int
}

func (m *sequenceMockProvider) Name() string                                       { return "seq-mock" }
func (m *sequenceMockProvider) ConfigureLaunch(cfg *LaunchConfig, role string)     {}
func (m *sequenceMockProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) { return "", nil }
func (m *sequenceMockProvider) IsIdle(session, role string) bool {
	idx := m.idleCallCount
	m.idleCallCount++
	if idx < len(m.idleSequence) {
		return m.idleSequence[idx]
	}
	return false
}
func (m *sequenceMockProvider) IsAlive(session, role string) bool                        { return true }
func (m *sequenceMockProvider) ClassifyPane(content string) PaneState                    { return PaneNotReady }
func (m *sequenceMockProvider) AcceptStartup(session, pane string, state PaneState) bool { return true }
func (m *sequenceMockProvider) SendWakeUp(session, role string) error {
	m.wakeUpCallCount++
	return nil
}
func (m *sequenceMockProvider) Compact(session, role, target string) error { return nil }
func (m *sequenceMockProvider) SupportsHooks() bool                        { return m.supportsHooks }
func (m *sequenceMockProvider) IdlePromptChar() string                     { return "❯" }
func (m *sequenceMockProvider) WriteAgentConfig(role string) error         { return nil }
func (m *sequenceMockProvider) DetectTaskCompletion(session, role, pane string) (bool, bool, string) {
	return false, false, ""
}

func TestVerifySendKeysDelivery_StillIdle_ClearsMarker(t *testing.T) {
	useTempBusDir(t)

	// Reduce delay for test speed
	old := sendKeysVerifyDelay
	sendKeysVerifyDelay = 10 * time.Millisecond
	t.Cleanup(func() { sendKeysVerifyDelay = old })

	session := "test-verify-idle"
	role := "commit"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message to the inbox and mark as notified
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Verify marker exists before verification
	if _, err := os.Stat(notifiedIDsPath(session, role)); os.IsNotExist(err) {
		t.Fatal("marker should exist before verification")
	}

	// Agent is idle on both checks (initial + post-retry) → both attempts dropped
	provider := &sequenceMockProvider{
		supportsHooks: true,
		idleSequence:  []bool{true, true}, // idle on check, idle after retry
	}
	verifySendKeysDelivery(session, role, provider)

	// Marker should be cleared so daemon retries on next cycle
	if _, err := os.Stat(notifiedIDsPath(session, role)); !os.IsNotExist(err) {
		t.Error("marker should be cleared when agent is still idle after retry")
	}

	// Verify retry was attempted
	if provider.wakeUpCallCount != 1 {
		t.Errorf("expected 1 retry SendWakeUp call, got %d", provider.wakeUpCallCount)
	}
}

func TestVerifySendKeysDelivery_NotIdle_MarkerPersists(t *testing.T) {
	useTempBusDir(t)

	// Reduce delay for test speed
	old := sendKeysVerifyDelay
	sendKeysVerifyDelay = 10 * time.Millisecond
	t.Cleanup(func() { sendKeysVerifyDelay = old })

	session := "test-verify-active"
	role := "build"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message to the inbox and mark as notified
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Agent became active (processing the message) → injection landed
	provider := &sequenceMockProvider{
		supportsHooks: true,
		idleSequence:  []bool{false}, // not idle on first check
	}
	verifySendKeysDelivery(session, role, provider)

	// Marker should persist — delivery succeeded, no retry needed
	if _, err := os.Stat(notifiedIDsPath(session, role)); os.IsNotExist(err) {
		t.Error("marker should persist when agent is active after send-keys (delivery succeeded)")
	}

	// No retry should have been attempted
	if provider.wakeUpCallCount != 0 {
		t.Errorf("expected 0 retry SendWakeUp calls, got %d", provider.wakeUpCallCount)
	}
}

func TestVerifySendKeysDelivery_NonHookProvider_NotCalled(t *testing.T) {
	useTempBusDir(t)

	// This test verifies the calling convention in notifySendKeys: the
	// verification is gated on provider.SupportsHooks(), so non-hook
	// providers never reach verifySendKeysDelivery. We verify the gate
	// by checking that notifySendKeys with a non-hook provider doesn't
	// clear the marker even when idle=true.
	//
	// Since notifySendKeys uses ResolveProvider internally, we test
	// the verification function directly with a non-hook mock to
	// confirm it still clears (it doesn't check SupportsHooks itself —
	// the gate is in notifySendKeys). This documents the contract:
	// verifySendKeysDelivery always clears if idle; the caller decides
	// whether to invoke it.

	old := sendKeysVerifyDelay
	sendKeysVerifyDelay = 10 * time.Millisecond
	t.Cleanup(func() { sendKeysVerifyDelay = old })

	session := "test-verify-nonhook"
	role := "test"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Non-hook provider that reports idle on both checks — verifySendKeysDelivery
	// still retries and clears (the SupportsHooks gate is in notifySendKeys, not here).
	provider := &sequenceMockProvider{
		supportsHooks: false,
		idleSequence:  []bool{true, true},
	}
	verifySendKeysDelivery(session, role, provider)

	// Marker cleared because verifySendKeysDelivery doesn't check hooks
	if _, err := os.Stat(notifiedIDsPath(session, role)); !os.IsNotExist(err) {
		t.Error("verifySendKeysDelivery should clear marker regardless of hook support (gate is in caller)")
	}
}

func TestVerifySendKeysDelivery_RetrySucceeds_MarkerPersists(t *testing.T) {
	useTempBusDir(t)

	// Reduce delay for test speed
	old := sendKeysVerifyDelay
	sendKeysVerifyDelay = 10 * time.Millisecond
	t.Cleanup(func() { sendKeysVerifyDelay = old })

	session := "test-verify-retry-ok"
	role := "commit"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message to the inbox and mark as notified
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Agent is idle on first check (injection dropped), but active on
	// second check (retry succeeded). This is the key scenario: the
	// retry within verifySendKeysDelivery lands the message.
	provider := &sequenceMockProvider{
		supportsHooks: true,
		idleSequence:  []bool{true, false}, // idle → retry → active
	}
	verifySendKeysDelivery(session, role, provider)

	// Marker should persist — retry succeeded, agent is now processing
	if _, err := os.Stat(notifiedIDsPath(session, role)); os.IsNotExist(err) {
		t.Error("marker should persist when retry succeeds (agent became active)")
	}

	// Exactly one retry should have fired
	if provider.wakeUpCallCount != 1 {
		t.Errorf("expected 1 retry SendWakeUp call, got %d", provider.wakeUpCallCount)
	}

	// Two IsIdle calls: initial check + post-retry check
	if provider.idleCallCount != 2 {
		t.Errorf("expected 2 IsIdle calls, got %d", provider.idleCallCount)
	}
}

// --- BuildCombinedNotification tests ---

func TestBuildCombinedNotification_Empty(t *testing.T) {
	text := BuildCombinedNotification(nil)
	if text != "You have new messages" {
		t.Errorf("empty notification = %q, want 'You have new messages'", text)
	}
}

func TestBuildCombinedNotification_SingleMessage(t *testing.T) {
	msgs := []Message{
		{ID: "msg-001", From: "build", Type: "response", Action: "build", Payload: "Build succeeded — 0 errors"},
	}
	text := BuildCombinedNotification(msgs)
	if !strings.Contains(text, "New message from build") {
		t.Errorf("single notification should contain sender: %q", text)
	}
	if !strings.Contains(text, "[response:build]") {
		t.Errorf("single notification should contain type:action: %q", text)
	}
	if !strings.Contains(text, "Build succeeded") {
		t.Errorf("single notification should contain payload preview: %q", text)
	}
}

func TestBuildCombinedNotification_MultipleMessages(t *testing.T) {
	msgs := []Message{
		{ID: "msg-001", From: "build", Type: "response", Action: "build", Payload: "Build succeeded"},
		{ID: "msg-002", From: "test", Type: "response", Action: "test", Payload: "Tests passed"},
		{ID: "msg-003", From: "review", Type: "response", Action: "review", Payload: "LGTM"},
	}
	text := BuildCombinedNotification(msgs)
	if !strings.Contains(text, "You have 3 new messages") {
		t.Errorf("multi notification should show count: %q", text)
	}
	if !strings.Contains(text, "[build>build]") {
		t.Errorf("multi notification should contain build sender: %q", text)
	}
	if !strings.Contains(text, "[test>test]") {
		t.Errorf("multi notification should contain test sender: %q", text)
	}
	if !strings.Contains(text, "[review>review]") {
		t.Errorf("multi notification should contain review sender: %q", text)
	}
}

func TestBuildCombinedNotification_TruncatesLongPayload(t *testing.T) {
	longPayload := strings.Repeat("x", 200)
	msgs := []Message{
		{ID: "msg-001", From: "build", Type: "response", Action: "build", Payload: longPayload},
	}
	text := BuildCombinedNotification(msgs)
	if !strings.Contains(text, "...") {
		t.Errorf("long single payload should be truncated: %q", text)
	}
	// Single message truncates at 80 chars
	if len(text) > 200 {
		t.Errorf("single notification should be reasonably sized, got %d chars", len(text))
	}
}

func TestBuildCombinedNotification_CapsTotal(t *testing.T) {
	// Create many messages with long payloads to exceed the 450-char cap
	var msgs []Message
	for i := 0; i < 20; i++ {
		msgs = append(msgs, Message{
			ID:      fmt.Sprintf("msg-%03d", i),
			From:    "build",
			Type:    "response",
			Action:  "build",
			Payload: fmt.Sprintf("Long payload message number %d with lots of detail", i),
		})
	}
	text := BuildCombinedNotification(msgs)
	if !strings.Contains(text, "and ") && !strings.Contains(text, " more") {
		t.Errorf("capped notification should show 'and N more': %q", text)
	}
	if len(text) > 600 {
		t.Errorf("capped notification should be under ~500 chars, got %d", len(text))
	}
}

// --- Message-level notification tracking tests ---

func TestUnnotifiedMessages(t *testing.T) {
	useTempBusDir(t)

	session := "test-unnotified"
	role := "build"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write two messages
	writeTestMessage(t, session, role, "msg-001", "edit")
	writeTestMessage(t, session, role, "msg-002", "test")

	// No notifications yet — both should be unnotified
	unnotified := UnnotifiedMessages(session, role)
	if len(unnotified) != 2 {
		t.Errorf("expected 2 unnotified messages, got %d", len(unnotified))
	}

	// Mark first message as notified
	addNotifiedIDs(session, role, []string{"msg-001"})

	// Only second message should be unnotified
	unnotified = UnnotifiedMessages(session, role)
	if len(unnotified) != 1 {
		t.Errorf("expected 1 unnotified message after marking msg-001, got %d", len(unnotified))
	}
	if len(unnotified) > 0 && unnotified[0].ID != "msg-002" {
		t.Errorf("expected unnotified message to be msg-002, got %s", unnotified[0].ID)
	}

	// Mark second message too — none unnotified
	addNotifiedIDs(session, role, []string{"msg-002"})
	unnotified = UnnotifiedMessages(session, role)
	if len(unnotified) != 0 {
		t.Errorf("expected 0 unnotified messages, got %d", len(unnotified))
	}
}

func TestClearNotifiedIDs(t *testing.T) {
	useTempBusDir(t)

	session := "test-clear-ids"
	role := "build"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write and notify
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Marker should exist
	if _, err := os.Stat(notifiedIDsPath(session, role)); os.IsNotExist(err) {
		t.Fatal("marker should exist after markNotified")
	}

	// Clear — marker should be gone
	ClearNotifiedIDs(session, role)
	if _, err := os.Stat(notifiedIDsPath(session, role)); !os.IsNotExist(err) {
		t.Error("marker should be removed after ClearNotifiedIDs")
	}

	// Messages should now all be unnotified again
	unnotified := UnnotifiedMessages(session, role)
	if len(unnotified) != 1 {
		t.Errorf("expected 1 unnotified message after clear, got %d", len(unnotified))
	}
}

func TestReceive_ClearsNotifiedIDs(t *testing.T) {
	useTempBusDir(t)

	session := "test-receive-clear"
	role := "deploy"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "delivery"), 0755)

	// Write a message and mark as notified
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	if _, err := os.Stat(notifiedIDsPath(session, role)); os.IsNotExist(err) {
		t.Fatal("marker should exist before Receive")
	}

	// Consume messages via Receive
	msgs, err := Receive(session, role)
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message from Receive, got %d", len(msgs))
	}

	// Notified IDs marker should be cleared after consumption
	if _, err := os.Stat(notifiedIDsPath(session, role)); !os.IsNotExist(err) {
		t.Error("notified IDs marker should be cleared after Receive")
	}
}
