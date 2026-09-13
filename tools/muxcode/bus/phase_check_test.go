package bus

import (
	"strings"
	"testing"
)

// The phase check sits between update-spec and the commit gate so a human
// is asked to approve a commit only when the guard will accept it: an open
// phase routes straight to the stuck gate instead of costing two gates per
// lap (2026-09-09, run 1788966148). The guard's own failure edge stays as
// the backstop for a spec edited between the check and the commit.
func TestSpecToPRPhaseCheckPrecedesGate(t *testing.T) {
	tpl, _, err := ResolveGraphTemplate("spec-to-pr")
	if err != nil {
		t.Fatal(err)
	}
	check := tpl.node("phase-check")
	if check == nil || check.Type != NodeCondition {
		t.Fatalf("phase-check must be a condition node, got %+v", check)
	}
	if id, _ := check.Conditions["spec_phase_committable"].(string); id != "commit" {
		t.Errorf("phase-check must ask spec_phase_committable about the commit node, got %v", check.Conditions)
	}
	if !templateEdge(tpl, "update-spec", "phase-check") || !templateEdge(tpl, "phase-check", "phase-gate") {
		t.Error("update-spec -> phase-check -> phase-gate is the only road to the commit gate")
	}
	if templateEdge(tpl, "update-spec", "phase-gate") {
		t.Error("a direct update-spec -> phase-gate edge asks the human before the check")
	}
	toStuck := map[string]bool{}
	for _, e := range tpl.Edges {
		if e.To == "stuck-gate" && e.Outcome == OutcomeFailure {
			toStuck[e.From] = true
		}
	}
	if !toStuck["phase-check"] {
		t.Error("an open phase must route phase-check -[failure]-> stuck-gate, skipping the commit gate")
	}
	if !toStuck["commit"] {
		t.Error("the guard's decline edge commit -[failure]-> stuck-gate must stay as the backstop")
	}
	for _, id := range []string{"implement", "fix"} {
		if n := tpl.node(id); n == nil || !strings.Contains(n.Message, "run agent") || !strings.Contains(n.Message, "never go test") || !strings.Contains(n.Message, "task id") {
			t.Errorf("%s must tell the worker to verify through the run agent, never run the suite itself, and report the task id with the counts", id)
		}
	}
	if v := tpl.Validate(); !v.OK() {
		t.Errorf("spec-to-pr must validate: %v", v.Errors)
	}
}

