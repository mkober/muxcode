# Spec Phase Parsing: Two Definitions of Complete, Matched Document-Wide

**Tracking:** [mkober/muxcode#57](https://github.com/mkober/muxcode/issues/57)

The graph orchestrator reasons about "which phase is done" through four predicates in
`bus/spec_items.go`. **Two of them define "complete" incompatibly, and all four match `### Phase N`
anywhere in the file rather than inside the implementation section.** On the repo's own specs the two
definitions disagree for **8 of 76** specs carrying phases, and the disagreement runs in the unsafe
direction: the `phase-progress` guard can be inflated into permitting a per-phase commit it exists to
decline.

> **Read first: [State of the code on 2026-11-24](#state-of-the-code-on-2026-11-24).** The Context
> below is the finding as filed on 2026-08-31. [MUX-183](../completed/MUX-183-phase-commit-ready-recredits-shipped-phases.md)
> has since replaced two of the four predicates, which closes Defect A and narrows Defect B; Defects
> C, D and E stand. The user settled the open decisions on 2026-11-24.

> **Adjacent spec — lands together.**
> [`MUX-143`](../backlog/MUX-143-run-carries-two-phase-identities.md) covers a run carrying **two unreconciled
> phase identities** (live `SpecCurrentPhase` vs frozen `IntentPhase`). This spec owns how a phase is
> **read**; MUX-143 owns which phase a run **means**. They meet in the parser: MUX-143's Defect A is
> settled as blessing a non-`Phase` `### Deferred` heading, which is the same heading rule this spec's
> Phase 3 scoping rewrites — so **MUX-143's Defect A lands with this spec's Phase 3**, in one change
> to `parseSpecPhases`, and MUX-143 keeps its dependency on this spec.

## Context

### Where this came from

[MUX-121](../completed/MUX-121-multi-phase-sequential-graph.md) closed with a post-close observation
(2026-08-31) recording that "the graph's notion of *Phase 4 done* and the spec's can disagree", and
left three questions open inside a spec marked Complete — so nothing tracked them. This spec picks
those up, and the investigation found the divergence is **not** a judgement call about graph-vs-spec
authority. It is three mechanical defects in the parser, each independently verifiable.

MUX-121's note is correct on the point it argued: `${completed_phase}` was **not** stale in run
`1788200907-req-code-pr-34c9a0d1`. That analysis stands. What it did not examine is why the two
predicates can disagree at all.

### Defect A — two incompatible definitions of "complete"

| Predicate | Definition | Used by |
|---|---|---|
| `SpecCompletedPhaseCount` (`spec_items.go:162`) | Counts **every** phase with zero open items, anywhere in the file — non-contiguous, no `break` | `phaseProgressGuardAllows` (`graph_exec.go:273`) — the commit guard |
| `SpecJustCompletedPhase` (`spec_items.go:144`) | Walks file order and **breaks at the first phase with open items** — contiguous prefix | `resolveCompletedPhaseText` (`graph_exec.go:343`) — the `${completed_phase}` gate message |

A spec with Phase 1 ✓, Phase 2 ✗ (one open item), Phase 3 ✓ yields `CompletedPhaseCount = 2` and
`JustCompletedPhase = Phase 1`. The guard reasons over one number and the message the user approves
shows the other.

### Defect B — `### Phase N` is matched document-wide, not scoped

`SpecPhases` (`spec_items.go:80`) resets its cursor on any `#` heading but only ever *appends* on a
`### Phase N` match. It has no notion of which `##` section it is inside. The repo's own convention
is to document phase outcomes in a `## Verification notes` section using `### Phase N (date) — 4/4
steps` headings — **so every such spec parses its verification notes as a second set of
implementation phases.**

[`completed/MUX-031-graph-run-tui.md`](../completed/MUX-031-graph-run-tui.md) is the clearest case —
verified against the file, not inferred:

```
## Implementation      → ### Phase 1..7           (7 real phases)
## Verification notes  → ### Phase 1..7, 7, 7     (8 more parsed as phases)
```

`SpecPhases` returns **15 entries** for a 7-phase spec; `SpecCompletedPhaseCount` returns **14**.
A guard comparing `completed < prior+1` against 14 on a 7-phase spec is not comparing anything real.

**8 specs** in `backlog/` and `completed/` carry `### Phase N` headings outside an `## Implementation`
section today.

### Defect C — duplicate and non-monotonic phase numbers are silently accepted

`SpecPhases` neither dedupes nor requires ascending order.
[`completed/MUX-109`](../completed/MUX-109-prompt-mode-graph-control-pane.md) parses as
`(2,·) (1,·) (2,·) (3,·) (4,·) (5,·) (6,·) (7,·) (7,·) (7,·)` — phase 2 appears **before** phase 1 in
file order, and phase 7 three times. `SpecJustCompletedPhase` walks **file order** while
`SpecCurrentPhase` (`:123`) picks the **lowest number**, so with duplicates the two disagree about
what ordering even means.

### Defect D — `Number 0` doubles as the "unset" sentinel, so a real Phase 0 is invisible

Found 2026-09-01 while verifying [`MUX-131`](../completed/MUX-131-spawn-implement-output-never-ported.md),
which numbers its phases from **0**.

Both predicates use the zero value of `SpecPhase` as "none found", and `SpecPhase.Number` is `0` in that
zero value — so a genuine `### Phase 0` is indistinguishable from *no phase*:

| Predicate | Code | Consequence |
|---|---|---|
| `SpecCurrentPhase` | `if len(p.Items) > 0 && (best.Number == 0 \|\| p.Number < best.Number)` | A Phase 0 selected as `best` has `Number == 0`, which still reads as "unset", so **any later open phase overwrites it**. Phase 0 can never be reported as the current phase while a later phase is also open. |
| `SpecJustCompletedPhase` | returns `last := SpecPhase{}` when nothing qualifies | `resolveCompletedPhaseText` (`graph_exec.go`) treats `p.Number == 0` as `(no completed phase)`. If **only** Phase 0 is complete, the gate message says nothing is complete while Phase 0 genuinely is. |

**It did not bite MUX-131 only by luck**: Phase 1 closed in the same pass, so `SpecJustCompletedPhase`
returned Phase 1 and the gate rendered correctly. Had Phase 0 closed alone — the ordinary case for a
spec that starts at 0 — the human gate would have been asked to approve a commit labelled
`(no completed phase)`.

The fix is to stop overloading the value: carry an explicit "found" boolean, or use a sentinel outside
the valid phase range. Contiguous-prefix semantics (decision 1) do not resolve this — the prefix length
would be correct while the *named* phase is still wrong.

### Defect E — the phase view can say "done" while the close guard says "not done"

Observed live 2026-09-01 on [`MUX-131`](../completed/MUX-131-spawn-implement-output-never-ported.md): all
five phases were complete while **two acceptance criteria remained open**, because those criteria live
under `## Requirements` — an `##` heading, so `SpecPhases` assigns them to no phase.

The two mechanisms then disagree about the same spec:

| Predicate | Answer | Consumer |
|---|---|---|
| `SpecCurrentPhase` | `(no open phase)` | `${current_phase}` in gate messages |
| `SpecOpenItems` | **2** | the close-spec guard ([MUX-114](../completed/MUX-114-close-spec-node-has-no-completion-check.md)) |

Both are internally correct — phase predicates answer *"which phase is open"*, `SpecOpenItems` answers
*"is anything open"*. The defect is what a **human** is shown: a `wait_human` gate rendered
`… steps of (no open phase)`, which reads as *the spec is finished* at the exact moment the close guard
would refuse to close it.

This is the concrete form of the question [MUX-121](../completed/MUX-121-multi-phase-sequential-graph.md)
left open — *"the gate message should say so rather than naming a phase number that reads as a
mistake"* — now with a live instance and a named cause.

Note this is **not** fixed by the contiguous-prefix decision, and not by Defect D's sentinel fix either:
both concern phases. Unscoped items are invisible to every phase predicate by construction.

**Second occurrence, 2026-09-02 on [`MUX-134`](../completed/MUX-134-status-bar-fkey-label-diverges-from-binding.md)** —
and it sharpens the diagnosis. A `verify-spec` dispatch rendered:

> Verify the implemented changes against the active requirements spec and check off completed criteria
> and steps of **(no open phase)** — the commit gate follows, so the spec must reflect reality before it

Here the placeholder was **factually accurate**: MUX-134 really had zero open items (23 ticked, 0 open).
That is what makes it the stronger example. The first occurrence could be read as a *parsing* bug —
open criteria hidden from the phase predicates. This one has nothing hidden and the sentence is still
malformed, because `${current_phase}` is interpolated into a slot that assumes a phase **name**, so a
sentinel meaning *"there is no phase"* lands where a noun belongs. The message instructs an agent to
check off "the steps of (no open phase)", which names no work and cannot be acted on.

So the fix is not only "make the predicates agree". A **sentinel value must never be interpolated into
prose as if it were a name** — the gate needs a distinct message for the no-open-phase case, chosen
before substitution rather than papered over by making the sentinel read better. Verified at the time
of observation: the spec was genuinely closed, the script backing it was still untracked, and nothing
needed ticking — so the dispatch was not merely ugly, it was asking for work that did not exist.

### Invariant worth stating: phase order is *file order*, never edit order

All four predicates walk `SpecPhases`, which appends in the order headings appear in the file. Nothing
records when a checkbox was ticked, so **the sequence in which phases are completed cannot affect any
of them.** A spec whose Phase 1 is closed before its Phase 0 reads identically to one closed in order.

Stated because it is the natural worry when phases close out of sequence (asked during MUX-131's
Phase 0/1 close-out, 2026-09-01), and because it bounds this spec's scope: every defect here is a
property of *how the file is parsed*, not of *when it was edited*. The one thing that does depend on
file order is `SpecJustCompletedPhase`'s prefix walk — which is why Defect C (non-monotonic numbering)
is a real problem while out-of-order *completion* is not.

### Why this matters: the guard fails permissive

`phaseProgressGuardAllows` declines when `completed < prior+1` — "commits shipped must not exceed
phases closed". Defect B inflates `completed`. An inflated `completed` **satisfies** the comparison,
so the guard passes. Its own doc comment says a decline is "the gate-and-ask trigger"; an inflated
count removes the trigger silently.

This is the same failure class as [MUX-114](../completed/MUX-114-close-spec-node-has-no-completion-check.md)
(a close-spec node that shipped without a completion check) and MUX-124 / MUX-006 (the diagnostic
that misleads): the mechanism built to catch a mistake is the mechanism that stops catching it.

### Evidence: what is verified, and what is not

**Verified** — by reading the source and by replicating both predicates over every spec in
`docs/requirements/` (76 with phases):

- [x] The two definitions differ in code (`break` vs no `break`) — direct read, not inference
- [x] They disagree on **8 of 76** real specs
- [x] MUX-031's duplicate headings are real file structure (`## Verification notes`), confirmed by
      reading the headings with their enclosing `##` sections
- [x] 8 specs carry phase headings outside an `## Implementation` section
- [x] Two of the divergent specs — [`MUX-120`](../completed/MUX-120-spawn-worker-never-woken-for-seeded-task.md)
      and [`MUX-128`](../backlog/MUX-128-fkey-navigation-for-spawn-windows.md) — are in `backlog/` **now**, so
      either could become the active spec

**Not verified — do not treat as established:**

- That a live run has actually mis-permitted a commit through this path. **Not established, and
  not pursued** (user, 2026-11-27): the defect was found by reading the code, and no live-run
  evidence exists; the argument is from code and reachability only. Stated as a note, not a task —
  it was a checkbox until then, which no step could ever have ticked
- [x] That Go's behaviour matches the Python replication used for the survey. The replication mirrors
      the fence handling, both regexes, and the trimmed-vs-raw line distinction, but **it was not run
      against the Go implementation.** Phase 1 exists to close this gap with a Go test rather than
      inherit the replica's authority — **now verified, 2026-11-27**: `TestSpecPhaseSurvey` checks the
      Go parser against an independent heading walk on every spec and reproduces the 2026-11-24
      table ([Findings for Phase 1](#findings-for-phase-1))

## State of the code on 2026-11-24

Read by plan in `bus/spec_items.go` and `bus/graph_exec.go` before the decisions below were recorded.
Line numbers elsewhere in this spec are from 2026-08-31 and no longer hold.

| Defect | Then | Now |
|--------|------|-----|
| A — two definitions of complete | `SpecCompletedPhaseCount` (non-contiguous count) fed the guard, `SpecJustCompletedPhase` (prefix walk) fed `${completed_phase}` | **Closed by MUX-183.** Both functions are gone. `phaseCommitReady` is the one predicate behind the `phase-progress` guard, the `spec_phase_committable` condition and `${completed_phase}`: a phase complete in the working tree and not complete in HEAD's copy of the spec (`newlyCompletedPhases`, `specAtHEAD`). The three cannot disagree |
| B — `### Phase N` matched document-wide | every phase heading anywhere counted, and an empty one counted as complete | **Narrowed, not closed.** `parseSpecPhases` still appends every `### Phase N` in the file. But `SpecPhase.Complete()` now needs at least one ticked box and none open (MUX-183 defect 2), so a narrative `### Phase 3 (date) — 4/4 steps` heading is empty, not complete, and no longer inflates the count. A phase heading outside the implementation section that **holds checkboxes** still parses as a phase |
| C — duplicate and non-monotonic numbers | silently accepted | **Stands.** `newlyCompletedPhases` keys shipped phases by number, `SpecPhaseOpenItems` sums duplicate sections, `SpecCurrentPhase` picks the lowest number |
| D — `Number 0` as the unset sentinel | Phase 0 invisible to both predicates | **Half closed.** `${completed_phase}` now reads `phaseCommitVerdict.ready`, so a completed Phase 0 is named. `SpecCurrentPhase` still tests `best.Number == 0`, and `resolveCurrentPhaseText` and `conditions.go` still read `p.Number == 0` as "none" |
| E — a sentinel interpolated as a phase name | `… steps of (no open phase)` | **Stands.** `resolveCurrentPhaseText` returns `(no open phase)` into the same slot |

Survey of `docs/requirements/` on 2026-11-24 (a Python replica of the heading rule, **not** run
against the Go parser — Phase 1 reproduces it in Go):

| Measure | Count | Which |
|---------|-------|-------|
| Specs carrying `### Phase N` | 184 | — |
| …with one outside a `## Implementation…` section | 8 | all in `completed/`: MUX-031, 069, 071, 077, 094, 105, 109, 117 |
| …of those, whose outside headings hold checkboxes | 2 | MUX-069 (`## Design`, `## Success criteria`), MUX-077 (`## Acceptance criteria`) |
| …in `backlog/` or `drafts/` | 0 | the two backlog specs named in 2026-08-31's survey (MUX-120, MUX-128) no longer show it |
| Duplicate or non-monotonic numbers inside the implementation section | 3 | `completed/` MUX-109, MUX-114, MUX-207 |
| Specs with a Phase 0 | 5 | `backlog/` MUX-022, MUX-116; `completed/` MUX-042, MUX-071, MUX-131 |

So no spec that can become active today is misparsed by scoping. The scoping rule is still worth
landing — nothing stops the next spec from putting boxes under a phase heading in its verification
notes — but the "8 of 76 disagree, in the unsafe direction" finding is history.

## Decisions

### 1. "Complete" means the contiguous prefix — settled 2026-08-31 (maintainer); overtaken in mechanism by MUX-183

The decision was that Phase 1 ✓, Phase 2 ✗, Phase 3 ✓ counts as **one** phase complete, so that
`SpecCompletedPhaseCount` and `SpecJustCompletedPhase` became one definition with two projections.

MUX-183 reached the aim by another road: there is now one predicate, anchored on HEAD, and no count
is compared with anything. It did **not** adopt the prefix rule. Under `phaseCommitReady` a later
phase that is complete in the tree and not at HEAD is committable while an earlier phase stays open.
This spec does not put the prefix rule back: the 2026-11-24 decisions say to rewrite Phase 4 around
MUX-183's logic, and decision 4 below covers what an open earlier item does (it holds the *loop*).
Phase 1 pins the behaviour as it is. Declining a commit for a phase stranded behind an open earlier
one would be a new blocking rule and is the user's to ask for — see [Notes](#notes).

## Decisions (user, 2026-11-24)

Relayed by edit. These settle the three decisions this spec left open for the maintainer.

| # | Question | Decision |
|---|----------|----------|
| 2 | Should phase parsing be scoped to a section? | **(a)** Only `### Phase N` under `## Implementation` is a phase. No pattern-matched section, no marker |
| 3 | Are duplicate phase numbers an error or a warning? | **(b)** A warning surfaced at dispatch. Not a validation error at `spec set`, not last-wins |
| 4 | When graph and spec disagree, who wins? | A **deliberately open item holds the loop**, and the gate text says so — naming the phase and the open item, so the hold reads as the spec's instruction and not as a stuck run |

Also directed: rewrite the stale Phase 4 around MUX-183's HEAD-anchored logic, and re-measure the
stranded specs. [MUX-143](../backlog/MUX-143-run-carries-two-phase-identities.md)'s Defect A — a `### Deferred`
heading as the way to park an item — lands with this spec's scoping.

How decision 4 and MUX-143's `### Deferred` fit together: an open box **inside** a phase holds the
loop on that phase, on purpose, and the gate says which box. An item that should not hold the loop is
moved under `### Deferred`, where no phase owns it. It still counts for `SpecOpenItems`, so the spec
cannot close while it is open.

## Decisions (user, 2026-11-27)

Relayed by edit, on the two items plan held open once all six phases were done.

| Question | Decision |
|----------|----------|
| A spec with **no** `## Implementation` section — read document-wide with a warning, or refused? | **Strict refusal.** Such a spec is never commit-ready: `phaseCommitReady` and the `phase-progress` guard refuse it, with the unscoped warning naming why. It is still read document-wide for display, never as zero phases |
| The Evidence item "a live run has actually mis-permitted a commit" | Restated as a plain **not-established** note — found by code reading, no live-run evidence — and no longer a checkbox |

This supersedes the fallback Phase 3 shipped, where a spec of MUX-077's shape could still make a
commit ready on a ticked acceptance-criteria phase. The refusal is added to Phase 6 as an open step
and a test, so the loop routes back to implement.

## Requirements

### Acceptance criteria

- [x] The commit guard, the pre-gate condition and `${completed_phase}` read one definition of
      "complete" and cannot disagree — shipped by MUX-183 (`phaseCommitReady`); the two predicates
      this criterion was written against no longer exist
- [x] A `### Phase N` heading is a phase only inside the spec's `## Implementation` section
      (decision 2a) — a `## Verification notes`, `## Design` or `## Acceptance criteria` section naming
      phases contributes none, with or without checkboxes — for a spec with an implementation
      section, by the scoped parser (Phase 3). For one that has none (MUX-077's shape) the headings
      are still listed for display, and by the user's decision of 2026-11-27 such a spec is never
      commit-ready ([Decisions (user, 2026-11-27)](#decisions-user-2026-11-27)) — so no heading
      outside an implementation section can contribute a phase to a commit. Plan held this open from
      Phase 3 until that refusal landed (Phase 6's last two steps)
- [x] `SpecPhases` on `completed/MUX-031-graph-run-tui.md` returns **7** phases, not 15, and on
      `completed/MUX-077-planner-agent.md` — which has **no** `## Implementation` section — all four of
      its phases, flagged unscoped and warned, never zero. *Corrected 2026-11-27:* as written on
      2026-11-24 this read "only its implementation phases", a false premise of plan's — MUX-077's
      four phases sit under `## Acceptance criteria` and it has no implementation section to scope to
      — `TestSpecPhases_RealSpecsScoped`, on both real files
- [x] A spec with phase headings and **no** `## Implementation` section degrades explicitly — a named
      error or warning the guard and the gate text carry — never silently to zero phases — Phase 3:
      read document-wide, flagged, the warning sentence on phase-naming messages and the decline
- [x] Duplicate or non-monotonic phase numbers inside the implementation section raise a warning at
      dispatch (decision 3b): a lifecycle row and a line in the dispatch or gate text naming the
      numbers; the run is not refused — Phase 4: `spec-phase-numbers` and the warning line
- [x] A run held on a phase with an open item says so (decision 4): the stuck-gate text and the
      `phase-progress` decline name the lowest open phase and its open items, and state that an item
      left open on purpose holds the loop until it is ticked or moved under `### Deferred` — Phase 4:
      `${phase_hold}` on `50-spec-to-pr`'s `stuck-gate`, and the decline
- [x] A gate or dispatch message never interpolates a "no phase" sentinel where a phase name belongs:
      the no-open-phase case has its own sentence, chosen before substitution — Phase 4, for the
      builtin templates: each node naming `${current_phase}` carries `message_no_open_phase`. A
      custom template gets the same by setting the field; without it the sentinel still appears
- [x] A gate message never implies completeness while `SpecOpenItems` is non-zero — it states that
      items remain outside the phases — Phase 4: `${phase_hold}`'s second form
- [x] **Negative control:** a spec that genuinely has nothing open still renders its honest completion
      text — the fix must not make every finished spec look unfinished — Phase 4: "Nothing in the
      spec is open."
- [x] A spec numbering phases from **0** behaves correctly: Phase 0 can be the reported current phase
      while a later phase is also open, and `spec_phases_remaining` counts it — the first half
      tested in Phase 4; the second by `TestSpecPhasesRemainingCountsAnOpenPhaseZero`, the condition
      through `EvaluateConditions` on a spec whose only open phase is Phase 0, with a control once
      it is closed (`PASS` in the 21:02 suite log). Plan held this open until that test existed and
      had run
- [x] **Negative control:** the genuine "no open phase" case still renders its honest text — the fix
      distinguishes *absent* from *zero*, and does not conflate them the other way — Phase 4: the
      control in `TestCurrentSpecPhase_AnOpenPhaseZeroIsCurrent`
- [x] A spec whose non-implementation section names phases with checkboxes cannot make a commit
      ready — pinned by a negative control that fails if the scoping is removed —
      `TestNewlyCompletedPhases_ANonImplementationPhaseHeadingIsNeverCommittable`, confirmed to fail
      with the scoping removed (Phase 5 mutation run). For a spec with no implementation section the
      same holds by the strict refusal: `TestPhaseCommitReady_AnUnscopedSpecIsNeverCommittable`
- [x] The survey is re-runnable: a Go test or script reports specs with phase headings outside the
      implementation section and with duplicate numbers, so this cannot silently return —
      `TestSpecPhaseSurvey` (Phase 1). It reports in the test log; it does not fail when a count grows
- [x] No existing spec's guard behaviour changes except where it was measurably wrong — the specs in
      the survey table above are enumerated before and after with the delta explained — the per-spec
      table in [Findings for Phase 3](#findings-for-phase-3): four completed specs lose their
      outside headings, four are unchanged and flagged; no spec in `backlog/` or `drafts/` changes

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/spec_items.go` | `parseSpecPhases`, `SpecPhase.Complete`, `SpecCurrentPhase`, `newlyCompletedPhases`, `SpecPhaseOpenItems` |
| `tools/muxcode/bus/graph_exec.go` | `phaseCommitReady`, `phaseProgressGuardAllows`, `resolveCompletedPhaseText`, `resolveCurrentPhaseText` |
| `tools/muxcode/bus/conditions.go` | `spec_phases_remaining`, `spec_phase_committable` |
| `tools/muxcode/bus/graph_run.go` | `specPhaseLoopCap` — the phase count a loop edge is capped by |
| `tools/muxcode/bus/spec_items_test.go`, `graph_exec_test.go` | Where the pinning tests land |
| `docs/requirements/completed/MUX-121-multi-phase-sequential-graph.md` | Post-close observation this spec supersedes |
| `docs/requirements/completed/MUX-183-phase-commit-ready-recredits-shipped-phases.md` | The HEAD-anchored predicate Phase 4 builds on |

## Implementation

### Phase 1: Pin the current behaviour in Go

- [x] Characterization tests, green before any fix: `SpecPhases` on a fixture mirroring MUX-031's
      shape returns the document-wide count; a fixture with boxes under a phase heading in
      `## Acceptance criteria` (MUX-077's shape) makes `phaseCommitReady` ready on that heading alone
      — `TestParseSpecPhases_CountsPhaseHeadingsDocumentWide_Characterization`,
      `TestNewlyCompletedPhases_ANonImplementationPhaseHeadingIsCommittable_Characterization`
- [x] Characterization test: under HEAD anchoring a phase complete in the tree behind an open earlier
      phase **is** committable — recorded as today's behaviour, see decision 1 —
      `TestNewlyCompletedPhases_APhaseBehindAnOpenEarlierPhaseIsCommittable`
- [x] Characterization tests for Defect D (`SpecCurrentPhase` loses an open Phase 0 to a later open
      phase) and Defect C (duplicate numbers: what `newlyCompletedPhases` and `SpecCurrentPhase`
      return) — `TestCurrentSpecPhase_AnOpenPhaseZeroLosesToALaterOpenPhase_Characterization`,
      `TestSpecPhases_DuplicateAndNonMonotonicNumbers_Characterization`
- [x] Reproduce the 2026-11-24 survey in Go over `docs/requirements/`; record any discrepancy with
      the table above rather than adjusting it — `TestSpecPhaseSurvey`; one discrepancy, in the
      findings below
- [x] Do **not** change behaviour in this phase — the point is a trustworthy baseline — the phase is
      one new test file, `bus/spec_phase_semantics_test.go`; no production file changed

### Findings for Phase 1

Recorded 2026-11-27 from the Phase 1 worker's report (run `1795811929`). Plan read the test file's
contracts and took the survey from the suite log itself
(`/tmp/muxcode-test-logs/20261127-200241-58069/muxcode.log`, all six tests `PASS`); `./test.sh`
passed 20:04 and the review returned 0/0/0.

**The survey, by the Go parser**, over 242 spec files:

| Measure | 2026-11-24 (Python replica) | 2026-11-27 (Go) | Which |
|---------|-----------------------------|-----------------|-------|
| Specs carrying `### Phase N` | 184 | **189** | — |
| …with one outside a `## Implementation…` section | 8 | 8 | the same eight, all in `completed/`: MUX-031, 069, 071, 077, 094, 105, 109, 117 |
| …of those, whose outside headings hold checkboxes | 2 | 2 | MUX-069, MUX-077 |
| …in `backlog/` or `drafts/` | 0 | 0 | — |
| Duplicate or non-monotonic numbers inside the implementation section | 3 | 3 | `completed/` MUX-109, MUX-114, MUX-207 |
| Specs with a Phase 0 | 5 | 5 | `backlog/` MUX-022, MUX-116; `completed/` MUX-042, MUX-071, MUX-131 |

| Finding | Detail |
|---------|--------|
| The one discrepancy | 189 specs carry phases against 184 on 2026-11-24. Every other count and every named spec is identical, so the difference is five specs written since (MUX-238 to MUX-245 were filed that afternoon; MUX-246 is not on this branch) — not established spec by spec |
| The Python replica's rule is the Go parser's | The survey's assertion: for every spec, the phase numbers `parseSpecPhases` returns, in order, equal the `### Phase N` headings an independent walk (fences, `##` sections and phase headings only) finds. This closes the second "Not verified" item under [Evidence](#evidence-what-is-verified-and-what-is-not) |
| What the characterization pins | A fixture of MUX-031's shape parses 4 phases document-wide (2 real, 2 verification-notes headings; the narrative ones are empty, not complete). Ticking a box under a phase heading in `## Acceptance criteria` makes a commit ready on that heading alone. Numbers `[2 1 2]` are accepted: current is 1, newly-complete is the first Phase 2 only though another is open, shipped phases are keyed by number, and `SpecPhaseOpenItems` sums the duplicates. An open Phase 0 loses to a later open phase |
| The survey reports through the test log | It `t.Log`s the table, so the numbers appear under `go test -v` — which `./test.sh` runs — rather than failing on a count. It is skipped outside a checkout |

### Phase 2: Decide and document the semantics

- [x] Settle decision 1 — **contiguous prefix**, maintainer 2026-08-31, recorded in [Decisions](#decisions); overtaken in mechanism by MUX-183
- [x] Settle open decisions 2–4 — the user, 2026-11-24, relayed by edit, recorded in [Decisions (user, 2026-11-24)](#decisions-user-2026-11-24)
- [x] Doc comments at `parseSpecPhases`, `SpecCurrentPhase` and `phaseCommitReady`: what a phase is
      (decision 2a), what holds the loop (decision 4), and which predicate each caller must use —
      written by the worker, comments only; read by plan, who checked in the parser that a non-`Phase`
      `###` heading already ends a phase today (`cur = -1` for any heading that is not `####` or
      deeper), so the `### Deferred` statement is true before Phase 3
- [x] `docs/architecture.md` graph section: the same three statements, and the `### Deferred` heading
      (with MUX-143) — "What a phase is, what holds the loop, and which predicate answers what", after
      the MUX-183 paragraph: two tables and the two limits that stand until Phases 3 and 4. It says the
      scoping is decided and **not yet in effect**; Phase 3 must reword that sentence. Its links to
      this spec was retargeted to `requirements/completed/` at close-out; its link to MUX-143
      (`requirements/backlog/`) needs retargeting when that spec moves

### Phase 3: Scope phase parsing to the implementation section

- [x] `parseSpecPhases` recognises `### Phase N` only inside `## Implementation` (a heading whose text
      begins `Implementation`, so `## Implementation phases` is included — the exact match is pinned
      by test); the Phase 1 characterization tests are inverted in the same change —
      `scanSpecPhases`; `TestParseSpecPhases_OnlyImplementationHeadingsArePhases`,
      `TestNewlyCompletedPhases_ANonImplementationPhaseHeadingIsNeverCommittable`,
      `TestParseSpecPhases_ImplementationSectionMatch`. The section ends at the next `##` **or `#`**
      heading (`TestParseSpecPhases_ALevelOneHeadingEndsTheImplementationSection`, the review's
      must-fix)
- [x] A non-`Phase` `###` heading inside the section still ends the current phase, so boxes under
      `### Deferred` belong to no phase — the rule MUX-143's Defect A blesses; its test and doc line
      land in this change — `TestParseSpecPhases_DeferredBoxesBelongToNoPhase` (the deferred box
      holds no loop and still counts for `SpecOpenItems`); the doc line is in `docs/architecture.md`
- [x] `SpecPhases` on MUX-031 returns 7 — `TestSpecPhases_RealSpecsScoped`, on the real file
- [x] Re-run the survey across all specs; enumerate what changed for each spec in the table — the
      per-spec table in the findings below; the 20:29 log reads the same as the 20:19 one
- [x] Specs with phase headings and no implementation section degrade explicitly, never silently to
      zero phases (a zero-phase spec makes `spec_phases_remaining` end a run at once and gives the
      loop cap nothing to count) — `TestScanSpecPhases_NoImplementationSectionDegradesExplicitly`,
      `TestInterpolateGraphMessage_UnscopedSpecCarriesTheWarning`

### Findings for Phase 3

Recorded 2026-11-27 from the Phase 3 worker's messages (run `1795811929`); the steps above were
ticked at the run's verification. Plan read the parser diff, the tests, MUX-077's headings and the
survey in the suite log (`/tmp/muxcode-test-logs/20261127-201914-50112/muxcode.log`, and again in
`20261127-202927-18452` after the fix below — nine Phase 3 tests `PASS`, no failure in the suite).
The first review returned one must-fix: only a `##` heading reset the section, so a `### Phase 9`
under a later `# Appendix` was still a scoped phase and committable. Both scans now share one
boundary rule; the re-review at 20:31 passed 0/0/0 and `./test.sh` passed at 20:31.
`scripts/test-multi-phase-graph.sh`, on a muxcode built from the tree after that fix: 52 passed,
0 failed.

**The survey, by the scoped Go parser** (189 specs carrying phases, as in Phase 1):

| Spec (all in `completed/`) | Has an implementation section holding phases? | Change under scoping |
|----------------------------|-----------------------------------------------|----------------------|
| MUX-031, MUX-105, MUX-109, MUX-117 | Yes | Phase headings outside it are no longer phases |
| MUX-069, MUX-071, MUX-077, MUX-094 | No | **None in what is parsed** — read document-wide as before, now flagged unscoped and warned |

Unchanged from Phase 1: outside headings holding checkboxes 2 (MUX-069, MUX-077); such specs in
`backlog/` or `drafts/` 0; duplicate or non-monotonic numbers 3 (MUX-109, MUX-114, MUX-207); Phase 0
specs 5. The survey's first line is labelled "no longer parsed as phases: 8"; for the four unscoped
specs that label is not true — their headings are still parsed.

| Finding | Detail |
|---------|--------|
| The rule | `scanSpecPhases` reads a `### Phase N` as a phase only while inside a `##` heading beginning `Implementation` (`isImplementationSection`, a prefix test — `## Implementation phases` and `## Implementation order` both match) |
| The explicit degradation | When no implementation section holds a phase heading — no such section, or one with none — the spec is read document-wide and flagged (`SpecPhasesUnscoped`). Messages naming a phase and the `phase-progress` decline carry `SpecPhasesUnscopedWarning` as a sentence of its own |
| **What the degradation costs two criteria** | "A `### Phase N` heading is a phase only inside `## Implementation` … with or without checkboxes" and "a spec whose non-implementation section names phases with checkboxes cannot make a commit ready" hold for a spec that **has** an implementation section with phases. For one that has none — MUX-077's shape — the headings under `## Acceptance criteria` are still phases and ticking them can still make a commit ready, with the warning. That is the fourth criterion (degrade explicitly, never to zero phases) taking precedence over a strict reading of decision 2a. The stricter alternative was not built in Phase 3. **Resolved 2026-11-27:** the user chose strict refusal — the spec is still read document-wide for display, and is never commit-ready (Phase 6's last two steps) |
| The warning's wording | As first written it said the spec "has no `## Implementation` section" in both cases. Reworded on plan's note: "no `### Phase` heading in the spec sits inside a `## Implementation` section, so every `### Phase` heading in the file is read as a phase" — true of both |
| A criterion's premise was wrong | The MUX-077 clause of the third criterion, written by plan on 2026-11-24, assumed that spec had implementation phases. Corrected above |

### Phase 4: Reconcile what is left around the HEAD-anchored predicate

> Rewritten 2026-11-24. The phase as filed derived `SpecCompletedPhaseCount` and
> `SpecJustCompletedPhase` from one prefix walk; MUX-183 removed both. What is left is everything
> `phaseCommitReady` and `SpecCurrentPhase` still get wrong or say badly. **Do not start before
> Phase 3 lands** — the texts below name phases, and they must name scoped ones.

- [x] Phase 0: `SpecCurrentPhase` and its callers carry an explicit "found" result instead of testing
      `Number == 0` (`resolveCurrentPhaseText`, the `spec_phases_remaining` condition); `phaseCommitVerdict`
      already does this through `ready` — `SpecCurrentPhase` returns `(phase, found, err)`; callers
      in `graph_exec.go`, `conditions.go` and `branch_freshness.go` read `found`;
      `TestCurrentSpecPhase_AnOpenPhaseZeroIsCurrent`, the Phase 1 characterization inverted
- [x] Duplicate or non-monotonic numbers: detected in the one parse, surfaced at dispatch as a
      lifecycle row and a line in the node's message (decision 3b); `newlyCompletedPhases` and
      `SpecPhaseOpenItems` behaviour on duplicates stated in their doc comments and pinned —
      `phaseNumberIssues`, `phaseNumberWarning`, row `spec-phase-numbers`;
      `TestInterpolateGraphMessage_DuplicatePhaseNumbersWarn`,
      `TestSpecPhases_DuplicateAndNonMonotonicNumbers`; both doc comments read by plan
- [x] `phaseProgressGuardAllows` decline text and the stuck gate name the lowest open phase and its
      open items, and say that an item left open on purpose holds the loop (decision 4) —
      `phaseHoldSentence`; the new `${phase_hold}` placeholder on `50-spec-to-pr`'s `stuck-gate`;
      `TestPhaseProgressDeclineNamesTheHold`, `TestResolvePhaseHoldText`
- [x] `resolveCurrentPhaseText` no longer returns a sentinel into a noun slot: the builtin templates
      that use `${current_phase}` get a distinct sentence for the no-open-phase case, chosen before
      substitution; where `SpecOpenItems` is non-zero that sentence says items remain outside the phases
      — the node field `message_no_open_phase`, picked by `nodeMessage` before substitution, on the
      five builtin nodes naming `${current_phase}`; `TestNodeMessage_NoOpenPhaseSentence`
- [x] Re-check `resolveCompletedPhaseText` still returns honest text, including its degradation
      strings — unchanged: `(no uncommitted completed phase)` and the unresolved-repo string; read by
      plan
- [x] Re-measure the stranded specs: for every spec in `backlog/` and `drafts/`, which phases are
      complete behind an open earlier phase, under the scoped parser; record the table here in place
      of 2026-08-31's (which measured the unscoped parser and the removed prefix predicate) — **none**;
      see the findings

### Findings for Phase 4

Recorded 2026-11-27 at the run's verification (run `1795811929`). Plan read the code and the tests;
seven Phase 4 tests `PASS` in the suite log
(`/tmp/muxcode-test-logs/20261127-204245-81873/muxcode.log`, no failure in the suite), `./test.sh`
passed 20:44, the review returned 0/0/0, and `scripts/test-multi-phase-graph.sh` on a muxcode built
from the tree passed 52/0.

**Stranded specs, re-measured** — the survey's new line, by the scoped Go parser, over `backlog/` and
`drafts/`:

| Measure | 2026-11-27 |
|---------|------------|
| Phases complete behind an open earlier phase | **0** — no spec |

So the case decision 1's Note describes — a later phase committed while an earlier one is open —
exists in no live spec today, and the decline that Note leaves to the user has nothing to act on.
2026-08-31's table measured the unscoped parser and a predicate MUX-183 removed; it is superseded,
not reproduced.

| Finding | Detail |
|---------|--------|
| The sentinel still exists | `resolveCurrentPhaseText` returns `(no open phase)` as before. What changed is that the five builtin nodes naming `${current_phase}` carry `message_no_open_phase`, so they never reach it. A user or project template naming `${current_phase}` without that field still gets the sentinel |
| When the alternate message is used | Only when the active spec was read and no phase has an open box (`activeSpecHasNoOpenPhase`). An unset, refused, transient or unreadable spec never swaps the message |
| `${phase_hold}` has three honest forms | A held phase and its items; "No implementation phase has an open item, but N item(s) remain open outside the phases: …"; or "Nothing in the spec is open." — the last is the control that a finished spec is not made to look unfinished |
| The duplicate warning is logged every time it is produced | One `spec-phase-numbers` row per phase-naming dispatch on such a spec, not once per run |
| `spec_phases_remaining` on a Phase 0 spec has no test | The condition now reads `found` — a one-line change plan read — but no test evaluates it on a spec numbered from 0. The Phase 0 criterion is held open on that half |

### Phase 5: Negative controls

- [x] Test that fails if section scoping is removed — a fixture with boxes under a phase heading in a
      non-implementation section must not yield a phase —
      `TestParseSpecPhases_OnlyImplementationHeadingsArePhases`,
      `TestNewlyCompletedPhases_ANonImplementationPhaseHeadingIsNeverCommittable` (Phase 3; `PASS`
      in the 20:42 suite log)
- [x] Test asserting the guard still **allows** a legitimate phase-closing commit — the guard must not
      become uniformly restrictive (a guard that always declines passes every decline test) — the
      second half of `TestPhaseProgressDeclineNamesTheHold` (the guard allows a commit closing
      Phase 1 and writes no further decline row). Run at 20:55 in the mutation script's baseline
      (`PASS`), and it fails when the guard is made to decline always
- [x] Test that the decline and gate text name the open phase and item; **control** — a spec with
      nothing open renders its completion text with no hold wording —
      `TestPhaseProgressDeclineNamesTheHold` (first half), `TestResolvePhaseHoldText` (Phase 4; `PASS`
      in the 20:42 log)
- [x] Test that a duplicate-number spec warns and a clean spec does not —
      `TestInterpolateGraphMessage_DuplicatePhaseNumbersWarn` (Phase 4; `PASS` in the 20:42 log)
- [x] Test that an open Phase 0 is the current phase while Phase 1 is also open; **control** — a spec
      with no open phase still reports none — `TestCurrentSpecPhase_AnOpenPhaseZeroIsCurrent`
      (Phase 4; `PASS` in the 20:42 log)
- [x] Confirm each negative control fails when its fix is reverted; a green suite that would stay
      green without the change proves nothing — **executed**, 2026-11-27 20:55: 13 mutations, 13
      confirmed. Plan first held this open on a static trace; see the findings

### Findings for Phase 5

Recorded 2026-11-27 from the Phase 5 worker's notes (`/tmp/mux130-phase5-reverts.md`, run
`1795811929`). Every Phase 5 control exists in `bus/spec_phase_semantics_test.go`; one is new, the
allow control. The worker left the confirm-by-revert step to plan's judgement, saying plainly that
each revert was **traced statically, not executed** — the edit role may not run `go test`.

| Control | Fix reverted | Assertion that would fail | Executed? |
|---------|--------------|---------------------------|-----------|
| `TestParseSpecPhases_OnlyImplementationHeadingsArePhases` | The phase match drops `(inImpl \|\| !scoped)` | 4 phases, want 2 | **Yes, in effect** — its Phase 1 form asserted the document-wide count and passed on the pre-fix parser (20:02 log) |
| `TestNewlyCompletedPhases_ANonImplementationPhaseHeadingIsNeverCommittable` | The same | A phase newly complete from the acceptance-criteria heading, want none | **Yes, in effect** — its Phase 1 form asserted that heading *is* committable and passed pre-fix |
| `TestCurrentSpecPhase_AnOpenPhaseZeroIsCurrent` | `currentSpecPhase` back to the `Number == 0` sentinel | Current phase 1, want 0 | **Yes, in effect** — its Phase 1 form asserted Phase 0 loses and passed pre-fix |
| `TestParseSpecPhases_ALevelOneHeadingEndsTheImplementationSection` | The section boundary drops the `# ` case | Phase 9 parsed; newly complete | Traced only. The review found this leak by reading the first cut, which is why the test exists |
| `TestSpecPhases_RealSpecsScoped` | Scoping removed | MUX-031 reads 15, want 7 | Traced only |
| `TestPhaseProgressDeclineNamesTheHold` — allow control | The guard made to decline always | "the guard declined a commit that closes Phase 1" | Traced only; the control itself has not run yet |
| `TestPhaseProgressDeclineNamesTheHold` — hold text | The decline drops `phaseHoldSentence` | Decline lacks "left open on purpose holds the loop" | Traced only |
| `TestResolvePhaseHoldText` — completion control | The resolver always emits hold wording, or never checks `SpecOpenItems` | Not "Nothing in the spec is open." / lacks "remain open outside the phases" | Traced only |
| `TestNodeMessage_NoOpenPhaseSentence` | `nodeMessage` always returns the ordinary message | "Implement (no open phase)." | Traced only |
| `TestInterpolateGraphMessage_DuplicatePhaseNumbersWarn` | The warning removed, or emitted always | Lacks the warning / a clean dispatch carries one | Traced only |

**Executed 2026-11-27 20:55, superseding the trace above.** After the review's should-fix the worker
wrote `/tmp/mux130-mutations.sh`, run by the run agent (task `1795830856-spawn-a5769f4f-6e6ec65f`,
log `/tmp/mux130-mutations.log`; script and log read by plan). It copies `tools/muxcode` and `docs/`
to a scratch directory, runs the nine control tests unmutated — all nine must `PASS`, a skip fails
the baseline — then applies thirteen one-substitution mutations. A mutation counts only if the
substitution applied, the package still compiles, and the named test **fails with its named
assertion text**: **13 confirmed, 0 not confirmed.**

| Mutation | Control that fails | Its assertion |
|----------|--------------------|---------------|
| Scoping removed | `TestParseSpecPhases_OnlyImplementationHeadingsArePhases` | the verification-notes headings parse again |
| Scoping removed | `TestNewlyCompletedPhases_ANonImplementationPhaseHeadingIsNeverCommittable` | "newly completed = [Phase 3 criteria], want none" |
| Scoping removed | `TestSpecPhases_RealSpecsScoped` | "MUX-031: 15 phases, want 7" — on the real file |
| Level-1 boundary dropped | `TestParseSpecPhases_ALevelOneHeadingEndsTheImplementationSection` | Phase 9 under `# Appendix` parsed |
| Guard always declines | `TestPhaseProgressDeclineNamesTheHold` | "the guard declined a commit that closes Phase 1" |
| Decline drops the hold | `TestPhaseProgressDeclineNamesTheHold` | the decline lacks the hold sentence |
| Completion text replaced by hold wording | `TestResolvePhaseHoldText` | a finished spec no longer reads "Nothing in the spec is open." |
| Items outside the phases ignored | `TestResolvePhaseHoldText` | an open criterion reads "Nothing in the spec is open." |
| No-phase message never chosen | `TestNodeMessage_NoOpenPhaseSentence` | "Implement (no open phase)." |
| Duplicate warning removed | `TestInterpolateGraphMessage_DuplicatePhaseNumbersWarn` | the duplicate dispatch carries no warning |
| Warning on a clean spec | `TestInterpolateGraphMessage_DuplicatePhaseNumbersWarn` | a clean dispatch carries one |
| Phase 0 sentinel restored | `TestCurrentSpecPhase_AnOpenPhaseZeroIsCurrent` | "current phase = 1, want the open Phase 0" |
| `found` always true | `TestCurrentSpecPhase_AnOpenPhaseZeroIsCurrent` | a spec with nothing open reports a current phase |

The first run read 12 of 13: the scratch copy lacked `docs/`, so the real-spec control *skipped* and
its mutation could not fail it. The script now copies `docs/` and refuses a baseline with a skip.
One limit: the script lives in `/tmp`, not in the repository, so this evidence is not re-runnable
from a checkout unless Phase 6 adopts it. The allow control then ran under `./test.sh` too
(`PASS` in the 20:56 suite log, no failure in the suite), and the review at 20:57 returned 0/0/0.

Plan's earlier judgement, kept for the record: the step says *confirm*, and a trace is a reading of the code, not a run of it.
Three controls are confirmed by the suite's own history — Phase 1 pinned the opposite assertion on
the pre-fix code, and that passing run is the reverted-fix result (plan saw the three `PASS` lines
in the 20:02 log; that each fixture was kept as it was when its assertion was inverted is the
worker's account, not diffed by plan). For the other seven nothing has
been executed against a reverted tree. **What closes the step:** one executed run per remaining
control against a copy of the tree with its fix reverted — the pattern this repo's integration
scripts already use for their control sections (a build from a patched copy, run by the run agent).
Phase 6's script is the natural home. The acceptance criterion "pinned by a negative control that
fails if the scoping is removed" closes with it.

### Phase 6: Integration test

- [x] Extend `scripts/test-multi-phase-graph.sh` (or add `scripts/test-spec-phase-parsing.sh`) with a
      fixture spec whose verification section names phases with checkboxes, a duplicate phase number,
      and one item left open on purpose — the new script: a `## Verification notes` section holding
      `### Phase 3 … — verified` with a box, a second `### Phase 2` section, and Phase 2's "keep open
      on purpose"
- [x] Drive a real run to the per-phase commit gate: the non-implementation headings contribute no
      phase, and the guard declines when no implementation phase newly closed — section C: only the
      verification note is ticked, the gate is approved, and the guard declines into the stuck gate;
      no commit is dispatched for that heading
- [x] The run holds on the phase with the open item, and the stuck gate's text names that phase and
      item and says the hold is deliberate; the duplicate number produces the dispatch warning —
      section C (decline and stuck gate both read "Phase 2: Second still has 1 open item(s): keep open
      on purpose" and the deliberate-hold sentence) and section B (the warning line and a
      `spec-phase-numbers` row)
- [x] `${completed_phase}` in the gate message names the phase the guard reasoned about — sections B
      (Phase 1, and the commit names it) and D (Phase 2)
- [x] Negative control: the same fixture with the item ticked and its phase closed dispatches and
      completes, with no hold wording and no warning once the duplicate is removed — section D: the
      retry carries no warning, the final gate reads "Nothing in the spec is open." with no hold
      wording, the run completes, and no further `spec-phase-numbers` row is written
- [x] Add a coverage floor so a skipped or partial run cannot report green — exactly 34 passes or
      exit 1
- [x] Run it and record passed/failed/exit code in this spec
- [x] **Strict refusal (user, 2026-11-27):** a spec read unscoped — phase headings and none inside a
      `## Implementation` section — is never commit-ready. `phaseCommitReady` reports it not ready
      and the `phase-progress` guard declines, the decline carrying `SpecPhasesUnscopedWarning` as
      the reason; the `spec_phase_committable` condition follows, since it reads the same predicate.
      The spec is still read document-wide for display — `${current_phase}`, the survey — and never
      as zero phases. Reword the unscoped row of `docs/architecture.md` and the unscoped line of
      `docs/agent-bus.md` in the same change (plan, on request) — `phaseCommitVerdict.unscoped`: the
      predicate returns not ready before it looks for a newly complete phase; the guard declines
      "a spec read unscoped is never commit-ready." with the warning; both docs reworded by plan
- [x] Test: an unscoped spec with a ticked acceptance-criteria phase is **not** committable, and the
      decline names why; **control** — a scoped spec closing an implementation phase still is.
      `TestSpecPhases_RealSpecsScoped`'s MUX-077 expectation stays (four phases read, flagged) —
      `TestPhaseCommitReady_AnUnscopedSpecIsNeverCommittable`: the predicate, the pre-gate condition
      and the guard's decline, with the scoped control; both tests `PASS` in the 21:16 suite log

The strict refusal, verified 2026-11-27: `./test.sh` passed at 21:18 with no failure in the suite,
the review returned 0/0/0, and both integration scripts were re-run on a muxcode built from the tree
after the change — `scripts/test-spec-phase-parsing.sh` 34 passed, 0 failed (21:15) and
`scripts/test-multi-phase-graph.sh` 52 passed, 0 failed (21:12). Test fixtures that ship commits
gained a `## Implementation` heading — the `bus` phase-check, graph-exec and phase-anchor tests and
`scripts/test-multi-phase-graph.sh` — because without one their specs are now refused. Neither
script has a section for the refusal itself: it is covered by the unit test, not end to end, and no
mutation run was made against it as Phase 5 did for the earlier controls.

Run through the run agent on 2026-11-27 21:00 (task `1795831183-spawn-a5769f4f-f54d7dac`, log
`/tmp/mux130-spec-phase-parsing.log`; script and log read by plan): **34 passed, 0 failed, coverage
floor 34, exit 0** — sections A (4), B (9), C (10), D (11) — against a muxcode built from this tree
(`/tmp/mux130-tree-run.sh` sets `MUXCODE_BIN`).

What the script is and is not:

| Point | Detail |
|-------|--------|
| A real run | A scratch daemon on a private tmux server walks a fixture spec in a real git repository through human-gated per-phase commits; the script plays edit, review, plan and commit by answering their inboxes, and approves gates as a configured gate authority |
| The graph is a fixture, not the builtin | It has `50-spec-to-pr`'s shape with send nodes in place of spawns, and **without** the `phase-check` condition — in the builtin that condition routes an open phase to the stuck gate before anyone is asked, so the `phase-progress` guard is never reached. Here an approval runs into the guard, which is what must decline. The builtin's own `message_no_open_phase` nodes are not exercised by this script |
| Section C discriminates | With scoping removed the ticked verification-notes box is a newly complete phase, the guard allows, and the stuck gate never waits. That is reasoning from the code and the Phase 5 mutation run — the script itself carries **no pre-fix control build** |
| It needs the new binary | It defaults to the muxcode on `PATH`, so it passes there only once this change is installed |
| Phase 5's mutation script was not adopted | It remains in `/tmp` |

## Notes

**Decision 1 and out-of-order completion — not reopened here.** Under MUX-183 a phase complete behind
an open earlier phase may be committed; under 2026-08-31's prefix rule it would have been declined.
Nothing in the 2026-11-24 decisions asks for the decline, and the run loop does not produce the case
by itself — it hands out the lowest open phase, so a later phase closes first only when someone works
out of order by hand. Phase 1 pins today's behaviour and Phase 4's re-measurement shows how many live
specs are in that state. If the count is not zero and the user wants the decline, it is one rule in
`newlyCompletedPhases`.

## Status

Complete

Closed out 2026-11-27 by `55-defect-backlog-loop` run `1795811929` at 55/55: six phases and fourteen
acceptance criteria.

| What shipped | |
|--------------|-|
| Scoping | A `### Phase N` is a phase only inside the spec's `## Implementation` section |
| Strict refusal | A spec with no implementation section holding phases is still read document-wide for display, and is never commit-ready (the user, 2026-11-27) |
| `### Deferred` | Boxes under a non-`Phase` `###` heading belong to no phase: they never hold the loop and still stop the close — [MUX-143](../backlog/MUX-143-run-carries-two-phase-identities.md)'s Defect A, landed here |
| The hold says why | The stuck gate and the commit guard's decline name the lowest open phase, its open items and the deliberate hold (`${phase_hold}`) |
| No sentinel in a noun slot | The builtin nodes naming `${current_phase}` dispatch `message_no_open_phase` when no phase is open |
| Duplicate numbers | Warned at dispatch, with a `spec-phase-numbers` row; never refused |
| Phase 0 | An open Phase 0 is the current phase, and `spec_phases_remaining` counts it |

Left as recorded, none of it open work under this spec: a custom template naming `${current_phase}`
without `message_no_open_phase` still gets the `(no open phase)` sentinel; the strict refusal has a
unit test but no integration-script section and no mutation run; Phase 5's mutation script lives in
`/tmp`, not in the repository; and decision 1's out-of-order commit stays permitted, with no live
spec in that state.

How it got here. Activated 2026-11-27 at 8/54 — one criterion shipped by MUX-183, two decision
steps and five evidence items from filing. Phases 1–6 were verified the same day. Plan held three
boxes open that no phase owned: the Phase 0 criterion, closed by a test; the scoping criterion,
closed by the user's strict-refusal decision, which reopened Phase 6 with two steps; and an
Evidence checkbox no step could tick, which the user had restated as a note — so the total moved
from 54 to 55 (one box removed, two added). Phase 3 changed the parser the loop running this very
spec read its phases with; its phases sit under `## Implementation`, so the scoped rule read them
unchanged.

Unparked 2026-11-24 on the user's decisions 2 (a), 3 (b) and 4, relayed by edit and recorded above.
Phase 4 was rewritten then around MUX-183's HEAD-anchored predicate, which had closed Defect A and
narrowed Defect B since filing.
