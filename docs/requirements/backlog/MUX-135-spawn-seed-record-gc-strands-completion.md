# Delivery-Record GC Permanently Strands a Long Spawn Iteration

A spawn node completes when its seed message shows `responded`. That signal lives in a delivery-status
record which `CleanExpiredDeliveries` deletes after **one hour** — with no exemption for a spawn still
running. Once the record is gone, `spawnHasResponded` reads its absence as **"has not responded"**, and
it will read that way forever. Any spawn iteration exceeding an hour becomes permanently uncompletable.

Tracking: _(no GitHub issue yet)_

## Context

### Observed 2026-09-01 — run `1788278591`

An `implement` iteration ran **83 minutes**. The worker replied at 13:26. The node never completed.

Verified against the tree, not inferred:

| Step | Code | Effect |
|---|---|---|
| 1 | `spawnHasResponded` (`bus/spawn.go`) — `ds, err := ReadDeliveryStatus(...); if err != nil { return false }` | a **missing** record is indistinguishable from **never responded** |
| 2 | `CleanExpiredDeliveries(d.session, 1*time.Hour)` (`daemon/daemon.go:3407`) | deletes records older than an hour, with no check for a live spawn |
| 3 | seed `1788278591-daemon-f81a5c8b` | **no status file on disk** — confirmed absent |

### Second occurrence 2026-09-01 16:05 — recurring, not a one-off

Verified independently against the delivery store, not taken from the report:

| | |
|---|---|
| Worker seed | `1788288792` — **14:53:12** |
| Reply | ~72 minutes later |
| Seed record on disk | **absent** |
| Oldest surviving record | `1788291401` — **15:36:41** |
| Node state | `implement` stranded `running` for 29+ minutes |
| Recovery | `graph cancel` + `retry --from update-spec` |

The seed predates the oldest surviving record by 43 minutes, so it was collected while its worker was
still live — exactly the mechanism above, now with a second independent instance and a measurable GC
boundary.

**Two occurrences in one afternoon on ordinary work.** The first was 83 minutes, this one 72. Both
crossed the one-hour line simply by being substantial pieces of work, which is the population this
defect selects for.

Note the recovery here was safe by luck of topology: `update-spec` sits **upstream** of `phase-gate` in
`req-code-pr`, so `retry --from update-spec` re-enters the gate rather than skipping it. Had the stranded
node been downstream of a satisfied gate, that recovery would have hit
[`MUX-132`](../completed/MUX-132-graph-retry-launders-gate-approval.md)'s stale-approval path — now
fixed, but worth noting that recovering from *this* defect routinely means retrying, which is exactly
the operation that one governed.

### Why it is permanent, not transient

Nothing rewrites the record. `MarkResponded` fires once, when the reply lands; if the record is deleted
afterwards, no later event recreates it. So the node's completion signal is not delayed — it is
**destroyed**, and every subsequent poll re-reads absence as a negative.

That distinguishes this from the usual delivery-gap shape, where a backstop eventually re-drives and
the work completes late. Here there is nothing to re-drive: the worker already replied.

### The recovery makes it worse

The operator's natural response — `spawn stop` to clear the stuck worker — is read as
**worker-killed → node failure**. So the intuitive fix converts a stalled node into a failed run, and
the failure looks like the worker's fault rather than a GC artifact.

### It compounds with the worker-reuse fix

[`MUX-131`](../completed/MUX-131-spawn-implement-output-never-ported.md) Phase 0 makes workers **persist
across iterations** rather than being rebuilt each pass. Longer-lived workers mean longer iterations,
which means **more** of them cross the one-hour line. The reuse fix is correct and should stay — but it
raises exposure to this defect, so the two want fixing together.

### Scope

Every `spawn` and `map` node in every template. The threshold is wall-clock, so it is hit by exactly
the work most worth not losing: the long, substantial iterations.

## Requirements

### Acceptance criteria

- [ ] A spawn iteration lasting longer than the delivery-retention window still completes when its
      worker replies
- [ ] A missing delivery record is never silently equivalent to "not responded" — absence and a
      negative answer are distinguishable at the call site
- [ ] The fix does not weaken delivery GC generally; records for finished spawns are still collected
- [ ] **Negative control:** a spawn whose worker genuinely never replies still fails to complete — the
      fix must not make every spawn look responded, which would be strictly worse than the bug
- [ ] **Negative control:** a spawn that replies *within* the window behaves exactly as it does today
- [ ] Recovering a stuck spawn does not present as a worker-caused node failure when the cause was a
      collected record

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/spawn.go` | `spawnHasResponded` — the read that treats absence as a negative |
| `tools/muxcode/daemon/daemon.go` | `:3407`, `CleanExpiredDeliveries(session, 1*time.Hour)` |
| `tools/muxcode/bus/delivery.go` | `CleanExpiredDeliveries`, `MarkResponded`, `ReadDeliveryStatus` |

## Implementation

### Phase 1: Pin the defect

- [ ] Characterization test: seed a spawn, mark it responded, delete the delivery record, assert
      `spawnHasResponded` returns false — green **before** the fix, so the change is visible
- [ ] Confirm no path recreates the record after `MarkResponded`, so the loss is genuinely permanent

### Phase 2: Choose and implement

- [ ] Decide between: **(a)** exempt seed records from expiry while the spawn entry is live, or
      **(b)** fall back to inbox-absence + reply-log when the record is missing. Record the choice
- [ ] (a) is narrower but needs the liveness check to be cheap and correct; (b) is more robust but
      introduces a second source of truth for "responded" — note which risk is preferred and why
- [ ] Implement, keeping GC effective for spawns that have finished

### Phase 3: Negative controls

- [ ] Never-replying spawn still does not complete
- [ ] Within-window spawn unchanged
- [ ] GC still collects records for finished spawns
- [ ] Confirm each control fails when its fix is reverted

### Phase 4: Integration test

- [ ] Extend a graph integration script: drive a spawn whose iteration outlives the retention window
      (compress the window via config rather than waiting an hour) and assert the node completes on the
      worker's reply
- [ ] Assert the never-replying case still fails, in the same run
- [ ] Coverage floor equal to the achievable maximum so a short-circuited run cannot report green
- [ ] Run it and record passed/failed/exit code here

## Status

Backlog
