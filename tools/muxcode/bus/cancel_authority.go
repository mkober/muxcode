package bus

import (
	"errors"
	"fmt"
)

// CheckCancelAuthority returns a deny message if actor may not stop run's work,
// or "" if it may. It governs `graph cancel` and `spawn stop` of a run's worker.
//
// MUX-182 defect 3: an agent read a run the user had launched by hand as an
// autonomous launch and cancelled it, and nothing checked who was cancelling
// whose work. The rules:
//
//   - The user may stop any run.
//   - Any actor may stop a run an agent created: autonomous work stays freely
//     cancellable.
//   - A run the user created — or one whose creator was never recorded or could
//     not be established — may be stopped only by the user. Unestablished is
//     treated as human, because the misreading this exists for is an agent
//     deciding a run was not the user's.
//   - A run already canceling or canceled is past the decision: re-running the
//     cancel or stopping a surviving worker completes the user's cancel, and
//     CancelIncompleteError tells whoever reads it to do exactly that.
//
// The approval for an agent-initiated stop of a human run is the user issuing
// the stop themselves. Anything an agent could pass on the command line — a
// flag, a token, an env var — would be self-service, the hole MUX-144 closed on
// gates.
//
// actor is compared against ActorUser and nothing else, and no role is granted
// anything, so the daemon→edit normalization cannot open this check as it
// opened the commit backstop in MUX-144 Phase 4. ActorUnknown is not the user.
func CheckCancelAuthority(actor string, run *GraphRun) string {
	if actor == ActorUser || run == nil {
		return ""
	}
	switch run.State {
	case GraphRunCanceling, GraphRunCanceled:
		return ""
	}
	switch run.CreatedBy {
	case ActorUser, ActorUnknown, "":
	default:
		return ""
	}
	who := actor
	if actor == ActorUnknown {
		who = "an actor that could not be identified"
	}
	return fmt.Sprintf(
		"stopping human-initiated work is user-initiated: %s may not stop run %s, launched by: %s. "+
			"Report the run and why it should stop, and let the user cancel it.",
		who, run.ID, DescribeRunCreator(run.CreatedBy))
}

// StopSpawnAuthorized is `spawn stop` for a caller that must pass
// CheckCancelAuthority: a worker tied to a graph run may be stopped only by an
// actor allowed to cancel that run. A worker tied to no run is not gated. A run
// that cannot be read cannot show it was not the user's, so the stop is refused
// unless the user issues it.
//
// The decision and the stop share the run lock, as CancelGraphRun's do, so a
// retry cannot resume the run between them; a lock that cannot be taken is
// treated like an unreadable run.
//
// The cancel path calls StopSpawn directly: it has already passed the check for
// the whole run.
func StopSpawnAuthorized(session, id string) error {
	e, err := GetSpawnEntry(session, id)
	if err != nil {
		return err
	}
	if e.RunID != "" {
		actor := BusActorVerified()
		run := &GraphRun{ID: e.RunID}
		if unlock, lerr := lockGraphRun(session, e.RunID, graphRunLockWait); lerr == nil {
			defer unlock()
			if r, rerr := ReadGraphRun(session, e.RunID); rerr == nil {
				run = r
			}
		}
		if deny := CheckCancelAuthority(actor, run); deny != "" {
			LogLifecycle(session, "warn", actor, "spawn-stop-refused",
				fmt.Sprintf("spawn %s (graph run %s): %s", id, e.RunID, deny))
			return errors.New(deny)
		}
		runStopAuthorizedHook()
	}
	return StopSpawn(session, id)
}
