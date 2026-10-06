package bus

import (
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
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

// stubSpawnClear records each worker an adoption clears instead of typing
// /clear into a pane; the clear returns fail.
func stubSpawnClear(t *testing.T, fail error) *[]string {
	t.Helper()
	var cleared []string
	orig := spawnClearFn
	t.Cleanup(func() { spawnClearFn = orig })
	spawnClearFn = func(_, spawnRole string) error {
		cleared = append(cleared, spawnRole)
		return fail
	}
	return &cleared
}

// liveRunWorkers counts a run's workers that are running with a live window.
func liveRunWorkers(t *testing.T, f *liveSpawnFake, runID string) int {
	t.Helper()
	entries, err := ReadSpawnEntries(runTestSession)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.RunID == runID && e.Status == "running" && !f.deadWindows[e.Window] {
			n++
		}
	}
	return n
}

// TestSpecToPRRunSharesOneWorker inverts the Phase 1 pin on road 1: a run
// shaped like 50-spec-to-pr — implement, build, fix on a build failure — runs
// fix on implement's worker. One launch, one worker, its node moved to fix and
// its new seed naming it.
func TestSpecToPRRunSharesOneWorker(t *testing.T) {
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
	if fixWorker != implementWorker || f.fresh != 1 {
		t.Fatalf("fix must run on implement's worker — implement %s, fix %s, %d launches", implementWorker, fixWorker, f.fresh)
	}
	if n := spawnCountForRun(t, runTestSession, run.ID); n != 1 {
		t.Fatalf("the run must hold 1 worker, got %d", n)
	}
	e := spawnByRole(t, fixWorker)
	msgs, _ := Peek(runTestSession, fixWorker)
	if e.NodeID != "fix" || len(msgs) != 1 || msgs[0].ID != e.SeedMsgID || !strings.Contains(msgs[0].Payload, "node fix") {
		t.Fatalf("the worker must move to fix with a seed naming it: node %q, inbox %+v", e.NodeID, msgs)
	}
}

// TestSequentialRunsAdoptTheIdleWorker inverts the Phase 1 pin across runs:
// a second run of the same template adopts the first run's idle worker
// instead of launching one. Ownership moves to the new run, the seed opens
// with the hand-over naming both owners, the context is kept because the
// spec is unchanged (Decision 1), and the adoption row names old and new
// owner.
func TestSequentialRunsAdoptTheIdleWorker(t *testing.T) {
	t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
	isolateLifecycle(t)
	cleared := stubSpawnClear(t, nil)
	f, first := finishedRunWorker(t)
	firstRun := spawnByRole(t, first).RunID
	refreshSpawns(t)
	requireIdleWorker(t, f, first)

	g := oneSpawnGraph()
	run2, err := CreateGraphRun(runTestSession, g, g.Name, "second run")
	if err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run2.ID)

	if second := nodeTask(t, run2.ID, "implement"); second != first || f.fresh != 1 || runningWorkers(t) != 1 {
		t.Fatalf("the second run must adopt %s: got %s, %d launches, %d live", first, second, f.fresh, runningWorkers(t))
	}
	e := spawnByRole(t, first)
	if e.RunID != run2.ID || e.NodeID != "implement" || e.IdleSince != 0 {
		t.Fatalf("ownership must move to the new run: run %q node %q idle since %d", e.RunID, e.NodeID, e.IdleSince)
	}
	msgs, _ := Peek(runTestSession, first)
	if len(msgs) != 1 || msgs[0].ID != e.SeedMsgID || !strings.Contains(msgs[0].Payload, "you now serve graph run "+run2.ID) ||
		!strings.Contains(msgs[0].Payload, firstRun) {
		t.Fatalf("the seed must hand over from %s to %s: %+v", firstRun, run2.ID, msgs)
	}
	if len(*cleared) != 0 {
		t.Fatalf("an unchanged spec keeps the context, but %v was cleared", *cleared)
	}
	rows := lifecycleDetails(t, "graph-spawn-adopted")
	if len(rows) != 1 || !strings.Contains(rows[0], "old owner run "+firstRun) ||
		!strings.Contains(rows[0], "new owner run "+run2.ID) || !strings.Contains(rows[0], "context kept") {
		t.Fatalf("want one graph-spawn-adopted row naming both owners, got %v", rows)
	}
}

