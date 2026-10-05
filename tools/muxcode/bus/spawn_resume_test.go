package bus

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const spawnResumeTestID = "8a744341-11bf-440f-b5d2-49248447a9c0"

// deadWorkerFake drives resumeDeadWorkers' seams at the tmux boundary: which
// workers read dead, whether their pane offers a session, the launch keys
// typed, and what the resumed pane shows. Everything between — pending
// verification, the reseed, the node outcome — runs for real.
type deadWorkerFake struct {
	dead      map[string]bool
	banner    map[string]bool
	pane      map[string]string
	capErr    map[string]error
	relaunchd []string
}

func fakeDeadWorkers(t *testing.T) *deadWorkerFake {
	t.Helper()
	t.Setenv("MUXCODE_EDIT_CLI", "claude")
	t.Setenv(autoResumeDisableEnv, "")
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	f := &deadWorkerFake{dead: map[string]bool{}, banner: map[string]bool{},
		pane: map[string]string{}, capErr: map[string]error{}}
	origAlive, origID, origLaunch, origCapture := spawnWorkerAliveFn, spawnWorkerResumeIDFn, spawnWorkerLaunchFn, spawnWorkerCaptureFn
	t.Cleanup(func() {
		spawnWorkerAliveFn, spawnWorkerResumeIDFn, spawnWorkerLaunchFn, spawnWorkerCaptureFn = origAlive, origID, origLaunch, origCapture
		deadWorkerMu.Lock()
		deadWorkerSince = map[string]int64{}
		deadWorkerMu.Unlock()
	})
	spawnWorkerAliveFn = func(_ string, e SpawnEntry) bool { return !f.dead[e.SpawnRole] }
	spawnWorkerResumeIDFn = func(_ string, e SpawnEntry) (string, bool) {
		if f.banner[e.SpawnRole] {
			return spawnResumeTestID, true
		}
		return "", false
	}
	spawnWorkerLaunchFn = func(_ string, e SpawnEntry, id string) error {
		f.relaunchd = append(f.relaunchd, e.SpawnRole+" "+id)
		f.dead[e.SpawnRole] = false
		return nil
	}
	spawnWorkerCaptureFn = func(_ string, e SpawnEntry) (string, error) {
		if err := f.capErr[e.SpawnRole]; err != nil {
			return "", err
		}
		if p, ok := f.pane[e.SpawnRole]; ok {
			return p, nil
		}
		return resumedPane("\n╭─ Claude Code ─╮\n\n❯ "), nil
	}
	return f
}

// resumedPane is a worker pane after its resume: the old session's exit
// banner, the typed resume launch line, then body — what the new session drew.
func resumedPane(body string) string {
	return "❯ old turn\nResume this session with:\nclaude --resume " + spawnResumeTestID +
		"\n❯ AGENT_ROLE=spawn-x muxcode agent launch edit --reason spawn --resume " + spawnResumeTestID + body
}

// definitionlessWorkerPane is a resumed worker whose new session announced its
// agent unavailable after the exit banner — the MUX-136 shape.
var definitionlessWorkerPane = resumedPane(
	"\nAgent code-editor, which is no longer available; the agent's tool restrictions no longer apply.\n❯ ")

// ageReady backdates a worker's first ready sighting past resumeSettleSecs.
func ageReady(t *testing.T, worker string) {
	t.Helper()
	if err := UpdateSpawnEntry(runTestSession, worker, func(e *SpawnEntry) {
		e.ReadyAt -= resumeSettleSecs + 1
	}); err != nil {
		t.Fatal(err)
	}
}

// settleResume steps a resumed worker through its first ready sighting and the
// settle window, the road a clean resume takes to its reseed.
func settleResume(t *testing.T, runID, worker string) {
	t.Helper()
	step(t, runTestSession, runID)
	if e, _ := GetSpawnEntry(runTestSession, worker); e.ReadyAt == 0 || e.ResumeID == "" {
		t.Fatalf("first ready sighting not recorded: ready_at %d pending %q", e.ReadyAt, e.ResumeID)
	}
	ageReady(t, worker)
	step(t, runTestSession, runID)
}

