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
> [`MUX-143`](./MUX-143-run-carries-two-phase-identities.md) covers a run carrying **two unreconciled
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
      and [`MUX-128`](./MUX-128-fkey-navigation-for-spawn-windows.md) — are in `backlog/` **now**, so
      either could become the active spec

**Not verified — do not treat as established:**

- [ ] That a live run has actually mis-permitted a commit through this path. No run evidence exists;
      the argument is from code and reachability only
- [ ] That Go's behaviour matches the Python replication used for the survey. The replication mirrors
      the fence handling, both regexes, and the trimmed-vs-raw line distinction, but **it was not run
      against the Go implementation.** Phase 1 exists to close this gap with a Go test rather than
      inherit the replica's authority

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
stranded specs. [MUX-143](./MUX-143-run-carries-two-phase-identities.md)'s Defect A — a `### Deferred`
heading as the way to park an item — lands with this spec's scoping.

How decision 4 and MUX-143's `### Deferred` fit together: an open box **inside** a phase holds the
loop on that phase, on purpose, and the gate says which box. An item that should not hold the loop is
moved under `### Deferred`, where no phase owns it. It still counts for `SpecOpenItems`, so the spec
cannot close while it is open.

## Requirements

### Acceptance criteria

- [x] The commit guard, the pre-gate condition and `${completed_phase}` read one definition of
      "complete" and cannot disagree — shipped by MUX-183 (`phaseCommitReady`); the two predicates
      this criterion was written against no longer exist
- [ ] A `### Phase N` heading is a phase only inside the spec's `## Implementation` section
      (decision 2a) — a `## Verification notes`, `## Design` or `## Acceptance criteria` section naming
      phases contributes none, with or without checkboxes
- [ ] `SpecPhases` on `completed/MUX-031-graph-run-tui.md` returns **7** phases, not 15, and on
      `completed/MUX-077-planner-agent.md` only its implementation phases
- [ ] A spec with phase headings and **no** `## Implementation` section degrades explicitly — a named
      error or warning the guard and the gate text carry — never silently to zero phases
- [ ] Duplicate or non-monotonic phase numbers inside the implementation section raise a warning at
      dispatch (decision 3b): a lifecycle row and a line in the dispatch or gate text naming the
      numbers; the run is not refused
- [ ] A run held on a phase with an open item says so (decision 4): the stuck-gate text and the
      `phase-progress` decline name the lowest open phase and its open items, and state that an item
      left open on purpose holds the loop until it is ticked or moved under `### Deferred`
- [ ] A gate or dispatch message never interpolates a "no phase" sentinel where a phase name belongs:
      the no-open-phase case has its own sentence, chosen before substitution
- [ ] A gate message never implies completeness while `SpecOpenItems` is non-zero — it states that
      items remain outside the phases
- [ ] **Negative control:** a spec that genuinely has nothing open still renders its honest completion
      text — the fix must not make every finished spec look unfinished
- [ ] A spec numbering phases from **0** behaves correctly: Phase 0 can be the reported current phase
      while a later phase is also open, and `spec_phases_remaining` counts it
- [ ] **Negative control:** the genuine "no open phase" case still renders its honest text — the fix
      distinguishes *absent* from *zero*, and does not conflate them the other way
- [ ] A spec whose non-implementation section names phases with checkboxes cannot make a commit
      ready — pinned by a negative control that fails if the scoping is removed
- [ ] The survey is re-runnable: a Go test or script reports specs with phase headings outside the
      implementation section and with duplicate numbers, so this cannot silently return
- [ ] No existing spec's guard behaviour changes except where it was measurably wrong — the specs in
      the survey table above are enumerated before and after with the delta explained

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

