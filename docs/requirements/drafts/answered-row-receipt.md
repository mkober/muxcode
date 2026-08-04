# Answered-Row Receipt

Fix the delivery-tracking defect where a request an agent **answered** (replied to) but never **consumed** from its inbox reads as un-receipted forever. The stale row makes the daemon's receipt-gap backstop re-drive delivery and emit `delivery-gap` alerts indefinitely, and keeps the request permanently actionable so the agent is re-woken and re-answers finished work — producing duplicate responses.

## Context

### Observed failure

A live `test → review` chain request was never receipted and was re-driven for ~21 hours (~76089s):

| Symptom | Evidence |
|---------|----------|
| 4+ duplicate LGTM responses from review | Only ONE real review ever ran (console history); every later LGTM was an echo of re-woken finished work |
| 3 `delivery-gap` events alerted to edit | Backstop re-drove delivery for a request that was already answered |
| Request row permanently actionable | `HasActionableMessages` stayed true, so the daemon kept re-waking the agent |

### Root cause — read/write asymmetry in the receipt model

The chain, step by step:

1. `bus/inbox.go` `Send()`: when `m.ReplyTo != ""` it calls `MarkResponded(session, m.ReplyTo, m.ID)`
2. `bus/delivery.go` `MarkResponded()`: sets `Status = StatusResponded` and `ResponseID` — it does **not** set `AckedAt`
3. `bus/delivery.go` `ReadReceipt()`: defined "carries a receipt" as `AckedAt > 0`
4. Therefore an answered-but-unconsumed message reads as un-receipted permanently
5. `bus/delivery.go` `ReceiptGap()`: skips only messages `ReadReceipt` calls acked, so the answered row stays in the gap forever
6. `daemon/daemon.go` `checkPollHealth()`: a non-empty gap triggers `ForceDeliver` / `SendWakeUp` plus a `delivery-gap` alert to edit

The intended invariant is already written down in `WriteReceipt`'s own doc comment: *"A receipt never regresses a message already marked responded (a reply already implies receipt)."* The WRITE path honors it. The READ path did not. That asymmetry is the defect.

**Why it repeated periodically rather than once**: `checkPollHealth` has a recover-once guard (`pollGapRecovered`), but the gate above it RESETS `pollGapRecovered` to false whenever the role transiently fails `roleHasWindow` / `agentAlive` / `HasActionableMessages`. Any blip re-arms the re-drive, so it fires again and again.

### Second half — the answered row never drains

Replying does not consume the request from the responder's own inbox. `HasActionableMessages` stays true, so the row remains actionable indefinitely, the agent keeps being woken for work it already finished, and it answers again. This is what actually produces the duplicate responses — the read-path fix alone only silences the false alarms.

## Requirements

### Acceptance criteria

- [ ] An answered-but-unconsumed request reads as receipted: `ReadReceipt` returns true when `Status == StatusResponded` even with `AckedAt == 0`
- [ ] `ReceiptGap` excludes answered rows — no permanent gap, no repeated `ForceDeliver` / `SendWakeUp` / `delivery-gap` alerts for finished work
- [ ] Replying drains the answered request from the responder's own inbox — `HasActionableMessages` goes false, no re-wake, no duplicate responses
- [ ] Hosted roles drain from the **host** inbox (`WindowForRole(m.From)`), never a phantom per-role inbox
- [ ] Drain is a no-op (no error, no double receipt) when the row was already consumed — the normal path
- [ ] Drain removes only the answered message — all other inbox messages untouched
- [ ] Auto-CC copies survive: draining keys on message ID, so a CC copy of the request in edit's inbox is never collaterally removed

### Technical approach

**Half 1 — one definition of "receipted" on the read path** (`bus/delivery.go`):

```go
hasReceipt(ds) = ds.AckedAt > 0 || ds.Status == StatusResponded
```

Shared by `ReadReceipt` (and therefore `ReceiptGap`). A reply is strictly stronger evidence than a consume-ack: the agent didn't just read the message, it finished the work and answered.

**Half 2 — drain the answered row** (`bus/inbox.go`):

In `Send()`, when `m.ReplyTo != ""`, consume that one message from the responder's own inbox and write a true consume-ack receipt for it. New helper `ConsumeByID(session, role, msgID)`:

- Targets `WindowForRole(m.From)` — matches the hosted-role routing `Send` already uses for delivery
- Targeted removal by message ID with the same atomic rename + write-back discipline as `ReceiveFromFunc` — restore on transient read error, write survivors back before reporting success so a failure can never silently discard unrelated messages
- Missing message, missing inbox, and already-consumed row are ordinary no-ops, not errors
- On found: `WriteReceipt(session, msgID, role, ReceiptKindAck)`

### Key files

