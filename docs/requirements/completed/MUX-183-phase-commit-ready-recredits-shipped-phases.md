# MUX-183: `phaseCommitReady` Credits a Fresh Run With Phases It Never Shipped

A second `spec-to-pr` run on a spec whose first phase an earlier run had already committed **opened the
per-phase commit gate at Phase 2 = 6/7**, with the phase's one open item reserved for the user. The
user approved what the gate asked for; the commit node then committed **whatever was in the tree** —
an unbuilt, untested spawn-road change a sibling agent had just written, which failed five tests on the
next lap. Undoing it took a history rewrite.

The gate asked for an approval the phase did not warrant. `phaseCommitReady` compares a **per-repo**
count (phases with no open items, ever) with a **per-run** counter (this run's commit-edge fires, from
zero), so every run after the first re-credits every phase already shipped. The `spec_phase_committable`
check and the `phase-progress` guard share the predicate — which is why they agreed, why the human gate
was the only thing left to catch it, and why it could not: the predicate wrote the gate's question.

## Context

### Source and standard of evidence

Observed **first-hand in this repo** on 2026-09-14 by plan, on graph run `1789399519-spec-to-pr-5aa52382`
(spec [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md)); written up by edit as
item 2 of `/tmp/mux-148-false-failure-and-phasecommit.md` (non-durable) and filed on the user's
instruction. **Every mechanism claim below was verified by plan against `f942c43`.** Two further findings
(defects 2 and 3) came out of that verification and are not in the report.

### Observed

| Time | Event |
|---|---|
| 09:43 | Run `1789393404` starts on MUX-148 (Phase 1). Phase 1 completes; `phase-gate` approved by the user 09:53; **`7e03dc9` commits Phase 1** |
| 10:03 | The same run loops into Phase 2 (0/7, all user decisions); plan refuses `verify-spec` with `EXIT=1`; the run fails (`update-spec` has no failure edge) |
| 11:25 | **Run `1789399519` starts** on the same spec. Phase 2 is 6/7; its open item is reserved for the user |
| 11:35 | `update-spec` → **`phase-check` succeeds** → `phase-gate` opens: "Approve committing …" |
| 11:39 | User approves. `commit` fires: **`5b32433`** sweeps plan's spec edits **and an unbuilt spawn-road change** edit had written minutes earlier — the run's build, test and review nodes had all completed *before* that change existed |
| 11:42 | `5b32433` reset; spec-only recommit `f942c43` |
| ~11:50 | Lap 2's `test` node fails 5 tests on the reverted code; the run is canceled |
| 13:00 | **Second occurrence**, run `1789402487-spec-to-pr-f05fd39f`, lap 2: `phase-check` passes at Phase 2 = 6/7 again, the user approves `phase-gate`, `81793df` commits — the commit agent holds back an unattributed `delivery.go` by its own judgment and reports that the dispatch named *"Phase 1: Establish the boundary"*, a phase already shipped in `7e03dc9`. `loop-check` has fired once of five; the worker reports each remaining lap will repeat this |
| 13:43 | **Third occurrence**, run `1789407209-spec-to-pr-6329cbb4` (a fresh run, loop budget reset): `phase-check` passes at Phase 2 = 6/7, the user approves `phase-gate`, `f9249ba` commits plan's lap notes — `delivery.go` held back once more by the commit agent. Three re-credited approvals in one afternoon, each on a prompt that named Phase 1 |

Plan had written into the MUX-148 spec that `phase-check` "should route to `stuck-gate`" — the
prediction a reader of `spec-to-pr`'s description would make — and was wrong; the correction is what
led here.

### Defect 1 — two incompatible counters

**Verified.** `phaseCommitReady` (`bus/graph_exec.go:511`):

```go
completed, err := SpecCompletedPhaseCount(path)          // per-REPO: phases with zero open items, ever
...
for _, e := range g.Edges {
    if e.From == commitNodeID && edgeOutcome(e) == OutcomeSuccess {
        v.shipped = max(v.shipped, run.EdgeFires[EdgeFireKey(e)])   // per-RUN: starts at 0
    }
}
v.ready = completed >= v.shipped+1
```

| Counter | Source | Scope | Value at 11:35 |
|---|---|---|---|
| `completed` | `SpecCompletedPhaseCount` (`spec_items.go:162`) — phases whose item list is empty | The spec file: **persistent across runs** | ≥ 1 (Phase 1, committed by the *previous* run) |
| `shipped` | `run.EdgeFires` on the commit node's success edges (`graph_run.go:53`, initialised `{}` at `:200`) | **This run only** | 0 |

