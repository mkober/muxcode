package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSpawns replaces graphSpawnFn with an in-memory dispatcher that
// records tasks and immediately writes a "completed" spawn entry, so
// executor tests run without tmux.
func fakeSpawns(t *testing.T, session string) *[]string {
	t.Helper()
	var tasks []string
	orig := graphSpawnFn
	n := 0
	graphSpawnFn = func(sess, role, task, owner, runID, nodeID string) (string, error) {
		n++
		id := fmt.Sprintf("spawn-fake%04d", n)
		tasks = append(tasks, role+": "+task)
		entry := SpawnEntry{ID: id, Role: role, SpawnRole: id, Owner: owner,
			Task: task, Status: "completed", StartedAt: time.Now().Unix(),
			RunID: runID, NodeID: nodeID}
		if err := appendSpawnEntry(sess, entry); err != nil {
			t.Fatalf("append spawn entry: %v", err)
		}
		return id, nil
	}
	t.Cleanup(func() { graphSpawnFn = orig })
	return &tasks
}

// appendSpawnEntry writes a spawn entry the way the spawn store expects.
func appendSpawnEntry(session string, e SpawnEntry) error {
	entries, _ := ReadSpawnEntries(session)
	entries = append(entries, e)
	return WriteSpawnEntries(session, entries)
}

// completeSendNode fakes an agent answering a running send node: completes
// its task and optionally writes an authoritative history row carrying the
// verdict.
func completeSendNode(t *testing.T, session, runID, nodeID, rowOutcome string) {
	t.Helper()
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		t.Fatalf("read node %s: %v", nodeID, err)
	}
	if st.State != GraphNodeRunning || st.TaskID == "" {
		t.Fatalf("node %s not running with a task: %+v", nodeID, st)
	}
	CompleteTask(session, st.TaskID, "resp-"+st.TaskID)

	if rowOutcome != "" {
		g, err := ReadGraphRunGraph(session, runID)
		if err != nil {
			t.Fatal(err)
		}
		var role string
		for _, n := range g.Nodes {
			if n.ID == nodeID {
				role = NormalizeBusRole(n.Role)
			}
		}
		row := HookHistoryEntry{TS: time.Now().Unix() + 1, Command: "./fake.sh",
			ExitCode: "0", Outcome: rowOutcome}
		if rowOutcome == OutcomeFailure {
			row.ExitCode = "1"
		}
		if err := WriteHookHistory(HistoryPath(session, role), row, 100); err != nil {
			t.Fatal(err)
		}
	}
}

func step(t *testing.T, session, runID string) {
	t.Helper()
	if err := StepGraphRun(session, runID); err != nil {
		t.Fatalf("StepGraphRun: %v", err)
	}
}

func nodeState(t *testing.T, session, runID, nodeID string) string {
	t.Helper()
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		t.Fatalf("read node %s: %v", nodeID, err)
	}
	return st.State
}

func TestExecLinearRun(t *testing.T) {
	run := createTestRun(t, linearGraph())

	// Tick 1: start node dispatches.
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeRunning {
		t.Fatalf("a state %q, want running", s)
	}
	// The send landed in the target role's inbox.
	msgs, _ := Peek(runTestSession, "build")
	if len(msgs) != 1 || msgs[0].Action != "build" {
		t.Fatalf("build inbox: %+v", msgs)
	}

	// Agent answers with an authoritative success row.
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeDone {
		t.Fatalf("a state %q, want done", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Fatalf("b state %q, want running", s)
	}

	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID)

	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunComplete {
		t.Errorf("run state %q, want complete", got.State)
	}
	// Completion wake: exactly one graph-complete request to edit.
	edit, _ := Peek(runTestSession, "edit")
	var wakes int
	for _, m := range edit {
		if m.Action == "graph-complete" {
			wakes++
		}
	}
	if wakes != 1 {
		t.Errorf("edit received %d graph-complete wakes, want 1", wakes)
	}
}

// conditionOutputGraph is a send → condition(output_contains PR-CONFIRMED)
// fan: success routes to b, failure to fail-note.
func conditionOutputGraph() *Graph {
	return &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "chk", Type: NodeCondition, Conditions: map[string]any{"output_contains": "PR-CONFIRMED"}},
			{ID: "b", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
			{ID: "fail-note", Type: NodeSend, Role: "build", Action: "build", Message: "retry"},
		},
		Edges: []Edge{
			{From: "a", To: "chk"},
			{From: "chk", To: "b"},
			{From: "chk", To: "fail-note", Outcome: OutcomeFailure},
		},
	}
}

// completeSendNodeWithPayload answers a running send node with a real
// response message so the node's harvested Output carries the payload.
func completeSendNodeWithPayload(t *testing.T, session, runID, nodeID, payload string) {
	t.Helper()
	resp := NewMessage("build", "edit", "response", "response", payload, "")
	if err := Send(session, resp); err != nil {
		t.Fatal(err)
	}
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	CompleteTask(session, st.TaskID, resp.ID)
	row := HookHistoryEntry{TS: time.Now().Unix() + 1, Command: "./fake.sh",
		ExitCode: "0", Outcome: OutcomeSuccess}
	if err := WriteHookHistory(HistoryPath(session, "build"), row, 100); err != nil {
		t.Fatal(err)
	}
}

// A condition node must see its predecessor's harvested output — the
// live 2026-08-31 incident had a run sail past a declined PR creation
// because conditions evaluated against an empty context.
func TestExecConditionSeesPredecessorOutput(t *testing.T) {
	run := createTestRun(t, conditionOutputGraph())
	step(t, runTestSession, run.ID)
	completeSendNodeWithPayload(t, runTestSession, run.ID, "a", "PR-CONFIRMED https://github.com/x/y/pull/1")
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Fatalf("b state %q, want running — condition did not see predecessor output", s)
	}
}

// Negative control: without the token the condition fails and routes the
// failure edge — a condition that always passes cannot survive this.
func TestExecConditionFailsWithoutToken(t *testing.T) {
	run := createTestRun(t, conditionOutputGraph())
	step(t, runTestSession, run.ID)
	completeSendNodeWithPayload(t, runTestSession, run.ID, "a", "NO-PR-FOUND for this branch")
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "fail-note"); s != GraphNodeRunning {
		t.Fatalf("fail-note state %q, want running — failure edge not routed", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s == GraphNodeRunning {
		t.Fatal("b dispatched despite missing confirmation token")
	}
}

func TestExecFailureRoutesFailureEdge(t *testing.T) {
	g := linearGraph()
	g.Nodes = append(g.Nodes, Node{ID: "fix", Type: NodeSpawn, Role: "edit", Message: "fix it"})
	g.Edges = append(g.Edges, Edge{From: "a", To: "fix", Outcome: OutcomeFailure})
	run := createTestRun(t, g)
	fakeSpawns(t, runTestSession)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeFailure)
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodePending {
		t.Errorf("b state %q, want pending — success edge must not fire on failure", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "fix"); s != GraphNodeRunning {
		t.Errorf("fix state %q, want running", s)
	}
}

func TestExecFailureWithNoEdgeFailsRun(t *testing.T) {
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeFailure)
	step(t, runTestSession, run.ID)

	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Errorf("run state %q, want failed — failure with no live edge", got.State)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodePending {
		t.Errorf("b state %q, want pending — failed run must not dispatch", s)
	}
}

func TestExecUnknownFallsBackToSuccessEdge(t *testing.T) {
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	// Complete the task with NO authoritative history row → outcome unknown.
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.Outcome != OutcomeUnknown {
		t.Errorf("a outcome %q, want unknown", st.Outcome)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Errorf("b state %q, want running — unknown must fall back to the success edge", s)
	}
}

func TestExecFanOutJoinAll(t *testing.T) {
	g := joinGraph(JoinAll, 0)
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)

	// a dispatches; complete it; fan-out arms both spawns.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if len(*tasks) != 2 {
		t.Fatalf("expected 2 spawned workers, got %d: %v", len(*tasks), *tasks)
	}

	// Fake spawns complete instantly, so the next ticks harvest both,
	// pass the join barrier, and run the join + downstream send.
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodeDone {
		t.Fatalf("join state %q, want done", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Fatalf("c state %q, want running after join", s)
	}
}

