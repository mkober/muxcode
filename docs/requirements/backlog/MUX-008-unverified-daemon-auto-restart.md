# Unverified Daemon Auto-Restart

**Tracking:** [mkober/muxcode#8](https://github.com/mkober/muxcode/issues/8)

The daemon's agent auto-restart path is fire-and-hope: `RestartLocalAgent()` sends `C-c`, sleeps a fixed 500ms, sends the relaunch line, and returns `nil` unconditionally. Nothing verifies the old process exited or that the relaunch produced a live agent — so a failed restart is recorded as `agent-recovered` and is indistinguishable from a successful one at every layer above it.

## Context

### Symptom reported (field report, `is-advising-gateway` subsession, 2026-08-17)

After SIGTERM of a wedged `build` agent, daemon auto-restart brought up a process **detached from the pane** — pane 1 sat at a bare bash prompt while an `opencode --agent build` process ran outside it. The agent looked alive to every liveness check while being unreachable by tmux wake-ups. The operator had to kill the orphan and relaunch manually. (Symptom link to the code path below is plausible, not proven.)

### What the code shows (verified)

`bus/health.go` `RestartLocalAgent()` — the daemon's auto-restart path (`daemon/daemon.go` ~:1615):

1. `tmux send-keys -t <pane> C-c`
2. `time.Sleep(500 * time.Millisecond)` — fixed, unconditional
3. `tmux send-keys -t <pane> "muxcode agent launch <role>" Enter`
4. `return nil`

No verification at any step:

- No check that the old process actually exited. The 500ms is a guess; if the CLI is still shutting down, the launch text is delivered to the *dying process's* stdin rather than the shell, and is swallowed. The pane then settles at a bare shell prompt with no agent — matching the reported symptom.
- No check that the relaunch produced a live agent in the pane.
- The function returns `nil` in every one of these cases, so the daemon records a successful restart and emits `agent-recovered`.

Compare `bus/reload.go` `ReloadAgent()`, which polls for launch verification for up to 15s after its send-keys. The supervised path verifies; the **automatic** path — which runs unattended and therefore needs it most — does not.

### Why it matters beyond this incident

`IsAgentAlive` → `provider.IsAlive` is pane-based, and its startup heuristic returns alive when the capture contains `"muxcode agent launch"`, `"claude"`, or `"opencode"`. A pane holding a swallowed launch line can therefore read as alive indefinitely. Combined with the `nil` return from `RestartLocalAgent`, a failed restart is invisible.

## Requirements

### Acceptance criteria

- [ ] `RestartLocalAgent` waits for actual process exit (bounded poll) instead of a fixed 500ms
- [ ] After relaunch, poll for a live agent in the pane; return a non-nil error on timeout
- [ ] Daemon treats a failed restart as a failed attempt (counts toward the 3-attempt cap) rather than emitting `agent-recovered`
- [ ] Detect the orphan case: an agent process for the role that is not a descendant of the pane shell
- [ ] Regression test: simulate a slow-exiting agent and assert the relaunch is not swallowed

### Key files

| File | Change |
|------|--------|
| `bus/health.go` | `RestartLocalAgent()`: bounded exit poll, post-relaunch verification, non-nil error on failure |
| `daemon/daemon.go` | Restart caller (~:1615): failed restart counts toward attempt cap, no `agent-recovered` on failure |
| `bus/agent_health.go` | Orphan detection: role process not descended from the pane shell |
| `bus/reload.go` | Reference implementation — reuse/extract its 15s launch-verification poll |

## Implementation

### Phase 1: Verified restart

- [ ] Bounded poll for old-process exit before sending the relaunch line
- [ ] Post-relaunch launch-verification poll (reuse `ReloadAgent()` mechanism); non-nil error on timeout
- [ ] Daemon: failed restart increments the attempt counter and emits `agent-restarting`/failure, never `agent-recovered`
- [ ] Unit tests: slow-exit simulation, relaunch-verification timeout path

### Phase 2: Orphan detection

- [ ] Detect a role process that is not a descendant of the pane shell; surface as a finding/alert
- [ ] Unit test: orphan process scenario

### Phase 3: Integration test

- [ ] Create `scripts/test-agent-restart.sh` (requires a running muxcode session)
- [ ] Test: kill an agent process → assert daemon restarts it and the pane holds a live, reachable agent
- [ ] Test: simulate slow exit → assert the launch line is not swallowed and the agent comes back
- [ ] Run the script and verify all checks pass

## Provenance

Filed by the edit agent from a field report in the `is-advising-gateway` subsession (2026-08-17). Code fragility verified in-tree; the incident's symptom link is plausible, not proven. The same investigation surfaced CLAUDE.md drift in the `bus/agent_health.go` export list (phantom `StartAgent()`/`StopAgent()`/`CheckAgentHealth()`), fixed alongside this filing.

## Findings at selection (2026-11-17) — parked, not activated

`55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` reached this spec as its
best-ranked unparked row (rank 19). Plan read it end to end against the tree at `cb6179e` and did
not activate it. It was written against August's restart code; three months of rework
(MUX-126, MUX-136, MUX-139, MUX-006, MUX-184) have overtaken part of it.

| Spec says | Tree today |
|-----------|------------|
| `RestartLocalAgent` sends `C-c`, sleeps a fixed 500 ms, sends the launch line and returns `nil` | **Still true in substance.** `RestartLocalAgent` (`bus/health.go:309`) takes the reload marker and calls `scrapeAndRelaunch`, which scrapes the pane for a resume id, sends `C-c`, sleeps `restartInterruptDelay` (500 ms), sends the launch line and returns. No check that the old process exited or that an agent came up |
| The daemon caller is at `daemon/daemon.go` ~`:1615` | `restartDeadAgent`, `:2293` |
| A failed restart is recorded as `agent-recovered` | **No longer how it works.** `agent-recovered` is emitted on a later sweep that finds the agent alive, and for a daemon-restarted agent only once `verifyRestricted` and `definitionApplied` pass (`:2164–2175`). Whether a swallowed launch line can still read as alive there was not checked |
| A failed restart should count toward the 3-attempt cap | **Already does.** `agentRestarts[role]++` runs before the restart is attempted |
| Orphan detection: a role process not descended from the pane shell | Not there. MUX-184's reaper proves ownership for **dead** sessions only; a live session's detached agent is not covered. It would need `ps`, which a Codex sandbox denies |
| Phase 3's script "requires a running muxcode session" | The suite's scripts are now hermetic — private tmux server, `claude-stub` — and live-session scripts are skipped by `test-all.sh` |

**Why parked.** Criteria 1 and 2 stand. Criterion 3 is met or moot. Criterion 4 needs a design that
does not exist, in territory two other specs have touched. And the restart path now carries rules
this spec predates — edit is only ever resumed, an agent relaunched without its definition is
stopped. Rewriting the criteria to today's code is a scoping decision, and it is not the loop's or
plan's to make unattended.

**What would unpark it:** the user's word on scope — at least whether orphan detection stays in
this spec, and whether the remaining gap is "verify exit and liveness inside `scrapeAndRelaunch`"
or something wider.

## Status

**Backlog** — 0/15, parked 2026-11-17 at selection; see the findings above.
