package bus

import "fmt"

// DescribeRunCreator renders a run's created_by for every surface an agent
// reads provenance from: gate requests, graph status, the run list, run.json,
// the graph-run-created event and the TUI.
//
// The bare actor name is not enough. Rendered as "Started by: user", an agent
// read a run the user had launched by hand as the auto agent's launch-on-restore
// and cancelled it (MUX-182, 2026-09-14); a second agent made the same misreading
// from created_by: user. The same class hit gate messages on 2026-09-03, when an
// agent refused a gate as an "auto-launched run" the user had started a minute
// earlier. Each form below names its category in words that cannot be read as
// another, and every agent name carries "(autonomous)".
func DescribeRunCreator(createdBy string) string {
	switch createdBy {
	case "":
		return "unrecorded"
	case ActorUnknown:
		return "could not be established"
	case ActorUser:
		return "the user, by hand"
	default:
		return createdBy + " (autonomous)"
	}
}

// stampMessageOrigin records, on a message a graph worker or the graph executor
// sends, the run it traces back to: its id, who launched it and its state at
// send time. Everything previously on the Origin fields is discarded first, so
// a sender can never carry a provenance the bus did not derive.
//
// A spawn's own sends carry From: spawn-<hex> and nothing else, so before this a
// recipient could not tell whether a prompt traced back to a human or to a run
// that had already been cancelled — and plan wrote "on a user request" into a
// spec on a cancelled run's orphan prompt (MUX-182). A worker the registry does
// not tie to a run is stamped with nothing and rendered as such by
// formatMessageOrigin, never as a human request.
//
// humanPrompt is the one positive human origin on the bus — SendHumanPrompt's
// in-process seam, fed by a person's keystrokes — and is stamped as ActorUser
// with no run.
//
// The fields are deliberately apart from GraphRun/GraphNode: those feed the
// commit-authority backstop, and a worker's send must not be judged as an
// executor dispatch.
func stampMessageOrigin(session string, m *Message, humanPrompt bool) {
	m.OriginRun, m.OriginCreatedBy, m.OriginRunState = "", "", ""
	if humanPrompt {
		m.OriginCreatedBy = ActorUser
		return
	}
	runID := m.GraphRun
	if runID == "" && IsSpawnRole(m.From) {
		runID, _ = spawnRunOwner(session, m.From)
	}
	if runID == "" {
		return
	}
	m.OriginRun = runID
	run, err := ReadGraphRun(session, runID)
	if err != nil {
		m.OriginRunState = "unreadable"
		return
	}
	m.OriginCreatedBy = run.CreatedBy
	m.OriginRunState = run.State
}

// formatMessageOrigin renders the provenance line FormatMessage shows a
// recipient, or "" when there is nothing to say: edit's own messages (edit is
// the one agent in conversation with the user, and the consent boundary) and
// responses from agents other than graph workers.
//
// Every other request says plainly that it carries no user request, so a
// recipient cannot turn an agent's prompt into "on a user request".
func formatMessageOrigin(m Message) string {
	switch {
	case m.OriginRun != "":
		s := fmt.Sprintf("Origin: graph run %s · launched by: %s · run state at send: %s",
			m.OriginRun, DescribeRunCreator(m.OriginCreatedBy), m.OriginRunState)
		if m.OriginRunState == GraphRunCanceled || m.OriginRunState == GraphRunCanceling {
			s += " — the run was cancelled; do not act on this"
		}
		return s + "\n"
	case m.OriginCreatedBy == ActorUser:
		return "Origin: typed by the user at the Prompt surface\n"
	case IsSpawnRole(m.From):
		return fmt.Sprintf("Origin: spawn worker %s, tied to no graph run — carries no record of a user request\n", m.From)
	case m.Type == "request" && m.From != "edit":
		return fmt.Sprintf("Origin: sent by %s, not by the user — carries no record of a user request\n", m.From)
	}
	return ""
}