- [ ] Characterization tests, green before any fix: `SpecPhases` on a fixture mirroring MUX-031's
      shape returns the document-wide count; a fixture with boxes under a phase heading in
      `## Acceptance criteria` (MUX-077's shape) makes `phaseCommitReady` ready on that heading alone
- [ ] Characterization test: under HEAD anchoring a phase complete in the tree behind an open earlier
      phase **is** committable — recorded as today's behaviour, see decision 1
- [ ] Characterization tests for Defect D (`SpecCurrentPhase` loses an open Phase 0 to a later open
      phase) and Defect C (duplicate numbers: what `newlyCompletedPhases` and `SpecCurrentPhase`
      return)
- [ ] Reproduce the 2026-11-24 survey in Go over `docs/requirements/`; record any discrepancy with
      the table above rather than adjusting it
- [ ] Do **not** change behaviour in this phase — the point is a trustworthy baseline

### Phase 2: Decide and document the semantics

- [x] Settle decision 1 — **contiguous prefix**, maintainer 2026-08-31, recorded in [Decisions](#decisions); overtaken in mechanism by MUX-183
- [x] Settle open decisions 2–4 — the user, 2026-11-24, relayed by edit, recorded in [Decisions (user, 2026-11-24)](#decisions-user-2026-11-24)
- [ ] Doc comments at `parseSpecPhases`, `SpecCurrentPhase` and `phaseCommitReady`: what a phase is
      (decision 2a), what holds the loop (decision 4), and which predicate each caller must use
- [ ] `docs/architecture.md` graph section: the same three statements, and the `### Deferred` heading
      (with MUX-143)

### Phase 3: Scope phase parsing to the implementation section

- [ ] `parseSpecPhases` recognises `### Phase N` only inside `## Implementation` (a heading whose text
      begins `Implementation`, so `## Implementation phases` is included — the exact match is pinned
      by test); the Phase 1 characterization tests are inverted in the same change
- [ ] A non-`Phase` `###` heading inside the section still ends the current phase, so boxes under
      `### Deferred` belong to no phase — the rule MUX-143's Defect A blesses; its test and doc line
      land in this change
- [ ] `SpecPhases` on MUX-031 returns 7
- [ ] Re-run the survey across all specs; enumerate what changed for each spec in the table
- [ ] Specs with phase headings and no implementation section degrade explicitly, never silently to
      zero phases (a zero-phase spec makes `spec_phases_remaining` end a run at once and gives the
      loop cap nothing to count)

### Phase 4: Reconcile what is left around the HEAD-anchored predicate

> Rewritten 2026-11-24. The phase as filed derived `SpecCompletedPhaseCount` and
> `SpecJustCompletedPhase` from one prefix walk; MUX-183 removed both. What is left is everything
> `phaseCommitReady` and `SpecCurrentPhase` still get wrong or say badly. **Do not start before
> Phase 3 lands** — the texts below name phases, and they must name scoped ones.

- [ ] Phase 0: `SpecCurrentPhase` and its callers carry an explicit "found" result instead of testing
      `Number == 0` (`resolveCurrentPhaseText`, the `spec_phases_remaining` condition); `phaseCommitVerdict`
      already does this through `ready`
- [ ] Duplicate or non-monotonic numbers: detected in the one parse, surfaced at dispatch as a
      lifecycle row and a line in the node's message (decision 3b); `newlyCompletedPhases` and
      `SpecPhaseOpenItems` behaviour on duplicates stated in their doc comments and pinned
- [ ] `phaseProgressGuardAllows` decline text and the stuck gate name the lowest open phase and its
      open items, and say that an item left open on purpose holds the loop (decision 4)
- [ ] `resolveCurrentPhaseText` no longer returns a sentinel into a noun slot: the builtin templates
      that use `${current_phase}` get a distinct sentence for the no-open-phase case, chosen before
      substitution; where `SpecOpenItems` is non-zero that sentence says items remain outside the phases
- [ ] Re-check `resolveCompletedPhaseText` still returns honest text, including its degradation
      strings
- [ ] Re-measure the stranded specs: for every spec in `backlog/` and `drafts/`, which phases are
      complete behind an open earlier phase, under the scoped parser; record the table here in place
      of 2026-08-31's (which measured the unscoped parser and the removed prefix predicate)

### Phase 5: Negative controls

- [ ] Test that fails if section scoping is removed — a fixture with boxes under a phase heading in a
      non-implementation section must not yield a phase
- [ ] Test asserting the guard still **allows** a legitimate phase-closing commit — the guard must not
      become uniformly restrictive (a guard that always declines passes every decline test)
- [ ] Test that the decline and gate text name the open phase and item; **control** — a spec with
      nothing open renders its completion text with no hold wording
- [ ] Test that a duplicate-number spec warns and a clean spec does not
- [ ] Test that an open Phase 0 is the current phase while Phase 1 is also open; **control** — a spec
      with no open phase still reports none
- [ ] Confirm each negative control fails when its fix is reverted; a green suite that would stay
      green without the change proves nothing

### Phase 6: Integration test

- [ ] Extend `scripts/test-multi-phase-graph.sh` (or add `scripts/test-spec-phase-parsing.sh`) with a
      fixture spec whose verification section names phases with checkboxes, a duplicate phase number,
      and one item left open on purpose
- [ ] Drive a real run to the per-phase commit gate: the non-implementation headings contribute no
      phase, and the guard declines when no implementation phase newly closed
- [ ] The run holds on the phase with the open item, and the stuck gate's text names that phase and
      item and says the hold is deliberate; the duplicate number produces the dispatch warning
- [ ] `${completed_phase}` in the gate message names the phase the guard reasoned about
- [ ] Negative control: the same fixture with the item ticked and its phase closed dispatches and
      completes, with no hold wording and no warning once the duplicate is removed
- [ ] Add a coverage floor so a skipped or partial run cannot report green
- [ ] Run it and record passed/failed/exit code in this spec

## Notes

**Decision 1 and out-of-order completion — not reopened here.** Under MUX-183 a phase complete behind
an open earlier phase may be committed; under 2026-08-31's prefix rule it would have been declined.
Nothing in the 2026-11-24 decisions asks for the decline, and the run loop does not produce the case
by itself — it hands out the lowest open phase, so a later phase closes first only when someone works
out of order by hand. Phase 1 pins today's behaviour and Phase 4's re-measurement shows how many live
specs are in that state. If the count is not zero and the user wants the decline, it is one rule in
`newlyCompletedPhases`.

## Status

Backlog

Unparked 2026-11-24 on the user's decisions 2 (a), 3 (b) and 4, relayed by edit and recorded above.
Phase 4 is rewritten around MUX-183's HEAD-anchored predicate, which had closed Defect A and narrowed
Defect B since filing; one criterion is ticked as shipped by MUX-183. Lands with
[MUX-143](./MUX-143-run-carries-two-phase-identities.md)'s Defect A. Not started.
