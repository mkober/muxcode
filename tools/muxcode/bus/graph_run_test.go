package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const runTestSession = "graphtest"

// createTestRun creates a run from the given graph in an isolated bus dir.
func createTestRun(t *testing.T, g *Graph) *GraphRun {
	t.Helper()
	useTempBusDir(t)
	run, err := CreateGraphRun(runTestSession, g, g.Name, "test intent")
	if err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	return run
}

// finish drives a node through running to a terminal state with an outcome.
func finish(t *testing.T, runID, nodeID, terminal, outcome string) {
	t.Helper()
	st, err := ReadNodeStatus(runTestSession, runID, nodeID)
	if err != nil {
		t.Fatalf("read %s: %v", nodeID, err)
	}
	if st.State == GraphNodePending {
		if err := TransitionGraphNode(runTestSession, runID, nodeID, GraphNodeReady, nil); err != nil {
			t.Fatalf("ready %s: %v", nodeID, err)
		}
	}
	if err := TransitionGraphNode(runTestSession, runID, nodeID, GraphNodeRunning, nil); err != nil {
		t.Fatalf("run %s: %v", nodeID, err)
	}
	if err := TransitionGraphNode(runTestSession, runID, nodeID, terminal, func(s *GraphNodeStatus) {
		s.Outcome = outcome
	}); err != nil {
		t.Fatalf("finish %s: %v", nodeID, err)
	}
}

func TestCreateGraphRunInitialState(t *testing.T) {
	run := createTestRun(t, linearGraph())

	got, err := ReadGraphRun(runTestSession, run.ID)
	if err != nil {
		t.Fatalf("ReadGraphRun: %v", err)
	}
	if got.State != GraphRunRunning || got.Intent != "test intent" {
		t.Errorf("unexpected run: %+v", got)
	}

	statuses, err := ReadAllNodeStatuses(runTestSession, run.ID)
	if err != nil {
		t.Fatalf("ReadAllNodeStatuses: %v", err)
	}
	if statuses["a"].State != GraphNodeReady {
		t.Errorf("start node state %q, want ready", statuses["a"].State)
	}
	if statuses["b"].State != GraphNodePending {
		t.Errorf("node b state %q, want pending", statuses["b"].State)
	}

	frozen, err := ReadGraphRunGraph(runTestSession, run.ID)
	if err != nil {
		t.Fatalf("ReadGraphRunGraph: %v", err)
	}
	if frozen.Name != "t" || len(frozen.Nodes) != 2 {
		t.Errorf("frozen graph mismatch: %+v", frozen)
	}
}

func TestCreateGraphRunRejectsInvalid(t *testing.T) {
	useTempBusDir(t)
	g := linearGraph()
	g.Start = "zz"
	if _, err := CreateGraphRun(runTestSession, g, "t", ""); err == nil {
		t.Error("expected error creating run from invalid graph")
	}
}

func TestTransitionLegality(t *testing.T) {
	run := createTestRun(t, linearGraph())

	// pending -> running skips ready: illegal.
	if err := TransitionGraphNode(runTestSession, run.ID, "b", GraphNodeRunning, nil); err == nil {
		t.Error("pending->running must be illegal")
	}
	// ready -> done skips running: illegal.
	if err := TransitionGraphNode(runTestSession, run.ID, "a", GraphNodeDone, nil); err == nil {
		t.Error("ready->done must be illegal")
	}
	// Legal chain: ready -> running -> done.
	if err := TransitionGraphNode(runTestSession, run.ID, "a", GraphNodeRunning, nil); err != nil {
		t.Fatalf("ready->running: %v", err)
	}
	if err := TransitionGraphNode(runTestSession, run.ID, "a", GraphNodeDone, func(s *GraphNodeStatus) {
		s.Outcome = OutcomeSuccess
	}); err != nil {
		t.Fatalf("running->done: %v", err)
	}
	// done -> running without re-arm: illegal.
	if err := TransitionGraphNode(runTestSession, run.ID, "a", GraphNodeRunning, nil); err == nil {
		t.Error("done->running must be illegal")
	}
	// done -> ready re-arms and clears the previous pass.
	if err := TransitionGraphNode(runTestSession, run.ID, "a", GraphNodeReady, nil); err != nil {
		t.Fatalf("done->ready re-arm: %v", err)
	}
	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.Outcome != "" || st.StartedAt != 0 || st.DoneAt != 0 {
		t.Errorf("re-arm did not clear previous pass: %+v", st)
	}
}