`1 >= 0+1` — ready. Nothing in the predicate asks whether the completed phase was completed *by this
run* or is *already committed*. The doc comment's intent — "the active spec must hold one more completed
phase than the commit node has already shipped" — is right; the implementation measures "shipped" in
the wrong frame.

**Both consumers share it** — the `spec_phase_committable` condition (`bus/conditions.go:393`, the
`phase-check` node) and the `phase-progress` guard on the commit node (`bus/graph_exec.go:464`). The
sharing came from MUX-167 so the pre-gate check and the guard could never disagree; here they agreed on
the wrong answer, and the mechanism that exists to "ask before the guard, not after" asked for an
approval the phase did not warrant.

### Defect 2 — "complete" is measured as "no open items", which an empty phase also satisfies

**Verified.** `SpecCompletedPhaseCount` (`spec_items.go:162-173`) counts a phase complete when
`len(p.Items) == 0`, and `Items` holds **only open boxes** — `SpecPhases` appends an item only on an
`openItemRe` match (`:109-114`). So three different things read as complete:

| Phase | `Items` | Counted complete? | Should be |
|---|---|---|---|
| Every box ticked | empty | yes | yes |
| **No boxes written yet** — a stub, or a phase whose steps are still being drafted | empty | **yes** | no — it is *empty*, not *done* |
| **A narrative heading** — `phaseHeadingRe` (`:72`) is `^### (Phase ([0-9]+)\b.*)$`, so any H3 that *starts* with `Phase N` is a phase | empty | **yes** | not a phase at all |
| **A real phase whose boxes sit under `####` subheadings** — `cur` resets to −1 on *any* heading line (`:97`) and only a `### Phase N` heading re-arms it (`:99-102`) | empty — the items attach to no phase | **yes** | no — its work is merely invisible |

MUX-148's spec carried two of the third kind when this fired — `### Phase 1 findings — recorded …` and
`### Phase 2 decision — recorded …` — so on that spec `completed` was **3**, not 1: the predicate would
have opened the gate three laps running with no phase actually finishing. And because any heading line resets `cur`
(`:97`), a checkbox under a `####` subheading attaches to **no phase at all**: MUX-148's
`#### Constraints Phase 3 inherits` list was invisible to the count, and plan's first remedy on filing
— moving it under Phase 3 behind H4 labels, with Phase 3's own steps under a second H4 — made
**Phase 3 read complete** until edit's review caught it and the labels became bold text. (Plan's first
draft of this paragraph said the H4 list had been counted as Phase 2's work; that was wrong, and is
corrected here rather than edited away.) Both shapes are natural for a spec that
records its findings inline, and a stub phase is the normal state of a spec being written; the
parser's contract — "`### Phase N` means a phase, and no open boxes means done" — is written nowhere a
spec author would read it. MUX-148's headings were renamed on filing so its live count is honest; the
counter is the defect. **The inflated numerator compounds the per-run denominator reset** (defect 1):
each empty or narrative phase is one more unearned gate opening per run, and per retry (defect 3).

### Defect 3 — `graph retry` resets the counter it depends on

**Verified.** Retrying from a node deletes `EdgeFires` for every downstream edge
(`graph_run.go:640-644`), the commit node's included. A `retry --from update-spec` therefore returns
`shipped` to 0 *within* one run: the same re-credit, no second run needed. The loop cap lives in the
same frame: `max_iterations` on `loop-check → implement` is per-run too, so a restarted run re-arms
five laps of the same cycle — observed 13:33 when run `1789407209` replaced `1789402487` with a fresh
budget on a phase whose only open item is a user decision. The cap bounds a run, never the cycle.

### Why the human gate did not catch it

