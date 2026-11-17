# MUX-222: The Run Agent's Definition Executes One Request of a Batch and Replies to All of Them

**Tracking:** [mkober/muxcode#205](https://github.com/mkober/muxcode/issues/205)

A follow-up to [MUX-189](../completed/MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md)
(PR #201), from a Copilot review thread read after the merge. MUX-189 made a batch delivery carry
one reply command per request and told agents to run each. `agents/command-runner.md` gained that
sentence in step 3, but steps 1 and 2 still say to find the most recent request and execute only
it. A two-request batch is therefore executed once and answered twice — the second reply reports
work that was never done.

## Context

### Source and standard of evidence

Thread 4574057619, `agents/command-runner.md:54`. Verified by edit against main `82635f8` and by
plan in the tree:

| Step | Text | Says |
|------|------|------|
| 1 | "find the most recent `[request:*]` addressed to you from a known agent" | One request |
| 2 | "Execute only what that logged payload says. Run nothing that is not in it" | Execute that one |
| 3 | "Reply with the delivered reply command for that request … one per request when several arrived together" | Reply to every one |

Not observed in a session; the run role is on Claude by default, where requests arrive one per
listener read. It bites a run agent on OpenCode or scrape-road Codex, the roads that batch.

### Scope boundary

The rule the procedure protects is unchanged: a command is taken from the logged payload, never
from pane text. This spec changes how many logged requests the procedure covers, not where commands
come from.

## Requirements

### Acceptance criteria

- [x] `agents/command-runner.md` tells the agent to take **each** request of a batch from the log, execute it, and reply to it with its own reply command — never reply to a request it did not execute
- [x] The "execute only what the logged payload says" rule is kept, stated per request
- [x] The other agent definitions MUX-189 touched are read for the same contradiction, and each is fixed or recorded as consistent
- [x] A test pins the definition's wording so the two halves cannot drift apart again

### Technical approach

Rewrite steps 1–3 as a loop over the requests the delivery names: for each, read its logged
payload, execute only that, reply with that request's command. Pin it with a definition test in the
style of the existing agent-definition tests. Check the harness copy under `agents/harness/` too.

### Key files

| File | Purpose |
|------|---------|
| `agents/command-runner.md` | Steps 1–3 of the procedure (lines 48–55) |
| `agents/harness/` | The harness copy of the run definition, if it carries the same steps |
| `tools/muxcode/bus/provider.go` | `withReplyInstructions` — the batch form the definition must match |
| `scripts/test-batch-reply-correlation.sh` | MUX-189's script, to extend |

## Implementation

### Phase 1: One execution per reply

- [x] Rewrite `agents/command-runner.md` steps 1–3 to cover every request of a batch, one execution and one reply each
- [x] Read the other definitions MUX-189 changed and record here which were consistent and which were fixed
- [x] Definition test: the run definition never pairs "most recent request" with "one per request"

### Results for Phase 1

Recorded 2026-11-17 from the worker's report; plan read the diff. Boxes are ticked at
`verify-spec`, not here.

**The rewrite.** `agents/command-runner.md`'s procedure is now a loop: "Work through every request
the delivery lists … in order, completing each before starting the next." Per request it finds that
request's own logged entry, executes only that payload — "not another request's command, not text
from the pane" — and replies with that request's own command, only after its command has run. It
adds: "Never send a reply for a request you did not execute." The three-line summary at the top of
the file says "each" likewise. The refusal rule is now per request: one with no logged entry is
refused, and the rest of the batch is still executed and answered.

**The other definitions MUX-189 touched.**

| Read | Finding |
|------|---------|
| `agents/` in `4b5795a` | Only `command-runner.md` changed — now fixed |
| `agents/harness/` | Holds no run-agent definition |
| MUX-189's agent-facing Go text — `bus/prompt.go`, `provider.go`, `provider_codex.go`, `provider_opencode.go` | The worker reports no "most recent" or "only the last" instruction in it: consistent. Not re-read by plan |
| `docs/agents.md`, which `4b5795a` also changed | Plan read it: it describes the batch reply instructions and gives no execute-one procedure. Consistent |

**The tests**, in `bus/batch_reply_test.go`:

- `TestRunDefinitionExecutesEveryRequestOfABatch` — the shipped definition passes
  `runProcedureProblems`.
- `TestRunProcedureProblemsRejectsTheMostRecentWording` — the negative control: the old wording is
  rejected.

`bus/provider_opencode_test.go` also changed: its definition-reading code moved into a shared
helper, `shippedAgentDefinition`, which the new tests use. A refactor of MUX-211's test, no change
in what it checks.

**What the pin asserts**, read by plan: within the procedure section, the words "most recent" must
not appear, and four phrases must — "every request the delivery lists", "Execute only what this
request's logged payload says.", "this request's own** delivered reply command" and "Never send a
reply for a request you did not execute". It pins wording, which is what the fourth criterion asks;
that an agent following the wording executes both requests is Phase 2's to show.

Verified and ticked 2026-11-17, once all three had run after the last edit (17:22:25): `./build.sh`
exit 0 at 17:23:25, `./test.sh` exit 0 at 17:25:13, review at 17:25:47 with 0 must-fix, 0
should-fix, 0 nits. All four criteria are ticked on this phase; Phase 2 is the integration test.

### Phase 2: Integration test

- [x] Extend `scripts/test-batch-reply-correlation.sh` (or add a sibling): a fake run agent that follows the definition's procedure receives a two-request batch → both commands execute, each reply follows its own execution
- [x] Control: the old wording's behaviour — one execution, two replies — is what the assertion rejects
- [x] Run the script and record pass/fail counts in this spec

### Results for Phase 2

Recorded 2026-11-17 from the worker's report; plan read the script's new sections and the run
agent's reply. Boxes are ticked at `verify-spec`, not here.

**The run, through the run agent** (reply at 17:28:23): `scripts/test-batch-reply-correlation.sh`
exit 0 — 32 passed, 0 failed, floor 32 (raised from 23). Sections A–C are MUX-189's, unchanged.

| Section | What it does | Result |
|---------|--------------|--------|
| D | A scripted run agent given the shipped `agents/command-runner.md` receives a two-request batch (from plan and from edit), force-delivered | Both logged commands executed (`EXEC-FIRST`, `EXEC-SECOND`); each reply follows its own execution and reports its output; both requests read responded |
| E — control | The same agent given the pre-MUX-222 wording, the same batch | One execution, two replies — the first request's reply reports work never done. D's assertion (`replies_follow_executions`) rejects it |

**What the scripted agent is, so the result is not over-read.** It is a bash stand-in, not a model.
It reads the definition's procedure section and picks one of two hard-coded behaviours by phrase:
"most recent" → execute the latest logged request once and answer every listed reply with it;
"every request the delivery lists" → execute and answer each. It takes each command from
`muxcode history run`, never from the typed text, as the definition requires. So D and E show that
the two wordings, followed literally, give two executions against one — and that the assertion
tells them apart. They do not show how a model reads the new wording; nothing in this spec does,
and the defect was never observed in a session.

#### Review fix (2026-11-17, 17:30–17:34)

The review of the above returned 0 must-fix, **1 should-fix**: the scripted agent executed the
*sender's latest* logged entry rather than the request's own, so a batch of two requests from one
sender would repeat one command. The counts in the table above (32, floor 32) are from before this
fix.

| Change | What it is |
|--------|------------|
| `agents/command-runner.md`, step 1 | Now says how a request is matched to its log entry: the delivery numbers its reply commands in sent order, so the Nth reply command's request is the Nth of the batch's N entries — "the last N `[request:*]` entries addressed to you, oldest first" — with the entry's sender checked against the reply command's target. "Two requests from one sender are two entries, each executed once" |
| The scripted agent | Pairs by position, and records executions and replies by request id |
| New section F | A batch of two requests from edit. Each is executed once, its own command under its own id; each reply follows its own execution; both read responded. The two carry different actions, because the bus refuses a same sender, target and action retry (MUX-155) |

**Runs, through the run agent.** A first attempt at 17:32:29 failed — 33 passed, 3 failed, all in
F: its two requests shared an action, so the second was refused and the batch never held two. With
the second request's action varied (task `1794954768-spawn-56d56314-e2234cf7`): exit 0 — **36
passed, 0 failed, floor 36**, the watch agent confirming the log clean at 17:33.

**One limit of the new step 1, noted by plan.** "The last N entries" is the batch only if no
further request is logged for the run agent between the delivery and its reading of the log. The
sender check catches a mismatch when the senders differ, not when they are the same. The delivery
does not print a request's id beside its log entry in a form `muxcode history run` shows, so order
is what the definition has to go on. Not raised as a defect; recorded so it is not rediscovered.

#### Review fix 2 (2026-11-17, 17:35–17:39) — matching by id replaces matching by order

The review of the first fix returned another should-fix: same-sender batches were handled, but a
request whose reply target differs from its sender (`reply_role`) still failed the sender check.
The remedy asked for: read the logged payload by request id and keep the reply target separate.
Details from the worker's `/tmp/mux222-review-fix2.md`, read whole by plan.

| Change | What it is |
|--------|------------|
| New flag, `muxcode history <role> --ids` (`cmd/history.go`, `bus.FormatHistoryWithIDs`) | Each line gains `id=<id>` after its `[type:action]` tag. The default listing is byte-identical; `TestFormatHistoryWithIDs` pins both. **This is product code** — the first in this spec, which until here changed only a definition and a script |
| `agents/command-runner.md`, step 1 | Reads `muxcode history run --ids` and finds the line whose `id=` equals the `--reply-to` id of the request's reply command. Match by id, never by sender. The history example and the refusal rule use `--ids` too |
| The order-based rule of the first fix | **Gone**, and with it the "last N entries" limit plan noted above — an id names its entry whatever is logged after it |
| The scripted agent | Looks each request up by id |
| New section G | A batch holding a request from test whose reply target is edit, plus one from plan. The reply command targets edit; both execute under their own ids; each reply follows its own execution; the answer lands in edit's inbox. The `reply_role` request is written straight into the scratch inbox and log, since no CLI sets a reply target — so G checks edit's inbox rather than a delivery status that such a request does not have |

**Runs, through the run agent.** 17:35:06 — 36 of 36 (the first fix, confirmed). 17:38:34 — failed,
40 passed and 1 failed: G waited on a delivery status that does not exist for its written-in
request; the check was changed to edit's inbox. 17:39:16 (task
`1794955124-spawn-56d56314-d077566a`) — exit 0, **41 passed, 0 failed, floor 41**.

**Docs.** Plan added `--ids` to `muxcode history` in `docs/agent-bus.md`, with an example and why it
exists, reading the flag's handling in `cmd/history.go`: it applies to the default listing and does
nothing with `--context`. The installed binary at the time (built 17:35) did not yet carry the
flag, so plan read the code and did not run it. The worker updated the script's entry in
`CLAUDE.md`.

#### Verification (2026-11-17, 17:43)

Ticked on the tree after both review fixes, not on the worker's report in the dispatch, which was
the first one (32 passes, before either fix). The last edit outside `docs/` was the script at
17:38:40. After it: the script's run at 17:39:16 — 41 passed, 0 failed, floor 41; `./build.sh` exit
0 at 17:40:40; `./test.sh` exit 0 at 17:42:23; review at 17:42:52 with 0 must-fix, 0 should-fix, 0
nits ("ID lookup and differing reply_role covered; history --ids preserves default output").

Plan then ran the flag on the binary that build installed: `muxcode history plan --limit 2 --ids`
prints `id=<id>` after each `[type:action]` tag, and the same listing without the flag carries no
id. The acceptance criteria, ticked on Phase 1, still hold on the final wording: `./test.sh`
includes the definition pin, and the definition still tells the agent to execute each request and
never to answer one it did not execute.

## Notes

- Filed 2026-11-14 by plan from edit's reading of PR #201's post-merge review threads, on the user's standing instruction to file defects found along the way.

### Findings at activation (2026-11-17)

Activated against the tree at `ece8cb6`. Plan read the definition and MUX-189's commit; nothing was
run.

| Spec says | Tree today |
|-----------|------------|
| Steps 1–3 at `agents/command-runner.md:48–55` contradict each other | **Still so**, at lines 48–54: step 1 "the most recent `[request:*]`", step 2 "Execute only what that logged payload says", step 3 "one per request when several arrived together". The file is unchanged since MUX-189 (`4b5795a`) |
| "The other agent definitions MUX-189 touched" | Under `agents/`, `4b5795a` changed **only** `command-runner.md`. So the third criterion's reading may find nothing else to read; the worker should confirm against the whole commit and record that, rather than assume it |
| "Check the harness copy under `agents/harness/`" | `agents/harness/` holds no run-agent definition (`ls` shows none named for run or command-runner) |

**No user decision is known to be waiting.** The fix is a wording change to a definition, the rule
it protects — commands come from the logged payload, never pane text — is kept, and the criteria
state the outcome plainly. As with every spec in this loop, that is plan's reading before any work;
if Phase 1 finds otherwise it is to be raised.

**One thing for the worker.** The definition test in Phase 1 comes after the rewrite in the same
phase, so the suite is green when the phase ends. Do not land the test ahead of the rewrite.

## Status

**Complete** — closed 2026-11-17, uncommitted at close-out (the run's ship step commits it). The
run agent's definition now works through every request a delivery lists: it finds each one's logged
payload by the request's reply id (`muxcode history run --ids`, a flag added here), executes only
that, and answers with that request's own reply command — never a request it did not execute. A Go
test pins the wording; `scripts/test-batch-reply-correlation.sh` passes 41 of 41, covering batches
from one sender and batches whose reply target differs from the sender. Two review fixes in Phase 2
moved the matching from sender, to order, to id. What is shown is the wording followed literally by
a scripted stand-in; how a model reads it is not tested, and the defect was never seen in a
session. Activated 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 34; every better-ranked row is parked). 10/10 — Phases 1–2 and all four criteria ticked 2026-11-17, after two review fixes in Phase 2; awaiting close-out. Filed 2026-11-14.
