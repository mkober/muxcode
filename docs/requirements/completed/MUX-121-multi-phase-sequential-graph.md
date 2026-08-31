# Sequential Multi-Phase Graph — One Run Delivers a Whole Spec

`req-code-pr` ships **one phase per run**. The intended shape is a single run that walks a spec's
phases in order: implement a phase, update the spec, commit the work and the spec (with the user
approving each commit), then start the next phase — and one final gate at the end where the user
approves the push and PR.

Observed 2026-08-28: three consecutive runs all carried the intent `MUX-115 … — Phase 1: Turn trace`.
The second re-implemented an already-complete phase from scratch and produced a **parallel,
incompatible implementation** that would have overwritten a verified one on harvest. Running one
phase per run is not just inconvenient; it is how that near-miss happened.

Tracking: _(no GitHub issue yet)_

## Context

### Intended flow

```
                ┌────────────────── next phase ──────────────────┐
                │                                                │
  implement ─→ build ─→ test ─→ review ─→ update-spec ─→ gate ─→ commit ─┘
  (phase N)      └─ fix loop ─┘            (plan)      (approve   (work +
                                                        commit)    spec)
                                                                     │
                                          all phases complete ───────┘
                                                     │
                                              final gate ─→ push ─→ pr
                                            (approve push+PR)
```

Per the maintainer (2026-08-28): **the user approves each commit**, and a final gate covers push and
PR. That resolves what would otherwise be the blocking constraint — see below.

### What already works

| Capability | Status |
|---|---|
| Capped loop edges (`max_iterations`) | ✅ `Edge.MaxIterations`; uncapped cycles are a validation error |
| Branching | ✅ `condition` nodes via `EvaluateConditions` |
| Human gates | ✅ `wait_human` |
| Spec update before the gate | ✅ `update-spec` node (added 2026-08-28) |
| Per-phase dispatch guard | ✅ `phase-complete` guard, `SpecPhaseOpenItems` |

### The gate rule is satisfied by the maintainer's choice

`nodeRequiresGate` (`graph.go:157`) returns true for **any** send/spawn/map to the `commit` role
except `pr-read` — it does not distinguish a local commit from a push. So ungated per-phase commits
would fail `graph validate` outright.

Because the user approves each commit, every commit node sits behind its own `wait_human` and the
rule is satisfied naturally. **No change to the authority model is needed, and none should be made** —
weakening `nodeRequiresGate` to allow "local commits only" would be a safety regression for a
convenience this design does not require.

### Two genuine gaps — neither is a template edit

**1. The run intent is immutable.** `Intent` is set once in `CreateGraphRun` (`graph_run.go:174`) and
thereafter only read (`:461`). No production path mutates it; only a test helper does. But the intent
is what carries `Phase N` — it drives `IntentPhase()`, the `phase-complete` guard, and the
`${intent}` interpolated into every node message. A loop that re-enters `implement` would re-run
**the same phase forever**, which is exactly the failure already observed three times.

Something must advance the run's notion of "current phase" per iteration.

**2. No condition can test spec state.** *(Closed 2026-08-28 by `spec_phases_remaining`; kept as the problem statement.)* The ten condition types were all git/command/env shaped —
`files_match`, `branch_match`, `command_match`, `env_set`, `output_contains`, `exit_code`, and their
negations. **None can ask "does this spec have another incomplete phase?"** So loop termination
cannot be expressed today.

The predicate itself already exists — `SpecPhaseOpenItems` and `SpecOpenItems` — but it is reachable
only from the dispatch guard, not from the condition dialect.

## Decisions (2026-08-28, maintainer)

| # | Decision |
|---|----------|
| 1 | **Phase derivation: stateless** — the lowest-numbered phase with open items, computed at each `implement` dispatch. No new run field |
| 2 | **Loop termination:** a new `spec_phases_remaining` condition type reusing `SpecOpenItems`, keeping one dialect. **Loop cap derived** from the spec's phase count at run creation; truncation surfaced loudly |
| 3 | **Extend `req-code-pr`** itself — no new template; the single-phase flow is superseded |
| 4 | **Stuck phase: gate and ask** — a `wait_human` parks the run naming the stuck phase and why; the user picks retry / skip (recorded) / stop. Never silent |
| 5 | **`nodeRequiresGate` unchanged** — per-commit approval already satisfies the gate rule |

### Loop-safety analysis — the answer is *bounded, but not yet loud*

