# MUX-146: Remove the Research and Auto Agents

Remove the `research` and `auto` agent roles from muxcode: their definitions, role
registrations, mode-cycle wiring, tool profiles, provider defaults, console renderers,
skills, docs, tests and generated artifacts.

Both are **mode roles** — neither has a static tmux window. `research` is mode index 1 on the
`plan` window (F1), `auto` is mode index 1 on the `edit` window (F2). Each is created lazily on
first cycle, so a session that never cycles never launches them.

## Context

### Why remove

| Reason | Evidence |
|--------|----------|
| Neither runs in a normal session | Verified live 2026-09-03: session windows are `plan edit build test serve review deploy run watch commit`; no process for either role |
| `auto` is the source of unrequested work | [`MUX-141`](./MUX-141-auto-agent-restart-relaunches-graph-runs.md) — every launch seeds `auto` a request-type `startup` task (`launch.go:852-869`), which the definition reads as a mandate; two unrequested graph runs observed 2026-09-02 |
| `auto` is an attribution blind spot | [`MUX-144`](./MUX-144-wait-human-gate-openable-by-any-agent.md) could not attribute an unauthorised gate release partly because `auto` "has no tmux window to scrape and nothing is logged" |
| Windowless roles leak undeliverable messages | `inbox/auto.jsonl` held a `long-active` watchdog event for a role with no process — the daemon believed `auto` had been "active 10m". Same family as [`MUX-145`](./MUX-145-messages-routed-to-windowless-role.md) |
| Carrying cost is spread across three modules | Role→file mapping is duplicated in `bus/launch.go`, `bus/agent.go` and the standalone harness; every role-list test pins both names |

### Current state — verified

Established directly rather than inferred, 2026-09-03:

- [x] Neither role appears in `DefaultLauncherConfig().Windows` (`bus/launcher.go:35`)
- [x] Neither had a live process; `research` inbox empty, `auto` inbox holding 1 undeliverable event
- [x] Both are reachable by keypress — `F1`/`prefix+r` for research, `F2`/`prefix+a` for auto
- [x] Neither participates in any `EventChain`, and neither is in `AutoCC` (`bus/profile.go:923-1056`, `:1057`)
- [x] Installed copies exist at `~/.config/muxcode/agents/{code-researcher,autonomous-agent}.md`, plus stale `agentdef-{auto,research}.hash` in the bus dir

**Removal is not a no-op.** Both roles are live features reachable by a documented keypress; this
spec removes working functionality, it does not merely delete dead code.

### The mode-cycle mechanism survives

