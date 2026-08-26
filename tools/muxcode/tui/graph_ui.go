package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// graphView tracks which screen is active in the Graph TUI.
type graphView int

const (
	viewGraphRuns graphView = iota
	viewGraphDAG
	viewGraphNode
	viewGraphTemplates
	viewGraphIntent
	viewGraphGates
	viewGraphConfirm
)

// graphTickInterval is the store re-read cadence — the spec requires a
// node transition to reflect within 2 seconds.
const graphTickInterval = 2 * time.Second

// LoadGraphSnapshot reads one run's store: metadata, frozen definition,
// node statuses, and the spawn-worktree enrichment for worker nodes.
func LoadGraphSnapshot(session, runID string) (GraphSnapshot, error) {
	run, err := bus.ReadGraphRun(session, runID)
	if err != nil {
		return GraphSnapshot{}, fmt.Errorf("unknown run %q: %w", runID, err)
	}
	g, err := bus.ReadGraphRunGraph(session, runID)
	if err != nil {
		return GraphSnapshot{}, err
	}
	statuses, err := bus.ReadAllNodeStatuses(session, runID)
	if err != nil {
		return GraphSnapshot{}, err
	}
	snap := GraphSnapshot{Run: run, Graph: g, Statuses: statuses}
	if entries, err := bus.ReadSpawnEntries(session); err == nil {
		for _, e := range entries {
			if e.Worktree == "" {
				continue
			}
			if snap.Worktrees == nil {
				snap.Worktrees = make(map[string]string)
			}
			snap.Worktrees[e.ID] = e.Worktree
		}
	}
	return snap, nil
}

