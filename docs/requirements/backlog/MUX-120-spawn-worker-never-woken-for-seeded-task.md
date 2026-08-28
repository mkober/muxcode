# Spawned Workers Never Receive Their Seeded Task

A graph `spawn`/`map` node seeds the worker's inbox and launches its agent, but the worker sits idle
instead of starting. Observed live 2026-08-28: the `req-code-pr` `implement` worker idled **4.5
minutes** with its task already in its inbox, until `muxcode deliver --force` was run by hand.

The reported cause was *"nothing wakes the fresh agent"*. Measurement shows something more specific,
and the difference changes the fix: **a wake does exist, it is just fired on a fixed 2-second timer
with no readiness check — and spawn roles are structurally invisible to both daemon delivery loops,
so nothing ever retries.**

Tracking: _(no GitHub issue yet)_

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
      within the normal receipt-gap window
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

### Phase 2: Readiness-gated wake

- [ ] Extract or reuse `LaunchSession`'s prompt-ready detection
- [ ] Replace the fixed 2 s sleep with it
- [ ] Confirm the wake landed; retry on failure
- [ ] Replace the discarded error with a lifecycle event

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
