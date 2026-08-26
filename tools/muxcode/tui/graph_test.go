package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// ── Fixtures ───────────────────────────────────────────────

func linearGraph() *bus.Graph {
	return &bus.Graph{
		Name:  "linear",
		Start: "build",
		Nodes: []bus.Node{
			{ID: "build", Type: bus.NodeSend, Role: "build", Action: "build", Message: "m"},
			{ID: "test", Type: bus.NodeSend, Role: "test", Action: "test", Message: "m"},
			{ID: "review", Type: bus.NodeSend, Role: "review", Action: "review", Message: "m"},
		},
		Edges: []bus.Edge{
			{From: "build", To: "test"},
			{From: "test", To: "review"},
		},
	}
}

func fanOutJoinGraph() *bus.Graph {
	return &bus.Graph{
		Name:  "fanout",
		Start: "start",
		Nodes: []bus.Node{
			{ID: "start", Type: bus.NodeSend, Role: "build", Action: "build", Message: "m"},
			{ID: "worker-a", Type: bus.NodeSend, Role: "test", Action: "test", Message: "m"},
			{ID: "worker-b", Type: bus.NodeSend, Role: "review", Action: "review", Message: "m"},
			{ID: "barrier", Type: bus.NodeJoin, Join: bus.JoinAll},
		},
		Edges: []bus.Edge{
			{From: "start", To: "worker-a"},
			{From: "start", To: "worker-b"},
			{From: "worker-a", To: "barrier"},
			{From: "worker-b", To: "barrier"},
		},
	}
}

func cappedLoopGraph() *bus.Graph {
	return &bus.Graph{
		Name:  "loop",
		Start: "build",
		Nodes: []bus.Node{
			{ID: "build", Type: bus.NodeSend, Role: "build", Action: "build", Message: "m"},
			{ID: "fix", Type: bus.NodeSend, Role: "edit", Action: "edit", Message: "m"},
			{ID: "ship", Type: bus.NodeSend, Role: "review", Action: "review", Message: "m"},
		},
		Edges: []bus.Edge{
			{From: "build", To: "fix", Outcome: bus.OutcomeFailure},
			{From: "build", To: "ship"},
			{From: "fix", To: "build", MaxIterations: 3},
		},
	}
}

func gateGraph() *bus.Graph {
	return &bus.Graph{
		Name:  "gated",
		Start: "review",
		Nodes: []bus.Node{
			{ID: "review", Type: bus.NodeSend, Role: "review", Action: "review", Message: "m"},
			{ID: "gate", Type: bus.NodeWaitHuman},
			{ID: "ship", Type: bus.NodeSend, Role: "commit", Action: "commit", Message: "m"},
		},
		Edges: []bus.Edge{
			{From: "review", To: "gate"},
			{From: "gate", To: "ship"},
		},
	}
}

func snapshot(g *bus.Graph, states map[string]string) GraphSnapshot {
	statuses := make(map[string]*bus.GraphNodeStatus, len(g.Nodes))
	for _, n := range g.Nodes {
		st := &bus.GraphNodeStatus{NodeID: n.ID, State: bus.GraphNodePending}
		if s, ok := states[n.ID]; ok {
			st.State = s
		}
		statuses[n.ID] = st
	}
	return GraphSnapshot{
		Run: &bus.GraphRun{
			ID: "run-1", Template: g.Name, State: bus.GraphRunRunning,
			CreatedAt: 1000, UpdatedAt: 1060, EdgeFires: map[string]int{},
		},
		Graph:    g,
		Statuses: statuses,
	}
}

var frameClock = time.Unix(1120, 0)

// ── Layout ─────────────────────────────────────────────────

func TestLayoutGraph_Linear(t *testing.T) {
	grid := LayoutGraph(linearGraph())
	want := [][]string{{"build"}, {"test"}, {"review"}}
	assertLayers(t, grid, want)
}

func TestLayoutGraph_FanOutJoin(t *testing.T) {
	grid := LayoutGraph(fanOutJoinGraph())
	want := [][]string{{"start"}, {"worker-a", "worker-b"}, {"barrier"}}
	assertLayers(t, grid, want)
}

