# MUX-225: A Reply Whose `--reply-to` Names No Message Is Accepted Silently, and the Task Waits Out Its Cap

**Tracking:** [mkober/muxcode#209](https://github.com/mkober/muxcode/issues/209)

`muxcode send --type response --reply-to <id>` does not check that `<id>` names a message. An agent
that mistypes the id gets "Sent", the reply correlates to nothing, and the request it meant to answer
stays open until its task expires. On 2026-11-14 a passing test run failed a graph node that way.

## Context

### Observed (2026-11-14, session `muxcode`, run `1794626267-55-defect-backlog-loop-15547e2e`)

Reported by edit; the rows below were re-read by plan in `log.jsonl` and the lifecycle log.

| Time | Event |
|------|-------|
| 14:18:10 | The daemon dispatches `request:test`, id `1794683890-daemon-30a89855`, for MUX-137's Phase 4 test node |
| 14:19:16 | The Codex test agent's `./test.sh` passes — test-history row, exit 0 |
| 14:19:20 | It replies with `--reply-to 1794683890-da0a89855` — six characters dropped from the id. Log row `1794683960-test-2f6fffdd`: `type` response, `action` `response`, sent to edit |
| 14:28:13 | `task-timeout daemon→test:test expired in-flight`, then `graph-node-done … test -> failure` |
| after | The run's fix worker diagnoses a timeout with no code defect and re-verifies — about 11 minutes lost |

### Mechanism — verified in code

- `cmd/send.go:45–51` — `--reply-to` is read into `replyTo` and never looked up.
- `bus/delivery.go:93–107` `MarkResponded` — reads the delivery status for the id and skips silently when there is none; `FindMessageByID` likewise finds nothing, so nothing is drained.
- `cmd/send.go:287–296` `responseAnswers` — a reply carrying a `ReplyTo` is matched on that id alone (`m.ReplyTo == msgID`); the unlinked fallback, which correlates by action, applies only when `ReplyTo` is empty. A wrong id is therefore worse than no id. Here the action was `response`, not the request's `test`, so the fallback would not have matched either.

### Decisions owed — the user's

| # | Decision | Options |
|---|----------|---------|
| 1 | What `send` does with an unknown `--reply-to` | (a) refuse, non-zero, naming the sender's open requests so the agent can retry; (b) accept, log and alert edit; (c) accept and fall back to the sender's single open request from that requester when exactly one exists, logging the repair. (a) relies on the agent retrying — a Codex agent that treats a failed send as final strands the reply entirely. (c) guesses, and a wrong guess closes the wrong task |
| 2 | Whether a close match is repaired | The observed id differs from a real open request by a dropped substring. Repairing "nearest id" is a second guess on top of Decision 1 |

Phase 1 needs neither decision; Phase 2 is Decision 1.

## Requirements

### Acceptance criteria

- [ ] An unknown `--reply-to` is never silent: it writes a `reply-to-unknown` lifecycle row naming the sender, the id given and the sender's open requests, and edit is told once
- [ ] A known `--reply-to` behaves exactly as today (negative control)
- [ ] A reply with no `--reply-to` behaves exactly as today (negative control)
- [ ] The behaviour chosen in Decision 1 is implemented, and on the 2026-11-14 timeline the test node does not wait out its 600 s
- [ ] The agent-facing text (`muxcode send` usage, the reply instructions providers inject) says what happens to an unknown id
- [ ] `docs/agent-bus.md` documents it

### Technical approach

In `send`, when `--type response` carries a `--reply-to`, resolve it (`FindMessageByID`, then the
task store) before sending. Unknown → the lifecycle row and the edit notice in every case; then the
branch Decision 1 selects. Open requests for the sender come from the receipts and task store
(`bus.OpenRequestFor` is the existing reader).

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/cmd/send.go` | `--reply-to` parsing, `responseAnswers` |
| `tools/muxcode/bus/delivery.go` | `MarkResponded`, `FindMessageByID` |
| `tools/muxcode/bus/task.go` | Open tasks for the fallback or the refusal message |
| `tools/muxcode/bus/provider.go` | `buildReplyCommand` — the instruction agents copy the id from |

## Implementation

### Phase 1: An unknown id is visible

- [ ] `send` resolves a response's `--reply-to`; unknown writes `reply-to-unknown` and notifies edit once
- [ ] Unit tests: unknown id → row and notice; known id and no id → neither (negative controls)

### Phase 2: The chosen behaviour

- [ ] Record Decisions 1 and 2 in this spec before any code
- [ ] Implement the chosen branch, with a test on the 2026-11-14 shape (an id with a dropped substring, action `response`)
- [ ] Negative control for the branch: two open requests from the same requester never resolve by guess

### Phase 3: Integration test

- [ ] Create `scripts/test-reply-to-unknown.sh`: a scratch daemon, a one-node graph `test` run, a stub agent replying with a mangled id → the row is written, edit is told, and the node ends as Decision 1 says, well inside 600 s
- [ ] Control: the same stub replying with the right id completes the node with no `reply-to-unknown` row
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-unmatched-reply.md` (edit), on the user's standing instruction to file defects found along the way.
- Parked at filing: Phase 2 holds Decision 1, which is unrecorded.
- Related: [MUX-189](../completed/MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) (reply commands are delivered verbatim, one per request — the id is meant to be copied, not retyped); [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) (a passing history row is not allowed to close a node on its own, which is why the green test row did not save this one).

## Status

**Backlog** — 0/14. Filed 2026-11-14. Parked pending Decision 1.
