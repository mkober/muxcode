# A Condition Node's False Branch Renders as a Failure

A `condition` node that evaluates correctly and takes its **false** edge is recorded as
`GraphNodeFailed` with `outcome=failure`, so `graph status` prints it red as `failed` and the TUI DAG
draws a red ✗ between green checks. A branch selector choosing a branch is control flow, not an error.

Tracking: _(no GitHub issue yet)_

## Context

### Observed 2026-08-31

A `req-code-pr` run ending its phase loop normally — `loop-check` took the false edge because no phase
remained — rendered as a failed node in both surfaces. Reported from a screenshot showing
`✗ loop-check` sitting between green ✓ nodes, and `graph status` printing `failed  condition
outcome=failure` for a run that was proceeding exactly as designed.

### The chain, verified end to end

| Step | Code | Effect |
|---|---|---|
| 1 | `bus/graph_exec.go:459` — `outcome := OutcomeFailure`, flipped to success only `if passed` | a false predicate is an "outcome=failure" |
| 2 | `finishNode` (`graph_exec.go`) — `terminal := GraphNodeDone; if outcome == OutcomeFailure { terminal = GraphNodeFailed }` | the node's **persisted state** becomes `failed` |
| 3 | `GraphNodeStateColor` (`bus/graph_run.go:618`) — `case GraphNodeFailed: return ColorRed` | red |
| 4 | `FormatGraphRun` (`graph_run.go:665-671`) prints state, then appends `outcome=` | `failed … outcome=failure` |

**This is not purely a display bug.** The state written to `nodes/<id>.json` is `failed`. The renderers
are merely faithful to it — `NodeCondition` appears **zero** times in both `bus/graph_run.go` and
`tui/graph_ui.go`, so neither surface can distinguish a branch selector from a broken node even in
principle.

### Why the run still works

`OutcomeFailure` is the **routing key**: the false edge is matched by `edgeOutcome(e)`, so a condition
must produce `failure` for its false branch to fire at all. A failed node with a live matching edge
continues the run; only a failed node with **no** live edge terminates it (the
`node X failed with no live edge` path that killed run `1788225109`).

So the mechanism is load-bearing, and "just stop setting failure" would break every `condition` node's
false branch. That constrains the fix.

### Why it matters

Not cosmetic. The red ✗ is the same signal the surfaces use for a genuinely broken node, and this
session produced both kinds minutes apart — a real `commit` failure and a routine `loop-check` false
branch, visually identical. An operator scanning for trouble is trained to ignore the alarming glyph
in exactly the situation where a real failure is most likely to appear alongside it.

Same family as [`MUX-124`](../backlog/MUX-124-lifecycle-since-truncated-by-limit.md) and
[`MUX-006`](../backlog/MUX-006-diagnose-false-clean-verdict.md): *the instrument misreports its own subject.*

## Decision (maintainer, 2026-09-01): option A shipped first

**A (display-level) is implemented**; B remains open. A shipped first precisely because of the
routing-key constraint above: the `failure` outcome is what `edgeOutcome` matches, so touching the
model risks every capped loop, while a renderer change cannot.

`ConditionTookBranch(nodeType, state)` (`bus/graph_run.go:624`) is the single shared predicate — one
definition, four consumers, no mirrored logic: the CLI formatter (`graph_run.go:678`, renders
`branched` in `ColorDim`), the TUI glyph (`tui/graph.go:122`, `◇` in `Comment`), the run-list
failed-cell exclusion (`tui/graph.go:537`), and `tui/graph_ui.go:97`.

## Open decision (maintainer)

| Option | Change | Trade |
|---|---|---|
| **A — display-level** (proposed by edit) | Renderers branch on `Node.Type == NodeCondition` and show a neutral branch-taken glyph/label; red ✗ reserved for dispatch/execution errors | Cheapest and zero routing risk. Leaves the persisted state reading `failed`, so anything else reading `nodes/<id>.json` (JSON consumers, future tooling, `diagnose`) still sees a failure |
| **B — model-level** | Condition nodes finish as `GraphNodeDone` while still emitting the `failure` **outcome** for edge matching — decoupling state from routing key | Correct at the source and fixes every consumer at once. Riskier: must confirm nothing keys run-level failure off node **state** rather than outcome, and the false-edge routing must be re-proven |

A is safe and partial; B is correct and needs the routing invariant re-verified. **Recommend deciding
before implementation** — B done carelessly silently breaks every capped loop in every template.

## Requirements

### Acceptance criteria

