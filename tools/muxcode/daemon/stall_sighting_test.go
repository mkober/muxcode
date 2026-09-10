package daemon

import "testing"

func newStallDaemon() *Daemon {
	return &Daemon{
		taskStallSeen:   make(map[string]int),
		taskRedrives:    make(map[string]int),
		stallBusyLogged: make(map[string]bool),
	}
}

// drive feeds a sequence of verdicts and returns the decision from each.
func drive(d *Daemon, id string, seq ...stallVerdict) []stallDecision {
	out := make([]stallDecision, 0, len(seq))
	for _, v := range seq {
		out = append(out, d.noteStallSighting(id, v))
	}
	return out
}

func wantDecisions(t *testing.T, got []stallDecision, want ...stallDecision) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d decisions, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sighting %d: got decision %d, want %d (full: %v)", i+1, got[i], want[i], got)
		}
	}
}

// TestNoteStallSighting_WorkingResetsDebounce is the sequence the static
// busy/rest panes cannot reach: an agent that stirs BETWEEN polls. The third
// sighting is the discriminating one — without the reset the task already has
// two at-rest sightings banked and fires the interrupting re-drive mid-turn.
func TestNoteStallSighting_WorkingResetsDebounce(t *testing.T) {
	d := newStallDaemon()
	wantDecisions(t,
		drive(d, "T1", stallAtRest, stallWorking, stallAtRest, stallAtRest),
		stallHold,    // 1st at-rest: banked
		stallLogBusy, // agent stirred — count cleared
		stallHold,    // fresh 1st at-rest, NOT the banked 2nd
		stallRedrive, // fresh 2nd
	)
}

// A pane that was READ and found neither working nor deliverable (dead, or
// showing no recoverable prompt) must reset the debounce too — it is not
// evidence of rest. The polls that read nothing at all are stallUnknown, below.
func TestNoteStallSighting_NotAtRestResetsDebounce(t *testing.T) {
	d := newStallDaemon()
	wantDecisions(t,
		drive(d, "T1", stallAtRest, stallNotAtRest, stallAtRest, stallAtRest),
		stallHold,
		stallHold,
		stallHold,
		stallRedrive,
	)
}

// TestNoteStallSighting_UnknownResetsDebounce covers the polls that observe
// nothing: a harness role, a reload in progress, a failed capture. The caller
// used to `continue` past the bookkeeping on all three, so a prior at-rest
// sighting stayed banked across the blind gap and ONE fresh reading fired the
// re-drive. The third sighting is the discriminating one.
func TestNoteStallSighting_UnknownResetsDebounce(t *testing.T) {
	d := newStallDaemon()
	wantDecisions(t,
		drive(d, "T1", stallAtRest, stallUnknown, stallAtRest, stallAtRest),
		stallHold,    // 1st at-rest: banked
		stallHold,    // blind poll — count cleared
		stallHold,    // fresh 1st at-rest, NOT the banked 2nd
		stallRedrive, // fresh 2nd
	)
}

// An unobservable poll must not be mistaken for a busy one: it writes no
// stall-skipped-busy row, since nothing was seen to be working.
func TestNoteStallSighting_UnknownIsNotBusy(t *testing.T) {
	d := newStallDaemon()
	wantDecisions(t, drive(d, "T1", stallUnknown, stallUnknown), stallHold, stallHold)
	if d.stallBusyLogged["T1"] {
		t.Error("a blind poll latched the busy log — that row means the agent was SEEN working")
	}
}

// TestNoteStallSighting_BusyLoggedOncePerTask pins the lifecycle row against a
// 5s poll loop: without the latch a task withheld for ten minutes writes a row
// every poll and buries the log it exists to make legible.
func TestNoteStallSighting_BusyLoggedOncePerTask(t *testing.T) {
	d := newStallDaemon()
	wantDecisions(t,
		drive(d, "T1", stallWorking, stallWorking, stallWorking),
		stallLogBusy,
		stallHold,
		stallHold,
	)

	// The latch is per task, not global — a second task still gets its row.
	if got := d.noteStallSighting("T2", stallWorking); got != stallLogBusy {
		t.Errorf("a different task must log its own busy row, got %d", got)
	}
}

// TestNoteStallSighting_RedriveCapAndGiveUp pins the cap: two re-drives, one
// give-up row, then silence — the task timeout owns it from there.
func TestNoteStallSighting_RedriveCapAndGiveUp(t *testing.T) {
	d := newStallDaemon()
	wantDecisions(t,
		drive(d, "T1",
			stallAtRest, stallAtRest, // redrive 1
			stallAtRest, stallAtRest, // redrive 2
			stallAtRest, stallAtRest, // cap reached
			stallAtRest, stallAtRest, // and stays reached
		),
		stallHold, stallRedrive,
		stallHold, stallRedrive,
		stallHold, stallGiveUp,
		stallHold, stallHold,
	)
}

// TestForgetCompletedTasks covers the cleanup on completion, including the part
// a map-size check would miss: the busy latch must re-arm for a task id that
// comes back, or a retried task is withheld silently.
func TestForgetCompletedTasks(t *testing.T) {
	d := newStallDaemon()
	drive(d, "gone", stallWorking, stallAtRest)
	drive(d, "live", stallWorking, stallAtRest)

	d.forgetCompletedTasks(map[string]bool{"live": true})

	if _, ok := d.taskStallSeen["gone"]; ok {
		t.Error("a completed task's sighting count must be dropped")
	}
	if d.stallBusyLogged["gone"] {
		t.Error("a completed task's busy latch must be dropped")
	}
	if _, ok := d.taskStallSeen["live"]; !ok {
		t.Error("an in-flight task's bookkeeping must survive (negative control)")
	}
	if !d.stallBusyLogged["live"] {
		t.Error("an in-flight task's busy latch must survive (negative control)")
	}

	if got := d.noteStallSighting("gone", stallWorking); got != stallLogBusy {
		t.Errorf("the latch must re-arm once the task is gone, got %d", got)
	}
	if got := d.noteStallSighting("live", stallWorking); got != stallHold {
		t.Errorf("a surviving latch must still suppress, got %d", got)
	}
}

// The redrive cap must be forgotten too, or a task id that returns is capped
// before its first re-drive.
func TestForgetCompletedTasks_ClearsRedriveCap(t *testing.T) {
	d := newStallDaemon()
	drive(d, "T1", stallAtRest, stallAtRest, stallAtRest, stallAtRest)
	if d.taskRedrives["T1"] != 2 {
		t.Fatalf("expected 2 redrives banked, got %d", d.taskRedrives["T1"])
	}

	d.forgetCompletedTasks(map[string]bool{})

	wantDecisions(t, drive(d, "T1", stallAtRest, stallAtRest), stallHold, stallRedrive)
}
