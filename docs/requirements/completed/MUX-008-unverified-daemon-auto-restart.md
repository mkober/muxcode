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

- [x] `scrapeAndRelaunch` waits for the old process's actual exit with a bounded poll instead of the fixed `restartInterruptDelay` (500 ms), and returns an error when the bound passes with the process still there — the launch line is never typed into a dying process
- [x] After the launch line is sent, `scrapeAndRelaunch` polls for a positively started agent in the pane and returns a non-nil error on timeout; a pane holding only the typed launch line is **not** ready
- [x] A restart that returns that error is logged as failed with its reason, and is never followed by `agent-recovered` for that attempt — as built: never on the liveness heuristic; a late start the pane positively shows is still announced
- [x] A failed restart counts toward the restart cap — moot, already so: `agentRestarts[role]++` precedes the attempt (`daemon/daemon.go:2359`); see the decisions
- [x] Orphan detection — dropped from this spec by the user's decision of 2026-11-24, not done; may be filed separately
- [x] Edit stays resume-only and the definition-unavailable banner still stops a relaunched agent — both unchanged by the new waits
- [x] Regression test: a slow-exiting agent — the relaunch is not swallowed; and an agent that never exits — the restart returns an error and types no launch line
- [x] **Negative control:** a normally exiting agent restarts as today, with no added failure row and within the same sweep

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

