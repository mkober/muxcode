package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
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
	viewGraphPrompt
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
		var failedNodes, doneNodes []string
		var failedOut string
		for j := range g.Nodes {
			n := &g.Nodes[j]
			state := bus.GraphNodePending
			var st *bus.GraphNodeStatus
			if s := statuses[n.ID]; s != nil {
				st, state = s, s.State
			}
			if state == bus.GraphNodeDone {
				row.Done++
				id := n.ID
				if st.Outcome == bus.OutcomeUnknown {
					id += "?" // completion inferred — the cell explains the mark
				}
				doneNodes = append(doneNodes, id)
			}
			if state == bus.GraphNodeFailed {
				failedNodes = append(failedNodes, n.ID)
				if failedOut == "" {
					failedOut = st.Output
				}
			}
			if n.Type == bus.NodeWaitHuman && state == bus.GraphNodeWaiting {
				row.GateWaiting = true
			}
		}
		row.Results = SummarizeRunResults(r.State, failedNodes, failedOut, doneNodes)
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

// LoadResolvedGates collects wait_human nodes that reached a terminal
// state across ALL runs (finished ones included) — the queue's history
// section. Newest resolutions first, capped at limit.
func LoadResolvedGates(session string, now time.Time, limit int) []ResolvedGate {
	runs, err := bus.ListGraphRuns(session)
	if err != nil {
		return nil
	}
	type stamped struct {
		gate ResolvedGate
		at   int64
	}
	var all []stamped
	for _, run := range runs {
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
			if st == nil || (st.State != bus.GraphNodeDone && st.State != bus.GraphNodeSkipped) {
				continue
			}
			at := st.DoneAt
			if at == 0 {
				at = st.UpdatedAt
			}
			var age time.Duration
			if at > 0 && now.Unix() > at {
				age = time.Duration(now.Unix()-at) * time.Second
			}
			all = append(all, stamped{
				gate: ResolvedGate{RunID: run.ID, NodeID: n.ID, State: st.State, Age: age},
				at:   at,
			})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at > all[j].at })
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]ResolvedGate, len(all))
	for i, s := range all {
		out[i] = s.gate
	}
	return out
}

// resolvedGateHistoryLimit caps the queue's history section.
const resolvedGateHistoryLimit = 10

// GateQueueRenderOnce renders the pending-gate queue as a single frame —
// the scriptable seam for integration tests. The first gate is selected
// so its downstream impact is visible in the frame.
func GateQueueRenderOnce(session string, width int) (string, error) {
	if width <= 0 {
		width = termWidth()
	}
	now := time.Now()
	return RenderGateQueueFrame(LoadPendingGates(session, now),
		LoadResolvedGates(session, now, resolvedGateHistoryLimit), width, 0), nil
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
	resolvedGates []ResolvedGate
	gateIdx       int
	directGates   bool         // opened in gate-queue mode — q from gates quits
	pending       *GraphAction // action awaiting its confirm keypress
	confirmReturn graphView    // view to restore after confirm/abandon
	actionErr     string       // action failure rendered in the confirm frame
	notice        string       // success confirmation shown after an action lands

	// Last-selected item ids per surface, so a selection survives a full
	// Tab cycle; a removed item degrades to the first row.
	runSelID   string
	tmplSelID  string
	gateSelKey string // runID + "/" + nodeID

	// Prompt surface (MUX-109). The input buffer lives here, not in the
	// view, so Tab-ing away and back never discards a half-typed prompt.
	promptInput         []rune
	promptInject        bool
	promptExchanges     []PromptExchange
	promptUnreachable   string
	promptActivity      []string // headless agent's log tail — live working detail
	promptScroll        int      // transcript rows scrolled up from the tail (0 = pinned)
	promptCursor        int      // rune index into promptInput (len = end)
	promptInjectTouched bool     // user toggled Ctrl-T — keyless default stands down
	promptWindow        string   // host window (resolved once — tmux call)
	promptActiveRole    string   // window's active agent, mode-cycle aware
	paneWindow          string   // this pane's tmux window (focused-pane authority)
	directPrompt        bool     // opened in prompt mode — q/Esc from it quits

	// Gates already seen waiting — a NEW one switches the UI to the
	// Pending Gates surface (the strip's ambient-attention contract).
	knownWaiting map[string]bool

	loadErr error
	keyCh   chan byte
	now     func() time.Time
}

