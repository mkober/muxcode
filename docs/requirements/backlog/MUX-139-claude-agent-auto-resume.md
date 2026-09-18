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

- [ ] On `agent-down` for a Claude-provider role, the daemon relaunches with `claude --resume <session-id>` when a resumable id is known, preserving the conversation
- [ ] When no id is known, it falls back to a fresh launch **and the lifecycle log says which path ran and why**
- [ ] Session id capture is **pane-first**: the `Resume this session with: claude --resume <id>` line from `capture-pane`; the chosen source is logged (`source=pane|transcript`)
- [ ] The transcript fallback never resumes another role's conversation — it is used only when the cwd maps to exactly one candidate session, and declines (fresh launch, logged reason) when the mapping is ambiguous (see [Decision 1](#decision-1-the-transcript-fallback-is-constrained-not-newest-wins))
- [ ] **Auto-resume carries the role's `--agent`/`--agents` definition**, and the pane is verified afterwards to NOT show the agent-unavailable / default-tools warning. On that warning the agent is stopped and an alert raised — it must **never** be left running unrestricted (MUX-136, reproduced live on the manual path)
- [ ] `edit` becomes **resume-only**: never fresh-launched automatically, but DO resume it — a resume restores the user's conversation, a fresh launch would not. Pinned so the exclusion cannot silently flip to fresh launches
- [ ] Spawn workers are covered: a dead worker whose run node is still `running` is resumed in its worktree with the same launch env and agent file
- [ ] When a worker cannot be resumed, the spawn is **marked failed so the graph run fails loudly instead of stalling** (MUX-131 reuse then falls back to a fresh start on retry)
- [ ] Mass-exit detection: >= 2 Claude agents down within `MUXCODE_MASS_EXIT_WINDOW_SECS` (default 60), across sessions where the bus dirs are visible, raises **one** `mass-agent-exit` event to edit naming the roles and sessions, with a lifecycle row — instead of N unrelated `agent-down` events. Per-role restart proceeds regardless
- [ ] Resume restarts skip the 3-strike wait: a pane showing the resume hint is **proof of exit**, not a health-check ambiguity — restart on first sighting (configurable, default on)
- [ ] `muxcode agent launch <role> --resume [<id>]` exposes the same path manually
- [ ] `muxcode diagnose` gains a `resumable-session` info finding when a dead agent's pane carries a resume hint
- [ ] Opt-out: `MUXCODE_AUTO_RESUME_DISABLE=1` restores today's behaviour exactly
- [ ] Docs updated: CLAUDE.md watchdog bullet, [`docs/agent-bus.md`](../../agent-bus.md), [`docs/configuration.md`](../../configuration.md)

#### Operator-initiated restart (`Restart Agents` menu entry)

Auto-resume handles the deaths the daemon notices. The operator also needs a deliberate
"bring everything back" control for the case where they are looking at a wrecked session and want it
restored in one action.