// resumeWorker kills the node's worker with a resume banner and steps once,
// returning the worker and its entry from before the resume.
func resumeWorker(t *testing.T, f *deadWorkerFake, runID string) (string, SpawnEntry) {
	t.Helper()
	step(t, runTestSession, runID)
	worker := nodeTaskID(t, runID)
	before, _ := GetSpawnEntry(runTestSession, worker)
	f.dead[worker], f.banner[worker] = true, true
	step(t, runTestSession, runID)
	return worker, before
}

// ageResume backdates a worker's pending resume past resumeVerifySecs.
func ageResume(t *testing.T, worker string) {
	t.Helper()
	if err := UpdateSpawnEntry(runTestSession, worker, func(e *SpawnEntry) {
		e.ResumedAt -= resumeVerifySecs + 1
	}); err != nil {
		t.Fatal(err)
	}
}

func spawnNodeGraph() *Graph {
	return &Graph{Name: "dead", Start: "w",
		Nodes: []Node{{ID: "w", Type: NodeSpawn, Role: "edit", Message: "implement phase"}}}
}

// ageDeadSighting backdates a worker's first dead sighting past the confirm window.
func ageDeadSighting(role string) {
	deadWorkerMu.Lock()
	defer deadWorkerMu.Unlock()
	for r, at := range deadWorkerSince {
		if r == role {
			deadWorkerSince[r] = at - deadWorkerConfirmSecs - 1
		}
	}
}

// A worker that exited mid-task with a resume banner is resumed into its
// conversation, held unseeded until its pane verifies clean, then reseeded;
// its answer to the new seed completes the node — the 2026-09-02 stall,
// recovered.
func TestExecSpawnDeadWorkerResumed(t *testing.T) {
	run := createTestRun(t, spawnNodeGraph())
	live := fakeLiveSpawns(t)
	f := fakeDeadWorkers(t)

	worker, before := resumeWorker(t, f, run.ID)

	if len(f.relaunchd) != 1 || f.relaunchd[0] != worker+" "+spawnResumeTestID {
		t.Fatalf("relaunches = %v, want [%s %s]", f.relaunchd, worker, spawnResumeTestID)
	}
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeRunning || st.TaskID != worker || st.Redrives != 1 || live.fresh != 1 {
		t.Fatalf("resume must keep the same worker running: state %q task %q redrives %d starts %d",
			st.State, st.TaskID, st.Redrives, live.fresh)
	}
	if held, _ := GetSpawnEntry(runTestSession, worker); held.SeedMsgID != before.SeedMsgID || held.ResumeID != spawnResumeTestID {
		t.Fatalf("an unverified resume was reseeded (seed %s→%s, pending %q)", before.SeedMsgID, held.SeedMsgID, held.ResumeID)
	}

	settleResume(t, run.ID, worker)
	after, _ := GetSpawnEntry(runTestSession, worker)
	if after.ResumeID != "" {
		t.Errorf("verified worker still pending: %q", after.ResumeID)
	}
	if after.SeedMsgID == before.SeedMsgID || !strings.HasPrefix(after.Task, "[resumed]") ||
		!strings.Contains(after.Task, before.Task) {
		t.Fatalf("resumed worker must be reseeded with its task: seed %s→%s task %q", before.SeedMsgID, after.SeedMsgID, after.Task)
	}
	if n := countLifecycleEvents(t, runTestSession, "graph-spawn-resumed"); n != 1 {
		t.Errorf("graph-spawn-resumed rows = %d, want 1", n)
	}

	answerSpawn(t, runTestSession, worker)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeDone {
		t.Fatalf("w state %q, want done after the resumed worker answered", s)
	}
}

