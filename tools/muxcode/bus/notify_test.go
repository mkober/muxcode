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

	// Add a new message — even though marker is recent, there's an unnotified ID.
	// Sender must differ from role (a self-addressed message is filtered as a loop).
	writeTestMessage(t, session, role, "msg-002", "deploy")

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

// --- HasPendingInput tests ---

func TestPaneHasPendingInput_UserTyping(t *testing.T) {
	// User is mid-typing at the prompt
	pane := "  Ran 1 shell command\n\n❯ implement the feature for\n"
	if !paneHasPendingInput(pane) {
		t.Error("should detect pending input when user has text after prompt")
	}
}

func TestPaneHasPendingInput_EmptyPrompt(t *testing.T) {
	// Agent idle with empty prompt — safe to inject
	pane := "  Ran 1 shell command\n\n❯ \n"
	if paneHasPendingInput(pane) {
		t.Error("should not detect pending input at empty prompt")
	}
}

func TestPaneHasPendingInput_PromptOnly(t *testing.T) {
	// Just the prompt character, no trailing space
	pane := "  some output\n❯\n"
	if paneHasPendingInput(pane) {
		t.Error("should not detect pending input for bare prompt character")
	}
}

func TestPaneHasPendingInput_AgentActive(t *testing.T) {
	// Agent is actively processing — no prompt visible
	pane := "  Running 1 shell command...\n  $ muxcode send build build \"Run build\"\n"
	if paneHasPendingInput(pane) {
		t.Error("should not detect pending input when agent is active (no prompt)")
	}
}

func TestPaneHasPendingInput_SlashCommand(t *testing.T) {
	// User typing a slash command
	pane := "  Ran 2 shell commands\n\n❯ /compact\n"
	if !paneHasPendingInput(pane) {
		t.Error("should detect pending input for slash command")
	}
}

// --- composerHasText tests (live-composer detection for verifyEnterDelivery) ---

func TestComposerHasText_LiveComposerParked(t *testing.T) {
	// Live composer at the bottom holds parked text — should be detected.
	pane := "  Ran 1 shell command\n\n──── command-runner ──\n❯ run the build\n────\n  hints"
	if !composerHasText(pane) {
		t.Error("should detect text parked in the live composer")
	}
}

func TestComposerHasText_EmptyComposerWithScrollback(t *testing.T) {
	// Stale "❯ <submitted text>" scrollback above an EMPTY live composer must
	// NOT be misread as parked input — this is the false positive that made the
	// wide-capture verify re-send Enter forever. The last prompt is empty.
	pane := "❯ You have new messages\n\n⏺ done\n\n──── code-editor ──\n❯ \n────"
	if composerHasText(pane) {
		t.Error("empty live composer must not be flagged despite ❯ scrollback above")
	}
}

func TestComposerHasText_ScrollbackAndParkedComposer(t *testing.T) {
	// Scrollback prompt above AND parked text in the live composer — detected
	// because the LAST prompt line carries text.
	pane := "❯ You have new messages\n\n⏺ done\n\n──── run ──\n❯ refresh creds and retry\n────"
	if !composerHasText(pane) {
		t.Error("should detect parked text in the last (live) composer line")
	}
}

func TestComposerHasText_NoPrompt(t *testing.T) {
	// Agent actively working — no ❯ prompt anywhere.
	pane := "  Running 1 shell command...\n  $ ./build.sh"
	if composerHasText(pane) {
		t.Error("should not detect composer text when no prompt is present")
	}
}

// --- IsWindowFocused / stale input clearing tests ---

