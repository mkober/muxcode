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

- [x] An open checkbox under a `### Deferred` heading inside `## Implementation` belongs to no phase:
      `SpecCurrentPhase` does not return its former phase on its account, and a spec whose only open
      item is deferred derives the next phase with open work, or none — Phase 3:
      `TestDeferredItemLetsDerivationMoveOn` (the next phase); MUX-130's
      `TestParseSpecPhases_DeferredBoxesBelongToNoPhase` (none)
- [x] The deferred item stays **visible and countable**: `SpecOpenItems` still counts it, and the
      close-spec guard still refuses to close the spec while it is open — the MUX-130 test (the
      count); `TestDeferredItemStillBlocksTheSpecCompleteGuard` (the decline names the item)
- [x] Negative control: the same open item left inside its `### Phase N` section pins derivation
      exactly as today — the in-phase control in `TestDeferredItemLetsDerivationMoveOn`
- [x] Derivation remains **stateless** — no stored phase pointer is reintroduced (MUX-121 decision 1)
      — Phase 3 changed no production code, and the same test flips the file back and forth and gets
      the answer re-derived each time
- [x] `docs/architecture.md` and the plan agent's spec conventions state that `### Deferred` is how an
      item is parked without holding the loop — Phase 3: all three files

Defect B — warn on a mismatch:

- [x] A launch whose intent names a phase that differs from the live `SpecCurrentPhase` **warns**,
      naming both phases; the run is created and no gate is added — Phase 2:
      `bus.PhaseIdentityWarning`; `TestPhaseIdentityWarning_WarnsOnADivergentIntent`
- [x] The warning is emitted from a **single shared surface** reaching both CLI and TUI, per the
      `UnscopedPhaseGuardWarning` precedent, and is also a lifecycle row on the run — one function
      called from `cmd/graph.go`, `tui/graph_ui.go` and `CreateGraphRun`; the row
      (`graph-phase-identity-mismatch`) is tested, the two call sites are by plan's code reading
- [x] A `phase-complete` guard that passes on the intent's phase while the live derivation names
      another phase says so in the guard's own lifecycle row — the pass is no longer silent, though
      it is still a pass — `graph-guard-phase-mismatch`; `TestPhaseCompleteGuard_LogsThePassOnTheIntentPhase`
- [x] Negative control: a run whose intent and derivation **agree** behaves exactly as today, with no
      warning — the agreeing control in the two tests above. One exception found in review, kept
      and stated: an intent naming **Phase 0** under a `phase-complete` guard still gets the
      unscoped-guard warning even when it agrees, because that guard does not check Phase 0
- [x] Negative control: a run whose intent names **no phase** keeps its current pass-through
      behaviour and existing warning, and gains no second one — the no-phase control in
      `TestPhaseIdentityWarning_WarnsOnADivergentIntent`

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

- [x] Characterization test, green on today's tree: a launch with intent "Phase 5" against a spec whose
      lowest open phase is 2 produces no warning from `UnscopedPhaseGuardWarning` —
      `TestUnscopedPhaseGuardWarning_SilentOnADivergentIntent_Characterization`; `PASS` in the 21:28
      suite log
- [x] Characterization test: with Phase 5 fully checked and Phase 2 open, `phaseCompleteGuardAllows`
      returns `true` for an intent naming Phase 5 —
      `TestPhaseCompleteGuard_PassesOnTheIntentPhaseNotTheDerivedOne_Characterization`; `PASS` in the
      same log, review 0/0/0
- [x] Characterization test: an open box under `### Deferred` inside a phase's section does not make
      that phase the current one today, and `SpecOpenItems` still counts it — already pinned by
      MUX-130's `TestParseSpecPhases_DeferredBoxesBelongToNoPhase` (in the suite since 2026-11-27,
      passing); not duplicated here
- [x] Record whether any existing test would have caught the mismatch (expected: none — no call site
      reads both identities), and which templates in the user's and this project's graph directories
      still set `"guard": "phase-complete"` — none would, and none does; see the findings

### Findings for Phase 1

Recorded 2026-11-27 from the Phase 1 worker's message (run `1795811929`). Plan checked the graph
directories, the builtin templates and the call sites; the two new tests first ran under `./test.sh`
at 21:28, and their steps were ticked then, at the run's verification.

