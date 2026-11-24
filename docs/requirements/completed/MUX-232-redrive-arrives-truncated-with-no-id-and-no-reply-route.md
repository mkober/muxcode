# MUX-232: A Re-Driven Request Arrives Truncated, With No Message Id and No Reply Route

**Tracking:** [mkober/muxcode#231](https://github.com/mkober/muxcode/issues/231)

A request an agent's own inbox listener consumed, and the agent then never acted on, is recovered by
a re-drive typed into its pane. The re-drive carries an 80-character preview of the payload, names
the sender as it was written (`daemon`), and holds neither the message id nor a reply command. The
row is already consumed, so `muxcode inbox` returns nothing. The agent can do the work and cannot
answer it; a graph node waits out its cap on a request that was read twice.

On 2026-11-17 this failed two `55-defect-backlog-loop` runs in the `tibo` session at their first
node, a read-only check on the commit agent, 600 s each.

## Context

### Source and standard of evidence

Filed 2026-11-17 on the user's instruction, from a screenshot of the `tibo` session's edit agent
reporting the failure. Plan read the `tibo` session's lifecycle log, `log.jsonl`, task and delivery
records, and the re-drive code at `09260a8`. What plan could not read is marked.

### Observed (session `tibo`, 2026-11-17)

| Time | Event | Source |
|------|-------|--------|
| 13:15:28–13:15:59 | `idle-task-retry`, then `idle-task-rescue`: commit idle with an unanswered `commit` task from edit | lifecycle |
| 13:16:31 | Edit relaunches commit into its conversation (`muxcode resume`, `reason=resume`) | lifecycle |
| 13:17:10 | Run `1794939430-55-defect-backlog-loop-5f8a1155` starts; `preflight` dispatches `request:pr-read`, id `1794939430-daemon-abe5b22f`, to commit | lifecycle, `log.jsonl` |
| 13:17:12 | The dispatch is `acked` by commit — receipt kind `ack`, listener pid 30023, parent alive | `delivery/…status` |
| 13:17:20 | The session's daemon is restarted by an install in another session (`daemon-upgraded`) | lifecycle |
| 13:18:56, 13:19:55 | `task-stall-redrive daemon→commit:pr-read` 1/2 and 2/2, each a `force-redrive commit: 1 in-flight task(s)`; then `task-stall-giveup` | lifecycle |
| 13:27:12 | `task-timeout`, `preflight -> failure`, `graph-run-failed … no live edge` | lifecycle |
| 13:35:49 | Run `1794940547-55-defect-backlog-loop-2bb49922`: the same dispatch, id `1794940549-daemon-382f2d62` | lifecycle, `log.jsonl` |
| 13:35:51 | `acked` by commit — listener pid 32494, parent alive | `delivery/…status` |
| 13:37:22–13:42:57 | Four re-drives in two rounds, two give-ups | lifecycle |
| 13:45:50 | `task-timeout`, `preflight -> failure`, run failed | lifecycle |
| 13:54:36 | Commit reloaded clean, same provider (`reason=reload`) | lifecycle |
| 13:54:41 | Edit sends its own `request:pr-read`, id `1794941681-edit-48928897` | `log.jsonl` |
| 13:54:44 | `acked` by commit — listener pid 25643, parent alive | `delivery/…status` |
| 13:55:34, 13:56:07 | `idle-task-retry`, then `idle-task-rescue`: commit idle with that request unanswered; the task ends `timed-out` with no response | lifecycle, `tasks/` |

In the whole window commit sent nothing to the bus but its two startup replies: `log.jsonl` holds no
response from it to any of the three requests.

**From the screenshot, not from records** (the `tibo` edit agent's account):

- The re-drive reached commit cut off after `git branch --show-current...` and with no message id.
- Commit tried to reply to `daemon`, which is not a bus role, and then answered only on its own
  screen — on `main` at `0ee6d45`, in sync, `CLAUDE.md` and `muxcode/` untracked. A refused send
  writes nothing, so no record shows the attempt.
- `muxcode diagnose commit` reported "No issues detected".

### Mechanism — verified in code

| Fact | Where |
|------|-------|
| Both re-drive roads build their text the same way: `"Re-drive (consumed but never completed): " + BuildCombinedNotification(msgs)` | `redriveInFlightTasks` (the stall watchdog's road, via `ForceDeliver`) and `RedriveTask` (the graph executor's), `bus/deliver.go` |
| For one message that is `New message from <From> [<type>:<action>]: <payload cut to 80 runes>...` — no id, no reply command | `BuildCombinedNotification`, `truncRunes`, `bus/notify.go:148–156` |
| The 80th rune of this payload is the last character of `--show-current` — exactly where the screenshot says it was cut | The dispatch's payload in `tasks/` |
| The message is rebuilt from the task, which holds the full payload and the id; nothing is missing at the source | `redriveMessages`, `bus/deliver.go`; `Task`, `bus/task.go` |
| `<From>` is the raw sender. A graph dispatch is sent as `daemon`; an ordinary delivery's `To reply:` line normalizes that to edit, and the re-drive has no such line | `BuildCombinedNotification`; `NormalizeBusRole` |
| The notification form was written as a wake for an agent that then runs `muxcode inbox`. On a re-drive the row is already consumed, so that read returns nothing — which is why the re-drive exists | Comment on `redriveInFlightTasks` |

### Not established

**Why a request the listener consumed never started a turn.** Three requests in a row were consumed
by commit's own listener within three seconds — true `acked` receipts, live parent — and the agent
stayed idle on each. It happened before and after a `resume`, and again after a clean reload. Plan
did not read the commit pane or its listener output. Candidates nobody has tested: the listener's
output never reached the agent's turn; the turn ran and ended without a reply; the daemon restart at
13:17:20 (one case of three).

This spec fixes what makes that state unrecoverable. It does not claim to fix what causes it.

Later the same day edit relayed a live observation of one of the three — the agent read the inbox,
found it empty, and never read its listener's output. See
[Results for Phase 1](#results-for-phase-1).

### Blast radius

- Any consumed-but-unanswered request on a provider that polls its own inbox (Claude): every
  recovery the bus has — the stall watchdog, `deliver --force`, the executor's re-drive — hands the
  agent text it cannot answer.
- Graph send nodes fail at their cap on work the agent did. Here that stopped a defect loop twice
  and edit gave up restarting it.
- `diagnose`, per the screenshot, reads the state as healthy.

## Requirements

### Acceptance criteria

- [x] A re-driven request carries its whole payload, not an 80-rune preview
- [x] A re-driven request carries its message id and a ready-to-run reply command addressed to the request's reply target — edit for a `daemon` dispatch, the `reply_role` when one is set — so the agent's reply correlates and completes the task
- [x] On the 2026-11-17 shape — a graph dispatch consumed with a receipt and never answered — the agent's reply to the re-drive completes the node; it does not wait out its cap
- [x] An ordinary wake notification is unchanged: still the short preview, still bounded in length (negative control)
- [x] `muxcode diagnose <role>` reports a role that holds an acked, unanswered in-flight request past the stall threshold, with re-drives already spent — it is not "No issues detected"
- [x] `docs/agent-bus.md` and `docs/architecture.md` describe what a re-drive carries

### Technical approach

Give the re-drive its own text builder instead of the wake notification's: for each task, the
request as `FormatMessage` renders a delivered message — sender, type and action, full payload, and
the `To reply:` line — under the "Re-drive (consumed but never completed)" lead. The reply target
comes from the request's log line where it exists (`ReplyTarget`, as MUX-225's `replyTargetFor`
resolves it), else the normalized sender. Bound the total length the way the injection roads
already do, cutting whole requests rather than the middle of one, and say how many were left out.

For `diagnose`, add a finding for an in-flight task whose request has a receipt, no response, and
age past `TaskStallSecs`, naming the task and how many re-drives it has had.

Phase 1 also records what the commit pane and listener output show for a consumed request that
starts no turn, if it can be reproduced; the cause is not in this spec's criteria.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/deliver.go` | `redriveInFlightTasks`, `RedriveTask`, `redriveMessages`, `redriveText` |
| `tools/muxcode/bus/notify.go` | `BuildCombinedNotification` — to stay as it is for wakes |
| `tools/muxcode/bus/message.go`, `bus/provider.go` | `FormatMessage`, `ReplyTarget`, `buildReplyCommand` |
| `tools/muxcode/bus/reply_to.go` | `replyTargetFor` — a task's reply target from its log line |
| `tools/muxcode/bus/diagnose.go` | The new finding |
| `scripts/test-redrive-reply-route.sh` | New |

## Implementation

### Phase 1: Pin

- [x] Unit test: a re-drive of one consumed graph dispatch with a 300-character payload — today the text is cut at 80 runes, names `daemon`, and holds neither the id nor a reply command (pin, inverted in Phase 2)
- [x] Unit test (negative control, stays green): a wake notification for an unconsumed message is the short preview
- [x] Record in this spec what a consumed request that starts no turn looks like in the pane and the listener's output, if it reproduces; if it does not, say so — **not attempted by the worker, and recorded as such** below. Ticked so the run is not held on work it cannot do. Since then edit has relayed a live observation of the state from `tibo`, also below; the cause is still open and has no spec of its own

### Results for Phase 1

Recorded 2026-11-17 from the worker's request; plan read `redriveNotification` and found both tests
in the tree. Boxes are ticked at `verify-spec`, not here.

**The reproduction was not attempted.** A consumed request that starts no turn has only been seen on
a live Claude agent's listener in the `tibo` session, and a graph worker in this session should not
drive another project's agents. So the third step's answer is not "it does not reproduce" but "it
was not tried": the cause stays **not established**, as under
[Not established](#not-established), and nothing here narrows it.

**Live evidence from `tibo`, relayed by edit at 14:14** (`1794942867-edit-61175b66`), recorded at
the user's request. It is an observation of the live agent, not a reproduction, and it is the first
account of what the no-turn state looks like:

| Step | What edit reports | What the records plan read show |
|------|-------------------|---------------------------------|
| 1 | A **fresh** Claude commit agent's background listener consumed the 13:54 request | `1794941681-edit-48928897`, `acked` at 13:54:44 by listener pid 25643, parent alive; the agent had been reloaded clean at 13:54:36 |
| 2 | The agent then ran `muxcode inbox`, saw it empty, and reported nothing to act on — **it never read the listener's captured output** | Not in any record plan read: the pane and the listener's output file were not read. `log.jsonl` holds no response from commit to that request |
| 3 | The re-drive then arrived truncated with no id, and the loop step waited 600 s | For the two loop dispatches, yes (the timeline above). For this 13:54 request the lifecycle log shows `idle-task-retry` and `idle-task-rescue`, not a stall re-drive, and it was edit's own request, not a loop step |

**What this adds, and what it does not.** It names one concrete path into the state: the listener
does its job — the message is consumed and returned as the background command's output — and the
agent, instead of reading that output, looks in the inbox, which is by then empty. On that reading
the bus reports a true `acked` receipt for a message the model never saw. It is one occurrence of
three, on edit's account of the pane; whether the two loop dispatches went the same way is not
recorded, and why the agent did not read the output is not known. The criteria are unchanged: this
spec still fixes the recovery, not that cause.

**One text builder for both roads.** `redriveInFlightTasks` and `RedriveTask` now call
`redriveNotification` (`bus/deliver.go`) where each built the same string inline. Behaviour is
unchanged — it is the seam Phase 2 replaces.

| Test (`bus/redrive_text_test.go`) | Pins |
|-----------------------------------|------|
| `TestRedriveNotification_CutsPayloadAndOmitsReplyRoute` | Today's defect, on a 300-character graph dispatch: the text is cut at 80 runes ending `--show-current`, names `daemon`, and holds neither the id nor a reply command. Inverted in Phase 2 |
| `TestWakeNotification_StaysAShortPreview` | Negative control: an ordinary wake notification is the short preview, and stays so |

Also run by the worker, not a step of this phase: `scripts/test-bus-dir-leak.sh` 22 passed, 0 failed
(run task `1794942004-spawn-26088880-c7209fac`), its full `./test.sh` runs green.

Verified and ticked 2026-11-17 once all three had run after the last edit (14:02:05): `./build.sh`
exit 0 at 14:03:53, `./test.sh` exit 0 at 14:05:32, review at 14:06:02 with 0 must-fix, 0 should-fix,
0 nits (a static review). The third step is ticked on its annotation, not on a finding: plan's
call, so that an open box does not hold the loop on a reproduction a worker here should not make.

### Phase 2: A re-drive the agent can answer

- [x] Both re-drive roads build their text from the full request: payload, id and a reply command to the request's reply target
- [x] A `daemon` dispatch's reply command addresses edit; a request carrying `reply_role` addresses that role
- [x] The text is bounded by whole requests, with a count of any left out
- [x] Invert the Phase 1 pin; the wake control stays green

### Results for Phase 2

Recorded 2026-11-17 from the worker's request; plan read `redriveNotification` and `redriveBlock`.
Boxes are ticked at `verify-spec`, not here.

**What a re-drive types now** (`redriveNotification(session, msgs)`, `bus/deliver.go`; both roads,
`redriveInFlightTasks` and `RedriveTask`, call it). One line:

```
Re-drive (consumed but never completed) — this request was already read from your inbox, so `muxcode inbox` will not show it; do it and reply with its command: | [<id>] from <sender> [request:<action>]: <full payload> — To reply: <reply command>
```

| Part | Rule | Where |
|------|------|-------|
| Payload | Whole. Newlines are flattened to ` ⏎ `, since a newline typed into the pane would submit the prompt before the reply command | `redriveNewlines` |
| Id | The request's own, at the head of its block and inside the reply command | `redriveBlock` |
| Reply command | `buildReplyCommand` to the request's reply target, read from its log line (`replyTargetFor`): a `daemon` dispatch answers edit, a `reply_role` is honoured; an unknown target falls back to edit | `redriveBlock` |
| Bound | At most `wakeUpMaxMessages` (10) requests and `wakeUpMaxBytes` (4000), cut between whole requests; the first is always shown; `[N more re-driven request(s) not shown — they are re-driven on a later pass]` counts the rest | `redriveNotification` |

`BuildCombinedNotification` is untouched, so an ordinary wake is still the short preview.

**Tests** (`bus/redrive_text_test.go`): the pin inverted,
`TestRedriveNotification_CarriesPayloadIDAndReplyRoute`; `_HonoursReplyRole`;
`_BoundedByWholeRequests`; the wake control `TestWakeNotification_StaysAShortPreview`, green.

Also run by the worker: `scripts/test-bus-dir-leak.sh` 22 passed, 0 failed (run task
`1794942488-spawn-26088880-987fa2e6`), full suite green.

**Plan's notes on the form.**

- The first request is always shown whole, whatever its size, so a single very large payload is
  typed past the 4000-byte bound. Plan did not check what the injection road does with one.
- "Re-driven on a later pass" is a promise the stall watchdog keeps only while it has re-drives
  left — it gives up after two.
- The sender is still printed as written (`from daemon`); it is the reply command, not that label,
  that now carries the route.

Docs (plan): `docs/agent-bus.md` — what a re-drive carries, under `muxcode deliver`;
`docs/architecture.md` — the stall re-drive row.

**One review must-fix after that record** (review of 14:15,
`/tmp/muxcode-review-1794942883.txt`; plan read the finding and the diff of `deliver.go` and
`daemon/daemon.go`). The bound introduced a failure of its own:

| Finding | Fix |
|---------|-----|
| The cap dropped the tail of a batch from the typed text, but `redriveInFlightTasks` still returned and logged every task as delivered. The stall watchdog calls that role-wide road once per stalled task and spends that task's retry first — so with more than 10 pending requests, or two whose blocks pass 4000 bytes together, a task left out of the text could use up both its re-drives while only the leading requests were ever typed. Its row is consumed, so it had no way back: the unanswerable dispatch again, for the tail | The watchdog re-drives through `ForceDeliverFor(session, role, true, <task id>)`, which puts the stalled task **first** (`redriveFirst`) — and the first request is always shown. `redriveNotification` returns how many it showed; that is what is returned and logged (`force-redrive … <shown> of <n> in-flight task(s)`). The trailing note is now `[N more re-driven request(s) not shown]`, without the "later pass" promise plan had questioned |

Tests: `TestForceDeliverFor_CountCapReportsShownAndLeadsWithTheTask`,
`TestForceDeliverFor_ByteCapGivesEachTaskItsOwnAttempt` — through the delivery road, not the
formatter alone.

Still as noted above: a single first request larger than the byte bound is typed whole.

Verified and ticked 2026-11-17 once all three had run after the last edit (14:17:02): `./build.sh`
exit 0 at 14:21:00, `./test.sh` exit 0 at 14:22:40, review at 14:23:09 with 0 must-fix, 0 should-fix,
0 nits. Criteria 1 and 4 are ticked on the unit tests, 6 on the docs, corrected after the must-fix.
Criterion 2 waits for Phase 4: the text carries the id and the command, but that the reply
completes the task is shown only end to end. `scripts/test-bus-dir-leak.sh` was last run before the
must-fix.

### Phase 3: Diagnose sees it

- [x] `diagnose` reports an acked, unanswered in-flight request past the stall threshold, naming the task and its re-drive count
- [x] Negative control: a request answered, or still inside the threshold, raises no finding

### Results for Phase 3

Recorded 2026-11-17 from the worker's request; plan read `checkConsumedUnanswered` and
`countTaskRedrives`. Boxes are ticked at `verify-spec`, not here.

**The finding: `consumed-unanswered`** (`checkConsumedUnanswered`, `bus/diagnose.go`, registered in
`diagnosticChecks`).

| | |
|---|---|
| Fires for | An in-flight request that has a receipt, no answer, and is past the stall threshold (`TaskStalled` — half the threshold for a `daemon` dispatch) |
| Names | The task id, its sender and action, its age, and how many re-drives the lifecycle log shows for it; every such task is listed as evidence |
| Severity | Warning. **Critical only on the stall watchdog's own `task-stall-giveup` row naming the task, written since the daemon last started** — proof that only the task timeout is left to end it. A count of re-drives, however high, stays a warning, and so does a give-up from before the latest daemon `started` row |
| Remediation | `muxcode deliver <role> --force`, which re-drives with the full text, id and reply command; look at the pane; `muxcode tasks` |
| Evidence | `in_flight_tasks` gains `received`, `stalled`, `redrives` and `watchdog_gave_up` — additive in `--json` |

**How re-drives are counted, and what the count is not.** From lifecycle rows that name the task:
`task-stall-redrive`, to which the daemon now appends `(task <id>)`, and the executor's targeted
`force-redrive … task <id>`. A role-wide `force-redrive` row names no task and is not counted, so a
manual `muxcode deliver --force` does not move the count. The finding words it as "re-drive(s)
observed in the lifecycle log", and it never implies a budget is spent.

**That was the review's should-fix** (`/tmp/muxcode-review-1794943938.txt`). The first cut made the
finding critical at a count of 2 and called that the watchdog's cap being spent. But the executor
and the watchdog keep separate budgets, so one of each read as critical with a watchdog attempt
still left; the log outlives a daemon restart, which resets the watchdog's budget; and rotation can
drop earlier rows. The daemon now appends `(task <id>)` to `task-stall-giveup` as well, and only
that row makes the finding critical.

**A second review fix, on the give-up itself.** The watchdog's retry budget lives in daemon memory
and starts afresh on every daemon start, so a give-up row from before a restart says nothing about
the retries the running daemon has left. `watchdogGaveUp` now counts a `task-stall-giveup` row only
if it follows the latest daemon `started` row; an earlier one reads as a warning.

**Tests** (`bus/diagnose_consumed_test.go`): `_ReportsReadUnansweredStalledRequest`;
`_MixedRedrivesWithoutGiveUpWarn` — an executor re-drive and a watchdog re-drive with no give-up
row stay a warning; `_GiveUpScopedToRunningDaemon` — a give-up before a restart is a warning, one
after it critical; `_NegativeControls` — answered, still inside the threshold, and never read.

Also run by the worker: `scripts/test-bus-dir-leak.sh` 22 passed, 0 failed (run task
`1794943554-spawn-26088880-d7f2b9c6`), full suite green.

**Plan's note.** Rows written before today carry no `(task <id>)`, so the count starts at zero for
any task the old daemon re-drove — and a session whose daemon has not been restarted onto this build
keeps writing the old form. For such a task the finding reads as a warning with 0 re-drives however
many it has had — and, since its give-up row names no task either, it can never read as critical.

Docs (plan): `docs/architecture.md` — `consumed-unanswered` in the `diagnose` passage, now eighteen
failure modes; `docs/agent-bus.md` — `muxcode deliver --force` as its recovery; `CLAUDE.md` — the
Agent diagnostics entry, 17 to 18. All three were first written to the count-of-2 rule and
corrected after the should-fix.

Verified and ticked 2026-11-17 once all three had run after the last edit (14:41:35): `./build.sh`
exit 0 at 14:45:52, `./test.sh` exit 0 at 14:47:35, review at 14:48:06 with 0 must-fix, 0 should-fix,
0 nits. Two earlier reviews each carried a should-fix (14:33 and 14:41); a `verify-spec` fired by
the daemon's chain on the first ticked nothing. The worker report in the graph's dispatch is the
original — "critical once 2 are spent" — and is superseded by the table above. Criterion 5 is ticked
on the unit tests: the finding fires for the state, as a warning or critical, where the verdict was
"No issues detected". Phase 4 runs `diagnose` against a live stalled role.


### Phase 4: Integration test

- [x] Create `scripts/test-redrive-reply-route.sh`: a scratch daemon and a one-node graph run whose dispatch a stub agent's listener consumes and does not answer; the re-drive arrives with the full payload, the id and a reply command; the stub runs that command as written and the node completes inside its cap
- [x] Control: a build with the old re-drive text leaves the stub nothing to reply with and the node running
- [x] `diagnose` on the stalled role, before the reply, reports the finding
- [x] Run the script and record pass/fail counts in this spec

### Results for Phase 4

Recorded 2026-11-17 from the worker's request; plan read the script's header and the run's log.
Boxes are ticked at `verify-spec`, not here.

`scripts/test-redrive-reply-route.sh` — hermetic: private tmux server, scratch daemons, muxcode and
`scripts/fixtures/claude-stub` built from the tree. The stub runs a real
`muxcode inbox --poll --loop` listener, which consumes the dispatch; the stub never answers it by
itself. About 5 minutes: the stall watchdog checks every 30 s and re-drives on its second at-rest
sighting.

The fixture gained two opt-in switches: `CLAUDE_STUB_TYPED_LOG` logs every line typed into the stub,
and while `CLAUDE_STUB_REPLY_FILE` holds `1` the stub runs the `To reply:` command a typed line
carries, as written.

**Run: 21 passed, 0 failed, floor 21, exit 0** (run task `1794945088-spawn-26088880-c53686c8`,
requested 14:51:28 after the script's last edit at 14:51:15; the run agent's history row confirms
it, and plan read the log — 21 `ok`, no `FAIL`).

| Section | Shows |
|---------|-------|
| A | A real one-node graph run dispatches a 300-character request to watch. The stub's listener consumes it with a receipt and nothing answers. The stall watchdog re-drives it; the re-drive carries the whole payload, the message id, and a reply command addressed to edit with that id — none addresses `daemon`. `diagnose watch` reports `consumed-unanswered` naming the task, before any reply. With replies held the node stays running; with replies on, the next re-drive's command, run as written, succeeds the node — **121 s after dispatch, inside its 300 s cap** — and the node's output is the stub's reply |
| B | Control: a build whose re-drive is the old wake notification. The re-drive arrives cut short with no reply command, the stub has nothing to run, and the node is still running on an in-flight task, left to its cap |

**What it does not drive.**

- A real agent. The stub runs the reply command mechanically; whether a model that receives the
  re-drive does the work and runs the command is not shown — and on 2026-11-17 the model did the
  work and could not answer, which is the half this fixes.
- The executor's own targeted re-drive (`RedriveTask`). Section A's re-drive is the stall
  watchdog's; both roads share `redriveNotification`, pinned by unit test.
- A request naming a `reply_role` (unit test `_HonoursReplyRole` only), and a batch past the bound
  (unit tests `TestForceDeliverFor_*` only).
- `diagnose` reading **critical**: section A asserts the finding is reported, not its severity.

Verified and ticked 2026-11-17 once all three had run after the last edit (the script at 14:51:15,
`CLAUDE.md` at 14:55:15): `./build.sh` exit 0 at 14:56:10, `./test.sh` exit 0 at 14:56:25, review at
14:57:00 with 0 must-fix, 0 should-fix, 0 nits. Criteria 2 and 3 are ticked on section A — the
re-drive carries the id and a reply command to edit, and the reply, run as written, completes the
node at 121 s of a 300 s cap — with section B as the control.

## Notes

- Filed 2026-11-17 by plan on the user's instruction.
- Related: [MUX-225](../completed/MUX-225-reply-to-unknown-id-accepted-silently.md) — a reply with a wrong id; this is a reply with no id to give. [MUX-196](../completed/MUX-196-agent-launch-expires-in-flight-graph-dispatch.md) — a relaunch clears a consumed dispatch and the executor re-sends it, a different recovery for a neighbouring state. [MUX-112](./MUX-112-idle-task-rescue-closes-live-work.md) — the idle-task rescue that closed the third request here.
- No user decision is open. The listener cause is recorded as not established and left out of the criteria.
- 2026-11-17 14:14 — the user asked for this spec to be prioritized, relayed by edit: it blocks the `tibo` session. It was already the active spec.

## Status

**Complete** — filed, activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked), against the tree at `09260a8`. Uncommitted at close-out; the run's ship step commits it. 19/19 — Phases 1–4 complete and all six criteria met. Filed on the user's instruction, with GitHub issue #231.

A re-drive now types the request itself: its id, its whole payload, and a ready-to-run reply
command addressed to the request's reply target — edit for a `daemon` dispatch. The stall watchdog
re-drives one stalled task at a time with that task first, so the length bound never cuts the task
being retried. `muxcode diagnose` reports the state as `consumed-unanswered`: a warning with the
re-drives seen in the lifecycle log, critical only once the running daemon's watchdog has given up
on the task. Replayed on a graph node, a stub's reply to the re-drive completes the node at 121 s of
a 300 s cap.

**What this does not fix, and nothing yet does.** Why a request the agent's own listener consumed
starts no turn is not established. It was seen three times on the `tibo` commit agent, once after a
clean reload; edit's account of one is that the agent read its inbox, found it empty, and never read
its listener's output. No reproduction was attempted — Phase 1's third step is ticked as not
attempted — and the cause has no spec. The first delivery can still be lost the same way; what
changed is that the recovery can now be answered.

Also for a later reader: no test puts a real agent in front of a re-drive; and a session whose
daemon predates this build names no task in its re-drive or give-up rows, so for it the finding
shows 0 re-drives and never reads critical until its daemon is restarted on the new build.