// A dead worker with no session to resume fails its node loudly — after the
// confirm window, never on one sighting — and is stopped, so a retry starts
// fresh instead of reusing the corpse.
func TestExecSpawnDeadWorkerUnresumableFailsLoudly(t *testing.T) {
	cases := []struct {
		name, reason string
		setup        func(t *testing.T, f *deadWorkerFake, worker string)
	}{
		{"no banner", "no resumable session", func(*testing.T, *deadWorkerFake, string) {}},
		{"provider cannot resume", "cannot resume", func(t *testing.T, f *deadWorkerFake, w string) {
			t.Setenv("MUXCODE_EDIT_CLI", "opencode")
			f.banner[w] = true
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := createTestRun(t, spawnNodeGraph())
			live := fakeLiveSpawns(t)
			f := fakeDeadWorkers(t)
			step(t, runTestSession, run.ID)
			worker := nodeTaskID(t, run.ID)

			f.dead[worker] = true
			c.setup(t, f, worker)
			step(t, runTestSession, run.ID)
			if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeRunning {
				t.Fatalf("one dead sighting failed the node (state %q) — must wait out the confirm window", s)
			}

			ageDeadSighting(worker)
			step(t, runTestSession, run.ID)
			st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
			if st.State != GraphNodeFailed || !strings.Contains(st.Output, "could not be resumed") || !strings.Contains(st.Output, c.reason) {
				t.Fatalf("unresumable dead worker must fail the node naming %q, got %q %q", c.reason, st.State, st.Output)
			}
			if len(f.relaunchd) != 0 || live.fresh != 1 {
				t.Errorf("an unresumable worker was relaunched (%v) or replaced (%d starts)", f.relaunchd, live.fresh)
			}
			if e, _ := GetSpawnEntry(runTestSession, worker); e.Status != "stopped" {
				t.Errorf("dead worker status %q, want stopped", e.Status)
			}
		})
	}
}

// The opt-out restores the executor as it was before MUX-139: a dead worker,
// banner or not, is neither resumed, failed nor stopped past the confirm
// window. Its opposite is the "no banner" case above, which fails and stops.
func TestExecSpawnDeadWorkerOptOutLeavesExecutorAlone(t *testing.T) {
	for _, banner := range []bool{true, false} {
		t.Run(fmt.Sprintf("banner=%v", banner), func(t *testing.T) {
			run := createTestRun(t, spawnNodeGraph())
			live := fakeLiveSpawns(t)
			f := fakeDeadWorkers(t)
			t.Setenv(autoResumeDisableEnv, "1")
			step(t, runTestSession, run.ID)
			worker := nodeTaskID(t, run.ID)

			f.dead[worker], f.banner[worker] = true, banner
			step(t, runTestSession, run.ID)
			ageDeadSighting(worker)
			step(t, runTestSession, run.ID)

			if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeRunning {
				t.Errorf("opt-out failed the node (state %q)", s)
			}
			if e, _ := GetSpawnEntry(runTestSession, worker); e.Status != "running" || e.ResumeID != "" {
				t.Errorf("opt-out touched the worker: status %q pending %q", e.Status, e.ResumeID)
			}
			if len(f.relaunchd) != 0 || live.fresh != 1 {
				t.Errorf("opt-out relaunched (%v) or replaced (%d starts)", f.relaunchd, live.fresh)
			}
		})
	}
}

// A resumed worker whose new session announces its definition unavailable is
// stopped and its node failed, and is never handed its task.
func TestExecSpawnResumedWithoutDefinitionIsStopped(t *testing.T) {
	run := createTestRun(t, spawnNodeGraph())
	fakeLiveSpawns(t)
	f := fakeDeadWorkers(t)
	worker, before := resumeWorker(t, f, run.ID)

	f.pane[worker] = definitionlessWorkerPane
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "without its agent definition") {
		t.Fatalf("definition-less resume must fail the node, got %q %q", st.State, st.Output)
	}
	e, _ := GetSpawnEntry(runTestSession, worker)
	if e.Status != "stopped" {
		t.Errorf("definition-less worker status %q, want stopped", e.Status)
	}
	if e.SeedMsgID != before.SeedMsgID {
		t.Errorf("a definition-less worker was handed its task (seed %s→%s)", before.SeedMsgID, e.SeedMsgID)
	}
}

