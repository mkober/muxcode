# Spec Phase Parsing: Two Definitions of Complete, Matched Document-Wide

The graph orchestrator reasons about "which phase is done" through four predicates in
`bus/spec_items.go`. **Two of them define "complete" incompatibly, and all four match `### Phase N`
anywhere in the file rather than inside the implementation section.** On the repo's own specs the two
definitions disagree for **8 of 76** specs carrying phases, and the disagreement runs in the unsafe
direction: the `phase-progress` guard can be inflated into permitting a per-phase commit it exists to
decline.

Tracking: _(no GitHub issue yet)_

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
- [x] Two of the divergent specs — [`MUX-120`](./MUX-120-spawn-worker-never-woken-for-seeded-task.md)
      and [`MUX-128`](./MUX-128-fkey-navigation-for-spawn-windows.md) — are in `backlog/` **now**, so
      either could become the active spec

**Not verified — do not treat as established:**

- [ ] That a live run has actually mis-permitted a commit through this path. No run evidence exists;
      the argument is from code and reachability only
- [ ] That Go's behaviour matches the Python replication used for the survey. The replication mirrors
      the fence handling, both regexes, and the trimmed-vs-raw line distinction, but **it was not run
      against the Go implementation.** Phase 1 exists to close this gap with a Go test rather than
      inherit the replica's authority

## Decisions

### 1. "Complete" means the contiguous prefix — settled 2026-08-31 (maintainer)

A spec is complete **up to its frontier**. Phase 1 ✓, Phase 2 ✗, Phase 3 ✓ counts as **one** phase
complete, not two.

This collapses Defect A rather than documenting around it: `SpecCompletedPhaseCount` becomes the
**length** of the contiguous prefix and `SpecJustCompletedPhase` its **last element** — one definition
with two projections, so they cannot drift apart again. The guard and the `${completed_phase}` gate
message stop being able to disagree by construction.

**Direction of change is safe.** Contiguous-prefix counts are ≤ the current non-contiguous counts, and
the guard declines when `completed < prior+1`, so a lower count means *more* declines. This fix cannot
make the guard more permissive than it is today.

#### The consequence: out-of-order phase work stops counting

Closing a later phase while an earlier one stays open no longer advances the count, so the commit that
closes it is **declined**. Measured across the repo — **8 specs currently have phases stranded behind
an open earlier phase**:

| Spec | Phases stranded behind an open one |
|---|---|
| `completed/MUX-031` | 8 |
| `completed/MUX-105` | 5 |
| `completed/MUX-108` | 4 |
| `completed/MUX-109` | 3 |
| `completed/MUX-014`, `completed/MUX-050` | 2 each |
| `backlog/MUX-120`, `backlog/MUX-128` | 1 each |

The realistic trigger is a **deliberately-open item** — an acceptance criterion that cannot be
exercised without side effects, left open on purpose. [MUX-117](../completed/MUX-117-pane-targeting-by-identity.md)
carried exactly one such item in Phase 4 while later work continued. Under this decision the frontier
stalls there and every subsequent per-phase commit is declined.

**This is acceptable, and the reason is in the code**: a `phase-progress` decline routes to the stuck
gate — `graph_exec.go` states it is "the gate-and-ask trigger, not a dead end". So the effect is *the
run asks the human*, not *the run dies*. A deliberately-open item making the orchestrator stop and ask
is defensible behaviour. It must be **documented in the decline text**, though, or it reads as a bug.

#### Sequencing is load-bearing: scoping must land before semantics

Most of the stranding above is a **Defect B artifact**, not real out-of-order work. MUX-031's 8
stranded phases are almost entirely its `## Verification notes` duplicates; scoped to its 7 real
phases it strands **zero**. Landing the contiguous-prefix change *before* section scoping combines an
inflated phase list with strict prefix semantics — the maximum-over-declining configuration, and one
that would look like the fix made things worse. **Phase 3 (scoping) must land before Phase 4
(semantics).** The implementation order below already reflects this; do not reorder it.

