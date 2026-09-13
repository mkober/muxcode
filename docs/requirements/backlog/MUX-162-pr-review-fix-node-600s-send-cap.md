# The PR-Review Fix Node Dies on the 600 s Send Cap

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

- [ ] `commit-pr-review-loop` node `c` survives a fix that takes longer than 600 s: a fix on the 2026-09-09 timeline (~12 min) ends the node on edit's reply, not on the 600 s default expiry — under Option A because a `spawn` has no tracked-task clock at all, under Option B because the explicit `timeout_secs` is sized above any observed fix
- [ ] Option A (`spawn`, the shape `spec-to-pr` uses for `fix`) is the primary fix; Option B (keep the `send`, set an explicit `timeout_secs` with the rationale on the node) is the fallback, taken only if A proves unworkable and with the reason recorded in this spec's Notes — one convention for "an agent does open-ended work" nodes across templates
- [ ] A send node that *does* keep the default cap says so in its template documentation, with the number
- [ ] `graph validate` on every builtin template still passes; a `spawn` node `c` sits downstream of `gate2` exactly as the send did (authority gates unchanged)
- [ ] A failed-then-shipped run cannot recur: with the fix, the 2026-09-09 timeline (12-minute fix) completes `c` on the reply and proceeds to `d`
- [ ] Docs: `docs/architecture.md` graph section and `docs/agent-bus.md` `graph` reference name the default cap, `timeout_secs`, and which builtin nodes carry which

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

### Phase 1: The review-fix step outlives the 600 s send cap

- [ ] Change node `c` in `commit-pr-review-loop` to a `spawn` (Option A, primary) — falling back to an explicit `timeout_secs` with a comment (Option B) only if A proves unworkable, with the reason in Notes — keeping its edges, its `gate2` upstream and `d` downstream
- [ ] Builtin-template tests: `graph validate` passes; a test asserts `c`'s type/cap and that every builtin send node either sets `timeout_secs` or is a bounded delegation (build/test/review/commit/pr-read/comment)
- [ ] Executor test: a `send` node with the default cap expires at 600 s (existing behaviour, pinned as the negative control) while the new `c` shape completes on a reply at 700 s — under Option A no expiry exists to hit; under Option B the explicit cap is the only expiry, so the test also pins that a reply past it still expires (the cap is real, just sized for the work)

### Phase 2: Caps are documented per template

- [ ] `docs/architecture.md` graph section: the 600 s send default, `timeout_secs`, and the send-vs-spawn rule for open-ended work, with the 2026-09-09 run as the incident
- [ ] `docs/agent-bus.md` `muxcode graph`: a per-builtin-template table of nodes that carry a cap and what ends each node type
- [ ] `CLAUDE.md` graph-orchestration constraint: one clause naming the default cap and the rule

### Phase 3: Integration test

- [ ] Extend `scripts/test-multi-phase-graph.sh` (or add `scripts/test-graph-node-caps.sh`): a scratch run of `commit-pr-review-loop` with a stubbed edit reply arriving after the default cap → node `c` completes and `d` dispatches; a control `send` node with no reply expires at the cap with `task-timeout` logged
- [ ] The script asserts the documented cap table matches `nodeTimeoutSecs` for every builtin template node (parse `graph_templates.go`, compare)
- [ ] Run the script and record pass/fail counts in this spec

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
- Related: [MUX-159](../completed/MUX-159-codex-hooks-provider.md) (persistent graph workers,
  `replaceLostWorkers`, the executor-owned stall path that makes a spawn the right shape here);
  [MUX-132](../completed/MUX-132-graph-retry-launders-gate-approval.md) (the `retry --from d`
  re-gating seen in the recovery); [MUX-131](../completed/MUX-131-spawn-implement-output-never-ported.md)
  (spawn output porting, which a spawn `c` inherits).

## Status

**Backlog** — 0/15. Filed 2026-09-09 10:20.