// graphSurfaces is the Tab cycle order over the top-level views — it
// must match the tab bar's visual order (renderSurfaceTabs).
var graphSurfaces = []graphView{viewGraphPrompt, viewGraphTemplates, viewGraphRuns, viewGraphGates}

// surfaceName maps a view to its tab-bar name; drill-ins highlight their
// parent surface so the bar stays static across every frame.
func surfaceName(v graphView) string {
	switch v {
	case viewGraphPrompt:
		return "Prompt"
	case viewGraphGates:
		return "Pending Gates"
	case viewGraphTemplates, viewGraphIntent:
		return "Launch Graph"
	default: // runs, DAG, node detail
		return "Graph Runs"
	}
}

// surfaceKey and surfaceForKey translate navigation state to the shared
// on-disk selection and back. Drill-ins share too — "run:<id>" carries
// the DAG view (node detail shares its run), so switching tmux windows
// lands on the SAME frame everywhere (user catch, 2026-08-27: F10's
// pane sat in a run DAG while F2's had gate-switched — every window
// change looked like the pane changing). Confirm and intent are active
// input flows and are never shared.
func surfaceKey(v graphView, runID string) string {
	switch v {
	case viewGraphPrompt:
		return "prompt"
	case viewGraphGates:
		return "gates"
	case viewGraphTemplates:
		return "launcher"
	case viewGraphDAG, viewGraphNode:
		if runID != "" {
			return "run:" + runID
		}
		return "runs"
	case viewGraphConfirm, viewGraphIntent:
		return ""
	default:
		return "runs"
	}
}

func surfaceForKey(k string) (graphView, string, bool) {
	if strings.HasPrefix(k, "run:") {
		if id := strings.TrimPrefix(k, "run:"); id != "" {
			return viewGraphDAG, id, true
		}
		return viewGraphRuns, "", true
	}
	switch k {
	case "prompt":
		return viewGraphPrompt, "", true
	case "gates":
		return viewGraphGates, "", true
	case "launcher":
		return viewGraphTemplates, "", true
	case "runs":
		return viewGraphRuns, "", true
	}
	return viewGraphRuns, "", false
}

// shareSurface persists a navigation change so every other control pane
// follows (they sync on tick). Unshareable views (confirm, intent) keep
// the previous shared value.
func (ui *GraphUI) shareSurface() {
	if k := surfaceKey(ui.view, ui.runID); k != "" {
		bus.WriteControlPaneSurface(ui.session, k)
	}
}

// paneFocused reports whether this pane's tmux window is the active one
// — the focused pane is authoritative (the human is driving it) and
// never adopts; every unfocused pane converges to what it shares.
func (ui *GraphUI) paneFocused() bool {
	if ui.paneWindow == "" {
		if ui.paneWindow = bus.BusRole(); ui.paneWindow == "" {
			return false
		}
	}
	return bus.IsWindowFocused(ui.session, ui.paneWindow)
}

