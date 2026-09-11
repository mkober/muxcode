# A Graph Spawn Node Cuts No Worktree and Ports Nothing — MUX-131 Has Regressed

**Tracking:** filed 2026-09-11 by plan on the user's request relayed by edit (`1789133110`), from the
MUX-167 Phase 4 runs. Verified against the run log before filing.

A graph `spawn` node launches its worker and then completes in **two seconds** with
`"output":"nothing to port","outcome":"success"` — having cut **no worktree at all**. `git worktree
list` in the scratch fixture repo shows only the checkout. The worker's output is therefore stranded:
nothing reaches the build, nothing is committed, and the node reports **success** while doing so.

[MUX-131](../completed/MUX-131-spawn-implement-output-never-ported.md) recorded this same script
**green at 64 passed / 0 failed, exit 0** (floor 63) on 2026-09-01. This is its defect class returning.

## Context

### Observed (2026-09-11 09:17–09:23, session `muxcode`)

From `scripts/test-multi-phase-graph.sh`, run four times deterministically; log
`/tmp/mux167-multiphase.log`, work dir kept at `/tmp/multiphase-work-78683`.

| Fact | Value |
|------|-------|
| Overall | **exit 1 — 39 passed / 19 failed** |
| Failures in MUX-167's phase-check sections | **0 of 19** (all five assertions pass, both controls) |
| Failures in the MUX-131 spawn/worktree sections | **19 of 19** |
| MUX-131's own recorded baseline | 64 passed / 0 failed, exit 0, floor 63 (2026-09-01) |

The node record, quoted verbatim from the log:

```json
{"node_id":"implement","state":"done","outcome":"success","output":"nothing to port",
 "task_id":"spawn-0b1af6e5","routed":true,
 "started_at":1789132779,"done_at":1789132781,"updated_at":1789132781}
```

Two seconds start to finish, `outcome: success`, nothing ported.

Representative failures, in the order the script hits them:

- `spawn worker or worktree never appeared for 1789132777-multiphase-ccb71dc3`
- `implement did not record a port` — the record above
- `ported file missing from checkout at build time — spawn output stranded`
- `worktree copy discarded while the port is uncommitted`
- `phase-1 commit did not land`
- `implement did not fail on the refused port: done` — the conflict control
- `no fresh worker after the kill: count=1` / `spawn store shows 1 workers — replacement did not happen`

### Mechanism — where it is, and where it is not

**Launch is fine.** The log carries `Spawn completed: …-spawn-0b1af6e5 (role: edit, window:
spawn-0b1af6e5)`, and `muxcode spawn` shows the worker. So this is **not** the launch path.

**The worktree is never cut.** `git worktree list` in the fixture repo shows only the checkout, so
the harvest/port path has nothing to read and correctly reports "nothing to port" — the report is
honest about an earlier failure that produced no signal of its own.

**The damage is that it reports `success`.** A node that ported nothing completes green, so the run
advances: build dispatches on a checkout that never received the work (`build dispatched despite
stranded output`), and the phase commit does not land. This is the same self-concealing shape as
[MUX-148](./MUX-148-node-outcome-reads-command-ran-as-task-done.md) and
[MUX-176](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) — a node claiming a success it
did not earn — and it is why this is filed as a defect rather than a test-fixture issue.

### One fixture cause already found and fixed — and it was *not* the blocker

Worth recording so it is not re-investigated. The fixture originally pointed the spawn role's CLI at
an **absent binary**, so `agent launch` died and left the worker pane at a bare shell;
`captureInjectionTarget` (shipped with [MUX-164](../drafts/MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md)
on this branch, *after* MUX-131 closed) then correctly refused every worker seed — three
`spawn-XXXX: pane ends at a shell prompt: ->` rows. Replaced with an idle-agent stub presenting the
`❯` the guard requires. **The refusals are gone and the change should be kept, but the failure count
did not move** — so the injection guard is not the cause, and its refusal was correct behaviour
throughout.

### Unresolved: the coverage floor reads 57 against a constant of 55

