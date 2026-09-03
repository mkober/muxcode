package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Graph executor — the daemon's checkGraphRuns() tick calls StepGraphRuns
// each poll; everything here derives from the persisted run store, so a
// daemon restart resumes runs exactly where the store says they are.
//
// Outcome model (honors the console-history provenance doctrine): an
// authoritative history row (real exit code) for the target role is the
// only evidence of success; a synthesized failure (response action
// "error", task timeout) routes failure; anything else is "unknown".
// An unknown outcome routes an explicit "unknown" edge when one exists,
// and otherwise falls back to the success edge with a lifecycle note —
// non-hook providers rarely produce authoritative rows, and stalling
// every run on them would make graphs unusable there.

// graphSender is the bus identity graph-originated sends carry.
// NormalizeBusRole maps it to edit for reply routing, same as other
// daemon-originated traffic.
const graphSender = "daemon"

// graphSpawnFn starts a FRESH worker for spawn/map nodes and stamps it
// with the run+node reuse key. Package variable so unit tests can run
// graphs without tmux — StartSpawn creates real tmux windows. Reuse of an
// existing worker is acquireSpawnWorker's job, deliberately outside this
// seam so tests that stub it still exercise the reuse decision. The key
// is stamped after StartSpawn returns so its CLI-shared signature stays
// put; a failed stamp only disables reuse for this one worker, degrading
// to the pre-MUX-131 fresh-start-per-iteration behavior.
var graphSpawnFn = func(session, role, task, owner, runID, nodeID string) (string, error) {
	// Ownership rides the entry from birth (StartSpawnOwned) — a
	// post-creation stamp had a race window and a swallowed error path.
	entry, err := StartSpawnOwned(session, role, task, owner, true, runID, nodeID)
	if err != nil {
		return "", err
	}
	return entry.SpawnRole, nil
}

// acquireSpawnWorker reuses the run+node's live worker when one exists,
// falling back to a fresh spawn (MUX-131 Defect B: an unconditional
// StartSpawn built a new worker per loop re-entry — three workers, one
// task, one run — discarding each predecessor's context and re-paying
// boot cost). A reseed failure also falls back: a fresh worker beats a
// wedged node.
func acquireSpawnWorker(session, runID, nodeID, role, task string) (string, error) {
	if entry, ok := FindLiveSpawn(session, runID, nodeID); ok {
		_, err := ReseedSpawn(session, entry, task)
		if err == nil {
			LogLifecycle(session, "info", "daemon", "graph-spawn-reuse",
				fmt.Sprintf("%s: %s reused worker %s", runID, nodeID, entry.SpawnRole))
			return entry.SpawnRole, nil
		}
		LogLifecycle(session, "warn", "daemon", "graph-spawn-reuse-failed",
			fmt.Sprintf("%s: %s reseed of %s failed (%v) — starting fresh", runID, nodeID, entry.SpawnRole, err))
	}
	return graphSpawnFn(session, role, task, graphSender, runID, nodeID)
}

// graphApprovalsDir holds wait_human gate markers for a run:
// <node>.pending while the gate blocks, <node>.approved once released.
func graphApprovalsDir(session, runID string) string {
	return filepath.Join(GraphRunDir(session, runID), "approvals")
}

func graphApprovalPath(session, runID, nodeID, state string) string {
	return filepath.Join(graphApprovalsDir(session, runID), nodeID+"."+state)
}

// ApproveGraphGate releases a wait_human gate. The next executor tick
// moves the node from waiting to done.
func ApproveGraphGate(session, runID, nodeID string) error {
	if _, err := ReadNodeStatus(session, runID, nodeID); err != nil {
		return fmt.Errorf("unknown run/node: %w", err)
	}
	if err := os.MkdirAll(graphApprovalsDir(session, runID), 0755); err != nil {
		return err
	}
	return atomicWriteJSON(graphApprovalPath(session, runID, nodeID, "approved"),
		map[string]int64{"approved_at": time.Now().Unix()})
}

// CancelGraphRun marks a run canceled and skips every node that has not
// started. Running nodes are left to finish; the canceled run state stops
// all further routing and dispatch.
func CancelGraphRun(session, runID string) error {
	if err := UpdateGraphRunState(session, runID, GraphRunCanceled); err != nil {
		return err
	}
	PurgeSessionArtifacts(session, "graph run "+runID+" canceled")
	statuses, err := ReadAllNodeStatuses(session, runID)
	if err != nil {
		return err
	}
	for id, st := range statuses {
		switch st.State {
		case GraphNodePending, GraphNodeReady, GraphNodeWaiting:
			_ = TransitionGraphNode(session, runID, id, GraphNodeSkipped, nil)
		}
		// Expire the node's correlated task: a canceled run's in-flight
		// task otherwise lingers, and the stall watchdog re-drives its
		// request into an agent for work nobody wants anymore (observed
		// live 2026-08-27: the canceled loop's edit node re-driven).
		if st.TaskID != "" {
			if task, err := ReadTask(session, st.TaskID); err == nil && task.Status == TaskInFlight {
				TimeoutTask(session, st.TaskID)
			}
		}
	}
	LogLifecycle(session, "info", "daemon", "graph-run-canceled", runID)
	return nil
}

// StepGraphRuns advances every in-flight run one tick.
func StepGraphRuns(session string) {
	for _, run := range ScanInFlightGraphRuns(session) {
		if err := StepGraphRun(session, run.ID); err != nil {
			LogLifecycle(session, "warn", "daemon", "graph-step-error",
				fmt.Sprintf("%s: %v", run.ID, err))
		}
	}
}

