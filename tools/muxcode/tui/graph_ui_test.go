package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// scratchGraphSession creates an isolated bus session for run-store tests
// and removes it afterwards — nothing lands in a real session's store.
func scratchGraphSession(t *testing.T) string {
	t.Helper()
	session := "tui-graph-" + strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, t.Name())
	t.Cleanup(func() { os.RemoveAll(bus.BusDir(session)) })
	return session
}

func mustCreateRun(t *testing.T, session string, g *bus.Graph) *bus.GraphRun {
	t.Helper()
	run, err := bus.CreateGraphRun(session, g, g.Name, "test intent")
	if err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	return run
}

func mustTransition(t *testing.T, session, runID, nodeID string, states ...string) {
	t.Helper()
	for _, s := range states {
		if err := bus.TransitionGraphNode(session, runID, nodeID, s, nil); err != nil {
			t.Fatalf("transition %s -> %s: %v", nodeID, s, err)
		}
	}
}

// ── Navigation state machine ───────────────────────────────

func TestGraphUI_NavigationStateMachine(t *testing.T) {
	session := scratchGraphSession(t)
	mustCreateRun(t, session, linearGraph())
	mustCreateRun(t, session, fanOutJoinGraph())

	ui := NewGraphUI(session, "")
	ui.refresh()
	if ui.view != viewGraphRuns {
		t.Fatalf("expected run list view, got %d", ui.view)
	}
	if len(ui.rows) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(ui.rows))
	}

	ui.handleKey('j')
	if ui.runIdx != 1 {
		t.Errorf("j should move the run cursor, got %d", ui.runIdx)
	}
	ui.handleKey('k')
	if ui.runIdx != 0 {
		t.Errorf("k should move the run cursor back, got %d", ui.runIdx)
	}

	ui.handleKey(13) // Enter → DAG
	if ui.view != viewGraphDAG {
		t.Fatalf("Enter should open the DAG view, got %d", ui.view)
	}
	if ui.snap == nil || len(ui.order) == 0 {
		t.Fatal("DAG view must load a snapshot and a selection order")
	}

	ui.handleKey('j')
	if ui.nodeIdx != 1 {
		t.Errorf("j should move the node cursor, got %d", ui.nodeIdx)
	}

	ui.handleKey(13) // Enter → node detail
	if ui.view != viewGraphNode {
		t.Fatalf("Enter should open node detail, got %d", ui.view)
	}

	if got := ui.handleKey('q'); got != "" || ui.view != viewGraphDAG {
		t.Errorf("q from detail should return to DAG, got %q view %d", got, ui.view)
	}
	if got := ui.handleKey('q'); got != "" || ui.view != viewGraphRuns {
		t.Errorf("q from DAG should return to run list, got %q view %d", got, ui.view)
	}
	if got := ui.handleKey('q'); got != "quit" {
		t.Errorf("q from run list should quit, got %q", got)
	}
}

// Launched with an explicit run id, the DAG is the top level — q quits.
func TestGraphUI_DirectDAGQuits(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, linearGraph())

	ui := NewGraphUI(session, run.ID)
	ui.refresh()
	if ui.view != viewGraphDAG {
		t.Fatalf("expected DAG view on direct open, got %d", ui.view)
	}
	if got := ui.handleKey('q'); got != "quit" {
		t.Errorf("q from a directly-opened DAG should quit, got %q", got)
	}
}

// ── Tick refresh ───────────────────────────────────────────

// A store change must appear on the next refresh — this is what the 2s
// tick delivers in the live loop.
func TestGraphUI_RefreshPicksUpStoreChange(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, linearGraph())

	ui := NewGraphUI(session, run.ID)
	ui.refresh()
	frame := StripAnsi(RenderGraphFrame(*ui.snap, 120, 40, "", time.Now()))
	if !strings.Contains(frame, "◆ build") {
		t.Fatalf("expected the start node ready before transition:\n%s", frame)
	}

	mustTransition(t, session, run.ID, "build", bus.GraphNodeRunning)

	ui.refresh()
	frame = StripAnsi(RenderGraphFrame(*ui.snap, 120, 40, "", time.Now()))
	if !strings.Contains(frame, "● build") {
		t.Errorf("expected the transition visible after refresh:\n%s", frame)
	}
}