// syncSharedSurface adopts the shared selection on unfocused panes —
// drill-ins included — so a window switch shows the frame the user was
// just looking at. Never adopts mid-input (confirm/intent), and
// adopting never writes back (one-way convergence).
func (ui *GraphUI) syncSharedSurface() {
	if ui.view == viewGraphConfirm || ui.view == viewGraphIntent {
		return
	}
	if ui.paneFocused() {
		return
	}
	k, ok := bus.ReadControlPaneSurface(ui.session)
	if !ok {
		return
	}
	v, runID, valid := surfaceForKey(k)
	if !valid || (v == ui.view && (runID == "" || runID == ui.runID)) {
		return
	}
	ui.view = v
	if runID != "" {
		ui.runID = runID
	}
	ui.refresh()
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

// NewGraphPromptUI creates the graph TUI opening straight into the
// Prompt surface (MUX-109).
func NewGraphPromptUI(session string) *GraphUI {
	return &GraphUI{session: session, view: viewGraphPrompt, directPrompt: true, now: time.Now}
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
		ui.resolvedGates = LoadResolvedGates(ui.session, ui.now(), resolvedGateHistoryLimit)
		if ui.gateIdx >= len(ui.gates) {
			ui.gateIdx = len(ui.gates) - 1
		}
		if ui.gateIdx < 0 {
			ui.gateIdx = 0
		}
	case viewGraphPrompt:
		ui.promptExchanges = LoadPromptExchanges(ui.session, promptTranscriptLimit)
		ui.promptUnreachable = PromptUnreachable(ui.session)
		ui.promptActivity = LoadPromptActivity(ui.session, promptActivityLimit)
		// Keyless gateway: interpret cannot deliver, so the toggle
		// defaults to inject — but a deliberate Ctrl-T stands.
		if PromptKeyless(ui.session) && !ui.promptInjectTouched {
			ui.promptInject = true
		}
		// Destination resolution is I/O (tmux + mode state), so it lives
		// here, never in the renderer. The window is stable; the active
		// role re-resolves each refresh because mode cycling can change
		// it under a live pane.
		if ui.promptWindow == "" {
			if ui.promptWindow = bus.BusRole(); ui.promptWindow == "" {
				ui.promptWindow = "edit"
			}
		}
		ui.promptActiveRole = ui.promptWindow
		if active, err := bus.ActiveModeRole(ui.session, ui.promptWindow); err == nil && active != "" {
			ui.promptActiveRole = active
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
	if ui.view == viewGraphPrompt {
		return ui.handlePromptKey(key)
	}
	ui.notice = "" // a receipt survives ticks, not the next keypress
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
	case 9: // Tab — cycle the top-level surfaces; inert everywhere else
		ui.cycleSurface(1)
	case 'L':
		if ui.view == viewGraphRuns {
			ui.view = viewGraphTemplates
			ui.tmplErr = ""
			ui.refresh()
			ui.shareSurface()
		}
	case 'g':
		if ui.view == viewGraphRuns {
			ui.view = viewGraphGates
			ui.refresh()
			ui.shareSurface()
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
// and Shift-Tab (ESC [ Z — cycle backward) from a bare Escape (go back),
// the same way RemoteUI does. The footers advertise ↑↓, so an arrow press
// must never read as "back". With no key channel (unit tests), the
// timeout path makes 27 behave as bare Escape.
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
				case 'C': // Right — the DAG reads left-to-right, so ←/→
					// walk the selection the way the graph is drawn
					// (user-requested, 2026-08-27)
					ui.moveSelection(1)
				case 'D': // Left
					ui.moveSelection(-1)
				case 'Z': // Shift-Tab
					ui.cycleSurface(-1)
				}
			case <-time.After(50 * time.Millisecond):
			}
		}
	case <-time.After(50 * time.Millisecond):
		return ui.goBack()
	}
	return ""
}

// cycleSurface moves to the next/previous top-level surface. Inert in
// every non-surface view (DAG, node detail, intent prompt, confirm) —
// those are drill-ins, and Tab yanking the user out of one mid-task
// would discard context.
func (ui *GraphUI) cycleSurface(delta int) {
	cur := -1
	for i, s := range graphSurfaces {
		if ui.view == s {
			cur = i
		}
	}
	if cur < 0 {
		return
	}
	ui.saveSelection()
	next := (cur + delta + len(graphSurfaces)) % len(graphSurfaces)
	ui.view = graphSurfaces[next]
	ui.tmplErr = ""
	ui.refresh()
	ui.restoreSelection()
	ui.shareSurface()
}

