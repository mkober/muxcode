package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// GraphSnapshot bundles one read of a run's store: metadata, the frozen
// graph definition, and per-node statuses. Layout and rendering are pure
// functions of a snapshot, so every frame is unit-testable without a
// terminal and `--render-once` is exactly one snapshot → one frame.
//
// Worktrees and Held are optional enrichment — loaders fill them, fixtures
// may leave nil. Held cannot be derived from state: a held node sits in Done.
type GraphSnapshot struct {
	Run       *bus.GraphRun
	Graph     *bus.Graph
	Statuses  map[string]*bus.GraphNodeStatus
	Worktrees map[string]string
	Held      map[string]bool
}

func (s GraphSnapshot) isHeld(id string) bool { return s.Held[id] }

// GraphGrid is the layered layout of a graph: layer index = column,
// nodes within a layer stacked in definition order.
type GraphGrid struct {
	Layers [][]string        // node ids per layer
	Pos    map[string][2]int // node id → (layer, row)
	Loops  []bus.Edge        // capped loop edges — annotated, never drawn as cycles
}

// LayoutGraph computes topological layers via Kahn's algorithm with capped
// loop edges (max_iterations > 0) removed — they close cycles by design,
// exactly as the validator's DAG check treats them. Layer index is the
// longest-path depth from the roots, so an edge always points to a strictly
// deeper layer and every drawn edge runs left to right.
func LayoutGraph(g *bus.Graph) *GraphGrid {
	grid := &GraphGrid{Pos: make(map[string][2]int)}

	indeg := make(map[string]int, len(g.Nodes))
	out := make(map[string][]string)
	for _, n := range g.Nodes {
		indeg[n.ID] = 0
	}
	for _, e := range g.Edges {
		if e.MaxIterations > 0 {
			grid.Loops = append(grid.Loops, e)
			continue
		}
		out[e.From] = append(out[e.From], e.To)
		indeg[e.To]++
	}

	layer := make(map[string]int, len(g.Nodes))
	var queue []string
	for _, n := range g.Nodes { // definition order keeps layout stable
		if indeg[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}
	placed := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		placed++
		for _, to := range out[cur] {
			if l := layer[cur] + 1; l > layer[to] {
				layer[to] = l
			}
			indeg[to]--
			if indeg[to] == 0 {
				queue = append(queue, to)
			}
		}
	}
	// A validated graph is acyclic here, but layout must never lose a node:
	// anything still unplaced (cycle in an unvalidated definition) lands in
	// one extra layer past the deepest placed node.
	if placed < len(g.Nodes) {
		deepest := 0
		for _, l := range layer {
			if l > deepest {
				deepest = l
			}
		}
		for _, n := range g.Nodes {
			if indeg[n.ID] > 0 {
				layer[n.ID] = deepest + 1
			}
		}
	}

	depth := 0
	for _, l := range layer {
		if l > depth {
			depth = l
		}
	}
	grid.Layers = make([][]string, depth+1)
	for _, n := range g.Nodes {
		l := layer[n.ID]
		grid.Pos[n.ID] = [2]int{l, len(grid.Layers[l])}
		grid.Layers[l] = append(grid.Layers[l], n.ID)
	}
	return grid
}

// ── Node presentation ──────────────────────────────────────

// nodeGlyph returns the state glyph and Dracula color for a node. A
// wait_human gate keeps its distinct flag glyph in every state so gates
// stay visually prominent in the DAG, per the MUX-031 authority note.
// held outranks state: a held node is in Done, so it would otherwise render
// green with a tick. Everything waiting on a person gets the same yellow flag.
func nodeGlyph(nodeType, state, outcome string, held bool) (glyph string, color string) {
	if held {
		return "⚑", Yellow + Bold
	}
	if nodeType == bus.NodeWaitHuman {
		if state == bus.GraphNodeWaiting {
			return "⚑", Yellow + Bold
		}
		return "⚑", stateColor(state)
	}
	if bus.ConditionTookBranch(nodeType, state, outcome) {
		return "◇", Comment // false branch taken — control flow, not a failure
	}
	switch state {
	case bus.GraphNodeDone:
		glyph = "✓"
	case bus.GraphNodeFailed:
		glyph = "✗"
	case bus.GraphNodeRunning:
		glyph = "●"
	case bus.GraphNodeWaiting:
		glyph = "◐"
	case bus.GraphNodeReady:
		glyph = "◆"
	case bus.GraphNodeSkipped:
		glyph = "○"
	default: // pending
		glyph = "·"
	}
	return glyph, stateColor(state)
}

// nodeWho names who runs a node — the label suffix that makes a DAG of
// terse ids readable: a bare "a → b → c" says nothing about which agent
// is active (user catch, 2026-08-27). Shared by the grid labels and the
// detail panel so the two never disagree.
func nodeWho(n *bus.Node) string {
	switch n.Type {
	case bus.NodeSend:
		return n.Role + ":" + n.Action
	case bus.NodeSpawn, bus.NodeMap:
		return n.Type + " " + n.Role
	case bus.NodeWaitHuman:
		return "human gate"
	default:
		return n.Type
	}
}

func stateColor(state string) string {
	switch state {
	case bus.GraphNodeDone:
		return Green
	case bus.GraphNodeFailed:
		return Red
	case bus.GraphNodeRunning:
		return Cyan
	case bus.GraphNodeWaiting:
		return Yellow
	case bus.GraphNodeReady:
		return Purple
	default:
		return Comment
	}
}

// nodeState reads a node's state from the snapshot, defaulting to pending
// when the status file is missing (a run store mid-creation).
func (s GraphSnapshot) nodeState(id string) string {
	if st, ok := s.Statuses[id]; ok && st != nil {
		return st.State
	}
	return bus.GraphNodePending
}

// nodeOutcome returns a node's recorded outcome, "" when it has no
// status yet. Paired with nodeState because a condition's branch-vs-break
// distinction needs both (MUX-133).
func (s GraphSnapshot) nodeOutcome(id string) string {
	if st, ok := s.Statuses[id]; ok && st != nil {
		return st.Outcome
	}
	return ""
}

// loopAnnotation renders the capped-loop badge for edges leaving a node:
// `↺ ×N` untraveled, `↺ k×N` once the edge has fired k times.
func loopAnnotation(run *bus.GraphRun, loops []bus.Edge, nodeID string) string {
	for _, e := range loops {
		if e.From != nodeID {
			continue
		}
		fired := 0
		if run != nil && run.EdgeFires != nil {
			fired = run.EdgeFires[bus.EdgeFireKey(e)]
		}
		if fired > 0 {
			return fmt.Sprintf(" ↺ %d×%d", fired, e.MaxIterations)
		}
		return fmt.Sprintf(" ↺ ×%d", e.MaxIterations)
	}
	return ""
}

// ── Canvas ─────────────────────────────────────────────────

// cell is one canvas position: a rune and the color it renders in.
type cell struct {
	r     rune
	color string
}

type canvas struct {
	cells [][]cell
	w, h  int
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h}
	c.cells = make([][]cell, h)
	for y := range c.cells {
		c.cells[y] = make([]cell, w)
		for x := range c.cells[y] {
			c.cells[y][x] = cell{r: ' '}
		}
	}
	return c
}

// lineMerge resolves what glyph results from drawing one line rune over
// another, so crossing and joining edges render as junctions instead of
// one edge erasing the other.
var lineMerge = map[[2]rune]rune{
	{'─', '│'}: '┼', {'│', '─'}: '┼',
	{'─', '┐'}: '┬', {'┐', '─'}: '┬',
	{'─', '┌'}: '┬', {'┌', '─'}: '┬',
	{'─', '┘'}: '┴', {'┘', '─'}: '┴',
	{'─', '└'}: '┴', {'└', '─'}: '┴',
	{'│', '┐'}: '┤', {'┐', '│'}: '┤',
	{'│', '┘'}: '┤', {'┘', '│'}: '┤',
	{'│', '┌'}: '├', {'┌', '│'}: '├',
	{'│', '└'}: '├', {'└', '│'}: '├',
	{'┌', '└'}: '├', {'└', '┌'}: '├',
	{'┐', '┘'}: '┤', {'┘', '┐'}: '┤',
}

