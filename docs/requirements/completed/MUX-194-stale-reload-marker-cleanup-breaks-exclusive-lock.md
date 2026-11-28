# MUX-194: Stale Reload-Marker Cleanup Can Delete a Live Exclusive Lock

**Tracking:** [mkober/muxcode#146](https://github.com/mkober/muxcode/issues/146)

[MUX-126](./MUX-126-edit-resume-aware-auto-restart.md) made the reload marker an **exclusive
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

The restart/resume family in the defects table — [MUX-008](./MUX-008-unverified-daemon-auto-restart.md),
[MUX-126](./MUX-126-edit-resume-aware-auto-restart.md),
[MUX-136](./MUX-136-bare-resume-loses-agent-definition.md): *restart and resume restore an
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
- [x] The three shipped behaviours above are pinned by unit tests, each with its control: a foreign-token marker survives the first holder's release and the owner's release removes its own; a renewed marker older than 60 s by creation is not stale and an unrenewed one is; a legacy bare `reloading` marker is cleaned by age — existing tests are named here, missing ones added — all three already pinned, none added; see [Findings for Phase 1](#findings-for-phase-1)
- [x] `diagnose` on a marker inside the stale threshold reports "relaunch in progress": not critical, no `rm` in the remediation, `diagnose` exit code unaffected by it — observed by the Phase 3 script: exit 0 with a marker held 65 s
- [x] `diagnose` on a marker past the threshold still reports critical `reload-marker-stuck` — **negative control** — `TestCheckReloadMarkerStuck_Detected`
- [x] `diagnose` and the daemon's cleanup share one predicate (`IsReloadMarkerStale`), so they cannot disagree about whether a lock is held — both read `ReadReloadMarker`'s `Stale`; `TestReadReloadMarker_SharesTheCleanupPredicate`
- [x] Every stale-marker removal is a lifecycle row naming the role and the marker's age and content — `reload-marker-reclaimed`, written only for a removal that succeeded; the age is since the last renewal
- [x] `writeReloadMarker` lives in a `_test.go` file; no production code writes the marker non-exclusively — `bus/reload_test.go`
- [x] `bash scripts/test-reload-marker-lock.sh` passes — 23/0, floor 23

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

- [x] Name the existing unit tests that cover the token-checked release and the renewal; add the missing ones, each green on today's tree — named in [Findings for Phase 1](#findings-for-phase-1); none was missing
- [x] Unit test: holder A acquires, the marker is replaced by a foreign token, A's `release()` leaves it — **control**: A's release removes its own marker — `TestAcquireReloadMarker_ReleaseSparesNewOwner`
- [x] Unit test: a held marker with `reloadMarkerRenewEvery` shortened is not stale after the threshold has passed since creation — **control**: an unrenewed marker of the same age is stale and is removed — `TestAcquireReloadMarker_RenewedWhileHeld`
- [x] Unit test: a legacy bare `reloading` marker is cleaned by age — `TestCleanStaleReloadMarkers`

### Phase 2: Diagnose, lifecycle row, tidy-up

- [x] `checkReloadMarkerStuck` on `IsReloadMarkerStale`: fresh marker → "relaunch in progress", not critical, no `rm`; stale marker → `reload-marker-stuck` with a remediation that re-checks before removing
- [x] Update `TestCheckReloadMarkerStuck_Detected`/`_NotDetected` in the same change and add the fresh-marker case — `_FreshIsRelaunchInProgress` added
- [x] One lifecycle row per stale removal: role, age, marker content
- [x] Move `writeReloadMarker` into a `_test.go` file
- [x] `docs/agent-bus.md` `muxcode diagnose` section and `docs/architecture.md`: the marker is a renewed lease; what `diagnose` reports for a fresh and a stale one — `agent-bus.md` has no `muxcode diagnose` section, so the lease and both verdicts sit under `muxcode reload`, where the marker is described; `architecture.md` under Agent diagnostics

### Phase 3: Integration test

- [x] Create `scripts/test-reload-marker-lock.sh` (hermetic: scratch bus, a scratch daemon on a private tmux server with its cleanup interval shortened for the test)
- [x] Test: a holder process kept alive past 60 s — the daemon's cleanup leaves its marker in place, a concurrent `muxcode resume` of the role is refused as held, and `diagnose` reports relaunch in progress, not critical
- [x] Test (negative control): kill the holder — once the marker passes the threshold `diagnose` reports `reload-marker-stuck`, the next cleanup pass removes the marker, and the lifecycle row names the role
- [x] Test: a replaced marker survives the original holder's release
- [x] Coverage floor so a skipped section cannot report green
- [x] Run the script and record the pass/fail counts here

**23 passed, 0 failed**, floor 23 — run agent, 2026-11-28 00:10 (script last edited 00:08), log
`/tmp/test-reload-marker-lock.log`. About 3 min: two real 60 s waits.

| Section | Checks | What it showed |
|---------|--------|----------------|
| Setup | 2 | muxcode built from the tree; the cleanup daemon and holder built from a copy |
| A — a live holder, 65 s | 11 | The marker carries the holder's token and is still in place after about 20 cleanup passes, with no `reload-marker-reclaimed` row; a concurrent `muxcode resume plan` is refused as held and leaves the marker; `diagnose` reports `relaunch-in-progress`, info, no `reload-marker-stuck`, no `rm`, the hold time, and **exits 0** |
| B — negative control, holder SIGKILLed | 5 | The marker remains; 51 s after the kill `diagnose` reports `reload-marker-stuck`, critical, exit 1; the restarted daemon's cleanup removes it and the row names plan, its age and the dead holder's token |
| C — token-checked release | 5 | A marker replaced under its holder survives the holder's release; control — a holder's own release removes its marker |

How it is built, and what that leaves:

- Every CLI call (`diagnose`, `resume`) is the tree's muxcode. The **daemon** is a scratch copy
  changed in one line — `checkCleanup`'s 300 s cadence set to 3 s — because the cadence has no
  setting; the 300 s cadence itself is not exercised.
- The holder is a small program in that copy calling the real `acquireReloadMarker` through a
  test-only export, so acquire, renewal and release are production code. No real relaunch holds the
  marker for 65 s in the script.
- In B the daemon is stopped while the marker ages, so `diagnose` reads the stale marker without
  racing a 3 s cleanup; the cleanup is then observed on restart.
- The worker's report counted section A as 12 checks; the log holds 11, and the total of 23 stands.

## Findings for Phase 1

Phase 1 changed no code: every behaviour was already pinned by a test in
`tools/muxcode/bus/reload_test.go`, so none was added.

| Behaviour | Test | Control in the same test |
|-----------|------|--------------------------|
| Token-checked release | `TestAcquireReloadMarker_ReleaseSparesNewOwner` (`:326`) — the first holder's marker is reclaimed and a second holder acquires; the first's `release()` leaves the second's marker | The second holder's own `release()` removes it |
| Renewal | `TestAcquireReloadMarker_RenewedWhileHeld` (`:287`) — renewal at 5 ms; a held marker backdated 2 min stops reading stale and the cleanup removes nothing | After release, an unheld marker backdated the same way is cleaned |
| Legacy marker cleaned by age | `TestCleanStaleReloadMarkers` (`:243`) — `writeReloadMarker` writes the bare `reloading`; backdated 2 min it is cleaned | A fresh marker beside it is kept; also `TestIsReloadMarkerStale_OldMarker`/`_FreshMarker` |

The control for the release test is the *second* holder removing its own marker, not holder A
removing A's — the same token check from the other side.

**Pin run** — `/tmp/mux194-pin.sh` on the run agent, 2026-11-27 23:47: **10 passed, 0 failed**. Six
tests green on the tree, and three mutants in a throwaway copy each fail the test named for them:

| Mutant | Fails |
|--------|-------|
| Release without the token check | `ReleaseSparesNewOwner` (`reload_test.go:343`) |
| Renewal removed | `RenewedWhileHeld` (`:304`) |
| Age rule never true | `TestCleanStaleReloadMarkers` (`:261`) |

A first run read 9/1: the age-rule mutant did not compile, so its test "still passed" by never
running. The mutant was fixed and the rerun is the result above.

## Findings for Phase 2

Built by the worker 2026-11-27; diff read by plan.

| File | Change |
|------|--------|
| `bus/reload.go` | `ReloadMarkerState` (`content`, `renewed_secs`, `held_secs` from the token's acquisition time, `-1` for a legacy marker, `stale`) and `ReadReloadMarker`; `Stale` is `IsReloadMarkerStale`, the cleanup's own predicate. `CleanStaleReloadMarkers` writes one `reload-marker-reclaimed` row per removal: `<role>: stale <n>s since its last renewal, content "<token>"`. `writeReloadMarker` removed |
| `bus/reload_test.go` | `writeReloadMarker` lives here; callers unchanged |
| `bus/diagnose.go` | `agent_state.reload_marker` in `--json`. `checkReloadMarkerStuck`: fresh → `relaunch-in-progress`, info, no `rm`, evidence prints token, last renewal and hold time; stale → `reload-marker-stuck`, critical, remediation names the daemon's cleanup or a re-run of `diagnose` before any `rm`. A marker gone between the two reads → no finding |

Tests: `TestCheckReloadMarkerStuck_Detected` (stale, critical, `rm` only after a re-check),
`_FreshIsRelaunchInProgress` (info, no `rm`, names the hold time), `_NotDetected` (plus the
vanished-marker case), `TestReadReloadMarker_SharesTheCleanupPredicate`, and
`TestCleanStaleReloadMarkers` now asserting exactly one row naming the role, its age and content.

**Review round, must-fix, fixed 23:59.** The first cut removed a stale marker unchecked and then
logged and counted it, so a removal that failed was reported as done. `CleanStaleReloadMarkers` now
judges each marker on one `ReadReloadMarker` reading (readable and `Stale`), and logs
`reload-marker-reclaimed` and counts **only a removal that succeeded**. A failed removal writes
`reload-marker-reclaim-failed` with the error and is not counted; a marker that vanished in between
writes nothing. New `TestCleanStaleReloadMarkers_CountsOnlyRemovals`: a renewed hold gets no row; a
stale marker in a read-only lock dir is kept, counted 0, with one `reclaim-failed` row naming the
role and `permission denied` and no `reclaimed` row; control — writable again, it is removed,
counted 1, one `reclaimed` row.

**Pin run** — `/tmp/mux194-p2-pin.sh` on the run agent, rerun 2026-11-28 00:00 after the fix:
**14 passed, 0 failed** — `go vet` clean, the bus tests, the daemon's cleanup tests,
`writeReloadMarker` absent from production code, and three mutants caught: dropping the fresh
branch fails `_FreshIsRelaunchInProgress` (`diagnose_test.go:649`), dropping the row fails
`TestCleanStaleReloadMarkers` (`reload_test.go:290`), and the unchecked removal fails
`_CountsOnlyRemovals` (`reload_test.go:335`). The run before the fix read 12/0.

**Released 2026-11-28 00:05.** The code steps were held while the last full `./test.sh` predated
the final edits. The suite then ran on the final tree — 00:02–00:03, both modules passed, no failing
test, `_CountsOnlyRemovals` among those run (last code edit 00:00) — and the re-review at 00:05 read
0 must-fix, 0 should-fix, 0 nits (static). Phase 2's three code steps and four criteria are ticked.

**The fresh-marker criterion was held one step longer.** Its "not critical, no `rm`" is pinned by
`_FreshIsRelaunchInProgress`; its "`diagnose` exit code unaffected" was only read from
`cmd/diagnose.go`. Phase 3's script ran `muxcode diagnose` against a marker held 65 s and read exit
0, and the criterion was ticked then.

**Limits as built:**

- "Exit code unaffected" by a fresh marker rests on reading `cmd/diagnose.go` (only a critical
  finding sets it); no test runs the command. Phase 3's script is where it is observed.
- The row's age is seconds since the last **renewal**, not since acquisition.
- The cleanup's own removal is still by age alone: it checks staleness, then removes without
  comparing a token, so a holder that acquires between the two is removed. The window is
  microseconds and was there before this spec; recorded, not fixed.
- The spec named a `muxcode diagnose` section in `docs/agent-bus.md`; there is none.

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

**Complete** — closed out 2026-11-28 by `55-defect-backlog-loop` run 1795811929 at 25/25.

### What shipped

| Part | Where |
|------|-------|
| The lease itself — owner token, 20 s renewal, token-checked release | `98edfac` (MUX-139), before this spec was worked; pinned here by existing tests, none added |
| `diagnose` reads the marker by the cleanup's own predicate: renewed within 60 s → `relaunch-in-progress`, info, no `rm`, hold time shown; past it → `reload-marker-stuck`, critical, remediation re-checks before any `rm` | `bus/diagnose.go`, `ReadReloadMarker` in `bus/reload.go` |
| One `reload-marker-reclaimed` row per stale removal that succeeded; `reload-marker-reclaim-failed` for one that did not | `CleanStaleReloadMarkers` |
| `writeReloadMarker` is test-only | `bus/reload_test.go` |
| `scripts/test-reload-marker-lock.sh`, 23/0, floor 23 | Phase 3 |
| Docs | `docs/agent-bus.md` (`muxcode reload`), `docs/architecture.md` (Agent diagnostics), `CLAUDE.md` |

### Left as recorded

- The cleanup removes by age without comparing a token: a holder acquiring in the instant between
  its staleness check and its removal loses the marker. Present before this spec; not fixed.
- A holder hung inside a live daemon keeps renewing and reads `relaunch-in-progress` indefinitely;
  the growing hold time is the only sign (see [Notes](#notes)).
- The integration script's daemon is a copy with a 3 s cleanup cadence and its holder a test
  program; the 300 s cadence and a real 65 s relaunch are not exercised.
- The lifecycle row's age is seconds since the last renewal, not since acquisition.

### How it got here

Filed 2026-09-28 on the user's instruction relayed by edit, from PR #95's MUX-126 lock change
(`c047b7e`); latent, never observed firing. Unparked 2026-11-24 on the user's decision relayed by
edit: the heartbeat had already shipped in `98edfac`, so the spec was restated to the leftover.
Activated 2026-11-27 at rank 2. Phase 1 changed no code; Phase 2 took one review must-fix (a failed
removal was logged as done); Phase 3 closed the last criterion by observing `diagnose` exit 0 under
a live hold. Full suite green and review clean on the final tree, 2026-11-28 00:14–00:15.