// ── Run list loader ────────────────────────────────────────

func TestLoadRunListRows_ProgressGateBadgeAndOrder(t *testing.T) {
	session := scratchGraphSession(t)
	first := mustCreateRun(t, session, linearGraph())
	second := mustCreateRun(t, session, gateGraph())

	// Runs created within the same second sort ambiguously — pin CreatedAt
	// so newest-first ordering is deterministic.
	first.CreatedAt = 100
	if err := bus.WriteGraphRun(session, first); err != nil {
		t.Fatalf("WriteGraphRun: %v", err)
	}
	second.CreatedAt = 200
	if err := bus.WriteGraphRun(session, second); err != nil {
		t.Fatalf("WriteGraphRun: %v", err)
	}

	// Drive the gated run to a waiting gate with one node done.
	mustTransition(t, session, second.ID, "review", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, second.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting)

	rows := LoadRunListRows(session, time.Now())
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].ID != second.ID {
		t.Errorf("expected newest run first, got %s", rows[0].ID)
	}
	gated := rows[0]
	if !gated.GateWaiting {
		t.Error("expected the gate badge on the run holding a waiting gate")
	}
	if gated.Done != 1 || gated.Total != 3 {
		t.Errorf("expected progress 1/3, got %d/%d", gated.Done, gated.Total)
	}
	if rows[1].ID != first.ID || rows[1].GateWaiting {
		t.Errorf("expected the linear run second and unbadged, got %+v", rows[1])
	}
}

// A completed run renders as a post-mortem from persisted state.
func TestLoadGraphSnapshot_PostMortem(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, linearGraph())
	for _, n := range []string{"build", "test", "review"} {
		if n != "build" {
			mustTransition(t, session, run.ID, n, bus.GraphNodeReady)
		}
		mustTransition(t, session, run.ID, n, bus.GraphNodeRunning, bus.GraphNodeDone)
	}
	if err := bus.UpdateGraphRunState(session, run.ID, bus.GraphRunComplete); err != nil {
		t.Fatalf("UpdateGraphRunState: %v", err)
	}

	snap, err := LoadGraphSnapshot(session, run.ID)
	if err != nil {
		t.Fatalf("LoadGraphSnapshot: %v", err)
	}
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", time.Now()))
	for _, want := range []string{"[complete]", "✓ build", "✓ test", "✓ review", "3/3 done"} {
		if !strings.Contains(frame, want) {
			t.Errorf("post-mortem frame missing %q:\n%s", want, frame)
		}
	}
}

// ── Render-once seam ───────────────────────────────────────

func TestGraphRenderOnce_EmptyRunList(t *testing.T) {
	session := scratchGraphSession(t)
	frame, err := GraphRenderOnce(session, "", 100)
	if err != nil {
		t.Fatalf("GraphRenderOnce: %v", err)
	}
	if !strings.Contains(StripAnsi(frame), "No graph runs") {
		t.Errorf("expected explicit empty state:\n%s", frame)
	}
}

func TestGraphRenderOnce_DAGAndUnknownRun(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, fanOutJoinGraph())

	frame, err := GraphRenderOnce(session, run.ID, 120)
	if err != nil {
		t.Fatalf("GraphRenderOnce: %v", err)
	}
	plain := StripAnsi(frame)
	for _, id := range []string{"start", "worker-a", "worker-b", "barrier"} {
		if !strings.Contains(plain, id) {
			t.Errorf("frame missing node %q:\n%s", id, plain)
		}
	}

	if _, err := GraphRenderOnce(session, "no-such-run", 120); err == nil {
		t.Error("expected an error for an unknown run id")
	}
}

// ── Template launcher ──────────────────────────────────────

