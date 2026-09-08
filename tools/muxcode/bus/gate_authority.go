package bus

import (
	"fmt"
	"os"
	"strings"
)

// A wait_human gate is the only control standing between a graph run and an
// irreversible, externally visible action — a pushed branch, an opened PR.
// MUX-144: it was openable by every role holding the `Bash(muxcode *)` glob,
// which is every role, so an autonomous run could raise a gate and release it
// itself. Observed live 2026-09-03: a gate went from pending to done in four
// seconds on a run nobody requested, and the commit dispatch behind it was
// stopped by the commit agent's own judgement rather than by any control.
//
// The check lives here rather than in cmd/graph.go because two roads reach the
// release — `muxcode graph approve` and the graph TUI — and a control verified
// on one road is not verified on all of them (MUX-142). ApproveGraphGate is
// where they meet.

// gateApprovalAuthorityDefault is who may release a gate: a person, and no
// agent. The narrow default is deliberate — a stalled autonomous arc is
// recoverable and a pushed branch is not — and it matches the humanness rule
// unverifiedHoldReleased already enforces on these same markers.
var gateApprovalAuthorityDefault = []string{ActorUser}

// GateApprovalAuthority returns the actors allowed to release a wait_human gate.
//
// Override with MUXCODE_GATE_AUTHORITY_ROLES (comma-separated) to opt an agent
// in — an arc meant to run unattended needs it:
//
//	MUXCODE_GATE_AUTHORITY_ROLES=user,auto
//
// Setting it to the empty string denies every actor, so every run parks at its
// first gate. That is a legitimate configuration: a session where no graph may
// reach a mutation at all.
func GateApprovalAuthority() []string {
	if v, ok := os.LookupEnv("MUXCODE_GATE_AUTHORITY_ROLES"); ok {
		return splitTrimmed(v)
	}
	return gateApprovalAuthorityDefault
}

// CheckGateApprovalAuthority returns a deny message if actor may not release a
// gate on run, or "" if the approval is allowed.
//
// Two rules, and the second survives the first being widened: the actor must
// hold the authority, and an agent may not approve a gate on a run it created.
// Self-approval is the shape the incident took — the same autonomous process on
// both ends — so opting an agent into the authority must not also opt it into
// rubber-stamping its own work. A person approving their own run is the normal
// path and is never refused.
//
// ActorUnknown is refused whatever the list says. It means the ancestry probe
// failed, and BusActorVerified fails closed to it precisely so that callers
// gating on humanness refuse; honouring it from a configured list would make
// sabotaging `ps` a way to open every gate.
//
// The deny message deliberately does not name the env var that would lift it.
// MUXCODE_GATE_AUTHORITY_ROLES is read at approve time from the caller's own
// environment, so an agent handed that string can self-authorize by prefixing
// it to the command it was just refused. The opt-in is documented for the USER,
// in docs/configuration.md, where it belongs.
func CheckGateApprovalAuthority(actor string, run *GraphRun) string {
	if actor == ActorUnknown {
		return "gate approvals need an identified approver, and who is acting could not be established. " +
			"Report that the gate is waiting and let the user release it."
	}
	actor = NormalizeBusRole(actor)
	authorized := GateApprovalAuthority()
	permitted := false
	for _, allowed := range authorized {
		if NormalizeBusRole(allowed) == actor {
			permitted = true
			break
		}
	}
	if !permitted {
		who := "no actor is authorized"
		if len(authorized) > 0 {
			who = "only " + strings.Join(authorized, ", ") + " may"
		}
		return fmt.Sprintf(
			"releasing a wait_human gate is user-initiated: %s may not approve one (%s). "+
				"Report that the gate is waiting and let the user release it.",
			actor, who)
	}
	if run != nil && actor != ActorUser && actor == NormalizeBusRole(run.CreatedBy) {
		return fmt.Sprintf(
			"%s created run %s and may not approve its own gate: a run that raises a gate and releases it "+
				"has no human in the loop. Report that the gate is waiting and let the user release it.",
			actor, run.ID)
	}
	return ""
}