// LoadRunListRows summarizes every run in the store, newest first —
// completed runs included, so they open as post-mortem views.
func LoadRunListRows(session string, now time.Time) []RunListRow {
	runs, err := bus.ListGraphRuns(session)
	if err != nil {
		return nil
	}
	rows := make([]RunListRow, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- { // ListGraphRuns is oldest first
		r := runs[i]
		g, err := bus.ReadGraphRunGraph(session, r.ID)
		if err != nil {
			continue
		}
		statuses, _ := bus.ReadAllNodeStatuses(session, r.ID)
		row := RunListRow{ID: r.ID, Template: r.Template, State: r.State, Total: len(g.Nodes)}
		for j := range g.Nodes {
			n := &g.Nodes[j]
			state := bus.GraphNodePending
			if st := statuses[n.ID]; st != nil {
				state = st.State
			}
			if state == bus.GraphNodeDone {
				row.Done++
			}
			if n.Type == bus.NodeWaitHuman && state == bus.GraphNodeWaiting {
				row.GateWaiting = true
			}
		}
		end := now.Unix()
		if r.State != bus.GraphRunRunning {
			end = r.UpdatedAt
		}
		if end > r.CreatedAt {
			row.Elapsed = time.Duration(end-r.CreatedAt) * time.Second
		}
		rows = append(rows, row)
	}
	return rows
}

// GraphRenderOnce renders a single frame to a string and returns — the
// scriptable seam for integration tests. No run id renders the run list;
// width <= 0 falls back to the terminal width.
func GraphRenderOnce(session, runID string, width int) (string, error) {
	if width <= 0 {
		width = termWidth()
	}
	if runID == "" {
		return RenderRunListFrame(LoadRunListRows(session, time.Now()), width, -1), nil
	}
	snap, err := LoadGraphSnapshot(session, runID)
	if err != nil {
		return "", err
	}
	return RenderGraphFrame(snap, width, termHeight(), "", time.Now()), nil
}

// LoadPendingGates scans every in-flight run for wait_human nodes in the
// waiting state — the cross-run approval queue. Downstream impact comes
// from each run's frozen definition, never the template file. Newest
// gates (shortest wait) list first.
func LoadPendingGates(session string, now time.Time) []PendingGate {
	var gates []PendingGate
	for _, run := range bus.ScanInFlightGraphRuns(session) {
		g, err := bus.ReadGraphRunGraph(session, run.ID)
		if err != nil {
			continue
		}
		statuses, err := bus.ReadAllNodeStatuses(session, run.ID)
		if err != nil {
			continue
		}
		for i := range g.Nodes {
			n := &g.Nodes[i]
			if n.Type != bus.NodeWaitHuman {
				continue
			}
			st := statuses[n.ID]
			if st == nil || st.State != bus.GraphNodeWaiting {
				continue
			}
			downstream, mutating := GateDownstream(g, n.ID)
			var waiting time.Duration
			if st.UpdatedAt > 0 && now.Unix() > st.UpdatedAt {
				waiting = time.Duration(now.Unix()-st.UpdatedAt) * time.Second
			}
			gates = append(gates, PendingGate{
				RunID: run.ID, NodeID: n.ID, Prompt: n.Message,
				Waiting: waiting, Downstream: downstream, Mutating: mutating,
			})
		}
	}
	sort.SliceStable(gates, func(i, j int) bool { return gates[i].Waiting < gates[j].Waiting })
	return gates
}

// GateQueueRenderOnce renders the pending-gate queue as a single frame —
// the scriptable seam for integration tests. The first gate is selected
// so its downstream impact is visible in the frame.
func GateQueueRenderOnce(session string, width int) (string, error) {
	if width <= 0 {
		width = termWidth()
	}
	return RenderGateQueueFrame(LoadPendingGates(session, time.Now()), width, 0), nil
}

// GraphUI is the interactive graph-run browser: run list → DAG → node
// detail, tick-driven re-read of the run store.
type GraphUI struct {
	session string
	view    graphView

	rows   []RunListRow
	runIdx int

	runID     string
	snap      *GraphSnapshot
	order     []string // layer-major node order for j/k selection
	nodeIdx   int
	directDAG bool // launched with a run id — q from the DAG quits

	// Template launcher
	templates       []bus.GraphTemplateInfo
	tmplIdx         int
	tmplErr         string     // validation error rendered in place
	pendingTemplate string     // template awaiting its ${intent} argument
	pendingGraph    *bus.Graph // parsed graph of pendingTemplate
	intentInput     []rune
	directLaunch    bool // opened in launcher mode — q from templates quits

	// Gate queue and action confirm
	gates         []PendingGate
	gateIdx       int
	directGates   bool         // opened in gate-queue mode — q from gates quits
	pending       *GraphAction // action awaiting its confirm keypress
	confirmReturn graphView    // view to restore after confirm/abandon
	actionErr     string       // action failure rendered in the confirm frame

	loadErr error
	keyCh   chan byte
	now     func() time.Time
}

// NewGraphLauncherUI creates the graph TUI opening straight into the
// template launcher — the `Launch Graph` menu surface.
func NewGraphLauncherUI(session string) *GraphUI {
	return &GraphUI{session: session, view: viewGraphTemplates, directLaunch: true, now: time.Now}
}

// NewGraphGatesUI creates the graph TUI opening straight into the
// pending-gate queue — the `Pending Gates` menu surface.
func NewGraphGatesUI(session string) *GraphUI {
	return &GraphUI{session: session, view: viewGraphGates, directGates: true, now: time.Now}
}

// NewGraphUI creates the graph TUI. A non-empty runID opens straight
// into that run's DAG view.
func NewGraphUI(session, runID string) *GraphUI {
	ui := &GraphUI{session: session, now: time.Now}
	if runID != "" {
		ui.runID = runID
		ui.directDAG = true
		ui.view = viewGraphDAG
	}
	return ui
}

// refresh re-reads the store for the current view. DAG and node views
// share the snapshot, so one read serves both.
func (ui *GraphUI) refresh() {
	switch ui.view {
	case viewGraphRuns:
		ui.rows = LoadRunListRows(ui.session, ui.now())
		if ui.runIdx >= len(ui.rows) {
			ui.runIdx = len(ui.rows) - 1
		}
		if ui.runIdx < 0 {
			ui.runIdx = 0
		}
	case viewGraphTemplates:
		ui.templates = bus.ListGraphTemplates()
		if ui.tmplIdx >= len(ui.templates) {
			ui.tmplIdx = len(ui.templates) - 1
		}
		if ui.tmplIdx < 0 {
			ui.tmplIdx = 0
		}
	case viewGraphGates:
		ui.gates = LoadPendingGates(ui.session, ui.now())
		if ui.gateIdx >= len(ui.gates) {
			ui.gateIdx = len(ui.gates) - 1
		}
		if ui.gateIdx < 0 {
			ui.gateIdx = 0
		}
	case viewGraphDAG, viewGraphNode:
		snap, err := LoadGraphSnapshot(ui.session, ui.runID)
		ui.loadErr = err
		if err != nil {
			ui.snap = nil
			return
		}
		ui.snap = &snap
		ui.order = ui.order[:0]
		for _, layer := range LayoutGraph(snap.Graph).Layers {
			ui.order = append(ui.order, layer...)
		}
		if ui.nodeIdx >= len(ui.order) {
			ui.nodeIdx = len(ui.order) - 1
		}
		if ui.nodeIdx < 0 {
			ui.nodeIdx = 0
		}
	}
}

// selectedNode returns the node id under the cursor in the DAG view.
func (ui *GraphUI) selectedNode() string {
	if ui.nodeIdx >= 0 && ui.nodeIdx < len(ui.order) {
		return ui.order[ui.nodeIdx]
	}
	return ""
}

// handleKey processes one keypress. Returns "quit" to exit, "" otherwise.
func (ui *GraphUI) handleKey(key byte) string {
	if ui.view == viewGraphIntent {
		return ui.handleIntentKey(key)
	}
	if ui.view == viewGraphConfirm {
		return ui.handleConfirmKey(key)
	}
	switch key {
	case 'q':
		return ui.goBack()
	case 27: // Escape or the start of an arrow-key sequence
		return ui.handleEscapeSequence()
	case 'j':
		ui.moveSelection(1)
	case 'k':
		ui.moveSelection(-1)
	case 10, 13: // Enter
		ui.enter()
	case 'L':
		if ui.view == viewGraphRuns {
			ui.view = viewGraphTemplates
			ui.tmplErr = ""
			ui.refresh()
		}
	case 'g':
		if ui.view == viewGraphRuns {
			ui.view = viewGraphGates
			ui.refresh()
		}
	case 'a':
		ui.requestApprove()
	case 'c':
		if ui.view == viewGraphDAG && ui.snap != nil {
			ui.confirm(&GraphAction{Kind: "cancel", RunID: ui.runID})
		}
	case 'r':
		ui.requestRetry()
	case 'R':
		ui.refresh()
	}
	return ""
}

// handleEscapeSequence distinguishes arrow keys (ESC [ A/B — navigate)
// from a bare Escape (go back), the same way RemoteUI does. The footers
// advertise ↑↓, so an arrow press must never read as "back". With no key
// channel (unit tests), the timeout path makes 27 behave as bare Escape.
func (ui *GraphUI) handleEscapeSequence() string {
	select {
	case b1 := <-ui.keyCh:
		if b1 == '[' {
			select {
			case b2 := <-ui.keyCh:
				switch b2 {
				case 'A': // Up
					ui.moveSelection(-1)
				case 'B': // Down
					ui.moveSelection(1)
				}
			case <-time.After(50 * time.Millisecond):
			}
		}
	case <-time.After(50 * time.Millisecond):
		return ui.goBack()
	}
	return ""
}

// handleConfirmKey resolves a pending action: y executes, n/Esc/q
// abandons. Nothing dispatches without the explicit confirm keypress.
func (ui *GraphUI) handleConfirmKey(key byte) string {
	switch key {
	case 'y':
		ui.executeAction()
	case 'n', 'q', 27:
		ui.pending = nil
		ui.actionErr = ""
		ui.view = ui.confirmReturn
	}
	return ""
}

// confirm parks an action behind the confirm prompt.
func (ui *GraphUI) confirm(act *GraphAction) {
	ui.pending = act
	ui.actionErr = ""
	ui.confirmReturn = ui.view
	ui.view = viewGraphConfirm
}

// requestApprove opens the approve confirm for the selected gate — from
// the gate queue, or from a waiting wait_human node in the DAG view. The
// prompt shows what the approval releases before the confirm key is
// accepted.
func (ui *GraphUI) requestApprove() {
	switch ui.view {
	case viewGraphGates:
		if ui.gateIdx >= len(ui.gates) {
			return
		}
		gate := ui.gates[ui.gateIdx]
		ui.confirm(&GraphAction{Kind: "approve", RunID: gate.RunID, NodeID: gate.NodeID,
			Releases: gate.Downstream, Mutating: gate.Mutating})
	case viewGraphDAG:
		if ui.snap == nil {
			return
		}
		nodeID := ui.selectedNode()
		for i := range ui.snap.Graph.Nodes {
			n := &ui.snap.Graph.Nodes[i]
			if n.ID != nodeID || n.Type != bus.NodeWaitHuman {
				continue
			}
			if ui.snap.nodeState(nodeID) != bus.GraphNodeWaiting {
				return
			}
			releases, mutating := GateDownstream(ui.snap.Graph, nodeID)
			ui.confirm(&GraphAction{Kind: "approve", RunID: ui.runID, NodeID: nodeID,
				Releases: releases, Mutating: mutating})
			return
		}
	}
}

// requestRetry opens the retry confirm for the selected DAG node, warning
// about every gate the retry would re-arm.
func (ui *GraphUI) requestRetry() {
	if ui.view != viewGraphDAG || ui.snap == nil {
		return
	}
	nodeID := ui.selectedNode()
	if nodeID == "" {
		return
	}
	ui.confirm(&GraphAction{Kind: "retry", RunID: ui.runID, NodeID: nodeID,
		Rearms: GatesRearmedByRetry(ui.snap.Graph, nodeID)})
}

// executeAction runs the confirmed action through the same bus functions
// the CLI handlers call — never an exec of the CLI, never a bus message,
// so approving here is indistinguishable from the user typing
// `muxcode graph approve` themselves. Failures render in the confirm
// frame; the view only advances on success.
func (ui *GraphUI) executeAction() {
	act := ui.pending
	if act == nil {
		return
	}
	var err error
	switch act.Kind {
	case "approve":
		// The confirm promised a waiting gate — refuse if the node moved
		// on between render and keypress.
		st, rerr := bus.ReadNodeStatus(ui.session, act.RunID, act.NodeID)
		if rerr != nil {
			err = rerr
		} else if st.State != bus.GraphNodeWaiting {
			err = fmt.Errorf("gate %s is %s, not waiting — nothing to approve", act.NodeID, st.State)
		} else {
			err = bus.ApproveGraphGate(ui.session, act.RunID, act.NodeID)
		}
	case "cancel":
		err = bus.CancelGraphRun(ui.session, act.RunID)
	case "retry":
		err = bus.RetryGraphRun(ui.session, act.RunID, act.NodeID)
	}
	if err != nil {
		ui.actionErr = err.Error()
		return
	}
	ui.pending = nil
	ui.actionErr = ""
	ui.view = ui.confirmReturn
	ui.refresh()
}

// handleIntentKey edits the ${intent} argument prompt: printable bytes
// append, backspace deletes, Enter launches, Escape returns to the picker.
func (ui *GraphUI) handleIntentKey(key byte) string {
	switch {
	case key == 27: // Escape
		ui.view = viewGraphTemplates
		ui.intentInput = nil
	case key == 10 || key == 13: // Enter
		ui.launchGraph(ui.pendingGraph, ui.pendingTemplate, string(ui.intentInput))
	case key == 127 || key == 8: // Backspace
		if len(ui.intentInput) > 0 {
			ui.intentInput = ui.intentInput[:len(ui.intentInput)-1]
		}
	case key >= 32 && key < 127: // printable ASCII
		ui.intentInput = append(ui.intentInput, rune(key))
	}
	return ""
}

// launchGraph validates and starts a run, then transitions into its DAG
// view. A validation failure renders in place and never creates a run
// directory — CreateGraphRun validates before it writes, and this path
// validates first anyway to render the full error list.
func (ui *GraphUI) launchGraph(g *bus.Graph, template, intent string) {
	if g == nil {
		ui.view = viewGraphTemplates
		return
	}
	if v := g.Validate(); !v.OK() {
		ui.tmplErr = v.Format()
		ui.view = viewGraphTemplates
		return
	}
	run, err := bus.CreateGraphRun(ui.session, g, template, intent)
	if err != nil {
		ui.tmplErr = err.Error()
		ui.view = viewGraphTemplates
		return
	}
	ui.tmplErr = ""
	ui.intentInput = nil
	ui.pendingGraph = nil
	ui.pendingTemplate = ""
	ui.runID = run.ID
	ui.nodeIdx = 0
	ui.view = viewGraphDAG
	ui.refresh()
}

// goBack pops one view level; from the top level it quits.
func (ui *GraphUI) goBack() string {
	switch ui.view {
	case viewGraphRuns:
		return "quit"
	case viewGraphDAG:
		if ui.directDAG {
			return "quit"
		}
		ui.view = viewGraphRuns
		ui.refresh()
	case viewGraphNode:
		ui.view = viewGraphDAG
	case viewGraphTemplates:
		if ui.directLaunch {
			return "quit"
		}
		ui.tmplErr = ""
		ui.view = viewGraphRuns
		ui.refresh()
	case viewGraphGates:
		if ui.directGates {
			return "quit"
		}
		ui.view = viewGraphRuns
		ui.refresh()
	}
	return ""
}

// moveSelection moves the cursor within the current view.
func (ui *GraphUI) moveSelection(delta int) {
	switch ui.view {
	case viewGraphRuns:
		ui.runIdx = clamp(ui.runIdx+delta, 0, len(ui.rows)-1)
	case viewGraphDAG:
		ui.nodeIdx = clamp(ui.nodeIdx+delta, 0, len(ui.order)-1)
	case viewGraphTemplates:
		ui.tmplIdx = clamp(ui.tmplIdx+delta, 0, len(ui.templates)-1)
	case viewGraphGates:
		ui.gateIdx = clamp(ui.gateIdx+delta, 0, len(ui.gates)-1)
	}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// enter descends one view level: run list → DAG, DAG → node detail.
func (ui *GraphUI) enter() {
	switch ui.view {
	case viewGraphRuns:
		if len(ui.rows) == 0 {
			return
		}
		ui.runID = ui.rows[ui.runIdx].ID
		ui.nodeIdx = 0
		ui.view = viewGraphDAG
		ui.refresh()
	case viewGraphDAG:
		if ui.selectedNode() != "" {
			ui.view = viewGraphNode
		}
	case viewGraphTemplates:
		if len(ui.templates) == 0 {
			return
		}
		name := ui.templates[ui.tmplIdx].Name
		g, _, err := bus.ResolveGraphTemplate(name)
		if err != nil {
			ui.tmplErr = err.Error()
			return
		}
		if v := g.Validate(); !v.OK() {
			ui.tmplErr = v.Format()
			return
		}
		if TemplateNeedsIntent(g) {
			ui.pendingTemplate = name
			ui.pendingGraph = g
			ui.intentInput = nil
			ui.view = viewGraphIntent
			return
		}
		ui.launchGraph(g, name, "")
	case viewGraphGates:
		ui.requestApprove()
	}
}

// render builds the frame for the current view, footer included.
func (ui *GraphUI) render() string {
	W := termWidth()
	H := termHeight()

	var frame, footer string
	switch ui.view {
	case viewGraphRuns:
		frame = RenderRunListFrame(ui.rows, W, ui.runIdx)
		footer = fmt.Sprintf("  %s↑↓/jk%s Navigate  %sEnter%s Open  %sL%s Launch  %sR%s Refresh  %sq%s Quit",
			Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST)
	case viewGraphDAG:
		if ui.snap == nil {
			frame = fmt.Sprintf("\n  %sCannot load run %s: %v%s\n", Red, ui.runID, ui.loadErr, RST)
		} else {
			frame = RenderGraphFrame(*ui.snap, W, H, ui.selectedNode(), ui.now())
		}
		footer = fmt.Sprintf("  %sjk%s Select  %sEnter%s Detail  %sa%s Approve gate  %sc%s Cancel  %sr%s Retry  %sq/Esc%s Back",
			Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST)
	case viewGraphNode:
		if ui.snap == nil {
			frame = fmt.Sprintf("\n  %sCannot load run %s: %v%s\n", Red, ui.runID, ui.loadErr, RST)
		} else {
			frame = RenderNodeDetailFrame(*ui.snap, ui.selectedNode(), W)
		}
		footer = fmt.Sprintf("  %sq/Esc%s Back", Yellow, RST)
	case viewGraphTemplates:
		frame = RenderTemplateListFrame(ui.templates, W, ui.tmplIdx, ui.tmplErr)
		footer = fmt.Sprintf("  %s↑↓/jk%s Navigate  %sEnter%s Launch  %sq/Esc%s Back",
			Yellow, RST, Yellow, RST, Yellow, RST)
	case viewGraphIntent:
		frame = RenderIntentPromptFrame(ui.pendingTemplate, string(ui.intentInput), W)
		footer = fmt.Sprintf("  %sEnter%s Launch  %sEsc%s Cancel", Yellow, RST, Yellow, RST)
	case viewGraphGates:
		frame = RenderGateQueueFrame(ui.gates, W, ui.gateIdx)
		footer = fmt.Sprintf("  %s↑↓/jk%s Navigate  %sEnter/a%s Approve  %sR%s Refresh  %sq/Esc%s Back",
			Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST)
	case viewGraphConfirm:
		if ui.pending != nil {
			frame = RenderConfirmFrame(*ui.pending, W, ui.actionErr) // carries its own y/n hint
		}
	}
	return frame + "\n" + Comment + HLine('─', W) + RST + "\n" + footer + "\n"
}

// Run starts the interactive loop. Blocks until the user quits.
func (ui *GraphUI) Run() {
	rawCmd := exec.Command("stty", "-icanon", "-echo", "min", "1")
	rawCmd.Stdin = os.Stdin
	rawErr := rawCmd.Run()

	fmt.Print("\033[2J\033[H")
	fmt.Print("\033[?25l")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ui.keyCh = make(chan byte, 16)
	go ui.readKeys()

	defer ui.cleanup(rawErr == nil)

	ui.refresh()

	for {
		fmt.Print("\033[H")
		fmt.Print(ClearFrame(ui.render()))
		fmt.Print("\033[J")

		select {
		case <-sigCh:
			return
		case key := <-ui.keyCh:
			if ui.handleKey(key) == "quit" {
				return
			}
		case <-time.After(graphTickInterval):
			ui.refresh()
		}
	}
}

// readKeys reads single bytes from stdin in a loop.
func (ui *GraphUI) readKeys() {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		ui.keyCh <- buf[0]
	}
}

// cleanup restores the terminal.
func (ui *GraphUI) cleanup(restoreStty bool) {
	if restoreStty {
		saneCmd := exec.Command("stty", "sane")
		saneCmd.Stdin = os.Stdin
		_ = saneCmd.Run()
	}
	fmt.Print("\033[?25h")
	fmt.Print(RST)
	fmt.Print("\033[2J")
	fmt.Print("\033[H")
}
