package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func spawnCancelGraph() *Graph {
	return &Graph{Name: "cancel-spawn", Start: "w",
		Nodes: []Node{{ID: "w", Type: NodeSpawn, Role: "edit", Message: "implement"}}}
}

// delegateFrom has a worker send a request, tracked as an in-flight task,
// the way a spawn delegates a run or plan request.
func delegateFrom(t *testing.T, from, to string) Message {
	t.Helper()
	m := NewMessage(from, to, "request", to, "work for "+to, "")
	if err := Send(runTestSession, m); err != nil {
		t.Fatalf("send %s->%s: %v", from, to, err)
	}
	if err := CreateTask(runTestSession, m, 600); err != nil {
		t.Fatal(err)
	}
	return m
}

func inboxHas(t *testing.T, role, msgID string) bool {
	t.Helper()
	msgs, _ := Peek(runTestSession, role)
	for _, m := range msgs {
		if m.ID == msgID {
			return true
		}
	}
	return false
}

// TestCancelStopsRunningSpawnWorker pins MUX-182 defect 2: canceling a run
// with a working spawn stops the worker by its entry ID (the fake's IDs
// differ from its roles, as in production), skips the running node, and
// retracts what the worker delegated. A request and task from outside the
// run survive — the negative control that the retraction is scoped to the
// run's own workers.
func TestCancelStopsRunningSpawnWorker(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	f.distinctIDs = true

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeRunning {
		t.Fatalf("worker node %q, want running", st.State)
	}
	worker := st.TaskID
	delegated := delegateFrom(t, worker, "run")
	bystander := delegateFrom(t, "edit", "plan")

	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceled {
		t.Errorf("run %q, want canceled", r.State)
	}
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeSkipped {
		t.Errorf("running node %q, want skipped", s)
	}
	e, _ := findSpawnByRole(runTestSession, worker)
	if e.Status != "stopped" || len(f.killed) != 1 || f.killed[0] != worker {
		t.Errorf("worker must be stopped by the cancel, got status %q, killed %v", e.Status, f.killed)
	}

	if inboxHas(t, "run", delegated.ID) {
		t.Error("the worker's unconsumed request must be withdrawn from run's inbox")
	}
	if task, _ := ReadTask(runTestSession, delegated.ID); task.Status != TaskTimedOut {
		t.Errorf("the worker's in-flight task %q, want timed-out", task.Status)
	}
	if ds, _ := ReadDeliveryStatus(runTestSession, delegated.ID); ds.Status != StatusExpired || ds.AckedAt != 0 {
		t.Errorf("a withdrawn request is expired, never receipted: %+v", ds)
	}

	if !inboxHas(t, "plan", bystander.ID) {
		t.Error("a request from outside the run must survive the cancel")
	}
	if task, _ := ReadTask(runTestSession, bystander.ID); task.Status != TaskInFlight {
		t.Errorf("an unrelated task %q, want in-flight", task.Status)
	}
}