// TestExecJoinQuorumBarrier drives a quorum join entirely through the
// executor: two send-node branches complete one at a time, and the join
// must hold at 1/2 and release at 2/2.
func TestExecJoinQuorumBarrier(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "b1", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
			{ID: "b2", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
			{ID: "j", Type: NodeJoin, Join: JoinQuorum, Quorum: 2},
			{ID: "c", Type: NodeSend, Role: "deploy", Action: "deploy", Message: "go"},
		},
		Edges: []Edge{
			{From: "a", To: "b1"},
			{From: "a", To: "b2"},
			{From: "b1", To: "j"},
			{From: "b2", To: "j"},
			{From: "j", To: "c"},
		},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)

	// One branch completes: quorum 1/2 — the barrier must hold.
	completeSendNode(t, runTestSession, run.ID, "b1", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodePending {
		t.Fatalf("join state %q with quorum 1/2, want pending", s)
	}
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunRunning {
		t.Fatalf("run state %q with a branch still running, want running", got.State)
	}

	// Second branch completes: quorum met, join runs, downstream fires.
	completeSendNode(t, runTestSession, run.ID, "b2", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodeDone {
		t.Errorf("join state %q, want done once quorum met", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("c state %q, want running after join", s)
	}
}

// TestExecJoinReleasesOnUnknownOutcomes pins the hookless-provider shape
// the integration test caught: branches finish with outcome "unknown"
// (no authoritative history rows), route via the unknown→success
// fallback, and the join barrier must count those fires and release —
// not re-derive outcomes and deadlock.
func TestExecJoinReleasesOnUnknownOutcomes(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "b1", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
			{ID: "b2", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
			{ID: "j", Type: NodeJoin, Join: JoinAll},
			{ID: "c", Type: NodeSend, Role: "deploy", Action: "deploy", Message: "go"},
		},
		Edges: []Edge{
			{From: "a", To: "b1"},
			{From: "a", To: "b2"},
			{From: "b1", To: "j"},
			{From: "b2", To: "j"},
			{From: "j", To: "c"},
		},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "") // unknown outcome
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b1", "")
	completeSendNode(t, runTestSession, run.ID, "b2", "")
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodeDone {
		t.Errorf("join state %q, want done — unknown-outcome fires must count toward the barrier", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("c state %q, want running after join release", s)
	}
}

func TestExecCappedLoopExhaustion(t *testing.T) {
	// a → b; b failure loops back to a, capped at 2 iterations.
	g := linearGraph()
	g.Edges = append(g.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure, MaxIterations: 2})
	run := createTestRun(t, g)

	for i := 0; i < 2; i++ {
		step(t, runTestSession, run.ID)
		completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
		step(t, runTestSession, run.ID)
		completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
		step(t, runTestSession, run.ID)
		if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeRunning {
			t.Fatalf("iteration %d: a state %q, want running (loop re-armed + dispatched)", i, s)
		}
	}

	// Third failure: loop edge exhausted → failure has no live edge → run fails.
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
	step(t, runTestSession, run.ID)

	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Errorf("run state %q, want failed after loop exhaustion", got.State)
	}
}

func TestExecCancelMidRun(t *testing.T) {
	run := createTestRun(t, linearGraph())
	step(t, runTestSession, run.ID)

	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunCanceled {
		t.Fatalf("run state %q, want canceled", got.State)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeSkipped {
		t.Errorf("b state %q, want skipped", s)
	}

	// Further ticks must not dispatch or settle a canceled run.
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	got, _ = ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunCanceled {
		t.Errorf("run state %q after tick, want canceled", got.State)
	}
}

func TestExecHumanGate(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve ${intent}"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q, want waiting", s)
	}
	// Gate notified edit.
	edit, _ := Peek(runTestSession, "edit")
	var gates int
	for _, m := range edit {
		if m.Action == "graph-approval" && strings.Contains(m.Payload, run.ID) {
			gates++
		}
	}
	if gates != 1 {
		t.Fatalf("edit received %d graph-approval requests, want 1", gates)
	}
	// The gated commit node must not have fired.
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q, want pending while gate blocks", s)
	}

	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeDone {
		t.Errorf("gate state %q, want done after approval", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("c state %q, want running after approval", s)
	}
}

// TestExecHumanGateRetryRequiresFreshApproval pins the stale-marker gate
// A suppressed re-dispatch with NO live task must adopt the QUEUED
// duplicate's message id — the agent answers that id, so a task keyed to
// the unsent fresh id would sit in-flight forever and time the node out
// (PR #38 Copilot finding).
func TestExecSuppressedDispatchAdoptsQueuedMessageID(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"}},
		Edges: []Edge{},
	}
	run := createTestRun(t, g)
	step(t, runTestSession, run.ID) // dispatch: message queued, task created

	msgs, _ := Peek(runTestSession, "build")
	if len(msgs) != 1 {
		t.Fatalf("expected the dispatched message queued, got %d", len(msgs))
	}
	queuedID := msgs[0].ID

	// Complete the first pass's task (no in-flight task remains), then
	// re-arm the node — the loop/retry shape.
	CompleteTask(runTestSession, queuedID, "resp-"+queuedID)
	mustNodeTransition(t, run.ID, "a", GraphNodeDone)
	mustNodeTransition(t, run.ID, "a", GraphNodeReady)

	step(t, runTestSession, run.ID) // re-dispatch: suppressed by the queued duplicate

	st, err := ReadNodeStatus(runTestSession, run.ID, "a")
	if err != nil {
		t.Fatalf("ReadNodeStatus: %v", err)
	}
	if st.TaskID != queuedID {
		t.Errorf("suppressed dispatch must adopt the queued id %s, got %s", queuedID, st.TaskID)
	}
	task, err := ReadTask(runTestSession, queuedID)
	if err != nil || task.Status != TaskInFlight {
		t.Errorf("adopted task must be re-armed in-flight, got %+v err %v", task, err)
	}
}

func mustNodeTransition(t *testing.T, runID, node, state string) {
	t.Helper()
	if err := TransitionGraphNode(runTestSession, runID, node, state, nil); err != nil {
		t.Fatalf("transition %s -> %s: %v", node, state, err)
	}
}

// Gate surfacing belongs to the control pane (MUX-108): dispatching a
// wait_human never opens a popup — the graph modals were removed with
// the pane's arrival, and the pane switches itself to Pending Gates.
func TestExecHumanGateNeverPopsModal(t *testing.T) {
	gated := &Graph{
		Name: "t", Start: "gate",
		Nodes: []Node{
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "b", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
		},
		Edges: []Edge{{From: "gate", To: "b"}},
	}

	orig := tmuxRunner
	var calls [][]string
	tmuxRunner = func(args ...string) error { calls = append(calls, args); return nil }
	t.Cleanup(func() { tmuxRunner = orig })

	run := createTestRun(t, gated)
	step(t, runTestSession, run.ID)
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), "display-popup") {
			t.Errorf("gate dispatch must never open a popup, calls: %v", calls)
		}
	}
}

// bypass (PR #34 Copilot must-fix): after a gate was approved once, a
// graph retry --from that gate must WAIT for a new approval — the old
// approved marker must not auto-release the fresh pass.
func TestExecHumanGateRetryRequiresFreshApproval(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	// First pass: run to completion through an approved gate.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "c", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunComplete {
		t.Fatalf("run state %q, want complete", got.State)
	}

	// Retry from the gate: it must wait for a NEW approval.
	if _, err := RetryGraphRun(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q after retry, want waiting — stale approval must not auto-release", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q after retry, want pending", s)
	}

	// A fresh approval releases it.
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("re-approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeDone {
		t.Errorf("gate state %q after fresh approval, want done", s)
	}
}

// TestExecRetryBelowGateRearmsGate is the MUX-132 Phase 2 acceptance
// test — the Phase 1 characterization test
// (TestExecRetryBelowGateConsumesStaleApproval) with its assertions
// inverted, updated rather than deleted so the hole cannot silently
// return. A retry whose --from target sits below a satisfied wait_human
// gate must re-target to the gate: re-arm it, purge the stale approval,
// leave the gated node un-fired, and ask edit for a SECOND approval —
// never resume on an approval granted for different content (observed
// 2026-08-31: retry --from commit after the tree changed post-approval).
// TestExecHumanGateRetryRequiresFreshApproval covers the retry that
// re-enters the gate itself; this is the sibling path below it.
func TestExecRetryBelowGateRearmsGate(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	// First pass: gate approved, then the gated node fails (the incident shape).
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	// Edit consumes the approval request before the human approves — the
	// live sequence. Left pending, the inbox dedup guard would rightly
	// suppress the identical re-ask (the pending one still asks for it).
	edit, _ := Peek(runTestSession, "edit")
	for _, m := range edit {
		if m.Action == "graph-approval" {
			_, _ = ConsumeByID(runTestSession, "edit", m.ID)
		}
	}
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "c", OutcomeFailure)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed after c fails", got.State)
	}

	// Retry from c — below the gate: the retry must re-target to the gate.
	res, err := RetryGraphRun(runTestSession, run.ID, "c")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(res.Rearmed) != 1 || res.Rearmed[0].Gate != "gate" || res.From != "gate" || res.Requested != "c" {
		t.Fatalf("retry result rearmed=%+v from=%q requested=%q, want re-target to the gate", res.Rearmed, res.From, res.Requested)
	}
	if res.Rearmed[0].ApprovedAt <= 0 {
		t.Errorf("retry result carries no original approval time — the re-target must name it")
	}
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeReady {
		t.Fatalf("gate state %q after retry below it, want ready — the gate re-arms", s)
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "gate", "approved")); !os.IsNotExist(err) {
		t.Fatalf("approved marker still present (err=%v) — the stale approval must be purged at retry time", err)
	}
	got, _ = ReadGraphRun(runTestSession, run.ID)
	if !strings.Contains(got.RetryNote, `"gate"`) {
		t.Errorf("run RetryNote %q does not name the re-armed gate — the decision must be visible in graph status", got.RetryNote)
	}

	// Next tick: the gate dispatches and waits; the gated node must NOT fire.
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q after tick, want waiting", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q after tick, want pending — it must not re-fire on the stale approval", s)
	}
	// The session log records every send: the re-armed gate must have
	// asked edit a SECOND time.
	logged, _ := readMessages(LogPath(runTestSession))
	var approvals int
	for _, m := range logged {
		if m.Action == "graph-approval" && strings.Contains(m.Payload, run.ID) {
			approvals++
		}
	}
	if approvals != 2 {
		t.Fatalf("edit received %d graph-approval requests for this run, want exactly 2 — the retry must ask again", approvals)
	}

	// Only a fresh approval releases the gated node.
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("re-approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Fatalf("c state %q after fresh approval, want running", s)
	}
}

