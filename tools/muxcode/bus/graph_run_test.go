package bus

import (
	"encoding/json"
	"errors"
	"fmt"
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
// completed run read as a broken pipeline. Under MUX-133 option B a
// branch-taker is state=done with outcome=failure, the failure outcome
// being the routing key the false edge matches.
//
// Two negative controls keep the rule from over-reaching: a genuinely
// failed send node must keep "failed" and never gain "branched", and a
// condition that genuinely failed to evaluate (state=failed) must also
// render "failed" — the distinction option B exists to make.
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
		"cond": {NodeID: "cond", State: GraphNodeDone, Outcome: OutcomeFailure},
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

	// A condition that could not be evaluated is a real error, not a
	// branch selection — the state carries the distinction.
	out = FormatGraphRun(run, g, map[string]*GraphNodeStatus{
		"cond": {NodeID: "cond", State: GraphNodeFailed, Outcome: OutcomeFailure, Output: "condition evaluation error: unknown condition type bogus"},
		"b":    {NodeID: "b", State: GraphNodeDone, Outcome: OutcomeSuccess},
	})
	if !strings.Contains(out, "failed") {
		t.Errorf("unevaluatable condition lost its failed rendering:\n%s", out)
	}
	if strings.Contains(out, "branched") {
		t.Errorf("unevaluatable condition rendered as branched — a broken predicate must not read as control flow:\n%s", out)
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

// pinActor fixes what BusActor reports for one test. The suite itself runs
// inside an agent pane that already exports AGENT_ROLE, so a test reading the
// ambient value would assert against whichever agent happened to run it.
func pinActor(t *testing.T, actor string) {
	t.Helper()
	t.Setenv("AGENT_ROLE", actor)
	t.Setenv("BUS_ROLE", "")
	pinProcessTable(t, fmt.Sprintf("%d 1 /bin/bash\n", os.Getpid()), nil)
}

// pinProcessTable replaces the table agentRuntimeAncestor walks. Every actor
// test pins one: the suite is itself run by an agent, so reading the real tree
// would resolve an identity-less actor to whichever runtime ran the tests.
func pinProcessTable(t *testing.T, out string, err error) {
	t.Helper()
	orig := psListRunner
	psListRunner = func() (string, error) { return out, err }
	t.Cleanup(func() { psListRunner = orig })
}

// pinAgentAncestry presents the bypass being defended against: an agent whose
// environment carries no identity but whose ancestry still names its runtime.
func pinAgentAncestry(t *testing.T, command string) {
	t.Helper()
	t.Setenv("AGENT_ROLE", "")
	t.Setenv("BUS_ROLE", "")
	const ancestorPID = 424242
	pinProcessTable(t, fmt.Sprintf("%d %d /bin/bash\n%d 1 %s\n",
		os.Getpid(), ancestorPID, ancestorPID, command), nil)
}

// editEventCount counts one action's events in edit's inbox mentioning runID.
func editEventCount(t *testing.T, action, runID string) int {
	t.Helper()
	msgs, _ := Peek(runTestSession, "edit")
	var n int
	for _, m := range msgs {
		if m.Action == action && strings.Contains(m.Payload, runID) {
			n++
		}
	}
	return n
}

func actorGateGraph() *Graph {
	return &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
}

func TestBusActorReportsUserWithoutAgentIdentity(t *testing.T) {
	pinActor(t, "")
	if got := BusActor(); got != ActorUser {
		t.Errorf("BusActor() = %q, want %q — a shell carrying no agent identity is a person", got, ActorUser)
	}
	pinActor(t, "edit")
	if got := BusActor(); got != "edit" {
		t.Errorf("BusActor() = %q, want edit", got)
	}
}

// The bypass the ancestry check exists to close: BusActor reads a missing
// AGENT_ROLE as a person, so `env -u AGENT_ROLE` would otherwise launder an
// agent into one.
func TestBusActorVerifiedCatchesStrippedIdentity(t *testing.T) {
	for _, cmd := range []string{"/usr/local/bin/claude", "/opt/homebrew/bin/opencode", "codex"} {
		pinAgentAncestry(t, cmd)
		if got := BusActor(); got != ActorUser {
			t.Fatalf("BusActor() = %q, want %q — the premise is that env alone reads as a person", got, ActorUser)
		}
		want := filepath.Base(cmd)
		if got := BusActorVerified(); got != want {
			t.Errorf("BusActorVerified() under %s = %q, want %q — ancestry must overrule a stripped environment", cmd, got, want)
		}
	}
}

// Negative control for TestBusActorVerifiedCatchesStrippedIdentity: without
// this, a BusActorVerified that returned an agent name unconditionally would
// pass that test while locking every real person out of their own gates.
func TestBusActorVerifiedKeepsGenuineUser(t *testing.T) {
	pinActor(t, "")
	if got := BusActorVerified(); got != ActorUser {
		t.Errorf("BusActorVerified() = %q, want %q — a shell with no agent above it is a person", got, ActorUser)
	}
}

// ps is resolved through PATH, so an agent does not need to break the probe —
// only to supply one that fails. Reading that as a person would hand back the
// entire bypass, so an unreadable table must be its own answer.
func TestBusActorVerifiedFailsClosedOnUnreadableProcessTable(t *testing.T) {
	pinActor(t, "")
	pinProcessTable(t, "", errors.New("ps unavailable"))
	if got := BusActorVerified(); got != ActorUnknown {
		t.Errorf("BusActorVerified() with an unreadable process table = %q, want %q — a probe that cannot tell must not answer %q", got, ActorUnknown, ActorUser)
	}
}

// A set identity is believed as given: ancestry is consulted only to overrule a
// claim of humanness, never to relabel an agent that already named itself.
func TestBusActorVerifiedTrustsDeclaredIdentity(t *testing.T) {
	pinAgentAncestry(t, "/usr/local/bin/claude")
	t.Setenv("AGENT_ROLE", "review")
	if got := BusActorVerified(); got != "review" {
		t.Errorf("BusActorVerified() = %q, want review", got)
	}
}

func TestCreateGraphRunAnnouncesManualLaunch(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	if run.CreatedBy != ActorUser {
		t.Errorf("run.CreatedBy = %q, want %q", run.CreatedBy, ActorUser)
	}
	if n := editEventCount(t, "graph-run-created", run.ID); n != 1 {
		t.Fatalf("edit received %d graph-run-created events, want 1 — a manual launch must announce itself", n)
	}
}

// Negative control for TestCreateGraphRunAnnouncesManualLaunch: without this a
// helper that announced unconditionally would pass the test above.
func TestCreateGraphRunSilentWhenEditLaunches(t *testing.T) {
	pinActor(t, "edit")
	run := createTestRun(t, linearGraph())

	if run.CreatedBy != "edit" {
		t.Errorf("run.CreatedBy = %q, want edit", run.CreatedBy)
	}
	if n := editEventCount(t, "graph-run-created", run.ID); n != 0 {
		t.Fatalf("edit received %d graph-run-created events for a launch it made itself, want 0", n)
	}
}

func TestApproveGraphGateRecordsAndAnnouncesApprover(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, actorGateGraph())

	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("ApproveGraphGate: %v", err)
	}

	data, err := os.ReadFile(graphApprovalPath(runTestSession, run.ID, "gate", "approved"))
	if err != nil {
		t.Fatalf("read approval marker: %v", err)
	}
	var marker struct {
		ApprovedAt int64  `json:"approved_at"`
		ApprovedBy string `json:"approved_by"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("unmarshal marker: %v", err)
	}
	if marker.ApprovedBy != ActorUser {
		t.Errorf("approved_by = %q, want %q — a release must be attributable", marker.ApprovedBy, ActorUser)
	}
	if marker.ApprovedAt <= 0 {
		t.Error("approved_at missing — the added field must not displace the timestamp")
	}
	if got := gateApprovalTime(runTestSession, run.ID, "gate"); got != marker.ApprovedAt {
		t.Errorf("gateApprovalTime = %d, want %d — approved_by must stay additive", got, marker.ApprovedAt)
	}
	if n := editEventCount(t, "graph-gate-approved", run.ID); n != 1 {
		t.Fatalf("edit received %d graph-gate-approved events, want 1", n)
	}
}

// Negative control for TestApproveGraphGateRecordsAndAnnouncesApprover.
func TestApproveGraphGateSilentWhenEditApproves(t *testing.T) {
	pinActor(t, "edit")
	run := createTestRun(t, actorGateGraph())

	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("ApproveGraphGate: %v", err)
	}
	if n := editEventCount(t, "graph-gate-approved", run.ID); n != 0 {
		t.Fatalf("edit received %d graph-gate-approved events for an approval it made itself, want 0", n)
	}
}
