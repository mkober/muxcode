# The Spec-Verification Gate Asks Its Own Question — `notifyPlanOnReview` Hardcodes `"success"`

The review chain is configured to hand plan a `verify-spec` only on a passing review:
`NotifyPlanOn: []string{"success"}`. The daemon consults that config with the outcome **written into
the call** — `bus.ChainShouldNotifyPlan("review", "success")` — so the gate is asked whether it would
fire on success, never whether *this* review succeeded. It always answers yes. The trigger upstream
is equally blind: any message from review to edit whose id differs from the last one. A review that
returns `EXIT=1` with must-fix findings therefore sends plan to verify a spec against work the
reviewer just rejected, and plan — which is told the review completed — must notice the contradiction
itself or tick criteria on refused code. It happened twice on 2026-09-09; the second time the review
carried two must-fix findings and the suite had not been run at all.

## Context

### Observed (session `muxcode`, 2026-09-09)

| When | What | Source |
|------|------|--------|
| ~13:30 | first occurrence, recorded the same day as a side-finding in [MUX-159](../completed/MUX-159-codex-hooks-provider.md)'s Notes ("verify-spec fired on an `EXIT=1` review") — noted, not filed | MUX-159 Notes |
| 16:58:46 | the only check run for the change under review: `gofmt -l bus/ daemon/`. No `./test.sh` row exists for it | test store |
| 16:59:49 | review row: `outcome=failure`, `exit_code=1` — 2 must-fix (`deliver.go:123` provider scope, `deliver.go:149` guard placement), 1 should-fix | review store |
| 16:59:50 | `workflow reviewed from=analyzing trigger=daemon:review-complete`, then `plan-verify docs/requirements/drafts/MUX-163-…` | lifecycle |
| 16:59:50 | plan receives `verify-spec` — "Review complete — verify progress against spec …" | plan inbox |
| 17:03:24 | third occurrence, one hour after the first: the follow-up review returns `outcome=failure exit 1`, both must-fixes **unchanged**, its reply text ending in the literal `EXIT=1` | review store |
| 17:03:25 | plan dispatched again, one second later. The suite had passed at 17:02:42, so the fire cannot be excused as reading the suite instead — nothing on this path reads any verdict | lifecycle, plan inbox |
| 17:07:12 | fourth occurrence: review `outcome=failure exit 1`, 1 must-fix, 3 should-fix | review store |
| 17:07:13 | plan dispatched. Twenty seconds later `./test.sh` returned **exit 1** — so this fire preceded a red suite as well as a failed review | lifecycle, test store |

The dispatch also names a changed file belonging to a different spec's work (`deliver_test.go`, MUX-171)
while the active pointer is MUX-163; that is [MUX-150](./MUX-150-verify-spec-names-last-routed-batch.md),
a separate defect that compounds this one.

### Mechanism — verified in code

- `daemon/daemon.go:486` `notifyPlanOnReview` — first line is
  `if !bus.ChainShouldNotifyPlan("review", "success") { return }`. The second argument is a literal.
  The function never reads the review's outcome, and no outcome is passed to it.
