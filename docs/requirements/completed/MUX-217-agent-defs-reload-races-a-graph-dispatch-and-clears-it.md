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
| Every launch times out every in-flight task for the role (`ClearInFlightTasksForRole`), consumed or not, and the executor fails the node on `timed-out` without consulting `timeout_secs` | [MUX-196](../backlog/MUX-196-agent-launch-expires-in-flight-graph-dispatch.md), verified there at `14f3a73` | Not re-read for this spec |

### Relation to MUX-196

The second half — a relaunch clearing an unconsumed dispatch and the node failing on it — is
[MUX-196](../backlog/MUX-196-agent-launch-expires-in-flight-graph-dispatch.md)'s mechanism, observed there
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

- [x] A watchdog reload never clears a graph dispatch: a request dispatched to a role while the
  daemon is reloading it is either delivered to the relaunched agent and answered, or the reload is
  deferred until the dispatch completes — script sections A–C and F
- [x] The watchdog defers while a running graph run holds the role as a node that is running or
  next to dispatch, and logs the deferral with the run and node — script section A
- [x] A deferred reload still happens once the run no longer needs the role — negative control: the
  definition change is not lost — script section C
- [x] A reload with no graph run and no pending work proceeds as today — negative control —
  `TestAutoReloadSafe_IdleRoleWithNoRunIsSafe`, a unit test
- [x] The node's outcome names the cause when a relaunch does clear its task — never a bare
  `timed-out` seconds into its allowance (shared with MUX-196) — `Task.ClearedBy`, unit test and
  script section E
- [x] Both watchdog reloads are covered: definition hash and sandbox fingerprint — the definition
  road by the script, the sandbox-fingerprint road by the daemon unit test
  `TestCheckAgentDefs_GraphDeferralLoggedOnceThenReloads`; both pass through the one check

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

- [x] Establish the order inside the window: from the task record and the code, record whether the
  dispatch preceded the check or fell between check and relaunch — from the code and the log rows;
  the task record was garbage-collected
- [x] Unit test: a role a running graph run is about to dispatch to → `AutoReloadSafe` returns true
  today (pin red)
- [x] Unit test (negative control, stays green): no run, no inbox, idle pane → safe

### Findings for Phase 1

Recorded 2026-11-13 from the run worker's handoff (run `1794626267`, node `implement`). The code
reading is the worker's; plan had read the same lifecycle rows when filing. Uncommitted. The run's
build, `./test.sh` (both modules passed) and review (no findings; it confirmed the code trace) then
passed, and the phase was ticked.

**Order inside the window: the dispatch fell between the check and the relaunch.**

From the code:

- One daemon poll iteration runs `checkAgentDefs()` (`daemon/daemon.go:399`) before
  `checkGraphRuns()` (`:414`).
- `checkAgentDefs` calls `bus.AutoReloadSafe`, logs `agent-defs-reload`, and starts the reload in a
  goroutine (`daemon.go:1547-1568`). The reload marker is acquired only inside `bus.ReloadAgent`
  (`bus/reload.go:440`), in that goroutine, followed by the graceful stop and the relaunch.
- The executor never reads the reload marker. In the same iteration `checkGraphRuns` finishes build
  and dispatches test with nothing to stop it; the relaunch seconds later runs
  `ClearInFlightTasksForRole`.

From the record — the daemon writes its rows sequentially, so file order is execution order within
a second:

| When | Row |
|------|-----|
| 20:42:16 | `agent-defs-reload` — the check had passed |
| 20:42:16 | `graph-node-done build -> success`, `graph-node-start test (send)`; the bus log holds dispatch `1794620536-daemon-170b70ee` (daemon→test, node `test`) |
| 20:42:18 | `launch role=test cli=codex reason=reload`, then `cleared-inflight-tasks` |
| 20:42:19 | `graph-node-done test -> failure` |

The dispatch's task file and delivery status have since been garbage-collected, so whether test
consumed it before the relaunch cannot be recovered. The order can: check, then dispatch in the
same tick, then relaunch about 2 s later. The check could not have seen the dispatch, and the
dispatch was not protected from the relaunch. This replaces the Mechanism table's "plan's reading"
for the second row.

**Tests** (`tools/muxcode/bus/agentdefs_test.go`):

- `TestMUX217Pin_ReloadSafeWhileGraphNeedsRole` — the pin, asserting today's behaviour; Phase 2
  inverts it. A build→test run with build running and the test role an idle Codex pane with no inbox
  and no task: `AutoReloadSafe(test)` reads safe. Second case: build done and the test node running
  before any dispatch task exists also reads safe.
- `TestAutoReloadSafe_IdleRoleWithNoRunIsSafe` — the negative control, stays green.

### Phase 2: Defer and protect

- [x] `AutoReloadSafe` refuses while a running graph run owns or is about to dispatch to the role;
  one lifecycle row per deferral naming run and node
- [x] The executor holds a dispatch to a role under a reload marker until the marker clears
- [x] The deferred reload fires once the run releases the role
- [x] Invert the Phase 1 pin; the negative control stays green