| Finding | Detail |
|---------|--------|
| The two characterization tests | `bus/phase_identity_test.go`: `TestUnscopedPhaseGuardWarning_SilentOnADivergentIntent_Characterization` — intent "Phase 5", lowest open phase 2, and the launch warning is silent; `TestPhaseCompleteGuard_PassesOnTheIntentPhaseNotTheDerivedOne_Characterization` — Phase 5 ticked, Phase 2 open, and the `phase-complete` guard passes. Phase 2 inverts the first and makes the second log the mismatch while still passing |
| No existing test would have caught it | `IntentPhase` is exercised only in `spec_items_test.go`. `TestExecCurrentPhaseInterpolation` runs a diverging intent (Phase 1) against a derived Phase 2 but asserts only that the dispatch carries the derived phase. Nothing compares the two |
| No call site reads both identities | Non-test callers today: `IntentPhase` in `graph_exec.go` (the `phase-complete` guard) and `spec_items.go` (`UnscopedPhaseGuardWarning`); `SpecCurrentPhase` in `graph_exec.go` (twice) and `conditions.go`. No function calls both |
| No template sets `"guard": "phase-complete"` | On this machine neither `~/.config/muxcode/graphs` nor this project's `.muxcode/graphs` exists, and no builtin uses the guard. So Defect B's vacuous pass is reachable only from a user or project template someone writes — the launch warning and the guard's log line are for that case |
| The `### Deferred` rule is already pinned | By MUX-130's test: the deferred box is in no phase, makes no phase current, and still counts for `SpecOpenItems` |

### Phase 2: Warn on a mismatch (Defect B)

Independent of MUX-130; may land first.

- [x] Extend `UnscopedPhaseGuardWarning` (or add a sibling sharing its two call sites) to compare
      `IntentPhase(intent)` against the live `SpecCurrentPhase`, warning when both name a phase and
      they differ; the Phase 1 characterization test for the silent launch is inverted in the same
      change — the sibling `PhaseIdentityWarning`, at both call sites;
      `TestPhaseIdentityWarning_WarnsOnADivergentIntent`
- [x] The warning is also written as a lifecycle row when the run is created, so a run launched by an
      agent leaves it on record — `graph-phase-identity-mismatch` in `CreateGraphRun`;
      `TestCreateGraphRun_LogsAPhaseIdentityMismatch`, with an agreeing launch writing none
- [x] `phaseCompleteGuardAllows` logs the mismatch when it passes on the intent's phase while the
      derivation names another — it still passes (the decision is warn, not refuse) —
      `graph-guard-phase-mismatch`; `TestPhaseCompleteGuard_LogsThePassOnTheIntentPhase`, with a
      finished-spec control logging nothing
- [x] Negative controls: identities agree → silent; intent has no phase → the existing warning only
      — in the tests above
- [x] `docs/agent-bus.md` `muxcode graph run`: the warning and what it means — "Launch warnings",
      written by plan from the code: both warnings, the two lifecycle rows, and a table of which
      intent gets which warning

### Findings for Phase 2

Recorded 2026-11-27 at the run's verification (run `1795811929`). Plan read the code and the test
contracts; four tests `PASS` in the suite log
(`/tmp/muxcode-test-logs/20261127-214251-75538/muxcode.log`, no failure in the suite), `./test.sh`
passed 21:44, and the review returned 0 must-fix, 0 should-fix and 1 nit after an earlier round.

| Finding | Detail |
|---------|--------|
| The warning does not depend on the graph | `PhaseIdentityWarning(session, intent)` takes no graph: it fires on any launch whose intent names a phase other than the spec's lowest open one, including a `50-spec-to-pr` run, which has no `phase-complete` guard. Its text still speaks of that guard |
| It is a launch-time reading | The intent is frozen at launch; the warning and its row are computed once, then. A divergence that opens later in a run is reported only where a `phase-complete` guard passes (`graph-guard-phase-mismatch`) |
| **An intent naming Phase 0** — found in review | Since MUX-130 a Phase 0 is a real phase, but `phaseCompleteGuardAllows` still passes a Phase 0 intent through unchecked, as it does an intent naming none. Rather than change the guard, every diagnostic now says so: the unscoped-guard warning has a Phase 0 form ("which the guard does not check"), the mismatch warning ends "passes Phase 0 through unchecked", and the guard's row claims no item check. `IntentPhase` still returns 0 for both; `intentPhaseNumber` tells them apart. `TestPhaseIdentityWarning_PhaseZeroIsReportedTruthfully` |
| That the guard does not check Phase 0 is left as it is | It is reported, not fixed. Whether a `phase-complete` guard should check a named Phase 0 is not something this spec's decisions cover |
| The TUI shows both warnings together | The run browser's notice joins the unscoped-guard warning and the mismatch warning when both apply; it showed only the first before |
| The review's nit | Against the first version of the docs block, which said agreeing identities get no warning: an agreeing Phase 0 intent under the guard still gets the unscoped-guard warning. The block now carries a table of which intent gets which warning |

