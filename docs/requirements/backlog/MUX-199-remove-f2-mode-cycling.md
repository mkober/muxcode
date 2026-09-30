# MUX-199: Remove F2 Mode Cycling

Remove F2 mode cycling — the edit ↔ auto pane swap on the `edit` window — together with the
mode-cycle launch road. A second agent on a window is no longer reached by cycling panes; the intended
direction is a **modal window behind a toggle**, the shape the `api` agent already uses (`prefix + i`,
`bus/modal.go`), which launches with `--reason user` and needs no hold windows, cycle state or
cycle-specific launch reason.

## Context

### Source

Filed 2026-09-30 on the user's decision, relayed by edit: "remove F2 mode cycling entirely; second
agents will later use modal windows behind a toggle." Triggered by the
[MUX-141](../completed/MUX-141-auto-agent-restart-relaunches-graph-runs.md) Phase 4 finding that a default
session first launches `auto` by mode cycle (`LaunchReasonModeCycle`), which that spec classes as not
user-initiated, so a default session never seeds the Jira story search. Rather than decide whether a
first cycle counts as a user start, the user removed the road.

### What mode cycling is today

| Piece | Where |
|-------|-------|
| Cycle state per window: current index, registered agents, hold windows | `ModeCycleState`, `DefaultModeCycleState` (edit ↔ auto), `DefaultPlanModeCycleState` (plan ↔ research), `bus/mode.go` (533 lines; `mode_test.go` 637) |
| Pane swap into a hidden holding window; lazy first launch of the second agent with `--reason mode-cycle` | `bus/mode.go:353` (`AgentLaunchCommand(…, LaunchReasonModeCycle)`), `modeAutoAcceptAndWake` |
| CLI | `muxcode mode cycle [--window plan]`, `mode status`, `mode switch <mode>`, `mode list` (`cmd/mode.go`) |
| Keys | `F2` on window 2 and `prefix + a` → `mode cycle`; `F1` on window 1 and `prefix + r` → `mode cycle --window plan` (`config/tmux.conf:31–51`) |
| Session init | edit-window cycle state written at launch (`bus/launcher.go:216`) |
| Reason | `LaunchReasonModeCycle` (`bus/launch_reason.go`) and its tests |
| Other call sites | `reload.go` / `reload_batch.go` (reload of a mode role), `launch.go`, `setup.go`, `provider_options.go`, `provider_claude.go`, `uitest_mode.go`, `tui/graph_ui.go` |
| Docs | `docs/agents.md` (Autonomous Agent § Mode cycling, F1 research), `docs/agent-bus.md` (`muxcode mode`), `docs/architecture.md`, `CLAUDE.md` (hot reload `--all` excludes edit/auto; "F1 also hosts research via mode cycling") |
| State files | `mode-cycle-edit.json`, `mode-cycle-plan.json` under `BusDir()` |

### Scope decision to make in Phase 1

The user named **F2**. F1 (plan ↔ research) rides the same mechanism, and removing the mechanism
removes both. The default is to remove the mechanism whole — one cycle road half-removed is worse
than either state — unless the user wants F1 kept until research has its modal. Related:
[MUX-146](./MUX-146-remove-research-and-auto-agents.md) removes the research and auto agents
themselves, which would leave nothing to cycle to; this spec removes only the cycling and leaves
the roles reachable by the replacement.

### The replacement, by intent only

Second agents will run in **modal windows behind a toggle**, as `api` does: `RegisterModal` /
`DefaultModalConfigs` (`bus/modal.go`), a tmux popup sized by `FitSize`, launched through
`AgentLaunchCommand(…, LaunchReasonUser)` on the user's keypress. That work is **not** this spec; this
spec removes the old road and records the direction so the removal is not read as a regression.

## Requirements

### Acceptance criteria

- [ ] No `F2` / `prefix + a` binding cycles panes; `muxcode mode` is gone (or, if F1 is kept, refuses
      `--window edit`)
- [ ] `LaunchReasonModeCycle` no longer exists, and `TestLaunchRoads_CarryExplicitReason` loses its
      mode-cycle road rather than keeping a dead one
