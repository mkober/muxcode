# Graph Spawn Workers: Output Never Ported, Worker Rebuilt Every Iteration

Two defects in how the graph executor dispatches spawn nodes. **Defect A**: a spawn's work never
reaches the run's branch, so every downstream node operates on a tree without it. **Defect B**: every
re-entry of a spawn node builds a brand-new worker, discarding the previous one's context and paying
full startup again. Filed together because they share one node, one dispatch site, and one fix window —
separate fix sites, but fixing either alone leaves the loop broken.

Tracking: _(no GitHub issue yet)_

## Defect B — the worker is destroyed and rebuilt every iteration

`dispatchNode` calls `StartSpawn(session, role, task, owner, true)` (`graph_exec.go:34`)
**unconditionally** — no lookup for an existing worker on the node, no reuse. `req-code-pr` re-enters
`implement` once per phase and again on each fix-loop pass, so each re-entry is a different agent.

Measured on the single run `1788225109-req-code-pr-deb2914a`:

| Spawn | Task |
|---|---|
| `1788225110-spawn-3bd8bf91` | Implement the active requirements spec's… |
| `1788225868-spawn-1a6aa1da` | Implement the active requirements spec's… |
| `1788226469-spawn-b041de6a` | Implement the active requirements spec's… |

Three workers, identical task, three worktrees, one run. Each pays:

- **Lost context** — the new worker knows nothing of what the previous one built, tried, or ruled out.
  It re-derives the phase from the spec and can re-do or contradict prior work
- **Startup tokens** — full agent boot (system prompt, agent definition, context files) per iteration,
  before any useful work
- **Wall clock** — iteration 1 alone took 195s; boot is paid again each time

