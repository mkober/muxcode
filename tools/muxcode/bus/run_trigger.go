package bus

// Run triggers: what set a graph run in motion, as far as the bus can show.
const (
	RunTriggerUser       = "user-request"
	RunTriggerStartup    = "startup"
	RunTriggerGraphEdge  = "graph-edge"
	RunTriggerBusRequest = "bus-request"
	RunTriggerUnknown    = "unknown"
)

// deriveRunTrigger names what set actor to launching a run, the detail that
// qualifies it, and whether that answer is only inferred. It reads only
// evidence the bus already holds — the verified actor, the spawn registry and
// the session message log — and nothing the launching agent declares, so an
// agent cannot label its own run.
//
// Established (inferred false) — the bus holds the tie between cause and run:
//
//   - the user, by hand: a user request
//   - a worker of another run: a graph edge, detail the parent run id
//   - no verified actor, or an agent with no request on the log: unknown
//
// Inferred — every other agent. The bus cannot tie a run to the request that
// caused it: an instruction typed into the agent's own pane leaves no record,
// and a request queued behind the one being worked is still the most recent.
// A graph dispatch is no exception — its node reads running from the moment it
// is enqueued, before the agent has read it. The agent's most recent request is
// therefore recorded as context, never as cause:
//
//   - its startup message: startup, detail the launch reason PreLaunchSetup
//     stamped on that message
//   - sent by a graph run (Message.OriginRun): a graph edge, detail that run id
//   - typed at the Prompt surface: a user request
//   - anything else: a bus request, detail its sender and action
//
// An inferred startup with launch reason restart fits the unrequested runs of
// 2026-09-02 (MUX-141) and equally fits a user who typed the instruction after
// the restart. It narrows the question; it does not answer it.
func deriveRunTrigger(session, actor string) (trigger, detail string, inferred bool) {
	switch actor {
	case ActorUser:
		return RunTriggerUser, "", false
	case "", ActorUnknown:
		return RunTriggerUnknown, "", false
	}
	if parent, ok := spawnRunOwner(session, actor); ok {
		return RunTriggerGraphEdge, parent, false
	}
	m, ok := lastRequestTo(session, actor)
	switch {
	case !ok:
		return RunTriggerUnknown, "", false
	case m.Action == "startup":
		return RunTriggerStartup, LaunchReason(m.LaunchReason).logName(), true
	case m.OriginRun != "":
		return RunTriggerGraphEdge, m.OriginRun, true
	case m.OriginCreatedBy == ActorUser:
		return RunTriggerUser, "typed at the Prompt surface", true
	}
	return RunTriggerBusRequest, m.From + ": " + m.Action, true
}

// lastRequestTo returns the most recent request the session log holds for role.
func lastRequestTo(session, role string) (Message, bool) {
	msgs, err := readMessages(LogPath(session))
	if err != nil {
		return Message{}, false
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Type == "request" && NormalizeBusRole(msgs[i].To) == role {
			return msgs[i], true
		}
	}
	return Message{}, false
}

// DescribeRunTrigger renders a run's trigger for graph status, run.json and
// the graph-run-created event. An inferred trigger is rendered as the launching
// agent's last bus request and says it is not established, so context is never
// read as cause. None of its own wording uses the words DescribeRunCreator
// reserves for a run the user launched ("the user") or an agent launched
// ("autonomous"), so a trigger is never read as the creator.
func DescribeRunTrigger(trigger, detail string, inferred bool) string {
	var what string
	switch trigger {
	case "":
		return "unrecorded"
	case RunTriggerUser:
		what = "a user request"
		if detail != "" {
			what += ", " + detail
		}
	case RunTriggerStartup:
		what = "its startup message (launch reason: " + detail + ")"
	case RunTriggerGraphEdge:
		what = "graph run " + detail
		if inferred {
			what = "a request from " + what
		}
	case RunTriggerBusRequest:
		what = "a bus request (" + detail + ")"
	default:
		return "could not be established"
	}
	if inferred {
		return "not established — the launching agent's last bus request was " + what
	}
	return what
}