// TestAdoptionClearsContextWhenTheSpecChanged: Decision 1 — an idle worker
// whose last seed was under another spec is /clear-ed before its new seed. A
// clear that fails forgoes the adoption for a fresh worker rather than carry
// the old spec's context into new work.
func TestAdoptionClearsContextWhenTheSpecChanged(t *testing.T) {
	cases := []struct {
		name      string
		clearErr  error
		wantAdopt bool
	}{
		{"cleared, then adopted", nil, true},
		{"clear failed, fresh worker", errors.New("pane gone"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
			isolateLifecycle(t)
			cleared := stubSpawnClear(t, tc.clearErr)
			f, idle := finishedRunWorker(t)
			refreshSpawns(t)
			if err := UpdateSpawnEntry(runTestSession, idle, func(e *SpawnEntry) { e.Spec = "docs/requirements/drafts/OLD.md" }); err != nil {
				t.Fatal(err)
			}

			got, err := acquireSpawnWorker(runTestSession, "run-new", "implement", "edit", "next task")
			if err != nil {
				t.Fatal(err)
			}

			if len(*cleared) != 1 || (*cleared)[0] != idle {
				t.Fatalf("the idle worker must be cleared first, cleared %v", *cleared)
			}
			if (got == idle) != tc.wantAdopt {
				t.Fatalf("adopted = %v, want %v (got %s, %d launches)", got == idle, tc.wantAdopt, got, f.fresh)
			}
			if !tc.wantAdopt {
				if e := spawnByRole(t, idle); e.RunID == "run-new" || f.fresh != 2 {
					t.Fatalf("a failed clear must not hand the worker over: run %q, %d launches", e.RunID, f.fresh)
				}
				return
			}
			if rows := lifecycleDetails(t, "graph-spawn-adopted"); len(rows) != 1 || !strings.Contains(rows[0], "cleared: active spec changed") {
				t.Fatalf("the adoption row must say the context was cleared, got %v", rows)
			}
		})
	}
}

