package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// GraphSnapshot bundles one read of a run's store: metadata, the frozen
// graph definition, and per-node statuses. Layout and rendering are pure
// functions of a snapshot, so every frame is unit-testable without a
// terminal and `--render-once` is exactly one snapshot → one frame.
type GraphSnapshot struct {
	Run      *bus.GraphRun
	Graph    *bus.Graph
	Statuses map[string]*bus.GraphNodeStatus
	// Worktrees maps a spawn id to its worktree path, so worker-node detail
	// can show where the work happened. Optional enrichment — loaders fill
	// it, fixtures may leave it nil.
	Worktrees map[string]string
}

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
func nodeGlyph(nodeType, state string) (glyph string, color string) {
	if nodeType == bus.NodeWaitHuman {
		if state == bus.GraphNodeWaiting {
			return "⚑", Yellow + Bold
		}
		return "⚑", stateColor(state)
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
	grid := LayoutGraph(snap.Graph)

	labels := make(map[string]string, len(snap.Graph.Nodes))
	types := make(map[string]string, len(snap.Graph.Nodes))
	for i := range snap.Graph.Nodes {
		n := &snap.Graph.Nodes[i]
		types[n.ID] = n.Type
		glyph, _ := nodeGlyph(n.Type, snap.nodeState(n.ID))
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

	// Wider or deeper than the pane degrades to the flat list — the grid
	// has no scroll, so an overflowing canvas would render clipped.
	headerLines := 5 // leading blank, tab bar, run line, trailing blank, + margin
	if snap.Run.Intent != "" {
		headerLines++
	}
	if gridW > width || gridH+skipLanes+headerLines > height {
		return renderGraphHeader(snap, now, width) + renderGraphFallback(snap, width)
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
			_, color := nodeGlyph(types[id], snap.nodeState(id))
			if id == selection {
				color = Cyan + Bold
			}
			writeCursorAndLabel(c, colX[i], nodeY(id), id == selection, labels[id], color)
		}
	}

	frame := renderGraphHeader(snap, now, width) + c.String()
	// The per-node detail panel renders only when it fits under the grid —
	// the grid is the load-bearing view and must never be pushed past the
	// pane clamp for the sake of detail rows.
	if gridH+skipLanes+headerLines+len(snap.Graph.Nodes)+1 <= height {
		frame += RenderNodeDetails(snap, width, now)
	}
	return frame
}

// RenderNodeDetails lists every node with who runs it, its timing, and
// what it is doing — the run view's answer to "what is actually
// happening": a running node shows the instruction it dispatched, a
// finished node the first line of its harvested result, a waiting gate
// its approval command.
func RenderNodeDetails(snap GraphSnapshot, width int, now time.Time) string {
	var b strings.Builder
	b.WriteString("\n")
	for i := range snap.Graph.Nodes {
		n := &snap.Graph.Nodes[i]
		st := snap.Statuses[n.ID]
		state := snap.nodeState(n.ID)
		glyph, color := nodeGlyph(n.Type, state)

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
		msg := strings.ReplaceAll(n.Message, "${intent}", snap.Run.Intent)
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
			if state == bus.GraphNodeFailed {
				detailColor = Red
			}
		}

		line := fmt.Sprintf("  %s%s %-10s%s %s%-20s%s %-8s %s%s%s",
			color, glyph, n.ID, RST, Comment, who, RST, timing,
			detailColor, detail, RST)
		b.WriteString(fitWidth(line, width) + "\n")
	}
	return b.String()
}

