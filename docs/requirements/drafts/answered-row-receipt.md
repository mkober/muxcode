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

- [x] An answered-but-unconsumed request reads as receipted: `ReadReceipt` returns true when `Status == StatusResponded` even with `AckedAt == 0` (`TestReadReceipt_RespondedCountsAsReceipted`)
- [x] `ReceiptGap` excludes answered rows — no permanent gap, no repeated `ForceDeliver` / `SendWakeUp` / `delivery-gap` alerts for finished work (`TestReply_ClearsReceiptGap`, `TestReceiptGap_ReturnsStaleUnreceipted`, `TestReceiptGap_IgnoresSelfSends`)
- [x] Replying drains the answered request from the responder's own inbox — `HasActionableMessages` goes false, no re-wake, no duplicate responses (`TestReply_DrainsAnsweredRequestRow`)
- [x] Hosted roles drain from the **host** inbox (`WindowForRole(original.To)`, resolved from the original request), never a phantom per-role inbox (`TestReply_DrainsFromHostInboxForHostedRole`)
- [x] Drain is a no-op (no error, no double receipt) when the row was already consumed — the normal path (`TestConsumeByID_NoopWhenAbsent`)
- [x] Drain removes only the answered message — all other inbox messages untouched (`TestConsumeByID_LeavesOtherMessages`)
- [x] Auto-CC copies survive: draining keys on message ID, so a CC copy of the request in edit's inbox is never collaterally removed (`TestReply_LeavesAutoCCCopyInEditInbox`)

### Technical approach

**Half 1 — one definition of "receipted" on the read path** (`bus/delivery.go`):

```go
hasReceipt(ds) = ds.AckedAt > 0 || ds.Status == StatusResponded
```

Shared by `ReadReceipt` (and therefore `ReceiptGap`). A reply is strictly stronger evidence than a consume-ack: the agent didn't just read the message, it finished the work and answered.

**Half 2 — drain the answered row** (`bus/delivery.go` + `bus/inbox.go`):

The drain lives at a single choke point — `MarkResponded()` (`bus/delivery.go`) — not in `Send()`. A request becomes "responded" by more than one path: `Send()` when a reply carries `ReplyTo`, and `cmd/send.go`'s `--wait` fallback, which correlates a response sent WITHOUT `ReplyTo` back to the request it was waiting on. The second path has no reply message to hang a drain off, so a `Send()`-only drain left those rows actionable forever — the same shape as the original defect (one path honoring an invariant, another quietly not). Caught live as a stranded `run.jsonl` row whose "response" predated its request by 13s. The invariant "marked responded implies drained" is therefore enforced at the one choke point every path already goes through. `MarkResponded()`:

- Resolves the recipient from the ORIGINAL request's `To` field via `FindMessageByID` — `ConsumeByID(session, WindowForRole(original.To), originalID)` — never from the reply's `From`, so a mis-attributed or stale correlation still drains the right inbox
- Treats the delivery-status read as non-fatal: a missing/GC'd status file skips the status update but never skips the drain
- Best-effort throughout: a request whose log entry has been rotated away simply isn't drained

The helper `ConsumeByID(session, role, msgID)` (`bus/inbox.go`):

- Targeted removal by message ID with the same atomic rename + write-back discipline as `ReceiveFromFunc` — restore on transient read error, write survivors back before reporting success so a failure can never silently discard unrelated messages
- Missing message, missing inbox, and already-consumed row are ordinary no-ops, not errors
- On found: writes a true consume-ack receipt (`WriteReceipt(session, msgID, role, ReceiptKindAck)`)

### Key files