The design **cannot loop forever**: `run.EdgeFires` is persisted per edge (`graph_run.go:46`) and
`max_iterations` is enforced against it before an edge fires (`graph_exec.go:639`), emitting a
`graph-loop-exhausted` lifecycle row. A never-completing phase therefore consumes the cap and stops.

**But two gaps sit between that and decision 4, and both must be closed for the design to hold.**

**(a) Cap exhaustion terminates as an apparent success.** When the loop edge is exhausted no edge
fires, and `graph_exec.go:656` treats `fired == 0` as a run failure *only if the last node's outcome
was failure*; the comment is explicit that "a success with no edges is a normal terminal path." So a
spec whose Phase 2 never completes burns its cap re-running Phase 2, its final `commit` succeeds, and
the run ends looking **normally complete** with Phases 3–4 never attempted. The only trace is an
`info`-level lifecycle row. That is the opposite of decision 2's *"truncation surfaced loudly"* —
loud must mean the run itself fails or gates, not a log line.

**(b) The gate-and-ask has no trigger.** "Lowest phase with open items" carries **no progress
signal** — it returns the same phase forever when that phase never completes, and nothing in it can
tell iteration 5 from iteration 1. Detecting *"this phase did not advance"* is inherently a
comparison across iterations, which pure stateless derivation does not provide.

**A trigger that needs no new run field**, preserving decision 1: at each `implement` dispatch,
compare `run.EdgeFires[<loop edge>]` — iterations already completed, and persisted — against the
count of **complete** phases in the spec. If completed phases < iterations, the previous iteration
finished without closing its phase, so gate and ask. Both inputs already exist; nothing new is
stored.

Without (b), decision 4 never fires and the design degrades to (a): silent truncation at the cap.

## Open decisions

- [x] **How does the current phase advance?** Candidates: a mutable `run.CurrentPhase` the executor
      increments on loop re-entry; or derive it each iteration as *the lowest-numbered phase with
      open items* (stateless, self-correcting, and needs no new field). The second is likely better —
      it cannot drift from the spec, and a phase re-opened by review is picked up automatically.
- [x] **How is loop termination expressed?** A new condition type (`spec_phases_remaining`) reusing
      `SpecOpenItems`, versus a dedicated node type. A condition keeps one dialect, which
      [MUX-014](../completed/MUX-014-graph-agent-orchestrator.md) was careful to preserve.
- [x] **What is the loop cap?** `max_iterations` must be set. Too low silently truncates a long spec;
      too high risks a runaway. Should it be derived from the phase count?
- [x] **Extend `req-code-pr` or add a new template?** Changing it alters behaviour for existing
      users; a new `spec-all-phases` template leaves the single-phase flow intact.
- [x] **What happens when a phase cannot complete?** Stop the run, skip to the next phase, or gate
      and ask? Silent skipping would produce a "complete" run with unfinished phases.

## Requirements

### Acceptance criteria

- [x] One run walks a multi-phase spec from the first incomplete phase to the last, in order
- [ ] Each phase's work **and** its spec update are committed together, after the user approves that
      commit
- [x] The spec is updated **before** each commit, so no commit records a stale spec
- [x] The next phase does not start until the previous phase's commit lands
- [x] A phase that is already complete is **not re-implemented** — the failure observed three times
      on 2026-08-28
- [x] The loop terminates when no incomplete phase remains, without a fixed guess at phase count
- [x] A final `wait_human` gate covers push and PR, and nothing pushes before it
- [x] Every commit node remains downstream of a `wait_human` gate — `graph validate` still passes and
      `nodeRequiresGate` is **unchanged**
- [x] The loop is capped; an uncapped cycle still fails validation
- [x] A phase that cannot be completed surfaces per the Phase 1 decision, never silently skipped
- [x] `go vet ./...` and `go test ./...` green in both modules — **2607 pass / 0 fail / 1 skip**, vet clean, `-count=1` with no `-run` filter (2026-08-28 16:17). Timing verified independently: the tree has been stable since 16:09:52, so the run postdates every change

### Technical approach

Prefer the **stateless** phase derivation: at each `implement` dispatch, compute the current phase as
the lowest-numbered phase in the active spec with open items. This needs no new run field, cannot
drift from the spec, and self-corrects when a phase is re-opened. It also composes with the existing
`phase-complete` guard, which already reads the same source.

