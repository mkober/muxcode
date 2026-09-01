# `graph retry --from` Launders a Stale Human Gate Approval

`RetryGraphRun` resets only nodes **downstream** of `--from`. A node chosen downstream of an already
satisfied `wait_human` gate therefore re-executes **without the gate being re-dispatched**, so the
approval purge never runs and the run proceeds on an approval a human gave for different content.

Tracking: _(no GitHub issue yet)_

## Context

### Observed 2026-08-31 — caught before it ran

Run `1788225109-req-code-pr-deb2914a` failed at `commit` (the `phase-progress` guard declined a
doc-only tree, correctly — see [`MUX-131`](./MUX-131-spawn-implement-output-never-ported.md)). The
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

Same class as [`MUX-114`](../completed/MUX-114-close-spec-node-has-no-completion-check.md): the check
exists and is correct, but there is a path that never reaches it.

### Scope

Every template with a `wait_human` gate followed by a mutating node — `req-code-pr` (per-phase commit,
final push/PR), `commit-pr-review-loop`, `story-lifecycle`, `story-to-spec` (gated tracker update),
`update-spec-docs`, `pr-local-review`. The tracker-update gates carry the same exposure for Atlassian
writes as the commit gates do for git.

## Requirements

### Acceptance criteria

- [ ] A retry whose `--from` target is dominated by a satisfied `wait_human` gate either re-arms that
      gate or refuses, naming the gate — it must never consume the prior approval silently
- [ ] Re-arming purges the `approved` marker exactly as a normal gate dispatch does, so a fresh
      `graph approve` is required
- [ ] The refusal/re-arm decision is visible in `graph status` and in a lifecycle event, not only on
      stderr
- [ ] **Negative control:** a retry whose target is *not* downstream of any gate still works unchanged —
      the fix must not make every retry demand an approval that was never part of the run
- [ ] **Negative control:** a retry that re-enters the gate itself still behaves as
      `TestExecHumanGateRetryRequiresFreshApproval` asserts — that path must not regress
- [ ] **Negative control:** a run whose gate was never approved at all is unaffected — this is about
      stale approvals, not missing ones
- [ ] Dominance is computed from the graph, not hardcoded to `phase-gate`/`commit` — templates differ
      and new ones will be added

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_run.go` | `RetryGraphRun` — the downstream-only reset |
| `tools/muxcode/bus/graph_exec.go` | Gate dispatch and the `approved` purge (:482); `graphApprovalPath` |
| `tools/muxcode/bus/graph.go` | `nodeRequiresGate` — existing dominance logic to reuse, not reimplement |
| `tools/muxcode/cmd/graph.go` | `retry` subcommand — where a refusal surfaces to the user |
| `scripts/test-graph-orchestrator.sh` | Integration coverage for `graph retry --from` |

## Implementation

### Phase 1: Pin the hole

- [ ] Test asserting the **current** behaviour: retry from a node below a satisfied gate re-executes it
      with no fresh approval — a characterization test, green before the fix
- [ ] Confirm `TestExecHumanGateRetryRequiresFreshApproval` still passes alongside it, demonstrating the
      two cases are genuinely different paths

### Phase 2: Dominance check on retry

- [ ] Compute which `wait_human` gates dominate the `--from` target, reusing the dominance logic behind
      `nodeRequiresGate` rather than writing a second one
- [ ] Decide and record: re-arm the gate automatically, or refuse and tell the user which node to retry
      from instead
- [ ] Surface the outcome in `graph status` and a lifecycle event

### Phase 3: Negative controls

- [ ] Retry not downstream of any gate — unchanged
- [ ] Retry re-entering the gate — unchanged
- [ ] Never-approved gate — unaffected
- [ ] Confirm each control fails when the fix is reverted

### Phase 4: Integration test

- [ ] Extend `scripts/test-graph-orchestrator.sh`: approve a gate, fail the node behind it, change the
      tree, then retry from the failed node — assert a fresh approval is demanded before it fires
- [ ] Assert the un-gated retry path still completes without prompting
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run it and record passed/failed/exit code here

## Status

Backlog
