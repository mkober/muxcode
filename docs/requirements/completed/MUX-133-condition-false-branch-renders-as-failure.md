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

## Decision (maintainer, 2026-09-01): option B — decouple state from routing key

**B is chosen.** A condition node finishes as `GraphNodeDone` while still emitting `failure` as its
**outcome**, so the persisted state stops lying while edge matching is unchanged. This fixes every
consumer at source — `nodes/<id>.json`, `graph status --json`, `diagnose`, and any future tooling —
rather than requiring each to re-implement the `ConditionTookBranch` exemption.

Option A stays in place. It is not wasted: it remains the correct rendering vocabulary, and it keeps the
surfaces right during and after the model change.

### The risk, and the gate on it

`finishNode` currently derives state **from** outcome:

```go
terminal := GraphNodeDone
if outcome == OutcomeFailure { terminal = GraphNodeFailed }
```

Splitting them is exactly where a capped loop can silently stop terminating — the false edge fires
*because* the node "failed", so anything keying run-level failure off node **state** where it means
**outcome** breaks quietly, and the symptom (`req-code-pr` hanging at `loop-check`) looks like a graph
bug rather than a rendering change.

**Therefore Phase 1's enumeration is the gate, not a formality.** Implement B only after every consumer
of state-vs-outcome is enumerated. If that enumeration comes back tangled, stop and re-open the
decision — A alone is a defensible resting point; a half-migrated model is not.

## Superseded decision record

| Option | Change | Trade |
|---|---|---|
| **A — display-level** (proposed by edit) | Renderers branch on `Node.Type == NodeCondition` and show a neutral branch-taken glyph/label; red ✗ reserved for dispatch/execution errors | Cheapest and zero routing risk. Leaves the persisted state reading `failed`, so anything else reading `nodes/<id>.json` (JSON consumers, future tooling, `diagnose`) still sees a failure |
| **B — model-level** | Condition nodes finish as `GraphNodeDone` while still emitting the `failure` **outcome** for edge matching — decoupling state from routing key | Correct at the source and fixes every consumer at once. Riskier: must confirm nothing keys run-level failure off node **state** rather than outcome, and the false-edge routing must be re-proven |

A is safe and partial; B is correct and needs the routing invariant re-verified. **Recommend deciding
before implementation** — B done carelessly silently breaks every capped loop in every template.

## Requirements

### Acceptance criteria

- [x] A `condition` node taking its false branch is visually distinct from a node that failed to execute,
      in **both** `graph status` and the TUI DAG. Declined at the 19:43 pass — the TUI half was
      threaded through `tui/graph.go:122` but had **no test at all** — and closed once
      `TestRenderGraphFrame_ConditionBranchGlyph` (`tui/graph_test.go:474`) landed. It asserts both
      directions (`◇ cond` present and `✗ cond` absent for a branch; the reverse for an unevaluatable
      condition) against `StripAnsi(RenderGraphFrame(...))`, so the distinction is carried by the
      **glyph, not colour** — the [TUI style rule](../../tui-style.md) that a frame must stay
      readable through `StripAnsi`
- [x] The false branch still routes — every `condition` false edge fires exactly as it does today,
      re-proven rather than assumed — at three independent levels: the executor
      (`TestExecConditionFalseBranchIsNotAFailure`), integration section 9 (the edge still routes
      after the state/outcome split), and integration section 10 (the edge terminates a capped loop)
- [x] A `condition` node whose evaluation genuinely errors (bad predicate, unreadable context) still
      renders as a failure — `TestExecConditionUnevaluatableIsAFailure` persists `GraphNodeFailed`,
      and `TestFormatGraphRunConditionBranchNeutral` gained a third control asserting that state
      renders `failed`, not `branched`
- [x] The run-list surface agrees with the DAG surface — no view shows ✗ while another shows neutral.
      DAG: `TestRenderGraphFrame_ConditionBranchGlyph` draws `◇`, never `✗`. Run list:
      `TestLoadRunListRows_BranchedConditionIsNotAFailureCell` (`tui/graph_ui_test.go:1113`) asserts
      `Results` carries no `✗`, the branch appears in the done chain, and `Done == 3` — a branch-taker
      is *counted* as done, not merely drawn as it