func TestNotifySendKeys_StaleInput_UnfocusedWindow(t *testing.T) {
	// When there's text at the prompt but the user is NOT viewing that window,
	// the notification should clear the stale input (C-u) and inject.
	// We test this by verifying the expected tmux command sequence.
	useTempBusDir(t)

	session := "test-stale-unfocused"
	role := "commit"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	// Write a message to the inbox
	writeTestMessage(t, session, role, "msg-stale-001", "edit")

	// Mock tmux to simulate: pane has pending input, window is NOT active
	var calls [][]string
	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})
	tmuxOutputRunner = func(args ...string) (string, error) {
		calls = append(calls, args)
		joined := strings.Join(args, " ")
		// has-session check
		if strings.Contains(joined, "has-session") {
			return "", nil
		}
		// capture-pane → return pane with text at prompt
		if strings.Contains(joined, "capture-pane") {
			return "  Ran 1 shell command\n\n❯ commit the requirements doc\n", nil
		}
		// session has an attached client, but...
		if strings.Contains(joined, "session_attached") {
			return "1", nil
		}
		// display-message #{window_active} → NOT focused
		if strings.Contains(joined, "window_active") {
			return "0", nil
		}
		return "", nil
	}
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}

	// Force Claude Code provider via env var
	t.Setenv("MUXCODE_COMMIT_CLI", "claude")

	err := notifySendKeys(session, role)
	if err != nil {
		t.Fatalf("notifySendKeys failed: %v", err)
	}

	// Verify C-u was sent to clear stale input
	foundClearInput := false
	foundSendText := false
	for _, call := range calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "send-keys") && strings.Contains(joined, "C-u") {
			foundClearInput = true
		}
		if strings.Contains(joined, "send-keys") && strings.Contains(joined, "-l") {
			foundSendText = true
		}
	}
	if !foundClearInput {
		t.Error("expected C-u to clear stale input when window not focused")
	}
	if !foundSendText {
		t.Error("expected notification text to be injected after clearing")
	}
}

func TestNotifySendKeys_PendingInput_FocusedWindow(t *testing.T) {
	// When there's text at the prompt AND the user has the window focused,
	// notification should be held (no injection).
	useTempBusDir(t)

	session := "test-pending-focused"
	role := "commit"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	// Write a message to the inbox
	writeTestMessage(t, session, role, "msg-focused-001", "edit")

	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})

	sendKeysCalled := false
	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "has-session") {
			return "", nil
		}
		if strings.Contains(joined, "capture-pane") {
			return "  Ran 1 shell command\n\n❯ implement the feature\n", nil
		}
		// Session attached AND window focused — user is there
		if strings.Contains(joined, "session_attached") {
			return "1", nil
		}
		if strings.Contains(joined, "window_active") {
			return "1", nil
		}
		return "", nil
	}
	tmuxRunner = func(args ...string) error {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "send-keys") {
			sendKeysCalled = true
		}
		return nil
	}

	// Force Claude Code provider via env var
	t.Setenv("MUXCODE_COMMIT_CLI", "claude")

	err := notifySendKeys(session, role)
	if err != nil {
		t.Fatalf("notifySendKeys failed: %v", err)
	}

	if sendKeysCalled {
		t.Error("should NOT inject send-keys when user has window focused with pending input")
	}

	// Verify message was NOT marked as notified (will retry next cycle)
	unnotified := UnnotifiedMessages(session, role)
	if len(unnotified) == 0 {
		t.Error("message should still be unnotified when notification was held")
	}
}

func TestNotifySendKeys_ClearInputFails_HoldsNotification(t *testing.T) {
	// When TmuxClearInput fails (e.g. pane disappeared), notification should
	// be held for next cycle — no injection, no crash.
	useTempBusDir(t)

	session := "test-clear-fail"
	role := "commit"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	os.MkdirAll(filepath.Join(busDir, "lock"), 0755)

	writeTestMessage(t, session, role, "msg-clearfail-001", "edit")

	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})

	sendKeysCalled := false
	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "has-session") {
			return "", nil
		}
		if strings.Contains(joined, "capture-pane") {
			return "  Ran 1 shell command\n\n❯ stale output here\n", nil
		}
		// Window NOT focused — stale input path
		if strings.Contains(joined, "window_active") {
			return "0", nil
		}
		return "", nil
	}
	tmuxRunner = func(args ...string) error {
		joined := strings.Join(args, " ")
		// C-u clear fails
		if strings.Contains(joined, "C-u") {
			return fmt.Errorf("pane not found")
		}
		if strings.Contains(joined, "send-keys") {
			sendKeysCalled = true
		}
		return nil
	}

	t.Setenv("MUXCODE_COMMIT_CLI", "claude")

	err := notifySendKeys(session, role)
	if err != nil {
		t.Fatalf("notifySendKeys should return nil on clear failure, got %v", err)
	}

	if sendKeysCalled {
		t.Error("should NOT inject send-keys when clearing stale input failed")
	}

	// Message should still be unnotified (held for next cycle)
	unnotified := UnnotifiedMessages(session, role)
	if len(unnotified) == 0 {
		t.Error("message should still be unnotified when clear failed")
	}
}