| File | Change |
|------|--------|
| `bus/delivery.go` | `hasReceipt()` single read-side definition; `ReadReceipt()` routes through it; `MarkResponded()` hosts the drain choke point (resolves recipient from the original request's `To`); invariant documented on `WriteReceipt`, `hasReceipt`, and `MarkResponded` |
| `bus/inbox.go` | `ConsumeByID()` helper — targeted removal by ID, atomic rename + write-back, ack receipt on found; `FindMessageByID()` resolves the original request |
| `bus/answered_row_test.go` | All 9 unit tests: read-path receipt/gap, drain-on-reply, hosted-role routing, CC survival, no-op paths, choke-point (`MarkResponded`) drain |

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
- [x] Unit tests: a responded-without-ack message reads as receipted; `ReceiptGap` excludes it; an unanswered unconsumed message still counts in the gap (`TestReadReceipt_RespondedCountsAsReceipted`, `TestReply_ClearsReceiptGap`, `TestReceiptGap_ReturnsStaleUnreceipted`, `TestReceiptGap_IgnoresSelfSends`)

### Phase 2: Drain the answered row (Half 2)

- [x] Add `ConsumeByID(session, role, msgID)` to `bus/inbox.go` — targeted removal by ID, atomic rename + write-back, restore on read error, ack receipt on found
- [x] Drain at the single choke point `MarkResponded()` (`bus/delivery.go`): resolve the recipient from the original request's `To` via `FindMessageByID`, then `ConsumeByID(session, WindowForRole(original.To), originalID)` — covers both the `Send()` `ReplyTo` path and the `--wait` no-`ReplyTo` correlation fallback; the delivery-status read is non-fatal and never skips the drain
- [x] No-op semantics: missing message, missing inbox, already-consumed row return found=false with no error
- [x] Key on message ID only — auto-CC copies with the same From/Action are never collaterally removed
- [x] Unit tests: drain on reply; no-op when already consumed; other messages untouched (order preserved); hosted-role drain via `WindowForRole`; CC copy in edit's inbox survives (`TestReply_DrainsAnsweredRequestRow`, `TestReply_DrainsFromHostInboxForHostedRole`, `TestReply_LeavesAutoCCCopyInEditInbox`, `TestConsumeByID_LeavesOtherMessages`, `TestConsumeByID_NoopWhenAbsent`; choke-point coverage: `TestMarkResponded_DrainsWithoutAReplyMessage`, `TestMarkResponded_SurvivesMissingStatusFile`)

### Phase 3: Verification and docs

- [x] `./test.sh` passes (`go vet` + full `go test`) with the new tests — verified from console history: exit 0, go vet clean, 5/5 packages PASS, 0 failures
- [x] Cross-link added in `docs/requirements/backlog/remove-gated-pane-scrape-delivery.md` Known limitation — answered-row cause fixed here, busy-TUI case remains, prerequisite NOT fully resolved
- [x] Update the delivery-acknowledgement bullet in `CLAUDE.md` if the receipt-gap description needs the answered-row nuance — done by edit 2026-08-11: `hasReceipt` semantics and the `MarkResponded` choke point added to the bullet

### Phase 4: Integration test

Coverage note: the script verifies these behaviours through the Go suite plus a **non-destructive live smoke check** — it never sends to, wakes, or disturbs a running agent (same shape as `scripts/test-delivery-ack.sh`). The bullets below are worded to match what the script actually does; the original wording described live bus sends the script deliberately does not perform.

- [x] Create `scripts/test-answered-row-receipt.sh` with end-to-end verification (requires running muxcode session)
- [x] Reply-without-consume drains the request row from the responder's inbox — via the Go suite (`TestReply_DrainsAnsweredRequestRow`, `TestReply_DrainsFromHostInboxForHostedRole`)
- [x] The request's delivery status reads receipted (`responded` status, ack receipt present) — via the Go suite (`TestReadReceipt_RespondedCountsAsReceipted`)
- [x] No `delivery-gap` for answered rows — gap exclusion via the Go suite (`TestReply_ClearsReceiptGap`) plus the live smoke assertion: no answered-but-still-actionable request rows in any inbox of the running session
- [x] CC copy of the request in edit's inbox survives the drain — via the Go suite (`TestReply_LeavesAutoCCCopyInEditInbox`)
- [x] Reply to an already-consumed request is an ordinary no-op — via the Go suite (`TestConsumeByID_NoopWhenAbsent`)
- [x] Run the script and verify all checks pass — exit 0, "All answered-row receipt checks passed"

## Status

In Progress — implementation, verification, and docs complete; every phase and acceptance criterion checked. Both halves committed (7a6fb5c, 8e9078a); the choke-point relocation of the drain into `MarkResponded()`, `scripts/test-answered-row-receipt.sh`, and the CLAUDE.md nuance are in the working tree, **not yet committed**. Sole remaining step: commit of the working-tree changes (user-initiated) — then ready to move to `completed/`.
