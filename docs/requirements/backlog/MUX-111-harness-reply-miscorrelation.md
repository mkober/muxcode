# Harness Reply Correlates to the Batch's Last Message, Not the Request

**Tracking:** [mkober/muxcode#45](https://github.com/mkober/muxcode/issues/45)

The local harness answers a whole inbox batch with a single reply addressed to — and correlated
with — `msgs[len(msgs)-1]`. When the last message in the batch is not the request being answered,
the request is never marked responded, never gains a receipt, and the delivery backstop re-drives it
indefinitely. Found during the [MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md) Phase 2
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
| A batch holding several requests | **Answer each request with its own reply** — recipient, action and correlation id taken from that request, the precedent [MUX-189](../completed/MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) set for typed batches. The alternative (answer the first, leave the rest actionable) is dropped |
| Self-addressed messages | **Filtered out** before the batch is formed |
| Integration script without Ollama | Skips its model-driven section with a reason (`SKIP`, exit 2 when nothing but the skip remains), and its hermetic section still carries a **coverage floor** |

How one model conversation yields one reply per request — one conversation per request, or one
conversation whose answer is sent to each — is an implementation choice for Phase 4, recorded there
when made; either satisfies the decision so long as every request is answered under its own id.

## Requirements

### Acceptance criteria

- [ ] A reply correlates to the **request** it answers, never to a response or an unrelated message that happened to arrive last
- [ ] A batch of `[request, response]` produces a reply whose `ReplyTo` is the **request's** id
- [ ] The answered request reaches `Status == responded`, closing the receipt gap
- [ ] Self-addressed messages never cause the agent to reply to itself
- [ ] A batch containing **no** request produces no spurious reply
- [ ] A batch containing **multiple** requests answers **each** with its own reply, correlated to that request's id and sent to that request's reply target; every one reaches `Status == responded` and none is silently dropped
- [ ] The reply's recipient and action come from the same message as the correlation id — they cannot disagree
- [ ] **Negative control:** the single-request case (the common path) is unchanged, proving the fix is not "never correlate"
- [ ] The delivery backstop stops re-driving a request the harness has answered — verified end to end, not only in unit tests
- [ ] `scripts/test-harness-reply-correlation.sh` passes

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

- [ ] Reproduce with a batch of `[request, response]` and confirm the reply carries the response's id
- [ ] Confirm the request's delivery status never reaches `responded`, and the receipt gap stays open
- [ ] Unit tests capturing current behaviour before changing it

### Phase 2: Correlate to the request

- [ ] Select the answered request; take recipient, action, and correlation id from that one message
- [ ] Test: `[request, response]` → reply correlates to the request
- [ ] Test: single request → unchanged (**negative control** against a "never correlate" regression)
- [ ] Test: no request in batch → no reply sent

### Phase 3: Self-addressed filtering

- [ ] Filter self-addressed messages out of the consumed batch
- [ ] Test: a self-addressed response never produces a reply to self

### Phase 4: Multi-request behaviour

- [ ] A batch with several requests sends one reply per request, each with that request's reply target, action and id; record here whether it runs one conversation per request or one for the batch
- [ ] Test: `[request A, request B]` from two senders → two replies, each correlated to its own request, both `responded`
- [ ] Test: two requests from **one** sender are each answered once (MUX-222's case)
- [ ] Document the behaviour in `docs/architecture.md`'s delivery-tracking section

### Phase 5: Integration test

- [ ] Create `scripts/test-harness-reply-correlation.sh` — hermetic: scratch `BUS_SESSION`, scratch inbox
- [ ] Hermetic section, no Ollama needed (a stub model endpoint through the harness's `*http.Client` seam, never a bound socket): a `[request, response]` batch → the request reaches `responded`; a two-request batch → two replies, each under its own id
- [ ] Assert the delivery backstop stops re-driving an answered request
- [ ] Assert no self-addressed reply appears in the agent's own inbox
- [ ] **Negative control:** a build from a copy of the tree with the request selection reverted to `msgs[len(msgs)-1]` correlates to the response and leaves the request un-receipted
- [ ] Model-driven section: the same batches through a real Ollama model; skipped with a reason when Ollama or the model is unavailable
- [ ] **Coverage floor** on the hermetic section, so a run that skipped the model cannot report green on nothing
- [ ] Run the script and confirm all checks pass

## Status

**Backlog** — unparked 2026-11-24 on the user's decisions above. Not started.
