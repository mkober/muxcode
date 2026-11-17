package bus

import (
	"os"
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
	if !HasInFlightTaskForRole(session, "run", "edit", "run") {
		t.Fatal("fresh in-flight task should block new requests")
	}

	// Complete it, then create a stale one — stale must NOT block.
	CompleteTask(session, "fresh-1", "")
	stale := Message{ID: "stale-1", TS: now - 700, From: "edit", To: "run", Type: "request", Action: "run"}
	if err := CreateTask(session, stale, 600); err != nil {
		t.Fatalf("create stale task: %v", err)
	}
	if HasInFlightTaskForRole(session, "run", "edit", "run") {
		t.Error("expired in-flight task must not block new requests")
	}
	if _, found := FindInFlightTask(session, "run", "edit", "run"); found {
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

// TestWriteTaskNeverExposesPartialRecord pins the atomic publish: a reader
// racing a rewrite must see a whole task every time. The in-place write this
// replaced truncated first, so a reader could get an empty file — read by the
// reply guard as "no answer yet" and by a graph harvest as "task record lost".
// The staging file must also never land where scanTasks would parse it.
func TestWriteTaskNeverExposesPartialRecord(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	msg := NewMessage("edit", "build", "request", "build", strings.Repeat("x", 64<<10), "")
	if err := CreateTask(session, msg, 600); err != nil {
		t.Fatal(err)
	}
	task, _ := ReadTask(session, msg.ID)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			task.ResponseAt = int64(i)
			if err := writeTask(session, task); err != nil {
				t.Errorf("writeTask: %v", err)
				return
			}
		}
	}()
	for reading := true; reading; {
		select {
		case <-done:
			reading = false
		default:
		}
		if _, err := ReadTask(session, msg.ID); err != nil {
			t.Fatalf("a reader saw a partial task during a rewrite: %v", err)
		}
	}

	_, unreadable, err := scanTasks(session, "")
	if err != nil || len(unreadable) != 0 {
		t.Errorf("tasks dir holds non-task files after rewrites: err=%v unreadable=%v", err, unreadable)
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

func TestClearInFlightTasksForRole_UnblocksSendsAfterRestart(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// A chain task to review that stuck in-flight because the review agent
	// crashed before responding — the wedge this fix targets.
	stuck := NewMessage("test", "review", "request", "review", "review the changes", "")
	_ = CreateTask(session, stuck, 600)

	// It blocks re-sends until cleared (the observed symptom).
	if !HasInFlightTaskForRole(session, "review", "test", "review") {
		t.Fatal("precondition: in-flight review task should block sends")
	}

	// Relaunching the review agent must clear it.
	if n := ClearInFlightTasksForRole(session, "review", "review relaunched (reason=restart)"); n != 1 {
		t.Fatalf("cleared %d tasks, want 1", n)
	}
	task, _ := ReadTask(session, stuck.ID)
	if task.Status != TaskTimedOut || task.ClearedBy != "review relaunched (reason=restart)" {
		t.Errorf("stuck task = %q cleared by %q, want %q naming the relaunch", task.Status, task.ClearedBy, TaskTimedOut)
	}
	if HasInFlightTaskForRole(session, "review", "test", "review") {
		t.Error("sends to review must be unblocked after the restart clears the task")
	}
}

func TestClearInFlightTasksForRole_LeavesOtherRolesAlone(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	toReview := NewMessage("test", "review", "request", "review", "review", "")
	toBuild := NewMessage("edit", "build", "request", "build", "build", "")
	_ = CreateTask(session, toReview, 600)
	_ = CreateTask(session, toBuild, 600)

	// Only the review agent is relaunching — build's task must survive.
	if n := ClearInFlightTasksForRole(session, "review", "review relaunched (reason=reload)"); n != 1 {
		t.Fatalf("cleared %d tasks, want 1 (build's task must be untouched)", n)
	}
	if bt, _ := ReadTask(session, toBuild.ID); bt.Status != TaskInFlight {
		t.Errorf("build task Status = %q, want %q — unrelated role's task was cleared", bt.Status, TaskInFlight)
	}
}

func TestClearInFlightTasksForRole_ClearsHostedRole(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	// docs is hosted on the plan window; a task addressed to docs is delivered
	// to plan's inbox, so a plan relaunch must clear it too.
	toDocs := NewMessage("edit", "docs", "request", "update-docs", "write spec", "")
	_ = CreateTask(session, toDocs, 600)

	if n := ClearInFlightTasksForRole(session, "plan", "plan relaunched (reason=reload)"); n != 1 {
		t.Fatalf("cleared %d tasks, want 1 (plan hosts docs)", n)
	}
	if dt, _ := ReadTask(session, toDocs.ID); dt.Status != TaskTimedOut {
		t.Errorf("hosted docs task Status = %q, want %q", dt.Status, TaskTimedOut)
	}
}

// sendTracked delivers a tracked request from edit to role, as a graph
// dispatch or a --track send lands: in role's inbox with a task and no receipt.
func sendTracked(t *testing.T, session, role string, timeout int) Message {
	t.Helper()
	return sendTrackedFrom(t, session, "edit", role, timeout)
}

// sendTrackedFrom is sendTracked from another sender — a second tracked
// request from the same sender while the first is in flight is suppressed as
// a duplicate.
func sendTrackedFrom(t *testing.T, session, from, role string, timeout int) Message {
	t.Helper()
	req := NewMessage(from, role, "request", role, "wait for CI", "")
	if err := Send(session, req); err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(session, req, timeout); err != nil {
		t.Fatal(err)
	}
	return req
}

// MUX-196, the Phase 1 pin inverted: a launch keeps a task whose request still
// sits unconsumed in the role's inbox — the relaunched agent will read and
// answer it — where on 2026-09-28 it stopped a ci-watch's clock 250 s into an
// hour. The kept task writes its own row naming it unconsumed.
func TestClearInFlightTasksForRole_KeepsUnconsumedRequest(t *testing.T) {
	useTempBusDir(t)
	isolateLifecycle(t)
	session := testSession(t)
	req := sendTracked(t, session, "watch", 3600)
	if _, received := ReadReceipt(session, req.ID); received {
		t.Fatal("fixture: the request must carry no receipt")
	}
	if msgs, _ := Peek(session, "watch"); len(msgs) != 1 || msgs[0].ID != req.ID {
		t.Fatalf("fixture: watch inbox %+v, want the unconsumed request", msgs)
	}

	if n := ClearInFlightTasksForRole(session, "watch", "watch relaunched (reason=reload)"); n != 0 {
		t.Fatalf("cleared %d tasks, want the unconsumed one kept", n)
	}
	if task, _ := ReadTask(session, req.ID); task.Status != TaskInFlight || task.ClearedBy != "" {
		t.Errorf("unconsumed task = %q cleared by %q, want %q on its own clock", task.Status, task.ClearedBy, TaskInFlight)
	}
	if log := lifecycleText(t); !strings.Contains(log, "inflight-task-kept") || !strings.Contains(log, req.ID+" edit→watch:watch kept — consumed=no") {
		t.Errorf("no inflight-task-kept row naming %s unconsumed:\n%s", req.ID, log)
	}
}

// MUX-196 review must-fix: a partial consume holds the inbox lock while the
// whole inbox is renamed away and its unmatched rows restored. A launch in that
// window must wait for the lock, not read the empty inbox as proof the request
// is gone. Positive control: a request truly gone from the inbox is cleared.
func TestClearInFlightTasksForRole_WaitsOutPartialConsume(t *testing.T) {
	useTempBusDir(t)
	isolateLifecycle(t)
	session := testSession(t)
	req := sendTracked(t, session, "watch", 3600)
	inbox := InboxPath(session, "watch")

	unlock, err := lockInboxCap(session, "watch")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(inbox, inbox+".partial"); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- ClearInFlightTasksForRole(session, "watch", "watch relaunched (reason=reload)") }()
	select {
	case n := <-done:
		t.Fatalf("the launch read the inbox under another consumer's lock (cleared %d)", n)
	case <-time.After(150 * time.Millisecond):
	}
	if err := os.Rename(inbox+".partial", inbox); err != nil {
		t.Fatal(err)
	}
	unlock()
	if n := <-done; n != 0 {
		t.Errorf("cleared %d tasks, want the restored unread request kept", n)
	}
	if task, _ := ReadTask(session, req.ID); task.Status != TaskInFlight {
		t.Errorf("task %q, want in flight", task.Status)
	}

	gone := sendTrackedFrom(t, session, "plan", "watch", 3600)
	if _, err := ConsumeByID(session, "watch", req.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inbox, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := ClearInFlightTasksForRole(session, "watch", "watch relaunched (reason=reload)"); n != 2 {
		t.Errorf("cleared %d tasks, want the consumed and the missing request", n)
	}
	if log := lifecycleText(t); !strings.Contains(log, gone.ID+" plan→watch:watch cleared — consumed=no, its request is gone from the inbox") {
		t.Errorf("no row clearing %s as gone from the inbox:\n%s", gone.ID, log)
	}
}

// MUX-196 review must-fix: the full drain (Receive) renames the inbox to
// .consuming without the lock; a request mid-drain is still queued. An inbox
// that cannot be read proves nothing either, so a receipt-less task is kept
// while a receipted one is still cleared.
func TestClearInFlightTasksForRole_KeepsWhatItCannotProveGone(t *testing.T) {
	useTempBusDir(t)
	isolateLifecycle(t)
	session := testSession(t)
	inbox := InboxPath(session, "watch")

	draining := sendTracked(t, session, "watch", 3600)
	if err := os.Rename(inbox, inbox+".consuming"); err != nil {
		t.Fatal(err)
	}
	if n := ClearInFlightTasksForRole(session, "watch", "watch relaunched (reason=reload)"); n != 0 {
		t.Errorf("cleared %d tasks, want the request mid-drain kept", n)
	}
	if err := os.Remove(inbox + ".consuming"); err != nil {
		t.Fatal(err)
	}

	read := sendTrackedFrom(t, session, "plan", "watch", 3600)
	WriteReceipt(session, read.ID, "watch", ReceiptKindAck)
	if err := os.Remove(inbox); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if n := ClearInFlightTasksForRole(session, "watch", "watch relaunched (reason=reload)"); n != 1 {
		t.Errorf("cleared %d tasks with the inbox unreadable, want only the receipted one", n)
	}
	if task, _ := ReadTask(session, draining.ID); task.Status != TaskInFlight {
		t.Errorf("receipt-less task %q with the inbox unreadable, want in flight", task.Status)
	}
	if log := lifecycleText(t); !strings.Contains(log, draining.ID+" edit→watch:watch kept — consumed=no, and the inbox could not be read") {
		t.Errorf("no kept row naming the unreadable inbox:\n%s", log)
	}
}

// MUX-196: a request an auto-CC copy of which edit read is still unconsumed —
// the receipt is the addressee's (MUX-216) — so the launch keeps its task.
func TestClearInFlightTasksForRole_CCReadIsNotConsumption(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	req := NewMessage("test", "review", "request", "review", "review the changes", "")
	if err := Send(session, req); err != nil {
		t.Fatal(err)
	}
	_ = CreateTask(session, req, 600)
	if _, err := ConsumeByID(session, "edit", req.ID); err != nil {
		t.Fatal(err)
	}
	if _, received := ReadReceipt(session, req.ID); received {
		t.Fatal("fixture: edit's CC read must leave review's request unreceipted")
	}

	if n := ClearInFlightTasksForRole(session, "review", "review relaunched (reason=reload)"); n != 0 {
		t.Errorf("cleared %d tasks, want review's unread request kept", n)
	}
}

// MUX-196 negative control, green through every phase: a request the prior
// instance consumed cannot be answered by the relaunched one, so its task is
// still cleared and stops blocking sends of the same (to, action).
func TestClearInFlightTasksForRole_ClearsConsumedRequest(t *testing.T) {
	useTempBusDir(t)
	isolateLifecycle(t)
	session := testSession(t)
	req := sendTracked(t, session, "watch", 3600)
	WriteReceipt(session, req.ID, "watch", ReceiptKindAck)

	if n := ClearInFlightTasksForRole(session, "watch", "watch relaunched (reason=reload)"); n != 1 {
		t.Fatalf("cleared %d tasks, want the consumed one", n)
	}
	task, _ := ReadTask(session, req.ID)
	if task.Status != TaskTimedOut || task.ClearedBy != "watch relaunched (reason=reload)" {
		t.Errorf("consumed task = %q cleared by %q, want %q naming the relaunch", task.Status, task.ClearedBy, TaskTimedOut)
	}
	if HasInFlightTaskForRole(session, "watch", "edit", "watch") {
		t.Error("a cleared consumed task still blocks sends to watch")
	}
	if log := lifecycleText(t); !strings.Contains(log, req.ID+" edit→watch:watch cleared — consumed=yes") {
		t.Errorf("no inflight-task-cleared row naming %s consumed:\n%s", req.ID, log)
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