// A capped loop edge must not distort layering — the cycle it closes is
// exempt, exactly as in the validator's DAG rule.
func TestLayoutGraph_CappedLoopExcluded(t *testing.T) {
	grid := LayoutGraph(cappedLoopGraph())
	want := [][]string{{"build"}, {"fix", "ship"}}
	assertLayers(t, grid, want)
	if len(grid.Loops) != 1 || grid.Loops[0].From != "fix" {
		t.Errorf("expected the fix->build loop edge captured, got %+v", grid.Loops)
	}
}

// Layout must never lose nodes, even on an unvalidated cyclic definition.
func TestLayoutGraph_UncappedCycleStillPlacesAll(t *testing.T) {
	g := linearGraph()
	g.Edges = append(g.Edges, bus.Edge{From: "review", To: "build"}) // uncapped cycle
	grid := LayoutGraph(g)
	placed := 0
	for _, l := range grid.Layers {
		placed += len(l)
	}
	if placed != len(g.Nodes) {
		t.Errorf("expected all %d nodes placed, got %d (layers %v)", len(g.Nodes), placed, grid.Layers)
	}
}

func assertLayers(t *testing.T, grid *GraphGrid, want [][]string) {
	t.Helper()
	if len(grid.Layers) != len(want) {
		t.Fatalf("layers = %v, want %v", grid.Layers, want)
	}
	for i := range want {
		if strings.Join(grid.Layers[i], ",") != strings.Join(want[i], ",") {
			t.Errorf("layer %d = %v, want %v", i, grid.Layers[i], want[i])
		}
	}
}

// ── DAG frame ──────────────────────────────────────────────

func TestRenderGraphFrame_ContainsEveryNode(t *testing.T) {
	snap := snapshot(fanOutJoinGraph(), map[string]string{
		"start": bus.GraphNodeDone, "worker-a": bus.GraphNodeRunning,
	})
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	for _, id := range []string{"start", "worker-a", "worker-b", "barrier"} {
		if !strings.Contains(frame, id) {
			t.Errorf("frame missing node %q:\n%s", id, frame)
		}
	}
	if strings.Contains(frame, "flat view") {
		t.Errorf("wide pane must render the grid, not the fallback:\n%s", frame)
	}
}

func TestRenderGraphFrame_StateGlyphs(t *testing.T) {
	snap := snapshot(linearGraph(), map[string]string{
		"build": bus.GraphNodeDone, "test": bus.GraphNodeFailed, "review": bus.GraphNodePending,
	})
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	for _, want := range []string{"✓ build", "✗ test", "· review"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame missing %q:\n%s", want, frame)
		}
	}
}

func TestRenderGraphFrame_EdgesDrawn(t *testing.T) {
	snap := snapshot(linearGraph(), nil)
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	if !strings.Contains(frame, "─") || !strings.Contains(frame, "▶") {
		t.Errorf("expected box-drawing edges with arrowheads:\n%s", frame)
	}
}

// Fan-out to different rows needs vertical routing in the gutter.
func TestRenderGraphFrame_FanOutRoutesVertically(t *testing.T) {
	snap := snapshot(fanOutJoinGraph(), nil)
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	if !strings.Contains(frame, "┐") && !strings.Contains(frame, "└") {
		t.Errorf("expected elbow glyphs for the fan-out:\n%s", frame)
	}
}

func TestRenderGraphFrame_LoopBadge(t *testing.T) {
	snap := snapshot(cappedLoopGraph(), nil)
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	if !strings.Contains(frame, "↺ ×3") {
		t.Errorf("expected untraveled loop badge ↺ ×3:\n%s", frame)
	}

	snap.Run.EdgeFires[bus.EdgeFireKey(bus.Edge{From: "fix", To: "build", MaxIterations: 3})] = 2
	frame = StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	if !strings.Contains(frame, "↺ 2×3") {
		t.Errorf("expected fired loop badge ↺ 2×3:\n%s", frame)
	}
}

// A waiting wait_human gate must be visually prominent: flag glyph.
func TestRenderGraphFrame_GateProminent(t *testing.T) {
	snap := snapshot(gateGraph(), map[string]string{
		"review": bus.GraphNodeDone, "gate": bus.GraphNodeWaiting,
	})
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	if !strings.Contains(frame, "⚑ gate") {
		t.Errorf("expected prominent gate glyph ⚑:\n%s", frame)
	}
}

func TestRenderGraphFrame_SelectionCursor(t *testing.T) {
	snap := snapshot(linearGraph(), nil)
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "test", frameClock))
	if !strings.Contains(frame, "▸") {
		t.Errorf("expected selection cursor ▸:\n%s", frame)
	}
}