func countRunDirs(t *testing.T, session string) int {
	t.Helper()
	entries, err := os.ReadDir(bus.GraphRunsDir(session))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// An invalid template is refused with the error rendered in place — and
// no run directory is ever created.
func TestGraphUI_InvalidTemplateRefusedBeforeRunDir(t *testing.T) {
	session := scratchGraphSession(t)
	ui := NewGraphLauncherUI(session)

	invalid := linearGraph()
	invalid.Edges = append(invalid.Edges, bus.Edge{From: "review", To: "build"}) // uncapped cycle
	ui.launchGraph(invalid, "broken", "")

	if ui.view != viewGraphTemplates {
		t.Errorf("expected the picker to stay after refusal, got view %d", ui.view)
	}
	if !strings.Contains(ui.tmplErr, "uncapped cycle") {
		t.Errorf("expected the validation error rendered in place, got %q", ui.tmplErr)
	}
	if n := countRunDirs(t, session); n != 0 {
		t.Errorf("expected no run directory created, found %d", n)
	}

	frame := StripAnsi(RenderTemplateListFrame(nil, 100, 0, ui.tmplErr))
	if !strings.Contains(frame, "validation failed") || !strings.Contains(frame, "uncapped cycle") {
		t.Errorf("expected the error in the picker frame:\n%s", frame)
	}
}

// A valid launch creates the run and lands in its DAG view.
func TestGraphUI_LaunchTransitionsToDAG(t *testing.T) {
	session := scratchGraphSession(t)
	ui := NewGraphLauncherUI(session)

	ui.launchGraph(linearGraph(), "linear", "ship it")
	if ui.view != viewGraphDAG {
		t.Fatalf("expected DAG view after launch, got %d", ui.view)
	}
	if ui.snap == nil || ui.snap.Run.Intent != "ship it" {
		t.Fatalf("expected the new run loaded with its intent, got %+v", ui.snap)
	}
	if n := countRunDirs(t, session); n != 1 {
		t.Errorf("expected exactly one run directory, found %d", n)
	}
}

func TestTemplateNeedsIntent(t *testing.T) {
	g := linearGraph()
	if TemplateNeedsIntent(g) {
		t.Error("plain messages must not demand an intent")
	}
	g.Nodes[0].Message = "do ${intent} now"
	if !TemplateNeedsIntent(g) {
		t.Error("${intent} in a message must demand the prompt")
	}
}

// The intent prompt edits with printable keys and backspace, launches on
// Enter, and cancels back to the picker on Escape.
func TestGraphUI_IntentPromptFlow(t *testing.T) {
	session := scratchGraphSession(t)
	ui := NewGraphLauncherUI(session)
	ui.pendingGraph = linearGraph()
	ui.pendingTemplate = "linear"
	ui.view = viewGraphIntent

	for _, k := range []byte{'a', 'b', 'c', 127, 27} { // type abc, backspace, Escape
		ui.handleKey(k)
	}
	if ui.view != viewGraphTemplates {
		t.Fatalf("Escape should cancel to the picker, got view %d", ui.view)
	}
	if n := countRunDirs(t, session); n != 0 {
		t.Fatalf("cancel must not create a run, found %d", n)
	}

	ui.pendingGraph = linearGraph()
	ui.pendingTemplate = "linear"
	ui.view = viewGraphIntent
	for _, k := range []byte{'g', 'o', 13} { // type go, Enter
		ui.handleKey(k)
	}
	if ui.view != viewGraphDAG {
		t.Fatalf("Enter should launch into the DAG view, got %d", ui.view)
	}
	if ui.snap == nil || ui.snap.Run.Intent != "go" {
		t.Errorf("expected intent 'go' on the run, got %+v", ui.snap.Run)
	}
}

func TestRenderTemplateListFrame_TiersAndEmpty(t *testing.T) {
	infos := []bus.GraphTemplateInfo{
		{Name: "coding-pr", Source: "builtin", Description: "code, test, PR"},
		{Name: "custom", Source: "project", Description: "local flow"},
	}
	frame := StripAnsi(RenderTemplateListFrame(infos, 100, 1, ""))
	for _, want := range []string{"coding-pr", "builtin", "code, test, PR", "custom", "project", "▸"} {
		if !strings.Contains(frame, want) {
			t.Errorf("picker frame missing %q:\n%s", want, frame)
		}
	}

	empty := StripAnsi(RenderTemplateListFrame(nil, 100, 0, ""))
	if !strings.Contains(empty, "No graph templates") {
		t.Errorf("expected explicit empty state:\n%s", empty)
	}
}

// ── Gate queue and actions ─────────────────────────────────

// nonMutatingGateGraph gates only ordinary sends — no commit/Atlassian
// node downstream, so its approval must not carry the mutation flag.
func nonMutatingGateGraph() *bus.Graph {
	return &bus.Graph{
		Name:  "gated-benign",
		Start: "start",
		Nodes: []bus.Node{
			{ID: "start", Type: bus.NodeSend, Role: "test", Action: "test", Message: "m"},
			{ID: "gate", Type: bus.NodeWaitHuman, Message: "release the verify step?"},
			{ID: "after", Type: bus.NodeSend, Role: "review", Action: "review", Message: "m"},
		},
		Edges: []bus.Edge{
			{From: "start", To: "gate"},
			{From: "gate", To: "after"},
		},
	}
}

func approvalMarkerPath(session, runID string) string {
	return bus.GraphRunDir(session, runID) + "/approvals/gate.approved"
}

func TestGateDownstream_ImpactAndMutationFlag(t *testing.T) {
	impacts, mutating := GateDownstream(gateGraph(), "gate")
	if !mutating {
		t.Error("commit node downstream must set the mutation flag")
	}
	if len(impacts) != 1 || impacts[0].NodeID != "ship" || !impacts[0].Mutating {
		t.Errorf("expected the ship node flagged mutating, got %+v", impacts)
	}

	impacts, mutating = GateDownstream(nonMutatingGateGraph(), "gate")
	if mutating {
		t.Error("plain sends downstream must not set the mutation flag")
	}
	if len(impacts) != 1 || impacts[0].NodeID != "after" || impacts[0].Mutating {
		t.Errorf("expected one benign impact, got %+v", impacts)
	}
}

func TestGatesRearmedByRetry(t *testing.T) {
	g := gateGraph()
	if got := GatesRearmedByRetry(g, "review"); len(got) != 1 || got[0] != "gate" {
		t.Errorf("retry upstream of the gate must re-arm it, got %v", got)
	}
	if got := GatesRearmedByRetry(g, "gate"); len(got) != 1 || got[0] != "gate" {
		t.Errorf("retry at the gate must re-arm it, got %v", got)
	}
	if got := GatesRearmedByRetry(g, "ship"); len(got) != 0 {
		t.Errorf("retry downstream of the gate must not re-arm it, got %v", got)
	}
}

// The queue spans all in-flight runs, reads the frozen run definitions
// (no template file for these graphs exists anywhere on disk), and flags
// only the gate whose downstream mutates.
func TestLoadPendingGates_CrossRunAndFlags(t *testing.T) {
	session := scratchGraphSession(t)
	mutRun := mustCreateRun(t, session, gateGraph())
	benignRun := mustCreateRun(t, session, nonMutatingGateGraph())

	mustTransition(t, session, mutRun.ID, "review", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, mutRun.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting)
	mustTransition(t, session, benignRun.ID, "start", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, benignRun.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting)

	gates := LoadPendingGates(session, time.Now().Add(30*time.Second))
	if len(gates) != 2 {
		t.Fatalf("expected 2 gates across runs, got %d", len(gates))
	}
	byRun := map[string]PendingGate{}
	for _, g := range gates {
		byRun[g.RunID] = g
		if g.Waiting <= 0 {
			t.Errorf("expected a positive elapsed wait, got %v", g.Waiting)
		}
	}
	if !byRun[mutRun.ID].Mutating {
		t.Error("the commit-downstream gate must carry the mutation flag")
	}
	if byRun[benignRun.ID].Mutating {
		t.Error("the benign gate must not carry the mutation flag")
	}
	if byRun[benignRun.ID].Prompt != "release the verify step?" {
		t.Errorf("expected the node's approval prompt, got %q", byRun[benignRun.ID].Prompt)
	}

	frame := StripAnsi(RenderGateQueueFrame(gates, nil, 120, 0))
	for _, want := range []string{"gate", "⚠ mutates", "approval releases:"} {
		if !strings.Contains(frame, want) {
			t.Errorf("queue frame missing %q:\n%s", want, frame)
		}
	}
}

// A completed run's gates never appear — the queue scans in-flight only.
func TestLoadPendingGates_IgnoresFinishedRuns(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, nonMutatingGateGraph())
	mustTransition(t, session, run.ID, "start", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, run.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting)
	if err := bus.CancelGraphRun(session, run.ID); err != nil {
		t.Fatalf("CancelGraphRun: %v", err)
	}

	if gates := LoadPendingGates(session, time.Now()); len(gates) != 0 {
		t.Errorf("canceled run must not list gates, got %+v", gates)
	}
}

