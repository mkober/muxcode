# Harness Reply Correlates to the Batch's Last Message, Not the Request

**Tracking:** [mkober/muxcode#45](https://github.com/mkober/muxcode/issues/45)

The local harness answers a whole inbox batch with a single reply addressed to — and correlated
with — `msgs[len(msgs)-1]`. When the last message in the batch is not the request being answered,
the request is never marked responded, never gains a receipt, and the delivery backstop re-drives it
indefinitely. Found during the [MUX-109](./MUX-109-prompt-mode-graph-control-pane.md) Phase 2
regression check.

## Context

### Root cause

`harness/loop.go` picks one message to represent the batch:

```go
lastMsg := msgs[len(msgs)-1]                                                          // :313
...
bus.Send(lastMsg.ReplyTarget(), lastMsg.Action, finalResponse, "response", lastMsg.ID)   // :568
```

(Line numbers and the `ReplyTarget()` call re-read 2026-11-24; the recipient now honours
`ReplyRole`, MUX-213, but is still taken from the last message.) Every field of the reply —
recipient, action, and correlation id — comes from whichever message happened to land **last**, with no check that it is a request, or even that it came from someone
else. Three consequences follow, and all three were observed:

| Consequence | Why it happens |
|-------------|----------------|
| The request is never receipted | `MarkResponded(session, originalID, …)` (`bus/delivery.go:94`) is keyed on the correlation id. Correlate to a *response's* id and the original request's status is never advanced |
| The backstop re-drives forever | `hasReceipt()` treats a request as received on `AckedAt > 0` **or** `Status == responded`. Neither ever becomes true, so the receipt gap never closes and delivery is re-driven on every poll |
| The agent replies to itself | If the last message is a self-addressed response sitting in the agent's own inbox, `lastMsg.From` is the agent itself. A self-addressed startup response echo was seen in the build agent's own inbox |

### Why a batch contains non-requests at all

`ConsumeInbox()` returns everything pending. A batch legitimately mixes a request with responses,
CC'd copies, and echoes. Representing that batch by its last element is arbitrary — ordering is an
artifact of arrival time, not of what the agent was asked to do.

### Interaction with MUX-110

This defect is what makes [MUX-110](./MUX-110-harness-startup-tool-loop-exhaustion.md) *repeat*: an
un-receipted startup request is re-driven every backstop cycle, and each redelivery burns a full
turn budget. The two are independent — this one would strand any un-receipted request even with a
perfectly-shaped prompt — but together they produce the observed five-minute loop.

### Scope note

This is a **harness-side** correlation bug. The bus-side receipt machinery behaves correctly given
the ids it is handed; it is being handed the wrong one. Do not "fix" this by loosening `hasReceipt()`
— that clause is load-bearing and was itself the fix for a 21-hour re-drive incident recorded in
`CLAUDE.md`.

## Decisions (user, 2026-11-24)

Relayed by edit; this spec was parked on Phase 4 and is unparked by them.

| Question | Decision |
|----------|----------|
| A batch holding several requests | **Answer each request with its own reply** — recipient, action and correlation id taken from that request, the precedent [MUX-189](./MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) set for typed batches. The alternative (answer the first, leave the rest actionable) is dropped |
| Self-addressed messages | **Filtered out** before the batch is formed |
| Integration script without Ollama | Skips its model-driven section with a reason (`SKIP`, exit 2 when nothing but the skip remains), and its hermetic section still carries a **coverage floor** |

How one model conversation yields one reply per request — one conversation per request, or one
conversation whose answer is sent to each — is an implementation choice for Phase 4, recorded there
when made; either satisfies the decision so long as every request is answered under its own id.

### Restated 2026-11-28 (edit's call)

Not a user decision: edit's call under the user's standing instruction for routine scope calls,
taking the conservative option, relayed to plan 2026-11-28.

| What | Was | Now | Why |
|------|-----|-----|-----|
| Criterion 9 and Phase 5 step 3 | The delivery backstop stops re-driving an answered request | The answered request reaches `responded` and its sender's tracked task completes | Phase 1 showed nothing re-drives today, so the old wording passed without the fix and proved nothing — see the table under [Findings for Phase 1](#findings-for-phase-1) |

| Criterion 4 | Self-addressed messages never cause the agent to reply to itself | The same, except the launch-time `request:startup` bootstrap | Edit's reading of the user's "self-addressed messages: filtered out" decision above, which states no exemption: the bootstrap is the bus's own exception (`isStartupBootstrap`), removing it would stop a harness agent receiving its context-restore startup, and the bus already correlates and drops the self-reply. **Flagged for the user to overrule** — see [Findings for Phase 3](#findings-for-phase-3) |

The other stale wording in the Phase 1 table (Context rows 1 and 2, the tail of criterion 3, the
MUX-110 paragraph) is left as written.

## Requirements

### Acceptance criteria

- [x] A reply correlates to the **request** it answers, never to a response or an unrelated message that happened to arrive last
- [x] A batch of `[request, response]` produces a reply whose `ReplyTo` is the **request's** id
- [x] The answered request reaches `Status == responded`, closing the receipt gap
- [x] Self-addressed messages never cause the agent to reply to itself — except the launch-time `request:startup` bootstrap, which is kept and whose self-reply the bus correlates and never delivers (exemption added 2026-11-28; see [Restated 2026-11-28](#restated-2026-11-28-edits-call))
- [x] A batch containing **no** request produces no spurious reply
- [x] A batch containing **multiple** requests answers **each** with its own reply, correlated to that request's id and sent to that request's reply target; every one reaches `Status == responded` and none is silently dropped
- [x] The reply's recipient and action come from the same message as the correlation id — they cannot disagree
- [x] **Negative control:** the single-request case (the common path) is unchanged, proving the fix is not "never correlate"
- [x] The answered request reaches `Status == responded` and its sender's tracked task completes — verified end to end, not only in unit tests (restated 2026-11-28 from "the delivery backstop stops re-driving a request the harness has answered"; see [Restated 2026-11-28](#restated-2026-11-28-edits-call))
- [x] `scripts/test-harness-reply-correlation.sh` passes

### Technical approach

- **Select the request, not the last message.** Choose the message the reply actually answers — the
  actionable request in the batch — and take recipient, action, and correlation id from that single
  message so the three cannot drift apart.
- **Filter self-addressed messages before the batch is formed.** The provider wake path already
  filters self-addressed messages to prevent echo loops (`SendWakeUp`); the harness consume path
  needs the same guard.
- **Reply per request** (decided 2026-11-24). Each request in the batch gets its own reply;
  leaving one silently unanswered is what creates orphaned requests the backstop chases.
- **Verify against the live path.** This class of defect is invisible to in-process tests — MUX-014
  shipped two live-path bugs every executor unit test passed over, including an unreplyable message
  whose reply target was wrong. Unit tests plus an integration run, not unit tests alone.

### Key files

| File | Change |
|------|--------|
| `harness/loop.go` | Request selection instead of `msgs[len(msgs)-1]` (`:313`, `:568`); one reply per request |
| `harness/bus.go` | `ConsumeInbox()` — self-addressed filtering |
| `harness/loop_test.go` | Batch-shape tests including the single-request negative control |
| `scripts/test-harness-reply-correlation.sh` | New — integration test |
| `docs/architecture.md` | Delivery-tracking section: note that correlation is the harness's responsibility |

## Implementation

### Phase 1: Reproduce and pin

- [x] Reproduce with a batch of `[request, response]` and confirm the reply carries the response's id
- [x] Confirm the request's delivery status never reaches `responded`, and the receipt gap stays open — checked 2026-11-28: the first half is confirmed, the second is **disproved** (the consume writes an ack receipt, so the gap closes); see [Findings for Phase 1](#findings-for-phase-1)
- [x] Unit tests capturing current behaviour before changing it

### Phase 2: Correlate to the request

- [x] Select the answered request; take recipient, action, and correlation id from that one message
- [x] Test: `[request, response]` → reply correlates to the request
- [x] Test: single request → unchanged (**negative control** against a "never correlate" regression)
- [x] Test: no request in batch → no reply sent

### Phase 3: Self-addressed filtering

- [x] Filter self-addressed messages out of the consumed batch
- [x] Test: a self-addressed response never produces a reply to self

### Phase 4: Multi-request behaviour

- [x] A batch with several requests sends one reply per request, each with that request's reply target, action and id; record here whether it runs one conversation per request or one for the batch — **one conversation for the batch**, its answer sent to each request; see [Findings for Phase 4](#findings-for-phase-4)
- [x] Test: `[request A, request B]` from two senders → two replies, each correlated to its own request, both `responded` — the two correlated replies are tested; "both `responded`" is **not yet observed** (the unit test's bus is a recording stub) and is carried by Phase 5's hermetic step and criterion 6, both open
- [x] Test: two requests from **one** sender are each answered once (MUX-222's case)
- [x] Document the behaviour in `docs/architecture.md`'s delivery-tracking section

### Phase 5: Integration test

- [x] Create `scripts/test-harness-reply-correlation.sh` — hermetic: scratch `BUS_SESSION`, scratch inbox
- [x] Hermetic section, no Ollama needed (a stub model endpoint through the harness's `*http.Client` seam, never a bound socket): a `[request, response]` batch → the request reaches `responded`; a two-request batch → two replies, each under its own id, and both requests `responded` (carried from Phase 4, 2026-11-28 — for two senders and for one sender twice, the second settling the response brake's exemption)
- [x] Assert the answered request reaches `responded` and its sender's tracked task completes (restated 2026-11-28 from "assert the delivery backstop stops re-driving an answered request")
- [x] Assert no self-addressed reply appears in the agent's own inbox — held 2026-11-28 while the assertion ran on a batch with no self-addressed message; closed by section F, which the harness's own filter does not decide; see [Findings for Phase 5](#findings-for-phase-5)
- [x] **Negative control:** a build from a copy of the tree with the request selection reverted to `msgs[len(msgs)-1]` correlates to the response and leaves the request un-receipted — as run, the control's request is left `acked`, not `responded`; "un-receipted" is the stale wording Phase 1 corrected
- [x] Model-driven section: the same batches through a real Ollama model; skipped with a reason when Ollama or the model is unavailable — the section exists and its skip was observed; **its model path has never run** (no Ollama endpoint on this machine); since the review fix of 00:47 it drives all three batch shapes
- [x] **Coverage floor** on the hermetic section, so a run that skipped the model cannot report green on nothing
- [x] Run the script and confirm all checks pass — 32 passed, 0 failed, floor 32, the model section skipped (2026-11-28 00:44)

## Findings for Phase 1

Recorded 2026-11-28 from the worker's findings and the pin run (run agent task
`1795843317-spawn-a5769f4f-594b96fb`, 00:22, **8 passed, 0 failed**); logs and the status record
read by plan.

### What is pinned

| Where | What it shows on today's tree |
|-------|-------------------------------|
| `harness/loop_test.go`, `TestProcessBatch_ReplyFollowsTheLastMessage_Characterization` | Three cases, all passing. `[request R1, response P1]` → `send build build … --reply-to P1`; a batch holding only a response → still a reply (`--reply-to P2`); a self-addressed last message → `send commit startup … --reply-to S1`, a reply to itself. Fixtures: `recordingBus` (a bus binary that logs each call's argv) and `textModel` (a stub model through the `*http.Client` seam, no socket). Its failure text reads "Phase 2/3 landed: invert this" |
| Live path, `/tmp/mux111-pin.sh` | A muxcode built from the tree on a scratch HOME and state: edit → commit request sent `--track`, build → commit response; the real `ConsumeInbox` read both and the real `processBatch` (stub model) answered. On the bus the reply went to **build** with `reply_to` = the **response's** id; the request's delivery status stayed `acked`; edit's tracked task for the request stayed `in-flight` |

### Step 2's second half is disproved

The request **does** carry a receipt today. The harness consumes through `muxcode inbox --raw`,
which writes an ack receipt — the pin read `acked_at` set, `receipt_kind` `ack`, `acked_by`
`commit` on the request's status record. `hasReceipt()` is therefore true, the receipt gap
**closes**, and the delivery backstop does not re-drive the request. The Context table's "never
receipted" and "re-drives forever" rows describe the tree before consume receipts.

What stands, and is the defect's cost now:

- The request never reaches `responded`, so the **sender's tracked task never completes** — a
  `--track` or `--wait` send, or a graph send node, waits out its timeout (600 s by default)
  though the harness answered.
- The answer lands with the wrong recipient and the wrong correlation id: build received a
  "response" to its own response.
- The reply to self and the reply with no request in the batch, both pinned by the unit test.

### Stale wording, left as written for the user

Plan has not restated any of these; each reads against a re-drive that no longer happens:

| Where | Wording | The worker's suggested restatement |
|-------|---------|-----------------------------------|
| Context table, rows 1 and 2 | "The request is never receipted", "The backstop re-drives forever" | The request is never marked `responded`; its sender's tracked task never completes |
| Criterion 3 | "…reaches `Status == responded`, closing the receipt gap" | The gap is already closed by the consume; the first half stands |
| Criterion 9 | "The delivery backstop stops re-driving a request the harness has answered" | The answered request reaches `responded` and its sender's tracked task completes, verified end to end |
| Phase 5, step 3 | "Assert the delivery backstop stops re-driving an answered request" | Assert the sender's tracked task completes |
| "Interaction with MUX-110" | "an un-receipted startup request is re-driven every backstop cycle" | Unverified on today's tree; MUX-110's own repeat mechanism may need re-reading |

As first written, criterion 9 and Phase 5's step 3 could be satisfied without the fix — nothing
re-drives today — so they could not tell a fixed tree from this one. Both were restated
2026-11-28 on edit's call; see [Restated 2026-11-28](#restated-2026-11-28-edits-call). The other
three rows are unchanged.

## Findings for Phase 2

Recorded 2026-11-28 from the worker's report, the diff, the test and the pin run (run agent task
`1795843728-spawn-a5769f4f-4cc8fa51`, 00:29, **10 passed, 0 failed**), all read by plan.

### What changed

| Where | Change |
|-------|--------|
| `harness/loop.go`, `answeredRequest(msgs)` | New. Returns the batch's **last** message of type `request`, and false when the batch holds none |
| `harness/loop.go`, `processBatch` | The reply's recipient (`ReplyTarget()`, so `ReplyRole` is honoured), action and `--reply-to` id all come from that one message. A batch with no request is still processed by the model but sends no reply, and its completion event says so. `lastMsg` is kept for display and trace labels only |
| `harness/loop_test.go`, `TestProcessBatch_ReplyCorrelatesToTheRequest` | Replaces the Phase 1 characterization test. Five cases: a lone request (the negative control), `[request, response]`, `[response, request]`, a request whose `ReplyRole` names the recipient, and a batch with no request (no send at all) |

### Evidence

| Check | Result |
|-------|--------|
| Harness package on the tree, after the code edit | 176 PASS, 0 FAIL; `go vet` clean |
| Mutant — `answeredRequest` replaced by `msgs[len(msgs)-1]` in a scratch copy | `[request, response]`, the `ReplyRole` case and the no-request case fail; the lone-request control and `[response, request]` pass |
| Live path — muxcode built from the tree, scratch bus, the real `ConsumeInbox` and `processBatch` with a stub model | The reply went to **edit** with action `commit` and `reply_to` = the **request's** id; the request's status reached `responded`; nothing was sent to build |

### Limits as built

- **Several requests in one batch still answer only the last one.** The others are consumed and
  never answered. Phase 4 owns this.
- **A self-addressed request is still answered to itself.** Selection is by type alone; Phase 3
  owns the filter. Phase 1's pin of the reply-to-self case went with the characterization test
  and nothing pins it now.
- **The sender's tracked task completing was not observed.** The pin's first run was 10 passed,
  1 failed: it expected the task complete with no daemon running, and completing a tracked task
  is the daemon's work (`checkTrackedTasks`). The worker dropped that check; Phase 5's script
  runs a daemon. The restated criterion 9 therefore rests on Phase 5.

## Findings for Phase 3

Recorded 2026-11-28 from the worker's report, the diff, the test and the pin run (run agent task
`1795843964-spawn-a5769f4f-f8b8ebdc`, 00:32, **5 passed, 0 failed**), all read by plan.

### What changed

| Where | Change |
|-------|--------|
| `harness/bus.go`, `dropSelfSends` | New, called by `ConsumeInbox()`. Drops every message the role sent itself, **except** the launch-time `request:startup` bootstrap — the bus's own rule (`isLoopingSelfSend`, `isStartupBootstrap` in `bus/inbox.go`) |
| `harness/loop_test.go`, `TestConsumeInbox_SelfAddressedNeverAnsweredToSelf` | Raw, unfiltered rows through a stub bus binary: a request from edit, the role's own response, and the bootstrap. The consume keeps the request and the bootstrap and drops the response; the reply then goes to edit and names no self-sent id |

### The bus already filters at consume

`muxcode inbox` drops looping self-sends before printing (`cmd/inbox.go` `readInbox` →
`bus.FilterLoopingSelfSends`), so a harness on today's binary never receives a self-addressed
response through `ConsumeInbox`. The harness guard covers an inbox the CLI did not filter — an
older binary's, or one edited by hand. It is a second line, not the one that was missing.

### Evidence

| Check | Result |
|-------|--------|
| Harness package on the tree | 177 PASS, 0 FAIL; `go vet` clean |
| Mutant — `dropSelfSends` removed in a scratch copy | Fails at the consume assertion: "consumed [R1 S1 B1], want R1 and the startup bootstrap B1" |
| `TestProcessBatch_ReplyCorrelatesToTheRequest` | Still passes |

### Limits as built

- **The startup bootstrap is still answered to self.** It is kept on purpose — it is how an agent
  learns it has launched — and its reply is the role's own `response:startup`, which the bus
  correlates and never delivers. The user's decision reads "self-addressed messages: filtered
  out"; the exemption follows the bus's rule, not that wording. Criterion 4 is held on it, below.
- **The test's reply assertion does not exercise the bootstrap.** It hands `processBatch` the
  first consumed message only. With the whole consumed batch — the request and then the
  bootstrap — the last request is the bootstrap, so the reply would go to self and edit's request
  would go unanswered until Phase 4 answers each request. The assertion that discriminates is the
  consume one, which the mutant fails.
- No live-path run this phase: on today's binary the CLI filters first, so a live run could not
  tell the harness filter from none.

**Criterion 4.** "Self-addressed messages never cause the agent to reply to itself" is true for
everything but the bootstrap. Plan held it on that; edit confirmed the exemption 2026-11-28 as its
reading of the user's decision, and the criterion now states it — see
[Restated 2026-11-28](#restated-2026-11-28-edits-call). It is ticked with Phase 3's code step,
once the suite and review have run on the change.

## Findings for Phase 4

Recorded 2026-11-28 from the worker's report, the diff, the test and the pin run (run agent task
`1795844199-spawn-a5769f4f-1fdfe4ca`, 00:36, **4 passed, 0 failed**), all read by plan.

### The implementation choice

**One conversation for the batch; its answer is sent to each request.** The model sees every
message through `FormatTask` and spends one turn budget; when it finishes, the same answer text
goes out once per request, as that request's own reply. The alternative — one conversation per
request — was not built.

### What changed

| Where | Change |
|-------|--------|
| `harness/loop.go`, `batchRequests(msgs)` | Replaces Phase 2's `answeredRequest`. Lists every message of type `request`, in arrival order |
| `harness/loop.go`, `processBatch` | Sends one reply per listed request — recipient (`ReplyTarget()`), action and `--reply-to` id from that request. A send that fails is reported and the rest still go. An empty list sends nothing |
| `harness/loop_test.go`, `TestProcessBatch_ReplyCorrelatesToTheRequest` | Now eight cases. Added: two requests from two senders (one reply each), two from one sender (each answered once, MUX-222's case), and `[request, startup bootstrap]` — the request is answered to edit and the bootstrap to self, where before the bootstrap, as the last request, took the only reply |
| `docs/architecture.md`, Delivery Tracking | New paragraph and table, "Correlation is the responder's job" (written by plan) |

### Evidence

| Check | Result |
|-------|--------|
| Harness package on the tree | 177 PASS, 0 FAIL; `go vet` clean |
| Mutant — only the last request answered, in a scratch copy | The three multi-request cases fail; the five others, the lone-request control among them, pass |

### Limits as built

- **"Both `responded`" is not observed.** The test's bus is a recording stub: it shows two sends
  with the right recipient, action and id, and nothing about delivery status. Phase 4's
  two-sender step is ticked for the half it tests and says so; the observation is carried by
  Phase 5's hermetic step, reworded 2026-11-28 to name it, and by criterion 6. Neither closes
  without a live-bus run of a two-request batch.
- **Two replies with the same text to one sender.** The response brake (MUX-175) refuses a
  response repeating the last one on the same sender, recipient and action within 300 s. It
  exempts a reply whose `--reply-to` names a request still awaiting its answer
  (`answersOpenRequest`, `bus/response_brake.go`), which is this case — read from the code, not
  run. The same live run settles it.
- **Every request gets the same answer.** A batch of unrelated requests is answered by one text
  addressed to all of them; nothing splits it per request.

## Findings for Phase 5

Recorded 2026-11-28 from the worker's report, `scripts/test-harness-reply-correlation.sh` and its
run log (run agent task `1795844524-spawn-a5769f4f-1092c3f2`, 00:42), all read by plan.

### The run: 32 passed, 0 failed, 1 skipped — floor 32

The final run is run agent task `1795844649-spawn-a5769f4f-c7d68c92`, 00:44, after section F was
added. The first complete run, 00:42, was 25 passed, 0 failed against a floor of 25, without F.

| Section | Checks | What it showed |
|---------|--------|----------------|
| F — a tracked request, commit's own `request:startup` bootstrap, and a self-addressed response planted in commit's inbox | 7 | The planted response never reached the batch and nothing answered it; the request was answered to edit under its own id and reached `responded`; commit's inbox held no message from commit afterwards; edit's task completed |
| Setup | 2 | muxcode built from the tree; harness driver and last-message control copies; scratch daemon running |
| A — `[tracked request, response]` | 8 | The reply went to edit with the request's action under the request's id; nothing correlated to the response or sent to build; the request reached `responded`; edit's tracked task was completed by the daemon; `diagnose` found no receipt gap |
| B — requests from edit and plan | 6 | Each answered to its own sender under its own id; **both `responded`**; both tasks completed |
| C — two requests from edit, the same answer text | 6 | Both replies on the bus under their own ids; no `response-suppressed` row; both `responded`; both tasks completed |
| D — control, selection reverted to the last message | 3 | The control correlated to the response and left the request `acked` |
| E — a real Ollama model | skipped | No endpoint answered at `http://localhost:11434`. **Not run** |

How it runs: a muxcode built from the tree on a scratch HOME, state and lifecycle log, a private
tmux server, and a scratch daemon polling every second. The harness is a scratch copy of its
module carrying one extra test, `TestDriveBatch`, which calls the real `ConsumeInbox` and
`processBatch` with a stub model through the `*http.Client` seam.

### What this closes

- Phase 4's unobserved "both `responded`" — section B for two senders, section C for one sender
  twice.
- The response-brake question from Phase 4: two same-text replies to one sender were both
  delivered. `answersOpenRequest`'s exemption, read then, is now run.
- The restated criterion 9: the tracked task completing, with a daemon, end to end.

### The self-addressed assertion: held, then closed by section F

At 00:42 the only such assertion was section A's "the bus log holds no commit → commit
message", on a batch holding no self-addressed message — it passed on a tree with no filter at
all. Plan held the step and the final run. The worker added section F and reran.

What F does and does not prove:

- **It proves the end-to-end behaviour the step names.** With a bootstrap and a planted
  self-response in the inbox, the real request is still answered to its sender, and commit's own
  inbox holds nothing from commit afterwards. A tree answering only the batch's last request
  would answer the bootstrap and fail F's request assertion.
- **It does not tell the harness's own filter from none.** `muxcode inbox` drops the planted row
  (`FilterLoopingSelfSends`) before the harness sees it, so F passes with or without
  `dropSelfSends`. The harness guard is pinned only by the unit test
  `TestConsumeInbox_SelfAddressedNeverAnsweredToSelf` and its mutant (Phase 3).

### Review should-fixes, fixed 00:47

Rerun as run agent task `1795844864-spawn-a5769f4f-52a58ec0`, 00:48: **32 passed, 0 failed, 32
hermetic**, E skipped. The log postdates the script edit.

| Finding | Fix |
|---------|-----|
| The coverage floor counted every check, so a passing model check could make up for a skipped hermetic one | `HERMETIC_PASS` is taken after section F, before the model section, and the floor of 32 is tested against it alone; the summary line prints it |
| The model section drove the `[request, response]` batch only, where the step says "the same batches" | Section E now drives all three shapes through the model — request and response, two senders, one sender twice — each request asserted answered to its own sender under its own id and `responded` (helper `answered_as`). Still skipped with a reason without Ollama |

### Other limits

- **The model path has never executed.** Section E's skip was observed; what it does with a
  model answering was not. The user's 2026-11-24 decision accepts the skip. Since the review
  fixes below E drives all three batch shapes; none of its cases has run.
- **The first run aborted in section B.** The CLI's 30-second dedup on the same sender, target
  and action refused a send, and `set -e` hid it. Sends now pass `--force`, and a failed send is
  a counted FAIL. So the script's requests reach the bus by a road an agent's ordinary send does
  not take.
- The script's driver test exists only in its scratch copies; nothing in the tree runs the real
  consume and reply together.

## Status

**Complete** — closed out 2026-11-28 by `55-defect-backlog-loop` run 1795811929 at 31/31.

### What shipped

- The local LLM harness answers the **request**, not the batch's last message: recipient
  (`ReplyTarget()`), action and `--reply-to` id all come from one request (`batchRequests`,
  `harness/loop.go`).
- A batch with several requests runs as one conversation, and its answer is sent to each request
  as that request's own reply. A batch with no request sends no reply.
- `ConsumeInbox` drops messages the role sent itself, except the launch-time `request:startup`
  bootstrap (`dropSelfSends`, `harness/bus.go`).
- `scripts/test-harness-reply-correlation.sh`: 32 passed, 0 failed, 32 hermetic against a floor
  of 32, on a real scratch bus and daemon; its real-model section skipped.
- `docs/architecture.md`, Delivery Tracking: "Correlation is the responder's job".

### Left as recorded

- **The real-model section has never run.** No Ollama endpoint answers on the machine that built
  this; the user's 2026-11-24 decision accepts the skip.
- **The harness's own self-send filter is told from none only by its unit test.** `muxcode inbox`
  filters first, so no live run can distinguish it.
- **Every request in a batch gets the same answer text.**
- **Criterion 4's bootstrap exemption, and the restated criterion 9 and Phase 5 step 3, are
  edit's calls** under the user's standing instruction, not user decisions — recorded under
  [Restated 2026-11-28](#restated-2026-11-28-edits-call) for the user to overrule.
- **Stale wording left as written:** Context rows 1 and 2, the tail of criterion 3 and the
  "Interaction with MUX-110" paragraph describe a re-drive that no longer happens — see
  [Findings for Phase 1](#findings-for-phase-1). MUX-110's own account of why its loop repeats
  may need re-reading against it.
- The script's sends pass `--force` past the CLI's 30-second same-sender dedup.

### How it got here

Unparked 2026-11-24 on the user's decisions above; activated 2026-11-28. Phase 1 found the
request already receipted at consume, which moved the defect's cost from a re-drive to a tracked
task that never completes. Each phase was verified against the full suite run after its last
code edit and a clean review.
