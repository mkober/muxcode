package daemon

import (
	"os"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Tests for the once-per-completion reviewed-transition gate (MUX-007):
// one review completion fires exactly one verify-spec at plan, unrelated
// edit-inbox growth never re-fires, a genuine new completion does, and the
// on-disk marker holds across a daemon restart.
//
// MUXCODE_DEDUP_WINDOW is set to 0 throughout: the observed echo storms
// fired outside the 30s log-dedup window, so the window must not be what
// makes these tests pass. Plan's inbox is drained between fires for the
// same reason — HasPendingInboxRequest would otherwise suppress a
// duplicate send and mask a missing gate.

// seedEditInbox appends a message directly to edit's inbox file, bypassing
// Send, so tests control exactly what sits unconsumed.
func seedEditInbox(t *testing.T, session string, m bus.Message) {
	t.Helper()
	data, err := bus.EncodeMessage(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	f, err := os.OpenFile(bus.InboxPath(session, "edit"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open inbox: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
}

// countVerifySpec returns how many verify-spec requests sit in plan's inbox.
func countVerifySpec(t *testing.T, session string) int {
	t.Helper()
	msgs, err := bus.Peek(session, "plan")
	if err != nil {
		t.Fatalf("peek plan: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Action == "verify-spec" {
			n++
		}
	}
	return n
}

// drainPlanInbox empties plan's inbox so the pending-duplicate send guard
// cannot stand in for the reviewed-transition gate.
func drainPlanInbox(t *testing.T, session string) {
	t.Helper()
	if err := os.WriteFile(bus.InboxPath(session, "plan"), nil, 0644); err != nil {
		t.Fatalf("drain plan inbox: %v", err)
	}
}

func TestCheckInboxes_VerifySpecOncePerReviewCompletion(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	seedRepoSpec(t, session)
	d := New(session, 5, 2)

	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM", "req-1"))
	d.checkInboxes()

	if got := countVerifySpec(t, session); got != 1 {
		t.Fatalf("expected 1 verify-spec after review completion, got %d", got)
	}
	if st := bus.ReadWorkflowState(session).State; st != bus.StateReviewed {
		t.Fatalf("expected reviewed state, got %s", st)
	}

	// Unrelated growth while the review message sits unconsumed: the storm
	// shape — plan's own reply, then a build response — must not re-fire.
	drainPlanInbox(t, session)
	bus.TransitionWorkflow(session, bus.StateEditing, "test:new-work")
	seedEditInbox(t, session, bus.NewMessage("plan", "edit", "response", "verify-spec", "verified, nothing new", "req-2"))
	d.checkInboxes()
	seedEditInbox(t, session, bus.NewMessage("build", "edit", "response", "build", "Build OK", "req-3"))
	d.checkInboxes()

	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("unrelated inbox growth re-fired verify-spec %d time(s)", got)
	}
	if st := bus.ReadWorkflowState(session).State; st != bus.StateEditing {
		t.Errorf("unrelated inbox growth re-transitioned workflow to %s", st)
	}

	// A genuine second completion (new review→edit message ID) fires again.
	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM round 2", "req-4"))
	d.checkInboxes()

	if got := countVerifySpec(t, session); got != 1 {
		t.Errorf("expected 1 verify-spec after second review completion, got %d", got)
	}
	if st := bus.ReadWorkflowState(session).State; st != bus.StateReviewed {
		t.Errorf("expected reviewed state after second completion, got %s", st)
	}
}

func TestCheckInboxes_ReviewCCDoesNotFire(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	seedRepoSpec(t, session)
	d := New(session, 5, 2)

	// An auto-CC'd review→test response in edit's inbox — the observed false
	// trigger (never a review→edit report) — must not read as a completion.
	seedEditInbox(t, session, bus.NewMessage("review", "test", "response", "review", "review of test task", "test-req-1"))
	d.checkInboxes()

	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("CC copy fired verify-spec %d time(s)", got)
	}
	if st := bus.ReadWorkflowState(session).State; st == bus.StateReviewed {
		t.Error("CC copy transitioned workflow to reviewed")
	}
}

// TestCheckInboxes_MarkerWriteFailureWithholdsTransition pins the fail-closed
// error path: a completion whose marker cannot be recorded must not transition
// or notify plan (firing unrecorded replays the same completion on every later
// growth — the storm itself), and must fire exactly once when the marker
// becomes writable again — fail-closed is not fail-forever.
func TestCheckInboxes_MarkerWriteFailureWithholdsTransition(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	seedRepoSpec(t, session)
	d := New(session, 5, 2)

	// A directory at the marker path makes the atomic rename fail.
	if err := os.Mkdir(bus.ReviewedMarkerPath(session), 0755); err != nil {
		t.Fatalf("mkdir marker path: %v", err)
	}

	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM", "req-1"))
	d.checkInboxes()

	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("verify-spec fired despite marker write failure: %d time(s)", got)
	}
	if st := bus.ReadWorkflowState(session).State; st == bus.StateReviewed {
		t.Error("workflow transitioned to reviewed despite marker write failure")
	}

	if err := os.Remove(bus.ReviewedMarkerPath(session)); err != nil {
		t.Fatalf("remove blocking dir: %v", err)
	}
	seedEditInbox(t, session, bus.NewMessage("build", "edit", "response", "build", "Build OK", "req-2"))
	d.checkInboxes()

	if got := countVerifySpec(t, session); got != 1 {
		t.Errorf("expected withheld completion to fire once after recovery, got %d", got)
	}
	if st := bus.ReadWorkflowState(session).State; st != bus.StateReviewed {
		t.Errorf("expected reviewed state after recovery, got %s", st)
	}
}

func TestCheckInboxes_ReviewedMarkerSurvivesDaemonRestart(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	seedRepoSpec(t, session)

	d := New(session, 5, 2)
	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM", "req-1"))
	d.checkInboxes()
	if got := countVerifySpec(t, session); got != 1 {
		t.Fatalf("expected 1 verify-spec before restart, got %d", got)
	}
	drainPlanInbox(t, session)

	// Fresh daemon, empty inbox-size map: the stale unconsumed review
	// message reads as growth again, and only the on-disk marker stops it.
	d2 := New(session, 5, 2)
	d2.checkInboxes()

	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("daemon restart replayed a stale review completion %d time(s)", got)
	}
}
