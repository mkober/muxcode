# MUX-217: The Agent-Defs Watchdog Reloads an Agent the Same Second a Graph Dispatches to It, and the Relaunch Clears the Dispatch

**Tracking:** [mkober/muxcode#194](https://github.com/mkober/muxcode/issues/194)

A build installed an edited agent definition. The daemon's agent-defs watchdog reloaded the test
agent in the same second the graph dispatched its test request; the relaunch cleared that in-flight
task, the node read `failure` three seconds after it started, and no suite ran. It recurs whenever
a phase edits an agent definition.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's request relayed by edit (`/tmp/defects-216-217.md`). Plan read the
timeline from session `muxcode`'s lifecycle log and `AutoReloadSafe` from the tree. The ordering
inside the shared second is not recorded, so the race below is plan's reading of the rows, not a
traced execution. Nothing was run for this spec.

### Observed (run `1794608375-55-defect-backlog-loop-d974dab7`, MUX-215 Phase 1)

| When | Row | Source |
|------|-----|--------|
| 20:42:05 | daemon restarted onto the build's binary (`daemon-upgraded`, `started`) | upgrade, daemon |
| 20:42:16 | `agent-defs-reload` — "test definition changed on disk — auto-reload" (the phase had edited `agents/test-runner.md`) | daemon |
| 20:42:16 | `graph-node-done … build -> success`, then `graph-node-start … test (send)` | daemon |
| 20:42:18 | `launch role=test cli=codex reason=reload`, then **`cleared-inflight-tasks — test: 1 stale in-flight task(s) cleared on launch`** | agent, launch |
| 20:42:19 | `graph-node-done … test -> failure`; the run moved to `fix` | daemon |

No `./test.sh` row exists for that node. The run's next test node, at 20:43, ran the suite and
passed.

### Mechanism

| Fact | Where | Status |
|------|-------|--------|
| The watchdog reloads only when `AutoReloadSafe` holds: no reload under way, no pending actionable inbox, no live in-flight task, pane not working | `bus/agentdefs.go:91` | Read by plan |
| That check and the reload are not atomic with the executor's dispatch: a request sent after the check passes and before the relaunch is neither seen by the check nor protected from it | rows at 20:42:16 | Plan's reading — the order within the second is not logged |
| Every launch times out every in-flight task for the role (`ClearInFlightTasksForRole`), consumed or not, and the executor fails the node on `timed-out` without consulting `timeout_secs` | [MUX-196](./MUX-196-agent-launch-expires-in-flight-graph-dispatch.md), verified there at `14f3a73` | Not re-read for this spec |

### Relation to MUX-196

The second half — a relaunch clearing an unconsumed dispatch and the node failing on it — is
[MUX-196](./MUX-196-agent-launch-expires-in-flight-graph-dispatch.md)'s mechanism, observed there
on a manual `muxcode reload`. This is a second occurrence on a different road, with one thing
MUX-196 does not cover: the reload was the daemon's own, issued by a watchdog whose safety check
exists to prevent exactly this and lost a race. Fixing MUX-196 would have kept this node alive;
it would not stop the watchdog reloading an agent a graph is about to use. The two can ship
together; whether to merge them is the user's call.

### Blast radius

- Any graph run whose phase edits an agent definition for a role the run then dispatches to: build
  installs it, the watchdog reloads, the next node's dispatch lands in the window. In the defect
  loop that is build → test, the tightest edge in the template.
- The same window exists for the sandbox-fingerprint reload (`codex-sandbox-reload`), which shares
  the check.
- The failure is cheap when a `fix` loop follows, as here; on a template with no retry edge the run
  fails.

## Requirements

### Acceptance criteria

- [ ] A watchdog reload never clears a graph dispatch: a request dispatched to a role while the
  daemon is reloading it is either delivered to the relaunched agent and answered, or the reload is
  deferred until the dispatch completes
- [ ] The watchdog defers while a running graph run holds the role as a node that is running or
  next to dispatch, and logs the deferral with the run and node
- [ ] A deferred reload still happens once the run no longer needs the role — negative control: the
  definition change is not lost
- [ ] A reload with no graph run and no pending work proceeds as today — negative control
- [ ] The node's outcome names the cause when a relaunch does clear its task — never a bare
  `timed-out` seconds into its allowance (shared with MUX-196)
- [ ] Both watchdog reloads are covered: definition hash and sandbox fingerprint

### Technical approach

Two layers, either sufficient for this occurrence, both wanted:

- **Close the window.** `AutoReloadSafe` also asks whether a running graph run owns the role
  (`GraphOwnsRunningSendNode`, or a successor about to dispatch to it), and the executor skips a
  dispatch to a role under a reload marker until the marker clears, so check and act cannot
  interleave.
- **Survive the relaunch.** MUX-196's change — a launch keeps a task whose request was never
  consumed — makes the race harmless when it is lost anyway.

Rejected: lengthening the watchdog's cooldown. It narrows the window without closing it.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/agentdefs.go` | `AutoReloadSafe`, `paneSafeToReload` |
| `tools/muxcode/daemon/daemon.go` | `checkAgentDefs`, the reload budget |
| `tools/muxcode/bus/graph_exec.go` | Dispatch, and the task-status switch that fails the node |
| `tools/muxcode/bus/task.go`, `bus/launch.go` | `ClearInFlightTasksForRole` and its call on launch |
| `scripts/test-reload-keeps-dispatch.sh` | MUX-196's planned script; this spec adds the watchdog road |

## Implementation

### Phase 1: Pin

- [ ] Establish the order inside the window: from the task record and the code, record whether the
  dispatch preceded the check or fell between check and relaunch
- [ ] Unit test: a role a running graph run is about to dispatch to → `AutoReloadSafe` returns true
  today (pin red)
- [ ] Unit test (negative control, stays green): no run, no inbox, idle pane → safe

### Phase 2: Defer and protect

- [ ] `AutoReloadSafe` refuses while a running graph run owns or is about to dispatch to the role;
  one lifecycle row per deferral naming run and node
- [ ] The executor holds a dispatch to a role under a reload marker until the marker clears
- [ ] The deferred reload fires once the run releases the role
- [ ] Invert the Phase 1 pin; the negative control stays green

### Phase 3: Integration test

- [ ] Script (new, or a section of `scripts/test-reload-keeps-dispatch.sh`): scratch daemon, a
  two-node graph whose first node changes the second role's installed definition
- [ ] Test: the second node's dispatch is answered and the node succeeds; no
  `cleared-inflight-tasks` row for its task
- [ ] Test: the reload happens after the run completes
- [ ] Negative control: with the deferral disabled the node fails as on 2026-11-13 — proving the
  script can see the race
- [ ] Coverage floor; run through the run agent and record the counts here
- [ ] Update `CLAUDE.md` (Codex sandbox watchdog entry) and `docs/architecture.md`

## Related

- [MUX-196](./MUX-196-agent-launch-expires-in-flight-graph-dispatch.md) — the launch-time clearing
  this occurrence ran into
- [MUX-215](./MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md) — the run it
  happened in
- [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) — another dispatch losing
  its task to bookkeeping

## Status

Backlog

Filed 2026-11-13 on the user's request relayed by edit, ranked second and eligible for
`55-defect-backlog-loop`. One occurrence, read from the lifecycle log. It overlaps MUX-196, which
owns the launch-time clearing; this spec owns the watchdog's race.