Loop termination then becomes "no phase has open items" — the `SpecOpenItems` predicate the
close-spec guard already uses ([MUX-114](../completed/MUX-114-close-spec-node-has-no-completion-check.md)).
Exposing it as a condition keeps one dialect rather than adding a second.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_run.go` | `CreateGraphRun`, run store — where `Intent`/phase state lives |
| `tools/muxcode/bus/graph_exec.go` | Executor, loop edges, dispatch, guards |
| `tools/muxcode/bus/graph.go` | `Edge.MaxIterations`, validation, `nodeRequiresGate` (**do not weaken**) |
| `tools/muxcode/bus/conditions.go` | Condition types — now 11 with `spec_phases_remaining` (added 2026-08-28) |
| `tools/muxcode/bus/spec_items.go` | `SpecOpenItems`, `SpecPhaseOpenItems`, `IntentPhase` |
| `tools/muxcode/bus/graph_templates.go` | `req-code-pr`, or a new `spec-all-phases` template |

## Implementation

### Phase 1: Decide the loop mechanics

- [x] Resolve the five open decisions above — recorded in the Decisions table
- [x] Confirm the chosen phase-derivation cannot loop forever on a phase that never completes — **bounded** by `EdgeFires`/`max_iterations`, but see the loop-safety analysis: cap exhaustion currently ends as an *apparent success*, and gate-and-ask has no trigger
- [x] Record the design in `docs/architecture.md` — *Sequential multi-phase runs (design)* under the graph control-plane section

### Phase 2: Phase derivation

- [x] Implement current-phase derivation from the active spec — `SpecPhases()` + `${current_phase}` resolved at dispatch (`interpolateGraphMessage`), so the frozen intent is no longer the carrier
- [x] Ensure an already-complete phase is skipped rather than re-implemented — derivation returns the lowest phase with **open** items, so a complete phase is structurally unreachable
- [x] Unit tests including the re-implementation case observed on 2026-08-28 — `TestExecPhaseProgressGuard`; full suite **2606 / 0 fail / 1 skip** at 15:36:26, 60s after the last source change (verified by mtime, not asserted)

### Phase 3: Loop termination

- [x] Expose spec-phase state to the condition dialect — `spec_phases_remaining` registered in the validation map and dispatch; transient resolution counts as *remaining* (wrongly continuing costs one bounded iteration; wrongly terminating loses work)
- [x] Cap the loop; verify an uncapped variant still fails validation — `TestValidateDerivedLoopCap`, green in the same suite run

### Phase 4: Template

- [x] Wire implement → build/test → review → update-spec → gate → commit → loop — `implement→build→test→review→update-spec→phase-gate→commit→loop-check→implement`, with `fix→build` capped at 3 and `stuck-gate→implement` on its own spec-derived budget
- [x] Final gate covering push and PR — `loop-check→final-gate→push-pr`; both commit-role nodes (`commit`, `push-pr`) sit downstream of a `wait_human`, so the authority rule holds with `nodeRequiresGate` unchanged
- [x] Verify `graph validate` passes and every commit is gate-dominated — live `muxcode graph validate req-code-pr`: **12 nodes, 15 edges, OK**; node/edge counts independently reparsed from the template, and both commit-role nodes (`commit`, `push-pr`) sit downstream of a `wait_human`

### Phase 5: Integration test

- [x] Create `scripts/test-multi-phase-graph.sh` (hermetic: scratch bus + tmux + daemon + spec) — dumps node states + task store on the **first** failure, before teardown
- [x] **Headline**: a 3-phase fixture spec runs to completion in **one** run, committing after each
      phase, with the phases executed in order — *"one run walked all 3 phases to complete"*, plus `commit 1/2/3 names Phase 1/2/3`
- [x] Test: a spec whose Phase 1 is already complete **starts at Phase 2** — the re-implementation
      guard ✅
- [x] Test: the loop stops when no phase has open items, without a hardcoded count ✅
- [x] Test: nothing pushes before the final gate; approving it releases push and PR ✅ (two checks)
- [x] **Negative control**: an ungated commit node still fails `graph validate` — the authority rule
      is intact ✅
- [x] **Negative control**: an uncapped loop edge still fails validation ✅
- [x] Test: a phase that cannot complete behaves per the Phase 1 decision, and does not silently
      advance — declined into the stuck gate, and the withheld commit never reached the commit role
- [x] Coverage floor so a skipped run cannot report green — **19**, itemized in the script and verified against the actual pass list (a floor of 25 was an imagined count that failed every complete run; the correction did not mask a failure — all 19 passed)
- [x] Run the script and verify all checks pass — **20 passed / 0 failed, exit 0** (2026-08-28 16:14), floor met at 19 substantive checks; six runs to get here, the first two finding a capture bug and a real phase-2 dispatch defect

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Frozen intent drives the loop | Re-runs the same phase forever — already observed three times | Phase 2 derivation, not the intent string |
| Weakening `nodeRequiresGate` | A safety regression for convenience this design does not need | Explicit criterion: rule unchanged, negative control |
| Loop cap too low | Silently truncates a long spec and reports success | Derive from phase count; surface truncation loudly |
| Phase never completes | Infinite loop, or a silent skip producing a false "done" | Phase 1 decision + integration test |
| Commit-per-phase fatigue | Many gates in one run; the human rubber-stamps | Gate text must name the phase and what it commits |

## Known gap at close-out

**One acceptance criterion is closed unverified, by explicit maintainer decision (2026-08-28), and
it is left unchecked on purpose.**

| Criterion | Why it is not verified | How it will be proven |
|-----------|------------------------|-----------------------|
| *Each phase's work **and** its spec update are committed together, after the user approves that commit* | `scripts/test-multi-phase-graph.sh` is hermetic: it substitutes **send nodes** for the spawns and runs against a scratch bus with **no git repository**. A send node records that a commit was *dispatched* and what it was *told to commit* — it cannot observe what a commit *contains*. Re-running the suite any number of times cannot close this; the test structurally lacks the evidence | **Organic proof on first real use**: the first genuine `req-code-pr` phase commit must contain both the code change and its spec update. Inspect that commit rather than adding a test the harness cannot support |

Everything else is verified against execution, not inspection: **36 of 37 items**, the integration
suite at **20 passed / 0 failed / exit 0** with its coverage floor met at 19 itemized checks, and the
unit suite at **2607 / 0 / 1** (`-count=1`, no `-run` filter) on a tree confirmed stable since
16:09:52 — so the run postdates every change it claims to cover.

The distinction matters and is the reason this row exists rather than a tick: the template ordering
(`update-spec` → `phase-gate` → `commit`) makes the criterion *very likely* true, and "the wiring
implies it" is exactly the reasoning that produced the defect
[MUX-114](../completed/MUX-114-close-spec-node-has-no-completion-check.md) was written to fix.

## Post-close observation (2026-08-31): `${completed_phase}` is correct, the divergence is real

A follow-up was reported as *"`${completed_phase}` interpolates stale on loop re-entry — second pass
still said Phase 3 after Phase 4 completed"* (run `1788200907-req-code-pr-34c9a0d1`). **The
interpolation is not stale, and filing it as such would send someone after a bug that does not
exist.**

`resolveCompletedPhaseText` (`graph_exec.go:340`) reads the **live active spec at dispatch** and calls
`SpecJustCompletedPhase`, which walks phases in order and breaks at the first one holding open items,
returning the phase before it (`spec_items.go:144`). At the time of the report,
[MUX-117](./MUX-117-pane-targeting-by-identity.md) Phase 4 held **one open item** —
delivery-reaches-agents, left open deliberately because `clear` and `compact` cannot be exercised
without side effects on a live agent. So Phase 3 *was* the last fully-completed phase, and the
message was accurate.

What is real is the divergence underneath the report: **the graph's notion of "Phase 4 done" and the
spec's can disagree.** The run had looped past its Phase 4 work (`implement` running a second pass,
`commit`/`loop-check` done from the first) while the spec still showed Phase 4 open. Worth pinning:

- [ ] Confirm whether the `phase-progress` guard fires when the loop advances past a phase the spec
      still shows open — this is the case it exists for
- [ ] Decide the intended semantics when the two disagree: does the graph defer to the spec, or is a
      deliberately-open item a legitimate reason to hold the loop?
- [ ] If they may legitimately diverge, the gate message should say so rather than naming a phase
      number that reads as a mistake

Recorded here rather than as a new defect spec because the reported symptom is correct behaviour;
the underlying question belongs to this spec's loop semantics.

## Status

Complete — closed on the [known gap](#known-gap-at-close-out) above, 2026-08-28. One
[post-close observation](#post-close-observation-2026-08-31-completed_phase-is-correct-the-divergence-is-real)
added 2026-08-31; it does not reopen the spec.

Closed at **36 of 37 items**. The single open criterion is unverifiable by the test harness that
covers the rest, not merely untested; the note above records what would settle it.