`research` and `auto` are the only two mode roles (`modeRoles`, `bus/config.go:679-682`). Removing
both leaves the mode-cycling machinery in `bus/mode.go` with **zero consumers** — but
[`MUX-015`](./MUX-015-refactor-agent.md) proposes a new one (an F6 review↔refactor toggle) and
uses `agents/code-researcher.md` as its reference pattern. This spec therefore **keeps
`bus/mode.go`** and removes only the two role registrations. See [Decision 2](#decision-2).

## Requirements

### Acceptance criteria

- [ ] `muxcode` builds and `./test.sh` passes with no reference to either role in non-test code
- [ ] `KnownRoles` contains neither `research` nor `auto`; `IsKnownRole` returns false for both
- [ ] `NormalizeBusRole("agent")` no longer resolves to `auto` — the legacy alias is removed with the role
- [ ] Pressing `F1` on the plan window selects it and does not cycle; the same for `F2` on edit
- [ ] `ModeCycle` is never invoked with fewer than 2 agents (no `"need at least 2 modes to cycle"` error reachable from a default session)
- [ ] `muxcode send research <action> "..."` and `muxcode send auto ...` fail with an unknown-role error rather than writing an inbox file
- [ ] No inbox, history, lock or `agentdef-*.hash` file is created for either role in a fresh session
- [ ] `muxcode skill list --role <any>` no longer offers `story-lifecycle`
- [ ] `muxcode reload --all`, the provider-select modal, and `muxcode diagnose --all` operate correctly with both roles gone (no empty-branch or missing-key panics)
- [ ] `agents/`, `agents/harness/`, `skills/`, `config/muxcode.json`, `config/tmux.conf` and `config/nvim/plugin/startscreen.lua` carry no reference to either role
- [ ] Docs carry no reference to either role as a live feature; `docs/requirements/completed/MUX-082-research-mode.md` and `MUX-102-agent-mode.md` are left intact as historical record
- [ ] Backlog specs whose premise depends on these roles are reconciled per [Decision 3](#decision-3)
- [ ] `bash scripts/test-remove-agents.sh` passes

### Technical approach

Remove in dependency order — role registration last, so intermediate commits keep the tree
buildable:

1. **Leaf consumers first** — console renderer, skills, tool profiles, provider defaults, tmux
   bindings, startscreen role list.
2. **Then role-specific logic** — the `research` skip branches in `cmd/send.go` and
   `daemon/daemon.go`, the `auto` startup-seed in `PreLaunchSetup`, `ResolveTaskFile`, the daemon
   heartbeat, and the five hard-exclusion sites that name `auto`.
3. **Then mode-cycle registration** — `DefaultPlanModeCycleState`, the `modeRoles` entries, and
   the `launcher.go` seeding, together with the `config/tmux.conf` F1/F2 bindings so the
   keybinding and the state never disagree.
4. **Role registry last** — `KnownRoles`, the three role→file mappings, env-var name functions,
   `NormalizeBusRole`/`resolveRoleAlias` aliases.
5. **Generated artifacts** — delete `.opencode/agents/research.md`; `.codex/AGENTS.md` and
   `.codex/review/AGENTS.md` regenerate on next launch from `SharedPrompt()`.

**Three duplicate role→file mappings must move together** (`bus/launch.go:83,93`,
`bus/agent.go:318,324`, `harness/prompt.go:269,275`). They are already flagged as mirrors in
comments; removing one and not the others yields a role that resolves under one launch path and
not another.

### Key files

| File | Change |
|------|--------|
| `agents/code-researcher.md` | Delete |
| `agents/autonomous-agent.md`, `agents/harness/autonomous-agent.md` | Delete both |
| `skills/story-lifecycle.md` | Delete — exists only to serve `auto` |
| `.opencode/agents/research.md` | Delete (generated artifact) |
| `tools/muxcode/bus/config.go` | `KnownRoles:13-18`; `modeRoles:679-682`; `NormalizeBusRole:728` (`agent`→`auto` alias) |
| `tools/muxcode/bus/mode.go` | `DefaultPlanModeCycleState:47-58`; `ReadModeCycleState:60-92` plan fallback; `modeAutoAcceptAndWake:445-536` comment |
| `tools/muxcode/bus/launcher.go` | `Windows:35` (no change needed); mode-state seeding `:216-228`; `HasConsoleView:112-120` |
| `tools/muxcode/bus/launch.go` | `AgentFileName:83,93`; `RoleCLIEnvVar:122`; `RoleClaudeModelEnvVar:164`; `RoleClaudeModelDefault:220-226`; `RoleOpenCodeModelDefault:242-255`; `InlineFallbackPrompt:281`; `ResolveTaskFile:549-555`; `PreLaunchSetup` auto seed `:852-869` |
| `tools/muxcode/bus/agent.go` | `agentFileName:318,324` (duplicate mapping) |
| `tools/muxcode-llm-harness/harness/prompt.go` | `AgentFileName:269,275` (third duplicate) |
| `tools/muxcode-llm-harness/harness/config.go` | `roleModelEnvVar:143` |
| `tools/muxcode/bus/profile.go` | Profiles `research:802-814`, `auto:889-900`; `resolveRoleAlias:250` |
| `tools/muxcode/bus/provider.go` | `roleDefaultCLI:139-149` (drop `research`) |
| `tools/muxcode/bus/console.go` | Config `:397-403`; `renderResearch:1536+` |
| `tools/muxcode/bus/clear.go` | `autoClearExcluded:23`; mode-active guard `:134-136` |
| `tools/muxcode/bus/reload.go` | `ReloadAll` skip `:227-229`; `ReloadTarget:38-71` mode-window loop |
| `tools/muxcode/bus/reload_batch.go` | `Orchestrator:59` |
| `tools/muxcode/bus/diagnose.go` | `DiagnosableRoles` exclude `:1420-1432` |
| `tools/muxcode/bus/ollama.go`, `bus/health.go` | `roleModelEnvVar:99`; `roleEnvMap:177` |
| `tools/muxcode/bus/prompt.go` | `SharedPrompt` targets list `:23` |
| `tools/muxcode/cmd/send.go` | Research history-skip `:489-494` |
| `tools/muxcode/daemon/daemon.go` | Research history-skip `:3338-3344`; agent-defs watchdog skip `:1381-1383`; `checkHeartbeat:3355-3405` |
| `config/muxcode.json` | Research profile `:139-151`; auto profile |
| `config/tmux.conf` | F1 `:8-14`, `prefix+r` `:31-32`, F2 `:13-15`, `prefix+a` `:29-30` |
| `config/nvim/plugin/startscreen.lua` | Role list `:52-56` |
| `.muxcode/config` | `MUXCODE_RESEARCH_CLI`, `MUXCODE_RESEARCH_MODEL` overrides |
| `CLAUDE.md`, `README.md`, `docs/agents.md`, `docs/architecture.md`, `docs/configuration.md`, `docs/agent-bus.md`, `skills/agent-debug.md` | Remove role documentation |

**Tests to update or delete:** `bus/mode_test.go` (330-434, 493-514, 606-634),
`bus/reload_test.go:275-320`, `bus/clear_test.go:116-126,217-232`, `bus/console_test.go:248`,
`bus/profile_test.go:16,327`, `bus/prompt_test.go:42`, `bus/ollama_test.go:491`,
`bus/agent_test.go:73,76`, `bus/launch_test.go` (36,67,90,110,435,758-860),
`bus/reload_batch_test.go:54`, `bus/commit_authority_test.go:48-56`,
`bus/atlassian_authority_test.go:146,169`, `bus/prompt_authority_test.go:15,33-34`,
`bus/prompt_inject_test.go:80-96`, `bus/provider_options_test.go:155-221`,
`bus/graph_authority_test.go:64-66`, `bus/uitest_mode.go`, `daemon/heartbeat_test.go`,
`daemon/poll_health_test.go:288-314`, `bus/spawn_test.go` (uses `research` as its example role
throughout — substitute another role rather than delete the coverage).

## Implementation

### Phase 1: Decisions and backlog reconciliation

- [ ] Resolve [Decision 1](#decision-1) (removal vs. opt-in disable) with the user
- [ ] Resolve [Decision 2](#decision-2) (keep or remove `bus/mode.go`)
- [ ] Resolve [Decision 3](#decision-3) (fate of MUX-016, MUX-119, MUX-141, MUX-015, MUX-021)
- [ ] Record the outcome of each decision in this spec before any code changes

### Phase 2: Remove the `research` role

- [ ] Delete `agents/code-researcher.md` and `.opencode/agents/research.md`
- [ ] Remove the console config and `renderResearch` from `bus/console.go`
- [ ] Remove the research history-skip branches (`cmd/send.go:489-494`, `daemon/daemon.go:3338-3344`)
- [ ] Remove the research tool profile from `bus/profile.go` and `config/muxcode.json`
- [ ] Remove research from `roleDefaultCLI`, `RoleOpenCodeModelDefault`, `roleModelEnvVar`, `roleEnvMap`
- [ ] Remove `MUXCODE_RESEARCH_*` env-var name functions and the `.muxcode/config` overrides
- [ ] Remove research from `DefaultPlanModeCycleState` and the `launcher.go` mode-state seeding
- [ ] Change the `F1` binding to a bare `select-window -t:1` and delete the `prefix+r` binding
- [ ] Remove research from `HasConsoleView` and `startscreen.lua`
- [ ] Update or delete the research tests listed in [Key files](#key-files)

### Phase 3: Remove the `auto` role

- [ ] Delete `agents/autonomous-agent.md`, `agents/harness/autonomous-agent.md`, `skills/story-lifecycle.md`
- [ ] Remove the auto startup-seed from `PreLaunchSetup` and `ResolveTaskFile` entirely
- [ ] Remove `checkHeartbeat` from the daemon (its only consumer is `auto`)
- [ ] Remove the five hard-exclusion sites naming `auto` (`clear.go:23`, `reload.go:227-229`, `daemon.go:1381-1383`, `reload_batch.go:59`, `diagnose.go:1420-1432`)
- [ ] Remove the auto tool profile, model default, and `MUXCODE_AUTO_*` / `MUXCODE_AGENT_*` env vars
- [ ] Remove auto from `DefaultModeCycleState` and the `F2` / `prefix+a` bindings
- [ ] Remove the `agent`→`auto` aliases (`NormalizeBusRole:728`, `resolveRoleAlias:250`)
- [ ] Update or delete the auto tests listed in [Key files](#key-files)

### Phase 4: Registry, docs and cleanup

- [ ] Remove both roles from `KnownRoles` and `modeRoles`
- [ ] Remove both from all three role→file mappings **in the same commit**
- [ ] Remove both from the `SharedPrompt()` targets list (`bus/prompt.go:23`)
- [ ] Update `CLAUDE.md`, `README.md`, `docs/agents.md`, `docs/architecture.md`, `docs/configuration.md`, `docs/agent-bus.md`, `skills/agent-debug.md`
- [ ] Fix the two **pre-existing** doc inaccuracies found during this inventory: `README.md:90-95` and `skills/agent-debug.md:29-33` both describe `research` as hosted on `edit`, which was never true
- [ ] Add a `## Provenance` note to `docs/requirements/completed/MUX-082-research-mode.md` and `MUX-102-agent-mode.md` recording that the feature was removed by this spec — do not edit their bodies
- [ ] Confirm no orphaned runtime state: no `inbox/{research,auto}.jsonl`, `research-history.jsonl`, `agentdef-{research,auto}.hash`, `mode-cycle-plan.json`

### Phase 5: Integration test

- [ ] Create `scripts/test-remove-agents.sh` (hermetic; private tmux server via `TMUX_TMPDIR`, scratch `BUS_SESSION`)
- [ ] Test: a fresh session creates exactly the 10 default windows and no hold window
- [ ] Test: `F1` on the plan window selects it and does **not** swap; `F2` on edit likewise
- [ ] Test: `muxcode send research ...` and `muxcode send auto ...` both fail with unknown-role and create no inbox file
- [ ] Test: no `inbox/{research,auto}.jsonl`, `research-history.jsonl` or `agentdef-*.hash` appears after a full session lifecycle
- [ ] Test: `muxcode skill list --role plan` does not offer `story-lifecycle`
- [ ] Test: `muxcode reload --all`, `muxcode diagnose --all` and `muxcode mode list` all exit 0
- [ ] **Negative control:** a scratch session with a two-agent mode-cycle state still cycles correctly, proving `bus/mode.go` was left functional rather than silently broken
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Open decisions

### Decision 1 — Remove outright, or keep behind an opt-in?

**Recommendation: remove outright.** Git history is the recovery path, and a disabled-but-present
role keeps every registration site, test and doc line alive — which is most of the carrying cost.
An opt-in flag would preserve the `MUX-141` seeding path that motivates removal.

### Decision 2 — Keep `bus/mode.go`?

**Recommendation: keep it.** Removing both roles leaves it with zero consumers, but
[`MUX-015`](./MUX-015-refactor-agent.md) proposes an F6 review↔refactor toggle built on it.
Deleting the mechanism would make that spec a from-scratch build. The cost of keeping it is
unexercised code, which the Phase 5 negative control mitigates. **If MUX-015 is also withdrawn,
revisit — the machinery then has no future consumer either.**

### Decision 3 — What happens to the dependent backlog specs?

| Spec | Impact | Recommendation |
|------|--------|----------------|
| [`MUX-016`](./MUX-016-research-dual-provider.md) | Moot — it extends the research agent into a dual-provider split view | Withdraw; retire the id per the [registry rules](./backlog.md#github-tracking-mux-ids) |
| [`MUX-141`](./MUX-141-auto-agent-restart-relaunches-graph-runs.md) | **Symptom retired, gap open.** The spurious-run harm disappears with `auto`, but its fix is a *general* launch-reason signal through `PreLaunchSetup`; only the auto-task seeding is auto-specific. Nothing else exploits the gap today | Downgrade and rescope to the general fresh-vs-restart distinction — do **not** close as fixed. Has GitHub issue **#67**, which needs a human decision |
| [`MUX-119`](./MUX-119-graph-routes-edit-work-off-the-edit-agent.md) | Premise removed — it routes graph implementation work *to* `auto` | Rescope to a different target role, or withdraw |
| [`MUX-015`](./MUX-015-refactor-agent.md) | Loses its reference pattern (`code-researcher.md`) but not its premise | Keep; update to cite a surviving exemplar. Interacts with [Decision 2](#decision-2) |
| [`MUX-021`](./MUX-021-demo-mode-agent-coverage.md) | Has explicit `research (F1 mode)` and `research-handoff` rows | Update coverage rows |
| [`MUX-144`](./MUX-144-wait-human-gate-openable-by-any-agent.md) | Unaffected — `auto` was the *suspected* actor, not the defect. The gate has no authority check regardless of who calls it | No change |

**Gate consequence worth stating:** `MUX-141` is **gate 2 of 3** on
[`MUX-139`](./MUX-139-claude-agent-auto-resume.md). If MUX-141 is withdrawn rather than rescoped,
MUX-139 loses a gate and moves up the schedule. If it is rescoped, the gate stands. The choice
therefore changes the defect ordering, not just this spec.

## Out of scope

- **The `analyze` role**, which is also windowless and stranding messages (17 and climbing as of
  2026-09-03). Same family, different fix — tracked as
  [`MUX-145`](./MUX-145-messages-routed-to-windowless-role.md). Named here only so the adjacency
  is on record.
- **Skill frontmatter role scoping.** `parseYAMLList` (`bus/skill.go:101-116`) parses only inline
  `roles: [a, b]`; a block sequence yields an empty list, which `SkillsForRole` treats as *applies
  to every role*. Both `skills/story-lifecycle.md` and `skills/docs-management.md` use block
  style, so **both are currently offered to every role** — verified: `story-lifecycle` lists for
  `commit` and `plan`. Deleting `story-lifecycle.md` removes one symptom but not the parser gap,
  and `docs-management.md` would remain mis-scoped. **Needs its own spec.**

## Status

Backlog
