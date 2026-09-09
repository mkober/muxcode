# The Stall Watchdog Re-Drives Into a Busy Claude Agent and Its Escape Preamble Kills the Running Tool

`checkStalledTasks` decides an agent has stalled on a consumed task when its pane shows the `❯`
prompt (`PaneHasIdlePrompt`) and nothing is pending. Claude Code keeps the `❯` composer on screen
while a tool call runs, so a busy Claude agent reads as idle — a fact two other daemon sites already
state in their comments and gate around with `PaneShowsRecoverableIdle`. After `TaskStallSecs` and
two consecutive sightings the watchdog force-delivers; with nothing unnotified that becomes
`redriveInFlightTasks` → `SendWakeUpWithText(force)`, whose Escape-before-payload preamble is
Claude's tool-interrupt key. The running command dies (`Interrupted · What should Claude do
instead?`), the re-drive text is the next prompt, the agent restarts the command, and the second
re-drive kills the restart. Net effect: any run-agent command longer than about two minutes cannot
finish. On 2026-09-09 that was MUX-167's integration script; the user ran it by hand.

## Context

### Observed (session `muxcode`, run agent, task `1788982499 edit→run:run`, 2026-09-09)

| When | What | Source |
|------|------|--------|
| 15:35:00 | edit → run `request:run` "run exactly this one command …: `bash scripts/test-multi-phase-graph.sh`" — a multi-minute integration script | history, lifecycle `inbox-notify run` |
| ~15:35:30 | run (Claude) consumes it through its listener and starts the Bash call | run pane |
| 15:37:24 | `deliver force-redrive run: 1 in-flight task(s)`; `daemon task-stall-redrive edit→run:run redrive 1/2` | lifecycle |
| 15:38:25 | `redrive 2/2` | lifecycle |
| 15:38 | run → edit `response:run-blocked`: "execution was rejected/interrupted twice at the tool-use level before running (no script output produced). Halting retries" | `muxcode history run` |
| 15:47 | run pane: the Bash call shows `⎿ Interrupted · What should Claude do instead?` followed by the injected `❯ Re-drive (consumed but never completed): New message from edit [request:run] …` — twice | pane capture (edit) |
| later | the user types "go ahead and run it" into the run pane by hand | edit's account |

### Mechanism — verified in code

- `daemon/daemon.go:2926` `checkStalledTasks` — `:2943` `TaskStalled(t, now, TaskStallSecs())`
  (90 s), `:2956` the idle test is `PaneHasIdlePrompt(content)` with `!HasPendingInput`; `:132`
  `taskStallSeen` debounces to two consecutive 30 s sightings; then `ForceDeliver(session, role,
  true)`.
- `daemon/daemon.go:2337–2338` and `:2489` — two sibling checks say it outright: "Gate on
  `PaneShowsRecoverableIdle`, NOT `PaneHasIdlePrompt`: the ❯ composer renders even mid-turn, so
  `PaneHasIdlePrompt` is true for a busy agent". The stall watchdog uses the weaker test they reject.
- `bus/deliver.go:117` `redriveInFlightTasks` and `:145` `RedriveTask` — both end in
  `SendWakeUpWithText(session, role, provider, text, true)` (`:132`, `:158`); `:101` is the
  `ForceDeliver` road into them.
- `bus/notify.go:871` `SendWakeUpWithText` — the typed injection, preceded by `TmuxDismissOverlay`
  (Escape → `C-e` absorber; [MUX-163](../drafts/MUX-163-prompt-inject-escape-eats-first-char.md)).
  On an idle pane the Escape clears an overlay; on a busy pane it is the interrupt. The Escape was
  there before MUX-163 — the preamble made it reliable, not new.
- `bus/graph_exec.go:1302–1305` `graphAgentIdleFn = IsAgentIdle` — the executor's own redrive path
  was given the provider-aware test on 2026-09-09 for exactly this class ("redriven into its running
  turn twice"); the stall watchdog was not.
- `bus/notify.go:547` `IsAgentIdle` and `bus/provider_claude.go:112` `ClaudeCodeProvider.IsIdle` —
  the provider-aware answer the watchdog should be asking.

### Scope boundary

The Escape preamble stays: an idle pane's overlay eats the Enter without it (MUX-163), and the
defect is *when* the wake fires, not *how*. The stall cadence stays. What changes is that no road —
watchdog, receipt-gap recovery, `deliver --force`, remote — re-drives a pane that is working.

## Requirements

### Acceptance criteria

- [ ] The stall watchdog never re-drives a Claude agent whose pane shows a running tool call (spinner / "esc to interrupt"), `❯` on screen or not; the stall sighting resets and a `stall-skipped-busy` row is written once per task
- [ ] An idle prompt with no running tool still re-drives after `TaskStallSecs` (negative control — the watchdog keeps catching real stalls)
- [ ] `redriveInFlightTasks` and `RedriveTask` refuse a busy pane themselves (`redrive-skipped-busy`), so `deliver --force`, receipt-gap recovery and remote callers cannot interrupt a running tool either
- [ ] A run-agent command longer than 2 × `TaskStallSecs` completes and its reply lands — `scripts/test-multi-phase-graph.sh`-length work through the run agent produces no `Interrupted` pair in the pane
- [ ] Docs: watchdog tables in `docs/architecture.md` and `docs/hooks.md`, `CLAUDE.md` "Daemon watchdogs" bullet

### Technical approach

**Primary — ask the provider, and refuse at the helper too.** `checkStalledTasks` replaces
`PaneHasIdlePrompt` with `bus.IsAgentIdle` (provider-aware; `ClaudeCodeProvider.IsIdle` must read a
running tool call — spinner, "esc to interrupt" — as busy, extended if it only looks for `❯`) and
treats not-idle as busy: reset `taskStallSeen`, one `stall-skipped-busy` row per task. Defence in
depth: `redriveInFlightTasks` and `RedriveTask` check the same predicate and log
`redrive-skipped-busy`, so every caller inherits the rule.

**Rejected — drop the Escape from forced wakes.** Breaks the idle case MUX-163 fixed and leaves the
real fault — waking a working agent — in place.

**Rejected — lengthen `TaskStallSecs`.** A twenty-minute script is still killed, later.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | `checkStalledTasks` (2926–2993), `taskStallSeen` (132), the sibling gates that already know (2337, 2489) |
| `tools/muxcode/bus/deliver.go` | `ForceDeliver` road (101), `redriveInFlightTasks` (117), `RedriveTask` (145) |
| `tools/muxcode/bus/notify.go` | `PaneHasIdlePrompt` (488), `IsAgentIdle` (547), `SendWakeUpWithText` (871) |
| `tools/muxcode/bus/provider_claude.go` | `IsIdle` (112) — the running-tool signals |
| `tools/muxcode/bus/graph_exec.go` | `graphAgentIdleFn` (1302–1305) — the precedent |
| `docs/architecture.md`, `docs/hooks.md`, `CLAUDE.md` | watchdog tables and bullet |

## Implementation

### Phase 1: The watchdog asks the provider

- [ ] `checkStalledTasks`: `bus.IsAgentIdle` in place of `PaneHasIdlePrompt`; not-idle → reset `taskStallSeen`, `stall-skipped-busy` once per task
- [ ] `ClaudeCodeProvider.IsIdle`: verified (or extended) to read a running tool call as busy — spinner line, "esc to interrupt"
- [ ] Tests: a pane fixture with `❯` plus an active spinner → no redrive, sighting reset, one row; an idle fixture without a spinner → redrive after `TaskStallSecs` (negative control)

### Phase 2: The helpers refuse

- [ ] `redriveInFlightTasks` and `RedriveTask` refuse a busy pane and log `redrive-skipped-busy`
- [ ] Tests: the helpers refuse the busy fixture; `ForceDeliver` on the idle fixture still re-drives (negative control)

### Phase 3: Docs

- [ ] Watchdog tables in `docs/architecture.md` and `docs/hooks.md`; `CLAUDE.md` "Daemon watchdogs" bullet — busy panes are never re-driven, from any road

### Phase 4: Integration test

- [ ] Hermetic script (scratch `BUS_SESSION`, scratch pane rendering a Claude-shaped busy frame — `❯` plus spinner — with an in-flight task older than `TaskStallSecs`, the daemon on a short `MUXCODE_TASK_STALL_SECS`): assert the pane content is untouched after two check intervals and the `stall-skipped-busy` row exists
- [ ] Negative control: the same with an idle frame → `task-stall-redrive` row and the re-drive text in the pane
- [ ] Run the script and record the counts in this spec

## Notes

- Filed 2026-09-09 16:02 by plan from edit's handoff (`mux-171-handoff.md`, session `03a897a2`);
  the lifecycle rows, run's `run-blocked` reply and the code lines above re-verified here. The pane
  capture is edit's.
- MUX-167 Phase 4 is what this blocked: `test-multi-phase-graph.sh` was never run through the run
  agent under the graph's rule that workers verify via the run agent, not `go test`.
- Related: [MUX-112](./MUX-112-idle-task-rescue-closes-live-work.md) (the synthetic-reply sibling —
  the same "❯ means idle" error closing live work from the other side);
  [MUX-123](./MUX-123-stall-watchdog-selective-misses.md) (the watchdog's misses; this is its false
  positive); [MUX-163](../drafts/MUX-163-prompt-inject-escape-eats-first-char.md) (the preamble);
  [MUX-170](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) (found in the same hour).

## Status

**Backlog** — 0/14. Filed 2026-09-09 16:02.
