# MUX-190: A Codex Agent Drops a Consumed Request After a Guard Denial

**Tracking:** [mkober/muxcode#144](https://github.com/mkober/muxcode/issues/144)

When `muxcode hook guard` denies a Codex agent's command — the evidence guard refusing a bundled
`./build.sh`, for instance — the agent ends its turn. The request it was working on is already
consumed and receipted, nothing re-drives it, and the graph node that dispatched it parks until the
600 s `task-timeout` records it `timed-out`. Observed 2026-09-23 on a codex build agent that bundled
`./build.sh` with acks in one call (the shape `CheckEvidenceGuard` exists to refuse); the denial did
its job and the node still burned ten minutes naming the clock.

## Context

### Source and standard of evidence

From the 2026-09-23 session notes; the incident has **not** been re-verified against the lifecycle
log (`lifecycle show --event guard-denied --since 2026-09-23` returned no rows at filing — the log
rotates at 1000 rows, so absence is not evidence either way). The mechanism below is verified in the
tree on 2026-09-24. Filed on the user's instruction relayed by edit.

### Mechanism — verified in code

| Step | Code | Behaviour |
|---|---|---|
| The guard denies and logs | `cmd/hook.go:272-304` (re-read 2026-11-24; `:221-251` at filing): `FormatGuardBlockFor` in the provider's dialect, `guard-denied` lifecycle row | The agent sees why; the bus sees a row |
| The request is already consumed | delivery on the codex hook road acks at `hook stop`/`prompt-submit` (`hasReceipt`), on the scrape road at injection | The inbox row is gone; the task is in flight |
| Nothing reacts to the denial | `daemon/daemon.go` has no `guard-denied` handler (grep: none) | No re-drive, no nudge, no alert |
| Codex ends the turn | provider behaviour on a denied tool call — observed, not pinned by a test | The agent does not retry with a compliant command; it stops |
| The node waits for the clock | in-flight task expiry `task-timeout` 600 s; executor-owned dispatches are exempt from the idle-task watchdog (`GraphOwnsTask`) | `timed-out`, cause invisible |

The Claude road differs in the agent, not the bus: Claude typically retries after a denial with the
guard's suggested form. The bus is equally blind on both roads; the Codex agent's turn-ending is
what makes it visible.

### Blast radius

- Every guard denial on a Codex agent inside a graph run costs the task timeout and a `timed-out`
  node whose recorded cause is the clock; a human reads "stuck" where the truth is "refused and
  gave up".
- The evidence guard fires most on exactly the roles that run graph nodes (build, test, deploy).

### Family

- [MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md), [MUX-153](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md) — Codex agent behaviour on the hook road.
- [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) — node outcomes named for the wrong reason.
- The codex-approval watchdog (CLAUDE.md, daemon watchdogs) — the precedent for the daemon *acting* on a Codex prompt state rather than waiting for the task clock.

## Decisions (user, 2026-11-24)

Relayed by edit; this spec was parked on Decision 1 and is unparked by it.

| Question | Decision |
|----------|----------|
| Decision 1 — re-drive or alert | **Re-drive once per task, carrying the guard's denial text**, through `RedriveTask` — which refuses a working pane (`AgentIsWorking`). Where the re-drive is refused or cannot be made, **fall back to an alert to edit naming the task and the node** |
| The node's record | The node's output **names the denial**, whether or not the re-drive rescues it |

## Requirements

### Acceptance criteria

- [x] A `guard-denied` row on a role with an in-flight request is followed, within one daemon poll, by one re-drive of that request with the denial's text prepended — script section A, on a Claude-road role; the text follows the payload. **A Codex or OpenCode role takes the next-but-one criterion's alert instead** — see [Findings for Phase 2](#findings-for-phase-2)
- [x] At most one re-drive per task: a second denial on the same task re-drives nothing and alerts edit instead — `TestGuardDenialSecondOnTheSameTaskAlertsAcrossARestart`, and script section B (exactly one re-drive in 100 s with the stall watchdog on); the second-denial case has no script section yet (Phase 4)
- [x] When the re-drive is refused — the pane is working, or `RedriveTask` cannot deliver — edit gets one alert naming the task and, for a graph dispatch, the run and node; nothing is typed into the pane — `TestGuardDenialOnAWorkingPaneAlertsOnlyAfterAFullIdleGrace`, `TestGuardDenialUndeliverableRedriveAlerts`; unit tests only until Phase 4's busy-pane section
- [x] A graph node whose agent was guard-denied names the denial in its output (`guard-denied` and the guard's reason); if the task still expires, the cause recorded is the denial, not a bare `timed-out` — script section A (`[guard-denied build Bash: …]` ahead of `node timeout`), `TestExecSendNodeOutputNamesAGuardDenialProcessedOrNot`
- [x] **Negative control:** a guard denial on a role with no in-flight request produces no re-drive and no node change — script section N, `TestGuardDenialWithNoInFlightTaskDoesNothing`
- [x] The Claude road is unchanged where the agent already retries: a re-drive into a working pane is refused by `AgentIsWorking`, as today, and an agent that answers after its own retry gets no alert — `TestGuardDenialDeferredThenAnsweredWithinTheGraceNeverAlerts`
- [x] Reproduced first by a failing test that pins the current behaviour (denial → nothing → timeout) — section A of the script, 3 failing assertions on 2026-11-27
- [x] `bash scripts/test-guard-denied-redrive.sh` passes — 57 passed, 0 failed, floor 57, 2026-11-27 18:04

### Technical approach

The guard already writes a lifecycle row; the daemon poll can watch for `guard-denied` rows newer
than each in-flight task's start, on that task's role, and act once per task: a **single
re-drive** carrying the denial text as context (the agent is told what to change), falling back to
an alert if the pane is working. A working pane may simply be the agent retrying, so the alert
waits until the pane has gone idle without an answer — the exact wait is Phase 2's to set and record. Cap at one per task so a repeat denial does not loop. Tie the
node's outcome to the row when the task then expires.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/cmd/hook.go:272-304` | guard denial output and `guard-denied` row |
| `tools/muxcode/bus/evidence_guard.go`, `bus/listener_guard.go`, `bus/guard.go` | the guards that deny |
| `tools/muxcode/daemon/daemon.go` (`checkTrackedTasks`, task-timeout) | where the watch and re-drive belong |
| `tools/muxcode/bus/deliver.go` (`RedriveTask`, `AgentIsWorking`) | the re-drive road and its busy-pane refusal |
| `tools/muxcode/bus/graph_exec.go` | node outcome on task expiry |
| `scripts/test-guard-denied-redrive.sh` | new |

## Implementation

### Phase 1: Establish the boundary

- [x] Reproduce on a scratch daemon with a stub Codex-shaped agent: seed a request, emit a `guard-denied` row for the role, idle the pane → task expires at timeout; failing test kept — `scripts/test-guard-denied-redrive.sh` section A; see [Findings for Phase 1](#findings-for-phase-1)
- [x] Pin Codex's turn-ending on a denied tool call if a stub can show it; otherwise record it as observed, not pinned — **observed (2026-09-23), not pinned**: the claude-stub stands in for the turn-ended state (it consumes with a receipt and never answers); no stub can show a provider's own behaviour

### Findings for Phase 1

Recorded 2026-11-27 from the `implement` node of run `1795811929-55-defect-backlog-loop`. The script
was run twice through the run agent (17:21, then 17:26 after the review's fixes to the denial helper
and the section N baselines); log `/tmp/test-guard-denied-redrive.log`: **16 checks, 13 passed,
3 failed, floor 16, exit 1.** The three failures are this spec's acceptance assertions, so the
failing run is the reproduction; Phase 2 turns them green.

| Finding | Detail |
|---------|--------|
| The defect reproduces (section A, stall watchdog off) | A real `hook guard` denial of a bundled `./build.sh` on a role holding a consumed, receipted graph dispatch: 0 lines typed into the pane afterwards, the only row a `guard-denied`, and the node `failed` at its 30 s cap with output `node timeout` — the task `timed-out`, the denial named nowhere |
| The negative control already holds (section N) | A denial with no in-flight request types nothing and writes no `guard-denied-redrive` or `guard-denied-alert` row — 5/5 |
| "Nothing re-drives it" is no longer wholly true (section B, boundary only, not asserted) | With the stall watchdog on, [MUX-232](../completed/MUX-232-redrive-arrives-truncated-with-no-id-and-no-reply-route.md)'s re-drive reached the denied graph dispatch once — **without** the denial text. Phase 2 must settle how the guard re-drive and the stall re-drive share "at most one re-drive per task": two roads may both fire on one task |
| The denial is emitted by a `hook guard` call from the script, not by the stub agent | The stub cannot issue a tool call; the row is real, its author is the test. `toolu_mux190` is a fixed id |

### Phase 2: Fix

- [x] Daemon: match `guard-denied` rows to in-flight tasks by role and time; one re-drive per task through `RedriveTask` with the denial text; lifecycle row `guard-denied-redrive` — `bus/guard_denial.go`: the hook files each denial bound to the tasks it interrupted (`RecordGuardDenial`, called from `hookGuard`), the daemon's `checkGuardDenials` → `ProcessGuardDenials` acts once per task; the text rides `redriveBlock`. Built differently from the wording — see [Findings for Phase 2](#findings-for-phase-2)
- [x] Fallback: re-drive refused or spent → one alert to edit naming task, run and node; lifecycle row `guard-denied-alert` — `alertGuardDenial`, an `event` `guard-denied` to edit
- [x] Node output names the denial; a task that still expires records the denial as its cause — `GuardDenialNote` in `harvestRunningNode`, a `[guard-denied <role> <tool>: <reason>]` prefix on success, expiry and node timeout
- [x] Unit tests: denial with task → re-drive; denial without task → nothing; second denial on the same task → alert, no second re-drive; working pane → no injection — `bus/guard_denial_test.go`, 14 tests; `./test.sh` passed 17:46

### Findings for Phase 2

Recorded 2026-11-27 from the Phase 2 `implement` and `fix` nodes of run `1795811929`. The first
review returned 2 must-fix and 1 should-fix (the stall and guard roads could both re-drive; a denial
on a task that ended before the daemon's poll was lost; the idle grace started at the denial); the
re-review at 17:47 passed 0/0/0. `./test.sh` passed 17:46. The script, re-run through the run agent
at 17:44 (log `/tmp/test-guard-denied-redrive.log`): **22 checks, 22 passed, 0 failed, floor 22.**

| Finding | Detail |
|---------|--------|
| **A Codex agent gets the alert, never the re-drive** | `redriveText` refuses any provider that does not poll its own inbox (`SelfPollsInbox` — Claude only): a re-drive's row is already consumed, and those providers rebuild a payload from the inbox. So for the agent this spec is named after, and for OpenCode, the outcome is Decision 1's fallback — one alert to edit naming task, run and node — plus the node output naming the denial. The re-drive rescues Claude roles only. A scrape-road Codex agent runs no `hook guard`, so no denial is filed for it at all. Within the user's decision as worded ("where the re-drive … cannot be made"), but the headline case is not rescued |
| The denial is filed by the hook, not read from the lifecycle row | The `guard-denied` row is cut at 160 runes (dropping the reason) and the log rotates; `RecordGuardDenial` writes `guard-denials/pending/` with the task ids bound at denial time, so a task answered or expired before the daemon's next poll is still annotated (`guard-denied-annotated`). Per-task state lives in `guard-denials/tasks/<id>.json`, on disk, so a daemon restart keeps it |
| One re-drive **after the first denial**, across every road | Once a task holds a denial the guard road owns its re-drives: `RedriveTask`, the force-redrive batch and the stall watchdog all refuse it (`GuardDenialOwns`, row `redrive-withheld-guard-denied`). A stall re-drive from *before* the denial is not counted — it predates the refusal the agent needs to be told about. This settles Phase 1's shared-budget question; the review asked for the scope to be recorded here |
| The idle wait is 10 s, continuous | A denial on a working pane is deferred (`guard-denied-redrive-deferred`); edit is alerted once the pane has read idle for 10 s without a break (`guardDenialIdleGraceSecs`; a working sighting restarts it) with the task unanswered. An answer inside the grace alerts nothing |
| A second denial alerts even when the first was only deferred | `applyGuardDenial` tests the denial count before the working pane, so a Claude agent that retries into a second refusal mid-turn alerts edit at once, with no re-drive ever made. This is the second criterion as worded; it is noted because that agent may still answer |
| The text is appended, not prepended | The denial sentence follows the request's payload in the re-drive block, ahead of the reply command. The first criterion says "prepended" |
| Lifecycle rows beyond the two the spec names | `guard-denied-annotated`, `guard-denied-redrive-deferred`, `redrive-withheld-guard-denied` |

### Phase 3: Docs

- [x] `docs/hooks.md` guard section and CLAUDE.md's evidence-guard bullet gain the re-drive sentence — `docs/hooks.md` gains "A denial on an in-flight request is acted on" after the evidence rule (a situation table and the not-covered note, written by plan); the CLAUDE.md bullet was written by edit. `docs/hooks.md`'s link to this spec was retargeted to `requirements/completed/` at close-out

### Phase 4: Integration test

- [x] Create `scripts/test-guard-denied-redrive.sh` — live scratch daemon, stub agent pane — scratch daemons and the claude-stub on a private tmux server (`private_tmux_init`), muxcode and the stub built from the tree
- [x] Test: in-flight request + `guard-denied` row + idle pane → one re-drive within one poll carrying the denial text, `guard-denied-redrive` row present; the stub answers and the node completes naming the denial — section A (re-drive and row) and section C (the stub runs the re-drive's reply command; the node succeeds, its output naming the denial)
- [x] **Negative control:** `guard-denied` row with no in-flight task → no lifecycle action row — section N, after waiting for section A's task to settle `timed-out`
- [x] Test: busy pane → no injection; once it idles unanswered, one alert to edit naming task and node — section D: `guard-denied-redrive-deferred`, nothing typed and no alert while the stub's spinner shows (`CLAUDE_STUB_BUSY_FILE`), then one `guard-denied-alert` and an alert to edit naming task, run and node, still nothing typed
- [x] Test: a second denial on the same task → alert, no second re-drive — section A2
- [x] **Negative control:** a build from a copy of the tree without the watch leaves the node to expire as `timed-out` with no denial in its output — section P: a copy with the hook's filing and the daemon's check removed; nothing typed, the node fails with output exactly `node timeout`, its task `timed-out`, no `guard-denied-redrive` row
- [x] Coverage floor; run and record counts here

Run through the run agent on 2026-11-27 18:04 (log `/tmp/test-guard-denied-redrive.log`): **57 checks,
57 passed, 0 failed, coverage floor 57 met, exit 0** — sections A, A2, N, B, C, D and P. The phase
worker's report quotes the 17:57 run (55); two checks were added by the review's fixes (section N
waits for a settled task, section P asserts the bare `node timeout` and the `timed-out` task).
`./test.sh` passed at 18:05 and the review at 18:06 returned 0 must-fix, 0 should-fix, 0 nits. Sections
A2 and D give the second and third acceptance criteria the script coverage they lacked at Phase 2.
Every section runs a Claude-road stub: the Codex alert road is covered by
`TestGuardDenialUndeliverableRedriveAlerts` only, with no live Codex run.

## Out of scope

- Making Codex retry on its own — provider behaviour.
- The guards' rules themselves.

## Status

Complete

Closed out 2026-11-27 by `55-defect-backlog-loop` run `1795811929` at 22/22: four phases and eight
acceptance criteria checked. What shipped rescues a guard-denied **Claude** agent with one re-drive
carrying the guard's reason; a Codex or OpenCode agent — the case this spec is named after — gets one
alert to edit and a node record naming the denial, since the re-drive road cannot reach it.

Activated 2026-11-27 by `55-defect-backlog-loop` run `1795811929` (`select` node), at 0/22. Unparked
2026-11-24 on the user's decision above.

All four phases and all eight criteria verified 2026-11-27 (22/22). For a Codex agent the fix
delivers the alert and the node record, not the re-drive — see
[Findings for Phase 2](#findings-for-phase-2). The tree also holds a change to `bus/launch.go` and `bus/launch_test.go` that is not this
spec's: it is [MUX-188](../completed/MUX-188-spawn-worker-launch-sends-edit-a-stray-startup-request.md), widened
on the user's request of 2026-11-27, closed out the same day at 21/21 and shipping in this spec's PR.

Filed 2026-09-24 on the user's instruction relayed by edit, from a 2026-09-23 observation (codex
build agent, bundled `./build.sh`). Mechanism verified in the tree; the incident is reported, not
re-verified in the rotated log.
