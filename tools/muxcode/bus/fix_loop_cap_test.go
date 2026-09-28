package bus

import (
	"strings"
	"testing"
)

const (
	reviewFindings = "Review: 1 must-fix, 0 should-fix, 0 nits — changes required. EXIT=0"
	reviewClean    = "Review: 0 must-fix, 0 should-fix, 0 nits — LGTM. EXIT=0"
	fixBuildKey    = "fix->build:success"
)

// fixLoopFixture is 50-spec-to-pr reduced to its fix cycle and phase loop:
// the template's own edges among implement, build, test, review, fix,
// loop-check and close-spec, with review routing straight to loop-check in
// place of the spec-update and commit-gate chain. The spawn and condition
// nodes become send nodes so a test answers each with a verdict token.
//
// The edges are copied from the builtin JSON rather than restated, so a
// change to the template's fix or loop-back edges (MUX-193 Phase 2) reaches
// every test built on this fixture.
func fixLoopFixture(t *testing.T, phases int) *Graph {
	t.Helper()
	tmpl, err := ParseGraph([]byte(builtinGraphJSON["50-spec-to-pr"]))
	if err != nil {
		t.Fatal(err)
	}
	g := &Graph{Name: "fix-loop-cap", Start: "implement",
		Nodes: []Node{
			{ID: "implement", Type: NodeSend, Role: "edit", Action: "implement", Message: "implement"},
			*tmpl.node("build"),
			*tmpl.node("test"),
			*tmpl.node("review"),
			{ID: "fix", Type: NodeSend, Role: "edit", Action: "fix", Message: "fix"},
			{ID: "loop-check", Type: NodeSend, Role: "plan", Action: "loop-check", Message: "phases remaining?"},
			{ID: "close-spec", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "close out"},
		},
		Edges: []Edge{{From: "review", To: "loop-check"}}}
	for _, e := range tmpl.Edges {
		if g.node(e.From) == nil || g.node(e.To) == nil {
			continue
		}
		if e.MaxIterationsFromSpec {
			e.MaxIterations, e.MaxIterationsFromSpec = phases, false
		}
		g.Edges = append(g.Edges, e)
	}
	return g
}

// answer completes a running node and ticks until its successor is
// dispatched. Build and test are evidenced by history rows, the rest by the
// verdict token in their reply.
func answer(t *testing.T, runID, nodeID, reply string) {
	t.Helper()
	switch nodeID {
	case "build", "test":
		completeSendNode(t, runTestSession, runID, nodeID, OutcomeSuccess)
	default:
		completeSendNodeSentinel(t, runTestSession, runID, nodeID, reply)
	}
	step(t, runTestSession, runID)
	step(t, runTestSession, runID)
}

func runFailed(t *testing.T, runID string) bool {
	t.Helper()
	r, err := ReadGraphRun(runTestSession, runID)
	if err != nil {
		t.Fatal(err)
	}
	return r.State == GraphRunFailed
}

// drivePhase walks one phase: implement, then build/test/review with the
// review failing reviewFailures times, each failure answered by a fix. It
// stops early, returning false, the moment the run fails; last answers
// loop-check "no phases remaining".
func drivePhase(t *testing.T, runID string, reviewFailures int, last bool) bool {
	t.Helper()
	answer(t, runID, "implement", "implemented. EXIT=0")
	for i := 0; ; i++ {
		answer(t, runID, "build", "")
		answer(t, runID, "test", "")
		if i == reviewFailures {
			answer(t, runID, "review", reviewClean)
			break
		}
		answer(t, runID, "review", reviewFindings)
		answer(t, runID, "fix", "fixed. EXIT=0")
		if runFailed(t, runID) {
			return false
		}
	}
	verdict := "EXIT=0"
	if last {
		verdict = "EXIT=1"
	}
	answer(t, runID, "loop-check", verdict)
	return !runFailed(t, runID)
}

func assertFixBudgetExhausted(t *testing.T, runID string) {
	t.Helper()
	r, _ := ReadGraphRun(runTestSession, runID)
	if r.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed", r.State)
	}
	if n := r.EdgeFires[fixBuildKey]; n != 3 {
		t.Errorf("%s fired %d times, want the cap of 3", fixBuildKey, n)
	}
	exhausted := cancelEvents(t, "graph-loop-exhausted", runID)
	if len(exhausted) != 1 || !strings.Contains(exhausted[0].Detail, fixBuildKey) {
		t.Errorf("want one graph-loop-exhausted row naming %s, got %+v", fixBuildKey, exhausted)
	}
	if s := nodeState(t, runTestSession, runID, "build"); s == GraphNodeRunning {
		t.Error("the fix past the cap must not be built")
	}
}

// TestSpecToPRFixBudgetIsRunWide pins MUX-193 as it stands: phases whose
// reviews fail 2, 1 and 0 times spend the three-fix budget between them, so
// Phase 4's single finding exhausts fix->build and fails the run with Phases
// 4 and 5 unfinished. MUX-193 Phase 2 inverts this to the run completing.
func TestSpecToPRFixBudgetIsRunWide(t *testing.T) {
	run := createTestRun(t, fixLoopFixture(t, 5))
	step(t, runTestSession, run.ID)

	for phase, failures := range []int{2, 1, 0} {
		if !drivePhase(t, run.ID, failures, false) {
			t.Fatalf("Phase %d failed the run; only Phase 4 should", phase+1)
		}
	}
	if drivePhase(t, run.ID, 1, false) {
		t.Fatal("Phase 4 completed; the run-wide budget should have been spent")
	}
	assertFixBudgetExhausted(t, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "close-spec"); s != GraphNodePending {
		t.Errorf("close-spec state %q, want pending — the close-out was never reached", s)
	}
}

// TestSpecToPRFixBudgetStopsOnePhase is the negative control: one phase
// needing a fourth fix still exhausts the cap and fails the run, however the
// budget is scoped.
func TestSpecToPRFixBudgetStopsOnePhase(t *testing.T) {
	run := createTestRun(t, fixLoopFixture(t, 1))
	step(t, runTestSession, run.ID)

	if drivePhase(t, run.ID, 4, true) {
		t.Fatal("a phase needing four fixes completed past a cap of three")
	}
	assertFixBudgetExhausted(t, run.ID)
}