// A failed capture is no verdict: the worker stays pending and unseeded, and
// a later clean capture reseeds it. Unread past resumeVerifySecs, the node
// fails loudly instead of stalling.
func TestExecSpawnResumedCaptureFailureStaysPending(t *testing.T) {
	t.Run("then clean", func(t *testing.T) {
		run := createTestRun(t, spawnNodeGraph())
		fakeLiveSpawns(t)
		f := fakeDeadWorkers(t)
		worker, before := resumeWorker(t, f, run.ID)

		f.capErr[worker] = errors.New("capture-pane: no such pane")
		step(t, runTestSession, run.ID)
		e, _ := GetSpawnEntry(runTestSession, worker)
		if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeRunning || e.SeedMsgID != before.SeedMsgID || e.ResumeID == "" {
			t.Fatalf("failed capture must hold: state %q seed moved %v pending %q", s, e.SeedMsgID != before.SeedMsgID, e.ResumeID)
		}

		delete(f.capErr, worker)
		settleResume(t, run.ID, worker)
		if e, _ := GetSpawnEntry(runTestSession, worker); e.SeedMsgID == before.SeedMsgID || e.ResumeID != "" {
			t.Errorf("clean capture did not reseed: seed moved %v pending %q", e.SeedMsgID != before.SeedMsgID, e.ResumeID)
		}
	})
	t.Run("unread past the bound", func(t *testing.T) {
		run := createTestRun(t, spawnNodeGraph())
		fakeLiveSpawns(t)
		f := fakeDeadWorkers(t)
		worker, _ := resumeWorker(t, f, run.ID)

		f.capErr[worker] = errors.New("capture-pane: no such pane")
		ageResume(t, worker)
		step(t, runTestSession, run.ID)
		st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
		if st.State != GraphNodeFailed || !strings.Contains(st.Output, "could not be read") {
			t.Fatalf("unverifiable resume must fail the node, got %q %q", st.State, st.Output)
		}
	})
}

// A pane mid-startup — launcher text, a blank screen, a shell ❯ above the
// launch line — is not a verified resume, and neither is a first clean prompt:
// a definition warning drawn after it, inside the settle window, still stops
// the worker before it is ever handed its task.
func TestExecSpawnResumedDelayedWarningIsStopped(t *testing.T) {
	run := createTestRun(t, spawnNodeGraph())
	fakeLiveSpawns(t)
	f := fakeDeadWorkers(t)
	worker, before := resumeWorker(t, f, run.ID)

	for _, startup := range []string{"", "\n", "\n\n   \n"} {
		f.pane[worker] = resumedPane(startup)
		step(t, runTestSession, run.ID)
		if e, _ := GetSpawnEntry(runTestSession, worker); e.ReadyAt != 0 || e.SeedMsgID != before.SeedMsgID {
			t.Fatalf("startup pane %q read as ready: ready_at %d seed moved %v", startup, e.ReadyAt, e.SeedMsgID != before.SeedMsgID)
		}
	}

	f.pane[worker] = resumedPane("\n╭─ Claude Code ─╮\n\n❯ ")
	step(t, runTestSession, run.ID)
	if e, _ := GetSpawnEntry(runTestSession, worker); e.ReadyAt == 0 || e.SeedMsgID != before.SeedMsgID {
		t.Fatalf("first clean prompt must record readiness without reseeding: ready_at %d seed moved %v", e.ReadyAt, e.SeedMsgID != before.SeedMsgID)
	}

	f.pane[worker] = resumedPane("\n╭─ Claude Code ─╮\n\nAgent code-editor, which is no longer available; the agent's tool restrictions no longer apply.\n❯ ")
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "without its agent definition") {
		t.Fatalf("delayed warning must fail the node, got %q %q", st.State, st.Output)
	}
	if e, _ := GetSpawnEntry(runTestSession, worker); e.Status != "stopped" || e.SeedMsgID != before.SeedMsgID {
		t.Errorf("delayed-warning worker: status %q, handed its task %v", e.Status, e.SeedMsgID != before.SeedMsgID)
	}
}

// A stop that fails with the window still live keeps the node running and the
// worker supervised — never a finished node over a live, definition-less
// worker — and the next tick retries it; the node fails only once the stop is
// confirmed, with the reason recorded when it was refused.
func TestExecSpawnFailedStopIsRetriedBeforeFailingNode(t *testing.T) {
	run := createTestRun(t, spawnNodeGraph())
	live := fakeLiveSpawns(t)
	f := fakeDeadWorkers(t)
	worker, before := resumeWorker(t, f, run.ID)

	kills := 0
	spawnKillWindowFn = func(string, string) error {
		kills++
		return errors.New("kill-window: server busy")
	}
	f.pane[worker] = definitionlessWorkerPane
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeRunning {
		t.Fatalf("node %q after a failed stop — finishing it abandons the live worker", s)
	}
	e, _ := GetSpawnEntry(runTestSession, worker)
	if e.Status != "running" || e.StopPending == "" {
		t.Fatalf("failed stop not held: status %q stop_pending %q", e.Status, e.StopPending)
	}
	if n := countLifecycleEvents(t, runTestSession, "graph-spawn-stop-failed"); n != 1 {
		t.Errorf("graph-spawn-stop-failed rows = %d, want 1", n)
	}

	step(t, runTestSession, run.ID)
	if kills != 2 || nodeState(t, runTestSession, run.ID, "w") != GraphNodeRunning {
		t.Fatalf("stop not retried while failing: kills %d state %q", kills, nodeState(t, runTestSession, run.ID, "w"))
	}

	spawnKillWindowFn = func(string, string) error { return nil }
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "without its agent definition") {
		t.Fatalf("confirmed stop must fail the node with the refusal reason, got %q %q", st.State, st.Output)
	}
	if e, _ := GetSpawnEntry(runTestSession, worker); e.Status != "stopped" || e.SeedMsgID != before.SeedMsgID {
		t.Errorf("worker after confirmed stop: status %q, handed its task %v", e.Status, e.SeedMsgID != before.SeedMsgID)
	}
	if live.fresh != 1 {
		t.Errorf("a replacement worker started while the stop was pending (%d starts)", live.fresh)
	}
}

