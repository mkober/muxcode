# MUX-195: Multiple Workers Are Spawned Per Graph Run — One Worker Per Run, One Per Spawning Agent

**Tracking:** [mkober/muxcode#97](https://github.com/mkober/muxcode/issues/97)

**Priority: Critical — the user's call, 2026-10-05: *"this is the highest priority defect."*** Tier 0,
rank 1 in [`backlog.md`](../backlog/backlog.md#defects--prioritized).

muxcode spawns **more than one worker per graph run** and **a fresh worker on every agent-initiated
spawn**, and the workers it spawns then **sit idle for hours**. Every worker is a full Claude process
with its own window, its own cold start (agent definition, `CLAUDE.md`, context load) and its own
context window — spent and then parked. The user's words, 2026-10-05:

> *"muxcode should only spawn 1 worker for each graph run or by another agent. Currently multiple
> graph worker agents are spawned, waste tokens and sit idle taking up resources."*

The rule this spec establishes:

| Spawner | Workers it may hold |
|---------|---------------------|
| A graph run | **One**, shared by every `spawn`/`map` node of the run (`implement`, `fix`, …) |
| An agent (`muxcode spawn start`) | **One per base role** — a second `start` while one idles reseeds it |
| The session, between runs | At most **one idle** worker per base role, adopted by the next run that needs it or reaped after a quiet window |

This spec **consolidates** the 2026-09-28 filing (no cross-run or cross-node reuse; finished runs'
workers stranded) with the duplicate-worker roads it had not named: the agent-initiated spawn road,
which has no reuse at all, and the executor's replace/resume roads, which always launch fresh.

## Context

### Source and standard of evidence

Filed 2026-09-28 on the user's instruction relayed by edit (brief: `worker-reuse-defect.md` in edit's
scratchpad); **broadened and raised to Critical 2026-10-05** by the user directly, in plan's pane, as
the one spec covering every road that produces a second worker. Everything under **Mechanism** was read
by plan from code — `5ac2495` on 09-28, `b0f6ead` on 10-05 — and the observations come from the live
session's `spawn.jsonl`, delivery store and `~/.config/muxcode/logs/muxcode.log`. The 09-28 brief's
framing ("a worker persists within its own run") is **narrowed** by what the code shows — reuse is per
run **and node**.

### Observed 2026-09-28 — three windows, two stranded

| Window | Spawn role | Run | Node | Run state |
|--------|-----------|-----|------|-----------|
| 11 | `spawn-d3d56f12` | `1790630495-50-spec-to-pr-6d13cbff` | `implement` | complete |
| 12 | `spawn-b25552f6` | `1790630495-50-spec-to-pr-6d13cbff` | `fix` | complete |
| 13 | `spawn-db7fd90d` | `1790641162-80-pr-review-fix-451d8466` | `fix` | running |

All three were `running` in the registry. The first two answered their seeds — responses
`1790630719-spawn-d3d56f12-702c2ee1` and `1790630941-spawn-b25552f6-8c4b2118` carry the seed ids as
`reply_to` — yet **neither seed (`1790630496-daemon-d8eba32c`, `1790630838-daemon-e161c0b5`) had a
delivery record**. The earlier run `1790629144` (failed at Phase 2) had its own worker as well.

### Observed 2026-10-05 — the same shape, one week on

Read from the lifecycle log (`spawn-complete`, `spawn-stop`, `graph-spawn-reuse`, `graph-run-created`
rows); a spawn id's leading timestamp is its creation time.

| Run | Workers spawned | Evidence |
|-----|-----------------|----------|
| `1791216367-50-spec-to-pr-dfb0abb4` (12:06, failed) | **2** — `spawn-2c03db0e` 12:21 (`implement`), `spawn-fb049cac` 12:28 (`fix`) | both `spawn-complete` 14:16 |
| `1791244235-50-spec-to-pr-4224ae33` (19:50, complete) | **2** — `spawn-7e2e7a97` 19:51 (`implement`), `spawn-8b202273` 20:03 (`fix`) | both `spawn-complete` 21:18 |
| `1791250026-80-pr-review-fix-91281e2d` (21:27) | **1 fresh** — `spawn-9d8b5650` 21:28, a cold start **ten minutes after two warm `edit` workers were torn down** | `spawn-complete` 21:42 |
| `1791152976-50-spec-to-pr-8e036b5a` (10-04 18:29, complete) | `spawn-2c49b5c8`, last reseeded 10-04 18:52, **`spawn-complete` 10-05 21:53** | alive ~27 h; its run's follow-on `80-pr-review-fix` launched 10-04 20:22, so it idled on a finished run for at least 25 h |
| `1791086572-50-spec-to-pr-1a73c35c` (10-04 00:02, canceled) | **2** — `spawn-d52dbf52`, `spawn-faaaeb48` | the cancel needed two `spawn-stop`s, 18:29 |

Across 09-28 → 10-05 the log records **21 worker launches** (17 `spawn-complete` + 4
`graph-cancel-spawn-stopped`) for roughly a dozen graph runs — about two per `50-spec-to-pr` run, one
per `80-pr-review-fix` run, and **zero** adoptions across runs (`graph-spawn-reuse` fires only within a
run's own node, 34 times). Workers of a finished run are torn down late or not at all; the session
registry was empty when read at 22:00 only because the 21:53 sweep had just cleared it.

### Mechanism — verified

| Fact | Where |
|------|-------|
| Graph reuse looks up a live worker keyed on `RunID` **and** `NodeID`; anything else falls through to a fresh `graphSpawnFn` | `acquireSpawnWorker`, `bus/graph_exec.go:79`; `FindLiveSpawn`, `bus/spawn.go:389` |
| So one `50-spec-to-pr` run holds **two** workers (`implement`, `fix`), and every new run builds its own | same |
| The agent road has **no lookup at all**: `muxcode spawn start` → `StartSpawn` → `StartSpawnOwned`, which unconditionally creates a window, seeds an inbox and launches an agent — a second `spawn start edit "…"` from the same owner is a second worker | `cmd/spawn.go:82` (`spawnStart`), `bus/spawn.go:133`, `:143` |
| `replaceLostWorkers` and `resumeDeadWorkers` reach for a **fresh** `graphSpawnFn` / relaunch per lost entry; neither consults the pool, and nothing bounds how many live entries one run may accumulate across redrives | `bus/graph_exec.go:1882`, `bus/spawn_resume.go:98` |
| **No cap exists anywhere**: no road counts live workers per role, per run or per session before creating one | `StartSpawnOwned` — the only creation point — takes no count |
| A responded worker is held only while its run is running (`spawnPersistent`); once the run is terminal, `RefreshSpawnStatus` kills its window — **if** `spawnHasResponded` reads true | `bus/spawn.go:455`, `:529–579` |
| `spawnHasResponded` reads the seed's delivery record; a missing record reads **false**, so the worker is neither held nor reaped — stranded running, window open, for hours | `bus/spawn.go:470` |
| Workers are never cut into a worktree: graph workers run in the session checkout, the tree build/test/review/commit use | `acquireSpawnWorker` doc comment, `bus/graph_exec.go:62–77` |
| Ownership is read from the registry's `RunID` by `spawnRunOwner` — first match by spawn role | `bus/graph_authority.go:43` |
| `CheckGraphNodeAuthority`, `CheckCancelAuthority` / `spawn stop`, `replaceLostWorkers` and `GraphOwnsTask` all key on that mapping | `graph_authority.go:17`, `cancel_authority.go:35`, `graph_exec.go:1882`, `:1578` |

Four roads, one symptom:

1. **Per-node reuse key** — a run with two spawn nodes is two workers; a second run is more.
2. **Agent road without reuse** — every `muxcode spawn start` is a cold start.
3. **Replace/resume without a pool or a bound** — a redrive adds a worker rather than re-pointing one.
4. **Terminal runs' workers stranded** when the seed's delivery record is gone — the
   [MUX-135](../backlog/MUX-135-spawn-seed-record-gc-strands-completion.md) mechanism (delivery-record GC
   strands a spawn) reaching the reaper, not only an in-flight iteration.

### Blast radius

- **Tokens.** Each cold start replays the agent definition, `CLAUDE.md` and the context files, and
  each parked worker holds a context window nothing will read again. Two workers per `50-spec-to-pr`
  run means the second pays the full boot to do what the first, now idle, already knows how to do.
- Every graph run with a `spawn`/`map` node: `50-spec-to-pr`, `60-integration-suite`,
  `80-pr-review-fix`, `90-ci-fix`. Each run pays one cold start per spawn node; a spec walked across
  several runs pays it per run.
- Stranded windows accumulate for the life of the session, each a live Claude process — the
  footprint concern of [MUX-147](../backlog/MUX-147-process-leak-and-memory-footprint.md) and
  [MUX-184](../backlog/MUX-184-orphaned-session-processes-never-reaped.md), from a different road.
- F-key reach: spawn windows sit at index 11+, so only the first two are F-key reachable
  ([MUX-128](../backlog/MUX-128-fkey-navigation-for-spawn-windows.md)); a pool that grows makes that worse.
- Cancel cost: a run with N workers needs N stops (`1791086572`, two `spawn-stop`s); a stop that fails
  on one of them keeps the node supervised (`stop_pending`) while the others are already gone.

## Requirements

### Acceptance criteria

**One worker per run**

- [x] A graph run holds **one** worker for all of its `spawn`/`map` nodes: a `50-spec-to-pr` run's `implement` and `fix` nodes run on the same worker — test: full run, spawn count **1** (Phase 3: `reserveRunWorker`; `TestSpecToPRRunSharesOneWorker`, `TestAcquireSpawnWorkerSharesTheRunsWorker`)
- [x] A new run **adopts** the session's idle worker of the same base role before spawning a fresh one; it spawns only when none is free — test: two sequential runs of the same template, spawn count **1** (Phase 3: `adoptIdleWorker`; the Phase 1 pin inverted to `TestSequentialRunsAdoptTheIdleWorker`)
- [x] `replaceLostWorkers` re-points or replaces **the run's one worker**: the lost window is confirmed gone before a fresh launch, and a replace and a resume never both act on one tick — test: a lost worker is still replaced (regression control), and the registry never shows two live entries for one run (Phase 3: `lostWindowsGone`; `TestLostWorkerReplacedOnlyOnceItsWindowIsGone`, `TestMapRestartWithLostWorkerReplacesOnce`)
- [x] A `map` node's items run on the run's one worker **serially** unless the template declares `workers: N` explicitly (Phase 3: `Node.Workers`, `mapLaneCount`, `advanceMapLanes`; `TestMapRunsItemsSeriallyOnTheRunsWorker`, `TestMapWorkersLanesTakeTheNextItem`) ([Decision 3](#decision-3--map-fan-out-and-busy-workers-serial-by-default-queue-on-busy))

**One worker per spawning agent**

- [x] `muxcode spawn start <role> "<task>"` from an owner that already holds an **idle** worker of that base role reseeds it instead of creating another; the command prints the reused spawn id and `spawn list` shows one entry — test: two `start`s, one worker (Phase 4: `AcquireAgentWorker` → `giveOwnWorker`, `How = reused`; `TestSpawnStartFromOneOwnerReusesItsIdleWorker`)
- [x] **Negative control:** an owner's worker that is busy (current seed unanswered) is not reseeded over — the second `start` either waits on the busy worker's queue or spawns a second worker, whichever Decision 3 fixes, and in both cases says so (Phase 4, per Decision 3: **queued** — the task lands behind the current one with no wake and no worktree advance (`postSpawnSeed`), `How = queued`, output `Queued on your busy spawn`; each queued task owes its own completion notice. `TestBusyWorkerNeverReused` "a second spawn start does not reseed over it", `TestQueuedTasksEachGetTheirOwnNotice`)

**Bound and reap**

- [x] A **hard cap** on live workers per base role per session (`MUXCODE_SPAWN_MAX_WORKERS`, default **3** per [Decision 4](#decision-4--the-per-role-worker-cap-muxcode_spawn_max_workers-default-3); only a fresh launch counts); an attempt past it is refused with a reason and a `spawn-cap-refused` lifecycle row — negative control: the cap refuses, and lowering it does not stop a run's single worker (Phase 4: `SpawnMaxWorkers`, `spawnCapError`, `reserveSpawnSlot`; `TestSpawnCapRefusesALaunchPastIt` — at cap 2 the third node waits `ready` with `DeferredOn` naming the cap, and under a lowered cap of 1 the run keeps its worker and nothing is killed; `TestMapLanesStopAtTheCap`)
- [x] Idle workers unowned by a live run are **reaped** after a quiet window (`MUXCODE_SPAWN_IDLE_SECS`, [Decision 2](#decision-2--idle-reap-window-muxcode_spawn_idle_secs-default-600)); adoption and reap each write a lifecycle row naming worker, old owner and new owner (Phase 2: `spawn-idle` / `spawn-reaped` name worker, last owner run/node, "new owner none"; Phase 3: `graph-spawn-adopted` names worker, old run/node, new run/node and the context policy)
- [x] A terminal run's worker is reaped (or returned as the idle worker) **even when its seed's delivery record is missing** — the 2026-09-28 and 2026-10-05 strandings cannot recur (Phase 2: `workerRepliedInLog` fallback; `TestTerminalRunWorkerFreedWithoutDeliveryRecord` — record present and record missing reach the same end state)
- [x] **Negative control:** a busy worker (current seed unanswered), or one whose owning run has an in-flight node on it, is never adopted or reaped (Phases 2–3: `workerAdoptable` requires an answered seed and a released run; `runWorkerHolder` refuses a worker another unfinished node still needs, and a missing node status reads as busy. `TestBusyWorkerNeverReused` — green through every phase — plus `TestForkedSpawnNodesKeepTheirOwnResults`)

**Ownership and context**

- [x] Adoption re-points ownership (`RunID`/`NodeID`/`Owner`) atomically under the registry lock: there is no instant at which the worker belongs to two runs or to none, and `CheckGraphNodeAuthority`, `CheckCancelAuthority`, `replaceLostWorkers` and `GraphOwnsTask` read the new owner immediately (Phase 3: `claimIdleWorker` rewrites `RunID`/`NodeID`/`Owner`/`SeedMsgID`/`Task`/`Spec` in one write under `withSpawnRegistryLock`; the authority readers all key on the registry's `RunID` via `spawnRunOwner`, so they see the new owner on their next read. End-to-end in Phase 5 section 2: after adoption, `spawn stop` and `CheckGraphNodeAuthority` both refuse naming the new run)
- [x] Two runs dispatching at once cannot adopt the same worker — test: concurrent dispatch yields one adoption and one spawn (within the cap) or one adoption and one wait, never a shared worker (Phase 3: `claimIdleWorker` re-checks under the lock; `TestConcurrentDispatchCannotShareAnIdleWorker`, run under `-race` by the run agent 2026-10-05 23:14, pass)
- [x] An adopted worker's context policy is explicit ([Decision 1](#decision-1--keep-or-clear-context-keep-within-a-spec-clear-on-a-spec-or-owner-kind-change)), and a stale task from the previous owner cannot reach the new one: the seed's ownership preamble (`graphWorkerTask`) names the new run and node (Phase 3: `adoptWorker` logs the policy — kept / cleared: spec changed / cleared: last served an agent — in the `graph-spawn-adopted` row; `adoptionNotice` opens the seed naming the new run and node; `dropStaleSeeds`. `TestAdoptionClearsContextWhenTheSpecChanged`, `TestAdoptionDropsThePreviousOwnersStaleSeed`)

**Visibility and docs**

- [x] `muxcode spawn list`/`status` show an **idle** (unowned, adoptable) worker distinctly from `parked` (held by a live run between iterations) and from `running` (Phase 2: `SpawnDisplayStatus`, `FormatSpawnStatus`; `TestFormatSpawnIdle`)
- [x] Docs: [`docs/agent-bus.md`](../../agent-bus.md#muxcode-spawn) (`spawn start` reuse, the cap, `idle`), [`docs/architecture.md`](../../architecture.md) spawn flow, `CLAUDE.md` graph-orchestration bullet (one worker per run) (2026-10-06: agent-bus.md spawn section + `map` `workers` note; architecture.md Agent Spawn Flow, one-worker paragraph, node rows, dispatch step; [`docs/configuration.md`](../../configuration.md) new *Spawn workers* section with both variables; `CLAUDE.md` bullet by edit)
- [x] `bash scripts/test-graph-worker-reuse.sh` passes (run agent 2026-10-06 10:20: 93 passed, 0 failed, floor 92 met; the run's graph test node — `./test.sh` — also returned `success`, the first authoritative test result on this spec)

### Technical approach

Make **one** function the only road to a worker. `acquireSpawnWorker` (graph) and `StartSpawn` (agent)
both call an `acquireWorker(session, baseRole, owner, runID, nodeID, task)` that, under the registry
lock: (1) finds the owner's live worker — the run's worker for a graph dispatch, the agent's idle
worker of that role for an agent spawn — and reseeds it; else (2) finds a **free** worker of the base
role (window exists, current seed answered, owning run terminal or absent) and **adopts** it, rewriting
`RunID`/`NodeID`/`Owner`/`SeedMsgID` in one `UpdateSpawnEntry` before `ReseedSpawn` sends, so ownership
and seed move together; else (3) checks the cap and spawns fresh via `StartSpawnOwned`. Free-ness must
not depend on the delivery record alone: fall back to the seed's `reply_to` in `log.jsonl` (the evidence
that proved the stranding), which also fixes the reaper. `replaceLostWorkers` and `resumeDeadWorkers`
go through the same function so a redrive cannot add a second live entry for a run. The reaper
(`RefreshSpawnStatus`) gains the idle quiet window and the cap check.

### Key files

| File | Role |
|------|------|
| `tools/muxcode/bus/graph_exec.go:53`, `:79` | `graphSpawnFn`, `acquireSpawnWorker` — the graph reuse key |
| `tools/muxcode/cmd/spawn.go:82` | `spawnStart` — the agent road, no reuse today |
| `tools/muxcode/bus/spawn.go:133`, `:143`, `:389`, `:425`, `:455`, `:470`, `:529` | `StartSpawn`, `StartSpawnOwned`, `FindLiveSpawn`, `ReseedSpawn`, `spawnPersistent`, `spawnHasResponded`, `RefreshSpawnStatus` |
| `tools/muxcode/bus/graph_exec.go:1882`, `bus/spawn_resume.go:98` | `replaceLostWorkers`, `resumeDeadWorkers` |
| `tools/muxcode/bus/graph_authority.go:43` | `spawnRunOwner` — ownership read |
| `tools/muxcode/bus/cancel_authority.go`, `graph_exec.go:1578` | cancel authority, `GraphOwnsTask` |
| `scripts/test-graph-worker-reuse.sh` | new |

## Implementation

### Phase 1: Pin

- [x] Unit test: one `50-spec-to-pr` run with `implement` and `fix` → two workers today (`TestSpecToPRRunSpawnsWorkerPerNodeToday`, `bus/spawn_reuse_test.go`; written as a green-today pin that Phase 3 inverts — see [note](#phase-1-verification-note))
- [x] Unit test: two sequential runs → two workers today (`TestSequentialRunsSpawnWorkerPerRunToday`; asserts the first worker is idle — running, answered, window live — before the second run spawns; Phase 3 inverts)
- [x] Unit test: two `spawn start` calls from one owner → two workers today (`TestSpawnStartFromOneOwnerSpawnsWorkerPerCallToday`; Phase 4 inverts. Enabled by one seam: `StartSpawnOwned`'s tmux window + launch moved verbatim into `launchSpawnWindow` behind `var spawnLaunchFn`, `bus/spawn.go` — order and behaviour unchanged)
- [x] Unit test: a terminal run's worker whose seed has no delivery record stays `running` after `RefreshSpawnStatus` (`TestTerminalRunWorkerStrandedWithoutDeliveryRecordToday`, paired with a record-present control that is reaped; Phase 2 inverts)
- [x] Unit test (negative control, stays green): a busy worker is never reused (`TestBusyWorkerNeverReused` — not adopted by another run, not reseeded over by a second `spawn start`, not reaped with or without its delivery record)

#### Phase 1 verification note

Verified 2026-10-05 22:25 by plan from the working tree (run `1791252906-50-spec-to-pr-aeb56461`).
The pins are written **green-today** — each asserts the duplicate-worker road as it stands, with a
precondition that the first worker is idle, so the fixing phase inverts the pin rather than adding a
test beside it. That is a sounder shape than the "pin red" wording above, which a suite cannot carry.
**A passing run is not yet proven**: the graph's test node returned `unknown` in 11 s — the Codex test
agent refused the suite (*"this review agent is restricted from executing tests/builds"*, the
[MUX-153](../backlog/MUX-153-codex-test-agent-cannot-run-the-suite.md) shape) — and the user released the hold by
hand at 22:21:53. The review node passed; the file's helpers all exist in the package and both files
are gofmt-clean. The five tests should be run green before Phase 2 builds on them. *Resolved 23:14 — run
green by the run agent; see the [Phase 3 note](#phase-3-verification-note).*

### Phase 2: Reaper and free-ness

- [x] `spawnHasResponded` falls back to a `reply_to` match in the session log when the delivery record is missing (`workerRepliedInLog`, `bus/spawn.go` — only on `os.ErrNotExist`, newest-first, stops at the seed line; only the worker's own reply to that seed counts. `TestSpawnHasRespondedFallsBackToWorkerReply`, 4 cases incl. 3 negative controls)
- [x] Terminal-run workers are reaped or kept as the one idle worker per Decision 2; lifecycle row on each (`spawn-idle`, `spawn-reaped`) (`RefreshSpawnStatus` + `idleVerdicts`: new `IdleSince` field; per base role the most recently released worker is held for `SpawnIdleSecs()` — `MUXCODE_SPAWN_IDLE_SECS`, env then config, default 600, `0` = reap on release — the rest reaped as superseded; a worker owed a stop is never held; `IdleSince` clears if the worker is busy or owned again. Agent spawns keep reap-on-answer until Phase 4. `TestIdlePoolHoldsOneWorkerPerBaseRole`, `TestIdleHoldNeverHoldsAWorkerOwedAStop`, `TestIdleWorkerOwnedAgainLosesIdleStamp`; Phase 1 stranding pin inverted to `TestTerminalRunWorkerFreedWithoutDeliveryRecord`)
- [x] `spawn list`/`status` render `idle` (`SpawnDisplayStatus` returns `idle` for a released graph worker, distinct from `parked`; `FormatSpawnStatus` prints an `Idle:` line. `TestFormatSpawnIdle`; `TestSpawnDisplayStatusParked` now expects `idle` after the run completes)

#### Phase 2 verification note

Verified 2026-10-05 22:42 by plan from the working tree (run `1791252906`, second iteration; Phase 1
committed as `a8d3295`). All three steps implemented in `bus/spawn.go` as the step annotations record,
and the four changed files are gofmt-clean (the `gofmt -l` hits in `agent_test.go`, `ollama.go`,
`ollama_test.go`, `reload.go`, `spec_items_test.go` predate this branch). **Still unrun**: the test node
returned `unknown` again (6 s) and the user released the hold by hand at 22:37:38; the Phase 1 pins and
every Phase 2 test have been reviewed but never executed. One cost to watch: `workerRepliedInLog` reads
the whole session log on each call, but only for an entry whose delivery record is already gone, so the
daemon's 2 s sweep pays it only for the stranded case it exists to fix.

### Phase 3: One worker per run

- [x] `acquireWorker`: run-owned lookup keyed on `RunID` alone; `implement` and `fix` share the worker (`acquireSpawnWorker` → `acquireSeededWorker`, `bus/graph_exec.go`, the single road: `reserveRunWorker` keyed on run + base role moves `NodeID` with the seed in one locked write; a worker another unfinished node still holds is never seeded over — `errRunWorkerBusy` and `deferDispatch` keep the node `ready`, retried each tick, `graph-spawn-deferred` once per holder. `TestAcquireSpawnWorkerSharesTheRunsWorker`, `TestForkedSpawnNodesKeepTheirOwnResults`)
- [x] Adoption of a free worker of the same base role; atomic ownership re-point under the registry lock, before the reseed; `graph-spawn-adopted` row with old and new owner (`adoptIdleWorker` → `findIdleWorker`/`workerAdoptable` → `claimIdleWorker`: one write under `withSpawnRegistryLock` — process mutex + `flock` on `spawn.jsonl.lock`, now held by every registry read-modify-write — re-checking the worker is still idle and unchanged; the loser of a race touches nothing. Row names old run/node, new run/node and the context policy. `TestConcurrentDispatchCannotShareAnIdleWorker` (`-race` pass), `TestLosingAdopterNeverClearsTheWinner`)
- [x] `replaceLostWorkers` and `resumeDeadWorkers` route through `acquireWorker`; one live entry per run invariant pinned (`replaceLostWorkers` calls `acquireSpawnWorker` and launches only after `lostWindowsGone` proves the lost window gone — a live one is killed and waited for, spending no replacement; a resume relaunches in the worker's own window and never creates an entry, and the two roads act on disjoint workers in one `||` chain. `TestLostWorkerReplacedOnlyOnceItsWindowIsGone`, `TestMapRestartWithLostWorkerReplacesOnce`)
- [x] Context policy per Decision 1; `graphWorkerTask` re-states the new owner (`adoptWorker`: kept while `SpawnEntry.Spec` — new field, the active spec at the last seed — matches; `/clear` via `spawnClearFn` when the spec changed or the worker last served an agent; a failed clear releases the claim. The seed opens with `adoptionNotice`, naming the new run/node and closing the previous owner's work; `dropStaleSeeds` consumes the previous owner's queued `spawn-task` rows. `TestAdoptionClearsContextWhenTheSpecChanged`, `TestAdoptionDropsThePreviousOwnersStaleSeed`)
- [x] `map` serial-by-default per Decision 3; invert the Phase 1 run pins (`Node.Workers`, validated non-negative; `mapLaneCount` = `min(max(workers,1), items)`; lanes advance through a persisted work queue — `MapLanes`/`MapNext`/`MapResults`/`MapPending` on `GraphNodeStatus`, dispatches persisted with their seed id before any worker is touched so a restart mid-dispatch neither loses nor double-runs an item; results recorded in item order. Pins inverted: `TestSpecToPRRunSharesOneWorker`, `TestSequentialRunsAdoptTheIdleWorker`. `TestMapRunsItemsSeriallyOnTheRunsWorker`, `TestMapWorkersLanesTakeTheNextItem`, `TestMapRestartMidDispatchKeepsEachItemsVerdict`, `TestMapPendingDispatchResendsASeedThatNeverLeft`)

#### Phase 3 verification note

Verified 2026-10-05 23:55 by plan from the working tree (run `1791252906`, third iteration; Phase 2
committed as `311bc3c`). **First executed evidence on this branch**: the worker had the run agent execute
`mux195-unit.sh` (task `1791256426-spawn-a4928558-3d348868`, 23:14) — `go vet ./...` (compiles every
test file), the named Phase 1–3 tests, `TestConcurrentDispatchCannotShareAnIdleWorker` under `-race`,
then the whole `bus` package: `RESULT vet=0 focused=0 missing=0 race=0 full=1`, 101 PASS / 0 FAIL on the
focused set, the package's one failure the pre-existing `TestOpenCodeModelsExist` (stale OpenCode model
list, unrelated). That run also executed the Phase 1 agent-road pin and every Phase 2 test, so the
"never run" caveat on those phases is retired. The graph's own test node still returned `unknown`
(three times this iteration, each released by the user) — MUX-153 is untouched. Review failed twice
before passing; the fixes it demanded are in the tree (persisted map dispatches, settle-before-replace).
**Live behaviour is not yet evidence either way**: the first `fix` dispatch at 23:18 gave the run a
second worker (`spawn-9fb5f090` beside `spawn-a4928558`) because the session daemon still runs the
pre-Phase-3 binary — `build.sh`'s `upgrade-daemons` cannot reach it from the build sandbox (MUX-161);
the first live confirmation comes after `muxcode upgrade-daemons` from the user's terminal.

### Phase 4: One worker per agent, and the cap

- [x] `spawnStart` routes through `acquireWorker`: an owner's idle worker of the base role is reseeded; output names the reuse (`AcquireAgentWorker`, `bus/spawn_pool.go` — own worker of the role and worktree kind reseeded when idle or the task **queued** on its inbox when busy (`giveOwnWorker`, claim before any worktree advance), else the session's idle worker adopted (`adoptWorkerFor`, shared with the graph road), else fresh; the whole decision under a per-agent/role/kind `flock` (`withAgentSpawnLock`) so two starts from one agent cannot both launch. `cmd/spawn.go` prints `Reused your idle spawn` / `Queued on your busy spawn` / `Adopted idle spawn` / `Started spawn`. `TestSpawnStartNeverSeedsOverAWorkerARunAdopted`, `TestSpawnStartAdoptsAnIdleWorkerOfItsKindOnly`, `TestQueuedTasksEachGetTheirOwnNotice`)
- [x] Per-role live-worker cap with `spawn-cap-refused`; `MUXCODE_SPAWN_IDLE_SECS` and the cap documented in [`docs/configuration.md`](../../configuration.md) (`SpawnMaxWorkers`, default 3 per Decision 4; the slot is **reserved** as a `starting` entry in the same locked write that checks the cap (`reserveSpawnSlot`), before any worktree, seed or window exists, rolled back by `abortSpawnStart`; a graph node at the cap waits `ready` like a busy-worker deferral, a `map` runs the lanes it got, `spawn start` fails with the live list. `TestSpawnCapRefusesALaunchPastIt` incl. the lowering control, `TestMapLanesStopAtTheCap`. Docs: *Spawn workers* section)
- [x] Invert the Phase 1 agent pin (`TestSpawnStartFromOneOwnerSpawnsWorkerPerCallToday` → `TestSpawnStartFromOneOwnerReusesItsIdleWorker`)
- [x] Docs: `agent-bus.md`, `architecture.md`, `CLAUDE.md` (plan, 2026-10-06, from the worker's handoff; `CLAUDE.md` by edit — see the docs acceptance criterion)

#### Phase 4 verification note

Verified 2026-10-06 00:40 by plan from the working tree (run `1791252906`, fourth iteration; Phase 3
committed as `04761f9`) — on a chain `verify-spec` that edit's own review in the `muxcode-fixes` worktree
triggered against the active spec (the MUX-150 shape), not yet on the run's own `update-spec` dispatch.
The run agent's `mux195-unit-run6.log` (00:15, pre-review code): `RESULT vet=0 focused=0 race=0 full=1`,
**106 PASS / 0 FAIL** focused, the package's one failure again the pre-existing `TestOpenCodeModelsExist`.
The run's review node then **failed at 00:24 with four must-fixes** — cap reserved only after launch;
one-worker-per-owner not atomic across concurrent starts; completion notices gated on `IdleSince` and
lost on a replaced seed; worktree advanced before the ownership recheck — and the `fix` worker is in
flight. All four are visibly addressed in the tree as read: `reserveSpawnSlot` writes a counted
`starting` entry under the registry lock before any side effect (`abortSpawnStart` rolls back);
`withAgentSpawnLock` serializes an agent's starts per role and kind with same-owner retry; owed
completions persist in `NoticesOwed` (`bus/spawn_notice.go`, `NotifySpawnCompletions`, acked by seed
after sending — the daemon's `checkSpawns` now reads them rather than the refresh's return); and
`giveOwnWorker` claims before `spawnAdvanceWorktreeFn`. **The post-fix code has not been re-reviewed or
re-run**; the graph's review node and its `verify-spec` will say whether the ticks above hold, and the
test node's `unknown` (the Codex test agent still declines to run tests, MUX-153) was released by the
user once more at 00:23. *Resolved: the user cancelled that run at 00:46 and committed Phase 4 as
`98a4aec`; `61205e7` then fixed four muxcode defects the run had exposed — among them the one behind
every `unknown` test node here: Codex roles shared a single `.codex/AGENTS.md`, so the test agent read
the review copy and refused as "a review agent". Each role now gets its own, and the Phase 5 run's test
node returned `success`.*

#### Phase 5 verification note

Verified 2026-10-06 10:30 by plan from the working tree (run `1791295134-50-spec-to-pr-c98bd5e0`,
launched by edit). The run's own nodes all returned authoritative results for the first time: `test`
**success** (`./test.sh`, 68 s), `review` success with 0 must-fix after one should-fix round (the stub
recorded a send before it succeeded; fixed, +5 checks, floor 87 → 92). The script was executed twice
by the run agent, 88/0 then **93/0 at floor 92**. No Go changes in this phase. With this, every
acceptance criterion and every phase step in this spec is ticked.

### Phase 5: Integration test

- [x] Create `scripts/test-graph-worker-reuse.sh` — hermetic scratch daemon, stub workers (496 lines; scratch `BUS_SESSION`, bus dir, `HOME`, config file, lifecycle log and repo; stub agents print the `❯` the injection guard needs and answer each seed with `EXIT=0`, or hold busy while `$CTL/hold` exists; graph road on base role `edit`, agent road on `research`, so the two idle pools never meet; cap and idle window retuned through the scratch config file; tests the **installed** binary, ~3 min; listed in `CLAUDE.md`)
- [x] Test: one run with two spawn nodes uses one worker (spawn count 1); lifecycle shows the second node's reuse (section 1: `fix` runs on `implement`'s worker, one `edit` worker and one window, `graph-spawn-reuse … (last node implement)`, released `spawn-idle`, `spawn status` reads `idle`)
- [x] Test: two sequential runs reuse one worker (spawn count 1); lifecycle shows the adoption with old and new owner (section 2: `graph-spawn-adopted` names old owner R1/fix, new owner R2/implement, context kept; still one `edit` worker; the registry entry now names R2)
- [x] Test: two `spawn start` calls from one owner → one worker; output says reused (section 4: `Reused your idle spawn`, one `research` worker, `spawn-reused` row; a start against it while busy prints `Queued on your busy spawn` and every queued task is answered — bus-log assertions correlate the worker's replies to four distinct seeds; another owner within the cap gets `Started spawn`)
- [x] **Negative control:** a busy worker is not adopted — the second demand waits or spawns within the cap, and says which (section 3: R4 runs on a worker of its own beside busy `W1`, no `graph-spawn-adopted` for R4, two `edit` workers; section 4: another owner past the cap is refused rather than handed the busy worker)
- [x] **Negative control:** the cap refuses a launch past it with `spawn-cap-refused` (section 3: at cap 2, R5's node waits `ready`, `DeferredOn` names `MUXCODE_SPAWN_MAX_WORKERS=2`, `spawn-cap-refused` + `graph-spawn-deferred` rows, no worker launched; lowering the cap to 1 stops neither live worker; R5 then completes **by adoption** while the cap is 1 — adoption is never refused by the cap)
- [x] Test: after adoption, `spawn stop` authority and `CheckGraphNodeAuthority` follow the new run, not the old (section 2: an agent's `spawn stop` is refused naming R2 — the user's run — where it would have passed under agent-launched R1; the worker's `build:build` request is refused by `CheckGraphNodeAuthority` naming R2's node; negative control: its request to plan, which R2 does not own, delivers)
- [x] Test: a finished run's worker with its delivery record removed is reaped (or idle) within the quiet window, never stranded (section 5: record present then removed; three ticks later still `idle` on the same stamp, not misread as busy; `spawn-reaped … last owner R6, new owner none` when the window closes; entry `completed`, window gone)
- [x] Test: a lost worker is replaced and the registry never shows two live entries for the run (section 6: window killed mid-task → `graph-spawn-replaced` exactly once, node runs on the replacement, run completes; a registry sampler over ≥10 samples never saw more than one live entry)
- [x] Coverage floor set to the maximum achievable count; run the script and record counts here (`FLOOR=92`, the section arithmetic 1+9+17+21+24+11+9 written out in the script, not a margin. Run agent, 2026-10-06 10:20, task `1791296350-spawn-f1ccb1ae-4592d6cd`: **93 passed, 0 failed, floor 92 met** — the first run was 88/0 at floor 87 before review's should-fix added five bus-log checks)

## Decisions

All three decided by the user 2026-10-05, in edit's pane, relayed by the run's worker. They are settled;
Phases 2–4 implement them as written.

### Decision 1 — keep or clear context: keep within a spec, clear on a spec or owner-kind change

**Decided.** An adopted worker **keeps** its conversation while the active spec is unchanged — the
saving the user asked for; the next run starts warm on the same codebase. It is **`/clear`ed** on
adoption when either the active spec has changed since the worker's last seed, or the owner switches
kind between a graph run and an agent (`muxcode spawn start`). The alternatives weighed were keep-always
(carries a previous run's assumptions across specs) and clear-always (loses most of the context saving;
only the boot cost is saved).

### Decision 2 — idle reap window: `MUXCODE_SPAWN_IDLE_SECS`, default 600

**Decided.** An idle worker unowned by a live run is reaped after **600 s** of quiet, configurable via
`MUXCODE_SPAWN_IDLE_SECS` — in the family of `MUXCODE_AUTO_CLEAR_QUIET_SECS` (60) and the 600 s task
timeout. One idle worker per base role is the pool; there is no larger pool.

### Decision 3 — map fan-out and busy workers: serial by default, queue on busy

**Decided.** A `map` node's items run **one at a time on the run's single worker** unless the template
sets `workers: N` explicitly (capped by the per-role cap). On the agent road, a second `spawn start`
against a **busy** worker **queues on that worker's inbox** and says so in its output — it does not spawn
a second worker. The `60-integration-suite` template is the one to check the serial default against.

### Decision 4 — the per-role worker cap: `MUXCODE_SPAWN_MAX_WORKERS`, default 3

**Decided by the user 2026-10-06, in edit's pane, relayed by the run's worker.** The hard cap on live
workers of one base role per session defaults to **3** — one idle worker, a graph run's worker and an
agent spawn side by side — settling the "1 idle + the concurrency Decision 3 allows" placeholder in the
acceptance criteria. Only a **fresh launch** counts and is refused: reuse and adoption never do, and
lowering the cap never stops a live worker. `0` disables the cap. At the cap a graph `spawn`/`map` node
waits `ready` (`graph-spawn-deferred` + `spawn-cap-refused`, once per reason), a `map` with `workers: N`
runs on the lanes it got, and `muxcode spawn start` fails with the reason and the live worker list
(exit 1, `spawn-cap-refused`). Env then config file, like `MUXCODE_SPAWN_IDLE_SECS`.

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-131](./MUX-131-spawn-implement-output-never-ported.md) | Predecessor, complete — stopped the worker being rebuilt **every iteration** within one node; this spec extends the same rule to the run, the session and the agent road |
| [MUX-135](../backlog/MUX-135-spawn-seed-record-gc-strands-completion.md) | Dependency — the delivery-record GC that makes `spawnHasResponded` lie is the stranding mechanism here; Phase 2's `reply_to` fallback closes this side of it |
| [MUX-120](../backlog/MUX-120-spawn-worker-never-woken-for-seeded-task.md) | Same family — a seeded worker never woken; not merged, a delivery defect rather than a count defect |
| [MUX-188](../backlog/MUX-188-spawn-worker-launch-sends-edit-a-stray-startup-request.md) | Same family — a side effect of each launch; fewer launches means fewer stray requests, but it is its own fix |
| [MUX-128](../backlog/MUX-128-fkey-navigation-for-spawn-windows.md) | Fewer worker windows is what makes its F11/F12 question tractable |
| [MUX-147](../backlog/MUX-147-process-leak-and-memory-footprint.md), [MUX-184](../backlog/MUX-184-orphaned-session-processes-never-reaped.md) | Same footprint, different roads |
| [MUX-178](./MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md) | Why a worker ends unanswered; graph workers run in the session checkout, so adoption does not cross trees |

## Out of scope

- Worktree isolation for spawn nodes — [MUX-178](./MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md). Graph workers run in the session checkout today, so reuse does not cross trees within a session; if worktrees return, an adopted worker must advance or re-cut its tree on adoption (`advanceSpawnWorktree` already runs on reseed).
- Cross-session reuse.
- The wake and delivery defects of [MUX-120](../backlog/MUX-120-spawn-worker-never-woken-for-seeded-task.md).

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-195-graph-runs-never-reuse-idle-workers | 2h 43m | 2026-10-06 10:25 |

## Status

Complete — 2026-10-06. All five phases and all 18 acceptance criteria verified; closed out the same day
by the `50-spec-to-pr` run `1791295134` and moved to `completed/`. Phases on
`MUX-195-graph-runs-never-reuse-idle-workers`: `a8d3295`, `311bc3c`, `04761f9`, `98a4aec`, `02c4754`
(+ `61205e7`, muxcode defects the run exposed). Rank 1 / Tier 0 in the defects table until this close;
the backlog index now carries it in the completed registry.

Filed 2026-09-28 on the user's instruction relayed by edit; mechanism verified the same day against
the live session (three worker windows, two stranded on a complete run). **Broadened 2026-10-05** by the
user directly — one worker per graph run, one per spawning agent — with the agent-spawn and
replace/resume roads added, re-verified at `b0f6ead` against the 10-05 lifecycle log, and raised to
**Critical / Tier 0 / rank 1** on the user's call.