### Findings and changes for Phase 2

Recorded 2026-11-13 from the run worker's handoff (run `1794626267`, node `implement`) and the
run's three reviews of it. Uncommitted. The first review returned a must-fix and the second a
should-fix; after the fixes the run's build, `./test.sh` (exit 0, both modules) and review (no
findings) passed, and the phase was ticked. The evidence is unit-level; Phase 3 owns the
end-to-end proof.

| Change | Where | What it does |
|--------|-------|--------------|
| `GraphNeedsRole(session, role)` | `bus/graph_run.go` | A running run's send node for the role counts when it is owned now (`GraphOwnsRunningSendNode`), ready, or fed by a node that is ready or running, looking through condition and join nodes (`feederActive`). The fed node's own state is ignored, so a loop re-entering build still covers test although test reads done from the last lap. A completed predecessor not yet routed still counts (`feederLive`). A feeder waiting at a human gate does not |
| Deferral | `bus/agentdefs.go`, `AutoReloadSafe` | Refuses while `GraphNeedsRole` holds, reason "graph run `<run>` needs `<role>` as node `<node>`" (`GraphReloadDeferralPrefix`). Its only caller is the daemon watchdog, so both the definition-hash and the sandbox-fingerprint reload are covered |
| Deferral row | `daemon/daemon.go`, `logGraphDeferral` | One `agent-defs-reload-deferred` row per role, run and node — not one per check. A deferral spends no reload budget, so the reload fires on the first check after the run releases the role. Non-graph unsafe reasons are not logged |
| Synchronous reservation | `daemon/definition_watchdog.go`, `bus/reload.go` | From the first review's must-fix: `checkAgentDefs` reserves the live reload marker before it schedules the reload, and `ReloadAgentReserved` carries that reservation through the relaunch (one shared `reloadHolding` body). Without it the marker was taken inside the goroutine, after the executor could already have dispatched — the original window |
| Dispatch hold | `bus/graph_exec.go` | A send node whose role, or host window, carries a live reload marker stays ready (`deferDispatch`, `graph-dispatch-deferred`, logged once per reason) and is retried each tick. A stale marker never holds. This also protects dispatches from manual reloads and health restarts, which take the same marker |

Tests: `TestAutoReloadSafe_DefersWhileGraphNeedsRole` (the Phase 1 pin inverted, plus the release
case); `TestGraphNeedsRole_LoopReentryAndParkedGate` (after the second review its prior-lap nodes
are marked routed, so removing the condition/join look-through fails it; the parked-gate case is
the negative control); `TestExecHoldsSendWhileRoleReloading` (with the stale-marker negative
control); the daemon's `TestCheckAgentDefs_GraphDeferralLoggedOnceThenReloads`, a real-executor
condition test and a held-relaunch daemon test from the first review's fix.
`TestAutoReloadSafe_IdleRoleWithNoRunIsSafe` is unchanged.

Not in this phase: the acceptance criterion that a node's outcome names the cause when a relaunch
clears its task. It is shared with MUX-196 and has no step in any phase of this spec.

### Phase 3: Integration test

- [x] Script (new, or a section of `scripts/test-reload-keeps-dispatch.sh`): scratch daemon, a
  two-node graph whose first node changes the second role's installed definition —
  `scripts/test-reload-defers-dispatch.sh`
- [x] Test: the second node's dispatch is answered and the node succeeds; no
  `cleared-inflight-tasks` row for its task
- [x] Test: the reload happens after the run completes
- [x] Negative control: with the deferral disabled the node fails as on 2026-11-13 — proving the
  script can see the race — on a scratch build with both guards patched out (section E)
- [x] Coverage floor; run through the run agent and record the counts here — 45 of 45, floor 45
- [x] Update `CLAUDE.md` (Codex sandbox watchdog entry) and `docs/architecture.md`

### Results for Phase 3

Recorded 2026-11-13 from the run worker's handoff (run `1794626267`); plan confirmed the result line
and exit code in the run agent's history. Uncommitted.

New script: `scripts/test-reload-defers-dispatch.sh`. Hermetic — muxcode and the claude-stub are
built from the tree, with a private tmux server, a scratch daemon and agent definitions in a scratch
install directory. Floor 45 after the review fix below (29 at first).

| Section | What it shows |
|---------|---------------|
| A | In a build → test graph, test's installed definition changes while build's node runs. The watchdog logs `agent-defs-reload-deferred` "test: graph run `<run>` needs test as node b" and does not reload test |
| B | Test's dispatch is answered with a watchdog check passing while it is in flight. Node b succeeds, there is no `cleared-inflight-tasks` row for test, and the run completes |
| C | The deferred reload (`agent-defs-reload`, then a `reason=reload` launch) lands after `graph-run-complete`, finishes and releases its marker |
| D | Deferral control on a pre-fix daemon: the same change reloads test while build's node is still running, and no deferral is logged |
| E | Negative control — on the pre-fix build a test dispatch lands inside the held stop-to-relaunch window, the relaunch logs `cleared-inflight-tasks`, and node t fails: the 2026-11-13 outcome reproduced |
| F | The same window on this build: node t is held ready with nothing sent and a `graph-dispatch-deferred` row, dispatched once the relaunch releases test, answered, and succeeds; no task is cleared |