`coverage floor mismatch — 57 checks executed, want exactly 55`. The floor comment's own breakdown
sums to 55, so the constant is not obviously wrong, and the extra two checks are not obviously
spurious. **This cannot be reconciled until the spawn sections pass**, because the executed count
while 19 checks are failing is not the count a green run would produce. Carried here rather than
guessed at.

### Scope boundary

In scope: why a spawn node cuts no worktree, and why a node that ported nothing reports `success`.
Not in scope: MUX-167's phase-check routing (verified green in the same runs), and the injection
guard's refusal behaviour (correct, and already worked around in the fixture).

## Requirements

### Acceptance criteria

- [ ] A graph `spawn` node cuts a worktree, and its absence is an error rather than a silent no-op
- [ ] A node that ports nothing **never reports `outcome: success`** — the run stops instead of advancing on stranded output
- [ ] `scripts/test-multi-phase-graph.sh` returns to green, and the coverage floor is reconciled against the count a green run actually produces
- [ ] **Negative control:** a spawn node that genuinely has nothing to port (an iteration that changed no files) still completes successfully — so the fix does not turn every empty port into a failure
- [ ] A lifecycle row records a spawn that produced no worktree, so the silent case becomes visible
- [ ] The regression is pinned by a test that fails against today's tree

### Technical approach

Find the point between "spawn launched" and "harvest read the worktree" where the worktree stops
being created, and establish whether it was never created or created and discarded — the script sees
both shapes (`worktree copy discarded while the port is uncommitted`, `worktree copy lost after the
refused port`), which may be one cause or two.

The load-bearing distinction is the fourth criterion: **"nothing to port" is a legitimate outcome**
for an iteration that changed no files, so the fix cannot simply treat an empty port as failure. What
must change is that an empty port arising from *a missing worktree* is an error, while an empty port
from *a worktree with no changes* stays a success. Those two are indistinguishable in today's record,
which is the heart of the defect.

Bisecting against MUX-131's 2026-09-01 green run is the cheapest way in — the script and its floor
are unchanged since, so the tree moved underneath it.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/spawn.go` | worker launch, worktree creation, the port/harvest path |
| `tools/muxcode/bus/graph_exec.go` | how a spawn node's outcome is decided and recorded |
| `scripts/test-multi-phase-graph.sh` | the 19 failing assertions and the coverage floor |
| [`MUX-131`](../completed/MUX-131-spawn-implement-output-never-ported.md) | the original defect, its fix, and the 64/0 baseline this regressed from |

## Implementation

### Phase 1: Locate the regression

- [ ] Establish whether the worktree is never created or created and discarded — the script observes both shapes
- [ ] Bisect against MUX-131's 2026-09-01 green run (the script is unchanged since)
- [ ] Record the discriminating evidence here before changing anything

### Phase 2: Fix

- [ ] A spawn node cuts its worktree, and a missing worktree is an error
- [ ] An empty port caused by a missing worktree fails; an empty port from a clean worktree still succeeds
- [ ] Lifecycle row for a spawn that produced no worktree

### Phase 3: Tests

- [ ] Unit test pinning the regression — fails against today's tree
- [ ] **Negative control:** a genuinely empty iteration still reports success
- [ ] A stranded port never dispatches the downstream build

### Phase 4: Integration test

- [ ] `scripts/test-multi-phase-graph.sh` green end to end
- [ ] Coverage floor reconciled against the count a green run produces (currently reads 57 vs a constant of 55)
- [ ] Run through the run agent (**foreground**, per [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)) and record the counts here

## Notes

**Found while closing something else.** These 19 failures surfaced during MUX-167's Phase 4 runs and
were initially read as MUX-167's own red. Separating them mattered: MUX-167's five phase-check
assertions pass in the same runs with both controls, so the two results had to be attributed
independently rather than the whole script being called a failure.

**Related:** [MUX-131](../completed/MUX-131-spawn-implement-output-never-ported.md) (the original, and
its green baseline); [MUX-148](./MUX-148-node-outcome-reads-command-ran-as-task-done.md) and
[MUX-176](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) (the same self-concealing shape —
a node reporting a success it did not earn);
[MUX-142](./MUX-142-spawn-worker-delegates-into-wrong-tree.md) (the other live spawn-tree defect).

## Status

Backlog
