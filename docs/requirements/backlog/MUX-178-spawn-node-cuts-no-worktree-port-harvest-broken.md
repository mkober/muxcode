# A Graph Spawn Node Credits an Unanswered Seed as Success, and the Multi-Phase Fixture Still Asserts the Retired Worktree Model

**Tracking:** [mkober/muxcode#136](https://github.com/mkober/muxcode/issues/136)

**Provenance:** filed 2026-09-11 by plan on the user's request relayed by edit (`1789133110`), from the
MUX-167 Phase 4 runs, as "a graph spawn node cuts no worktree and ports nothing — MUX-131 has
regressed". **Reframed 2026-10-03** on the user's decision (relayed by edit, `1791036146`) after
Phase 1 found that the missing worktree is the 2026-09-03 design, not a regression: worktrees stay
retired, and the defect is the two things that remain true.

A graph `spawn` node completes in **two seconds** with `"output":"nothing to port","outcome":"success"`
for a worker that never answered its seed. `spawnGroupOutcome` (`tools/muxcode/bus/graph_exec.go:1652`)
credits a worker whose `Status` is `completed` as success whenever it has no answered seed to judge —
the process ended, so the work must be done. The run advances: build dispatches on a checkout that
received no work, the phase commit does not land, and the node record says the opposite.

Alongside it, `scripts/test-multi-phase-graph.sh` still asserts the MUX-131 worktree model — an
isolated worktree per worker, a durability copy, a worktree advance at reseed, a conflict-control
worktree — against a tree that deliberately removed worktrees on 2026-09-03. Its 19 failures are
stale assertions, not a code regression, and its coverage floor cannot be reconciled until they are
rewritten to the session-checkout model.

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

### Mechanism — the unearned success

**Launch is fine.** The log carries `Spawn completed: …-spawn-0b1af6e5 (role: edit, window:
spawn-0b1af6e5)`, and `muxcode spawn` shows the worker.

**No worktree, by design.** `graphSpawnFn` calls `StartSpawnOwned(…, false, …)` — `useWorktree=false`
— and the doc comment above `acquireSpawnWorker` (`graph_exec.go:70-78`) records why: a `fix`
worktree cut from `HEAD` could not see the previous node's uncommitted output, so graph workers run
in the session checkout, the one tree build, test, review and commit already use
([MUX-142](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) § "The third tree").
`portSpawnGroup` (`bus/graph_port.go:215-237`) skips every member with `Worktree == ""` and returns
"nothing to port" — inert by construction.

**The defect is what `spawnGroupOutcome` does with a worker it cannot judge.** The function reads
each worker in the node's group (`graph_exec.go:1628-1659`):

| Worker state | Outcome today | Why that is wrong |
|--------------|---------------|-------------------|
| Seed answered (`SeedMsgID != ""` and `spawnHasResponded`) | the reply's verdict token (`spawnWorkerVerdict`); no token → unknown, holds | correct — this is the 2026-09-14 fix for a decline reading as work |
| Seed **not** answered, `Status == "running"` | hold (`return "", false`) | correct |
| Seed **not** answered, `Status == "completed"` | **success** (line 1652, "success — no change") | the worker's process ended without ever reporting; nothing says the work was done. This is the two-second record above |
| Seed not answered, any other status | failure | correct |

A `completed` worker with no answered seed is the worker-never-worked shape of
[MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) and
[MUX-195](./MUX-195-graph-runs-never-reuse-idle-workers.md): the seed was never consumed, or the
worker exited at its prompt. The node has **no evidence** either way, and absence of evidence is
`unknown`, the outcome that holds for a human — the same rule the answered branch already applies to
a reply without a verdict token. Crediting it as success is the self-concealing shape of
[MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) and
[MUX-176](./MUX-176-run-chain-fires-success-on-backgrounded-call.md): a node claiming a success it
did not earn.

### Mechanism — the stale fixture

The script was last touched by `133a4c8` / `970d8fc` / `b64774f` (MUX-182, 2026-09-24), none of
which revisited its MUX-131 sections. Four blocks still assert the worktree model:

| Script lines | Asserts | Under the no-worktree design |
|--------------|---------|------------------------------|
| 548-551 | `spawn_for_run && [ -n "$SPAWN_WT" ]` — "worker created with an isolated worktree" | `SPAWN_WT` is always empty; the worker exists in the session checkout |
| 561-630 | phase-1 output written into `$SPAWN_WT`, ported to the checkout, durability copy kept, worktree advanced to the shipped tip at reseed | the worker writes to the checkout directly; there is no port, no copy, no advance |
| 668-715 | conflict-control worker "created with a worktree", conflicting edit made in `$SPAWN_WT`, port refused | the conflict shape needs restating in checkout terms, or retiring with the floor lowered |
| 772-787 | coverage floor `== 55` | reads 57 while 19 checks fail; reconcilable only once the sections above pass |

Every one of these is a check whose premise the tree no longer holds. They must be rewritten to
assert what the design actually promises — the worker's output lands in the session checkout and the
phase commit ships it — or removed with the floor lowered, so the script stops reporting a
regression that is not one.

### One fixture cause already found and fixed — and it was *not* the blocker

Worth recording so it is not re-investigated. The fixture originally pointed the spawn role's CLI at
an **absent binary**, so `agent launch` died and left the worker pane at a bare shell;
`captureInjectionTarget` (shipped with [MUX-164](../completed/MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md)
on this branch, *after* MUX-131 closed) then correctly refused every worker seed — three
`spawn-XXXX: pane ends at a shell prompt: ->` rows. Replaced with an idle-agent stub presenting the
`❯` the guard requires. **The refusals are gone and the change should be kept, but the failure count
did not move** — so the injection guard is not the cause, and its refusal was correct behaviour
throughout.

### Phase 1 findings (2026-10-03)

Recorded by the `implement` worker of graph run `1791034896-50-spec-to-pr` before any code change.
These are what reframed the spec.

**Never created, not created-and-discarded — one cause, not two.**

| Evidence | Location |
|----------|----------|
| `graphSpawnFn` calls `StartSpawnOwned(session, role, task, owner, false, runID, nodeID)` — `useWorktree=false`. No graph spawn worker is ever given a worktree; `SpawnEntry.Worktree` is `""` | `tools/muxcode/bus/graph_exec.go:53-61` |
| `portSpawnGroup` skips every member with `Worktree == ""` and returns "nothing to port" — the harvest is inert **by construction**, not broken | `tools/muxcode/bus/graph_port.go:215-237` |
| Both script shapes (`worktree copy discarded while the port is uncommitted`, `worktree copy lost after the refused port`) are downstream of the same cause: `SPAWN_WT` extracts empty because no worktree field is written | `scripts/test-multi-phase-graph.sh` |

**Bisect.**

| Fact | Value |
|------|-------|
| MUX-131 green baseline | 2026-09-01, 64/0, floor 63 |
| The `false` argument and the "Graph workers are never isolated in their own worktree" doc comment (`graph_exec.go:70-78`) were introduced together | `fafec97` "MUX-136 Attribute graph run/gate actions, fix worker worktree isolation", 2026-09-03 01:45:17 -0400 (`git log -S`, via commit agent) — the user's 2026-09-03 decision recorded in MUX-142 § "The third tree" |
| The script was **not** unchanged since MUX-131 | Last touched by `133a4c8` / `970d8fc` / `b64774f` (MUX-182, 2026-09-24) — all after the 2026-09-11 observation, none updating the MUX-131 worktree sections |

**Discriminating conclusion.** The 19 failures are the script asserting the retired MUX-131 worktree
model against a tree that deliberately removed it — a stale-fixture problem, not a code regression in
the port path. What remains a genuine defect is the two-second `outcome: success` from
`spawnGroupOutcome` for a `completed` worker with no answered seed. **User's decision (2026-10-03):**
keep the no-worktree design; reframe this spec around the unearned success and the stale fixture.

### Scope boundary

In scope: the outcome a spawn node records for a worker it has no reply from, and bringing
`scripts/test-multi-phase-graph.sh` to the no-worktree design. Not in scope: restoring worktrees
(decided against, 2026-09-03 and again 2026-10-03); *why* a worker ends without answering
([MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) /
[MUX-195](./MUX-195-graph-runs-never-reuse-idle-workers.md) — this spec makes the node honest about
it, those make it not happen); MUX-167's phase-check routing (verified green in the same runs); and
the injection guard's refusal behaviour (correct, already worked around in the fixture).

## Requirements

### Acceptance criteria

- [ ] A `completed` spawn worker with **no answered seed** resolves the node `unknown` — the run holds for a human — never `success`
- [ ] The node's output for that case says why it is holding (worker ended without answering its seed), and a lifecycle row records it, so the silent case becomes visible
- [ ] **Negative control:** a worker whose seed was answered with a success verdict still resolves `success`; a `running` worker still holds; a `stopped` worker still fails — the precedence `failure > unknown > success` is unchanged
- [ ] `scripts/test-multi-phase-graph.sh` asserts the no-worktree design: the worker's output lands in the session checkout, the phase commit ships it, and no check reads `SPAWN_WT`
- [ ] The script returns to green and its coverage floor equals the count a green run actually produces
- [ ] The outcome change is pinned by a unit test that fails against today's `graph_exec.go`
- [x] ~~A graph `spawn` node cuts a worktree, and its absence is an error~~ — **dropped 2026-10-03**: contradicts the 2026-09-03 no-worktree design, which the user chose to keep

### Technical approach

**Outcome.** In `spawnGroupOutcome`, the `case "completed":` branch for an unanswered seed becomes
`outcome = worseOutcome(outcome, OutcomeUnknown)` with a detail naming the worker and the fact
(seed `SeedMsgID` never answered — or no seed recorded at all, which is the same absence of
evidence). The existing precedence already makes `unknown` hold the group unless another member
failed, so the human sees a hold, not a green node. Emit a lifecycle row (`spawn-unanswered-seed` or
the existing vocabulary's nearest event) so `muxcode lifecycle show` surfaces the case the record
used to hide. The answered branch and the `running`/default branches are untouched.

**Fixture.** Rewrite the four script blocks in checkout terms. The worker stub writes
`impl-phase1.txt` into `$REPO` (the session checkout) instead of `$SPAWN_WT`; "durability" becomes
"the file is in the checkout, uncommitted, at build time"; "worktree advance at reseed" has no
equivalent and is removed; the conflict control restates as "a worker whose checkout edit conflicts
with the fixture's own change" or is retired. Then recount: the floor is **equality**, so it must be
set from a green run's executed count, not guessed — the current 57-vs-55 mismatch was measured with
19 checks failing and says nothing about the green count.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | `spawnGroupOutcome` (:1628-1659) — the `completed`-without-answer branch at :1652 |
| `tools/muxcode/bus/graph_port.go` | `portSpawnGroup` — inert under the design; its "nothing to port" output should name the reason |
| `tools/muxcode/bus/spawn.go` | `SpawnEntry`, `spawnHasResponded`, seed bookkeeping |
| `scripts/test-multi-phase-graph.sh` | the four stale worktree blocks and the coverage floor |
| [`MUX-131`](../completed/MUX-131-spawn-implement-output-never-ported.md) | the original worktree-era defect whose fixture this script still carries |
| [`MUX-142`](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) | § "The third tree" — the 2026-09-03 decision this spec now builds on |

## Implementation

### Phase 1: Locate the regression

- [x] Establish whether the worktree is never created or created and discarded — the script observes both shapes (never created: `useWorktree=false` by design since `fafec97`; see Phase 1 findings)
- [x] Bisect against MUX-131's 2026-09-01 green run (the script is unchanged since) (script was *not* unchanged — last touched by MUX-182 on 2026-09-24; see Phase 1 findings)
- [x] Record the discriminating evidence here before changing anything

### Phase 2: An unanswered seed is unknown, not success

- [ ] `spawnGroupOutcome`: a `completed` worker with no answered seed resolves `unknown` with a detail naming the worker and the unanswered seed
- [ ] Lifecycle row for the case; `portSpawnGroup`'s summary says "no worktree (session checkout)" rather than a bare "nothing to port"
- [ ] Unit test pinning the change — fails against today's tree
- [ ] **Negative controls** in the same test: answered-success still succeeds; `running` still holds; `stopped` still fails; a mixed group with one failure still fails

### Phase 3: Bring the fixture to the no-worktree design

- [ ] Rewrite script lines 548-551 and 561-630: the worker writes into the session checkout; assert the file is present and uncommitted at build time and shipped by the phase-1 commit; remove the worktree-advance check
- [ ] Rewrite or retire the conflict control (668-715) in checkout terms; record which here
- [ ] Add a check that the unanswered-seed shape parks the node `unknown` (kill the worker before it answers, as the existing replacement control already does) — the integration pin for Phase 2
- [ ] Reset the coverage floor from a green run's executed count; update the floor comment's breakdown to match

### Phase 4: Integration test

- [ ] `scripts/test-multi-phase-graph.sh` green end to end
- [ ] Coverage floor equals the executed count (equality, not `>=`)
- [ ] Run through the run agent (**foreground**, per [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)) and record the counts here

## Notes

**Found while closing something else.** These 19 failures surfaced during MUX-167's Phase 4 runs and
were initially read as MUX-167's own red. Separating them mattered: MUX-167's five phase-check
assertions pass in the same runs with both controls, so the two results had to be attributed
independently rather than the whole script being called a failure.

**Reframed, not abandoned.** The original title read the missing worktree as a regression of
MUX-131. Phase 1 showed it is the 2026-09-03 design, and the user chose to keep that design. The
defect that survives is narrower and sharper: a node that reports success with no evidence, and a
fixture that tests a model the tree no longer has.

**Related:** [MUX-131](../completed/MUX-131-spawn-implement-output-never-ported.md) (the worktree-era
original and its fixture); [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) and
[MUX-176](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) (the same self-concealing shape —
a node reporting a success it did not earn);
[MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) /
[MUX-195](./MUX-195-graph-runs-never-reuse-idle-workers.md) (why a worker ends unanswered — the
causes this spec makes visible rather than fixes);
[MUX-142](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) (the no-worktree decision).

## Status

In Progress — Phase 1 complete (2026-10-03); reframed the same day on the user's decision; Phase 2 next
