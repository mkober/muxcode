package bus

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// MUX-195. Each *Today test pins a duplicate-worker road as it stands, so the
// phase that closes the road inverts its pin rather than adding a test beside
// it. TestBusyWorkerNeverReused is the negative control and stays green
// through every phase.

func refreshSpawns(t *testing.T) {
	t.Helper()
	if _, err := RefreshSpawnStatus(runTestSession); err != nil {
		t.Fatal(err)
	}
}

// ageIdle moves a worker's idle stamp secs into the past.
func ageIdle(t *testing.T, spawnRole string, secs int64) {
	t.Helper()
	if err := UpdateSpawnEntry(runTestSession, spawnByRole(t, spawnRole).ID, func(e *SpawnEntry) { e.IdleSince -= secs }); err != nil {
		t.Fatal(err)
	}
}

// isolateLifecycle gives a test its own lifecycle log: the package shares one
// and fake worker names repeat across tests, so another test's row could
// satisfy an assertion.
func isolateLifecycle(t *testing.T) {
	t.Helper()
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
}

func lifecycleDetails(t *testing.T, event string) []string {
	t.Helper()
	rows, err := ReadLifecycleLog(runTestSession)
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for _, r := range rows {
		if r.Event == event {
			details = append(details, r.Detail)
		}
	}
	return details
}

func oneSpawnGraph() *Graph {
	return &Graph{Name: "one-spawn", Start: "implement",
		Nodes: []Node{{ID: "implement", Type: NodeSpawn, Role: "edit", Message: "implement the phase"}}}
}

func nodeTask(t *testing.T, runID, nodeID string) string {
	t.Helper()
	st, err := ReadNodeStatus(runTestSession, runID, nodeID)
	if err != nil || st.TaskID == "" {
		t.Fatalf("node %s has no worker: (%+v, %v)", nodeID, st, err)
	}
	return st.TaskID
}

// spawnByRole looks a worker up by its bus role — a real StartSpawn entry's ID
// differs from its SpawnRole, so GetSpawnEntry cannot.
func spawnByRole(t *testing.T, spawnRole string) SpawnEntry {
	t.Helper()
	e, ok := findSpawnByRole(runTestSession, spawnRole)
	if !ok {
		t.Fatalf("no spawn entry for %s", spawnRole)
	}
	return e
}

func runningWorkers(t *testing.T) int {
	t.Helper()
	entries, err := ReadSpawnEntries(runTestSession)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.Status == "running" {
			n++
		}
	}
	return n
}

// requireIdleWorker fails unless spawnRole is a worker the fix must reuse:
// running, current seed answered, window neither dead nor killed (f nil when
// no window fake is in play). A pin passing on a dead worker would still pass
// after the fix, since a dead worker justifies a fresh one.
func requireIdleWorker(t *testing.T, f *liveSpawnFake, spawnRole string) {
	t.Helper()
	e := spawnByRole(t, spawnRole)
	answered := spawnHasResponded(runTestSession, e)
	gone := f != nil && (f.deadWindows[spawnRole] || slices.Contains(f.killed, spawnRole))
	if e.Status != "running" || !answered || gone {
		t.Fatalf("precondition: %s must be idle — status %q, answered %v, window gone %v", spawnRole, e.Status, answered, gone)
	}
}

// finishedRunWorker drives a one-spawn-node run to complete and returns its
// worker: answered, window live, owning run terminal — the state every
// stranded worker of 2026-09-28 and 2026-10-05 was found in.
func finishedRunWorker(t *testing.T) (*liveSpawnFake, string) {
	t.Helper()
	run := createTestRun(t, oneSpawnGraph())
	f := fakeLiveSpawns(t)
	step(t, runTestSession, run.ID)
	worker := nodeTask(t, run.ID, "implement")
	answerSpawn(t, runTestSession, worker)
	step(t, runTestSession, run.ID)
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunComplete {
		t.Fatalf("run state %q, want complete", r.State)
	}
	return f, worker
}