// set draws a line rune, merging with any line rune already present. The
// stronger (non-Comment) color wins so an active edge stays highlighted
// through a crossing.
func (c *canvas) set(x, y int, r rune, color string) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	prev := c.cells[y][x]
	if merged, ok := lineMerge[[2]rune{prev.r, r}]; ok {
		r = merged
	}
	if prev.color != "" && prev.color != Comment && color == Comment {
		color = prev.color
	}
	c.cells[y][x] = cell{r: r, color: color}
}

// writeText places a text run, one color for the whole run.
func (c *canvas) writeText(x, y int, s, color string) {
	for i, r := range []rune(s) {
		if x+i >= c.w || y < 0 || y >= c.h {
			return
		}
		c.cells[y][x+i] = cell{r: r, color: color}
	}
}

// String renders the canvas, grouping color runs and trimming trailing
// blanks per row.
func (c *canvas) String() string {
	var b strings.Builder
	for y := 0; y < c.h; y++ {
		end := c.w
		for end > 0 && c.cells[y][end-1].r == ' ' {
			end--
		}
		cur := ""
		for x := 0; x < end; x++ {
			cl := c.cells[y][x]
			color := cl.color
			if cl.r == ' ' {
				color = cur // spaces never open a new color run
			}
			if color != cur {
				if cur != "" {
					b.WriteString(RST)
				}
				if color != "" {
					b.WriteString(color)
				}
				cur = color
			}
			b.WriteRune(cl.r)
		}
		if cur != "" {
			b.WriteString(RST)
		}
		b.WriteRune('\n')
	}
	return b.String()
}

// ── DAG frame ──────────────────────────────────────────────

const (
	gutterWidth = 5 // connector space between layer columns
	rowPitch    = 2 // one blank row between stacked nodes for edge routing
)

// RenderGraphFrame renders a run's layered DAG view: header, node grid
// with box-drawing edges, and loop badges. Pure — every input including
// the clock is a parameter. Graphs whose grid is wider than width fall
// back to the flat list ordered by state.
func RenderGraphFrame(snap GraphSnapshot, width, height int, selection string, now time.Time) string {
	return RenderGraphFrameH(snap, width, height, selection, now, 0)
}

// RenderGraphFrameH is RenderGraphFrame with a vertical scroll offset for
// the node-detail panel — long results wrap to the pane width and the
// panel windows vertically (user request 2026-08-28: wrap + scroll, the
// tail of a result must be readable without a wider pane).
func RenderGraphFrameH(snap GraphSnapshot, width, height int, selection string, now time.Time, scroll int) string {
	grid := LayoutGraph(snap.Graph)

	labels := make(map[string]string, len(snap.Graph.Nodes))
	types := make(map[string]string, len(snap.Graph.Nodes))
	for i := range snap.Graph.Nodes {
		n := &snap.Graph.Nodes[i]
		types[n.ID] = n.Type
		glyph, _ := nodeGlyph(n.Type, snap.nodeState(n.ID), snap.nodeOutcome(n.ID), snap.isHeld(n.ID))
		label := glyph + " " + n.ID
		// Terse ids say nothing about which agent is active (a bare
		// "a → b → c" was unreadable live; user catch, 2026-08-27) — send
		// and worker nodes carry who runs them. Gates keep their glyph.
		switch n.Type {
		case bus.NodeSend, bus.NodeSpawn, bus.NodeMap:
			label += " " + nodeWho(n)
		}
		labels[n.ID] = label + loopAnnotation(snap.Run, grid.Loops, n.ID)
	}

	// Column geometry: each layer block is as wide as its widest label.
	blockW := make([]int, len(grid.Layers))
	colX := make([]int, len(grid.Layers))
	x := 2 // left margin
	for i, layerIDs := range grid.Layers {
		for _, id := range layerIDs {
			if w := len([]rune(labels[id])) + 2; w > blockW[i] { // +2 for cursor prefix
				blockW[i] = w
			}
		}
		colX[i] = x
		x += blockW[i] + gutterWidth
	}
	gridW := x - gutterWidth + 2

	maxRows := 0
	for _, l := range grid.Layers {
		if len(l) > maxRows {
			maxRows = len(l)
		}
	}
	gridH := maxRows*rowPitch + 1

	// Skip edges (spanning >1 layer) route below the grid, one lane each.
	skipLanes := 0
	for _, e := range snap.Graph.Edges {
		if e.MaxIterations > 0 {
			continue
		}
		if grid.Pos[e.To][0]-grid.Pos[e.From][0] > 1 {
			skipLanes++
		}
	}

	// Wider than the pane: a single-row chain WRAPS at node boundaries —
	// the user wants the chain shape, not the flat list, when only width
	// overflows (user request 2026-08-28). Multi-row grids cannot wrap (a
	// 2D canvas has no line boundaries), and height overflow still
	// degrades to the flat list.
	headerLines := 5 // leading blank, tab bar, run line, trailing blank, + margin
	if snap.Run.Intent != "" {
		headerLines++
	}
	if gridW > width && maxRows == 1 && skipLanes == 0 && headerLines+4 <= height {
		top := renderGraphHeader(snap, now, width) + renderWrappedChain(grid.Layers, labels, types, snap, selection, width)
		return frameWithDetails(top, snap, width, height, now, scroll)
	}
	if gridW > width || gridH+skipLanes+headerLines > height {
		return renderGraphHeader(snap, now, width) + renderGraphFallback(snap, width, height-headerLines, selection, scroll)
	}

	c := newCanvas(gridW+2, gridH+skipLanes+1)

	nodeY := func(id string) int { return grid.Pos[id][1] * rowPitch }

	// Edges first so labels overwrite line stubs cleanly.
	lane := 0
	for _, e := range snap.Graph.Edges {
		if e.MaxIterations > 0 {
			continue // loop edges are badges, not drawn cycles
		}
		fromL, toL := grid.Pos[e.From][0], grid.Pos[e.To][0]
		color := Comment
		if snap.edgeActive(e) {
			color = Cyan
		}
		fromEnd := colX[fromL] + len([]rune(labels[e.From])) + 3 // past cursor prefix + label
		yF, yT := nodeY(e.From), nodeY(e.To)
		if toL-fromL == 1 {
			drawAdjacentEdge(c, fromEnd, colX[fromL]+blockW[fromL]+gutterWidth-2, yF, yT, color)
		} else if toL > fromL {
			laneY := gridH + lane
			lane++
			drawSkipEdge(c, fromEnd, colX[toL]-2, yF, yT, laneY, color)
		}
	}

	// Node labels.
	for i, layerIDs := range grid.Layers {
		for _, id := range layerIDs {
			_, color := nodeGlyph(types[id], snap.nodeState(id), snap.nodeOutcome(id), snap.isHeld(id))
			if id == selection {
				color = Yellow + Bold
			}
			writeCursorAndLabel(c, colX[i], nodeY(id), id == selection, labels[id], color)
		}
	}

	return frameWithDetails(renderGraphHeader(snap, now, width)+c.String(), snap, width, height, now, scroll)
}

// frameWithDetails appends the vertically-windowed detail panel to a
// rendered grid or wrapped chain. Trailing blank rows are trimmed (dead
// space — user catch 2026-08-28) and the panel gets whatever height
// remains; the grid stays the load-bearing view.
func frameWithDetails(top string, snap GraphSnapshot, width, height int, now time.Time, scroll int) string {
	top = strings.TrimRight(top, "\n") + "\n"
	detailBudget := height - strings.Count(top, "\n") - 3 // separator + ↑/↓ markers
	if detailBudget >= 3 {
		top += RenderNodeDetails(snap, width, detailBudget, now, scroll)
	}
	return top
}

// renderWrappedChain renders a single-row DAG wider than the pane as a
// glyph chain wrapped at node boundaries; a trailing arrow marks the
// continuation. The chain keeps glyphs, colors, selection, and loop
// badges — everything the flat fallback loses.
func renderWrappedChain(layers [][]string, labels map[string]string, types map[string]string, snap GraphSnapshot, selection string, width int) string {
	const arrow = " ─→ "
	var b strings.Builder
	b.WriteString("\n")
	line, plain := "  ", 2
	for li, layerIDs := range layers {
		id := layerIDs[0]
		_, color := nodeGlyph(types[id], snap.nodeState(id), snap.nodeOutcome(id), snap.isHeld(id))
		lbl := labels[id]
		seg := color + lbl + RST
		segPlain := len([]rune(lbl))
		if id == selection {
			seg = Yellow + Bold + "▶ " + lbl + RST
			segPlain += 2
		}
		arrowPlain := 0
		if li > 0 {
			arrowPlain = len([]rune(arrow))
		}
		if li > 0 && plain+arrowPlain+segPlain > width-2 {
			b.WriteString(line + Comment + " ─→" + RST + "\n")
			line, plain = "    ", 4
		} else if li > 0 {
			line += Comment + arrow + RST
			plain += arrowPlain
		}
		line += seg
		plain += segPlain
	}
	b.WriteString(line + "\n")
	return b.String()
}