func TestRenderGateQueueFrame_EmptyState(t *testing.T) {
	frame := StripAnsi(RenderGateQueueFrame(nil, nil, 100, 0))
	if !strings.Contains(frame, "No gates waiting") {
		t.Errorf("expected explicit empty state:\n%s", frame)
	}
}

// Resolved gates stay visible as history — approved and skipped alike —
// even when nothing is currently waiting.
func TestGateQueue_ResolvedHistoryVisible(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, gateGraph())
	mustTransition(t, session, run.ID, "review", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, run.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting, bus.GraphNodeDone)

	canceled := mustCreateRun(t, session, nonMutatingGateGraph())
	mustTransition(t, session, canceled.ID, "gate", bus.GraphNodeReady, bus.GraphNodeSkipped)

	resolved := LoadResolvedGates(session, time.Now().Add(30*time.Second), 10)
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved gates, got %+v", resolved)
	}

	frame := StripAnsi(RenderGateQueueFrame(nil, resolved, 140, 0))
	for _, want := range []string{"recent gates", "✓ approved", "○ skipped", "No gates waiting"} {
		if !strings.Contains(frame, want) {
			t.Errorf("history frame missing %q:\n%s", want, frame)
		}
	}
}

// Nothing dispatches without the confirm keypress: 'a' only parks the
// action, 'n' abandons it, and only 'y' writes the approval marker.
func TestGraphUI_ApproveGatedOnConfirm(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, gateGraph())
	mustTransition(t, session, run.ID, "review", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, run.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting)

	ui := NewGraphGatesUI(session)
	ui.refresh()
	if len(ui.gates) != 1 {
		t.Fatalf("expected 1 pending gate, got %d", len(ui.gates))
	}

	ui.handleKey('a')
	if ui.view != viewGraphConfirm || ui.pending == nil || ui.pending.Kind != "approve" {
		t.Fatalf("a should park an approve behind the confirm, got view %d pending %+v", ui.view, ui.pending)
	}
	if !ui.pending.Mutating {
		t.Error("the confirm must carry the mutation flag")
	}
	frame := StripAnsi(RenderConfirmFrame(*ui.pending, 100, ""))
	for _, want := range []string{"Approve gate gate", "ship", "git/Atlassian mutation"} {
		if !strings.Contains(frame, want) {
			t.Errorf("confirm frame missing %q:\n%s", want, frame)
		}
	}
	if _, err := os.Stat(approvalMarkerPath(session, run.ID)); err == nil {
		t.Fatal("no approval may be written before the confirm keypress")
	}

	ui.handleKey('n')
	if ui.view != viewGraphGates || ui.pending != nil {
		t.Fatalf("n should abandon the action, got view %d", ui.view)
	}
	if _, err := os.Stat(approvalMarkerPath(session, run.ID)); err == nil {
		t.Fatal("an abandoned confirm must not write an approval")
	}

	ui.handleKey('a')
	ui.handleKey('y')
	if _, err := os.Stat(approvalMarkerPath(session, run.ID)); err != nil {
		t.Fatalf("confirmed approve must write the approval marker: %v", err)
	}
	if ui.view != viewGraphGates {
		t.Errorf("expected return to the queue after approval, got view %d", ui.view)
	}
}

