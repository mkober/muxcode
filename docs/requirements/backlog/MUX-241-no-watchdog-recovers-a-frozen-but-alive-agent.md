# MUX-241: No Watchdog Recovers a Frozen-but-Alive Agent

**Tracking:** [mkober/muxcode#255](https://github.com/mkober/muxcode/issues/255)

**Split from:** [MUX-010](./MUX-010-delegation-message-hygiene.md) on the user's decision of
2026-11-24, relayed by edit. This is MUX-010's primary goal, moved whole. **Parked at filing:** the
signal that tells a frozen agent from a busy one is undesigned.

An agent whose TUI has hung — the process alive, every keystroke ignored — is invisible to every
recovery road muxcode has. The daemon restarts only a **dead** agent; `reload`, `GracefulStop` and
`RestartLocalAgent` recover by `tmux send-keys`, which a frozen pane ignores; `agent-health
--stop/--start` toggles a marker file. The remedy is a human finding the pid and running `kill`.

## Context

### Origin — observed incident

In the `is-service-providers-gateway` subsession the commit agent stopped receiving messages for
about 8.5 minutes. `muxcode diagnose commit` reported `active-with-stale-messages`. In order:

1. **Wedged active.** A "watch the PR checks to completion" request had run `gh pr checks --watch`
   in the commit agent's own pane. The pane showed "1 shell still running" and stayed active; the
   daemon delivers only to idle agents, so seven chain messages piled up unnotified.
2. **The indicator was stale.** No live `gh` process existed. "Find and kill the hung child" was a
   dead end.
3. **Frozen TUI.** Queued text sat unsent in the input box. `Escape`, `Ctrl-U` and `Ctrl-C` sent by
   `tmux send-keys` had no effect.
4. **Every muxcode recovery road failed**, each for the reason above.
5. **Resolution: `kill -TERM` on the agent's own process.** The now-dead process tripped the
   daemon's restart; the inbox, on disk, survived.
6. **Post-restart wake gap.** The fresh agent sat idle without running its startup inbox check, and
   the pending request waited for a manual wake.

### What has changed since this was written

Read by plan from `CLAUDE.md` on 2026-11-24, not re-verified against a frozen agent — nobody has
one to test with:

| Then | Now |
|------|-----|
| No documented recovery for a frozen TUI | `CLAUDE.md` "Keep agents deliverable": kill the OS process (`-TERM`, then `-KILL`) and let the daemon restart it — still by hand |
| Respawn discarded the conversation | Health restarts are resume-first (MUX-139) |
| A respawned agent could sit on its inbox | `checkIdleAgents` wakes idle agents holding requests; receipts and `checkPollHealth` re-drive a delivery gap; `diagnose` reports `consumed-unanswered` (MUX-232) |
| `diagnose` on Codex/OpenCode misread a constant `IsIdle` | `wake-blocked-by-task` (MUX-192) |
| A stuck provider was left alone | The stuck-provider reload watchdog (`bus/stuck.go`, cap 3 per role) — whether it sees a frozen Claude TUI is this spec's Phase 1 question |

So item 6 may already be closed and item 4 partly; Phase 1 says which.

## Open decisions — why this is parked

- [ ] **The freeze signal.** How the watchdog tells *frozen* from *legitimately busy* (a long turn
      still making progress). Candidates recorded in MUX-010: pane content static for N s with an
      active state and a stale inbox; an unanswered liveness nudge; no token output for N s. A wrong
      signal kills working agents — and `AgentIsWorking` exists precisely so no road interrupts a
      working pane (MUX-171). This is the user's call and nothing below is buildable without it
- [ ] **Aggressiveness.** Thresholds before a kill, how many attempts, and what happens when
      recovery itself keeps failing
- [ ] **Whether an automatic kill is wanted at all**, or only the manual primitive plus an alert —
      `edit` is resume-only precisely because losing its conversation is not acceptable
- [ ] **Blocking foreground commands** (`--watch`, `tail -f` in a non-watch pane): guidance only,
      as today, or a guard that warns or denies. Listed here because it prevents the same freeze

## Requirements

### Acceptance criteria

Provisional — written from MUX-010's, to be restated once the decisions above are made.

- [ ] A force-kill primitive (`muxcode kill <role>`, or `reload --force-kill`) terminates the
      agent's OS process (TERM, then KILL) so the daemon's restart road takes over; it works on a
      pane that ignores keystrokes
- [ ] The primitive refuses, or asks for `--force`, on a pane `AgentIsWorking` reads as working
- [ ] `edit` is never killed automatically
- [ ] A frozen-but-alive agent — passing `IsAgentAlive`, unresponsive, inbox undeliverable past the
      decided threshold — is detected by the decided signal and either recovered or alerted, per the
      decision
- [ ] A healthy agent in a long turn is never killed by the watchdog (negative control)
- [ ] After a recovery the daemon confirms the relaunched agent consumed its pending inbox, and
      re-wakes or alerts if it did not
- [ ] "1 shell still running" is reconciled against real processes, so a stale indicator is not
      read as a live hang
- [ ] `muxcode diagnose` names the force-kill road for a frozen agent, not a send-keys restart
- [ ] Every automatic action writes a lifecycle row naming the role, the signal and the evidence

### Key files

| File | Purpose / change |
|------|------------------|
| `tools/muxcode/daemon/daemon.go` | Health loop — the watchdog |
| `tools/muxcode/bus/agent_health.go`, `bus/health.go` | `IsAgentAlive` is not enough; the kill road |
| `tools/muxcode/bus/stuck.go` | The existing stuck-provider reload — extend or leave |
| `tools/muxcode/bus/diagnose.go` | Stale-indicator reconciliation, remediation text |
| `tools/muxcode/cmd/` | The force-kill command |
| `docs/architecture.md`, `docs/agent-bus.md`, `CLAUDE.md` | Watchdog list, the command, "Keep agents deliverable" |

## Implementation

### Phase 1: Measure what exists

- [ ] Build a stand-in frozen agent: a process that holds the pane, ignores input and keeps its
      argv — and record whether it is a fair model of the incident
- [ ] Against it, record what each existing road does: the health restart, the stuck-provider
      reload, `deliver --force`, `reload`, `resume --force`, `diagnose`
- [ ] Record whether the post-restart wake gap (item 6) still exists

### Phase 2: Decide

- [ ] The user settles the open decisions above with Phase 1's record in hand; criteria and the
      phases below are rewritten to them

### Phase 3: The force-kill primitive

- [ ] Implement the command; refuse on a working pane without `--force`; lifecycle row
- [ ] Unit tests: kills by pid, escalates TERM to KILL, refuses a working pane, never targets a pid
      it cannot prove is the role's agent

### Phase 4: The watchdog

- [ ] Implement detection on the decided signal and the decided response
- [ ] Verify the relaunched agent drained its inbox
- [ ] Reconcile the stale-shell indicator in `diagnose`; update its remediation text
- [ ] Unit tests: detection, the negative control for a long turn, `edit` excluded

### Phase 5: Integration test

- [ ] Create `scripts/test-frozen-agent-recovery.sh` — muxcode and a frozen-agent stub built from
      the tree, a scratch daemon on a private tmux server (`scripts/lib/private-tmux.sh`)
- [ ] A frozen stub with a pending request: the decided response happens with no manual step, and
      the request is consumed by the relaunched agent
- [ ] Negative control: a stub in a long, progressing turn is left alone past the threshold
- [ ] `muxcode kill <role>` on the frozen stub relaunches it; on a working stub it refuses
- [ ] Coverage floor, and the script is named in `CLAUDE.md`'s scripts row
- [ ] Run the script and confirm all checks pass

## Status

**Backlog — parked at filing 2026-11-24**, 0/29. Split from MUX-010 by plan on the user's decision
relayed by edit. Parked on the same decision: the freeze signal is undesigned. Not started.
