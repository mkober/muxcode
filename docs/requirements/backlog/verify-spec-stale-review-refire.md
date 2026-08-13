# Verify-Spec Stale Review Refire

One review completion can generate an unbounded stream of identical `verify-spec` requests to the plan agent. The daemon fires the reviewed-transition on **any** growth of edit's inbox while **any** unconsumed review→edit message exists — and plan's mandated "reply to edit" is itself growth, so plan's own compliance sustains the loop.

## Context

### Observed failure (2026-08-13, 12:07–12:09)

- Review completed once (its response to edit sat unconsumed while edit was busy in a long turn).
- Plan received 4 identical `verify-spec` requests in ~2 minutes (12:07:40, 12:08:09, 12:08:40, 12:09:36), each naming only the spec doc itself as the changed file.
- Each fire landed on the daemon poll ~30s after plan sent a reply to edit — 1:1 correlation with plan's replies (verification summary, then per-message no-op replies, then a loop alert).
- `muxcode diagnose review` during the storm: review **idle, alive, inbox empty** — no reviews were running. The generator was the daemon re-firing on stale state.
- The loop only terminated when plan stopped replying to edit.

### Root cause

`daemon/daemon.go` `checkInboxes()` (~:360-368):

```go
if size > prev && size > 0 {
    if role == "edit" && bus.HasNewMessageFrom(d.session, "edit", "review") {
        bus.TransitionWorkflow(d.session, bus.StateReviewed, "daemon:review-complete", ...)
        d.notifyPlanOnReview()
    }
}
```

- The growth check (`size > prev`) is sender-agnostic: **any** message to edit trips it.
- `HasNewMessageFrom()` (`bus/workflow.go` ~:350) just peeks for **any** unconsumed message from review — it has no notion of "new since last check".
- So the condition is really "edit got mail while a review message is still unconsumed", which stays true for as long as edit is busy — and every re-fire both re-transitions the workflow state and sends plan another `verify-spec`.
- Plan's `verify-spec` instructions end with "Reply to edit with a summary" — that reply is the next inbox growth. One review completion + one busy edit + one compliant plan = self-sustaining loop.

### Impact

1. **Duplicate work requests**: plan burns turns re-verifying an unchanged spec; its no-op replies add noise to edit's already-backlogged inbox.
2. **Workflow state churn**: `TransitionWorkflow(StateReviewed)` re-fires per echo, so the state log records review completions that never happened.
3. **Self-amplifying under load**: the busier edit is, the longer the review message sits unconsumed, the more echoes fire — worst exactly when the session is busiest.
4. **Time-recording double exposure**: on a non-ignored branch each echo would also re-run the time-recording pass (harmless in value terms — absolute totals are idempotent — but each pass costs a ledger read/write cycle).

## Requirements

### Proposed fix

Make the reviewed-transition fire once per actual review completion. Options, roughly in order of robustness:

1. **Track the last-seen review message ID** — daemon remembers the ID of the review→edit message that last triggered the transition; `HasNewMessageFrom` gains a variant returning the newest matching message ID so the daemon only fires when it changes.
2. **Inspect the growth delta** — only fire when the newly appended bytes (messages after `prev`) contain a message from review, so unrelated senders growing edit's inbox never trip the check.
3. **Dedup at the send** — keep the trigger as-is but make `notifyPlanOnReview()` idempotent per workflow transition (e.g. skip if the workflow state is already `StateReviewed` with the same outcome and no intervening state change).

Option 1 or 2 also fixes the workflow-state churn; option 3 alone does not.

### Acceptance criteria

- [ ] One review completion produces exactly one `verify-spec` request to plan, regardless of how many other messages edit receives before draining its inbox
- [ ] Plan replying to edit while a review message sits unconsumed does not re-fire the transition or `notifyPlanOnReview()`
- [ ] `TransitionWorkflow(StateReviewed)` fires once per actual review completion
- [ ] A genuine second review completion (new review→edit message) still fires a new `verify-spec`
- [ ] Existing daemon and workflow tests still pass

### Key files

| File | Change |
|------|--------|
| `daemon/daemon.go` | `checkInboxes()` reviewed-transition gate (~:360-368) |
| `bus/workflow.go` | `HasNewMessageFrom()` or an ID-aware variant |
| `daemon/daemon_test.go` / `bus/workflow_test.go` | Unit tests for once-per-completion semantics |

## Implementation

### Phase 1: Once-per-completion gate

- [ ] Implement the chosen gate (last-seen review message ID or growth-delta inspection)
- [ ] Unit tests: unrelated inbox growth does not re-fire; a new review message does; state transitions once per completion

### Phase 2: Integration test

- [ ] Create `scripts/test-verify-spec-refire.sh` (or extend an existing daemon integration script)
- [ ] Test: seed edit's inbox with one review response, send two unrelated messages to edit → assert exactly one `verify-spec` lands in plan's inbox
- [ ] Test: append a second review response → assert a second `verify-spec` fires
- [ ] Run the script and verify all checks pass

## Provenance

Found by the plan agent on 2026-08-13 while receiving the echo storm first-hand during branch-time-tracking verification; confirmed via `muxcode diagnose review` (idle/empty during the storm) and reading `checkInboxes()` + `HasNewMessageFrom()`. The bug is in committed code — today's working-tree change to `notifyPlanOnReview()` only touched the message body.

## Status

Backlog
