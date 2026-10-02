# MUX-194: Stale Reload-Marker Cleanup Can Delete a Live Exclusive Lock

**Tracking:** [mkober/muxcode#146](https://github.com/mkober/muxcode/issues/146)

[MUX-126](../completed/MUX-126-edit-resume-aware-auto-restart.md) made the reload marker an **exclusive
lock** (`c047b7e`): every relauncher now takes it with `O_EXCL` before typing into a pane. Two older
paths still treat the marker as a plain flag file: the daemon deletes any marker older than 60 s, and
`diagnose` tells the operator to `rm` any marker it sees. Neither asks whether the holder is still
running. A lock that anyone may delete by age is not a lock — and once it has been deleted, the holder's
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
case (Decision 1).

### Family

The restart/resume family in the defects table — [MUX-008](./MUX-008-unverified-daemon-auto-restart.md),
[MUX-126](../completed/MUX-126-edit-resume-aware-auto-restart.md),
[MUX-136](../completed/MUX-136-bare-resume-loses-agent-definition.md): *restart and resume restore an
agent incompletely*. This one is the lock those paths share.

## Requirements

### Acceptance criteria

- [ ] The marker records its holder — at least pid, process start time and a per-acquisition token — written atomically at acquisition
- [ ] `release()` removes the marker **only if** it still carries the holder's own token; a holder whose marker was replaced leaves the new holder's marker in place — **negative control:** the owning holder's release does remove it
- [ ] Stale cleanup removes a marker only when its holder is provably gone (Decision 1); a marker held past 60 s by a live holder is **not** removed — **negative control:** a marker whose holder process is dead **is** removed on the next pass
- [ ] A marker in the legacy format (bare `reloading`, no holder) is still cleaned by age, so upgrading mid-reload cannot wedge a role
- [ ] `diagnose` uses the same holder predicate: a live holder is reported as "relaunch in progress" (not critical, no `rm`); a dead holder's marker is `reload-marker-stuck` with a remediation that does not race a live holder
- [ ] Every stale-marker removal is a lifecycle row naming the role and the holder it judged gone
- [ ] `writeReloadMarker` moves into a `_test.go` file; no production code writes the marker non-exclusively
- [ ] `bash scripts/test-reload-marker-lock.sh` passes

### Technical approach

1. **Holder identity.** `acquireReloadMarker` writes `pid`, `start` (process start time, so a reused pid
   is not mistaken for the holder) and a random `token` as the marker's content, via the existing
   `O_EXCL` create. `release` re-reads the marker and removes it only on a token match.
2. **Liveness.** `IsReloadMarkerStale` parses the holder: a marker whose `pid`+`start` no longer names a
   live process is stale at once; a live holder is stale only under Decision 1's in-daemon bound. An
   unparseable or legacy marker falls back to today's 60 s age rule.
3. **One predicate, three readers.** `CleanStaleReloadMarkers`, `checkReloadMarkerStuck` and any future
   reader share it, so `diagnose` and the daemon cannot disagree about whether a lock is held.
4. **Tidy-up.** Move `writeReloadMarker` to a test helper; the four test files that use it as a
   foreign-holder fixture switch to writing a marker with a dead or foreign holder, so they exercise the
   new predicate rather than the legacy fallback.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/reload.go` | `acquireReloadMarker` (`:44`), `clearReloadMarker` (`:68`), `IsReloadMarkerStale` (`:221`), `CleanStaleReloadMarkers` (`:231`), `writeReloadMarker` (`:27`) |
| `tools/muxcode/daemon/daemon.go` | `checkCleanup` (`:3772`) — the 300 s cleanup pass |
| `tools/muxcode/bus/diagnose.go` | `checkReloadMarkerStuck` (`:889`) |
| `tools/muxcode/bus/resume.go`, `bus/health.go`, `bus/reload.go:383` | The three holders |
| `scripts/test-reload-marker-lock.sh` | New integration script (Phase 3) |

## Implementation

### Phase 1: Pin

- [ ] Unit test: a marker acquired by a live holder and aged past 60 s is removed by `CleanStaleReloadMarkers` (asserts today's behaviour; Phase 2 inverts it)
- [ ] Unit test: holder A acquires, the marker is deleted and holder B acquires, then A's `release()` removes B's marker (asserts today's behaviour; Phase 2 inverts it)
- [ ] Unit test: a marker with no live holder is removed (negative control; stays green)

### Phase 2: Holder-aware lock

- [ ] Write `pid`, `start` and `token` into the marker at acquisition
- [ ] Token-checked `release`
- [ ] Holder-aware `IsReloadMarkerStale`, with the Decision 1 bound for in-daemon holders and the legacy-format age fallback
- [ ] `checkReloadMarkerStuck` on the shared predicate: live holder → not critical and no `rm`; dead holder → `reload-marker-stuck`
- [ ] Lifecycle row on every stale removal (role, judged-gone holder)
- [ ] Move `writeReloadMarker` into a `_test.go` file and update its four test-file callers
- [ ] Invert the two Phase 1 pins; the negative control stays green

### Phase 3: Integration test

- [ ] Create `scripts/test-reload-marker-lock.sh` (hermetic: scratch bus + real scratch daemon with its cleanup interval shortened for the test)
- [ ] Test: a holder process kept alive past 60 s — the daemon's cleanup leaves its marker in place, and a concurrent `muxcode resume` of the role is refused as held
- [ ] Test (negative control): kill the holder — the next cleanup pass removes the marker and logs the removal
- [ ] Test: a replaced marker survives the original holder's release
- [ ] Test: `diagnose` on a live-held marker is not critical; on a dead holder's marker it is `reload-marker-stuck`
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and record the pass/fail counts here

## Open decisions

### Decision 1 — how is a live in-daemon holder bounded?

A pid check cannot tell a daemon goroutine that is still relaunching from one that died without
releasing: the daemon's pid is alive either way. Options: (a) **heartbeat** — long holders refresh the
marker's mtime (or a `beat` field) while holding, and a live-pid marker is stale only when its heartbeat
is old; (b) **per-holder ceiling** — a live-pid marker is stale past a generous fixed bound (e.g. 10 min)
well above any legitimate hold; (c) record the daemon's own start time and treat an in-daemon marker as
stale after a daemon restart only. Recommendation: (b) — no background writer to get wrong, and the only
cost of the ceiling is a role excluded from monitoring for a few extra minutes after an in-daemon crash.

## Out of scope

- Changing which operations take the lock (MUX-126's decision).
- The 300 s cleanup cadence itself.

## Status

Backlog — filed 2026-09-28 on the user's instruction relayed by edit, from PR #95's MUX-126 lock
change (`c047b7e`). Mechanism verified by plan against `c047b7e`; latent, not observed firing. Not
started.
