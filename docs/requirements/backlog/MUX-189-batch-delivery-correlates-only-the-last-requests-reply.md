# MUX-189: Batch Delivery Correlates Only the Last Request's Reply

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
- [MUX-145](./MUX-145-messages-routed-to-windowless-role.md), [MUX-009](./MUX-009-response-echo-chain-retrigger.md) — the reply-routing cluster.

## Requirements

### Acceptance criteria

- [ ] A batched payload ends with one reply instruction **per request** in the batch, each naming its own id and sender, on both the Codex scrape road and OpenCode — unit test with two requests from different senders asserts two `--reply-to` lines
- [ ] A batch of one request renders exactly as today (positive control)
- [ ] Event and response messages in the batch still get no reply instruction
- [ ] The Codex hook road is confirmed to deliver one request per prompt, or fixed the same way — recorded in Phase 1
- [ ] The agent definitions for listenerless providers say "reply to each request id listed", so the instruction and the payload agree
- [ ] `bash scripts/test-batch-reply-correlation.sh` passes

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

- [ ] Failing unit test: two requests → one reply line (the negative control, kept)
- [ ] Confirm the Codex hook road's per-prompt delivery count; record here

### Phase 2: Fix

- [ ] Slice of `(id, from)`; one reply line per request on both roads; positive control for a single request; no line for event/response rows
- [ ] Definition wording updated; `docs/agents.md` listenerless-provider note

### Phase 3: Integration test

- [ ] Create `scripts/test-batch-reply-correlation.sh` — hermetic: scratch bus dir, two seeded requests, `muxcode deliver` payload captured with a `❯` stand-in pane
- [ ] Test: captured payload contains both ids in reply lines; replying to each closes each task (`muxcode tasks` shows both `responded`)
- [ ] **Negative control:** a single request → exactly one reply line
- [ ] Coverage floor; run and record counts here

## Out of scope

- Claude's listener road — already per-message.
- The 600 s task timeout itself.

## Status

Backlog

Filed 2026-09-24 on the user's instruction relayed by edit, from a 2026-09-18 note (a timed-out task
answered in prose). Mechanism verified at `provider_codex.go:597-641` and `provider_opencode.go:173-212`;
the incident is reported, not re-verified. Not started.
