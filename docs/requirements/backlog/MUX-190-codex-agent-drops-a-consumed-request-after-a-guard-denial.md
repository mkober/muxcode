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

- [ ] A `guard-denied` row on a role with an in-flight request is followed, within one daemon poll, by one re-drive of that request with the denial's text prepended
- [ ] At most one re-drive per task: a second denial on the same task re-drives nothing and alerts edit instead
- [ ] When the re-drive is refused — the pane is working, or `RedriveTask` cannot deliver — edit gets one alert naming the task and, for a graph dispatch, the run and node; nothing is typed into the pane
- [ ] A graph node whose agent was guard-denied names the denial in its output (`guard-denied` and the guard's reason); if the task still expires, the cause recorded is the denial, not a bare `timed-out`
- [ ] **Negative control:** a guard denial on a role with no in-flight request produces no re-drive and no node change
- [ ] The Claude road is unchanged where the agent already retries: a re-drive into a working pane is refused by `AgentIsWorking`, as today, and an agent that answers after its own retry gets no alert
- [ ] Reproduced first by a failing test that pins the current behaviour (denial → nothing → timeout)
- [ ] `bash scripts/test-guard-denied-redrive.sh` passes

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

- [ ] Reproduce on a scratch daemon with a stub Codex-shaped agent: seed a request, emit a `guard-denied` row for the role, idle the pane → task expires at timeout; failing test kept
- [ ] Pin Codex's turn-ending on a denied tool call if a stub can show it; otherwise record it as observed, not pinned

### Phase 2: Fix

- [ ] Daemon: match `guard-denied` rows to in-flight tasks by role and time; one re-drive per task through `RedriveTask` with the denial text; lifecycle row `guard-denied-redrive`
- [ ] Fallback: re-drive refused or spent → one alert to edit naming task, run and node; lifecycle row `guard-denied-alert`
- [ ] Node output names the denial; a task that still expires records the denial as its cause
- [ ] Unit tests: denial with task → re-drive; denial without task → nothing; second denial on the same task → alert, no second re-drive; working pane → no injection

### Phase 3: Docs

- [ ] `docs/hooks.md` guard section and CLAUDE.md's evidence-guard bullet gain the re-drive sentence

### Phase 4: Integration test

- [ ] Create `scripts/test-guard-denied-redrive.sh` — live scratch daemon, stub agent pane
- [ ] Test: in-flight request + `guard-denied` row + idle pane → one re-drive within one poll carrying the denial text, `guard-denied-redrive` row present; the stub answers and the node completes naming the denial
- [ ] **Negative control:** `guard-denied` row with no in-flight task → no lifecycle action row
- [ ] Test: busy pane → no injection; once it idles unanswered, one alert to edit naming task and node
- [ ] Test: a second denial on the same task → alert, no second re-drive
- [ ] **Negative control:** a build from a copy of the tree without the watch leaves the node to expire as `timed-out` with no denial in its output
- [ ] Coverage floor; run and record counts here

## Out of scope

- Making Codex retry on its own — provider behaviour.
- The guards' rules themselves.

## Status

**Backlog** — unparked 2026-11-24 on the user's decision above. Not started.

Filed 2026-09-24 on the user's instruction relayed by edit, from a 2026-09-23 observation (codex
build agent, bundled `./build.sh`). Mechanism verified in the tree; the incident is reported, not
re-verified in the rotated log.
