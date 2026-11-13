package bus

import (
	"strings"
	"testing"
)

// TestDefectLoopMergeEdgeClearsCriteriaOutput pins that the loop edge back to
// select erases the acceptance worker's report, which update-spec embeds: a
// node's output lives until the node runs again, and criteria runs only when a
// close-out is refused, so a defect that needed it leaked its report into the
// next defect's verify-spec.
func TestDefectLoopMergeEdgeClearsCriteriaOutput(t *testing.T) {
	g := mustTemplate(t, "55-defect-backlog-loop")
	if !strings.Contains(g.node("update-spec").Message, "${output:criteria}") {
		t.Fatal("update-spec no longer embeds ${output:criteria}; the premise of this test is gone")
	}
	found := false
	for _, e := range g.Edges {
		if e.From == "merge" && e.To == "select" {
			found = len(e.ClearsOutputs) == 1 && e.ClearsOutputs[0] == "criteria"
		}
	}
	if !found {
		t.Error("merge->select must clear criteria's output")
	}
	if v := g.Validate(); len(v.Errors) > 0 {
		t.Errorf("template invalid: %v", v.Errors)
	}
}

// TestClearEdgeOutputs: a firing edge erases only the nodes it names.
func TestClearEdgeOutputs(t *testing.T) {
	g := &Graph{
		Name:  "g",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "x"},
			{ID: "b", Type: NodeSend, Role: "test", Action: "test", Message: "y"},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
	run := createTestRun(t, g)
	if err := TransitionGraphNode(runTestSession, run.ID, "b", GraphNodeReady, nil); err != nil {
		t.Fatal(err)
	}
	for id, out := range map[string]string{"a": "report-a", "b": "report-b"} {
		out := out
		if err := TransitionGraphNode(runTestSession, run.ID, id, GraphNodeRunning, func(s *GraphNodeStatus) { s.Output = out }); err != nil {
			t.Fatal(err)
		}
	}
	msg := "${output:a}|${output:b}"

	clearEdgeOutputs(runTestSession, run, Edge{From: "b", To: "a", ClearsOutputs: []string{"a"}})
	got := interpolateGraphMessage(runTestSession, run, msg, "")
	if strings.Contains(got, "report-a") || !strings.Contains(got, "no report recorded") {
		t.Errorf("a's report survived the clearing edge: %q", got)
	}
	if !strings.Contains(got, "report-b") {
		t.Errorf("an unnamed node's report was erased: %q", got)
	}

	// Negative control: an edge naming nothing clears nothing.
	clearEdgeOutputs(runTestSession, run, Edge{From: "a", To: "b"})
	if got := interpolateGraphMessage(runTestSession, run, "${output:b}", ""); !strings.Contains(got, "report-b") {
		t.Errorf("an edge with no ClearsOutputs erased b: %q", got)
	}
}

// routeClearsOutputsRun drives a real edge firing: a seeded stale report on z,
// then a's completion routes the a->b edge, which carries clears when non-nil.
// It returns ${output:z} as a later dispatch would expand it.
func routeClearsOutputsRun(t *testing.T, clears []string) string {
	t.Helper()
	g := &Graph{
		Name:  "g",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "x"},
			{ID: "b", Type: NodeSend, Role: "test", Action: "test", Message: "y"},
			{ID: "z", Type: NodeSend, Role: "review", Action: "review", Message: "z"},
		},
		Edges: []Edge{{From: "a", To: "b", ClearsOutputs: clears}, {From: "b", To: "z"}},
	}
	run := createTestRun(t, g)
	if err := MutateNodeStatus(runTestSession, run.ID, "z", func(s *GraphNodeStatus) { s.Output = "stale-report" }); err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Fatalf("a->b did not fire: b is %q, want running", s)
	}
	return interpolateGraphMessage(runTestSession, run, "${output:z}", "")
}

// TestExecEdgeFiringClearsOutputs drives the executor, so removing the
// clearEdgeOutputs call from the routing loop fails it; the edge without
// ClearsOutputs is the negative control.
func TestExecEdgeFiringClearsOutputs(t *testing.T) {
	if got := routeClearsOutputsRun(t, []string{"z"}); strings.Contains(got, "stale-report") || !strings.Contains(got, "no report recorded") {
		t.Errorf("routed edge left the stale report in place: %q", got)
	}
	if got := routeClearsOutputsRun(t, nil); !strings.Contains(got, "stale-report") {
		t.Errorf("an edge without ClearsOutputs erased z's report: %q", got)
	}
}

// TestValidateClearsOutputsNamesNode: a typo in clears_outputs is an error, not
// a silent no-op that leaves the stale report in place.
func TestValidateClearsOutputsNamesNode(t *testing.T) {
	g := &Graph{
		Name:  "g",
		Start: "a",
		Nodes: []Node{{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "x"}},
		Edges: []Edge{{From: "a", To: "a", MaxIterations: 2, ClearsOutputs: []string{"nosuch"}}},
	}
	v := g.Validate()
	for _, e := range v.Errors {
		if strings.Contains(e, "names no node") {
			return
		}
	}
	t.Errorf("clears_outputs naming an unknown node passed validation: %v", v.Errors)
}
