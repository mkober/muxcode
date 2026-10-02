# A Graph Dispatch Adopts a Foreign In-Flight Task That Shares Its Action

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

### Scope boundary

Adoption of the daemon's **own** duplicate stays — loop re-entry and the retry race are real and the
mechanism is right for them. The send-side dedup is not changed here (MUX-155). A foreign task is
neither adopted nor fatal: the node waits.

## Requirements

### Acceptance criteria

- [ ] A dispatch suppressed by a **foreign** in-flight task (a different `From`, or the same `From` with a different payload) is never adopted: the node stays `ready`, a `graph-dispatch-deferred` row names the blocking task once, and the node dispatches on a later tick once that task clears
- [ ] The daemon's own identical earlier dispatch is still adopted (negative control — loop re-entry and retry races keep working)
- [ ] The wait is bounded by the node's timeout; a node that times out while deferred fails with a reason naming the blocking task — never `no live edge` after silence
- [ ] The 2026-09-09 shape replayed: a foreign `request:test` in flight, then a `build-test-review` run — the test node defers with the row and delivers after the foreign task completes or expires
- [ ] Docs: `CLAUDE.md` graph bullet (one clause), `docs/architecture.md` graph section, `docs/agent-bus.md` graph reference

### Technical approach

**Primary — ownership before adoption, deferral otherwise.** In `dispatchNode`, adopt only when the
found task's `From` is the executor's own sender identity and its payload equals the interpolated
message; anything else is foreign. A foreign task leaves the node `ready` and logs
`graph-dispatch-deferred` (`<node>: waiting for in-flight <id> <from>→<to>:<action>`) exactly once
per blocking task id; later ticks retry the send, and the node's timeout clock still runs so the
wait is bounded — on expiry the failure reason carries the task id.

**Rejected — fail the node on a foreign task.** Loses a run to a transient; the foreign task will
clear or expire within its own timeout, and a bounded wait costs nothing.

**Rejected — bypass the dedup for graph dispatches (`SendForce`).** Two identical `request:test`
messages in flight would both run the suite; the dedup is right to refuse, the executor is wrong to
claim what it refused.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | `dispatchNode` adoption (874–902); the deferred state and its row |
| `tools/muxcode/bus/dedup.go` | `HasInFlightTaskForRole` (217), `FindInFlightTask` (235) — the `(to, action)` key |
| `tools/muxcode/bus/task.go` | `Task.From` / payload, what the ownership check reads |
| `tools/muxcode/bus/phase_check_test.go`, `tools/muxcode/bus/graph_exec_test.go` | executor tests |
| `CLAUDE.md`, `docs/architecture.md`, `docs/agent-bus.md` | graph bullet, section, reference |

## Implementation

### Phase 1: Ownership, deferral, the row

- [ ] `dispatchNode`: adopt only a task with the executor's own `From` and an equal payload; otherwise leave the node `ready` and log `graph-dispatch-deferred` once per blocking task id
- [ ] A deferred node that reaches its timeout fails with a reason naming the blocking task
- [ ] Tests: foreign task → deferred, no adoption, dispatches after the task completes; the daemon's own duplicate → adopted (negative control); the row is emitted exactly once across repeated ticks

### Phase 2: Docs

- [ ] `CLAUDE.md` graph bullet clause; `docs/architecture.md` graph section; `docs/agent-bus.md` graph reference

### Phase 3: Integration test

- [ ] Hermetic section (scratch `BUS_SESSION`, `build-test-review` with stubbed agents): seed a foreign tracked `request:test` to test, start the run, assert the test node is `ready` with the `graph-dispatch-deferred` row and no `daemon → test` row; mark the foreign task responded → the dispatch lands on the next tick
- [ ] Negative control: no foreign task → the test node dispatches on the first tick with no deferred row
- [ ] Run the section and record the counts in this spec

## Notes

- Filed 2026-09-09 16:02 by plan from edit's handoff (`mux-170-handoff.md`, session `03a897a2`),
  the timeline re-verified against the lifecycle log, `muxcode history test` and the code lines
  above.
- Second case added 2026-09-28 on the user's instruction relayed by edit, verified against the
  worker's two reports, `log.jsonl` and `cmd/send.go:177–198`.
- Related: [MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md) (the trigger);
  [MUX-171](./MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) (found in the same hour, the
  other way a daemon mechanism written for one case fires on another);
  [MUX-112](./MUX-112-idle-task-rescue-closes-live-work.md) and
  [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) (the task-correlation family).

## Status

**Backlog** — 0/12. Filed 2026-09-09 16:02.
