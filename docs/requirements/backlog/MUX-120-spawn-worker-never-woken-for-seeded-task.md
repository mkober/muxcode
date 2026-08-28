# Spawned Workers Never Receive Their Seeded Task

A graph `spawn`/`map` node seeds the worker's inbox and launches its agent, but the worker sits idle
instead of starting. Observed live 2026-08-28: the `req-code-pr` `implement` worker idled **4.5
minutes** with its task already in its inbox, until `muxcode deliver --force` was run by hand.

The reported cause was *"nothing wakes the fresh agent"*. Measurement showed something more
specific: a wake **did** exist, fired on a fixed 2-second timer with no readiness check, **and**
spawn roles are structurally invisible to both daemon delivery loops, so nothing retried.

**A fix for the first half landed while this spec was being written** (`1356694`, 2026-08-28 14:16).
It is a good fix. This spec is therefore **narrowed to the half that remains** — and that half is now
*more* urgent, not less, because the shipped fix explicitly leans on a fallback that does not exist.

Tracking: _(no GitHub issue yet)_

## What already landed, and the gap it leaves

`1356694` added a proper readiness-gated wake to `StartSpawn`:

```go
go func() {
    LogLifecycle(session, "info", "daemon", "spawn-wake", spawnRole)
    wakeAfterReload(session, spawnRole)
}()
```

`wakeAfterReload` (`bus/reload.go:424`) is the right mechanism, and it is careful: it polls for the
idle prompt up to 15 s at 500 ms intervals with a wide-capture fallback for a `❯` hidden behind
status-bar overlays, **always** sends the wake (on detection *or* timeout, letting the text buffer in
the PTY), calls `ClearNotifiedIDs` first so stale markers cannot suppress it, and clears pending
input before injecting. It also handles non-hook providers via `SendWakeUp`.

Two observations, one minor and one that keeps this spec open:

**Minor — the original 2 s notify was left in place alongside the new wake, and has since been
removed** (2026-08-28 16:31). For the record: `spawn.go:216-218` ran
`time.Sleep(2 * time.Second); Notify(...)` next to `wakeAfterReload` for about two hours. It was
*probably* harmless — `wakeAfterReload` calls `ClearNotifiedIDs` before rebuilding the notification,
so prematurely-set markers got reset — but it was a redundant early wake whose error was discarded,
and leaving it would have meant two wake paths where one is authoritative. `wakeAfterReload` is now
the sole, readiness-gated wake; verified by absence (`Sleep(2 *` and a bare
`Notify(session, spawnRole)` are both gone from `spawn.go`).

**The one that matters — the fix's stated fallback does not cover spawns.** Its own comment reads:

> *Callers that exit immediately (CLI spawn) may cut the goroutine short — the receipt-gap backstop
> covers them.*

**Measured: it does not.** `checkPollHealth` iterates `for _, role := range bus.KnownRoles`
(`daemon.go:~1851`), and `bus.KnownRoles` (`config.go:13`) is a static slice that never contains a
dynamic `spawn-<8hex>` role. The same is true of `checkInboxes` (`daemon.go:368`).

So the acknowledged failure mode — a short-lived caller cutting the goroutine before it wakes — lands
in exactly the same stranded state as the original defect, with no backstop. The fix is sound for the
daemon-driven path (long-lived process, goroutine survives); it is the **CLI spawn path** that still
has no net.

## Context

### The wake that exists, and why it misses

`StartSpawn()` (`bus/spawn.go`) seeds the inbox *before* the agent exists — correct, since the
message must be on disk when the agent first reads it — then launches the agent by `send-keys` and
schedules exactly one wake:

```go
// Async: wait 2s then notify spawn to read inbox
go func() {
    time.Sleep(2 * time.Second)
    _ = Notify(session, spawnRole)
}()
```

Three defects in five lines:

| Defect | Consequence |
|--------|-------------|
| Fixed 2 s delay, no readiness check | A Claude Code agent takes far longer than 2 s to reach its `❯` prompt. The notify lands on a pane still starting up and is swallowed |
| Fires exactly once | Nothing re-drives it when it misses |
| Error discarded (`_ =`) | A failed notify is silent — no log, no lifecycle event, no retry |

Contrast `LaunchSession()` (`bus/launcher.go` ~700-754), which does this properly for regular
agents: it polls pane content, distinguishes the trust prompt from the bypass prompt from the idle
prompt, wakes **only** once the agent is at `❯` ("past all startup prompts"), applies a 1 s
stabilization delay, guards with `NeedsWakeUp()` and a `woken[win]` map, **re-captures to confirm
the wake text landed**, retries with Enter, and logs a lifecycle event at each step.

The machinery for doing this correctly already exists. Spawns simply do not use it.

### The deeper cause: spawn roles are invisible to the daemon

This is the part that matters more than the timer, because it means the 2 s wake is a **single point
of failure with no recovery net**.

Both daemon delivery loops enumerate the *static* role list:

| Loop | Line | Iterates | Sees `spawn-<hex>`? |
|------|------|----------|---------------------|
| `checkInboxes` (5 s idle-delivery) | `daemon.go:368` | `for _, role := range bus.KnownRoles` | ❌ No |
| `checkPollHealth` (receipt-gap backstop, 45 s / 20 s listenerless) | `daemon.go:~1851` | `for _, role := range bus.KnownRoles` | ❌ No |

`bus.KnownRoles` is a fixed slice (`config.go:13`) that never contains dynamic `spawn-<8hex>` roles.
`IsSpawnRole()` exists and *is* used — but only to **exclude** spawns (`daemon.go:1600`, agent-health)
and inside `IsKnownRole()` (`config.go:631`). No daemon loop ever enumerates live spawns.

So the observed timeline is fully explained:

1. `StartSpawn` seeds the inbox and fires its 2 s notify → **missed**, agent not yet at prompt
2. `checkInboxes` would re-deliver every 5 s → **never runs for this role**
3. `checkPollHealth` would recover the receipt gap in 45 s → **never runs for this role**
4. `checkStalledTasks` eventually notices → matches the **4.5 min** observed

The receipt-gap backstop was built precisely to catch "message delivered, never received". It cannot
catch it here because it does not know the role exists.

### Why this is worth fixing beyond the one incident

Every graph `spawn` and `map` node depends on this path. A wide `map` fan-out launches many workers
at once, each racing the same 2 s timer on a machine that is now busier — so the failure gets *more*
likely exactly when the graph is doing the most work. A join barrier downstream then waits on
workers that never started.

## Third gap: completion is window-death, and it deletes the evidence

Observed live 2026-08-28: a graph `implement` node sat at `running` for **8 minutes after its worker
reported done**, and was only released by killing the window by hand.

`RefreshSpawnStatus` (`spawn.go:277`) decides completion by one signal — `CheckSpawnWindow` returning
false, i.e. **the tmux window is gone**. A Claude worker finishes its task and returns to its prompt;
it does not exit. The window lives, so the spawn stays `running` forever and any downstream join
waits on it indefinitely. Callers are automatic and frequent: `graph_exec.go:431` on every executor
tick for spawn/map nodes, and `daemon.go:734` in the poll loop.

**The part that changes sequencing — and corrects an earlier claim of mine.** On detecting
window-gone, the same function immediately calls `removeSpawnWorktree(e.Worktree)` (`spawn.go:300`).
Worktree deletion is therefore **automatic on completion detection**, not a manual-only action.

I previously told the edit agent that a spawn worktree "persists after the node completes — only
`spawn stop` / `spawn clean` destroy it." **That was wrong.** It came from grepping `graph_exec.go`
for `StopSpawn`/`CleanFinishedSpawns` and finding none, without reading what the status refresh
itself does. Searching for cleanup-shaped *names* instead of reading the cleanup *behaviour* is the
same form-over-substance error this repo keeps producing.

The consequence is a real ordering hazard:

> **The stall bug is currently the only thing preserving the harvest window.** Because workers never
> exit, their worktrees are never auto-removed, which is why MUX-115's Phase 1 work survived long
> enough to be harvested. Fix the stall — make workers exit on done — **without** first implementing
> harvest-before-cleanup, and the worktree is deleted on the very tick the worker exits. The harvest
> path that just worked would stop working, and the failure would be silent.

This ties directly to [MUX-014](../completed/MUX-014-graph-agent-orchestrator.md)'s known gap,
*"worktree harvest-before-cleanup contract"*, which closed with no implementation.

- [ ] **Sequencing requirement**: harvest-before-cleanup lands **before**, or with, any fix that
      makes workers exit on completion
- [ ] Completion signal moves off window-death — candidate: correlate the spawn task's reply, which
      is positive evidence the work finished, rather than inferring it from a dead window
- [ ] A worker that finishes without exiting still completes its node within a bounded time
- [ ] Worktree removal does not race the harvest — deletion happens only after the work is claimed
      or explicitly abandoned

## Open decisions

- [ ] **Fix at the source, the daemon, or both?** Source = readiness-gated wake in `StartSpawn`.
      Daemon = enumerate live spawn roles in the delivery loops. They fix different halves: the
      first stops the miss, the second provides the net when it misses anyway. **Recommendation:
      both** — the incident showed a single-point wake is what failed.
- [ ] **How should the daemon enumerate spawns?** Read the spawn registry (`ReadSpawnEntries`) each
      tick, or generalize `KnownRoles` to include live spawn roles. The former is narrower; the
      latter risks touching every loop that iterates `KnownRoles`.
- [ ] **Should the readiness gate be shared code?** `LaunchSession`'s prompt-detection is the proven
      implementation. Extracting it is the right structural answer but widens the change.
- [ ] **What is the acceptable worst-case start latency?** This sets whether the daemon net is
      sufficient on its own (45 s) or the source fix is required (near-immediate).

## Requirements

### Acceptance criteria

- [ ] A spawned worker begins its seeded task without manual intervention, in a **fresh** session
- [ ] Worst-case start latency is bounded and stated — no path where a worker waits on
      `checkStalledTasks`
