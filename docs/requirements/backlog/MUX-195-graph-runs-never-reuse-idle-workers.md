# MUX-195: Graph Runs Never Reuse an Idle Worker

**Tracking:** [mkober/muxcode#97](https://github.com/mkober/muxcode/issues/97)

A graph `spawn`/`map` node reuses a worker only when one exists for **the same run and the same node**.
Every other dispatch — the next run, or a different node in the same run — pays a full cold start
(launch, agent definition, context load) and opens another window. On top of that, the workers of a
finished run are not reliably reaped, so they accumulate: on 2026-09-28 the status bar showed three
worker windows at once, two of them belonging to a run that had completed hours earlier.

The user's words: *"if a worker has already been spawned any graph run should be able to use it to
save on context and speed."*

## Context

### Source and standard of evidence

Filed 2026-09-28 on the user's instruction relayed by edit (brief:
`worker-reuse-defect.md` in edit's scratchpad). Everything under **Mechanism** was read by plan from
code at `5ac2495` (+ the uncommitted close-spec change) and from the live session's `spawn.jsonl`,
delivery store and `log.jsonl`; the brief's framing ("a worker persists within its own run") is
**narrowed** by what the code shows — reuse is per run **and node**.

### Observed

| Window | Spawn role | Run | Node | Run state |
|--------|-----------|-----|------|-----------|
| 11 | `spawn-d3d56f12` | `1790630495-50-spec-to-pr-6d13cbff` | `implement` | complete |
| 12 | `spawn-b25552f6` | `1790630495-50-spec-to-pr-6d13cbff` | `fix` | complete |
| 13 | `spawn-db7fd90d` | `1790641162-80-pr-review-fix-451d8466` | `fix` | running |

All three are `running` in the registry. The first two answered their seeds — responses
`1790630719-spawn-d3d56f12-702c2ee1` and `1790630941-spawn-b25552f6-8c4b2118` carry the seed ids as
`reply_to` — yet **neither seed (`1790630496-daemon-d8eba32c`, `1790630838-daemon-e161c0b5`) has a
delivery record**. The earlier run `1790629144` (failed at Phase 2) had its own worker as well.

### Mechanism — verified

| Fact | Where |
|------|-------|
| Reuse looks up a live worker keyed on `RunID` **and** `NodeID`; anything else falls through to a fresh `graphSpawnFn` | `acquireSpawnWorker`, `bus/graph_exec.go:78`; `FindLiveSpawn`, `bus/spawn.go:381` |
| So one `50-spec-to-pr` run holds **two** workers (`implement`, `fix`), and every new run builds its own | same |
| A responded worker is held only while its run is running (`spawnPersistent`); once the run is terminal, `RefreshSpawnStatus` kills its window — **if** `spawnHasResponded` reads true | `bus/spawn.go:447`, `:520–560` |
| `spawnHasResponded` reads the seed's delivery record; a missing record reads **false**, so the worker is neither held nor reaped — stranded running, window open, forever | `bus/spawn.go:462` |
| Workers are never cut into a worktree: graph workers run in the session checkout, the tree build/test/review/commit use | `acquireSpawnWorker` doc comment, `bus/graph_exec.go:62–77` |
| Ownership is read from the registry's `RunID` by `spawnRunOwner` — first match by spawn role | `bus/graph_authority.go:43` |
| `CheckGraphNodeAuthority`, `CheckCancelAuthority` / `spawn stop`, `replaceLostWorkers` and `GraphOwnsTask` all key on that mapping | `graph_authority.go:17`, `cancel_authority.go:35`, `graph_exec.go:1820`, `:1578` |

Two defects, one symptom:

1. **No cross-run or cross-node reuse** — the reuse key is too narrow for what the user wants.
2. **Terminal runs' workers are stranded** when the seed's delivery record is gone — the
   [MUX-135](./MUX-135-spawn-seed-record-gc-strands-completion.md) mechanism (delivery-record GC
   strands a spawn) reaching the reaper, not only an in-flight iteration.

### Blast radius

- Every graph run with a `spawn`/`map` node: `50-spec-to-pr`, `60-integration-suite`,
  `80-pr-review-fix`, `90-ci-fix`. Each run pays one cold start per spawn node; a spec walked across
  several runs pays it per run.
- Stranded windows accumulate for the life of the session, each a live Claude process — the
  footprint concern of [MUX-147](./MUX-147-process-leak-and-memory-footprint.md) and
  [MUX-184](./MUX-184-orphaned-session-processes-never-reaped.md), from a different road.
- F-key reach: spawn windows sit at index 11+, so only the first two are F-key reachable
  ([MUX-128](./MUX-128-fkey-navigation-for-spawn-windows.md)); a pool that grows makes that worse.

## Requirements

### Acceptance criteria

- [ ] A `spawn`/`map` dispatch adopts an **idle** worker of the same base role in the same session before spawning a fresh one; it spawns only when none is free — test: two sequential runs of the same template use one worker (spawn count 1)
- [ ] Within one run, `implement` and `fix` share a worker when neither is mid-task (reuse is no longer keyed on node) — or the spec records why they must not
- [ ] **Negative control:** a busy worker (current seed unanswered), or one whose owning run has an in-flight node on it, is never adopted — a fresh worker is spawned instead
- [ ] Adoption re-points ownership (`RunID`/`NodeID`) atomically under a registry lock: there is no instant at which the worker belongs to two runs or to none, and `CheckGraphNodeAuthority`, `CheckCancelAuthority`, `replaceLostWorkers` and `GraphOwnsTask` read the new owner immediately
- [ ] Two runs dispatching at once cannot adopt the same worker — test: concurrent dispatch yields two distinct workers, or one adoption and one spawn
- [ ] An adopted worker's context policy is explicit ([Decision 1](#decision-1--keep-or-clear-context)), and a stale task from the previous run cannot reach the new one: the seed's ownership preamble (`graphWorkerTask`) names the new run and node
- [ ] Idle workers are bounded: a pool cap and a quiet-window reap ([Decision 2](#decision-2--pool-cap-and-idle-reap)); adoption and reap each write a lifecycle row naming worker, old owner and new owner
- [ ] A terminal run's worker is reaped (or returned to the pool) **even when its seed's delivery record is missing** — the stranding observed 2026-09-28 cannot recur
- [ ] `muxcode spawn list`/`status` show a pooled worker distinctly from `parked` (held by a live run) and from `running`
- [ ] `bash scripts/test-graph-worker-reuse.sh` passes

### Technical approach

Replace the `(runID, nodeID)` lookup in `acquireSpawnWorker` with a pool lookup: a registry entry is
**free** when its window exists, its current seed is answered, and its owning run is terminal or does
not hold it for an in-flight node. Adoption rewrites `RunID`/`NodeID`/`SeedMsgID` in one
`UpdateSpawnEntry` under the registry lock before `ReseedSpawn` sends, so ownership and the seed move
together. Free-ness must not depend on the delivery record alone: fall back to the seed's
`reply_to` in `log.jsonl` (the evidence that proved the stranding here), which also fixes the reaper.

### Key files

| File | Role |
|------|------|
| `tools/muxcode/bus/graph_exec.go:78` | `acquireSpawnWorker` — the reuse key |
| `tools/muxcode/bus/spawn.go:381`, `:417`, `:447`, `:462`, `:520` | `FindLiveSpawn`, `ReseedSpawn`, `spawnPersistent`, `spawnHasResponded`, `RefreshSpawnStatus` |
| `tools/muxcode/bus/graph_authority.go:43` | `spawnRunOwner` — ownership read |
| `tools/muxcode/bus/cancel_authority.go`, `graph_exec.go:1578`, `:1820` | cancel authority, `GraphOwnsTask`, `replaceLostWorkers` |
| `scripts/test-graph-worker-reuse.sh` | new |

## Implementation

### Phase 1: Pin

- [ ] Unit test: two sequential runs → two workers today (pin red, inverted in Phase 3)
- [ ] Unit test: a terminal run's worker whose seed has no delivery record stays `running` after `RefreshSpawnStatus` (pin red)
- [ ] Unit test (negative control, stays green): a busy worker is never reused

### Phase 2: Reaper and free-ness

- [ ] `spawnHasResponded` falls back to a `reply_to` match in the session log when the delivery record is missing
- [ ] Terminal-run workers are reaped or pooled per Decision 2; lifecycle row on each

### Phase 3: Adoption

- [ ] Pool lookup in `acquireSpawnWorker` (same base role, same session, free); fresh spawn only when none is free
- [ ] Atomic ownership re-point under the registry lock, before the reseed
- [ ] Context policy per Decision 1; `graphWorkerTask` re-states the new owner
- [ ] `spawn list`/`status` pooled display; invert the Phase 1 pins

### Phase 4: Integration test

- [ ] Create `scripts/test-graph-worker-reuse.sh` — hermetic scratch daemon, stub workers
- [ ] Test: two sequential runs reuse one worker (spawn count 1), lifecycle shows the adoption with old and new owner
- [ ] **Negative control:** a busy worker is not adopted — a second worker is spawned
- [ ] Test: after adoption, `spawn stop` authority and `CheckGraphNodeAuthority` follow the new run, not the old
- [ ] Test: a finished run's worker with its delivery record removed is still reaped or pooled
- [ ] Coverage floor; run and record counts here

## Open decisions

### Decision 1 — keep or clear context

**Keep** the conversation (the saving the user asked for — the next run starts warm on the same
codebase) vs **`/clear` between runs** (no carry-over of the previous run's assumptions, but most of
the context saving is lost; the boot cost is still saved). A middle road: keep within a spec's runs,
clear when the active spec changes.

### Decision 2 — pool cap and idle reap

How many idle workers a session may hold (1 per base role is the smallest useful pool), and how long
an idle worker lives before it is reaped (a quiet window like `MUXCODE_AUTO_CLEAR_QUIET_SECS`).

## Out of scope

- Worktree isolation for spawn nodes — [MUX-178](./MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md). Graph workers run in the session checkout today, so reuse does not cross trees within a session; if MUX-178 reintroduces worktrees, a pooled worker must advance or re-cut its tree on adoption (`advanceSpawnWorktree` already runs on reseed).
- Cross-session reuse.

## Status

Backlog

Filed 2026-09-28 on the user's instruction relayed by edit; mechanism verified the same day against
the live session (three worker windows, two stranded on a complete run).
