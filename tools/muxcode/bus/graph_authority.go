package bus

import "fmt"

// CheckGraphNodeAuthority refuses a delegation the sender's own graph run
// already owns, returning a denial string or "" to allow.
//
// It is the enforcement half of the graph-ownership preamble
// (graphWorkerTask). The preamble tells a spawn worker that the graph
// dispatches build/test/review itself; this refuses the send when a worker
// does it anyway. An instruction alone was not enough the first time: the
// defect being fixed is precisely that code-editor.md's "Orchestration Role"
// section told a worker to delegate build→test→review and the worker obeyed
// it, running a second ungated pipeline in the wrong tree beside the graph's
// parked nodes. Answering an instruction failure with another instruction
// leaves the same gap, so ownership is enforced where every send funnels
// through, alongside the commit and prompt authority gates.
//
// Only spawn roles owned by a still-running run are constrained: the graph's
// own dispatches (from "daemon") and anything a human drives through edit are
// not spawn roles and never match, so no ordinary delegation is affected.
//
// Role alone is too coarse a key — a run owns a node's *work*, not every
// message to the agent that performs it. Matching the action too leaves a
// worker free to ask the build agent an unrelated question while still
// refusing it the build the graph will dispatch itself.
func CheckGraphNodeAuthority(session, from, to, action string) string {
	if from == "" || to == "" || action == "" || from == graphSender {
		return ""
	}
	runID, ok := spawnRunOwner(session, from)
	if !ok {
		return ""
	}
	run, err := ReadGraphRun(session, runID)
	if err != nil || run.State != GraphRunRunning {
		return ""
	}
	g, err := ReadGraphRunGraph(session, runID)
	if err != nil {
		return ""
	}
	for _, n := range g.Nodes {
		if n.Type == NodeSend && n.Role == to && n.Action == action {
			return fmt.Sprintf("graph run %s owns the %s:%s work as node %q — report to your requester and stop, the graph dispatches it",
				runID, to, action, n.ID)
		}
	}
	return ""
}

// spawnRunOwner reports which graph run a spawn role was created for.
func spawnRunOwner(session, role string) (string, bool) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.SpawnRole == role && e.RunID != "" {
			return e.RunID, true
		}
	}
	return "", false
}
