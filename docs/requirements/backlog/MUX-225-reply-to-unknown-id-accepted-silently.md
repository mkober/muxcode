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

### Observed again (2026-11-16) — a stale id, not an unknown one

Reported by edit (`1794866234-edit-18b29d28`), same session, same run, MUX-180's Phase 2 test node.
Plan re-read the task store and `log.jsonl`.

| Time | Event | Source |
|------|-------|--------|
| 16:36:18 | The daemon dispatches `request:test`, id `1794864978-daemon-a1fc0b39` | `log.jsonl` |
| 16:37:54 | The Codex test agent answers it — response `1794865074-test-c9f0696a`, `reply_to` that id; the task is `completed` | `log.jsonl`, `tasks/` |
| 16:46:16 | The daemon dispatches the **next** `request:test`, id `1794865576-daemon-1f847a92` | `log.jsonl` |
| ~16:47 | The test agent answers it with `--reply-to 1794864978-daemon-a1fc0b39` — the previous request's id. `muxcode send` prints `NOT SENT`: the task that id names is already answered (MUX-155's duplicate-reply guard, `ErrReplySuppressed`) | Edit's account. Nothing is written for a refused reply, so no row in `log.jsonl` shows it; plan found none and could not confirm the time or the id from records |
| 16:56:17 | `task-timeout daemon→test:test expired in-flight` — task `1794865576-daemon-1f847a92` is `timed-out` at its 600 s cap; the graph's test node fails and the run retries it | lifecycle log, `tasks/` |

**How this differs from the 2026-11-14 case, and why it matters for the decisions.**

| | 2026-11-14 | 2026-11-16 |
|---|-----------|-----------|
| The id given | Names **no** message (six characters dropped) | Names a **real** message — the sender's previous, already-answered request |
| What `send` said | `Sent` — silent | `NOT SENT`, exit 1 — the agent was told, and did not send a corrected reply |
| What caught it | Nothing | MUX-155's duplicate-reply guard, which shipped two days after this spec was filed |
| The sender's open requests from that requester | One | One |
| The outcome | The open request waits out 600 s | The same |

The outcome and the repair that would have fixed it are the same: exactly one open request from that
requester, so Decision 1's first branch would attach the reply to it. But **this spec's trigger is an
unknown id**, and this id is known. As written:

- Decision 1 speaks of "an unknown `--reply-to`", and the title, the first criterion and Phase 2 all
  key on the id naming no message. A stale id never enters that branch.
- The second criterion — "a known `--reply-to` behaves exactly as today" — is a negative control that
  would **forbid** repairing it.
- The optional timestamp guard would refuse it too: the two ids' leading timestamps differ
  (`1794864978` and `1794865576`).

So this occurrence is evidence for the defect's cost, and for a wider rule than the one decided:
"a `--reply-to` that cannot be the request being answered" covers both an id that names nothing and
one that names a request already answered. **Whether Decision 1 extends to the stale-id case is not
decided.** It needs the user's word before Phase 2 is built, because it changes a criterion.

### Mechanism — verified in code

- `cmd/send.go:45–51` — `--reply-to` is read into `replyTo` and never looked up.
- `bus/delivery.go:93–107` `MarkResponded` — reads the delivery status for the id and skips silently when there is none; `FindMessageByID` likewise finds nothing, so nothing is drained.
- `cmd/send.go:287–296` `responseAnswers` — a reply carrying a `ReplyTo` is matched on that id alone (`m.ReplyTo == msgID`); the unlinked fallback, which correlates by action, applies only when `ReplyTo` is empty. A wrong id is therefore worse than no id. Here the action was `response`, not the request's `test`, so the fallback would not have matched either.

### Decisions — recorded 2026-11-14

Both were owed at filing. Edit relayed them on 2026-11-14 (`/tmp/decisions-223-225.md`) and plan
recorded them here. Decision 1 is the user's. Decision 2 is **edit's reading, not separately stated
by the user**, who may override it.

| # | Decision | Options offered | Decided |
|---|----------|-----------------|---------|
| 1 | What `send` does with an unknown `--reply-to` | (a) refuse, non-zero, naming the sender's open requests so the agent can retry; (b) accept, log and alert edit; (c) accept and fall back to the sender's single open request from that requester when exactly one exists, logging the repair | **(c), with (a) as the fallback** — the user's, as below |
| 2 | Whether a close match is repaired | Repairing "nearest id" is a second guess on top of Decision 1 | **No** nearest-id or fuzzy repair beyond Decision 1 — edit's reading; the single-open-request rule covers the unambiguous case |

**Decision 1 — behaviour.**

- **Exactly one open request** held by the sender from the reply's requester (`daemon` normalizes to edit): attach the reply to it, write `reply-to-repaired` naming the id given and the id repaired to, and tell edit once.
- **Zero or several open requests**: refuse with a non-zero exit, print the correct ready-to-copy reply command for each open request, write `reply-to-unknown`, and tell edit once.
- On the 2026-11-14 shape — one open request, a mangled id, action `response` — the node closes on the repair, well inside 600 s.

**Optional guard, the implementer's call** (proposed by edit): repair only when the given id's
leading unix timestamp equals the open request's. It did on 2026-11-14 (`1794683890`).

Phase 1 needs neither decision; Phase 2 is Decision 1.

## Requirements

### Acceptance criteria

- [ ] An unknown `--reply-to` is never silent: it writes a lifecycle row naming the sender, the id given and the sender's open requests — `reply-to-unknown` when refused, `reply-to-repaired` (also naming the id repaired to) when Decision 1 attaches it — and edit is told once
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

- [x] Record Decisions 1 and 2 in this spec before any code — recorded 2026-11-14, see "Decisions"; Decision 2 is edit's reading
- [ ] Implement the chosen branch, with a test on the 2026-11-14 shape (an id with a dropped substring, action `response`)
- [ ] Negative control for the branch: two open requests from the same requester never resolve by guess — the send is refused and prints a reply command for each; zero open requests is refused the same way

### Phase 3: Integration test

- [ ] Create `scripts/test-reply-to-unknown.sh`: a scratch daemon, a one-node graph `test` run, a stub agent replying with a mangled id → the row is written, edit is told, and the node ends as Decision 1 says, well inside 600 s
- [ ] Control: the same stub replying with the right id completes the node with no `reply-to-unknown` row
- [ ] Run the script and record pass/fail counts in this spec

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-unmatched-reply.md` (edit), on the user's standing instruction to file defects found along the way.
- Parked at filing: Phase 2 held Decision 1, which was unrecorded.
- Unparked 2026-11-14: the user decided Decision 1 (relayed by edit); Decision 2 stands as edit's reading — no fuzzy repair — unless the user overrides it.
- Related: [MUX-189](../completed/MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) (reply commands are delivered verbatim, one per request — the id is meant to be copied, not retyped); [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) (a passing history row is not allowed to close a node on its own, which is why the green test row did not save this one).

## Status

**Backlog** — 1/14. Filed 2026-11-14. Unparked 2026-11-14 with Decisions 1 and 2 recorded; eligible for the defect loop.

**2026-11-16:** a second occurrence recorded (see [Observed again](#observed-again-2026-11-16--a-stale-id-not-an-unknown-one)) — a `--reply-to` naming the sender's previous, already-answered request, which this spec's "unknown id" trigger does not cover. Not parked: the decided scope is still buildable as written. **One question is open for the user** — whether Decision 1's repair extends to a stale id — and it should be answered before Phase 2, since saying yes changes the second criterion.