// TestExecRetryPurgeFailureFailsClosed pins the purge's error contract
// (PR #56 review): when the stale approval marker cannot be removed, the
// retry must REFUSE — an error return, marker still on disk, gate not
// re-armed — never proceed while logging "purged". A silent failure
// leaves the marker to satisfy the re-armed gate: the laundered
// approval MUX-132 closed, reintroduced through the filesystem.
func TestExecRetryPurgeFailureFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — directory permissions cannot block os.Remove")
	}
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "c", OutcomeFailure)
	step(t, runTestSession, run.ID)

	// Make the marker un-removable: unlinking needs write on the parent.
	approvals := graphApprovalsDir(runTestSession, run.ID)
	if err := os.Chmod(approvals, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(approvals, 0755) })

	if _, err := RetryGraphRun(runTestSession, run.ID, "c"); err == nil {
		t.Fatal("retry succeeded with an un-removable stale approval — must fail closed")
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "gate", "approved")); err != nil {
		t.Fatalf("approved marker missing after refused retry (err=%v) — refusal must leave state untouched", err)
	}
	if s := nodeState(t, runTestSession, run.ID, "gate"); s == GraphNodeReady {
		t.Fatal("gate re-armed despite the refused purge — the retry must leave the run untouched")
	}

	// Positive control: with the permission restored the same retry
	// purges and re-arms — the refusal above was the chmod, not a
	// broken re-arm path.
	if err := os.Chmod(approvals, 0755); err != nil {
		t.Fatalf("restore chmod: %v", err)
	}
	res, err := RetryGraphRun(runTestSession, run.ID, "c")
	if err != nil {
		t.Fatalf("retry after restore: %v", err)
	}
	if len(res.Rearmed) != 1 || res.Rearmed[0].Gate != "gate" {
		t.Fatalf("rearmed=%+v, want the gate — positive control", res.Rearmed)
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "gate", "approved")); !os.IsNotExist(err) {
		t.Fatalf("approved marker survived the successful retry (err=%v)", err)
	}
}

// TestExecDispatchPurgeFailureFailsClosed pins the dispatch-time purge's
// error contract (MUX-132 post-close finding): when a stale approved
// marker cannot be removed as the gate arms, the node must FAIL — never
// reach waiting, where harvestWaitingNode would release it on the
// surviving marker and relaunder the approval through the dispatch door.
func TestExecDispatchPurgeFailureFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — directory permissions cannot block os.Remove")
	}
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)

	// Plant a stale marker, then make it un-removable before the gate dispatches.
	approvals := graphApprovalsDir(runTestSession, run.ID)
	if err := os.MkdirAll(approvals, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(graphApprovalPath(runTestSession, run.ID, "gate", "approved"), []byte("{}"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := os.Chmod(approvals, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(approvals, 0755) })

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeFailed {
		t.Fatalf("gate state %q after failed purge, want failed — never waiting on a stale approval", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q, want pending — the gated node must not fire", s)
	}
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed — the refused purge must surface loudly", got.State)
	}

	// Positive control: restored permission → retry re-arms, dispatch purges and waits.
	if err := os.Chmod(approvals, 0755); err != nil {
		t.Fatalf("restore chmod: %v", err)
	}
	if _, err := RetryGraphRun(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q after restore, want waiting — positive control", s)
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "gate", "approved")); !os.IsNotExist(err) {
		t.Fatalf("approved marker survived dispatch (err=%v) — the purge must remove it", err)
	}
}

// TestExecRetryBelowParallelGateCutRearmsAll pins the cut form of the
// re-arm (review finding, 2026-08-31): with the target fed by two
// parallel branches each behind its own satisfied gate, NO single gate
// dominates every path — a dominator-only check finds nothing and both
// stale approvals stay usable. The re-arm set must be the nearest-gate
// cut: every satisfied gate whose territory contains the target, all
// re-armed, all markers purged, target left pending.
func TestExecRetryBelowParallelGateCutRearmsAll(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "s",
		Nodes: []Node{
			{ID: "s", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "g1", Type: NodeWaitHuman, Message: "approve branch one commit"},
			{ID: "g2", Type: NodeWaitHuman, Message: "approve branch two commit"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{
			{From: "s", To: "g1"}, {From: "s", To: "g2"},
			{From: "g1", To: "c"}, {From: "g2", To: "c"},
		},
	}
	run := createTestRun(t, g)

	// Drive both branches through their gates, then fail the target.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "s", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if err := ApproveGraphGate(runTestSession, run.ID, "g1"); err != nil {
		t.Fatalf("approve g1: %v", err)
	}
	if err := ApproveGraphGate(runTestSession, run.ID, "g2"); err != nil {
		t.Fatalf("approve g2: %v", err)
	}
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "c", OutcomeFailure)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed after c fails", got.State)
	}

	res, err := RetryGraphRun(runTestSession, run.ID, "c")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	rearmed := map[string]bool{}
	for _, r := range res.Rearmed {
		rearmed[r.Gate] = true
	}
	if len(res.Rearmed) != 2 || !rearmed["g1"] || !rearmed["g2"] {
		t.Fatalf("rearmed=%+v, want both parallel gates — a dominator-only check re-arms neither", res.Rearmed)
	}
	for _, gate := range []string{"g1", "g2"} {
		if s := nodeState(t, runTestSession, run.ID, gate); s != GraphNodeReady {
			t.Errorf("%s state %q after retry, want ready", gate, s)
		}
		if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, gate, "approved")); !os.IsNotExist(err) {
			t.Errorf("%s approved marker still present (err=%v) — its stale approval remains usable", gate, err)
		}
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q after retry, want pending — it must not fire on either stale approval", s)
	}
	got, _ = ReadGraphRun(runTestSession, run.ID)
	if !strings.Contains(got.RetryNote, `"g1"`) || !strings.Contains(got.RetryNote, `"g2"`) {
		t.Errorf("run RetryNote %q does not name both re-armed gates", got.RetryNote)
	}
}

// TestExecRetryBelowNeverApprovedGateUnaffected is the MUX-132 Phase 3
// negative control for the re-arm's precondition: the cut re-arms gates
// whose STALE approval a retry would consume — a gate that never reached
// done holds no approval to go stale, so a retry below it must not
// re-target, must leave the gate untouched, and must not demand an
// approval that was never part of the run. This is the only assertion on
// staleApprovalGates' done/success check: without it a re-arm-
// unconditional mutant passes the rest of the suite.
func TestExecRetryBelowNeverApprovedGateUnaffected(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	// Drive to the gate and leave it waiting — no approval ever granted.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q before cancel, want waiting", s)
	}
	// Cancel: the only way a run stalled at an unanswered gate stops running.
	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	res, err := RetryGraphRun(runTestSession, run.ID, "c")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(res.Rearmed) != 0 || res.From != "c" {
		t.Fatalf("retry result rearmed=%+v from=%q — a never-approved gate must not re-arm (stale approvals, not missing ones)", res.Rearmed, res.From)
	}
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeSkipped {
		t.Fatalf("gate state %q after retry, want skipped (untouched) — the retry must not reset a gate that holds no approval", s)
	}
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.RetryNote != "" {
		t.Errorf("RetryNote %q on a retry with no stale approval, want empty", got.RetryNote)
	}

	// The retry resumes where asked; no second approval request is sent.
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Fatalf("c state %q after tick, want running — the retry must proceed unaffected", s)
	}
	logged, _ := readMessages(LogPath(runTestSession))
	var approvals int
	for _, m := range logged {
		if m.Action == "graph-approval" && strings.Contains(m.Payload, run.ID) {
			approvals++
		}
	}
	if approvals != 1 {
		t.Fatalf("edit received %d graph-approval requests for this run, want exactly 1 (the original dispatch) — the retry must not re-ask a never-approved gate", approvals)
	}
}