// saveSelection records the current surface's selected item by id.
func (ui *GraphUI) saveSelection() {
	switch ui.view {
	case viewGraphRuns:
		if ui.runIdx >= 0 && ui.runIdx < len(ui.rows) {
			ui.runSelID = ui.rows[ui.runIdx].ID
		}
	case viewGraphTemplates:
		if ui.tmplIdx >= 0 && ui.tmplIdx < len(ui.templates) {
			ui.tmplSelID = ui.templates[ui.tmplIdx].Name
		}
	case viewGraphGates:
		if ui.gateIdx >= 0 && ui.gateIdx < len(ui.gates) {
			ui.gateSelKey = ui.gates[ui.gateIdx].RunID + "/" + ui.gates[ui.gateIdx].NodeID
		}
	}
}

// restoreSelection re-finds the surface's last-selected item by id after
// a refresh; a removed item degrades to the first row.
func (ui *GraphUI) restoreSelection() {
	switch ui.view {
	case viewGraphRuns:
		ui.runIdx = 0
		for i, r := range ui.rows {
			if r.ID == ui.runSelID {
				ui.runIdx = i
			}
		}
	case viewGraphTemplates:
		ui.tmplIdx = 0
		for i, t := range ui.templates {
			if t.Name == ui.tmplSelID {
				ui.tmplIdx = i
			}
		}
	case viewGraphGates:
		ui.gateIdx = 0
		for i, g := range ui.gates {
			if g.RunID+"/"+g.NodeID == ui.gateSelKey {
				ui.gateIdx = i
			}
		}
	}
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
	// A visible receipt: the gate vanishes from the queue on the next
	// tick, and without this line a successful approval is
	// indistinguishable from a press that did nothing (user-reported
	// 2026-08-26 — repeated approvals of an already-released gate).
	switch act.Kind {
	case "approve":
		ui.notice = fmt.Sprintf("✓ approved %s — downstream released (run %s)", act.NodeID, act.RunID)
	case "cancel":
		ui.notice = fmt.Sprintf("✓ canceled run %s", act.RunID)
	case "retry":
		ui.notice = fmt.Sprintf("✓ retrying run %s from %s", act.RunID, act.NodeID)
	}
	ui.pending = nil
	ui.actionErr = ""
	ui.view = ui.confirmReturn
	ui.refresh()
}

// editLine applies one keypress to a line buffer: printable ASCII
// appends, backspace deletes. Returns the new buffer and whether the key
// was consumed — the shared editor behind the ${intent} prompt and the
// Prompt surface, so the two never drift on byte handling. End-anchored;
// the Prompt surface uses the cursor-aware editLineAt underneath.
func editLine(buf []rune, key byte) ([]rune, bool) {
	out, _, ok := editLineAt(buf, len(buf), key)
	return out, ok
}

// editLineAt is the cursor-aware editor: printable ASCII inserts at the
// cursor, backspace deletes before it. An out-of-range cursor clamps to
// the end, so end-anchored callers never need to track one.
func editLineAt(buf []rune, cursor int, key byte) ([]rune, int, bool) {
	if cursor < 0 || cursor > len(buf) {
		cursor = len(buf)
	}
	switch {
	case key == 127 || key == 8: // Backspace
		if cursor > 0 {
			buf = append(buf[:cursor-1], buf[cursor:]...)
			cursor--
		}
		return buf, cursor, true
	case key >= 32 && key < 127: // printable ASCII
		buf = append(buf[:cursor], append([]rune{rune(key)}, buf[cursor:]...)...)
		return buf, cursor + 1, true
	}
	return buf, cursor, false
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
	default:
		ui.intentInput, _ = editLine(ui.intentInput, key)
	}
	return ""
}

