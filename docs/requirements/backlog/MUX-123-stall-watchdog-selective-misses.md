# Stall Watchdog Fires Routinely — and Still Missed Three Live Stalls

`checkStalledTasks` is **not inert**. It fired **26 times on 2026-08-28** alone. Yet three live
stalls that day — two `pr-read` tasks and one spawn task, each idle for 4–8 minutes — were resolved
only by a human running `muxcode deliver --force`.

The question is therefore not *"why does the watchdog never fire"* but **"why does a working
watchdog selectively miss?"** — a narrower and far more tractable investigation.

Tracking: _(no GitHub issue yet)_

## Context

### The premise this spec was almost written on

This spec was first requested as *"root-cause the inert watchdog — zero `task-stall-redrive`
lifecycle rows this session."* That is measurably false, and filing it would have sent someone
hunting a bug that does not exist.

Parsed from the raw log by timestamp:

| Time | Redrive |
|------|---------|
| 16:22:55 | `edit→commit:commit` redrive 1/2 |
| 16:28:52 | `daemon→edit:edit` redrive 1/2 |
| 16:33:35 | `edit→plan:update-docs` redrive 1/2 |
| 16:34:35 | `edit→plan:update-docs` redrive 2/2 |
| 16:36:07 | `test→edit:edit` redrive 1/2 |

**26 task-stall rows that day.** The "zero rows" reading came from `muxcode lifecycle show`, whose
default limit truncates any query to the most recent 50 entries — see
[MUX-124](./MUX-124-lifecycle-since-truncated-by-limit.md), filed alongside this. **The investigation
tool produced the false premise**, which is why that bug is worth fixing before anyone debugs
timing-sensitive daemon behaviour again.

### The gating chain, and where a miss can hide

`checkStalledTasks` (`daemon/daemon.go`) drops a candidate at any of these:

| # | Gate | Miss mode |
|---|------|-----------|
| 1 | `TaskStallDisabled()` | env opt-out |
| 2 | `now - lastStallCheck < 30` | 30 s throttle |
| 3 | `!TaskStalled(t, now, TaskStallSecs())` | not yet past threshold |
| 4 | `IsHarnessActive \|\| IsReloading` | skipped while the role is busy/reloading |
| 5 | `!PaneHasIdlePrompt && !HasPendingInput` | **pane not recognised as idle** |
| 6 | `taskStallSeen[t.ID] < 2` | **two-sighting debounce — needs ≥2 passes 30 s apart** |
| 7 | `taskRedrives[t.ID] >= 2` | gave up; task timeout owns it |

Gates 5 and 6 are the first places to look, and gate 5 has a known shape: the pane is resolved with
`bus.PaneTarget(d.session, role)`. For a **spawn task** the role is a dynamic `spawn-<8hex>`, and
[MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) already established that spawn roles
are invisible to the other daemon loops because they iterate the static `bus.KnownRoles`. Whether
`PaneTarget` and `PaneHasIdlePrompt` behave correctly for a spawn window is an open question and the
single most promising lead — one of the three missed stalls was a spawn task.

Gate 6 compounds it: two sightings at a 30 s throttle means **no redrive before ~60–90 s**, and any
transient failure of gate 4 or 5 resets nothing but delays everything.

### The inverse failure: redriving a demonstrably live worker

The spec above is framed entirely around **false negatives** — stalls the watchdog missed. A
2026-08-31 incident on run `1788195259-req-code-pr-3fbcc7da` shows the **false positive** direction,
and it is the same root confusion seen from the other side.

The `implement` node's spawn worker was redriven as stalled while it was **actively working and
answering on the bus**. The redrive escalated to its cap:

```
13:44:27  spawn-wake           spawn-3017de3d
13:44:27  graph-stall-redrive  ...: implement re-woke 1 stalled spawn worker(s) — redrive 3/3
```

The worker was not stalled. Minutes later it completed a test task (`13:47:31 task-detected test task
test from spawn-3017de3d: succeeded`) and harvested `bus/pane.go` and `bus/pane_test.go` into the
repo. What it had *not* done was consume its seed message — so no receipt existed.

**The detector treats receipt-absence as stall, and ignores contrary liveness evidence.** An agent
that answers messages, completes tasks, and writes files is demonstrably alive; "has not consumed the
seed" is one signal among several, and it is currently the only one that counts. `hasReceipt()` is
the right definition of *received*, but it is being used as a proxy for *alive*, which it is not.

