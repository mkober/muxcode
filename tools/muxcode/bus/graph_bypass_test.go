package bus

import (
	"os"
	"testing"
)

// MUX-144 Phase 1 — characterization pins for the bypasses that let a graph run
// reach an irreversible git action with no human in the loop.
//
// Every test here asserts the CURRENT behavior, which is the defect. Each is
// written to FAIL the moment its phase lands, so the fix cannot ship without
// someone reading the pin and inverting it deliberately. A failure here is
// never repaired by relaxing an assertion: invert it, and move the phase
// checkbox in the spec with it.
//
//	Defect A (Phase 2) — TestApproveGraphGateAcceptsAnyRole
//	                     TestApproveGraphGateAcceptsSelfApproval
//	Defect C (Phase 4) — TestGraphCommitDispatchPassesCommitAuthority
//	                     TestGraphCommitDispatchReachesCommitInbox
//
// Two halves of the control already work and are pinned elsewhere; neither is
// duplicated here. Defect B (audit) is closed — see
// TestApproveGraphGateRecordsAndAnnouncesApprover in graph_run_test.go. The
// structural gate rule is sound — see TestValidateGateRule in graph_test.go,
// which rejects an ungated commit node and accepts a gated one.

// pinCompiledAuthorities clears both authority overrides so a pin reads the
// compiled-in defaults rather than whatever the session running the suite
// happens to export. These tests assert what ships, and an ambient opt-in would
// flip them silently in either direction.
func pinCompiledAuthorities(t *testing.T) {
	t.Helper()
	for _, key := range []string{"MUXCODE_COMMIT_AUTHORITY_ROLES", "MUXCODE_ATLASSIAN_AUTHORITY_ROLES"} {
		t.Setenv(key, "") // registers the restore
		os.Unsetenv(key)
	}
}

// gateApprover reads the approver identity a release recorded.
func gateApprover(t *testing.T, runID, nodeID string) string {
	t.Helper()
	data, err := os.ReadFile(graphApprovalPath(runTestSession, runID, nodeID, "approved"))
	if err != nil {
		t.Fatalf("read approval marker: %v", err)
	}
	return approvalGrantedBy(data)
}

// TestGraphCommitDispatchPassesCommitAuthority pins Defect C: a graph send node
// addressed to commit clears CheckCommitAuthority, because graphSender is
// "daemon" and NormalizeBusRole maps that to edit — the default authority. The
// runtime backstop therefore cannot tell "the user's agent asked" from "a graph
// node asked", and the whole weight of the guarantee falls on the gate.
//
// The build assertion is the discriminator: without it a CheckCommitAuthority
// that allowed everything would pass this test, and the pin would record an
// inert check rather than a bypass.
//
// Phase 4 inverts this: the dispatch must be judged on the gate's recorded
// approval, and every action below must then be denied.
func TestGraphCommitDispatchPassesCommitAuthority(t *testing.T) {
	pinCompiledAuthorities(t)

	if got := NormalizeBusRole(graphSender); got != "edit" {
		t.Fatalf("NormalizeBusRole(%q) = %q, want edit — the premise of the bypass no longer holds", graphSender, got)
	}
	for _, action := range []string{"commit", "stage", "push", "merge", "rebase", "tag"} {
		if deny := CheckCommitAuthority(graphSender, "commit", action); deny != "" {
			t.Errorf("CheckCommitAuthority(%q, commit, %s) = %q — the bypass is closed; invert this pin and check off MUX-144 Phase 4",
				graphSender, action, deny)
		}
	}
	if deny := CheckCommitAuthority("build", "commit", "commit"); deny == "" {
		t.Error("build must still be denied — the pin above records a bypass only if the check refuses someone")
	}
}

// TestGraphCommitDispatchReachesCommitInbox walks the whole laundering path on
// the machinery that actually runs it: an agent opens the gate, the executor
// dispatches, and the incident's own commit message lands in commit's inbox
// with nothing having refused it. Steps 2 and 3 of the spec's laundering table,
// in one run.
//
// The predicate check above is not a substitute. CheckCommitAuthority is
// enforced inside Send, several layers from the node definition, and this
// subsystem has shipped defects that every executor unit test sailed over and
// only the live path caught.
func TestGraphCommitDispatchReachesCommitInbox(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "auto")
	g := &Graph{
		Name:  "launder",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve the commit and push"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit",
				Message: "Stage all unstaged files, commit, push, and create a PR"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q, want waiting", s)
	}

	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("agent approval refused: %v — the gate is guarded; invert this pin and check off MUX-144 Phase 2", err)
	}
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Fatalf("commit node state %q, want running — the dispatch is refused; invert this pin and check off MUX-144 Phase 4", s)
	}
	msgs, _ := Peek(runTestSession, "commit")
	if len(msgs) != 1 || msgs[0].Action != "commit" || msgs[0].From != graphSender {
		t.Fatalf("commit inbox = %+v, want one %s-sent commit request", msgs, graphSender)
	}
}