// StepGraphRun advances one run a single tick: harvest completions of
// running/waiting nodes, route unrouted finished nodes, dispatch ready
// nodes, then settle the run state if nothing is active.
func StepGraphRun(session, runID string) error {
	run, err := ReadGraphRun(session, runID)
	if err != nil {
		return err
	}
	if run.State != GraphRunRunning {
		return nil
	}
	g, err := ReadGraphRunGraph(session, runID)
	if err != nil {
		return err
	}
	byID := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}

	statuses, err := ReadAllNodeStatuses(session, runID)
	if err != nil {
		return err
	}

	for id, st := range statuses {
		n := byID[id]
		if n == nil {
			continue
		}
		switch st.State {
		case GraphNodeRunning:
			harvestRunningNode(session, run, n, st)
		case GraphNodeWaiting:
			harvestWaitingNode(session, run, n, st)
		}
	}

	// Re-read after harvest so routing sees fresh terminal states.
	statuses, err = ReadAllNodeStatuses(session, runID)
	if err != nil {
		return err
	}
	routeFinishedNodes(session, run, g, byID, statuses)

	// Routing may have failed the run (failure with no live edge) — a
	// failed run dispatches nothing further.
	if fresh, err := ReadGraphRun(session, runID); err != nil || fresh.State != GraphRunRunning {
		return err
	}

	statuses, err = ReadAllNodeStatuses(session, runID)
	if err != nil {
		return err
	}
	for id, st := range statuses {
		if st.State == GraphNodeReady {
			dispatchNode(session, run, g, byID[id], st)
		}
	}

	settleRun(session, run)
	return nil
}

// guardAllowsDispatch evaluates a node's dispatch-time guard: true means
// dispatch proceeds; a decline finishes the node as failed so the run
// stops before any downstream mutation. Why guards are daemon-side: see
// knownNodeGuards.
func guardAllowsDispatch(session string, run *GraphRun, g *Graph, n *Node) bool {
	switch n.Guard {
	case GuardSpecComplete:
		return specCompleteGuardAllows(session, run, n)
	case GuardPhaseComplete:
		return phaseCompleteGuardAllows(session, run, n)
	case GuardPhaseProgress:
		return phaseProgressGuardAllows(session, run, g, n)
	}
	return true
}

// activeSpecFile resolves the active spec pointer to an absolute path
// through ResolveSpecPath, the single pointer boundary — the pointer is
// agent-written data, and every caller here goes on to read the file it
// names, so a pointer that cannot be proven inside the repo must never
// resolve (review must-fix 2026-09-01: absolute pointers used to be
// followed unconditionally, bypassing the boundary entirely).
//
// The four states are distinct and none may collapse into another:
// ok=true resolves; transient=true means the repo dir was unresolvable
// this tick, so containment is unprovable either way (caller postpones —
// the node stays ready to retry); refused=true means the repo dir IS
// known and the pointer resolves outside it (caller fails loudly — a
// refused pointer reading as "unset" would make the close-spec guard
// pass through and close out against nothing); all-false means no
// active spec is set.
func activeSpecFile(session string) (path string, ok, transient, refused bool) {
	specRel := ReadActiveSpec(session)
	if specRel == "" {
		return "", false, false, false
	}
	repo := SessionRepoDir(session)
	if repo == "" {
		return "", false, true, false
	}
	full := ResolveSpecPath(repo, specRel)
	if full == "" {
		return "", false, false, true
	}
	return full, true, false, false
}

// specCompleteGuardAllows blocks dispatch while the active spec has ANY
// open checkbox items. No active spec passes through — blocking there
// would make the node inert (MUX-114 criterion). A set-but-unreadable
// spec declines loudly: closing out against an unreadable spec is as
// wrong as closing an open one.
func specCompleteGuardAllows(session string, run *GraphRun, n *Node) bool {
	path, ok, transient, refused := activeSpecFile(session)
	if transient {
		return false // node stays ready, retried next tick
	}
	if refused {
		finishNode(session, run, n, OutcomeFailure,
			"spec-complete guard: active spec pointer resolves outside the repo — refusing to read it")
		return false
	}
	if !ok {
		return true
	}
	count, names, err := SpecOpenItems(path)
	if err != nil {
		finishNode(session, run, n, OutcomeFailure,
			fmt.Sprintf("spec-complete guard: cannot read active spec: %v", err))
		return false
	}
	if count > 0 {
		declineGuard(session, run, n, fmt.Sprintf("spec-complete guard declined: %d open items: %s",
			count, summarizeOpenItems(names, 5)))
		return false
	}
	return true
}

// phaseCompleteGuardAllows blocks dispatch while the phase named in the
// run's intent still has open items in the active spec. No phase in the
// intent, or no active spec, passes through — the guard scopes a ship to
// what the run claimed to deliver, nothing more (user decision
// 2026-08-28: a full-spec guard would block every partial-phase ship).
func phaseCompleteGuardAllows(session string, run *GraphRun, n *Node) bool {
	phase := IntentPhase(run.Intent)
	if phase == 0 {
		return true
	}
	path, ok, transient, refused := activeSpecFile(session)
	if transient {
		return false // node stays ready, retried next tick
	}
	if refused {
		finishNode(session, run, n, OutcomeFailure,
			"phase-complete guard: active spec pointer resolves outside the repo — refusing to read it")
		return false
	}
	if !ok {
		return true
	}
	count, names, err := SpecPhaseOpenItems(path, phase)
	if err != nil {
		finishNode(session, run, n, OutcomeFailure,
			fmt.Sprintf("phase-complete guard: cannot read active spec: %v", err))
		return false
	}
	if count > 0 {
		declineGuard(session, run, n, fmt.Sprintf("phase-complete guard declined: Phase %d has %d open items: %s",
			phase, count, summarizeOpenItems(names, 5)))
		return false
	}
	return true
}