// TestConcurrentDispatchCannotShareAnIdleWorker: two runs dispatching at once
// against one idle worker — the registry lock lets exactly one adopt it and
// the other launches its own. Never a shared worker.
func TestConcurrentDispatchCannotShareAnIdleWorker(t *testing.T) {
	t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
	stubSpawnClear(t, nil)
	f, idle := finishedRunWorker(t)
	refreshSpawns(t)

	runs := []string{"run-a", "run-b"}
	got := make([]string, len(runs))
	errs := make([]error, len(runs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, runID := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got[i], errs[i] = acquireSpawnWorker(runTestSession, runID, "implement", "edit", "task for "+runID)
		}()
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winner := ""
	for i, w := range got {
		if w == idle {
			if winner != "" {
				t.Fatalf("both runs were handed %s", idle)
			}
			winner = runs[i]
		}
	}
	if winner == "" || got[0] == got[1] || f.fresh != 2 {
		t.Fatalf("want one adoption and one launch: got %v, %d launches", got, f.fresh)
	}
	if e := spawnByRole(t, idle); e.RunID != winner {
		t.Fatalf("the adopted worker belongs to %q, want the run that took it, %q", e.RunID, winner)
	}
}

// TestAdoptionDropsThePreviousOwnersStaleSeed: a spawn-task the previous
// owner left queued is dropped on adoption, so only the new owner's work
// reaches the worker.
func TestAdoptionDropsThePreviousOwnersStaleSeed(t *testing.T) {
	t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
	stubSpawnClear(t, nil)
	_, idle := finishedRunWorker(t)
	refreshSpawns(t)
	if err := Send(runTestSession, NewMessage(graphSender, idle, "request", "spawn-task", "stale work of the finished run", "")); err != nil {
		t.Fatal(err)
	}

	got, err := acquireSpawnWorker(runTestSession, "run-new", "implement", "edit", "new work")
	if err != nil || got != idle {
		t.Fatalf("fixture: the idle worker must be adopted, got %s (%v)", got, err)
	}

	msgs, _ := Peek(runTestSession, idle)
	if len(msgs) != 1 || strings.Contains(msgs[0].Payload, "stale work") || !strings.Contains(msgs[0].Payload, "new work") {
		t.Fatalf("only the new owner's seed may be pending: %+v", msgs)
	}
}

// TestLostWorkerReplacedOnlyOnceItsWindowIsGone: a lost worker is replaced
// only after its window is confirmed gone, so the run never holds two live
// workers. One stopped with its window still live is killed and waited for,
// spending no replacement; once the window is gone it is replaced — the
// regression control that replacement still happens.
func TestLostWorkerReplacedOnlyOnceItsWindowIsGone(t *testing.T) {
	isolateLifecycle(t)
	run := createTestRun(t, oneSpawnGraph())
	f := fakeLiveSpawns(t)
	step(t, runTestSession, run.ID)
	lost := nodeTask(t, run.ID, "implement")
	if err := UpdateSpawnEntry(runTestSession, lost, func(e *SpawnEntry) { e.Status = "stopped" }); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "implement")
	if f.fresh != 1 || st.TaskID != lost || st.Redrives != 0 || !slices.Contains(f.killed, lost) {
		t.Fatalf("a live lost window must be killed and waited for: %d launches, task %q, redrives %d, killed %v",
			f.fresh, st.TaskID, st.Redrives, f.killed)
	}
	if rows := lifecycleDetails(t, "graph-spawn-replace-held"); len(rows) == 0 || !strings.Contains(rows[0], lost) {
		t.Fatalf("the hold must be logged naming %s, got %v", lost, rows)
	}

	f.deadWindows[lost] = true
	step(t, runTestSession, run.ID)
	st, _ = ReadNodeStatus(runTestSession, run.ID, "implement")
	if f.fresh != 2 || st.TaskID == lost || st.Redrives != 1 {
		t.Fatalf("a gone window's worker must be replaced: %d launches, task %q, redrives %d", f.fresh, st.TaskID, st.Redrives)
	}
	if n := liveRunWorkers(t, f, run.ID); n != 1 {
		t.Fatalf("the run must hold exactly 1 live worker, got %d", n)
	}
}

// TestMapRunsItemsSeriallyOnTheRunsWorker pins Decision 3's default: a map
// node's items run one at a time on one worker — the next is seeded only once
// the last is answered — and the node's output carries every item's report in
// item order.
func TestMapRunsItemsSeriallyOnTheRunsWorker(t *testing.T) {
	g := &Graph{Name: "serial-map", Start: "m",
		Nodes: []Node{{ID: "m", Type: NodeMap, Role: "edit", Items: "one,two,three", Message: "process ${item}"}}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)

	step(t, runTestSession, run.ID)
	worker := nodeTask(t, run.ID, "m")
	step(t, runTestSession, run.ID)
	for _, item := range []string{"one", "two", "three"} {
		if e := spawnByRole(t, worker); !strings.Contains(e.Task, "process "+item) {
			t.Fatalf("item %q must be the one seeded, worker holds %q", item, e.Task)
		}
		answerSpawnWith(t, runTestSession, worker, "did "+item+". EXIT=0")
		step(t, runTestSession, run.ID)
		if got := nodeTask(t, run.ID, "m"); got != worker {
			t.Fatalf("item after %q moved to worker %s, want %s", item, got, worker)
		}
	}

	st, _ := ReadNodeStatus(runTestSession, run.ID, "m")
	if st.State != GraphNodeDone || st.Outcome != OutcomeSuccess || f.fresh != 1 {
		t.Fatalf("map must finish success on one worker: state %q outcome %q, %d launches", st.State, st.Outcome, f.fresh)
	}
	one, two, three := strings.Index(st.Output, "did one"), strings.Index(st.Output, "did two"), strings.Index(st.Output, "did three")
	if one < 0 || two < one || three < two {
		t.Fatalf("every item's report must be in the output, in order: %q", st.Output)
	}
}

