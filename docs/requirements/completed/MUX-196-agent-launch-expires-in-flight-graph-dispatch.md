# MUX-196: Relaunching an Agent Expires Its In-Flight Graph Dispatch

**Tracking:** [mkober/muxcode#98](https://github.com/mkober/muxcode/issues/98)

Every agent launch times out **every** in-flight task addressed to the role, on the premise that a
fresh instance cannot be working on a task delivered to the previous one. The premise fails for a
request that was never consumed: it is still in the inbox, and the relaunched agent will read and
answer it. The graph executor reads the forced `timed-out` status as the node's own timeout and fails
the node on the spot — overriding the node's `timeout_secs` — so a run fails on a dispatch that is
seconds away from being answered.

## Context

### Source and standard of evidence

Filed 2026-09-28 on the user's instruction relayed by edit (brief
`/tmp/muxcode-reload-expires-dispatch.md`). Timeline read by plan from the lifecycle log, the task
record and `graph status`; mechanism read from code at `14f3a73` (main after PR #96).

### Observed (run `1790641898-110-pr-merge-6fba807b`, PR #96)

| When (unix) | What | Source |
|------|------|--------|
| 1790641971 | `ci-watch` (send, role `watch`, `timeout_secs: 3600`) dispatches task `1790641971-daemon-a4b22695` | task record |
| → | the watch agent is down (its provider, opencode, not on PATH); the request sits unconsumed in its inbox | brief |
| 1790642220 | `muxcode reload watch --cli claude`: `launch role=watch cli=claude`, then **`cleared-inflight-tasks — watch: 1 stale in-flight task(s) cleared on launch`** | lifecycle (`source: launch`) |
| 1790642221 | `graph-node-done ci-watch -> failure` (output `task timed-out`), `graph-run-failed … node ci-watch failed with no live edge` — 250 s into a 3600 s allowance | lifecycle |
| 1790642235 | `force-deliver watch: 7 messages` — the same request reaches the relaunched agent | lifecycle |
| 1790642302 | `task-late-response daemon→watch:watch completed after timeout (response: 1790642301-watch-4dde2154)` — `CI-GREEN`, answered to the task the launch expired | lifecycle, task record (`status: completed`) |
| later | `muxcode graph retry <run> --from ci-watch` — the run then completed through `merge` | brief, `graph status` |

### Mechanism — verified

| Fact | Where |
|------|-------|
| Every launch calls `ClearInFlightTasksForRole` before exec | `RunAgentLaunchResume`, `bus/launch.go:998`, call at `:1034` |
| It times out every in-flight task whose target shares the role's window — no check of whether the request was ever consumed | `bus/task.go:113–127` (`TimeoutTask`) |
| Its stated purpose: a task left by a crashed agent blocks every new send of the same `(to, action)` via the dedup guard until the 600 s grace | comment at `bus/launch.go:1027–1032` |
| The executor treats `TaskTimedOut` as terminal and fails the node with `"task timed-out"` — the node's `timeout_secs` is never consulted on this branch | `bus/graph_exec.go:1378–1379` |
| A late reply still completes the task (`task-late-response`), but nothing re-opens the finished node or the failed run | `daemon/daemon.go:2758–2806` |
| Every relaunch road goes through the same launch: `muxcode reload`, stuck-provider reload, health restart, `muxcode resume` | `RunAgentLaunchResume` is the `agent launch` entry |

The output names the clock, not the cause: the node reads *"task timed-out"* at the instant of a
reload, 250 s into an hour. The same misattribution CLAUDE.md records for the codex-approval
watchdog — a node recorded `timed-out`, *"naming the clock instead of the cause"*.

### Blast radius

- Any graph send node whose target agent is relaunched while the request is still unconsumed — the
  normal recovery for a dead or stuck agent is exactly that relaunch, so the fix for the stall is
  what fails the run.
- Long-timeout nodes are hit hardest (`ci-watch` 3600 s, `60-integration-suite`'s run nodes).
- Non-graph tracked tasks lose their `--wait`/`--track` wake too, though the late reply is recorded.

## Requirements

### Acceptance criteria

- [x] A launch does not time out a task whose request is still **unconsumed** in the role's inbox (no receipt): the task stays in flight on its own clock and the relaunched agent's reply completes it
- [x] A task whose request **was** consumed by the prior instance is still cleared on launch (the stale-dedup purpose of the clearing is kept) — negative control
- [x] A graph send node whose task is cleared by a launch records the cause — `agent relaunched` with the role and the clearing — not `task timed-out`, and the node is re-dispatched or held rather than failed while its own `timeout_secs` has not elapsed
- [x] A late reply to a task a launch cleared, arriving inside the node's `timeout_secs`, completes the node (or the node never finished, per the first criterion) — the run is not failed by a reply that satisfies it
- [x] A genuinely unanswered dispatch still fails at its own `timeout_secs` — negative control
- [x] The clearing writes one lifecycle row per task it clears, naming the task id and whether its request had been consumed
- [x] Every relaunch road is covered: `muxcode reload`, stuck-provider reload, health restart, `muxcode resume`
- [x] `bash scripts/test-reload-keeps-dispatch.sh` passes

What each tick rests on is tabled under [Results for Phase 3](#results-for-phase-3) — three of them
in part on unit tests or a reading of the code rather than the integration run.

### Technical approach

In `ClearInFlightTasksForRole`, clear only tasks whose request has a receipt (`hasReceipt`: acked,
delivered or responded) — the prior instance consumed it and cannot answer it now. A task with no
receipt is still in the inbox and will be delivered to the new instance; leave it on its own clock.
In the executor, keep the `TaskTimedOut` branch for the task's own expiry, and give a launch-cleared
task a distinct cause so the node can be re-dispatched (it is an executor-owned send) instead of
failed. Neither change touches the dedup guard itself
([MUX-155](../completed/MUX-155-send-dedup-keys-on-target-not-sender.md)).

### Key files

| File | Role |
|------|------|
| `tools/muxcode/bus/launch.go:998`, `:1027–1037` | the launch-time clearing and its rationale |
| `tools/muxcode/bus/task.go:74–127` | `TimeoutTask`, `expireTask`, `ClearInFlightTasksForRole` |
| `tools/muxcode/bus/delivery.go` | `hasReceipt` — consumed vs unconsumed |
| `tools/muxcode/bus/graph_exec.go:1368–1386` | the send node's task-status switch |
| `tools/muxcode/daemon/daemon.go:2758–2806` | late responses to timed-out tasks |
| `scripts/test-reload-keeps-dispatch.sh` | new |

## Implementation

### Phase 1: Pin

- [x] Unit test: an in-flight task with no receipt, then `ClearInFlightTasksForRole` → today timed-out (pin red, inverted in Phase 2)
- [x] Unit test: a graph send node whose task turns `timed-out` 250 s into a 3600 s `timeout_secs` → today fails with `task timed-out` (pin red)
- [x] Unit test (negative control, stays green): an in-flight task whose request was acked is cleared on launch

### Results for Phase 1

| Step | Test | What it pins |
|------|------|--------------|
| 1 | `TestClearInFlightTasksForRole_ExpiresUnconsumedRequestToday` (`bus/task_test.go`) | A request with no receipt, sitting in watch's inbox: the launch times its task out. Inverted in Phase 2 |
| 2 | `TestExecRelaunchClearedDispatchFailsNodeToday` (`bus/graph_exec_test.go`) | A 3600 s send node whose dispatch a relaunch clears 250 s in fails at once, done/failure. The output asserted is today's — "into its 3600s allowance", naming the relaunch (MUX-217) — not the `task timed-out` this step was written against in September |
| 3 | `TestClearInFlightTasksForRole_ClearsConsumedRequest` (`bus/task_test.go`) | Negative control: an acked request's task is still cleared, `ClearedBy` names the relaunch and sends are unblocked |

The pins assert today's behaviour, so the suite stays green; helper `sendTracked`. The step-2 fixture
writes a receipt for its dispatch, so it pins the **consumed** case's node failure — Phase 2 decides
whether that node is re-dispatched or held, and inverts it accordingly.

For Phase 2, from the worker: four existing tests clear receipt-less tasks and will need a receipt
once unconsumed tasks are kept — `UnblocksSendsAfterRestart`, `LeavesOtherRolesAlone`,
`ClearsHostedRole` and `TestExecClearedDispatchNamesTheRelaunch`.

Verified and ticked 2026-11-17 once all three had run after the last edit (10:09:16): `./build.sh`
exit 0 at 10:10:01, `./test.sh` exit 0 at 10:11:46, review at 10:12:34 with 0 must-fix, 0 should-fix,
0 nits.

### Phase 2: Keep unconsumed dispatches

- [x] `ClearInFlightTasksForRole` skips tasks without a receipt; one lifecycle row per cleared task (id, consumed yes/no)
- [x] Executor: a launch-cleared task is re-dispatched or held with cause `agent relaunched`, never `task timed-out`, while `timeout_secs` has not elapsed
- [x] Invert the Phase 1 pins; the negative control stays green

### Results for Phase 2

Recorded 2026-11-17 from the worker's request; plan read `ClearInFlightTasksForRole` and
`continueAfterRelaunch`. Boxes are ticked at `verify-spec`, not here.

**The third criterion's choice — "re-dispatched or held" — was the worker's: re-sent, except commits, tracker writes and deploys.**

| Case | What happens | Row |
|------|--------------|-----|
| Request has no addressee receipt and still sits in the inbox | The launch keeps the task in flight | `inflight-task-kept` (`consumed=no`) |
| Request has a receipt | The launch clears it, cause recorded | `inflight-task-cleared` (`consumed=yes`) |
| No receipt, but gone from the inbox | Cleared the same way | `inflight-task-cleared` (`consumed=no, its request is gone from the inbox`) |
| A graph send node's dispatch is cleared inside its `timeout_secs` | The executor **re-sends** it on the allowance left, after any live reload marker clears | `graph-dispatch-resent` |
| The same, for a commit action other than `pr-read`, a `jira-write`/`confluence-write`/`issue-write`, or a deploy node (`nodeRequiresGate(n) \|\| isDeployNode(n)`) | **Held** for a late answer, never re-sent — the dead instance may have half-run it | `graph-dispatch-held` |
| A late answer to the cleared dispatch | Finishes the node; the unread re-send is expired and withdrawn | — |
| Past `timeout_secs` | The node fails, naming the relaunch | — |

`scripts/test-reload-defers-dispatch.sh` (MUX-217): its pre-fix copy now also patches out the keep and
the re-send, so its control still reproduces the old failure — 46 passed, 0 failed (run task
`1794928730-spawn-926e95e5-58ba5429`).

**Two things plan noted reading the first cut, both acted on by the worker the same morning.**

- **A re-send repeats work the prior instance may have done.** The first cut held git-mutating
  actions only, so a deploy or a Jira write sent as a graph node would have been re-sent. The hold
  now uses the validator's gate predicate plus deploy (`nodeRequiresGate(n) || isDeployNode(n)`).
  Everything else — a build, a test run, a watch, a review, a `pr-read` — is re-sent and so may run
  twice, which is the point of the fix. Vet clean (run task `1794928943-spawn-926e95e5-0b86d7d7`);
  no test names the widened set yet.
- **The third row.** The comment on `continueAfterRelaunch` said only a consumed dispatch is ever
  cleared; it now says consumed, or gone unread from the inbox.

**Tests.** Pins inverted: `TestClearInFlightTasksForRole_KeepsUnconsumedRequest`,
`TestExecRelaunchClearedDispatchIsResent`. Negative control kept:
`TestClearInFlightTasksForRole_ClearsConsumedRequest`. New:
`TestClearInFlightTasksForRole_CCReadIsNotConsumption` (edit reading its auto-CC copy does not make
the request consumed), `TestExecLateAnswerToClearedDispatchFinishesNode`,
`TestContinueAfterRelaunchHoldsMutations` (the held set). MUX-217's
`TestExecClearedDispatchNamesTheRelaunch` now needs a receipt and a spent allowance.

**Two review fixes before the clean pass** (from the review's summary; plan did not read them in
code): the inbox check and the clearing run under the inbox's partial-consume lock, a read error
counting as "not provably waiting"; and a dispatch cleared twice has a completed earlier answer
harvested before it is retried.

Corrected at Phase 3's verification, when plan read `clearUnlessQueued`: a lock or read failure
**keeps** every receipt-less task (`inflight-task-kept` at `warn`, naming the error) — it is absence
from the inbox that must be proven, not presence. The sentence above had it backwards.

Verified and ticked 2026-11-17 once all three had run after the last edit (10:33:41): `./build.sh`
exit 0 at 10:34:35, `./test.sh` exit 0 at 10:36:13, review at 10:36:56 with 0 must-fix, 0 should-fix,
0 nits (`/tmp/muxcode-review-1794929778.txt`). The acceptance criteria are left for Phase 3, which
proves them end to end; the 46/46 script run predates the widened hold.

Docs (plan): `docs/architecture.md` — "What a launch clears, and what the executor does about it",
under the reload rules; `docs/agent-bus.md` — four rows in the lifecycle event catalog.

### Phase 3: Integration test

- [x] Create `scripts/test-reload-keeps-dispatch.sh` — hermetic scratch daemon, a graph send node to a stub role
- [x] Test: relaunch the role mid-dispatch with the request unconsumed, reply after relaunch → node `success`, run not failed, no `task timed-out`
- [x] **Negative control:** a dispatch never answered still fails at its own `timeout_secs`
- [x] Test: a consumed-then-orphaned task is still cleared on launch, with its row
- [x] Coverage floor; run and record counts here

### Results for Phase 3

Recorded 2026-11-17 from the worker's request; plan read the script's header and the run agent's
history rows. Boxes are ticked at `verify-spec`, not here.

`scripts/test-reload-keeps-dispatch.sh` — hermetic: scratch bus, private tmux server, scratch daemon,
muxcode and the claude-stub built from the tree. The stub runs a real `inbox --poll --loop` listener;
SIGSTOPping it leaves a dispatch unread with the agent up. About 3 min.

**Run: 60 passed, 0 failed, floor 60, exit 0** (run task `1794930681-spawn-926e95e5-36808264`; the run
agent's history row confirms it).

| Section | Road | Shows |
|---------|------|-------|
| A | `muxcode reload`, dispatch unread | `inflight-task-kept` names it `consumed=no`, no row clears it, the relaunched agent reads it with the task still in flight, its answer succeeds the node and the run completes |
| B | The daemon's health restart of a crashed (SIGTERMed) agent | The same |
| C | `muxcode resume --force` | The same |
| D | A dispatch the live agent consumed and never answered | Cleared on reload (`inflight-task-cleared consumed=yes`), re-sent (`graph-dispatch-resent`); the answer to the re-send succeeds the node |
| E | Negative control | A kept dispatch nobody answers fails at its own 25 s `timeout_secs` — not before, not on the relaunch |
| F | Control: a scratch copy of the tree with the keep and re-send patched out | Reproduces 2026-09-28 — the reload clears the unread dispatch and the node fails naming the relaunch |

Two earlier runs failed, each on the script rather than the fix: 38/60 (B used SIGKILL, which left
the stub's `❯` in the pane, read as a live agent) and 59/60 (C asserted an `agent-resume` row, which
a manual resume writes only when it scraped a session id; it now asserts the launch's `reason=resume`).

**What the script does not drive — for the verifier to weigh against the criteria.**

- **Stuck-provider reload** is not its own section. The worker's ground: it calls `ReloadAgent`,
  section A's road. That is an argument from the code, not a run.
- **The held set** — a commit, tracker-write or deploy node cleared by a relaunch and held, not
  re-sent — is covered by the unit test `TestContinueAfterRelaunchHoldsMutations` only.
- **A late answer to the cleared dispatch itself** (the fourth criterion's first reading) is
  covered by the unit test `TestExecLateAnswerToClearedDispatchFinishesNode` only; section D answers
  the re-send.

**One review fix after that run.** The review of 11:01 returned 0 must-fix, 1 should-fix
(`/tmp/muxcode-review-1794931246.txt`): a node's explicit `timeout_secs` deadline is checked first and
failed a held, launch-cleared dispatch with a bare `node timeout`, losing the relaunch. Fixed by
`explicitTimeoutReason` (`bus/graph_exec.go`), pinned by `TestExecExplicitTimeoutKeepsRelaunchCause`
with an ordinary unanswered dispatch as its control. Last code edit 11:02:39.

**Verified and ticked 2026-11-17**, every piece of evidence after that edit:

| Evidence | When | Result |
|----------|------|--------|
| `bash scripts/test-reload-keeps-dispatch.sh` (log born 11:02:53, so it built the final tree) | ended 11:04:20 | 60 passed, 0 failed, floor 60, exit 0 — plan read the log: 60 `ok`, no `FAIL` |
| `./build.sh` | 11:05:02 | exit 0 |
| `./test.sh` (hook row) | 11:06:40 | exit 0, `modules=2 ran=2 passed=2 failed=0 never-executed=0` |
| Review | 11:07:20 | 0 must-fix, 0 should-fix, 0 nits; the prior finding resolved |

What each acceptance criterion rests on:

| # | Criterion | Evidence |
|---|-----------|----------|
| 1 | Unconsumed request kept, the relaunched agent's reply completes it | Sections A, B, C |
| 2 | Consumed request still cleared | Section D; `TestClearInFlightTasksForRole_ClearsConsumedRequest` |
| 3 | Cause recorded, node re-dispatched or held | Re-dispatch: section D's `graph-dispatch-resent` row naming the run and `agent relaunched: watch relaunched (reason=reload)`. **Held: unit test only** (`TestContinueAfterRelaunchHoldsMutations`) |
| 4 | Late reply completes the node | The parenthetical reading — the node never finished — by sections A–C. **A late answer to a cleared dispatch itself: unit test only** (`TestExecLateAnswerToClearedDispatchFinishesNode`) |
| 5 | Unanswered dispatch fails at its own `timeout_secs` | Section E: failed after its 25 s, output `node timeout`, not the relaunch |
| 6 | One row per task, id and consumed | Sections A and D assert `inflight-task-kept … consumed=no` and `inflight-task-cleared … consumed=yes` by task id |
| 7 | Every relaunch road | `muxcode reload` A, health restart B, `muxcode resume` C. **Stuck-provider reload: by code, not a run** — plan read it: `daemon.go:1328` calls `ReloadAgent`, which sends the same `AgentLaunchCommand`, and `ClearInFlightTasksForRole` has one production caller (`launch.go:1094`). The script sets `MUXCODE_STUCK_RELOAD_DISABLE=1` |
| 8 | The script passes | The run above |

Docs (plan), written after the review: `docs/architecture.md` — the unreadable-inbox row and the
explicit-deadline sentence; `docs/agent-bus.md` — the `warn` form of `inflight-task-kept`.

## Related

- [MUX-170](../completed/MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) and
  [MUX-155](../completed/MUX-155-send-dedup-keys-on-target-not-sender.md) — the `(to, action)` dedup the
  launch-time clearing was written to unblock; narrowing that key reduces the pressure to clear at all.
- [MUX-162](../completed/MUX-162-pr-review-fix-node-600s-send-cap.md) — another node failed by a clock rather
  than by its work.
- [MUX-008](../backlog/MUX-008-unverified-daemon-auto-restart.md) — the restart roads this spec must cover.

### Findings at activation (2026-11-17)

Checked against the tree at `a15a110`. The defect stands; the line numbers above are stale.

| Spec says | Tree today |
|-----------|------------|
| `ClearInFlightTasksForRole` at `bus/task.go:113–127` clears every in-flight task for the role | Still does, now at `bus/task.go:136` with a `cause` argument (`expireTaskBecause`); no receipt check |
| The launch calls it at `bus/launch.go:1034` | Now `bus/launch.go:1099`, passing "`<role>` relaunched (reason=`<r>`)" |
| The executor fails the node with `task timed-out` | It names the relaunch from `Task.ClearedBy` (`bus/graph_exec.go:1475`, MUX-217) — and still fails the node |
| One lifecycle row per cleared task | Not there: one `cleared-inflight-tasks` row with a count |
| `scripts/test-reload-keeps-dispatch.sh` | Does not exist. `scripts/test-reload-defers-dispatch.sh` (MUX-217) covers the reload-marker hold, and pins today's clearing as its pre-fix control |

Two things for the worker. The third criterion's "re-dispatched or held" is left to the worker, and
the choice made is recorded under Phase 2's results. And the task's receipt is the addressee's
(MUX-216), so an auto-CC copy consumed by edit does not make a request "consumed".

## Status

**Complete** — activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). Uncommitted at close-out; the run's ship step commits it. 19/19 — Phases 1–3 complete and all eight criteria met.

A launch now keeps an in-flight task whose request has no receipt and still waits in the role's
inbox (`inflight-task-kept`), and clears the rest with the relaunch as the cause
(`inflight-task-cleared`). A graph send node whose dispatch was cleared is re-sent on the allowance
left (`graph-dispatch-resent`) — or, for a commit, tracker write or deploy, held for a late answer
(`graph-dispatch-held`) — and fails only past its own `timeout_secs`, naming the relaunch. Three
criteria rest in part on unit tests or a reading of the code rather than the integration run; they
are tabled under [Results for Phase 3](#results-for-phase-3).

Filed 2026-09-28 on the user's instruction relayed by edit; timeline and mechanism verified the same
day.

**2026-11-13 — partly overtaken by [MUX-217](../completed/MUX-217-agent-defs-reload-races-a-graph-dispatch-and-clears-it.md)**,
a second occurrence on the daemon's own watchdog reload. Nothing is ticked here, and the line
numbers above predate it. What MUX-217 shipped that bears on this spec:

- A launch-cleared task records its cause (`Task.ClearedBy`, "`<role>` relaunched
  (reason=`<r>`)"), and the node's outcome reads "dispatch cleared by a relaunch — …" rather than
  `task timed-out`. That is the first half of this spec's third acceptance criterion; the node is
  still failed, not re-dispatched or held.
- The executor holds a send node while its role carries a live reload marker, which covers a
  dispatch issued during a `muxcode reload` or health restart. It does not cover this spec's
  observed case — a request already sitting unconsumed in the inbox when the relaunch happens.

Still this spec's: a launch keeping a task whose request was never consumed, the late reply
completing the node, the per-task lifecycle row, and `scripts/test-reload-keeps-dispatch.sh`.