// RenderNodeDetails lists every node with who runs it, its timing, and
// what it is doing — the run view's answer to "what is actually
// happening": a running node shows the instruction it dispatched, a
// finished node the first line of its harvested result, a waiting gate
// its approval command. Detail text wraps onto indented continuation
// lines instead of clipping at the pane edge; maxLines windows the panel
// vertically at scroll with ↑/↓ overflow markers, clamped so overscroll
// parks on the tail rather than a blank panel.
func RenderNodeDetails(snap GraphSnapshot, width, maxLines int, now time.Time, scroll int) string {
	var lines []string
	for i := range snap.Graph.Nodes {
		n := &snap.Graph.Nodes[i]
		st := snap.Statuses[n.ID]
		state := snap.nodeState(n.ID)
		glyph, color := nodeGlyph(n.Type, state, snap.nodeOutcome(n.ID), snap.isHeld(n.ID))

		who := nodeWho(n)

		timing := ""
		if st != nil && st.StartedAt > 0 {
			end := now.Unix()
			if st.DoneAt > 0 {
				end = st.DoneAt
			}
			if end > st.StartedAt {
				timing = (time.Duration(end-st.StartedAt) * time.Second).String()
			}
		}

		detail, detailColor := "", FG
		msg := strings.ReplaceAll(n.Message, "${spec}", snap.Run.Intent)
		msg = strings.ReplaceAll(msg, "${intent}", snap.Run.Intent)
		switch state {
		case bus.GraphNodeRunning:
			detail = msg
		case bus.GraphNodeWaiting:
			detail = "waiting for approval — muxcode graph approve " + snap.Run.ID + " " + n.ID
			if n.Type != bus.NodeWaitHuman {
				detail = msg
			}
			detailColor = Yellow
		case bus.GraphNodeDone, bus.GraphNodeFailed:
			if st != nil {
				detail = strings.TrimSpace(st.Output)
				if nl := strings.IndexByte(detail, '\n'); nl >= 0 {
					detail = strings.TrimSpace(detail[:nl])
				}
			}
			// Branch-takers are done, so failed here is always genuine.
			if state == bus.GraphNodeFailed {
				detailColor = Red
			}
		}

		const detailCol = 45 // rune width of the fixed columns before the detail text
		segs := wrapRunes(detail, maxInt(10, width-detailCol))
		if len(segs) == 0 {
			segs = []string{""}
		}
		first := fmt.Sprintf("  %s%s %-10s%s %s%-20s%s %-8s %s%s%s",
			color, glyph, n.ID, RST, Comment, who, RST, timing,
			detailColor, segs[0], RST)
		lines = append(lines, fitWidth(first, width))
		indent := strings.Repeat(" ", detailCol)
		for _, s := range segs[1:] {
			lines = append(lines, fitWidth(indent+detailColor+s+RST, width))
		}
	}

	if maxLines <= 0 {
		maxLines = len(lines)
	}
	start := scroll
	if start > len(lines)-maxLines {
		start = len(lines) - maxLines
	}
	if start < 0 {
		start = 0
	}
	end := start + maxLines
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	b.WriteString("\n")
	if start > 0 {
		fmt.Fprintf(&b, "  %s↑ %d more%s\n", Comment, start, RST)
	}
	for _, ln := range lines[start:end] {
		b.WriteString(ln + "\n")
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "  %s↓ %d more%s\n", Comment, len(lines)-end, RST)
	}
	return b.String()
}

