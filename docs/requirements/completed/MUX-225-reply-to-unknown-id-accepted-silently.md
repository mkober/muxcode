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

- [x] An unknown `--reply-to` is never silent: it writes a lifecycle row naming the sender, the id given and the sender's open requests — `reply-to-unknown` when refused, `reply-to-repaired` (also naming the id repaired to) when Decision 1 attaches it — and edit is told once
- [x] A known `--reply-to` behaves exactly as today (negative control)
- [x] A reply with no `--reply-to` behaves exactly as today (negative control)
- [x] The behaviour chosen in Decision 1 is implemented, and on the 2026-11-14 timeline the test node does not wait out its 600 s
- [x] The agent-facing text (`muxcode send` usage, the reply instructions providers inject) says what happens to an unknown id
- [x] `docs/agent-bus.md` documents it

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

- [x] `send` resolves a response's `--reply-to`; unknown writes `reply-to-unknown` and notifies edit once
- [x] Unit tests: unknown id → row and notice; known id and no id → neither (negative controls)

### Results for Phase 1

Recorded 2026-11-17 from the worker's report in the `verify-spec` dispatch, which predates the
review's should-fix; where they differ, this is the tree. Plan read `bus/reply_to.go`, the change to
`cmd/send.go` and `receivedOpenRequests`.

| What | Where |
|------|-------|
| A response's `--reply-to` is resolved before the send: known if the bus log holds a message with that id, or the task store a task | `ReplyToKnown`, `CheckResponseReplyTo` (`bus/reply_to.go`), called from `cmd/send.go` |
| An unknown id writes `reply-to-unknown` (warn) naming the sender, the id given and the sender's open requests | `CheckResponseReplyTo` |
| Edit is told once per sender and id — an `event` from `daemon`; a retry with the same mistyped id logs again and does not tell edit again; edit's own unknown id tells nobody | `firstUnknownReplyNotice`, an `O_EXCL` marker under `<bus>/reply-to-unknown/` |
| The sender is warned on stderr, and **the reply is still sent as before** — what happens to it is Phase 2 | `cmd/send.go` |
| A request, a response with no `--reply-to`, and one whose id is known are left alone | `CheckResponseReplyTo` |

**The sender's open requests** (`OpenRequestsFor`) are tracked requests to its inbox still in flight
and unanswered, plus requests it received by receipt and has not answered, each once, oldest first.
The second half was the review's one should-fix (`/tmp/muxcode-review-1794939126.txt`): the first
cut read task files only, so an ordinary untracked request the agent had received was reported as
"open requests: none" — the very id the row exists to name.

**A change outside this spec's files.** The receipt rule was lifted out of `OpenRequestFor` into
`receivedOpenRequests` (`bus/chain_request.go`) so both readers share one definition.
`OpenRequestFor` is the gate MUX-213 and MUX-216 put on the test→review chain; plan read the lifted
function and did not compare it line for line with what it replaced. The suite passed after it.

**Tests** (`bus/reply_to_test.go`): `TestCheckResponseReplyTo` — the 2026-11-14 shape gives one row
and one notice, a retry logs and does not re-notify; negative controls: no `--reply-to`, the right
id, a task-only id, and a request carrying `--reply-to` touch nothing.
`TestCheckResponseReplyTo_NamesReceivedUntrackedRequest` — the should-fix's regression.
`TestOpenRequestsFor`.

Also reported by the worker, not a step of this phase: `scripts/test-bus-dir-leak.sh` 22 passed,
0 failed (run task `1794938802-spawn-26088880-5acf2816`), its two full `./test.sh` runs green — the
first run of that script on a tree holding all of
[MUX-223](../completed/MUX-223-project-memory-resolves-against-the-callers-cwd.md), which closed
without one.

Verified and ticked 2026-11-17 once all three had run after the last edit (13:13:37): `./build.sh`
exit 0 at 13:17:20, `./test.sh` exit 0 at 13:18:56, review at 13:19:25 with 0 must-fix, 0 should-fix,
0 nits. An earlier `verify-spec`, fired by the daemon's chain on the review that carried the
should-fix, ticked nothing. No acceptance criterion is ticked yet: Phase 2 changes what an unknown
id does, and the negative controls are to hold after it.

### Phase 2: The chosen behaviour

- [x] Record Decisions 1 and 2 in this spec before any code — recorded 2026-11-14, see "Decisions"; Decision 2 is edit's reading
- [x] Implement the chosen branch, with a test on the 2026-11-14 shape (an id with a dropped substring, action `response`)
- [x] Negative control for the branch: two open requests from the same requester never resolve by guess — the send is refused and prints a reply command for each; zero open requests is refused the same way