// TestMapWorkersLanesTakeTheNextItem: with workers: N the items share N
// lanes, and a lane whose worker answers takes the next unassigned item on
// that same worker while the other lane is still busy.
func TestMapWorkersLanesTakeTheNextItem(t *testing.T) {
	g := &Graph{Name: "lanes", Start: "m",
		Nodes: []Node{{ID: "m", Type: NodeMap, Role: "edit", Items: "one,two,three", Workers: 2, Message: "process ${item}"}}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)

	step(t, runTestSession, run.ID)
	lanes := strings.Split(nodeTask(t, run.ID, "m"), ",")
	if len(lanes) != 2 || lanes[0] == lanes[1] || f.fresh != 2 {
		t.Fatalf("two lanes on two workers expected: %v, %d launches", lanes, f.fresh)
	}

	answerSpawnWith(t, runTestSession, lanes[1], "did two. EXIT=0")
	step(t, runTestSession, run.ID)
	if e := spawnByRole(t, lanes[1]); !strings.Contains(e.Task, "process three") || f.fresh != 2 {
		t.Fatalf("the free lane must take item three on its own worker: task %q, %d launches", e.Task, f.fresh)
	}
	if e := spawnByRole(t, lanes[0]); !strings.Contains(e.Task, "process one") {
		t.Fatalf("the busy lane must keep item one, holds %q", e.Task)
	}

	answerSpawnWith(t, runTestSession, lanes[0], "did one. EXIT=0")
	answerSpawnWith(t, runTestSession, lanes[1], "did three. EXIT=0")
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "m")
	one, two, three := strings.Index(st.Output, "did one"), strings.Index(st.Output, "did two"), strings.Index(st.Output, "did three")
	if st.State != GraphNodeDone || one < 0 || two < one || three < two {
		t.Fatalf("map must finish with reports in item order: state %q output %q", st.State, st.Output)
	}
}

