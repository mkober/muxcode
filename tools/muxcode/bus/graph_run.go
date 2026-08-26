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
type GraphRun struct {
	ID        string         `json:"id"`
	Template  string         `json:"template"`
	Intent    string         `json:"intent,omitempty"`
	State     string         `json:"state"`
	CreatedAt int64          `json:"created_at"`
	UpdatedAt int64          `json:"updated_at"`
	EdgeFires map[string]int `json:"edge_fires,omitempty"`
}

// GraphNodeStatus is the persisted per-node execution state of a run.
// Routed records that a finished node's outgoing edges have been fired,
// so a tick (or a resume after a crash) never routes the same completion
// twice.
type GraphNodeStatus struct {
	NodeID    string `json:"node_id"`
	State     string `json:"state"`
	Outcome   string `json:"outcome,omitempty"` // success/failure/custom, set when finished
	Output    string `json:"output,omitempty"`  // harvested response payload
	TaskID    string `json:"task_id,omitempty"` // correlated tracked task or spawn id(s)
	Routed    bool   `json:"routed,omitempty"`
	StartedAt int64  `json:"started_at,omitempty"`
	DoneAt    int64  `json:"done_at,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

// legalNodeTransitions defines the allowed node state machine. done/failed/
// skipped → ready re-arms a node for loop edges and retry --from; every
// other terminal-to-anything move is rejected so a crashed executor cannot
// corrupt run history on resume.
var legalNodeTransitions = map[string]map[string]bool{
	GraphNodePending: {GraphNodeReady: true, GraphNodeSkipped: true},
	GraphNodeReady:   {GraphNodeRunning: true, GraphNodeWaiting: true, GraphNodeSkipped: true},
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

// RetryGraphRun re-executes a run from the named node: the node re-arms
// to ready, every node downstream of it resets to pending with its
// previous pass cleared, and edge-fire counts inside the retried subtree
// reset so capped loops get a fresh budget. Upstream results are kept —
// they are exactly what retry promises not to re-run. Statuses are reset
// by direct write rather than TransitionGraphNode: retry is an
// administrative reset spanning states the legality gate rightly forbids
// during execution.
//
// A running run must be canceled first — resetting nodes under a live
// executor would race it.
func RetryGraphRun(session, runID, fromNode string) error {
	run, err := ReadGraphRun(session, runID)
	if err != nil {
		return err
	}
	if run.State == GraphRunRunning {
		return fmt.Errorf("run %s is still running — cancel it before retrying", runID)
	}
	g, err := ReadGraphRunGraph(session, runID)
	if err != nil {
		return err
	}
	if _, err := ReadNodeStatus(session, runID, fromNode); err != nil {
		return fmt.Errorf("unknown node %q: %w", fromNode, err)
	}

	// Downstream set: everything reachable from fromNode, itself included.
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

	now := time.Now().Unix()
	for id := range downstream {
		state := GraphNodePending
		if id == fromNode {
			state = GraphNodeReady
		}
		st := &GraphNodeStatus{NodeID: id, State: state, UpdatedAt: now}
		if err := atomicWriteJSON(graphNodePath(session, runID, id), st); err != nil {
			return err
		}
	}
	for _, e := range g.Edges {
		if downstream[e.From] {
			delete(run.EdgeFires, EdgeFireKey(e))
		}
	}
	run.State = GraphRunRunning
	return WriteGraphRun(session, run)
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
	for _, n := range g.Nodes {
		st := statuses[n.ID]
		if st == nil {
			fmt.Fprintf(&b, "  %-16s %-10s (no status)\n", n.ID, "?")
			continue
		}
		state := fmt.Sprintf("%-10s", st.State)
		if colored {
			state = GraphNodeStateColor(st.State) + state + ColorReset
		}
		row := fmt.Sprintf("  %-16s %s %s", n.ID, state, n.Type)
		if st.Outcome != "" {
			row += "  outcome=" + st.Outcome
		}
		if st.DoneAt > 0 && st.StartedAt > 0 {
			row += fmt.Sprintf("  took=%ds", st.DoneAt-st.StartedAt)
		}
		b.WriteString(row + "\n")
	}
	return b.String()
}
