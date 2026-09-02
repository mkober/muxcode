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

- [x] A spawn node whose work must reach the branch either runs **without** worktree isolation, or has
      an explicit port step that lands its output before any downstream node runs
- [x] A spawn that produced changes but failed to port them **fails the node** — it must not report
      `success` while its output is stranded
- [x] `req-code-pr` and `story-lifecycle` both walk a phase end-to-end with the work reaching the branch — both templates exercised in the one harness; `story-lifecycle` has 10 dedicated checks where it previously had none
      **NOT covered:** only `req-code-pr` is exercised end-to-end; `story-lifecycle`'s spawn `implement` has no test.
- [x] No downstream node ever sees a branch missing the `implement` output
- [x] **Negative control:** a spawn that legitimately produces no changes (verify-only pass, phase
      already complete) still completes `success` — the fix must not turn "nothing to port" into a
      failure, since `implement`'s own message tells it to verify rather than re-implement when the
      phase is done
- [x] **Negative control:** the port step does not silently overwrite branch changes made while the
      spawn ran; a conflict surfaces loudly rather than resolving to either side
- [x] The guard is no longer the first thing to notice — a stranded-output run fails at or before
      `build`, not at `commit` after a human gate

### Acceptance criteria — Defect B

- [x] Re-entering a spawn node reuses the **same** worker when it is alive, rather than starting a new one
- [x] A full multi-phase run creates **one** `implement` worker, not one per iteration — asserted by
      counting spawns for the run, not by reading the code
- [x] The reused worker retains its conversation across iterations (no re-boot cost per phase) — *"worker process retained across iterations — conversation kept, no re-boot"*, with the replacement control proving the observable distinguishes reuse from replacement
      **NOT asserted:** retention follows structurally from reusing the same process, but nothing pins it — and avoiding re-boot cost is the entire point of reuse.
- [x] **Negative control:** when the previous worker is genuinely dead or unreachable, a fresh one is
      started — reuse must not wedge the run behind a corpse
- [x] **Negative control:** two *different* spawn nodes (e.g. `implement` and a `map` fan-out member)
      still get distinct workers — reuse is keyed per node, never global