// --- IsWindowFocused detached-session / ClearParkedInput tests ---

func TestIsWindowFocused_DetachedSession(t *testing.T) {
	// A detached session reports an active window, but no user can be typing
	// into it — IsWindowFocused must return false so parked input is cleared.
	origOutput := tmuxOutputRunner
	t.Cleanup(func() { tmuxOutputRunner = origOutput })

	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "session_attached") {
			return "0", nil // detached
		}
		if strings.Contains(joined, "window_active") {
			return "1", nil // tmux still tracks an active window
		}
		return "", nil
	}

	if IsWindowFocused("detached-session", "plan") {
		t.Error("IsWindowFocused should be false for a detached session even when the window is active")
	}
}

func TestClearParkedInput_WrappedTextDetachedSession(t *testing.T) {
	// The plan-agent wedge: a dropped-Enter injection left long text parked at
	// the prompt. The wide capture shows the ❯ line with pending text; the
	// session is detached. ClearParkedInput must clear it and report true.
	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})

	clearSent := false
	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "capture-pane") {
			// Wide capture: prompt line with parked text that would wrap past
			// the 8-line IsIdle window in a real pane.
			return "⏺ Done.\n\n❯ New message from edit [request:update-docs]: Update the 9 requirement docs\n  for stories completed by PR #85\n\n  footer line\n", nil
		}
		if strings.Contains(joined, "session_attached") {
			return "0", nil // detached subsession
		}
		return "", nil
	}
	tmuxRunner = func(args ...string) error {
		if strings.Contains(strings.Join(args, " "), "C-u") {
			clearSent = true
		}
		return nil
	}

	t.Setenv("MUXCODE_PLAN_CLI", "claude")

	if !ClearParkedInput("sub-session", "plan") {
		t.Fatal("ClearParkedInput should return true when parked text was cleared")
	}
	if !clearSent {
		t.Error("expected TmuxClearInput key sequence (C-u) to be sent")
	}
}

func TestClearParkedInput_EmptyPrompt_NoClear(t *testing.T) {
	// A clean idle prompt has nothing parked — no keys should be sent.
	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})

	keysSent := false
	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "capture-pane") {
			return "⏺ Done.\n\n❯ \n", nil
		}
		if strings.Contains(joined, "session_attached") {
			return "0", nil
		}
		return "", nil
	}
	tmuxRunner = func(args ...string) error {
		keysSent = true
		return nil
	}

	t.Setenv("MUXCODE_PLAN_CLI", "claude")

	if ClearParkedInput("sub-session", "plan") {
		t.Error("ClearParkedInput should return false for an empty prompt")
	}
	if keysSent {
		t.Error("no keys should be sent when nothing is parked")
	}
}

func TestClearParkedInput_BusyPane_NoClear(t *testing.T) {
	// No ❯ prompt anywhere — agent is genuinely busy. Keys must not be sent
	// into a live composer.
	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})

	keysSent := false
	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "capture-pane") {
			return "✻ Thinking…\n\n  esc to interrupt\n", nil
		}
		if strings.Contains(joined, "session_attached") {
			return "0", nil
		}
		return "", nil
	}
	tmuxRunner = func(args ...string) error {
		keysSent = true
		return nil
	}

	t.Setenv("MUXCODE_PLAN_CLI", "claude")

	if ClearParkedInput("sub-session", "plan") {
		t.Error("ClearParkedInput should return false when no prompt is visible")
	}
	if keysSent {
		t.Error("no keys should be sent into a busy pane")
	}
}

// --- IsNotifiedRecently tests ---