// TestCancelStopsParkedWorker covers the persistent worker: its node is
// done, but the worker idles at its prompt for the next iteration, so it
// is found by the run stamp rather than by a running node.
func TestCancelStopsParkedWorker(t *testing.T) {
	g := spawnCancelGraph()
	g.Nodes = append(g.Nodes, Node{ID: "b", Type: NodeSend, Role: "build", Action: "build", Message: "build"})
	g.Edges = []Edge{{From: "w", To: "b"}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)
	f.distinctIDs = true

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	answerSpawn(t, runTestSession, st.TaskID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeDone {
		t.Fatalf("answered worker node %q, want done", s)
	}

	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if e, _ := findSpawnByRole(runTestSession, st.TaskID); e.Status != "stopped" {
		t.Errorf("parked worker %q, want stopped", e.Status)
	}
}

// TestCancelFailsClosedOnSurvivor pins Decision 1: a worker whose window
// outlives its kill keeps the run canceling, keeps its node running, and
// the error names the survivor and the command that stops it. Retry is
// refused meanwhile. Once the kill works, re-running cancel completes it.
func TestCancelFailsClosedOnSurvivor(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	f.distinctIDs = true
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	worker := st.TaskID
	entry, _ := findSpawnByRole(runTestSession, worker)

	spawnKillWindowFn = func(string, string) error { return errors.New("kill-window: no server") }
	err := CancelGraphRun(runTestSession, run.ID)
	var cerr *CancelIncompleteError
	if !errors.As(err, &cerr) || len(cerr.Survivors) != 1 {
		t.Fatalf("cancel with a surviving worker must fail closed, got %v", err)
	}
	if !strings.Contains(err.Error(), "muxcode spawn stop "+entry.ID) || !strings.Contains(err.Error(), "NOT canceled") {
		t.Errorf("error must name the survivor's stop command: %v", err)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceling {
		t.Errorf("run %q, want canceling — never canceled while a worker runs", r.State)
	}
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeRunning {
		t.Errorf("survivor's node %q, want running", s)
	}
	if e, _ := findSpawnByRole(runTestSession, worker); e.Status != "running" {
		t.Errorf("survivor's entry %q, want running", e.Status)
	}
	if _, err := RetryGraphRun(runTestSession, run.ID, "w"); err == nil {
		t.Error("retry of a canceling run must be refused")
	}

	f.fresh = 0
	spawnKillWindowFn = func(_, w string) error { f.killed = append(f.killed, w); return nil }
	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("re-cancel after the kill works: %v", err)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceled {
		t.Errorf("run %q, want canceled on retry", r.State)
	}
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeSkipped {
		t.Errorf("node %q, want skipped", s)
	}
	if f.fresh != 0 {
		t.Errorf("no worker may be started or replaced during a cancel, got %d", f.fresh)
	}
}

// TestCancelWithoutSpawnsReportsSuccess is the negative control: a run
// with no workers cancels cleanly and kills nothing — including another
// run's live worker.
func TestCancelWithoutSpawnsReportsSuccess(t *testing.T) {
	run := createTestRun(t, linearGraph())
	f := fakeLiveSpawns(t)
	other, err := graphSpawnFn(runTestSession, "edit", "other work", graphSender, "other-run", "w")
	if err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run.ID)

	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceled {
		t.Errorf("run %q, want canceled", r.State)
	}
	if len(f.killed) != 0 {
		t.Errorf("a run without workers kills nothing, killed %v", f.killed)
	}
	if e, _ := findSpawnByRole(runTestSession, other); e.Status != "running" {
		t.Errorf("another run's worker %q, want running", e.Status)
	}
}

// TestReplacementFailClosedStopsByRole pins the second Phase 1 latent bug:
// failClosed held spawn roles and handed them to StopSpawn, which keys on
// the entry ID, so with production-shaped IDs every stop failed "spawn not
// found" and the replacement kept running while named "still live".
func TestReplacementFailClosedStopsByRole(t *testing.T) {
	g := &Graph{Name: "map-lost", Start: "m",
		Nodes: []Node{{ID: "m", Type: NodeMap, Role: "edit", Items: "one,two", Message: "handle ${item}"}}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)
	f.distinctIDs = true

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "m")
	for _, role := range strings.Split(st.TaskID, ",") {
		e, _ := findSpawnByRole(runTestSession, role)
		if err := UpdateSpawnEntry(runTestSession, e.ID, func(e *SpawnEntry) { e.Status = "stopped" }); err != nil {
			t.Fatal(err)
		}
	}
	inner := graphSpawnFn
	starts := 0
	graphSpawnFn = func(sess, role, task, owner, runID, nodeID string) (string, error) {
		starts++
		if starts == 2 {
			return "", errors.New("tmux new-window: no server")
		}
		return inner(sess, role, task, owner, runID, nodeID)
	}

	step(t, runTestSession, run.ID)
	st, _ = ReadNodeStatus(runTestSession, run.ID, "m")
	if st.State != GraphNodeFailed || strings.Contains(st.Output, "still live") {
		t.Fatalf("the launched replacement must be stopped, got %q %q", st.State, st.Output)
	}
	if len(f.killed) != 1 {
		t.Fatalf("exactly the launched replacement is killed, got %v", f.killed)
	}
	if e, _ := findSpawnByRole(runTestSession, f.killed[0]); e.Status != "stopped" {
		t.Errorf("replacement %q, want stopped", e.Status)
	}
}