// phaseProgressGuardAllows blocks a per-phase commit that would ship no
// newly-completed phase: completed phases must exceed the node's own
// prior successful fires (each past commit shipped one phase; this one
// must too). Counting the guard node's success edges — not loop
// iterations — keeps fix-loop and stuck-gate retries out of the math: a
// phase that needed two build fixes or a gate-approved retry still
// commits once its phase closes (review catch 2026-08-28: an
// iteration-max count inflated by the fix loop declined healthy phases).
// The failure edge routes to the stuck gate, so a decline is the
// gate-and-ask trigger, not a dead end (MUX-121 decision 4). No active
// spec declines — never commit blind; transient repo-dir postpones.
func phaseProgressGuardAllows(session string, run *GraphRun, g *Graph, n *Node) bool {
	path, ok, transient, refused := activeSpecFile(session)
	if transient {
		return false // node stays ready, retried next tick
	}
	if refused {
		finishNode(session, run, n, OutcomeFailure,
			"phase-progress guard: active spec pointer resolves outside the repo — refusing to read it")
		return false
	}
	if !ok {
		declineGuard(session, run, n, "phase-progress guard declined: no active spec to verify the phase against")
		return false
	}
	completed, err := SpecCompletedPhaseCount(path)
	if err != nil {
		finishNode(session, run, n, OutcomeFailure,
			fmt.Sprintf("phase-progress guard: cannot read active spec: %v", err))
		return false
	}
	// max, not sum: every success edge fires together on one completion,
	// so summing counts each shipped commit once per edge and a fan-out
	// commit node would overstate its history (PR #50 Copilot).
	prior := 0
	for _, e := range g.Edges {
		if e.From == n.ID && edgeOutcome(e) == OutcomeSuccess {
			prior = max(prior, run.EdgeFires[EdgeFireKey(e)])
		}
	}
	if completed < prior+1 {
		declineGuard(session, run, n, fmt.Sprintf(
			"phase-progress guard declined: %d commits shipped but only %d phases complete — this commit's phase is still open", prior, completed))
		return false
	}
	return true
}

// declineGuard records a guard decline: lifecycle event plus the failed
// node carrying the reason.
func declineGuard(session string, run *GraphRun, n *Node, detail string) {
	LogLifecycle(session, "warn", "daemon", "graph-guard-declined",
		fmt.Sprintf("%s: %s — %s", run.ID, n.ID, detail))
	finishNode(session, run, n, OutcomeFailure, detail)
}

// summarizeOpenItems joins item names for a decline message, truncating
// past limit so the detail line stays bounded.
func summarizeOpenItems(names []string, limit int) string {
	if len(names) <= limit {
		return strings.Join(names, "; ")
	}
	return strings.Join(names[:limit], "; ") + fmt.Sprintf("; … +%d more", len(names)-limit)
}

// interpolateGraphMessage expands run/worker placeholders in a node
// message template. ${current_phase} resolves from the active spec at
// dispatch time, not from the frozen intent — the frozen "Phase N" string
// is what made three runs re-implement a completed phase (MUX-121).
func interpolateGraphMessage(session, msg, intent, item string) string {
	msg = strings.ReplaceAll(msg, "${intent}", intent)
	if item != "" {
		msg = strings.ReplaceAll(msg, "${item}", item)
	}
	if strings.Contains(msg, "${current_phase}") {
		msg = strings.ReplaceAll(msg, "${current_phase}", resolveCurrentPhaseText(session))
	}
	if strings.Contains(msg, "${completed_phase}") {
		msg = strings.ReplaceAll(msg, "${completed_phase}", resolveCompletedPhaseText(session))
	}
	return msg
}

// graphOwnedRoles lists the send-node roles reachable from the worker's node,
// in node order and de-duplicated, so a worker is told which delegations the
// graph will make on its behalf.
//
// Reachability rather than the whole graph: only nodes downstream of this
// worker are its succession. A send on a branch the worker can never reach is
// not work the graph is about to do for it, and naming it would forbid a
// delegation nothing was going to duplicate.
func graphOwnedRoles(g *Graph, fromNode string) []string {
	if g == nil {
		return nil
	}
	reachable := reachableNodes(g, fromNode)
	seen := map[string]bool{}
	var roles []string
	for _, n := range g.Nodes {
		if n.Type != NodeSend || n.Role == "" || seen[n.Role] || !reachable[n.ID] {
			continue
		}
		seen[n.Role] = true
		roles = append(roles, n.Role)
	}
	return roles
}

// reachableNodes returns the set of node ids reachable from start by following
// edges, excluding start itself.
func reachableNodes(g *Graph, start string) map[string]bool {
	out := map[string][]string{}
	for _, e := range g.Edges {
		out[e.From] = append(out[e.From], e.To)
	}
	seen := map[string]bool{}
	queue := append([]string{}, out[start]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, out[id]...)
	}
	return seen
}

// graphWorkerTask prefixes a spawn worker's task with the graph's ownership
// of the surrounding nodes.
//
// A worker launches on its base role's definition, which for "edit" tells it
// to delegate build→test→review after making changes, and nothing in the
// launch says it is a graph node — run ownership lives in the spawn registry,
// which the worker never reads. It therefore drove a second ungated pipeline
// beside the graph's parked nodes (live: run 1788405573-spec-to-pr-8281a147).
// The task message is the only channel into the worker's context, so the
// contradiction is stated there. CheckGraphNodeAuthority enforces it.
func graphWorkerTask(g *Graph, runID, nodeID, msg string) string {
	roles := graphOwnedRoles(g, nodeID)
	if len(roles) == 0 {
		return msg
	}
	owned := strings.Join(roles, ", ")
	return fmt.Sprintf(
		"[graph run %s · node %s] The graph owns the rest of this pipeline: %s run as separate nodes AFTER you report. "+
			"Do NOT delegate them (no `muxcode send %s ...`) — a self-delegated chain races the graph and runs in the wrong working directory. "+
			"Do the work below, reply to the requester, and stop.\n\n%s",
		runID, nodeID, owned, strings.Join(roles, "|"), msg)
}