- [x] **Negative control:** a real `send`/`spawn` node failure still renders red in both surfaces —
      the fix must not make failures unreadable, which would be a strictly worse outcome. CLI: the
      failed-send control in `TestFormatGraphRunConditionBranchNeutral`. DAG: pre-existing
      `TestRenderGraphFrame_StateGlyphs` asserts `✗ test` for a failed send. Run list: the
      failed-condition control keeps `✗ cond` in the failure cell.
      **`spawn` is not covered at render level** — spawn dispatch is unit-tested with a fake
      dispatcher and the integration script deliberately avoids real spawns (they launch tmux
      windows). Ticked anyway on a structural argument rather than a missing test:
      `ConditionTookBranch` is **type-gated** to `NodeCondition`, so a failed spawn can never satisfy
      it and necessarily renders by state — the same `✗` path as the failed send that *is* pinned
- [x] **Negative control:** a capped loop still terminates via its false edge, and a fan-out/join graph
      still completes — pinned end to end, not by inspection. Loop: integration section 10 drives a
      **real** iteration (a reply of `AGAIN` loops, `STOP` exits), then asserts termination, the
      `branched` rendering, and `state=done` in the run store. Fan-out/join: pre-existing section 5,
      still green in the same 60/0 run — regression coverage, which is what "still completes" asks for