// wrapRunes greedily wraps s into segments of at most w runes, breaking
// on spaces where one falls in the back half of the segment.
func wrapRunes(s string, w int) []string {
	if s == "" {
		return nil
	}
	var out []string
	r := []rune(s)
	for len(r) > 0 {
		if len(r) <= w {
			out = append(out, string(r))
			break
		}
		cut := w
		for i := w; i > w/2; i-- {
			if r[i] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimRight(string(r[:cut]), " "))
		r = r[cut:]
		for len(r) > 0 && r[0] == ' ' {
			r = r[1:]
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// writeCursorAndLabel places the selection cursor and the node label.
func writeCursorAndLabel(c *canvas, x, y int, selected bool, label, color string) {
	if selected {
		c.writeText(x, y, "▶", Yellow+Bold)
	}
	c.writeText(x+2, y, label, color)
}

// drawAdjacentEdge connects two nodes in neighboring layers through the
// gutter: horizontal from the source, a vertical segment at the gutter
// midpoint when rows differ, horizontal into the target.
func drawAdjacentEdge(c *canvas, x0, x1, y0, y1 int, color string) {
	mid := (x0 + x1) / 2
	for x := x0; x <= mid; x++ {
		c.set(x, y0, '─', color)
	}
	if y0 != y1 {
		if y1 > y0 {
			c.set(mid, y0, '┐', color)
			for y := y0 + 1; y < y1; y++ {
				c.set(mid, y, '│', color)
			}
			c.set(mid, y1, '└', color)
		} else {
			c.set(mid, y0, '┘', color)
			for y := y1 + 1; y < y0; y++ {
				c.set(mid, y, '│', color)
			}
			c.set(mid, y1, '┌', color)
		}
	}
	for x := mid + 1; x <= x1; x++ {
		c.set(x, y1, '─', color)
	}
	c.set(x1, y1, '▶', color)
}

// drawSkipEdge routes an edge spanning multiple layers through a lane
// below the grid, keeping it clear of every node label.
func drawSkipEdge(c *canvas, x0, x1, y0, y1, laneY int, color string) {
	c.set(x0, y0, '─', color)
	c.set(x0+1, y0, '┐', color)
	for y := y0 + 1; y < laneY; y++ {
		c.set(x0+1, y, '│', color)
	}
	c.set(x0+1, laneY, '└', color)
	for x := x0 + 2; x < x1; x++ {
		c.set(x, laneY, '─', color)
	}
	c.set(x1, laneY, '┘', color)
	for y := y1 + 1; y < laneY; y++ {
		c.set(x1, y, '│', color)
	}
	c.set(x1, y1, '┌', color)
	c.set(x1+1, y1, '▶', color)
}

// edgeActive reports whether an edge should render highlighted: it has
// fired and its target is still in flight.
func (s GraphSnapshot) edgeActive(e bus.Edge) bool {
	if s.Run == nil || s.Run.EdgeFires[bus.EdgeFireKey(e)] == 0 {
		return false
	}
	switch s.nodeState(e.To) {
	case bus.GraphNodeRunning, bus.GraphNodeReady, bus.GraphNodeWaiting:
		return true
	}
	return false
}

// renderGraphHeader renders the run summary above the grid, under the
// static surface tab bar (a DAG is a drill-in of Graph Runs). Elapsed
// time freezes at UpdatedAt for finished runs so post-mortem views are
// stable.
func renderGraphHeader(snap GraphSnapshot, now time.Time, width int) string {
	run := snap.Run
	end := now.Unix()
	if run.State != bus.GraphRunRunning {
		end = run.UpdatedAt
	}
	elapsed := time.Duration(end-run.CreatedAt) * time.Second
	if elapsed < 0 {
		elapsed = 0
	}

	stateColor := Cyan
	switch run.State {
	case bus.GraphRunComplete:
		stateColor = Green
	case bus.GraphRunFailed:
		stateColor = Red
	case bus.GraphRunCanceled:
		stateColor = Comment
	}

	done, total := 0, len(snap.Graph.Nodes)
	for i := range snap.Graph.Nodes {
		if snap.nodeState(snap.Graph.Nodes[i].ID) == bus.GraphNodeDone {
			done++
		}
	}

	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Graph Runs", width))
	fmt.Fprintf(&b, "  %s%s%s%s  %s[%s]%s  %s%s  %d/%d done  %s%s\n",
		Purple, Bold, run.ID, RST,
		stateColor, run.State, RST,
		Comment, run.Template, done, total, elapsed.String(), RST)
	if run.Intent != "" {
		fmt.Fprintf(&b, "  %s%s%s\n", Comment, run.Intent, RST)
	}
	b.WriteString("\n")
	return b.String()
}

// ── Fallback flat list ─────────────────────────────────────

// fallbackStateOrder ranks node states for the flat list: what needs eyes
// first. Failed and waiting outrank everything, per the spec.
var fallbackStateOrder = map[string]int{
	bus.GraphNodeFailed:  0,
	bus.GraphNodeWaiting: 1,
	bus.GraphNodeRunning: 2,
	bus.GraphNodeReady:   3,
	bus.GraphNodePending: 4,
	bus.GraphNodeDone:    5,
	bus.GraphNodeSkipped: 6,
}

// fallbackRow is one node's row in the flat list, pre-sort.
type fallbackRow struct {
	id, typ, state, outcome string
	held                    bool
	defIdx                  int
}

// fallbackRank ranks a row for the flat list. A held node ranks with a waiting
// gate — both are the run stopped on a person — rather than with Done, which
// sorted the blocker below every pending node and off the bottom of the pane.
func fallbackRank(state string, held bool) int {
	if held {
		return fallbackStateOrder[bus.GraphNodeWaiting]
	}
	return fallbackStateOrder[state]
}

// fitWidth truncates a rendered line to the pane width, ANSI-preserving.
func fitWidth(line string, width int) string {
	if VisibleWidth(line) > width {
		return TruncateAnsi(line, width)
	}
	return line
}

// renderSurfaceTabs renders the surface tab bar every top-level frame
// shares: all surfaces named, the active one highlighted, then the cycle
// hint. Width-clamped like every other frame line — at four surfaces the
// bar outgrows a narrow pane, and an unclamped bar wraps and shifts the
// whole frame down a row.
func renderSurfaceTabs(active string, width int) string {
	names := []string{"Prompt", "Launch Graph", "Graph Runs", "Pending Gates"}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if n == active {
			parts = append(parts, Purple+Bold+n+RST)
		} else {
			parts = append(parts, Comment+n+RST)
		}
	}
	// One blank row above the bar for breathing room under the popup
	// border. Safe against the scroll-shift bug: clampLines guarantees a
	// frame never prints past the pane, so this row cannot be eaten.
	bar := "  " + strings.Join(parts, Comment+" / "+RST) + Comment + "   ⇥ Tab: next surface" + RST
	return "\n" + fitWidth(bar, width) + "\n"
}

// ── Run list ───────────────────────────────────────────────

// RunListRow is one run's summary for the run browser — precomputed by
// the loader so the renderer stays pure.
type RunListRow struct {
	ID          string
	Template    string
	State       string
	Done, Total int
	Elapsed     time.Duration
	GateWaiting bool   // a wait_human node is waiting on this run
	Results     string // one-line outcome: issues first, else what completed
}

// SummarizeRunResults compresses a run's node outcomes into one results
// cell: issues win (first failed node and why), otherwise the completed
// node chain. Success cells are built from node IDs, never node output —
// harvested output is multi-line prose whose first line lands
// mid-sentence and reads as text flowing across rows. A done id may
// carry a trailing "?": that completion was inferred with no exit-code
// proof (the run list renders the legend once). The glyph prefix
// carries identity — the renderer colors by it, and the cell stays
// readable through StripAnsi.
func SummarizeRunResults(runState string, failed []string, failedOut string, done []string) string {
	firstLine := func(s string) string {
		s = strings.TrimSpace(s)
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		return s
	}
	chain := strings.Join(done, " → ")
	switch {
	case len(failed) > 0:
		msg := "✗ " + failed[0]
		if len(failed) > 1 {
			msg += fmt.Sprintf(" +%d", len(failed)-1)
		}
		if out := firstLine(failedOut); out != "" {
			msg += ": " + out
		}
		return msg
	case runState == bus.GraphRunComplete:
		if chain != "" {
			return "✓ " + chain
		}
		return "✓ complete"
	case runState == bus.GraphRunCanceled:
		return "canceled"
	default:
		if chain != "" {
			return chain + " ⋯" // in flight — what has finished so far
		}
		return ""
	}
}

// resultsCellColor maps a results cell to its color by glyph prefix.
func resultsCellColor(results string) string {
	switch {
	case strings.HasPrefix(results, "✗"):
		return Red
	case strings.HasPrefix(results, "✓"):
		return Green
	default:
		return Comment
	}
}

// clampCol truncates s to w runes with a … marker — an overlong value in
// a %-Ns cell shoves every later column right (user catch 2026-08-28: a
// 41-rune run id broke the whole row's alignment).
func clampCol(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

// RenderRunListFrame renders the run browser: all runs newest first, with
// state, node progress, elapsed, and a gate badge where a wait_human node
// waits. Empty state renders explicitly — never a blank frame.
func RenderRunListFrame(rows []RunListRow, width, sel int) string {
	return RenderRunListFrameH(rows, width, 0, sel)
}

// RenderRunListFrameH is RenderRunListFrame with a height budget: the
// list scrolls vertically in a window that follows the selection, with
// ↑/↓ overflow indicators. height <= 0 renders every row.
func RenderRunListFrameH(rows []RunListRow, width, height, sel int) string {
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Graph Runs", width))
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	if len(rows) == 0 {
		fmt.Fprintf(&b, "  %sNo graph runs yet in this session.%s\n", Comment, RST)
		fmt.Fprintf(&b, "  %sStart one: muxcode graph run <template> [intent…] — or Tab to the launcher.%s\n", Comment, RST)
		fmt.Fprintf(&b, "  %sTemplates: muxcode graph list%s\n", Comment, RST)
		return b.String()
	}

	anyMark := false
	for _, r := range rows {
		if strings.Contains(r.Results, "?") {
			anyMark = true
			break
		}
	}

	// Window the rows to the pane, keeping the selection visible.
	start, end := 0, len(rows)
	if height > 0 {
		avail := height - 8
		if anyMark {
			avail--
		}
		start, end = scrollWindow(len(rows), avail, sel)
	}

	fmt.Fprintf(&b, "  %s   %-40s %-10s %-9s %-9s %-28s %s%s\n",
		Comment, "RUN", "STATE", "PROGRESS", "ELAPSED", "TEMPLATE", "RESULTS", RST)
	if start > 0 {
		fmt.Fprintf(&b, "  %s↑ %d more%s\n", Comment, start, RST)
	}
	for i, r := range rows[start:end] {
		i += start
		cursor := " "
		idColor := FG
		if i == sel {
			cursor = Yellow + "▸" + RST
			idColor = Cyan + Bold
		}
		stateColor := Cyan
		switch r.State {
		case bus.GraphRunComplete:
			stateColor = Green
		case bus.GraphRunFailed:
			stateColor = Red
		case bus.GraphRunCanceled:
			stateColor = Comment
		}
		badge := ""
		if r.GateWaiting {
			badge = "  " + Yellow + Bold + "⚑ gate" + RST
		}
		results := clampCol(r.Results, 90)
		line := fmt.Sprintf("  %s %s%-40s%s %s%-10s%s %d/%-7d %-9s %s%-28s%s %s%s%s%s",
			cursor, idColor, clampCol(r.ID, 40), RST,
			stateColor, r.State, RST,
			r.Done, r.Total, r.Elapsed.String(),
			Comment, clampCol(r.Template, 28), RST,
			resultsCellColor(results), results, RST, badge)
		b.WriteString(fitWidth(line, width) + "\n")
	}
	if end < len(rows) {
		fmt.Fprintf(&b, "  %s↓ %d more%s\n", Comment, len(rows)-end, RST)
	}
	// The ? explainer renders once as a legend, never per-cell
	if anyMark {
		fmt.Fprintf(&b, "  %s? = completion inferred (no exit-code proof)%s\n", Comment, RST)
	}
	return b.String()
}

// ── Template launcher ──────────────────────────────────────

// RenderTemplateListFrame renders the launcher's template picker: every
// resolvable template with its source tier and description. A validation
// failure renders in place under the list — the launch is refused, the
// picker stays.
func RenderTemplateListFrame(infos []bus.GraphTemplateInfo, width, sel int, errMsg string) string {
	return RenderTemplateListFrameH(infos, width, 0, sel, errMsg)
}

// scrollWindow computes a selection-following [start, end) row window:
// visible rows within avail (minus the two overflow indicator lines,
// floored at min 5), centered on sel. avail >= total means no window.
func scrollWindow(total, avail, sel int) (int, int) {
	if avail <= 0 || total <= avail {
		return 0, total
	}
	visible := avail - 2
	if visible < 5 {
		visible = 5
	}
	if visible > total {
		visible = total
	}
	start := sel - visible/2
	if start < 0 {
		start = 0
	}
	if start > total-visible {
		start = total - visible
	}
	return start, start + visible
}

// RenderTemplateListFrameH is RenderTemplateListFrame with a height
// budget — the picker scrolls the same way the run list does.
func RenderTemplateListFrameH(infos []bus.GraphTemplateInfo, width, height, sel int, errMsg string) string {
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Launch Graph", width))
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	if len(infos) == 0 {
		fmt.Fprintf(&b, "  %sNo graph templates found%s\n", Comment, RST)
	}
	start, end := 0, len(infos)
	if height > 0 {
		start, end = scrollWindow(len(infos), height-7, sel)
	}
	if start > 0 {
		fmt.Fprintf(&b, "  %s↑ %d more%s\n", Comment, start, RST)
	}
	for i, t := range infos[start:end] {
		i += start
		cursor := " "
		nameColor := FG
		if i == sel {
			cursor = Yellow + "▸" + RST
			nameColor = Cyan + Bold
		}
		tierColor := Comment
		if t.Source == "project" {
			tierColor = Green
		} else if t.Source == "user" {
			tierColor = Cyan
		}
		line := fmt.Sprintf("  %s %s%-24s%s %s%-8s%s %s%s%s",
			cursor, nameColor, t.Name, RST, tierColor, t.Source, RST, Comment, t.Description, RST)
		b.WriteString(fitWidth(line, width) + "\n")
	}
	if end < len(infos) {
		fmt.Fprintf(&b, "  %s↓ %d more%s\n", Comment, len(infos)-end, RST)
	}
	if errMsg != "" {
		fmt.Fprintf(&b, "\n  %svalidation failed:%s\n", Red+Bold, RST)
		for _, ln := range strings.Split(strings.TrimRight(errMsg, "\n"), "\n") {
			b.WriteString(fitWidth("  "+Red+ln+RST, width) + "\n")
		}
	}
	return b.String()
}

// TypeaheadIndex returns the first index whose name starts with the
// case-insensitive prefix, or -1.
func TypeaheadIndex(names []string, prefix string) int {
	p := strings.ToLower(prefix)
	for i, n := range names {
		if strings.HasPrefix(strings.ToLower(n), p) {
			return i
		}
	}
	return -1
}

// RenderIntentPromptFrame renders the argument prompt shown when a
// template's messages interpolate ${spec} or ${intent}. hint, when set, is
// why the branch derivation did not apply — the prompt then says why it is
// asking instead of presenting an unexplained blank.
//
// isSpec picks the wording. A template whose argument is not a spec was still
// asked for one by name, telling the person launching pr-local-review to type
// a spec id where a PR number goes.
func RenderIntentPromptFrame(template, input, hint string, isSpec bool, width int) string {
	return RenderIntentPromptFrameH(template, input, hint, isSpec, width, 0)
}

// RenderIntentPromptFrameH is RenderIntentPromptFrame with a height
// budget: the hint is elided so the input field always renders.
//
// The caller pads the frame from the bottom, so the field — the last row
// — is the first thing an overlong hint costs, leaving a prompt with no
// visible way to answer it. That is the failure RenderConfirmFrameH
// already exists to prevent (live, 2026-08-27); wrapping the hint to the
// pane width made it reachable here, since a hint that used to be one
// overflowing line is now many. height <= 0 = unbudgeted.
func RenderIntentPromptFrameH(template, input, hint string, isSpec bool, width, height int) string {
	ask, label := "This template needs an argument — type it:", "argument:"
	if isSpec {
		ask, label = "This template needs a spec — describe the work, or type a spec id:", "spec:"
	}
	askRows := promptTextLines(ask, width)
	hintRows := promptTextLines(hint, width)
	if height > 0 {
		hintRows = elideRows(hintRows, intentHintBudget(height, len(askRows)))
	}
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Launch Graph", width))
	fmt.Fprintf(&b, "  %s%sLaunch %s%s\n", Purple, Bold, template, RST)
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)
	for _, line := range append(askRows, hintRows...) {
		fmt.Fprintf(&b, "  %s%s%s\n", Comment, line, RST)
	}
	fmt.Fprintf(&b, "\n  %s%s%s %s%s█%s\n", Comment, label, RST, FG, input, RST)
	return b.String()
}

// intentHintBudget is the rows the intent prompt can spend on its hint:
// the pane height less the caller's three-row margin, the two-row tab
// bar, the title and divider, and the blank and input rows the field
// occupies.
func intentHintBudget(height, askRows int) int {
	return height - 3 - (askRows + 6)
}

// elideRows caps rows to budget, spending the last kept row on a count
// of what was dropped so a truncated list never reads as complete.
func elideRows(rows []string, budget int) []string {
	if budget <= 0 {
		return nil
	}
	if len(rows) <= budget {
		return rows
	}
	out := make([]string, 0, budget)
	out = append(out, rows[:budget-1]...)
	return append(out, fmt.Sprintf("… +%d more", len(rows)-(budget-1)))
}

// promptTextLines wraps one of the frame's prose rows to its two-column
// indent, returning nothing for an empty string so the caller emits no
// line at all.
//
// The wrap is the structural guarantee, not a nicety: the hint is built
// from repo contents, so its length is unbounded. Rendered on one line
// it ran a screen-wide list of spec paths off the pane, taking the
// input field with it. The fixed ask line overflowed a 60-column pane on
// its own, so it is wrapped by the same path rather than trusted to fit.
func promptTextLines(s string, width int) []string {
	if s == "" {
		return nil
	}
	avail := width - 4
	if avail < 8 {
		avail = 8
	}
	return wrapPlain(s, avail)
}

// RenderSpecConfirmFrame renders the branch-derived launch confirm: the
// branch, the spec it names, the intent the run will carry, and what
// confirming does to the active-spec pointer — stated before any key is
// accepted, because a launch that silently re-points the session's spec
// is the surprise this frame exists to prevent. A spec found only under
// completed/ is flagged: the run would verify, not implement.
func RenderSpecConfirmFrame(template string, spec bus.BranchSpec, active bus.ActiveSpecRelation, errMsg string, width int) string {
	return RenderSpecConfirmFrameH(template, spec, active, errMsg, width, 0)
}

// RenderSpecConfirmFrameH is RenderSpecConfirmFrame with a height budget.
//
// The caller pads from the bottom while the footer is appended after the
// padding, so a short pane truncates the pointer consequence, the
// completed/ warning and the error while "Enter/y Yes" stays visible —
// the confirm would invite a keypress with the very thing it exists to
// state cut off. Those rows are therefore never elided: a tight budget
// compacts the context rows above them instead, and drops them from the
// bottom only once compacting is not enough. height <= 0 = unbudgeted.
func RenderSpecConfirmFrameH(template string, spec bus.BranchSpec, active bus.ActiveSpecRelation, errMsg string, width, height int) string {
	context, consequence, _ := specConfirmBody(spec, active, errMsg, width, height)
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Launch Graph", width))
	fmt.Fprintf(&b, "  %s%sLaunch %s%s\n", Purple, Bold, template, RST)
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)
	for _, line := range append(context, consequence...) {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// specConfirmChrome is the rows the confirm frame spends before its body:
// the two-row tab bar, the title and the divider.
const specConfirmChrome = 4

// specConfirmBody fits the confirm's rows to the pane, reporting whether
// the consequence had to be replaced by the too-short notice.
//
// The renderer and the key handler both read that flag, from this one
// function: if they computed it apart they could disagree, and a frame
// showing the consequence while the handler refused the key — or worse,
// the reverse — is the drift this exists to prevent.
func specConfirmBody(spec bus.BranchSpec, active bus.ActiveSpecRelation, errMsg string, width, height int) (context, consequence []string, tooShort bool) {
	consequence = specConfirmConsequence(spec, active, errMsg, width, false)
	context = specConfirmContext(spec, width, false)
	if height <= 0 {
		return context, consequence, false
	}
	body := height - 3 - specConfirmChrome
	if len(consequence)+len(context) > body {
		context = specConfirmContext(spec, width, true)
	}
	if len(consequence)+len(context) > body {
		consequence = specConfirmConsequence(spec, active, errMsg, width, true)
	}
	for len(context) > 0 && len(consequence)+len(context) > body {
		context = context[:len(context)-1]
	}
	if len(consequence) > body {
		return nil, specConfirmTooShort(width, body), true
	}
	return context, consequence, false
}

// SpecConfirmTooShort reports whether the pane is too short to show what
// confirming does. The key handler calls it at the keypress rather than
// trusting the drawn frame: the pane can be resized in between.
func SpecConfirmTooShort(spec bus.BranchSpec, active bus.ActiveSpecRelation, errMsg string, width, height int) bool {
	_, _, tooShort := specConfirmBody(spec, active, errMsg, width, height)
	return tooShort
}

// specConfirmContext renders the rows that orient the reader — the
// question and the branch, spec and derived values. compact fits each
// value onto one line instead of wrapping it, the first thing given up
// when the pane is too short to show everything.
func specConfirmContext(spec bus.BranchSpec, width int, compact bool) []string {
	out := colorLines("Branch "+spec.Branch+" names a spec — work through it?", Comment, width)
	out = append(out, "")
	out = append(out, labeledRowLines("branch:", spec.Branch, FG, width, compact)...)
	out = append(out, labeledRowLines("spec:", spec.Path, FG, width, compact)...)
	return append(out, labeledRowLines("derived:", spec.Intent, FG, width, compact)...)
}

// specConfirmConsequence renders what a person must see before pressing a
// key: what confirming does to the active-spec pointer, the completed/
// flag, and any error. compact holds each to a single line, bounding the
// block so a short pane cannot truncate it away.
func specConfirmConsequence(spec bus.BranchSpec, active bus.ActiveSpecRelation, errMsg string, width int, compact bool) []string {
	text, color := activeSpecChange(active, compact)
	out := labeledRowLines("active:", text, color, width, compact)
	if spec.Dir == "completed" {
		out = append(out, "")
		out = append(out, noticeLines("⚠ spec is under completed/ — the run will verify, not implement", Yellow, width, compact)...)
	}
	if errMsg != "" {
		out = append(out, "")
		out = append(out, noticeLines("✗ "+errMsg, Red, width, compact)...)
	}
	return out
}

// noticeLines renders one notice, wrapped or held to a single fitted
// line when the pane cannot afford the wrapped form.
func noticeLines(text, color string, width int, compact bool) []string {
	if compact {
		return []string{fmt.Sprintf("  %s%s%s", color, fitOneLine(text, width-4), RST)}
	}
	return colorLines(text, color, width)
}

// specConfirmTooShort is the last resort when the pane cannot show even
// the compacted consequence. The footer is drawn outside this frame, so
// "Enter/y Yes" stays on screen regardless — saying plainly that the
// consequence is not visible beats rendering a confirm that looks
// complete and is not.
func specConfirmTooShort(width, body int) []string {
	lines := colorLines("⚠ pane too short to show what confirming does — resize before answering", Yellow, width)
	if body > 0 && len(lines) > body {
		lines = lines[:body]
	}
	return lines
}

// colorLines wraps text to the pane at the frame's two-column indent,
// every line carrying the same colour.
func colorLines(text, color string, width int) []string {
	var out []string
	for _, line := range promptTextLines(text, width) {
		out = append(out, fmt.Sprintf("  %s%s%s", color, line, RST))
	}
	return out
}

// specConfirmLabelCol is the column values start at, after the frame's
// two-space indent: the widest label plus a trailing space.
const specConfirmLabelCol = 9

// labeledRowLines renders one "label:  value" row, wrapping the value to
// the pane with continuation lines aligned under the value column.
//
// Every value here is unbounded — a branch name, two repo paths and a
// derived intent — so an unwrapped row runs off the frame on any pane
// narrower than the longest of them. compact fits the value onto a
// single line instead, trading the tail for height.
func labeledRowLines(label, value, color string, width int, compact bool) []string {
	avail := width - 2 - specConfirmLabelCol
	if avail < 8 {
		avail = 8
	}
	rows := wrapPlain(value, avail)
	if compact {
		rows = []string{fitOneLine(value, avail)}
	}
	out := make([]string, 0, len(rows))
	for i, line := range rows {
		lead := Comment + Pad(label, specConfirmLabelCol) + RST
		if i > 0 {
			lead = strings.Repeat(" ", specConfirmLabelCol)
		}
		out = append(out, fmt.Sprintf("  %s%s%s%s", lead, color, line, RST))
	}
	return out
}

// fitOneLine truncates s to w columns, marking the cut so a shortened
// value is never read as the whole one.
func fitOneLine(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w < 2 {
		return s
	}
	return string(r[:w-1]) + "…"
}

// plainActiveSpecChange is the pointer consequence without color, for the
// intent editor's hint line when the editor is entered from the confirm.
func plainActiveSpecChange(active bus.ActiveSpecRelation, path string) string {
	switch {
	case active.Current == "":
		return "launch sets the active spec to " + path
	case active.Matches:
		return "active spec unchanged: " + path
	}
	return "launch switches the active spec from " + active.Current + " to " + path
}

// activeSpecChange words the pointer consequence of confirming, as plain
// text plus the colour carrying its urgency.
//
// The text is returned uncoloured because the caller wraps it, and
// wrapPlain measures runes — an escape sequence is runes, so a
// pre-coloured string wraps at the wrong column. The switch case is
// coloured whole rather than emphasising one word mid-string, for the
// same reason.
func activeSpecChange(active bus.ActiveSpecRelation, compact bool) (string, string) {
	switch {
	case active.Current == "":
		return "(unset) → confirming sets it to this spec", Comment
	case active.Matches:
		return "matches — unchanged", Green
	case compact:
		return "switches from " + filepath.Base(active.Current) + " to this spec", Yellow
	}
	return active.Current + " → confirming switches it to this spec", Yellow
}

// TemplateNeedsIntent reports whether any node message or action of a
// graph interpolates ${spec} (or its former name ${intent}) — those
// templates resolve it in the launcher instead of failing after launch.
func TemplateNeedsIntent(g *bus.Graph) bool {
	return templateInterpolates(g, "${spec}", "${intent}")
}

// TemplateIntentIsSpec reports whether the argument a template needs is a
// requirements spec, which is what makes deriving it from the active spec
// pointer correct rather than merely convenient.
//
// ${spec} names a spec. ${intent} is the former name for the same slot and
// promises nothing about its content — pr-local-review deliberately uses it
// for a PR number. Deriving on TemplateNeedsIntent instead, which only asks
// whether an argument is wanted at all, handed that template the active spec
// id and ran `gh pr checkout <spec-id>`.
//
// A template using only the legacy name is asked for rather than derived. That
// costs a prompt for any older template that did mean a spec, and refusing to
// guess beats driving the wrong work — the reasoning requireIntent already
// applies to an unresolved argument.
func TemplateIntentIsSpec(g *bus.Graph) bool {
	return templateInterpolates(g, "${spec}")
}

func templateInterpolates(g *bus.Graph, placeholders ...string) bool {
	for i := range g.Nodes {
		for _, ph := range placeholders {
			if strings.Contains(g.Nodes[i].Message, ph) || strings.Contains(g.Nodes[i].Action, ph) {
				return true
			}
		}
	}
	return false
}

// ── Gate queue ─────────────────────────────────────────────

// GateImpact is one node an approval would release, with enough context
// for the human to know what they are unblocking.
type GateImpact struct {
	NodeID   string
	Type     string
	Role     string
	Action   string
	Mutating bool // fires a git mutation or Atlassian write
}

// PendingGate is one node awaiting a person, across all in-flight runs:
// a waiting wait_human gate, or a node parked by the unverified hold.
type PendingGate struct {
	RunID      string
	NodeID     string
	Prompt     string // the node's optional approval prompt
	Waiting    time.Duration
	Downstream []GateImpact
	Mutating   bool // any downstream node is a commit/Atlassian mutation
	Unverified bool // parked by the unverified hold, not a declared gate
}

// GateDownstream computes what approving a gate releases: every node
// reachable from it in the frozen run definition, BFS order, loop edges
// included. The second return reports whether any released node fires a
// git/Atlassian mutation — the authority boundary the gate exists for.
// Computed from the run's frozen graph, never the template file, so the
// queue reports what the run will do.
func GateDownstream(g *bus.Graph, gateID string) ([]GateImpact, bool) {
	byID := make(map[string]*bus.Node, len(g.Nodes))
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}

	seen := map[string]bool{gateID: true}
	queue := []string{gateID}
	var impacts []GateImpact
	mutating := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.Edges {
			if e.From != cur || seen[e.To] {
				continue
			}
			seen[e.To] = true
			queue = append(queue, e.To)
			n := byID[e.To]
			if n == nil {
				continue
			}
			imp := GateImpact{NodeID: n.ID, Type: n.Type, Role: n.Role, Action: n.Action,
				Mutating: bus.NodeRequiresGate(n)}
			if imp.Mutating {
				mutating = true
			}
			impacts = append(impacts, imp)
		}
	}
	return impacts, mutating
}

// GatesRearmedByRetry returns the wait_human node ids at or downstream of
// fromNode. Retrying from there re-arms them: MUX-014 purges the approval
// marker at dispatch, so each demands a fresh approval — the confirm
// prompt must say so.
func GatesRearmedByRetry(g *bus.Graph, fromNode string) []string {
	downstream, _ := GateDownstream(g, fromNode)
	var gates []string
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != bus.NodeWaitHuman {
			continue
		}
		if n.ID == fromNode {
			gates = append(gates, n.ID)
			continue
		}
		for _, imp := range downstream {
			if imp.NodeID == n.ID {
				gates = append(gates, n.ID)
				break
			}
		}
	}
	return gates
}

