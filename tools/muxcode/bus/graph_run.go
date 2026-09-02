package bus

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Graph node run states. Transitions are gated by legalNodeTransitions —
// see TransitionGraphNode.
const (
	GraphNodePending = "pending" // upstream edges not yet satisfied
	GraphNodeReady   = "ready"   // schedulable by the executor
	GraphNodeRunning = "running" // work dispatched, completion pending
	GraphNodeWaiting = "waiting" // blocked on a human gate, event, or join barrier
	GraphNodeDone    = "done"    // finished with an outcome
	GraphNodeFailed  = "failed"  // finished unsuccessfully
	GraphNodeSkipped = "skipped" // branch not taken or run canceled
)

// Graph run states.
const (
	GraphRunRunning  = "running"
	GraphRunComplete = "complete"
	GraphRunFailed   = "failed"
	GraphRunCanceled = "canceled"
)

// GraphRun is the persisted metadata for one graph run instance.
// EdgeFires counts how many times each edge has fired (keyed by
// EdgeFireKey) so capped loop edges stop at max_iterations across
// daemon restarts.
//
// RetryNote records the most recent retry's re-targeting decision
// (MUX-132): set when a retry aimed below a satisfied human gate was
// re-armed at the gate instead, cleared by a retry that needed no
// re-target. Surfaced by graph status so the decision stays visible
// after the CLI output scrolls away.
type GraphRun struct {
	ID        string         `json:"id"`
	Template  string         `json:"template"`
	Intent    string         `json:"intent,omitempty"`
	State     string         `json:"state"`
	CreatedAt int64          `json:"created_at"`
	UpdatedAt int64          `json:"updated_at"`
	EdgeFires map[string]int `json:"edge_fires,omitempty"`
	RetryNote string         `json:"retry_note,omitempty"` // last retry's re-target decision — see GraphRun doc
}

// GraphNodeStatus is the persisted per-node execution state of a run.
// Routed records that a finished node's outgoing edges have been fired,
// so a tick (or a resume after a crash) never routes the same completion
// twice.
type GraphNodeStatus struct {
	NodeID  string `json:"node_id"`
	State   string `json:"state"`
	Outcome string `json:"outcome,omitempty"` // success/failure/custom, set when finished
	Output  string `json:"output,omitempty"`  // harvested response payload
	TaskID  string `json:"task_id,omitempty"` // correlated tracked task or spawn id(s)
	Routed  bool   `json:"routed,omitempty"`
	// Stall-redrive bookkeeping (MUX-123): persisted here so it survives
	// daemon restarts — the watchdog's in-memory debounce reset on every
	// build-triggered restart, which is how live stalls outlived it.
	Redrives    int   `json:"redrives,omitempty"`
	LastRedrive int64 `json:"last_redrive,omitempty"`
	StartedAt   int64 `json:"started_at,omitempty"`
	DoneAt      int64 `json:"done_at,omitempty"`
	UpdatedAt   int64 `json:"updated_at"`
	// Branched marks a condition's false branch for JSON consumers —
	// render-time only, never persisted; see ConditionTookBranch.
	Branched bool `json:"branched,omitempty"`
}

// legalNodeTransitions defines the allowed node state machine. done/failed/
// skipped → ready re-arms a node for loop edges and retry --from; every
// other terminal-to-anything move is rejected so a crashed executor cannot
// corrupt run history on resume. ready → failed is a dispatch-time guard
// decline (MUX-114): the node fails without ever running, so routing it
// through running would stamp a start that never happened.
var legalNodeTransitions = map[string]map[string]bool{
	GraphNodePending: {GraphNodeReady: true, GraphNodeSkipped: true},
	GraphNodeReady:   {GraphNodeRunning: true, GraphNodeWaiting: true, GraphNodeSkipped: true, GraphNodeFailed: true},
	GraphNodeRunning: {GraphNodeDone: true, GraphNodeFailed: true},
	GraphNodeWaiting: {GraphNodeRunning: true, GraphNodeDone: true, GraphNodeFailed: true, GraphNodeSkipped: true},
	GraphNodeDone:    {GraphNodeReady: true},
	GraphNodeFailed:  {GraphNodeReady: true},
	GraphNodeSkipped: {GraphNodeReady: true},
}

// GraphRunsDir returns the per-session root of all graph run stores.
func GraphRunsDir(session string) string {
	return filepath.Join(BusDir(session), "graphs")
}

