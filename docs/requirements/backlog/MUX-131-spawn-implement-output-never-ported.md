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
  [`MUX-127`](./MUX-127-review-completion-routing.md) census fires 12–14)

### Scope

`req-code-pr` and `story-lifecycle` both use a `spawn` `implement` node. Any template whose spawn
produces artifacts consumed by a later node has this gap — the spawn contract has no output-porting
step at all, so this is not a one-template typo.

Related: [`MUX-091`](../completed/MUX-091-spawn-worktrees.md) added worktree isolation;
[`MUX-120`](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) covers spawn wake-up. Neither
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

- [ ] Key spawn workers per run+node so `dispatchNode` looks one up before starting a new one
- [ ] Liveness check with a fresh-start fallback when the worker is gone
- [ ] Verify a multi-phase run creates one `implement` worker by counting spawns for the run
- [ ] Negative controls: dead worker → fresh start; distinct nodes → distinct workers

### Phase 1: Decide the porting model

- [ ] Choose: (a) `implement` runs without worktree isolation, (b) an explicit port node between
      `implement` and `build`, or (c) `StartSpawn` harvests on completion
- [ ] Record the trade: worktree isolation exists so parallel spawns do not collide — option (a) gives
      that up for `map` fan-out, so decide what `map` does before changing `implement`
- [ ] Decide conflict semantics when the branch moved while the spawn ran

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

## Status

Backlog