// Readiness reads what the resumed session drew last, so it survives Claude
// redrawing the screen over the banner and launch line, and is never fooled by
// the launch line (whole or wrapped under a ❯-drawn shell prompt), an older
// composer in scrollback, or a session that has exited again.
func TestResumedSessionReady(t *testing.T) {
	banner := "Resume this session with:\nclaude --resume " + spawnResumeTestID + "\n"
	launch := "❯ AGENT_ROLE=spawn-x muxcode agent launch edit --reason spawn --resume " + spawnResumeTestID + "\n"
	scrollback := "❯ old turn\n" + strings.Repeat("output line\n", 20)
	cases := []struct {
		name, pane string
		want       bool
	}{
		{"composer below the banner", "❯ old turn\n" + banner + launch + "╭─ Claude Code ─╮\n\n❯ \n? for shortcuts", true},
		{"screen redrawn over banner and launch line", "╭─ Claude Code ─╮\n\n❯ \n? for shortcuts", true},
		{"wrapped launch line, nothing drawn yet", "❯ old turn\n" + banner + "❯ AGENT_ROLE=spawn-x muxcode agent\nlaunch edit --reason spawn --resume " + spawnResumeTestID + "\n", false},
		{"launch line only", "❯ old turn\n" + banner + launch, false},
		{"blank startup", "❯ old turn\n" + banner + launch + "\n\n", false},
		{"older composer only in scrollback", scrollback + "$ muxcode agent launch edit --resume " + spawnResumeTestID + "\n", false},
		{"session exited again", banner + launch + "❯ \n" + banner + "$ ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resumedSessionReady(c.pane); got != c.want {
				t.Errorf("resumedSessionReady = %v, want %v for:\n%s", got, c.want, c.pane)
			}
		})
	}
}

// A pending stop outranks the node timeout and lost-window replacement: the
// node neither times out over a live worker nor replaces it, and a stop that
// lands — here because the window went away — fails the node with the reason
// recorded when it was refused.
func TestExecSpawnPendingStopOutranksTimeoutAndReplacement(t *testing.T) {
	g := spawnNodeGraph()
	g.Nodes[0].TimeoutSec = 3600
	run := createTestRun(t, g)
	live := fakeLiveSpawns(t)
	f := fakeDeadWorkers(t)
	worker, _ := resumeWorker(t, f, run.ID)

	spawnKillWindowFn = func(string, string) error { return errors.New("kill-window: server busy") }
	f.pane[worker] = definitionlessWorkerPane
	step(t, runTestSession, run.ID)
	if err := MutateNodeStatus(runTestSession, run.ID, "w", func(s *GraphNodeStatus) { s.StartedAt -= 7200 }); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)
	if st, _ := ReadNodeStatus(runTestSession, run.ID, "w"); st.State != GraphNodeRunning {
		t.Fatalf("node %q (%q) — the timeout finished it over a worker still owed a stop", st.State, st.Output)
	}

	live.deadWindows[worker] = true
	if _, err := RefreshSpawnStatus(runTestSession); err != nil {
		t.Fatal(err)
	}
	if e, _ := GetSpawnEntry(runTestSession, worker); e.Status != "completed" || e.StopPending == "" {
		t.Fatalf("fixture: the daemon's refresh should leave a completed entry still owed a stop, got %q %q", e.Status, e.StopPending)
	}
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "without its agent definition") {
		t.Fatalf("node must fail with the refusal reason once stopped, got %q %q", st.State, st.Output)
	}
	if live.fresh != 1 {
		t.Errorf("a worker owed a stop was replaced (%d starts)", live.fresh)
	}
	if e, _ := GetSpawnEntry(runTestSession, worker); e.StopPending != "" {
		t.Errorf("confirmed stop left the mark: %q", e.StopPending)
	}
}

