# Loop-Detector Granularity

The daemon's message-loop detector (`DetectMessageLoop`, `bus/guard.go`) fires on normal, healthy edit↔commit traffic. Its ping-pong pass counts a request and its own correlated reply as loop evidence, and its tuple pass cannot tell two unrelated delegations apart from a repeated one. The result is alert fatigue on a safety detector — the operator learns to ignore it, so a genuine relay storm goes unread.

## Context

### Observed false positives (2026-08-24, one session, zero real loops)

| Time | Alert | Actual traffic |
|------|-------|----------------|
| 09:09 | `ping-pong edit <-> commit action:commit 4x in 4m31s` | Two unrelated push requests plus a branch-setup request, all answered first try |
| 09:30 | `action:pr-read 4x in 1m20s` | Two distinct investigation reads (git status detail, GitHub issue numbering facts) |
| 10:01 | `action:commit 4x in 2m46s` | A GitHub issue retitle followed by an unrelated stage-and-commit |

### Anatomy of the detector

`DetectMessageLoop` runs two passes over the recent window (events, daemon traffic, and system actions already excluded):

1. **Repeated-tuple** — counts *requests only* keyed on `(from, to, action)`; responses are already excluded here. Fires at `count >= threshold`.
2. **Ping-pong** — walks `recentAll`, which *includes responses*, counting direction flips (and same-direction repeats) with the same action.

Two defects compound:

- **Correlated replies count as pongs.** A delegation's reply flips direction with the same action — exactly the ping-pong shape. One healthy request/response pair contributes 2; two unrelated same-action delegations inside the window reach the default threshold of 4. This is the 09:09 alert. Note the tension with the delivery-ack model, where a reply is the *strongest* evidence of health ("a reply implies receipt", `hasReceipt()`); the ping-pong pass reads the same reply as pathology.
- **The `commit` action is overloaded.** It covers issue edits, branch setup, staging, pushing, and PR creation. At `(from, to, action)` granularity, two unrelated delegations a minute apart are indistinguishable from a genuine repeat — the 09:30 and 10:01 alerts.

### The scoping contrast

The relay-suppression guard (`CountRecentRequestTuple`, wired in `cmd/send.go`) is deliberately scoped to **non-edit senders** because edit-to-agent traffic is expected to be chatty. `DetectMessageLoop` has no such scoping — it applies loop-storm thresholds to the one direction where bursts are routine.

### Why it matters

A detector that is almost always wrong trains the operator to dismiss it. The genuine failure modes it exists for are real and have happened — response-echo chain retriggers ([`MUX-009`](./MUX-009-response-echo-chain-retrigger.md)), task-redrive re-execution storms ([`MUX-007`](../completed/MUX-007-verify-spec-stale-review-refire.md)) — and the next one will arrive wrapped in the same alert text as three false positives.

## Requirements

### Evaluation of candidate approaches

| Approach | Verdict | Reasoning |
|----------|---------|-----------|
| Exempt correlated request/response pairs from ping-pong | **Adopt** | A response correlated to a live request (`ReplyTo` set, or response-type answering the prior request) is the delivery-ack model's definition of health; counting it as a pong contradicts `hasReceipt()`. True echo storms re-inject payloads as *new requests* (MUX-009) or fire uncorrelated/duplicate responses — both still counted |
| Compare content (normalized hash), not only the tuple | **Adopt** | Genuine storms repeat the same payload (re-drives, echoes); healthy consecutive delegations differ. Requiring `>= threshold` *identical-content* requests per tuple kills the overloaded-`commit` false positive without touching senders. Caveat: normalize before hashing (trim, collapse whitespace) so trivially-varying storms still match |
| Sub-type actions (`commit:stage`, `commit:push`, …) | **Reject as primary** | Requires classifying intent at every sender, fights the "delegate intent, not artifacts" hygiene rule, and drifts as payload wording changes. Content hashing achieves the discrimination without a new taxonomy. Optional later as advisory metadata |
| Scope/raise threshold for the edit direction | **Reject as primary** | Mirrors relay-suppression's scoping but is blunt: the observed real storm (~21h of duplicate review echoes) was on the edit path. Direction-aware thresholds may ship as a tuning knob, never as the fix |

### Acceptance criteria