// The confirm promised a waiting gate — a node in any other state refuses.
func TestGraphUI_ApproveRefusedOnNonWaitingNode(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, gateGraph()) // gate still pending

	ui := NewGraphGatesUI(session)
	ui.refresh()
	ui.confirm(&GraphAction{Kind: "approve", RunID: run.ID, NodeID: "gate"})
	ui.handleKey('y')

	if !strings.Contains(ui.actionErr, "not waiting") {
		t.Errorf("expected a not-waiting refusal, got %q", ui.actionErr)
	}
	if ui.view != viewGraphConfirm {
		t.Errorf("a failed action must stay on the confirm with its error, got view %d", ui.view)
	}
	if _, err := os.Stat(approvalMarkerPath(session, run.ID)); err == nil {
		t.Fatal("a refused approve must not write an approval marker")
	}
}

// DAG-view 'a' opens the same confirm — but only on a waiting gate.
func TestGraphUI_DAGApproveOnlyOnWaitingGate(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, gateGraph())
	mustTransition(t, session, run.ID, "review", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, run.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting)

	ui := NewGraphUI(session, run.ID)
	ui.refresh()

	ui.nodeIdx = 2 // ship — not a gate
	ui.handleKey('a')
	if ui.view != viewGraphDAG {
		t.Fatalf("a on a non-gate node must be a no-op, got view %d", ui.view)
	}

	ui.nodeIdx = 1 // the waiting gate
	ui.handleKey('a')
	if ui.view != viewGraphConfirm || ui.pending == nil || ui.pending.NodeID != "gate" {
		t.Fatalf("a on a waiting gate must open the approve confirm, got view %d", ui.view)
	}
}