- [x] Unit test pinning today's behaviour: `scrapeAndRelaunch` against a pane whose process outlives 500 ms still sends the launch line and returns nil
- [x] Settle what proves "the old process exited" for a pane (the exit banner, the shell prompt, or the pane's process) and what proves readiness per provider; record both here — see [Findings for Phase 1](#findings-for-phase-1-2026-11-27)
- [x] Check whether a swallowed launch line still reads alive to the sweep that writes `agent-recovered` (not checked at selection); record the answer — yes for non-Claude, a silent hold for Claude

### Phase 2: Verified restart

- [x] Bounded poll for old-process exit before the launch line; error when the bound passes
- [x] Post-launch readiness poll on the Phase 1 evidence; non-nil error on timeout
- [x] `restartDeadAgent` logs the failed attempt with its reason; no `agent-recovered` follows it
- [x] Every caller of `scrapeAndRelaunch` (daemon restart, `muxcode resume`, operator restart) handles the new error; the waits run off the daemon's poll loop or within its keepalive budget — inside the loop, the keepalive kept fresh by a heartbeat; see the limits in [Findings for Phase 2](#findings-for-phase-2-2026-11-27)
- [x] Unit tests: slow exit, never exits, launch line swallowed, normal exit (negative control); edit with no resumable session is still left down; the banner stop still fires

### Phase 3: Docs

- [x] CLAUDE.md's agent-health bullet and `docs/architecture.md`: the restart is verified, and what a failed one logs — `CLAUDE.md` by the worker; `docs/architecture.md` gained a "Verified agent restart" section and two stale lines in `docs/agents.md` were corrected, by plan

### Phase 4: Integration test

- [x] Create `scripts/test-agent-restart.sh` — hermetic: private tmux server, scratch daemon, `claude-stub`
- [x] Test: kill an agent → the daemon restarts it and the pane holds a started agent that receipts a request sent afterwards
- [x] Test: a stub slow to exit (`CLAUDE_STUB_EXIT_DELAY_FILE`) → the launch line is not swallowed and the agent comes back
- [x] Test: a stub that never exits → the restart is logged failed, no launch line is typed, no `agent-recovered`
- [x] **Negative control:** a build from a copy of the tree with the two waits reverted swallows the launch line in the slow-exit case
- [x] Coverage floor so a skipped section cannot report green
- [x] Run the script and record the counts here — 30 passed, 0 failed; see [Findings for Phase 4](#findings-for-phase-4-2026-11-27)

## Findings for Phase 4 (2026-11-27)

`scripts/test-agent-restart.sh`, run by the run agent at 23:20, log `/tmp/test-agent-restart.log`:
**30 passed, 0 failed**, no SKIP. Hermetic — muxcode and the `claude-stub` built from the tree (the
stub as `claude`, so tmux names it in the pane's foreground), scratch HOME, bus and lifecycle log,
a private tmux server, a scratch daemon on a 3 s health sweep.

| Section | Checks | Shows |
|---------|--------|-------|
| Setup | 3 | Both binaries build; the control daemon builds from a copy with both waits disabled; scratch daemon up |
| A — launch | 3 | Plan and run stubs up, plan's listener running |
| B — normal exit (negative control) | 8 | Plan killed → `agent-relaunch`, a new stub in the pane, `agent-recovered`, **no failure row**; a request sent afterwards is consumed and carries the new agent's receipt |
| C — slow exit, 3 s | 6 | The wedged pane reads as a bare prompt with the stub still running; a second `agent-relaunch`; a new stub replaces it; the launch line was **not** typed into the dying process; `agent-recovered`; no failure row, the 3 s exit fitting the 5 s bound |
| D — never exits | 6 | `agent-restart-failed … run attempt 1/3 … foreground claude … no launch line typed`; `agent-relaunch-unverified`; the stub still the pane's process; no launch line typed; no `agent-relaunch` row; `agent-recovered` withheld across three more sweeps |
| E — control | 4 | A daemon without the two waits relaunches plan in C's slow-exit case and the launch line reaches the dying stub's stdin — swallowed, the defect |

The stub (`scripts/fixtures/claude-stub/main.go`) gained a wedge mode for this — with
`CLAUDE_STUB_WEDGE_FILE` set, a SIGTERM draws the teardown and a bare prompt while the process stays
in the foreground, exits `CLAUDE_STUB_EXIT_DELAY_FILE` seconds after a Ctrl-C (`-1`: never), and
logs and swallows lines typed meanwhile — and a fix: its listener respawn no longer races a
teardown. The first run, before that fix, was 25 passed, 5 failed (the respawn drew a composer under
the wedge prompt).

Notes:

- The coverage floor reads 30 in the script; the 23:20 run executed under a floor of 29, raised
  straight after (script edited 23:20:26, log written 23:20:11). The count met either.
- Fourteen other integration scripts build the same stub. Its change was made in this phase and is
  covered by the run's review and suite, not by a rerun of those scripts.

## Findings for Phase 1 (2026-11-27)

Worked by the run's worker in the session checkout; checked by plan against the logs and the tree.

### Today's behaviour, pinned

Two tests in `tools/muxcode/bus/restart_resume_test.go`:

| Test | Pins |
|------|------|
| `TestRestartLocalAgent_TypesTheLaunchLineIntoALiveProcess_Characterization` | A pane still showing a running agent after `C-c` gets the launch line typed into it and `RestartLocalAgent` returns nil; nothing is read from the pane after `C-c`. Phase 2 inverts it, and its failure text says so |
| `TestIsAlive_UnsubmittedLaunchLineReadsAlive_Characterization` | An OpenCode pane ending in an unsubmitted `muxcode agent launch …` line reads alive; control — a bare shell prompt reads dead |

Run: `/tmp/mux008-pin.sh` by the run agent, 22:46, log `/tmp/mux008-pin.log` — **4 passed, 0 failed**.
Both tests pass on the tree. A mutant copy whose `scrapeAndRelaunch` refuses after `C-c` fails the
restart test at `restart_resume_test.go:77` (`RestartLocalAgent = mutant: build still running, want
today's nil`), so the characterization can fail.

### What proves the old process exited

| Candidate | Verdict |
|-----------|---------|
| Exit banner | Rejected. Claude only, printed only on a clean teardown, and a restart usually finds a pane already dead whose banner is older — it is the resume scrape's evidence, not exit's |
| Shell prompt in a capture (`isShellPrompt`) | Supporting only. A dead Claude's last TUI frame (a `❯` anywhere in the capture) reads as not-a-prompt, and a prompt-shaped output line reads as one |
| **The pane's foreground process** — `tmux display-message -p -t <pane> '#{pane_current_command}'` is the pane's own shell | **Chosen.** Provider-agnostic, asks tmux rather than the screen, needs no `ps` (MUX-161). Shell = the basename of the pane's start shell, falling back to a fixed set (bash, zsh, sh, dash, fish, ksh, tcsh) when that cannot be read. Polled to a bound after `C-c`; still non-shell at the bound → error, no launch line typed |

### What proves readiness, per provider

Polled after the launch line, reading only below the typed launch line. A timeout is a non-nil
error naming the evidence that was missing.

| Provider | Evidence |
|----------|----------|
| All | Foreground process is no longer the shell — the line ran. Necessary, not sufficient: `muxcode agent launch` is itself non-shell |
| Claude | `resumedSessionReady` — the `❯` composer in the live tail below the last exit banner, not the typed launch line. The definition-unavailable banner stays the sweep's `refuseUnrestricted` |
| Codex | `ClassifyPane` on the text below the launch line is not `PaneNotReady` (idle, or a trust/update/approval prompt, which startup acceptance and the watchdog own — the process is up). Scoped below the launch line because `ClassifyPane` matches "codex" and box-drawing anywhere in scrollback |
| OpenCode | `ClassifyPane` below the launch line is `PaneIdle` |
| Local (harness) | `IsHarnessActive` — there is no pane evidence (`ClassifyPane` is constantly not-ready) |

### Does a swallowed launch line read alive to the `agent-recovered` sweep?

Traced in `checkAgentHealth` (`daemon/daemon.go`) and `definitionApplied`
(`daemon/definition_watchdog.go`). Two shapes:

| Shape | Reads | Consequence |
|-------|-------|-------------|
| Whole line eaten by the dying process — pane ends at a bare prompt | Dead (pinned control) | Not hidden: the next sweep restarts again, spending another of the 3 attempts |
| Line reached the shell, Enter eaten — pane ends in the command | **Alive** (pinned for OpenCode; traced, not pinned, for Claude's startup heuristic and Codex's assume-alive fall-through) | OpenCode and Codex: `definitionApplied` passes every non-Claude role, so **`agent-recovered` is written and edit alerted with no agent running**. Claude: the definition probe finds no process and the sweep `continue`s — no `agent-recovered`, but no restart and no alert either, a silent hold |

A dead Claude whose last frame still shows `❯` in the capture also reads alive before any launch
line, and lands in the same hold.

**Answer:** yes for non-Claude (a false recovery), and a silent hold for Claude. Phase 2's readiness
poll closes both by failing the restart itself.

## Findings for Phase 2 (2026-11-27)

Worked by the run's worker in the session checkout; the diff read by plan. The worker's own evidence
is a filtered run (`go test -run` on named `bus` and `daemon` tests, `go vet` on `bus`, `daemon`,
`cmd`); the code steps and the criteria resting on them were held until the graph's test node ran
the full suite on the final tree, recorded under the review round below.

### What changed

| File | Change |
|------|--------|
| `bus/health.go` | `restartInterruptDelay` (the fixed 500 ms) is gone. `scrapeAndRelaunch` polls every `restartPollInterval` (250 ms) for the pane's foreground process to be a shell (`paneForegroundIsShell`, `#{pane_current_command}`) up to `restartExitWait` (5 s); past it, an `agent-relaunch-unverified` row naming the foreground process, `ErrRestartUnverified`, and **no launch line typed**. After the launch line it polls `paneShowsStartedAgent` up to `restartReadyWait` (20 s); past it, the same row and error. Readiness reads only below the last typed `agent launch` (`belowLastLaunchLine`). New exports `AgentStarted` and `ErrRestartUnverified`; `RestartLocalAgent` takes a `heartbeat func()` called on every poll round |
| `daemon/daemon.go` | `restartDeadAgent`: an error writes `agent-restart-failed "<role> attempt N/3: <reason>"` and marks `restartFailed[role]`; an unverified restart keeps `daemonRestarted`, so a late start still passes `verifyRestricted`. `checkAgentHealth`: a `restartFailed` role counts alive only when `bus.AgentStarted` says so, not by the liveness heuristic. The health restart and the Ollama relaunch pass `bus.TouchKeepaliveDaemon` as heartbeat |
| `bus/resume.go`, `bus/restart_agents.go` | Pass a nil heartbeat; `muxcode resume` and the operator restart return the error to the CLI and per role, as before |

### Tests and their run

`/tmp/mux008-phase2-pin.sh` by the run agent, 23:04, log `/tmp/mux008-phase2-pin.log` — **18 passed,
0 failed**, on the tree with the review fix below. Earlier runs: two failed on a test assertion and
a script floor, both corrected; the 22:57 run was 16 passed, 0 failed before the fix.

| Test | Covers |
|------|--------|
| `TestRestartLocalAgent_WaitsForTheOldProcessToExit` | Control — already exited; slow exit waited out; never exits → error, no launch line, row naming the foreground process. Replaces Phase 1's `…TypesTheLaunchLineIntoALiveProcess_Characterization`, as its inversion |
| `TestRestartLocalAgent_SwallowedLaunchLineIsUnverified` | A pane holding only the typed launch line → `ErrRestartUnverified` |
| `TestRestartLocalAgent_ShellPromptWithComposerGlyphIsUnverified` | The same pane text with a `zsh` foreground is unverified and with a `claude` foreground is started (review fix) |
| `TestPaneShowsStartedAgent`, `TestIsShellCommand` | Readiness per provider, including evidence in scrollback above the launch line not counted, and a shell or unreadable foreground never ready; shell names |
| `TestCheckAgentHealth_UnverifiedRestartIsNotRecovered` | One failure row with attempt and reason; a heuristic-alive sweep writes no `agent-recovered`, a started sweep writes one; control — a verified restart recovers on the next sweep with no failure row |
| `TestCheckAgentHealth_LateStartWithoutDefinitionIsStopped` | The banner stop still fires after an unverified late start |

A mutant copy with both waits disabled fails the never-exits, slow-exit and swallowed-line cases
(`restart_resume_test.go:126`, `:131`, `:156`); its already-exited control still passes. A second
mutant without the readiness foreground check fails the `zsh` case (`:189`) while the `claude`
control passes. Phase 1's `TestIsAlive_UnsubmittedLaunchLineReadsAlive_Characterization` is kept —
the heuristic is routed around, not changed.

### Review round (23:03): one must-fix, fixed

The review found that readiness never checked the foreground process: a launch that fails back to a
shell prompt drawn with `❯` passed `resumedSessionReady`, so the restart returned nil and
`AgentStarted` could clear `restartFailed` with no agent running. The test stub hid it — its
foreground stayed `-bash` after the launch. Fix: `paneShowsStartedAgent` now also requires a
readable, non-shell `pane_current_command`, alongside the provider's pane evidence; the local
harness path is unchanged, and the stub reports the agent as foreground after the launch.

The full `./test.sh` passed at 23:02 (both modules) on the code before this fix, and again at
23:05–23:07 on the fixed tree (last code edit 23:03:52): every package `ok`, no failure, log
`/tmp/muxcode-test-logs/20261127-230545-97870/`. That run includes the `muxcode resume` and
operator-restart tests the filtered pin did not. The second review, 23:08, returned 0 must-fix,
0 should-fix, 0 nits. Phase 2's code steps and criteria 1, 2, 3, 6 and 8 were ticked on that.

### Limits and deviations, as built

- **The waits run inside the daemon's poll loop.** A restarted role can hold the sweep for up to
  5 s + 20 s; the heartbeat keeps the keepalive fresh so the monitor does not relaunch the daemon,
  but graph ticks and delivery wait behind it — and several agents dying together wait in series.
  The Phase 2 step asked for "off the poll loop or within its keepalive budget"; this is the second
  only by way of the heartbeat.
- **The shell test differs from Phase 1's finding.** Phase 1 settled on the pane's own start shell;
  the code compares against the daemon's `$SHELL` and a fixed list. A pane running a shell that is
  neither would never read as exited, and every restart of it would fail with no launch line typed.
- **A late start is still announced.** After a failed restart, `agent-recovered` is written once the
  pane positively shows a started agent. The criterion's "never followed by `agent-recovered` for
  that attempt" is read as: never on the liveness heuristic alone.
- `muxcode resume` and the operator restart now also wait for readiness, up to 20 s, and return the
  error when it is not seen.

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

**Complete** — closed out 2026-11-27 by `55-defect-backlog-loop` run `1795811929` at 24/24.

### What shipped

| Area | Change |
|------|--------|
| `bus/health.go` | `scrapeAndRelaunch` waits up to 5 s after `C-c` for the pane's foreground process to be a shell, and types no launch line past that bound; after the launch line it waits up to 20 s for a started agent — a non-shell foreground process and the provider's evidence below the typed line. Either bound passing writes `agent-relaunch-unverified` and returns `ErrRestartUnverified` |
| `daemon/daemon.go` | A failed restart is logged `agent-restart-failed <role> attempt N/3: <reason>`; that role counts alive only once its pane shows a started agent, so the liveness heuristic alone never writes `agent-recovered` |
| Callers | The daemon's health restart, the Ollama relaunch, `muxcode resume` and the operator restart all take the error |
| Tests | Unit tests in `bus/restart_resume_test.go` and `daemon/agent_resume_test.go`; `scripts/test-agent-restart.sh`, 30 passed, 0 failed, floor 30 |
| Docs | `CLAUDE.md`, `docs/architecture.md` ("Verified agent restart"), `docs/agents.md` |

Of the eight acceptance criteria, six are met and two are ticked as moot and as dropped by the
user's decision of 2026-11-24, not as work done.

### Left as recorded

- The waits run inside the daemon's poll loop — up to 25 s per restarted role, in series — with the
  keepalive kept fresh by a heartbeat. Graph ticks and delivery wait behind a restart.
- The shell test compares against the daemon's `$SHELL` and a fixed list, not the pane's own start
  shell that Phase 1 settled on. A pane on a shell that is neither would fail every restart.
- A late start after a failed restart is still announced `agent-recovered`, once the pane
  positively shows a started agent.
- The Claude and Codex "unexecuted launch line reads alive" cases are traced, not pinned by a test;
  only OpenCode's is.
- The integration run executed under a floor of 29, raised to 30 straight after.
- ~~The shared `claude-stub` fixture changed; the other scripts that build it were not rerun.~~
  **Resolved 2026-11-27 23:41, after close-out:** 14 scripts build the stub, this spec's own among
  them; the other 13 were re-run against the final stub (last edited 23:18) — 13 passed, 0 failed
  (`test-agent-resume`, `test-auto-startup-gating`, `test-diagnose-dead-agent`,
  `test-edit-auto-resume`, `test-guard-denied-redrive`, `test-orphan-reaper`, `test-pane-state-env`,
  `test-redrive-reply-route`, `test-reload-defers-dispatch`, `test-reload-hosted-role`,
  `test-reload-keeps-dispatch`, `test-restart-definition`, `test-windowless-routing`);
  `test-agent-restart` was left out as already run at 30/0. The limit as first written said "14
  other", one too many.

### How it got here

Filed 2026-08-17 from a field report; reached and parked by the loop on 2026-11-17 as overtaken by
three months of restart rework; unparked 2026-11-24 on the user's scope decisions; activated and
worked in one pass on 2026-11-27, with one review must-fix in Phase 2 (readiness did not check the
foreground process).