// stubSpawnLaunch replaces StartSpawnOwned's tmux boundary and records each
// worker it would have launched.
func stubSpawnLaunch(t *testing.T) *[]string {
	t.Helper()
	if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
		t.Fatalf("delivery dir: %v", err)
	}
	var launched []string
	orig := spawnLaunchFn
	t.Cleanup(func() { spawnLaunchFn = orig })
	spawnLaunchFn = func(_ string, e SpawnEntry) error {
		launched = append(launched, e.SpawnRole)
		return nil
	}
	return &launched
}

// TestSpecToPRRunSpawnsWorkerPerNodeToday pins road 1 (per-node reuse key): a
// run shaped like 50-spec-to-pr — implement, build, fix on a build failure —
// launches a second worker for fix while implement's worker sits idle in the
// same run. Phase 3 inverts it: one worker, count 1.
func TestSpecToPRRunSpawnsWorkerPerNodeToday(t *testing.T) {
	g := &Graph{Name: "spec-to-pr-shape", Start: "implement",
		Nodes: []Node{
			{ID: "implement", Type: NodeSpawn, Role: "edit", Message: "implement the phase"},
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "build it"},
			{ID: "fix", Type: NodeSpawn, Role: "edit", Message: "fix the build"},
		},
		Edges: []Edge{
			{From: "implement", To: "build"},
			{From: "build", To: "fix", Outcome: OutcomeFailure},
			{From: "fix", To: "build", MaxIterations: 3},
		}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)

	step(t, runTestSession, run.ID)
	implementWorker := nodeTask(t, run.ID, "implement")
	answerSpawn(t, runTestSession, implementWorker)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "build", OutcomeFailure)
	requireIdleWorker(t, f, implementWorker)
	step(t, runTestSession, run.ID)

	fixWorker := nodeTask(t, run.ID, "fix")
	if fixWorker == implementWorker || f.fresh != 2 {
		t.Fatalf("pin: fix gets its own worker today — implement %s, fix %s, %d launches", implementWorker, fixWorker, f.fresh)
	}
	if n := spawnCountForRun(t, runTestSession, run.ID); n != 2 {
		t.Fatalf("pin: run holds 2 workers today, got %d", n)
	}
}

// TestSequentialRunsSpawnWorkerPerRunToday pins road 1 across runs: a second
// run of the same template launches a fresh worker while the first run's
// worker sits in the idle pool — answered, window live, its run complete.
// Phase 3 inverts it: the second run adopts, count 1.
func TestSequentialRunsSpawnWorkerPerRunToday(t *testing.T) {
	t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
	f, first := finishedRunWorker(t)
	refreshSpawns(t)
	requireIdleWorker(t, f, first)

	g := oneSpawnGraph()
	run2, err := CreateGraphRun(runTestSession, g, g.Name, "second run")
	if err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run2.ID)

	second := nodeTask(t, run2.ID, "implement")
	if second == first || f.fresh != 2 {
		t.Fatalf("pin: the second run launches its own worker today — first %s, second %s, %d launches", first, second, f.fresh)
	}
	if n := runningWorkers(t); n != 2 {
		t.Fatalf("pin: 2 live workers today, got %d", n)
	}
}

// TestSpawnStartFromOneOwnerSpawnsWorkerPerCallToday pins road 2 (the agent
// road has no lookup): a second `spawn start` from an owner whose first worker
// is idle launches another. Phase 4 inverts it: the idle worker is reseeded.
func TestSpawnStartFromOneOwnerSpawnsWorkerPerCallToday(t *testing.T) {
	useTempBusDir(t)
	launched := stubSpawnLaunch(t)

	first, err := StartSpawn(runTestSession, "edit", "task one", "edit", false)
	if err != nil {
		t.Fatal(err)
	}
	answerSpawn(t, runTestSession, first.SpawnRole)
	requireIdleWorker(t, nil, first.SpawnRole)

	second, err := StartSpawn(runTestSession, "edit", "task two", "edit", false)
	if err != nil {
		t.Fatal(err)
	}
	if second.SpawnRole == first.SpawnRole || len(*launched) != 2 {
		t.Fatalf("pin: each start launches a worker today — first %s, second %s, launches %v", first.SpawnRole, second.SpawnRole, *launched)
	}
	if n := runningWorkers(t); n != 2 {
		t.Fatalf("pin: 2 live workers today, got %d", n)
	}
}

