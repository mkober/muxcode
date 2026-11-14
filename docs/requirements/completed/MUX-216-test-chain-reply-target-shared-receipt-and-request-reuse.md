# MUX-216: Three Gaps Left by MUX-213 — Reply Target Ignored Off the Hook Road, Auto-CC Copies Share a Receipt, One Request Authorizes Many Reviews

**Tracking:** [mkober/muxcode#193](https://github.com/mkober/muxcode/issues/193)

Copilot's review of merged PR #191 ([MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md))
left three comments on `tools/muxcode/bus/chain_request.go` that the merge did not address. Each is
a road on which the behaviour MUX-213 fixed can still occur.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's request relayed by edit (`/tmp/defects-216-217.md`). The three
findings are Copilot's, quoted by comment id. Plan read `bus/chain_request.go`, `bus/delivery.go`,
`bus/message.go` and `bus/inbox.go` at `0e6df9b` plus the working tree; what plan confirmed in the
code is marked, the rest is Copilot's reading. None of the three has been observed failing in a
session, and nothing was run for this spec.

### The three findings

| # | Comment | Line | Finding | Plan's check |
|---|---------|------|---------|--------------|
| A | 4570487974 | `:34` | OpenCode, hookless (scrape-road) Codex and the `muxcode agent` local loop build a reply target from `From` and ignore `Message.ReplyRole`, so on those roads a test-chain review still answers test, not edit | Partly confirmed: outside tests, `ReplyRole` is read only in `bus/message.go` (`FormatMessage`'s `To reply` line) and the LLM harness (`harness/message.go`, `loop.go:567`). Which other roads build their own reply instruction was not traced road by road |
| B | 4570488015 | `:65` | An auto-CC copy keeps the original message's id. Consuming the copy calls `WriteReceipt`, which overwrites `AckedBy`; `OpenRequestFor` then finds the receipt acked by edit, not test, and a genuine request stands behind no run — its test→review is withheld | Confirmed in code: `WriteReceipt` (`bus/delivery.go`) sets `ds.AckedBy = ackedBy` unconditionally, keyed by message id; `OpenRequestFor` skips a status whose `AckedBy` window is not the role's. That an auto-CC copy carries the same id is Copilot's reading, consistent with `deliverMessage` writing the same line |
| C | 4570488050 | `:181` | `TakeTestRunOrigin` discards the captured request id, so one unanswered request authorizes every test run started while it stays open — several test→review dispatches for one request | Confirmed in code: `testRunRecord.Request` is written by `RecordRunProvenance` and never read; `TakeTestRunOrigin` returns only `Requested` |

### Consequences if left

- **A** — on a non-hook provider the 2026-11-13 loop (a review reply waking the test agent, and
  edit receiving each review twice) is not closed. `codexTestNoRequestPreamble` covers only the
  Codex hook road.
- **B** — a requested suite run loses its review whenever edit consumes its CC copy before the test
  command starts. Fails closed (a withheld review, a `chain-withheld` row), but silently for the
  requester.
- **C** — an agent that reruns the suite before replying fires a second review for the same
  request. MUX-213's script holds this at one review only because its stub replies between runs.

## Requirements

### Acceptance criteria

- [x] A test-chain review request is answered to edit, not to test, on every road that renders a
  reply instruction: OpenCode, scrape-road Codex, the `muxcode agent` loop and the LLM harness —
  each road named and tested, or shown not to render one — the roads are named in Phase 1's table
  and tested by unit tests (Phase 2), not end to end
- [x] A receipt written for an auto-CC copy never changes who received the original request:
  `OpenRequestFor` still returns the request for the role it was addressed to after edit consumes
  its copy — unit test and script section G
- [x] One request stands behind at most one test→review dispatch: a second test run started while
  the same request is still open logs `chain-withheld` naming that request — script section H
- [x] Negative controls: a hook-road Claude test agent's requested run still fires exactly one
  review naming edit; a second, distinct request still fires its own review; a failing run still
  notifies — the script's existing sections and section H
- [x] `bash scripts/test-unrequested-test-cycles.sh` covers B and C and its floor is raised — 40 → 55
- [x] `CLAUDE.md`'s hook-driven chains entry describes the per-request limit and the receipt rule

### Technical approach

- **A** — route every reply-instruction builder through `Message.ReplyTarget()` rather than `From`.
  First enumerate them; a road with no reply instruction of its own needs a test, not a change.
- **B** — either give an auto-CC copy its own id (recording the original in a field), or record
  receipts per recipient so a CC's consume cannot overwrite the addressee's. Decide in Phase 1 from
  what else keys on a CC sharing the id (`hasReceipt`, the dedup comment at `bus/inbox.go:631`).
- **C** — keep the request id in `TestRunOrigin`, and mark it spent when its review is dispatched;
  a later run naming a spent request is withheld.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/chain_request.go` | `NewChainRequest`, `OpenRequestFor`, `RecordRunProvenance`, `TakeTestRunOrigin` |
| `tools/muxcode/bus/message.go` | `ReplyRole`, `ReplyTarget`, `FormatMessage` |
| `tools/muxcode/bus/delivery.go` | `WriteReceipt`, `AckedBy` |
| `tools/muxcode/bus/inbox.go` | `deliverMessage` and the auto-CC copy |
| `tools/muxcode/bus/provider_opencode.go`, `provider_codex.go`, `cmd/agent*.go` | Reply instructions on the non-hook roads |
| `scripts/test-unrequested-test-cycles.sh` | MUX-213's script, extended here |

## Implementation

### Phase 1: Pin

- [x] Enumerate every place a reply instruction or reply send is built from a message; record the
  list here with which honour `ReplyRole`
- [x] Unit test: a request acked by test, then its CC copy consumed by edit → `OpenRequestFor`
  returns no request today (pin red)
- [x] Unit test: two test runs started under one open request → both report `Requested` today (pin
  red)
- [x] Record the choice for B (own id for the copy, or per-recipient receipts) and why

### Findings for Phase 1

Recorded 2026-11-13 from the run worker's handoff (run `1794626267`, node `implement`). The trace is
the worker's, at `0e6df9b` plus the working tree; plan confirmed the two pin tests exist in
`tools/muxcode/bus/chain_request_test.go` and did not re-trace the roads. Uncommitted. The run's
build, `./test.sh` (both modules passed) and review (no findings; it checked the pins and the three
`From`-built roads) then passed, and the phase was ticked.

**Reply-instruction and reply-send builders**

| Road | Where | Built from | Honours `ReplyRole` |
|------|-------|-----------|---------------------|
| `FormatMessage` "To reply" line | `bus/message.go:113` | `ReplyTarget(m)` | Yes |
| Claude listener, `muxcode inbox` | `cmd/inbox.go:272` → `FormatMessage` | `ReplyTarget` | Yes |
| Codex hook road (prompt-submit, stop) | `bus/hook_codex.go:290` → `FormatMessage`; the wake text at `:246` defers to the "To reply" line | `ReplyTarget` | Yes |
| `send --wait` result, `spawn`, edit's response collector | `cmd/send.go:353,381,651`, `cmd/spawn.go:199`, `bus/hook.go:2069` → `FormatMessage` | `ReplyTarget` | Yes |
| LLM harness | `harness/loop.go:564,567` → `Message.ReplyTarget()` | `ReplyRole`, else `From` — not normalized, so a `daemon` sender is not mapped to edit as on the bus | Yes |
| **OpenCode** pane injection | `bus/provider_opencode.go:191-205` → `buildReplyCommand` | `lastRequestFrom` = `m.From` | **No** |
| **Scrape-road Codex** pane injection | `bus/provider_codex.go:904-918` → `buildReplyCommand` | `lastRequestFrom` = `msg.From` | **No** |
| **`muxcode agent` local loop** — a reply send, not an instruction | `bus/agent.go:226`, `NewMessage(…, lastMsg.From, "response", …)` | `lastMsg.From` | **No** |
| Static prompt text | `bus/prompt.go:117,124-166`, `bus/provider_codex.go:1197` | Defers to the "To reply" line, or hardcodes `send edit`; no message-derived target | n/a |

Finding A holds on three roads: OpenCode, scrape-road Codex and the `muxcode agent` loop. Phase 2
routes all three through `ReplyTarget`. The key-files row naming `cmd/agent*.go` is superseded by
`bus/agent.go`.

**Pins** — both assert today's defect, so they pass now; Phase 2 inverts them.

- `TestMUX216Pin_CCConsumeOverwritesAddresseeReceipt` (B): a build→test `Send` with auto-CC on;
  test consumes, and `OpenRequestFor(test)` returns the request (positive control); edit then
  consumes its CC copy, the receipt's `AckedBy` becomes `edit`, and `OpenRequestFor(test)` returns
  none.
- `TestMUX216Pin_OneRequestCreditsEveryRun` (C): one received request, two `RecordRunProvenance`
  test runs, and `TakeTestRunOrigin` credits both.

**Decision for B: per-recipient receipts, keeping the shared id.** A consume by a role that is not
the message's addressee (a window mismatch, as `OpenRequestFor` compares) writes no receipt and
never overwrites one. The CC copy keeps the original id.

- Why not a separate id for the copy: `--reply-to` correlation, `MarkResponded`, `FindMessageByID`
  (which scans the log, where a copy's id would not appear) and `ConsumeByID`'s id-keyed drain
  (the `bus/inbox.go:631` comment, pinned by `TestReply_LeavesAutoCCCopyInEditInbox`) all assume one
  id per request. A second id needs a new field and a change on each of those roads.
- A receipt is delivery evidence for the addressee, and an observer's consume is not. The shared
  receipt has a second symptom beyond B: if edit consumes the CC copy before the addressee does, the
  request reads as acked, so `hasReceipt` and the receipt-gap check hide a real delivery gap. The
  addressee-only rule fixes both.

### Phase 2: Fix

- [x] Every reply-instruction builder uses the message's reply target
- [x] An auto-CC copy's receipt leaves the addressee's receipt intact
- [x] A request is spent by its first review dispatch; a later run under it is withheld and logged
- [x] Invert the Phase 1 pins; the negative controls stay green

### Findings and changes for Phase 2

Recorded 2026-11-13 from the run worker's handoff (run `1794626267`, node `implement`). Uncommitted.
The run's build, `./test.sh` (exit 0, both modules passed) and review (no findings) passed on it,
and the phase was ticked. The evidence is unit-level; the review notes that the real hook
dispatch and withholding are still Phase 3's to show, so no acceptance criterion is ticked here.

| Finding | Change | Tests |
|---------|--------|-------|
| A — reply target | `bus/provider_opencode.go` and `bus/provider_codex.go` (scrape road) take the batch's reply target from `ReplyTarget(m)` of the last request, not `m.From`; `bus/agent.go` replies to `ReplyTarget(lastMsg)`. Side effect: a `daemon` sender's reply from the `muxcode agent` loop now goes to edit, as `FormatMessage` already instructs — graph replies correlate by `ReplyTo`, so the recipient does not matter | `TestSendWakeUp_ReplyInstructionNamesReplyTarget` (OpenCode and scrape Codex on a stubbed pane: a test-chain review types `muxcode send edit response … --reply-to <id>`; control: a plain build request types `send build`); `TestProcessMessages_RepliesToReplyTarget` (agent loop over `newPipeServer`: answered to edit, nothing in test's inbox; control: answered to build) |
| B — receipts | `writeConsumeReceipt` (`bus/delivery.go`): a consume writes a receipt only when the consumer is the message's addressee (`WindowForRole(m.To) == WindowForRole(role)`). Used by `Receive`, `receiveMatching` and `ConsumeByID`. The shared id stays | `TestOpenRequestFor_CCConsumeLeavesAddresseeReceipt` (the Phase 1 pin inverted): `AckedBy` stays test after edit consumes its copy; edit consuming first leaves the request unreceipted |
| C — one review per request | `TestRunOrigin.Request` carries the id. `ClaimRequestReview` (`bus/chain_request.go`) writes an `O_EXCL` marker under `spent-requests/` in the bus directory, swept after `openRequestMaxAge`, failing open; `ReleaseRequestReview` undoes it; `LogChainRequestSpent` writes `chain-withheld` "request <id> already dispatched its review". `OpenRequestFor` prefers an unspent open request and falls back to a spent one, so a distinct request gets its own review while a rerun is named. `triggerChain` (`cmd/hook.go`) claims before a test-success dispatch, logs and returns on a lost claim, and releases the claim when the dispatch is not sent. Failure outcomes never claim and still notify | `TestClaimRequestReview_OneReviewPerRequest` (the Phase 1 pin inverted; covers release, the log row and the distinct-request control) |

A change in behaviour wider than this spec's three findings: a non-addressee consume no longer
writes any receipt, for every message, not only test-chain requests.

### Phase 3: Integration test

- [x] Extend `scripts/test-unrequested-test-cycles.sh`: edit consuming the CC copy before the run
  starts still yields one review; two runs under one request yield one review and one
  `chain-withheld`; raise the floor
- [x] Negative control: with the spent-request check removed the second review fires — proving the
  check can see it
- [x] Run through the run agent and record the counts here — 55 of 55, floor 55
- [x] Update `CLAUDE.md` and `docs/hooks.md`

### Results for Phase 3

Recorded 2026-11-13 from the run worker's handoff (run `1794626267`); plan confirmed the result line
and exit code in the run agent's history and the floor in the script. Uncommitted.

| Section | What it shows |
|---------|---------------|
| G | Edit consumes the auto-CC copy after test read the request: the receipt stays test's, and the run yields one review |
| H | Two runs under one request: one review, and one `chain-withheld` naming the request. A second, distinct request (sent with `--force`) gets its own review |
| H-neg | Negative control: with the spent marker removed, the second review fires |

`bash scripts/test-unrequested-test-cycles.sh` through the run agent (task
`1794627605-spawn-0616aad2-c9efffb1`): **52 passed, 0 failed, floor 52** (raised from 40), exit 0.
The live section was skipped — no real `codex` was run.

**After review.** The first review of this phase returned one should-fix: every case posted a
passing run, so the checks could not tell whether the spent-request guard had wrongly been applied
to failures. Section H gained a failure control — after both open requests have spent their review,
a `./test.sh` exiting 3 is fed through the real hook as a `PostToolUseFailure`: edit gets one
"Tests FAILED" notice, the review count is unchanged and no further spent-request row is logged.
Rerun through the run agent (process `1794627829-proc-48b8dfff`): **55 passed, 0 failed, floor 55**,
exit 0, live skipped. The run's `./test.sh` passed and the re-review returned no findings.

`CLAUDE.md` was updated by edit and `docs/hooks.md` by plan (the "Three rules that close what
MUX-213 left open" table).

Not shown by the script: the reply target on OpenCode, scrape-road Codex and the `muxcode agent`
loop is covered by unit tests only (Phase 2), not end to end.

## Related

- [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) —
  the fix these comments were left on
- [MUX-189](../backlog/MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) — another reply
  correlated to the wrong message
- [MUX-201](../backlog/MUX-201-non-hook-prompt-and-generated-body-drift.md) — non-hook prompt text drifting
  from the hook road

## Status

Complete — closed 2026-11-13 by `55-defect-backlog-loop` run `1794626267`. The three gaps Copilot
found on PR #191 are fixed: OpenCode, scrape-road Codex and the `muxcode agent` loop reply to the
message's reply target; a receipt is written only by the addressee's consume; and one request
stands behind at most one test→review dispatch.

Activated 2026-11-13 by the same run (Defects rank 1). Phase 1 (pin) is complete and reviewed: finding A is confirmed on three roads, both pins are in the
tree, and B is decided as addressee-only receipts. Phase 2 (fix) is complete and reviewed: all three
findings are fixed with unit tests and both pins are inverted. Phase 3 (integration test) is complete
and reviewed: `scripts/test-unrequested-test-cycles.sh` passes 55 of 55 through the real hook road,
including a failure control. All 18 boxes are ticked. Limits: the
reply target on OpenCode, scrape-road Codex and the `muxcode agent` loop is unit-tested only, and
the script's live `codex` section was not run.

Filed 2026-11-13 on the user's request relayed by edit, ranked first and eligible for
`55-defect-backlog-loop`. Findings B and C are confirmed in code; A is partly confirmed. None has
been observed failing. The three PR #191 review threads are to be answered with a pointer to this
spec's issue — the commit agent's work, on the user's request.