func TestExecConditionNode(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "cond",
		Nodes: []Node{
			{ID: "cond", Type: NodeCondition, Conditions: map[string]any{"env_set": "MUXCODE_GRAPH_TEST_ENV"}},
			{ID: "yes", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "no", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{
			{From: "cond", To: "yes", Outcome: OutcomeSuccess},
			{From: "cond", To: "no", Outcome: OutcomeFailure},
		},
	}
	t.Setenv("MUXCODE_GRAPH_TEST_ENV", "1")
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "yes"); s != GraphNodeRunning {
		t.Errorf("yes state %q, want running — condition passed", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "no"); s != GraphNodePending {
		t.Errorf("no state %q, want pending", s)
	}
}

// TestExecConditionFalseBranchIsNotAFailure pins the state/outcome split
// for a condition that takes its false branch (MUX-133 option B). The
// node is a branch selector choosing a branch, so its terminal STATE is
// done; the failure OUTCOME is retained because it is the routing key
// edgeOutcome matches, and every capped loop's terminating edge depends
// on it. Before option B this asserted GraphNodeFailed — the diff of
// this assertion is the model change.
func TestExecConditionFalseBranchIsNotAFailure(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "cond",
		Nodes: []Node{
			{ID: "cond", Type: NodeCondition, Conditions: map[string]any{"env_set": "MUXCODE_GRAPH_TEST_UNSET_ENV"}},
			{ID: "yes", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "no", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{
			{From: "cond", To: "yes", Outcome: OutcomeSuccess},
			{From: "cond", To: "no", Outcome: OutcomeFailure},
		},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "cond")
	if err != nil {
		t.Fatalf("read cond: %v", err)
	}
	if st.State != GraphNodeDone {
		t.Errorf("cond state %q, want %q — a false branch is control flow, not a broken node",
			st.State, GraphNodeDone)
	}
	if st.Outcome != OutcomeFailure {
		t.Fatalf("cond outcome %q, want %q — the failure outcome is the routing key the false edge matches; changing it breaks every capped loop",
			st.Outcome, OutcomeFailure)
	}

	// The routing invariant: the false edge must still have fired.
	if s := nodeState(t, runTestSession, run.ID, "no"); s != GraphNodeRunning {
		t.Errorf("no state %q, want running — the false edge must still route after the split", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "yes"); s != GraphNodePending {
		t.Errorf("yes state %q, want pending", s)
	}

	// The run must not be marked failed by a routine branch.
	got, err := ReadGraphRun(runTestSession, run.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if got.State == GraphRunFailed {
		t.Errorf("run state %q — a condition taking its false branch must never fail the run", got.State)
	}
}

// TestExecConditionUnevaluatableIsAFailure is the negative control for
// the split: a predicate that cannot be evaluated at all is a genuine
// error and must still persist GraphNodeFailed, so option B does not
// make every condition look done.
//
// Graph.Validate rejects an unknown condition type, so this state is
// unreachable through graph run|validate — it is reachable only by
// replaying a definition frozen before the rule existed, which is what
// this test constructs by rewriting the run's frozen graph.json. That
// is precisely why the executor branch is worth having: the run store
// replays frozen definitions without re-validating them.
func TestExecConditionUnevaluatableIsAFailure(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "cond",
		Nodes: []Node{
			{ID: "cond", Type: NodeCondition, Conditions: map[string]any{"env_set": "MUXCODE_GRAPH_TEST_UNSET_ENV"}},
			{ID: "no", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{
			{From: "cond", To: "no", Outcome: OutcomeFailure},
		},
	}
	run := createTestRun(t, g)

	// Freeze a definition the validator would now reject, as an older
	// binary could have written.
	frozen := *g
	frozen.Nodes = append([]Node(nil), g.Nodes...)
	frozen.Nodes[0].Conditions = map[string]any{"bogus_condition_type": "x"}
	blob, err := json.MarshalIndent(&frozen, "", "  ")
	if err != nil {
		t.Fatalf("marshal frozen graph: %v", err)
	}
	if err := os.WriteFile(graphDefPath(runTestSession, run.ID), blob, 0o644); err != nil {
		t.Fatalf("rewrite frozen graph: %v", err)
	}

	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "cond")
	if err != nil {
		t.Fatalf("read cond: %v", err)
	}
	if st.State != GraphNodeFailed {
		t.Errorf("cond state %q, want %q — an unevaluatable predicate is a real error, not a branch",
			st.State, GraphNodeFailed)
	}
	if st.Output == "" {
		t.Error("cond output empty — a genuine evaluation error must say what went wrong")
	}
	if ConditionTookBranch(NodeCondition, st.State, st.Outcome) {
		t.Error("ConditionTookBranch true for an unevaluatable predicate — it must render as a failure, not a branch")
	}
}

func TestRetryGraphRunFromNode(t *testing.T) {
	run := createTestRun(t, linearGraph())

	// Drive the run to completion.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunComplete {
		t.Fatalf("run state %q, want complete", got.State)
	}

	res, err := RetryGraphRun(runTestSession, run.ID, "b")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(res.Rearmed) != 0 || res.From != "b" {
		t.Fatalf("retry result rearmed=%+v from=%q — an ungated retry must not re-target", res.Rearmed, res.From)
	}
	// Upstream a keeps its result; b is re-armed; run is running again.
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeDone {
		t.Errorf("a state %q, want done (upstream preserved)", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeReady {
		t.Errorf("b state %q, want ready", s)
	}

	// The next tick dispatches ONLY b — a must not re-run.
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeDone {
		t.Errorf("a state %q after tick, want done", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Errorf("b state %q after tick, want running", s)
	}
}

func TestRetryGraphRunRefusesRunningRun(t *testing.T) {
	run := createTestRun(t, linearGraph())
	if _, err := RetryGraphRun(runTestSession, run.ID, "b"); err == nil {
		t.Error("retry must refuse a running run")
	}
}

func TestRetryGraphRunResetsLoopBudget(t *testing.T) {
	g := linearGraph()
	g.Edges = append(g.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure, MaxIterations: 1})
	run := createTestRun(t, g)

	// Exhaust the loop: a ok, b fails, loop fires once, a ok, b fails again → run failed.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed", got.State)
	}

	// Retry from a: the loop edge budget resets with the subtree.
	if _, err := RetryGraphRun(runTestSession, run.ID, "a"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	fresh, _ := ReadGraphRun(runTestSession, run.ID)
	if len(fresh.EdgeFires) != 0 {
		t.Errorf("edge fires not reset: %v", fresh.EdgeFires)
	}
}

func TestExecWaitEventRelease(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "ev", Type: NodeWaitEvent, Event: "deploy-finished"},
			{ID: "c", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{{From: "a", To: "ev"}, {From: "ev", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "ev"); s != GraphNodeWaiting {
		t.Fatalf("ev state %q, want waiting", s)
	}

	// A bus message with the event's action releases it.
	m := NewMessage("deploy", "edit", "event", "deploy-finished", "done", "")
	if err := SendNoCC(runTestSession, m); err != nil {
		t.Fatalf("send event: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "ev"); s != GraphNodeDone {
		t.Errorf("ev state %q, want done after event", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("c state %q, want running after event release", s)
	}
}

func TestExecMapNodeFansOutPerItem(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "m",
		Nodes: []Node{
			{ID: "m", Type: NodeMap, Role: "edit", Message: "process ${item}", Items: "one, two, three"},
		},
	}
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)

	step(t, runTestSession, run.ID)
	if len(*tasks) != 3 {
		t.Fatalf("expected 3 workers, got %d: %v", len(*tasks), *tasks)
	}
	for i, want := range []string{"process one", "process two", "process three"} {
		if !strings.Contains((*tasks)[i], want) {
			t.Errorf("worker %d task %q missing %q", i, (*tasks)[i], want)
		}
	}

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "m"); s != GraphNodeDone {
		t.Errorf("map state %q, want done once all workers completed", s)
	}
}

// specGuardGraph returns a one-node graph whose send carries the
// spec-complete guard (MUX-114).
func specGuardGraph() *Graph {
	return &Graph{
		Name:  "g",
		Start: "close",
		Nodes: []Node{{ID: "close", Type: NodeSend, Role: "plan", Action: "update-docs",
			Message: "close out the spec", Guard: GuardSpecComplete}},
	}
}

// writeSpecFixture writes a spec file inside a scratch repo dir, pins the
// session repo dir to it, and points the active-spec marker at the spec's
// absolute path. The pointer must live inside the pinned repo: every
// pointer, absolute included, now resolves through the containment
// boundary (review must-fix 2026-09-01).
func writeSpecFixture(t *testing.T, content string) string {
	t.Helper()
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	path := filepath.Join(repo, "spec.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecSpecGuardDeclinesOpenSpec(t *testing.T) {
	run := createTestRun(t, specGuardGraph())
	writeSpecFixture(t, "# S\n- [x] done\n- [ ] open item one\n- [ ] open item two\n")

	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "close")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || st.Outcome != OutcomeFailure {
		t.Fatalf("close state %q outcome %q, want failed/failure", st.State, st.Outcome)
	}
	if !strings.Contains(st.Output, "2 open items") || !strings.Contains(st.Output, "open item one") {
		t.Errorf("decline must name the count and the open items, got %q", st.Output)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 0 {
		t.Errorf("declined dispatch must never send — plan inbox: %+v", msgs)
	}

	// Failure routing runs on the next tick; with no failure edge it fails the run.
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Errorf("run state %q, want failed — the chain must stop before any downstream mutation", got.State)
	}
}

// TestExecSpecGuardAllowsCompleteSpec is the negative control: a guard
// that simply never closes anything cannot pass it.
func TestExecSpecGuardAllowsCompleteSpec(t *testing.T) {
	run := createTestRun(t, specGuardGraph())
	writeSpecFixture(t, "# S\n- [x] done\n- [X] loudly done\n")

	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "close"); s != GraphNodeRunning {
		t.Fatalf("close state %q, want running — a fully-checked spec must dispatch", s)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 1 {
		t.Errorf("plan inbox has %d messages, want the close-out send", len(msgs))
	}
}

func TestExecSpecGuardNoActiveSpecDispatches(t *testing.T) {
	run := createTestRun(t, specGuardGraph())

	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "close"); s != GraphNodeRunning {
		t.Fatalf("close state %q, want running — no active spec is the node's own nothing-to-do path", s)
	}
}

func TestExecSpecGuardUnreadableSpecDeclines(t *testing.T) {
	run := createTestRun(t, specGuardGraph())
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	if err := WriteActiveSpec(runTestSession, filepath.Join(repo, "absent.md")); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "close")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "cannot read active spec") {
		t.Errorf("unreadable spec must decline loudly, got state %q output %q", st.State, st.Output)
	}
}

func TestExecSpecGuardResolvesRelativeSpecPath(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "spec.md"), []byte("- [ ] open\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	run := createTestRun(t, specGuardGraph())
	if err := WriteActiveSpec(runTestSession, "spec.md"); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "close")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "1 open items") {
		t.Errorf("relative spec path must resolve against the session repo dir, got state %q output %q", st.State, st.Output)
	}
}