// TestApproveGraphGateAcceptsAnyRole pins Defect A: `graph approve` carries no
// authority check, so every role holding the `Bash(muxcode *)` glob — which is
// every role — can release the gate that is the only control between a run and
// a push.
//
// What makes this a missing refusal rather than a missing fact is the approver
// the marker records: the evidence needed to deny is already written to disk
// beside the approval it should have refused.
//
// Phase 2 inverts this: an unauthorized role's approval must fail and leave the
// gate pending.
func TestApproveGraphGateAcceptsAnyRole(t *testing.T) {
	for _, role := range []string{"build", "test", "review", "plan", "auto"} {
		pinActor(t, role)
		run := createTestRun(t, actorGateGraph())

		if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
			t.Fatalf("ApproveGraphGate as %s: %v — the gate is guarded; invert this pin and check off MUX-144 Phase 2", role, err)
		}
		if by := gateApprover(t, run.ID, "gate"); by != role {
			t.Errorf("approved_by = %q, want %q — the identity a refusal would read is not being recorded", by, role)
		}
	}
}

// TestApproveGraphGateAcceptsSelfApproval pins the sharpest form of Defect A:
// the agent that created a run may open that run's own gate, so an autonomous
// arc approves itself and no human is ever involved. This is the acceptance
// criterion "an autonomous agent cannot approve a gate on a run it created",
// failing.
func TestApproveGraphGateAcceptsSelfApproval(t *testing.T) {
	pinActor(t, "auto")
	run := createTestRun(t, actorGateGraph())

	if run.CreatedBy != "auto" {
		t.Fatalf("run.CreatedBy = %q, want auto — the creator identity is the premise of self-approval", run.CreatedBy)
	}
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("self-approval failed: %v — self-approval is refused; invert this pin and check off MUX-144 Phase 2", err)
	}
	if by := gateApprover(t, run.ID, "gate"); by != run.CreatedBy {
		t.Errorf("approved_by = %q, creator = %q — both identities are on disk and nothing compared them", by, run.CreatedBy)
	}
}

// TestGraphAtlassianWriteRefusedOnlyByConfiguration answers Phase 1's question
// "does the commit bypass apply to CheckAtlassianAuthority too?" — it does not,
// and the reason is a coincidence rather than a control.
//
// A graph-dispatched Atlassian write is refused today only because the
// authority sits with plan while the graph sender normalizes to edit. Point the
// authority back at edit — where it sat until recently — and the identical
// dispatch passes. So the refusal must not be read as evidence that the
// Atlassian path is defended: it is the commit bypass with a different role
// name in the list.
func TestGraphAtlassianWriteRefusedOnlyByConfiguration(t *testing.T) {
	pinCompiledAuthorities(t)
	sender := NormalizeBusRole(graphSender)

	if deny := CheckAtlassianAuthority(sender, "jira", "update"); deny == "" {
		t.Fatal("a graph-dispatched Jira write must be refused while plan holds the authority")
	}
	t.Setenv("MUXCODE_ATLASSIAN_AUTHORITY_ROLES", "edit")
	if deny := CheckAtlassianAuthority(sender, "jira", "update"); deny != "" {
		t.Errorf("with the authority at edit the graph sender was refused (%q) — the survival above was meant to be incidental", deny)
	}
}

// TestGraphAtlassianWriteNeedsNoBypass pins the second half of the Phase 1
// Atlassian finding, and the more important one: the graph never needs to
// defeat CheckAtlassianAuthority, because that check does not sit on the bus
// path at all. Send gates commit, prompt and graph-node authority; an Atlassian
// write is gated where plan runs the CLI, as plan, which holds the authority.
//
// story-to-spec's jira-update node dispatches to plan carrying a
// template-authored sentence asserting the user approved. The instruction is
// simply handed to the role that may act on it, so the gate is again the only
// control — and Defect A says any agent opens it.
func TestGraphAtlassianWriteNeedsNoBypass(t *testing.T) {
	pinCompiledAuthorities(t)
	useTempBusDir(t)

	m := NewMessage(graphSender, "plan", "request", "jira-write",
		"The user approved the tracker update: sync the story to the spec", "")
	if err := SendNoCC(runTestSession, m); err != nil {
		t.Fatalf("SendNoCC to plan: %v", err)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 1 || msgs[0].Action != "jira-write" {
		t.Fatalf("plan inbox = %+v, want the graph's jira-write request", msgs)
	}
	if deny := CheckAtlassianAuthority("plan", "jira", "update"); deny != "" {
		t.Errorf("plan was refused its own Jira write (%q) — then the dispatch would need a bypass and this finding changes", deny)
	}
}