// ResolvedGate is a wait_human node that already reached a terminal
// state — the queue's history section, so past approvals stay visible.
type ResolvedGate struct {
	RunID  string
	NodeID string
	State  string // done (approved) or skipped (run canceled / branch not taken)
	Age    time.Duration
}

// RenderGateQueueFrame renders the cross-run pending-gate queue. The
// selected gate expands to show what its approval releases; gates whose
// downstream mutates git or Atlassian are flagged in the list itself.
// Resolved gates render as a dimmed history section. Empty state is
// explicit — never a blank frame.
func RenderGateQueueFrame(gates []PendingGate, resolved []ResolvedGate, width, sel int) string {
	return RenderGateQueueFrameH(gates, resolved, width, 0, sel, 0)
}

// RenderGateQueueFrameH is RenderGateQueueFrame with a height budget:
// the pending section keeps priority, and the resolved history scrolls
// in the rows that remain (histScroll, ↑/↓ overflow indicators). height
// <= 0 renders everything.
func RenderGateQueueFrameH(gates []PendingGate, resolved []ResolvedGate, width, height, sel, histScroll int) string {
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Pending Gates", width))
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	if len(gates) == 0 {
		fmt.Fprintf(&b, "  %sNo approvals waiting.%s\n", Comment, RST)
		fmt.Fprintf(&b, "  %sA row appears here when an in-flight run reaches a wait_human node, or%s\n", Comment, RST)
		fmt.Fprintf(&b, "  %sparks one nothing proved succeeded — both need you before it continues.%s\n", Comment, RST)
		b.WriteString(renderResolvedGatesH(resolved, width, historyBudget(height, &b), histScroll))
		return b.String()
	}

	for i, gate := range gates {
		cursor := " "
		nameColor := FG
		if i == sel {
			cursor = Yellow + "▸" + RST
			nameColor = Cyan + Bold
		}
		flag := ""
		if gate.Mutating {
			flag = "  " + Red + Bold + "⚠ mutates" + RST
		}
		// Glyph, not colour, separates a declared gate from an unverified hold.
		glyph := "⚑"
		if gate.Unverified {
			glyph = "?"
			flag = "  " + Comment + "unverified" + RST + flag
		}
		line := fmt.Sprintf("  %s %s%s %-16s%s %swaiting %-9s%s %s%s%s%s",
			cursor, nameColor, glyph, gate.NodeID, RST,
			Yellow, gate.Waiting.String(), RST,
			Comment, gate.RunID, RST, flag)
		b.WriteString(fitWidth(line, width) + "\n")

		if i != sel {
			continue
		}
		if gate.Prompt != "" {
			fmt.Fprintf(&b, "      %s%s%s\n", Comment, gate.Prompt, RST)
		}
		fmt.Fprintf(&b, "      %sapproval releases:%s\n", Comment, RST)
		for _, imp := range gate.Downstream {
			b.WriteString("        " + formatGateImpact(imp) + "\n")
		}
	}
	b.WriteString(renderResolvedGatesH(resolved, width, historyBudget(height, &b), histScroll))
	return b.String()
}