// TestExecSpecGuardRefusesExternalPointer pins the pointer boundary at
// the guard (review must-fix 2026-09-01): an active-spec pointer
// resolving outside the repo fails the node loudly — it must NOT read as
// "no active spec", which would pass the guard through and close out
// against nothing (the inert-guard hazard), and the daemon must never
// read the external file.
func TestExecSpecGuardRefusesExternalPointer(t *testing.T) {
	run := createTestRun(t, specGuardGraph())
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	ext := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(ext, []byte("- [x] not a spec\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, ext); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "close")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "outside the repo") {
		t.Fatalf("external pointer must fail the node, got state %q output %q", st.State, st.Output)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 0 {
		t.Errorf("refused dispatch must never send — plan inbox: %+v", msgs)
	}
}

// TestActiveSpecFileBoundary pins the four pointer states as distinct.
// The one that must never collapse: an external pointer is refused, not
// unset — and with the repo dir unresolvable, containment is unprovable
// for EVERY pointer shape, absolute included, so all postpone as
// transient (the old code followed absolute pointers unconditionally).
func TestActiveSpecFileBoundary(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)

	if _, ok, transient, refused := activeSpecFile(runTestSession); ok || transient || refused {
		t.Errorf("unset: ok=%v transient=%v refused=%v, want all false", ok, transient, refused)
	}

	spec := filepath.Join(repo, "spec.md")
	if err := os.WriteFile(spec, []byte("# s\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, spec); err != nil {
		t.Fatal(err)
	}
	if path, ok, _, _ := activeSpecFile(runTestSession); !ok || path == "" {
		t.Errorf("in-repo absolute pointer: ok=%v path=%q, want resolved", ok, path)
	}

	ext := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(ext, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, ext); err != nil {
		t.Fatal(err)
	}
	if p, ok, transient, refused := activeSpecFile(runTestSession); !refused || ok || transient || p != "" {
		t.Errorf("external pointer: ok=%v transient=%v refused=%v path=%q, want refused only", ok, transient, refused, p)
	}

	t.Setenv("MUXCODE_SESSION_REPO_DIR", "")
	if _, ok, transient, refused := activeSpecFile(runTestSession); !transient || ok || refused {
		t.Errorf("no repo dir with absolute pointer: ok=%v transient=%v refused=%v, want transient only", ok, transient, refused)
	}
}

// phaseGuardGraph returns a one-node graph whose send carries the
// phase-complete guard.
func phaseGuardGraph() *Graph {
	return &Graph{
		Name:  "g",
		Start: "ship",
		Nodes: []Node{{ID: "ship", Type: NodeSend, Role: "plan", Action: "update-docs",
			Message: "commit the phase", Guard: GuardPhaseComplete}},
	}
}

// TestExecPhaseGuard pins the phase-scoped guard: the intent's phase with
// open items declines; the same phase fully checked dispatches even while
// OTHER phases are open (the discriminator against spec-complete); no
// phase in the intent passes through.
func TestExecPhaseGuard(t *testing.T) {
	spec := "# S\n### Phase 1: Now\n- [ ] open one\n### Phase 2: Later\n- [ ] later work\n"

	run := createTestRun(t, phaseGuardGraph())
	writeSpecFixture(t, spec)
	mutateRunIntent(t, run.ID, "Ship — Phase 1: Now")
	step(t, runTestSession, run.ID)
	st, err := ReadNodeStatus(runTestSession, run.ID, "ship")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "Phase 1 has 1 open items") {
		t.Fatalf("open phase must decline, got state %q output %q", st.State, st.Output)
	}

	run2 := createTestRun(t, phaseGuardGraph())
	writeSpecFixture(t, "# S\n### Phase 1: Now\n- [x] done\n### Phase 2: Later\n- [ ] later work\n")
	mutateRunIntent(t, run2.ID, "Ship — Phase 1: Now")
	step(t, runTestSession, run2.ID)
	if s := nodeState(t, runTestSession, run2.ID, "ship"); s != GraphNodeRunning {
		t.Fatalf("checked phase must dispatch despite open later phases, got %q", s)
	}

	run3 := createTestRun(t, phaseGuardGraph())
	writeSpecFixture(t, spec)
	mutateRunIntent(t, run3.ID, "no phase named")
	step(t, runTestSession, run3.ID)
	if s := nodeState(t, runTestSession, run3.ID, "ship"); s != GraphNodeRunning {
		t.Fatalf("intent without a phase must pass through, got %q", s)
	}
}

// TestExecCurrentPhaseInterpolation pins ${current_phase} resolution at
// dispatch: the message carries the spec's lowest OPEN phase, not the
// frozen intent's — the mechanism that stops a loop re-implementing a
// completed phase (MUX-121).
func TestExecCurrentPhaseInterpolation(t *testing.T) {
	g := &Graph{
		Name:  "g",
		Start: "impl",
		Nodes: []Node{{ID: "impl", Type: NodeSend, Role: "build", Action: "build",
			Message: "Implement ${current_phase}"}},
	}
	run := createTestRun(t, g)
	writeSpecFixture(t, "# S\n### Phase 1: Done\n- [x] a\n### Phase 2: Attribution\n- [ ] b\n")
	mutateRunIntent(t, run.ID, "MUX-115 — Phase 1: Turn trace")

	step(t, runTestSession, run.ID)

	msgs, _ := Peek(runTestSession, "build")
	if len(msgs) != 1 {
		t.Fatalf("build inbox: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Payload, "Phase 2: Attribution") ||
		strings.Contains(msgs[0].Payload, "Phase 1") {
		t.Errorf("dispatch must carry the derived open phase, not the frozen intent's: %q", msgs[0].Payload)
	}
}

// TestSpecPhasesRemainingCondition pins the loop-termination condition
// (MUX-121): true passes while an open phase remains, false passes once
// none do, and a missing spec counts as nothing remaining so a loop
// terminates rather than spins.
func TestSpecPhasesRemainingCondition(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	spec := filepath.Join(repo, "spec.md")
	if err := os.WriteFile(spec, []byte("### Phase 1: A\n- [ ] open\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, spec); err != nil {
		t.Fatal(err)
	}
	ctx := &ChainContext{Session: runTestSession}

	if ok, _ := EvaluateConditions(map[string]any{"spec_phases_remaining": true}, ctx); !ok {
		t.Error("open phase: remaining=true must pass")
	}
	if ok, _ := EvaluateConditions(map[string]any{"spec_phases_remaining": false}, ctx); ok {
		t.Error("open phase: remaining=false must fail (negative control)")
	}

	if err := os.WriteFile(spec, []byte("### Phase 1: A\n- [x] done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := EvaluateConditions(map[string]any{"spec_phases_remaining": false}, ctx); !ok {
		t.Error("all complete: remaining=false must pass — the termination edge")
	}

	if err := ClearActiveSpec(runTestSession); err != nil {
		t.Fatal(err)
	}
	if ok, _ := EvaluateConditions(map[string]any{"spec_phases_remaining": false}, ctx); !ok {
		t.Error("no active spec must count as nothing remaining — a loop must terminate, not spin")
	}
}

// TestExecPhaseProgressGuard pins the per-phase commit guard (MUX-121):
// each commit must ship one newly-completed phase, counted against the
// guard node's own prior successful fires — fix-loop and retry fires
// must never distort the math (review catch 2026-08-28).
func TestExecPhaseProgressGuard(t *testing.T) {
	guardGraph := func() *Graph {
		return &Graph{Name: "g", Start: "commit",
			Nodes: []Node{
				{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs",
					Message: "commit the phase", Guard: GuardPhaseProgress},
				{ID: "next", Type: NodeCondition, Conditions: map[string]any{"spec_phases_remaining": true}},
			},
			Edges: []Edge{{From: "commit", To: "next"}}}
	}
	seedFires := func(runID string, fires map[string]int) {
		run, err := ReadGraphRun(runTestSession, runID)
		if err != nil {
			t.Fatal(err)
		}
		run.EdgeFires = fires
		if err := WriteGraphRun(runTestSession, run); err != nil {
			t.Fatal(err)
		}
	}

	// First commit, one phase complete — ships, and heavy fix-loop fires
	// must not inflate the requirement (the review-found bug).
	run := createTestRun(t, guardGraph())
	writeSpecFixture(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n")
	seedFires(run.ID, map[string]int{"fix->build:success": 3})
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "commit"); s != GraphNodeRunning {
		t.Fatalf("first commit with its phase complete must ship despite fix-loop fires, got %q", s)
	}

	// Second commit (one prior success fire) with no second phase closed:
	// decline toward the stuck gate, naming the counts.
	run2 := createTestRun(t, guardGraph())
	writeSpecFixture(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n")
	seedFires(run2.ID, map[string]int{"commit->next:success": 1})
	step(t, runTestSession, run2.ID)
	st, err := ReadNodeStatus(runTestSession, run2.ID, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "1 commits shipped but only 1 phases complete") {
		t.Errorf("no-progress commit must decline with counts, got %q %q", st.State, st.Output)
	}

	// No active spec: decline, never commit blind.
	run3 := createTestRun(t, guardGraph())
	step(t, runTestSession, run3.ID)
	st3, err := ReadNodeStatus(runTestSession, run3.ID, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if st3.State != GraphNodeFailed || !strings.Contains(st3.Output, "no active spec") {
		t.Errorf("no-spec commit must decline, got %q %q", st3.State, st3.Output)
	}
}

// TestExecHumanGateLoopReArmRequiresFreshApproval covers the loop path
// the multi-phase template lives on (plan coverage gap 2026-08-28): a
// gate approved on iteration 1 and re-armed by a loop edge must WAIT
// again — without the dispatch purge, approving Phase 1's commit would
// silently release every later phase's commit.
func TestExecHumanGateLoopReArmRequiresFreshApproval(t *testing.T) {
	g := &Graph{Name: "g", Start: "gate",
		Nodes: []Node{
			{ID: "gate", Type: NodeWaitHuman, Message: "approve the commit"},
			{ID: "work", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
		},
		Edges: []Edge{
			{From: "gate", To: "work"},
			{From: "work", To: "gate", MaxIterations: 2},
		}}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q, want waiting", s)
	}
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run.ID) // approval releases work
	completeSendNode(t, runTestSession, run.ID, "work", OutcomeSuccess)
	step(t, runTestSession, run.ID) // loop edge re-arms the gate
	step(t, runTestSession, run.ID) // gate re-dispatches

	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("re-armed gate state %q, want waiting — the loop pass must demand a fresh approval", s)
	}
	msgs, _ := Peek(runTestSession, "build")
	if len(msgs) != 1 {
		t.Errorf("work dispatched %d times, want 1 — the stale approval must not release iteration 2", len(msgs))
	}
}

// TestExecLoopExhaustionFailsRunLoudly pins the MUX-121 plan finding: a
// SUCCESS outcome whose only edge is an exhausted loop edge must fail the
// run — a cap shortfall must never settle as a run that looks complete.
func TestExecLoopExhaustionFailsRunLoudly(t *testing.T) {
	g := linearGraph()
	g.Edges = []Edge{{From: "a", To: "b"}, {From: "b", To: "a", MaxIterations: 1}}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID) // b succeeds, loop edge fires (1/1), a re-arms
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID) // b succeeds again, loop edge exhausted

	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Errorf("run state %q, want failed — exhaustion suppressed b's only edge", got.State)
	}
}

// TestExecRedriveStalledDispatch pins executor-owned stall resolution
// (MUX-123): an un-receipted in-flight dispatch past the stall threshold
// is redriven with persisted bookkeeping, rate-limited between attempts,
// and fails loudly as undeliverable after the cap; a receipted task is
// never touched (negative control — receipt means genuinely working).
func TestExecRedriveStalledDispatch(t *testing.T) {
	oneNode := func() *Graph {
		return &Graph{Name: "g", Start: "a",
			Nodes: []Node{{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"}}}
	}
	backdateTask := func(id string, secs int64) {
		task, err := ReadTask(runTestSession, id)
		if err != nil {
			t.Fatal(err)
		}
		task.SentAt -= secs
		if err := writeTask(runTestSession, task); err != nil {
			t.Fatal(err)
		}
	}
	nodeStatus := func(runID string) *GraphNodeStatus {
		st, err := ReadNodeStatus(runTestSession, runID, "a")
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	// Observe DELIVERIES through the seam, not just counters (review
	// should-fix: a counter test proves bookkeeping, not behavior).
	var driven []string
	origRedrive := graphRedriveFn
	graphRedriveFn = func(session, role string, task Task) int {
		driven = append(driven, role+":"+task.ID)
		return 1
	}
	t.Cleanup(func() { graphRedriveFn = origRedrive })

	run := createTestRun(t, oneNode())
	step(t, runTestSession, run.ID)
	st := nodeStatus(run.ID)
	if st.State != GraphNodeRunning || st.Redrives != 0 {
		t.Fatalf("fresh dispatch: state %q redrives %d", st.State, st.Redrives)
	}

	// Fresh task: no redrive even on another tick.
	step(t, runTestSession, run.ID)
	if st = nodeStatus(run.ID); st.Redrives != 0 || len(driven) != 0 {
		t.Fatalf("un-stalled task must not redrive, got %d (%v)", st.Redrives, driven)
	}

	// Stalled + un-receipted: one real delivery attempt, bookkeeping persisted.
	backdateTask(st.TaskID, 120)
	step(t, runTestSession, run.ID)
	if st = nodeStatus(run.ID); st.Redrives != 1 || st.LastRedrive == 0 || len(driven) != 1 {
		t.Fatalf("stalled dispatch must redrive once, got %d (%v)", st.Redrives, driven)
	}

	// Rate limit: an immediate tick delivers nothing further.
	step(t, runTestSession, run.ID)
	if st = nodeStatus(run.ID); st.Redrives != 1 || len(driven) != 1 {
		t.Fatalf("redrive must rate-limit, got %d (%v)", st.Redrives, driven)
	}

	// Walk to the cap, then the node fails as undeliverable.
	for i := 0; i < 3; i++ {
		if err := MutateNodeStatus(runTestSession, run.ID, "a", func(s *GraphNodeStatus) {
			s.LastRedrive -= 61
		}); err != nil {
			t.Fatal(err)
		}
		step(t, runTestSession, run.ID)
	}
	st = nodeStatus(run.ID)
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "undeliverable") {
		t.Fatalf("capped redrives must fail undeliverable, got %q %q (redrives %d)", st.State, st.Output, st.Redrives)
	}

	// Negative control: a receipted task is working, never redriven.
	run2 := createTestRun(t, oneNode())
	step(t, runTestSession, run2.ID)
	st2 := nodeStatus(run2.ID)
	backdateTask(st2.TaskID, 120)
	if err := os.MkdirAll(filepath.Dir(DeliveryPath(runTestSession, st2.TaskID)), 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(DeliveryStatus{ID: st2.TaskID, AckedAt: time.Now().Unix()})
	if err := os.WriteFile(DeliveryPath(runTestSession, st2.TaskID), data, 0644); err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run2.ID)
	if st2 = nodeStatus(run2.ID); st2.Redrives != 0 {
		t.Fatalf("receipted task must never redrive, got %d", st2.Redrives)
	}
}

// TestExecRedriveStalledSpawns pins the spawn/map side of stall
// resolution: a worker whose seeded task sits unconsumed past the stall
// threshold is re-woken with persisted bookkeeping, and cap exhaustion
// FAILS the node — default spawn nodes carry no timeout, so a
// never-waking worker would otherwise leave the node running forever
// (review must-fix 2026-08-28). A drained inbox is the negative control.
func TestExecRedriveStalledSpawns(t *testing.T) {
	var woken []string
	origWake := graphSpawnWakeFn
	graphSpawnWakeFn = func(_, spawnRole string) { woken = append(woken, spawnRole) }
	t.Cleanup(func() { graphSpawnWakeFn = origWake })

	g := &Graph{Name: "g", Start: "w",
		Nodes: []Node{{ID: "w", Type: NodeSpawn, Role: "build", Message: "go"}}}
	run := createTestRun(t, g)
	n := &g.Nodes[0]
	now := time.Now().Unix()

	startWorker := func(runID, spawnRole string, seedInbox bool) {
		if err := TransitionGraphNode(runTestSession, runID, "w", GraphNodeRunning, func(s *GraphNodeStatus) {
			s.TaskID = spawnRole
			s.StartedAt = now - 120
		}); err != nil {
			t.Fatal(err)
		}
		if seedInbox {
			if err := SendNoCC(runTestSession, NewMessage("daemon", spawnRole, "request", "task", "go", "")); err != nil {
				t.Fatal(err)
			}
		}
	}
	status := func(runID string) *GraphNodeStatus {
		st, err := ReadNodeStatus(runTestSession, runID, "w")
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	startWorker(run.ID, "spawn-w1", true)

	// Stalled + unconsumed: one wake, bookkeeping persisted.
	redriveStalledSpawns(runTestSession, run, n, status(run.ID), now)
	st := status(run.ID)
	if st.Redrives != 1 || st.LastRedrive == 0 || len(woken) != 1 || woken[0] != "spawn-w1" {
		t.Fatalf("stalled worker must re-wake once, got %d (%v)", st.Redrives, woken)
	}

	// Rate limit: an immediate retry does nothing.
	redriveStalledSpawns(runTestSession, run, n, st, now)
	if st = status(run.ID); st.Redrives != 1 || len(woken) != 1 {
		t.Fatalf("spawn redrive must rate-limit, got %d (%v)", st.Redrives, woken)
	}

	// Walk to the cap; the still-stalled worker then fails the node.
	for i := 0; i < 3; i++ {
		if err := MutateNodeStatus(runTestSession, run.ID, "w", func(s *GraphNodeStatus) {
			s.LastRedrive -= 61
		}); err != nil {
			t.Fatal(err)
		}
		redriveStalledSpawns(runTestSession, run, n, status(run.ID), now)
	}
	st = status(run.ID)
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "undeliverable") {
		t.Fatalf("capped spawn redrives must fail undeliverable, got %q %q (redrives %d)", st.State, st.Output, st.Redrives)
	}

	// Negative control: a drained inbox means the worker consumed its
	// task and is working — never woken, never failed.
	woken = nil
	run2 := createTestRun(t, g)
	startWorker(run2.ID, "spawn-w2", false)
	redriveStalledSpawns(runTestSession, run2, n, status(run2.ID), now)
	st = status(run2.ID)
	if st.State != GraphNodeRunning || st.Redrives != 0 || len(woken) != 0 {
		t.Fatalf("working spawn must be untouched, got %q redrives %d (%v)", st.State, st.Redrives, woken)
	}
}

// mutateRunIntent rewrites a run's intent in the store.
func mutateRunIntent(t *testing.T, runID, intent string) {
	t.Helper()
	run, err := ReadGraphRun(runTestSession, runID)
	if err != nil {
		t.Fatal(err)
	}
	run.Intent = intent
	if err := WriteGraphRun(runTestSession, run); err != nil {
		t.Fatal(err)
	}
}

func TestExecSpecGuardPostponesWhenRepoDirUnknown(t *testing.T) {
	t.Setenv("MUXCODE_SESSION_REPO_DIR", "")
	run := createTestRun(t, specGuardGraph())
	if err := WriteActiveSpec(runTestSession, "docs/requirements/drafts/spec.md"); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "close"); s != GraphNodeReady {
		t.Fatalf("close state %q, want ready — an unresolvable repo dir is transient, not a decline", s)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 0 {
		t.Errorf("postponed dispatch must not send — plan inbox: %+v", msgs)
	}
}

// --- MUX-131 Defect B: spawn worker reuse ---

// liveSpawnFake mirrors the real graphSpawnFn closely enough for the
// reuse tests: a fresh worker gets a RUNNING entry, a real seeded inbox
// message, and the run+node stamp, so FindLiveSpawn, ReseedSpawn, and
// spawnGroupOutcome run their live paths without tmux. Windows listed in
// deadWindows read as gone; kill attempts are recorded.
type liveSpawnFake struct {
	fresh       int
	killed      []string
	deadWindows map[string]bool
}

func fakeLiveSpawns(t *testing.T) *liveSpawnFake {
	t.Helper()
	if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
		t.Fatalf("delivery dir: %v", err)
	}
	f := &liveSpawnFake{deadWindows: map[string]bool{}}
	origSpawn, origExists, origKill, origWake := graphSpawnFn, spawnWindowExistsFn, spawnKillWindowFn, graphSpawnWakeFn
	t.Cleanup(func() {
		graphSpawnFn, spawnWindowExistsFn, spawnKillWindowFn, graphSpawnWakeFn = origSpawn, origExists, origKill, origWake
	})
	graphSpawnWakeFn = func(string, string) {}
	spawnWindowExistsFn = func(_, w string) bool { return !f.deadWindows[w] }
	spawnKillWindowFn = func(_, w string) error { f.killed = append(f.killed, w); return nil }
	graphSpawnFn = func(sess, role, task, owner, runID, nodeID string) (string, error) {
		f.fresh++
		id := fmt.Sprintf("spawn-live%04d", f.fresh)
		msg := NewMessage(owner, id, "request", "spawn-task", task, "")
		if err := Send(sess, msg); err != nil {
			t.Fatalf("seed send: %v", err)
		}
		entry := SpawnEntry{ID: id, Role: role, SpawnRole: id, Owner: owner, Task: task,
			Status: "running", Window: id, StartedAt: time.Now().Unix(),
			SeedMsgID: msg.ID, RunID: runID, NodeID: nodeID}
		if err := appendSpawnEntry(sess, entry); err != nil {
			t.Fatalf("append spawn entry: %v", err)
		}
		return id, nil
	}
	return f
}

// answerSpawn fakes the worker replying to its CURRENT seed — the same
// MarkResponded a real reply drives, which spawnHasResponded reads.
func answerSpawn(t *testing.T, session, spawnRole string) {
	t.Helper()
	entries, _ := ReadSpawnEntries(session)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].SpawnRole != spawnRole {
			continue
		}
		if entries[i].SeedMsgID == "" {
			t.Fatalf("worker %s has no seed", spawnRole)
		}
		MarkResponded(session, entries[i].SeedMsgID, "resp-"+entries[i].SeedMsgID)
		return
	}
	t.Fatalf("no spawn entry for %s", spawnRole)
}

func spawnCountForRun(t *testing.T, session, runID string) int {
	t.Helper()
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		t.Fatalf("read spawn entries: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.RunID == runID {
			n++
		}
	}
	return n
}

// TestReseedSpawnIdentityFirstFailClosed pins the reseed ordering (review
// must-fix, 2026-09-01): SeedMsgID persists BEFORE the seed is sent, so a
// failure between the two leaves an id whose message does not exist — a
// state that can never read as responded — rather than a sent seed whose
// entry still carries the previous iteration's responded id (a false
// completion). The discriminator: a failing entry update must mean NO
// seed reaches the worker's inbox; send-first ordering delivers one.
func TestReseedSpawnIdentityFirstFailClosed(t *testing.T) {
	useTempBusDir(t)

	bogus := SpawnEntry{ID: "spawn-doesnotexist", SpawnRole: "spawn-doesnotexist", Owner: "daemon"}
	if _, err := ReseedSpawn(runTestSession, bogus, "phase 2"); err == nil {
		t.Fatal("reseed of a missing entry must error")
	}
	msgs, _ := Peek(runTestSession, "spawn-doesnotexist")
	for _, m := range msgs {
		if m.Action == "spawn-task" {
			t.Fatalf("seed was sent despite the entry update failing — identity must persist first, got %q", m.Payload)
		}
	}
}

// TestAcquireSpawnWorkerReusesLiveWorker pins the reuse core: a second
// acquire for the same run+node reseeds the live worker instead of
// starting a fresh one, and the entry's SeedMsgID moves to the new seed.
func TestAcquireSpawnWorkerReusesLiveWorker(t *testing.T) {
	useTempBusDir(t)
	f := fakeLiveSpawns(t)

	id1, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if f.fresh != 1 {
		t.Fatalf("first acquire must start fresh, got %d", f.fresh)
	}
	answerSpawn(t, runTestSession, id1)
	seed1, _ := GetSpawnEntry(runTestSession, id1)

	id2, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 2")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("re-entry must reuse the live worker: got %s, want %s", id2, id1)
	}
	if f.fresh != 1 {
		t.Fatalf("re-entry must not start a fresh worker, got %d starts", f.fresh)
	}
	e, err := GetSpawnEntry(runTestSession, id1)
	if err != nil {
		t.Fatal(err)
	}
	if e.SeedMsgID == "" || e.SeedMsgID == seed1.SeedMsgID {
		t.Fatalf("reseed must move SeedMsgID to the new iteration, got %q (was %q)", e.SeedMsgID, seed1.SeedMsgID)
	}
	if e.Task != "phase 2" {
		t.Fatalf("reseed must update the task, got %q", e.Task)
	}
	msgs, _ := Peek(runTestSession, id1)
	if len(msgs) != 1 || msgs[0].Payload != "phase 2" || msgs[0].ID != e.SeedMsgID {
		t.Fatalf("new seed must be the one pending row in the worker inbox: %+v", msgs)
	}
}

