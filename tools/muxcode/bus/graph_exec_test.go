package bus

import (
	"fmt"
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
	graphSpawnFn = func(sess, role, task, owner string) (string, error) {
		n++
		id := fmt.Sprintf("spawn-fake%04d", n)
		tasks = append(tasks, role+": "+task)
		entry := SpawnEntry{ID: id, Role: role, SpawnRole: id, Owner: owner,
			Task: task, Status: "completed", StartedAt: time.Now().Unix()}
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
	if err := RetryGraphRun(runTestSession, run.ID, "gate"); err != nil {
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

	if err := RetryGraphRun(runTestSession, run.ID, "b"); err != nil {
		t.Fatalf("retry: %v", err)
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
	if err := RetryGraphRun(runTestSession, run.ID, "b"); err == nil {
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
	if err := RetryGraphRun(runTestSession, run.ID, "a"); err != nil {
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
