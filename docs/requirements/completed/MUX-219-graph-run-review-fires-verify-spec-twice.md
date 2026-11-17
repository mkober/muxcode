# MUX-219: A Graph Run's Review Fires `verify-spec` Twice

**Tracking:** [mkober/muxcode#200](https://github.com/mkober/muxcode/issues/200)

When a graph run owns the review, plan is asked to verify the same review twice in the same second:
once by the daemon's review chain (`notifyPlanOnReview`) and once by the run's own `update-spec`
node. Plan answers both. Every phase of every `55-defect-backlog-loop` and `50-spec-to-pr` run pays
a duplicate plan turn, and the two passes edit the same spec file's checkboxes.

## Context

### Source and standard of evidence

Filed 2026-11-14 on the user's instruction relayed by edit, from edit's handoff
(`/tmp/defect-verify-dup.md`). Plan was the recipient of every request below and confirms the pairs
first-hand; the lifecycle rows were read from `muxcode lifecycle show --event plan-verify`. The
mechanism is read from the tree; the fix is not designed beyond the approach below.

### Observed (2026-11-14, run `1794626267-55-defect-backlog-loop-15547e2e`)

| Time | Spec | Daemon review-chain request | Graph `update-spec` request |
|------|------|-----------------------------|-----------------------------|
| 09:26:37 | MUX-189 Phase 1 | `1794666397-daemon-ed9e00b5` | `1794666397-daemon-3c0a782b` |
| 09:34:21 | MUX-189 Phase 2 | `1794666861-daemon-864ca161` | `1794666861-daemon-d975796d` |

Both requests of a pair arrived in one inbox read. Plan did the verification once and answered the
second "Same verification as the graph's request". The same pairing ran through MUX-216, MUX-217
and MUX-009 on 2026-11-13 and 2026-11-14.

Two neighbouring fires show where the line falls:

| Time | What fired | Why it matters |
|------|------------|----------------|
| 09:31:40 | Daemon request only (`1794666700-daemon-c4898961`), for a graph-owned review that returned `EXIT=0` with 1 should-fix | The graph routed that review to its `fix` node and asked for no verification; the daemon asked anyway. Suppression covers this too |
| 09:18:40 | Daemon request only (`1794665920-daemon-a59f9ae8`), for a review edit requested by hand while the run sat at `ship-gate` | Not graph-owned. It was the only verification of a fix made after close-out, and must keep firing |

### Mechanism — read from the tree

| Step | Code | Behaviour |
|------|------|-----------|
| A review reply reaches edit | `daemon/daemon.go:477` | `d.notifyPlanOnReview(reply.ID, outcome, evidence)` for any review reply whose verdict passes the `NotifyPlanOn` gate |
| The daemon sends plan `verify-spec` | `daemon/daemon.go:546-619` (`notifyPlanOnReview`) | Checks verdict, active spec and movement since the last fire; logs `plan-verify <spec>`. It does not ask whether a graph run dispatched the review |
| The run sends plan `verify-spec` | the template's `update-spec` send node | Fires on the review node's success edge, carrying the worker's report and requiring an `EXIT=<n>` sentinel |
| The precedent | `cmd/hook.go:180` | A graph-owned test run's success logs `chain-suppressed` naming the run and node and fires no test→review (MUX-213) |

`plan-verify-suppressed` already exists as an event (`daemon.go:578`, "nothing moved since last
verify-spec"); a graph suppression needs a detail that reads differently, or its own event name.

### Cost

- One wasted plan turn per phase, on every graph run with a review node.
- Two requests for one piece of work invite two edits of the same checkboxes. Plan has so far
  noticed the pair and answered the second without re-editing; nothing enforces that.
- The daemon's request carries no worker report and names only the last routed batch's files
  ([MUX-150](../backlog/MUX-150-verify-spec-names-last-routed-batch.md)), so when it arrives first plan
  verifies with less than the graph's request would have given it.

### Family

- [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) — graph ownership suppressing a hook chain, and every review answered twice.
- [MUX-172](../completed/MUX-172-plan-verify-gate-hardcodes-success.md) — the gate this fire already passes through.
- [MUX-150](../backlog/MUX-150-verify-spec-names-last-routed-batch.md) — what the daemon's request names.

## Requirements

### Acceptance criteria

- [x] A review dispatched by a graph run's executor sends plan no daemon `verify-spec`; the run's `update-spec` node is the one verification
- [x] The withheld fire writes one lifecycle row naming the run and node, distinguishable from the existing "nothing moved" `plan-verify-suppressed` row
- [x] A withheld fire leaves the movement marker unwritten, so a later review outside the run still fires
- [x] A graph-owned review that routes to `fix` (no `update-spec`) also sends plan nothing from the daemon
- [x] Negative control: a review requested outside any graph run still fires exactly one `verify-spec`, including while a run is parked on a gate
- [x] The reviewed marker and the `reviewed` workflow transition still happen for a graph-owned review
- [x] `bash scripts/test-graph-verify-once.sh` passes

### Technical approach

Decide ownership from the review **request**, not from whether a run happens to be live: a dispatch
carries its run and node (`Message.GraphRun` / `GraphNode`). In the daemon's review-reply handling,
resolve the request the reply answers; if it names a graph run, log the suppression with run and
node and return before `notifyPlanOnReview` writes its movement marker. A run merely existing — the
09:18:40 case, parked at `ship-gate` — must not suppress.

### Key files

| File | Role |
|------|------|
| `tools/muxcode/daemon/daemon.go` (`notifyPlanOnReview`, the review-reply branch near `:477`) | Where the daemon fires and where the ownership check goes |
| `tools/muxcode/bus/message.go` (`GraphRun`, `GraphNode`) | The provenance a dispatch carries |
| `tools/muxcode/bus/graph_exec.go` | How the executor stamps its review dispatch |
| `tools/muxcode/cmd/hook.go` (`chain-suppressed`) | The precedent's wording |
| `scripts/test-plan-verify-verdict.sh` | The existing script for this fire; its cases must still pass |
| `CLAUDE.md`, `docs/architecture.md` | The spec-verification passage |

## Implementation

### Phase 1: Pin the duplicate

- [x] Unit test (kept as the negative control's mirror): a review reply answering a graph-stamped request reaches `notifyPlanOnReview` today and a `verify-spec` is sent
- [x] Confirm from the tree whether a review reply can be traced to its request's `GraphRun` in the daemon's review branch; record what is available here

### Results for Phase 1

Recorded 2026-11-17 from the worker's request; plan found the test in the tree and the reply lookup
at `daemon/daemon.go:519`. Boxes are ticked at `verify-spec`, not here.

**The pin.** `TestCheckInboxes_GraphReviewStillFiresVerifySpec` (`daemon/graph_verify_test.go`): a
review reply answering a request stamped with `GraphRun` and `GraphNode` reaches
`notifyPlanOnReview`, and plan gets one `verify-spec` — today's behaviour, inverted in Phase 2. Its
mirror in the same test, a hand-delegated review with no stamp, fires exactly one and stays green.

**Can the reply be traced to its request's run? Yes, when it carries `--reply-to`.**

| Link | Where |
|------|-------|
| The reply the daemon acts on | `bus.NewestMessageFrom(session, "edit", "review")`, in the review branch |
| From reply to request | The reply's `ReplyTo`, resolved through the bus log by `bus.FindRequestByID` |
| From request to run | The dispatch's `GraphRun` and `GraphNode`, stamped by the executor — the pin asserts both are there |
| A second source | The task record (`tasks/<id>.json`) carries `GraphRun` and `GraphNode` too |

**Not traceable: a review reply with no `--reply-to`.** Its `ReplyTo` is empty and nothing links it
to a request. Phase 2 must treat that as not graph-owned and keep firing — the safe direction, since
a missed suppression costs a duplicate turn and a wrong one would drop the only verification.

Also run by the worker: `scripts/test-bus-dir-leak.sh` 22 passed, 0 failed (run task
`1794945886-spawn-26088880-62593ef5`), full suite green.

Verified and ticked 2026-11-17 once all three had run after the only code edit (15:04:15):
`./build.sh` exit 0 at 15:08:50, `./test.sh` exit 0 at 15:10:33, review at 15:11:04 with 0 must-fix,
0 should-fix, 0 nits. This `verify-spec` came from the graph alone, with no daemon request beside
it — a graph-only arrival, as six of today's were.

### Phase 2: Suppress for graph-owned reviews

- [x] A graph-owned review reply sends plan nothing from the daemon, writes the suppression row naming run and node, and leaves the movement marker unwritten
- [x] A review outside a run fires exactly as before, including with a run parked on a gate
- [x] The reviewed marker and transition are unchanged for both
- [x] `CLAUDE.md` and `docs/architecture.md` spec-verification passages updated

### Results for Phase 2

Recorded 2026-11-17 from the worker's request; plan read the diff of `daemon/daemon.go` and found
the tests in the tree. Boxes are ticked at `verify-spec`, not here.

**The change** is one branch. Where the review branch called `notifyPlanOnReview` for every review
reply, it now first asks `graphOwnedReview(session, reply)` (`daemon/daemon.go`):

| The review reply | What the daemon does |
|------------------|----------------------|
| Its `ReplyTo` resolves — through the bus log (`FindRequestByID`), else the request's task record — to a request stamped `GraphRun` | Sends plan nothing, **whatever the verdict**; writes one `plan-verify-graph-owned` row: `review <id> answers graph run <run> node <node> — the run verifies it; no daemon verify-spec`. `notifyPlanOnReview` is not entered, so the movement marker stays unwritten |
| Resolves to an unstamped request — a review delegated by hand, including while a run waits on a gate | `notifyPlanOnReview` as before |
| Has no `ReplyTo` | `notifyPlanOnReview` as before — it names no request |

Only the call to `notifyPlanOnReview` moved inside the branch; the reviewed marker and the `reviewed`
transition around it are untouched in the diff.

**The event is its own name**, `plan-verify-graph-owned`, rather than a new detail on
`plan-verify-suppressed`, so it cannot be mistaken for "nothing moved since last verify-spec".

**Tests** (`daemon/graph_verify_test.go`): the Phase 1 pin inverted,
`TestCheckInboxes_GraphOwnedReviewSendsNoVerifySpec`, for both the `update-spec` and the `fix`
routes; `TestCheckInboxes_GraphOwnedByTaskRecord` — the stamp read from the task record when the bus
log has no line; `TestCheckInboxes_HandDelegatedReviewStillFires` — the controls, a hand review
after a graph-owned one among them.

**Plan's note.** The verdict is no longer read at all for a graph-owned review, so no
`plan-verify-withheld` row is written for one either: a graph-owned review with no sentinel, or
`EXIT=0` beside a must-fix, leaves only the `plan-verify-graph-owned` row. That is the run's to
judge now, through its own review node's outcome.

Docs: `docs/architecture.md` — a paragraph and table in the spec-verification passage (plan);
`docs/agent-bus.md` — `plan-verify-graph-owned` in the lifecycle event catalog (plan); `CLAUDE.md` —
the Spec verification entry (edit).

Also run by the worker: `scripts/test-bus-dir-leak.sh` 22 passed, 0 failed (run task
`1794946412-spawn-26088880-732ab28c`), full suite green.

Verified and ticked 2026-11-17 once all three had run after the last edit (15:12:37): `./build.sh`
exit 0 at 15:17:25, `./test.sh` exit 0 at 15:19:20, review at 15:19:56 with 0 must-fix, 0 should-fix,
0 nits. The steps are ticked on the unit tests and that review. No criterion is ticked yet.

**What the live session showed at this verification — and why it proves nothing either way.** This
session's daemon restarted onto the new build at 15:17:24. The Phase 2 review reply
(`1794946796-review-32d64188`, 15:19:56) answers a request stamped with this run and its `review`
node — plan traced it in `log.jsonl`. No daemon `verify-spec` arrived. But **no
`plan-verify-graph-owned` row was written either**, and nor was any other `plan-verify` row since
14:33.

The reason is upstream of this fix, and plan read it in the code:

| Fact | Where |
|------|-------|
| The whole review branch — reviewed marker, `reviewed` transition, and now the ownership check — runs only on a poll where the daemon sees **edit's inbox grow** | `checkInboxes`, `daemon/daemon.go` |
| It then looks for the newest review message by **peeking edit's inbox** | `NewestMessageFrom`, `bus/workflow.go:363` |
| Edit's own listener consumed this reply one second after it was sent (`acked` at 15:19:57, live parent); the daemon polls every 2 s | `delivery/…status` |

So when edit reads a review reply before the daemon's next poll, the branch never runs: no marker,
no transition, no fire, no row. That is what the six unpaired requests recorded at activation were
— the cause left unconfirmed there — and it happened again here. The duplicate this spec removes
only ever occurred on the polls that won that race.

**What follows for the criteria**, to be weighed at Phase 3:

- The second criterion (a row naming run and node) and the sixth (the reviewed marker and transition
  still happen for a graph-owned review) hold **when the branch runs**. When edit consumes first,
  neither happens — before this fix and after it. The fix does not change that, and does not claim
  to.
- Phase 3's script must keep the review reply in edit's inbox until the daemon has polled, or its
  assertions will pass or fail on timing.
- The race itself — the `reviewed` transition silently skipped whenever edit is quick — is outside
  this spec and has no spec of its own.

### Phase 3: Integration test

- [x] Create `scripts/test-graph-verify-once.sh` — hermetic: scratch daemon, a graph with `review` → `update-spec`, stub review and plan agents
- [x] Test: plan receives exactly one `verify-spec` for the run's review, and the suppression row names the run and node
- [x] Test: a graph-owned review routed to `fix` sends plan none
- [x] **Negative control:** a hand-delegated review with no graph fires exactly one `verify-spec`; so does one requested while a run waits on a gate
- [x] Coverage floor; run the script and record the counts here

### Results for Phase 3

Recorded 2026-11-17 from the worker's request; plan read the script's header and the run's log.
Boxes are ticked at `verify-spec`, not here.

`scripts/test-graph-verify-once.sh` — muxcode built from the tree; scratch `HOME`, `XDG_STATE_HOME`,
lifecycle log, config, memory and a committed repo; one session and daemon per section; a private
tmux server. The script plays review and plan itself. About a minute.

**Run: 21 passed, 0 failed, floor 21, exit 0** (run task `1794946998-spawn-26088880-e95c1a4d`,
requested 15:23:18 after the script's last edit at 15:23:16; the run agent's history row confirms
it, and plan read the log — 21 `PASS`, no `FAIL`).

| Section | Shows |
|---------|-------|
| A | A real run, `review` → `update-spec`, answered clean. Plan receives **exactly one** `verify-spec` — the run's, none from the daemon. One `plan-verify-graph-owned` row names the run and the node; no `plan-verify` row; the reviewed marker and transition still happen; the movement marker stays unwritten |
| B | The same run answered with a should-fix: it routes to `fix`, the daemon logs `plan-verify-graph-owned`, and plan receives **no** `verify-spec` at all |
| C | Negative control: a review edit requests by hand, answered with `--reply-to`, fires exactly one daemon `verify-spec` and no graph-owned row |
| D | Negative control: the same while a run waits on a `wait_human` gate — exactly one; a parked run does not own a review it did not dispatch |
| E | Control: section A on a build with the ownership check bypassed — plan receives two `verify-spec`s for the one review, the duplicate |

**The race recorded under Phase 2 is absent here by construction.** The script runs no edit agent,
so nothing consumes the review reply from edit's inbox and the daemon's poll always finds it. That
is the right fixture for what this spec changes — every section's row and marker assertions are
about the branch once it runs — and it means the script says nothing about a session where edit's
listener reads the reply first. There the branch is skipped whole, as it was before this spec.

**What it does not drive.** A review reply with no `--reply-to` (the unit test's untraceable case
only), and the stamp read from the task record when the bus log has no line
(`TestCheckInboxes_GraphOwnedByTaskRecord` only).

**Verified and ticked 2026-11-17**, every piece of evidence after the last edit (the script at
15:23:16, `CLAUDE.md` at 15:23:59):

| Evidence | When | Result |
|----------|------|--------|
| `bash scripts/test-graph-verify-once.sh` | requested 15:23:18 | 21 passed, 0 failed, floor 21, exit 0 |
| `./build.sh` | 15:24:58 | exit 0 |
| `./test.sh` (hook row) | 15:25:12 | exit 0, `modules=2 ran=2 passed=2 failed=0 never-executed=0` |
| Review | 15:26:41 | 0 must-fix, 0 should-fix, 0 nits |

**And once, live, in this session.** The review of this very phase
(`1794947201-review-cff235f7`, 15:26:41) was dispatched by run
`1794626267-55-defect-backlog-loop-15547e2e`. Edit read the reply two seconds after it was sent, so
the daemon's poll caught it, and at 15:26:43 the daemon wrote
`plan-verify-graph-owned … answers graph run 1794626267-55-defect-backlog-loop-15547e2e node review`.
Plan received one `verify-spec`, the run's. One occurrence, on the fixed build — where the Phase 2
review had been consumed before the poll and showed nothing.

What each criterion rests on:

| # | Criterion | Evidence |
|---|-----------|----------|
| 1 | No daemon `verify-spec` for a graph-dispatched review | Section A; the live occurrence above |
| 2 | One row naming run and node, distinct from `plan-verify-suppressed` | Section A — its own event, `plan-verify-graph-owned`; the live row |
| 3 | Movement marker unwritten, so a later review outside the run still fires | Section A (marker unwritten); `TestCheckInboxes_HandDelegatedReviewStillFires` (a hand review after a graph-owned one fires) |
| 4 | A graph-owned review routed to `fix` sends plan nothing | Section B |
| 5 | A review outside any run fires exactly once, also while a run is parked on a gate | Sections C and D |
| 6 | Reviewed marker and transition still happen for a graph-owned review | Section A. **As far as this spec changes anything:** they happen whenever the branch runs, exactly as before. When edit consumes the reply before the daemon's poll they do not happen, before and after — the race recorded under Phase 2, which this spec leaves alone |
| 7 | The script passes | The run above |

## Out of scope

- What the daemon's request names as changed files — [MUX-150](../backlog/MUX-150-verify-spec-names-last-routed-batch.md).
- Whether plan should refuse a second verification of the same review on its own.

### Findings at activation (2026-11-17)

Checked against the tree at `ba749b0`. The defect stands; the daemon's line numbers have drifted.

| Spec says | Tree today |
|-----------|------------|
| The review-reply branch calls `notifyPlanOnReview` near `daemon/daemon.go:477` | Now `:528` |
| `notifyPlanOnReview` at `:546-619`, with no check of whether a graph run dispatched the review | Now at `:597`; still none |
| `plan-verify-suppressed` at `:578` ("nothing moved since last verify-spec") | Now `:629` |
| `chain-suppressed` at `cmd/hook.go:180` | Unchanged |
| `Message.GraphRun` / `GraphNode` | `bus/message.go:43–44` |
| `scripts/test-graph-verify-once.sh` | Does not exist; `scripts/test-plan-verify-verdict.sh` does |

**Seen again all day on 2026-11-17, same run** (`1794626267-55-defect-backlog-loop-15547e2e`). Plan
was the recipient and answered each:

| Shape | Occurrences plan answered today |
|-------|---------------------------------|
| A pair in one inbox read — the daemon's request and the graph's `update-spec`, one second apart at most | Eight: MUX-220 Phases 1 and 3; MUX-223 Phases 1 and 2; MUX-225 Phases 1 and 3; MUX-232 Phases 1 and 2 |
| Daemon request only, for a graph-owned review that returned `EXIT=0` with a should-fix while the run's `fix` worker was already editing | Two: MUX-225 Phase 1 at 13:13 and MUX-232 Phase 3 at 14:33. Plan ticked nothing on either — the tree was moving under the evidence — which is the 09:31:40 row above, and the fourth criterion |
| Graph request only — no daemon request beside it | Six: MUX-196 Phase 3; MUX-220 Phase 2; MUX-223 Phase 3; MUX-225 Phase 2; MUX-232 Phases 3 and 4. Plan did not look up why the daemon's fire was absent each time, and guessed at the "nothing moved" gate. **That guess was wrong** — see [Results for Phase 2](#results-for-phase-2): the daemon's review branch never ran, because edit had consumed the reply before the daemon's poll |

So the duplicate is not every phase: of sixteen graph verifications plan answered today, eight came
paired. In each pair plan verified once and answered the second as a duplicate. Nothing but plan's own care
kept the second from re-editing the same checkboxes.

Nothing in this spec waits on a user decision.

## Status

**Complete** — activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). Uncommitted at close-out; the run's ship step commits it. 18/18 — Phases 1–3 complete and all seven criteria met.

A review a graph run dispatched is no longer verified twice. The daemon's review branch resolves
the reply's `--reply-to` to its request; if that request carries a graph run's stamp the daemon
sends plan nothing, whatever the verdict, and writes `plan-verify-graph-owned` naming the review,
run and node. The run verifies its own review, through `update-spec` or `fix`. A review delegated by
hand still fires exactly once, also while a run waits on a gate. Seen live once on the fixed build,
on this spec's own Phase 3 review.

**Found on the way, not fixed, and without a spec.** The daemon's review branch runs only on a poll
that finds the review reply still in edit's inbox. When edit's listener reads the reply first — it
often does, inside the daemon's two-second poll — the branch is skipped whole: no reviewed marker,
no `reviewed` transition, no verification fire, no row. That is why the duplicate hit only about
half of the graph verifications before this fix, and it is unchanged by it. Criterion 6 is met in
the sense this spec can meet it — nothing about the marker and transition moved.

Filed 2026-11-14 on the user's instruction relayed by edit. Observed on run `1794626267`; mechanism
read from `daemon/daemon.go`.