### Phase 3: Bless `### Deferred` (Defect A)

**Lands with [MUX-130](../completed/MUX-130-spec-phase-parsing-semantics.md)'s Phase 3**, which rewrites the
heading rule this depends on. Do not land it against the unscoped parser and then again against the
scoped one.

- [x] Unit test pinning the rule under MUX-130's scoped parser: boxes under `### Deferred` inside
      `## Implementation` belong to no phase; a spec whose only open item is deferred reports the next
      open phase, or none — `TestDeferredItemLetsDerivationMoveOn` (Phase 2 becomes current);
      MUX-130's `TestParseSpecPhases_DeferredBoxesBelongToNoPhase` (no current phase)
- [x] Negative control: the same item inside its `### Phase N` section pins derivation exactly as
      today — the same test's in-phase fixture reads Phase 1
- [x] Test that the deferred item is still counted by `SpecOpenItems` and still declines the
      spec-complete guard — the MUX-130 test (count of 1); `TestDeferredItemStillBlocksTheSpecCompleteGuard`
      (declines naming the item; ticked, it allows)
- [x] Verify statelessness is preserved — the property comes from re-reading the spec, not from
      storing a cursor — the same test writes parked, in-phase, parked and reads 2, 1, 2
- [x] Doc line in `docs/architecture.md` (graph section) and in the plan agent's spec conventions
      (`agents/planner.md`, `skills/docs-management.md`): `### Deferred` parks an item without holding
      the loop; the item still blocks close-out until ticked or annotated as deferred to another spec
      — `docs/architecture.md`: the "What holds the loop" and "What still stops the close" rows,
      written with MUX-130; `agents/planner.md`: "Parked in this spec" under What to check off;
      `skills/docs-management.md`: the same rule with an example — both by the worker, read by plan

### Findings for Phase 3

Recorded 2026-11-27 from the Phase 3 worker's message (run `1795811929`), and the four test steps
ticked at the run's verification: plan read both tests, and both `PASS` in the suite log
(`/tmp/muxcode-test-logs/20261127-214830-35169/muxcode.log`, the `bus` package run uncached, no
failure in the suite); `./test.sh` passed 21:50.

| Finding | Detail |
|---------|--------|
| `TestDeferredItemLetsDerivationMoveOn` | A parked item under `### Deferred` inside `## Implementation`: the current phase moves on to Phase 2. Control — the same item inside Phase 1 pins Phase 1. Statelessness — flipping the file back and forth flips the answer, re-derived on every read with no stored cursor |
| `TestDeferredItemStillBlocksTheSpecCompleteGuard` | The spec-complete guard declines, naming the deferred item. Control — ticked, it allows |
| Already covered by MUX-130 | `TestParseSpecPhases_DeferredBoxesBelongToNoPhase`: a spec whose only open item is deferred has no current phase, and `SpecOpenItems` still counts the item |
| No production code changed | The rule was already in the parser; this phase adds tests and the two convention lines |

### Phase 4: Integration test

- [x] Create `scripts/test-phase-identity.sh` with end-to-end verification against a scratch bus,
      scratch daemon on a private tmux server and fixture spec — plus a scratch git repo, HOME and
      lifecycle log; each dispatch is read per run id from the bus log
- [x] Test: a run whose intent phase and derived phase **diverge** prints the warning at launch and
      leaves the lifecycle row; the run is created — section B (5 checks), which also reads the
      dispatch: `Implement Phase 2: Config and CDK` under a Phase 5 intent
- [x] Test: a spec whose only open item sits under `### Deferred` derives the next phase with open work
      rather than re-deriving the parked one, and the run's loop moves on — section E: implement is
      dispatched for Phase 2
- [x] Test: `close-spec` on that spec is still refused, naming the deferred item — section G: a node
      under the `spec-complete` guard fails, with one new `graph-guard-declined` row naming the item
- [x] Test (negative control): intent and derivation agreeing runs with no warning and no new gate —
      section C: no warning, no mismatch row, implement dispatched straight away
- [x] Test (negative control): an intent naming no phase keeps the existing unscoped-guard warning —
      section D: that warning alone, exactly one warning line
- [x] Test (negative control): the parked item moved back inside its phase holds the loop on that
      phase — section F: implement is dispatched for Phase 1
- [x] Coverage floor so a skipped section cannot report green — exactly 23 passes, else exit 1
- [x] Run the script and verify all checks pass — 23 passed, 0 failed, 2026-11-27 21:53

