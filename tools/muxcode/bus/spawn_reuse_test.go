package bus

import (
	"os"
	"slices"
	"testing"
)

// MUX-195 Phase 1 pins. Each *Today test asserts a duplicate-worker road as it
// stands, so the phase that closes the road inverts its pin rather than adding
// a test beside it. TestBusyWorkerNeverReused is the negative control and
// stays green through every phase.

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
	entries, err := ReadSpawnEntries(runTestSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.SpawnRole == spawnRole {
			return e
		}
	}
	t.Fatalf("no spawn entry for %s", spawnRole)
	return SpawnEntry{}
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
// worker is free — answered, window live, its run complete. Phase 3 inverts
// it: the second run adopts, count 1.
func TestSequentialRunsSpawnWorkerPerRunToday(t *testing.T) {
	f, first := finishedRunWorker(t)
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

// TestTerminalRunWorkerStrandedWithoutDeliveryRecordToday pins road 4: a
// finished run's worker is reaped only while its seed's delivery record
// survives. With the record gone it stays running, window open, although its
// reply is still on record in the session log. Phase 2 inverts it: both cases
// reach the same end state.
func TestTerminalRunWorkerStrandedWithoutDeliveryRecordToday(t *testing.T) {
	cases := []struct {
		name       string
		dropRecord bool
		wantStatus string
		wantKilled bool
	}{
		{"record present is reaped", false, "completed", true},
		{"record missing is stranded", true, "running", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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

			if _, err := RefreshSpawnStatus(runTestSession); err != nil {
				t.Fatal(err)
			}

			e := spawnByRole(t, worker)
			if killed := slices.Contains(f.killed, worker); e.Status != tc.wantStatus || killed != tc.wantKilled {
				t.Fatalf("status %q killed %v, want %q killed %v", e.Status, killed, tc.wantStatus, tc.wantKilled)
			}
		})
	}
}

// TestBusyWorkerNeverReused is the negative control for every pin above: a
// worker whose current seed is unanswered is never adopted by another run,
// never reseeded over by a second `spawn start`, and never reaped — with or
// without its delivery record. Its owning run is absent, the most adoptable a
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

			if e := spawnByRole(t, busy); e.Status != "running" || slices.Contains(f.killed, busy) {
				t.Fatalf("busy worker reaped: status %q, killed %v", e.Status, f.killed)
			}
		})
	}
}
