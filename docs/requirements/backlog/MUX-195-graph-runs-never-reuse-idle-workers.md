# MUX-195: Multiple Workers Are Spawned Per Graph Run — One Worker Per Run, One Per Spawning Agent

**Tracking:** [mkober/muxcode#97](https://github.com/mkober/muxcode/issues/97)

**Priority: Critical — the user's call, 2026-10-05: *"this is the highest priority defect."*** Tier 0,
rank 1 in [`backlog.md`](./backlog.md#defects--prioritized).

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
   [MUX-135](./MUX-135-spawn-seed-record-gc-strands-completion.md) mechanism (delivery-record GC
   strands a spawn) reaching the reaper, not only an in-flight iteration.

### Blast radius

- **Tokens.** Each cold start replays the agent definition, `CLAUDE.md` and the context files, and
  each parked worker holds a context window nothing will read again. Two workers per `50-spec-to-pr`
  run means the second pays the full boot to do what the first, now idle, already knows how to do.
- Every graph run with a `spawn`/`map` node: `50-spec-to-pr`, `60-integration-suite`,
  `80-pr-review-fix`, `90-ci-fix`. Each run pays one cold start per spawn node; a spec walked across
  several runs pays it per run.
- Stranded windows accumulate for the life of the session, each a live Claude process — the
  footprint concern of [MUX-147](./MUX-147-process-leak-and-memory-footprint.md) and
  [MUX-184](./MUX-184-orphaned-session-processes-never-reaped.md), from a different road.
- F-key reach: spawn windows sit at index 11+, so only the first two are F-key reachable
  ([MUX-128](./MUX-128-fkey-navigation-for-spawn-windows.md)); a pool that grows makes that worse.
- Cancel cost: a run with N workers needs N stops (`1791086572`, two `spawn-stop`s); a stop that fails
  on one of them keeps the node supervised (`stop_pending`) while the others are already gone.

## Requirements

### Acceptance criteria

**One worker per run**

- [ ] A graph run holds **one** worker for all of its `spawn`/`map` nodes: a `50-spec-to-pr` run's `implement` and `fix` nodes run on the same worker — test: full run, spawn count **1**
- [ ] A new run **adopts** the session's idle worker of the same base role before spawning a fresh one; it spawns only when none is free — test: two sequential runs of the same template, spawn count **1** (today: 2, pinned red in Phase 1)
- [ ] `replaceLostWorkers` re-points or replaces **the run's one worker**: the lost window is confirmed gone before a fresh launch, and a replace and a resume never both act on one tick — test: a lost worker is still replaced (regression control), and the registry never shows two live entries for one run
- [ ] A `map` node's items run on the run's one worker **serially** unless the template declares `workers: N` explicitly ([Decision 3](#decision-3--map-fan-out-concurrency))

**One worker per spawning agent**

- [ ] `muxcode spawn start <role> "<task>"` from an owner that already holds an **idle** worker of that base role reseeds it instead of creating another; the command prints the reused spawn id and `spawn list` shows one entry — test: two `start`s, one worker
- [ ] **Negative control:** an owner's worker that is busy (current seed unanswered) is not reseeded over — the second `start` either waits on the busy worker's queue or spawns a second worker, whichever Decision 3 fixes, and in both cases says so

**Bound and reap**

- [ ] A **hard cap** on live workers per base role per session (default **1 idle + the concurrency Decision 3 allows**); an attempt past it is refused with a reason and a `spawn-cap-refused` lifecycle row — negative control: the cap refuses, and lowering it does not stop a run's single worker
- [ ] Idle workers unowned by a live run are **reaped** after a quiet window (`MUXCODE_SPAWN_IDLE_SECS`, [Decision 2](#decision-2--idle-reap-window)); adoption and reap each write a lifecycle row naming worker, old owner and new owner
- [ ] A terminal run's worker is reaped (or returned as the idle worker) **even when its seed's delivery record is missing** — the 2026-09-28 and 2026-10-05 strandings cannot recur
- [ ] **Negative control:** a busy worker (current seed unanswered), or one whose owning run has an in-flight node on it, is never adopted or reaped

**Ownership and context**

- [ ] Adoption re-points ownership (`RunID`/`NodeID`/`Owner`) atomically under the registry lock: there is no instant at which the worker belongs to two runs or to none, and `CheckGraphNodeAuthority`, `CheckCancelAuthority`, `replaceLostWorkers` and `GraphOwnsTask` read the new owner immediately
- [ ] Two runs dispatching at once cannot adopt the same worker — test: concurrent dispatch yields one adoption and one spawn (within the cap) or one adoption and one wait, never a shared worker
- [ ] An adopted worker's context policy is explicit ([Decision 1](#decision-1--keep-or-clear-context)), and a stale task from the previous owner cannot reach the new one: the seed's ownership preamble (`graphWorkerTask`) names the new run and node

**Visibility and docs**

- [ ] `muxcode spawn list`/`status` show an **idle** (unowned, adoptable) worker distinctly from `parked` (held by a live run between iterations) and from `running`
- [ ] Docs: [`docs/agent-bus.md`](../../agent-bus.md#muxcode-spawn) (`spawn start` reuse, the cap, `idle`), [`docs/architecture.md`](../../architecture.md) spawn flow, `CLAUDE.md` graph-orchestration bullet (one worker per run)
- [ ] `bash scripts/test-graph-worker-reuse.sh` passes

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
[MUX-153](./MUX-153-codex-test-agent-cannot-run-the-suite.md) shape) — and the user released the hold by
hand at 22:21:53. The review node passed; the file's helpers all exist in the package and both files
are gofmt-clean. The five tests should be run green before Phase 2 builds on them.

### Phase 2: Reaper and free-ness

- [ ] `spawnHasResponded` falls back to a `reply_to` match in the session log when the delivery record is missing
- [ ] Terminal-run workers are reaped or kept as the one idle worker per Decision 2; lifecycle row on each (`spawn-idle`, `spawn-reaped`)
- [ ] `spawn list`/`status` render `idle`

### Phase 3: One worker per run

- [ ] `acquireWorker`: run-owned lookup keyed on `RunID` alone; `implement` and `fix` share the worker
- [ ] Adoption of a free worker of the same base role; atomic ownership re-point under the registry lock, before the reseed; `graph-spawn-adopted` row with old and new owner
- [ ] `replaceLostWorkers` and `resumeDeadWorkers` route through `acquireWorker`; one live entry per run invariant pinned
- [ ] Context policy per Decision 1; `graphWorkerTask` re-states the new owner
- [ ] `map` serial-by-default per Decision 3; invert the Phase 1 run pins

### Phase 4: One worker per agent, and the cap

- [ ] `spawnStart` routes through `acquireWorker`: an owner's idle worker of the base role is reseeded; output names the reuse
- [ ] Per-role live-worker cap with `spawn-cap-refused`; `MUXCODE_SPAWN_IDLE_SECS` and the cap documented in [`docs/configuration.md`](../../configuration.md)
- [ ] Invert the Phase 1 agent pin
- [ ] Docs: `agent-bus.md`, `architecture.md`, `CLAUDE.md`

### Phase 5: Integration test

- [ ] Create `scripts/test-graph-worker-reuse.sh` — hermetic scratch daemon, stub workers
- [ ] Test: one run with two spawn nodes uses one worker (spawn count 1); lifecycle shows the second node's reuse
- [ ] Test: two sequential runs reuse one worker (spawn count 1); lifecycle shows the adoption with old and new owner
- [ ] Test: two `spawn start` calls from one owner → one worker; output says reused
- [ ] **Negative control:** a busy worker is not adopted — the second demand waits or spawns within the cap, and says which
- [ ] **Negative control:** the cap refuses a launch past it with `spawn-cap-refused`
- [ ] Test: after adoption, `spawn stop` authority and `CheckGraphNodeAuthority` follow the new run, not the old
- [ ] Test: a finished run's worker with its delivery record removed is reaped (or idle) within the quiet window, never stranded
- [ ] Test: a lost worker is replaced and the registry never shows two live entries for the run
- [ ] Coverage floor set to the maximum achievable count; run the script and record counts here

## Open decisions

### Decision 1 — keep or clear context

**Keep** the conversation (the saving the user asked for — the next run starts warm on the same
codebase) vs **`/clear` between owners** (no carry-over of the previous run's assumptions, but most of
the context saving is lost; the boot cost is still saved). A middle road: keep within a spec's runs,
clear when the active spec changes or the owner changes from a run to an agent.

### Decision 2 — idle reap window

How long an unowned idle worker lives before it is reaped. The user's priority is resources, so the
default should be short — `MUXCODE_SPAWN_IDLE_SECS`, proposed **600**, in the family of
`MUXCODE_AUTO_CLEAR_QUIET_SECS` (60) and the 600 s task timeout. One idle worker per base role is the
pool; there is no larger pool.

### Decision 3 — map fan-out concurrency

A `map` node over N items could run N workers at once. Under the one-worker rule it runs them serially
on the run's worker unless the template says otherwise (`workers: N`, capped by the per-role cap). The
same decision settles the agent road: a second `spawn start` against a busy worker **waits** (queued on
the worker's inbox) or **spawns a second** within the cap. Serial-by-default is the proposal; the
`60-integration-suite` template is the one to check it against.

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-131](../completed/MUX-131-spawn-implement-output-never-ported.md) | Predecessor, complete — stopped the worker being rebuilt **every iteration** within one node; this spec extends the same rule to the run, the session and the agent road |
| [MUX-135](./MUX-135-spawn-seed-record-gc-strands-completion.md) | Dependency — the delivery-record GC that makes `spawnHasResponded` lie is the stranding mechanism here; Phase 2's `reply_to` fallback closes this side of it |
| [MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) | Same family — a seeded worker never woken; not merged, a delivery defect rather than a count defect |
| [MUX-188](./MUX-188-spawn-worker-launch-sends-edit-a-stray-startup-request.md) | Same family — a side effect of each launch; fewer launches means fewer stray requests, but it is its own fix |
| [MUX-128](./MUX-128-fkey-navigation-for-spawn-windows.md) | Fewer worker windows is what makes its F11/F12 question tractable |
| [MUX-147](./MUX-147-process-leak-and-memory-footprint.md), [MUX-184](./MUX-184-orphaned-session-processes-never-reaped.md) | Same footprint, different roads |
| [MUX-178](../completed/MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md) | Why a worker ends unanswered; graph workers run in the session checkout, so adoption does not cross trees |

## Out of scope

- Worktree isolation for spawn nodes — [MUX-178](../completed/MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md). Graph workers run in the session checkout today, so reuse does not cross trees within a session; if worktrees return, an adopted worker must advance or re-cut its tree on adoption (`advanceSpawnWorktree` already runs on reseed).
- Cross-session reuse.
- The wake and delivery defects of [MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md).

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-195-graph-runs-never-reuse-idle-workers | 6m | 2026-10-05 22:22 |

## Status

Backlog

Filed 2026-09-28 on the user's instruction relayed by edit; mechanism verified the same day against
the live session (three worker windows, two stranded on a complete run). **Broadened 2026-10-05** by the
user directly — one worker per graph run, one per spawning agent — with the agent-spawn and
replace/resume roads added, re-verified at `b0f6ead` against the 10-05 lifecycle log, and raised to
**Critical / Tier 0 / rank 1** on the user's call.
