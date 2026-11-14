# The PR-Review Fix Node Dies on the 600 s Send Cap

**Tracking:** [mkober/muxcode#125](https://github.com/mkober/muxcode/issues/125)

The `commit-pr-review-loop` template's node `c` — *"Address the PR review comments"* — is a `send`
to edit with no `timeout_secs`, so it inherits the task store's 600 s default. Addressing review
feedback routinely takes longer than that: the fix runs its own build→test→review rounds before it
answers. On 2026-09-09 the node expired at 602 s while the work was being finished, the run failed
with "no live edge", and the fix landed on the branch anyway — a run that reported failure for work
that shipped. `spec-to-pr`'s `fix` node is a persistent-worker `spawn` and carries no such cap.

## Context

### Observed (2026-09-09, session `muxcode`, run `1788962181-commit-pr-review-loop-d72d9396`)

| Measurement | Value |
|-------------|-------|
| Gate | `gate2` approved 10:02:57 → node `c` dispatched to edit as a tracked task |
| The work | two `build-test-review` rounds on the PR #78 fixes (`1788962886-build-test-review-bd7b6fb5` at 10:08 and another at 10:11), ~12 min end to end |
| Expiry | 10:13:00 `task-timeout daemon→edit:edit expired in-flight` (602 s after dispatch) |
| Run | 10:13:00 `graph-node-done … c -> failure`, then `graph-run-failed … node c failed with no live edge` |
| The fix | landed as `1bb1817` "Address PR #78 review: stream rollout scan, pin watchdog test provider, sentinel on graph replies" — 7 files, 10:14:34 |
| Recovery | user `graph retry --from d` at 10:15:32 → `graph-retry-regated` re-armed `gate2` (MUX-132 behaviour); the run waits for a fresh approval |

### Mechanism — verified in code

- `bus/graph_templates.go:80` — `{"id": "c", "type": "send", "role": "edit", "action": "edit", "message": "Address the PR review comments"}`: no `timeout_secs`.
- `bus/graph_exec.go:981–988` `nodeTimeoutSecs` — a node's `TimeoutSec` when set, else **600**, handed to `CreateTask` on every send dispatch (`:865–877`); `bus/task.go:198–205` `taskTimeoutSecs` is the same default at the store.
- The daemon's tracked-task sweep marks the task `TaskTimedOut` and logs `task-timeout` (`daemon/daemon.go:2704`); `harvestRunningNode` then reads the expired task and finishes the node as a failure — with no failure edge from `c`, the run ends "no live edge".
- `bus/graph_exec.go:1458` — a send node with no `TimeoutSec` "relies on the tracked-task expiry above it, the same backstop every other stuck send depends on": the cap is a *stuck* backstop, applied to a node whose normal duration exceeds it.
- `bus/graph_templates.go:39` — `spec-to-pr`'s `fix` is `{"type": "spawn", "role": "edit", …}`: a persistent worker (MUX-159's executor fixes: reused between iterations, replaced if lost, stalls owned by the executor's capped force-redrive), harvested by its seed's response and liveness, not by a tracked task's clock.
- Nothing under `docs/` mentions `timeout_secs` or the 600 s send-node default (grep empty), so a template author has no way to learn a node carries a cap.

### Scope boundary

The 600 s default itself is right for what it was built for — a delegated build, test, review or
commit that has gone silent. This spec changes which node type the review-fix step uses (or its cap)
and documents the caps per template; it does not retune the store default or the stuck-task sweep.

## Requirements

### Acceptance criteria

- [x] `commit-pr-review-loop` node `c` survives a fix that takes longer than 600 s: a fix on the 2026-09-09 timeline (~12 min) ends the node on edit's reply, not on the 600 s default expiry — under Option A because a `spawn` has no tracked-task clock at all, under Option B because the explicit `timeout_secs` is sized above any observed fix — shown on `80-pr-review-fix`'s `fix` at a simulated 700 s (script section 2)
- [x] Option A (`spawn`, the shape `spec-to-pr` uses for `fix`) is the primary fix; Option B (keep the `send`, set an explicit `timeout_secs` with the rationale on the node) is the fallback, taken only if A proves unworkable and with the reason recorded in this spec's Notes — one convention for "an agent does open-ended work" nodes across templates — Option A holds in the tree (`80-pr-review-fix`'s `fix`), pinned by `TestBuiltinSendNodesAreBoundedDelegations`; B not taken
- [x] A send node that *does* keep the default cap says so in its template documentation, with the number — the `docs/agent-bus.md` cap table: every builtin send not listed runs on the 600 s default, with `120-deploy-verify`'s two long-running sends named
- [x] `graph validate` on every builtin template still passes; a `spawn` node `c` sits downstream of `gate2` exactly as the send did (authority gates unchanged) — `TestBuiltinTemplatesListInWorkflowOrder` validates every builtin; the script shows `fix-gate` approved before `fix` spawns; no template changed
- [x] A failed-then-shipped run cannot recur: with the fix, the 2026-09-09 timeline (12-minute fix) completes `c` on the reply and proceeds to `d` — script section 2, with section 3 reproducing the old failure as the control
- [x] Docs: `docs/architecture.md` graph section and `docs/agent-bus.md` `graph` reference name the default cap, `timeout_secs`, and which builtin nodes carry which