// historyBudget returns the rows left for the gate history after the
// pending section already in b. The body budget is height-3 (divider,
// footer, clamp margin); height <= 0 means unbudgeted.
func historyBudget(height int, b *strings.Builder) int {
	if height <= 0 {
		return 0
	}
	avail := height - 3 - strings.Count(b.String(), "\n")
	if avail < 0 {
		avail = 0
	}
	return avail
}

// renderResolvedGatesH renders the dimmed gate-history section within
// avail rows, windowed at scroll with ↑/↓ overflow indicators. avail
// <= 0 renders every row.
func renderResolvedGatesH(resolved []ResolvedGate, width, avail, scroll int) string {
	total := len(resolved)
	if total == 0 {
		return ""
	}
	visible := total
	if avail > 0 {
		rows := avail - 2 // blank + "recent gates" header
		if rows < 3 {
			rows = 3
		}
		if total > rows {
			visible = rows - 2 // ↑/↓ indicator rows
			if visible < 1 {
				visible = 1
			}
		}
	}
	maxScroll := total - visible
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %srecent gates%s\n", Cyan, RST)
	if scroll > 0 {
		fmt.Fprintf(&b, "  %s↑ %d more%s\n", Comment, scroll, RST)
	}
	for _, g := range resolved[scroll : scroll+visible] {
		verdict := "✓ approved"
		if g.State == bus.GraphNodeSkipped {
			verdict = "○ skipped"
		}
		line := fmt.Sprintf("  %s%-11s %-16s %-9s %s%s", Comment, verdict, g.NodeID, g.Age.String(), g.RunID, RST)
		b.WriteString(fitWidth(line, width) + "\n")
	}
	if rest := total - scroll - visible; rest > 0 {
		fmt.Fprintf(&b, "  %s↓ %d more%s\n", Comment, rest, RST)
	}
	return b.String()
}