This is also how a fresh worker can **re-implement an already-complete phase**, the near-miss recorded
in [`MUX-121`](../completed/MUX-121-multi-phase-sequential-graph.md) where a second run produced a
parallel, incompatible implementation. The `implement` message tries to mitigate it in prose ("if it is
already complete, verify and report rather than re-implementing") — prose is not a mechanism.

Interaction with Defect A: a **reused** worker holds its worktree across iterations, so porting
(Defect A) still has to happen — reuse does not remove the need for it, and a persistent worktree
diverging further from the branch each pass makes the eventual port harder, not easier. Sequence the
fixes accordingly.

## Update 2026-09-01: two further failure modes, observed in one run

Run `1788228263-req-code-pr-a93ae921` (MUX-132, four phases) exercised both defects repeatedly and
surfaced **two modes this spec did not previously describe**. Both are worse than the filed symptom.

### A2 — the defect fabricates verification, it does not only lose work

A full `go test -count=1 -v ./...` executed **inside a spawn worktree** reported **exit 0, 2455 passing,
zero failures** — while the test it was run to verify was **absent from its output entirely**. The same
suite in the main checkout **failed**.

Losing work is recoverable once noticed. A green suite from a tree that does not contain the code under
test is *evidence that is confidently wrong*, and it is indistinguishable from a real pass unless the
reader checks **where** the run executed. Any acceptance criterion satisfied by such a run is false.

### B2 — worker churn produces duplicate work, not just wasted startup

Two workers independently implemented **the same Phase 3 negative control** under different names —
`TestExecRetryBelowNeverApprovedGateNoRearm` (`spawn-2adb4438`) and
`TestExecRetryBelowNeverApprovedGateUnaffected` (harvested to the branch). Neither knew of the other.

So Defect B's cost is not only re-paid startup and lost context: **the same work gets built twice** and
someone must then reconcile which copy is authoritative.

### Scale in a single run

Six spawn worktrees for one run, five still holding uncommitted trees at differing HEADs afterwards:

| Worktree | HEAD | Uncommitted | Disposition |
|---|---|---|---|
| `spawn-3bd8bf91` | `d8a36cf` | 0 | — |
| `spawn-0ef7d4b4` | `706a335` | 1 file | superseded (pre-inversion test) |
| `spawn-8782c2da` | `4fdd32c` | 5 files | harvested (Phase 2) |
| `spawn-242ab323` | `1c47948` | 1 file | harvested (Phase 3) |
| `spawn-2adb4438` | `1c47948` | 1 file | duplicate of Phase 3 (B2) |
| `spawn-a0d10c42` | `a20b2c3` | 1 file | harvested (Phase 4) |

Establishing that nothing was *lost* required diffing every worktree against the branch — the reason
"is the work safe?" is expensive to answer at all is this defect.

### What this implies for the fix order

A2 argues for treating **Defect A as the higher priority**: while it stands, any test evidence produced
by a spawn is untrustworthy, so verification of the spawn machinery itself cannot be relied on. The
current spec sequences B (worker reuse) as Phase 0 because reuse changes what porting means — that
ordering still holds for *implementation*, but A's blast radius is larger and worth stating plainly.

## Defect A — spawn output never reaches the branch

`req-code-pr`'s `implement` node is a **spawn**, which runs in an isolated git worktree. Nothing in the
template ports that worktree's output back to the run's branch. The spawn reports `success`, and every
downstream node — `build`, `test`, `review`, `update-spec`, `commit` — then operates on a branch that
does not contain the work.

## Context

### Observed failure — run `1788225109-req-code-pr-deb2914a`, 2026-08-31

```
implement    done    spawn  outcome=success  took=195s
build        done    send   outcome=unknown  took=15s
test         done    send   outcome=unknown  took=12s
review       done    send   outcome=unknown  took=11s
update-spec  done    send   outcome=unknown  took=35s
phase-gate   done    wait_human  outcome=success
commit       failed  send   outcome=failure
  ↳ phase-progress guard declined: 0 commits shipped but only 0 phases complete —
    this commit's phase is still open
```

The run reached a **human approval gate** and burned four downstream nodes before failing. The work
existed the whole time — in spawn worktree #1 — and had to be ported to the branch by hand afterwards
(5 files: `workflow.go`, `workflow_test.go`, `daemon.go`, `reviewed_gate_test.go`, `CLAUDE.md`).

### The guard was the only thing that noticed

`phase-progress` declined because the spec's phase was still open — correct, and the sole reason a
commit of an empty tree did not ship. But the guard is the **last** node in the chain. Everything
before it accepted a branch with no work:

- `build` and `test` returned `unknown` and routed via the success edge, so neither asserted anything
- `update-spec` asked the plan agent to check off a phase against changes that were not on the branch —
  had plan complied, a false `- [x]` would have satisfied the guard and shipped the empty commit

That last point is the serious one: **the guard's protection depends on the verifier refusing to check
off work it cannot see.** Two independent refusals (the guard, and plan reading a zero-line `git diff`)
are what held. Neither is a structural fix.

### Cost

- One full phase iteration wasted (~32 min wall clock, spawn + 4 nodes + a human gate approval)
- A human approved a gate for a commit that could never have succeeded
- Continued `verify-spec` traffic against a **terminal** run afterwards — every fire answered was
  against a run that had already failed and could not proceed (the same shape as
  [`MUX-127`](../backlog/MUX-127-review-completion-routing.md) census fires 12–14)

### Scope

`req-code-pr` and `story-lifecycle` both use a `spawn` `implement` node. Any template whose spawn
produces artifacts consumed by a later node has this gap — the spawn contract has no output-porting
step at all, so this is not a one-template typo.

Related: [`MUX-091`](../completed/MUX-091-spawn-worktrees.md) added worktree isolation;
[`MUX-120`](../backlog/MUX-120-spawn-worker-never-woken-for-seeded-task.md) covers spawn wake-up. Neither
addresses harvesting a spawn's work.

## Requirements

### Acceptance criteria

- [ ] A spawn node whose work must reach the branch either runs **without** worktree isolation, or has
      an explicit port step that lands its output before any downstream node runs
- [ ] A spawn that produced changes but failed to port them **fails the node** — it must not report
      `success` while its output is stranded
- [ ] `req-code-pr` and `story-lifecycle` both walk a phase end-to-end with the work reaching the branch
- [ ] No downstream node ever sees a branch missing the `implement` output
- [ ] **Negative control:** a spawn that legitimately produces no changes (verify-only pass, phase
      already complete) still completes `success` — the fix must not turn "nothing to port" into a
      failure, since `implement`'s own message tells it to verify rather than re-implement when the
      phase is done
- [ ] **Negative control:** the port step does not silently overwrite branch changes made while the
      spawn ran; a conflict surfaces loudly rather than resolving to either side
- [ ] The guard is no longer the first thing to notice — a stranded-output run fails at or before
      `build`, not at `commit` after a human gate

### Acceptance criteria — Defect B

- [ ] Re-entering a spawn node reuses the **same** worker when it is alive, rather than starting a new one
- [ ] A full multi-phase run creates **one** `implement` worker, not one per iteration — asserted by
      counting spawns for the run, not by reading the code
- [ ] The reused worker retains its conversation across iterations (no re-boot cost per phase)
- [ ] **Negative control:** when the previous worker is genuinely dead or unreachable, a fresh one is
      started — reuse must not wedge the run behind a corpse
- [ ] **Negative control:** two *different* spawn nodes (e.g. `implement` and a `map` fan-out member)
      still get distinct workers — reuse is keyed per node, never global
- [ ] Worktrees do not accumulate one-per-iteration; a run's spawn worktrees are accounted for at the
      end (`graph_exec` currently never calls `StopSpawn`/`CleanFinishedSpawns`, so they persist —
      confirm before changing, this was observed 2026-08-28 and not re-verified here)

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_templates.go` | `req-code-pr`, `story-lifecycle` — the `implement` spawn nodes |
| `tools/muxcode/bus/graph_exec.go` | Spawn dispatch and outcome derivation |
| `tools/muxcode/bus/spawn.go` | `StartSpawn`, worktree create/remove, `GetSpawnResult` |
| `scripts/test-multi-phase-graph.sh` | Multi-phase integration test — where the end-to-end proof lands |

## Implementation

> **Sequence B before A where they touch.** A reused worker keeps one worktree across the whole run,
> which changes what "port the output" means — decide reuse first, then build porting against the
> model you actually have.

### Phase 0: Worker reuse (Defect B)

- [x] Key spawn workers per run+node so `dispatchNode` looks one up before starting a new one —
      `acquireSpawnWorker` (`graph_exec.go:56`): reuse lookup → reseed → fresh-start fallback, with
      `SpawnEntry.RunID`/`NodeID` as the key and `graph-spawn-reuse` lifecycle events
- [x] Liveness check with a fresh-start fallback when the worker is gone — `FindLiveSpawn` gates on the
      **window** being alive, so a corpse entry can never wedge a run
      (`TestAcquireSpawnWorkerDeadWorkerFreshStart`)
- [x] Verify a multi-phase run creates one `implement` worker by counting spawns for the run —
      `TestExecSpawnLoopReusesWorker` asserts **spawn count == 1 across two iterations, counted from the
      store**. That is the assertion that would have caught the three-worker run
- [x] Negative controls: dead worker → fresh start; distinct nodes → distinct workers — both pinned,
      and `map` members key per item index (`node#i`) so fan-out members never share a worker

### Phase 1: Decide the porting model

**Decision 2026-09-01: option (c), executor-side harvest at iteration completion.**

- **(a) drop isolation — rejected.** Isolation exists so parallel `map` members do not collide. A mode
  split (implement shared, map isolated) forks the spawn contract, and a shared-tree `implement` also
  collides with the edit agent or a human working in the main checkout, and with any concurrent run.
- **(b) explicit port node per template — rejected.** This spec's own finding is that *the spawn
  contract has no porting step at all*, so a template-authored fix re-creates the omission class for
  every future template — and makes porting LLM-mediated where it should be deterministic git mechanics.
- **(c) harvest on completion — chosen.** Structural (covers every spawn node in every template),
  deterministic (the daemon does the git, no model in the loop), and sited exactly where
  `spawnGroupOutcome` derives success — so **"produced changes but failed to port → the node fails"**
  falls out naturally, and a stranded-output run fails **at the spawn node, before `build`**. That
  directly satisfies the criterion that the guard must no longer be the first thing to notice.

Reuse-aware mechanics carried into Phase 2 (reuse means one persistent worktree per run):

1. Harvest fires **per iteration** (current seed responded), not per worker lifetime: diff the worktree,
   land it on the branch, then advance the worktree to the new branch tip so it tracks the branch
   instead of diverging further each phase — the divergence hazard recorded above.
2. **Conflict** (branch moved while the spawn ran): the apply fails, the node fails with the conflicting
   paths named, and neither side is auto-resolved. The worktree is left intact.
3. **No-op iteration** (verify-only pass, phase already complete): empty diff succeeds without porting —
   "nothing to port" must never become a failure.
4. **Map fan-out**: same harvest per member at completion, landing sequentially in completion order, so
   parallel collisions surface as explicit conflicts on the later member rather than racing a shared tree.
5. **Landing mechanism**: prefer committing in the worktree and advancing the branch ref over mutating
   the main checkout's working tree, so a human mid-edit is never clobbered.


- [x] Choose: (a) `implement` runs without worktree isolation, (b) an explicit port node between
      `implement` and `build`, or (c) `StartSpawn` harvests on completion — **(c) chosen**, see above
- [x] Record the trade: isolation exists so parallel spawns do not collide — (a) rejected for exactly
      that reason, and `map` is settled ahead of `implement`: same harvest per member, landing in
      completion order so collisions surface as conflicts rather than a race
- [x] Decide conflict semantics when the branch moved while the spawn ran — the apply fails, the node
      fails naming the conflicting paths, neither side is auto-resolved, worktree left intact

### Phase 2: Implement porting

- [ ] Implement the chosen model
- [ ] A spawn with unported changes fails rather than reporting success
- [ ] Conflicts surface as a node failure with the conflicting paths named

### Phase 3: Negative controls

- [ ] No-op spawn (nothing to port) still succeeds
- [ ] Conflicting branch change surfaces loudly, does not silently pick a side
- [ ] Confirm each control fails when its fix is reverted

### Phase 4: Integration test

- [ ] Extend `scripts/test-multi-phase-graph.sh`: a spawn-backed `implement` whose output must appear
      on the branch before `build` runs
- [ ] Assert `build` sees the ported files — pin the actual failure mode, not just a green run
- [ ] Assert the no-op spawn path still completes
- [ ] **Assert spawn count for a multi-phase run is 1, not one-per-phase** (Defect B end-to-end) — this
      is the check that would have caught the three-worker run
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run it and record passed/failed/exit code here

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-132-graph-retry-launders-gate-approval | 2h 10m | 2026-09-01 00:42 |

**Attribution caveat.** The branch is `MUX-132-…`, not a MUX-131 branch: this spec became the active
spec while work continued on the MUX-132 branch, so the recorded total covers MUX-132, MUX-133 and
MUX-134 work as well. The ledger is keyed by **branch**, not by spec, so it cannot separate them — the
row is kept honest by naming the branch rather than implying the time was spent here.

## Status

In Progress — moved to `drafts/` 2026-09-01. **Phases 0 and 1 complete.**

**Phase 0 (worker reuse, Defect B)** — `acquireSpawnWorker` reuses a live worker per run+node, with
window-liveness gating and a fresh-start fallback. Verified from the primary artifact:
`go test -count=1 -v ./...` in the **main checkout**, exit 0, **2463 PASS / 0 FAIL**, with verbatim
`--- PASS:` lines for all four new tests. The load-bearing one is `TestExecSpawnLoopReusesWorker`,
which counts spawns **from the store** across two iterations and asserts exactly one — the assertion
whose absence let a single run create six workers.

**Phase 1 (porting model, Defect A)** — option (c), executor-side harvest at iteration completion.

Phases 2–4 open.

Sequencing note recorded before implementation starts: **A2 makes Defect A the higher-priority defect**,
because while it stands, any test evidence produced by a spawn is untrustworthy — including evidence
about the spawn machinery itself. The phase order below still runs B (worker reuse) first, since reuse
changes what "port the output" means, but the verification of *either* fix must not rely on a
spawn-produced run until A is closed.