// handlePromptKey drives the Prompt surface. Unlike the modal intent
// prompt, this is a top-level surface: Tab and Shift-Tab still cycle
// away (the input buffer survives the round trip), and the surface never
// blocks on the model — Enter sends a bus request and returns to
// rendering immediately.
func (ui *GraphUI) handlePromptKey(key byte) string {
	switch {
	case key == 9: // Tab
		ui.cycleSurface(1)
	case key == 27:
		return ui.handlePromptEscape()
	case key == 10 || key == 13: // Enter
		ui.submitPrompt()
	case key == 20: // Ctrl-T — inject/interpret toggle
		ui.promptInject = !ui.promptInject
		ui.promptInjectTouched = true // a deliberate choice outranks the keyless default
	default:
		ui.promptInput, ui.promptCursor, _ = editLineAt(ui.promptInput, ui.promptCursor, key)
	}
	return ""
}

// handlePromptEscape mirrors handleEscapeSequence for the Prompt surface:
// ESC [ Z (Shift-Tab) cycles backward, arrows are inert (no selection
// here), and a bare Escape clears a non-empty input before it means
// "back" — so one stray Escape never both wipes the text and exits.
func (ui *GraphUI) handlePromptEscape() string {
	select {
	case b1 := <-ui.keyCh:
		if b1 == '[' {
			select {
			case b2 := <-ui.keyCh:
				switch b2 {
				case 'Z': // Shift-Tab
					ui.cycleSurface(-1)
				case 'A': // Up — scroll to older transcript rows
					ui.promptScroll++
				case 'B': // Down — back toward the newest
					if ui.promptScroll > 0 {
						ui.promptScroll--
					}
				case 'C': // Right — cursor toward the end
					if ui.promptCursor < len(ui.promptInput) {
						ui.promptCursor++
					}
				case 'D': // Left — cursor toward the start
					if ui.promptCursor > 0 {
						ui.promptCursor--
					}
				}
			case <-time.After(50 * time.Millisecond):
			}
		}
	case <-time.After(50 * time.Millisecond):
		if len(ui.promptInput) > 0 {
			ui.promptInput = nil
			ui.promptCursor = 0
			return ""
		}
		return ui.goBack()
	}
	return ""
}

