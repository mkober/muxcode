# MUX-147: Reap Orphaned Harness Processes and Reduce Session Memory Footprint

Two related pieces of process hygiene, filed together because they were found by the same
measurement and share a subject — what a running muxcode session costs in memory.

1. **Reap orphaned harness processes.** Nothing cleans up `muxcode-llm-harness` processes whose
   launching parent dies. 36 had accumulated over ~2 days holding ~243 MB and burning CPU.
2. **Reduce the footprint of a multi-agent session.** A 10-window session measured **2.5–4.2 GB**,
   and the three largest consumers have **no memory-reclamation path at all**.

## Context

### Measured 2026-09-03

A live session (10 windows, 9 agents, 46 min uptime) measured with `ps -axo rss=`:

| Category | Procs | RSS | Note |
|----------|------:|----:|------|
| OpenCode agents | 6 | 1,076 → **3,004 MB** | Grew ~2 GB during a 46-min session |
| Claude agents | 3 | 881–962 MB | plan, edit, run |
| muxcode binary | 22–24 | ~320 MB | daemon, monitor, 8 consoles, 11 control panes, 4 listeners |
| Orphaned harness | 36 | **~243 MB** | **Leak — reaped by hand, see Defect 1** |
| **Total** | **~70** | **2.5 → 4.2 GB** | |

The muxcode binary itself is the cheapest part. The cost is the agent CLIs it launches, and the
leak.

### Defect 1 — orphaned harness processes are never reaped

36 `muxcode-llm-harness run prompt` processes, **every one with `ppid=1`** (parent dead,
reparented to launchd). Zero were correctly parented. Characteristics:

- Accumulated over ~2 days (oldest 2026-09-02 10:13, newest 2026-09-03)
- **Not idle** — ~50s CPU each, still polling their inboxes
- Spanned **13 `BUS_SESSION`s**, including **8 from integration test runs**:
  `version-test-*` (5), `auto-clear-test-*`, `force-respond-test-*`, `multiphase-test-*`
- Only 10 belonged to the live session

Reaped by hand 2026-09-03 (`kill -TERM` filtered on `ppid == 1`), reclaiming ~243 MB. **The
mechanism that created them is untouched**, so they will accumulate again.

**Same family as [`MUX-137`](./MUX-137-test-bus-dir-leak.md)** — hermetic test scripts not
cleaning up after themselves. MUX-137 leaks *directories*; this leaks *running processes*, which
is strictly worse: they hold memory, consume CPU, and poll live bus inboxes indefinitely.

#### Root cause — observed live, 2026-09-03 14:27

A **fresh orphan appeared within minutes of the hand-reap**, which exposed the mechanism:

| Time | Event |
|------|-------|
| 14:27:28 | harness `13317` running, parented to daemon `11463` (`AGENT_ROLE=prompt`) |
| ~14:28 | daemon `11463` dies — the keepalive monitor kills a daemon whose keepalive is stale (>30s) |
| 14:28:27 | replacement daemon `17412` starts |
| after | harness `13317` still alive, now **`ppid=1`** — orphaned, and the new daemon neither adopts nor reaps it |

**The daemon restart *is* the leak.** Each restart orphans the prompt harness it had parented,
and the replacement has no knowledge of its predecessor's children. The lifecycle log records
`monitor / daemon-restart` events — **8 in this session**, including a **storm of 5 in 61 seconds**
(01:57:58 → 01:58:59, one per ~15s monitor tick) — against **10 orphans** traced to this session.
The remaining 26 came from other sessions and test runs, whose parents died the same way.

This makes the leak **rate-proportional to daemon instability**, so it worsens exactly when the
session is already unhealthy. It also means Phase 1's question is largely answered: any reaper
must handle the *predecessor-daemon* case explicitly, and a new daemon adopting or sweeping its
predecessor's children is the most direct fix.

**A live prompt agent is indistinguishable from an orphan by name alone.** The one surviving
process (`ppid=11463`, parented to the session daemon, `AGENT_ROLE=prompt`) is legitimate. Any
reaper **must** key on parentage/liveness, never on the process name — `pgrep -f
muxcode-llm-harness` also matches unrelated processes whose argv merely quotes the string.

### Defect 2 — the largest consumers have no reclamation path

Six OpenCode agents, identical 46-minute uptime, split sharply by whether they did work:

| Agent | RSS | Activity |
|-------|----:|----------|
| `test` | 924 MB | worked this session |
| `review` | 892 MB | worked this session |
| `build` | 671 MB | worked this session |
| `watch` / `deploy` / `serve` | ~152 MB each | idle |

**~150 MB idle baseline, growing ~6× with conversation length.** Nothing brings it back down:

- **Auto-clear (MUX-103) refuses non-Claude providers** — `bus/clear.go:129` returns
  `"provider %s (/clear is a Claude Code built-in)"`. The three largest consumers are all
  OpenCode, so they are excluded from the only reclamation mechanism that exists.
