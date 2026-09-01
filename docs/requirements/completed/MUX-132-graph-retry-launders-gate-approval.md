# `graph retry --from` Launders a Stale Human Gate Approval

`RetryGraphRun` resets only nodes **downstream** of `--from`. A node chosen downstream of an already
satisfied `wait_human` gate therefore re-executes **without the gate being re-dispatched**, so the
approval purge never runs and the run proceeds on an approval a human gave for different content.

Tracking: _(no GitHub issue yet)_

## Context

### Observed 2026-08-31 — caught before it ran

Run `1788225109-req-code-pr-deb2914a` failed at `commit` (the `phase-progress` guard declined a
doc-only tree, correctly — see [`MUX-131`](../drafts/MUX-131-spawn-implement-output-never-ported.md)). The
proposed recovery was:

```
muxcode graph retry --from commit
```

The run's shape is `… → update-spec → phase-gate (wait_human) → commit → …`. `phase-gate` had already
completed `outcome=success` — approved while the branch held **no implementation work**. Between the
approval and the retry, 5 Go files were ported onto the branch plus several doc edits.

`retry --from commit` would have committed all of it on that stale approval. The human approved a
commit of an empty tree; the commit that fired would have carried the entire implementation.

### Mechanism

Two pieces combine:

1. **`RetryGraphRun` (`bus/graph_run.go`) resets only the downstream set** — it walks outgoing edges
   from `fromNode` and resets those, plus `fromNode` itself. Upstream nodes keep their state, so a
   satisfied `phase-gate` upstream of `commit` stays `done / success`.
2. **The approval purge lives in the gate's dispatch path** (`bus/graph_exec.go:482`) — it removes the
   `approved` marker when a `wait_human` node is *dispatched*. A gate that is never re-dispatched is
   never re-armed.

So the guarantee "a re-entered gate demands fresh approval" holds only when the retry re-enters the
gate. Starting **downstream** of it walks around the check entirely.

### Why the existing test does not cover this

`TestExecHumanGateRetryRequiresFreshApproval` pins the case where a retry **re-enters** the gate — it
asserts the purge fires and a fresh `graph approve` is required. That test passes, and would keep
passing with this hole wide open, because it never exercises a `--from` target *below* the gate.

A green test asserting the protection works is exactly what makes this hard to see.

### Why it matters more than a normal retry bug

The authority model's stated invariant is that **a graph cannot launder an action around the rules that
govern it** — `graph validate` rejects a commit or Atlassian node not dominated by a `wait_human`, and
`CheckCommitAuthority` / `CheckAtlassianAuthority` back it at runtime. Both are checks on the *static
definition* and on *who* is acting. Neither asks whether the approval being consumed was granted for
*this content*.

Same class as [`MUX-114`](./MUX-114-close-spec-node-has-no-completion-check.md): the check
exists and is correct, but there is a path that never reaches it.

### Scope

Every template with a `wait_human` gate followed by a mutating node — `req-code-pr` (per-phase commit,
final push/PR), `commit-pr-review-loop`, `story-lifecycle`, `story-to-spec` (gated tracker update),
`update-spec-docs`, `pr-local-review`. The tracker-update gates carry the same exposure for Atlassian
writes as the commit gates do for git.

## Requirements

### Acceptance criteria

- [x] A retry whose `--from` target is dominated by a satisfied `wait_human` gate **re-arms that gate and
      resumes there**, naming the gate and its original approval time in the output — it must never
      consume the prior approval, and never re-target silently
- [x] `TestExecRetryBelowGateConsumesStaleApproval`'s assertions are **inverted** by this phase (the test now reads `TestExecRetryBelowGateRearmsGate` after two renames — same test, inverted, never deleted): the gate
      re-arms, the `approved` marker is gone, the gated node does **not** re-fire unapproved, and edit
      receives a **second** `graph-approval` request. The characterization test is the fix's acceptance
      test read backwards — update it rather than deleting it, so the hole cannot silently return