// TestAcquireSpawnWorkerDeadWorkerFreshStart is the spec's negative
// control: reuse must never wedge a run behind a corpse — a gone window
// falls back to a fresh worker.
func TestAcquireSpawnWorkerDeadWorkerFreshStart(t *testing.T) {
	useTempBusDir(t)
	f := fakeLiveSpawns(t)

	id1, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 1")
	if err != nil {
		t.Fatal(err)
	}
	answerSpawn(t, runTestSession, id1)
	f.deadWindows[id1] = true

	id2, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 2")
	if err != nil {
		t.Fatal(err)
	}
	if id2 == id1 || f.fresh != 2 {
		t.Fatalf("dead worker must trigger a fresh start: got %s after %s, %d starts", id2, id1, f.fresh)
	}
}

// TestAcquireSpawnWorkerDistinctNodesDistinctWorkers is the spec's other
// negative control: reuse is keyed per run+node, never global — a second
// node (and a second run) must not adopt the first node's worker.
func TestAcquireSpawnWorkerDistinctNodesDistinctWorkers(t *testing.T) {
	useTempBusDir(t)
	f := fakeLiveSpawns(t)

	id1, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "task a")
	if err != nil {
		t.Fatal(err)
	}
	idOtherNode, err := acquireSpawnWorker(runTestSession, "run-1", "fanout#0", "edit", "task b")
	if err != nil {
		t.Fatal(err)
	}
	idOtherRun, err := acquireSpawnWorker(runTestSession, "run-2", "implement", "edit", "task c")
	if err != nil {
		t.Fatal(err)
	}
	if id1 == idOtherNode || id1 == idOtherRun || idOtherNode == idOtherRun {
		t.Fatalf("distinct nodes/runs must get distinct workers: %s %s %s", id1, idOtherNode, idOtherRun)
	}
	if f.fresh != 3 {
		t.Fatalf("expected 3 fresh workers, got %d", f.fresh)
	}
}

