package bus

import (
	"bytes"
	"io"
	"os"
)

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
//   - a worker of another run still working that run's seed — unanswered and
//     its most recent request: a graph edge, detail the parent run id
//   - no verified actor, or an agent with no request on the log: unknown
//
// A worker that has answered its seed is parked, and one whose seed a later
// request superseded is working something else: either one's run is inferred
// like any agent's, since the registry's run id no longer ties it to the cause.
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
	m, ok := lastRequestTo(session, actor)
	if parent, working := workerOnSeed(session, actor, m.ID); ok && working {
		return RunTriggerGraphEdge, parent, false
	}
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

// workerOnSeed reports the run a graph worker is working for: role is a running
// worker tied to a run, lastRequestID is its current seed, and that seed is
// unanswered.
func workerOnSeed(session, role, lastRequestID string) (string, bool) {
	entries, err := ReadSpawnEntries(session)
	if err != nil || lastRequestID == "" {
		return "", false
	}
	for _, e := range entries {
		if e.SpawnRole == role && e.RunID != "" && e.Status == "running" &&
			e.SeedMsgID == lastRequestID && !spawnHasResponded(session, e) {
			return e.RunID, true
		}
	}
	return "", false
}

// lastRequestTo returns the most recent request the session log holds for role,
// scanning back from the end so a long session log is not read whole.
func lastRequestTo(session, role string) (Message, bool) {
	var found Message
	var ok bool
	_ = scanLinesBackward(LogPath(session), func(line []byte) bool {
		m, err := DecodeMessage(line)
		if err != nil || m.Type != "request" || NormalizeBusRole(m.To) != role {
			return true
		}
		found, ok = m, true
		return false
	})
	return found, ok
}

// scanLinesBackward calls visit on each non-blank line of path, last line
// first, until visit returns false. A missing file is no lines.
func scanLinesBackward(path string, visit func(line []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	const chunk = 64 << 10
	var partial []byte
	for pos := info.Size(); pos > 0; {
		n := int64(chunk)
		if n > pos {
			n = pos
		}
		pos -= n
		buf := make([]byte, int(n)+len(partial))
		if _, err := f.ReadAt(buf[:n], pos); err != nil && err != io.EOF {
			return err
		}
		copy(buf[n:], partial)
		for i := bytes.LastIndexByte(buf, '\n'); i >= 0; i = bytes.LastIndexByte(buf, '\n') {
			if line := buf[i+1:]; len(bytes.TrimSpace(line)) > 0 && !visit(line) {
				return nil
			}
			buf = buf[:i]
		}
		partial = buf
	}
	if len(bytes.TrimSpace(partial)) > 0 {
		visit(partial)
	}
	return nil
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