First run, sections A–D as then written (task `1794629993-spawn-0616aad2-2faef0cf`): 29 passed,
0 failed, floor 29. An earlier attempt failed one check because the script stopped section C's
daemon mid-reload and the killed reload's marker held off the next daemon until it went stale at
60 s; C now waits for the reload to finish — marker behaviour as designed, not a defect.

**After review.** The first version's negative control used a new environment opt-out
(`MUXCODE_RELOAD_GRAPH_DEFER_DISABLE`) and did not reproduce the failure: with the deferral off the
reserved marker still held the dispatch and the run completed. Review asked for no production
bypass and a real reproduction. Both were done:

- The opt-out is removed from the code and from `docs/architecture.md`.
- The pre-fix build is this tree with both MUX-217 guards patched out of a scratch copy — the
  `GraphNeedsRole` clause in `AutoReloadSafe` and the reload-marker hold in `dispatchNode`.
- The claude-stub fixture gained `CLAUDE_STUB_EXIT_DELAY_FILE`: `/exit` waits N seconds, so a
  reload's graceful stop holds the marker and the stop-to-relaunch window stays open
  deterministically. Sections E and F place a dispatch inside it.

Rerun through the run agent (task `1794630426-spawn-0616aad2-29e4a6eb`): **45 passed, 0 failed,
floor 45**, exit 0. The negative-control step is met as written.

**The open criterion — a node's outcome names the cause.** The run's close-out guard declined on
it (one open item), and the acceptance worker (node `criteria`) then implemented it. The run's
build, `./test.sh` (exit 0) and review (no findings) passed on it, and the criterion was ticked:

- `Task.ClearedBy` (`bus/task.go`) is set by `ClearInFlightTasksForRole` on launch to
  "`<role>` relaunched (reason=`<r>`)".
- The executor's timed-out branch (`bus/graph_exec.go`) reports "dispatch cleared by a relaunch —
  `<cause>`, N s into its M s allowance" when the task carries a cause, and the bare `timed-out`
  only when it does not.
- Unit test `TestExecClearedDispatchNamesTheRelaunch`, with a no-cause control. Script section E
  now asserts the named cause on the pre-fix build's failed node.
- Rerun through the run agent (task `1794630858-spawn-0616aad2-aa55e5c1`): 46 passed, 0 failed,
  floor 46, exit 0 — confirmed by plan in the run agent's history.

This overlaps MUX-196's third acceptance criterion (the cause recorded instead of `task
timed-out`); MUX-196's other criteria — keeping an unconsumed task across a launch, re-dispatching
rather than failing — are untouched.

`CLAUDE.md` was updated by edit (the Codex sandbox watchdog entry and the script list) and
`docs/architecture.md` by plan ("The agent-defs watchdog and a graph run").

## Related

- [MUX-196](../backlog/MUX-196-agent-launch-expires-in-flight-graph-dispatch.md) — the launch-time clearing
  this occurrence ran into
- [MUX-215](../backlog/MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md) — the run it
  happened in
- [MUX-170](../backlog/MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) — another dispatch losing
  its task to bookkeeping

## Status

Complete — closed 2026-11-13 by `55-defect-backlog-loop` run `1794626267`. The agent-defs watchdog
no longer reloads a role a running graph run needs, it reserves its reload marker before scheduling
the reload, the executor holds a dispatch to a role that is reloading, and a node whose task a
relaunch does clear names that relaunch in its outcome.

Activated 2026-11-13 by the same run (Defects rank 1). Phase 1 (pin) is complete and reviewed: the dispatch fell between the watchdog's check and the
relaunch, and the pin and its negative control are in the tree. Phase 2 (defer and protect) is
complete and reviewed: the watchdog defers while a graph run needs the role and reserves its marker
before scheduling the reload, and the executor holds a dispatch under a live marker. Phase 3
(integration test) is complete and reviewed: `scripts/test-reload-defers-dispatch.sh` passes 45 of
45, reproducing the 2026-11-13 failure on a pre-fix build and showing it held on this one.

All 19 boxes are ticked. The last — a node's outcome names the cause when a relaunch clears its
task — had no phase step; the run's close-out guard declined on it and its acceptance worker
implemented it (`Task.ClearedBy`), with the script now at 46 of 46. That change also meets one of
MUX-196's criteria; the rest of MUX-196 (keeping an unconsumed task across a launch,
re-dispatching rather than failing) is untouched.

Filed 2026-11-13 on the user's request relayed by edit, ranked second and eligible for
`55-defect-backlog-loop`. One occurrence, read from the lifecycle log. It overlaps MUX-196, which
owns the launch-time clearing; this spec owns the watchdog's race.
