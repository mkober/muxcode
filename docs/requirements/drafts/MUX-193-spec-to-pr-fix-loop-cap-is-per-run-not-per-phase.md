# MUX-193: The Spec-to-PR Fix-Loop Cap Is Per-Run, Not Per-Phase

`50-spec-to-pr` caps its `fix → build` edge at three iterations so one phase cannot churn forever on
review findings. The counter behind the cap is never reset when the run advances to the next phase, so
the three fixes are a budget for the **whole run**: a multi-phase spec whose early phases needed fixing
reaches a later phase with nothing left, and the first review finding there fails the run with work
unattempted.

## Context

### Source and standard of evidence

Filed 2026-09-28 on the user's instruction relayed by edit, from run
`1790608128-50-spec-to-pr-942b435a` (MUX-126, five phases). Evidence brief: `/tmp/loop-cap-defect.md`.
Everything under **Mechanism** was read from code at `408dce9` and from the run's persisted
`run.json`, not inferred from the brief; the brief's root cause was marked "to confirm" and is
confirmed.

### Observed

The run failed at 12:00:17 with two lifecycle rows:

```
graph-loop-exhausted  1790608128-50-spec-to-pr-942b435a: edge fix->build:success exhausted after 3 iterations
graph-run-failed      1790608128-50-spec-to-pr-942b435a: node fix loop cap exhausted with its edge suppressed — remaining work never attempted
```

The run's persisted counters at failure (`graphs/<run>/run.json`, `edge_fires`):

| Edge | Fires | Reading |
|------|-------|---------|
| `review->fix:failure` | 4 | four review failures across the run |
| `fix->build:success` | 3 | three fixes built; the fourth was suppressed |
| `phase-gate->commit:success` | 3 | Phases 1–3 committed (`9284f26`, `408dce9`, Phase 3) |
| `loop-check->implement:success` | 3 | the run had reached Phase 4 |

Fixes spent per phase: Phase 1 = 2 (the `ScrapeResumeSessionID` must-fix and its predecessor), Phase 2
= 1 (the silent-pass launch test), Phase 3 = 0, Phase 4 = 1 — the fourth fire. Phase 4's fix was
complete in the tree but never built, tested or reviewed; Phase 4's gate, Phase 5 and the close-out
never ran. No phase came near three fixes on its own.

### Mechanism — verified

| Fact | Where |
|------|-------|
| The cap is checked against `run.EdgeFires[key] >= e.MaxIterations` before an edge fires, and a fired edge increments it | `bus/graph_exec.go:2206`, `:2215` |
| `EdgeFires` lives on the run and is documented as counting "across daemon restarts" — run-scoped by design | `bus/graph_run.go:37–54` |
| The only code that ever clears an entry is `RetryGraphRun`, for edges downstream of the `--from` node | `bus/graph_run.go:665–668` |
| `50-spec-to-pr` loops phases through `commit → loop-check → implement`; the fix edge is a plain `"max_iterations": 3` | `bus/graph_templates.go:58`, `:68` |
| An exhausted cap with no other live edge fails the run, loudly (the MUX-121 rule) | `bus/graph_exec.go:2227–2236` |

So nothing resets the fix counter when `loop-check → implement` starts the next phase. The loudness is
working as designed — the run did not look complete — but the budget it enforces is the wrong one.

The phase-loop edges themselves are correctly run-scoped: `loop-check → implement` and
`stuck-gate → implement` carry `max_iterations_from_spec` (the phase count), a whole-run bound by
intent. Only the inner `build → test → review → fix` cycle is meant to be per-phase.

### Workaround

`muxcode graph retry <run> --from build` clears `EdgeFires` for every edge downstream of `build`,
including `fix → build`, and resumes the phase. It is a human intervention per exhaustion, and it gives
the retried phase a fresh budget only by accident of the downstream walk.

### Blast radius

Every multi-phase `50-spec-to-pr` run: the more phases, and the rougher the early ones, the more likely
a later phase starts with a spent budget. The other templates carrying `fix → build` capped at 3
(`graph_templates.go:143`, `:237`, `:265`) run a single pass, so a run-wide cap and a per-pass cap
coincide there — unaffected unless they gain a loop.

### Family

[MUX-121](../completed/MUX-121-multi-phase-sequential-graph.md) introduced the multi-phase loop and the
rule that a cap shortfall must fail loudly — this spec keeps that rule and fixes what the cap counts.
[MUX-167](../completed/MUX-167-spec-to-pr-commit-gate-before-phase-check.md) and
[MUX-183](../completed/MUX-183-phase-commit-ready-recredits-shipped-phases.md) are the same family of
run-state that fails to respect phase boundaries.
[MUX-178](../backlog/MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md) owns the red spawn sections of
`test-multi-phase-graph.sh`, which is why Phase 3 below writes a new script rather than extending it.