### Findings for Phase 4

Recorded 2026-11-27 from the Phase 4 worker's message (run `1795811929`). Plan read the script and
the run agent's reply and log (`/tmp/mux143-phase-identity.log`: 23 `PASS` lines, no `FAIL`), and
counted the script's checks by section against its floor.

| Finding | Detail |
|---------|--------|
| The run | `bash scripts/test-phase-identity.sh` by the run agent, 21:53, against a muxcode built from the tree: 23 passed, 0 failed, floor 23, exit 0 |
| Checks by section | A setup 3 · B divergent intent 5 · C agreeing control 4 · D no-phase control 4 · E parked item 2 · F item back in its phase 2 · G close-out refused 3 |
| What "the loop moves on" is measured by | The phase the `implement` dispatch names. Sections E and F launch the same graph and intent on two fixtures differing only in where the item sits, and read Phase 2 and Phase 1 |
| What "close-spec" is here | A fixture node carrying the `spec-complete` guard — the guard a close-out node runs under — not the `55-defect-backlog-loop` template's own node. The decline row's count is taken before and after, so the row naming the item is this section's |
| No stale dispatch can satisfy a check | Every dispatch is selected by its run id from the append-only bus log, and each section cancels its run |
| `CLAUDE.md` | The script's entry in the test list was added by the worker |

## Status

Complete

Closed out 2026-11-27 by `55-defect-backlog-loop` run `1795811929` at 33/33: four phases and all ten
acceptance criteria.

| What shipped | |
|--------------|-|
| A launch warning (Defect B) | `bus.PhaseIdentityWarning`: an intent naming a phase other than the active spec's lowest open one warns at launch, naming both, on the CLI and in the run browser. The run is created and no gate is added |
| A record of it | `CreateGraphRun` logs `graph-phase-identity-mismatch`; a `phase-complete` guard that passes on the intent's phase while the spec's lowest open phase is another logs `graph-guard-phase-mismatch` — still a pass |
| `### Deferred` (Defect A) | Blessed as the way to park an item: its boxes belong to no phase, so the loop moves on, and they still stop the close. The rule itself shipped with [MUX-130](../completed/MUX-130-spec-phase-parsing-semantics.md); this spec added tests pinning it and the convention lines in `agents/planner.md` and `skills/docs-management.md` |
| Documentation | `docs/agent-bus.md`, "Launch warnings" under `muxcode graph run` |
| `scripts/test-phase-identity.sh` | Scratch daemon, real git repo, private tmux server; 23 passed, 0 failed, floor 23, against a binary built from the tree |

Left as recorded, none of it open work under this spec:

- A `phase-complete` guard still passes an intent naming **Phase 0** through unchecked. Every
  diagnostic says so; whether the guard should check a named Phase 0 is outside this spec's
  decisions (see [Findings for Phase 2](#findings-for-phase-2)).
- The mismatch warning fires on any launch whose intent names another phase, including a
  `50-spec-to-pr` run, which carries no `phase-complete` guard; its text still speaks of that guard.
- No builtin template uses the `phase-complete` guard, so the guard's log row matters only to a
  user or project template that sets it.
- The integration script's close-out section drives a fixture node under the `spec-complete` guard,
  not the `55-defect-backlog-loop` template's own `close-spec` node.

How it got here. Filed 2026-09-03 from a live observation relayed from another session and
repository: the narrative (Phase 5 run, Phase 2 prompt, user challenge) is **relayed and not
reproducible here**, while every mechanism claim in the tables above was **verified by plan against
this repo's source** and is marked as such. The two defects were filed together because they were
observed together — the parked box (A) produced the divergence the guard then failed to catch (B) —
though each is reachable alone. Activated 2026-11-27 at 0/33, the day MUX-130 closed, and verified
phase by phase the same day; `./test.sh` passed at 21:55 and the last review returned 0 must-fix,
0 should-fix and 0 nits. The total is 33, not the 34 the index carried: the Context section quotes
one checkbox inside a code fence, which a plain count includes and the close-out guard
(`SpecOpenItems`) rightly skips.

Unparked 2026-11-24 on the user's decisions, relayed by edit and recorded in
[Decisions](#decisions-user-2026-11-24): Defect A is `### Deferred`, landing with MUX-130's Phase 3;
Defect B warns and does not refuse. The three questions this section used to ask — warn or refuse,
which mechanism, split or keep — are answered there: warn, `### Deferred`, one spec with a phase per
defect. Depends on [MUX-130](../completed/MUX-130-spec-phase-parsing-semantics.md) for Phase 3 only.
