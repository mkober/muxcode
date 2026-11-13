# Session Re-Init Purges the Active Spec While the Graph Run That Reads It Survives

**Tracking:** [mkober/muxcode#128](https://github.com/mkober/muxcode/issues/128)

A relaunch of a session (`bus.Init` over an existing bus dir) purges "stale data from the previous
session". Its removal list names the `active-spec` marker; it does not name `graphs/`. So a
`spec-to-pr` run outlives the relaunch — the daemon's first tick after restart is the resume scan —
and comes back with the pointer it reads gone. `spec_phases_remaining` counts "no spec" as "nothing
remaining", so the next `loop-check` would route to `final-gate` and ask for push and PR with phases
still open; MUX-167's `phase-check` fails closed to `stuck-gate`; the review chain's `plan-verify`
fires nothing. Nothing logs the removal: `spec set` and `spec clear` write no lifecycle row, and the
daemon's pointer check treats "unset" as fine. On 2026-09-09 at 15:34 the loss surfaced only because
plan's restart restore ran `muxcode spec get` beside `muxcode graph status` and saw an empty pointer
next to a `[running]` run.

## Context

### Observed (session `muxcode`, run `1788966148-spec-to-pr-2338488d`, 2026-09-09)

| When | What | Source |
|------|------|--------|
| 15:21 | plan's last note before the relaunch: pointer on MUX-163, run `2338488d` parked at `stuck-gate` after the lap-10 guard decline | plan memory |
| 15:34:02 | `spawn-complete` for both run workers (`spawn-c1c2a2a7`, `spawn-e011b05c`) — the old tmux session is gone | lifecycle |
| 15:34:07 | `daemon-orphan-killed` — the old daemon follows it | lifecycle |
| 15:34:13 | `session-start`; `init re-init "Purging stale data from previous session"`; `bus-init`; new daemon PID 50351 on `v0.1.0-71-g7191251-dirty` | lifecycle |
| 15:34:14 | plan relaunched; its restore runs `muxcode spec get` → `No active spec set`, and `muxcode graph status` → `2338488d [running]`, `stuck-gate waiting` | this session |
| 15:37 | plan re-sets the pointer by hand (`muxcode spec set docs/requirements/drafts/MUX-163-…`) — a repair, not the fix | this session |

The window was invisible at first: `lifecycle show --since 30m` returned nothing before 15:34 until
`--limit 3000` was added ([MUX-124](../backlog/MUX-124-lifecycle-since-truncated-by-limit.md)).

### Second occurrence (same run, 2026-09-10)

The relaunch on 2026-09-10 reproduced it exactly, on the same still-running run:

| When | What | Source |
|------|------|--------|
| 2026-09-09 17:07 | plan's last note before the relaunch: pointer on MUX-163, run `2338488d` parked at `stuck-gate` | plan memory |
| 2026-09-10 08:24:46 | session relaunched; plan's startup `context` and bootstrap arrive | this session |
| 2026-09-10 08:26 | plan's restore: `muxcode spec get` → `No active spec set`, beside `muxcode graph status` → `2338488d [running]`, `stuck-gate waiting`, elapsed 21h24m | this session |
| 2026-09-10 08:27 | plan re-sets the pointer by hand — the same repair, a second time | this session |

Two for two: every relaunch while a run is live loses the pointer, and the loss is found only because
plan's restore happens to pair `spec get` with `graph status`. Nothing else looks, and nothing logs it.
This raises the priority — the defect is not a one-off race but the guaranteed outcome of a restart.

### Third pointer loss (2026-09-10 10:06) — a different cause, deliberately not counted

The 10:06 relaunch also came up with no pointer, but **this one is not an occurrence of this defect**
and must not be tallied as one:

| Evidence | Reading |
|----------|---------|
| `kern.boottime` = 2026-09-10 10:03:56, `uptime` 3 min | the machine rebooted |
| every entry in `/tmp/muxcode-bus-muxcode/` stamped 10:06, `graphs/` absent entirely | the whole bus dir is new, not purged in place |
| `graph status` → `No graph runs`; no `graph-cancel`/`graph-complete` row for `2338488d` | the run store went with `/tmp`, unrecorded |

`purgeStaleFiles` removes the pointer and leaves `graphs/`; a reboot takes both, because `BusDir()`
lives under `/tmp`. The signature that identifies this defect — pointer gone **beside** a surviving
`[running]` run — is precisely what is missing here, so counting it would inflate the occurrence
record with an event the fix would not have prevented.

Two corollaries for the fix: a survival test must distinguish re-init from a cold `/tmp`, and the
"the graph's own resume (it works)" boundary below holds only **within a boot** — run `2338488d`,
21h of laps and seven gate approvals, was unrecoverable the moment the machine restarted.

### Mechanism — verified in code

- `bus/setup.go:15` `Init` — on an existing bus dir, `reInit` is set and `purgeStaleFiles` (150)
  runs. Its list truncates inboxes, history and cron, removes session meta, locks, spawn inboxes,
  proc logs, delivery and task files, and at **261–262** `// Remove active spec marker` →
  `os.Remove(ActiveSpecPath(session))`. `graphs/` appears nowhere in the file: the run store survives
  by omission, not by decision.
- `daemon/daemon.go:2905–2911` `checkGraphRuns` — "all state lives in the per-run store under
  `BusDir()/graphs` — the first tick after a daemon restart IS the resume scan". The run resumes; the
  pointer it depends on does not.
- `bus/conditions.go:335–364` `evalSpecPhasesRemaining` — "No active spec, or an unreadable one,
  counts as nothing remaining — a loop must terminate, not spin, when its spec disappears." Correct
  for a spec that vanished; on a pointer that vanished it turns the `loop-check` edges
  (`graph_templates.go:45, 66–67`) into `final-gate` → `push-pr` with the spec's phases open.
- `bus/conditions.go:366+` `evalSpecPhaseCommittable` — "no spec … fails closed, which in
  spec-to-pr routes to the stuck gate". The MUX-167 lap would park on a gate whose message blames the
  phase.
- `daemon/daemon.go:489` — the review chain's `plan-verify` reads `bus.ReadActiveSpec` and fires
  nothing when it is empty; no `verify-spec` reaches plan.
- `daemon/daemon.go:555` `checkActiveSpec` and `bus/spec_pointer.go:42` `ReconcileActiveSpec` —
  handle `Repointed` (close-out move) and `Dangling` (file gone → warn + event to edit, "never
  silently cleared, since the pointer is the only record of which spec was active"). `Unset` returns
  silently. The MUX-007 principle already covers the file going missing; the purge removes the
  pointer itself, one layer below that guard.
- `cmd/spec.go:80, 87` — `set` and `clear` print a line and write no lifecycle row, so a pointer's
  history cannot be read from `lifecycle show`.
- `bus/setup_test.go:67` `TestInit_ReInit_PurgesStaleData` — asserts nothing about the pointer
  either way. The fix flips no test; it needs a new one.

### Scope boundary

In scope: the pointer's survival across re-init, lifecycle rows for every change to it, and a daemon
alert when it is unset under a live run that needs it. Not in scope: what else re-init purges
(inboxes, tasks, delivery, the spawn ledger stay as they are), the graph's own resume (it works),
the semantics of `spec_phases_remaining` on a genuinely missing spec (MUX-121, correct), or the
MUX-007 gate markers purged beside the pointer (per-chain transient state — a re-init should reset
the chain, not the spec).

## Requirements

### Acceptance criteria

- [x] A session re-init (`bus.Init` over an existing bus dir) leaves the `active-spec` marker in place: after `muxcode init` over a bus dir holding a pointer, `muxcode spec get` prints the same path — pinned at `bus.Init` by `TestInit_ReInit_PurgesStaleData`; the CLI form is Phase 4's script
- [x] The re-init writes a `spec-kept` lifecycle row naming the path and the count of live runs under `graphs/`, so the pointer's survival is readable from `lifecycle show` without a code read — `TestInit_ReInit_CountsLiveRunsBesideKeptSpec`
- [x] `muxcode spec set` and `muxcode spec clear` write `spec-set` / `spec-clear` lifecycle rows carrying the actor (`BUS_ROLE`, or `user` when unset) and the path — every change to the pointer has a row — the two commands in Phase 1; every other writer tagged ` via=<road>` by the run's `criteria` node — see [Findings for Phase 1](#findings-for-phase-1). `bus/intent_test.go` asserts the `graph-launch`, `launch-rollback` and `branch` rows with their actor, and that a no-op launch, rollback or branch call logs nothing; the graph picker's row (`via=graph-picker`) has **no test** — the picker reads interactive stdin. `./test.sh` and review passed; the integration script re-ran 29 passed, 0 failed
- [x] A `spec-to-pr` run resumed across a re-init evaluates `loop-check` against the spec it started with: with a phase still open, `spec_phases_remaining: true` passes and the run re-enters `implement`, never `final-gate` — `TestSpecToPRLoopCheckSurvivesReInit`
- [x] An unset pointer while a run whose template `RequiresSpec` is `running` or `waiting` raises a `spec-unset-live-run` warn row and a one-time `event` to edit naming the run (cooldown via `shouldSendEvent`), so a deliberate `spec clear` under a live run is announced rather than absorbed — `TestCheckActiveSpec_UnsetUnderLiveRunAlertedOnce` and its negative controls
- [x] A deliberate `muxcode spec clear` still clears (the negative control): re-init preserves, it never resurrects — a pointer cleared before the relaunch stays cleared after it, and a bus dir that never held one gains none — `TestInit_ReInit_HonoursClearBeforeIt`, `TestInit_ReInit_NoPointerStaysUnset`
- [x] Docs: `docs/architecture.md` session re-init and spec-verification sections, `docs/agent-bus.md` `muxcode spec` reference, `CLAUDE.md` spec-verification constraint — Phase 3

### Technical approach

**Primary — the pointer is durable session state, like the run store.** Drop the removal at
`setup.go:261–262` and log `spec-kept <path> live-runs=N` (or `spec-none`) in its place; the two
MUX-007 gate markers on the lines below stay purged. Staleness is already handled one layer up by
`ReconcileActiveSpec` — a moved spec is followed, a missing one is reported with cooldown — so keeping
the pointer produces no false alert, which is the reason `Init`'s comment gives for purging at all
("so the daemon doesn't fire alerts based on old data"). `cmd/spec.go` writes `spec-set` /
`spec-clear` rows with the actor from `BUS_ROLE` (`user` when unset) through `LogLifecycle`.
`checkActiveSpec` gains a `SpecPointerUnset` arm: scan the run store for a `running`/`waiting` run
whose template `RequiresSpec`; if one exists, a `spec-unset-live-run` warn row and an `event` to edit
naming the run, keyed on the run id under `shouldSendEvent`.

**Rejected — keep the pointer only when a live run needs it.** The common relaunch — `./build.sh`,
then a fresh session mid-spec with no run — would still lose it, and the pointer's other reader, the
review chain's `plan-verify` (`daemon.go:489`), has no run to be live. A conditional keep also makes
the outcome depend on timing: a run that completed a second before the relaunch flips it.

**Rejected — make the run carry its own spec path.** Sound for `spec_phases_remaining` alone, but
`plan-verify`, `phase-check` (`phaseCommitReady`) and the close-out guard all read the pointer; a
second copy inside `run.json` is two sources that can disagree
([MUX-143](../backlog/MUX-143-run-carries-two-phase-identities.md)'s class). The pointer stays the single
record; the fix is that it survives.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/setup.go` | `Init` (15), `purgeStaleFiles` (150) — the removal at 261–262 and its replacement row |
| `tools/muxcode/bus/setup_test.go` | `TestInit_ReInit_PurgesStaleData` (67) — the kept-pointer assertion and its negative controls |
| `tools/muxcode/cmd/spec.go` | `set` (80) / `clear` (87) — the lifecycle rows |
| `tools/muxcode/cmd/init.go` | `bus.Init` entry point (18) — what the integration script drives as the re-init |
| `tools/muxcode/daemon/daemon.go` | `checkActiveSpec` (555) — the `Unset` arm; `plan-verify` read (489); `checkGraphRuns` resume comment (2905); `shouldSendEvent` (3453) |
| `tools/muxcode/bus/spec_pointer.go` | `ReconcileActiveSpec` (42) — the staleness guard that makes keeping safe |
| `tools/muxcode/bus/conditions.go` | `evalSpecPhasesRemaining` (335), `evalSpecPhaseCommittable` (366) — the readers whose behaviour the resume test pins |
| `tools/muxcode/bus/graph_templates.go` | `spec-to-pr` `loop-check` (45) and its edges (66–67) |
| `docs/architecture.md`, `docs/agent-bus.md`, `CLAUDE.md` | re-init list, `muxcode spec` reference, the constraint clause |

## Implementation

### Phase 1: Keep the pointer, log its changes

- [x] `setup.go` `purgeStaleFiles`: drop the removal at 261–262; write `spec-kept <path> live-runs=N` (or `spec-none`) in its place, with a comment naming this incident — `logActiveSpecKept`; live runs counted by `ScanInFlightGraphRuns`
- [x] `setup_test.go`: `TestInit_ReInit_PurgesStaleData` asserts the pointer survives; siblings assert a bus dir with no pointer stays without one (re-init never invents a pointer) and that a clear before re-init is honoured after it — plus `TestInit_ReInit_CountsLiveRunsBesideKeptSpec` (one live run, one complete → `live-runs=1`), `TestInit_ReInit_NoPointerStaysUnset`, `TestInit_ReInit_HonoursClearBeforeIt`
- [x] `cmd/spec.go` `set` / `clear`: `spec-set` / `spec-clear` rows with actor and path — `bus.LogSpecPointerChange`, called from both cases; `clear` records the path it cleared
- [x] Unit test for the rows: actor from `BUS_ROLE`; `user` when unset — `TestLogSpecPointerChange_Actor` pins the helper; `TestSpecSetClearWriteLifecycleRows` (`cmd/spec_test.go`) drives the real `Spec` handlers — `set` as `plan`, `clear` with no role → `actor=user`, naming the path it removed — and fails if either call is dropped. Added after the first review's must-fix (the helper-only test stayed green with the feature disconnected); the second review passed with no findings

### Findings for Phase 1

Recorded 2026-11-13 from the Phase 1 implement and fix nodes of run
`1794608375-55-defect-backlog-loop`; `./test.sh` passed, review clean on the second pass.

| Finding | Detail |
|---------|--------|
| **Other writers of the pointer logged nothing — fixed by the run's `criteria` node** | After Phase 1 the rows came only from `muxcode spec set` / `clear`, while the third acceptance criterion ends "every change to the pointer has a row" and no phase step covered the rest. The acceptance worker gave `LogSpecPointerChange` a `via` argument and called it from every other writer: `PointSpecForLaunch` (`via=graph-launch`), `UnpointSpecForLaunch` (`via=launch-rollback`, only when it actually clears), `LaunchIntentFromBranch` (`via=branch`) and the `cmd/graph.go` picker (`via=graph-picker`). The two commands write no `via`. `ReconcileActiveSpec`'s close-out repoint keeps its own `spec-repoint` row. Plan checked the call sites: all seven non-test callers of `WriteActiveSpec` / `ClearActiveSpec` are covered. No user decision was recorded — the worker widened the code rather than narrowing the criterion |
| "`spec clear` is the only way it goes" is not quite true (Phase 3 review) | `UnpointSpecForLaunch` (`bus/intent.go`) also clears the pointer, when a graph launch that wrote it fails to start — only if the pointer still holds the value that launch set. The docs say "removed only by `spec clear` or a failed-launch rollback"; this spec's own wording of that clause is looser than the code |
| Actor source | The row's actor is `BusActor()` (the agent role, else `user`), not a direct `BUS_ROLE` read as the spec's wording has it; the tests set `BUS_ROLE` and clear `AGENT_ROLE` |

### Phase 2: The daemon notices an unset pointer under a live run

- [x] `daemon.go` `checkActiveSpec`: on `SpecPointerUnset`, scan the run store for a `running`/`waiting` run whose template `RequiresSpec`; `spec-unset-live-run` warn row + `event` to edit naming the run, gated by `shouldSendEvent` — `alertSpecUnsetLiveRun` / `liveRunRequiringSpec` (the oldest in-flight run whose **frozen** graph `RequiresSpec`, via `ScanInFlightGraphRuns`), keyed per run; the action is in `isSystemAction` (`bus/guard.go`)
- [x] Test: unset pointer + live spec-requiring run → one row and one event, the next poll silent; unset pointer with no run, or with a run whose template does not require a spec → nothing (negative control) — `TestCheckActiveSpec_UnsetUnderLiveRunAlertedOnce`, `TestCheckActiveSpec_UnsetSilentWithoutSpecRun` (also a completed run → nothing), `daemon/provenance_gate_test.go`
- [x] Resume test: a run store with an open-phase spec, `bus.Init` called twice on the same bus dir, then `loop-check` evaluated → `spec_phases_remaining: true` passes; the same with the pointer explicitly cleared → fails (the MUX-121 termination, still intact) — `TestSpecToPRLoopCheckSurvivesReInit` (`bus/graph_exec_test.go`), on a real `50-spec-to-pr` run's frozen `loop-check`

### Phase 3: Docs

- [x] `docs/architecture.md`: the session re-init list — what is purged and what is kept, the pointer named beside `graphs/`; the spec-verification paragraph. `docs/agent-bus.md` `muxcode spec`: the rows — Session re-init gains a "Kept in the bus directory" bullet; the verify-spec section gains a pointer-lifetime table with the `spec-unset-live-run` arm (at most once per run per 300 s — a cooldown, not strictly one-time); `docs/agent-bus.md` gains a `muxcode spec` reference section (there was none) and five lifecycle-table rows
- [x] `CLAUDE.md` spec-verification constraint: one clause — the pointer survives re-init; `spec clear` is the only way it goes — added by edit, with the rows and the unset alert

### Phase 4: Integration test

- [x] `scripts/test-reinit-active-spec.sh` (hermetic: scratch `BUS_SESSION`, scratch repo with a one-open-phase spec, `muxcode init` as the re-init): `spec set` → `muxcode init` again → `spec get` prints the path and `lifecycle show` carries `spec-kept` — tree-built binary, scratch HOME and lifecycle dir; two re-inits, each keeping the path and writing one `spec-kept … live-runs=0`
- [x] Negative control: `spec clear` → `muxcode init` → `spec get` still empty; a fresh bus dir → no pointer after init — also `spec-none` after the clear with no further `spec-kept`, and a never-pointed session gaining no marker across two re-inits
- [x] `spec-set` and `spec-clear` rows present with the actor — `actor=plan` on set, `actor=user` and the cleared path on clear
- [x] Run the script and record the counts in this spec

Run through the run agent on 2026-11-13 17:37 (task `1794609465-spawn-2f5b5fbd-55b0c1ca`, log
`/tmp/test-reinit-active-spec.log`): **29 passed, 0 failed, coverage floor 28 met, exit 0.** The
script is not executable in the tree (`-rw-r--r--`) and was run as `bash scripts/…`.

## Notes

- Filed 2026-09-09 15:41 by plan on its own observation during the restart restore — no request.
  The pointer was re-set by hand at 15:37 so run `2338488d`'s next lap reads the right spec; that is
  a repair for today, not the fix.
- Also seen at the relaunch: both run workers logged `spawn-complete` at 15:34:02 as the old session
  died and `spawn.jsonl` was truncated. The run's `implement` node is `done` and a loop re-entry
  spawns a fresh worker, so this is not a defect — noted so the Phase 4 replay knows the run store,
  not the spawn ledger, is what resumes.
- The graph run's own template snapshot is the one it started with: `2338488d` carries no
  `phase-check` node even though the daemon now runs the MUX-167 binary. Exercising MUX-167 needs a
  fresh run; unrelated to this defect, recorded because both were found in the same restore.
- Related: [MUX-007](../completed/MUX-007-verify-spec-stale-review-refire.md) (Phase 3 —
  `ReconcileActiveSpec`, the pointer-honesty principle this extends one layer down);
  [MUX-121](../completed/MUX-121-multi-phase-sequential-graph.md) (the termination
  semantics that make a vanished pointer look like a finished spec); [MUX-124](../backlog/MUX-124-lifecycle-since-truncated-by-limit.md)
  (why the window was invisible at first); [MUX-143](../backlog/MUX-143-run-carries-two-phase-identities.md)
  (why the run must not carry a second copy); [MUX-167](../completed/MUX-167-spec-to-pr-commit-gate-before-phase-check.md)
  (the fail-closed `phase-check` path a vanished pointer would take).

## Status

Complete

Closed out 2026-11-13 by `55-defect-backlog-loop` run `1794608375`, which activated it the same day.
All four phases and seven acceptance criteria are checked. The third criterion was met by the run's
acceptance worker widening the audit rows to every writer of the pointer; the graph picker's row is
the one call site with no test. Filed 2026-09-09 15:41. Last seen 2026-11-13 15:06: a session
re-init cleared the pointer while a defect-loop run was at its `close-spec` node, and plan re-set it
by hand.