- [ ] No `mode-cycle-*.json` state is written at session init or read by any road
- [ ] `auto` (and `research`, if F1 goes too) stays launchable by an explicit
      `muxcode agent launch <role> --reason user` until its modal exists; the definition's startup
      table no longer lists "mode cycle" as a way it came back
- [ ] `muxcode reload --all` and per-role reload no longer special-case mode roles
- [ ] Docs: `docs/agents.md`, `docs/agent-bus.md`, `docs/architecture.md`, `CLAUDE.md` describe no
      cycling; the modal-behind-a-toggle direction is stated once, in `docs/agents.md`
- [ ] Every test that exercised cycling is removed or repointed, not skipped

### Technical approach

Delete rather than disable. Remove `bus/mode.go`, `cmd/mode.go`, the tmux bindings and the launcher's
cycle-state init; then follow the compiler through the other call sites. Where a call site exists
only to handle a mode role (reload's hold-window lookup, `uitest_mode.go`), remove it; where it
handles a real window, leave it. The `api` modal is the pattern for whatever replaces this, so nothing
here should make `bus/modal.go` harder to extend.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/mode.go`, `mode_test.go` | The mechanism — to delete |
| `tools/muxcode/cmd/mode.go` | The CLI — to delete |
| `config/tmux.conf` | `F1`/`F2`/`prefix + a`/`prefix + r` bindings |
| `tools/muxcode/bus/launcher.go` | Cycle-state init at session launch |
| `tools/muxcode/bus/launch_reason.go`, `launch_reason_test.go` | `LaunchReasonModeCycle` and its road test |
| `tools/muxcode/bus/reload.go`, `reload_batch.go`, `setup.go`, `provider_options.go`, `provider_claude.go`, `uitest_mode.go`, `tui/graph_ui.go` | Call sites to follow |
| `agents/autonomous-agent.md`, `agents/harness/autonomous-agent.md` | Startup table mentions mode cycle |
| `docs/agents.md`, `docs/agent-bus.md`, `docs/architecture.md`, `CLAUDE.md` | Docs |

## Implementation

### Phase 1: Scope

- [ ] Confirm with the user whether F1 (plan ↔ research) goes with F2; record the answer here
- [ ] List every reference (`grep -rn 'mode cycle\|mode-cycle\|ModeCycle'` across Go, tmux.conf,
      agents, docs) and mark each delete / repoint / keep

### Phase 2: Remove the mechanism

- [ ] Delete `bus/mode.go`, `cmd/mode.go` and their tests; remove the `mode` subcommand
- [ ] Remove the tmux bindings and the launcher's cycle-state init
- [ ] Remove `LaunchReasonModeCycle`; repoint or drop each call site the compiler names
- [ ] Definitions: drop "mode cycle" from the auto agent's startup table

### Phase 3: Docs

- [ ] Update `docs/agents.md`, `docs/agent-bus.md`, `docs/architecture.md`, `CLAUDE.md`
- [ ] State the modal-behind-a-toggle direction once, pointing at the `api` modal as the pattern

### Phase 4: Integration test

- [ ] Create `scripts/test-no-mode-cycle.sh` — hermetic: tree-built binary, private tmux session
- [ ] Assert `muxcode mode cycle` is not a subcommand (exit non-zero, usage names no `mode`)
- [ ] Assert a fresh session writes no `mode-cycle-*.json` and `F2` on window 2 swaps no panes
- [ ] Assert `muxcode agent launch auto --reason user` still launches and seeds the task
- [ ] Negative control: the `api` modal toggle still opens its popup
- [ ] Coverage floor set to the maximum achievable count
- [ ] Run the script and confirm all checks pass

## Related

- [MUX-141](../completed/MUX-141-auto-agent-restart-relaunches-graph-runs.md) — launch reasons; the
  open question this spec supersedes
- [MUX-146](./MUX-146-remove-research-and-auto-agents.md) — removes the roles themselves
- [MUX-016](./MUX-016-research-dual-provider.md) — research agent split view; would ride the modal
  road instead

## Status

Backlog
