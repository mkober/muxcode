# Harness Reply Correlates to the Batch's Last Message, Not the Request

The local harness answers a whole inbox batch with a single reply addressed to — and correlated
with — `msgs[len(msgs)-1]`. When the last message in the batch is not the request being answered,
the request is never marked responded, never gains a receipt, and the delivery backstop re-drives it
indefinitely. Found during the [MUX-109](../drafts/MUX-109-prompt-mode-graph-control-pane.md) Phase 2
regression check.

## Context

### Root cause

`harness/loop.go` picks one message to represent the batch:

```go
lastMsg := msgs[len(msgs)-1]                                            // :276
...
bus.Send(lastMsg.From, lastMsg.Action, finalResponse, "response", lastMsg.ID)   // :491
```

Every field of the reply — recipient, action, and correlation id — comes from whichever message
happened to land **last**, with no check that it is a request, or even that it came from someone
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

## Requirements

### Acceptance criteria

- [ ] A reply correlates to the **request** it answers, never to a response or an unrelated message that happened to arrive last
- [ ] A batch of `[request, response]` produces a reply whose `ReplyTo` is the **request's** id
- [ ] The answered request reaches `Status == responded`, closing the receipt gap
- [ ] Self-addressed messages never cause the agent to reply to itself
- [ ] A batch containing **no** request produces no spurious reply
- [ ] A batch containing **multiple** requests has defined, documented behaviour — answer each, or answer the first and leave the rest actionable; silently dropping the others is not acceptable
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
- **Decide multi-request behaviour explicitly.** The honest options are reply-per-request or
  answer-one-and-leave-the-rest-actionable. Either is defensible; leaving them silently unanswered
  is what creates orphaned requests the backstop chases.
- **Verify against the live path.** This class of defect is invisible to in-process tests — MUX-014
  shipped two live-path bugs every executor unit test passed over, including an unreplyable message
  whose reply target was wrong. Unit tests plus an integration run, not unit tests alone.

### Key files

| File | Change |
|------|--------|
| `harness/loop.go` | Request selection instead of `msgs[len(msgs)-1]` (`:276`, `:491`) |
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

- [ ] Choose and document the behaviour for a batch with several requests
- [ ] Test the chosen behaviour, including that no request is silently dropped

### Phase 5: Integration test

- [ ] Create `scripts/test-harness-reply-correlation.sh` — hermetic: scratch `BUS_SESSION`, scratch inbox
- [ ] Drive a `[request, response]` batch through a harness agent and assert the request reaches `responded`
- [ ] Assert the delivery backstop stops re-driving that request
- [ ] Assert no self-addressed reply appears in the agent's own inbox
- [ ] Skip-with-reason when Ollama or the model is unavailable, with a **coverage floor** so a skipped run cannot report green
- [ ] Run the script and confirm all checks pass

## Status

Draft