// resolveCompletedPhaseText expands ${completed_phase}: the completion
// frontier the commit ships — see SpecJustCompletedPhase for why the
// commit must not use ${current_phase}.
func resolveCompletedPhaseText(session string) string {
	path, ok, transient, _ := activeSpecFile(session)
	if transient {
		return "(completed phase unresolved — repo dir unavailable this tick)"
	}
	if !ok {
		// refused folds in: a pointer outside the repo yields no phase text.
		return "(no completed phase)"
	}
	p, err := SpecJustCompletedPhase(path)
	if err != nil || p.Number == 0 {
		return "(no completed phase)"
	}
	return p.Title
}

// resolveCurrentPhaseText expands ${current_phase}: the active spec's
// lowest open phase heading, or explicit degradation text — honest words
// beat a leftover placeholder in an agent-facing message, and a transient
// repo-dir failure must not masquerade as "no open phase".
func resolveCurrentPhaseText(session string) string {
	path, ok, transient, _ := activeSpecFile(session)
	if transient {
		return "(current phase unresolved — repo dir unavailable this tick)"
	}
	if !ok {
		// refused folds in: a pointer outside the repo yields no phase text.
		return "(no open phase)"
	}
	p, err := SpecCurrentPhase(path)
	if err != nil || p.Number == 0 {
		return "(no open phase)"
	}
	return p.Title
}

// purgeStaleApproval removes a gate's approved marker left by a previous
// pass (graph retry --from, or a loop edge re-arming the gate) so a fresh
// pass demands a fresh approval — a surviving marker would let
// harvestWaitingNode release the gate instantly, a retried run sailing
// through its human gate with nobody approving (MUX-132). Fails closed
// like RetryGraphRun's purge: on an unremovable marker the caller must
// fail the node, never arm a gate the stale approval can release.
func purgeStaleApproval(session, runID, nodeID string) error {
	err := os.Remove(graphApprovalPath(session, runID, nodeID, "approved"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// dispatchNode fires a ready node's work and moves it to running/waiting.
//
// A suppressed send means the identical request is already queued or in
// flight (loop re-entry or a retry racing the prior pass), so the node
// adopts the existing work instead of failing. The adopted task must be
// keyed to the QUEUED duplicate's message ID, not the unsent one — the
// agent answers the queued id, and a task keyed to the unsent id sits
// in-flight forever (PR #38 Copilot finding).
func dispatchNode(session string, run *GraphRun, g *Graph, n *Node, st *GraphNodeStatus) {
	if n == nil {
		return
	}
	if !guardAllowsDispatch(session, run, g, n) {
		return
	}
	LogLifecycle(session, "info", "daemon", "graph-node-start",
		fmt.Sprintf("%s: %s (%s)", run.ID, n.ID, n.Type))

	switch n.Type {
	case NodeSend:
		msg := interpolateGraphMessage(session, n.Message, run.Intent, "")
		m := NewMessage(graphSender, n.Role, "request", n.Action, msg, "")
		if err := SendNoCC(session, m); err != nil {
			if errors.Is(err, ErrSendSuppressed) {
				taskID := m.ID // suppressed = duplicate exists; adopt it — see doc comment
				if t, found := FindInFlightTask(session, n.Role, n.Action); found {
					taskID = t.ID
				} else if pm, found := FindPendingInboxRequest(session, m.To, m.From, m.Action, m.Payload); found {
					adopted := m // keyed to the queued duplicate's id — see doc comment
					adopted.ID = pm.ID
					adopted.TS = pm.TS
					taskID = pm.ID
					_ = CreateTask(session, adopted, nodeTimeoutSecs(n))
				} else {
					_ = CreateTask(session, m, nodeTimeoutSecs(n))
				}
				_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, func(s *GraphNodeStatus) {
					s.TaskID = taskID
				})
				return
			}
			finishNode(session, run, n, OutcomeFailure, "send failed: "+err.Error())
			return
		}
		_ = CreateTask(session, m, nodeTimeoutSecs(n))
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, func(s *GraphNodeStatus) {
			s.TaskID = m.ID
		})

	case NodeSpawn:
		msg := graphWorkerTask(g, run.ID, n.ID,
			interpolateGraphMessage(session, n.Message, run.Intent, ""))
		spawnID, err := acquireSpawnWorker(session, run.ID, n.ID, n.Role, msg)
		if err != nil {
			finishNode(session, run, n, OutcomeFailure, "spawn failed: "+err.Error())
			return
		}
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, func(s *GraphNodeStatus) {
			s.TaskID = spawnID
		})

	case NodeMap:
		// One worker per literal item; reuse keyed per item index so distinct members never share a worker.
		items := splitMapItems(n.Items)
		if len(items) == 0 {
			finishNode(session, run, n, OutcomeFailure, "map node has no items")
			return
		}
		var ids []string
		for i, item := range items {
			nodeKey := fmt.Sprintf("%s#%d", n.ID, i)
			msg := graphWorkerTask(g, run.ID, n.ID,
				interpolateGraphMessage(session, n.Message, run.Intent, item))
			spawnID, err := acquireSpawnWorker(session, run.ID, nodeKey, n.Role, msg)
			if err != nil {
				finishNode(session, run, n, OutcomeFailure, "map spawn failed: "+err.Error())
				return
			}
			ids = append(ids, spawnID)
		}
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, func(s *GraphNodeStatus) {
			s.TaskID = strings.Join(ids, ",")
		})

	case NodeCondition:
		ctx := &ChainContext{Session: session, Output: predecessorOutput(session, run, g, n.ID)}
		passed, results := EvaluateConditions(n.Conditions, ctx)
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, nil)
		// See unevaluatableCondition: a broken predicate is not a branch.
		if detail := unevaluatableCondition(results); detail != "" {
			finishNode(session, run, n, OutcomeFailure, detail)
			return
		}
		outcome := OutcomeFailure
		if passed {
			outcome = OutcomeSuccess
		}
		// See finishCondition: a branch selection finishes done.
		finishCondition(session, run, n, outcome)

	case NodeJoin:
		// The barrier was satisfied at arming — a join completes immediately.
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, nil)
		finishNode(session, run, n, OutcomeSuccess, "")

	case NodeWaitHuman:
		prompt := interpolateGraphMessage(session, n.Message, run.Intent, "")
		if err := purgeStaleApproval(session, run.ID, n.ID); err != nil {
			finishNode(session, run, n, OutcomeFailure,
				fmt.Sprintf("cannot purge stale approval for gate %q: %v", n.ID, err))
			return
		}
		// The pending marker is informational — a write failure logs, never blocks the gate.
		if err := os.MkdirAll(graphApprovalsDir(session, run.ID), 0755); err != nil {
			LogLifecycle(session, "warn", "daemon", "graph-gate-marker-error",
				fmt.Sprintf("%s: %s: %v", run.ID, n.ID, err))
		} else if err := atomicWriteJSON(graphApprovalPath(session, run.ID, n.ID, "pending"),
			map[string]string{"message": prompt}); err != nil {
			LogLifecycle(session, "warn", "daemon", "graph-gate-marker-error",
				fmt.Sprintf("%s: %s: %v", run.ID, n.ID, err))
		}
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeWaiting, nil)
		LogLifecycle(session, "info", "daemon", "graph-gate-pending",
			fmt.Sprintf("%s: %s", run.ID, n.ID))
		gate := NewMessage(graphSender, "edit", "request", "graph-approval",
			fmt.Sprintf("Graph run %s is waiting at human gate %q: %s — approve with: muxcode graph approve %s %s",
				run.ID, n.ID, prompt, run.ID, n.ID), "")
		_ = SendNoCC(session, gate)
		_ = Notify(session, "edit")
		// Gate surfacing is the control pane's job (MUX-108) — no modal fallback remains.

	case NodeWaitEvent:
		// Parked — harvestWaitingNode releases it on the node's event action.
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeWaiting, nil)
	}
}