## Requirements

### Acceptance criteria

- [x] The `fix → build` budget in `50-spec-to-pr` is per phase: a run whose phases each need up to the cap in fixes completes every phase — test: a five-phase fixture where Phases 1 and 4 each fail review twice runs to close-out — Phase 2 `TestSpecToPRFixBudgetIsPerPhase` (the inverted pin): phases needing 3, 2, 0, 3 and 1 fixes — a harder case than the criterion names — reach close-out
- [x] **Negative control:** a single phase needing more fixes than the cap still stops, with `graph-loop-exhausted` naming the edge — the per-phase reset must not become an unbounded loop — `TestSpecToPRFixBudgetStopsOnePhase`, now for Phase 1 **and** for a phase entered after a reset
- [x] The phase-loop edges (`loop-check → implement`, `stuck-gate → implement`) keep their run-wide `max_iterations_from_spec` bound — the reset touches the inner cycle only — the per-phase test asserts the `loop-check` count untouched by the resets; `validateResets` forbids a reset naming an edge that itself resets
- [x] The reset survives a daemon restart mid-phase: the counter a resumed run reads is the current phase's, not zero and not the run's total — `TestSpecToPRFixBudgetSurvivesRestart`: persisted count 0 at phase entry, 1 mid-phase, and the reset row records the prior 2; the reset is applied in the same `run` value and `WriteGraphRun` as the fire (`graph_exec.go:2215–2219`)
- [x] Each reset is a lifecycle row naming the run, the phase entered and the edges cleared, so a budget refresh is visible in `lifecycle show` — `graph-loop-budget-reset` (run, resetting edge, phase entered, each cleared key with its prior count); four rows asserted across the five-phase run
- [x] `graph validate` keeps rejecting an uncapped cycle; a template that declares a per-phase cap validates — `TestValidateResetsIterations`; the uncapped-cycle check is unchanged and the builtin template validates with the new attribute
- [ ] `bash scripts/test-fix-loop-cap.sh` covers the per-phase budget and its negative control and passes

### Technical approach

**Reset on phase entry** (Decision 1). A new edge attribute, `"resets_iterations"`, lists the edge
keys whose `EdgeFires` entries are cleared when that edge fires. `50-spec-to-pr` declares
`"resets_iterations": ["fix->build"]` on both loop-back edges into the phase head —
`loop-check → implement` and `stuck-gate → implement` — so each phase starts with the full fix budget.
The reset is applied where `graph_exec.go:2215` records the fire, in the same `run` value and the same
`WriteGraphRun` as the increment, so a restart cannot observe the fire without the reset. It is declared
on the template, not hard-coded in the executor: the reset is an explicit, reviewable line, and any
future looping template opts in the same way.

The exhaustion path is **unchanged** (Decision 2): a phase that spends its whole budget still fails the
run loudly, per the MUX-121 rule (`graph_exec.go:2227`).

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | Cap check and fire record (`:2206`, `:2215`); exhaustion → run failed (`:2227`) |
| `tools/muxcode/bus/graph_run.go` | `GraphRun.EdgeFires` (`:54`), `RetryGraphRun`'s downstream reset (`:665`) |
| `tools/muxcode/bus/graph.go` | `GraphEdge` (`:79–91`) — the new attribute; validation (`:402–454`) |
| `tools/muxcode/bus/graph_templates.go` | `50-spec-to-pr` fix and loop-back edges (`:58`, `:67–68`) |
| `scripts/test-fix-loop-cap.sh` | New integration script (Phase 3) |
| `scripts/test-multi-phase-graph.sh` | Existing multi-phase fixture — a model for the new script, not extended (its spawn sections are red under MUX-178) |

## Implementation

### Phase 1: Pin

- [x] Unit test: a `50-spec-to-pr` run driven through three phases whose reviews fail 2, 1, 0 times, then one failure in Phase 4 → today fails with `graph-loop-exhausted` (pins the defect red) — `TestSpecToPRFixBudgetIsRunWide` (`bus/fix_loop_cap_test.go`): the run fails in Phase 4 with `fix->build` fired 3 times, one `graph-loop-exhausted` row naming `fix->build:success`, the fourth fix unbuilt and `close-spec` never reached. It asserts today's behaviour, so it passes now and Phase 2 inverts it. `fixLoopFixture` reduces the template using the builtin's **own** edges, so Phase 2's template change reaches the test unedited — 2026-09-28, run `1790623512`, review `1790623721` clean
- [x] Unit test: one phase failing review four times → exhausts (the negative control, green today, must stay green) — `TestSpecToPRFixBudgetStopsOnePhase`: exhausts at 3 fixes, the phase never completes

