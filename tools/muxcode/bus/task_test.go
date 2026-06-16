package bus

import (
	"strings"
	"testing"
	"time"
)

func TestTaskExpired(t *testing.T) {
	now := time.Now().Unix()
	if TaskExpired(Task{SentAt: now - 10, Timeout: 600}, now) {
		t.Error("fresh task within timeout should not be expired")
	}
	if !TaskExpired(Task{SentAt: now - 700, Timeout: 600}, now) {
		t.Error("task past its timeout should be expired")
	}
	// Timeout unset → default 600s.
	if !TaskExpired(Task{SentAt: now - 601, Timeout: 0}, now) {
		t.Error("task past default timeout should be expired")
	}
	if TaskExpired(Task{SentAt: now - 100, Timeout: 0}, now) {
		t.Error("task within default timeout should not be expired")
	}
}

func TestHasInFlightTaskForRole_IgnoresExpired(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	now := time.Now().Unix()

	// A fresh in-flight task blocks duplicate sends.
	fresh := Message{ID: "fresh-1", TS: now, From: "edit", To: "run", Type: "request", Action: "run"}
	if err := CreateTask(session, fresh, 600); err != nil {
		t.Fatalf("create fresh task: %v", err)
	}
	if !HasInFlightTaskForRole(session, "run", "run") {
		t.Fatal("fresh in-flight task should block new requests")
	}

	// Complete it, then create a stale one — stale must NOT block.
	CompleteTask(session, "fresh-1", "")
	stale := Message{ID: "stale-1", TS: now - 700, From: "edit", To: "run", Type: "request", Action: "run"}
	if err := CreateTask(session, stale, 600); err != nil {
		t.Fatalf("create stale task: %v", err)
	}
	if HasInFlightTaskForRole(session, "run", "run") {
		t.Error("expired in-flight task must not block new requests")
	}
	if _, found := FindInFlightTask(session, "run", "run"); found {
		t.Error("FindInFlightTask must not reattach to an expired task")
	}
}

func TestCreateAndReadTask(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "test", "request", "test", "Run tests and report results", "")

	err := CreateTask(session, msg, 600)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	task, err := ReadTask(session, msg.ID)
	if err != nil {
		t.Fatalf("ReadTask: %v", err)
	}

	if task.ID != msg.ID {
		t.Errorf("ID = %q, want %q", task.ID, msg.ID)
	}
	if task.From != "edit" {
		t.Errorf("From = %q, want edit", task.From)
	}
	if task.To != "test" {
		t.Errorf("To = %q, want test", task.To)
	}
	if task.Action != "test" {
		t.Errorf("Action = %q, want test", task.Action)
	}
	if task.Status != TaskInFlight {
		t.Errorf("Status = %q, want %q", task.Status, TaskInFlight)
	}
	if task.Timeout != 600 {
		t.Errorf("Timeout = %d, want 600", task.Timeout)
	}
	if task.Payload != "Run tests and report results" {
		t.Errorf("Payload = %q, want 'Run tests and report results'", task.Payload)
	}
}

func TestCompleteTask(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "build", "request", "build", "Run build", "")
	_ = CreateTask(session, msg, 300)

	CompleteTask(session, msg.ID, "resp-456")

	task, err := ReadTask(session, msg.ID)
	if err != nil {
		t.Fatalf("ReadTask: %v", err)
	}
	if task.Status != TaskCompleted {
		t.Errorf("Status = %q, want %q", task.Status, TaskCompleted)
	}
	if task.ResponseID != "resp-456" {
		t.Errorf("ResponseID = %q, want resp-456", task.ResponseID)
	}
	if task.ResponseAt == 0 {
		t.Error("ResponseAt should be set")
	}
}

func TestTimeoutTask(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "test", "request", "test", "Run tests", "")
	_ = CreateTask(session, msg, 600)

	TimeoutTask(session, msg.ID)

	task, _ := ReadTask(session, msg.ID)
	if task.Status != TaskTimedOut {
		t.Errorf("Status = %q, want %q", task.Status, TaskTimedOut)
	}
}

func TestTimeoutTask_SkipsCompleted(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	msg := NewMessage("edit", "test", "request", "test", "Run tests", "")
	_ = CreateTask(session, msg, 600)
	CompleteTask(session, msg.ID, "resp-123")

	// Timeout should not override completed status
	TimeoutTask(session, msg.ID)

	task, _ := ReadTask(session, msg.ID)
	if task.Status != TaskCompleted {
		t.Errorf("Status = %q, want %q (timeout should not override completed)", task.Status, TaskCompleted)
	}
}