// splitMapItems parses a map node's item list (comma-separated literal).
func splitMapItems(items string) []string {
	var out []string
	for _, it := range strings.Split(items, ",") {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

// nodeTimeoutSecs returns a node's task timeout, defaulting to the task
// store's own 600s default.
func nodeTimeoutSecs(n *Node) int {
	if n.TimeoutSec > 0 {
		return n.TimeoutSec
	}
	return 600
}

// finishNode records a terminal transition with an outcome. The outcome
// must never be empty — an empty outcome matches no edge in
// routeFinishedNodes, which would stall the subtree.
// predecessorOutput joins the harvested outputs of a node's direct
// predecessors so a condition node can branch on what the prior step
// actually reported (output_contains). Without it a condition sees an
// empty context and a pipeline sails past a step that answered
// "nothing happened" — live incident 2026-08-31: the commit agent
// declined PR creation, pr-read reported no PR, and the run still
// reached its close gate.
func predecessorOutput(session string, run *GraphRun, g *Graph, nodeID string) string {
	statuses, err := ReadAllNodeStatuses(session, run.ID)
	if err != nil {
		return ""
	}
	var parts []string
	for _, e := range g.Edges {
		if e.To != nodeID {
			continue
		}
		if st, ok := statuses[e.From]; ok && st.Output != "" {
			parts = append(parts, st.Output)
		}
	}
	return strings.Join(parts, "\n")
}

// unevaluatableCondition returns a detail string when a predicate could
// not be interpreted, or "" when every predicate was genuinely tested.
// EvaluateConditions has no error return: an uninterpretable predicate
// arrives as a ConditionResult carrying "unknown condition type" and
// Passed false, otherwise identical to an honest false. That is the only
// error signal available, so it bounds what any caller can distinguish.
func unevaluatableCondition(results []ConditionResult) string {
	for _, r := range results {
		if r.Detail == "unknown condition type" {
			return "condition evaluation error: unknown condition type " + r.Type
		}
	}
	return ""
}

// finishCondition finishes a condition node that evaluated cleanly. It
// always lands on GraphNodeDone — a condition is a branch selector, and
// choosing the false branch is not a failure — while still recording the
// outcome that edge matching keys on. Genuine evaluation errors go
// through finishNode instead and keep the failed state (MUX-133).
func finishCondition(session string, run *GraphRun, n *Node, outcome string) {
	_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeDone, func(s *GraphNodeStatus) {
		s.Outcome = outcome
	})
	LogLifecycle(session, "info", "daemon", "graph-node-done",
		fmt.Sprintf("%s: %s -> %s", run.ID, n.ID, outcome))
}

func finishNode(session string, run *GraphRun, n *Node, outcome, output string) {
	terminal := GraphNodeDone
	if outcome == OutcomeFailure {
		terminal = GraphNodeFailed
	}
	_ = TransitionGraphNode(session, run.ID, n.ID, terminal, func(s *GraphNodeStatus) {
		s.Outcome = outcome
		if output != "" {
			s.Output = output
		}
	})
	LogLifecycle(session, "info", "daemon", "graph-node-done",
		fmt.Sprintf("%s: %s -> %s", run.ID, n.ID, outcome))
}

// harvestRunningNode checks whether a dispatched node's work completed
// and, if so, finishes the node with a derived outcome. A spawn or map
// success is finished only after portSpawnGroup lands the worktree
// output uncommitted into the checkout working tree (MUX-131 Defect A,
// graph_port.go) — a port failure fails the node here, before any
// downstream node runs, and no porting path ever creates a commit.
func harvestRunningNode(session string, run *GraphRun, n *Node, st *GraphNodeStatus) {
	now := time.Now().Unix()
	if n.TimeoutSec > 0 && st.StartedAt > 0 && now-st.StartedAt > int64(n.TimeoutSec) {
		finishNode(session, run, n, OutcomeFailure, "node timeout")
		return
	}

	switch n.Type {
	case NodeSend:
		task, err := ReadTask(session, st.TaskID)
		if err != nil {
			// Task file gone (expired GC) — nothing left to correlate.
			finishNode(session, run, n, OutcomeFailure, "task record lost")
			return
		}
		switch task.Status {
		case TaskCompleted:
			outcome, output := deriveSendOutcome(session, n, st, task)
			finishNode(session, run, n, outcome, output)
		case TaskTimedOut, TaskFailed:
			finishNode(session, run, n, OutcomeFailure, "task "+task.Status)
		default:
			if TaskExpired(task, now) {
				finishNode(session, run, n, OutcomeFailure, "task expired")
				return
			}
			redriveStalledDispatch(session, run, n, st, task, now)
		}

	case NodeSpawn, NodeMap:
		_, _ = RefreshSpawnStatus(session)
		outcome, done := spawnGroupOutcome(session, st.TaskID)
		if !done {
			redriveStalledSpawns(session, run, n, st, now)
			return
		}
		output := ""
		if outcome == OutcomeSuccess {
			summary, perr := portSpawnGroup(session, st.TaskID)
			if errors.Is(perr, errPortTransient) {
				return // node stays running, harvest retried next tick
			}
			if perr != nil {
				finishNode(session, run, n, OutcomeFailure, "harvest: "+perr.Error())
				return
			}
			output = summary
			LogLifecycle(session, "info", "daemon", "graph-harvest",
				fmt.Sprintf("%s: %s — %s", run.ID, n.ID, summary))
		}
		finishNode(session, run, n, outcome, output)
	}
}

// redriveStalledSpawns is the spawn/map side of executor stall
// resolution: a worker whose seeded task sits unconsumed past the stall
// threshold is re-woken, with the same persisted bookkeeping and cap as
// the send path. Cap exhaustion with workers still stalled fails the
// node — default spawn nodes carry no timeout, so a never-waking worker
// would otherwise run forever.
func redriveStalledSpawns(session string, run *GraphRun, n *Node, st *GraphNodeStatus, now int64) {
	stall := int64(TaskStallSecs() / 2)
	if st.StartedAt == 0 || now-st.StartedAt < stall || now-st.LastRedrive < 60 {
		return
	}
	stalled := stalledSpawnWorkers(session, st.TaskID)
	if len(stalled) == 0 {
		return
	}
	if st.Redrives >= graphRedriveMax {
		finishNode(session, run, n, OutcomeFailure, fmt.Sprintf(
			"undeliverable: spawn worker(s) %s never consumed their task after %d redrives",
			strings.Join(stalled, ","), graphRedriveMax))
		return
	}
	for _, id := range stalled {
		graphSpawnWakeFn(session, id)
	}
	_ = MutateNodeStatus(session, run.ID, n.ID, func(s *GraphNodeStatus) {
		s.Redrives++
		s.LastRedrive = now
	})
	st.Redrives++
	st.LastRedrive = now
	LogLifecycle(session, "warn", "daemon", "graph-stall-redrive",
		fmt.Sprintf("%s: %s re-woke %d stalled spawn worker(s) — redrive %d/%d",
			run.ID, n.ID, len(stalled), st.Redrives, graphRedriveMax))
}

// graphSpawnWakeFn re-wakes one stalled spawn worker; a seam so tests
// observe wake attempts instead of spinning prompt-wait goroutines
// against a session that does not exist.
var graphSpawnWakeFn = func(session, spawnRole string) {
	go wakeSpawnedAgent(session, spawnRole)
}

// stalledSpawnWorkers lists the spawn roles whose seeded task still sits
// unconsumed in their inbox.
func stalledSpawnWorkers(session, taskIDs string) []string {
	var out []string
	for _, id := range strings.Split(taskIDs, ",") {
		if HasActionableMessages(session, id) {
			out = append(out, id)
		}
	}
	return out
}

// graphRedriveMax caps executor stall redrives per dispatch before the
// node fails as undeliverable — waiting out the 600s task timeout would
// disguise a delivery failure as slowness.
const graphRedriveMax = 3

// redriveStalledDispatch is the executor-owned form of the graph-priority
// rule (MUX-123): a dispatch in flight with NO delivery receipt past the
// stall threshold is redriven via ForceDeliver from the harvest tick
// itself. Receipt absence is the trigger — a positive signal, not a pane
// scrape — and the bookkeeping lives in the node status, so it survives
// the daemon restarts that reset the watchdog's in-memory debounce (the
// suspected cause of the three live stalls this design replaces). After
// graphRedriveMax attempts the node fails loudly as undeliverable.
func redriveStalledDispatch(session string, run *GraphRun, n *Node, st *GraphNodeStatus, task Task, now int64) {
	if !TaskStalled(task, now, TaskStallSecs()) {
		return
	}
	if _, received := ReadReceipt(session, task.ID); received {
		return // the agent has it — genuinely working, not stalled
	}
	if now-st.LastRedrive < 60 {
		return
	}
	role := NormalizeBusRole(n.Role)
	if st.Redrives >= graphRedriveMax {
		finishNode(session, run, n, OutcomeFailure, fmt.Sprintf(
			"undeliverable: %s never received the dispatch after %d redrives", role, graphRedriveMax))
		return
	}
	_ = MutateNodeStatus(session, run.ID, n.ID, func(s *GraphNodeStatus) {
		s.Redrives++
		s.LastRedrive = now
	})
	st.Redrives++
	st.LastRedrive = now
	delivered := graphRedriveFn(session, role, task)
	LogLifecycle(session, "warn", "daemon", "graph-stall-redrive",
		fmt.Sprintf("%s: %s dispatch to %s un-receipted %ds — redrive %d/%d (delivered %d)",
			run.ID, n.ID, role, now-task.SentAt, st.Redrives, graphRedriveMax, delivered))
}

// graphRedriveFn performs the redelivery for one stalled dispatch:
// pending inbox rows deliver in bulk (undelivered work duplicates
// nothing), while the consumed case uses the TARGETED RedriveTask — the
// role-wide redrive re-injected unrelated in-flight work (review
// must-fix 2026-08-28). Package variable so tests observe delivery
// attempts rather than just bookkeeping counters.
var graphRedriveFn = func(session, role string, task Task) int {
	if HasActionableMessages(session, role) {
		if res, err := ForceDeliver(session, role, true); err == nil {
			return res.Delivered
		}
		return 0
	}
	if RedriveTask(session, task) {
		return 1
	}
	return 0
}

// spawnGroupOutcome inspects the comma-separated spawn ids of a spawn or
// map node. done is true when no worker is still running; the outcome is
// success only when every worker completed. A persistent (graph-keyed)
// worker is never reaped while its run is in flight, so for it a running
// entry whose CURRENT seed is responded IS this iteration's completion —
// ReseedSpawn moves SeedMsgID before the next iteration starts, so a
// prior pass's reply can never satisfy a new dispatch.
func spawnGroupOutcome(session, taskIDs string) (string, bool) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return OutcomeFailure, false
	}
	byRole := make(map[string]SpawnEntry, len(entries))
	for _, e := range entries {
		byRole[e.SpawnRole] = e
	}

	outcome := OutcomeSuccess
	for _, id := range strings.Split(taskIDs, ",") {
		e, ok := byRole[id]
		if !ok {
			outcome = OutcomeFailure
			continue
		}
		switch e.Status {
		case "running":
			if e.RunID != "" && spawnHasResponded(session, e) {
				continue // persistent worker, iteration answered — see doc comment
			}
			return "", false
		case "completed":
			// success — no change
		default: // stopped or anything else
			outcome = OutcomeFailure
		}
	}
	return outcome, true
}