- `bus/profile.go:437` `ChainShouldNotifyPlan(eventType, outcome)` — matches `outcome` against the
  `NotifyPlanOn` entries. It is correct; it is asked the wrong question. With the argument fixed at
  `"success"`, `["success"]` and `["*"]` are indistinguishable, and only an empty or absent list
  disables the fire. **The configuration point documented in `CLAUDE.md` ("Gated by `NotifyPlanOn` on
  the review chain (default `["success"]`)") does not exist in behaviour.**
- `daemon/daemon.go:420–428` `checkInboxes` — the trigger is `NewestMessageIDFrom(session, "edit",
  "review")` differing from the reviewed marker: the *arrival* of a review reply, not its verdict.
  The three MUX-007 controls documented above `notifyPlanOnReview` all concern the spec pointer and
  the changed-file list; none concerns the outcome.
- The real outcome is available at both ends: the review history row carries `outcome` and
  `exit_code` (`ReadLatestHistory`-shaped access, as `deriveSendOutcome` already uses for graph
  routing), and the reviewer's reply text ends in the `EXIT=0` / `EXIT=1` sentinel that the graph
  executor reads.

### Why it matters more than a wasted pass

Plan is the instrument that decides a phase is done. Handing it a rejected review inverts the
control: the daemon's message says "review complete", the spec's criteria look satisfiable, and the
only thing standing between a must-fix review and a ticked acceptance criterion is plan re-deriving
the verdict from the store every time. That worked here — both fires were caught and reported — but
a verification instrument whose correctness depends on the verifier distrusting its own trigger is
not a control. This is the family the backlog's own ordering rule names: *repair the instruments you
will verify the rest with*.

### Scope boundary

In scope: the outcome the gate reads, and a lifecycle row when a fire is withheld. Not in scope: the
changed-file list naming another spec's work ([MUX-150](./MUX-150-verify-spec-names-last-routed-batch.md)),
the reviewed-marker transition itself (it is right that the workflow state moves on any completed
review), and what plan does once dispatched.

## Requirements

### Acceptance criteria

- [ ] A review whose recorded outcome is `failure` (or whose reply carries `EXIT=1`) fires no `verify-spec`; a `plan-verify-withheld` lifecycle row names the review and its outcome
- [ ] A passing review still fires exactly as today (negative control), and the reviewed-marker transition is unaffected in both cases
- [ ] `NotifyPlanOn` becomes load-bearing: with `["success"]` a failing review withholds and a passing one fires; with `["*"]` both fire; with `[]` neither does — each pinned by a test
- [ ] The outcome is read from evidence, not inferred: the review history row's `outcome`/`exit_code`, falling back to the reply's `EXIT=` sentinel; when neither is available the fire is **withheld** with a row saying why (fail closed — a verification pass on an unknown verdict is the defect)
- [ ] Docs: `CLAUDE.md` spec-verification constraint, `docs/architecture.md` spec-verification section, `docs/hooks.md` chain table

### Technical approach

**Primary — pass the outcome the reviewer produced.** `notifyPlanOnReview` takes the review's
outcome as a parameter; `checkInboxes` derives it at the trigger from the newest review history row
(`outcome`, `exit_code`), falling back to the `EXIT=` sentinel in the reply that tripped the marker,
and withholds on neither. `ChainShouldNotifyPlan` is unchanged — it becomes reachable with a real
argument for the first time. A withheld fire writes `plan-verify-withheld <outcome> <review-id>`, and
like the existing pointer-withhold path leaves the movement marker unwritten so a later passing
review still fires.

**Rejected — let plan decide.** It is what happens today, and it is why this took two occurrences to
file: the check lives in the least reliable place, downstream of a message that asserts the opposite.

**Rejected — drop `NotifyPlanOn` and always fire.** Honest about current behaviour, but it makes the
review verdict irrelevant to the verification pass and abandons a documented control rather than
implementing it.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | `notifyPlanOnReview` (486) — the hardcoded `"success"`; `checkInboxes` (420–428) — the trigger and where the outcome is derived |
| `tools/muxcode/bus/profile.go` | `ChainShouldNotifyPlan` (437), `NotifyPlanOn` (49), the review chain default (1040) |
| `tools/muxcode/bus/history.go` | the review row's `outcome`/`exit_code` — the evidence read |
| `tools/muxcode/bus/graph_exec.go` | `deriveSendOutcome` — the existing precedent for reading a verdict from a history row plus sentinel |
| `CLAUDE.md`, `docs/architecture.md`, `docs/hooks.md` | the constraint, the spec-verification section, the chain table |

## Implementation

### Phase 1: Read the verdict

- [ ] `notifyPlanOnReview(outcome string)`; `checkInboxes` derives it from the newest review history row, falling back to the reply's `EXIT=` sentinel, and withholds when neither is available
- [ ] `plan-verify-withheld` lifecycle row naming the outcome and the review message id; the movement marker stays unwritten so a later passing review fires
- [ ] Tests: `failure` → no fire, one row; `success` → fires as today (negative control); no row and no sentinel → withheld; `NotifyPlanOn` `["success"]` / `["*"]` / `[]` each pinned — the `["success"]` vs `["*"]` pair must **disagree** on a failing review, which is the assertion today's code cannot pass
- [ ] The reviewed-marker transition still happens on a failing review (it is the workflow state, not the verdict)

### Phase 2: Docs

- [ ] `CLAUDE.md` spec-verification constraint: the gate reads the review's outcome, and a withheld fire is logged; `docs/architecture.md` spec-verification section; `docs/hooks.md` chain table

### Phase 3: Integration test

- [ ] Hermetic section (scratch `BUS_SESSION`, seeded review history row + review→edit reply, active spec set): a `failure` row → no `verify-spec` in plan's inbox and a `plan-verify-withheld` row; a `success` row → the dispatch arrives (negative control)
- [ ] A seeded `EXIT=1` sentinel with no history row → withheld; neither → withheld with the "unknown verdict" reason
- [ ] Run the section and record the counts in this spec

## Notes

- Filed 2026-09-09 17:03 by plan on its own observation, from the second occurrence — the first was
  recorded the same day in MUX-159's Notes as side-finding (c) and not filed. Both fires were caught
  and reported by plan rather than acted on; no criterion was ticked on a rejected review.
- The 16:59 review's own findings (`deliver.go:123`, `:149`) are MUX-171's implementation, in flight
  with edit — unrelated to this defect except as the occasion.
- Related: [MUX-150](./MUX-150-verify-spec-names-last-routed-batch.md) (the same dispatch's
  changed-file list names the last routed batch, not the spec's work — the two compound);
  [MUX-007](../completed/MUX-007-verify-spec-stale-review-refire.md) (the three controls this fire
  already has, all about the pointer, none about the verdict);
  [MUX-127](./MUX-127-review-completion-routing.md) (review-completion routing, the same junction).

## Status

**Backlog** — 0/13. Filed 2026-09-09 17:03, from the second of **four** occurrences that hour
(~13:30, 16:59:50, 17:03:25, 17:07:13). The third fired one second after a review whose reply text
ends in the literal `EXIT=1`; the fourth preceded a red suite by twenty seconds. Every one was
caught by plan re-deriving the verdict from the history store — which is the argument for the fix,
not against it.
