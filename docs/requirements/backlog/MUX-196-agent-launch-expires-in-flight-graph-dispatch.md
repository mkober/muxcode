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

- [ ] A launch does not time out a task whose request is still **unconsumed** in the role's inbox (no receipt): the task stays in flight on its own clock and the relaunched agent's reply completes it
- [ ] A task whose request **was** consumed by the prior instance is still cleared on launch (the stale-dedup purpose of the clearing is kept) — negative control
- [ ] A graph send node whose task is cleared by a launch records the cause — `agent relaunched` with the role and the clearing — not `task timed-out`, and the node is re-dispatched or held rather than failed while its own `timeout_secs` has not elapsed
- [ ] A late reply to a task a launch cleared, arriving inside the node's `timeout_secs`, completes the node (or the node never finished, per the first criterion) — the run is not failed by a reply that satisfies it
- [ ] A genuinely unanswered dispatch still fails at its own `timeout_secs` — negative control
- [ ] The clearing writes one lifecycle row per task it clears, naming the task id and whether its request had been consumed
- [ ] Every relaunch road is covered: `muxcode reload`, stuck-provider reload, health restart, `muxcode resume`
- [ ] `bash scripts/test-reload-keeps-dispatch.sh` passes

### Technical approach

In `ClearInFlightTasksForRole`, clear only tasks whose request has a receipt (`hasReceipt`: acked,
delivered or responded) — the prior instance consumed it and cannot answer it now. A task with no
receipt is still in the inbox and will be delivered to the new instance; leave it on its own clock.
In the executor, keep the `TaskTimedOut` branch for the task's own expiry, and give a launch-cleared
task a distinct cause so the node can be re-dispatched (it is an executor-owned send) instead of
failed. Neither change touches the dedup guard itself
([MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md)).

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

- [ ] Unit test: an in-flight task with no receipt, then `ClearInFlightTasksForRole` → today timed-out (pin red, inverted in Phase 2)
- [ ] Unit test: a graph send node whose task turns `timed-out` 250 s into a 3600 s `timeout_secs` → today fails with `task timed-out` (pin red)
- [ ] Unit test (negative control, stays green): an in-flight task whose request was acked is cleared on launch

### Phase 2: Keep unconsumed dispatches

- [ ] `ClearInFlightTasksForRole` skips tasks without a receipt; one lifecycle row per cleared task (id, consumed yes/no)
- [ ] Executor: a launch-cleared task is re-dispatched or held with cause `agent relaunched`, never `task timed-out`, while `timeout_secs` has not elapsed
- [ ] Invert the Phase 1 pins; the negative control stays green

### Phase 3: Integration test

- [ ] Create `scripts/test-reload-keeps-dispatch.sh` — hermetic scratch daemon, a graph send node to a stub role
- [ ] Test: relaunch the role mid-dispatch with the request unconsumed, reply after relaunch → node `success`, run not failed, no `task timed-out`
- [ ] **Negative control:** a dispatch never answered still fails at its own `timeout_secs`
- [ ] Test: a consumed-then-orphaned task is still cleared on launch, with its row
- [ ] Coverage floor; run and record counts here

## Related

- [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) and
  [MUX-155](./MUX-155-send-dedup-keys-on-target-not-sender.md) — the `(to, action)` dedup the
  launch-time clearing was written to unblock; narrowing that key reduces the pressure to clear at all.
- [MUX-162](./MUX-162-pr-review-fix-node-600s-send-cap.md) — another node failed by a clock rather
  than by its work.
- [MUX-008](./MUX-008-unverified-daemon-auto-restart.md) — the restart roads this spec must cover.

## Status

Backlog

Filed 2026-09-28 on the user's instruction relayed by edit; timeline and mechanism verified the same
day.
