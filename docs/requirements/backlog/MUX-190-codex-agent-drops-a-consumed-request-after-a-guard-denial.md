# MUX-190: A Codex Agent Drops a Consumed Request After a Guard Denial

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
| The guard denies and logs | `cmd/hook.go:221-251`: `FormatGuardBlockFor` in the provider's dialect, `guard-denied` lifecycle row | The agent sees why; the bus sees a row |
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

- [MUX-154](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md), [MUX-153](./MUX-153-codex-test-agent-cannot-run-the-suite.md) — Codex agent behaviour on the hook road.
- [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) — node outcomes named for the wrong reason.
- The codex-approval watchdog (CLAUDE.md, daemon watchdogs) — the precedent for the daemon *acting* on a Codex prompt state rather than waiting for the task clock.

## Requirements

### Acceptance criteria

- [ ] A `guard-denied` row on a role with an in-flight request is followed, within one daemon poll, by a re-drive of that request with the denial's guidance prepended (or by a `guard-denied` alert to edit naming the task and node) — the behaviour chosen in Phase 1 is recorded here
- [ ] A graph node whose agent was guard-denied and then idled is recorded with a cause naming the denial (`guard-denied` in the node output), not `timed-out`
- [ ] **Negative control:** a guard denial on a role with no in-flight request produces no re-drive and no node change
- [ ] The Claude road is unchanged where the agent already retries (a re-drive into a working pane is refused by `AgentIsWorking`, as today)
- [ ] Reproduced first by a failing test that pins the current behaviour (denial → nothing → timeout)
- [ ] `bash scripts/test-guard-denied-redrive.sh` passes

### Technical approach

The guard already writes a lifecycle row; the daemon poll can watch for `guard-denied` rows newer
than each in-flight task's start, on that task's role, and act once per task: prefer a **single
re-drive** carrying the denial text as context (the agent is told what to change), falling back to
an alert if the pane is working. Cap at one per task so a repeat denial does not loop. Tie the
node's outcome to the row when the task then expires.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/cmd/hook.go:221-260` | guard denial output and `guard-denied` row |
| `tools/muxcode/bus/evidence_guard.go`, `bus/listener_guard.go`, `bus/guard.go` | the guards that deny |
| `tools/muxcode/daemon/daemon.go` (`checkTrackedTasks`, task-timeout) | where the watch and re-drive belong |
| `tools/muxcode/bus/deliver.go` (`RedriveTask`, `AgentIsWorking`) | the re-drive road and its busy-pane refusal |
| `tools/muxcode/bus/graph_exec.go` | node outcome on task expiry |
| `scripts/test-guard-denied-redrive.sh` | new |

## Implementation

### Phase 1: Establish the boundary

- [ ] Reproduce on a scratch daemon with a stub Codex-shaped agent: seed a request, emit a `guard-denied` row for the role, idle the pane → task expires at timeout; failing test kept
- [ ] Decide re-drive vs alert ([Decision 1](#decision-1--re-drive-or-alert)); record here

### Phase 2: Fix

- [ ] Daemon: match `guard-denied` rows to in-flight tasks by role and time; one action per task; lifecycle row `guard-denied-redrive` or `guard-denied-alert`
- [ ] Node outcome names the denial when the task still expires
- [ ] Unit tests: denial with task → action; denial without task → nothing; second denial on the same task → no second re-drive

### Phase 3: Docs

- [ ] `docs/hooks.md` guard section and CLAUDE.md's evidence-guard bullet gain the re-drive sentence

### Phase 4: Integration test

- [ ] Create `scripts/test-guard-denied-redrive.sh` — live scratch daemon, stub agent pane
- [ ] Test: in-flight request + `guard-denied` row + idle pane → re-drive (or alert) within one poll, lifecycle row present
- [ ] **Negative control:** `guard-denied` row with no in-flight task → no lifecycle action row
- [ ] Test: busy pane → alert, no injection
- [ ] Coverage floor; run and record counts here

## Open decisions

### Decision 1 — re-drive or alert

A re-drive puts the guard's own guidance in front of the agent and usually resolves it; an alert
keeps the daemon from typing into a pane on a road where MUX-171's lesson says be careful. The
re-drive already refuses a working pane, which is the case that matters.

## Out of scope

- Making Codex retry on its own — provider behaviour.
- The guards' rules themselves.

## Status

Backlog

Filed 2026-09-24 on the user's instruction relayed by edit, from a 2026-09-23 observation (codex
build agent, bundled `./build.sh`). Mechanism verified in the tree; the incident is reported, not
re-verified in the rotated log. Not started.