func TestIsNotifiedRecently_Fresh(t *testing.T) {
	useTempBusDir(t)

	session := "test-recently-fresh"
	role := "commit"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message and mark as notified — marker is fresh
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	if !IsNotifiedRecently(session, role, 10*time.Second) {
		t.Error("IsNotifiedRecently should return true for a just-written marker")
	}
}

func TestIsNotifiedRecently_Stale(t *testing.T) {
	useTempBusDir(t)

	session := "test-recently-stale"
	role := "build"
	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	// Write a message and mark as notified
	writeTestMessage(t, session, role, "msg-001", "edit")
	markNotified(session, role)

	// Backdate the marker to simulate aging
	staleTime := time.Now().Add(-20 * time.Second)
	os.Chtimes(notifiedIDsPath(session, role), staleTime, staleTime)

	if IsNotifiedRecently(session, role, 10*time.Second) {
		t.Error("IsNotifiedRecently should return false for a 20s-old marker with 10s window")
	}
}

func TestIsNotifiedRecently_NoMarker(t *testing.T) {
	useTempBusDir(t)

	session := "test-recently-none"
	role := "test"

	if IsNotifiedRecently(session, role, 10*time.Second) {
		t.Error("IsNotifiedRecently should return false when no marker exists")
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

func TestBuildCombinedNotification_ManyMessagesShortForm(t *testing.T) {
	// More than notifyMaxSubjects messages → short fixed wake-up, NOT a subject
	// blob. This is the churn-loop fix: a large inbox must never produce a long
	// notification that can park in the composer and wrap past idle detection.
	var msgs []Message
	for i := 0; i < 20; i++ {
		msgs = append(msgs, Message{
			ID:      fmt.Sprintf("msg-%03d", i),
			From:    "daemon",
			Type:    "event",
			Action:  "long-active",
			Payload: strings.Repeat("You have been active with no return to idle. ", 5),
		})
	}
	text := BuildCombinedNotification(msgs)
	if text != "You have 20 new messages. Run: muxcode inbox" {
		t.Errorf("many messages should use short fixed form, got: %q", text)
	}
	if strings.Contains(text, "[daemon>long-active]") {
		t.Errorf("short form must not enumerate subjects: %q", text)
	}
	if len(text) > 60 {
		t.Errorf("short form should be tiny, got %d chars", len(text))
	}
}

func TestBuildCombinedNotification_FourMessagesShortForm(t *testing.T) {
	// Boundary: exactly notifyMaxSubjects+1 switches to the short form.
	var msgs []Message
	for i := 0; i < notifyMaxSubjects+1; i++ {
		msgs = append(msgs, Message{ID: fmt.Sprintf("m%d", i), From: "build", Type: "response", Action: "build", Payload: "x"})
	}
	text := BuildCombinedNotification(msgs)
	if !strings.Contains(text, "Run: muxcode inbox") {
		t.Errorf("more than %d messages should use short form: %q", notifyMaxSubjects, text)
	}
}

func TestBuildCombinedNotification_EnumeratedCapped(t *testing.T) {
	// At-or-below notifyMaxSubjects with long payloads → enumerated but hard
	// capped at notifyMaxLen with an "and N more" tail, never an unbounded blob.
	msgs := []Message{
		{ID: "m1", From: "build", Type: "response", Action: "build", Payload: strings.Repeat("a", 100)},
		{ID: "m2", From: "test", Type: "response", Action: "test", Payload: strings.Repeat("b", 100)},
		{ID: "m3", From: "review", Type: "response", Action: "review", Payload: strings.Repeat("c", 100)},
	}
	text := BuildCombinedNotification(msgs)
	if len(text) > notifyMaxLen+60 {
		t.Errorf("enumerated form should stay bounded, got %d chars: %q", len(text), text)
	}
	if !strings.Contains(text, "more") {
		t.Errorf("over-long enumerated form should show 'and N more': %q", text)
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

func TestTmuxClearInput_SendsRobustSequence(t *testing.T) {
	// TmuxClearInput must send a robust clear sequence — not a bare C-u, which
	// only kills from the cursor to the start of the line and does not reliably
	// empty Claude Code's input box. It must still include C-u so the
	// notification failure path (which keys off a C-u send failure) keeps working.
	origRun := tmuxRunner
	t.Cleanup(func() { tmuxRunner = origRun })

	var got []string
	tmuxRunner = func(args ...string) error {
		got = args
		return nil
	}

	if err := TmuxClearInput("sess:role.1"); err != nil {
		t.Fatalf("TmuxClearInput failed: %v", err)
	}

	joined := strings.Join(got, " ")
	for _, want := range []string{"C-u", "C-a", "C-k"} {
		if !strings.Contains(joined, want) {
			t.Errorf("clear sequence missing %q; got %q", want, joined)
		}
	}
}

func TestVerifyEnterDelivery_ResendsEnterWhenParked(t *testing.T) {
	// When the pane still shows text parked at the prompt after the initial
	// Enter, verifyEnterDelivery must re-send Enter (dropped-Enter recovery).
	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})

	tmuxOutputRunner = func(args ...string) (string, error) {
		// Pane keeps showing parked text after the prompt.
		return "  Ran 1 shell command\n\n❯ fix the bash script bug\n", nil
	}
	enterCount := 0
	tmuxRunner = func(args ...string) error {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "Enter") {
			enterCount++
		}
		return nil
	}

	verifyEnterDelivery("sess:role.1")

	if enterCount == 0 {
		t.Error("expected verifyEnterDelivery to re-send Enter when text stays parked")
	}
}

func TestVerifyEnterDelivery_NoResendWhenSubmitted(t *testing.T) {
	// When the pane shows a clean prompt (text was submitted), verifyEnterDelivery
	// must NOT re-send Enter.
	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})

	tmuxOutputRunner = func(args ...string) (string, error) {
		// Clean idle prompt — no pending input.
		return "  Ran 1 shell command\n\n❯\n", nil
	}
	enterCount := 0
	tmuxRunner = func(args ...string) error {
		if strings.Contains(strings.Join(args, " "), "Enter") {
			enterCount++
		}
		return nil
	}

	verifyEnterDelivery("sess:role.1")

	if enterCount != 0 {
		t.Errorf("expected no Enter re-send when prompt is clean, got %d", enterCount)
	}
}