// cancelInsideNextSpawn wraps graphSpawnFn so the next worker creation
// starts a concurrent cancel and stalls before registering its worker —
// the window where a cancel that snapshots the registry misses the worker.
// The returned channel yields the cancel's result.
func cancelInsideNextSpawn(t *testing.T, runID string) <-chan error {
	t.Helper()
	inner := graphSpawnFn
	done := make(chan error, 1)
	fired := false
	graphSpawnFn = func(sess, role, task, owner, rid, nid string) (string, error) {
		if !fired {
			fired = true
			go func() { done <- CancelGraphRun(sess, runID) }()
			select {
			case err := <-done:
				t.Errorf("cancel returned while a tick was mid-spawn: %v", err)
				done <- err
			case <-time.After(200 * time.Millisecond):
			}
		}
		return inner(sess, role, task, owner, rid, nid)
	}
	return done
}

func awaitCancel(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("cancel never returned after the tick released the run")
		return nil
	}
}

// TestCancelWaitsOutSpawnCreation pins review must-fix 1: a cancel issued
// while a tick is creating a worker waits for the tick, then finds and
// stops that worker — never reporting canceled with it about to start.
func TestCancelWaitsOutSpawnCreation(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	f.distinctIDs = true
	done := cancelInsideNextSpawn(t, run.ID)

	step(t, runTestSession, run.ID)
	if err := awaitCancel(t, done); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if f.fresh != 1 || len(f.killed) != 1 {
		t.Fatalf("the worker created mid-cancel must be stopped: %d started, killed %v", f.fresh, f.killed)
	}
	if e, _ := findSpawnByRole(runTestSession, f.killed[0]); e.Status != "stopped" {
		t.Errorf("worker %q, want stopped", e.Status)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceled {
		t.Errorf("run %q, want canceled", r.State)
	}
}

// TestCancelWaitsOutReplacement is the replaceLostWorkers half: a cancel
// issued while a tick replaces a lost worker stops the replacement.
func TestCancelWaitsOutReplacement(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	f.distinctIDs = true
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	lost, _ := findSpawnByRole(runTestSession, st.TaskID)
	if err := UpdateSpawnEntry(runTestSession, lost.ID, func(e *SpawnEntry) { e.Status = "stopped" }); err != nil {
		t.Fatal(err)
	}
	done := cancelInsideNextSpawn(t, run.ID)

	step(t, runTestSession, run.ID)
	if err := awaitCancel(t, done); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if f.fresh != 2 {
		t.Fatalf("the tick must have replaced the lost worker, %d started", f.fresh)
	}
	replacement := fmt.Sprintf("spawn-live%04d", 2)
	if e, _ := findSpawnByRole(runTestSession, replacement); e.Status != "stopped" {
		t.Errorf("replacement created mid-cancel %q, want stopped", e.Status)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceled {
		t.Errorf("run %q, want canceled", r.State)
	}
}

// TestCancelFailsClosedOnUnreadableRegistry pins review must-fix 2: an
// unreadable spawn registry is a failed cancel, never an empty worker set.
// Once the registry reads again, re-running cancel stops the worker.
func TestCancelFailsClosedOnUnreadableRegistry(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	f.distinctIDs = true
	step(t, runTestSession, run.ID)
	registry := SpawnPath(runTestSession)
	data, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(registry, 0755); err != nil {
		t.Fatal(err)
	}

	err = CancelGraphRun(runTestSession, run.ID)
	var cerr *CancelIncompleteError
	if !errors.As(err, &cerr) || len(cerr.Cleanup) != 1 || !strings.Contains(err.Error(), "spawn registry") {
		t.Fatalf("an unreadable registry must fail the cancel, got %v", err)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceling {
		t.Errorf("run %q, want canceling", r.State)
	}
	if len(f.killed) != 0 {
		t.Errorf("nothing is stopped without a registry, killed %v", f.killed)
	}

	if err := os.Remove(registry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("re-cancel with the registry readable: %v", err)
	}
	if len(f.killed) != 1 {
		t.Errorf("the worker must be stopped on re-cancel, killed %v", f.killed)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceled {
		t.Errorf("run %q, want canceled", r.State)
	}
}

// TestCancelFailsClosedOnRetractionFailure pins the retraction half of
// must-fix 2: a delegated request that cannot be withdrawn keeps the run
// canceling, and a re-cancel withdraws it once the inbox is writable.
func TestCancelFailsClosedOnRetractionFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permission this test relies on")
	}
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	f.distinctIDs = true
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	delegated := delegateFrom(t, st.TaskID, "run")

	inboxDir := filepath.Dir(InboxPath(runTestSession, "run"))
	if err := os.Chmod(inboxDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(inboxDir, 0755) })

	err := CancelGraphRun(runTestSession, run.ID)
	var cerr *CancelIncompleteError
	if !errors.As(err, &cerr) || len(cerr.Cleanup) == 0 || !strings.Contains(err.Error(), "withdraw from run inbox") {
		t.Fatalf("a failed retraction must fail the cancel, got %v", err)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceling {
		t.Errorf("run %q, want canceling", r.State)
	}

	if err := os.Chmod(inboxDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("re-cancel with the inbox writable: %v", err)
	}
	if inboxHas(t, "run", delegated.ID) {
		t.Error("the delegated request must be withdrawn on re-cancel")
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceled {
		t.Errorf("run %q, want canceled", r.State)
	}
}

// readOnlyTask makes a task file unwritable until the test restores it.
func readOnlyTask(t *testing.T, taskID string) (restore func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the file permission this test relies on")
	}
	path := TaskPath(runTestSession, taskID)
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	restore = func() { _ = os.Chmod(path, 0644) }
	t.Cleanup(restore)
	return restore
}

// cancelIncomplete asserts a cancel failed closed on cleanup alone — no
// survivor — naming want, and that retry is refused without claiming a
// worker survived.
func cancelIncomplete(t *testing.T, runID, want string) {
	t.Helper()
	err := CancelGraphRun(runTestSession, runID)
	var cerr *CancelIncompleteError
	if !errors.As(err, &cerr) || len(cerr.Survivors) != 0 || !strings.Contains(err.Error(), want) {
		t.Fatalf("cancel must fail closed on %q with no survivor, got %v", want, err)
	}
	if r, _ := ReadGraphRun(runTestSession, runID); r.State != GraphRunCanceling {
		t.Errorf("run %q, want canceling", r.State)
	}
	_, rerr := RetryGraphRun(runTestSession, runID, "w")
	if rerr == nil || strings.Contains(rerr.Error(), "worker that survived") || !strings.Contains(rerr.Error(), "incomplete cancel") {
		t.Errorf("retry refusal must name an incomplete cancel, not a survivor: %v", rerr)
	}
}

func recancel(t *testing.T, runID string) {
	t.Helper()
	if err := CancelGraphRun(runTestSession, runID); err != nil {
		t.Fatalf("re-cancel once cleanup can succeed: %v", err)
	}
	if r, _ := ReadGraphRun(runTestSession, runID); r.State != GraphRunCanceled {
		t.Errorf("run %q, want canceled", r.State)
	}
}

// TestCancelFailsClosedOnUnexpirableDelegatedTask pins review must-fix 3:
// a worker's in-flight task whose file cannot be written stays in-flight,
// so the cancel must not count it expired or report canceled.
func TestCancelFailsClosedOnUnexpirableDelegatedTask(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	f.distinctIDs = true
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	delegated := delegateFrom(t, st.TaskID, "run")
	restore := readOnlyTask(t, delegated.ID)

	cancelIncomplete(t, run.ID, "expire task "+delegated.ID)
	if task, _ := ReadTask(runTestSession, delegated.ID); task.Status != TaskInFlight {
		t.Fatalf("fixture: an unwritable task stays %q, want in-flight", task.Status)
	}

	restore()
	recancel(t, run.ID)
	if task, _ := ReadTask(runTestSession, delegated.ID); task.Status != TaskTimedOut {
		t.Errorf("delegated task %q after re-cancel, want timed-out", task.Status)
	}
}

// The node-correlated half: a running send node's task that cannot be
// expired would let the stall watchdog re-drive a canceled node.
func TestCancelFailsClosedOnUnexpirableNodeTask(t *testing.T) {
	run := createTestRun(t, linearGraph())
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if task, err := ReadTask(runTestSession, st.TaskID); err != nil || task.Status != TaskInFlight {
		t.Fatalf("fixture: node a must hold an in-flight task, got %+v %v", task, err)
	}
	restore := readOnlyTask(t, st.TaskID)

	cancelIncomplete(t, run.ID, "expire node a task "+st.TaskID)

	restore()
	recancel(t, run.ID)
	if task, _ := ReadTask(runTestSession, st.TaskID); task.Status != TaskTimedOut {
		t.Errorf("node task %q after re-cancel, want timed-out", task.Status)
	}
}

// A map node's TaskID is its joined worker roles, not a task id. With
// twenty workers the joined list is too long to be a filename, so treating
// it as one fails with ENAMETOOLONG on every cancel (review must-fix,
// 2026-09-24). The cancel must complete, and a re-cancel is clean.
func TestCancelLargeMapIsNotATaskID(t *testing.T) {
	items := make([]string, 20)
	for i := range items {
		items[i] = fmt.Sprintf("item%d", i)
	}
	g := &Graph{Name: "cancel-map", Start: "m",
		Nodes: []Node{{ID: "m", Type: NodeMap, Role: "edit", Items: strings.Join(items, ","), Message: "handle ${item}"}}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)
	f.distinctIDs = true
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "m")
	if len(filepath.Base(TaskPath(runTestSession, st.TaskID))) <= 255 {
		t.Fatalf("fixture: joined roles must overflow a filename, got %d bytes", len(st.TaskID))
	}

	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("cancel of a large map: %v", err)
	}
	if len(f.killed) != len(items) {
		t.Errorf("every map worker must be stopped, killed %d of %d", len(f.killed), len(items))
	}
	recancel(t, run.ID)
}