// GraphRunDir returns the store directory for one run.
func GraphRunDir(session, runID string) string {
	return filepath.Join(GraphRunsDir(session), runID)
}

func graphRunPath(session, runID string) string {
	return filepath.Join(GraphRunDir(session, runID), "run.json")
}

func graphDefPath(session, runID string) string {
	return filepath.Join(GraphRunDir(session, runID), "graph.json")
}

func graphNodesDir(session, runID string) string {
	return filepath.Join(GraphRunDir(session, runID), "nodes")
}

func graphNodePath(session, runID, nodeID string) string {
	return filepath.Join(graphNodesDir(session, runID), nodeID+".json")
}

// NewGraphRunID generates a unique run id: <unix>-<name>-<hex4>. The name
// becomes part of a directory path under BusDir, so it is reduced to
// [a-zA-Z0-9_-] — a graph file named "../x" must not escape the run store.
func NewGraphRunID(name string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d-%s-%s", time.Now().Unix(), clean, hex.EncodeToString(b))
}

// EdgeFireKey identifies an edge in GraphRun.EdgeFires.
func EdgeFireKey(e Edge) string {
	return e.From + "->" + e.To + ":" + edgeOutcome(e)
}

// atomicWriteJSON marshals v and writes it via tmp-file + rename so a
// crash mid-write can never leave a half-written status behind — run
// state must survive daemon restarts intact (resume reads it as truth).
func atomicWriteJSON(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data)
}

// atomicWriteFile writes data via tmp-file + rename. Shared by the run
// store and the template write path (WriteGraphDefinition), so both get
// the same crash-safety guarantee from one implementation.
func atomicWriteFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// CreateGraphRun validates the graph, freezes its definition into the run
// store, and initializes node statuses: the start node ready, all others
// pending. The frozen copy makes the run immune to template edits while
// in flight.
func CreateGraphRun(session string, g *Graph, template, intent string) (*GraphRun, error) {
	if v := g.Validate(); !v.OK() {
		return nil, fmt.Errorf("graph %q is invalid: %s", g.Name, strings.Join(v.Errors, "; "))
	}
	// The run-creation chokepoint covers every launch road (CLI, launcher
	// surface, prompt-agent) — a spec-driven graph must not start against
	// nothing (req-code-pr implements per the active requirements spec;
	// with none set its implement node would freewheel).
	if g.RequiresSpec && strings.TrimSpace(ReadActiveSpec(session)) == "" {
		return nil, fmt.Errorf("graph %q requires an active requirements spec — set one first: muxcode spec set <path>", g.Name)
	}
	g, err := resolveDerivedLoopCaps(session, g)
	if err != nil {
		return nil, err
	}

	run := &GraphRun{
		ID:        NewGraphRunID(g.Name),
		Template:  template,
		Intent:    intent,
		State:     GraphRunRunning,
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		EdgeFires: map[string]int{},
	}

	if err := os.MkdirAll(graphNodesDir(session, run.ID), 0755); err != nil {
		return nil, err
	}
	if err := atomicWriteJSON(graphDefPath(session, run.ID), g); err != nil {
		return nil, err
	}
	for _, n := range g.Nodes {
		st := &GraphNodeStatus{NodeID: n.ID, State: GraphNodePending, UpdatedAt: time.Now().Unix()}
		if n.ID == g.Start {
			st.State = GraphNodeReady
		}
		if err := atomicWriteJSON(graphNodePath(session, run.ID, n.ID), st); err != nil {
			return nil, err
		}
	}
	if err := atomicWriteJSON(graphRunPath(session, run.ID), run); err != nil {
		return nil, err
	}
	return run, nil
}

