# MUX-220: The Wake Batch Drops a Listenerless Role's Own Delegation Reply, and May Re-Inject an Answered Request

**Tracking:** [mkober/muxcode#203](https://github.com/mkober/muxcode/issues/203)

A follow-up to [MUX-009](../completed/MUX-009-response-echo-chain-retrigger.md) (PR #198), from two
Copilot review threads read after the merge. MUX-009 stopped responses being typed into a
listenerless agent's pane by consuming them. It consumes **every** response a non-orchestrator role
holds — including the correlated reply to a request that role itself sent with `--wait` or
`--track`, which is deleted before the sender's poll can find it. Separately, the batch split never
asks whether a queued request has already been answered.

## Context

### Source and standard of evidence

| Thread | Location | Claim | Verified |
|--------|----------|-------|----------|
| 4573825335 | `bus/provider.go:340` | `settleWakeResponses` consumes every withheld response for a role that is not edit or auto; a reply correlated to that role's own delegation is dropped | **Yes**, by edit against main `82635f8` and by plan in the tree: the `receiveMatching` call matches on message id alone, with no reply-to or task check |
| 4573825311 | `bus/provider.go:310` | A request already answered (`responded`) but still queued can be re-injected into a listenerless pane | **Not confirmed.** `splitWakeBatch` keeps every non-response message and reads no status — that much is in the tree — but whether an answered request can still be queued when a wake is built has not been shown. `MarkResponded` drains the answered row via `ConsumeByID`, which may already close this |

### Mechanism — verified in code

- `bus/provider.go:305–314` `splitWakeBatch` — splits on `m.Type == "response"` only.
- `bus/provider.go:327–344` `settleWakeResponses` — for any base role other than `edit`/`auto`, builds an id set from the withheld responses and consumes them with `receiveMatching(session, role, "", …)` (no receipt), logging one `wake-response-dropped` row.
- Callers: `bus/provider_opencode.go:153` and `bus/provider_codex.go:861` — the OpenCode road and the scrape-road Codex road. Claude and hook-road Codex are not affected.
- A role's own `muxcode send --wait`/`--track` finds its answer by polling its inbox for the correlated reply; once the wake has consumed it, the poll degrades to a tracked task that never completes on that reply.

### Scope boundary

MUX-009's rule stands: an uncorrelated response is never typed into a pane and never re-fires a
chain. This spec narrows what is **consumed**, not what is typed.

## Requirements

### Acceptance criteria

- [ ] A response that answers a request the receiving role itself issued (its `ReplyTo` names a live task or request that role sent) is not consumed by `settleWakeResponses`; the role's `--wait` or `--track` receives it
- [ ] Such a response is still not typed into the pane as an instruction — MUX-009's `scripts/test-response-echo.sh` passes unchanged
- [ ] Uncorrelated responses are still consumed and logged `wake-response-dropped` (negative control)
- [ ] The answered-request claim is settled with evidence: either a reproduction of an answered request being re-injected, followed by a responded check on both provider roads and the daemon wake, or a recorded finding that the row cannot be queued once answered
- [ ] A mixed batch — one open request, one correlated reply, one uncorrelated response — is covered by a regression test

### Technical approach

In `settleWakeResponses`, partition the withheld responses before consuming: a response whose
`ReplyTo` matches a task the role holds as sender (`tasks/`) stays queued for the role's own poll;
the rest are consumed as today. The lifecycle row names both counts. For the answered-request
claim, read the consume and `MarkResponded` paths first and write down what they guarantee.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider.go` | `splitWakeBatch`, `settleWakeResponses` |
| `tools/muxcode/bus/provider_opencode.go`, `provider_codex.go` | The two listenerless wake roads |
| `tools/muxcode/bus/task.go`, `bus/delivery.go` | Sender-side task lookup; `MarkResponded` and `ConsumeByID` |
| `scripts/test-response-echo.sh` | MUX-009's script, to extend |

## Implementation

### Phase 1: A role's own reply survives the wake

- [ ] `settleWakeResponses` keeps a response correlated to a task the role sent; consumes the rest
- [ ] Unit test: a correlated reply stays queued and is found by the sender's poll; an uncorrelated one is consumed (negative control)
- [ ] `wake-response-dropped` names how many were dropped and how many kept

### Phase 2: The answered-request claim

- [ ] Read the consume and `MarkResponded` paths and record in this spec whether an answered request can remain queued at wake time
- [ ] If it can: add the responded check to both provider roads and the daemon wake, with a test. If it cannot: record the finding and the code that guarantees it

### Phase 3: Integration test

- [ ] Extend `scripts/test-response-echo.sh` (or add `scripts/test-wake-own-reply.sh`): an OpenCode-role fake agent sends a tracked request, the reply arrives while it is listenerless, a wake is built → the reply is still in its inbox and its task completes
- [ ] Control: an uncorrelated response in the same batch is dropped with a `wake-response-dropped` row and is never typed
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-11-14 by plan from edit's reading of PR #198's post-merge review threads, on the user's standing instruction to file defects found along the way.

## Status

**Backlog** — 0/13. Filed 2026-11-14.