// Cancel and retry run behind confirms and call the MUX-014 paths.
func TestGraphUI_CancelAndRetryFlow(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, linearGraph())

	ui := NewGraphUI(session, run.ID)
	ui.refresh()

	ui.handleKey('c')
	if ui.view != viewGraphConfirm || ui.pending.Kind != "cancel" {
		t.Fatalf("c should park a cancel confirm, got view %d", ui.view)
	}
	ui.handleKey('y')
	got, err := bus.ReadGraphRun(session, run.ID)
	if err != nil || got.State != bus.GraphRunCanceled {
		t.Fatalf("confirmed cancel must cancel the run, got %+v err %v", got, err)
	}

	ui.nodeIdx = 0 // build
	ui.handleKey('r')
	if ui.view != viewGraphConfirm || ui.pending.Kind != "retry" {
		t.Fatalf("r should park a retry confirm, got view %d", ui.view)
	}
	ui.handleKey('y')
	got, err = bus.ReadGraphRun(session, run.ID)
	if err != nil || got.State != bus.GraphRunRunning {
		t.Fatalf("confirmed retry must restart the run, got %+v err %v", got, err)
	}

	// The retry confirm warns when it re-arms a gate.
	frame := StripAnsi(RenderConfirmFrame(GraphAction{
		Kind: "retry", RunID: "r", NodeID: "review", Rearms: []string{"gate"},
	}, 100, ""))
	if !strings.Contains(frame, "re-arms gate gate") || !strings.Contains(frame, "fresh approval") {
		t.Errorf("retry confirm must warn about re-armed gates:\n%s", frame)
	}
}

// ── Surface cycling (MUX-105 Phases 6–7) ───────────────────

func TestGraphUI_TabCyclesSurfacesForwardAndBack(t *testing.T) {
	session := scratchGraphSession(t)
	mustCreateRun(t, session, linearGraph())

	ui := NewGraphUI(session, "")
	ui.refresh()

	want := []graphView{viewGraphGates, viewGraphPrompt, viewGraphTemplates, viewGraphRuns}
	for _, w := range want {
		ui.handleKey(9) // Tab
		if ui.view != w {
			t.Fatalf("Tab should cycle to view %d, got %d", w, ui.view)
		}
	}

	ui.cycleSurface(-1) // Shift-Tab path
	if ui.view != viewGraphTemplates {
		t.Errorf("backward cycle from runs should reach the launcher, got %d", ui.view)
	}
}

