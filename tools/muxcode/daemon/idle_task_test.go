package daemon

import (
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// TestIdleTaskWatchdogExemptsGraphDispatch pins the executor exemption in
// checkIdleTaskCompletion: an idle agent holding a graph-dispatched task
// with stale retry bookkeeping is neither re-queued nor rescued — the task
// stays in flight for the executor's own stall path and its tracking is
// dropped — while an otherwise identical plain task still takes the
// watchdog's rescue (review should-fix 2026-09-09; the un-exempted
// watchdog synthesized a commit node's reply at 75s and parked it on an
// unverified hold).
//
// The target role's provider is pinned to claude: the watchdog skips
// scrape-road roles (checkNonHookTasks owns them), and this repo runs
// build on codex, which resolves to the scrape road wherever no codex
// binary is installed — CI run 34360098424 failed exactly the three
// assertions below because neither task was ever examined.
func TestIdleTaskWatchdogExemptsGraphDispatch(t *testing.T) {
	session := testSession(t)
	t.Setenv(bus.RoleCLIEnvVar("build"), "claude")
	d := New(session, 5, 8)
	origIdle := agentIdleFn
	agentIdleFn = func(string, string) bool { return true }
	t.Cleanup(func() { agentIdleFn = origIdle })

	g := &bus.Graph{Name: "g", Start: "a",
		Nodes: []bus.Node{{ID: "a", Type: bus.NodeSend, Role: "build", Action: "build", Message: "build it"}}}
	run, err := bus.CreateGraphRun(session, g, "g", "intent")
	if err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	if err := bus.StepGraphRun(session, run.ID); err != nil {
		t.Fatalf("StepGraphRun: %v", err)
	}
	st, err := bus.ReadNodeStatus(session, run.ID, "a")
	if err != nil || st.TaskID == "" {
		t.Fatalf("dispatch must create a task: %+v %v", st, err)
	}
	owned := st.TaskID

	plain := bus.NewMessage("edit", "build", "request", "build", "plain work", "")
	if err := bus.CreateTask(session, plain, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Both tasks old enough to act on, both past the retry phase.
	now := time.Now().Unix() + 120
	for _, id := range []string{owned, plain.ID} {
		d.idleTaskFirstSeen[id] = now - idleTaskGracePeriod - 1
		d.idleTaskRetried[id] = true
	}
	d.lastIdleTaskCheck = 0
	d.checkIdleTaskCompletionAt(now)

	if task, err := bus.ReadTask(session, owned); err != nil || task.Status != bus.TaskInFlight {
		t.Fatalf("executor-owned task must stay in flight, got %+v %v", task, err)
	}
	if _, seen := d.idleTaskFirstSeen[owned]; seen {
		t.Error("executor-owned task must be dropped from watchdog tracking")
	}
	if _, seen := d.idleTaskRetried[owned]; seen {
		t.Error("executor-owned task must be dropped from retry bookkeeping")
	}
	if msgs, _ := bus.Peek(session, "build"); len(msgs) != 1 {
		t.Errorf("no re-queue for an executor-owned task — build inbox: %+v", msgs)
	}

	// Control: the plain task follows the watchdog path and is rescued.
	if task, err := bus.ReadTask(session, plain.ID); err != nil || task.Status != bus.TaskCompleted {
		t.Fatalf("plain task must be rescued with a synthetic response, got %+v %v", task, err)
	}
}