func TestListTasks(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Create 3 tasks with different statuses
	m1 := NewMessage("edit", "build", "request", "build", "Build it", "")
	m2 := NewMessage("edit", "test", "request", "test", "Test it", "")
	m3 := NewMessage("edit", "review", "request", "review", "Review it", "")
	_ = CreateTask(session, m1, 600)
	_ = CreateTask(session, m2, 600)
	_ = CreateTask(session, m3, 600)
	CompleteTask(session, m1.ID, "resp-1")
	TimeoutTask(session, m3.ID)

	// List all
	all, err := ListTasks(session, "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("len(all) = %d, want 3", len(all))
	}

	// List in-flight only
	inflight, _ := ListTasks(session, TaskInFlight)
	if len(inflight) != 1 {
		t.Errorf("len(inflight) = %d, want 1", len(inflight))
	}

	// List completed
	completed, _ := ListTasks(session, TaskCompleted)
	if len(completed) != 1 {
		t.Errorf("len(completed) = %d, want 1", len(completed))
	}

	// List timed-out
	timedOut, _ := ListTasks(session, TaskTimedOut)
	if len(timedOut) != 1 {
		t.Errorf("len(timedOut) = %d, want 1", len(timedOut))
	}
}

func TestListTasks_EmptyDir(t *testing.T) {
	useTempBusDir(t)
	// Don't init — no tasks dir exists
	tasks, err := ListTasks("nonexistent-session", "")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected empty, got %d tasks", len(tasks))
	}
}

func TestCleanExpiredTasks(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Create an old task (2 hours ago) and a recent one
	oldMsg := Message{ID: "old-task", TS: time.Now().Add(-2 * time.Hour).Unix(), From: "edit", To: "build"}
	newMsg := Message{ID: "new-task", TS: time.Now().Unix(), From: "edit", To: "test"}
	_ = CreateTask(session, oldMsg, 600)
	_ = CreateTask(session, newMsg, 600)

	cleaned := CleanExpiredTasks(session, 1*time.Hour)
	if cleaned != 1 {
		t.Errorf("cleaned = %d, want 1", cleaned)
	}

	// Old task should be gone
	_, err := ReadTask(session, "old-task")
	if err == nil {
		t.Error("expected old task to be removed")
	}

	// New task should still exist
	_, err = ReadTask(session, "new-task")
	if err != nil {
		t.Error("expected new task to still exist")
	}
}

func TestFormatTask(t *testing.T) {
	task := Task{
		ID:     "1711324800-edit-abc123",
		From:   "edit",
		To:     "test",
		Action: "test",
		Status: TaskInFlight,
	}

	s := FormatTask(task)
	if s == "" {
		t.Error("expected non-empty format")
	}
	if !strings.Contains(s, "in-flight") {
		t.Errorf("expected 'in-flight' in format: %s", s)
	}
	if !strings.Contains(s, "edit") || !strings.Contains(s, "test") {
		t.Errorf("expected from/to in format: %s", s)
	}
}

func TestFormatTask_WithResponse(t *testing.T) {
	task := Task{
		ID:         "task-1",
		From:       "edit",
		To:         "test",
		Action:     "test",
		Status:     TaskCompleted,
		ResponseID: "resp-123",
	}

	s := FormatTask(task)
	if !strings.Contains(s, "completed") {
		t.Errorf("expected 'completed' in format: %s", s)
	}
	if !strings.Contains(s, "response=resp-123") {
		t.Errorf("expected response ID in format: %s", s)
	}
}

func TestFormatTask_TruncatesLongPayload(t *testing.T) {
	task := Task{
		ID:      "task-1",
		From:    "edit",
		To:      "test",
		Action:  "test",
		Status:  TaskInFlight,
		Payload: "This is a very long payload that exceeds sixty characters and should be truncated with ellipsis",
	}

	s := FormatTask(task)
	if !strings.Contains(s, "...") {
		t.Errorf("expected truncated payload with '...' in format: %s", s)
	}
}

func TestReadTask_NotFound(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	_, err := ReadTask(session, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestCompleteTask_Nonexistent(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Should not panic
	CompleteTask(session, "nonexistent", "resp-1")
}

func TestTimeoutTask_Nonexistent(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// Should not panic
	TimeoutTask(session, "nonexistent")
}