## Open decisions (maintainer)

Still open. These change the shape of the fix and should be settled before Phase 3.

| # | Question | Options |
|---|----------|---------|
| 2 | Should phase parsing be scoped to a section? | (a) only `### Phase N` under `## Implementation`; (b) first `## Implementation`-like section by pattern; (c) an explicit marker in the spec |
| 3 | Are duplicate phase numbers an error or a warning? | (a) validation error at `spec set`; (b) warning surfaced at dispatch; (c) accept, last-wins |
| 4 | When graph and spec disagree, who wins? | MUX-121's original question. Decision 1 removes the *predicate* disagreement; what remains is whether a deliberately-open item is a legitimate reason to hold the loop. If it is, the gate message must say so rather than naming a phase number that reads as a mistake |

## Requirements

### Acceptance criteria

- [ ] `SpecCompletedPhaseCount` returns the **length of the contiguous prefix** of complete phases, and
      `SpecJustCompletedPhase` returns that prefix's **last element** — derived from one traversal so
      they cannot disagree (decision 1)
- [ ] A spec with Phase 1 ✓, Phase 2 ✗, Phase 3 ✓ reports **one** phase complete, not two
- [ ] A `phase-progress` decline caused by a stranded phase says so in its text — naming the open
      earlier phase blocking the frontier, not just the counts
- [ ] Phase headings are recognised only within the spec's implementation section — a
      `## Verification notes` section naming phases does not inflate the phase count
- [ ] `SpecPhases` on `completed/MUX-031-graph-run-tui.md` returns **7** phases, not 15
- [ ] Duplicate or non-monotonic phase numbers are surfaced, not silently accepted
- [ ] A gate message never implies completeness while `SpecOpenItems` is non-zero — either the phase
      text accounts for unscoped items, or it states plainly that items remain outside the phases
- [ ] **Negative control:** a spec that genuinely has nothing open still renders its honest completion
      text — the fix must not make every finished spec look unfinished
- [ ] A spec numbering phases from **0** behaves correctly: Phase 0 can be the reported current phase,
      and a spec with only Phase 0 complete names it rather than rendering `(no completed phase)`
- [ ] **Negative control:** the genuine "no phases complete" and "no open phase" cases still render
      their honest empty text — the fix must distinguish *absent* from *zero*, not conflate them the
      other way
- [ ] `phaseProgressGuardAllows` compares against a count that cannot exceed the spec's real phase
      count
- [ ] A spec whose verification section names phases cannot weaken the guard — pinned by a negative
      control that fails if the scoping is removed