// submitPrompt dispatches the typed text. Interpret mode sends an
// ordinary bus request to the prompt role (SendNoCC — a prompt typed on
// the build window must not CC edit's inbox) and returns to rendering;
// the reply arrives through the transcript on a later refresh, so the
// pane never waits on inference. Inject delivery is Phase 6.
func (ui *GraphUI) submitPrompt() {
	text := strings.TrimSpace(string(ui.promptInput))
	if text == "" {
		return
	}
	if ui.promptInject {
		window := ui.promptWindow
		if window == "" {
			if window = bus.BusRole(); window == "" {
				window = "edit"
			}
		}
		role, err := bus.InjectPromptText(ui.session, window, text)
		if err != nil {
			// Input survives a failed inject — retyping is the one cost
			// the failure must not add.
			ui.notice = "inject failed: " + err.Error()
			return
		}
		ui.promptInput = nil
		ui.notice = fmt.Sprintf("⇒ injected to %s", role)
		return
	}
	// Interpret cannot deliver without a gateway key — refuse loudly
	// instead of sending into a 401ing harness; the typed text survives.
	if PromptKeyless(ui.session) {
		ui.notice = "interpret needs a gateway key — add MUXCODE_OPENCODE_API_KEY to ~/.config/muxcode/config (Ctrl-T injects instead)"
		return
	}
	from := bus.BusRole()
	if from == "" {
		from = "edit"
	}
	// SendHumanPrompt is the surface's sanctioned path: prompt requests
	// are refused from every bus identity (CheckPromptAuthority) because
	// their text is what the approve guard trusts as human words — only
	// this in-process seam, which a human's keystrokes just fed, may send.
	if err := bus.SendHumanPrompt(ui.session, from, text); err != nil {
		ui.notice = "send failed: " + err.Error()
		return
	}
	ui.promptInput = nil
	ui.promptCursor = 0
	ui.promptScroll = 0 // a new question pins the view back to the tail
	ui.refresh()        // the sent question renders as the working exchange
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
	case viewGraphPrompt:
		if ui.directPrompt {
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
		footer = fmt.Sprintf("  %s←→/jk%s Select  %sEnter%s Detail  %sa%s Approve gate  %sc%s Cancel  %sr%s Retry  %sq/Esc%s Back",
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
		frame = RenderGateQueueFrame(ui.gates, ui.resolvedGates, W, ui.gateIdx)
		footer = fmt.Sprintf("  %s↑↓/jk%s Navigate  %sEnter/a%s Approve  %sR%s Refresh  %sq/Esc%s Back",
			Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST)
	case viewGraphPrompt:
		st := PromptSurfaceState{
			Exchanges:   ui.promptExchanges,
			Input:       string(ui.promptInput),
			Cursor:      ui.promptCursor,
			Inject:      ui.promptInject,
			Destination: promptDestinationLabel(ui.promptInject, ui.promptActiveRole),
			Unreachable: ui.promptUnreachable,
			Activity:    ui.promptActivity,
			Scroll:      ui.promptScroll,
		}
		if n := len(st.Exchanges); n > 0 && !st.Exchanges[n-1].Answered {
			st.Working = true
		}
		frame = RenderPromptFrame(st, W, H)
		footer = fmt.Sprintf("  %sEnter%s Send  %sCtrl-T%s Inject/Interpret  %s↑↓%s Scroll  %s←→%s Cursor  %sBksp%s Delete  %sEsc%s Clear/Back",
			Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST)
		backend, model := bus.PromptBackendInfo(ui.session)
		footer = PromptFooterStatus(footer, backend, model, W)
	case viewGraphConfirm:
		if ui.pending != nil {
			// Static tab bar for the surface the confirm returns to,
			// then the prompt (which carries its own y/n hint).
			frame = renderSurfaceTabs(surfaceName(ui.confirmReturn), W) +
				RenderConfirmFrameH(*ui.pending, W, H, ui.actionErr)
		}
	}
	if ui.notice != "" {
		frame += "\n  " + Green + ui.notice + RST + "\n"
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
		// Clamp to the pane height: printing past the last row scrolls
		// the popup, shifting the tab bar up — the header must be static.
		fmt.Print(ClearFrame(clampLines(ui.render(), termHeight()-1)))
		fmt.Print("\033[J")

		select {
		case <-sigCh:
			return
		case key := <-ui.keyCh:
			prevView, prevRun := ui.view, ui.runID
			if ui.handleKey(key) == "quit" {
				return
			}
			// Every key-driven navigation shares — window switches must
			// land on the frame the user just navigated to, drill-ins
			// included.
			if ui.view != prevView || ui.runID != prevRun {
				ui.shareSurface()
			}
		case <-time.After(graphTickInterval):
			ui.syncSharedSurface()
			ui.checkGateSwitch()
			ui.refresh()
		}
	}
}

// checkGateSwitch switches to the Pending Gates surface when a gate
// becomes newly waiting — once per gate, never from a drill-in (DAG,
// node detail, intent, confirm), which would discard the user's context.
func (ui *GraphUI) checkGateSwitch() {
	// The Prompt surface counts as top-level: switching away does not
	// discard context, because the input buffer lives on the UI struct.
	topLevel := ui.view == viewGraphRuns || ui.view == viewGraphTemplates ||
		ui.view == viewGraphGates || ui.view == viewGraphPrompt
	gates := LoadPendingGates(ui.session, ui.now())
	fresh := false
	seen := make(map[string]bool, len(gates))
	for _, g := range gates {
		k := g.RunID + "/" + g.NodeID
		seen[k] = true
		if !ui.knownWaiting[k] {
			fresh = true
		}
	}
	ui.knownWaiting = seen
	if fresh && topLevel && ui.view != viewGraphGates {
		ui.view = viewGraphGates
		ui.notice = "⚑ new gate waiting"
		ui.refresh()
		ui.shareSurface() // every pane follows the gate
	}
}

// clampLines truncates a frame to at most h lines.
func clampLines(s string, h int) string {
	if h < 1 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n") + "\n"
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