### Phase 2: Per-phase budget

- [x] Add `ResetsIterations []string` (`"resets_iterations"`) to `GraphEdge`, and clear those keys' `EdgeFires` entries in the executor when the edge fires — same `run` value, same `WriteGraphRun` as the increment — `Edge.ResetsIterations` (`graph.go:100`; entries `from->to` or `from->to:outcome`); `resetLoopBudgets` (`graph_exec.go:2250`) runs right after the increment, before the one `WriteGraphRun` — 2026-09-28, run `1790623512` lap 2, review `1790624067` (0 must-fix, 0 should-fix, 1 nit: a test comment's fix total)
- [x] Declare `"resets_iterations": ["fix->build"]` on `50-spec-to-pr`'s `loop-check → implement` and `stuck-gate → implement` edges — `graph_templates.go:67–68`
- [x] Lifecycle row on every reset (run, phase entered, edges cleared) — `graph-loop-budget-reset`
- [x] `graph validate`: accept the new attribute; still reject an uncapped cycle; reject a reset naming an edge that does not exist — `validateResets` also rejects, beyond the spec, a resetting edge with no cap of its own, a self-reset and chained resets, each of which would make the refreshed budget unbounded
- [x] Invert the Phase 1 pin to green; the negative control stays green — `TestSpecToPRFixBudgetIsRunWide` became `…IsPerPhase`; `…StopsOnePhase` extended to a post-reset phase
- [x] Restart test: persist mid-phase, reload the run, confirm the counter read is the current phase's — `TestSpecToPRFixBudgetSurvivesRestart`

### Phase 3: Integration test

- [ ] Create `scripts/test-fix-loop-cap.sh` (hermetic: scratch bus + real scratch daemon, modelled on `test-multi-phase-graph.sh` but without its spawn sections, which are red under MUX-178) with fake agents that fail review a scripted number of times per phase
- [ ] Test: five phases, Phases 1 and 4 each fail review twice → the run reaches close-out
- [ ] Test (negative control): one phase fails review cap+1 times → `graph-loop-exhausted` and `graph-run-failed`, later phases never dispatched
- [ ] Test: each phase entry writes one reset row
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and record the pass/fail counts here

## Decisions

Both resolved by the user on 2026-09-28, relayed by edit when the spec was started.

### Decision 1 — reset on phase entry (resolved)

`resets_iterations` declared on the loop-back edges, as in the technical approach. The alternative — a
counter keyed by spec phase (`EdgeFireKey(e) + "@" + phase`) — was not taken: it needs phase identity
derived at fire time, the HEAD-anchored question MUX-183 had to answer, where the reset keeps
`EdgeFires` a flat map and makes the refresh an explicit line in the template.

### Decision 2 — exhaustion keeps failing the run (resolved)

A phase that spends its whole fix budget fails the run, as today (`graph_exec.go:2227`). Routing
exhaustion to `stuck-gate` would change the MUX-121 contract and is not done here.

## Out of scope

- Routing an exhausted phase to `stuck-gate` instead of failing the run — Decision 2.
- The phase-loop edges' run-wide bound (`max_iterations_from_spec`) — correct as is.
- Single-pass templates' fix caps — run-wide and per-pass coincide there.
- MUX-178's spawn-worktree regression in `test-multi-phase-graph.sh`.

## Status

In Progress — 14/21 on 2026-09-28 15:4x: **Phases 1–2 complete**; acceptance criteria 6/7 (the
integration script is the one left). Phase 2 on run `1790623512`: `resets_iterations` on the loop-back
edges, `graph-loop-budget-reset`, stricter `validateResets`, the pin inverted and a restart test; review
`1790624067` passed with one nit. Phase 3 (`scripts/test-fix-loop-cap.sh`) next. Phase 1 `ae2070e`:
`bus/fix_loop_cap_test.go`, review `1790623721` clean first pass. Started 2026-09-28 15:2x on the user's instruction relayed by edit, on the MUX-126
branch (`MUX-126-edit-resume-aware-auto-restart`, PR #95) rather than a branch of its own; moved
`backlog/` → `drafts/`. Both decisions resolved at start: reset on phase entry, and exhaustion keeps
failing the run.

**Filed** 2026-09-28 on the user's instruction relayed by edit, from run `1790608128` (MUX-126), which
failed at Phase 4 with its fix budget spent by Phases 1–2. Mechanism verified by plan against `408dce9`
and the run's `edge_fires`.