// An entry a manual stop marked "stopped" while its window survived is still
// owed: its window is killed before the node fails with the saved reason, and
// a kill that keeps failing holds the node rather than abandoning the window.
func TestExecSpawnPendingStopOnStoppedEntry(t *testing.T) {
	run := createTestRun(t, spawnNodeGraph())
	live := fakeLiveSpawns(t)
	f := fakeDeadWorkers(t)
	worker, _ := resumeWorker(t, f, run.ID)

	spawnKillWindowFn = func(string, string) error { return errors.New("kill-window: server busy") }
	f.pane[worker] = definitionlessWorkerPane
	step(t, runTestSession, run.ID)
	if err := UpdateSpawnEntry(runTestSession, worker, func(e *SpawnEntry) { e.Status = "stopped" }); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeRunning {
		t.Fatalf("node %q while a stopped entry's window survives", s)
	}

	var killed []string
	spawnKillWindowFn = func(_, w string) error { killed = append(killed, w); live.deadWindows[w] = true; return nil }
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "without its agent definition") {
		t.Fatalf("node must fail with the refusal reason once the window is gone, got %q %q", st.State, st.Output)
	}
	if len(killed) != 1 || live.fresh != 1 {
		t.Errorf("kills %v starts %d, want one kill and no replacement", killed, live.fresh)
	}
}

// A worker that keeps dying is resumed only up to the shared redrive cap.
func TestExecSpawnDeadWorkerResumeCapped(t *testing.T) {
	run := createTestRun(t, spawnNodeGraph())
	fakeLiveSpawns(t)
	f := fakeDeadWorkers(t)
	step(t, runTestSession, run.ID)
	worker := nodeTaskID(t, run.ID)
	if err := MutateNodeStatus(runTestSession, run.ID, "w", func(s *GraphNodeStatus) { s.Redrives = graphRedriveMax }); err != nil {
		t.Fatal(err)
	}

	f.dead[worker], f.banner[worker] = true, true
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "resumes exhausted") || len(f.relaunchd) != 0 {
		t.Fatalf("capped resume must fail the node, got %q %q relaunches %v", st.State, st.Output, f.relaunchd)
	}
}

// Negative controls: a live worker, and a dead one that already answered
// (parked between iterations), are never resumed or failed.
func TestExecSpawnDeadWorkerLeavesLiveAndAnsweredAlone(t *testing.T) {
	t.Run("live worker", func(t *testing.T) {
		run := createTestRun(t, spawnNodeGraph())
		fakeLiveSpawns(t)
		f := fakeDeadWorkers(t)
		step(t, runTestSession, run.ID)
		worker := nodeTaskID(t, run.ID)
		f.banner[worker] = true

		step(t, runTestSession, run.ID)
		ageDeadSighting(worker)
		step(t, runTestSession, run.ID)
		if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeRunning || len(f.relaunchd) != 0 {
			t.Fatalf("live worker touched: state %q relaunches %v", s, f.relaunchd)
		}
	})
	t.Run("answered worker", func(t *testing.T) {
		run := createTestRun(t, spawnNodeGraph())
		fakeLiveSpawns(t)
		f := fakeDeadWorkers(t)
		step(t, runTestSession, run.ID)
		worker := nodeTaskID(t, run.ID)
		answerSpawn(t, runTestSession, worker)
		f.dead[worker], f.banner[worker] = true, true

		dead, _ := deadSpawnWorkers(runTestSession, worker)
		if len(dead) != 0 {
			t.Fatalf("an answered worker read as dead: %v", dead)
		}
	})
}

func nodeTaskID(t *testing.T, runID string) string {
	t.Helper()
	st, err := ReadNodeStatus(runTestSession, runID, "w")
	if err != nil || st.TaskID == "" {
		t.Fatalf("node w has no worker: %v", err)
	}
	return st.TaskID
}