| File | Change |
|------|--------|
| `bus/delivery.go` | `hasReceipt()` single read-side definition; `ReadReceipt()` routes through it; invariant documented on both `WriteReceipt` and `hasReceipt` |
| `bus/inbox.go` | `ConsumeByID()` helper; `Send()` drains the answered row on `ReplyTo != ""` |
| `bus/delivery_test.go` | Read-path tests: responded-without-ack reads receipted, gap exclusion |
| `bus/inbox_test.go` | Drain tests: targeted removal, no-op paths, CC-copy survival, hosted-role routing |

### Risks

| Risk | Mitigation |
|------|-----------|
| Half 2 touches EVERY agent reply — blast radius is global | No-op semantics on every abnormal path; write-back-before-success discipline; unit tests over all paths |
| Hosted role drains a phantom inbox | Use `WindowForRole(m.From)` — same routing `Send` uses for delivery |
| Double receipt / error when row already consumed (the normal path) | `ConsumeByID` returns found=false with no error; receipt written only when found |
| Collateral removal of other inbox messages | Targeted removal, atomic rename + write-back (same discipline as `ReceiveFromFunc`) |
| Auto-CC copy in edit's inbox collaterally removed (CC keeps `To` = original recipient) | Drain keys on message ID, never on sender/action |

### Relationship to existing backlog

[remove-gated-pane-scrape-delivery](../backlog/remove-gated-pane-scrape-delivery.md) lists a "receipt-gap mis-fire" prerequisite (its Known limitation section) that blames only busy non-hook TUIs and freshly-idle Claude agents, and prescribes a self-poll liveness heartbeat as the durable fix. The answered-row case documented here is a **distinct and much cheaper** cause of the same symptom. Fixing it removes a large share of those mis-fires without any heartbeat work, so it **partially unblocks** that prerequisite — but the busy-TUI case remains, so that prerequisite is not fully resolved by this spec.

Same disease family: [echo-as-result](echo-as-result.md) — pane-scrape echoes recorded as passing command results in console history. Both bugs treat pane/chat text as evidence of work; this one produced duplicate-response noise, that one fabricates false GREEN.

## Implementation

### Phase 1: Read-path receipt unification (Half 1)

- [x] Add `hasReceipt(ds)` to `bus/delivery.go` — single definition: `AckedAt > 0 || Status == StatusResponded`
- [x] Route `ReadReceipt()` through `hasReceipt()` (and thereby `ReceiptGap()`)
- [x] Document the write/read invariant on both `WriteReceipt` and `hasReceipt` — the two disagreeing was the defect
- [ ] Unit tests: a responded-without-ack message reads as receipted; `ReceiptGap` excludes it; an unanswered unconsumed message still counts in the gap

### Phase 2: Drain the answered row (Half 2)

- [x] Add `ConsumeByID(session, role, msgID)` to `bus/inbox.go` — targeted removal by ID, atomic rename + write-back, restore on read error, ack receipt on found
- [x] `Send()`: on `ReplyTo != ""`, drain via `ConsumeByID(session, WindowForRole(m.From), m.ReplyTo)` after `MarkResponded`
- [x] No-op semantics: missing message, missing inbox, already-consumed row return found=false with no error
- [x] Key on message ID only — auto-CC copies with the same From/Action are never collaterally removed
- [ ] Unit tests: drain on reply; no-op when already consumed; other messages untouched (order preserved); hosted-role drain via `WindowForRole`; CC copy in edit's inbox survives

### Phase 3: Verification and docs

- [ ] `./test.sh` passes (`go vet` + full `go test`) with the new tests
- [x] Cross-link added in `docs/requirements/backlog/remove-gated-pane-scrape-delivery.md` Known limitation — answered-row cause fixed here, busy-TUI case remains, prerequisite NOT fully resolved
- [ ] Update the delivery-acknowledgement bullet in `CLAUDE.md` if the receipt-gap description needs the answered-row nuance

### Phase 4: Integration test

- [ ] Create `scripts/test-answered-row-receipt.sh` with end-to-end verification (requires running muxcode session)
- [ ] Test: send a request to an agent's inbox, send a reply with `--reply-to` WITHOUT consuming → verify the request row is drained from the responder's inbox
- [ ] Test: verify the request's delivery status reads receipted (`responded` status, ack receipt present)
- [ ] Test: verify no `delivery-gap` alert fires for the answered row after the gap threshold elapses
- [ ] Test: place a CC copy of the request in edit's inbox → verify it survives the drain
- [ ] Test: reply to an already-consumed request → verify no error and no inbox disturbance
- [ ] Run the script and verify all checks pass

## Status

In Progress — both halves implemented in the working tree (uncommitted); unit tests, verification, and integration test pending.