// deriveSendOutcome maps a completed task to an outcome per the
// provenance doctrine: an authoritative history row for the target role
// newer than dispatch is the verdict; a response with action "error" is
// a failure; everything else is unknown.
func deriveSendOutcome(session string, n *Node, st *GraphNodeStatus, task Task) (string, string) {
	output := ""
	if resp, ok := FindMessageByID(session, task.ResponseID); ok {
		output = resp.Payload
		if resp.Action == "error" {
			return OutcomeFailure, output
		}
	}

	if row, ok := latestAuthoritativeRow(session, NormalizeBusRole(n.Role), st.StartedAt); ok {
		if row.Outcome == OutcomeSuccess || row.Outcome == OutcomeFailure {
			return row.Outcome, output
		}
	}
	return OutcomeUnknown, output
}

// latestAuthoritativeRow returns the newest console-history entry for a
// role with a real verdict (authoritative source, non-unknown outcome)
// recorded at or after since.
func latestAuthoritativeRow(session, role string, since int64) (ConsoleEntry, bool) {
	entries := ReadConsoleEntries(HistoryPath(session, role), 0)
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.TS < since || e.Source == SourceBusResponse || e.Outcome == OutcomeUnknown || e.Outcome == "" {
			continue
		}
		return e, true
	}
	return ConsoleEntry{}, false
}