- [ ] The survey is re-runnable: a check reports divergent specs so this cannot silently return
- [ ] No existing spec's guard behaviour changes except where it was measurably wrong — the 8
      divergent specs are enumerated before and after with the delta explained

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/spec_items.go` | All four predicates, both regexes — the whole defect surface |
| `tools/muxcode/bus/graph_exec.go` | `phaseProgressGuardAllows` (:273), `resolveCompletedPhaseText` (:343), `resolveCurrentPhaseText` (:358) |
| `tools/muxcode/bus/spec_items_test.go` | Existing predicate tests — where the pinning tests land |
| `tools/muxcode/bus/graph_exec_test.go` | Guard tests, incl. the phase-complete guard at :894 |
| `docs/requirements/completed/MUX-121-multi-phase-sequential-graph.md` | Post-close observation this spec supersedes |

## Implementation

### Phase 1: Pin the current behaviour in Go

- [ ] Write a Go test that asserts the **current** divergent behaviour of `SpecCompletedPhaseCount`
      vs `SpecJustCompletedPhase` on a fixture with a gap (1 ✓, 2 ✗, 3 ✓) — a characterization test,
      green before any fix
- [ ] Assert `SpecPhases` on a fixture mirroring MUX-031's shape returns the inflated count today
- [ ] Confirm the Python survey's numbers reproduce in Go for at least MUX-031 and MUX-109; record
      any discrepancy rather than adjusting the fixture to match
- [ ] Do **not** change behaviour in this phase — the point is a trustworthy baseline

### Phase 2: Decide and document the semantics

- [x] Settle decision 1 — **contiguous prefix**, maintainer 2026-08-31, recorded in [Decisions](#decisions)
- [ ] Settle open decisions 2–4 with the maintainer; record each alongside decision 1
- [ ] Write the contiguous-prefix definition as a doc comment at both predicates, each naming the other
      as its sibling projection
- [ ] State which predicate each caller must use and why

### Phase 3: Scope phase parsing to the implementation section

- [ ] Add section scoping to `SpecPhases` per decision 2
- [ ] Verify `SpecPhases` on MUX-031 returns 7
- [ ] Re-run the survey across all specs; confirm the divergent set shrinks and enumerate what changed
- [ ] Handle specs with no recognisable implementation section — degrade explicitly, never silently
      to zero phases (a zero-phase spec makes the guard vacuous)

### Phase 4: Reconcile the predicates and the guard

> **Do not start before Phase 3 lands** — strict prefix semantics over an unscoped (inflated) phase
> list is the maximum-over-declining configuration. See [Sequencing](#sequencing-is-load-bearing-scoping-must-land-before-semantics).

- [ ] Derive both `SpecCompletedPhaseCount` and `SpecJustCompletedPhase` from **one** contiguous-prefix
      traversal — count and last element, not two independent walks (decision 1)
- [ ] Ensure `phaseProgressGuardAllows` cannot compare against a count exceeding the real phase count
- [ ] Extend the decline text to name the open earlier phase when the frontier is stalled behind one
- [ ] Re-measure the stranded-phase table in [Decisions](#the-consequence-out-of-order-phase-work-stops-counting)
      after scoping; confirm the count drops and record the new numbers
- [ ] Surface duplicate/non-monotonic phase numbers per decision 3
- [ ] Re-check `resolveCompletedPhaseText` and `resolveCurrentPhaseText` still return honest text
      under the new semantics, including the degradation strings

### Phase 5: Negative controls

- [ ] Test that fails if section scoping is removed — a fixture with a `## Verification notes` section
      naming phases must not inflate the count
- [ ] Test that fails if the guard's comparison is reverted to the unscoped count — asserts the guard
      **declines** a no-progress commit on a spec whose verification section names phases
- [ ] Test asserting the guard still **allows** a legitimate phase-closing commit — the guard must not
      become uniformly restrictive (a guard that always declines passes every decline test). This
      control matters more under decision 1, which moves the guard toward declining
- [ ] Test pinning the contiguous-prefix consequence in **both** directions: closing a phase stranded
      behind an open earlier one **declines**, and closing the open earlier phase then **allows** —
      proving the frontier advances rather than latching
- [ ] Test that the decline text names the blocking earlier phase; a decline that only prints counts
      is indistinguishable from the inflated-count bug this spec fixes
- [ ] Confirm each negative control fails when its fix is reverted; a green suite that would stay
      green without the change proves nothing

### Phase 6: Integration test

- [ ] Extend `scripts/test-multi-phase-graph.sh` (or add `scripts/test-spec-phase-parsing.sh`) with a
      fixture spec carrying a verification section that names phases
- [ ] Drive a real run to the per-phase commit gate; verify the guard **declines** when no new phase
      closed, with the decline reason naming the true count
- [ ] Verify `${completed_phase}` in the gate message names the same phase the guard reasoned about —
      the two must not disagree in the text a human approves
- [ ] Negative control: the same fixture with its phase genuinely closed dispatches and completes
- [ ] Add a coverage floor so a skipped or partial run cannot report green
- [ ] Run it and record passed/failed/exit code in this spec

## Status

Backlog