- [ ] The three observed 2026-08-24 sequences, replayed as message fixtures, produce **no** alert: (a) two unrelated same-action delegations each answered first try, (b) two distinct `pr-read` requests with replies, (c) retitle-then-commit pair with replies
- [ ] A response correlated to a preceding request (matching `ReplyTo`, or the immediate response-type answer on the same tuple) never counts toward ping-pong
- [ ] The repeated-tuple pass fires only when the *normalized content* of `>= threshold` requests on a tuple matches — distinct-content requests on a shared tuple stay silent
- [ ] Negative control: a genuine repeated-identical relay storm (same payload re-sent `threshold` times in the window) still alerts, at unchanged threshold and window defaults
- [ ] Negative control: a bidirectional echo loop of uncorrelated responses (mutual acknowledgement pattern) still alerts via ping-pong
- [ ] `CountRecentRequestTuple` relay suppression is untouched — source-side suppression and daemon-side detection stay independent
- [ ] Existing `bus/guard_test.go` tests pass; new table-driven tests cover the fixture taxonomy (healthy pairs, distinct-content repeats, identical storms, response echoes)
- [ ] `checkLoops` daemon wiring and alert formatting unchanged — only detection semantics move

### Key files

| File | Change |
|------|--------|
| `bus/guard.go` | `DetectMessageLoop`: correlated-reply exemption in the ping-pong pass; normalized-content repeat requirement in the tuple pass |
| `bus/guard_test.go` | Fixture taxonomy: the three real incidents as regression fixtures + storm/echo negative controls |
| `bus/message.go` | Read-only reference — `ReplyTo` correlation field |
| `docs/architecture.md` | Loop-detection paragraph updated with the correlation/content semantics |

## Implementation

### Phase 1: Fixture taxonomy

- [ ] Encode the three 2026-08-24 incidents as message-slice fixtures in `bus/guard_test.go`, asserting the **current** detector fires on them (pinning the bug)
- [ ] Add storm fixtures: repeated-identical relay, uncorrelated response echo — asserting the current detector fires (behavior to preserve)

### Phase 2: Correlated-reply exemption

- [ ] Ping-pong pass skips responses correlated to a counted request (`ReplyTo` match, or immediate same-tuple response answer)
- [ ] Flip the Phase 1 healthy-pair fixtures to assert silence; storm fixtures still fire
- [ ] Unit tests: reply-without-`ReplyTo` fallback correlation, duplicate responses to one request still count

### Phase 3: Content-aware tuple repeats

- [ ] Normalized content hash (trim, collapse internal whitespace) per request; tuple pass requires `>= threshold` matching hashes
- [ ] Flip the distinct-content fixtures to assert silence; identical-storm fixture still fires
- [ ] Unit tests: near-identical payloads with whitespace drift still match; genuinely distinct payloads never do

### Phase 4: Docs

- [ ] `docs/architecture.md` loop-detection wording: correlation exemption + content requirement, contrast with relay suppression retained

### Phase 5: Integration test

- [ ] Create `scripts/test-loop-detector.sh` (hermetic — scratch `BUS_SESSION`, writes message-log fixtures, no live session needed)
- [ ] Test: healthy-traffic fixture set (the three real incidents) → detector reports no loop
- [ ] Test: repeated-identical relay-storm fixture → detector alerts (negative control — a fix that silences everything is caught)
- [ ] Test: uncorrelated response-echo fixture → detector alerts
- [ ] Run the script and verify all checks pass

## Sources

- `tools/muxcode/bus/guard.go` — `DetectMessageLoop`, `CountRecentRequestTuple`
- `tools/muxcode/bus/delivery.go` — `hasReceipt()` reply-implies-receipt semantics
- [`MUX-009-response-echo-chain-retrigger.md`](./MUX-009-response-echo-chain-retrigger.md), [`MUX-007-verify-spec-stale-review-refire.md`](../completed/MUX-007-verify-spec-stale-review-refire.md) — the genuine storm shapes the detector must keep catching

## Provenance

Filed by the plan agent on 2026-08-24 from an edit-relayed, user-approved brief documenting three same-day false positives. The MUX-032 id was freed by renumbering the delivered agent-mode spec to MUX-102.

## Status

Backlog