// writeCursorAndLabel places the selection cursor and the node label.
func writeCursorAndLabel(c *canvas, x, y int, selected bool, label, color string) {
	if selected {
		c.writeText(x, y, "▸", Yellow)
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
// node chain. Success cells are built from node IDS, never node output —
// harvested output is multi-line agent prose whose first line lands
// mid-sentence, and four such fragments stacked in the run list read as
// one message flowing across rows (user's catch, 2026-08-27). A done id
// may carry a trailing "?" — that node's completion was INFERRED (its
// non-hook provider gave no exit-code proof), and the cell explains the
// mark inline instead of a bare count nobody could interpret (user's
// second catch, same day). The glyph prefix carries identity — the
// renderer colors by it, and the cell stays readable through StripAnsi.
func SummarizeRunResults(runState string, failed []string, failedOut string, done []string) string {
	firstLine := func(s string) string {
		s = strings.TrimSpace(s)
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
		return s
	}
	chain := strings.Join(done, " → ")
	inferred := false
	for _, d := range done {
		if strings.HasSuffix(d, "?") {
			inferred = true
			break
		}
	}
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
		msg := "✓ complete"
		if chain != "" {
			msg = "✓ " + chain
		}
		if inferred {
			msg += "  (? = completion inferred, no exit-code proof)"
		}
		return msg
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

// RenderRunListFrame renders the run browser: all runs newest first, with
// state, node progress, elapsed, and a gate badge where a wait_human node
// waits. Empty state renders explicitly — never a blank frame.
func RenderRunListFrame(rows []RunListRow, width, sel int) string {
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Graph Runs", width))
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	if len(rows) == 0 {
		fmt.Fprintf(&b, "  %sNo graph runs yet in this session.%s\n", Comment, RST)
		fmt.Fprintf(&b, "  %sStart one: muxcode graph run <template> [intent…] — or Tab to the launcher.%s\n", Comment, RST)
		fmt.Fprintf(&b, "  %sTemplates: muxcode graph list%s\n", Comment, RST)
		return b.String()
	}

	fmt.Fprintf(&b, "  %s   %-40s %-10s %-9s %-9s %-28s %s%s\n",
		Comment, "RUN", "STATE", "PROGRESS", "ELAPSED", "TEMPLATE", "RESULTS", RST)
	for i, r := range rows {
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
		results := r.Results
		if len([]rune(results)) > 90 {
			results = string([]rune(results)[:89]) + "…"
		}
		line := fmt.Sprintf("  %s %s%-40s%s %s%-10s%s %d/%-7d %-9s %s%-28s%s %s%s%s%s",
			cursor, idColor, r.ID, RST,
			stateColor, r.State, RST,
			r.Done, r.Total, r.Elapsed.String(),
			Comment, r.Template, RST,
			resultsCellColor(results), results, RST, badge)
		b.WriteString(fitWidth(line, width) + "\n")
	}
	return b.String()
}

// ── Template launcher ──────────────────────────────────────

// RenderTemplateListFrame renders the launcher's template picker: every
// resolvable template with its source tier and description. A validation
// failure renders in place under the list — the launch is refused, the
// picker stays.
func RenderTemplateListFrame(infos []bus.GraphTemplateInfo, width, sel int, errMsg string) string {
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Launch Graph", width))
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	if len(infos) == 0 {
		fmt.Fprintf(&b, "  %sNo graph templates found%s\n", Comment, RST)
	}
	for i, t := range infos {
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
	if errMsg != "" {
		fmt.Fprintf(&b, "\n  %svalidation failed:%s\n", Red+Bold, RST)
		for _, ln := range strings.Split(strings.TrimRight(errMsg, "\n"), "\n") {
			b.WriteString(fitWidth("  "+Red+ln+RST, width) + "\n")
		}
	}
	return b.String()
}

// RenderIntentPromptFrame renders the argument prompt shown when a
// template's messages interpolate ${intent}.
func RenderIntentPromptFrame(template, input string, width int) string {
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Launch Graph", width))
	fmt.Fprintf(&b, "  %s%sLaunch %s%s\n", Purple, Bold, template, RST)
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)
	fmt.Fprintf(&b, "  %sThis template interpolates ${intent} — describe the work:%s\n\n", Comment, RST)
	fmt.Fprintf(&b, "  %sintent:%s %s%s█%s\n", Comment, RST, FG, input, RST)
	return b.String()
}

// TemplateNeedsIntent reports whether any node message or action of a
// graph interpolates ${intent} — those templates prompt for it in the
// launcher instead of failing after launch.
func TemplateNeedsIntent(g *bus.Graph) bool {
	for i := range g.Nodes {
		if strings.Contains(g.Nodes[i].Message, "${intent}") || strings.Contains(g.Nodes[i].Action, "${intent}") {
			return true
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

// PendingGate is one waiting wait_human node, across all in-flight runs.
type PendingGate struct {
	RunID      string
	NodeID     string
	Prompt     string // the node's optional approval prompt
	Waiting    time.Duration
	Downstream []GateImpact
	Mutating   bool // any downstream node is a commit/Atlassian mutation
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
	var b strings.Builder
	b.WriteString(renderSurfaceTabs("Pending Gates", width))
	fmt.Fprintf(&b, "%s%s%s\n", Comment, HLine('─', width), RST)

	if len(gates) == 0 {
		fmt.Fprintf(&b, "  %sNo gates waiting.%s\n", Comment, RST)
		fmt.Fprintf(&b, "  %sA gate appears here when an in-flight run reaches a wait_human node%s\n", Comment, RST)
		fmt.Fprintf(&b, "  %sand needs your approval before firing what is behind it.%s\n", Comment, RST)
		b.WriteString(renderResolvedGates(resolved, width))
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
		line := fmt.Sprintf("  %s %s⚑ %-16s%s %swaiting %-9s%s %s%s%s%s",
			cursor, nameColor, gate.NodeID, RST,
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
	b.WriteString(renderResolvedGates(resolved, width))
	return b.String()
}

// renderResolvedGates renders the dimmed gate-history section.
func renderResolvedGates(resolved []ResolvedGate, width int) string {
	if len(resolved) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %srecent gates%s\n", Cyan, RST)
	for _, g := range resolved {
		verdict := "✓ approved"
		if g.State == bus.GraphNodeSkipped {
			verdict = "○ skipped"
		}
		line := fmt.Sprintf("  %s%-11s %-16s %-9s %s%s", Comment, verdict, g.NodeID, g.Age.String(), g.RunID, RST)
		b.WriteString(fitWidth(line, width) + "\n")
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
	glyph, color := nodeGlyph(node.Type, state)

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
// wider than the pane: one row per node, failed/waiting first.
func renderGraphFallback(snap GraphSnapshot, width int) string {
	type row struct {
		id, typ, state string
		defIdx         int
	}
	rows := make([]row, 0, len(snap.Graph.Nodes))
	for i := range snap.Graph.Nodes {
		n := &snap.Graph.Nodes[i]
		rows = append(rows, row{id: n.ID, typ: n.Type, state: snap.nodeState(n.ID), defIdx: i})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		oi, oj := fallbackStateOrder[rows[i].state], fallbackStateOrder[rows[j].state]
		if oi != oj {
			return oi < oj
		}
		return rows[i].defIdx < rows[j].defIdx
	})

	var b strings.Builder
	fmt.Fprintf(&b, "  %s(graph wider than pane — flat view)%s\n", Comment, RST)
	for _, r := range rows {
		glyph, color := nodeGlyph(r.typ, r.state)
		line := fmt.Sprintf("  %s%s %-24s%s %s%-10s %s%s", color, glyph, r.id, RST, Comment, r.state, r.typ, RST)
		if st := snap.Statuses[r.id]; st != nil && st.Outcome != "" {
			line += fmt.Sprintf("  %soutcome=%s%s", Comment, st.Outcome, RST)
		}
		b.WriteString(fitWidth(line, width) + "\n")
	}
	return b.String()
}
