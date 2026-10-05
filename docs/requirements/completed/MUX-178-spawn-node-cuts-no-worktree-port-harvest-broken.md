# A Graph Spawn Node Credits an Unanswered Seed as Success, and the Multi-Phase Fixture Still Asserts the Retired Worktree Model

**Tracking:** [mkober/muxcode#136](https://github.com/mkober/muxcode/issues/136)

**Provenance:** filed 2026-09-11 by plan on the user's request relayed by edit (`1789133110`), from the
MUX-167 Phase 4 runs, as "a graph spawn node cuts no worktree and ports nothing — MUX-131 has
regressed". **Reframed 2026-10-03** on the user's decision (relayed by edit, `1791036146`) after
Phase 1 found that the missing worktree is the 2026-09-03 design, not a regression: worktrees stay
retired, and the defect is the two things that remain true.

A graph `spawn` node completes in **two seconds** with `"output":"nothing to port","outcome":"success"`
for a worker whose seed was never recorded. `spawnGroupOutcome` (`tools/muxcode/bus/graph_exec.go:1652`)
credits a worker whose `Status` is `completed` as success whenever it has no seed to judge it by —
the process ended, so the work must be done. (A worker killed *after* its seed was recorded is a
different case: `lostSpawnWorkers` sees it and `replaceLostWorkers` replaces it, and that road stays
as it is.) The run advances: build dispatches on a checkout that
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
| Seed recorded (`SeedMsgID != ""`), **not** answered, `Status == "completed"`/`"stopped"` | never reaches this function on that tick — `lostSpawnWorkers` (`graph_exec.go:1804`) returns it as *lost* and `replaceLostWorkers` re-seeds a fresh worker with the same task | correct — a killed worker is a delivery failure, replaced (kept as-is under option (a)) |
| Any state, `Status == "running"` | hold (`return "", false`) | correct |
| **No seed recorded** (`SeedMsgID == ""`), `Status == "completed"` | **success** (line 1652, "success — no change") | `lostSpawnWorkers` skips an empty `SeedMsgID`, so nothing replaces it, and this branch is its only judge. The process ended without a seed ever being recorded; nothing says the work was done. This is the two-second record above |
| No seed recorded, any other status | failure | correct |

A `completed` entry with **no recorded seed** is the worker-never-worked shape of
[MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) and
[MUX-195](./MUX-195-graph-runs-never-reuse-idle-workers.md): the seed never landed in the store, so
there is no message to have been answered. The node has **no evidence** either way, and absence of
evidence is `unknown`, the outcome that holds for a human — the same rule the answered branch already
applies to a reply without a verdict token. Crediting it as success is the self-concealing shape of
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

### Findings from Phase 1 (2026-10-03)

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

- [x] A spawn entry recorded `completed` with **no recorded seed** (`SeedMsgID == ""`) resolves the node `unknown` — the run holds for a human — never `success`. This is the entry `lostSpawnWorkers` cannot see (it skips an empty `SeedMsgID`, `graph_exec.go:1804`), so no replacement fires and `spawnGroupOutcome` is the only judge — *Phase 2, `TestExecSpawnUnansweredWorkerHoldsNode`: node parks `unknown`, build stays pending*
- [x] The node's output for that case says why it is holding (worker recorded completed with no seed to judge it by), and a lifecycle row records it, so the silent case becomes visible — *output appends "[worker ended without answering its seed: … — outcome not established]"; row `graph-spawn-unanswered`*
- [x] **Negative control — replacement kept:** a worker killed **after** its seed was recorded and before it answered is still a *lost* worker: `replaceLostWorkers` replaces it on a fresh worker with the same task, as today, and the node does not resolve `unknown` — *`TestExecSpawnKilledSeededWorkerStillReplaced`*
- [x] **Negative control — precedence unchanged:** a worker whose seed was answered with a success verdict still resolves `success`; a `running` worker still holds; a `stopped` worker still fails; `failure > unknown > success` holds for a mixed group — *`TestSpawnGroupOutcomeUnansweredCompletedIsUnknown` rows*
- [x] `scripts/test-multi-phase-graph.sh` asserts the no-worktree design: the worker's output lands in the session checkout, the phase commit ships it, and no check reads `SPAWN_WT` — *Phase 3, section 7*
- [x] The script returns to green and its coverage floor equals the count a green run actually produces — *run agent task `1791153362-spawn-2c49b5c8-7c12cbc3`, hook row ts 1791153549, foreground: exit 0, 52 passed / 0 failed, floor 51 == 51 executed*
- [x] The outcome change is pinned by a unit test that fails against today's `graph_exec.go` — *`TestSpawnGroupOutcomeUnansweredCompletedIsUnknown`*
- [x] ~~A graph `spawn` node cuts a worktree, and its absence is an error~~ — **dropped 2026-10-03**: contradicts the 2026-09-03 no-worktree design, which the user chose to keep

### Technical approach

**Outcome.** In `spawnGroupOutcome`, the `case "completed":` branch becomes
`outcome = worseOutcome(outcome, OutcomeUnknown)` **when `SeedMsgID == ""`** — a detail naming the
worker and the missing seed. That is the one shape no other road judges: a recorded-but-unanswered
seed on a dead worker is already a *lost* worker (`lostSpawnWorkers`, `graph_exec.go:1804`) and is
replaced by `replaceLostWorkers` before this function sees it, and that road is kept unchanged under
option (a). The existing precedence already makes `unknown` hold the group unless another member
failed, so the human sees a hold, not a green node. Emit a lifecycle row (`graph-spawn-unanswered`, level
`warn`, source `daemon`) so `muxcode lifecycle show` surfaces the case the record used to hide. The answered branch and the `running`/default branches are untouched.

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

### Phase 2: A completed worker with no recorded seed is unknown, not success

Decision (user, 2026-10-04, option a — **keep replacement**): the kill→replacement road stays exactly
as it is. The hold targets only the entry that road cannot see — `completed` with an empty
`SeedMsgID` — which today falls through `lostSpawnWorkers` (skips `SeedMsgID == ""`) to
`spawnGroupOutcome`'s `case "completed"` and is credited as success.

- [x] `spawnGroupOutcome`: a `completed` entry with `SeedMsgID == ""` resolves `unknown` with a detail naming the worker and the missing seed; the `SeedMsgID != ""` branches are untouched — *verified 2026-10-04: the `case "completed"` branch now yields `unknown` for any unanswered worker, and the seeded case is kept off it by call order — `replaceLostWorkers` runs first (`graph_exec.go:1404`) and returns before `spawnGroupOutcome` whenever it replaces or fails the node — pinned by `TestExecSpawnKilledSeededWorkerStillReplaced`; `unansweredWorkers` names each worker as "(no seed recorded)" or "(seed … never answered)"*
- [x] Lifecycle row for the case; `portSpawnGroup`'s summary says "no worktree (session checkout)" rather than a bare "nothing to port" — *`graph-spawn-unanswered` (warn, daemon) at `graph_exec.go:1440`; summary is "no worktree (session checkout) — nothing to port"*
- [x] Unit test pinning the change — fails against today's tree — *`TestSpawnGroupOutcomeUnansweredCompletedIsUnknown` (rows "completed, seed never answered" and "completed, no seed recorded" returned `success` before)*
- [x] **Negative control — replacement kept:** in the same test, a `completed`/`stopped` entry with a recorded, unanswered seed is still returned by `lostSpawnWorkers` and replaced by `replaceLostWorkers`; it never reaches the new `unknown` branch — *as its own executor-level test, `TestExecSpawnKilledSeededWorkerStillReplaced`: fresh worker started, node stays running, outcome never `unknown`*
- [x] **Negative control — precedence unchanged:** answered-success still succeeds; `running` still holds; `stopped` still fails; a mixed group with one failure still fails — *rows "answered success", "running still holds the tick", "stopped still fails", "unanswered + stopped fails", "answered + unanswered holds"*

### Phase 3: Bring the fixture to the no-worktree design

- [x] Rewrite script lines 548-551 and 561-630: the worker writes into the session checkout; assert the file is present and uncommitted at build time and shipped by the phase-1 commit; remove the worktree-advance check — *verified 2026-10-04, section 7: "worker created in the session checkout, no worktree", "build sees the worker's file in the checkout", "worker output uncommitted at build time (working tree only)", "HEAD unchanged before the gate", "gated phase-1 commit shipped the worker's file"; no `SPAWN_WT` read remains; the worktree-advance check is gone*
- [x] Rewrite or retire the conflict control (668-715) in checkout terms; record which here — ***retired**: with no worktree there is no port to refuse, so the MUX-131 clobber-conflict control has no subject; its slot is now section 8 (below)*
- [x] Add a check that a spawn entry recorded `completed` with **no seed** (`SeedMsgID` empty — write the store entry that way, since no live road produces it on demand) parks the node `unknown` with the reason in its output — the integration pin for Phase 2 — *section 8, 7 checks: run started, worker seeded, `spawn.jsonl` entry rewritten to `completed` with `seed_msg_id` removed, `implement` outcome `unknown`, output names "`<worker>` (no seed recorded)", `graph-spawn-unanswered` lifecycle row names the worker, build inbox empty behind the held node*
- [x] **Negative control — replacement kept:** the existing kill-before-answer control (`no fresh worker after the kill` / `spawn store shows N workers`) still passes — a killed worker with a recorded seed is replaced, not held — *section 9, 5 checks, passing; `worker_pane_pids` now excludes the `muxcode graph ui` control pane, which had made the retention pin fail falsely*
- [x] Reset the coverage floor from a green run's executed count; update the floor comment's breakdown to match — *floor `== 51`; breakdown 4+1+1+3+3+4+1+5+1+16+7+5 = 51*

### Phase 4: Integration test

- [x] `scripts/test-multi-phase-graph.sh` green end to end — *2026-10-04, exit 0, 52 passed / 0 failed*
- [x] Coverage floor equals the executed count (equality, not `>=`) — *floor 51 == 51 executed*
- [x] Run through the run agent (**foreground**, per [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)) and record the counts here — *run agent task `1791154354-spawn-2c49b5c8-6fc2797f` (fresh run after the Phase 3 commit), hook row ts 1791154537: exit 0, foreground; **52 passed, 0 failed, 51 checks executed, floor met**. Consistent with the Phase 3 confirming run (task `1791153362-spawn-2c49b5c8-7c12cbc3`, same counts)*

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

## Also in this PR

Committed in `8ea87c1` ("MUX-178 Reframe spec; add spec-to-pr branch check and PR title fix",
2026-10-03) on this branch, alongside the reframe. Done, not part of this spec's phases — recorded so
the PR's contents are accounted for in one place.

- [x] **`50-spec-to-pr` branch check.** New condition type `spec_branch` (`bus/conditions.go`, `evalSpecBranch`): passes when the session repo's current branch is the active spec's id or starts with `<id>-`; fails closed on no active spec, a pointer outside the repo, an empty or unknown branch, or an unresolvable repo dir. The template now starts at `branch-check` → `implement`; a failed check parks at `branch-gate` (`wait_human`) and, once approved, `create-branch` (commit, `checkout`) runs `git switch -c <id>-<slug>` from the current HEAD carrying the working tree, or switches to the branch if it exists. Tests: `TestSpecBranchCondition`, `TestSpecBranchConditionWithoutID`, `TestSpecBranchConditionFailsClosed`, `TestSpecBranchConditionUnknownBranch` (`spec_branch_test.go`); `TestSpecToPRStartsOnSpecBranch` (`graph_workflow_templates_test.go`)
- [x] **PR title fix.** New placeholder `${spec_title}` (`specTitle`, `bus/intent.go`): the run intent with its launch-time ` — Phase …` suffix cut, so `<key> <title>`. `push-pr` titles the PR exactly `${spec_title}`; `${spec}` is unchanged for phase nodes. PRs #104 and #105 were titled after their Phase 1 because `${spec}` is `run.Intent`, frozen at launch by `describeSpecIntent`. Tests: `TestSpecTitle`, `TestPushPRTitleOmitsLaunchPhase` (`spec_title_test.go`)
- [x] **Docs.** `docs/hooks.md` condition table (+`spec_branch`, `spec_phases_remaining`, `spec_phase_committable`); `docs/agent-bus.md` "`50-spec-to-pr` head" paragraph and new "Message placeholders" table; `docs/architecture.md` template row and 13-type count; `CLAUDE.md` condition counts 11 → 13

Shipped in this PR on the user's request (2026-10-04), uncommitted at the time of writing:

- [x] **Codex hook markers aged out of `/tmp`.** macOS's daily sweep removed the hook-road marker files, so build and review silently fell back to the scrape road with no signal. Fix: `bus.RefreshCodexHooksMarkers` (`codex_hooks.go:334`), called from the daemon's `touchKeepalive` (`daemon.go:1772`) so every poll re-touches them. Test: `TestRefreshCodexHooksMarkers` (`codex_hooks_refresh_test.go`)
- [x] **Codex 0.158 reworded its self-update prompt.** "Update available · 0.158.0 → 0.160.0" over "enter continue · esc skip" replaced "Press enter to continue", so `SkipCodexUpdate` never fired and the startup wake's Enter chose "Update now" — the 2026-09-22 shape again. Fix: `codexUpdatePromptLive` (`provider_codex.go:354`) accepts both tails (`codexUpdatePromptTails`), and `captureInjectionTarget` refuses to inject while a self-update is in progress (`codexUpdateRunning`, `:372`). Tests: `TestCodexUpdatePrompt_Codex158Wording`, `TestCaptureInjectionTarget_RefusesCodexUpdateInProgress` (`codex_update_prompt_test.go`)

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken | 1h 38m | 2026-10-04 19:00 |

## Status

Complete — all four phases and all seven acceptance criteria verified 2026-10-04; closed out the same day: moved to `completed/`, backlog row moved to the id registry (defect ranks renumbered), cross-references in MUX-142, MUX-167, MUX-186, MUX-193 and MUX-195 repointed. Shipped in PR #148, which closes issue #136.