// resolveDerivedLoopCaps rewrites max_iterations_from_spec edges into
// concrete caps on a copy of the graph before it is frozen: cap = the
// active spec's phase count (MUX-121 decision 2 — a fixed cap silently
// truncates a long spec). A derived cap that cannot be computed is a
// creation error, not a silent default: a loop with a guessed bound is
// the failure the derivation exists to prevent. The cap resolution is
// logged so the bound is visible before the run ever loops.
func resolveDerivedLoopCaps(session string, g *Graph) (*Graph, error) {
	needs := false
	for _, e := range g.Edges {
		if e.MaxIterationsFromSpec {
			needs = true
			break
		}
	}
	if !needs {
		return g, nil
	}
	path, ok, transient, refused := activeSpecFile(session)
	if refused {
		return nil, fmt.Errorf("graph %q: active spec pointer resolves outside the repo — refusing to read it for a loop cap", g.Name)
	}
	if transient {
		return nil, fmt.Errorf("graph %q derives a loop cap from the spec but the repo dir is unresolvable — retry", g.Name)
	}
	if !ok {
		return nil, fmt.Errorf("graph %q derives a loop cap from the spec but no active spec is set", g.Name)
	}
	phases, err := SpecPhases(path)
	if err != nil {
		return nil, fmt.Errorf("graph %q: cannot read active spec for loop cap: %v", g.Name, err)
	}
	if len(phases) == 0 {
		return nil, fmt.Errorf("graph %q derives a loop cap but the active spec has no phase sections", g.Name)
	}
	resolved := *g
	resolved.Edges = append([]Edge(nil), g.Edges...)
	for i := range resolved.Edges {
		if resolved.Edges[i].MaxIterationsFromSpec {
			resolved.Edges[i].MaxIterations = len(phases)
			resolved.Edges[i].MaxIterationsFromSpec = false // frozen copy carries only the number
			LogLifecycle(session, "info", "daemon", "graph-loop-cap-derived",
				fmt.Sprintf("%s: edge %s->%s capped at %d (spec phase count)",
					g.Name, resolved.Edges[i].From, resolved.Edges[i].To, len(phases)))
		}
	}
	return &resolved, nil
}

// ReadGraphRun reads a run's metadata.
func ReadGraphRun(session, runID string) (*GraphRun, error) {
	data, err := os.ReadFile(graphRunPath(session, runID))
	if err != nil {
		return nil, err
	}
	var run GraphRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// WriteGraphRun persists run metadata, refreshing UpdatedAt.
func WriteGraphRun(session string, run *GraphRun) error {
	run.UpdatedAt = time.Now().Unix()
	return atomicWriteJSON(graphRunPath(session, run.ID), run)
}

// UpdateGraphRunState sets the run state and persists.
func UpdateGraphRunState(session, runID, state string) error {
	run, err := ReadGraphRun(session, runID)
	if err != nil {
		return err
	}
	run.State = state
	return WriteGraphRun(session, run)
}

// ReadGraphRunGraph reads the frozen graph definition of a run.
func ReadGraphRunGraph(session, runID string) (*Graph, error) {
	return LoadGraphFile(graphDefPath(session, runID))
}

// ListGraphRuns returns all runs for a session, oldest first.
func ListGraphRuns(session string) ([]GraphRun, error) {
	entries, err := os.ReadDir(GraphRunsDir(session))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []GraphRun
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		run, err := ReadGraphRun(session, e.Name())
		if err != nil {
			continue
		}
		runs = append(runs, *run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt < runs[j].CreatedAt })
	return runs, nil
}

// ScanInFlightGraphRuns returns runs still in the running state — the
// daemon calls this on start to resume execution from persisted state.
func ScanInFlightGraphRuns(session string) []GraphRun {
	runs, err := ListGraphRuns(session)
	if err != nil {
		return nil
	}
	var inflight []GraphRun
	for _, r := range runs {
		if r.State == GraphRunRunning {
			inflight = append(inflight, r)
		}
	}
	return inflight
}

// ReadNodeStatus reads one node's persisted status.
func ReadNodeStatus(session, runID, nodeID string) (*GraphNodeStatus, error) {
	data, err := os.ReadFile(graphNodePath(session, runID, nodeID))
	if err != nil {
		return nil, err
	}
	var st GraphNodeStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ReadAllNodeStatuses reads every node status of a run, keyed by node id.
func ReadAllNodeStatuses(session, runID string) (map[string]*GraphNodeStatus, error) {
	entries, err := os.ReadDir(graphNodesDir(session, runID))
	if err != nil {
		return nil, err
	}
	statuses := make(map[string]*GraphNodeStatus, len(entries))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		nodeID := strings.TrimSuffix(e.Name(), ".json")
		st, err := ReadNodeStatus(session, runID, nodeID)
		if err != nil {
			continue
		}
		statuses[nodeID] = st
	}
	return statuses, nil
}

