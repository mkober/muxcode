# MUX-200: Live-Agent Test of Auto Startup and Restore Behaviour

[MUX-141](../completed/MUX-141-auto-agent-restart-relaunches-graph-runs.md) gates the auto agent's
startup task on the launch reason and rewrites its definition so a restored agent idles. Its
integration script proves the **launcher and delivery** side with a `claude` stub
(`scripts/fixtures/claude-stub`) that never interprets the definition. What a real agent does with
each startup is observed, not tested. This spec closes that gap with a live-session test.

## Context

### Source

Filed 2026-09-30 on the user's decision, relayed by edit, to defer MUX-141's Phase 4
"live behaviour follow-up" box rather than hold that spec on it.

### What is unproven

| Claim | Proven for the launcher | Unproven for the live agent |
|-------|-------------------------|-----------------------------|
| A user start (`--reason user`) begins the story search | The `Agent started —` task is seeded and consumed with an `acked` receipt | The agent actually runs `muxcode atlassian jira search` and presents the list |
| A restore (`restart`, `reload`, `resume`, omitted reason) does no work | Only the `Session started —` context restoration is seeded | The agent restores context, reports what it would have resumed, and **idles** |
| `MUXCODE_AUTO_STARTUP_TASK=0` withholds the task | No task is seeded even on a user start | The agent idles |
| No graph run follows a restart | — (the stub cannot create runs) | No `graph-run-created` row appears; `Triggered by:` never reads "startup (launch reason: restart)" |

The "Jira-search boundary" is the point where the agent issues the search command; the test needs
no Jira instance, only evidence that the command was attempted (a `muxcode atlassian` invocation in
the pane or a `bash` history row) or, if the CLI reports no credentials, that the agent reported that
and stopped.

### Constraints

Live-session scripts are gated behind `MUXCODE_TEST_LIVE=1` in `scripts/test-all.sh` and run a real
`claude`. That makes them slow, non-deterministic in wording, and dependent on the installed provider,
so assertions read **evidence rows** (lifecycle log, console history, bus log, `graph status`), never
the agent's prose. `MUXCODE_AUTO_STARTUP_TASK` and the launch reason are the only inputs varied.

## Requirements

### Acceptance criteria

- [ ] A live-session script asserts, from evidence rows, each row of the table above
- [ ] The script is gated behind `MUXCODE_TEST_LIVE=1` and listed with the other live scripts in
      `scripts/test-all.sh` and `CLAUDE.md`
- [ ] Negative controls: the user-start case is the control for the restore case and vice versa —
      a script that cannot tell them apart fails
- [ ] Each assertion has a bounded wait (the agent must act or idle within N seconds) so an idle agent
      is a pass, not a hang
- [ ] MUX-141's deferred box points here, and this spec's Status carries the result of the first run

### Technical approach

Reuse `scripts/test-auto-startup-gating.sh`'s scratch layout (private tmux session, scratch bus dir,
tree-built binary) but launch the real provider. Drive the four cases in one session by relaunching
`auto` with each reason; after each launch, wait for the pane to reach `❯` and then read: the
lifecycle `launch` row (`reason=`), the console history for the role (a `muxcode atlassian jira
search` row or its absence), the bus log (a delegation send or its absence) and `graph status`
(no run created). Idle is asserted as "no work row within the wait", with the user-start case
proving the wait is long enough to see one.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-auto-startup-gating.sh` | Hermetic sibling; scratch layout to reuse |
| `scripts/test-all.sh` | Live-script gate |
| `agents/autonomous-agent.md` | The behaviour under test (§ Startup, § Heartbeat) |
| `tools/muxcode/bus/launch_reason.go`, `launch.go` | The seeding logic the live run exercises |

## Implementation

### Phase 1: Script

- [ ] Create `scripts/test-auto-startup-live.sh` gated on `MUXCODE_TEST_LIVE=1` (skips with a note
      otherwise, counting as a skip, never a pass)
- [ ] Case: user start reaches the Jira-search boundary
- [ ] Case: restart, reload and omitted reason each idle — no search row, no delegation, no run
- [ ] Case: `MUXCODE_AUTO_STARTUP_TASK=0` on a user start idles
- [ ] Bounded waits; evidence-row assertions only

### Phase 2: Integration test

- [ ] Coverage floor set to the maximum achievable count so a skipped case cannot report green
- [ ] Register in `scripts/test-all.sh` (live list) and `CLAUDE.md`
- [ ] Run with `MUXCODE_TEST_LIVE=1` against the installed provider and record the result in Status

## Related

- [MUX-141](../completed/MUX-141-auto-agent-restart-relaunches-graph-runs.md) — the spec this closes out
- [MUX-199](./MUX-199-remove-f2-mode-cycling.md) — removes the mode-cycle launch road; the case
  list here does not include it

## Status

Backlog