- [x] **Nothing keys run-level failure off node `state` where it means `outcome`** — enumerated, not
      asserted. Option B is chosen, so this is now a live requirement rather than a conditional one.
      Satisfied by the [gate enumeration](#gate-enumeration-2026-09-01-state-vs-outcome-consumers);
      both load-bearing sites re-verified in the tree
- [x] **A condition's false edge still fires after the split** — re-proven end-to-end, not inferred from
      the outcome value being unchanged. `TestExecConditionFalseBranchIsNotAFailure` drives the
      executor and asserts the false target reaches `running` while the true target stays `pending`
- [x] **Negative control:** a capped loop still terminates via its false edge, and a run that should
      fail still fails. If B silently broke routing, `req-code-pr` would hang at `loop-check` and the
      symptom would read as a graph bug rather than as this change. Loop: section 10. Run-still-fails:
      pinned end to end by pre-existing section 8, which asserts a run **reaches `failed`**
      (`wait_run_state "$RID4" failed`, `:413`) — a live assertion in the same 60/0 run, not the
      inference from `graph_exec.go` keying run failure on `Outcome` that the enumeration offered
- [x] **Negative control:** a node that genuinely fails still persists `GraphNodeFailed` — the split
      must not make *every* node look done. Held by `TestExecConditionUnevaluatableIsAFailure`
      (an unevaluatable **condition** still persists `GraphNodeFailed`) plus the failed-send control
      in `TestFormatGraphRunConditionBranchNeutral`
- [x] `graph status --json` ~~, `diagnose`,~~ and the run store agree with the rendered surfaces after
      the split — the whole point of B over A. `--json`: section 9 asserts `"branched": true`. Run
      store: sections 9 and 10 assert `state=done` + `outcome=failure` in `nodes/<id>.json`.
      **`diagnose` struck as misworded, not satisfied** — it is not a consumer of graph node state at
      all: `grep -ci graph bus/diagnose.go` returns **0**, no references of any kind. The original
      criterion named a consumer that does not exist, so it could be neither satisfied nor violated,
      and ticking it as written would have asserted a check nobody ran. **If graph awareness is ever
      added to `diagnose`, it must honour `ConditionTookBranch`** or it will re-introduce this exact
      defect on a new surface

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
- [x] ~~Characterization test: green before the fix~~ → **the behaviour change is pinned in both
      directions by tests observed red against pre-fix code and green after.** Reworded 2026-09-01,
      because the literal form became unsatisfiable the moment option B landed first: the test that
      exists (`TestExecConditionFalseBranchIsNotAFailure`) asserts the *post-fix* state
      (`GraphNodeDone` + `OutcomeFailure`), which is the opposite of characterising the old one.
      What actually happened serves the same purpose — making the change visible rather than
      assumed: `TestExecConditionUnevaluatableIsAFailure` and
      `TestFormatGraphRunConditionBranchNeutral` were **red at 19:35 against pre-fix code** and green
      in their rebuilt forms after. **Provenance:** that red observation is edit's, reported at
      19:43; this pass did not re-run it. The rewording is recorded rather than silent because
      changing an item to match what happened is only honest if the substitution is visible — the
      original text is struck above, not deleted
- [x] **GATE for option B — do not implement before this is complete.** Enumerate every consumer of
      node **state** vs node **outcome** (renderers, `graph status --json`,
      TUI, diagnose, any JSON reader) — the list decides whether B is safe. **Verdict: PASS** —
      enumeration recorded below; the two load-bearing sites were re-checked in the tree rather than
      accepted from the report

#### Gate enumeration (2026-09-01): state vs outcome consumers

| Site | Keys on | Effect under option B |
|---|---|---|
| `graph_exec.go` edge match | `edgeOutcome(e) == st.Outcome` | unchanged — false edge still fires |
| `graph_exec.go` run-level failure | `st.Outcome == OutcomeFailure` | unchanged — no-live-edge still fails the run |
| `graph_exec.go` routing gate | state in {Done, Failed} | safe — Done already accepted |
| `graph_exec.go` loop re-arm (`armTarget`) | state in {Done, Failed, Skipped} | safe — Done already accepted |
| `graph_exec.go` `settleRun` | state in {Done, Failed} | safe — Done already accepted |
| `graph_run.go:88-92` transition table | `GraphNodeDone: {GraphNodeReady: true}` | safe — Done→Ready legal, capped loops still re-arm |
| `graph_run.go` `DoneAt` stamp | state in {Done, Failed} | safe — both stamp |
| `graph_run.go` `staleApprovalGates` | state+outcome, filtered to `NodeWaitHuman` | not applicable — conditions never reach it |
| `graph_run.go` failed-output detail | `state == GraphNodeFailed` | intended change — a branch no longer prints as error detail |

**Conclusion: nothing keys run-level failure off `state` where it means `outcome`.** The two
load-bearing sites both read `Outcome`, which option B leaves untouched. Verified in the working tree
at `graph_exec.go:964` (`edgeOutcome(e) == st.Outcome`) and `:1005`
(`st.Outcome == OutcomeFailure || exhausted > 0`) — line numbers in the original enumeration had
already drifted, so the claims were re-checked semantically rather than by position.

Two findings the enumeration produced:

- **Option B silently disables option A.** `ConditionTookBranch` keyed on `state == GraphNodeFailed`,
  so a branch-taker finishing `Done` would have made the predicate false and reverted all five
  consumers to a plain green check. Shipping B without re-keying would have regressed A. Now
  `nodeType == NodeCondition && state == GraphNodeDone && outcome == OutcomeFailure` — both halves
  load-bearing: outcome alone re-classifies a genuine error as a branch, state alone matches the true
  branch too. (Predicate confirmed at `graph_run.go:634`.)
- **The genuine-error criterion is reachable, but not as the spec assumed.** `EvaluateConditions`
  has **no error return** — it is `(bool, []ConditionResult)` (`conditions.go:55`), so an
  uninterpretable predicate surfaces as `Passed: false` with `Detail: "unknown condition type"`,
  otherwise identical to an honest false. `Graph.Validate` rejects unknown condition types at create,
  so the state is **unreachable via `graph run|validate`** and arises only when replaying a definition
  frozen before the rule existed. The executor branch is defence-in-depth for frozen definitions.

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

**Option B landed 2026-09-01**, committed as `4964f66`. What it changed, at source rather than at the
renderers:

| File | Change |
|---|---|
| `bus/graph_exec.go` | Condition dispatch splits branch from evaluation error; new `finishCondition` (always `Done`) and `unevaluatableCondition` helpers |
| `bus/graph_run.go` | `ConditionTookBranch` re-keyed from `(nodeType, state)` to `(nodeType, state, outcome)` |
| `tui/graph.go`, `tui/graph_ui.go`, `cmd/graph.go` | Call sites threaded with outcome; `GraphSnapshot.nodeOutcome` added; structurally dead `!ConditionTookBranch(...)` guards removed |
| `bus/graph_exec_test.go`, `bus/graph_run_test.go` | Two new executor tests; display fixture flipped to `Done`+failure, third control added |

A branch-taker now persists `state=done, outcome=failure` — the failure **outcome** is retained
deliberately, because it is the routing key the false edge matches; only the **state** moved. The
consequence is that `nodes/<id>.json`, `--json`, `diagnose` and the run store now agree with the
rendered surfaces, which is what option B was chosen over A to achieve.

**Verification provenance:** the implementing agent reported `go build`/`go vet`/`go test ./...` green
(4/4 packages); the **test agent independently reported the suite green at 19:43**, after option B
landed. This pass verified the *code and assertions* directly in the working tree, not a run — no
uncached run in the main checkout is recorded here. The work is committed as `4964f66`.

### Phase 4: Negative controls

- [x] Real node failure still renders red — `TestFormatGraphRunConditionBranchNeutral` asserts **both**
      directions: the condition renders `branched` and not `failed`, and a failed send node renders
      `failed` and not `branched`. A fix that neutralised everything fails the second half
- [x] Genuine condition-evaluation error still renders as a failure — pinned at **all three** layers:
      `TestExecConditionUnevaluatableIsAFailure` (executor persists `GraphNodeFailed`), the third
      control in `TestFormatGraphRunConditionBranchNeutral` (CLI renders `failed`, not `branched`),
      and the `broken` case in `TestRenderGraphFrame_ConditionBranchGlyph` (TUI keeps `✗`, never `◇`)
- [x] Capped loop still terminates via its false edge — integration section 9
      (`scripts/test-graph-orchestrator.sh:460`): the false edge routes and the run reaches complete
- [x] Confirm each control fails when the fix is reverted — with `ConditionTookBranch` reverted to the
      old state-only predicate, `TestRenderGraphFrame_ConditionBranchGlyph` fails
      `"false branch missing the ◇ branch glyph"`. **Checked structurally, not re-run here:** that is
      the verbatim error string at `tui/graph_test.go:21`, and it is the assertion a state-only
      predicate must trip — a branch-taker persists `state=done`, so `state == GraphNodeFailed`
      returns false, the `◇` is never emitted, and that exact branch fires

### Phase 5: Integration test

- [x] Extend `scripts/test-graph-orchestrator.sh`: run a graph whose condition takes the false branch,
      assert the rendered frame shows the branch-taken form and **not** the failure form — section 9,
      *"Condition false branch renders as a branch, not a failure"* (`:460`)
- [x] Assert a genuinely failed node in the same run still renders as a failure — both in one frame.
      One frame is the point: the reported defect was an operator unable to tell the two apart, so
      showing them **together** tests the discrimination rather than each glyph in isolation
- [x] Assert the loop still terminates and the run completes
- [x] Coverage floor raised to match the added checks; verify the new floor equals the achievable
      maximum so a skipped section cannot pass — **`FLOOR=60`** (`:609`), raised 47 → 56 → 60 as
      sections 9 and 10 landed, each step matching the count of checks added. **Caveat:** the floor
      tests `pass -ge FLOOR` with `FLOOR` at the achievable maximum, so a *failed* check also trips
      it — the floor cannot distinguish a skipped section from a failing one. `fail` is counted
      separately and drives `exit 1`, so the verdict stays correct; only the diagnostic is ambiguous
- [x] Run it and record passed/failed/exit code here — **60 passed, 0 failed, exit 0** (2026-09-01),
      floor met and equal to the maximum. Supersedes the 56/0 figure recorded before section 10 landed.
      **Provenance:** reported by the implementing agent; this pass verified the script's structure
      (sections 9 and 10, the floor, the run-store assertions on `nodes/<id>.json`) but did not
      execute it — running integration scripts is the run agent's role

## Status

Complete — closed 2026-09-01 at 11/11 acceptance criteria and 5/5 phases, against commit `4964f66`.
**Both options landed.** Option A (display-level) shipped first and is pinned by a two-direction test;
**option B (model-level)** is the change that makes a branched condition stop being `failed` at the
source, which is why B was chosen over A.

Both gaps flagged at the first verification are now closed: `graph status --json` sets
`GraphNodeStatus.Branched` (`cmd/graph.go:443`), and the `◇` vocabulary is recorded in
[`docs/tui-style.md`](../../tui-style.md) with its per-surface forms.

| Phase | | Note |
|---|---|---|
| 1 · Decide and pin | **3/3** | Gate PASSed by enumeration; the characterization item is **reworded** to what was actually done, with the original struck rather than deleted |
| 2 · Glyph and label vocabulary | **3/3** | |
| 3 · Implement | **2/2** | Option B landed — see the change table above |
| 4 · Negative controls | **4/4** | Genuine evaluation error pinned at executor, CLI **and** TUI |
| 5 · Integration test | **5/5** | `scripts/test-graph-orchestrator.sh` sections 9 **and 10**; floor 47 → 56 → **60**, equal to the achievable maximum |

**All five phases and all eleven acceptance criteria are now closed — zero open checkboxes.** They
closed in three rounds, and the sequence is worth keeping, because each decline named a *specific
missing artifact* and got it built rather than argued away:

| Round | Declined | Closed by |
|---|---|---|
| 19:43 | *Visually distinct in **both** surfaces* — `tui/` had no test referencing `ConditionTookBranch` | `TestRenderGraphFrame_ConditionBranchGlyph`, asserted on the `StripAnsi` frame |
| 19:47 | Six criteria, incl. *run-list agrees with DAG* and *run that should fail still fails* | Integration sections 9 + 10, `TestLoadRunListRows_BranchedConditionIsNotAFailureCell`, and section 8's pre-existing `wait_run_state … failed` |
| 19:55 | *`diagnose` agrees* — **corrected, not ticked** | Struck as misworded: `grep -ci graph bus/diagnose.go` returns 0 |

Two ticks carry an explicit caveat rather than a clean pin, and both are recorded at the criterion:

- **`spawn` is not covered at render level.** Ticked on a structural argument — `ConditionTookBranch`
  is type-gated to `NodeCondition`, so a failed spawn cannot satisfy it and must render by state —
  not on a test. The integration script avoids real spawns by design.
- **`diagnose` was struck from a criterion, which lowers the bar by definition.** Justified because
  the named consumer does not exist, but it is a *correction to the spec*, not evidence, and it is
  marked as such. If graph awareness is ever added to `diagnose`, this criterion must come back.

**Verification standing.** The test agent reported **2075 passed / 0 failed / 1 skipped** (19:45), and
the integration script **60 passed / 0 failed / exit 0** with `FLOOR=60` equal to its achievable
maximum — up from 47 → 56 → 60 as sections 9 and 10 landed. Those are independent reports from the
roles that run tests and scripts; this pass verified the *code, assertions and script structure*
directly in the working tree but executed neither.

One weakness in the floor is worth recording: it tests `pass -ge FLOOR` with `FLOOR` at the achievable
maximum, so a **failing** check also trips it. `fail` is counted separately and drives `exit 1`, so the
verdict stays correct — but the floor can no longer distinguish a skipped section from a failing one,
which was its original purpose.

**Complete.** The last thing holding this open was that every figure above described a *working tree*
rather than a commit. That is discharged: the work landed as **`4964f66`** ("MUX-133: condition false
branch renders branched, not failed") on 2026-09-01, 11 files including this spec, the Go changes, the
two new TUI tests, and the expanded `scripts/test-graph-orchestrator.sh`. Verified at close: the
commit carries **no MUX-134 material** (the two streams shared a working tree and had to be separated),
and this spec file was clean against it **at the time of the close** (it then lived at
`docs/requirements/drafts/`) — so the ticks above describe committed content, not an editor buffer.

Moved to [`completed/`](../completed/) on 2026-09-02 on the user's word, with its four inbound links
rewritten in the same pass.

**Not yet pushed** as of the move. The branch held four unpushed commits at that point (`4964f66`,
`1470fe3`, `aefcd05`, `73209b4`) plus `4e26589`; this spec is complete, the branch delivery is separate.
