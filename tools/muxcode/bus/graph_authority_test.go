package bus

import (
	"strings"
	"testing"
	"time"
)

// ownedWorkerRun creates a running graph run owning a build send node and
// registers a spawn worker belonging to it, returning the worker's role.
func ownedWorkerRun(t *testing.T) (*GraphRun, string) {
	t.Helper()
	g := &Graph{
		Name:  "t",
		Start: "impl",
		Nodes: []Node{
			{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "work"},
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
		},
		Edges: []Edge{{From: "impl", To: "build"}},
	}
	run := createTestRun(t, g)
	worker := "spawn-owned01"
	if err := appendSpawnEntry(runTestSession, SpawnEntry{
		ID: worker, Role: "edit", SpawnRole: worker, Owner: graphSender,
		Status: "running", StartedAt: time.Now().Unix(),
		RunID: run.ID, NodeID: "impl",
	}); err != nil {
		t.Fatalf("append spawn entry: %v", err)
	}
	return run, worker
}

func TestCheckGraphNodeAuthority_RefusesOwnedRole(t *testing.T) {
	_, worker := ownedWorkerRun(t)

	deny := CheckGraphNodeAuthority(runTestSession, worker, "build", "build")
	if deny == "" {
		t.Fatal("a worker delegating the build its own run owns must be refused")
	}
	if !strings.Contains(deny, "build") {
		t.Errorf("denial should name the owned role: %q", deny)
	}
}

// TestCheckGraphNodeAuthority_AllowsUnownedAction is the negative control for
// key granularity: a run owns a node's work, not every message to the agent
// that performs it. A guard keyed on destination role alone would pass the
// case above and fail here by refusing an ordinary question.
func TestCheckGraphNodeAuthority_AllowsUnownedAction(t *testing.T) {
	_, worker := ownedWorkerRun(t)

	if deny := CheckGraphNodeAuthority(runTestSession, worker, "build", "notify"); deny != "" {
		t.Errorf("run owns build:build, not build:notify — must be allowed: %q", deny)
	}
}

// TestCheckGraphNodeAuthority_AllowsUnownedRole is the negative control: the
// guard constrains only what the run actually owns. A guard that refused every
// send from a worker would pass the case above and fail here.
func TestCheckGraphNodeAuthority_AllowsUnownedRole(t *testing.T) {
	_, worker := ownedWorkerRun(t)

	if deny := CheckGraphNodeAuthority(runTestSession, worker, "research", "research"); deny != "" {
		t.Errorf("run owns no research node, send must be allowed: %q", deny)
	}
}

// TestCheckGraphNodeAuthority_AllowsOrdinaryRoles pins that nothing outside a
// graph-owned worker is constrained — the graph's own dispatches and the
// human's hand through edit must pass untouched.
func TestCheckGraphNodeAuthority_AllowsOrdinaryRoles(t *testing.T) {
	ownedWorkerRun(t)

	for _, from := range []string{graphSender, "edit", "test", "spawn-unowned"} {
		if deny := CheckGraphNodeAuthority(runTestSession, from, "build", "build"); deny != "" {
			t.Errorf("%s → build must be allowed, got %q", from, deny)
		}
	}
}

// TestCheckGraphNodeAuthority_AllowsAfterRunEnds pins that a finished run
// releases its workers: ownership is scoped to a run still in flight.
func TestCheckGraphNodeAuthority_AllowsAfterRunEnds(t *testing.T) {
	run, worker := ownedWorkerRun(t)

	if deny := CheckGraphNodeAuthority(runTestSession, worker, "build", "build"); deny == "" {
		t.Fatal("precondition: running run must refuse")
	}
	if err := UpdateGraphRunState(runTestSession, run.ID, GraphRunComplete); err != nil {
		t.Fatalf("complete run: %v", err)
	}
	if deny := CheckGraphNodeAuthority(runTestSession, worker, "build", "build"); deny != "" {
		t.Errorf("a completed run must not keep constraining its worker: %q", deny)
	}
}

// TestGraphOwnsRunningSendNode covers the hook-chain provenance test: only a
// send node actually running counts, so an ordinary build chains normally.
func TestGraphOwnsRunningSendNode(t *testing.T) {
	run, _ := ownedWorkerRun(t)

	if _, _, ok := GraphOwnsRunningSendNode(runTestSession, "build"); ok {
		t.Fatal("a pending build node must not suppress the chain — nothing is running yet")
	}
	for _, state := range []string{GraphNodeReady, GraphNodeRunning} {
		if err := TransitionGraphNode(runTestSession, run.ID, "build", state, nil); err != nil {
			t.Fatalf("transition build to %s: %v", state, err)
		}
	}
	runID, nodeID, ok := GraphOwnsRunningSendNode(runTestSession, "build")
	if !ok || runID != run.ID || nodeID != "build" {
		t.Errorf("running build node must be owned, got %q/%q ok=%v", runID, nodeID, ok)
	}

	// A node still marked running whose dispatch already completed no longer
	// owns the agent: an ordinary build must chain normally in that window.
	dispatch := NewMessage(graphSender, "build", "request", "build", "go", "")
	if err := CreateTask(runTestSession, dispatch, 600); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := MutateNodeStatus(runTestSession, run.ID, "build",
		func(s *GraphNodeStatus) { s.TaskID = dispatch.ID }); err != nil {
		t.Fatalf("stamp task id: %v", err)
	}
	if _, _, ok := GraphOwnsRunningSendNode(runTestSession, "build"); !ok {
		t.Error("an in-flight dispatch must still be owned")
	}
	CompleteTask(runTestSession, dispatch.ID, "resp-1")
	if _, _, ok := GraphOwnsRunningSendNode(runTestSession, "build"); ok {
		t.Error("a completed dispatch must release the chain — the agent is free")
	}
	if _, _, ok := GraphOwnsRunningSendNode(runTestSession, "test"); ok {
		t.Error("run declares no test node — its chain must still fire")
	}
}
