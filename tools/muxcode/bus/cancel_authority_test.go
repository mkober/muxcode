package bus

import (
	"strings"
	"testing"
	"time"
)

// TestCheckCancelAuthority pins the whole rule table, each refusal paired with
// the autonomous negative control that must stay free.
func TestCheckCancelAuthority(t *testing.T) {
	for _, tc := range []struct {
		actor, creator, state string
		allowed               bool
	}{
		{ActorUser, ActorUser, GraphRunRunning, true},
		{ActorUser, "auto", GraphRunRunning, true},
		{ActorUser, "", GraphRunRunning, true},
		{"edit", ActorUser, GraphRunRunning, false},
		{"auto", ActorUser, GraphRunRunning, false},
		{"plan", ActorUser, GraphRunComplete, false},
		{ActorUnknown, ActorUser, GraphRunRunning, false},
		{"edit", ActorUnknown, GraphRunRunning, false},
		{"edit", "", GraphRunRunning, false},
		{"edit", "auto", GraphRunRunning, true},
		{"auto", "auto", GraphRunRunning, true},
		{"spawn-abcd1234", "research", GraphRunRunning, true},
		{ActorUnknown, "auto", GraphRunRunning, true},
		{"edit", ActorUser, GraphRunCanceling, true},
		{"edit", ActorUser, GraphRunCanceled, true},
	} {
		run := &GraphRun{ID: "r-1", CreatedBy: tc.creator, State: tc.state}
		deny := CheckCancelAuthority(tc.actor, run)
		if (deny == "") != tc.allowed {
			t.Errorf("actor %q, creator %q, state %s: allowed=%v, deny %q", tc.actor, tc.creator, tc.state, tc.allowed, deny)
		}
		if deny != "" && (!strings.Contains(deny, "r-1") || !strings.Contains(deny, "let the user cancel it")) {
			t.Errorf("refusal must name the run and hand the decision to the user: %q", deny)
		}
	}
}

func cancelEvents(t *testing.T, event, runID string) []LifecycleEntry {
	t.Helper()
	entries, err := ReadLifecycleLog(runTestSession)
	if err != nil {
		t.Fatalf("read lifecycle: %v", err)
	}
	var out []LifecycleEntry
	for _, e := range entries {
		if e.Event == event && strings.Contains(e.Detail, runID) {
			out = append(out, e)
		}
	}
	return out
}

// TestCancelGraphRunGatesHumanRuns is MUX-182 defect 3 end to end: an agent's
// cancel of a user-launched run is refused and changes nothing, the user's own
// cancel succeeds, and graph-run-canceled names who cancelled.
func TestCancelGraphRunGatesHumanRuns(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	pinActor(t, "edit")
	if err := CancelGraphRun(runTestSession, run.ID); err == nil {
		t.Fatal("edit cancelled a run the user launched by hand")
	}
	if got, _ := ReadGraphRun(runTestSession, run.ID); got.State != GraphRunRunning {
		t.Errorf("a refused cancel moved the run to %q", got.State)
	}
	if refused := cancelEvents(t, "graph-cancel-refused", run.ID); len(refused) != 1 || refused[0].Source != "edit" {
		t.Errorf("want one graph-cancel-refused row sourced edit, got %+v", refused)
	}

	pinActor(t, "")
	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("the user's own cancel was refused: %v", err)
	}
	done := cancelEvents(t, "graph-run-canceled", run.ID)
	if len(done) != 1 || done[0].Source != ActorUser || !strings.Contains(done[0].Detail, "canceled by user") {
		t.Errorf("graph-run-canceled must record the user as actor, got %+v", done)
	}
}

// Negative control: an autonomous run stays freely cancellable by another agent,
// and the event names that agent.
func TestCancelGraphRunLeavesAgentRunsFree(t *testing.T) {
	pinActor(t, "auto")
	run := createTestRun(t, linearGraph())

	pinActor(t, "edit")
	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("edit refused on an auto-launched run: %v", err)
	}
	done := cancelEvents(t, "graph-run-canceled", run.ID)
	if len(done) != 1 || done[0].Source != "edit" || !strings.Contains(done[0].Detail, "canceled by edit") {
		t.Errorf("graph-run-canceled must record edit as actor, got %+v", done)
	}
}

