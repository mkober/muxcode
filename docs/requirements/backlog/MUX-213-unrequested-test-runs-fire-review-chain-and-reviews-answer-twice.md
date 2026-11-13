# MUX-213: Test Runs Nobody Requested Fire the Review Chain, and Every Review Is Answered Twice

**Tracking:** [mkober/muxcode#187](https://github.com/mkober/muxcode/issues/187)

The test agent runs `./test.sh` more often than anything asks it to, and every one of those runs
that passes fires the test→review chain. Review then reviews a tree that has not changed, or one a
worker is halfway through editing, and its reply goes out twice — once to the test agent that asked,
once to edit. On 2026-11-13 the daemon's own loop detector flagged the pattern twice. The cost is
wasted turns, findings and FAILED notices about a half-edited tree that read as authoritative, and
extra `verify-spec` passes for plan set off by a chain no graph node started.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's request relayed by edit, from edit's evidence note
(`/tmp/defect-unrequested-test-review-cycles.md`). Plan checked the note against the session's
lifecycle log and the bus directory of session `muxcode`; the counts below cover 15:06 (the last
session re-init, which truncated the stores) to about 18:00. Rows marked *edit's account* were not
re-verified by plan. Code was **not** read for this spec: the root cause is undetermined and
establishing it is Phase 1.

### Observed — verified by plan

| Fact | Where |
|------|-------|
| 57 `./test.sh` rows in the test agent's history in about three hours | `test-history.jsonl` |
| 30 requests were addressed to the test agent in the same window: 8 from edit, 10 from build (the build→test chain), 12 from `daemon` (graph nodes and daemon sends) | `log.jsonl`, `"to":"test"` |
| So roughly 27 history rows have no request behind them. Some are seconds apart — `1794610411`, `1794610423`, `1794610442`, `1794610447` are four rows inside 36 s, though one suite run takes about a minute — so a row is not always a whole run. What each row is has not been established | `test-history.jsonl` |
| 24 `test → review` requests were sent in the window | `log.jsonl` |
| The daemon flagged it twice: `loop-detected test type=command` at 16:33:08, and `loop-detected test type=message` at 17:54:42 | `muxcode lifecycle show --event loop-detected` |
| **Each "duplicate" review reply is two messages to two recipients.** For request `1794610622-test-df8c7470` (`test → review`), review sent `review-complete` to `test` at `…645` and to `edit` at `…646`, both carrying that `reply_to`. The same pair exists for `1794607113`, `1794607188`, `1794610293`, `1794610337`, `1794610411` and `1794610442` | `log.jsonl` |
| A `verify-spec` reached plan at 17:24 while the graph's review node stood `failed` on a must-fix, and several more arrived naming only plan's own doc edits | plan's inbox; [MUX-172](../completed/MUX-172-plan-verify-gate-hardcodes-success.md) |

### Observed — edit's account, not re-verified

- The test agent starts a run with no request at all: after daemon timeout notices for synthetic
  stuck-agent tasks, after build chains while files were mid-edit, and spontaneously.
- Runs made mid-edit produce false FAILED notices — 16:21 (`writeCancelRequest` undefined) and 17:57
  (during the MUX-172 worker's edits) — and stale review findings that edit must triage.
- The detector's detail for the two rows: "`./test.sh` failed 3x in 1m44s" and
  "test <-> review action:review repeated 4x in 4m3s".
- Edit's memory note of 10:57 the same day: "review replies twice per request; unrequested test
  cycles observed".

### Cause — not determined

Candidates, none verified:

- **The reply to the test agent is itself the trigger.** Review answers the test agent's request, so
  a `review-complete` response lands in the test agent's inbox. If the test agent treats that as
  work — re-running the suite to "confirm" — the run passes, the chain asks review again, and the
  loop is test ↔ review, which is what the 17:54 row names. Plan's own hypothesis, from the reply
  pairs above.
- **The second reply is auto-CC.** Messages from review to a non-edit agent are copied to edit by
  design; the pair may be one send and its copy rather than two sends. The two ids differ and are a
  second apart, which does not settle it.
- **The test agent's definition** tells it to run the suite on something other than a request.
- **A daemon re-drive** (idle wake, stall watchdog, synthetic task timeout) re-delivers an already
  answered test request.
- **Two history rows per run**, from a precheck or a second hook event, inflating the count without
  an extra run.

### Why it matters

- The test→review chain is deterministic and trusted: a passing run **is** the review request. An
  unrequested run therefore launders itself into a review nobody asked for, and a review into a
  `verify-spec`.
- A FAILED notice or a must-fix finding about a tree mid-edit is indistinguishable, to edit, from one
  about finished work.
- Each cycle occupies the test and review agents, so a graph node's real dispatch to either waits or
  is suppressed as a duplicate ([MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md),
  [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md)).

## Requirements

### Acceptance criteria

- [ ] The cause of the unrequested runs is recorded here, with the evidence that distinguishes it
  from the other candidates — including what a `test-history.jsonl` row actually is
- [ ] The test agent starts a suite run only for a request — a graph node, the build chain, or edit.
  A response arriving in its inbox, a timeout notice or an idle wake starts none
- [ ] One review request produces one review, and edit is told about it once. Whether the requester
  also receives a reply is decided and recorded, and that reply cannot start a new test run
- [ ] An unrequested or re-driven run does not fire the test→review chain
- [ ] Negative control: a requested run still passes its result on — build→test→review fires exactly
  once per request, and a graph `test` node still completes on its reply
- [ ] `docs/hooks.md` and `docs/architecture.md` state what may start a test run and who receives a
  review reply

### Technical approach

Phase 1 is attribution, not a fix. For every `./test.sh` row in a bounded window, find the message
or event that preceded it: a request id, a response delivered to the test agent, a wake, or
nothing. That table decides between the candidates. The fix then goes where the cause is — the
agent definition, the chain condition, the reply's recipient list, or the re-drive — and nowhere
else.

### Key files

| File | Purpose |
|------|---------|
| `agents/test-runner.md`, `agents/code-reviewer.md` | What each is told to do on a response, and whom to reply to |
| `tools/muxcode/bus/hook.go`, `bus/profile.go` | The test→review chain and its conditions |
| `tools/muxcode/bus/inbox.go` | Auto-CC of review's replies to edit |
| `tools/muxcode/daemon/daemon.go` | Idle wake, stall and timeout re-drives |
| `tools/muxcode/bus/guard.go` | `DetectMessageLoop` — alert-only today |

## Implementation

### Phase 1: Attribute every run

- [ ] For one window of at least an hour, list each `./test.sh` history row beside what preceded it
  in `log.jsonl` and the lifecycle log; record the table here
- [ ] Establish what one history row is: a whole run, a precheck, or a second hook event
- [ ] Establish whether the paired review replies are one send plus auto-CC, or two sends
- [ ] Reproduce on a scratch session: deliver a `review-complete` response to a stub test agent and
  observe whether a run starts
- [ ] Record the cause and the candidates it rules out

### Phase 2: Fix at the cause

- [ ] Stop the unrequested run where Phase 1 placed it, with a test that fails without the change
- [ ] One review, one notice to edit; record the decision on the requester's reply
- [ ] The chain does not fire for a run with no request behind it
- [ ] Update `docs/hooks.md` and `docs/architecture.md`

### Phase 3: Integration test

- [ ] Create `scripts/test-unrequested-test-cycles.sh` — hermetic: scratch `BUS_SESSION`, scratch
  daemon, stub test and review agents
- [ ] A `review-complete` response delivered to the test agent starts no run and sends review nothing
- [ ] One `test` request → one run, one `review` request, one review, one notice to edit
- [ ] Negative control: with the fix reverted in the stub's configuration the loop reproduces, so
  the script proves it can see the defect
- [ ] Coverage floor; run the script through the run agent and record the counts here

## Related

- [MUX-127](./MUX-127-review-completion-routing.md) — review completion routes nowhere on failure
  and loops on success; the same junction
- [MUX-009](./MUX-009-response-echo-chain-retrigger.md) — a response echo re-triggers the chain;
  possibly the same mechanism from the other side
- [MUX-175](./MUX-175-response-ack-ping-pong-has-no-brake.md) — two agents acknowledging each other
  with no brake
- [MUX-172](../completed/MUX-172-plan-verify-gate-hardcodes-success.md) — why each of these chains also
  reaches plan
- [MUX-032](./MUX-032-loop-detector-granularity.md) — the detector that saw this and could not act

## Status

Backlog

Filed 2026-11-13 on the user's request relayed by edit. The counts and the paired replies are
verified by plan against the session's stores; the unrequested starts are edit's account; the cause
is **not determined**. MUX-127 and MUX-009 may turn out to be the same defect — Phase 1 should say.
Not started.

**Unparked 2026-11-13 18:15, on the user's decision relayed by edit.** Plan had filed it parked an
hour earlier under the user's same-day rule, because one design point is unrecorded: whether the
requester also receives the review reply. The user's decision: unpark it, rank it first among the
eligible defects (Defects rank 1, tier 0) so the running `55-defect-backlog-loop` takes it right
after MUX-172, and **leave that design point to the loop's worker**. The worker records its choice
and the reason in this spec; the third acceptance criterion is where it lands.