- [ ] A `Restart Agents` entry in the MuxCode quick menu (`config/tmux.conf`, the `prefix + b` `display-menu`), placed next to `Provider`
- [ ] The modal lists providers with **live agent counts** (`claude N` / `opencode M` / `all`), confirm before acting
- [ ] Live per-agent progress, reusing the multi-agent reload progress view (`tui/provider_select.go`, `bus.ReloadResult`) rather than a second implementation
- [ ] Claude agents restart through the **MUX-139 resume path** with the role's `--agent`/`--agents` carried — the same definition guard as auto-resume, not a parallel launch path
- [ ] `edit` is **included, as resume-only** — this deliberately overrides `ReloadAll`'s standing edit/auto skip (`reload.go:228`, *"interactive orchestrator — require explicit reload"*); the menu action **is** that explicit request. Pinned by test so the override cannot leak into the ordinary `--all` path
- [ ] Non-Claude agents restart as a same-provider fresh reload
- [ ] **The restart never changes provider or model.** The provider list is a *filter over current assignment*, never a switch — changing what runs an agent is user-approved only, and a bulk control is the easiest place for that rule to be violated by accident. Pinned by test: no `--cli`/`--model` override is written by this path
- [ ] CLI parity: `muxcode reload --all --provider <cli> --resume` (`--all`/`--provider` already exist at `cmd/reload.go:29-30`; `--resume` is the addition)
- [ ] **Dead agents are in scope.** `ReloadAll` currently skips them (`reload.go:231`, `if !IsAgentAlive(...) { continue }`) — correct for a config reload, exactly wrong here, since after a mass exit *every* target is dead. The restart path must select dead agents too, or the control does nothing in the situation that motivates it (see [Decision 2](#decision-2-restart-must-not-inherit-reloadalls-skip-dead-agents-rule))

#### Decision 2: restart must not inherit `ReloadAll`'s skip-dead-agents rule

`ReloadAll` filters to live agents because reloading a dead agent is pointless *when the goal is
picking up new config*. The goal here is the opposite — the agents are dead and that is the reason the
operator opened the menu. Reusing `ReloadAll` unchanged would produce a control that reports
"0 agents restarted" precisely after a mass exit.

- [ ] Restart selects by **role and provider**, not by liveness; a live agent is stopped and relaunched, a dead one is launched (resumed where an id is known)
- [ ] The liveness filter stays untouched on the existing `reload --all` config path — this is an additional selection mode, not a change to reload semantics

### Sequencing constraint

- [ ] **MUX-139 does not ship before [MUX-136](./MUX-136-bare-resume-loses-agent-definition.md) is fixed and pinned.** Auto-resume multiplies MUX-136's blast radius from one hand-resumed agent to every Claude role on the machine; the definition-carrying criterion above is the guard, and MUX-136 is where that guard is built
- [ ] Confirm the interaction with [MUX-126](./MUX-126-edit-resume-aware-auto-restart.md): that spec is `edit`'s bare `--resume` losing all launch flags. This spec **adds** automatic `--resume` for edit, so MUX-126's defect becomes reachable automatically — its flag-preserving fix must land with or before Phase 2

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

- [ ] The transcript fallback applies only where cwd identifies the agent uniquely — in practice **spawn workers**, each of which owns a private worktree
- [ ] For shared-cwd roles it declines and fresh-launches with a logged reason, rather than guessing
- [ ] Encoding is pinned by test including the macOS `/var` to `/private/var` resolution: observed spawn dirs encode as `-private-var-folders-…-T-muxcode-spawn-<session>-spawn-<id>`, so resolving the symlink before encoding is required, not cosmetic

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

- [ ] `ResumeHintFromPane` + tests: hint present, hint absent, malformed uuid, hint above the last 5 lines (the deeper-capture case)
- [ ] `TranscriptIDForCwd` + tests: unique candidate resolves, **ambiguous candidate declines**, encoded-path mapping pinned including `/private/var` resolution
- [ ] `LaunchConfig.ResumeID`; `--resume` emitted by `ClaudeCodeProvider.BuildExecArgs`; arg shape pinned by test
- [ ] `muxcode agent launch <role> --resume [<id>]`
- [ ] Verify live that `claude --resume <id>` alongside the role's `--agent`/`--agents` flags restores the conversation **with the role's tools**, not default tools

### Phase 2: Daemon resume-first restart

- [ ] Resume-first branch in `checkAgentHealth` for Claude roles; lifecycle `agent-resume` with source; fallback to the existing path with a logged reason
- [ ] Definition-carried verification: after resume, confirm the pane shows no agent-unavailable/default-tools warning; on warning, stop the agent and alert — never leave it running unrestricted
- [ ] Edit becomes resume-only; pinned by a test that a **missing id leaves edit down and alerts** rather than fresh-launching it
- [ ] First-sighting restart when the resume hint is present (skip the 3-strike wait), configurable
- [ ] `MUXCODE_AUTO_RESUME_DISABLE` opt-out + test

### Phase 3: Spawn worker coverage

- [ ] Dead-worker detection for spawns whose graph node is still `running`
- [ ] Resume in the worktree with the same launch env and agent file
- [ ] Fail-loud when resume is impossible: the graph node **fails** rather than stalls + test

### Phase 4: Mass-exit correlation

- [ ] Sliding-window detector; single `mass-agent-exit` event naming roles, sessions and window; lifecycle row
- [ ] Cross-session counting via `DiscoverSessions()`
- [ ] **Negative control:** two unrelated single deaths 5 minutes apart raise no mass event
- [ ] `diagnose` `resumable-session` finding + test

### Phase 5: Operator restart control

- [ ] `Restart Agents` entry in the `prefix + b` `display-menu` (`config/tmux.conf`), next to `Provider`
- [ ] Modal: provider list with live agent counts (`claude N` / `opencode M` / `all`) + confirm step
- [ ] Restart selection by role and provider **including dead agents** (Decision 2), with the existing `reload --all` liveness filter left untouched + test covering both selection modes
- [ ] Claude targets routed through the Phase 1–2 resume path, definition carried and verified
- [ ] `edit` included as resume-only; test pins that the override does not leak into ordinary `reload --all`
- [ ] Non-Claude targets: same-provider fresh reload
- [ ] Test: **no `--cli`/`--model` override is written by this path** — provider is a filter, never a switch
- [ ] Live per-agent progress reusing the multi-agent reload progress view
- [ ] `muxcode reload --all --provider <cli> --resume` CLI parity + test

### Phase 6: Integration test

- [ ] Create `scripts/test-agent-resume.sh` — hermetic: scratch session, fake `claude` shim that prints the resume line and exits on SIGTERM, recording the args it was relaunched with
- [ ] Kill 3 agents within one second, assert **exactly one** `mass-agent-exit` event
- [ ] Each agent relaunched with `--resume <its own id>` (args captured by the shim) — assert the id-to-role pairing, not merely that `--resume` appeared
- [ ] `edit` resumed, never fresh-launched
- [ ] A spawn worker with a running node resumed in its worktree
- [ ] Assert the relaunch carried the role's `--agent`/`--agents` flags (MUX-136 guard)
- [ ] **Negative controls:** opt-out env leaves today's behaviour; a single death raises no mass event; a pane with no hint falls back to fresh launch with the logged reason; an ambiguous transcript cwd declines rather than resuming the wrong session
- [ ] `Restart Agents` end-to-end: kill all agents, invoke the restart action filtered to `claude`, assert **every dead Claude agent came back** (the skip-dead-agents regression, Decision 2) with its `--agent`/`--agents` flags, `edit` among them via resume
- [ ] **Negative controls for restart:** an `opencode` filter leaves Claude agents untouched; the run writes **no `--cli`/`--model` override file** for any role (provider never switched); ordinary `muxcode reload --all` still skips dead agents and still skips `edit`
- [ ] Coverage floor, set to the maximum achievable count so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-136](./MUX-136-bare-resume-loses-agent-definition.md) | **Blocking.** Resume must carry the agent file; reproduced live on the manual path during this incident |
| [MUX-126](./MUX-126-edit-resume-aware-auto-restart.md) | Edit's bare `--resume` loses all launch flags — this spec makes that path automatic, so the fix must land with or before Phase 2 |
| [MUX-008](./MUX-008-unverified-daemon-auto-restart.md) | Restart reported without confirming the agent came back; the definition-verification criterion here is the same shape |
| [MUX-131](../completed/MUX-131-spawn-implement-output-never-ported.md) | Worker reuse — dead-worker fallback semantics |
| [MUX-123](./MUX-123-stall-watchdog-selective-misses.md) | A dead worker with a `running` node is exactly the stall this spec prevents at source |

Together with MUX-136, MUX-126 and MUX-008 this forms the *restart and resume restore an agent
incompletely* family named in the [defect clustering](./backlog.md#defects--prioritized). Those three
describe what restore gets **wrong**; this one describes what restore does not **attempt**.

## Status

Backlog