func TestRenderGraphFrame_Header(t *testing.T) {
	snap := snapshot(linearGraph(), map[string]string{"build": bus.GraphNodeDone})
	snap.Run.Intent = "ship the feature"
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	for _, want := range []string{"run-1", "[running]", "linear", "1/3 done", "ship the feature"} {
		if !strings.Contains(frame, want) {
			t.Errorf("header missing %q:\n%s", want, frame)
		}
	}
}

// A finished run's elapsed time freezes at UpdatedAt — post-mortem frames
// must not keep counting.
func TestRenderGraphFrame_PostMortemElapsedFrozen(t *testing.T) {
	snap := snapshot(linearGraph(), map[string]string{
		"build": bus.GraphNodeDone, "test": bus.GraphNodeDone, "review": bus.GraphNodeDone,
	})
	snap.Run.State = bus.GraphRunComplete // CreatedAt 1000, UpdatedAt 1060
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	if !strings.Contains(frame, "1m0s") {
		t.Errorf("expected elapsed frozen at 1m0s (not %s):\n%s", frameClock.Sub(time.Unix(1000, 0)), frame)
	}
	if !strings.Contains(frame, "[complete]") {
		t.Errorf("expected complete state in header:\n%s", frame)
	}
}

// ── Fallback flat list ─────────────────────────────────────

func TestRenderGraphFrame_NarrowPaneFallsBack(t *testing.T) {
	snap := snapshot(linearGraph(), map[string]string{
		"build": bus.GraphNodeDone, "test": bus.GraphNodeFailed, "review": bus.GraphNodeWaiting,
	})
	frame := StripAnsi(RenderGraphFrame(snap, 30, 40, "", frameClock))
	if !strings.Contains(frame, "flat view") {
		t.Fatalf("expected fallback marker on a narrow pane:\n%s", frame)
	}
	// Failed and waiting nodes must list before done ones.
	iFailed := strings.Index(frame, "test")
	iWaiting := strings.Index(frame, "review")
	iDone := strings.Index(frame, "build")
	if iFailed > iDone || iWaiting > iDone {
		t.Errorf("expected failed/waiting before done in fallback order:\n%s", frame)
	}
}

// A graph deeper than the pane (tall fan-out) must also degrade to the
// flat list — the grid has no scroll, so height overflow would clip.
func TestRenderGraphFrame_DeepGraphFallsBack(t *testing.T) {
	g := &bus.Graph{Name: "deep", Start: "start"}
	g.Nodes = append(g.Nodes, bus.Node{ID: "start", Type: bus.NodeSend, Role: "build", Action: "build", Message: "m"})
	for _, id := range []string{"w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8"} {
		g.Nodes = append(g.Nodes, bus.Node{ID: id, Type: bus.NodeSend, Role: "test", Action: "test", Message: "m"})
		g.Edges = append(g.Edges, bus.Edge{From: "start", To: id})
	}
	snap := snapshot(g, nil)

	frame := StripAnsi(RenderGraphFrame(snap, 200, 12, "", frameClock))
	if !strings.Contains(frame, "flat view") {
		t.Fatalf("expected fallback on a short pane:\n%s", frame)
	}

	frame = StripAnsi(RenderGraphFrame(snap, 200, 40, "", frameClock))
	if strings.Contains(frame, "flat view") {
		t.Errorf("tall pane must render the grid:\n%s", frame)
	}
}

func TestRenderGraphFrame_FallbackContainsEveryNode(t *testing.T) {
	snap := snapshot(fanOutJoinGraph(), nil)
	frame := StripAnsi(RenderGraphFrame(snap, 25, 40, "", frameClock))
	for _, id := range []string{"start", "worker-a", "worker-b", "barrier"} {
		if !strings.Contains(frame, id) {
			t.Errorf("fallback missing node %q:\n%s", id, frame)
		}
	}
}

// Missing status files (store mid-creation) must render as pending, not
// crash or vanish.
func TestRenderGraphFrame_MissingStatusIsPending(t *testing.T) {
	snap := snapshot(linearGraph(), nil)
	snap.Statuses = map[string]*bus.GraphNodeStatus{} // no files at all
	frame := StripAnsi(RenderGraphFrame(snap, 120, 40, "", frameClock))
	if !strings.Contains(frame, "· build") {
		t.Errorf("expected missing statuses to render pending:\n%s", frame)
	}
}