// An unparseable task file's sender is unknown, so it may be the worker's
// delegation: the cancel fails closed rather than skip it. ListTasks keeps
// skipping it — the negative control that only the cancel is strict.
func TestCancelFailsClosedOnUnreadableTaskFile(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	f.distinctIDs = true
	step(t, runTestSession, run.ID)
	corrupt := filepath.Join(TaskDir(runTestSession), "corrupt.json")
	if err := os.MkdirAll(TaskDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corrupt, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ListTasks(runTestSession, TaskInFlight); err != nil {
		t.Errorf("ListTasks must still skip an invalid file, got %v", err)
	}

	cancelIncomplete(t, run.ID, "task file corrupt.json")

	if err := os.Remove(corrupt); err != nil {
		t.Fatal(err)
	}
	recancel(t, run.ID)
}

// Copilot on PR #89, cancel_authority.go:82: a spawn stop that cannot take an
// existing run's lock is refused, never decided unlocked — the worker is left
// running for a retry. Negative control: once the lock is free, it stops.
func TestStopSpawnAuthorizedRefusesWithoutTheRunLock(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	e, _ := findSpawnByRole(runTestSession, st.TaskID)

	prev := graphRunLockWait
	graphRunLockWait = 50 * time.Millisecond
	t.Cleanup(func() { graphRunLockWait = prev })
	unlock, err := lockGraphRun(runTestSession, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := StopSpawnAuthorized(runTestSession, e.ID); err == nil || !strings.Contains(err.Error(), "NOT stopped") {
		t.Errorf("a stop that cannot take the run lock must be refused, got %v", err)
	}
	if len(f.killed) != 0 {
		t.Errorf("nothing may be stopped without the lock, killed %v", f.killed)
	}
	unlock()
	if err := StopSpawnAuthorized(runTestSession, e.ID); err != nil {
		t.Errorf("with the lock free the user's stop proceeds, got %v", err)
	}
}

// Copilot on PR #89, graph_cancel.go:104: artifacts are purged only after every
// worker is stopped — a surviving worker may still be writing them.
func TestCancelPurgesArtifactsOnlyAfterWorkersStop(t *testing.T) {
	purges := 0
	prev := cancelPurgeFn
	cancelPurgeFn = func(string, string) *ArtifactPurgeResult { purges++; return nil }
	t.Cleanup(func() { cancelPurgeFn = prev })

	run := createTestRun(t, spawnCancelGraph())
	fakeLiveSpawns(t)
	step(t, runTestSession, run.ID)
	spawnKillWindowFn = func(string, string) error { return errors.New("kill-window: no server") }
	if err := CancelGraphRun(runTestSession, run.ID); err == nil {
		t.Fatal("a surviving worker must fail the cancel")
	}
	if purges != 0 {
		t.Errorf("artifacts purged %d time(s) while a worker survived", purges)
	}
	spawnKillWindowFn = func(string, string) error { return nil }
	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("re-cancel: %v", err)
	}
	if purges != 1 {
		t.Errorf("artifacts must be purged once the workers are stopped, got %d", purges)
	}
}

// sendAgentRead starts a linear run and has the build agent read node a's
// request, leaving a receipt; the agent's pane reads idle or busy per idle.
func sendAgentRead(t *testing.T, idle *bool) (*GraphRun, string) {
	t.Helper()
	if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	prev := graphAgentIdleFn
	graphAgentIdleFn = func(string, string) bool { return *idle }
	t.Cleanup(func() { graphAgentIdleFn = prev })
	run := createTestRun(t, linearGraph())
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if _, err := Receive(runTestSession, "build"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadReceipt(runTestSession, st.TaskID); !ok {
		t.Fatal("fixture: the build agent's read must leave a receipt")
	}
	return run, st.TaskID
}

// Copilot on PR #89, graph_cancel.go:146: a send node whose agent read the
// request and is still busy is working for the run, so the cancel fails closed
// — and purges nothing — until it answers. A timed-out task is no proof the
// agent stopped, so the daemon timing it out changes nothing. Once the agent
// answers, a re-cancel completes and purges once. (An unread request is
// withdrawn instead — TestExecCancelMidRun.)
func TestCancelFailsClosedOnARunningSendAgent(t *testing.T) {
	purges := 0
	prevPurge := cancelPurgeFn
	cancelPurgeFn = func(string, string) *ArtifactPurgeResult { purges++; return nil }
	t.Cleanup(func() { cancelPurgeFn = prevPurge })
	idle := false
	run, taskID := sendAgentRead(t, &idle)

	for _, stage := range []string{"agent working", "task timed out, agent still working"} {
		if stage == "task timed out, agent still working" {
			TimeoutTask(runTestSession, taskID)
		}
		err := CancelGraphRun(runTestSession, run.ID)
		var cerr *CancelIncompleteError
		if !errors.As(err, &cerr) || !strings.Contains(err.Error(), "still working on the request") {
			t.Fatalf("%s: the cancel must fail closed, got %v", stage, err)
		}
		if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunCanceling {
			t.Errorf("%s: run %q, want canceling", stage, r.State)
		}
		if purges != 0 {
			t.Errorf("%s: artifacts purged while the send agent works", stage)
		}
	}

	CompleteTask(runTestSession, taskID, "resp-"+taskID)
	recancel(t, run.ID)
	if purges != 1 {
		t.Errorf("artifacts must be purged once the agent answered, got %d", purges)
	}
}

// A missing or malformed delivery record is no proof the request went unread —
// Receive drains the inbox before its best-effort receipt write — so with the
// agent busy the cancel still fails closed; once the agent is idle it proceeds.
func TestCancelTreatsLostReceiptAsUnknown(t *testing.T) {
	for _, c := range []struct {
		name   string
		damage func(path string) error
	}{
		{"missing record", os.Remove},
		{"malformed record", func(p string) error { return os.WriteFile(p, []byte("{truncated"), 0644) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			idle := false
			run, taskID := sendAgentRead(t, &idle)
			if err := c.damage(DeliveryPath(runTestSession, taskID)); err != nil {
				t.Fatal(err)
			}
			err := CancelGraphRun(runTestSession, run.ID)
			if err == nil || !strings.Contains(err.Error(), "still working on the request") {
				t.Fatalf("a lost receipt with a busy agent must fail closed, got %v", err)
			}
			idle = true
			recancel(t, run.ID)
		})
	}
}

// Negative control: an agent that read the request but sits idle without
// answering — finished, abandoned or restarted — is working on nothing, so
// it never blocks a cancel.
func TestCancelProceedsPastAnIdleSendAgent(t *testing.T) {
	idle := true
	run, taskID := sendAgentRead(t, &idle)
	recancel(t, run.ID)
	if task, _ := ReadTask(runTestSession, taskID); task.Status != TaskTimedOut {
		t.Errorf("the idle agent's task %q, want timed-out so it cannot be re-driven", task.Status)
	}
}

// Copilot on PR #89, graph_cancel.go:181: a malformed spawn registry line could
// be a live worker of this run, so it fails the cancel rather than read as
// absent. Negative control: the registry repaired, the cancel completes.
func TestCancelFailsClosedOnMalformedRegistryLine(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	fakeLiveSpawns(t)
	step(t, runTestSession, run.ID)
	registry := SpawnPath(runTestSession)
	good, err := os.ReadFile(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, append(append([]byte{}, good...), []byte("{truncated\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	if entries, err := ReadSpawnEntries(runTestSession); err != nil || len(entries) == 0 {
		t.Fatalf("ReadSpawnEntries must still skip a malformed line for other callers, got %d, %v", len(entries), err)
	}

	cancelIncomplete(t, run.ID, "malformed spawn registry")

	if err := os.WriteFile(registry, good, 0644); err != nil {
		t.Fatal(err)
	}
	recancel(t, run.ID)
}

// TestStepSkipsWhileCancelHoldsRun: a tick that finds the run lock held
// dispatches nothing and is not an error.
func TestStepSkipsWhileCancelHoldsRun(t *testing.T) {
	run := createTestRun(t, spawnCancelGraph())
	f := fakeLiveSpawns(t)
	unlock, err := lockGraphRun(runTestSession, run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run.ID)
	unlock()
	if f.fresh != 0 {
		t.Fatalf("a tick under a held run lock must spawn nothing, %d started", f.fresh)
	}
	step(t, runTestSession, run.ID)
	if f.fresh != 1 {
		t.Errorf("the next tick after release dispatches, %d started", f.fresh)
	}
}
