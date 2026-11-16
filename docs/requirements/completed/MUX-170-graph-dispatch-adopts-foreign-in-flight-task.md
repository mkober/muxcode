# MUX-170: A Graph Dispatch Adopts a Foreign In-Flight Task That Shares Its Action

**Tracking:** [mkober/muxcode#129](https://github.com/mkober/muxcode/issues/129)

When a graph node's dispatch is suppressed by the bus's in-flight dedup, the executor assumes the
duplicate is its own earlier request — a loop re-entry, or a retry racing the prior pass — and adopts
it. The lookup that finds the duplicate keys on `(to, action)` alone. On 2026-09-09 edit's "STOP the
startup ack loop" request to test (`request:test`, tracked, never answered) was still in flight when
run `d67cd45e`'s test node dispatched `request:test` five minutes later: the send was suppressed, the
node adopted edit's request as its own work, waited on a reply to a message it never sent, and failed
at the task timeout with `no live edge`. No lifecycle row named the suppression or the adoption — five
minutes of silence, then a failed run that `graph retry` cleared at once because the foreign task had
expired by then.

## Context

### Observed (session `muxcode`, run `1788982872-build-test-review-d67cd45e`, 2026-09-09)

| When | What | Source |
|------|------|--------|
| 15:36:30 | edit → test `request:test` "STOP the startup ack loop …" — tracked task `1788982590`; test never answers it | `muxcode history test`, `muxcode tasks` |
| 15:41:12 | run `d67cd45e` created by edit | lifecycle |
| 15:41:35 | build → success; `graph-node-start test (send)` | lifecycle |
| 15:41:35 | the daemon's `request:test` to test is suppressed — `HasInFlightTaskForRole("test","test")` matches the stop task; no `daemon → test` row in test's history | history (absence) |
| 15:41:35 → 15:46:32 | the node holds the adopted task; not one lifecycle row for test in between | lifecycle (absence) |
| 15:46:32 | `task-timeout edit→test:test expired in-flight`; `graph-node-done test -> failure`; `graph-run-failed … node test failed with no live edge` | lifecycle |
| 15:48:30 | `graph retry --from test` — the foreign task has expired, the dispatch lands, test → success 15:50:30 | lifecycle |

### Second case — a worker's own send (session `muxcode`, run `1790641162-80-pr-review-fix-451d8466`, 2026-09-28)

| When | What | Source |
|------|------|--------|
| 20:22:45 | edit → plan `request:update-docs` (file MUX-195) — tracked task `1790641365-edit-2f78075e`, answered 20:25:42 | `tasks/`, `log.jsonl` |
| 20:20:42 → 20:24:42 | fix worker `spawn-db7fd90d` (seed `1790641242-daemon-f7646554`) sends plan `update-docs` for the new `recheck-comments` docs, `--track` | the worker's own report |
| same | the CLI finds edit's task by `(to, action)` and returns *"already tracking"* **without writing a message**; no `spawn-db7fd90d → plan` row exists in `log.jsonl`, and no lifecycle row names the suppression | `log.jsonl` (absence), lifecycle (absence) |
| 20:24:42 | the worker's report claims the delegation: *"The docs update for the new shape went to plan (update-docs, tracked task 1790641365-edit-2f78075e)"* — edit's task id | `1790641482-spawn-db7fd90d-de871482` |
| 20:27:48 | review catches the stale docs (should-fix); the worker's iteration 2 records *"my first docs delegation reattached to an unrelated in-flight plan task and never landed"* | `1790641668-spawn-db7fd90d-914333f4` |

Same shape as the 2026-09-09 case — a foreign in-flight task found by `(to, action)` is treated as
the caller's own — but on a different road: not `dispatchNode`'s adoption but the CLI's pre-send
dedup, `cmd/send.go:184–189` (`--track`) and `:191–197` (`--wait` reattach), which calls the same
`FindInFlightTask`.

**Does the fix have to cover worker sends?** Yes — a worker's delegation is lost silently and the
worker reports someone else's task as its own. But that road is
[MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md)'s, which already names
`cmd/send.go:184-189` and plans to add `from` to the `FindInFlightTask` / `HasInFlightTaskForRole`
key. That one change fixes this case and narrows this spec's trigger; this spec's `dispatchNode`
ownership check alone would **not** have prevented it. The two should ship together (they share
`bus/dedup.go`), and this case is MUX-155's replay test as much as this spec's.

### Mechanism — verified in code

- `bus/graph_exec.go:874–902` `dispatchNode` — on `ErrSendSuppressed` the node "adopts the existing
  work instead of failing": `FindInFlightTask(session, n.Role, n.Action)` and, if found, a task
  record re-keyed to the duplicate's id (`adopted.ID = pm.ID`, `CreateTask`). The comment describes
  the case it was written for — the same request, from the same sender, still in flight.
- `bus/dedup.go:217` `HasInFlightTaskForRole(session, to, action)` and `:235` `FindInFlightTask` —
  both key on `(to, action)`; neither reads `From` or the payload. The doc comment on the finder
  says what it is for: "Used by --wait reattachment". Reattachment semantics — *my* earlier request
  is still open, attach to it — leaked into graph dispatch, where the open request can be anyone's.
- The suppression path writes no lifecycle row and the adoption writes none, so the run's only
  visible events are the node start and, 297 s later, its failure.
- Related but distinct: [MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md) — the send-side
  dedup keying on target rather than sender is why a foreign task could suppress the dispatch at
  all. Fixing MUX-155 narrows the trigger; this spec is about what the executor does when it fires.

### Findings after MUX-155 shipped (2026-11-14)

Re-read by plan against `06a72ed` (MUX-155's commit) on edit's request. The park reason recorded for
this spec — "ships together with MUX-155" — has lapsed. Two decisions now hold it instead.

| Spec says | Tree at `06a72ed` |
|-----------|-------------------|
| A foreign in-flight task (a different `From`) suppresses the dispatch, and the node adopts it | No longer reachable. `HasInFlightTaskForRole` and `FindInFlightTask` match the sender (`bus/dedup.go`), so another sender's task neither suppresses the daemon's dispatch (`bus/inbox.go`, `Send`'s guard) nor is found for adoption (`dispatchNode`, `bus/graph_exec.go:1262–1266`) |
| Criterion 1: such a dispatch stays `ready`, deferred behind the foreign task | The opposite shipped: the dispatch is **delivered** beside the foreign request, pinned by `TestExecDispatchIgnoresForeignInFlightTask` ("the graph's build must be delivered despite edit's task"). On the 2026-09-09 shape the test agent would now hold edit's `request:test` and the run's `request:test` together |
| "Rejected — bypass the dedup": two same-action requests in flight "would both run the suite; the dedup is right to refuse" | That is now the behaviour for requests from different senders. MUX-155 chose it for `notify`; it was not weighed for `test` or `build` |
| The second case (a worker's `--track` send reattaching to edit's task, 2026-09-28) | Fixed by MUX-155: the CLI's pre-send check matches the sender |
| Still open: the same `From`, a different payload | Reachable. Every graph dispatch is sent as `daemon`, so a second run's node — or a loop re-entry carrying a different message — to the same role and action is suppressed by the first's in-flight task and adopts it (`FindInFlightTask(session, n.Role, m.From, n.Action)` compares no payload, run or node). The node then completes on a reply to a request it did not send |
| No lifecycle row on suppression or adoption | Still true for the adoption branch. `graph-dispatch-deferred` now exists (`deferDispatch`, `bus/graph_exec.go:1409`) for a reloading role and a busy worker, and is the row this spec asked for |
| Ownership read from the payload | `Message.GraphRun` and `GraphNode` (`bus/message.go:43–44`) now carry a dispatch's run and node, which this spec predates |

### Findings at activation (2026-11-16)

Re-read by plan against `aca9aba`. The 2026-11-14 findings above still hold; what moved is line
numbers, and the line references in [Mechanism](#mechanism--verified-in-code) are the 2026-09-09 ones.

| Spec says | Tree at `aca9aba` |
|-----------|-------------------|
| `dispatchNode` adoption at `bus/graph_exec.go:874–902` | `dispatchNode` is at `:1240`; the adoption branch is `:1262–1271` (`FindInFlightTask(session, n.Role, m.From, n.Action)`, then `CreateTask` on a copy keyed to the duplicate's id) |
| `HasInFlightTaskForRole` (`:217`), `FindInFlightTask` (`:235`) key on `(to, action)` | `bus/dedup.go:223` and `:243`; both take `from` since MUX-155 |
| `Task` has no run or node field | Still true — `GraphRun` / `GraphNode` exist only on `Message` (`bus/message.go:43–44`) |
| `TestExecDispatchIgnoresForeignInFlightTask` pins Decision 1's behaviour | Present, `bus/graph_exec_test.go:2097` |

### Decisions — the user's, recorded 2026-11-15

Answered by the user in conversation and relayed by edit (`1794787239-edit-1aacf9a4`). The criteria
and phases below were rewritten to them the same day.

| # | Decision | Chosen |
|---|----------|--------|
| 1 | A graph dispatch meeting **another sender's** in-flight request for the same role and action | **(a) Keep what MUX-155 shipped.** The dispatch is delivered beside the other sender's request; the agent may run the suite twice. This spec's foreign-`From` deferral and the 2026-09-09 replay are struck as overtaken by MUX-155. Option (b), restoring the deferral for graph dispatches only, was not taken |
| 2 | What makes an in-flight `daemon` task the node's own | **Run id and node id** (`Message.GraphRun` / `GraphNode`), not payload equality. A `daemon` task from a different run or a different node is foreign: the node stays `ready`, one `graph-dispatch-deferred` row per blocking task, bounded by the node's timeout |

How the check reads ownership — decided by the user 2026-11-16 (relayed by edit,
`1794844857-edit-5f52a782`): `Task` (`bus/task.go`) carries no run or node field today — only the
message does (`bus/message.go:43–44`) — so **the task record gains `GraphRun` and `GraphNode`, set at
dispatch**, and ownership is a direct compare of the two fields. Reading the blocking task's message
instead was the alternative and was not taken. A task with neither field set (any non-graph send, or
a task written before the fields existed) is foreign to every node.

### Scope boundary

Adoption of the node's **own** duplicate stays — the same run and node, a loop re-entry or a retry
racing the prior pass. The send-side dedup is not changed here (MUX-155), and neither is what happens
when the in-flight request is another sender's (Decision 1: delivered beside it). A `daemon` task
from another run or node is neither adopted nor fatal: the node waits.

## Requirements

### Acceptance criteria

Rewritten 2026-11-15 to Decisions 1 and 2. Struck, not carried: ~~a dispatch suppressed by a task
with a different `From` is deferred~~ and ~~the 2026-09-09 shape replayed (a foreign `request:test`
in flight, the run's test node defers)~~ — both overtaken by MUX-155, which delivers the dispatch
beside the other sender's request.

- [x] A dispatch suppressed by an in-flight `daemon` task from a **different run or node** (`GraphRun` / `GraphNode`) is never adopted: the node stays `ready`, a `graph-dispatch-deferred` row names the blocking task once, and the node dispatches on a later tick once that task clears
- [x] The node's own earlier dispatch — the same run and node — is still adopted (negative control — loop re-entry and retry races keep working)
- [x] The wait is bounded by the node's timeout; a node that times out while deferred fails with a reason naming the blocking task — never `no live edge` after silence
- [x] Two runs replayed: run A's `test` dispatch in flight, then run B reaches its own `test` node — B defers with the row, never completes on A's reply, and delivers after A's task completes or expires
- [x] Another sender's in-flight request for the same role and action does **not** defer the dispatch (negative control — Decision 1; `TestExecDispatchIgnoresForeignInFlightTask` keeps passing unchanged)
- [x] Docs: `CLAUDE.md` graph bullet (one clause), `docs/architecture.md` graph section, `docs/agent-bus.md` graph reference

### Technical approach

**Primary — ownership before adoption, deferral otherwise.** In `dispatchNode`, adopt only when the
found task belongs to this run and this node (Decision 2); a `daemon` task carrying another run or
node id, or none, is foreign. Payload is not compared — a loop re-entry may legitimately carry a
different message for the same node. A foreign task leaves the node `ready` and logs
`graph-dispatch-deferred` (`<node>: waiting for in-flight <id> <from>→<to>:<action>`) exactly once
per blocking task id; later ticks retry the send, and the node's timeout clock still runs so the
wait is bounded — on expiry the failure reason carries the task id.

**Rejected — fail the node on a foreign task.** Loses a run to a transient; the foreign task will
clear or expire within its own timeout, and a bounded wait costs nothing.

**Rejected — bypass the dedup for graph dispatches (`SendForce`).** Two identical `request:test`
messages in flight would both run the suite; the dedup is right to refuse, the executor is wrong to
claim what it refused. *This still holds between two `daemon` dispatches. Between different senders
the user accepted the double run (Decision 1) — that is MUX-155's behaviour, not a bypass added
here.*

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | `dispatchNode` (1240) and its adoption branch (1262–1271); the deferred state and its row (`deferDispatch`) |
| `tools/muxcode/bus/dedup.go` | `HasInFlightTaskForRole` (223), `FindInFlightTask` (243) — the `(to, from, action)` key |
| `tools/muxcode/bus/task.go` | `Task` gains `GraphRun` / `GraphNode`, set at dispatch — what the ownership check compares |
| `tools/muxcode/bus/phase_check_test.go`, `tools/muxcode/bus/graph_exec_test.go` | executor tests |
| `CLAUDE.md`, `docs/architecture.md`, `docs/agent-bus.md` | graph bullet, section, reference |

## Implementation

### Phase 1: Ownership, deferral, the row

- [x] `dispatchNode`: adopt only an in-flight `daemon` task whose run id and node id are this node's (Decision 2 — the `Task` record gains `GraphRun` and `GraphNode`, set where the dispatch's task is created, and the check is a direct compare of the two; a task with neither set is foreign); a task from another run or node leaves the node `ready` and logs `graph-dispatch-deferred` once per blocking task id
- [x] A deferred node that reaches its timeout fails with a reason naming the blocking task
- [x] Tests: another run's task → deferred, no adoption, dispatches after the task completes; another node of the same run → deferred; the node's own duplicate → adopted (negative control); another sender's task → delivered beside it, no deferral (negative control, Decision 1); the row is emitted exactly once across repeated ticks

### Results for Phase 1

Recorded 2026-11-16 from the worker's report; the diff and the review notes were read by plan.
Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit (12:07:41):
`./build.sh` exit 0 at 12:11:16, `./test.sh` exit 0 at 12:12:25 (both modules passed), review at
12:13:02 with 0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794849150.txt`). An earlier
review of this phase found the timeout was checked only while still blocked; that finding is resolved
(the deadline row below).

| Item | Result |
|------|--------|
| The task record | `Task` gains `GraphRun` / `GraphNode`, copied by `CreateTask` from the dispatch message; `Task.ownedByNode(run, node)` is true only when both match and the run is non-empty — a task with neither field belongs to no node (`bus/task.go`) |
| Adoption | `dispatchNode` adopts an in-flight `daemon` task only when `ownedByNode`; otherwise `deferBehindForeignDispatch` keeps the node `ready` with `graph-dispatch-deferred` — `<node>: waiting for in-flight <id> <from>→<to>:<action>` — once per blocking task (`bus/graph_exec.go`) |
| The deadline | `GraphNodeStatus.DeferredSince` (`bus/graph_run.go`) is stamped when a foreign hold begins; a change of blocker keeps it. `foreignHoldExpired` runs **before any send** and fails the node `timed out after <n>s deferred behind <blocker>, another run's or node's request` — so a blocker that clears after the deadline fails the node rather than releasing it into fresh work. A reload hold carries no such deadline |
| Kept, and why | A **queued** duplicate with no in-flight task is still adopted whichever node queued it: nobody else waits on its id, and `110-pr-merge`'s `recheck-comments` repeats `read-comments`' message and relies on it. This is outside Decision 2's wording, which speaks of in-flight tasks |
| Tests | `bus/graph_exec_test.go`: `TestExecDispatchDefersBehindAnotherRunsTask` (the two-run replay), `TestExecDispatchDefersBehindForeignDaemonDuplicate` (another node; a task with no fields), `TestExecForeignDeferralTimesOutNamingTheTask`, `TestExecForeignHoldDeadlineCheckedBeforeSend`. The own-adoption control and `TestExecDispatchIgnoresForeignInFlightTask` are unchanged |
| Runs | Run task `1794848457-spawn-926e95e5-e877382c`: 13 passed, 0 failed. After the review fix, proc `1794848898-proc-96763514`: 21 passed, 0 failed (expected 21) — the focused tests three times over, full `bus`/`cmd`/`daemon` passing; with the ownership check off the new tests fail and the controls pass, and with the pre-send deadline check off the expired-release and still-blocked timeout cases fail while the inside-deadline and reload controls pass |

### Phase 2: Docs

- [x] `CLAUDE.md` graph bullet clause; `docs/architecture.md` graph section; `docs/agent-bus.md` graph reference

### Results for Phase 2

Written 2026-11-16. Verified and ticked the same day once all three had run after the last doc edit
(12:14:46): `./build.sh` exit 0 at 12:15:25, `./test.sh` exit 0 at 12:15:41, review at 12:16:14 with
0 must-fix, 0 should-fix, 0 nits (`/tmp/muxcode-review-1794849345.txt`).

| Doc | What was added |
|-----|----------------|
| `CLAUDE.md` | Graph bullet: an "Adoption is by ownership" clause (the worker's edit) |
| `docs/architecture.md` | Graph orchestration: "A suppressed dispatch is adopted only by its owner" — the four cases as a table, and the deadline rule (plan) |
| `docs/agent-bus.md` | `muxcode graph`: "A node waiting on another run's request" — what an operator sees in each case (plan) |
| Noted while writing | `graph status` and the graph TUI do not render a deferred node's reason: `DeferredOn` is read only in `bus/graph_exec.go` and `bus/graph_run.go`, so the hold is visible in the lifecycle log and `nodes/<id>.json` alone. Not in this spec's scope; the same is true of the MUX-217 reload hold |

### Phase 3: Integration test

- [x] Hermetic section (scratch `BUS_SESSION`, two `build-test-review` runs with stubbed agents): hold run A's `test` dispatch unanswered, bring run B to its `test` node, assert B's node is `ready` with the `graph-dispatch-deferred` row naming A's task and no second `daemon → test` row; answer A's task → A's node completes, B's dispatch lands on the next tick and B completes only on its own reply
- [x] Negative control: no other run → the test node dispatches on the first tick with no deferred row
- [x] Negative control (Decision 1): an in-flight `request:test` from edit → the run's dispatch is delivered beside it with no deferred row
- [x] Run the section and record the counts in this spec

### Results for Phase 3

Recorded 2026-11-16 from the worker's request; the script's header and both run logs were read by
plan. Uncommitted. Verified and ticked 2026-11-16 once all three had run after the last edit
(12:21:40): `./build.sh` exit 0 at 12:22:41, `./test.sh` exit 0 at 12:22:57, review at 12:23:40 with
0 must-fix, 0 should-fix, 1 nit (`/tmp/muxcode-review-1794849781.txt`) — the nit was against this
table's last row and is fixed there.

| Item | Result |
|------|--------|
| The script | `scripts/test-graph-foreign-dispatch.sh` — a script of its own rather than a section of an existing one. Builds muxcode from the tree (`MUXCODE_GRAPH_FOREIGN_BIN` overrides), scratch HOME, state, config and repo, a scratch daemon per section pair on a **private tmux server**; two real `30-build-test-review` runs, with the script playing build, test and review over the bus (replies carry `EXIT=0`; no hooks run) |
| A — negative control | No other run: the test node dispatches as soon as build answers, with no `graph-dispatch-deferred` row |
| B — two runs | With run A's `test` dispatch held unanswered, run B reaches its `test` node and stays `ready`, deferred on A's task id, with one row naming it across several ticks and no second `daemon → test` request. Answering A's test releases B, whose own request lands under its own task id; A's reply never completes B, which completes only on its own |
| C — negative control (Decision 1) | edit's in-flight `request:test` does not defer the run: the dispatch is delivered beside it, no deferred row, edit's task left in flight |
| D — control | A muxcode built from a copy of the tree with the ownership check removed fails B: B's node takes A's task id with no deferred row and is done on A's reply |
| Run 1 | Proc `1794849542-proc-bec4ec65`: 22 passed, 2 failed, exit 1 — a fault in the script's stand-in review replies, which lacked the findings count line the review verdict reads; not a product failure |
| Run 2 | Proc `1794849650-proc-afc6d7d5` (run task `1794849643-spawn-926e95e5-2bc10f87`): 24 passed, 0 failed, floor 24, exit 0 |
| Not covered anywhere | "Delivers after A's task … expires" — the script releases B by answering A, not by letting A's task expire, and no unit test expires the blocker either. Phase 1's tests cover the *waiting node's* timeout (`TestExecForeignDeferralTimesOutNamingTheTask`) and the blocker *completing* inside and after the deadline (`TestExecForeignHoldDeadlineCheckedBeforeSend`). Release on expiry rests on reading the code: an expired task is no longer in flight, so the dedup stops suppressing the send, the same road as a completed one. Corrected 2026-11-16 after review's nit that this row first claimed expiry was unit-tested |

## Notes

- Filed 2026-09-09 16:02 by plan from edit's handoff (`mux-170-handoff.md`, session `03a897a2`),
  the timeline re-verified against the lifecycle log, `muxcode history test` and the code lines
  above.
- Second case added 2026-09-28 on the user's instruction relayed by edit, verified against the
  worker's two reports, `log.jsonl` and `cmd/send.go:177–198`.
- Related: [MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md) (the trigger);
  [MUX-171](./MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) (found in the same hour, the
  other way a daemon mechanism written for one case fires on another);
  [MUX-112](../backlog/MUX-112-idle-task-rescue-closes-live-work.md) and
  [MUX-148](./MUX-148-node-outcome-reads-command-ran-as-task-done.md) (the task-correlation family).

## Status

**Complete** — activated and closed 2026-11-16 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 6 then; ranks 1–5 were parked). 14/14: Phases 1–3 complete and every criterion met. Uncommitted at close-out; the run's ship step commits it. One part of the two-run criterion — B delivering after A's task *expires* rather than completes — is not exercised by any test (see Results for Phase 3).

History: filed 2026-09-09 16:02. Parked 2026-11-13; unparked 2026-11-15 once the user's Decisions 1 and 2 were recorded and the criteria and phases rewritten to them (12 items became 14: one criterion struck, the Decision 1 control added as a criterion and an integration step, a two-run replay in place of the 2026-09-09 one).
