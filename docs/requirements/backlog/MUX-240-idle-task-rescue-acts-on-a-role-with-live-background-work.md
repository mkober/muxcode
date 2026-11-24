# MUX-240: Idle-Task Rescue Re-Queues and Times Out a Task Whose Role Still Has Live Background Work

**Tracking:** [mkober/muxcode#254](https://github.com/mkober/muxcode/issues/254)

**Split from:** [MUX-112](../completed/MUX-112-idle-task-rescue-closes-live-work.md), closed as
overtaken 2026-11-24. This is the part of it that never shipped.

MUX-112's headline harm is gone: the rescue no longer answers for the agent. Since MUX-182 (defect
4b) its second phase sends the requester an `event:no-answer` notice and times the task out
(`bus.TimeoutTask`), a late real reply still completes the task, graph dispatches are exempt
(`bus.GraphOwnsTask`), and `run` is excluded by default (`idleRescueExcluded`,
`MUXCODE_IDLE_RESCUE_EXCLUDE`).

What `checkIdleTaskCompletionAt` (`daemon/daemon.go`) still does not ask is MUX-112's first
criterion: whether the idle role owns live background work. A role at its prompt with a running
`muxcode proc` or spawn is re-queued its own request after one grace period — a second copy of work
it is already doing — and after a second its requester is told it never answered. The notice also
carries the last 50 pane lines whatever they show, including a dead pane's login banner.

## Decisions (user, 2026-11-24)

Relayed by edit:

- Gate the re-queue and the notice on "the role owns a live proc or spawn". A declined rescue writes
  an `idle-task-rescue-declined` row.
- Skip the notice when the pane shows a dead-pane banner.
- `run` stays excluded by default.

## Requirements

### Acceptance criteria

- [ ] An idle role holding an in-flight task and owning a live proc or spawn is neither re-queued
      nor timed out by the idle rescue while that work runs
- [ ] Each task declined this way writes one `idle-task-rescue-declined` row naming the task, the
      role and the proc or spawn — once per task per episode, not once per 10 s check
- [ ] When the work ends and the role is still idle and unanswered, the rescue proceeds as today
      from a fresh grace period
- [ ] A pane showing a dead-pane banner produces no `no-answer` notice from this road; the role's
      death is the agent-health road's to report
- [ ] `run` is still excluded by default, and `MUXCODE_IDLE_RESCUE_EXCLUDE` still overrides the list
- [ ] Negative control: an idle role with no live proc or spawn is re-queued once and then noticed
      and timed out exactly as today, so a rescue that never fires cannot pass
- [ ] The task's own timeout still bounds a declined task

### Technical approach

- Ask the question the pre-commit safeguard already asks: `RefreshProcStatus()` /
  `CheckProcAlive()` (`bus/proc.go`) and the spawn registry (`bus/spawn.go`), by owner role.
- The dead-pane read reuses what `checkNonHookTasks` and the agent-health road use
  (`PaneResumeID` / the exit-banner detection) — no second definition of "dead".
- The declined row is deduplicated in daemon memory per task id, cleared when the task leaves
  in-flight.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | `checkIdleTaskCompletionAt`, `idleRescueExcluded` |
| `tools/muxcode/bus/proc.go` | Live procs by owner |
| `tools/muxcode/bus/spawn.go` | Live spawns by owner |
| `docs/architecture.md` | The idle-rescue paragraph names the gate and the new row |

## Implementation

### Phase 1: Pin

- [ ] Daemon test, tmux-free through the `agentIdleFn` seam: an idle role with an in-flight task
      and a live proc is re-queued today — record the row it writes
- [ ] Record whether a spawn can back a non-graph task at all; if it cannot, say so here and drop
      "or spawn" from the gate rather than build an unreachable branch

### Phase 2: The gate

- [ ] Decline the re-queue and the notice while the role owns live work; write
      `idle-task-rescue-declined` once per task
- [ ] Restart the grace period when the work ends
- [ ] Skip the notice on a dead-pane banner
- [ ] Tests: live proc declines; no proc rescues (negative control); proc ends then rescue fires;
      dead-pane banner sends no notice; `run` exclusion unchanged
- [ ] Add `idle-task-rescue-declined` to the system-event lists in `bus/diagnose.go` beside
      `idle-task-rescue`, and to `docs/architecture.md`

### Phase 3: Integration test

- [ ] Create `scripts/test-idle-rescue-live-work.sh` — muxcode and the claude-stub built from the
      tree, a scratch daemon on a private tmux server (`scripts/lib/private-tmux.sh`), short grace
      periods
- [ ] A stub agent that starts a `muxcode proc` and returns to its prompt without answering: no
      re-queued request in its inbox, no `no-answer` notice to the requester, one
      `idle-task-rescue-declined` row
- [ ] The proc exits: the rescue then re-queues once and, unanswered, sends the notice and times
      the task out
- [ ] Negative control: the same stub with no proc is re-queued and noticed as today
- [ ] Coverage floor, and the script is named in `CLAUDE.md`'s scripts row
- [ ] Run the script and confirm all checks pass

## Status

**Backlog** — 0/20. Filed 2026-11-24 by plan on the user's decisions relayed by edit, when MUX-112
closed. Not started.