// TestForkedSpawnNodesKeepTheirOwnResults is the negative control for the
// run-keyed reuse (review must-fix): two spawn nodes armed in one tick must
// not share the run's worker at once — reseeded over, both would harvest the
// second seed's answer. One takes the worker; the other waits ready, deferred
// once however many ticks pass, and runs on the same worker only after the
// first has harvested its own answer.
func TestForkedSpawnNodesKeepTheirOwnResults(t *testing.T) {
	isolateLifecycle(t)
	g := &Graph{Name: "fork", Start: "kick",
		Nodes: []Node{
			{ID: "kick", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "left", Type: NodeSpawn, Role: "edit", Message: "do the left half"},
			{ID: "right", Type: NodeSpawn, Role: "edit", Message: "do the right half"},
		},
		Edges: []Edge{{From: "kick", To: "left"}, {From: "kick", To: "right"}}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "kick", OutcomeSuccess)
	step(t, runTestSession, run.ID)

	first, second := "left", "right"
	if nodeState(t, runTestSession, run.ID, first) != GraphNodeRunning {
		first, second = second, first
	}
	worker := nodeTask(t, run.ID, first)
	if s := nodeState(t, runTestSession, run.ID, second); s != GraphNodeReady || f.fresh != 1 {
		t.Fatalf("%s must wait ready while %s holds the worker: state %q, %d launches", second, first, s, f.fresh)
	}
	msgs, _ := Peek(runTestSession, worker)
	if e := spawnByRole(t, worker); e.NodeID != first || len(msgs) != 1 || msgs[0].ID != e.SeedMsgID {
		t.Fatalf("the worker must hold only %s's seed: node %q, inbox %+v", first, e.NodeID, msgs)
	}
	step(t, runTestSession, run.ID)
	if rows := lifecycleDetails(t, "graph-spawn-deferred"); len(rows) != 1 || !strings.Contains(rows[0], second+" waits") ||
		!strings.Contains(rows[0], "node "+first+" holds "+worker) {
		t.Fatalf("want one graph-spawn-deferred row naming %s waiting on %s, got %v", second, first, rows)
	}

	answerSpawnWith(t, runTestSession, worker, "did "+first+". EXIT=0")
	step(t, runTestSession, run.ID)
	if got := nodeTask(t, run.ID, second); got != worker || f.fresh != 1 {
		t.Fatalf("%s must run on the freed worker: got %s, %d launches", second, got, f.fresh)
	}
	answerSpawnWith(t, runTestSession, worker, "did "+second+". EXIT=0")
	step(t, runTestSession, run.ID)

	for node, other := range map[string]string{first: second, second: first} {
		st, _ := ReadNodeStatus(runTestSession, run.ID, node)
		if st.State != GraphNodeDone || st.Outcome != OutcomeSuccess || !strings.Contains(st.Output, "did "+node) ||
			strings.Contains(st.Output, "did "+other) {
			t.Errorf("%s must record its own result: state %q outcome %q output %q", node, st.State, st.Outcome, st.Output)
		}
	}
}

// TestLosingAdopterNeverClearsTheWinner (review must-fix): two runs found the
// same idle worker under a changed spec. The second acts on its stale find
// while the first is mid-clear — the tightest interleaving — and must fail at
// the claim having touched nothing. Clearing before claiming, it typed /clear
// into a worker the winner had already claimed, and could claim it outright.
func TestLosingAdopterNeverClearsTheWinner(t *testing.T) {
	t.Setenv("MUXCODE_SPAWN_IDLE_SECS", "600")
	f, idle := finishedRunWorker(t)
	refreshSpawns(t)
	if err := UpdateSpawnEntry(runTestSession, idle, func(e *SpawnEntry) { e.Spec = "docs/requirements/drafts/OLD.md" }); err != nil {
		t.Fatal(err)
	}
	stale, ok := findIdleWorker(runTestSession, "edit")
	if !ok || stale.SpawnRole != idle {
		t.Fatalf("fixture: %s must be found idle, found %+v", idle, stale)
	}

	var cleared []string
	loserTook := ""
	orig := spawnClearFn
	t.Cleanup(func() { spawnClearFn = orig })
	spawnClearFn = func(_, spawnRole string) error {
		cleared = append(cleared, spawnRole)
		if len(cleared) == 1 {
			if got, ok := adoptWorker(runTestSession, stale, "run-b", "implement", "run b's task", NewMsgID(graphSender)); ok {
				loserTook = got
			}
		}
		return nil
	}

	won, err := acquireSpawnWorker(runTestSession, "run-a", "implement", "edit", "run a's task")
	if err != nil || won != idle {
		t.Fatalf("run-a must adopt %s, got %s (%v)", idle, won, err)
	}
	if loserTook != "" || len(cleared) != 1 || f.fresh != 1 {
		t.Fatalf("the loser must touch nothing: took %q, cleared %v, %d launches", loserTook, cleared, f.fresh)
	}
	e := spawnByRole(t, idle)
	msgs, _ := Peek(runTestSession, idle)
	if e.RunID != "run-a" || e.Task != "run a's task" || len(msgs) != 1 || msgs[0].ID != e.SeedMsgID {
		t.Fatalf("the winner must own the worker with its one seed: run %q task %q inbox %+v", e.RunID, e.Task, msgs)
	}
}

