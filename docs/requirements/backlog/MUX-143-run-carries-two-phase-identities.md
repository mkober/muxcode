# A Run Carries Two Unreconciled Phase Identities

**Tracking:** [mkober/muxcode#114](https://github.com/mkober/muxcode/issues/114)

A graph run labelled **Phase 5** handed its `implement` worker the prompt **"Phase 2: Config and
CDK"**. The worker began implementing Phase 2 on a run whose stated purpose was Phase 5, and noticed
only because the user challenged it — *"why are you working on p2 it should be p5"*.

Nothing in the system reconciles the two. A run carries **two independent phase identities** derived
from two different sources, consumed by two different sets of nodes, and never compared. The commit
guard whose entire stated purpose is to scope a ship to what the run claimed to deliver validates the
identity the worker was **not** given.

## Decisions (user, 2026-11-24)

Relayed by edit. The spec is split by defect, and both questions it left for the user are answered.

| Defect | Decision |
|--------|----------|
| A — a parked checkbox pins derivation | **Bless `### Deferred`.** A non-`Phase` `###` heading is the way to park an item: boxes under it belong to no phase, so they cannot hold `SpecCurrentPhase`. No item-level syntax is added. Delivered as a test and a doc line, **landing with [MUX-130](../completed/MUX-130-spec-phase-parsing-semantics.md)'s Phase 3 scoping** — one change to the heading rule in `parseSpecPhases` |
| B — intent phase and derived phase disagree | **Warn, do not refuse.** When the launch intent names a phase and the live derivation names another, the launch warns. No new blocking gate is added to the ship path |

The two defects stay in this one spec, as separate phases. Defect A's phase is done when MUX-130's
Phase 3 lands with its test; Defect B's is independent of MUX-130.

State of the code on 2026-11-24, read by plan before recording the above:

- A non-`Phase` `###` heading **already** ends the current phase in `parseSpecPhases`
  (`bus/spec_items.go` — any heading shallower than `####` resets the cursor). So `### Deferred`
  works today by accident of the rule; what is missing is a test that pins it, a doc line that says
  it is the mechanism, and its survival through MUX-130's scoping rewrite.
- An item under `### Deferred` still counts for `SpecOpenItems`, so the spec cannot be closed out
  while it is open. That is intended: parked is visible and countable, not done.
- **No builtin template carries the `phase-complete` guard any more.** `50-spec-to-pr` commits under
  `phase-progress`, which [MUX-183](../completed/MUX-183-phase-commit-ready-recredits-shipped-phases.md)
  anchored on HEAD's copy of the spec — it ships the phase that is complete in the tree and not at
  HEAD, whatever the intent says. `phaseCompleteGuardAllows` and `IntentPhase` are still in the code
  and still scope to the frozen intent, so the vacuous pass described under Defect B is reachable
  only from a project or user template that sets `"guard": "phase-complete"`.
- `UnscopedPhaseGuardWarning` is unchanged: it warns only when the intent names no phase, and is
  called from both launch surfaces (`cmd/graph.go`, `tui/graph_ui.go`).

## Context

### How this surfaced

Observed live 2026-09-03 in a subsession on a `PBP1-` branch (pnpm/TypeScript, a **different
repository**), relayed to plan through edit as pane captures. The narrative figures are *relayed* and
not reproducible here; the **mechanism** was verified independently against muxcode source in this
repo and is recorded with provenance below.

### Defect A — a human-parked checkbox livelocks phase derivation

| Fact | How established |
|------|-----------------|
| `SpecCurrentPhase` returns the lowest-numbered phase with **any** open checkbox | **Verified** — `bus/spec_items.go:121-123` and its doc comment |
| `${current_phase}` resolves through it on every dispatch | **Verified** — `bus/graph_exec.go:486-500` (`resolveCurrentPhaseText`) |
| Derivation is deliberately stateless, recomputed per tick, never stored | **Verified** — `bus/spec_items.go:74-79`, citing MUX-121 decision 1 |

Statelessness is correct and was hard-won — it is what stops a completed phase being re-implemented
(the failure observed three times on 2026-08-28). The gap is that **nothing distinguishes unfinished
work from work deliberately parked on a human decision.** In the observed spec, Phase 2's single open
box was:

```
- [ ] piiAllowedRoles for stage01/prod01 — currently [] (fail-closed). Populate
      with the pkh-Operations GUID 8428a4e8-… only on a user decision
```

That box cannot be ticked by any agent, by construction. Every `spec-to-pr` run therefore re-derives
Phase 2 forever and no later phase is ever reachable while it stays open. The loop **livelocks on
human-blocked work with no signal that it is doing so** — it does not fail, it does not warn, it
simply keeps handing out a phase nobody can close.

The workaround the subsession reached for — "move it into Phase 6 where it belongs" — is a
spec-authoring convention, not a fix. The tool should be able to express *open, but not actionable by
you*.

> This repo already has the shape of an answer. MUX-136 carries a `### Deferred` section holding
> items blocked on environment, placed under a **non-`Phase` heading** precisely so `SpecPhases()`
> (`bus/spec_items.go:97-107`) resets on it and they cannot hold the spec open or re-spawn a worker.
> That is the same problem solved by convention. The user chose on 2026-11-24 to bless that
> convention rather than add item-level syntax — see [Decisions](#decisions-user-2026-11-24).

### Defect B — the commit guard validates a phase the worker was never given

This is the sharper half. The two identities:

| Identity | Source | Consumers |
|----------|--------|-----------|
| `${current_phase}` | `SpecCurrentPhase` — **live**, lowest open phase in the spec | `implement`, `fix`, `update-spec` nodes |
| `IntentPhase(run.Intent)` | parsed from the **frozen** intent string captured at run creation | `phaseCompleteGuardAllows` — the commit gate |

| Fact | How established |
|------|-----------------|
| `phaseCompleteGuardAllows` scopes entirely to `IntentPhase(run.Intent)` | **Verified** — `bus/graph_exec.go:281-282` |
| Its stated purpose is to "scope a ship to what the run claimed to deliver, nothing more" (user decision 2026-08-28) | **Verified** — doc comment, `bus/graph_exec.go:276-280` |
| `IntentPhase` returning 0 passes the guard straight through | **Verified** — `bus/graph_exec.go:282-284` |
| The two are never compared at any call site | **Verified** — `IntentPhase` has two non-test callers (`spec_items.go:182`, `graph_exec.go:282`); `SpecCurrentPhase` has two (`graph_exec.go:496`, `conditions.go:353`). No call site reads both |

**The consequence.** In the observed run, intent = Phase 5, derived = Phase 2. The worker was told to
implement Phase 2; the commit guard checked Phase 5. If Phase 5 happens to have no open items, the
guard **passes vacuously** and the commit ships Phase 2 work under a Phase 5 claim. The guard's entire
stated property is void precisely in the case it most needs to catch.

The failure is silent in both directions: the parked Phase 2 box that pins the derivation is
simultaneously **invisible to the guard**, because the guard only ever looks at the intent's phase.

### There is already a warning surface to extend

`UnscopedPhaseGuardWarning` (`bus/spec_items.go:180-190`) is a **partial** reconciliation and the
natural home for this check. It warns when a graph carries a phase-complete guard but the intent names
no phase at all — `IntentPhase(intent) != 0` returns early. It does **not** compare the intent's phase
against the live derivation, so divergence between two *non-zero* phases passes unremarked.

Its doc comment records why it exists: *"Shared by both launch surfaces — CLI and TUI — so neither can
drift (plan finding 2026-08-28: the CLI warned, the TUI did not)."* Extending it keeps that
single-surface property rather than adding a third place a warning can go missing.

### Relationship to existing specs

| Spec | Relationship |
|------|--------------|
| [`MUX-130`](../completed/MUX-130-spec-phase-parsing-semantics.md) | **Adjacent, and a dependency.** MUX-130 covers phase *parsing* semantics — how headings are read. This is **not** a parsing bug: parsing is correct in the observed case (Phase 2 genuinely has an open box). The defect is **semantic** — two correct parses of two different things, used as if they were the same thing. Kept as a separate spec, but Defect A's `### Deferred` rule lives in the heading rule MUX-130's Phase 3 rewrites, so it lands with that phase. |
| [`MUX-142`](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) | Same family: a graph worker given a **wrong picture of what it is working on**. MUX-142 is the wrong *tree*; this is the wrong *phase*. Independent mechanisms, shared consequence — a worker proceeds confidently on a false premise. |
| [`MUX-121`](../completed/MUX-121-multi-phase-sequential-graph.md) | Owns the stateless-derivation decision this spec must not regress. Defect A's fix cannot reintroduce stored phase state. |

### Why it matters

The commit guard is a **ship gate**. It is the last mechanical check between a worker's output and a
push, and it is one of the gates the graph validator insists on (a commit node must sit downstream of
a `wait_human`). A gate that validates the wrong subject while reporting success is worse than no
gate: it consumes the human's attention budget and returns a guarantee it did not check.

## Requirements

### Acceptance criteria

Defect A — `### Deferred`:

- [ ] An open checkbox under a `### Deferred` heading inside `## Implementation` belongs to no phase:
      `SpecCurrentPhase` does not return its former phase on its account, and a spec whose only open
      item is deferred derives the next phase with open work, or none
- [ ] The deferred item stays **visible and countable**: `SpecOpenItems` still counts it, and the
      close-spec guard still refuses to close the spec while it is open
- [ ] Negative control: the same open item left inside its `### Phase N` section pins derivation
      exactly as today
- [ ] Derivation remains **stateless** — no stored phase pointer is reintroduced (MUX-121 decision 1)
- [ ] `docs/architecture.md` and the plan agent's spec conventions state that `### Deferred` is how an
      item is parked without holding the loop

Defect B — warn on a mismatch:

- [ ] A launch whose intent names a phase that differs from the live `SpecCurrentPhase` **warns**,
      naming both phases; the run is created and no gate is added
- [ ] The warning is emitted from a **single shared surface** reaching both CLI and TUI, per the
      `UnscopedPhaseGuardWarning` precedent, and is also a lifecycle row on the run
- [ ] A `phase-complete` guard that passes on the intent's phase while the live derivation names
      another phase says so in the guard's own lifecycle row — the pass is no longer silent, though
      it is still a pass
- [ ] Negative control: a run whose intent and derivation **agree** behaves exactly as today, with no
      warning
- [ ] Negative control: a run whose intent names **no phase** keeps its current pass-through
      behaviour and existing warning, and gains no second one

### Key files

| File | Relevance |
|------|-----------|
| `bus/spec_items.go` | `SpecPhases()`, `SpecCurrentPhase()`, `IntentPhase()`, `UnscopedPhaseGuardWarning()` — both identities and the existing warning surface |
| `bus/graph_exec.go` | `phaseCompleteGuardAllows()` (:281), `resolveCurrentPhaseText()` (:486) — the two consumers |
| `bus/conditions.go` | `spec_phases_remaining` condition (:353) — the third `SpecCurrentPhase` reader |
| `cmd/graph.go` | Launch surface where the mismatch is first knowable |
| `bus/graph_run.go` | Where `run.Intent` is frozen at creation |

## Implementation

### Phase 1: Pin the divergence

- [ ] Characterization test, green on today's tree: a launch with intent "Phase 5" against a spec whose
      lowest open phase is 2 produces no warning from `UnscopedPhaseGuardWarning`
- [ ] Characterization test: with Phase 5 fully checked and Phase 2 open, `phaseCompleteGuardAllows`
      returns `true` for an intent naming Phase 5
- [ ] Characterization test: an open box under `### Deferred` inside a phase's section does not make
      that phase the current one today, and `SpecOpenItems` still counts it
- [ ] Record whether any existing test would have caught the mismatch (expected: none — no call site
      reads both identities), and which templates in the user's and this project's graph directories
      still set `"guard": "phase-complete"`

### Phase 2: Warn on a mismatch (Defect B)

Independent of MUX-130; may land first.

- [ ] Extend `UnscopedPhaseGuardWarning` (or add a sibling sharing its two call sites) to compare
      `IntentPhase(intent)` against the live `SpecCurrentPhase`, warning when both name a phase and
      they differ; the Phase 1 characterization test for the silent launch is inverted in the same
      change
- [ ] The warning is also written as a lifecycle row when the run is created, so a run launched by an
      agent leaves it on record
- [ ] `phaseCompleteGuardAllows` logs the mismatch when it passes on the intent's phase while the
      derivation names another — it still passes (the decision is warn, not refuse)
- [ ] Negative controls: identities agree → silent; intent has no phase → the existing warning only
- [ ] `docs/agent-bus.md` `muxcode graph run`: the warning and what it means

### Phase 3: Bless `### Deferred` (Defect A)

**Lands with [MUX-130](../completed/MUX-130-spec-phase-parsing-semantics.md)'s Phase 3**, which rewrites the
heading rule this depends on. Do not land it against the unscoped parser and then again against the
scoped one.

- [ ] Unit test pinning the rule under MUX-130's scoped parser: boxes under `### Deferred` inside
      `## Implementation` belong to no phase; a spec whose only open item is deferred reports the next
      open phase, or none
- [ ] Negative control: the same item inside its `### Phase N` section pins derivation exactly as
      today
- [ ] Test that the deferred item is still counted by `SpecOpenItems` and still declines the
      spec-complete guard
- [ ] Verify statelessness is preserved — the property comes from re-reading the spec, not from
      storing a cursor
- [ ] Doc line in `docs/architecture.md` (graph section) and in the plan agent's spec conventions
      (`agents/planner.md`, `skills/docs-management.md`): `### Deferred` parks an item without holding
      the loop; the item still blocks close-out until ticked or annotated as deferred to another spec

### Phase 4: Integration test

- [ ] Create `scripts/test-phase-identity.sh` with end-to-end verification against a scratch bus,
      scratch daemon on a private tmux server and fixture spec
- [ ] Test: a run whose intent phase and derived phase **diverge** prints the warning at launch and
      leaves the lifecycle row; the run is created
- [ ] Test: a spec whose only open item sits under `### Deferred` derives the next phase with open work
      rather than re-deriving the parked one, and the run's loop moves on
- [ ] Test: `close-spec` on that spec is still refused, naming the deferred item
- [ ] Test (negative control): intent and derivation agreeing runs with no warning and no new gate
- [ ] Test (negative control): an intent naming no phase keeps the existing unscoped-guard warning
- [ ] Test (negative control): the parked item moved back inside its phase holds the loop on that
      phase
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Status

**Backlog** — filed 2026-09-03 from a live observation relayed from another session and repository.
The narrative (Phase 5 run, Phase 2 prompt, user challenge) is **relayed and not reproducible here**;
every mechanism claim in the tables above was **verified by plan against this repo's source** and is
marked as such.

No implementation has started. Two defects are filed together because they were observed together and
compound each other — the parked box (A) is what produced the divergence that the guard then failed to
catch (B) — and they are **independently reachable**:

- **Defect B** is reachable whenever intent and derivation diverge for *any* reason, parked box or
  not: a stale intent, a spec edited mid-run, a phase completed by a parallel path.
- **Defect A** is reachable with no graph run at all — any consumer of `SpecCurrentPhase`, including
  the `spec_phases_remaining` condition, sees the same livelock.

Unparked 2026-11-24 on the user's decisions, relayed by edit and recorded in
[Decisions](#decisions-user-2026-11-24): Defect A is `### Deferred`, landing with MUX-130's Phase 3;
Defect B warns and does not refuse. The three questions this section used to ask — warn or refuse,
which mechanism, split or keep — are answered there: warn, `### Deferred`, one spec with a phase per
defect. Depends on [MUX-130](../completed/MUX-130-spec-phase-parsing-semantics.md) for Phase 3 only.
