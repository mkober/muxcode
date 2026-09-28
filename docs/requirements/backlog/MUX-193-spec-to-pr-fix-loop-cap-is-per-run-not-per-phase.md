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
[MUX-178](./MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md) owns the red spawn sections of
`test-multi-phase-graph.sh`, which Phase 3 below extends.

## Requirements

### Acceptance criteria

- [ ] The `fix → build` budget in `50-spec-to-pr` is per phase: a run whose phases each need up to the cap in fixes completes every phase — test: a five-phase fixture where Phases 1 and 4 each fail review twice runs to close-out
- [ ] **Negative control:** a single phase needing more fixes than the cap still stops, with `graph-loop-exhausted` naming the edge — the per-phase reset must not become an unbounded loop
- [ ] The phase-loop edges (`loop-check → implement`, `stuck-gate → implement`) keep their run-wide `max_iterations_from_spec` bound — the reset touches the inner cycle only
- [ ] The reset survives a daemon restart mid-phase: the counter a resumed run reads is the current phase's, not zero and not the run's total
- [ ] Each reset is a lifecycle row naming the run, the phase entered and the edges cleared, so a budget refresh is visible in `lifecycle show`
- [ ] `graph validate` keeps rejecting an uncapped cycle; a template that declares a per-phase cap validates
- [ ] `bash scripts/test-multi-phase-graph.sh` (or a new `scripts/test-fix-loop-cap.sh`) covers the per-phase budget and its negative control and passes

### Technical approach

Two shapes; Decision 1 chooses.

1. **Reset on phase entry.** When an edge into the phase-loop head fires (`loop-check → implement`,
   and `stuck-gate → implement`), clear `EdgeFires` for the edges of the inner cycle. Declared on the
   template, not hard-coded: an edge attribute such as `"resets_iterations": ["fix->build"]` on the
   loop-back edge, applied where `graph_exec.go:2215` records the fire. Small, and the reset is an
   explicit, reviewable line in the template.
2. **Scope the counter.** Give the fix edge `"max_iterations_scope": "phase"` and key its counter by
   the spec phase it fired in (`EdgeFireKey(e) + "@" + phase`). No reset step exists to be missed, and
   a restart reads the right counter for free — but phase identity must be derived at fire time, which
   is the HEAD-anchored question MUX-183 had to answer for `phaseCommitReady`.

Either way the exhaustion path is unchanged: loud failure, per the MUX-121 rule. Whether an exhausted
phase should route to `stuck-gate` (ask a human) rather than fail the run is Decision 2.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | Cap check and fire record (`:2206`, `:2215`); exhaustion → run failed (`:2227`) |
| `tools/muxcode/bus/graph_run.go` | `GraphRun.EdgeFires` (`:54`), `RetryGraphRun`'s downstream reset (`:665`) |
| `tools/muxcode/bus/graph.go` | `GraphEdge` (`:79–91`) — the new attribute; validation (`:402–454`) |
| `tools/muxcode/bus/graph_templates.go` | `50-spec-to-pr` fix and loop-back edges (`:58`, `:67–68`) |
| `scripts/test-multi-phase-graph.sh` | Multi-phase fixture to extend (Phase 3) |

## Implementation

### Phase 1: Pin

- [ ] Unit test: a `50-spec-to-pr` run driven through three phases whose reviews fail 2, 1, 0 times, then one failure in Phase 4 → today fails with `graph-loop-exhausted` (pins the defect red)
- [ ] Unit test: one phase failing review four times → exhausts (the negative control, green today, must stay green)

### Phase 2: Per-phase budget

- [ ] Implement the Decision 1 shape in the executor and the `GraphEdge` schema
- [ ] Declare it on `50-spec-to-pr`'s fix edge / loop-back edges
- [ ] Lifecycle row on every reset (run, phase entered, edges cleared)
- [ ] `graph validate`: accept the new attribute; still reject an uncapped cycle; reject a reset naming an edge that does not exist
- [ ] Invert the Phase 1 pin to green; the negative control stays green
- [ ] Restart test: persist mid-phase, reload the run, confirm the counter read is the current phase's

### Phase 3: Integration test

- [ ] Extend `scripts/test-multi-phase-graph.sh` (or create `scripts/test-fix-loop-cap.sh` if MUX-178's red spawn sections make the shared script unusable) with fake agents that fail review a scripted number of times per phase
- [ ] Test: five phases, Phases 1 and 4 each fail review twice → the run reaches close-out
- [ ] Test (negative control): one phase fails review cap+1 times → `graph-loop-exhausted` and `graph-run-failed`, later phases never dispatched
- [ ] Test: each phase entry writes one reset row
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and record the pass/fail counts here

## Open decisions

### Decision 1 — reset on entry, or a phase-scoped counter?

Reset-on-entry is the smaller change and keeps `EdgeFires` a flat map; the scoped counter removes the
reset step entirely and is restart-safe by construction, at the price of deriving phase identity at
fire time. Recommendation: reset-on-entry, declared on the loop-back edge, unless Phase 1's restart
test shows a reset can be lost between the fire and the write.

### Decision 2 — should an exhausted phase ask a human instead of failing the run?

Today exhaustion fails the run (`graph_exec.go:2227`). A `fix → stuck-gate` route on exhaustion would
turn "three fixes did not converge" into a question, matching how an incomplete phase is already
handled. It changes the MUX-121 contract, so it is out of this spec's default scope; the user decides.

## Out of scope

- The phase-loop edges' run-wide bound (`max_iterations_from_spec`) — correct as is.
- Single-pass templates' fix caps — run-wide and per-pass coincide there.
- MUX-178's spawn-worktree regression in `test-multi-phase-graph.sh`.

## Status

Backlog — filed 2026-09-28 on the user's instruction relayed by edit, from run `1790608128` (MUX-126),
which failed at Phase 4 with its fix budget spent by Phases 1–2. Mechanism verified by plan against
`408dce9` and the run's `edge_fires`. Not started.