- [ ] A `condition` node taking its false branch is visually distinct from a node that failed to execute,
      in **both** `graph status` and the TUI DAG
- [ ] The false branch still routes — every `condition` false edge fires exactly as it does today,
      re-proven rather than assumed
- [ ] A `condition` node whose evaluation genuinely errors (bad predicate, unreadable context) still
      renders as a failure
- [ ] The run-list surface agrees with the DAG surface — no view shows ✗ while another shows neutral
- [ ] **Negative control:** a real `send`/`spawn` node failure still renders red in both surfaces —
      the fix must not make failures unreadable, which would be a strictly worse outcome
- [ ] **Negative control:** a capped loop still terminates via its false edge, and a fan-out/join graph
      still completes — pinned end to end, not by inspection
- [ ] If option B is chosen: nothing keys run-level failure off node **state** where it means outcome —
      enumerated, not asserted

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | `case NodeCondition` (:459) sets the outcome; `finishNode` maps outcome → terminal state |
| `tools/muxcode/bus/graph_run.go` | `GraphNodeStateColor` (:618), `FormatGraphRun` (:665-671) — the CLI surface |
| `tools/muxcode/tui/graph_ui.go` | DAG and run-list renderers — zero `NodeCondition` references today |
| `docs/tui-style.md` | Glyph vocabulary; a branch-taken glyph must be added there, not invented locally |

## Implementation

### Phase 1: Decide and pin current behaviour

- [x] Settle option A vs B with the maintainer; record the choice and its reasoning here — **A shipped
      first**, B still open (see Decision above)
- [ ] Characterization test: a condition taking its false branch is `GraphNodeFailed` /
      `outcome=failure` today — green before the fix, so the change is visible in the diff
- [ ] Enumerate every consumer of node **state** vs node **outcome** (renderers, `graph status --json`,
      TUI, diagnose, any JSON reader) — the list decides whether B is safe

### Phase 2: Glyph and label vocabulary

- [x] Choose the branch-taken glyph and label — `◇` in `Comment` (TUI), the word `branched` in
      `ColorDim` (CLI)
- [x] Add it to [`docs/tui-style.md`](../../tui-style.md) so both surfaces draw from one vocabulary —
      added to the glyph table with the per-surface forms (TUI `◇`, CLI `branched`, JSON `branched: true`)
      and a note that `◆`/`◇` differ only by fill once colour is stripped
- [x] Confirm it reads correctly **without colour** — the CLI prints the word `branched` and the TUI
      uses a distinct `◇` glyph, so both survive `StripAnsi`

### Phase 3: Implement

- [x] Apply the chosen option across `graph status`, the TUI DAG, and the run-list together — all four
      consumers share the one `ConditionTookBranch` predicate, so they cannot drift apart
- [x] Keep `graph status --json` coherent with the rendered surfaces — `cmd/graph.go:443` sets
      `GraphNodeStatus.Branched` for branch-takers, so machine consumers no longer read a bare `failed`

### Phase 4: Negative controls

- [x] Real node failure still renders red — `TestFormatGraphRunConditionBranchNeutral` asserts **both**
      directions: the condition renders `branched` and not `failed`, and a failed send node renders
      `failed` and not `branched`. A fix that neutralised everything fails the second half
- [ ] Genuine condition-evaluation error still renders as a failure
- [ ] Capped loop still terminates via its false edge
- [ ] Confirm each control fails when the fix is reverted

### Phase 5: Integration test

- [ ] Extend `scripts/test-graph-orchestrator.sh`: run a graph whose condition takes the false branch,
      assert the rendered frame shows the branch-taken form and **not** the failure form
- [ ] Assert a genuinely failed node in the same run still renders as a failure — both in one frame
- [ ] Assert the loop still terminates and the run completes
- [ ] Coverage floor raised to match the added checks; verify the new floor equals the achievable
      maximum so a skipped section cannot pass
- [ ] Run it and record passed/failed/exit code here

## Status

In Progress — moved to `drafts/` 2026-09-01. **Option A (display-level) is implemented** and pinned by
a two-direction test; option B (model-level) remains open.

Both gaps flagged at the first verification are now closed: `graph status --json` sets
`GraphNodeStatus.Branched` (`cmd/graph.go:443`), and the `◇` vocabulary is recorded in
[`docs/tui-style.md`](../../tui-style.md) with its per-surface forms.

Phases 4 (remaining negative controls) and 5 (integration test) are open. Option B — decoupling the
persisted state from the routing key — remains unstarted and is the only path that makes a branched
condition stop being `failed` at the source.