// TestTerminalRunWorkerFreedWithoutDeliveryRecord inverts the Phase 1 pin on
// road 4, where a missing seed record stranded a finished run's worker
// running for hours. Present or missing, the worker is now freed alike — its
// reply in the session log is the evidence: held idle, not re-stamped by a
// second pass, then reaped when the quiet window closes.
func TestTerminalRunWorkerFreedWithoutDeliveryRecord(t *testing.T) {
	cases := []struct {
		name       string
		dropRecord bool
	}{
		{"delivery record present", false},
		{"delivery record missing", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
			isolateLifecycle(t)
			f, worker := finishedRunWorker(t)
			seed := spawnByRole(t, worker).SeedMsgID
			if reply, ok := GetSpawnResult(runTestSession, worker); !ok || reply.ReplyTo != seed {
				t.Fatalf("fixture: the worker's reply to %s must be in the session log, got (%+v, %v)", seed, reply, ok)
			}
			if tc.dropRecord {
				if err := os.Remove(DeliveryPath(runTestSession, seed)); err != nil {
					t.Fatal(err)
				}
			}

			refreshSpawns(t)
			held := spawnByRole(t, worker)
			display := SpawnDisplayStatus(runTestSession, held)
			if held.Status != "running" || held.IdleSince == 0 || display != "idle" || slices.Contains(f.killed, worker) {
				t.Fatalf("released worker must be held idle: status %q, idle since %d, display %q, killed %v",
					held.Status, held.IdleSince, display, f.killed)
			}

			refreshSpawns(t)
			if e := spawnByRole(t, worker); e.Status != "running" || e.IdleSince != held.IdleSince {
				t.Fatalf("a second pass inside the window must leave the hold alone: status %q, idle since %d (was %d)",
					e.Status, e.IdleSince, held.IdleSince)
			}
			if rows := lifecycleDetails(t, "spawn-idle"); len(rows) != 1 || !strings.Contains(rows[0], worker) {
				t.Fatalf("want one spawn-idle row naming %s, got %v", worker, rows)
			}

			ageIdle(t, worker, 600)
			refreshSpawns(t)
			if e := spawnByRole(t, worker); e.Status != "completed" || !slices.Contains(f.killed, worker) {
				t.Fatalf("closed quiet window must reap: status %q, killed %v", e.Status, f.killed)
			}
			rows := lifecycleDetails(t, "spawn-reaped")
			if len(rows) != 1 || !strings.Contains(rows[0], worker) || !strings.Contains(rows[0], "quiet window") ||
				!strings.Contains(rows[0], "new owner none") {
				t.Fatalf("want one spawn-reaped row naming %s, its closed window and no new owner, got %v", worker, rows)
			}
		})
	}
}

