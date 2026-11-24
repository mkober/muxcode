# MUX-147: Reap Orphaned Harness Processes and Reduce Session Memory Footprint

**Tracking:** [mkober/muxcode#116](https://github.com/mkober/muxcode/issues/116)

**Closed 2026-11-24** on the user's decision, relayed by edit — see [Decisions](#decisions-user-2026-11-24). Nothing was built under this spec: every ticked box is a hand-off to another spec, named on its line, and the six boxes still open in Phase 1 are measurements nobody made.

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

**Same family as [`MUX-137`](../completed/MUX-137-test-bus-dir-leak.md)** — hermetic test scripts not
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

**What this spec no longer carries (2026-11-15).** The user decided, relayed by edit
(`1794787239-edit-1aacf9a4`), that this spec's reaper phases point to
[MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) — the reaper for every process
family — and that this spec keeps only its harness-specific half: the integration scripts' own
cleanup, the harness self-exit question (Decision 1 below, still open) and the footprint. Twenty
items are ticked below as deferred, not done. Two things differ where they landed: MUX-184 proves
ownership by a recorded pid that still names the same process, not by "parentage and session
liveness", and its sweep runs at session launch rather than as a periodic daemon check
(`MUXCODE_ORPHAN_REAP=0` is its opt-out). MUX-184 Decision 4 also closes this spec's root cause at
the source: the daemon stops its harness on TERM/INT.

**Reaping**

- [x] Orphaned harness processes are reaped automatically without human intervention — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] The reaper identifies orphans by **parentage and session liveness**, never by process name alone — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md), whose proof rule replaces this predicate
- [x] A live prompt agent with a healthy parent is **never** killed (negative control) — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] A harness process whose `BUS_SESSION` tmux session no longer exists is reaped — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Integration test scripts clean up their own harness processes on exit, including on failure and interrupt — deferred on the user's decision of 2026-11-24 to [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), [MUX-235](./MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) and [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md)
- [x] Each reap emits a lifecycle event naming the pid, session and age — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) (`orphan-reaped`)
- [x] The reaper is opt-out via an env var, consistent with the other daemon watchdogs — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) (`MUXCODE_ORPHAN_REAP=0`)
- [x] Reaping never touches a process belonging to a *different, live* muxcode session — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)

**Footprint**

- [x] A documented baseline exists: RSS per agent per provider, idle vs. after work, over time — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] The growth curve is characterised — whether it plateaus, and at what — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] At least three reduction options are evaluated with measured savings and stated trade-offs — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] A reclamation path exists for **non-Claude** agents, or its absence is documented with a reason — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] `muxcode status` (or equivalent) can report the session's current memory footprint — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] Recommendations are recorded in this spec; implementing them may be split into follow-up specs — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)

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

Deferred whole to MUX-184 on 2026-11-15 (see "What this spec no longer carries").

- [x] Decide between daemon sweep, startup sweep, or both (see [Technical approach](#technical-approach)) — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) (its Decision 2: at every session launch)
- [x] Define the orphan predicate precisely — parentage, session liveness, age threshold — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) (its Decision 1)
- [x] Confirm the predicate cannot match a live prompt agent or a peer session's process — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Record the decision and its rationale in this spec — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md), where it is recorded

### Phase 3: Implement reaping

The first three steps are deferred to MUX-184; the script cleanup stays here.

- [x] Implement the orphan predicate with unit tests, including the live-agent negative control — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Add the daemon reaper check with an opt-out env var — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Emit a lifecycle event per reap (pid, session, age, reason) — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Add `trap`-based cleanup to the leaking integration scripts — deferred on the user's decision of 2026-11-24 to [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), [MUX-235](./MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) and [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md)
- [x] Add a shared cleanup helper under `scripts/lib/` so new scripts inherit it — deferred on the user's decision of 2026-11-24 to [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), [MUX-235](./MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) and [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md)
- [x] Verify a full test-suite run leaves zero orphaned processes — deferred on the user's decision of 2026-11-24 to [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), [MUX-235](./MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) and [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md)

### Phase 4: Measure the footprint

- [x] Build a repeatable measurement (RSS per role, per provider, with uptime and activity state) — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] Baseline: idle session, all agents launched, no work — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] Curve: same session sampled over several hours of normal work — does growth plateau? — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] Compare providers at equal work: OpenCode vs Claude vs harness, same role — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] Compare models — the observed spread (`deepseek-v4-flash` 924 MB vs `minimax-m3` 153 MB) may be model-driven, activity-driven, or both; **separate these variables** — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] Quantify the fixed overhead: 8 console panes + 11 control panes + 4 listeners ≈ 320 MB — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] Record all measurements in this spec — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)

### Phase 5: Evaluate reduction options

Each option gets measured savings and a stated trade-off — no option is adopted before Phase 4:

- [x] **Reclamation for non-Claude agents** — provider-native compaction/restart equivalent to `/clear`; closes the gap at `clear.go:129` — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] **Idle eviction** — stop agents idle beyond a threshold, relaunch on demand (`research`/`auto` already prove lazy launch works); trade-off is cold-start latency and lost conversation — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] **Enable auto-clear by default** for eligible roles, given it is currently off entirely — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] **Reduce fixed overhead** — whether 11 control panes and 8 console panes need separate processes — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] **Model/provider guidance** — if the spread is model-driven, document it in the model-selection table — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] **Fewer resident agents** — launch on first use rather than at session start — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)
- [x] Recommend a ranked set; split adoption into follow-up specs rather than expanding this one — moved to [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md)