Two things worth recording precisely, because the incident was initially understood differently:

- The redrive **reached 3/3**; it was not defused below the cap. An operator instruction to the
  worker to drain its inbox was issued around the same time, but the log shows the cap was hit.
- **Reaching 3/3 did not fail the node.** `implement` remained `running` and the worker stayed
  productive. So either the cap does not fail a node the way it is assumed to, or the failure is
  deferred — worth pinning, since the assumed consequence drove the operator response.

Any fix that makes the watchdog fire *more* readily to close the misses above must not widen this
direction. A liveness signal — recent bus activity, task completion, or file writes attributable to
the role — belongs in the gating chain alongside the receipt check.

### Why "move it into the executor" is a design option, not the requirement

The original request proposed moving stall resolution into `harvestRunningNode`, which sees every
running node per tick. That is a reasonable design — but it should be chosen *after* the misses are
explained, not instead of explaining them. Relocating a watchdog that already fires 26 times a day,
without knowing why it missed three specific cases, risks reproducing the same blind spot in a new
location.

## Requirements

### Acceptance criteria

- [ ] Each of the three observed misses is explained by a **named gate** in the chain above, with
      evidence — not attributed to a general theory
- [ ] Whether `PaneTarget` / `PaneHasIdlePrompt` resolve correctly for a dynamic `spawn-<hex>` role
      is answered definitively, yes or no
- [ ] Worst-case time-to-redrive is stated as a number, given the 30 s throttle and two-sighting
      debounce
- [ ] The fix addresses the *identified* gate; if stall resolution moves into the graph executor,
      the spec records why that placement fixes the observed misses rather than relocating them
- [ ] A hermetic regression test stages a **consumed-but-unstarted task with a verifiably idle pane**
      and asserts the redrive fires
- [ ] **Negative control**: a task whose agent is genuinely working is **not** redriven — a watchdog
      that always fires is as wrong as one that never does
- [ ] The spawn-role case is covered by a test, not only the static-role case
- [ ] `go vet ./...` and `go test ./...` green in both modules

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | `checkStalledTasks`, `taskStallSeen`, `taskRedrives` |
| `tools/muxcode/bus/task.go` | `TaskStalled`, `TaskStallSecs`, `TaskExpired` |
| `tools/muxcode/bus/deliver.go` | `ForceDeliver` — the redrive itself |
| `tools/muxcode/bus/config.go` | `PaneTarget`, `KnownRoles`, `IsSpawnRole` |
| `tools/muxcode/bus/stuck.go` | `PaneHasIdlePrompt` and friends |
| `tools/muxcode/bus/graph_exec.go` | `harvestRunningNode` — the proposed alternative home |

## Implementation

### Phase 1: Explain the misses

- [ ] Reconstruct the three episodes from the raw log (`~/.config/muxcode/logs/<session>.log`,
      parsed by `ts` — **not** via `lifecycle show`, until MUX-124 lands)
- [ ] For each, identify which gate dropped it
- [ ] Answer the spawn-role pane question definitively
- [ ] Record the worst-case time-to-redrive implied by throttle × debounce

### Phase 2: Fix the identified gate

- [ ] Implement the fix the evidence points at
- [ ] If relocating into the executor, state why that placement fixes these misses
- [ ] Unit tests including the spawn-role case

### Phase 3: Integration test

- [ ] Create `scripts/test-stall-redrive.sh` (hermetic: scratch bus + tmux + daemon)
- [ ] Stage a consumed-but-unstarted task with a verifiably idle pane; assert the redrive fires
- [ ] Stage the same for a **spawn role**
- [ ] **Negative control**: a genuinely-working agent is not redriven
- [ ] **Negative control**: assert the pre-fix path fails, so the suite cannot pass vacuously
- [ ] Coverage floor itemized against the actual check count
- [ ] Run the script and verify all checks pass

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Fixing a theory instead of the observed misses | The original framing was already wrong once | Phase 1 must name a gate per episode |
| Relocating the blind spot | A move that does not address gate 5/6 reproduces the miss elsewhere | Explicit criterion tying placement to cause |
| Debugging with `lifecycle show` | It produced the false premise that started this | MUX-124, and parse the raw log meanwhile |
| An always-firing watchdog | Redriving working agents is its own failure | Negative control |

## Status

Backlog