// formatGateImpact renders one released node for the queue and the
// confirm prompt — mutating nodes carry the warning inline.
func formatGateImpact(imp GateImpact) string {
	detail := imp.Type
	if imp.Role != "" {
		detail += " → " + imp.Role
		if imp.Action != "" {
			detail += ":" + imp.Action
		}
	}
	line := fmt.Sprintf("%s%-16s%s %s%s%s", FG, imp.NodeID, RST, Comment, detail, RST)
	if imp.Mutating {
		line += "  " + Red + Bold + "⚠ mutation" + RST
	}
	return line
}

// ── Action confirm ─────────────────────────────────────────

// GraphAction is a destructive action awaiting its confirm keypress.
type GraphAction struct {
	Kind     string // "approve", "cancel", or "retry"
	RunID    string
	NodeID   string       // gate for approve, from-node for retry
	Releases []GateImpact // approve: what the gate releases
	Mutating bool         // approve: a released node mutates git/Atlassian
	Rearms   []string     // retry: gates that will demand fresh approval
}

// RenderConfirmFrame renders the confirm prompt for a pending action.
// Approval of a gate releasing a commit/Atlassian node says so here —
// this prompt is the consent the gate exists to collect.
func RenderConfirmFrame(act GraphAction, width int, errMsg string) string {
	return RenderConfirmFrameH(act, width, 0, errMsg)
}

