package bus

import "fmt"

// CheckGraphNodeAuthority refuses a delegation the sender's own graph run
// already owns, returning a denial string or "" to allow.
//
// It is the enforcement half of graphWorkerTask's preamble. The defect being
// fixed is an instruction failing to constrain a worker, so answering it with
// only another instruction would leave the same gap; ownership is enforced
// where every send funnels through, beside the commit and prompt gates.
//
// Only spawn roles owned by a still-running run are constrained — the graph's
// own dispatches and a human's through edit are not spawn roles. Role alone
// would be too coarse: a run owns a node's work, not every message to the
// agent performing it, so the action must match too.
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