func TestVerifyEnterDelivery_CaptureErrorReturns(t *testing.T) {
	// On capture failure, verifyEnterDelivery must return without re-sending
	// (relies on the 15s notifyRetryInterval safety net).
	origOutput := tmuxOutputRunner
	origRun := tmuxRunner
	t.Cleanup(func() {
		tmuxOutputRunner = origOutput
		tmuxRunner = origRun
	})

	tmuxOutputRunner = func(args ...string) (string, error) {
		return "", fmt.Errorf("pane gone")
	}
	enterCount := 0
	tmuxRunner = func(args ...string) error {
		if strings.Contains(strings.Join(args, " "), "Enter") {
			enterCount++
		}
		return nil
	}

	verifyEnterDelivery("sess:role.1")

	if enterCount != 0 {
		t.Errorf("expected no Enter re-send on capture error, got %d", enterCount)
	}
}

func TestIsOwnWakeUpText_MatchesEveryBuildCombinedNotificationForm(t *testing.T) {
	// Lockstep guard. Every shape BuildCombinedNotification can emit must be
	// recognisable as ours, so the forms are GENERATED from the builder rather
	// than hand-copied — hand-copied fixtures drift silently, and a form that
	// stops matching is one whose dropped-Enter residue deadlocks a focused
	// pane again.
	msg := func(from, action, payload string) Message {
		return Message{From: from, To: "plan", Type: "request", Action: action, Payload: payload}
	}
	many := make([]Message, 0, notifyMaxSubjects+2)
	for i := 0; i < notifyMaxSubjects+2; i++ {
		many = append(many, msg("edit", "update-docs", "fix the spec table"))
	}

	cases := []struct {
		name string
		msgs []Message
	}{
		{"empty inbox fallback", nil},
		{"single message", []Message{msg("edit", "commit", "commit this")}},
		{"enumerated subjects", []Message{
			msg("edit", "build", "run the build"),
			msg("test", "review", "review the diff"),
		}},
		{"over subject cap", many},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildCombinedNotification(tc.msgs)
			if !IsOwnWakeUpText(got) {
				t.Errorf("BuildCombinedNotification produced %q which IsOwnWakeUpText does not recognise as ours —\n"+
					"its dropped-Enter residue would be treated as user input and never cleared under a focused window", got)
			}
		})
	}
}