- **Auto-clear is off by default** — `AutoClearRoles()` (`clear.go:63-67`) returns `nil` when
  `MUXCODE_AUTO_CLEAR_ROLES` is unset, which it is in this session. So even the Claude agents
  are not reclaiming.

The result: a long-lived session's memory is monotonically non-decreasing for every agent that
does work. This is an **analysis-first** problem — the phases below deliberately measure before
proposing a fix, because the right answer (idle eviction, provider-native compaction, model
choice, fewer resident agents) is not yet established.

## Requirements

### Acceptance criteria

**Reaping**

- [ ] Orphaned harness processes are reaped automatically without human intervention
- [ ] The reaper identifies orphans by **parentage and session liveness**, never by process name alone
- [ ] A live prompt agent with a healthy parent is **never** killed (negative control)
- [ ] A harness process whose `BUS_SESSION` tmux session no longer exists is reaped
- [ ] Integration test scripts clean up their own harness processes on exit, including on failure and interrupt
- [ ] Each reap emits a lifecycle event naming the pid, session and age
- [ ] The reaper is opt-out via an env var, consistent with the other daemon watchdogs
- [ ] Reaping never touches a process belonging to a *different, live* muxcode session

**Footprint**

- [ ] A documented baseline exists: RSS per agent per provider, idle vs. after work, over time
- [ ] The growth curve is characterised — whether it plateaus, and at what
- [ ] At least three reduction options are evaluated with measured savings and stated trade-offs
- [ ] A reclamation path exists for **non-Claude** agents, or its absence is documented with a reason
- [ ] `muxcode status` (or equivalent) can report the session's current memory footprint
- [ ] Recommendations are recorded in this spec; implementing them may be split into follow-up specs

### Technical approach

**Reaping** belongs in the daemon, beside the existing watchdogs (`checkStuckProviders`,
`checkActiveWatchdog`, `checkTrackedTasks`), which already own the "detect a bad process state and
self-heal" role. The daemon knows which sessions are live and which roles it launched, so it has
the context a standalone script would lack.

Two candidate triggers, to be decided in Phase 2:

| Trigger | Catches | Misses |
|---------|---------|--------|
| Daemon sweep on its own session | Orphans from its session's own crashed launches | Orphans from dead sessions and test runs (the majority here — 26 of 36) |
| Startup sweep across all sessions | Everything, including test leftovers | Needs care not to reap a *live* peer session's processes |

The evidence favours **both**: a periodic per-session sweep plus a startup sweep that reaps
processes whose `BUS_SESSION` names a tmux session that no longer exists. The second is what
would have caught all 26 foreign orphans.

**Test cleanup** is separable and cheaper: a `trap`-based cleanup in the integration scripts that
kills processes started under the script's scratch `BUS_SESSION`, firing on `EXIT`/`INT`/`TERM`.