- [x] Worktrees do not accumulate one-per-iteration; a run's spawn worktrees are accounted for at the
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
5. **Landing mechanism — AMENDED 2026-09-01 on authority grounds.** The original wording ("commit in
   the worktree and advance the branch ref") is **withdrawn**: it would have the daemon create commits,
   which is exactly the laundering shape the authority model exists to prevent.

   The point is sharper than "it bypasses `CheckCommitAuthority`". That function is called from only two
   sites — `cmd/send.go:133` and `bus/inbox.go:151`, **both message paths**. It governs who may *request*
   a commit over the bus. Daemon-side git does not bypass the check; it runs **where the check does not
   exist**. No `wait_human` dominates it, and no runtime backstop would catch it.

   **Amended mechanism: land the work UNCOMMITTED.** Apply the diff into the branch's working tree
   (`cherry-pick --no-commit` / `git apply`), leaving the **existing gated `commit` node as the only
   thing that ever creates a commit.** Defect A is still fixed — `build`, `test`, `review` and the
   `phase-progress` guard all see the work, because they read the working tree — while the consent
   invariant is untouched.

   **The cost, stated rather than glossed:** the original wording's rationale was clobber-avoidance.
   Landing uncommitted into the checkout working tree **restores that hazard**, so the amendment is only
   safe with an explicit guard — refuse to apply when the working tree carries modifications in the
   affected paths, and fail the node naming them, exactly as the conflict semantics in (2) do.


- [x] Choose: (a) `implement` runs without worktree isolation, (b) an explicit port node between
      `implement` and `build`, or (c) `StartSpawn` harvests on completion — **(c) chosen**, see above
- [x] Record the trade: isolation exists so parallel spawns do not collide — (a) rejected for exactly
      that reason, and `map` is settled ahead of `implement`: same harvest per member, landing in
      completion order so collisions surface as conflicts rather than a race
- [x] Decide conflict semantics when the branch moved while the spawn ran — the apply fails, the node
      fails naming the conflicting paths, neither side is auto-resolved, worktree left intact

### Phase 2: Implement porting

- [x] Implement the chosen model — landing **uncommitted** per the amended mechanism 5
      (`graph_port.go`; `TestPortSpawnWorktreeLandsChangesUncommitted` shows the files present in the
      checkout with `status --porcelain` reporting them uncommitted)
- [x] **The daemon never creates a commit** — verified two ways: no `commit`/`merge`/`cherry-pick`
      call exists anywhere in `graph_port.go` (only `reset --hard`, which creates nothing), and
      `rev-parse HEAD` is asserted unchanged across success **and** refusal paths (19 `rev-parse`
      assertions in the port tests)
- [x] **Clobber guard** — `checkoutDirtyIn` refuses on any affected path `git status --porcelain`
      reports dirty, failing the node and naming the paths
      (`TestPortSpawnWorktreeDirtyCheckoutRefusalNamesPaths`, human edit byte-identical after)
- [x] **REOPENED and RE-CLOSED 2026-09-01 — `worktreeContentEqualsTip` tested equality where containment is required.**
      Found live by worker `e1e3790b`. `git diff --cached <tip>` is **bidirectional**, so a worktree based
      on an older commit reports tip-only files as *deletions* and the diff is never empty — even when the
      worktree holds nothing the tip lacks. Auto-advance then refuses permanently and the worker
      phantom-blocks.

      **My ruling's wording is the root cause and I own it**: I wrote "advance only when the worktree's
      content is *contained in* the tip (diff-vs-tip empty)", which reads naturally as equality. Those
      are not the same predicate, and the stricter one shipped. The safety question is one-directional:
      *does the worktree hold anything the tip lacks?* A worktree merely **missing** tip content loses
      nothing on reset.

      **It failed in the safe direction** — stuck, never lossy — which is why it surfaced as a block
      rather than as data loss. That is the fail-closed design working; it is still a bug.
- [x] Fix landed: `worktreeContainedInTip` (`graph_port.go:394`) lists only paths the worktree's staged
      content **touches** and compares each against tip, so tip-only files can no longer register as
      deletions. Both advance sites use it; `err → false` preserves the fail-safe. The equality function
      is gone, not merely bypassed. Scoped to paths the worktree's staged content touches, so deletions of
      untouched tip-only files never count against it (or advance the base first, then compare)
- [x] **Negative control:** a stale-based worktree whose content is a strict subset of the tip **does**
      advance — `TestReseedAdvanceStaleBaseSubsetWorktree`, the phantom-block pin
- [x] **Negative control:** a worktree holding content the tip lacks still **refuses** to advance,
      including the deliberate-deletion case — the relaxation did not become "always advance"
- [x] A spawn with unported changes fails rather than reporting success —
      `TestExecSpawnHarvestConflictFailsNodeBeforeBuild`: node FAILED, `build` never dispatched
- [x] Conflicts surface as a node failure with the conflicting paths named —
      `TestPortSpawnWorktreeConflictFailsNamingPaths`; checkout untouched, HEAD unmoved

### Phase 2b decision (2026-09-01): do not discard the worktree copy while a port is uncommitted

The uncommitted-landing amendment has a fix-loop consequence the worker surfaced: after iteration 1
ports, `portSpawnWorktree` does `reset --hard`, so at reseed the worktree is **clean** and
`advanceSpawnWorktree` moves it to the branch tip — **a tip that does not contain the port**, because
the port created no commit. The fix pass then works without its own prior output, and its harvest is
clobber-refused against the run's own earlier port.

Two options were offered: (a) reseed re-applies the checkout's uncommitted delta into the worktree, or
(b) the guard recognises self-ported content by blob hash.

**Adjudication: (b), plus the root fix — stop discarding the worktree's copy.** Option (a) is a
workaround for having thrown the copy away in the first place. Instead:

1. **Do not `reset --hard` or advance the worktree while this run has an uncommitted port outstanding.**
   The fix pass then retains its own prior output naturally, with nothing to re-apply.
2. **Advance at reseed only when no uncommitted port is outstanding** — i.e. after the gated `commit`
   node has landed the work, which is exactly when the branch tip does contain it.
3. **The clobber guard must distinguish foreign edits from this run's own ported content** (option b).
   Its purpose is protecting a *human's* in-progress edit; content this run itself ported is not a
   clobber. Track it by path + blob hash.

**This also closes the durability window** edit flagged: between port and gated commit, the checkout's
uncommitted state would otherwise be the work's **sole copy** — one `git checkout`, `stash`, or failed
operation from losing it, with the worktree already reset. Keeping the worktree copy until the commit
lands means there are always two.

The cost is that the worktree diverges from the branch for the duration of one phase. That divergence
is precisely the run's own uncommitted work, so it is compatible by construction — unlike the
open-ended divergence the original spec warned about, which came from *never* advancing.

#### Scheduling ruling (2026-09-01): durability slice pulled into Phase 2, blob-hash guard stays Phase 3

Review holds a must-fix on ruling item 1 (the sole-copy window), which was scoped to Phase 3. The
durability slice is **approved for Phase 2**:

- a successful port keeps the worktree **dirty** — no `reset --hard`;
- at reseed, advance only when the worktree's content is **contained in the tip** (diff-vs-tip empty
  ⇒ the gated commit landed ⇒ `reset --hard` to tip is safe).