// RenderConfirmFrameH is RenderConfirmFrame with a height budget: the
// downstream-impact list is what gets truncated ("… +N more"), NEVER the
// y/n confirm line — in a short control pane the full list pushed the
// confirm keys past the clamp and the user faced a question with no
// visible way to answer it (live, 2026-08-27). height <= 0 = unbudgeted.
func RenderConfirmFrameH(act GraphAction, width, height int, errMsg string) string {
	releases := act.Releases
	// Rows outside the list: tabs(2) + blanks/question/header(4) +
	// warning(2) + err(2) + confirm(2) + outer rule+footer(3).
	if height > 0 && len(releases) > 0 {
		budget := height - 15
		if budget < 1 {
			budget = 1
		}
		if len(releases) > budget {
			keep := budget - 1 // the "… +N more" line spends one row
			if keep < 0 {
				keep = 0
			}
			releases = releases[:keep]
		}
	}
	var b strings.Builder
	b.WriteString("\n")
	switch act.Kind {
	case "approve":
		fmt.Fprintf(&b, "  %s%sApprove gate %s%s on run %s?\n", Yellow, Bold, act.NodeID, RST, act.RunID)
		if len(releases) > 0 {
			fmt.Fprintf(&b, "\n  %sapproval releases:%s\n", Comment, RST)
			for _, imp := range releases {
				b.WriteString("    " + formatGateImpact(imp) + "\n")
			}
			if hidden := len(act.Releases) - len(releases); hidden > 0 {
				fmt.Fprintf(&b, "    %s… +%d more%s\n", Comment, hidden, RST)
			}
		}
		if act.Mutating {
			fmt.Fprintf(&b, "\n  %s⚠ this approval releases a git/Atlassian mutation%s\n", Red+Bold, RST)
		}
	case "cancel":
		fmt.Fprintf(&b, "  %s%sCancel run %s?%s\n", Yellow, Bold, act.RunID, RST)
		fmt.Fprintf(&b, "  %sUnstarted nodes will be skipped; running nodes finish.%s\n", Comment, RST)
	case "retry":
		fmt.Fprintf(&b, "  %s%sRetry run %s from node %s?%s\n", Yellow, Bold, act.RunID, act.NodeID, RST)
		fmt.Fprintf(&b, "  %sDownstream results are reset; upstream results are kept.%s\n", Comment, RST)
		for _, gate := range act.Rearms {
			fmt.Fprintf(&b, "  %s⚠ re-arms gate %s — a fresh approval will be demanded%s\n", Red+Bold, gate, RST)
		}
	}
	if errMsg != "" {
		fmt.Fprintf(&b, "\n  %s%s%s\n", Red, errMsg, RST)
	}
	fmt.Fprintf(&b, "\n  %sy%s Confirm  %sn/Esc%s Cancel\n", Yellow, RST, Yellow, RST)
	return b.String()
}

// ── Node detail ────────────────────────────────────────────

// RenderNodeDetailFrame renders one node's full status: definition fields,
// state, timestamps, outcome, correlated task ids, worktree for workers,
// and an output preview. Pure — a function of the snapshot alone.
func RenderNodeDetailFrame(snap GraphSnapshot, nodeID string, width int) string {
	var node *bus.Node
	for i := range snap.Graph.Nodes {
		if snap.Graph.Nodes[i].ID == nodeID {
			node = &snap.Graph.Nodes[i]
		}
	}
	if node == nil {
		return fmt.Sprintf("  %sunknown node %q%s\n", Red, nodeID, RST)
	}
	st := snap.Statuses[nodeID]
	state := snap.nodeState(nodeID)
	glyph, color := nodeGlyph(node.Type, state, snap.nodeOutcome(nodeID), snap.isHeld(nodeID))

	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Graph Runs", width))
	fmt.Fprintf(&b, "  %s%s%s %s%s  %s%s%s\n", color, glyph, RST, Bold+node.ID, RST, color, state, RST)
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	row := func(label, value string) {
		if value == "" {
			return
		}
		b.WriteString(fitWidth(fmt.Sprintf("  %s%-10s%s %s", Comment, label, RST, value), width) + "\n")
	}

	row("type", node.Type)
	row("role", node.Role)
	row("action", node.Action)
	row("input", node.Message)
	if node.Type == bus.NodeJoin {
		row("join", node.Join)
	}
	if st != nil {
		if st.StartedAt > 0 {
			row("started", time.Unix(st.StartedAt, 0).Format("15:04:05"))
		}
		if st.DoneAt > 0 {
			row("done", time.Unix(st.DoneAt, 0).Format("15:04:05"))
			if st.StartedAt > 0 {
				row("took", fmt.Sprintf("%ds", st.DoneAt-st.StartedAt))
			}
		}
		row("outcome", st.Outcome)
		row("task", st.TaskID)
		if wt, ok := snap.Worktrees[st.TaskID]; ok {
			row("worktree", wt)
		}
		if st.Output != "" {
			b.WriteString(fmt.Sprintf("  %soutput%s\n", Comment, RST))
			lines := strings.Split(strings.TrimRight(st.Output, "\n"), "\n")
			const outputPreviewLines = 10
			for i, ln := range lines {
				if i >= outputPreviewLines {
					fmt.Fprintf(&b, "    %s… %d more lines%s\n", Comment, len(lines)-outputPreviewLines, RST)
					break
				}
				b.WriteString(fitWidth("    "+ln, width) + "\n")
			}
		}
	}
	return b.String()
}

// renderGraphFallback renders the flat node list used when the grid is
// wider than the pane: one row per node, failed/waiting/held first.
//
// budget is the rows available below the header; <= 0 means unbudgeted. The
// list is truncated from the bottom because it is sorted by what needs eyes,
// so an overflow drops the least urgent rows and never the blocker.
func renderGraphFallback(snap GraphSnapshot, width, budget int, selection string, scroll int) string {
	rows := make([]fallbackRow, 0, len(snap.Graph.Nodes))
	for i := range snap.Graph.Nodes {
		n := &snap.Graph.Nodes[i]
		rows = append(rows, fallbackRow{id: n.ID, typ: n.Type, state: snap.nodeState(n.ID),
			outcome: snap.nodeOutcome(n.ID), held: snap.isHeld(n.ID), defIdx: i})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		oi, oj := fallbackRank(rows[i].state, rows[i].held), fallbackRank(rows[j].state, rows[j].held)
		if oi != oj {
			return oi < oj
		}
		return rows[i].defIdx < rows[j].defIdx
	})

	window, above, below := rows, 0, 0
	if budget > 0 {
		window, above, below = flatWindow(rows, budget-1, scroll, selection)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %s(graph wider than pane — flat view)%s\n", Comment, RST)
	if above > 0 {
		fmt.Fprintf(&b, "  %s↑ +%d more%s\n", Comment, above, RST)
	}
	for _, r := range window {
		glyph, color := nodeGlyph(r.typ, r.state, r.outcome, r.held)
		state := r.state
		if r.held {
			state = "held"
		}
		cursor, name := " ", color
		if r.id == selection {
			cursor, name = Yellow+"▸"+RST, Cyan+Bold
		}
		line := fmt.Sprintf("%s %s%s%s %s%-24s%s %s%-10s %s%s", cursor, color, glyph, RST,
			name, r.id, RST, Comment, state, r.typ, RST)
		if st := snap.Statuses[r.id]; st != nil && st.Outcome != "" {
			line += fmt.Sprintf("  %soutcome=%s%s", Comment, st.Outcome, RST)
		}
		b.WriteString(fitWidth(line, width) + "\n")
	}
	if below > 0 {
		fmt.Fprintf(&b, "  %s↓ +%d more%s\n", Comment, below, RST)
	}
	return b.String()
}

// flatWindow is the slice of rows visible at a scroll offset, with the
// counts hidden above and below; each notice costs a row of the budget.
//
// The list used to keep the top rows by rank and drop the rest, so
// completed nodes fell off with no way to reach them — scrolling was
// wired to the detail panel only. A selection outside the window pulls
// it back into view, so moving the cursor still follows it.
func flatWindow(rows []fallbackRow, capacity, scroll int, selection string) (window []fallbackRow, above, below int) {
	if capacity < 1 || len(rows) == 0 {
		return nil, 0, len(rows)
	}
	for avail := capacity; avail >= 1; avail-- {
		start, end := windowBounds(rows, avail, scroll, selection)
		a, b := start, len(rows)-end
		need := avail
		if a > 0 {
			need++
		}
		if b > 0 {
			need++
		}
		if need <= capacity {
			return rows[start:end], a, b
		}
		if avail == 1 {
			// One line left and notices will not fit beside it: the row the
			// cursor is on beats a count of rows you cannot see.
			return rows[start:end], 0, 0
		}
	}
	return nil, 0, len(rows)
}

// windowBounds is the row range shown for a given size, pulled to keep the
// selection inside it and clamped to the ends of the list.
func windowBounds(rows []fallbackRow, avail, scroll int, selection string) (start, end int) {
	start = clamp(scroll, 0, len(rows)-1)
	if sel := rowIndexOf(rows, selection); sel >= 0 {
		if sel < start {
			start = sel
		} else if sel >= start+avail {
			start = sel - avail + 1
		}
	}
	end = start + avail
	if end > len(rows) {
		end = len(rows)
		start = max(0, end-avail)
	}
	return start, end
}

// rowIndexOf is the position of id in rows, or -1.
func rowIndexOf(rows []fallbackRow, id string) int {
	for i := range rows {
		if rows[i].id == id {
			return i
		}
	}
	return -1
}