// serialMapAfterItemOne starts a serial three-item map and answers item one,
// returning the run, its worker and the window fake.
func serialMapAfterItemOne(t *testing.T) (*GraphRun, string, *liveSpawnFake) {
	t.Helper()
	g := &Graph{Name: "serial-map", Start: "m",
		Nodes: []Node{{ID: "m", Type: NodeMap, Role: "edit", Items: "one,two,three", Message: "process ${item}"}}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)
	step(t, runTestSession, run.ID)
	worker := nodeTask(t, run.ID, "m")
	answerSpawnWith(t, runTestSession, worker, "did one. EXIT=0")
	return run, worker, f
}

// killStepAfterSeedSent runs one tick and kills it the moment a worker's seed
// is on the bus — the panic in the wake seam stands for the daemon dying
// before anything after the send is written.
func killStepAfterSeedSent(t *testing.T, runID string) {
	t.Helper()
	killed := errors.New("daemon killed after the seed was sent")
	graphSpawnWakeFn = func(string, string) { panic(killed) }
	panicked := false
	func() {
		defer func() {
			r := recover()
			if r != nil && r != killed {
				panic(r)
			}
			panicked = r == killed
		}()
		_ = StepGraphRun(runTestSession, runID)
	}()
	graphSpawnWakeFn = func(string, string) {}
	if !panicked {
		t.Fatal("fixture: the tick never sent a seed")
	}
}

// itemSeeds counts the spawn-task seeds of a map item sent to each worker.
func itemSeeds(t *testing.T, item string) map[string]int {
	t.Helper()
	msgs, err := readMessages(LogPath(runTestSession))
	if err != nil {
		t.Fatal(err)
	}
	seeds := map[string]int{}
	for _, m := range msgs {
		if m.Type == "request" && m.Action == "spawn-task" && strings.Contains(m.Payload, "process "+item) {
			seeds[m.To]++
		}
	}
	return seeds
}

// TestMapRestartMidDispatchKeepsEachItemsVerdict (review must-fix): the daemon
// dies after a serial map's worker was sent item two's seed but before the
// lane was pointed at it. Recorded only after the reseed, the restart read
// item two's answer as item one's, lost item one's verdict and seeded item
// two again. Each item's distinct verdict must land on that item, and item
// two must be seeded exactly once.
func TestMapRestartMidDispatchKeepsEachItemsVerdict(t *testing.T) {
	isolateLifecycle(t)
	run, worker, _ := serialMapAfterItemOne(t)
	killStepAfterSeedSent(t, run.ID)

	step(t, runTestSession, run.ID)
	if rows := lifecycleDetails(t, "graph-map-dispatch-recovered"); len(rows) != 1 || !strings.Contains(rows[0], worker) {
		t.Fatalf("the restart must find item two on %s, got %v", worker, rows)
	}
	answerSpawnWith(t, runTestSession, worker, "two broke. EXIT=1")
	step(t, runTestSession, run.ID)
	answerSpawnWith(t, runTestSession, worker, "did three. EXIT=0")
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "m")
	want := []string{OutcomeSuccess, OutcomeFailure, OutcomeSuccess}
	for i, r := range st.MapResults {
		if !r.Done || r.Outcome != want[i] {
			t.Errorf("item %d recorded %+v, want outcome %s", i, r, want[i])
		}
	}
	if seeds := itemSeeds(t, "two"); len(seeds) != 1 || seeds[worker] != 1 {
		t.Fatalf("item two must be seeded exactly once, on %s: %v", worker, seeds)
	}
}