// TestSpawnHasRespondedFallsBackToWorkerReply: with the seed's delivery record
// gone, the worker's own reply in the session log proves an answer. The other
// rows are the negative controls — anything short of that reply read as an
// answer frees a busy worker for adoption mid-task.
func TestSpawnHasRespondedFallsBackToWorkerReply(t *testing.T) {
	reply := func(t *testing.T, from string, e SpawnEntry) {
		t.Helper()
		if err := Send(runTestSession, NewMessage(from, "daemon", "response", "spawn-task", "done. EXIT=0", e.SeedMsgID)); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, e SpawnEntry) SpawnEntry
		want  bool
	}{
		{"worker replied to the seed", func(t *testing.T, e SpawnEntry) SpawnEntry {
			reply(t, e.SpawnRole, e)
			return e
		}, true},
		{"no reply", func(t *testing.T, e SpawnEntry) SpawnEntry { return e }, false},
		{"reply from another role", func(t *testing.T, e SpawnEntry) SpawnEntry {
			reply(t, "research", e)
			return e
		}, false},
		{"worker answered the previous seed, not the current one", func(t *testing.T, e SpawnEntry) SpawnEntry {
			answerSpawn(t, runTestSession, e.SpawnRole)
			if _, err := ReseedSpawn(runTestSession, e, "phase 2"); err != nil {
				t.Fatal(err)
			}
			return spawnByRole(t, e.SpawnRole)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempBusDir(t)
			fakeLiveSpawns(t)
			worker, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 1")
			if err != nil {
				t.Fatal(err)
			}
			e := tc.setup(t, spawnByRole(t, worker))
			if err := os.Remove(DeliveryPath(runTestSession, e.SeedMsgID)); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}

			if got := spawnHasResponded(runTestSession, e); got != tc.want {
				t.Errorf("spawnHasResponded = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIdlePoolHoldsOneWorkerPerBaseRole: of the released workers of one base
// role only the most recently released is held; the rest are reaped as
// superseded, naming the keeper. Another base role keeps its own — the pool is
// per role, not per session.
func TestIdlePoolHoldsOneWorkerPerBaseRole(t *testing.T) {
	t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
	useTempBusDir(t)
	isolateLifecycle(t)
	f := fakeLiveSpawns(t)
	release := func(runID, role string) string {
		t.Helper()
		w, err := acquireSpawnWorker(runTestSession, runID, "implement", role, "work")
		if err != nil {
			t.Fatal(err)
		}
		answerSpawn(t, runTestSession, w)
		return w
	}

	older := release("run-a", "edit")
	refreshSpawns(t)
	ageIdle(t, older, 60)
	newer := release("run-b", "edit")
	research := release("run-c", "research")
	refreshSpawns(t)

	for _, w := range []string{newer, research} {
		if e := spawnByRole(t, w); e.Status != "running" || e.IdleSince == 0 || slices.Contains(f.killed, w) {
			t.Errorf("%s must be held as its role's idle worker: status %q, idle since %d, killed %v", w, e.Status, e.IdleSince, f.killed)
		}
	}
	if e := spawnByRole(t, older); e.Status != "completed" || !slices.Contains(f.killed, older) {
		t.Fatalf("the older edit worker must be reaped: status %q, killed %v", e.Status, f.killed)
	}
	rows := lifecycleDetails(t, "spawn-reaped")
	if len(rows) != 1 || !strings.Contains(rows[0], older) || !strings.Contains(rows[0], "superseded by "+newer) ||
		!strings.Contains(rows[0], "last owner run run-a") {
		t.Fatalf("want one spawn-reaped row for %s superseded by %s, got %v", older, newer, rows)
	}
}

// TestIdleHoldNeverHoldsAWorkerOwedAStop: a worker the executor is stopping —
// one refused for running without its definition — is reaped on release,
// never held for another run to adopt.
func TestIdleHoldNeverHoldsAWorkerOwedAStop(t *testing.T) {
	t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
	useTempBusDir(t)
	f := fakeLiveSpawns(t)
	w, err := acquireSpawnWorker(runTestSession, "run-a", "implement", "edit", "work")
	if err != nil {
		t.Fatal(err)
	}
	answerSpawn(t, runTestSession, w)
	if err := UpdateSpawnEntry(runTestSession, w, func(e *SpawnEntry) { e.StopPending = "ran without its agent definition" }); err != nil {
		t.Fatal(err)
	}

	refreshSpawns(t)

	if e := spawnByRole(t, w); e.Status != "completed" || e.IdleSince != 0 || !slices.Contains(f.killed, w) {
		t.Fatalf("a worker owed a stop must be reaped, not held: status %q, idle since %d, killed %v", e.Status, e.IdleSince, f.killed)
	}
}

// TestIdleWorkerOwnedAgainLosesIdleStamp: a held worker whose run resumes — a
// `graph retry` of a failed run — is parked again and its idle stamp clears,
// so a later release opens a fresh quiet window instead of reaping at once.
func TestIdleWorkerOwnedAgainLosesIdleStamp(t *testing.T) {
	t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
	_, worker := finishedRunWorker(t)
	refreshSpawns(t)
	if spawnByRole(t, worker).IdleSince == 0 {
		t.Fatal("fixture: the released worker must be held idle first")
	}

	run, err := ReadGraphRun(runTestSession, spawnByRole(t, worker).RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.State = GraphRunRunning
	if err := WriteGraphRun(runTestSession, run); err != nil {
		t.Fatal(err)
	}
	refreshSpawns(t)

	e := spawnByRole(t, worker)
	if display := SpawnDisplayStatus(runTestSession, e); e.IdleSince != 0 || display != "parked" {
		t.Fatalf("an owned-again worker must be parked with no idle stamp: idle since %d, display %q", e.IdleSince, display)
	}
}

// TestBusyWorkerNeverReused is the negative control for every pin above: a
// worker whose current seed is unanswered is never adopted by another run,
// never reseeded over by a second `spawn start`, and never reaped or marked
// idle — with or without its delivery record. Its owning run is absent, the most adoptable a
// worker can be short of answering, so busy-ness is the only thing protecting
// it. Must stay green through Phases 2–4.
func TestBusyWorkerNeverReused(t *testing.T) {
	t.Run("another run does not adopt it", func(t *testing.T) {
		useTempBusDir(t)
		fakeLiveSpawns(t)
		busy, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 1")
		if err != nil {
			t.Fatal(err)
		}
		before := spawnByRole(t, busy)

		if other, err := acquireSpawnWorker(runTestSession, "run-2", "implement", "edit", "phase 1 of run 2"); err == nil && other == busy {
			t.Fatalf("run-2 was handed run-1's busy worker %s", busy)
		}

		after := spawnByRole(t, busy)
		if after.RunID != before.RunID || after.NodeID != before.NodeID || after.SeedMsgID != before.SeedMsgID {
			t.Fatalf("busy worker re-pointed: before %+v, after %+v", before, after)
		}
		msgs, _ := Peek(runTestSession, busy)
		if len(msgs) != 1 || msgs[0].ID != before.SeedMsgID {
			t.Fatalf("busy worker's inbox must hold only its own seed %s: %+v", before.SeedMsgID, msgs)
		}
	})

	t.Run("a second spawn start does not reseed over it", func(t *testing.T) {
		useTempBusDir(t)
		stubSpawnLaunch(t)
		first, err := StartSpawn(runTestSession, "edit", "task one", "edit", false)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := StartSpawn(runTestSession, "edit", "task two", "edit", false); err != nil {
			t.Fatal(err)
		}

		after := spawnByRole(t, first.SpawnRole)
		if after.SeedMsgID != first.SeedMsgID || after.Task != first.Task || after.Owner != first.Owner {
			t.Fatalf("busy worker reseeded over: before %+v, after %+v", first, after)
		}
	})

	for _, dropRecord := range []bool{false, true} {
		name := "the reaper leaves it running, delivery record present"
		if dropRecord {
			name = "the reaper leaves it running, delivery record missing"
		}
		t.Run(name, func(t *testing.T) {
			useTempBusDir(t)
			f := fakeLiveSpawns(t)
			busy, err := acquireSpawnWorker(runTestSession, "run-gone", "implement", "edit", "phase 1")
			if err != nil {
				t.Fatal(err)
			}
			if dropRecord {
				if err := os.Remove(DeliveryPath(runTestSession, spawnByRole(t, busy).SeedMsgID)); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := RefreshSpawnStatus(runTestSession); err != nil {
				t.Fatal(err)
			}

			e := spawnByRole(t, busy)
			if display := SpawnDisplayStatus(runTestSession, e); e.Status != "running" || e.IdleSince != 0 || display == "idle" || slices.Contains(f.killed, busy) {
				t.Fatalf("busy worker reaped or freed: status %q, idle since %d, display %q, killed %v", e.Status, e.IdleSince, display, f.killed)
			}
		})
	}
}