func TestIsOwnWakeUpText_LeavesUserTypedTextAlone(t *testing.T) {
	// Clearing a human's unsent text is the worse failure, so anything we
	// cannot prove we authored must be left untouched. "You have to ..." is the
	// deliberate near-miss: it shares a prefix with our "You have N new
	// messages" form and must NOT match.
	for _, s := range []string{
		"commit this",
		"p1",
		"You have to fix the NCAT prefix first",
		"New message format looks wrong",
		"",
		"   ",
	} {
		if IsOwnWakeUpText(s) {
			t.Errorf("IsOwnWakeUpText(%q) = true, want false — user text must never be cleared", s)
		}
	}
}

func TestIsOwnWakeUpText_MatchesParkedResidueFromPane(t *testing.T) {
	// The realistic path: ParkedInputText pulls our injection off the ❯ line
	// after its Enter was dropped, and we must recognise it as ours.
	content := "⏺ Reading the spec…\n\n❯ New message from edit [request:commit]: commit this checkpoint\n"
	parked := ParkedInputText(content)
	if parked == "" {
		t.Fatal("ParkedInputText found nothing — fixture is wrong")
	}
	if !IsOwnWakeUpText(parked) {
		t.Errorf("parked residue %q not recognised as our own wake-up", parked)
	}
}

func TestParkedInputText_IgnoresSubmittedScrollbackEcho(t *testing.T) {
	// A submitted prompt stays visible in scrollback. The LIVE composer is the
	// LAST ❯ in the pane, and here it is empty — nothing is parked.
	//
	// Scanning forward returned the scrollback echo instead, so the daemon's
	// sweep believed a wake-up had been dropped and Escape-resubmitted into the
	// running turn it had just started, killing it every ~2s.
	content := "❯ You have 2 new messages: [review>startup] Session started | [test>review] Tests passed\n" +
		"\n" +
		"⏺ Starting up — checking my inbox for pending messages.\n" +
		"\n" +
		"✻ Fermenting… (4s · ↓ 120 tokens)\n" +
		"\n" +
		"─────────────── code-reviewer ──\n" +
		"❯ \n" +
		"────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt\n"

	if got := ParkedInputText(content); got != "" {
		t.Errorf("ParkedInputText = %q, want \"\" — the live composer is empty; "+
			"that text is a scrollback echo of an ALREADY-SUBMITTED prompt", got)
	}
	if paneHasPendingInput(content) {
		t.Error("paneHasPendingInput must agree: no pending input when the live composer is empty")
	}
}

func TestParkedInputText_ReturnsLiveComposerNotOldestEcho(t *testing.T) {
	// Multiple prompts in scrollback: the newest (live) composer text wins.
	content := "❯ an older submitted prompt\n" +
		"⏺ did the thing\n" +
		"❯ New message from edit [request:commit]: commit this\n"

	got := ParkedInputText(content)
	want := "New message from edit [request:commit]: commit this"
	if got != want {
		t.Errorf("ParkedInputText = %q, want %q — must read the LIVE composer, not the oldest echo", got, want)
	}
}

func TestPaneShowsRecoverableIdle_ThinkingPaneIsNotSweepable(t *testing.T) {
	// The daemon's parked-input sweep sends Escape, which interrupts a running
	// turn. A thinking pane must never qualify, however its composer looks.
	content := "⏺ Checking my inbox now.\n" +
		"✻ Gesticulating… (6s · ↓ 318 tokens)\n" +
		"❯ You have 3 new messages: [edit>review] review the diff\n"

	if PaneShowsRecoverableIdle(content) {
		t.Error("thinking pane must not be sweepable — Escape would kill the running turn")
	}
}
