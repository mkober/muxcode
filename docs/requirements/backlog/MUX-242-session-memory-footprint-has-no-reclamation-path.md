# MUX-242: A Session's Memory Footprint Only Grows — the Largest Consumers Have No Reclamation Path

**Tracking:** [mkober/muxcode#116](https://github.com/mkober/muxcode/issues/116) — filed for
MUX-147, which carried this and the orphan reaper together; the issue still has MUX-147's title.

**Split from:** [MUX-147](../completed/MUX-147-process-leak-and-memory-footprint.md) on the user's
decision of 2026-11-24, relayed by edit: its Defect 2 and Phases 4–5, moved whole. **Parked at
filing**, on the same decision: this is measurement-first work whose one adoptable saving — idle
eviction — is deferred.

A 10-window session measured 2.5–4.2 GB on 2026-09-03, and every agent that does work only grows.

## Context

### Measured 2026-09-03

A live session (10 windows, 9 agents, 46 min uptime) measured with `ps -axo rss=`:

| Category | Procs | RSS | Note |
|----------|------:|----:|------|
| OpenCode agents | 6 | 1,076 → **3,004 MB** | Grew ~2 GB during a 46-min session |
| Claude agents | 3 | 881–962 MB | plan, edit, run |
| muxcode binary | 22–24 | ~320 MB | daemon, monitor, 8 consoles, 11 control panes, 4 listeners |
| Orphaned harness | 36 | ~243 MB | The leak — MUX-147's other half, closed by MUX-184 |

The muxcode binary is the cheapest part. The cost is the agent CLIs it launches.

### The largest consumers have no reclamation path

Six OpenCode agents, identical 46-minute uptime, split by whether they did work:

| Agent | RSS | Activity |
|-------|----:|----------|
| `test` | 924 MB | worked this session |
| `review` | 892 MB | worked this session |
| `build` | 671 MB | worked this session |
| `watch` / `deploy` / `serve` | ~152 MB each | idle |

About 150 MB idle, growing roughly sixfold with conversation length. As filed in September:

- Auto-clear refused non-Claude providers (`bus/clear.go`: "`/clear` is a Claude Code built-in"),
  so the three largest consumers were outside the only reclamation mechanism.
- Auto-clear was off by default (`MUXCODE_AUTO_CLEAR_ROLES` unset), so the Claude agents were not
  reclaiming either.

**None of this has been re-measured.** The numbers are eleven weeks old; since then Codex became a
provider, graph spawn workers are reused and reaped (MUX-195), and the orphan reaper shipped
(MUX-184). Phase 1 starts by measuring again.

## Decisions (user, 2026-11-24)

Relayed by edit, as MUX-147's Decision 3: **idle eviction is deferred.** It is not adopted and not
ruled out; nothing here builds it. No other decision about the footprint has been made, which is
why this spec is parked.

## Requirements

### Acceptance criteria

- [ ] A documented baseline exists: RSS per agent per provider, idle and after work, over time
- [ ] The growth curve is characterised — whether it plateaus, and at what
- [ ] At least three reduction options are evaluated with measured savings and stated trade-offs
- [ ] A reclamation path exists for non-Claude agents, or its absence is documented with a reason
- [ ] `muxcode session status` (or an equivalent) reports the session's current memory footprint
- [ ] Recommendations are recorded in this spec; adopting one is split into its own spec

### Technical approach

Measurement first. No option is committed to before Phase 2 reports, and the measurement must
separate the variables the September numbers conflate: provider, model, and activity.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/clear.go` | `AutoClearEligible` provider gate; `AutoClearRoles` default-off |
| `tools/muxcode/cmd/` | Where a footprint report would live |
| `docs/configuration.md` | Model-selection guidance, if the spread is model-driven |

## Implementation

### Phase 1: Measure the footprint

- [ ] Build a repeatable measurement (RSS per role, per provider, with uptime and activity state)
- [ ] Baseline: an idle session, all agents launched, no work
- [ ] Curve: the same session sampled over several hours of normal work — does growth plateau?
- [ ] Compare providers at equal work: OpenCode, Codex, Claude and the harness, same role
- [ ] Compare models — the September spread (`deepseek-v4-flash` 924 MB vs `minimax-m3` 153 MB) may
      be model-driven, activity-driven, or both; separate the variables
- [ ] Quantify the fixed overhead (console panes, control panes, listeners — ~320 MB in September)
- [ ] Record all measurements in this spec

### Phase 2: Evaluate reduction options

Each option gets measured savings and a stated trade-off:

- [ ] Reclamation for non-Claude agents — a provider-native equivalent of `/clear`
- [ ] Idle eviction — evaluated only, per the decision above: stop agents idle beyond a threshold,
      relaunch on demand; the cost is cold-start latency and, without resume, the conversation
- [ ] Auto-clear on by default for eligible roles
- [ ] Reduce fixed overhead — whether control and console panes need separate processes
- [ ] Model and provider guidance, if the spread is model-driven
- [ ] Fewer resident agents — launch on first use rather than at session start
- [ ] Recommend a ranked set; split adoption into follow-up specs rather than growing this one

### Phase 3: Integration test

- [ ] Create `scripts/test-session-footprint.sh` — muxcode and the claude-stub built from the tree,
      a private tmux server (`scripts/lib/private-tmux.sh`), two stub agents
- [ ] The footprint report lists each launched role with a non-zero RSS and a session total equal
      to the sum of its rows
- [ ] Negative control: a role whose agent is stopped is absent from the report, or listed at zero
      and marked stopped — never carried at its last value
- [ ] Coverage floor, and the script is named in `CLAUDE.md`'s scripts row
- [ ] Run the script and confirm all checks pass

## Out of scope

- Reducing the LLM CLIs' own memory use — third-party; this spec can change how many run, how long
  and with what context
- Orphaned processes — MUX-184, shipped
- Machine-level memory pressure

## Status

**Backlog — parked at filing 2026-11-24**, 0/25. Split from MUX-147 by plan on the user's decision
relayed by edit. Not started; the September measurements have not been repeated.