// TransitionGraphNode moves a node to newState after checking legality
// against the persisted current state, applies the optional mutate hook
// (outcome, output, task correlation), stamps timestamps, and persists
// atomically. Returns an error on an illegal transition.
func TransitionGraphNode(session, runID, nodeID, newState string, mutate func(*GraphNodeStatus)) error {
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		return err
	}
	if !legalNodeTransitions[st.State][newState] {
		return fmt.Errorf("illegal node transition %s: %s -> %s", nodeID, st.State, newState)
	}

	st.State = newState
	now := time.Now().Unix()
	st.UpdatedAt = now
	switch newState {
	case GraphNodeRunning:
		st.StartedAt = now
	case GraphNodeDone, GraphNodeFailed:
		st.DoneAt = now
	case GraphNodeReady:
		// Re-arm for a loop iteration or retry: clear the previous pass.
		st.Outcome = ""
		st.Output = ""
		st.TaskID = ""
		st.Routed = false
		st.StartedAt = 0
		st.DoneAt = 0
		st.Redrives = 0
		st.LastRedrive = 0
	}
	if mutate != nil {
		mutate(st)
	}
	return atomicWriteJSON(graphNodePath(session, runID, nodeID), st)
}

// MutateNodeStatus applies a mutation that is not a state change (e.g.
// setting the Routed flag on an already-terminal node) and persists
// atomically. State changes must go through TransitionGraphNode so the
// legality gate holds.
func MutateNodeStatus(session, runID, nodeID string, mutate func(*GraphNodeStatus)) error {
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		return err
	}
	mutate(st)
	st.UpdatedAt = time.Now().Unix()
	return atomicWriteJSON(graphNodePath(session, runID, nodeID), st)
}

// GraphRetryResult reports what a retry actually did: the node the
// caller asked to resume from, where the run actually resumes, and —
// when the two differ — the satisfied wait_human gate(s) whose stale
// approvals forced the re-target, each with its original approval time.
type GraphRetryResult struct {
	Requested string      // --from node the caller named
	From      string      // where the run actually resumes (comma-joined when several gates re-arm)
	Rearmed   []GateRearm // the re-armed gates, empty on a plain retry
}

// GateRearm is one re-armed gate: its id and the purged approval's
// original grant time (unix; 0 when unreadable).
type GateRearm struct {
	Gate       string
	ApprovedAt int64
}