### Results for Phase 2

Recorded 2026-11-17 from the worker's notes (`/tmp/mux225-phase2.md`); plan read
`ResolveResponseReplyTo`, the changes to `cmd/send.go` and `bus/provider.go`, the fixture diff and
the run agent's history row. Boxes are ticked at `verify-spec`, not here.

**Decision 1 as built** (`ResolveResponseReplyTo`, `bus/reply_to.go`, called by `cmd/send.go` before
the message is built). It replaces Phase 1's `CheckResponseReplyTo`, which only reported.

| The response's `--reply-to` | What happens | Row |
|-----------------------------|--------------|-----|
| Absent, or an id the bus knows (bus log or task store) — **a stale, already-answered id included** | Sent as given, unchanged | — |
| Names no message, and the sender holds **exactly one** open request from the role it is replying to (`daemon` counts as edit; compared by inbox owner) | Sent under that request's id; a note on stderr names both ids | `reply-to-repaired` — the id given, the id repaired to, the sender's open requests |
| Names no message, and the sender holds **none or several** from that role | Refused (`ErrReplyToUnknown`): `NOT SENT`, exit 1, a ready-to-copy reply command printed for each request the sender holds open, or "(you hold no open request)" | `reply-to-unknown` |

Edit is told once per sender and id either way (a `reply-to-repaired` or `reply-to-unknown` event);
edit's own replies are not echoed back to it.

**The scope held.** Nothing is built toward the stale-id case: a known id passes straight through,
and `TestResolveResponseReplyTo_PassThrough` pins a stale answered id among its unchanged cases. The
optional timestamp guard was not taken, and there is no nearest-id matching (Decision 2).

**Agent-facing text** — the fifth criterion. `muxcode send`'s usage says what happens to an id naming
no message. The reply reminder providers inject (`withReplyInstructions`, single and batch forms)
now ends: "copy each request id exactly as written: an id naming no message is attached to your one
open request from that requester, or the reply is refused and the right commands printed". It never
spells the flag, which scripted fake agents grep for.

**Tests** (`bus/reply_to_test.go`): `TestResolveResponseReplyTo_PassThrough` (no id, the right id, a
task-only id, a stale answered id, a request — unchanged, no row, no notice);
`_RepairsTheOneOpenRequest` — the 2026-11-14 shape, dispatch `1794683890-daemon-30a89855` answered
with `1794683890-da0a89855` to edit, attached to the dispatch, one row and one notice, a retry
silent to edit; `_RefusesWithoutOneOpenRequest` (two from the requester — refused, both returned
with reply commands; none open — refused; one from another requester — refused);
`_ReceivedUntrackedRequest`; `_EditSenderNotNotified`; `TestOpenRequestsFor`.

**A fixture elsewhere had to change, and it shows the new refusal at work.**
`scripts/test-unrequested-test-cycles.sh` (MUX-213) sent its review→test reply with a made-up
`--reply-to` that named nothing. Review holds no open request from test, so that reply is now
refused; the fixture sends it with no `--reply-to`, and its second reply in one session carries a
tag, because without a pending `--reply-to` the response brake (MUX-175) refuses an identical
repeat.

**Runs** (run task `1794940031-spawn-26088880-f94d231f`, ended 13:30:48, after the last edit at
13:27:07; the run agent's history row confirms both): `scripts/test-unrequested-test-cycles.sh`
55 passed, 0 failed, floor 55; `scripts/test-bus-dir-leak.sh` 22 passed, 0 failed, floor 22, its two
full `./test.sh` runs green.

**Plan's notes, for whoever reads the rule later.**

- "Known" means the bus log or the task store holds the id. A reply to a request whose log line and
  task are both gone would now read as unknown, and be refused or attached to the sender's other
  open request from that role. Plan did not establish whether that can happen inside a request's
  life.
- The rule now turns an invented id into a hard refusal wherever the sender holds no single open
  request from that role — the fixture above was one such caller. Any other script or agent habit
  that passes a placeholder id will fail the same way; none was searched for.

Docs (plan): `docs/agent-bus.md` — the `--reply-to` bullet under `muxcode send`, a row in its
refusal table, and `reply-to-repaired` / `reply-to-unknown` in the lifecycle event catalog.

**Two review must-fixes after that record** (review of 13:35,
`/tmp/muxcode-review-1794940466.txt`; plan read both findings and the code as it now stands).

| Finding | Fix |
|---------|-----|
| **The repair could attach a reply to a dead request.** The receipt half of the open-request list brought back tracked requests whose task had timed out — a timeout changes the task's status and leaves its receipt. A consumed, timed-out dispatch could become the sole repair target, or make a live one look ambiguous. In Phase 1 that only skewed a diagnostic; here it decided correlation | A task record is authoritative: a request counts as open by receipt only when it has **no** task record at all (`OpenRequestsFor`). `TestResolveResponseReplyTo_TimedOutTaskIsNotOpen` — a timed-out task alone is refused; beside one live request, the reply repairs to the live one |
| **Matching and the printed commands ignored the request's reply target.** A chain request from test to review tells review to answer **edit** (`ReplyRole`, MUX-213). A mangled reply sent to edit was refused, and the command printed told review to send to test — the reply-to-test cycle MUX-213 closed | A candidate is matched on where its reply goes (`ReplyTarget`: `ReplyRole` when set, read from the request's log line for a tracked task), and `ReplyCommandFor` addresses that target. `TestResolveResponseReplyTo_HonoursReplyRole` |