- [ ] The wake is **readiness-gated**, not timer-gated: it fires when the agent is at its prompt
- [ ] A missed or failed wake is **retried**, not swallowed — the current `_ =` discard is removed
      and failures emit a lifecycle event
- [ ] Live spawn roles are visible to the daemon's delivery backstop, so a missed wake recovers
      within the normal receipt-gap window — **the specific claim `1356694` already relies on**
- [ ] A **CLI spawn** whose process exits before the wake goroutine completes still gets its worker
      started — this is the acknowledged hole the shipped fix leaves uncovered
- [ ] A **wide `map` fan-out** starts every worker — the many-workers-at-once case, which is when
      the race is worst
- [ ] Non-hook providers (OpenCode/Codex) are covered — they use `SendWakeUp`, not text injection
- [ ] Regular (non-spawn) agent startup is unchanged (negative control)
- [ ] `go vet ./...` and `go test ./...` green in both modules

### Technical approach

Two independent halves; each is testable alone.

**Source** — replace the fixed timer in `StartSpawn` with the readiness gate `LaunchSession` already
uses: wait for the agent to reach its prompt, wake, confirm the wake landed, retry on failure, log
the outcome. Prefer extracting the existing logic over writing a second implementation — a divergent
copy is how the two paths drifted in the first place.

**Net** — make live spawn roles visible to `checkInboxes` and `checkPollHealth`. The receipt store
already tracks per-message receipts for any role; the only gap is enumeration. This is the change
that would have capped the incident at ~45 s regardless of the timer.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/spawn.go` | `StartSpawn` — inbox seeding, agent launch, the 2 s notify |
| `tools/muxcode/bus/launcher.go` | `LaunchSession` prompt-ready wake — the proven implementation to reuse |
| `tools/muxcode/daemon/daemon.go` | `checkInboxes` (368), `checkPollHealth` (~1837), `checkStalledTasks` (2747) |
| `tools/muxcode/bus/config.go` | `KnownRoles` (13), `IsSpawnRole` (601), `IsKnownRole` (631) |
| `tools/muxcode/bus/delivery.go` | Receipt store the backstop reads |
| `tools/muxcode/bus/graph_exec.go` | `graphSpawnFn` — the graph-side caller |

## Implementation

### Phase 1: Reproduce and bound

- [ ] Reproduce the miss deterministically — a scratch spawn whose agent takes longer than 2 s
- [ ] Confirm from lifecycle logs that the notify fires and is lost, rather than never firing
- [ ] Record the current worst-case latency as the baseline the fix must beat

### Phase 2: Readiness-gated wake — **landed in `1356694`**

- [x] Extract or reuse `LaunchSession`'s prompt-ready detection — `wakeAfterReload` (`reload.go:424`)
- [x] Replace the fixed 2 s sleep with it — *added alongside; see cleanup step below*
- [x] Confirm the wake landed; retry on failure — always-send on detect-or-timeout, PTY buffering
- [x] Replace the discarded error with a lifecycle event — `spawn-wake` lifecycle row
- [x] **Cleanup**: remove the now-redundant 2 s `Notify` — removed 2026-08-28 16:31; `wakeAfterReload` remains as the sole, readiness-gated wake (verified: no `Sleep(2 *` or bare `Notify(session, spawnRole)` left in `spawn.go`)

### Phase 3: Daemon net

- [ ] Enumerate live spawn roles in `checkInboxes` and `checkPollHealth`
- [ ] Verify a spawn with a missed wake recovers within the receipt-gap window
- [ ] Confirm no regression for the static roles those loops already serve

### Phase 4: Integration test

- [ ] Create `scripts/test-spawn-wake.sh` (hermetic: scratch bus + tmux + daemon)
- [ ] **Headline test**: a spawned worker with a seeded task starts **without** `deliver --force`
- [ ] Test: an agent that is slow to reach its prompt is still woken (the 2 s race, forced)
- [ ] Test: with the source wake suppressed, the daemon net recovers it within the receipt window —
      proves the two halves are independent
- [ ] Test: a wide `map` fan-out starts **every** worker, not just the first
- [ ] **Negative control**: regular agent startup is unchanged
- [ ] **Negative control**: assert the pre-fix path actually fails, so the suite cannot pass
      vacuously against a worker that would have started anyway
- [ ] Coverage floor so a skipped run cannot report green
- [ ] Run the script and verify all checks pass

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Fixing only the timer | Restores a single point of failure with no recovery net — the incident's actual shape | Phase 3 is not optional |
| Fixing only the daemon net | Every worker pays up to a receipt-gap window of latency on every spawn | Phase 2 keeps the common case fast |
| A second copy of prompt-detection | Two implementations drift; that drift is this defect | Extract and share rather than duplicate |
| Enumerating spawns broadly | `KnownRoles` is iterated in many places; widening it has broad blast radius | Prefer registry-based enumeration in the two loops that need it |
| Vacuous integration pass | A worker that would have started anyway proves nothing | Negative control asserting the pre-fix failure |

## Status

Backlog