// Tab is inert in every drill-in view — cycling out mid-task would
// discard context.
func TestGraphUI_TabInertInGuardedViews(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, linearGraph())

	ui := NewGraphUI(session, run.ID)
	ui.refresh()

	guarded := []graphView{viewGraphDAG, viewGraphNode, viewGraphIntent, viewGraphConfirm}
	for _, v := range guarded {
		ui.view = v
		if v == viewGraphConfirm {
			ui.pending = &GraphAction{Kind: "cancel", RunID: run.ID}
		}
		ui.handleKey(9)
		if ui.view != v {
			t.Errorf("Tab must be inert in view %d, moved to %d", v, ui.view)
		}
	}
}

func TestGraphUI_SelectionSurvivesFullCycle(t *testing.T) {
	session := scratchGraphSession(t)
	first := mustCreateRun(t, session, linearGraph())
	second := mustCreateRun(t, session, fanOutJoinGraph())
	first.CreatedAt = 100
	second.CreatedAt = 200
	for _, r := range []*bus.GraphRun{first, second} {
		if err := bus.WriteGraphRun(session, r); err != nil {
			t.Fatalf("WriteGraphRun: %v", err)
		}
	}

	ui := NewGraphUI(session, "")
	ui.refresh()
	ui.runIdx = 1 // the older run (newest lists first)
	selected := ui.rows[1].ID

	for i := 0; i < len(graphSurfaces); i++ {
		ui.handleKey(9)
	}
	if ui.view != viewGraphRuns {
		t.Fatalf("expected a full cycle back to runs, got %d", ui.view)
	}
	if ui.rows[ui.runIdx].ID != selected {
		t.Errorf("selection must survive a full cycle: want %s, got %s", selected, ui.rows[ui.runIdx].ID)
	}
}

func TestGraphUI_RemovedSelectionFallsBackToFirstRow(t *testing.T) {
	session := scratchGraphSession(t)
	first := mustCreateRun(t, session, linearGraph())
	second := mustCreateRun(t, session, fanOutJoinGraph())
	first.CreatedAt = 100
	second.CreatedAt = 200
	for _, r := range []*bus.GraphRun{first, second} {
		if err := bus.WriteGraphRun(session, r); err != nil {
			t.Fatalf("WriteGraphRun: %v", err)
		}
	}

	ui := NewGraphUI(session, "")
	ui.refresh()
	ui.runIdx = 1 // select the older run, then delete it mid-cycle
	os.RemoveAll(bus.GraphRunDir(session, ui.rows[1].ID))

	for i := 0; i < len(graphSurfaces); i++ {
		ui.handleKey(9)
	}
	if ui.runIdx != 0 {
		t.Errorf("a removed selection must degrade to the first row, got idx %d", ui.runIdx)
	}
}

// Every surface header names itself and carries the cycle hint — the
// scriptable seam Phase 7 asserts on.
func TestSurfaceHeadersCarryCycleHint(t *testing.T) {
	frames := map[string]string{
		"Graph Runs":    StripAnsi(RenderRunListFrame(nil, 100, -1)),
		"Launch Graph":  StripAnsi(RenderTemplateListFrame(nil, 100, 0, "")),
		"Pending Gates": StripAnsi(RenderGateQueueFrame(nil, nil, 100, 0)),
	}
	for name, frame := range frames {
		if !strings.Contains(frame, name) || !strings.Contains(frame, "Tab: next surface") {
			t.Errorf("%s header must carry the surface name and cycle hint:\n%s", name, frame)
		}
	}
}

// ── Gate ambient switch (MUX-108) ──────────────────────────

