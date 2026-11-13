# The Spec-Verification Gate Asks Its Own Question — `notifyPlanOnReview` Hardcodes `"success"`

**Tracking:** [mkober/muxcode#130](https://github.com/mkober/muxcode/issues/130)

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
while the active pointer is MUX-163; that is [MUX-150](../backlog/MUX-150-verify-spec-names-last-routed-batch.md),
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
changed-file list naming another spec's work ([MUX-150](../backlog/MUX-150-verify-spec-names-last-routed-batch.md)),
the reviewed-marker transition itself (it is right that the workflow state moves on any completed
review), and what plan does once dispatched.

## Requirements

### Acceptance criteria

- [x] A review whose reply carries `EXIT=1` fires no `verify-spec`; a `plan-verify-withheld` lifecycle row names the review and its outcome _(amended 2026-11-13 — the verdict is the reply's sentinel; see [Findings for Phase 1](#findings-for-phase-1))_
- [x] A passing review still fires exactly as today (negative control), and the reviewed-marker transition is unaffected in both cases
- [x] `NotifyPlanOn` becomes load-bearing: with `["success"]` a failing review withholds and a passing one fires; with `["*"]` both fire; with `[]` neither does — each pinned by a test
- [x] The outcome is read from evidence the reply itself carries, not inferred: its `EXIT=<n>` sentinel (the last one in the reply). Review history rows are **not** read — they carry no request or reply id, and review logs after it replies, so a row cannot be attributed to a reply. A reply with no sentinel is an unknown verdict and the fire is **withheld** with a row saying why; so is `EXIT=0` beside a findings line counting a must-fix (`conflicting verdict`). Fail closed — a verification pass on an unknown verdict is the defect _(amended 2026-11-13; originally "the review history row's `outcome`/`exit_code`, falling back to the reply's `EXIT=` sentinel")_
- [x] Docs: `CLAUDE.md` spec-verification constraint, `docs/architecture.md` spec-verification section, `docs/hooks.md` chain table — Phase 2

### Technical approach

**Primary — pass the outcome the reviewer produced.** `notifyPlanOnReview` takes the review's
outcome as a parameter; `checkInboxes` derives it at the trigger from the `EXIT=` sentinel in the
reply that tripped the marker, and withholds when the reply carries none or contradicts itself. The
review history row is not consulted (amended 2026-11-13 — the original approach read the newest row
first and fell back to the sentinel; see [Findings for Phase 1](#findings-for-phase-1)).
`ChainShouldNotifyPlan` is unchanged — it becomes reachable with a real
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

- [x] `notifyPlanOnReview(outcome string)`; `checkInboxes` derives it from the `EXIT=` sentinel of the reply that tripped the marker, and withholds when the reply carries none or pairs `EXIT=0` with a must-fix count — history rows are not read _(amended 2026-11-13)_ — `bus.ReviewReplyOutcome` (`bus/review_verdict.go`), called on the reply `NewestMessageFrom` returns; the signature is `notifyPlanOnReview(reviewID, outcome, evidence)`
- [x] `plan-verify-withheld` lifecycle row naming the outcome and the review message id; the movement marker stays unwritten so a later passing review fires — detail `<outcome> <review-id> (<evidence>)`, or `unknown <review-id> (<reason>)` at warn; `TestReviewVerdict_LaterPassingReviewFires`
- [x] Tests: `failure` → no fire, one row; `success` → fires as today (negative control); no row and no sentinel → withheld; `NotifyPlanOn` `["success"]` / `["*"]` / `[]` each pinned — the `["success"]` vs `["*"]` pair must **disagree** on a failing review, which is the assertion today's code cannot pass — `daemon/review_verdict_test.go`: `FailureWithholds`, `PassingFires`, `UnknownWithholds`, `NotifyPlanOnMatrix` (six cases; `["success"]` 0 and `["*"]` 1 on the failing review), plus `PreviousReviewsLateRowIgnored` and `SameSecondConsecutiveReviews` from the review must-fix
- [x] The reviewed-marker transition still happens on a failing review (it is the workflow state, not the verdict) — asserted in `TestReviewVerdict_FailureWithholds` (state `reviewed`, marker written, movement marker not)

### Findings for Phase 1

Recorded 2026-11-13 from the fix node of run `1794608375-55-defect-backlog-loop`.

| Finding | Detail |
|---------|--------|
| **The verdict source changed — a review must-fix, not a user decision** | The first implementation (`bus/review_verdict.go`) followed this spec: newest review history row first, the reply's sentinel as fallback. Review rejected it. Review replies **before** it logs, and a history row carries no request or reply id — neither a `muxcode log` row nor a `NewBusResponseEntry` row — so the helper attributed a row to a reply by time window alone. Review A replies at T and logs success at T+1; review B at T+2, with no sentinel, inherited A's success and fired `verify-spec`. A late failure row from A likewise contradicted B's valid `EXIT=0` and withheld it for good |
| The fix the worker chose | The reviewer named two: correlate the row to the actual review, or fall back to the current reply's sentinel and treat anything else as unknown. No id exists to correlate on, so the worker took the second: the verdict is read **only** from the reply — its last `EXIT=<n>` sentinel (`parseExitSentinel`). No sentinel → `unknown`, withheld (`plan-verify-withheld unknown <id> (unknown verdict: the reply carries no EXIT sentinel)`). `EXIT=0` beside a findings line counting must-fix > 0 → `unknown`, withheld (`conflicting verdict`) — the guard for the 2026-09-23 "EXIT meant ran" pattern |
| What this changes in the requirement | Acceptance criteria 1 and 4, the technical approach, Phase 1's first step and Phase 3's cases were amended by plan at the worker's request; the original wording is kept inline. The amended rule is narrower in what it trusts and still fails closed. Its cost: a reviewer that omits the sentinel now always withholds, where the original would have accepted its history row |
| Context left as written | [Mechanism](#mechanism--verified-in-code) still says the real outcome is "available at both ends". The history-row end is real but unattributable — the reason it is no longer used |

### Phase 2: Docs

- [x] `CLAUDE.md` spec-verification constraint: the gate reads the review's outcome, and a withheld fire is logged; `docs/architecture.md` spec-verification section; `docs/hooks.md` chain table — `CLAUDE.md` by edit; `docs/architecture.md` gains the verdict table, the withheld row and why history rows are not read; `docs/hooks.md` gains a daemon-driven "Review reply → plan verify-spec" edge and step 8 of the Build-Test-Review chain

### Phase 3: Integration test

- [x] Hermetic section (scratch `BUS_SESSION`, seeded review→edit reply, active spec set): a reply ending `EXIT=1` → no `verify-spec` in plan's inbox and a `plan-verify-withheld` row; a reply ending `EXIT=0` → the dispatch arrives (negative control) _(amended 2026-11-13 — the cases were seeded history rows)_
- [x] A seeded `EXIT=1` sentinel with no history row → withheld; neither → withheld with the "unknown verdict" reason; and a **passing history row beside a sentinel-less reply → withheld**, proving the row is not read _(last case added 2026-11-13)_
- [x] Run the section and record the counts in this spec

Run through the run agent on 2026-11-13 18:28. The hermetic section became its own script,
`scripts/test-plan-verify-verdict.sh` (task `1794612511-spawn-100fae85-5ff92399`, log
`/tmp/test-plan-verify-verdict.log`): **36 passed, 0 failed, coverage floor 35 met, exit 0.**

| Case | Seeded | Expected |
|------|--------|----------|
| A | A reply ending `EXIT=1`, no history row | Withheld |
| B | A reply with no sentinel | Withheld, unknown verdict |
| C | A passing history row beside a sentinel-less reply | Withheld — the row is not read |
| D | `EXIT=0` beside a must-fix count | Withheld, conflicting verdict |
| E | A reply ending `EXIT=0` (negative control) | `verify-spec` arrives; also shows the withheld fires before it left the movement marker unwritten |

The new gate changed one existing script: the review replies `scripts/test-verify-spec-refire.sh`
seeds now end `EXIT=0`, because a sentinel-less reply is withheld. Re-run the same minute (task
`1794612534-spawn-100fae85-24d4ecdb`): **43 passed, 0 failed, coverage floor 42 met, exit 0.**

## Notes

- Filed 2026-09-09 17:03 by plan on its own observation, from the second occurrence — the first was
  recorded the same day in MUX-159's Notes as side-finding (c) and not filed. Both fires were caught
  and reported by plan rather than acted on; no criterion was ticked on a rejected review.
- The 16:59 review's own findings (`deliver.go:123`, `:149`) are MUX-171's implementation, in flight
  with edit — unrelated to this defect except as the occasion.
- Related: [MUX-150](../backlog/MUX-150-verify-spec-names-last-routed-batch.md) (the same dispatch's
  changed-file list names the last routed batch, not the spec's work — the two compound);
  [MUX-007](../completed/MUX-007-verify-spec-stale-review-refire.md) (the three controls this fire
  already has, all about the pointer, none about the verdict);
  [MUX-127](../backlog/MUX-127-review-completion-routing.md) (review-completion routing, the same junction).

## Status

Complete

Closed out 2026-11-13 by `55-defect-backlog-loop` run `1794608375`, which activated it the same day.
All three phases and five acceptance criteria are checked. Criteria 1 and 4 were amended mid-run
after a review must-fix — the verdict is read from the reply's `EXIT=` sentinel alone, not from a
history row — a change made by the run's worker and recorded under
[Findings for Phase 1](#findings-for-phase-1), not a user decision. Last seen 2026-11-13 17:24:
`verify-spec` for MUX-168 arrived while that run's review node stood `failed` on a must-fix.

Filed 2026-09-09 17:03, from the second of **four** occurrences that hour
(~13:30, 16:59:50, 17:03:25, 17:07:13). The third fired one second after a review whose reply text
ends in the literal `EXIT=1`; the fourth preceded a red suite by twenty seconds. Every one was
caught by plan re-deriving the verdict from the history store — which is the argument for the fix,
not against it.