// RetryGraphRun re-executes a run from the named node: the node re-arms
// to ready, every node downstream of it resets to pending with its
// previous pass cleared, and edge-fire counts inside the retried subtree
// reset so capped loops get a fresh budget. Upstream results are kept —
// they are exactly what retry promises not to re-run. Statuses are reset
// by direct write rather than TransitionGraphNode: retry is an
// administrative reset spanning states the legality gate rightly forbids
// during execution.
//
// Stale-approval cut (MUX-132): a target covered by satisfied wait_human
// gates outside the reset set does not start there — the retry re-targets
// to those gates, purges their stale approvals, and normal dispatch
// demands fresh ones before anything downstream fires. Without this,
// retry --from below a gate consumed an approval granted for different
// content (observed 2026-08-31: the tree changed between approval and
// retry). Re-arming beats refusing because the safe path is then the
// default path, not a second command; the re-target is never silent —
// it is named in the result, a lifecycle event, and the run's RetryNote.
// The re-arm set is the NEAREST-GATE CUT — every satisfied gate whose
// gate-free territory contains the target (gateTerritory, the walk
// validateGateText shares). Single-dominator selection is not enough:
// on parallel gated branches no one gate dominates every path, yet each
// branch's approval covers the target — skipping the cut leaves every
// one of those stale approvals usable (review finding, 2026-08-31).
// Territory membership makes each re-armed gate the last gate on some
// path to the target, so nearer gates never hide behind outer ones and
// outer gates never re-run work an inner approval already covers. Nodes
// between a gate and the original target reset with everything else
// downstream of the gate: they are territory the purged approval
// covered, and routing re-arms finished targets when the gate's edges
// re-fire regardless — preserving them would take a cached-result node
// semantic that does not exist. In shipped templates gates sit directly
// before their mutations and expensive spawns sit above the gates, so
// nothing costly re-runs.
//
// A running run must be canceled first — resetting nodes under a live
// executor would race it.
//
// Purging a stale gate approval fails CLOSED (PR #56 review): claiming
// "purged" while os.Remove failed would leave the marker to satisfy the
// re-armed gate — the laundered approval MUX-132 closed. The purge runs
// before any store write, so refusing leaves the run untouched.
func RetryGraphRun(session, runID, fromNode string) (*GraphRetryResult, error) {
	run, err := ReadGraphRun(session, runID)
	if err != nil {
		return nil, err
	}
	if run.State == GraphRunRunning {
		return nil, fmt.Errorf("run %s is still running — cancel it before retrying", runID)
	}
	g, err := ReadGraphRunGraph(session, runID)
	if err != nil {
		return nil, err
	}
	if _, err := ReadNodeStatus(session, runID, fromNode); err != nil {
		return nil, fmt.Errorf("unknown node %q: %w", fromNode, err)
	}

	res := &GraphRetryResult{Requested: fromNode, From: fromNode}
	byID := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}
	downstream := retryDownstream(g, fromNode)

	resume := map[string]bool{fromNode: true}
	if rearms := staleApprovalGates(session, runID, g, byID, fromNode, downstream); len(rearms) > 0 {
		res.Rearmed = rearms
		resume = map[string]bool{}
		var names, noted []string
		for _, r := range rearms {
			resume[r.Gate] = true
			names = append(names, r.Gate)
			noted = append(noted, fmt.Sprintf("%q (approved %s)", r.Gate, FormatApprovalTime(r.ApprovedAt)))
			for id := range retryDownstream(g, r.Gate) {
				downstream[id] = true
			}
			// Purge must fail closed — see the doc comment.
			if err := os.Remove(graphApprovalPath(session, runID, r.Gate, "approved")); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("retry --from %s: cannot purge stale approval for gate %q: %w", fromNode, r.Gate, err)
			}
			LogLifecycle(session, "warn", "daemon", "graph-retry-regated",
				fmt.Sprintf("%s: --from %s is covered by satisfied gate %s (approved %s) — re-armed the gate, stale approval purged",
					runID, res.Requested, r.Gate, FormatApprovalTime(r.ApprovedAt)))
		}
		res.From = strings.Join(names, ",")
		run.RetryNote = fmt.Sprintf("retry --from %s re-armed at gate(s) %s — the approvals predate the retry; fresh approval required",
			res.Requested, strings.Join(noted, ", "))
	} else {
		run.RetryNote = ""
	}

	now := time.Now().Unix()
	for id := range downstream {
		state := GraphNodePending
		if resume[id] {
			state = GraphNodeReady
		}
		st := &GraphNodeStatus{NodeID: id, State: state, UpdatedAt: now}
		if err := atomicWriteJSON(graphNodePath(session, runID, id), st); err != nil {
			return nil, err
		}
	}
	for _, e := range g.Edges {
		if downstream[e.From] {
			delete(run.EdgeFires, EdgeFireKey(e))
		}
	}
	run.State = GraphRunRunning
	return res, WriteGraphRun(session, run)
}

// retryDownstream returns everything reachable from fromNode, itself
// included — the reset set of a retry.
func retryDownstream(g *Graph, fromNode string) map[string]bool {
	out := g.outgoing()
	downstream := map[string]bool{fromNode: true}
	queue := []string{fromNode}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, i := range out[cur] {
			to := g.Edges[i].To
			if !downstream[to] {
				downstream[to] = true
				queue = append(queue, to)
			}
		}
	}
	return downstream
}

// staleApprovalGates finds every wait_human gate whose prior approval a
// retry from target would silently consume: satisfied (done/success),
// outside the reset set (a gate inside it re-arms naturally), and with
// the target inside its gate-free territory — the nearest-gate cut.
// Territory membership, not single-gate dominance: on parallel gated
// branches no one gate dominates every path, yet each branch's stale
// approval covers the target — see RetryGraphRun.
func staleApprovalGates(session, runID string, g *Graph, byID map[string]*Node, target string, reset map[string]bool) []GateRearm {
	var rearms []GateRearm
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != NodeWaitHuman || reset[n.ID] {
			continue
		}
		st, err := ReadNodeStatus(session, runID, n.ID)
		if err != nil || st.State != GraphNodeDone || st.Outcome != OutcomeSuccess {
			continue
		}
		if g.gateTerritory(byID, n.ID)[target] {
			rearms = append(rearms, GateRearm{Gate: n.ID, ApprovedAt: gateApprovalTime(session, runID, n.ID)})
		}
	}
	return rearms
}