The gate is designed to: it is `wait_human`, and the user approved. But the prompt's premise was
supplied by the predicate — "Approve committing `${completed_phase}`" — and the user is entitled to
trust that the system asked because a phase completed. The question a gate poses is the control; when
the question is wrong, the approval is not a check, it is a signature on whatever the tree holds.
**Verified 13:47 (edit, re-verified by plan):** `${completed_phase}` is `resolveCompletedPhaseText`
(`graph_exec.go:728-741`), whose only source is `SpecJustCompletedPhase` (`spec_items.go:149-156`) — a
walk in file order that **breaks at the first phase with open items and returns the last complete phase
before it**. That is the right answer on a lap that just closed a phase, the design case its comment
describes (`${current_phase}` already points one ahead by commit time). It is the wrong answer on every
lap defect 1 lets through: with Phase 2 open, the frontier is Phase 1 whether it closed a minute ago or
was shipped hours ago in `7e03dc9`, because nothing in the walk knows *since when*. So on every
re-credited run and lap the gate names an already-shipped phase **by construction, not by staleness** —
**primary evidence, 13:49:** the three `graph-approval` requests the daemon sent edit (bus copies at
11:36:22, 12:22:06 and 13:39:39, one per run) all read verbatim *"Approve committing Phase 1: Establish
the boundary: the phase's work plus its spec update (commit only — push and PR wait for the final
gate)"* while every run's intent named Phase 2, and the approvals followed at 11:37:01 (edit's inbox),
13:00:29 and 13:43:27 (lifecycle, read by plan). Observed text, not inferred from code. The human
backstop is blind at the moment of authorization: the prompt asserts exactly the false premise the
predicate computed. Chronology, kept honest: the **first** approval preceded any warning — plan had
written that `phase-check` "should route to `stuck-gate`", a wrong prediction; the second and third
followed explicit written warnings (12:20 and 13:38) and were approved anyway, because the prompt in
front of the approver said the opposite. The harm was avoided each time only because the commit agent
declined to sweep an unattributed file — judgment, not a safeguard.

### Blast radius

