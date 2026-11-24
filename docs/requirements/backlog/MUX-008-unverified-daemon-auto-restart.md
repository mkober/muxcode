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

The sections above describe the code at filing (August). [Findings at selection](#findings-at-selection-2026-11-17)
tables what still stands; the decisions below set the scope from them.

## Decisions (user, 2026-11-24)

Relayed by edit; this spec was parked on its scope and is unparked by them.

| Question | Decision |
|----------|----------|
| (a) Orphan detection (the old criterion 4) | **Dropped from this spec.** It has no design, and a live session's detached agent is outside what MUX-184's reaper proves. To be filed separately if wanted — nothing is filed by this decision |
| (b) What remains | **Verify the old process's exit and the relaunched agent's readiness inside `scrapeAndRelaunch`**; a restart that cannot be verified returns an error |
| The old criterion 3 (a failed restart counts toward the cap, no `agent-recovered`) | **Moot** — `agentRestarts[role]++` already runs before the attempt, and `agent-recovered` is written by a later sweep, not by the restart |
| Rules the restart path gained since filing | **Kept as they are**: edit is resume-only (`ResumeOnlyRole`), and a relaunched agent showing the definition-unavailable banner is stopped (`refuseUnrestricted`) |

**Read in the tree 2026-11-24, for whoever implements it.** The decision names two existing checks
to reuse — `ReloadAgent`'s poll and `resumedSessionReady`. They are not equivalent:

| Check | Where | What it proves |
|-------|-------|----------------|
| `ReloadAgent`'s poll | `bus/reload.go:536-545` — `IsAgentAlive` every 500 ms, up to 15 s | Liveness by pane heuristic. This spec's own Context records that the heuristic reads a pane holding the text `muxcode agent launch` as alive, so a **swallowed launch line passes it**. Reusing it alone would not close the defect, and `ReloadAgent` does not fail when it times out |
| `resumedSessionReady` | `bus/spawn_resume.go:215` | Positive start: the `❯` composer in the live bottom of the capture, below the last exit banner and not the typed launch line. Claude panes only |

So readiness here means the second kind of evidence. What a non-Claude provider's readiness reads
is settled in Phase 1 from what each provider's pane offers; it is not decided here.

## Requirements

### Acceptance criteria

- [ ] `scrapeAndRelaunch` waits for the old process's actual exit with a bounded poll instead of the fixed `restartInterruptDelay` (500 ms), and returns an error when the bound passes with the process still there — the launch line is never typed into a dying process
- [ ] After the launch line is sent, `scrapeAndRelaunch` polls for a positively started agent in the pane and returns a non-nil error on timeout; a pane holding only the typed launch line is **not** ready
- [ ] A restart that returns that error is logged as failed with its reason, and is never followed by `agent-recovered` for that attempt
- [x] A failed restart counts toward the restart cap — moot, already so: `agentRestarts[role]++` precedes the attempt (`daemon/daemon.go:2359`); see the decisions
- [x] Orphan detection — dropped from this spec by the user's decision of 2026-11-24, not done; may be filed separately
- [ ] Edit stays resume-only and the definition-unavailable banner still stops a relaunched agent — both unchanged by the new waits
- [ ] Regression test: a slow-exiting agent — the relaunch is not swallowed; and an agent that never exits — the restart returns an error and types no launch line
- [ ] **Negative control:** a normally exiting agent restarts as today, with no added failure row and within the same sweep

### Key files

| File | Change |
|------|--------|
| `bus/health.go` | `scrapeAndRelaunch` (`:384`): bounded exit poll in place of `restartInterruptDelay`, post-launch readiness poll, error on either timeout. `RestartLocalAgent` (`:309`) returns it |
| `daemon/daemon.go` | `restartDeadAgent`: a returned error is a failed attempt with its reason logged |
| `bus/spawn_resume.go` | `resumedSessionReady` (`:215`) — the readiness evidence to reuse |
| `bus/reload.go` | `ReloadAgent`'s poll (`:536`) — the loop shape to reuse, not its `IsAgentAlive` test |
| `scripts/test-agent-restart.sh` | New |

## Implementation

### Phase 1: Pin and settle the evidence

- [ ] Unit test pinning today's behaviour: `scrapeAndRelaunch` against a pane whose process outlives 500 ms still sends the launch line and returns nil
- [ ] Settle what proves "the old process exited" for a pane (the exit banner, the shell prompt, or the pane's process) and what proves readiness per provider; record both here
- [ ] Check whether a swallowed launch line still reads alive to the sweep that writes `agent-recovered` (not checked at selection); record the answer

### Phase 2: Verified restart

- [ ] Bounded poll for old-process exit before the launch line; error when the bound passes
- [ ] Post-launch readiness poll on the Phase 1 evidence; non-nil error on timeout
- [ ] `restartDeadAgent` logs the failed attempt with its reason; no `agent-recovered` follows it
- [ ] Every caller of `scrapeAndRelaunch` (daemon restart, `muxcode resume`, operator restart) handles the new error; the waits run off the daemon's poll loop or within its keepalive budget
- [ ] Unit tests: slow exit, never exits, launch line swallowed, normal exit (negative control); edit with no resumable session is still left down; the banner stop still fires

### Phase 3: Docs

- [ ] CLAUDE.md's agent-health bullet and `docs/architecture.md`: the restart is verified, and what a failed one logs

### Phase 4: Integration test

- [ ] Create `scripts/test-agent-restart.sh` — hermetic: private tmux server, scratch daemon, `claude-stub`
- [ ] Test: kill an agent → the daemon restarts it and the pane holds a started agent that receipts a request sent afterwards
- [ ] Test: a stub slow to exit (`CLAUDE_STUB_EXIT_DELAY_FILE`) → the launch line is not swallowed and the agent comes back
- [ ] Test: a stub that never exits → the restart is logged failed, no launch line is typed, no `agent-recovered`
- [ ] **Negative control:** a build from a copy of the tree with the two waits reverted swallows the launch line in the slow-exit case
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and record the counts here

## Provenance

Filed by the edit agent from a field report in the `is-advising-gateway` subsession (2026-08-17). Code fragility verified in-tree; the incident's symptom link is plausible, not proven. The same investigation surfaced CLAUDE.md drift in the `bus/agent_health.go` export list (phantom `StartAgent()`/`StopAgent()`/`CheckAgentHealth()`), fixed alongside this filing.

## Findings at selection (2026-11-17)

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
or something wider. **Given 2026-11-24** — see [Decisions](#decisions-user-2026-11-24): orphan
detection is out, and the gap is exactly that.

## Status

**Backlog** — unparked 2026-11-24 on the user's decisions above. Two boxes are ticked as moot and
as dropped, not as work done; nothing is implemented.