// The condition and the guard share one predicate, so the check passes
// exactly when the guard would ship: one more completed phase than the
// commit node has fired. Every other state fails closed — toward the stuck
// gate, never toward a human approval the guard would then decline.
func TestSpecPhaseCommittableCondition(t *testing.T) {
	g := &Graph{Name: "g", Start: "commit",
		Nodes: []Node{
			{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "commit", Guard: GuardPhaseProgress},
			{ID: "next", Type: NodeCondition, Conditions: map[string]any{"spec_phases_remaining": true}},
		},
		Edges: []Edge{{From: "commit", To: "next"}}}
	run := createTestRun(t, g)
	ctx := &ChainContext{Session: runTestSession, GraphRun: run, Graph: g}
	cond := map[string]any{"spec_phase_committable": "commit"}

	writeSpecFixture(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n")
	if ok, res := EvaluateConditions(cond, ctx); !ok {
		t.Errorf("first commit with its phase complete must pass, got %+v", res)
	}

	run.EdgeFires = map[string]int{"commit->next:success": 1}
	if ok, res := EvaluateConditions(cond, ctx); ok || !strings.Contains(res[0].Detail, "1 phases complete, 1 shipped") {
		t.Errorf("one shipped, one complete: the phase is open and must fail with counts, got %+v", res)
	}

	writeSpecFixture(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [x] b\n")
	if ok, _ := EvaluateConditions(cond, ctx); !ok {
		t.Error("two complete, one shipped: the next phase is committable")
	}

	if err := ClearActiveSpec(runTestSession); err != nil {
		t.Fatal(err)
	}
	if ok, res := EvaluateConditions(cond, ctx); ok || !strings.Contains(res[0].Detail, "no active spec") {
		t.Errorf("no active spec must fail closed, got %+v", res)
	}

	bare := &ChainContext{Session: runTestSession}
	if ok, res := EvaluateConditions(cond, bare); ok || !strings.Contains(res[0].Detail, "graph-run context") {
		t.Errorf("without a graph-run context the check must fail closed, got %+v", res)
	}
	if ok, res := EvaluateConditions(map[string]any{"spec_phase_committable": "nope"}, ctx); ok || !strings.Contains(res[0].Detail, "names no node") {
		t.Errorf("an unknown commit node must fail closed, got %+v", res)
	}
	if ok, res := EvaluateConditions(map[string]any{"spec_phase_committable": true}, ctx); ok || !strings.Contains(res[0].Detail, "must name") {
		t.Errorf("a non-string value must fail closed, got %+v", res)
	}
}

// Validation refuses a check that mirrors nothing: the named node must
// exist and carry the phase-progress guard, or the pre-gate question and
// the guard's answer could diverge.
func TestValidateSpecPhaseCommittableNamesGuardedNode(t *testing.T) {
	build := func(target string, guard string) *Graph {
		return &Graph{Name: "g", Start: "check",
			Nodes: []Node{
				{ID: "check", Type: NodeCondition, Conditions: map[string]any{"spec_phase_committable": target}},
				{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "commit", Guard: guard},
				{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			},
			Edges: []Edge{{From: "check", To: "gate"}, {From: "gate", To: "commit"}}}
	}
	if v := build("commit", GuardPhaseProgress).Validate(); !v.OK() {
		t.Errorf("a check naming the guarded commit node must validate: %v", v.Errors)
	}
	for _, tc := range []struct{ target, guard, want string }{
		{"commit", "", "lacks the phase-progress guard"},
		{"nope", GuardPhaseProgress, "unknown node"},
		{"", GuardPhaseProgress, "must name"},
	} {
		v := build(tc.target, tc.guard).Validate()
		if v.OK() || !strings.Contains(strings.Join(v.Errors, "\n"), tc.want) {
			t.Errorf("target %q guard %q: expected error containing %q, got %v", tc.target, tc.guard, tc.want, v.Errors)
		}
	}
}

// End to end in the executor: with the phase open the check routes to the
// stuck gate and the commit gate is never armed; with the phase complete
// the commit gate arms and the stuck gate stays pending.
func TestExecPhaseCheckRoutesOpenPhaseToStuckGate(t *testing.T) {
	pinActor(t, "")
	graph := func() *Graph {
		return &Graph{Name: "g", Start: "check",
			Nodes: []Node{
				{ID: "check", Type: NodeCondition, Conditions: map[string]any{"spec_phase_committable": "commit"}},
				{ID: "gate", Type: NodeWaitHuman, Message: "approve the commit"},
				{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "commit", Guard: GuardPhaseProgress},
				{ID: "stuck", Type: NodeWaitHuman, Message: "retry or cancel"},
			},
			Edges: []Edge{
				{From: "check", To: "gate"},
				{From: "check", To: "stuck", Outcome: OutcomeFailure},
				{From: "gate", To: "commit"},
				{From: "commit", To: "stuck", Outcome: OutcomeFailure},
			}}
	}

	run := createTestRun(t, graph())
	writeSpecFixture(t, "### Phase 1: A\n- [ ] open\n")
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "stuck"); s != GraphNodeWaiting {
		t.Errorf("open phase: stuck gate must be armed, got %q", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodePending {
		t.Errorf("open phase: commit gate must never arm, got %q", s)
	}

	run2 := createTestRun(t, graph())
	writeSpecFixture(t, "### Phase 1: A\n- [x] done\n")
	step(t, runTestSession, run2.ID)
	step(t, runTestSession, run2.ID)
	if s := nodeState(t, runTestSession, run2.ID, "gate"); s != GraphNodeWaiting {
		t.Errorf("complete phase: commit gate must arm, got %q", s)
	}
	if s := nodeState(t, runTestSession, run2.ID, "stuck"); s != GraphNodePending {
		t.Errorf("complete phase: stuck gate must stay pending, got %q", s)
	}

	// Backstop: the spec reopened between the check and the commit. The
	// approved gate releases the commit, whose guard asks the same predicate
	// again and must decline toward the stuck gate — the check removes the
	// double prompt, not the guard.
	writeSpecFixture(t, "### Phase 1: A\n- [ ] reopened\n")
	if err := ApproveGraphGate(runTestSession, run2.ID, "gate"); err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run2.ID)
	step(t, runTestSession, run2.ID)
	st, err := ReadNodeStatus(runTestSession, run2.ID, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "still open") {
		t.Errorf("reopened phase: the guard must still decline the released commit, got %q %q", st.State, st.Output)
	}
	if s := nodeState(t, runTestSession, run2.ID, "stuck"); s != GraphNodeWaiting {
		t.Errorf("reopened phase: the declined commit must route to the stuck gate, got %q", s)
	}
}