func TestTransitionTimestamps(t *testing.T) {
	run := createTestRun(t, linearGraph())
	finish(t, run.ID, "a", GraphNodeDone, OutcomeSuccess)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.StartedAt == 0 || st.DoneAt == 0 {
		t.Errorf("timestamps not stamped: %+v", st)
	}
	if st.Outcome != OutcomeSuccess {
		t.Errorf("outcome %q, want success", st.Outcome)
	}
}

func TestIdempotentRePersist(t *testing.T) {
	run := createTestRun(t, linearGraph())
	before, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if err := atomicWriteJSON(graphNodePath(runTestSession, run.ID, "a"), before); err != nil {
		t.Fatalf("re-persist: %v", err)
	}
	after, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if *after != *before {
		t.Errorf("re-persist changed status: %+v vs %+v", after, before)
	}
}

func TestUpdateGraphRunState(t *testing.T) {
	run := createTestRun(t, linearGraph())
	if err := UpdateGraphRunState(runTestSession, run.ID, GraphRunCanceled); err != nil {
		t.Fatalf("UpdateGraphRunState: %v", err)
	}
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunCanceled {
		t.Errorf("state %q, want canceled", got.State)
	}
}

func TestScanInFlightGraphRuns(t *testing.T) {
	useTempBusDir(t)
	g := linearGraph()
	r1, err := CreateGraphRun(runTestSession, g, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := CreateGraphRun(runTestSession, g, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateGraphRunState(runTestSession, r2.ID, GraphRunComplete); err != nil {
		t.Fatal(err)
	}

	inflight := ScanInFlightGraphRuns(runTestSession)
	if len(inflight) != 1 || inflight[0].ID != r1.ID {
		t.Errorf("expected only %s in flight, got %+v", r1.ID, inflight)
	}
}

// TestCrashResumeExecutor pins that a run resumes purely from the
// persisted store: node a's work completes, then the "restarted" executor
// — StepGraphRun holds no in-memory state and reads run, graph, and node
// statuses fresh from disk on every call — must route the completion and
// dispatch b. The daemon-process version of this (kill and restart the
// real daemon mid-run) lives in scripts/test-graph-orchestrator.sh.
func TestCrashResumeExecutor(t *testing.T) {
	run := createTestRun(t, linearGraph())
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeDone {
		t.Errorf("a state %q after resume tick, want done", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Errorf("b state %q after resume tick, want running", s)
	}
}

func TestFormatGraphRun(t *testing.T) {
	run := createTestRun(t, linearGraph())
	finish(t, run.ID, "a", GraphNodeDone, OutcomeSuccess)
	g, _ := ReadGraphRunGraph(runTestSession, run.ID)
	statuses, _ := ReadAllNodeStatuses(runTestSession, run.ID)
	out := FormatGraphRun(run, g, statuses)
	for _, want := range []string{run.ID, "running", "done", "pending", "outcome=success"} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatGraphRun output missing %q:\n%s", want, out)
		}
	}
}

// TestFormatGraphRunShowsFailedNodeReason pins the operator-facing decline
// visibility (MUX-114): a failed node's output renders under its row, a
// done node's output does not (negative control — done outputs are
// harvested payloads, not failure reasons).
func TestFormatGraphRunShowsFailedNodeReason(t *testing.T) {
	g := linearGraph()
	run := &GraphRun{ID: "r1", Template: "t", State: GraphRunFailed, CreatedAt: time.Now().Unix()}
	statuses := map[string]*GraphNodeStatus{
		"a": {NodeID: "a", State: GraphNodeFailed, Outcome: OutcomeFailure,
			Output: "spec-complete guard declined: 2 open items in spec.md: one; two"},
		"b": {NodeID: "b", State: GraphNodeDone, Outcome: OutcomeSuccess,
			Output: "harvested response payload"},
	}
	out := FormatGraphRun(run, g, statuses)
	if !strings.Contains(out, "2 open items") || !strings.Contains(out, "one; two") {
		t.Errorf("failed node reason not rendered:\n%s", out)
	}
	if strings.Contains(out, "harvested response payload") {
		t.Errorf("done node output must not render:\n%s", out)
	}
}

// TestFormatGraphRunConditionBranchNeutral pins the condition-node
// display rule (ConditionTookBranch): a condition that took its false
// branch renders "branched", never "failed" — a red failure row on a
// completed run read as a broken pipeline. The negative control keeps
// the rule from over-reaching: a genuinely failed send node must keep
// "failed" and never gain "branched".
func TestFormatGraphRunConditionBranchNeutral(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "cond",
		Nodes: []Node{
			{ID: "cond", Type: NodeCondition, Conditions: map[string]any{"env_set": "X"}},
			{ID: "b", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
		},
		Edges: []Edge{{From: "cond", To: "b", Outcome: OutcomeFailure}},
	}
	run := &GraphRun{ID: "r2", Template: "t", State: GraphRunComplete, CreatedAt: time.Now().Unix()}

	out := FormatGraphRun(run, g, map[string]*GraphNodeStatus{
		"cond": {NodeID: "cond", State: GraphNodeFailed, Outcome: OutcomeFailure},
		"b":    {NodeID: "b", State: GraphNodeDone, Outcome: OutcomeSuccess},
	})
	if !strings.Contains(out, "branched") {
		t.Errorf("condition false branch not rendered as branched:\n%s", out)
	}
	if strings.Contains(out, "failed") {
		t.Errorf("condition false branch rendered as failed:\n%s", out)
	}

	out = FormatGraphRun(run, g, map[string]*GraphNodeStatus{
		"cond": {NodeID: "cond", State: GraphNodeDone, Outcome: OutcomeSuccess},
		"b":    {NodeID: "b", State: GraphNodeFailed, Outcome: OutcomeFailure, Output: "build broke"},
	})
	if !strings.Contains(out, "failed") {
		t.Errorf("failed send node lost its failed rendering:\n%s", out)
	}
	if strings.Contains(out, "branched") {
		t.Errorf("failed send node rendered as branched:\n%s", out)
	}
}

// TestCreateGraphRunDerivesLoopCap pins spec-derived caps (MUX-121): the
// frozen definition carries cap = phase count, and a derived-cap graph
// with no active spec refuses to start rather than guessing a bound.
func TestCreateGraphRunDerivesLoopCap(t *testing.T) {
	g := linearGraph()
	g.Edges = append(g.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure, MaxIterationsFromSpec: true})

	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	spec := filepath.Join(repo, "spec.md")
	content := "### Phase 1: A\n- [ ] a\n### Phase 2: B\n- [ ] b\n### Phase 3: C\n- [ ] c\n"
	if err := os.WriteFile(spec, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, spec); err != nil {
		t.Fatal(err)
	}
	run, err := CreateGraphRun(runTestSession, g, "t", "x")
	if err != nil {
		t.Fatalf("derived-cap run must start with an active spec: %v", err)
	}
	frozen, err := ReadGraphRunGraph(runTestSession, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotCap := 0
	for _, e := range frozen.Edges {
		if e.From == "b" && e.To == "a" {
			gotCap = e.MaxIterations
		}
	}
	if gotCap != 3 {
		t.Errorf("frozen loop cap = %d, want 3 (spec phase count)", gotCap)
	}

	if err := ClearActiveSpec(runTestSession); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateGraphRun(runTestSession, g, "t", "x"); err == nil {
		t.Error("derived-cap graph with no active spec must refuse to start")
	}
}

func TestNewGraphRunIDSanitizesName(t *testing.T) {
	id := NewGraphRunID("../evil/name x")
	if strings.ContainsAny(id, "/ .") {
		t.Errorf("run id %q contains path or space characters", id)
	}
}

func TestAtomicWriteLeavesNoTmp(t *testing.T) {
	run := createTestRun(t, linearGraph())
	path := graphNodePath(runTestSession, run.ID, "a")
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file left behind after atomic write")
	}
}