### Phase 6: Integration test

- [x] Create `scripts/test-process-reaper.sh` (hermetic; private tmux server via `TMUX_TMPDIR`, scratch `BUS_SESSION`) — deferred on the user's decision of 2026-11-24 to [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), [MUX-235](./MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) and [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md)
- [x] Test: a deliberately orphaned harness process (parent killed) is reaped within one sweep — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) (`scripts/test-orphan-reaper.sh`)
- [x] Test: a lifecycle event is emitted naming pid, session and age — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] **Negative control:** a *live* harness process with a healthy parent survives the sweep — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] **Negative control:** a harness process belonging to a different, still-live scratch session is not reaped — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Test: a process whose `BUS_SESSION` tmux session was killed is reaped by the startup sweep — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Test: the opt-out env var fully disables reaping — deferred to [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)
- [x] Test: a scratch integration script that is interrupted mid-run leaves zero orphans — deferred on the user's decision of 2026-11-24 to [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), [MUX-235](./MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) and [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md)
- [x] Coverage floor keeps a skipped section from reporting green — deferred on the user's decision of 2026-11-24 to [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), [MUX-235](./MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) and [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md)
- [x] Run the script and verify all checks pass — deferred on the user's decision of 2026-11-24 to [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), [MUX-235](./MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) and [MUX-236](./MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md)

## Open decisions

### Decision 1 — should the harness self-exit when orphaned?

A process that notices its parent is gone and its bus dir is unreachable could exit on its own,
making the reaper a backstop rather than the primary mechanism. Cheaper and more robust across
sessions, but it puts the logic in the harness where it cannot help any *other* leaked process
type. **Recommendation: do both** — self-exit as primary, daemon reaper as the backstop that
catches processes already wedged.

### Decision 2 — may a daemon reap another session's processes?

*Answered 2026-11-15 in MUX-184 (its Decisions 1 and 2): the sweep runs at every session launch over
all dead sessions, and kills only what a pid record under a positively absent session's bus dir
still names. The text below is the question as filed.*

The startup sweep needs this to catch the 26 foreign orphans, but a wrong predicate could kill a
healthy peer session's agents — and multiple fleets do run on this machine. **Recommendation:**
reap only when the named `BUS_SESSION` has **no live tmux session**, which is decidable and safe.
Never reap based on age alone.

### Decision 3 — is idle eviction acceptable?

It is the largest available saving (three idle agents held ~456 MB doing nothing) but changes
interaction: a keypress on an evicted window pays a cold start, and conversation is lost unless
resume is wired in. Interacts with
[`MUX-139`](../completed/MUX-139-claude-agent-auto-resume.md). **Needs a user ruling before Phase 5 adopts it.**

## Decisions (user, 2026-11-24)

Relayed by edit. This spec is split and closed:

| Part | Where it went |
|------|---------------|
| The reaper (20 items, ticked 2026-11-15) | [MUX-184](./MUX-184-orphaned-session-processes-never-reaped.md), shipped |
| The integration scripts' own cleanup (8 items) | Deferred to MUX-233, MUX-235 and MUX-236, shipped, and `scripts/test-bus-dir-leak.sh` |
| Decision 1 — harness self-exit | **No.** The harness does not self-exit; MUX-184's reaper and the daemon stopping its harness on TERM/INT are the mechanism |
| Decision 3 — idle eviction | **Deferred.** Carried into MUX-242 as an option to evaluate, not to build |
| The footprint (Defect 2, Phases 4–5, 20 items) | [MUX-242](../backlog/MUX-242-session-memory-footprint-has-no-reclamation-path.md), parked |

What the script hand-off does and does not cover, checked by plan at close: MUX-233 and MUX-235 put
every hermetic script's tmux session on a private server and kill it by socket path, with
`scripts/measure-tmux-leak.sh` reporting any server left running; MUX-236 makes `test-all.sh` fail
on a file left in the checkout; MUX-184's launch sweep reaps what a dead scratch session leaves.
**No check asserts that a full suite run leaves zero orphaned harness processes** — the item
"verify a full test-suite run leaves zero orphaned processes" was handed off, not measured.

Phase 1's six open items close unanswered. One is moot: whether the harness can detect its
own orphaning, since it will not self-exit.

## Out of scope

- **Reducing the LLM CLIs' own memory use.** OpenCode and Claude Code are third-party; this spec
  can change *how many run, how long, and with what context*, not their internals.
- **The MUX-137 test bus-dir leak** — same family, already tracked separately.
- **Machine-level memory pressure.** The host measured 17 GB used with 84 MB unused, but browsers
  accounted for ~1.4 GB of that. muxcode's share is what this spec addresses.

## Status

**Complete — closed 2026-11-24** at 49/55, on the user's decision relayed by edit: 48 boxes are hand-offs to other specs (20 to MUX-184, 8 to MUX-233/235/236, 20 to MUX-242) and one is Phase 1's root-cause finding; six Phase 1 items are open. Nothing was implemented under this spec.

**Previously:** **Backlog** — 21/55, of which 20 are reaper items ticked as deferred to MUX-184 on 2026-11-15 (the
user's decision) and one is Phase 1's root-cause finding. Still parked: Decision 1 (harness
self-exit) and Decision 3 (idle eviction) are open, and Phase 1 holds open measurements.