### Technical approach

**Option A — primary** (matches `spec-to-pr`): make `c` a `spawn` — `{"id": "c", "type": "spawn",
"role": "edit", "message": "Address the PR review comments on PR ${pr} …"}` — so it is a persistent
worker in the session checkout, harvested on its seed's reply, with the executor's stall path
(`graphRedriveMax`, `replaceLostWorkers`) instead of a clock. The worker is released when the run
ends. **Option B — fallback**, only if A proves unworkable (record why in Notes): keep the send and
set `"timeout_secs"` to a value sized for a multi-round fix (the observed run needed ~720 s; 1800 is
the conservative pick), documented on the node. B still ends the node on a clock — a larger one —
so it satisfies the first criterion only as long as no fix outruns the explicit cap. A is primary
because the cap is the wrong instrument for open-ended work, and because a spawn's output is ported
and its worker reused on the loop's next lap.

Either way, add a per-template table to the docs: node → type → cap (600 s default, `timeout_secs`,
or none) → what ends it. The template authoring notes gain one sentence: a `send` node is for a
bounded delegation; open-ended work is a `spawn`.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_templates.go` | `commit-pr-review-loop` node `c` (line 80); `spec-to-pr` `fix` spawn (line 39) as the model |
| `tools/muxcode/bus/graph_exec.go` | `nodeTimeoutSecs` (981), send dispatch `CreateTask` (865–877), `harvestRunningNode` timeout branches (1070, 1458) |
| `tools/muxcode/bus/task.go` | `taskTimeoutSecs` default 600 (198–205) |
| `tools/muxcode/bus/graph.go` | `Node.TimeoutSec` (`timeout_secs`, line 50) and its validation (363) |
| `tools/muxcode/bus/graph_templates_test.go` | builtin-template validation tests |
| `docs/architecture.md`, `docs/agent-bus.md` | graph section / `muxcode graph` reference — the per-template cap table |
| `scripts/test-multi-phase-graph.sh` | existing graph integration script to extend, or a sibling |

## Implementation

### Findings at activation (2026-11-14)

Checked against the tree by plan before the first phase. The spec was filed 2026-09-09 and the templates were reworked since; read the phases through this table.

| The spec says | The tree says | Consequence for the work |
|---------------|---------------|--------------------------|
| The template is `commit-pr-review-loop` and its fix step is node `c`, a `send` to edit | That template no longer exists. Its successor is `80-pr-review-fix` (`bus/graph_templates.go`), whose fix step is node `fix`, already `{"type": "spawn", "role": "edit"}` behind `fix-gate` | Option A is already in the tree. Phase 1's first step is a check that it holds, not a change; Option B is moot. Read "node `c`" as `80-pr-review-fix`'s `fix`, `gate2` as `fix-gate`, `d` as `build` |
| No builtin send to edit should carry the default cap | No builtin template sends to edit at all; the two explicit caps are `60-integration-suite`'s `suite` (5400) and `ci-watch` (3600) | The "every builtin send node sets `timeout_secs` or is a bounded delegation" test is still unwritten and still worth pinning |
| `nodeTimeoutSecs` is at `graph_exec.go:981` | It is at `:1456`, unchanged in behaviour (a node's `TimeoutSec`, else 600); `graph_workflow_templates_test.go` already pins the 600 default and the `suite` cap | Line numbers in "Mechanism" and "Key files" are stale; the test file is `graph_workflow_templates_test.go`, not `graph_templates_test.go` |
| Nothing under `docs/` mentions `timeout_secs` | Still true (grep of `docs/*.md` and `CLAUDE.md` is empty) | Phase 2 stands as written |
| The script runs a scratch `commit-pr-review-loop` | — | Phase 3 runs `80-pr-review-fix` (or a scratch template of the same shape) instead |

### Phase 1: The review-fix step outlives the 600 s send cap

- [x] Change node `c` in `commit-pr-review-loop` to a `spawn` (Option A, primary) — falling back to an explicit `timeout_secs` with a comment (Option B) only if A proves unworkable, with the reason in Notes — keeping its edges, its `gate2` upstream and `d` downstream — no change needed: the successor node already had this shape (see Findings at activation); `graph_templates.go` is untouched by this run
- [x] Builtin-template tests: `graph validate` passes; a test asserts `c`'s type/cap and that every builtin send node either sets `timeout_secs` or is a bounded delegation (build/test/review/commit/pr-read/comment) — `TestBuiltinSendNodesAreBoundedDelegations`
- [x] Executor test: a `send` node with the default cap expires at 600 s (existing behaviour, pinned as the negative control) while the new `c` shape completes on a reply at 700 s — under Option A no expiry exists to hit; under Option B the explicit cap is the only expiry, so the test also pins that a reply past it still expires (the cap is real, just sized for the work) — `TestReviewFixOutlivesTheSendCap`

### Results for Phase 1 (2026-11-14)

Verified by plan against the tree. The only source file this phase changed is `tools/muxcode/bus/graph_workflow_templates_test.go` (last edit 1794681315), followed in order by the build (exit 0, 1794681353), `./test.sh` (2 of 2 modules passed, 1794681421) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794681425.txt`).

| Item | State |
|------|-------|
| Option A | Already in the tree before this run: `80-pr-review-fix`'s `fix` is a `spawn` with no `timeout_secs`, behind `fix-gate`. No production code changed |
| `TestBuiltinSendNodesAreBoundedDelegations` | Every builtin send node is a listed bounded role:action or sets `timeout_secs`; `fix` is a clock-free spawn; the old send-to-edit shape is the negative control |
| `TestReviewFixOutlivesTheSendCap` | A send on the default cap expires at 700 s and its run fails; the real `80-pr-review-fix` `fix`, answered at 700 s, completes and dispatches `build` |
| Carried to Phase 2 | The worker notes `120-deploy-verify`'s `deploy` and `watch` nodes rely on the 600 s default — the cap table must list them |

Criteria left for later phases: the survives-a-long-fix and failed-then-shipped criteria are shown at unit level only and wait for Phase 3's script; the two documentation criteria wait for Phase 2; the `graph validate` criterion waits for the script's run over every builtin template.

### Phase 2: Caps are documented per template

- [x] `docs/architecture.md` graph section: the 600 s send default, `timeout_secs`, and the send-vs-spawn rule for open-ended work, with the 2026-09-09 run as the incident — `#### Node time limits`
- [x] `docs/agent-bus.md` `muxcode graph`: a per-builtin-template table of nodes that carry a cap and what ends each node type — `#### Node time limits`
- [x] `CLAUDE.md` graph-orchestration constraint: one clause naming the default cap and the rule — the **Node time limits** clause, written by edit

### Results for Phase 2 (2026-11-14)

Verified by plan against the tree. Last doc edit 1794681717, then the build (1794681758), `./test.sh` (2 of 2 modules, 1794681773) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794681773.txt`).

| Item | State |
|------|-------|
| First review round | One should-fix (`/tmp/muxcode-review-1794681636.txt`): both docs said `timeout_secs` is a hard timeout on any node type. Wrong — it is read only in `harvestRunningNode`, so only a running `send`, `spawn` or `map` honors it; `wait_human`/`wait_event` wait in `harvestWaitingNode` with no clock, and `condition`/`join` complete at dispatch. The claim came from the worker's fact sheet; plan had not checked it |
| Second round | The three documents corrected; review clean |
| Cap table | Checked by plan against `graph_templates.go`: spawns uncapped, `suite` 5400 s, `ci-watch` 3600 s (in `55-defect-backlog-loop` and `110-pr-merge`), every other send on 600 s. `120-deploy-verify`'s `deploy` and `watch` are named as sitting on the default — documented, not changed |
| For close-out | Both doc sections link this spec at its `drafts/` path |

### Phase 3: Integration test

- [x] Extend `scripts/test-multi-phase-graph.sh` (or add `scripts/test-graph-node-caps.sh`): a scratch run of `commit-pr-review-loop` with a stubbed edit reply arriving after the default cap → node `c` completes and `d` dispatches; a control `send` node with no reply expires at the cap with `task-timeout` logged — the sibling `scripts/test-graph-node-caps.sh`, run against `80-pr-review-fix`; elapsed time is simulated by backdating
- [x] The script asserts the documented cap table matches `nodeTimeoutSecs` for every builtin template node (parse `graph_templates.go`, compare) — reads the templates through `muxcode graph export` rather than parsing the Go source; two negative controls
- [x] Run the script and record pass/fail counts in this spec — 29 passed, 0 failed, floor 29 (proc `1794682009-proc-57ffc09f`)

### Results for the integration phase (2026-11-14)

Verified by plan against the tree and the proc log. The script's last edit (1794682003) precedes its run (1794682009–1794682042, exit 0); `CLAUDE.md`'s script index was edited after (1794682064), then the build (1794682097), `./test.sh` (2 of 2 modules, 1794682112) and the review (0 must-fix, 0 should-fix, 0 nits, `/tmp/muxcode-review-1794682112.txt`).

| Section | Checks | What it shows |
|---------|--------|---------------|
| 1. Documented caps vs templates | 7 | 13 builtin templates, none shadowed; the `docs/agent-bus.md` table's 13 node rows equal every builtin's `timeout_secs` and spawn nodes; the 600 s default is named; a changed cap and a dropped row are each detected |
| 2. `80-pr-review-fix`, a fix answered after 700 s | 14 | Real template on a scratch daemon: `fix-gate` waits and is approved before the `fix` worker spawns; `fix` is still running 700 s after dispatch with no failure recorded; the late reply dispatches `build`; outcome success |
| 3. Control, a send on the default | 8 | The old shape (a send to edit, node `c`): running at 300 s, the run fails at 700 s, `task-timeout` logged, `d` never dispatched |

Deviations from the spec's wording, all following from the Findings at activation: the run is `80-pr-review-fix` and its `fix`/`build` nodes, not `commit-pr-review-loop`'s `c`/`d`; the 12-minute timeline is simulated by backdating the dispatch, not waited out; the cap table is compared against `graph export`. The `graph validate` criterion rests on `TestBuiltinTemplatesListInWorkflowOrder`, which validates every builtin with no warnings and passed in this round's `./test.sh`, plus section 2's gate-before-spawn ordering.

## Notes

- Filed 2026-09-09 10:20 by plan from edit's account of run `d72d9396`, with the template, the
  timeout path and the lifecycle rows re-checked; the "pushed before the timeout" ordering in that
  account is not in the store — the commit `1bb1817` carries 10:14:34, 94 s after the expiry — and
  nothing here depends on it: the node had nothing to time out on but a legitimately long fix.
- 2026-09-09 10:30 review should-fix (relayed by edit): the first criterion said `c` ends "never on
  the tracked-task clock" while Option B keeps a clock. Reconciled by ranking the options rather than
  dropping either — Option A (`spawn`) is the primary fix and is clock-free; Option B (explicit
  `timeout_secs`) is the fallback, and the criterion now states what each shape must show. The Phase 1
  heading ("not on a clock") carried the same presumption and was renamed.
- Related: [MUX-159](./MUX-159-codex-hooks-provider.md) (persistent graph workers,
  `replaceLostWorkers`, the executor-owned stall path that makes a spawn the right shape here);
  [MUX-132](./MUX-132-graph-retry-launders-gate-approval.md) (the `retry --from d`
  re-gating seen in the recovery); [MUX-131](./MUX-131-spawn-implement-output-never-ported.md)
  (spawn output porting, which a spawn `c` inherits).

## Status

Complete — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`, 15/15 (activated the same day from Defects rank 14; ranks 1–13 were parked). Filed 2026-09-09 10:20.