// A newly-waiting gate switches the UI to the gates surface — once per
// gate, and never out of a drill-in view.
func TestGraphUI_NewGateSwitchesToGatesSurface(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, gateGraph())

	ui := NewGraphUI(session, "")
	ui.refresh()
	ui.checkGateSwitch() // no gate waiting yet — no switch
	if ui.view != viewGraphRuns {
		t.Fatalf("no gate: view must stay runs, got %d", ui.view)
	}

	mustTransition(t, session, run.ID, "review", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, run.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting)

	ui.checkGateSwitch()
	if ui.view != viewGraphGates {
		t.Fatalf("new gate must switch to the gates surface, got %d", ui.view)
	}
	if !strings.Contains(ui.notice, "new gate") {
		t.Errorf("expected a new-gate notice, got %q", ui.notice)
	}

	// The same gate on the next tick must not re-switch after the user
	// navigates away.
	ui.view = viewGraphRuns
	ui.checkGateSwitch()
	if ui.view != viewGraphRuns {
		t.Errorf("a known gate must not re-switch, got view %d", ui.view)
	}
}

func TestGraphUI_GateSwitchNeverYanksDrillIns(t *testing.T) {
	session := scratchGraphSession(t)
	run := mustCreateRun(t, session, gateGraph())
	mustTransition(t, session, run.ID, "review", bus.GraphNodeRunning, bus.GraphNodeDone)
	mustTransition(t, session, run.ID, "gate", bus.GraphNodeReady, bus.GraphNodeWaiting)

	ui := NewGraphUI(session, run.ID) // DAG drill-in
	ui.refresh()
	ui.checkGateSwitch()
	if ui.view != viewGraphDAG {
		t.Errorf("a drill-in must never be yanked to gates, got %d", ui.view)
	}
}

// ── Shared surface (MUX-108) ───────────────────────────────

// The selected surface is shared: one pane's switch is adopted by every
// other pane on its tick — but adoption never yanks a drill-in.
func TestGraphUI_SurfaceSharedAcrossPanes(t *testing.T) {
	session := scratchGraphSession(t)
	mustCreateRun(t, session, linearGraph())

	a := NewGraphUI(session, "")
	a.refresh()
	b := NewGraphUI(session, "")
	b.refresh()

	a.cycleSurface(1) // runs → gates (writes the shared selection)
	if a.view != viewGraphGates {
		t.Fatalf("cycle order: expected gates, got %d", a.view)
	}
	b.syncSharedSurface()
	if b.view != viewGraphGates {
		t.Errorf("pane B must adopt pane A's surface, got %d", b.view)
	}

	run := mustCreateRun(t, session, fanOutJoinGraph())
	c := NewGraphUI(session, run.ID) // DAG drill-in
	c.refresh()
	c.syncSharedSurface()
	if c.view != viewGraphDAG {
		t.Errorf("adoption must never yank a drill-in, got %d", c.view)
	}

	// Adoption is one-way: B adopting must not have rewritten the file.
	k, ok := bus.ReadControlPaneSurface(session)
	if !ok || k != "gates" {
		t.Errorf("shared selection must remain gates, got %q ok=%v", k, ok)
	}
}

// ── Node detail ────────────────────────────────────────────

func TestRenderNodeDetailFrame_FieldsAndWorktree(t *testing.T) {
	snap := snapshot(linearGraph(), map[string]string{"build": bus.GraphNodeDone})
	snap.Statuses["build"].Outcome = bus.OutcomeSuccess
	snap.Statuses["build"].Output = "line-1\nline-2"
	snap.Statuses["build"].TaskID = "spawn-abc"
	snap.Statuses["build"].StartedAt = 1000
	snap.Statuses["build"].DoneAt = 1042
	snap.Worktrees = map[string]string{"spawn-abc": "/tmp/wt/spawn-abc"}

	frame := StripAnsi(RenderNodeDetailFrame(snap, "build", 100))
	for _, want := range []string{"build", "send", "outcome", "success", "spawn-abc", "/tmp/wt/spawn-abc", "line-1", "took", "42s"} {
		if !strings.Contains(frame, want) {
			t.Errorf("detail frame missing %q:\n%s", want, frame)
		}
	}
}

func TestRenderNodeDetailFrame_UnknownNode(t *testing.T) {
	snap := snapshot(linearGraph(), nil)
	frame := StripAnsi(RenderNodeDetailFrame(snap, "nope", 100))
	if !strings.Contains(frame, "unknown node") {
		t.Errorf("expected unknown-node message:\n%s", frame)
	}
}
