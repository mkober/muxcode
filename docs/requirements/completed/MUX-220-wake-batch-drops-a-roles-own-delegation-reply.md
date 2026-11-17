# MUX-220: The Wake Batch Drops a Listenerless Role's Own Delegation Reply, and May Re-Inject an Answered Request

**Tracking:** [mkober/muxcode#203](https://github.com/mkober/muxcode/issues/203)

A follow-up to [MUX-009](../completed/MUX-009-response-echo-chain-retrigger.md) (PR #198), from two
Copilot review threads read after the merge. MUX-009 stopped responses being typed into a
listenerless agent's pane by consuming them. It consumes **every** response a non-orchestrator role
holds — including the correlated reply to a request that role itself sent with `--wait` or
`--track`, which is deleted before the sender's poll can find it. Separately, the batch split never
asks whether a queued request has already been answered.

## Context

### Source and standard of evidence

| Thread | Location | Claim | Verified |
|--------|----------|-------|----------|
| 4573825335 | `bus/provider.go:340` | `settleWakeResponses` consumes every withheld response for a role that is not edit or auto; a reply correlated to that role's own delegation is dropped | **Yes**, by edit against main `82635f8` and by plan in the tree: the `receiveMatching` call matches on message id alone, with no reply-to or task check |
| 4573825311 | `bus/provider.go:310` | A request already answered (`responded`) but still queued can be re-injected into a listenerless pane | **Not confirmed.** `splitWakeBatch` keeps every non-response message and reads no status — that much is in the tree — but whether an answered request can still be queued when a wake is built has not been shown. `MarkResponded` drains the answered row via `ConsumeByID`, which may already close this. **Since 2026-11-17: yes, it can** — see [Results for Phase 2](#results-for-phase-2) |

### Mechanism — verified in code

- `bus/provider.go:305–314` `splitWakeBatch` — splits on `m.Type == "response"` only.
- `bus/provider.go:327–344` `settleWakeResponses` — for any base role other than `edit`/`auto`, builds an id set from the withheld responses and consumes them with `receiveMatching(session, role, "", …)` (no receipt), logging one `wake-response-dropped` row.
- Callers: `bus/provider_opencode.go:153` and `bus/provider_codex.go:861` — the OpenCode road and the scrape-road Codex road. Claude and hook-road Codex are not affected.
- A role's own `muxcode send --wait`/`--track` finds its answer by polling its inbox for the correlated reply; once the wake has consumed it, the poll degrades to a tracked task that never completes on that reply.

### Scope boundary

MUX-009's rule stands: an uncorrelated response is never typed into a pane and never re-fires a
chain. This spec narrows what is **consumed**, not what is typed.

## Requirements

### Acceptance criteria

- [x] A response that answers a request the receiving role itself issued (its `ReplyTo` names a live task or request that role sent) is not consumed by `settleWakeResponses`; the role's `--wait` or `--track` receives it
- [x] Such a response is still not typed into the pane as an instruction — MUX-009's `scripts/test-response-echo.sh` passes, its MUX-009 assertions unchanged (amended 2026-11-17 by plan from "passes unchanged"; see [Results for Phase 3](#results-for-phase-3))
- [x] Uncorrelated responses are still consumed and logged `wake-response-dropped` (negative control)
- [x] The answered-request claim is settled with evidence: either a reproduction of an answered request being re-injected, followed by a responded check on both provider roads and the daemon wake, or a recorded finding that the row cannot be queued once answered
- [x] A mixed batch — one open request, one correlated reply, one uncorrelated response — is covered by a regression test

### Technical approach

In `settleWakeResponses`, partition the withheld responses before consuming: a response whose
`ReplyTo` matches a task the role holds as sender (`tasks/`) stays queued for the role's own poll;
the rest are consumed as today. The lifecycle row names both counts. For the answered-request
claim, read the consume and `MarkResponded` paths first and write down what they guarantee.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider.go` | `splitWakeBatch`, `settleWakeResponses` |
| `tools/muxcode/bus/provider_opencode.go`, `provider_codex.go` | The two listenerless wake roads |
| `tools/muxcode/bus/task.go`, `bus/delivery.go` | Sender-side task lookup; `MarkResponded` and `ConsumeByID` |
| `scripts/test-response-echo.sh` | MUX-009's script, to extend |

## Implementation

### Phase 1: A role's own reply survives the wake

- [x] `settleWakeResponses` keeps a response correlated to a task the role sent; consumes the rest
- [x] Unit test: a correlated reply stays queued and is found by the sender's poll; an uncorrelated one is consumed (negative control)
- [x] `wake-response-dropped` names how many were dropped and how many kept

### Results for Phase 1

Recorded 2026-11-17 from the worker's handoff (`/tmp/mux220-phase1-reply.md`); plan read the diff of
`bus/provider.go` and both tests.

| What | Where |
|------|-------|
| A withheld response whose `ReplyTo` names a task sent from the role's own inbox stays queued and untyped; the rest are consumed as before | `settleWakeResponses`, new `answersOwnDelegation` (`bus/provider.go`) |
| Ownership is by **inbox**, not role name (`inboxOwner`: `WindowForRole(NormalizeBusRole(…))`), so a reply to `docs` is kept on plan's wake and one to `pr-read` on commit's; spawn identities stay distinct | The review's one must-fix (`/tmp/muxcode-review-1794932608.txt`): the first cut compared role names and still dropped a hosted role's reply |
| Any task status counts — a `--track` result is read after the daemon completes its task; a task removed by GC no longer protects its reply | `answersOwnDelegation` |
| The row reads `<role>: <n> response(s) not typed into the pane, <k> kept as replies to its own requests: …`; no row when nothing is dropped | `settleWakeResponses` |

**Tests** (`bus/wake_policy_test.go`, each on the OpenCode and scrape-road Codex wakes):

- `TestSendWakeUp_KeepsReplyToOwnDelegation` — the mixed batch: the open request is typed, the
  correlated reply stays queued with its `ReplyTo` and is not typed, the uncorrelated response is
  consumed with one row naming it and `1 kept`. A second wake neither types the kept reply nor writes
  another row.
- `TestSendWakeUp_KeepsHostedRoleDelegationReply` — docs's reply is kept on plan's wake; a reply
  correlated to a task **build** sent, and an uncorrelated one, are dropped (`2 response(s)`,
  `1 kept`).

**Step 2's "found by the sender's poll" is shown by its precondition, not by running the poll.** The
tests leave the reply queued, from the request's target, with `ReplyTo` equal to the request id —
which is what `--wait` matches (`responseAnswers`, `cmd/send.go:318`; plan read it). The poll itself
is driven in Phase 3.

**Left open by the worker, and it bears on the first criterion.** A kept reply is not typed and no
notice is given, so a listenerless role that sent with `--track` is not told its result is waiting:
the reply sits in its inbox and the daemon's wake for it types nothing. `--wait` is not affected — it
polls. The first criterion says the role's `--wait` **or `--track`** receives the reply; whether "is
in its inbox" is enough for `--track`, or the role must be told as edit and auto are, is not decided
here. It stays open with the criterion.

Settled at Phase 3's review, as a must-fix: the role is told, with a count-only notice — see
[Results for Phase 3](#results-for-phase-3).

Verified and ticked 2026-11-17 once all three had run after the last edit (11:24:53): `./build.sh`
exit 0 at 11:25:26, `./test.sh` exit 0 at 11:27:08, review at 11:27:37 with 0 must-fix, 0 should-fix,
0 nits. Criteria 3 and 5 are ticked on the two tests above; 1, 2 and 4 wait on Phases 2–3.

Docs (plan): `docs/agent-bus.md` — a `wake-response-dropped` row in the lifecycle event catalog.

### Phase 2: The answered-request claim

- [x] Read the consume and `MarkResponded` paths and record in this spec whether an answered request can remain queued at wake time
- [x] If it can: add the responded check to both provider roads and the daemon wake, with a test. If it cannot: record the finding and the code that guarantees it

### Results for Phase 2

Recorded 2026-11-17 from the worker's finding (`/tmp/mux220-phase2-finding.md`); plan read
`MarkResponded`, `HasActionableMessages`, `settleAnsweredRequests` and the test. Boxes are ticked at
`verify-spec`, not here.

**Finding: an answered request can remain queued when a wake is built.**

| Fact | Where |
|------|-------|
| `MarkResponded` sets the status to responded, then drains the row only if `FindMessageByID` finds the request in `log.jsonl`, and ignores the result of the `ConsumeByID` that follows (`_, _ =`) | `bus/delivery.go:101–115` |
| So the answered row stays queued when the request has no bus-log line (`AppendToInbox` writes none; a rotated or unreadable log), or when `ConsumeByID` loses the inbox lock, which fails closed | The same |
| The daemon wake already skipped such a row: `HasActionableMessages` reads `IsResponded` (MUX-009 Phase 2) | `bus/inbox.go:794` |
| The two provider wake roads did not: `splitWakeBatch` kept, and so typed, every queued request | `bus/provider.go` |

**Fix.** `settleAnsweredRequests` (`bus/provider.go`) runs ahead of `splitWakeBatch` on both roads
(`provider_opencode.go:153`, `provider_codex.go:861`): it takes responded requests out of the batch,
consumes their rows with no receipt and writes one `wake-answered-dropped` row.

**Test.** `TestSendWakeUp_AnsweredRequestNotReinjected` (`bus/wake_policy_test.go`), on the OpenCode
and scrape-road Codex wakes: after the real `MarkResponded` the row is still queued and responded;
the wake does not type it, consumes it with no receipt and writes one row, and an open request
beside it is still typed (negative control).

**What the reproduction is.** The test builds the state with `AppendToInbox`, a row with no bus-log
line. That shows the state is reachable through the real `MarkResponded`; it is not an observation
of an answered request being re-injected in a live session, and none is on record.

The worker reads this as satisfying both Phase 2 steps and the fourth criterion — the daemon wake
had the check already, the provider roads have it now.

Verified and ticked 2026-11-17 once all three had run after the last edit (11:30:04): `./build.sh`
exit 0 at 11:31:17, `./test.sh` exit 0 at 11:33:08, review at 11:33:53 with 0 must-fix, 0 should-fix,
0 nits. The fourth criterion is ticked on the first of its two readings, with the reproduction being
the constructed one described above.

### Phase 3: Integration test

- [x] Extend `scripts/test-response-echo.sh` (or add `scripts/test-wake-own-reply.sh`): an OpenCode-role fake agent sends a tracked request, the reply arrives while it is listenerless, a wake is built → the reply is still in its inbox and its task completes
- [x] Control: an uncorrelated response in the same batch is dropped with a `wake-response-dropped` row and is never typed
- [x] Run the script and record pass/fail counts in this spec

### Results for Phase 3

Recorded 2026-11-17 from the worker's results (`/tmp/mux220-phase3-results.md`); plan read the
script's diff, the run agent's history row and the changed `answersOwnDelegation`. Boxes are ticked
at `verify-spec`, not here.

`scripts/test-response-echo.sh` was extended; no `scripts/test-wake-own-reply.sh` was added. Floor
17 → 29.

**Run: 29 passed, 0 failed, floor 29, exit 0** (run task `1794933574-spawn-26088880-89d3618f`, ended
11:40:13; the run agent's history row confirms it, and watch read the log clean). One earlier run
failed on the script, not the fix: section F's control patch did not apply.

| Section | Shows |
|---------|-------|
| E | Build sends test a `--track` request; test answers with `--reply-to` while build is listenerless, beside review's uncorrelated response; `muxcode deliver build --force` builds the wake. Nothing is typed, the reply is still queued, the stray is dropped, and one `wake-response-dropped` row reads `build: 1 response(s) … 1 kept … review>review`. A scratch daemon then completes the task, its own wake of build keeps the reply (no new row, nothing typed), and build's `muxcode inbox` returns it |
| F | Control: a build with `answersOwnDelegation` patched to return false consumes build's own reply; its row counts 2 responses |

**Criterion 2 cannot hold as written.** It says MUX-009's script "passes unchanged". Section A's test
answer is a reply to build's own `request:test`, which is tracked, so since this fix it is kept
queued rather than dropped. A's two consume assertions were rewritten: no `wake-response-dropped`
row, and the one answer queued for build. A's MUX-009 assertions — one `request:test`, the answer
never typed, the chain not re-fired — are unchanged and pass. What the criterion protects, that a
response is still never typed into the pane, holds; the word "unchanged" does not.

**A production change found by running it.** `cmd/send.go` notifies the target (`:271`) before it
writes the request's task (`:289`, `:303`), so a fast reply could reach a wake before its task file
existed and be dropped — section A flipped between runs. `answersOwnDelegation` now falls back, when
no task file exists, to the sender embedded in the request id (`MessageIDSender`), for as long as the
request is younger than the default 600 s task allowance. `TestAnswersOwnDelegation_BeforeTaskWritten`:
a fresh id with no task is kept; an id 700 s old and another role's request are not (negative
controls). The send order itself is unchanged.

That fallback did not survive the review; see the second fix below.

**Two review fixes after that run** (review of 11:44, 1 must-fix, 1 should-fix,
`/tmp/muxcode-review-1794933794.txt`; recorded from the worker's `/tmp/mux220-review-fix.md`, and
plan read `settleWakeResponses` and `answersOwnDelegation` as they now stand).

| Finding | Fix |
|---------|-----|
| **Must-fix: a tracked sender was never woken.** A kept reply returned no notice, so a listenerless role that sent with `--track` never learned its result had arrived — the question left open under [Results for Phase 1](#results-for-phase-1) | `settleWakeResponses` returns a count-only notice for kept replies: "N result(s) of your own requests are waiting in your inbox as data, not instructions: read them with `muxcode inbox`". Never the response's text; uncorrelated responses stay silent. A wake holding only an own result types that notice alone |
| **Should-fix: the fallback judged ownership by the id's spelling and age.** A minted, never-sent id was accepted, as was a future-dated one | With no task file, `answersOwnDelegation` resolves the originating request from the bus log (`FindRequestByID`): it must be a logged **request** whose sender reads the waking inbox. `Send` logs before `Notify`, so a fast reply still resolves. The 600 s id-timestamp rule is gone |

Tests: `TestSendWakeUp_OwnResultOnlyTypesNotice` (an idle role holding only its own result: the
notice is typed, no response text, no reply instruction, the result still queued; both roads).
`TestAnswersOwnDelegation_BeforeTaskWritten` now uses sent requests — a sent request with no task is
kept; a minted never-sent id, a response's id and another role's sent request are not.
`TestSendWakeUp_KeepsReplyToOwnDelegation` also asserts the notice.

The script changed with it, floor still 29: the fake build agent reads its inbox on the notice and
starts no work. Section A — the answer's text is never typed; the pane gets edit's request and the
notice; build reads the answer. Section E — only the notice is typed, the stub reads its own reply,
the daemon's completing wake types no repeat notice, and build sends no further `request:test`.

**Re-run after the fixes: 29 passed, 0 failed, floor 29, exit 0** (run task
`1794933962-spawn-26088880-20ecd7cb`, ended 11:46:41, after the last edits — `provider.go` 11:44:59,
the test file 11:45:24, the script 11:46:00; the run agent's history row confirms it).

**Not fixed, from the worker.** A kept own result that the agent never reads stays queued — and is
noticed again on each wake — until its task is collected, and counts toward the 50-message inbox
cap.

**Verified and ticked 2026-11-17**, every piece of evidence after the last code and script edit
(11:46:00):

| Evidence | When | Result |
|----------|------|--------|
| `bash scripts/test-response-echo.sh` | requested 11:46:02, ended 11:46:41 | 29 passed, 0 failed, floor 29, exit 0 — plan read the log: 29 `ok`, no `FAIL` |
| `./build.sh` | 11:47:59 | exit 0 |
| `./test.sh` (hook row) | 11:49:34 | exit 0, `modules=2 ran=2 passed=2 failed=0 never-executed=0` |
| Review | 11:50:09 | 0 must-fix, 0 should-fix, 0 nits; both findings resolved |

The `verify-spec` dispatch's worker report predates the review fixes and still describes the 600 s
id fallback; the table above is of the tree as it stands.

What the last two criteria rest on:

| # | Criterion | Evidence |
|---|-----------|----------|
| 1 | The role's own reply is not consumed; its `--wait` or `--track` receives it | `--track`: section E — the reply stays queued, the notice is typed, the stub agent reads its own reply and the task completes. **`--wait`: by its precondition only** — the reply is left queued with the fields `responseAnswers` matches (Results for Phase 1); no test runs a `--wait` poll against a wake |
| 2 | Not typed as an instruction; MUX-009's script passes | Sections A and E: the response's text is never typed, only the count-only notice. **Amended by plan** from "passes unchanged", which the fix makes impossible — section A's two consume assertions had to change, as recorded above. The amendment is plan's reading of what the criterion protects, not a user decision |

## Notes

- Filed 2026-11-14 by plan from edit's reading of PR #198's post-merge review threads, on the user's standing instruction to file defects found along the way.

### Findings at activation (2026-11-17)

Checked against the tree at `3838a24`. The defect stands.

| Spec says | Tree today |
|-----------|------------|
| `splitWakeBatch` at `bus/provider.go:305–314`, `settleWakeResponses` at `:327–344` | Both still there; the consume is `receiveMatching(session, role, "", …)` at `:340`, by message id alone |
| Callers at `provider_opencode.go:153` and `provider_codex.go:861` | One line lower: `:154` and `:862` |
| `scripts/test-response-echo.sh` to extend | Exists; `scripts/test-wake-own-reply.sh` does not |

Nothing in the spec waits on a user decision. Phase 2's either/or is decided by what the worker
finds in the consume and `MarkResponded` paths, and is to be recorded here either way.

## Status

**Complete** — activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). Uncommitted at close-out; the run's ship step commits it. 13/13 — Phases 1–3 complete and all five criteria met, criterion 2 as amended by plan. Filed 2026-11-14.

A wake for a listenerless role no longer consumes a reply to a request sent from that role's own
inbox: the reply stays queued, its text is never typed, and the wake types a count-only notice
telling the role to read its inbox. Ownership is by inbox, so a hosted role's reply is kept on its
host's wake, and with no task file yet the request is resolved from the bus log. Uncorrelated
responses are still consumed and logged `wake-response-dropped`. Separately, a request already
answered but still queued is dropped from the wake rather than typed again
(`wake-answered-dropped`).

Three things a reader should not take for more than they are, each tabled in the results above:
criterion 2 was reworded by plan, not by the user; criterion 1's `--wait` half rests on its
precondition, with only `--track` driven end to end; and criterion 4's reproduction is a constructed
state, not a live observation. One follow-up is unfixed and unfiled: an own result the agent never
reads is noticed again on each wake and counts toward the 50-message inbox cap.
