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

func budgetResets(t *testing.T, runID string) []LifecycleEntry {
	t.Helper()
	return cancelEvents(t, "graph-loop-budget-reset", runID)
}

// TestSpecToPRFixBudgetIsPerPhase is the MUX-193 defect inverted: under the
// run-wide budget, run 1790608128 failed at Phase 4 after Phases 1 and 2 had
// spent its three fixes. Here phases needing up to the cap each — 3, 2, 0, 3
// and 1 fixes, nine in all — run to close-out, and every phase entry after
// the first writes one reset row.
func TestSpecToPRFixBudgetIsPerPhase(t *testing.T) {
	run := createTestRun(t, fixLoopFixture(t, 5))
	step(t, runTestSession, run.ID)

	fixes := []int{3, 2, 0, 3, 1}
	for i, failures := range fixes {
		if !drivePhase(t, run.ID, failures, i == len(fixes)-1) {
			t.Fatalf("Phase %d failed the run with %d fixes, within its own budget", i+1, failures)
		}
	}
	if s := nodeState(t, runTestSession, run.ID, "close-spec"); s != GraphNodeRunning {
		t.Errorf("close-spec state %q, want running — every phase completed", s)
	}
	if rows := budgetResets(t, run.ID); len(rows) != len(fixes)-1 {
		t.Errorf("want %d reset rows, one per phase entered by the loop-back, got %+v", len(fixes)-1, rows)
	}
	r, _ := ReadGraphRun(runTestSession, run.ID)
	if n := r.EdgeFires["loop-check->implement:success"]; n != len(fixes)-1 {
		t.Errorf("the phase loop's run-wide count must be untouched by the reset: %d, want %d", n, len(fixes)-1)
	}
}

// TestSpecToPRFixBudgetStopsOnePhase is the negative control: a phase needing
// a fourth fix still exhausts the cap and fails the run — in Phase 1, and in
// a later phase whose budget a reset has refreshed.
func TestSpecToPRFixBudgetStopsOnePhase(t *testing.T) {
	for _, fixes := range [][]int{{4}, {1, 4}} {
		run := createTestRun(t, fixLoopFixture(t, len(fixes)))
		step(t, runTestSession, run.ID)
		for i, failures := range fixes[:len(fixes)-1] {
			if !drivePhase(t, run.ID, failures, false) {
				t.Fatalf("%v: Phase %d failed the run", fixes, i+1)
			}
		}
		if drivePhase(t, run.ID, fixes[len(fixes)-1], true) {
			t.Fatalf("%v: a phase needing four fixes completed past a cap of three", fixes)
		}
		assertFixBudgetExhausted(t, run.ID)
	}
}

// TestSpecToPRFixBudgetSurvivesRestart reads the counter the way a restarted
// daemon does — from run.json — at phase entry and mid-phase: it holds the
// current phase's fixes, neither zero nor the run's total.
func TestSpecToPRFixBudgetSurvivesRestart(t *testing.T) {
	run := createTestRun(t, fixLoopFixture(t, 2))
	step(t, runTestSession, run.ID)
	if !drivePhase(t, run.ID, 2, false) {
		t.Fatal("Phase 1 failed the run")
	}
	persisted := func() int {
		r, err := ReadGraphRun(runTestSession, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r.EdgeFires[fixBuildKey]
	}
	if n := persisted(); n != 0 {
		t.Errorf("at Phase 2 entry the persisted fix count is %d, want 0 — Phase 1's 2 cleared", n)
	}
	rows := budgetResets(t, run.ID)
	if len(rows) != 1 || !strings.Contains(rows[0].Detail, fixBuildKey+" (was 2)") {
		t.Errorf("want one reset row naming %s (was 2), got %+v", fixBuildKey, rows)
	}

	answer(t, run.ID, "implement", "implemented. EXIT=0")
	answer(t, run.ID, "build", "")
	answer(t, run.ID, "test", "")
	answer(t, run.ID, "review", reviewFindings)
	answer(t, run.ID, "fix", "fixed. EXIT=0")
	if n := persisted(); n != 1 {
		t.Errorf("mid-Phase 2 the persisted fix count is %d, want Phase 2's 1", n)
	}
}

func TestValidateResetsIterations(t *testing.T) {
	base := func() *Graph {
		return &Graph{Name: "g", Start: "a",
			Nodes: []Node{
				{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
				{ID: "b", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
			},
			Edges: []Edge{
				{From: "a", To: "b"},
				{From: "b", To: "a", Outcome: OutcomeFailure, MaxIterations: 3},
				{From: "b", To: "b", MaxIterations: 2},
			}}
	}
	if v := base().Validate(); !v.OK() {
		t.Fatalf("base graph must validate: %v", v.Errors)
	}
	for _, c := range []struct {
		name, want string
		mutate     func(g *Graph)
	}{
		{"unknown edge", "names no edge", func(g *Graph) { g.Edges[2].ResetsIterations = []string{"a->z"} }},
		{"uncapped resetter", "no cap of its own", func(g *Graph) { g.Edges[0].ResetsIterations = []string{"b->a"} }},
		{"self reset", "resets its own iterations", func(g *Graph) { g.Edges[2].ResetsIterations = []string{"b->b"} }},
		{"chained", "chained resets", func(g *Graph) {
			g.Edges[1].ResetsIterations = []string{"b->b"}
			g.Edges[2].ResetsIterations = []string{"b->a:failure"}
		}},
		{"uncapped cycle", "cycle", func(g *Graph) { g.Edges[1].MaxIterations = 0 }},
	} {
		g := base()
		c.mutate(g)
		assertErrorContains(t, g.Validate(), c.want)
	}

	ok := base()
	ok.Edges[2].ResetsIterations = []string{"b->a"}
	if v := ok.Validate(); !v.OK() {
		t.Errorf("a capped edge resetting another's budget must validate: %v", v.Errors)
	}
	tmpl, err := ParseGraph([]byte(builtinGraphJSON["50-spec-to-pr"]))
	if err != nil {
		t.Fatal(err)
	}
	if v := tmpl.Validate(); !v.OK() {
		t.Errorf("50-spec-to-pr with its per-phase resets must validate: %v", v.Errors)
	}
}