// The check turns on the verified actor, so neither the daemon identity (which
// NormalizeBusRole maps to edit, the MUX-144 Phase 4 hole) nor an agent that
// claims to be the user through its environment nor one that strips its
// identity reaches a user-launched run.
func TestCancelGraphRunNotBypassedByIdentity(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	for name, pin := range map[string]func(){
		"daemon":            func() { pinActor(t, graphSender) },
		"AGENT_ROLE=user":   func() { pinAgentAncestry(t, "/usr/local/bin/claude"); t.Setenv("AGENT_ROLE", ActorUser) },
		"stripped identity": func() { pinAgentAncestry(t, "/usr/local/bin/codex") },
	} {
		pin()
		if err := CancelGraphRun(runTestSession, run.ID); err == nil {
			t.Errorf("%s cancelled a user-launched run", name)
		}
	}
	if got, _ := ReadGraphRun(runTestSession, run.ID); got.State != GraphRunRunning {
		t.Errorf("run moved to %q", got.State)
	}
}

// TestStopSpawnAuthorizedGatesRunWorkers: `spawn stop` of a user-run's worker
// is refused to an agent and left running; the same stop of an agent-run's
// worker, and the user's stop, go through.
func TestStopSpawnAuthorizedGatesRunWorkers(t *testing.T) {
	for _, tc := range []struct {
		creator, stopper string
		allowed          bool
	}{
		{ActorUser, "edit", false},
		{ActorUser, ActorUser, true},
		{"auto", "edit", true},
	} {
		pinActor(t, tc.creator)
		run := createTestRun(t, spawnCancelGraph())
		f := fakeLiveSpawns(t)
		f.distinctIDs = true
		step(t, runTestSession, run.ID)
		st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
		entry, ok := findSpawnByRole(runTestSession, st.TaskID)
		if !ok {
			t.Fatalf("no spawn entry for role %q", st.TaskID)
		}

		pinActor(t, tc.stopper)
		err := StopSpawnAuthorized(runTestSession, entry.ID)
		after, _ := GetSpawnEntry(runTestSession, entry.ID)
		if tc.allowed && (err != nil || after.Status != "stopped") {
			t.Errorf("creator %q, stopper %q: stop refused (%v), status %q", tc.creator, tc.stopper, err, after.Status)
		}
		if !tc.allowed && (err == nil || after.Status != "running" || len(f.killed) != 0) {
			t.Errorf("creator %q, stopper %q: stop went through (err %v, status %q, killed %v)", tc.creator, tc.stopper, err, after.Status, f.killed)
		}
	}
}

// TestStopAuthoritySerializedWithRetry pins the review must-fix of 2026-09-24:
// an agent authorized to finish the cleanup of a canceled human run must act on
// the state it was authorized on. A retry fired in the window between the
// decision and the stop blocks until the stop is done, and once it resumes the
// run, the same agent is refused. Both stop roads are driven: the cancel and a
// spawn stop.
func TestStopAuthoritySerializedWithRetry(t *testing.T) {
	for _, road := range []string{"graph cancel", "spawn stop"} {
		pinActor(t, "")
		run := createTestRun(t, spawnCancelGraph())
		f := fakeLiveSpawns(t)
		f.distinctIDs = true
		step(t, runTestSession, run.ID)
		st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
		entry, ok := findSpawnByRole(runTestSession, st.TaskID)
		if !ok {
			t.Fatalf("%s: no spawn entry for role %q", road, st.TaskID)
		}
		if err := UpdateGraphRunState(runTestSession, run.ID, GraphRunCanceled); err != nil {
			t.Fatal(err)
		}

		retried := make(chan error, 1)
		orig := runStopAuthorizedHook
		runStopAuthorizedHook = func() {
			runStopAuthorizedHook = orig
			go func() {
				_, err := RetryGraphRun(runTestSession, run.ID, "w")
				retried <- err
			}()
			select {
			case <-retried:
				t.Errorf("%s: a retry completed between the stop's authorization and the stop", road)
			case <-time.After(300 * time.Millisecond):
			}
		}
		t.Cleanup(func() { runStopAuthorizedHook = orig })

		pinActor(t, "edit")
		var err error
		if road == "graph cancel" {
			err = CancelGraphRun(runTestSession, run.ID)
		} else {
			err = StopSpawnAuthorized(runTestSession, entry.ID)
		}
		if err != nil {
			t.Fatalf("%s: finishing a canceled run's cleanup must stay open to an agent: %v", road, err)
		}
		select {
		case rerr := <-retried:
			if rerr != nil {
				t.Fatalf("%s: retry after the stop: %v", road, rerr)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: retry never ran — the hook did not fire or the lock was never released", road)
		}
		if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunRunning {
			t.Fatalf("%s: retry left the run %q, want running", road, r.State)
		}
		if err := CancelGraphRun(runTestSession, run.ID); err == nil {
			t.Errorf("%s: edit cancelled the user's run after a retry resumed it", road)
		}
	}
}
