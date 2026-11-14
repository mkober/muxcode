# MUX-189: Batch Delivery Correlates Only the Last Request's Reply

**Tracking:** [mkober/muxcode#143](https://github.com/mkober/muxcode/issues/143)

On the Codex and OpenCode injection roads a batch of inbox messages is rendered into one payload,
and the reply instruction at the end of it names **one** request id — the last request in the batch.
An agent that receives requests A and B together is told to reply to B; A's task never sees a
`--reply-to` naming it, and A times out at 600 s (`task-timeout`) although the agent answered both
in prose. Noted 2026-09-18 from a timed-out task whose answer was visible in the pane; filed now as
one of the carried-over defects.

## Context

### Source and standard of evidence

Mechanism verified by plan in the tree on 2026-09-24 (line numbers below). The 2026-09-18 timeout is
from the session notes and has **not** been re-verified against a log; treat the incident as
reported, the mechanism as confirmed. Filed on the user's instruction relayed by edit.

### Mechanism — verified in code

| Step | Code | Behaviour |
|---|---|---|
| The payload builder tracks one request | `bus/provider_codex.go:597` and `bus/provider_opencode.go:173`: `var lastFrom, lastRequestID, lastRequestFrom string` | Scalars, not a list |
| Each request overwrites it | `provider_codex.go:614`, `provider_opencode.go:188`: `lastRequestID, lastRequestFrom = msg.ID, msg.From` inside the message loop | Only the final request survives |
| One reply command is appended | `provider_codex.go:641`, `provider_opencode.go:212`: `buildReplyCommand(replyTarget, lastRequestID)` | The agent is instructed to `--reply-to` B only |
| Receipts are per message, tasks are per request | `bus/delivery.go`, `bus/task.go` | Both A and B are `delivered`; only B becomes `responded`; A expires at `task-timeout` and, on a graph node, parks it `timed-out` |
| Claude is unaffected | `muxcode inbox --poll --loop` returns each message with its own reply line | The listener road prints one instruction per message |

### Blast radius

- Any listenerless provider (Codex scrape road, OpenCode) receiving two requests in one delivery —
  common when the daemon re-drives a backlog after a restart or when a chain and a user request land
  together.
- A graph node whose dispatch is the *earlier* request in a batch parks on `timed-out` with the
  answer on screen — the "names the clock instead of the cause" failure MUX-182 documented for
  another road.
- The Codex hook road (`hook stop` / `prompt-submit`) delivers one pending request per prompt and
  is believed unaffected; Phase 1 confirms.

### Family

- [MUX-154](./MUX-154-codex-status-line-closes-tracked-tasks.md) — the same road's task-closing false positive; this is its false negative.
- [MUX-145](../backlog/MUX-145-messages-routed-to-windowless-role.md), [MUX-009](./MUX-009-response-echo-chain-retrigger.md) — the reply-routing cluster.

## Requirements

### Acceptance criteria

- [x] A batched payload ends with one reply instruction **per request** in the batch, each naming its own id and sender, on both the Codex scrape road and OpenCode — unit test with two requests from different senders asserts two `--reply-to` lines. Deviation: each names its reply target (`ReplyTarget`), which is the sender unless the request carries a reply role
- [x] A batch of one request renders exactly as today (positive control) — the exact pre-fix IMPORTANT/REMINDER form is asserted
- [x] Event and response messages in the batch still get no reply instruction — asserted for an event row; a response row is covered by the same request-only filter but has no test of its own
- [x] The Codex hook road is confirmed to deliver one request per prompt, or fixed the same way — recorded in Phase 1. Met in intent, not in letter: it delivers the whole inbox, and needs no fix because every request already carries its own reply line (`TestConsumeInboxForHook_EveryRequestCarriesItsOwnReply`)
- [x] The agent definitions for listenerless providers say "reply to each request id listed", so the instruction and the payload agree — `bus/prompt.go`, the Codex AGENTS reply protocol and `agents/command-runner.md`, each saying to run the listed command as written
- [x] `bash scripts/test-batch-reply-correlation.sh` passes — 23 passed, 0 failed, floor 23

### Technical approach

Collect `(id, from)` pairs in a slice during the loop and emit `buildReplyCommand` once per pair,
in delivery order, in a "Reply to each:" block. Keep `replyTarget` normalization per request (the
`daemon` → `edit` mapping) so a mixed batch routes each reply correctly.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/bus/provider_codex.go:590-650` | Codex scrape-road payload builder |
| `tools/muxcode/bus/provider_opencode.go:165-220` | OpenCode payload builder |
| `tools/muxcode/bus/provider.go` (`buildReplyCommand`) | shared reply-line renderer |
| `tools/muxcode/bus/hook_codex.go` | the hook road — confirm one-per-prompt |
| `agents/*.md` (listenerless dialect) | reply instruction wording |

## Implementation

### Phase 1: Establish the boundary

- [x] Failing unit test: two requests → one reply line (the negative control, kept) — `TestSendWakeUp_BatchReplyBoundary`; deviation: it pins the defect green rather than failing, and Phase 2 inverts it
- [x] Confirm the Codex hook road's per-prompt delivery count; record here — the whole inbox per prompt, each message with its own reply line (findings below)

### Findings for Phase 1 (2026-11-14)

Reported by the phase worker and verified the same day after this run's build (exit 0), `./test.sh` (exit 0, 2 of 2 modules, test-history row 1794666371) and review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794666374.txt`).

| Finding | Detail |
|---------|--------|
| The Codex hook road is **not** one request per prompt | `ConsumeInboxForHook` (`bus/hook_codex.go`) hands `hook stop` / `prompt-submit` the whole inbox. The "Blast radius" belief above ("delivers one pending request per prompt") is wrong |
| It is unaffected all the same, and needs no fix | Each message is rendered by `FormatMessage` with its own `To reply … --reply-to <id>` line addressed to `ReplyTarget(m)`, and the preamble says "one reply per request". Pinned by `TestConsumeInboxForHook_EveryRequestCarriesItsOwnReply` in the new `bus/batch_reply_test.go` |
| The boundary test | `TestSendWakeUp_BatchReplyBoundary`, on OpenCode and scrape-road Codex: two requests (from plan and edit) yield a reply line for the last only, the first id is named zero times, and a single request is the positive control |
| Deviation — the test passes, it does not fail | The step asks for a failing unit test. This one **pins the defect green**, because a red test would fail the graph's per-phase test node. Phase 2 inverts the first-request assertion, at which point it becomes the fix's regression test |
| No integration script | None in this phase; that is the integration-test phase's work |

### Phase 2: Fix

- [x] Slice of `(id, from)`; one reply line per request on both roads; positive control for a single request; no line for event/response rows — `withReplyInstructions`, `TestSendWakeUp_EveryRequestInABatchGetsItsReply`
- [x] Definition wording updated; `docs/agents.md` listenerless-provider note — after the review's should-fix was resolved (second round below)

### Results for Phase 2 (2026-11-14, recorded before verification)

Reported by the phase worker; the boxes above stay open until this run's build, test and review pass.

| Item | Result |
|------|--------|
| Fix | `withReplyInstructions` (`bus/provider.go`) takes the batch's requests and emits one reply command per request, each to its own `ReplyTarget` (an unknown target falls back to edit), before and after the payload. Several requests render as "This message holds N requests" plus a numbered list; a single request renders as before; events and responses get none |
| Roads | OpenCode and scrape-road Codex. Claude's listener and the Codex hook road were already per-message (Phase 1) |
| Definition wording | `bus/prompt.go` (the shared rule: reply to each request id listed), the AGENTS instructions in `bus/provider_codex.go`, and `agents/command-runner.md` |
| Docs | `docs/agents.md`, "Message delivery and receipts" — the "One reply command per request" note, added by plan |
| Deviation from the technical approach | The approach names a "Reply to each:" block; the shipped wording is a numbered list under "This message holds N requests" |
| Tests | `TestSendWakeUp_EveryRequestInABatchGetsItsReply` (the Phase 1 pin, inverted and renamed) on both providers, with the single-request control and an event negative control |

**First verification round — not ticked.** Build (exit 0) and `./test.sh` (exit 0, 2 of 2 modules, row 1794666660) passed; review returned 0 must-fix, **1 should-fix** (`/tmp/muxcode-review-1794666665.txt`):

- The renderer is right — `withReplyInstructions` addresses each reply to `ReplyTarget`, which can differ from the sender (a test-chain review request from test carries `ReplyRole` edit).
- The new definition wording is not: the AGENTS instructions written by `writeCodexAgentConfig` (`bus/provider_codex.go`) and `agents/command-runner.md` say each reply goes to its **sender**. That contradicts the payload, and the acceptance criterion asks that "the instruction and the payload agree".

Both Phase 2 steps stay open until the wording names the listed reply command rather than the sender and a review passes clean.

**Wording fix (reported by the fix node, recorded before verification).** The AGENTS reply protocol in `bus/provider_codex.go` and `agents/command-runner.md` now say to execute each listed reply command as written, keeping its target and `--reply-to` id, and no longer name the sender. `docs/agents.md` never said "sender"; plan added the same "as written" sentence to its note.

**Second verification round — ticked.** Build (exit 0), `./test.sh` (exit 0, 2 of 2 modules, row 1794666838) and review (0 must-fix, 0 should-fix, prior finding resolved, `/tmp/muxcode-review-1794666842.txt`) all ran after the wording fix.

### Phase 3: Integration test

- [x] Create `scripts/test-batch-reply-correlation.sh` — hermetic: scratch bus dir, two seeded requests, `muxcode deliver` payload captured with a `❯` stand-in pane — an OpenCode-shaped composer instead (deviation below); listed in `CLAUDE.md`
- [x] Test: captured payload contains both ids in reply lines; replying to each closes each task (`muxcode tasks` shows both `responded`) — section A; closed by the scratch daemon's `checkTrackedTasks` (deviation below)
- [x] **Negative control:** a single request → exactly one reply line — section B, plus the pre-fix control in section C
- [x] Coverage floor; run and record counts here — floor 23, 23 passed, 0 failed

### Results for the integration phase (2026-11-14)

Reported by the phase worker and verified the same day after this run's build (exit 0), `./test.sh` (exit 0, 2 of 2 modules, row 1794667232) and review (0 must-fix, 0 should-fix, `/tmp/muxcode-review-1794667235.txt`).

| Item | Result |
|------|--------|
| Script | New `scripts/test-batch-reply-correlation.sh` — hermetic: scratch bus, private tmux server, `muxcode` built from the tree. Two requests are queued with `--no-notify` and delivered as one batch by `muxcode deliver build --force` to an OpenCode-role pane running a fake agent that logs what is typed and executes every listed reply command as written |
| Run | 23 passed, 0 failed, floor 23 — run agent, task `1794667120-spawn-0616aad2-6de68afa`, confirmed in `run-history.jsonl` |
| A | Requests from edit and plan in one batch: one typed payload naming both ids, each reply command to its own target, before and after the payload; both tasks complete and both requests read responded |
| B | Negative control — a single request keeps the old one-command form |
| C (added) | Pre-fix control — the tree with `withReplyInstructions` cut to the batch's last request in a scratch copy leaves the first task in flight |

Deviations from the phase's wording:

- **Tasks are completed by the daemon, not by the reply.** `checkTrackedTasks` closes a tracked task, so the script starts a scratch daemon only once the batch is answered. "`muxcode tasks` shows both `responded`" is checked as task status `completed` plus delivery status `responded`.
- **The pane is an OpenCode-shaped composer, not a `❯` stand-in,** since OpenCode is one of the two affected roads.
- **One road end to end.** The script drives OpenCode only; scrape-road Codex is covered by the unit test (`TestSendWakeUp_EveryRequestInABatchGetsItsReply`), not by this script.

## Out of scope

- Claude's listener road — already per-message.
- The 600 s task timeout itself.

## Status

Complete — closed 2026-11-14 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e`. 14/14: `withReplyInstructions` (`bus/provider.go`) gives every request in a typed batch its own reply command on OpenCode and scrape-road Codex; the definitions say to run each as written; `scripts/test-batch-reply-correlation.sh` passes 23/23. Limits: the script drives OpenCode only, and a response row's "no reply line" has no test of its own.

Activated the same day from Defects rank 13 (ranks 1–12 were parked). At activation the mechanism is still in the tree, at moved lines: `provider_codex.go:879-925` and `provider_opencode.go:171-212`, now tracking `lastRequestTarget` via `ReplyTarget(msg)` (MUX-216) rather than the sender.

Filed 2026-09-24 on the user's instruction relayed by edit, from a 2026-09-18 note (a timed-out task
answered in prose). Mechanism verified at `provider_codex.go:597-641` and `provider_opencode.go:173-212`;
the incident is reported, not re-verified.