// harvestWaitingNode releases human gates whose approval marker has
// appeared and wait_event nodes whose event has been observed on the bus.
func harvestWaitingNode(session string, run *GraphRun, n *Node, st *GraphNodeStatus) {
	switch n.Type {
	case NodeWaitHuman:
		if _, err := os.Stat(graphApprovalPath(session, run.ID, n.ID, "approved")); err != nil {
			return
		}
		_ = os.Remove(graphApprovalPath(session, run.ID, n.ID, "pending"))
		finishNode(session, run, n, OutcomeSuccess, "approved")

	case NodeWaitEvent:
		// Released by any bus message whose action equals the node's event
		// name, sent at or after the node began waiting (UpdatedAt stamps
		// the waiting transition).
		if eventObservedSince(session, n.Event, st.UpdatedAt) {
			finishNode(session, run, n, OutcomeSuccess, "event "+n.Event)
		}
	}
}

// eventObservedSince scans the bus log (newest first) for a message with
// the given action at or after since.
func eventObservedSince(session, action string, since int64) bool {
	msgs, err := readMessages(LogPath(session))
	if err != nil {
		return false
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].TS < since {
			return false
		}
		if msgs[i].Action == action {
			return true
		}
	}
	return false
}

// routeFinishedNodes fires the outgoing edges of every finished node not
// yet routed, arming targets (and re-arming finished loop targets). A
// failure outcome with no live edge fails the whole run.
func routeFinishedNodes(session string, run *GraphRun, g *Graph, byID map[string]*Node, statuses map[string]*GraphNodeStatus) {
	for id, st := range statuses {
		if st.Routed || (st.State != GraphNodeDone && st.State != GraphNodeFailed) {
			continue
		}

		var matching []Edge
		for _, e := range g.Edges {
			if e.From == id && edgeOutcome(e) == st.Outcome {
				matching = append(matching, e)
			}
		}
		// Unknown outcome with no explicit unknown edge: fall back to the
		// success edges (see the outcome-model comment at the top).
		if len(matching) == 0 && st.Outcome == OutcomeUnknown {
			for _, e := range g.Edges {
				if e.From == id && edgeOutcome(e) == OutcomeSuccess {
					matching = append(matching, e)
				}
			}
			if len(matching) > 0 {
				LogLifecycle(session, "info", "daemon", "graph-unknown-fallback",
					fmt.Sprintf("%s: %s routed unknown outcome via success edge", run.ID, id))
			}
		}

		fired, exhausted := 0, 0
		for _, e := range matching {
			key := EdgeFireKey(e)
			if e.MaxIterations > 0 && run.EdgeFires[key] >= e.MaxIterations {
				exhausted++
				LogLifecycle(session, "warn", "daemon", "graph-loop-exhausted",
					fmt.Sprintf("%s: edge %s exhausted after %d iterations", run.ID, key, e.MaxIterations))
				continue
			}
			if run.EdgeFires == nil {
				run.EdgeFires = map[string]int{}
			}
			run.EdgeFires[key]++
			fired++
			armTarget(session, run, g, byID, e.To)
		}
		_ = WriteGraphRun(session, run)

		// A failure with nowhere to route fails the run; a success with no
		// edges is a normal terminal path — UNLESS an exhausted cap is what
		// suppressed the route. A cap shortfall must be a loud failure,
		// never a run that looks complete with work unattempted (MUX-121
		// plan finding: gate-retries silently costing the last phase).
		if fired == 0 && (st.Outcome == OutcomeFailure || exhausted > 0) {
			run.State = GraphRunFailed
			_ = WriteGraphRun(session, run)
			PurgeSessionArtifacts(session, "graph run "+run.ID+" failed")
			reason := "failed with no live edge"
			if st.Outcome != OutcomeFailure {
				reason = "loop cap exhausted with its edge suppressed — remaining work never attempted"
			}
			LogLifecycle(session, "warn", "daemon", "graph-run-failed",
				fmt.Sprintf("%s: node %s %s", run.ID, id, reason))
		}

		_ = MutateNodeStatus(session, run.ID, id, func(s *GraphNodeStatus) {
			s.Routed = true
		})
	}
}