- [x] Re-arming purges the `approved` marker exactly as a normal gate dispatch does, so a fresh
      `graph approve` is required
- [x] The refusal/re-arm decision is visible in `graph status` and in a lifecycle event, not only on
      stderr
- [x] **Negative control:** a retry whose target is *not* downstream of any gate still works unchanged —
      unit: `TestRetryGraphRunFromNode` (`len(res.Rearmed)==0`, resumes at the requested node);
      integration: ungated retry carries no re-arm note and demands **0** approvals across the path —
      the fix must not make every retry demand an approval that was never part of the run
- [x] **Negative control:** a retry that re-enters the gate itself still behaves as
      `TestExecHumanGateRetryRequiresFreshApproval` asserts — that path must not regress
- [x] **Negative control:** a run whose gate was never approved at all is unaffected
      (`TestExecRetryBelowNeverApprovedGateUnaffected`, pinning `staleApprovalGates`' done/success check —
      without it a re-arm-unconditional mutant passed the whole suite) — this is about
      stale approvals, not missing ones
- [x] Dominance is computed from the graph, not hardcoded to `phase-gate`/`commit` — templates differ
      and new ones will be added

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_run.go` | `RetryGraphRun` — the downstream-only reset |
| `tools/muxcode/bus/graph_exec.go` | Gate dispatch and the `approved` purge (:482); `graphApprovalPath` |
| `tools/muxcode/bus/graph.go` | `reachableStoppingAt` (the shared primitive), `gateDominates` (per-gate form of the `validateGates` rule), `nodeRequiresGate` (node predicate only — it holds **no** dominance logic; the original criterion naming it was wrong) |
| `tools/muxcode/cmd/graph.go` | `retry` subcommand — where a refusal surfaces to the user |
| `scripts/test-graph-orchestrator.sh` | Integration coverage for `graph retry --from` |

## Implementation

### Phase 1: Pin the hole

- [x] Test asserting the **current** behaviour: retry from a node below a satisfied gate re-executes it
      with no fresh approval — a characterization test, green before the fix.
      `TestExecRetryBelowGateConsumesStaleApproval` — since renamed `TestExecRetryBelowGateRearmsGate` when Phase 2 inverted it — builds the incident
      shape (`a → gate → c`, approve, fail `c`, retry from `c`) and asserts the gate stays `done`, the
      `approved` marker **survives**, `c` re-fires on it, and **edit receives exactly one**
      `graph-approval` request — the retry never asks again. Its comment states Phase 2 must invert
      these assertions, so it cannot be mistaken for desired behaviour
- [x] Confirm `TestExecHumanGateRetryRequiresFreshApproval` still passes alongside it, demonstrating the
      two cases are genuinely different paths — both PASS in the same run

### Phase 2: Dominance check on retry

**Decision (maintainer, 2026-08-31): re-arm the gate and resume there.** A retry whose `--from` target
is dominated by a satisfied `wait_human` does not start at the target — it re-dispatches the gate,
purging the approval, and waits for a fresh one before anything downstream fires. Chosen over refusing
because the user cannot accidentally skip the gate: the safe path is the default path, not a second
command they have to know to type.

The cost is that `--from` no longer starts exactly where asked, so **the re-targeting must be stated in
the output, never silent** — the run visibly resumes at the gate.

- [x] Compute which `wait_human` gates dominate the `--from` target, reusing the dominance logic behind
      `nodeRequiresGate` rather than writing a second one
- [x] Re-target the retry to the dominating gate: reset from the gate, purge its `approved` marker, and
      let normal dispatch re-request approval
- [x] **Say so in the output** — name the gate and its original approval time, as in the decision above.
      A silent re-target trades one surprise for another
- [x] **Refined during implementation:** strict dominance re-arms *neither* gate when two sit on
      parallel branches (neither is on *every* start→target path), so the implementation uses the **cut**
      form — every satisfied gate whose territory the target sits behind re-arms, pinned by
      `TestExecRetryBelowParallelGateCutRearmsAll` ("a dominator-only check re-arms neither")
- [x] Resolve **which** gate when several dominate: nearest to the target, or outermost. Nearest is the
      minimum re-work; outermost is the most conservative. Record the choice and why
- [x] Decide what happens to nodes **between** the gate and the original target — they are downstream of
      the gate, so a naive reset re-runs them. In the incident the gate sits directly before `commit` so
      the question does not arise; it will in other templates. Re-running an `implement` spawn to reach a
      commit would be expensive and is exactly the waste [`MUX-131`](../drafts/MUX-131-spawn-implement-output-never-ported.md)
      Defect B describes
- [x] Surface the outcome in `graph status` and a lifecycle event

### Phase 3: Negative controls

- [x] Retry not downstream of any gate — unchanged: `TestRetryGraphRunFromNode`
      (`bus/graph_exec_test.go:830`) asserts `len(res.Rearmed) == 0` and `res.From == requested`
      ("an ungated retry must not re-target"), plus upstream preserved. This is the control that
      catches the fix over-reaching into every retry
- [x] Retry re-entering the gate — unchanged: `TestExecHumanGateRetryRequiresFreshApproval` still passes
      alongside the new tests, so the pre-existing path did not regress
- [x] Never-approved gate — unaffected: `TestExecRetryBelowNeverApprovedGateUnaffected` pins
      `staleApprovalGates`' done/success check (`graph_run.go:577`). Without it, a mutant making re-arm
      unconditional passed the entire suite — the vacuum this control closes
- [x] Confirm each control fails when the fix is reverted — mutation table recorded per control with
      verbatim failure text; all mutants reverted, proven by `git diff --stat` being exactly the one
      test file. One honest nuance recorded: `TestExecRetryBelowGateRearmsGate` **survives** the
      dispatch-purge mutant because the retry path purges independently — two distinct purge sites,
      each separately pinned, which is defense in depth rather than a gap

### Phase 4: Integration test

- [x] Extend `scripts/test-graph-orchestrator.sh`: approve a gate, fail the node behind it, change the
      tree, then retry from the failed node — assert a fresh approval is demanded before it fires
- [x] Assert the un-gated retry path still completes without prompting — retry output carries no re-arm
      note and resumes at the requested node, and `graph-approval` count in edit's inbox is **0** across
      the entire ungated path
- [x] Coverage floor so a skipped run cannot report green — `FLOOR=47`, verified **strict**: 45 `ok`
      sites, one inside a 3-iteration loop, so 44+3 = 47 is the *maximum* achievable. The floor sits
      exactly at max, so any skipped section fails it
- [x] Run it and record passed/failed/exit code here — `bash scripts/test-graph-orchestrator.sh`
      2026-08-31 23:39:05: **47 passed, 0 failed, exit 0**. Passing count equals the floor *and* the
      maximum, so every check executed. (Trailing `Terminated: 15` on the background `muxcode watch` is
      the script's own teardown, not a failure.)

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-132-graph-retry-launders-gate-approval | 1h 12m | 2026-08-31 23:40 |

## Status

Complete — closed 2026-09-01 at **25/25**, all four phases, zero open items.

Delivered: `graph retry --from` no longer consumes a stale human approval. A target dominated by a
satisfied `wait_human` re-arms that gate and resumes there, naming the gate and its original approval
time in the CLI, the run's `RetryNote`, and a `graph-retry-regated` lifecycle event.

Verified end to end: unit suite in the **main checkout** at exit 0, and
`bash scripts/test-graph-orchestrator.sh` at **47 passed / 0 failed / exit 0** — a floor that equals the
maximum achievable, so a green run proves every check executed rather than merely that none failed.

Two things worth carrying forward:

- The characterization test that pinned the hole was **inverted, not deleted**, so the defect cannot
  return unnoticed.
- `gateDominates` is true dominance built on the shared `reachableStoppingAt` primitive — the same walk
  `validateGates` uses — so retry-time and validation-time notions of "what a gate covers" cannot drift
  apart.
