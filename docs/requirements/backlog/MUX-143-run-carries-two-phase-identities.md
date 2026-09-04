# A Run Carries Two Unreconciled Phase Identities

A graph run labelled **Phase 5** handed its `implement` worker the prompt **"Phase 2: Config and
CDK"**. The worker began implementing Phase 2 on a run whose stated purpose was Phase 5, and noticed
only because the user challenged it — *"why are you working on p2 it should be p5"*.

Nothing in the system reconciles the two. A run carries **two independent phase identities** derived
from two different sources, consumed by two different sets of nodes, and never compared. The commit
guard whose entire stated purpose is to scope a ship to what the run claimed to deliver validates the
identity the worker was **not** given.

Tracking: _(no GitHub issue yet)_

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
> That is the same problem solved by convention. Whether the fix here is to bless that convention
> mechanically, or to add explicit item-level syntax, is the design question this spec opens.

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
| [`MUX-130`](./MUX-130-spec-phase-parsing-semantics.md) | **Adjacent, not the same.** MUX-130 covers phase *parsing* semantics — how headings and completion counts are read. This is **not** a parsing bug: parsing is correct in the observed case (Phase 2 genuinely has an open box). The defect is **semantic** — two correct parses of two different things, used as if they were the same thing. Keep separate; cross-linked both ways. |
| [`MUX-142`](./MUX-142-spawn-worker-delegates-into-wrong-tree.md) | Same family: a graph worker given a **wrong picture of what it is working on**. MUX-142 is the wrong *tree*; this is the wrong *phase*. Independent mechanisms, shared consequence — a worker proceeds confidently on a false premise. |
| [`MUX-121`](../completed/MUX-121-multi-phase-sequential-graph.md) | Owns the stateless-derivation decision this spec must not regress. Defect A's fix cannot reintroduce stored phase state. |

### Why it matters

The commit guard is a **ship gate**. It is the last mechanical check between a worker's output and a
push, and it is one of the gates the graph validator insists on (a commit node must sit downstream of
a `wait_human`). A gate that validates the wrong subject while reporting success is worse than no
gate: it consumes the human's attention budget and returns a guarantee it did not check.

## Requirements

### Acceptance criteria

- [ ] A phase-identity mismatch between the frozen intent and the live derivation is **detected and
      surfaced**, not silently tolerated
- [ ] The commit guard cannot pass **vacuously** on a phase the worker was never given
- [ ] A checkbox that is open but **not agent-actionable** is expressible, and does not pin derivation
      forever
- [ ] Derivation remains **stateless** — no stored phase pointer is reintroduced (MUX-121 decision 1)
- [ ] The mismatch warning is emitted from a **single shared surface** reaching both CLI and TUI, per
      the `UnscopedPhaseGuardWarning` precedent
- [ ] Negative control: a run whose intent and derivation **agree** behaves exactly as today, with no
      new gate in the path
- [ ] Negative control: a run whose intent names **no phase** keeps its current pass-through
      behaviour and existing warning

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

- [ ] Write a failing test that constructs a run with intent "Phase 5" against a spec whose lowest
      open phase is 2, and asserts the mismatch is currently undetected
- [ ] Confirm the vacuous pass: with Phase 5 fully checked and Phase 2 open,
      `phaseCompleteGuardAllows` returns `true`
- [ ] Record whether any existing test would have caught this (expected: none — no call site reads
      both identities)

### Phase 2: Detect and surface the mismatch (Defect B)

- [ ] Extend `UnscopedPhaseGuardWarning` (or add a sibling sharing its call sites) to compare
      `IntentPhase(intent)` against the live `SpecCurrentPhase`, warning when both are non-zero and
      differ
- [ ] Decide with the user whether a mismatch **warns** or **refuses** at the commit gate — a refusal
      is a new blocking gate and needs an explicit ruling, not a default
- [ ] Ensure the warning reaches both CLI and TUI launch surfaces from the one function
- [ ] Add negative controls: identities agree → silent; intent has no phase → existing behaviour

### Phase 3: Express a non-actionable open item (Defect A)

- [ ] Choose the mechanism with the user: bless the `### Deferred` non-`Phase` heading convention
      mechanically, or add explicit item-level syntax (e.g. a marker `SpecPhases()` recognises)
- [ ] Implement so a parked item is **visible and countable** but does not pin `SpecCurrentPhase`
- [ ] Verify statelessness is preserved — the property comes from re-reading the spec, not from
      storing a cursor
- [ ] Negative control: an ordinary open item still pins derivation exactly as today

### Phase 4: Integration test

- [ ] Create `scripts/test-phase-identity.sh` with end-to-end verification against a scratch bus,
      scratch daemon and fixture spec
- [ ] Test: a run whose intent phase and derived phase **diverge** surfaces the mismatch at launch and
      at the commit gate
- [ ] Test: the **vacuous pass** is closed — Phase 5 complete + Phase 2 open no longer ships silently
- [ ] Test: a spec whose only open item is **parked** derives the next actionable phase rather than
      livelocking
- [ ] Test (negative control): intent and derivation agreeing runs to completion with no new warning
      and no new gate
- [ ] Test (negative control): an intent naming no phase keeps the existing unscoped-guard warning
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Status

**Backlog** — filed 2026-09-03 from a live observation relayed from another session and repository.
The narrative (Phase 5 run, Phase 2 prompt, user challenge) is **relayed and not reproducible here**;
every mechanism claim in the tables above was **verified by plan against this repo's source** and is
marked as such.

No implementation has started. Two defects are filed together because they were observed together and
compound each other — the parked box (A) is what produced the divergence that the guard then failed to
catch (B) — but they are **independently reachable** and could be split:

- **Defect B** is reachable whenever intent and derivation diverge for *any* reason, parked box or
  not: a stale intent, a spec edited mid-run, a phase completed by a parallel path.
- **Defect A** is reachable with no graph run at all — any consumer of `SpecCurrentPhase`, including
  the `spec_phases_remaining` condition, sees the same livelock.

Open questions for the user:

- **Warn or refuse?** Phase 2 asks whether a detected mismatch blocks the commit gate. A refusal is a
  new blocking gate in a ship path and is the user's call, not a default to be picked here.
- **Which mechanism for Defect A?** Blessing the existing `### Deferred` convention is cheap and
  already in use in this repo (MUX-136); explicit item syntax is more precise but touches the parser
  MUX-130 is separately reworking. Sequencing against MUX-130 matters.
- **Split or keep?** As above — coherent separately, but the evidence trail is shared.