So "the role it is replying to" in the table above means the request's **reply target**, which is
its sender unless the request names another.

**The two script runs above predate these fixes** (13:30:48; `reply_to.go` was last edited at
13:35:49). Neither script has been run since.

Verified and ticked 2026-11-17 once all three had run after the last edit (13:36:11): `./build.sh`
exit 0 at 13:40:23, `./test.sh` exit 0 at 13:41:59, review at 13:42:29 with 0 must-fix, 0 should-fix,
0 nits. Criteria 1, 2 and 3 are ticked on the unit tests (`_RepairsTheOneOpenRequest`,
`_RefusesWithoutOneOpenRequest`, `_PassThrough`), 5 on the two texts plan read in the diff, 6 on the
docs. Criterion 4 waits for Phase 3, which drives the 2026-11-14 timeline through a graph node.

### Phase 3: Integration test

- [x] Create `scripts/test-reply-to-unknown.sh`: a scratch daemon, a one-node graph `test` run, a stub agent replying with a mangled id → the row is written, edit is told, and the node ends as Decision 1 says, well inside 600 s
- [x] Control: the same stub replying with the right id completes the node with no `reply-to-unknown` row
- [x] Run the script and record pass/fail counts in this spec

### Results for Phase 3

Recorded 2026-11-17 from the worker's request; plan read the script's header and the run's log.
Boxes are ticked at `verify-spec`, not here.

`scripts/test-reply-to-unknown.sh` — muxcode built from the tree, scratch `HOME`, `XDG_STATE_HOME`,
lifecycle log, config, memory and repo, one session and daemon per section, a private tmux server.
The script plays the test agent by reading its inbox. About a minute.