**Footprint** work is measurement-first. No optimisation is committed to before Phase 4 reports.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/daemon/daemon.go` | Watchdog loop — new reaper check alongside `checkStuckProviders` |
| `tools/muxcode/bus/stuck.go` | Precedent for watchdog detection helpers |
| `tools/muxcode/bus/agent_health.go` | `IsAgentAlive`, `RoleHasWindow` — liveness primitives to reuse |
| `tools/muxcode/bus/proc.go` | `CheckProcAlive`, `RefreshProcStatus`, `CleanFinished` — existing process-tracking patterns |
| `tools/muxcode/bus/clear.go` | `AutoClearEligible:129` provider gate; `AutoClearRoles:63` default-off |
| `tools/muxcode/bus/lifecycle.go` | `LogLifecycle` for reap events |
| `tools/muxcode/bus/remote.go` | `DiscoverSessions` — enumerating live sessions for the startup sweep |
| `scripts/test-*.sh` | Add `trap` cleanup; `scripts/lib/` is the natural home for a shared helper |
| `tools/muxcode-llm-harness/harness/loop.go` | Whether the harness can detect its own orphaning and self-exit |

## Implementation

### Phase 1: Characterise the leak

- [x] Determine why harness processes outlive their parent — **daemon restart orphans its child harness; the replacement neither adopts nor reaps it** (observed live 2026-09-03 14:27, see [Root cause](#root-cause--observed-live-2026-09-03-1427))
- [ ] Confirm the same mechanism explains the 26 orphans from other sessions and test runs
- [ ] Establish whether a new daemon can adopt or sweep its predecessor's children directly
- [ ] Establish whether the harness can detect orphaning itself (parent death, unreachable bus dir) and exit
- [ ] Confirm which integration scripts leak (`version-test`, `auto-clear-test`, `force-respond-test`, `multiphase-test` observed)
- [ ] Record whether any non-harness muxcode process leaks the same way
- [ ] Write the findings into this spec before building the reaper

### Phase 2: Reaper design decision

- [ ] Decide between daemon sweep, startup sweep, or both (see [Technical approach](#technical-approach))
- [ ] Define the orphan predicate precisely — parentage, session liveness, age threshold
- [ ] Confirm the predicate cannot match a live prompt agent or a peer session's process
- [ ] Record the decision and its rationale in this spec

### Phase 3: Implement reaping

- [ ] Implement the orphan predicate with unit tests, including the live-agent negative control
- [ ] Add the daemon reaper check with an opt-out env var
- [ ] Emit a lifecycle event per reap (pid, session, age, reason)
- [ ] Add `trap`-based cleanup to the leaking integration scripts
- [ ] Add a shared cleanup helper under `scripts/lib/` so new scripts inherit it
- [ ] Verify a full test-suite run leaves zero orphaned processes

### Phase 4: Measure the footprint

- [ ] Build a repeatable measurement (RSS per role, per provider, with uptime and activity state)
- [ ] Baseline: idle session, all agents launched, no work
- [ ] Curve: same session sampled over several hours of normal work — does growth plateau?
- [ ] Compare providers at equal work: OpenCode vs Claude vs harness, same role
- [ ] Compare models — the observed spread (`deepseek-v4-flash` 924 MB vs `minimax-m3` 153 MB) may be model-driven, activity-driven, or both; **separate these variables**
- [ ] Quantify the fixed overhead: 8 console panes + 11 control panes + 4 listeners ≈ 320 MB
- [ ] Record all measurements in this spec

### Phase 5: Evaluate reduction options

Each option gets measured savings and a stated trade-off — no option is adopted before Phase 4:

- [ ] **Reclamation for non-Claude agents** — provider-native compaction/restart equivalent to `/clear`; closes the gap at `clear.go:129`
- [ ] **Idle eviction** — stop agents idle beyond a threshold, relaunch on demand (`research`/`auto` already prove lazy launch works); trade-off is cold-start latency and lost conversation
- [ ] **Enable auto-clear by default** for eligible roles, given it is currently off entirely
- [ ] **Reduce fixed overhead** — whether 11 control panes and 8 console panes need separate processes
- [ ] **Model/provider guidance** — if the spread is model-driven, document it in the model-selection table
- [ ] **Fewer resident agents** — launch on first use rather than at session start
- [ ] Recommend a ranked set; split adoption into follow-up specs rather than expanding this one

### Phase 6: Integration test

- [ ] Create `scripts/test-process-reaper.sh` (hermetic; private tmux server via `TMUX_TMPDIR`, scratch `BUS_SESSION`)
- [ ] Test: a deliberately orphaned harness process (parent killed) is reaped within one sweep
- [ ] Test: a lifecycle event is emitted naming pid, session and age
- [ ] **Negative control:** a *live* harness process with a healthy parent survives the sweep
- [ ] **Negative control:** a harness process belonging to a different, still-live scratch session is not reaped
- [ ] Test: a process whose `BUS_SESSION` tmux session was killed is reaped by the startup sweep
- [ ] Test: the opt-out env var fully disables reaping
- [ ] Test: a scratch integration script that is interrupted mid-run leaves zero orphans
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Open decisions

### Decision 1 — should the harness self-exit when orphaned?

A process that notices its parent is gone and its bus dir is unreachable could exit on its own,
making the reaper a backstop rather than the primary mechanism. Cheaper and more robust across
sessions, but it puts the logic in the harness where it cannot help any *other* leaked process
type. **Recommendation: do both** — self-exit as primary, daemon reaper as the backstop that
catches processes already wedged.

### Decision 2 — may a daemon reap another session's processes?

The startup sweep needs this to catch the 26 foreign orphans, but a wrong predicate could kill a
healthy peer session's agents — and multiple fleets do run on this machine. **Recommendation:**
reap only when the named `BUS_SESSION` has **no live tmux session**, which is decidable and safe.
Never reap based on age alone.

### Decision 3 — is idle eviction acceptable?

It is the largest available saving (three idle agents held ~456 MB doing nothing) but changes
interaction: a keypress on an evicted window pays a cold start, and conversation is lost unless
resume is wired in. Interacts with
[`MUX-139`](./MUX-139-claude-agent-auto-resume.md). **Needs a user ruling before Phase 5 adopts it.**

## Out of scope

- **Reducing the LLM CLIs' own memory use.** OpenCode and Claude Code are third-party; this spec
  can change *how many run, how long, and with what context*, not their internals.
- **The MUX-137 test bus-dir leak** — same family, already tracked separately.
- **Machine-level memory pressure.** The host measured 17 GB used with 84 MB unused, but browsers
  accounted for ~1.4 GB of that. muxcode's share is what this spec addresses.

## Status

Backlog