// TestExecSpawnLoopReusesWorker walks a loop over a spawn node end to end:
// one worker serves both iterations (the assertion that would have caught
// the three-worker run in the MUX-131 report), the worker survives
// RefreshSpawnStatus mid-run, and is released by it once the run is
// terminal.
func TestExecSpawnLoopReusesWorker(t *testing.T) {
	g := &Graph{Name: "spawn-loop", Start: "w",
		Nodes: []Node{
			{ID: "w", Type: NodeSpawn, Role: "edit", Message: "implement"},
			{ID: "b", Type: NodeSend, Role: "build", Action: "build", Message: "build it"},
		},
		Edges: []Edge{
			{From: "w", To: "b"},
			{From: "b", To: "w", Outcome: OutcomeFailure, MaxIterations: 2},
		}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)

	// Iteration 1: worker starts fresh, answers, node completes.
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	worker := st.TaskID
	if worker == "" || f.fresh != 1 {
		t.Fatalf("fresh worker expected on first dispatch: task %q, %d starts", worker, f.fresh)
	}
	answerSpawn(t, runTestSession, worker)

	// Mid-run persistence: the responded worker is NOT reaped while its
	// run is in flight — reaping here is exactly what forced a fresh
	// worker per iteration.
	if _, err := RefreshSpawnStatus(runTestSession); err != nil {
		t.Fatal(err)
	}
	if e, _ := GetSpawnEntry(runTestSession, worker); e.Status != "running" {
		t.Fatalf("responded worker of an in-flight run must stay running, got %q", e.Status)
	}
	if len(f.killed) != 0 {
		t.Fatalf("responded worker of an in-flight run must not be killed: %v", f.killed)
	}

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeDone {
		t.Fatalf("w state %q, want done after worker answered", s)
	}

	// Build fails -> loop edge re-arms the spawn node.
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
	step(t, runTestSession, run.ID)

	// Iteration 2: same worker, no fresh start.
	st, _ = ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeRunning || st.TaskID != worker {
		t.Fatalf("re-entry must reuse worker %s: state %q task %q", worker, st.State, st.TaskID)
	}
	if f.fresh != 1 {
		t.Fatalf("re-entry started a fresh worker: %d starts", f.fresh)
	}
	answerSpawn(t, runTestSession, worker)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID)

	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunComplete {
		t.Fatalf("run state %q, want complete", r.State)
	}
	// THE Defect B assertion: one worker for the whole multi-iteration
	// run, counted from the spawn store, not read off the code.
	if n := spawnCountForRun(t, runTestSession, run.ID); n != 1 {
		t.Fatalf("run must have exactly 1 spawn worker, got %d", n)
	}

	// Run terminal: the normal reap path now releases the worker.
	if _, err := RefreshSpawnStatus(runTestSession); err != nil {
		t.Fatal(err)
	}
	e, _ := GetSpawnEntry(runTestSession, worker)
	if e.Status != "completed" || len(f.killed) != 1 || f.killed[0] != worker {
		t.Fatalf("terminal run must release the worker: status %q killed %v", e.Status, f.killed)
	}
}