**Run: 22 passed, 0 failed, floor 22, exit 0** (run task `1794941108-spawn-26088880-e1faef26`,
requested 13:45 after the script's last edit at 13:45:07, on the code as reviewed at 13:42; the run
agent's history row confirms it, and plan read the log — 22 `PASS`, no `FAIL`).

| Section | Shows |
|---------|-------|
| A | A real one-node graph `test` run on a scratch daemon. The dispatch `1794941118-daemon-319f8afb` is answered to edit with the id mangled as on 2026-11-14 (`1794941118-da19f8afb`). `send` exits 0 naming the repair; one `reply-to-repaired` row names both ids; no `reply-to-unknown` row; edit holds one notice; **the node and run complete 2 s after the reply, not at 600 s**; the outcome is success and the dispatch's task is completed |
| B | Control: the same run answered with the right id completes with no repair note, no `reply-to-repaired` or `reply-to-unknown` row, and nothing told to edit |
| C | Refusals, no daemon. Holding no open request: `NOT SENT`, exit 1, saying none is open, one `reply-to-unknown` row. Holding two from edit: `NOT SENT` with a reply command to edit for each, a second row, no guess. Edit told once per refused id |
| D | Control: a build with the resolver bypassed sends the 2026-11-14 reply silently, and 15 s later the node is still running on an in-flight task — left to its 600 s cap; nothing logged |

**What it does not drive.** The stale-id case (a known id is a pass-through by design, pinned by the
unit test only); a chain request whose reply target is edit (unit test
`TestResolveResponseReplyTo_HonoursReplyRole` only); and a real agent — the script types the reply
itself, so whether an agent that sees `NOT SENT` and the printed commands then re-sends correctly
is not shown.

Verified and ticked 2026-11-17 once all three had run after the last edit (the script at 13:45:07,
`CLAUDE.md` at 13:45:49): `./build.sh` exit 0 at 13:46, `./test.sh` exit 0 at 13:47:03, review at
13:47:42 with 0 must-fix, 0 should-fix, 0 nits. Criterion 4 is ticked on section A — the node
completes 2 s after the repaired reply — with section D as its control.

Not repeated since Phase 2's must-fixes: `scripts/test-unrequested-test-cycles.sh`, whose fixture
this spec changed, and `scripts/test-bus-dir-leak.sh`. Both last passed at 13:30, before
`reply_to.go`'s final edit at 13:35:49.

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-unmatched-reply.md` (edit), on the user's standing instruction to file defects found along the way.
- Parked at filing: Phase 2 held Decision 1, which was unrecorded.
- Unparked 2026-11-14: the user decided Decision 1 (relayed by edit); Decision 2 stands as edit's reading — no fuzzy repair — unless the user overrides it.
- Related: [MUX-220](../completed/MUX-220-wake-batch-drops-a-roles-own-delegation-reply.md) (2026-11-17) resolves a reply's originating request from the bus log (`FindRequestByID`) when no task file exists — a lookup this spec can share.
- Related: [MUX-189](../completed/MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) (reply commands are delivered verbatim, one per request — the id is meant to be copied, not retyped); [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) (a passing history row is not allowed to close a node on its own, which is why the green test row did not save this one).

### Findings at activation (2026-11-17)

Checked against the tree at `7f3a88d`. The defect stands; the line numbers above have drifted.

| Spec says | Tree today |
|-----------|------------|
| `cmd/send.go:45–51` reads `--reply-to` into `replyTo` and never looks it up | Still never looked up; the parse is at `:61`, `:78–84`, and the message is built with it at `:226` |
| `MarkResponded` at `bus/delivery.go:93–107` skips silently when the id has no status, and drains nothing when `FindMessageByID` finds nothing | Unchanged in behaviour, now at `:101–116` |
| `responseAnswers` at `cmd/send.go:287–296` | Now at `:318–329`, same rule |
| `reply-to-unknown`, `reply-to-repaired` | Neither exists |
| The duplicate-reply guard (`ErrReplySuppressed`, MUX-155) | There — `bus/inbox.go`, surfaced in `cmd/send.go:243`. It is what answered the 2026-11-16 stale id with `NOT SENT` |
| `scripts/test-reply-to-unknown.sh` | Does not exist |

**Scope for this run — build what is decided, and no more.** The trigger is an **unknown** id, one
that names no message: Decision 1 for that case, Decision 2's no-fuzzy-repair, and the second
criterion as written — a known `--reply-to`, a stale one included, behaves exactly as today. Whether
the repair extends to a stale id is the user's question and is still unanswered; it is not for the
worker to settle, and nothing here should be built toward it. If the user says yes later, that is a
change to this spec's criteria or a follow-up spec.

## Status

**Complete** — activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). Uncommitted at close-out; the run's ship step commits it. 14/14 — Phases 1–3 complete and all six criteria met. Filed 2026-11-14; unparked 2026-11-14 with Decisions 1 and 2 recorded.

A response whose `--reply-to` names no message is no longer accepted silently. If the sender holds
exactly one open request whose reply goes to the role it is replying to, the reply is attached to
that request (`reply-to-repaired`); with none or several it is refused — `NOT SENT`, exit 1 — with a
ready-to-copy reply command for each request the sender holds open (`reply-to-unknown`). Edit is
told once per sender and id. Replayed on the 2026-11-14 shape, the graph node completes two seconds
after the repaired reply instead of failing at 600 s.

**Closed with one question still open, and it is the user's.** This spec was activated and built
with the stale-id question below unanswered, on this status's own earlier reading that the decided
scope is buildable as written. What shipped is that scope only: a `--reply-to` the bus knows — a
stale, already-answered id included — is sent exactly as before, so the 2026-11-16 occurrence would
play out today as it did then. Extending the repair to a stale id needs the user's word and a
follow-up spec; none is filed.

Also for a later reader, each recorded in the results above: Decision 2 (no fuzzy repair) is edit's
reading, not the user's; an invented placeholder id is now a hard refusal for any caller, and no
search was made for others beyond the one fixture changed; and
`scripts/test-unrequested-test-cycles.sh` and `scripts/test-bus-dir-leak.sh` were not re-run after
Phase 2's last fixes.

**2026-11-16:** a second occurrence recorded (see [Observed again](#observed-again-2026-11-16--a-stale-id-not-an-unknown-one)) — a `--reply-to` naming the sender's previous, already-answered request, which this spec's "unknown id" trigger does not cover. Not parked: the decided scope is still buildable as written. **One question is open for the user** — whether Decision 1's repair extends to a stale id — and it should be answered before Phase 2, since saying yes changes the second criterion. (Still unanswered at close on 2026-11-17; Phase 2 was built to the decided scope.)