Every `spec-to-pr` run started after its spec's first committed phase — which is **every re-run after
a failed or cancelled run**, the ordinary case (today's second run was exactly that). The commit sweeps
the whole working tree, so it ships whatever any agent has in flight, built or not. With defect 2, a spec
re-credits once per narrative `### Phase N …` heading **and once per phase whose steps are not yet
written**; with defect 3, one run can do it repeatedly — the inflated numerator compounds the reset
denominator.

### Family

The false-completion family — [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md)
(a node's evidence), [MUX-178](./MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md) (a spawn's
port), [MUX-182](../completed/MUX-182-cancelled-run-keeps-working-provenance-unreadable.md) (a run's provenance, closed 2026-09-24).
This is its **human-gate member**: it fakes no evidence, it converts a real approval into a commit of
unverified work. MUX-167's "ask before the guard" and MUX-144's attributable approvals both held; the
question they gated was wrong.

## Requirements

### Acceptance criteria

- [x] A run started on a spec with N already-complete phases does **not** open `phase-gate` until a phase completes that was not complete when the run started — 2026-09-24, `TestPhaseCommitReadyAnchorsOnHEAD` (a committed Phase 1 opens nothing and is not named)
- [x] A run that completes a phase **does** open `phase-gate` for it — **negative control: a predicate that never opens the gate is not a fix** — same test: completing Phase 2 in the tree opens the gate and names Phase 2
- [x] `phase-check` and the `phase-progress` guard still agree on every input (the MUX-167 property is preserved) — both call the one `phaseCommitReady(session)`; `phase_check_test.go` re-asserts it against the HEAD anchor
- [x] `graph retry --from` a node upstream of the commit does not re-credit a phase the run already committed — by construction: the predicate reads only the spec in the tree and at HEAD, so there is no per-run counter for a retry to reset (a dedicated retry test is Phase 3's open step)
- [ ] A phase with **no items at all** — a narrative `### Phase N …` heading, or a stub whose steps are not yet written — is never counted complete: complete means *at least one item and none open*; and `spec set` / `graph validate` warn on an item-less phase — first half done 2026-09-24 (`SpecPhase.Complete()` = `Done > 0 && len(Items) == 0`, `TestSpecPhaseCompleteness`); the `spec set` / `graph validate` warning is not yet written
- [x] A checkbox under a `####` subheading inside a phase counts as that phase's item — a heading line below `### Phase N` that is not itself a phase heading does not detach what follows — 2026-09-24, `parseSpecPhases` keeps the phase across `####`-and-deeper headings; `TestSpecPhaseCompleteness`
- [x] The `phase-gate` prompt names the phase it proposes to commit, and that phase is complete in the working tree and not at HEAD — today it names the completion frontier, which cannot tell just-closed from long-shipped — 2026-09-24, `${completed_phase}` now expands from the predicate's `phase` (lowest complete in the tree and not at HEAD), so the label and the guard cannot disagree
- [ ] `bash scripts/test-phase-commit-ready.sh` passes

### Technical approach — options, deliberately not yet chosen

| Option | Mechanism | For | Against |
|---|---|---|---|
| **1. Baseline at run start** | Record `SpecCompletedPhaseCount` in `run.json` when the run is created; `ready = completed >= baseline + shipped + 1` | Smallest change; per-run state already persists and survives restart | A phase completed in the tree but uncommitted when the run starts is baselined as done and never committed; defect 3 still needs `shipped` protected across retry |
| **2. HEAD-anchored** | `ready = completed(working tree) > completed(HEAD's copy of the spec)` — `git show HEAD:<spec>` | **No run state at all**; self-resets after every commit; measures exactly "complete and uncommitted", which is what the gate is for; immune to retry | A git read inside the predicate (already available via `PopulateGitInfo`); a spec absent at HEAD reads as 0 |
| **3. Commit-attributed** | Count phases named in commits on the branch | — | Parses commit messages; fragile by construction |

Option 2 fits the gate's meaning best and removes the frame mismatch rather than compensating for it.
Phase 2 decides. Defect 2 is orthogonal and small, in three parts: `SpecCompletedPhaseCount` counts a
phase complete only when it has at least one item and none open (an empty phase is *empty*, not
*done*); `SpecPhases` keeps `cur` across non-phase subheadings (`####` and deeper) so nested lists
count for the enclosing phase; and `phaseHeadingRe` tightens to `^### Phase N:` (every real phase
heading in this repo uses the colon) or `spec set` warns on an item-less phase.

### Key files

| File | Purpose |
|---|---|
| `tools/muxcode/bus/graph_exec.go` | `phaseCommitReady:511`, `phase-progress` guard call `:464`, `resolveCompletedPhaseText:725` |
| `tools/muxcode/bus/conditions.go` | `spec_phase_committable` condition `:393` |
| `tools/muxcode/bus/spec_items.go` | `phaseHeadingRe:72`, `SpecPhases:80` (`Items` = open boxes only, `:109-114`), `SpecCurrentPhase:123`, `SpecCompletedPhaseCount:162-173` (`len(p.Items) == 0`) |
| `tools/muxcode/bus/graph_run.go` | `EdgeFires` (`:53`, `:200`), retry reset `:640-644` |
| `tools/muxcode/bus/graph_templates.go` | `spec-to-pr` `phase-check`/`phase-gate`/`commit` `:42-44`, edges `:59-62` |
| `scripts/test-multi-phase-graph.sh` | Existing multi-phase harness to extend or model on |
| `tools/muxcode/bus/phase_anchor_test.go` | The anchor's tests (Phase 3): pin + negative control, real-git HEAD reads, moved-spec baseline, fail-closed seam |

## Implementation

### Phase 1: Establish the boundary

- [x] Pin the defect: a unit test with one spec (Phase 1 complete) and a fresh run whose commit node has fired zero times — assert today's predicate says ready, then invert the assertion with the fix — `TestPhaseCommitReadyAnchorsOnHEAD` (`bus/phase_anchor_test.go`), 2026-09-24; the fix and the pin landed together
- [x] Record what `${completed_phase}` rendered on run `1789399519`'s gate (lifecycle log / gate message) and whether it named Phase 1 — **established by code, 13:47**: `SpecJustCompletedPhase` returns the last complete phase before the first open one, so with Phase 2 open it names Phase 1 on every lap; **primary evidence 13:49**: all three runs' `graph-approval` requests (bus copies 11:36:22, 12:22:06, 13:39:39) read *"Approve committing Phase 1: Establish the boundary …"* with every intent at Phase 2
- [x] Confirm defect 2 with a fixture spec carrying a `### Phase 1 findings` heading **and** a stub `### Phase 4:` with no boxes, and count the inflation from each — confirmed by the fix's own fixtures in `TestSpecPhaseCompleteness` (narrative heading and stub phase each read *empty*, not *done*), 2026-09-24
- [x] Confirm defect 3 with `graph retry --from update-spec` on a run that has committed once — made moot 2026-09-24: the anchor removed the `EdgeFires` counter from the predicate, so there is nothing left for a retry to reset; never reproduced live
- [x] Enumerate every consumer of `SpecCompletedPhaseCount` and `phaseCommitReady` and state which the fix changes — all three consumers now share `phaseCommitReady(session)`: the `phase-progress` guard (`phaseProgressGuardAllows`), the `spec_phase_committable` condition (`evalSpecPhaseCommittable`) and `${completed_phase}` (`resolveCompletedPhaseText`); `SpecCompletedPhaseCount` is replaced by `completedPhaseCount` over `SpecPhase.Complete()`

### Phase 2: Choose the anchor

- [x] Weigh options 1–3 against Phase 1's findings and record the choice and rationale here — **option 2, HEAD-anchored** (Decision 1 below), recorded 2026-09-24 from edit's relay of the implementation
- [x] Decide the defect 2 remedy — tighten the regex, or warn on an item-less phase — and record it — neither, for now: the decisive half is `SpecPhase.Complete()` requiring at least one item, plus `####` subheadings staying inside their phase; the regex is untouched and the `spec set` warning is left open under the acceptance criteria (Decision 2 below)
- [x] Decide whether defect 3 is closed by the anchor choice or needs `shipped` protected on retry — **closed by the anchor**: the predicate carries no run state, so `graph retry`'s `EdgeFires` reset cannot reach it

### Phase 3: Implement

- [x] Implement the chosen anchor with unit tests — `phaseCommitReady(session)` compares `SpecPhases(tree)` with `parseSpecPhases(specAtHEAD(...))` through `newlyCompletedPhases`; `TestPhaseCommitReadyAnchorsOnHEAD`, `TestNewlyCompletedPhases`, `TestSpecAtHEADReadsCommittedCopy` (real git), 2026-09-24
- [x] **Negative control:** a phase completed during the run still opens `phase-gate` — `TestPhaseCommitReadyAnchorsOnHEAD`, second half
- [x] `phase-check` and `phase-progress` share the fixed predicate and a test asserts they agree — one function, `phase_check_test.go` updated to the HEAD anchor
- [x] Fix defect 2 as decided, with tests that neither a narrative `### Phase N …` heading nor an item-less stub phase counts complete, that boxes under a `####` subheading count for the enclosing phase — and that an all-ticked phase still does (negative control) — `SpecPhase.Complete()`, `parseSpecPhases`; `TestSpecPhaseCompleteness`
- [ ] Fix defect 3 as decided, with a retry test — closed by construction (see Phase 2); the retry test is still to be written
- [x] `phase-gate`'s prompt states the phase and that it is complete and uncommitted — `${completed_phase}` from the predicate's `phase`

#### In flight 2026-09-24 — review must-fix on the HEAD-anchored implementation

Implementation landed in the working tree of the `MUX-182-cancelled-run-keeps-working` branch
(`phaseCommitReady(session)` now anchored on the spec's content at HEAD via `specAtHEADFn`,
`graph_exec.go:491-517`; `spec_items.go` reworked; `phase_check_test.go`, `graph_test.go`, the
`spec-to-pr` template and two integration scripts touched) — without this spec moving to `drafts/`
or the phase boxes above being ticked. Review `/tmp/muxcode-review-1790260651.txt` returned two
must-fix on it, recorded here so they have a home:

- [x] **Must-fix (`graph_exec.go:512-517`) — closed 2026-09-24 10:52** (`specAtHEAD`: `""` only for an unborn HEAD or a file HEAD holds under neither path nor name; every other failure is an error and the gate holds; `TestSpecAtHEADFailsClosed` against the real git seam). Original: `specAtHEADFn` converts every `rev-parse`/`cat-file` failure into an empty, successful baseline — an inaccessible or corrupt repository, a missing `git`, or an unreadable object makes every completed tree phase commit-eligible instead of holding the guard. Distinguish a verified unborn HEAD or absent path from operational failures; propagate the rest. Failure regressions against the default git seam, not only a stub returning an error
- [x] **Must-fix (`graph_exec.go:515-517`) — closed 2026-09-24 10:52** (a path absent at HEAD is looked up by its id-bearing file name across `ls-tree`; more than one match is an error; `TestSpecAtHEADFollowsAMovedSpec`, `TestPhaseCommitReadyAnchorsOnHEAD`). Original: the baseline reads only the active path at HEAD. Move an already-committed spec `backlog/` → `drafts/`, update the pointer, leave its complete Phase 1 unchanged: the new path is absent at HEAD, the baseline is empty, Phase 1 is re-credited — the false-completion defect reproduced in the normal spec lifecycle. Resolve the committed predecessor of a renamed spec, or hold when a missing baseline cannot be told from new work. Real-git rename regression with a complete phase and an open next phase

### Phase 4: Integration test

- [ ] Create `scripts/test-phase-commit-ready.sh` (hermetic; scratch repo, bus, tmux session and daemon)
- [ ] Test: spec with Phase 1 complete **and committed**; a fresh `spec-to-pr`-shaped run reaches `phase-check` → routes to `stuck-gate`, `phase-gate` never opens
- [ ] **Negative control:** complete Phase 2 in the tree → `phase-check` opens `phase-gate`
- [ ] Test: `graph retry --from update-spec` after one commit does not reopen `phase-gate` for the same phase
- [ ] Test: a spec with a `### Phase 1 findings` section and an item-less stub phase is credited for neither
- [ ] Test: a phase whose open boxes sit under a `####` subheading is not credited complete
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Open decisions

### Decision 1 — anchor (Phase 2)

Option 2 is recommended above; the counter-argument is that a predicate reading git is a new dependency
in a path that today reads only files under `BusDir`.

**Resolved 2026-09-24: option 2, HEAD-anchored**, implemented in `bus/graph_exec.go` and relayed by
edit. `phaseCommitReady(session)` is ready when a phase is complete in the working tree and not in
HEAD's copy of the spec; it takes no run state. `specAtHEAD` is strict: `""` only for an unborn
HEAD or a file HEAD holds under neither its path nor its id-bearing file name (so a spec moved
`backlog/` → `drafts/` keeps its baseline); every other git failure is an error, and the gate holds.
The counter-argument stands as accepted cost — the git read is the price of measuring "committed"
in the frame that means it.

### Decision 2 — is a phase heading with no items an error?

Tightening the regex silently stops counting sections an author meant as phases but wrote without a
colon. Warning at `spec set` time is louder and teaches the contract; the two are not exclusive.
Independently of either, `SpecCompletedPhaseCount` should stop calling an empty phase complete — that
half needs no decision, only the negative control that an all-ticked phase still counts.

**Partly resolved 2026-09-24**: the no-decision half shipped (`SpecPhase.Complete()` needs at least
one item and none open; `####`-and-deeper headings stay inside their phase). Regex tightening and the
`spec set` / `graph validate` warning are both still open — the acceptance criterion keeps its box.

### Decision 3 — should the gate prompt show the evidence?

"Approve committing Phase 2" is a premise. "Phase 2 completed this run: 7/7 in the tree, 6/7 at HEAD"
is evidence the approver can check. Cheap once option 2 exists. Today the prompt is not merely thin —
on a re-credited lap it is false, naming a phase already shipped, so the approver is asked to confirm
a claim the machinery itself got wrong.

## Out of scope

- **Whether the user should have approved.** The prompt was the control; the approval was reasonable given it.
- **What the swept commit contained** — the spawn-road change is MUX-148's Defect 3.
- **The reset and recommit** — a manual recovery, not a mechanism.

## Status

Complete — closed 2026-09-24 **on the user's instruction ("remove this from backlog because it's
done"), by acceptance, at 21/32.** The fix is on `main` in PR #89 (`3e9ac86`): `phaseCommitReady`
anchored on HEAD's copy of the spec (`specAtHEAD` strict — git failures hold the gate; a moved spec
is followed by its id-bearing filename), an item-less phase is never complete, `####` boxes count
for their phase; review `1790261523` 0 must-fix, build and test green. **Open at closure, recorded
not done:** Phase 3's retry test; the `spec set` / `graph validate` warning on an item-less phase
(the second half of AC 5); all of Phase 4 — `scripts/test-phase-commit-ready.sh` and its controls.
Phases 1–2 complete, Phase 3 at 5/6, Phase 4 at 0/7; acceptance criteria 6/8. Whoever picks the
residual up files it as its own backlog item or reopens this one; the boxes above stay open so the
record is honest.

Before closure — entered `drafts/` 2026-09-24 on the user's instruction relayed by edit, with the
implementation already in the MUX-182 branch's working tree.

Filed 2026-09-14 on the user's instruction relayed by edit, from plan's first-hand observation on run
`1789399519`. Defects 2 and 3 were found during filing verification and are plan's, not the report's.
MUX-148's two narrative `### Phase N …` headings were renamed the same day so that spec's live phase
count stops being inflated, and its inherited-constraints list was moved under Phase 3 so the parser
counts it there.

Sharpened 12:20 on edit's review: defect 2 restated as the done/empty conflation — verified that
`Items` holds open boxes only (`:109-114`), so a stub phase with no steps written reads as complete
alongside the narrative-heading case; the compounding with defects 1 and 3 is stated.

Corrected 12:25 on edit's review (must-fix, verified at `spec_items.go:97`): a `####` subheading
detaches the boxes below it from any phase. Plan's own remedy in MUX-148 — H4 labels under Phase 3 —
had made that phase read complete; the labels are bold text now, and the wrong claim that an H4 list
counted toward Phase 2 is struck in Defect 2 rather than removed.
