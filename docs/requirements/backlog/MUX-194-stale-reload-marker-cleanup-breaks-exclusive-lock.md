# MUX-194: Stale Reload-Marker Cleanup Can Delete a Live Exclusive Lock

**Tracking:** [mkober/muxcode#146](https://github.com/mkober/muxcode/issues/146)

[MUX-126](../completed/MUX-126-edit-resume-aware-auto-restart.md) made the reload marker an **exclusive
lock** (`c047b7e`): every relauncher now takes it with `O_EXCL` before typing into a pane. Two older
paths still treat the marker as a plain flag file: the daemon deletes any marker older than 60 s, and
`diagnose` tells the operator to `rm` any marker it sees. Neither asks whether the holder is still
running. (The daemon half was closed by the lease in `98edfac` — see
[Decisions](#decisions-user-2026-11-24); the `diagnose` half stands.) A lock that anyone may delete by age is not a lock — and once it has been deleted, the holder's
own `release()` removes whoever took it next.

## Context

### Source and standard of evidence

Filed 2026-09-28 on the user's instruction relayed by edit, from PR #95's review of the MUX-126 lock
(brief `/tmp/mux194-stale-marker.md`). Every fact under **Mechanism** was read from code at `c047b7e`.
**Not observed firing**: this is a latent race, filed before it has cost a run — see **Likelihood**.

### Mechanism — verified

| Fact | Where |
|------|-------|
| `acquireReloadMarker` creates the marker with `O_CREATE\|O_EXCL`, writes the literal `reloading`, and returns a `release` that is `os.Remove` of the path — no ownership check | `bus/reload.go:44–66`, `clearReloadMarker` `:68` |
| Its holders: `ResumeAgent` (`muxcode resume`), `RestartLocalAgent` (daemon health restart and the Ollama restart road), `ReloadAgent` (CLI and daemon watchdogs) | `bus/resume.go:58`, `bus/health.go:305`, `bus/reload.go:383` |
| `IsReloadMarkerStale` is `mtime > 60s`; `CleanStaleReloadMarkers` removes every such marker | `bus/reload.go:221`, `:231` |
| The daemon runs that cleanup from `checkCleanup`, at most every 300 s | `daemon/daemon.go:3772–3784` |
| `diagnose` reports **any** present marker as critical `reload-marker-stuck` and remediates with `rm <marker>` — a fresh marker held by a running resume included | `bus/diagnose.go:889–910` |
| `writeReloadMarker` (the non-exclusive write) has **no production caller**; it survives only as a foreign-holder fixture in four test files | `bus/reload.go:27`; `reload_test.go` ×5, `resume_test.go` ×2, `restart_resume_test.go`, `agent_health_test.go` |

### The failure

1. A holder takes the marker and is still working 60 s later.
2. A daemon cleanup pass (or an operator following `diagnose`) deletes the marker by age.
3. A second relauncher — a daemon health restart, another `muxcode resume`, a reload — acquires it and
   drives the **same pane** as the first: the double-typing race `c047b7e` exists to close.
4. The first holder finishes and its `release()` removes the **second** holder's marker, so the health
   sweep resumes on a pane still mid-relaunch.

### Likelihood

The normal holds are well under the window: `muxcode resume --force` runs `GracefulStop` (≈2 s, then up
to 10 s of polling) plus a scrape and relaunch; `ReloadAgent` adds up to 15 s of launch polling and 1 s
of grace — ≈30 s in all. It takes a holder stalled past 60 s (a hung stop, a slow tmux, a paused
machine) **and** a 300 s cleanup tick landing inside that stall. Rare, but the consequence is the exact
failure the lock exists to prevent, and it is silent.

### Why age alone cannot be fixed by raising the threshold

The cleanup exists for a real reason — a holder that crashed without releasing would otherwise exclude
its role from health monitoring forever. But **process liveness alone is not the answer either**: two
of the three holders can run inside the long-lived daemon, whose pid stays alive after a holder
goroutine has died without releasing. The spec needs a holder identity *and* a bound for the in-daemon
case — settled as the heartbeat, see [Decisions](#decisions-user-2026-11-24).

### Family

The restart/resume family in the defects table — [MUX-008](../completed/MUX-008-unverified-daemon-auto-restart.md),
[MUX-126](../completed/MUX-126-edit-resume-aware-auto-restart.md),
[MUX-136](../completed/MUX-136-bare-resume-loses-agent-definition.md): *restart and resume restore an
agent incompletely*. This one is the lock those paths share.

## Decisions (user, 2026-11-24)

Relayed by edit. **Decision 1 is settled by what shipped: option (a), the heartbeat.** `98edfac`
(MUX-139) made the marker a lease after this spec was filed, and the spec is restated to what is left.

What shipped, read in the tree on 2026-11-24 (`bus/reload.go`):

| Shipped | Where |
|---------|-------|
| The marker's content is an owner token, `reloading <pid>-<acquisition nanotime>`, written at the `O_EXCL` create | `acquireReloadMarker` `:58` |
| A held marker's mtime is refreshed every 20 s (`reloadMarkerRenewEvery`) for as long as it is held, and only while it still carries the holder's token | `renewReloadMarker` `:97` |
| `release` removes the marker only while it still carries the holder's token | `ownsReloadMarker` `:114` |
| So the unchanged 60 s age rule (`IsReloadMarkerStale` `:273`) now reclaims only a holder that stopped renewing — a dead process, or a legacy bare `reloading` marker nobody renews | — |

What is left, and is this spec's whole scope:

1. `diagnose` reads a freshly renewed marker as **relaunch in progress** — not critical, no `rm` —
   and keeps `reload-marker-stuck` for a marker past the stale threshold.
2. `writeReloadMarker` moves into a `_test.go` file.
3. Every stale removal writes a lifecycle row naming the role.

The pid-and-start-time liveness probe and the per-holder ceiling (options b and c) are dropped.

## Requirements

### Acceptance criteria

- [x] The marker records its holder with a per-acquisition token written at acquisition — shipped in `98edfac` (MUX-139), `acquireReloadMarker`; the token is pid plus acquisition time, with no separate process start time, which the heartbeat makes unnecessary
- [x] `release()` removes the marker only if it still carries the holder's own token — shipped in `98edfac`, `ownsReloadMarker`
- [x] A marker held past 60 s by a live holder is not removed by stale cleanup — shipped in `98edfac`, `renewReloadMarker` keeps its mtime inside the threshold
- [ ] The three shipped behaviours above are pinned by unit tests, each with its control: a foreign-token marker survives the first holder's release and the owner's release removes its own; a renewed marker older than 60 s by creation is not stale and an unrenewed one is; a legacy bare `reloading` marker is cleaned by age — existing tests are named here, missing ones added
- [ ] `diagnose` on a marker inside the stale threshold reports "relaunch in progress": not critical, no `rm` in the remediation, `diagnose` exit code unaffected by it
- [ ] `diagnose` on a marker past the threshold still reports critical `reload-marker-stuck` — **negative control**
- [ ] `diagnose` and the daemon's cleanup share one predicate (`IsReloadMarkerStale`), so they cannot disagree about whether a lock is held
- [ ] Every stale-marker removal is a lifecycle row naming the role and the marker's age and content
- [ ] `writeReloadMarker` lives in a `_test.go` file; no production code writes the marker non-exclusively
- [ ] `bash scripts/test-reload-marker-lock.sh` passes

### Technical approach

1. **`checkReloadMarkerStuck`** (`bus/diagnose.go:933`) today reports critical for any present marker
   and tells the operator to `rm` it — a lock held by a running resume included. Split on
   `IsReloadMarkerStale`: fresh → an informational finding, "relaunch in progress", whose remediation
   is to wait and re-run `diagnose`; stale → today's critical finding. Even the stale remediation
   should not be a bare `rm`: between the report and the keypress a new holder may have acquired, so
   it names the daemon's own cleanup (which re-checks) or tells the operator to re-check the age first.
2. **Lifecycle row.** `CleanStaleReloadMarkers` returns a count and `checkCleanup` logs one aggregate
   row (`reload-markers=<n>`). Log one row per removal — role, age, the marker's content (its token
   names the holder's pid) — from the removal itself, so every caller is covered.
3. **`writeReloadMarker`** has no production caller (checked 2026-11-24: defined in `bus/reload.go:28`,
   called only from `agent_health_test.go`, `graph_exec_test.go`, `reload_test.go`,
   `restart_resume_test.go` and `resume_test.go`). Move it into a `_test.go` helper in the same
   package; the callers are unchanged.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/reload.go` | `acquireReloadMarker`, `renewReloadMarker`, `ownsReloadMarker`, `IsReloadMarkerStale`, `CleanStaleReloadMarkers`, `writeReloadMarker` |
| `tools/muxcode/daemon/daemon.go` | `checkCleanup` — the 300 s cleanup pass |
| `tools/muxcode/bus/diagnose.go` | `checkReloadMarkerStuck` (`:933`) |
| `scripts/test-reload-marker-lock.sh` | New integration script (Phase 3) |

## Implementation

### Phase 1: Pin what shipped

- [ ] Name the existing unit tests that cover the token-checked release and the renewal; add the missing ones, each green on today's tree
- [ ] Unit test: holder A acquires, the marker is replaced by a foreign token, A's `release()` leaves it — **control**: A's release removes its own marker
- [ ] Unit test: a held marker with `reloadMarkerRenewEvery` shortened is not stale after the threshold has passed since creation — **control**: an unrenewed marker of the same age is stale and is removed
- [ ] Unit test: a legacy bare `reloading` marker is cleaned by age

### Phase 2: Diagnose, lifecycle row, tidy-up

- [ ] `checkReloadMarkerStuck` on `IsReloadMarkerStale`: fresh marker → "relaunch in progress", not critical, no `rm`; stale marker → `reload-marker-stuck` with a remediation that re-checks before removing
- [ ] Update `TestCheckReloadMarkerStuck_Detected`/`_NotDetected` in the same change and add the fresh-marker case
- [ ] One lifecycle row per stale removal: role, age, marker content
- [ ] Move `writeReloadMarker` into a `_test.go` file
- [ ] `docs/agent-bus.md` `muxcode diagnose` section and `docs/architecture.md`: the marker is a renewed lease; what `diagnose` reports for a fresh and a stale one

### Phase 3: Integration test

- [ ] Create `scripts/test-reload-marker-lock.sh` (hermetic: scratch bus, a scratch daemon on a private tmux server with its cleanup interval shortened for the test)
- [ ] Test: a holder process kept alive past 60 s — the daemon's cleanup leaves its marker in place, a concurrent `muxcode resume` of the role is refused as held, and `diagnose` reports relaunch in progress, not critical
- [ ] Test (negative control): kill the holder — once the marker passes the threshold `diagnose` reports `reload-marker-stuck`, the next cleanup pass removes the marker, and the lifecycle row names the role
- [ ] Test: a replaced marker survives the original holder's release
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and record the pass/fail counts here

## Notes

**A limit the heartbeat does not close.** The renewal runs in its own goroutine, so it proves the
holder's *process* is alive and has not released — not that the relaunch is making progress. A holder
hung inside a live daemon keeps renewing, and its role stays excluded from health monitoring until the
daemon restarts. Not observed; recorded so the "relaunch in progress" finding is not read as proof of
progress. A marker `diagnose` sees fresh on two runs far apart is the visible symptom, and the finding
should print the marker's creation age for that reason.

## Out of scope

- Changing which operations take the lock (MUX-126's decision).
- The 300 s cleanup cadence itself.

## Status

Backlog — filed 2026-09-28 on the user's instruction relayed by edit, from PR #95's MUX-126 lock
change (`c047b7e`). Mechanism verified by plan against `c047b7e`; latent, not observed firing. Not
started.

Unparked 2026-11-24 on the user's decision relayed by edit: the heartbeat (option a) had already
shipped in `98edfac` (MUX-139), so the spec is restated to the leftover — `diagnose`, the lifecycle
row and the `writeReloadMarker` move. Three criteria are ticked as shipped, each naming the code.