// gateApprovalTime reads a satisfied gate's original approval time: the
// approved marker's grant timestamp, falling back to the node's DoneAt
// when the marker is unreadable.
func gateApprovalTime(session, runID, gateID string) int64 {
	if data, err := os.ReadFile(graphApprovalPath(session, runID, gateID, "approved")); err == nil {
		var m struct {
			ApprovedAt int64 `json:"approved_at"`
		}
		if json.Unmarshal(data, &m) == nil && m.ApprovedAt > 0 {
			return m.ApprovedAt
		}
	}
	if st, err := ReadNodeStatus(session, runID, gateID); err == nil {
		return st.DoneAt
	}
	return 0
}

// FormatApprovalTime renders an approval unix time for user-facing
// retry output, degrading honestly when the time is unknown. Exported
// for the retry CLI and TUI notices, which state the re-target with
// the same wording the run store logs.
func FormatApprovalTime(t int64) string {
	if t <= 0 {
		return "unknown time"
	}
	return time.Unix(t, 0).Format("2006-01-02 15:04:05")
}

// ConditionTookBranch reports whether a node finished by selecting its
// false branch. A condition's failure OUTCOME is the routing key that
// edgeOutcome matches, so it is retained; what distinguishes a branch
// from a break is the terminal STATE, which a branch-taking condition
// leaves as done (MUX-133 option B). Keying on outcome alone would
// re-classify a genuine evaluation error as a branch, and keying on
// state alone would match every completed condition including the true
// branch — both halves are load-bearing. Shared by the CLI formatter,
// the TUI and the JSON surface so they cannot drift.
func ConditionTookBranch(nodeType, state, outcome string) bool {
	return nodeType == NodeCondition && state == GraphNodeDone && outcome == OutcomeFailure
}

// GraphNodeStateColor returns the Dracula color for a node run state,
// matching the console palette.
func GraphNodeStateColor(state string) string {
	switch state {
	case GraphNodeDone:
		return ColorGreen
	case GraphNodeFailed:
		return ColorRed
	case GraphNodeRunning:
		return ColorCyan
	case GraphNodeWaiting:
		return ColorYellow
	case GraphNodeReady:
		return ColorPurple
	case GraphNodeSkipped:
		return ColorDim
	default: // pending
		return ColorDim
	}
}

// FormatGraphRun renders a run for `muxcode graph status`: header line
// plus one row per node in definition order. Colored adds Dracula state
// colors for terminal display; plain output stays machine-greppable.
func FormatGraphRun(run *GraphRun, g *Graph, statuses map[string]*GraphNodeStatus) string {
	return formatGraphRun(run, g, statuses, false)
}

// FormatGraphRunColored is FormatGraphRun with Dracula state colors.
func FormatGraphRunColored(run *GraphRun, g *Graph, statuses map[string]*GraphNodeStatus) string {
	return formatGraphRun(run, g, statuses, true)
}

func formatGraphRun(run *GraphRun, g *Graph, statuses map[string]*GraphNodeStatus, colored bool) string {
	var b strings.Builder
	elapsed := time.Since(time.Unix(run.CreatedAt, 0)).Round(time.Second)
	fmt.Fprintf(&b, "Run %s  [%s]  template=%s  elapsed=%s\n", run.ID, run.State, run.Template, elapsed)
	if run.Intent != "" {
		fmt.Fprintf(&b, "Intent: %s\n", run.Intent)
	}
	if run.RetryNote != "" {
		fmt.Fprintf(&b, "Retry: %s\n", run.RetryNote)
	}
	for _, n := range g.Nodes {
		st := statuses[n.ID]
		if st == nil {
			fmt.Fprintf(&b, "  %-16s %-10s (no status)\n", n.ID, "?")
			continue
		}
		stateWord, stateColor := st.State, GraphNodeStateColor(st.State)
		if ConditionTookBranch(n.Type, st.State, st.Outcome) {
			stateWord, stateColor = "branched", ColorDim
		}
		state := fmt.Sprintf("%-10s", stateWord)
		if colored {
			state = stateColor + state + ColorReset
		}
		row := fmt.Sprintf("  %-16s %s %s", n.ID, state, n.Type)
		if st.Outcome != "" {
			row += "  outcome=" + st.Outcome
		}
		if st.DoneAt > 0 && st.StartedAt > 0 {
			row += fmt.Sprintf("  took=%ds", st.DoneAt-st.StartedAt)
		}
		b.WriteString(row + "\n")
		if st.State == GraphNodeFailed && st.Output != "" {
			// a decline's count+names must be operator-visible (MUX-114)
			fmt.Fprintf(&b, "  %-16s ↳ %s\n", "", truncate(st.Output, 200))
		}
	}
	return b.String()
}