// armTarget moves an edge's target toward ready: pending targets arm
// directly (joins only once their barrier policy is met), and finished
// targets re-arm for a fresh loop iteration.
func armTarget(session string, run *GraphRun, g *Graph, byID map[string]*Node, targetID string) {
	st, err := ReadNodeStatus(session, run.ID, targetID)
	if err != nil {
		return
	}
	n := byID[targetID]
	if n == nil {
		return
	}

	switch st.State {
	case GraphNodePending:
		if n.Type == NodeJoin && !joinBarrierMet(run, g, n) {
			return
		}
		_ = TransitionGraphNode(session, run.ID, targetID, GraphNodeReady, nil)
	case GraphNodeDone, GraphNodeFailed, GraphNodeSkipped:
		_ = TransitionGraphNode(session, run.ID, targetID, GraphNodeReady, nil)
	}
}

// joinBarrierMet counts incoming edges that have fired against the join
// policy. Fires are the routing layer's own decision record (EdgeFires,
// persisted per run), so the barrier inherits every routing rule —
// including the unknown→success fallback — instead of re-deriving
// outcomes and drifting. Re-deriving is exactly how the barrier once
// deadlocked: a branch finishing with outcome "unknown" fired its edge
// via the fallback, but an outcome-equality re-check refused to count
// it, and fan-in never released on hookless providers.
func joinBarrierMet(run *GraphRun, g *Graph, n *Node) bool {
	total, delivered := 0, 0
	for _, e := range g.Edges {
		if e.To != n.ID {
			continue
		}
		total++
		if run.EdgeFires[EdgeFireKey(e)] > 0 {
			delivered++
		}
	}
	switch n.Join {
	case JoinAll:
		return delivered == total
	case JoinAny:
		return delivered >= 1
	case JoinQuorum:
		return delivered >= n.Quorum
	}
	return false
}

// settleRun completes the run when nothing is active or armable. Called
// at the end of every tick; a run already failed or canceled is left
// alone.
func settleRun(session string, run *GraphRun) {
	fresh, err := ReadGraphRun(session, run.ID)
	if err != nil || fresh.State != GraphRunRunning {
		return
	}
	statuses, err := ReadAllNodeStatuses(session, run.ID)
	if err != nil {
		return
	}
	for _, st := range statuses {
		switch st.State {
		case GraphNodeReady, GraphNodeRunning, GraphNodeWaiting:
			return
		case GraphNodeDone, GraphNodeFailed:
			if !st.Routed {
				return
			}
		}
	}

	fresh.State = GraphRunComplete
	_ = WriteGraphRun(session, fresh)
	LogLifecycle(session, "info", "daemon", "graph-run-complete", run.ID)
	PurgeSessionArtifacts(session, "graph run "+run.ID+" complete")

	// The single completion wake the acceptance criteria promise edit.
	done := NewMessage(graphSender, "edit", "request", "graph-complete",
		fmt.Sprintf("Graph run %s (%s) completed. Status: muxcode graph status %s", run.ID, run.Template, run.ID), "")
	_ = SendNoCC(session, done)
	_ = Notify(session, "edit")
}