// TestMapRestartWithLostWorkerReplacesOnce (review must-fix): the restart
// finds item two's interrupted dispatch on a worker whose window has since
// gone. Replaced before the pending dispatch was settled, the replacement was
// then pointed back at the dead worker and replaced again next tick — a
// second redrive and a second copy of item two. The lane must settle on the
// dead worker, be replaced exactly once, stay on the replacement, and every
// item must still land its own verdict.
func TestMapRestartWithLostWorkerReplacesOnce(t *testing.T) {
	run, worker, f := serialMapAfterItemOne(t)
	killStepAfterSeedSent(t, run.ID)
	f.deadWindows[worker] = true

	step(t, runTestSession, run.ID)
	replacement := nodeTask(t, run.ID, "m")
	if replacement == worker || f.fresh != 2 {
		t.Fatalf("the lost worker must be replaced on the restart: lane %s, %d launches", replacement, f.fresh)
	}
	for i := 0; i < 3; i++ {
		step(t, runTestSession, run.ID)
	}
	st, _ := ReadNodeStatus(runTestSession, run.ID, "m")
	if st.TaskID != replacement || len(st.MapPending) != 0 || st.Redrives != 1 || f.fresh != 2 {
		t.Fatalf("the lane must stay on %s after one replacement: lane %s, pending %v, redrives %d, %d launches",
			replacement, st.TaskID, st.MapPending, st.Redrives, f.fresh)
	}
	if seeds := itemSeeds(t, "two"); len(seeds) != 2 || seeds[worker] != 1 || seeds[replacement] != 1 {
		t.Fatalf("item two must reach the lost worker and its one replacement once each: %v", seeds)
	}

	answerSpawnWith(t, runTestSession, replacement, "two broke. EXIT=1")
	step(t, runTestSession, run.ID)
	answerSpawnWith(t, runTestSession, replacement, "did three. EXIT=0")
	step(t, runTestSession, run.ID)
	st, _ = ReadNodeStatus(runTestSession, run.ID, "m")
	want := []string{OutcomeSuccess, OutcomeFailure, OutcomeSuccess}
	for i, r := range st.MapResults {
		if !r.Done || r.Outcome != want[i] {
			t.Errorf("item %d recorded %+v, want outcome %s", i, r, want[i])
		}
	}
	if f.fresh != 2 {
		t.Errorf("item three must run on the replacement, got %d launches", f.fresh)
	}
}

// TestMapPendingDispatchResendsASeedThatNeverLeft: a restart between the
// reservation and the send leaves the worker carrying the pending seed id with
// no such message on the bus. The restart sends it once, under that id —
// neither dispatching the item afresh nor waiting on a seed that never comes.
func TestMapPendingDispatchResendsASeedThatNeverLeft(t *testing.T) {
	run, worker, _ := serialMapAfterItemOne(t)
	seed := NewMsgID(graphSender)
	if err := MutateNodeStatus(runTestSession, run.ID, "m", func(s *GraphNodeStatus) {
		s.MapResults[0] = MapItemResult{Done: true, Outcome: OutcomeSuccess, Report: "did one. EXIT=0"}
		s.MapLanes[0], s.MapNext = 1, 2
		s.MapPending = []MapDispatch{{Lane: 0, Item: 1, Seed: seed}}
	}); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSpawnEntry(runTestSession, worker, func(e *SpawnEntry) { e.SeedMsgID, e.Task = seed, "process two" }); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	msgs, _ := Peek(runTestSession, worker)
	if len(msgs) != 1 || msgs[0].ID != seed || msgs[0].Payload != "process two" {
		t.Fatalf("item two's seed must be sent once under %s: %+v", seed, msgs)
	}
	st, _ := ReadNodeStatus(runTestSession, run.ID, "m")
	if len(st.MapPending) != 0 || st.TaskID != worker || !st.MapResults[0].Done || st.MapResults[1].Done {
		t.Fatalf("the lane must settle on %s with only item one recorded: %+v", worker, st)
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
	release := func(runID, role string) string { // launched fresh: acquiring would adopt the held worker
		t.Helper()
		w, err := graphSpawnFn(runTestSession, role, "work", graphSender, runID, "implement", "")
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
