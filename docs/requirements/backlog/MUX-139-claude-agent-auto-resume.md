# MUX-139: Claude Agent Auto-Resume After Mass Exit

**Tracking:** [mkober/muxcode#66](https://github.com/mkober/muxcode/issues/66)

## Context

When Claude Code processes die out from under muxcode, the daemon's agent-health restart brings back
only the non-excluded specialist roles, ~90s later, as **fresh sessions**. The `edit` orchestrator and
spawn workers stay dead until a human resumes them by hand, and every restarted agent loses its
conversation. Claude Code prints the exact recovery command on exit
(`Resume this session with: claude --resume <session-id>`); muxcode should use it.

### Incident (2026-09-02 13:35:31–13:36:08, evidence gathered by edit)

| Observation | Detail |
|-------------|--------|
| Scope | Every Claude agent on the machine exited inside one 30s window, across **all three** sessions — `muxcode` (plan, run, commit, edit, spawn worker `spawn-1a15e8f0`), `is-advising-gateway` (plan, commit, …), `is-operations-gateway` (run, auto) |
| Survivors | **Every OpenCode agent lived.** The dying set is exactly the Claude-provider agents |
| Manner | Graceful — each pane printed `Resume this session with: claude --resume <id>` and dropped to a shell. No macOS crash reports; `claude` v2.1.258 unchanged since 11:27 |
| muxcode's part | **None.** No lifecycle event preceded the deaths in any session (no reload/stop/cleanup/stale-kill), and muxcode's only `pkill -f` paths are anchored per-session daemon/monitor patterns (`launcher.go killStaleProcesses`, `daemon_health.go RestartDaemon`) that cannot match `claude`. Cause is external — Claude Code-side or an OS-level broadcast termination |

The cause being external is what makes this spec worth filing: muxcode cannot prevent the exit, so
**recovery quality is the only lever it has.**

### The four gaps the incident exposed

| # | Gap | Consequence observed |
|---|-----|----------------------|
| a | `edit` is hard-excluded from auto-restart (`agentHealthExcludedRoles` = `{edit, webhook}`, `agent_health.go:13`) | The user resumed the orchestrator by hand |
| b | Spawn workers are not covered at all | The Phase 3 worker stayed dead with its graph node `implement` still `running`, **stalling run `1788365614-spec-to-pr-64c5fe4b` indefinitely** |
| c | Restarts are fresh launches | Conversation context lost — for a mid-task worker or the orchestrator, that is the expensive part |
| d | Nothing correlates the deaths | The user learns from N separate `agent-down` events per session, never one "mass exit" |

### Live evidence that resume is not yet safe (2026-09-02, same incident)

The user hand-resumed the Phase 3 worker with `claude --resume`. Claude Code reported agent
`code-editor` **unavailable and continued with DEFAULT tools** — restrictions gone. That is
[MUX-136](./MUX-136-bare-resume-loses-agent-definition.md) reproducing on the manual path.

**This governs the whole spec.** Automating resume without carrying the role's definition would not
inherit MUX-136, it would *industrialise* it: today one hand-resume produced one unrestricted agent;
auto-resume would produce an unrestricted agent **per role, per session, automatically, on every mass
exit** — including privileged roles (`plan` holds sole Atlassian write authority; `commit` holds git
authority). See [Sequencing constraint](#sequencing-constraint).

## Requirements

### Acceptance criteria

- [x] On `agent-down` for a Claude-provider role, the daemon relaunches with `claude --resume <session-id>` when a resumable id is known, preserving the conversation — *Phase 2*
- [x] When no id is known, it falls back to a fresh launch **and the lifecycle log says which path ran and why** — *Phase 2: `resume-scrape-miss` / `resume-scrape-stale` / `resume-disabled`; resume-only `edit` instead logs `agent-resume-unavailable` and stays down*
- [x] Session id capture is **pane-first**: the `Resume this session with: claude --resume <id>` line from `capture-pane`; the chosen source is logged (`source=pane|transcript`) — *Phase 1, `FindResumeID` + `resume-found` row*
- [x] The transcript fallback never resumes another role's conversation — it is used only when the cwd maps to exactly one candidate session, and declines (fresh launch, logged reason) when the mapping is ambiguous (see [Decision 1](#decision-1-the-transcript-fallback-is-constrained-not-newest-wins)) — *Phase 1, `TranscriptIDForCwd` matches on the transcript's `agentSetting` and only inside a worktree the spawn registry says this role owns alone (`spawnOwnsWorktree`); another agent's transcript, an ambiguous pair, and any shared cwd all decline, pinned*
- [x] **Auto-resume carries the role's `--agent`/`--agents` definition**, and the pane is verified afterwards to NOT show the agent-unavailable / default-tools warning. On that warning the agent is stopped and an alert raised — it must **never** be left running unrestricted (MUX-136, reproduced live on the manual path) — *Phase 1 (`TestClaudeBuildExecArgs_ResumeAppendsToFullFlagSet`) + Phase 2 (`refuseUnrestricted`)*
- [x] `edit` becomes **resume-only**: never fresh-launched automatically, but DO resume it — a resume restores the user's conversation, a fresh launch would not. Pinned so the exclusion cannot silently flip to fresh launches — *Phase 2*
- [x] Spawn workers are covered: a dead worker whose run node is still `running` is resumed in its worktree with the same launch env and agent file — *Phase 3 (in its own directory — no worktree by design)*
- [x] When a worker cannot be resumed, the spawn is **marked failed so the graph run fails loudly instead of stalling** (MUX-131 reuse then falls back to a fresh start on retry) — *Phase 3, `failDeadWorker`*
- [x] Mass-exit detection: >= 2 Claude agents down within `MUXCODE_MASS_EXIT_WINDOW_SECS` (default 60), across sessions where the bus dirs are visible, raises **one** `mass-agent-exit` event to edit naming the roles and sessions, with a lifecycle row — instead of N unrelated `agent-down` events. Per-role restart proceeds regardless — *Phase 4*
- [x] Resume restarts skip the 3-strike wait: a pane showing the resume hint is **proof of exit**, not a health-check ambiguity — restart on first sighting (configurable, default on) — *Phase 2*
- [x] `muxcode agent launch <role> --resume [<id>]` exposes the same path manually — *Phase 1*
- [x] `muxcode diagnose` gains a `resumable-session` info finding when a dead agent's pane carries a resume hint — *Phase 4*
- [x] Opt-out: `MUXCODE_AUTO_RESUME_DISABLE=1` restores today's behaviour exactly — *Phase 2, pinned; script C2*
- [x] Docs updated: CLAUDE.md watchdog bullet, [`docs/agent-bus.md`](../../agent-bus.md), [`docs/configuration.md`](../../configuration.md) — *CLAUDE.md "Agent-health restarts are resume-first" bullet; agent-bus.md `agent launch --resume` (Phase 1); configuration.md `MUXCODE_AUTO_RESUME_DISABLE` / `MUXCODE_RESUME_FIRST_SIGHTING` rows (Phase 2)*

#### Operator-initiated restart (`Restart Agents` menu entry)

Auto-resume handles the deaths the daemon notices. The operator also needs a deliberate
"bring everything back" control for the case where they are looking at a wrecked session and want it
restored in one action.

- [x] A `Restart Agents` entry in the MuxCode quick menu (`config/tmux.conf`, the `prefix + b` `display-menu`), placed next to `Provider` — *Phase 5*
- [x] The modal lists providers with **live agent counts** (`claude N` / `opencode M` / `all`), confirm before acting — *Phase 5*
- [x] Live per-agent progress, reusing the multi-agent reload progress view (`tui/provider_select.go`, `bus.ReloadResult`) rather than a second implementation — *Phase 5, `renderBatchProgress` shared*
- [x] Claude agents restart through the **MUX-139 resume path** with the role's `--agent`/`--agents` carried — the same definition guard as auto-resume, not a parallel launch path — *Phase 5, shared `scrapeAndRelaunch`; the guard runs daemon-side via `RestartVerification`*
- [x] `edit` is **included, as resume-only** — this deliberately overrides `ReloadAll`'s standing edit/auto skip (`reload.go:228`, *"interactive orchestrator — require explicit reload"*); the menu action **is** that explicit request. Pinned by test so the override cannot leak into the ordinary `--all` path — *Phase 5*
- [x] Non-Claude agents restart as a same-provider fresh reload — *Phase 5*
- [x] **The restart never changes provider or model.** The provider list is a *filter over current assignment*, never a switch — changing what runs an agent is user-approved only, and a bulk control is the easiest place for that rule to be violated by accident. Pinned by test: no `--cli`/`--model` override is written by this path — *Phase 5, `TestRestartAgents_NeverChangesProviderOrModel`; the CLI refuses `--cli`/`--model` outright*
- [x] CLI parity: `muxcode reload --all --provider <cli> --resume` (`--all`/`--provider` already exist at `cmd/reload.go:29-30`; `--resume` is the addition) — *Phase 5*
- [x] **Dead agents are in scope.** `ReloadAll` currently skips them (`reload.go:231`, `if !IsAgentAlive(...) { continue }`) — correct for a config reload, exactly wrong here, since after a mass exit *every* target is dead. The restart path must select dead agents too, or the control does nothing in the situation that motivates it (see [Decision 2](#decision-2-restart-must-not-inherit-reloadalls-skip-dead-agents-rule)) — *Phase 5, `RestartTargets`*

#### Decision 2: restart must not inherit `ReloadAll`'s skip-dead-agents rule

`ReloadAll` filters to live agents because reloading a dead agent is pointless *when the goal is
picking up new config*. The goal here is the opposite — the agents are dead and that is the reason the
operator opened the menu. Reusing `ReloadAll` unchanged would produce a control that reports
"0 agents restarted" precisely after a mass exit.

- [x] Restart selects by **role and provider**, not by liveness; a live agent is stopped and relaunched, a dead one is launched (resumed where an id is known) — *Phase 5*
- [x] The liveness filter stays untouched on the existing `reload --all` config path — this is an additional selection mode, not a change to reload semantics — *Phase 5, `reloadAllTargets` extracted behaviour-preserving and pinned*

### Sequencing constraint

- [x] **MUX-139 does not ship before [MUX-136](../completed/MUX-136-bare-resume-loses-agent-definition.md) is fixed and pinned.** Auto-resume multiplies MUX-136's blast radius from one hand-resumed agent to every Claude role on the machine; the definition-carrying criterion above is the guard, and MUX-136 is where that guard is built — *MUX-136 is in `completed/`; the guard is `refuseUnrestricted` + the full-flag-set pin*
- [x] Confirm the interaction with [MUX-126](../completed/MUX-126-edit-resume-aware-auto-restart.md): that spec is `edit`'s bare `--resume` losing all launch flags. This spec **adds** automatic `--resume` for edit, so MUX-126's defect becomes reachable automatically — its flag-preserving fix must land with or before Phase 2 — *MUX-126 is completed; its `TestClaudeBuildExecArgs_ResumeAppendsToFullFlagSet` is what Phase 1 step 3 credits, so the automatic road inherits the full flag set*

### Technical approach

**Resume-hint capture reuses knowledge already in the tree.** `ClaudeCodeProvider.IsAlive`
(`provider_claude.go:258-280`) already reasons about this exact line: it orders the shell-prompt check
*before* the startup-text check precisely because the exit message contains the word `claude` and
would otherwise false-positive as "starting up". So **the hint is present exactly when `IsAlive`
returns false** — the two signals are the same event, and the detector for one is the natural place to
read the other.

One caveat: `IsAlive` captures only `-S -5`. The hint can sit above the last five lines, so hint
extraction needs its own deeper capture. **Do not widen `IsAlive`'s window** to share the capture —
its narrowness is load-bearing for the false-positive ordering described in its own comment.

| Area | Change |
|------|--------|
| `bus/agent_health.go` | `ResumeHintFromPane(session, role) (id string, ok bool)` — deeper `capture-pane`, parse `claude --resume <uuid>`, validate the uuid shape. `TranscriptIDForCwd(cwd)` — constrained fallback, see Decision 1 |
| `bus/launch.go` | `LaunchConfig.ResumeID` field |
| `bus/provider_claude.go` | `BuildExecArgs` emits `--resume <id>` alongside the role's normal `--agent`/`--agents` flags. **Note:** `BuildExecArgs` is a `Provider` interface method (`provider.go:40`) implemented per provider — not a `bus/launch.go` function, as CLAUDE.md's code-reference table currently implies |
| `daemon/daemon.go` (`checkAgentHealth`) | Resume-first branch for Claude roles; lifecycle `agent-resume` with `id=<uuid> source=pane\|transcript`; else the existing 3-strike path. Edit: resume-only branch. Spawn workers: iterate running spawns whose window has fallen to a shell (`RefreshSpawnStatus` already detects pane state) and resume in the worktree from the spawn launch config |
| `bus/mass_exit.go` (new) | Sliding-window counter over `agent-down` sightings keyed by `(session, role)`; one event per window; cross-session count via `DiscoverSessions()` (`remote.go:25`) |
| `bus/diagnose.go` | `resumable-session` info finding |

#### Decision 1: the transcript fallback is constrained, not newest-wins

The brief proposed "else the newest transcript under `~/.claude/projects/<encoded cwd>/`". **Verified
against the live machine, that is unsafe as stated.** The encoding is confirmed —
`/Users/mkoberlein/Repos/mkober/muxcode` maps to `-Users-mkoberlein-Repos-mkober-muxcode` — but that
directory currently holds **three `.jsonl` transcripts sharing the same 13:44 mtime**, because `plan`,
`edit` and `commit` all run with the repo root as cwd. Newest-by-mtime would hand one role **another
role's conversation**, which is worse than a fresh launch: a fresh agent starts empty, whereas a
mis-resumed one starts with a privileged peer's context and its own tools.

Therefore:

- [x] The transcript fallback applies only where cwd identifies the agent uniquely — in practice **spawn workers**, each of which owns a private worktree — *Phase 1: `spawnOwnsWorktree` — the spawn registry must show exactly one entry claiming the cwd, and it must be this role*
- [x] For shared-cwd roles it declines and fresh-launches with a logged reason, rather than guessing — *Phase 1: `ErrTranscriptSharedCwd`, `resume-fresh` row; graph workers run in the session checkout (no worktree), so they decline too*
- [x] Encoding is pinned by test including the macOS `/var` to `/private/var` resolution: observed spawn dirs encode as `-private-var-folders-…-T-muxcode-spawn-<session>-spawn-<id>`, so resolving the symlink before encoding is required, not cosmetic — *Phase 1: `TestClaudeProjectDirName`*

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/agent_health.go` | resume-hint + constrained transcript id capture |
| `tools/muxcode/bus/launch.go` | `LaunchConfig.ResumeID` |
| `tools/muxcode/bus/provider_claude.go` | `--resume` arg construction alongside `--agent`/`--agents` |
| `tools/muxcode/daemon/daemon.go` | resume-first restart, edit resume-only, spawn coverage |
| `tools/muxcode/bus/mass_exit.go` (new) | mass-exit correlation + single event |
| `tools/muxcode/bus/spawn.go` | worker resume / fail-loud |
| `tools/muxcode/bus/diagnose.go` | `resumable-session` finding |
| `tools/muxcode/cmd/agent.go` | `agent launch <role> --resume [<id>]` |
| `config/tmux.conf` | `Restart Agents` entry in the `prefix + b` menu |
| `tools/muxcode/bus/reload.go`, `reload_batch.go` | restart selection incl. dead agents; edit resume-only override |
| `tools/muxcode/tui/provider_select.go` | reused per-agent progress view |
| `tools/muxcode/cmd/reload.go` | `--resume` alongside existing `--all`/`--provider` |
| `scripts/test-agent-resume.sh` (new) | integration test |

## Implementation

### Phase 1: Resume id capture and `--resume` launch

- [x] `ResumeHintFromPane` + tests: hint present, hint absent, malformed uuid, hint above the last 5 lines (the deeper-capture case) — *verified 2026-10-05: shipped as `CaptureResumeSessionID` / `ScrapeResumeSessionID` (`bus/agent_health.go`); `TestScrapeResumeSessionID` covers present ("incident pane"), absent ("no banner"), malformed ("truncated id", "overlong id", "malformed latest never falls back"); `TestCaptureResumeSessionID_HintAboveLastFiveLines` (`resume_id_test.go`) covers the deeper capture*
- [x] `TranscriptIDForCwd` + tests: unique candidate resolves, **ambiguous candidate declines**, encoded-path mapping pinned including `/private/var` resolution — *`TranscriptIDForCwd`, `claudeProjectDirName`, `transcriptAgent` (`bus/agent_health.go`); `TestTranscriptIDForCwd` (7 cases: one resolves, two decline as ambiguous, another agent's lone transcript declines, no-agent transcript never qualifies, non-uuid name ignored, no project dir, no agent name) and `TestClaudeProjectDirName` with the `/private/var` resolution. Tightened in review: the transcript road applies only when `spawnOwnsWorktree` proves the cwd is the role's own private spawn worktree — shared cwds (repo-root roles, worktree-less graph workers) decline `ErrTranscriptSharedCwd`; `TestTranscriptIDForCwd_OwnershipGate` (6 rows: own worktree resolves; unowned shared cwd, persistent role, another worker's worktree, a worktree two spawns claim, and no role all decline)*
- [x] `LaunchConfig.ResumeID`; `--resume` emitted by `ClaudeCodeProvider.BuildExecArgs`; arg shape pinned by test — *shipped as `LaunchConfig.ResumeSessionID` → `provider_claude.go:111` (MUX-126 Phase 2, pre-dating this phase); pinned by `TestClaudeBuildExecArgs_ResumeAppendsToFullFlagSet` (`provider_test.go:378`): resumed argv == fresh argv + `--resume <id>`, with `--agent`/`--agents`/`--allowedTools`/`--append-system-prompt`/`--dangerously-skip-permissions` all retained. (Plan's 2026-10-05 verify first marked this unpinned — a search miss, corrected the same day)*
- [x] `muxcode agent launch <role> --resume [<id>]` — *`cmd/launch.go` usage; `ParseLaunchArgs` returns `ResumeAuto` for a bare `--resume` (pinned in `launch_reason_test.go`); `findLaunchResumeID` → `FindResumeID` pane-first then transcript, logging `resume-found` (`source=pane|transcript`) / `resume-fresh` (reason) / `resume-ignored` (provider); `TestFindResumeID` pins pane-first, fallback, and both-decline naming both reasons; documented in `docs/agent-bus.md`*
- [x] Verify live that `claude --resume <id>` alongside the role's `--agent`/`--agents` flags restores the conversation **with the role's tools**, not default tools — *deferred to Phase 6 — `scripts/test-agent-resume.sh` (user's decision, 2026-10-05); needs a human-observed real agent, which Phase 1 had no road to*

### Phase 2: Daemon resume-first restart

- [x] Resume-first branch in `checkAgentHealth` for Claude roles; lifecycle `agent-resume` with source; fallback to the existing path with a logged reason — *verified 2026-10-05: `bus.RestartLocalAgent` (`health.go`) picks resume / resume-only / fresh; a hit logs `agent-resume id=<uuid> source=pane`, a miss or stale banner `resume-scrape-miss`/`-stale` then fresh, the opt-out `resume-disabled`; daemon `markAgentDown`/`restartDeadAgent` (`daemon.go`)*
- [x] Definition-carried verification: after resume, confirm the pane shows no agent-unavailable/default-tools warning; on warning, stop the agent and alert — never leave it running unrestricted — *`refuseUnrestricted` (`daemon.go:2013`): stop marker + `agent-resume-unrestricted` row and alert to edit, even when the argv probe reads present; `TestCheckAgentHealth_RestartedWithoutDefinitionIsStopped`, with negative controls `…OldDefinitionWarningIsIgnored` (a pre-exit warning does not fire) and the `FailedCapture`/`FailedStop`/`FailedMarker` fail-closed cases*
- [x] Edit becomes resume-only; pinned by a test that a **missing id leaves edit down and alerts** rather than fresh-launching it — *`ResumeOnlyRole` (Claude `edit`, unless opted out); `ErrResumeUnavailable`, nothing typed, `agent-resume-unavailable` row + deduped `agent-down` alert, no attempt spent; `TestCheckAgentHealth_EditWithoutSessionIsLeftDown`, `TestRestartLocalAgent_EditIsResumeOnly`, `TestResumeOnlyRole`; script section C*
- [x] First-sighting restart when the resume hint is present (skip the 3-strike wait), configurable — *`ResumeFirstSighting` (default on, `MUXCODE_RESUME_FIRST_SIGHTING=0` off), `PaneResumeID` non-stale banner only; snapshot + `agent-down` still recorded first, restart cap kept; `TestCheckAgentHealth_FirstSightingResumes` + `…NoFirstSightingWithoutBannerOrWhenOff` (3 negative controls), `TestResumeFirstSighting`, `TestPaneResumeID`*
- [x] `MUXCODE_AUTO_RESUME_DISABLE` opt-out + test — *`AutoResumeDisabled`: no scrape, no first sighting, edit not resume-only, manual `muxcode resume` unaffected; `TestRestartLocalAgent_AutoResumeDisabled`, `TestCheckAgentHealth_EditResumesOrOptsOut`; script section C2. `scripts/test-edit-auto-resume.sh` via the run agent (task `1791219175-spawn-2c03db0e-39ac9d32`, hook row ts 1791219753): exit 0, **84 passed / 0 failed**, floor 77 → 84*

### Phase 3: Spawn worker coverage

- [x] Dead-worker detection for spawns whose graph node is still `running` — *verified 2026-10-05: `deadSpawnWorkers`/`resumeDeadWorkers` (`bus/spawn_resume.go`), in the spawn/map tick after `replaceLostWorkers` (`graph_exec.go:1404`): registry `running`, window live, seed unanswered, pane dead with an exit banner, confirmed after 30 s; `TestExecSpawnDeadWorkerResumed`, negative control `…LeavesLiveAndAnsweredAlone`*
- [x] Resume in the worktree with the same launch env and agent file — *graph workers take no worktree by design (MUX-142), so "in the worktree" reads as "in its own directory": `launchResumedWorker` types the worker's own `spawnLaunchCommand(worktree, spawnRole, launcher, role)` line plus `--resume <id>` into its pane — same dir, same `AGENT_ROLE`, same base role and therefore the same agent file — then `verifyResumedWorkers` holds the node until the session is positively ready and 5 s clean of the definition-unavailable banner before the `[resumed]` reseed (`…ResumedWithoutDefinitionIsStopped`, `…ResumedDelayedWarningIsStopped`, `…ResumedCaptureFailureStaysPending`; readiness itself pinned by `TestResumedSessionReady` — composer in the live tail below the last banner, launch line excluded, survives redraw and wrap)*
- [x] Fail-loud when resume is impossible: the graph node **fails** rather than stalls + test — *`failDeadWorker`: no banner after 30 s, non-Claude provider, or cap → node fails "could not be resumed" with the worker stopped first (`graph-spawn-dead`); a failed stop persists `stop_pending` and is retried each tick, the node failing only on confirmed stop (`graph-spawn-stopped`); `MUXCODE_AUTO_RESUME_DISABLE=1` skips the road entirely. `TestExecSpawnDeadWorkerUnresumableFailsLoudly`, `…ResumeCapped`, `…FailedStopIsRetriedBeforeFailingNode`, `…PendingStopOutranksTimeoutAndReplacement` (runs first in `harvestRunningNode`), `…OptOutLeavesExecutorAlone`*

### Phase 4: Mass-exit correlation

- [x] Sliding-window detector; single `mass-agent-exit` event naming roles, sessions and window; lifecycle row — *verified 2026-10-05: `bus/mass_exit.go` — `RecordAgentExit` (`agent-exits.jsonl`, 1 h retention, atomic rewrite), `DetectMassExit` (≥ 2 distinct `(session, role)` within `MUXCODE_MASS_EXIT_WINDOW_SECS`, default 60), `MassExit.Detail`/`FormatMassExitAlert`; `checkAgentHealth` raises **one** `mass-agent-exit` event to edit plus a lifecycle row per window (`daemon.go:2056-2063`), per-role `agent-down`/restart unchanged; `TestCheckAgentHealth_MassExitRaisesOneEvent` (3 deaths → exactly 1 event over 3 sweeps, restarts still run), `…MassExitWindowBoundary` (10 s yes / 300 s no), `…NonClaudeDeathsNotCorrelated`, `…PersistentOutageNotRefreshed`, `…RecoveredRoleRecordedAgain`*
- [x] Cross-session counting via `DiscoverSessions()` — *`RecentAgentExits` → `DiscoverSessions` → `AgentExitsIn` (`mass_exit.go:96-112`); a session re-evaluates for one window after its own last sighting so deaths that preceded a peer's still join the burst; `TestRecentAgentExits_CrossSession`, `TestCheckAgentHealth_MassExitSeesLaterPeerSession`; daemon `TestMain` confines the scan to the test's own session*
- [x] **Negative control:** two unrelated single deaths 5 minutes apart raise no mass event — *`TestDetectMassExit_SpacedDeathsAreNotCorrelated` (300 s apart), `TestCheckAgentHealth_SingleDeathRaisesNoMassExit`, `TestDetectMassExit_DistinctAgents` (same agent twice is not two)*
- [x] `diagnose` `resumable-session` finding + test — *`AgentStateEvidence.ResumeSessionID` (dead Claude pane via `PaneResumeID`, `diagnose.go:147`) and `checkResumableSession` (`:1168`, severity `info`, remediation `muxcode resume` / `agent launch --resume <id>`); `TestCheckResumableSession` (5 rows incl. alive / reloading / no-banner negatives); `mass-agent-exit` added to `isSystemAction` and diagnose's `roleRelevantEvents`*

### Phase 5: Operator restart control

- [x] `Restart Agents` entry in the `prefix + b` `display-menu` (`config/tmux.conf`), next to `Provider` — *verified 2026-10-05: `config/tmux.conf:83`, key `A`, `muxcode modal open restart`, directly under `Provider` (`R`); `bus/modal.go` `restart` modal → `muxcode restart-select`*
- [x] Modal: provider list with live agent counts (`claude N` / `opencode M` / `all`) + confirm step — *`tui/restart_select.go`, `RestartProviderCounts` (live + down counts, `all` row only with > 1 provider); confirm names only the filter's roads, targets re-read at `y`; `TestRestartSelect_ListsProviderCounts`, `…ConfirmStatesConsequences`, `…ConfirmBacksOut`, `…SingleProviderHasNoAllRow`, `…EmptyAndErrorStates`*
- [x] Restart selection by role and provider **including dead agents** (Decision 2), with the existing `reload --all` liveness filter left untouched + test covering both selection modes — *`RestartTargets` (`bus/restart_agents.go`; windowed roles only, unreadable window list is an error); `ReloadAll`'s selection extracted behaviour-preserving to `reloadAllTargets` (`reload.go:281`) so both modes are pinned side by side in `TestRestartTargets_SelectsDeadAgentsAndEdit` (restart: dead + edit; reload: live-only, no edit/auto) and `…WindowsRequired`*
- [x] Claude targets routed through the Phase 1–2 resume path, definition carried and verified — *`RestartAgent` → shared `scrapeAndRelaunch` (live agent exited first); the definition check is handed to the daemon: a `RestartVerification` record (`bus/restart_verify.go`) is written before anything is typed, `checkRestartVerifications` → `AdvanceRestartVerification` stops on the banner and retries the stop until confirmed, the CLI only waits for the verdict, so a closed modal never leaves an agent unsupervised; `TestRestartAgent_ClaudeRoutesThroughResume`, `…HandsVerificationToDaemon`, `…RefusedWithoutHandoff`, `TestAdvanceRestartVerification`*
- [x] `edit` included as resume-only; test pins that the override does not leak into ordinary `reload --all` — *`relaunchResumeOnly` → `ErrResumeUnavailable`, never fresh; `TestRestartAgent_EditWithoutSessionStaysDown`; the no-leak half is the `reloadAllTargets` side of `TestRestartTargets_SelectsDeadAgentsAndEdit`*
- [x] Non-Claude targets: same-provider fresh reload — *`ReloadAgent(role, "", "")`; `TestRestartAgents_FilterLeavesOtherProvidersAlone` (an `opencode` filter leaves Claude agents untouched)*
- [x] Test: **no `--cli`/`--model` override is written by this path** — provider is a filter, never a switch — *`TestRestartAgents_NeverChangesProviderOrModel`: no override file, provider and model unchanged*
- [x] Live per-agent progress reusing the multi-agent reload progress view — *`renderBatchProgress` extracted from `provider_select`'s `renderProgress` and shared; `ReloadResult` gains `Restarted`/`ResumedID`; `TestRenderBatchProgress_RestartAndReloadRows`, `TestRestartSelect_CloseDuringProgressWaitsForBatch`, `…CloseImmediateWhenIdleOrDone`*
- [x] `muxcode reload --all --provider <cli> --resume` CLI parity + test — *`cmd/reload.go` `validateRestartFlags` (needs `--all`; refuses `--cli`/`--model`/`--compact`); `TestValidateRestartFlags`; documented in `docs/agent-bus.md`*

### Phase 6: Integration test

- [ ] Create `scripts/test-agent-resume.sh` — hermetic: scratch session, fake `claude` shim that prints the resume line and exits on SIGTERM, recording the args it was relaunched with
- [ ] Kill 3 agents within one second, assert **exactly one** `mass-agent-exit` event
- [ ] Each agent relaunched with `--resume <its own id>` (args captured by the shim) — assert the id-to-role pairing, not merely that `--resume` appeared
- [ ] `edit` resumed, never fresh-launched
- [ ] A spawn worker with a running node resumed in its worktree
- [ ] Assert the relaunch carried the role's `--agent`/`--agents` flags (MUX-136 guard)
- [ ] **Live tools-restored check** (deferred here from Phase 1 step 5): resume a **real** Claude agent — not the shim — with `muxcode agent launch <role> --resume <id>` and verify the pane shows the conversation restored **and** the role's tools, with no agent-unavailable / default-tools warning. Human-observed, or a live section gated like `test-codex-hooks` (`MUXCODE_AGENT_RESUME_LIVE=1`); record the observation here
- [ ] **Negative controls:** opt-out env leaves today's behaviour; a single death raises no mass event; a pane with no hint falls back to fresh launch with the logged reason; an ambiguous transcript cwd declines rather than resuming the wrong session
- [ ] `Restart Agents` end-to-end: kill all agents, invoke the restart action filtered to `claude`, assert **every dead Claude agent came back** (the skip-dead-agents regression, Decision 2) with its `--agent`/`--agents` flags, `edit` among them via resume
- [ ] **Negative controls for restart:** an `opencode` filter leaves Claude agents untouched; the run writes **no `--cli`/`--model` override file** for any role (provider never switched); ordinary `muxcode reload --all` still skips dead agents and still skips `edit`
- [ ] Coverage floor, set to the maximum achievable count so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-136](./MUX-136-bare-resume-loses-agent-definition.md) | **Blocking.** Resume must carry the agent file; reproduced live on the manual path during this incident |
| [MUX-126](../completed/MUX-126-edit-resume-aware-auto-restart.md) | Edit's bare `--resume` loses all launch flags — this spec makes that path automatic, so the fix must land with or before Phase 2 |
| [MUX-008](./MUX-008-unverified-daemon-auto-restart.md) | Restart reported without confirming the agent came back; the definition-verification criterion here is the same shape |
| [MUX-131](../completed/MUX-131-spawn-implement-output-never-ported.md) | Worker reuse — dead-worker fallback semantics |
| [MUX-123](./MUX-123-stall-watchdog-selective-misses.md) | A dead worker with a `running` node is exactly the stall this spec prevents at source |

Together with MUX-136, MUX-126 and MUX-008 this forms the *restart and resume restore an agent
incompletely* family named in the [defect clustering](./backlog.md#defects--prioritized). Those three
describe what restore gets **wrong**; this one describes what restore does not **attempt**.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-139-claude-agent-auto-resume | 2h 10m | 2026-10-05 20:45 |

## Status

In Progress — Phases 1-5 complete (2026-10-05; Phase 1 step 5 deferred to Phase 6 by the user); Phase 6 (integration test) next