// TestExecSpawnTaskNamesOwnedRoles pins the ownership preamble a graph-owned
// worker receives. Without it the worker runs on its base definition alone,
// whose Orchestration Role section tells every edit-shaped agent to delegate
// build/test/review — a second pipeline racing the graph's own parked nodes.
func TestExecSpawnTaskNamesOwnedRoles(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "impl",
		Nodes: []Node{
			{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "Implement phase 4"},
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "test", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{{From: "impl", To: "build"}, {From: "build", To: "test"}},
	}
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)
	step(t, runTestSession, run.ID)

	if len(*tasks) != 1 {
		t.Fatalf("expected 1 spawned worker, got %d: %v", len(*tasks), *tasks)
	}
	task := (*tasks)[0]
	for _, want := range []string{"build, test", "Do NOT delegate", run.ID, "node impl", "Implement phase 4"} {
		if !strings.Contains(task, want) {
			t.Errorf("worker task missing %q:\n%s", want, task)
		}
	}
	if strings.Contains(task, "muxcode send edit") {
		t.Errorf("preamble must not claim the graph owns the worker's own role:\n%s", task)
	}
}

// TestExecSpawnTaskNamesOnlyReachableRoles pins the reachability narrowing: a
// send the worker's node can never reach is not its succession, so claiming it
// would forbid a delegation nothing was going to duplicate.
func TestExecSpawnTaskNamesOnlyReachableRoles(t *testing.T) {
	// A branch node fans to two arms: only the worker's own arm is its
	// succession. Both arms stay reachable from start, so the graph is valid.
	g := &Graph{
		Name:  "t",
		Start: "fork",
		Nodes: []Node{
			{ID: "fork", Type: NodeCondition, Conditions: map[string]any{"env_set": "HOME"}},
			{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "work"},
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "other", Type: NodeSend, Role: "deploy", Action: "deploy", Message: "go"},
		},
		Edges: []Edge{
			{From: "fork", To: "impl"},
			{From: "fork", To: "other", Outcome: OutcomeFailure},
			{From: "impl", To: "build"},
		},
	}

	roles := graphOwnedRoles(g, "impl")
	if len(roles) != 1 || roles[0] != "build" {
		t.Errorf("worker at impl owns only its downstream build, got %v", roles)
	}
	if forkRoles := graphOwnedRoles(g, "fork"); len(forkRoles) != 2 {
		t.Errorf("both arms are downstream of the fork, got %v", forkRoles)
	}
}

// TestExecSpawnTaskUnprefixedWithoutSendNodes is the negative control: a graph
// that dispatches nothing but workers owns no delegations, so the worker's
// task must arrive verbatim. An implementation that always prefixed would pass
// the positive case above and fail here.
func TestExecSpawnTaskUnprefixedWithoutSendNodes(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "impl",
		Nodes: []Node{{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "Just do it"}},
	}
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)
	step(t, runTestSession, run.ID)

	if len(*tasks) != 1 {
		t.Fatalf("expected 1 spawned worker, got %d: %v", len(*tasks), *tasks)
	}
	if got := (*tasks)[0]; got != "edit: Just do it" {
		t.Errorf("task %q, want the message verbatim — no send nodes means nothing is owned", got)
	}
}

// TestExecMapTaskCarriesOwnership covers the second call site: map fans out
// through its own dispatch path, which regresses independently of NodeSpawn.
func TestExecMapTaskCarriesOwnership(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "fan",
		Nodes: []Node{
			{ID: "fan", Type: NodeMap, Role: "edit", Items: "one,two", Message: "Handle ${item}"},
			{ID: "review", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
		},
		Edges: []Edge{{From: "fan", To: "review"}},
	}
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)
	step(t, runTestSession, run.ID)

	if len(*tasks) != 2 {
		t.Fatalf("expected 2 map workers, got %d: %v", len(*tasks), *tasks)
	}
	for i, task := range *tasks {
		if !strings.Contains(task, "review") || !strings.Contains(task, "Do NOT delegate") {
			t.Errorf("map worker %d missing ownership preamble:\n%s", i, task)
		}
	}
	if !strings.Contains((*tasks)[0], "Handle one") || !strings.Contains((*tasks)[1], "Handle two") {
		t.Errorf("preamble must not displace per-item interpolation: %v", *tasks)
	}
}