That test is a sound proxy: unported work, a failed port, or an outstanding uncommitted port all leave
a non-empty diff and correctly refuse to advance. It errs conservative — extra uncommitted work merely
delays an advance — which is the safe direction.

**Known limitation this leaves open, recorded deliberately.** Without the blob-hash guard, a fix loop's
**second** harvest is still clobber-refused against the run's *own* first port: `checkoutDirtyIn` blocks
any affected path that `git status --porcelain` reports dirty, **regardless of who dirtied it**, and
iteration 1's port is exactly what dirtied them.

Blessed anyway because it is **strictly better than the status quo**, not because it is complete:

| | Before Phase 2 | With this slice |
|---|---|---|
| First iteration | work stranded **silently**, spawn reports success | ported |
| Fix-loop iteration | work stranded silently | node **fails loudly**, naming paths, routed to the stuck gate |

Defect A's core harm is the *silence*. Converting a silent strand into a loud refusal is the win; the
refusal itself is a known cost until the blob-hash guard lands.

**Conditions on this ruling:**

- [x] The fix-loop refusal fails **loudly** and is test-pinned as expected behaviour —
      `TestPortSpawnWorktreeOwnPortFixLoopRefusalIsExpected`, whose comment states it pins *"a RECORDED
      LIMITATION, not a defect ... so the limitation is lifted deliberately, not discovered in
      production"*. Condition satisfied as written
- [x] The blob-hash guard lands before MUX-131 closes; a permanently-refusing fix loop is not an
      acceptable end state, only an acceptable intermediate one

### Phase 3: Negative controls

- [x] **Fix-loop control:** iteration 1 ports, build fails, the fix pass re-enters — the worktree still
      contains iteration 1's output, and the second harvest succeeds rather than being clobber-refused
      against the run's own port
- [x] **Durability control:** between a successful port and the gated commit, the work exists in **two**
      places (checkout working tree and worktree) — assert the worktree copy is not discarded
- [x] No-op spawn (nothing to port) still succeeds
- [x] Conflicting branch change surfaces loudly, does not silently pick a side
- [x] **Negative control:** a porting run leaves the branch ref and commit graph **unchanged** — assert
      `git rev-parse HEAD` is identical before and after a successful port, so a future refactor cannot
      quietly reintroduce daemon-side committing
- [x] **Negative control:** a dirty working tree in the affected paths fails the node rather than being
      overwritten
- [x] Confirm each control fails when its fix is reverted — mutation-confirmed per control (C3–C7),
      including reintroducing a daemon-side commit, which the HEAD-unchanged assertions caught

### Phase 4: Integration test

- [x] Extend `scripts/test-multi-phase-graph.sh`: a spawn-backed `implement` whose output must appear
      on the branch before `build` runs
- [x] Assert `build` sees the ported files — pin the actual failure mode, not just a green run
     
- [x] Assert the no-op spawn path still completes
- [x] **Assert spawn count for a multi-phase run is 1, not one-per-phase** (Defect B end-to-end)
      — `run_spawn_count` store helper — this
      is the check that would have caught the three-worker run
- [x] Coverage floor so a skipped run cannot report green — `>= 46`, and the itemisation sums exactly
      (4 validation + daemon + headline + 3 implement + 3 commits + 4 termination + start-at-2 +
      2 stuck-phase + 1 fixture + 17 spawn-run + 9 conflict-control). It counts checks **executed**
      (`pass + fail`) rather than passed, with the exit code handling failures separately — so it
      catches a short-circuited run without failing a complete one
- [x] Run it and record passed/failed/exit code here — `bash scripts/test-multi-phase-graph.sh`
      2026-09-01 10:34:02 in the **main checkout** (a 10:14 run used an absolute worktree path and is
      not counted): **47 passed, 0 failed, exit 0**, floor met at 46 checks executed. Verbatim among
      them: *"multi-phase run created ONE implement worker total (Defect B end-to-end)"*, *"stranded
      output failed the spawn node itself"*, *"build never dispatched — failure landed before build,
      not at commit"*

### Phase 5: Close-out coverage

Added 2026-09-01. Phases 0–4 are complete, but two acceptance criteria remained **unscoped** — they sit
under `## Requirements`, an `##` heading, so `SpecPhases` assigns them to no phase and the graph
machinery cannot drive them. `SpecCurrentPhase` was returning `(no open phase)` while the close-spec
guard still counted 2. That is [`MUX-130`](../backlog/MUX-130-spec-phase-parsing-semantics.md) Defect E,
and this phase is the same remedy applied to MUX-131 that Phase 3 was for MUX-007.

