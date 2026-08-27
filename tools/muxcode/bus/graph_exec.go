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

// graphSpawnFn dispatches a worker for spawn/map nodes. Package variable
// so unit tests can run graphs without tmux — StartSpawn creates real
// tmux windows.
var graphSpawnFn = func(session, role, task, owner string) (string, error) {
	entry, err := StartSpawn(session, role, task, owner, true)
	if err != nil {
		return "", err
	}
	return entry.SpawnRole, nil
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
			dispatchNode(session, run, byID[id], st)
		}
	}

	settleRun(session, run)
	return nil
}

// interpolateGraphMessage expands run/worker placeholders in a node
// message template.
func interpolateGraphMessage(msg, intent, item string) string {
	msg = strings.ReplaceAll(msg, "${intent}", intent)
	if item != "" {
		msg = strings.ReplaceAll(msg, "${item}", item)
	}
	return msg
}

// dispatchNode fires a ready node's work and moves it to running/waiting.
func dispatchNode(session string, run *GraphRun, n *Node, st *GraphNodeStatus) {
	if n == nil {
		return
	}
	LogLifecycle(session, "info", "daemon", "graph-node-start",
		fmt.Sprintf("%s: %s (%s)", run.ID, n.ID, n.Type))

	switch n.Type {
	case NodeSend:
		msg := interpolateGraphMessage(n.Message, run.Intent, "")
		m := NewMessage(graphSender, n.Role, "request", n.Action, msg, "")
		if err := SendNoCC(session, m); err != nil {
			if errors.Is(err, ErrSendSuppressed) {
				// The identical request is already queued or in flight (a
				// loop re-entry or retry racing the prior pass) — adopt it
				// instead of failing the node: the work the send would
				// have queued already exists.
				taskID := m.ID
				if t, found := FindInFlightTask(session, n.Role, n.Action); found {
					taskID = t.ID
				} else if pm, found := FindPendingInboxRequest(session, m.To, m.From, m.Action, m.Payload); found {
					// Adopt the queued duplicate's ID: the agent answers
					// THAT id, so a task keyed to the unsent m.ID would
					// sit in-flight forever (PR #38 Copilot finding).
					adopted := m
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
		msg := interpolateGraphMessage(n.Message, run.Intent, "")
		spawnID, err := graphSpawnFn(session, n.Role, msg, graphSender)
		if err != nil {
			finishNode(session, run, n, OutcomeFailure, "spawn failed: "+err.Error())
			return
		}
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, func(s *GraphNodeStatus) {
			s.TaskID = spawnID
		})

	case NodeMap:
		// v1 item source: a comma-separated literal list. One worker per
		// item, ${item} interpolated into each worker's message.
		items := splitMapItems(n.Items)
		if len(items) == 0 {
			finishNode(session, run, n, OutcomeFailure, "map node has no items")
			return
		}
		var ids []string
		for _, item := range items {
			msg := interpolateGraphMessage(n.Message, run.Intent, item)
			spawnID, err := graphSpawnFn(session, n.Role, msg, graphSender)
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
		ctx := &ChainContext{Session: session}
		passed, _ := EvaluateConditions(n.Conditions, ctx)
		outcome := OutcomeFailure
		if passed {
			outcome = OutcomeSuccess
		}
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, nil)
		finishNode(session, run, n, outcome, "")

	case NodeJoin:
		// The barrier was satisfied when the node was armed; a join
		// completes immediately.
		_ = TransitionGraphNode(session, run.ID, n.ID, GraphNodeRunning, nil)
		finishNode(session, run, n, OutcomeSuccess, "")

	case NodeWaitHuman:
		prompt := interpolateGraphMessage(n.Message, run.Intent, "")
		// A fresh pass through a gate requires a fresh approval: purge any
		// approved marker left by a previous pass (graph retry --from, or a
		// loop edge re-arming the gate). Without this, harvestWaitingNode
		// sees the stale marker and releases the gate instantly — a retried
		// run would sail through its human gate with nobody approving.
		_ = os.Remove(graphApprovalPath(session, run.ID, n.ID, "approved"))
		// The pending marker is informational (surfaced by graph status);
		// the release signal is the approved marker plus the edit
		// notification below, so a marker write failure is logged but
		// does not block the gate.
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
		// Gate surfacing is the control pane's job: it switches itself to
		// Pending Gates on the next tick (MUX-108). The graph popups were
		// removed with the pane's arrival — no modal fallback remains.

	case NodeWaitEvent:
		// Parked here; harvestWaitingNode releases it when a bus message
		// with the node's event action is observed.
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
// and, if so, finishes the node with a derived outcome.
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
			}
		}

	case NodeSpawn, NodeMap:
		_, _ = RefreshSpawnStatus(session)
		outcome, done := spawnGroupOutcome(session, st.TaskID)
		if done {
			finishNode(session, run, n, outcome, "")
		}
	}
}

// spawnGroupOutcome inspects the comma-separated spawn ids of a spawn or
// map node. done is true when no worker is still running; the outcome is
// success only when every worker completed.
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

		fired := 0
		for _, e := range matching {
			key := EdgeFireKey(e)
			if e.MaxIterations > 0 && run.EdgeFires[key] >= e.MaxIterations {
				LogLifecycle(session, "info", "daemon", "graph-loop-exhausted",
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

		// A failure with nowhere to route (no failure edge, or the loop
		// edge exhausted) fails the run. A success with no edges is a
		// normal terminal path.
		if fired == 0 && st.Outcome == OutcomeFailure {
			run.State = GraphRunFailed
			_ = WriteGraphRun(session, run)
			LogLifecycle(session, "warn", "daemon", "graph-run-failed",
				fmt.Sprintf("%s: node %s failed with no live edge", run.ID, id))
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

	// The single completion wake the acceptance criteria promise edit.
	done := NewMessage(graphSender, "edit", "request", "graph-complete",
		fmt.Sprintf("Graph run %s (%s) completed. Status: muxcode graph status %s", run.ID, run.Template, run.ID), "")
	_ = SendNoCC(session, done)
	_ = Notify(session, "edit")
}
