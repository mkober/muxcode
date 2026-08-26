package bus

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindRequestByID(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-findreq"
	os.MkdirAll(filepath.Dir(LogPath(session)), 0755)

	req := NewMessage("edit", "plan", "request", "update-docs", "document the reload fix", "")
	data, _ := EncodeMessage(req)
	appendToFile(LogPath(session), append(data, '\n'))

	got, ok := FindRequestByID(session, req.ID)
	if !ok {
		t.Fatal("logged request not found by ID")
	}
	if got.Payload != "document the reload fix" {
		t.Errorf("payload = %q, want %q", got.Payload, "document the reload fix")
	}

	// Both unresolvable cases must report false so callers fail open and send.
	if _, ok := FindRequestByID(session, ""); ok {
		t.Error("empty ID must not match")
	}
	if _, ok := FindRequestByID(session, "1234567890-edit-deadbeef"); ok {
		t.Error("unknown ID must not match")
	}
}

func TestIsDuplicateMessage_NoDuplicate(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a message to the log
	m1 := NewMessage("build", "edit", "event", "notify", "Build succeeded", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Different (from, to, action, type) — not a duplicate
	m2 := NewMessage("test", "edit", "event", "notify", "Tests passed", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("different from should not be duplicate")
	}

	m3 := NewMessage("build", "review", "event", "notify", "Build succeeded", "")
	if IsDuplicateMessage(session, m3) {
		t.Error("different to should not be duplicate")
	}

	m4 := NewMessage("build", "edit", "request", "review", "Review please", "")
	if IsDuplicateMessage(session, m4) {
		t.Error("different action/type should not be duplicate")
	}
}

func TestIsDuplicateMessage_Duplicate(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a request message to the log (responses are exempt from dedup)
	m1 := NewMessage("edit", "build", "request", "build", "Run build", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Same (from, to, action, type) — is a duplicate
	m2 := NewMessage("edit", "build", "request", "build", "Run build again", "")
	if !IsDuplicateMessage(session, m2) {
		t.Error("same from/to/action/type within window should be duplicate")
	}
}

func TestIsDuplicateMessage_ResponseExcluded(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a response to the log
	m1 := NewMessage("test", "edit", "response", "test", "Tests passed", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Same tuple — should NOT be deduped because responses are exempt
	m2 := NewMessage("test", "edit", "response", "test", "Tests passed again", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("response messages should never be deduped")
	}
}

func TestIsDuplicateMessage_ExpiredWindow(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a message with timestamp outside the dedup window
	m1 := Message{
		ID:      "old-msg",
		TS:      time.Now().Unix() - 60, // 60s ago, outside 30s default window
		From:    "review",
		To:      "test",
		Type:    "response",
		Action:  "review-complete",
		Payload: "LGTM",
	}
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Same tuple but original is expired — not a duplicate
	m2 := NewMessage("review", "test", "response", "review-complete", "LGTM", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("expired message should not count as duplicate")
	}
}

func TestIsDuplicateMessage_SystemActionExcluded(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a system action to the log
	m1 := NewMessage("daemon", "edit", "event", "loop-detected", "test<->review loop", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Same system action — should NOT be deduped
	m2 := NewMessage("daemon", "edit", "event", "loop-detected", "another loop", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("system actions should never be deduped")
	}
}

func TestIsDuplicateMessage_DisabledByEnv(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	m1 := NewMessage("review", "test", "response", "review-complete", "LGTM", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	m2 := NewMessage("review", "test", "response", "review-complete", "LGTM", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("dedup should be disabled when window is 0")
	}
}

func TestIsDuplicateMessage_EmptyLog(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	// No log file exists

	m := NewMessage("review", "test", "response", "review-complete", "LGTM", "")
	if IsDuplicateMessage(session, m) {
		t.Error("no log file should mean no duplicate")
	}
}

func TestHasPendingInboxRequest(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-inbox-dedup"

	// Setup: create inbox directory
	inboxDir := filepath.Dir(InboxPath(session, "deploy"))
	os.MkdirAll(inboxDir, 0755)

	// No inbox → no pending request
	if HasPendingInboxRequest(session, "deploy", "edit", "deploy", "Run cdk deploy") {
		t.Error("empty inbox should have no pending request")
	}

	// Write a request to the inbox
	m1 := NewMessage("edit", "deploy", "request", "deploy", "Run cdk deploy", "")
	data, _ := EncodeMessage(m1)
	appendToFile(InboxPath(session, "deploy"), append(data, '\n'))

	// Same (from, action, payload) → found
	if !HasPendingInboxRequest(session, "deploy", "edit", "deploy", "Run cdk deploy") {
		t.Error("should find pending request with matching from+action+payload")
	}

	// Same (from, action) but different payload → not found
	if HasPendingInboxRequest(session, "deploy", "edit", "deploy", "Run cdk deploy again") {
		t.Error("different payload should not match")
	}

	// Different from → not found
	if HasPendingInboxRequest(session, "deploy", "build", "deploy", "Run cdk deploy") {
		t.Error("different from should not match")
	}

	// Different action → not found
	if HasPendingInboxRequest(session, "deploy", "edit", "build", "Run cdk deploy") {
		t.Error("different action should not match")
	}

	// Response messages should NOT match
	m2 := NewMessage("edit", "deploy", "response", "deploy", "Done", "")
	data2, _ := EncodeMessage(m2)
	os.WriteFile(InboxPath(session, "deploy"), append(data2, '\n'), 0644)
	if HasPendingInboxRequest(session, "deploy", "edit", "deploy", "Done") {
		t.Error("response messages should not be matched")
	}
}

func TestHasInFlightTaskForRole(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-task-dedup"

	// No tasks → false
	if HasInFlightTaskForRole(session, "deploy", "deploy") {
		t.Error("no tasks should mean no in-flight task")
	}

	os.MkdirAll(TaskDir(session), 0755)

	// Create an in-flight task
	m := NewMessage("edit", "deploy", "request", "deploy", "Run cdk deploy", "")
	CreateTask(session, m, 600)

	// Same (to, action) → found
	if !HasInFlightTaskForRole(session, "deploy", "deploy") {
		t.Error("should find in-flight task with matching to+action")
	}

	// Different action → not found
	if HasInFlightTaskForRole(session, "deploy", "build") {
		t.Error("different action should not match")
	}

	// Different target → not found
	if HasInFlightTaskForRole(session, "build", "deploy") {
		t.Error("different target should not match")
	}

	// Completed task → not found
	CompleteTask(session, m.ID, "resp-123")
	if HasInFlightTaskForRole(session, "deploy", "deploy") {
		t.Error("completed task should not be found")
	}
}

func TestFindInFlightTask(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-find-task"

	// No tasks
	_, found := FindInFlightTask(session, "deploy", "deploy")
	if found {
		t.Error("should not find task when none exist")
	}

	os.MkdirAll(TaskDir(session), 0755)

	// Create in-flight task
	m := NewMessage("edit", "deploy", "request", "deploy", "Run cdk deploy", "")
	CreateTask(session, m, 600)

	task, found := FindInFlightTask(session, "deploy", "deploy")
	if !found {
		t.Fatal("should find in-flight task")
	}
	if task.ID != m.ID {
		t.Errorf("task ID mismatch: got %s, want %s", task.ID, m.ID)
	}
	if task.Action != "deploy" {
		t.Errorf("task action: got %s, want deploy", task.Action)
	}
}

func TestSendMessage_SuppressesDuplicateInboxRequest(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-send-dedup"

	// Setup: init session directories and config (deploy is auto-CC)
	Init(session, dir)
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// First send should succeed
	m1 := NewMessage("edit", "deploy", "request", "deploy", "Run cdk deploy", "")
	err := Send(session, m1)
	if err != nil {
		t.Fatalf("first send failed: %v", err)
	}

	// Verify message is in inbox
	msgs, _ := Peek(session, "deploy")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in inbox, got %d", len(msgs))
	}

	// Second send with same (from, to, action, type, payload) is suppressed
	// and must SAY so — the nil return here once read as a successful send
	// and jammed retries on phantom tasks (ErrSendSuppressed, MUX-105).
	m2 := NewMessage("edit", "deploy", "request", "deploy", "Run cdk deploy", "")
	err = Send(session, m2)
	if !errors.Is(err, ErrSendSuppressed) {
		t.Fatalf("second send must return ErrSendSuppressed, got %v", err)
	}

	// Verify still only 1 message in inbox (duplicate suppressed)
	msgs, _ = Peek(session, "deploy")
	if len(msgs) != 1 {
		t.Errorf("expected 1 message after dedup, got %d", len(msgs))
	}

	// Third send with DIFFERENT payload should NOT be suppressed
	m3 := NewMessage("edit", "deploy", "request", "deploy", "Deploy with different context", "")
	err = Send(session, m3)
	if err != nil {
		t.Fatalf("third send should not error: %v", err)
	}

	// Should have 2 messages now (original + different payload)
	msgs, _ = Peek(session, "deploy")
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages (different payloads), got %d", len(msgs))
	}
}

func TestSendMessage_SuppressesDuplicateWithInFlightTask(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-task-send-dedup"
	Init(session, dir)

	// Send first request and create a task (simulating --wait)
	m1 := NewMessage("edit", "deploy", "request", "deploy", "Run cdk deploy", "")
	err := Send(session, m1)
	if err != nil {
		t.Fatalf("first send failed: %v", err)
	}
	CreateTask(session, m1, 600)

	// Consume the inbox (simulating SendWakeUp consuming after injection)
	Receive(session, "deploy")

	// Second send: inbox is empty but task is in-flight → suppressed, and
	// the suppression must be visible (ErrSendSuppressed, MUX-105).
	m2 := NewMessage("edit", "deploy", "request", "deploy", "Run cdk deploy retry", "")
	err = Send(session, m2)
	if !errors.Is(err, ErrSendSuppressed) {
		t.Fatalf("second send must return ErrSendSuppressed, got %v", err)
	}

	// Verify inbox is still empty (suppressed)
	msgs, _ := Peek(session, "deploy")
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after task dedup, got %d", len(msgs))
	}
}

func TestSendMessage_AllowsResponsesDespiteInFlightTask(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-response-pass"
	Init(session, dir)

	// Create an in-flight task
	m1 := NewMessage("edit", "deploy", "request", "deploy", "Run cdk deploy", "")
	CreateTask(session, m1, 600)

	// Response messages should NOT be suppressed
	m2 := NewMessage("deploy", "edit", "response", "deploy", "Deploy complete", "")
	err := Send(session, m2)
	if err != nil {
		t.Fatalf("response send failed: %v", err)
	}

	msgs, _ := Peek(session, "edit")
	if len(msgs) != 1 {
		t.Errorf("response should not be suppressed, got %d messages", len(msgs))
	}
}

func TestSendMessage_AllowsSystemActions(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-system-pass"
	Init(session, dir)

	// Write a system action to inbox
	m1 := NewMessage("daemon", "edit", "request", "compact-recommended", "Context growing", "")
	Send(session, m1)

	// Same system action should NOT be suppressed
	m2 := NewMessage("daemon", "edit", "request", "compact-recommended", "Context growing more", "")
	Send(session, m2)

	msgs, _ := Peek(session, "edit")
	if len(msgs) != 2 {
		t.Errorf("system actions should not be deduped, got %d messages (want 2)", len(msgs))
	}
}

// TestFindResponseSince covers the guard that stops the daemon from
// synthesizing a duplicate reply for a non-hook agent that already answered.
func TestFindResponseSince(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-find-response-since"
	Init(session, dir)
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	if _, ok := FindResponseSince(session, "review", "test", 0); ok {
		t.Error("must report no response before one is sent")
	}

	// A request in the opposite direction is not a response.
	if err := Send(session, NewMessage("test", "review", "request", "review", "review the diff", "")); err != nil {
		t.Fatalf("Send request: %v", err)
	}
	if _, ok := FindResponseSince(session, "review", "test", 0); ok {
		t.Error("a request must not satisfy the response lookup")
	}

	// The agent answers for itself — exactly the case that must suppress
	// synthesis of a second, pane-scraped response.
	resp := NewMessage("review", "test", "response", "response", "LGTM", "")
	if err := Send(session, resp); err != nil {
		t.Fatalf("Send response: %v", err)
	}
	id, ok := FindResponseSince(session, "review", "test", 0)
	if !ok {
		t.Fatal("must find the response the agent sent")
	}
	if id != resp.ID {
		t.Errorf("wrong response id: got %q want %q", id, resp.ID)
	}

	// A reply older than the task must not count, or a stale response would
	// suppress synthesis for a brand-new task forever.
	if _, ok := FindResponseSince(session, "review", "test", time.Now().Unix()+60); ok {
		t.Error("a response older than `since` must not count")
	}

	// Direction matters.
	if _, ok := FindResponseSince(session, "test", "review", 0); ok {
		t.Error("response lookup must be direction-sensitive")
	}
}