- [x] **Exercise `story-lifecycle` end-to-end** with the work reaching the branch. `req-code-pr` is — 10 dedicated checks in `scripts/test-multi-phase-graph.sh`
      covered by `scripts/test-multi-phase-graph.sh`; `story-lifecycle` uses a spawn `implement` too and
      is exercised **nowhere**, so half the stated scope of Defect A is unproven
- [x] **Pin conversation retention across iterations.** Retention follows structurally from reusing the — *"worker process retained across iterations — conversation kept, no re-boot"*
      same process, but nothing asserts it — and avoiding re-boot cost is the entire point of the
      Defect B fix. Without a pin, a future change that silently restarts the worker per iteration
      would keep every existing test green while removing the benefit
- [x] **Negative control:** a worker that is genuinely replaced (dead → fresh start) does **not** report — *"replaced worker does NOT read as retained — the observable distinguishes reuse from replacement"*, 5 replacement-control checks
      retained conversation — the assertion must distinguish reuse from replacement, or it passes
      vacuously
- [x] Extend the integration coverage rather than adding a parallel script, so `story-lifecycle` and — both templates live in the one harness
      `req-code-pr` are exercised by one harness and cannot drift apart
- [x] Raise the coverage floor to the new achievable maximum, and verify floor **equals** max so a — implemented as **equality** (`-eq 63`), stronger than asked: a `>=` floor lets newly added checks raise max above it, so a partially short-circuited run could still report green. Itemisation sums exactly to 63
      short-circuited run cannot report green
- [x] Run it and record passed/failed/exit code here — from the **main checkout**, not a spawn — `bash scripts/test-multi-phase-graph.sh` 2026-09-01 15:44:01 from the **main checkout**: **64 passed / 0 failed / exit 0**, *"coverage floor met and equals max (63 checks executed)"*. Script mtime 14:49:07 predates the run, so it covers the current script
      worktree (per A2, a run inside the tree under test proves nothing about the branch)

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-132-graph-retry-launders-gate-approval | 7h 34m | 2026-09-01 14:59 |

**Attribution caveat.** The branch is `MUX-132-…`, not a MUX-131 branch: this spec became the active
spec while work continued on the MUX-132 branch, so the recorded total covers MUX-132, MUX-133 and
MUX-134 work as well. The ledger is keyed by **branch**, not by spec, so it cannot separate them — the
row is kept honest by naming the branch rather than implying the time was spent here.

## Status

Complete — closed 2026-09-01 at **zero open items**, all six phases (0–5).

Both defects fixed and proven end-to-end.

| Defect | Fix | Proof |
|---|---|---|
| **B** — worker rebuilt every iteration | `acquireSpawnWorker` reuses a live worker per run+node, window-liveness gated, fresh-start fallback | *"multi-phase run created ONE implement worker total"* — counted from the store, not inferred |
| **A** — output never reaches the branch | executor-side harvest at iteration completion, landing **uncommitted** so the gated `commit` node stays the only commit creator | *"stranded output failed the spawn node itself"*, *"build never dispatched — failure landed before build, not at commit"* |

Final verification: `bash scripts/test-multi-phase-graph.sh` 2026-09-01 15:44:01 from the **main
checkout** — **64 passed / 0 failed / exit 0**, *"coverage floor met and equals max (63 checks
executed)"*. The floor is strict equality, not `>=`, so a green run proves every check executed and
newly added checks cannot silently raise max above the floor.

Three things worth carrying forward:

- **The landing mechanism was withdrawn on authority grounds mid-implementation.** The original plan had
  the daemon commit and advance the branch ref; `CheckCommitAuthority` guards only the message paths, so
  daemon-side git would have run where the check does not exist. Landing uncommitted keeps the gated
  `commit` node as the sole commit creator.
- **`worktreeContentEqualsTip` tested equality where containment was required**, permanently refusing
  auto-advance on any stale-based worktree. It failed *closed* — stuck, never lossy — which is why it
  surfaced as a phantom block rather than data loss.
- **The fix-loop limitation was lifted deliberately, not discovered.** Its pin
  (`…OwnPortFixLoopRefusalIsExpected`) was **removed** when the blob-hash guard landed, rather than left
  contradicting the new behaviour.

Related: [`MUX-135`](../backlog/MUX-135-spawn-seed-record-gc-strands-completion.md) raises exposure to a
separate defect — persistent workers mean longer iterations, and an iteration outliving the one-hour
delivery-record retention becomes permanently uncompletable. That is filed separately and unfixed.
