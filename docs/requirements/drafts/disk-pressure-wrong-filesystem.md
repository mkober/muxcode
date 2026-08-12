# Disk-Pressure Check Measures the Wrong Filesystem

The daemon's disk-pressure watchdog reports the **boot volume's** percent-used, not the space muxcode actually occupies or could ever free. On any normal dev machine sitting at 85–90% full it fires indefinitely, cleans nothing, and trains the user to ignore a disk alert that would matter if the bus directory were ever genuinely at risk.

## Context

### Observed failure (2026-08-11)

The daemon emitted:

```
/tmp disk pressure: 90% used (threshold: 90%)
```

On macOS `/tmp` is a symlink to `/private/tmp` on the main data volume, so the check is reporting the BOOT VOLUME, not `/tmp`:

```
/dev/disk3s5  460Gi  368Gi  49Gi  89%  /System/Volumes/Data
```

Actual `/tmp` contents totalled ~14MB, of which muxcode owned ~11MB — against 49Gi free on the volume. The alert fired twice in ~6 minutes; the second pass "cleaned 12 muxcode artifact(s)" and still freed **0 B**.

### Root cause

`TmpDiskUsage()` (`bus/cleanup.go`) runs `syscall.Statfs("/tmp")` and computes `(Blocks - Bfree) / Blocks` — the percent-used of whichever volume `/tmp` resolves to. Two problems:

1. **Wrong denominator** — on macOS (and any single-volume Linux box) that volume is the entire boot/data volume. Muxcode's few MB of artifacts are noise against it; cleanup can never move the number.
2. **Wrong signal** — percent-used of a 460Gi volume says nothing about whether the bus directory (`/tmp/muxcode-bus-{session}`) is at risk. 49Gi of headroom at "90% used" is not pressure. (Minor: the math also uses `Bfree` rather than `Bavail`, overstating usage relative to what a user process can actually allocate.)

`checkDiskPressure()` (`daemon/daemon.go`) then alerts on that number every 60s once over threshold, so the false alarm repeats for as long as the volume stays above it.

### Impact

- Perpetual `disk pressure` alerts on healthy machines — alarm fatigue for a warning that would matter in a genuine tmpfs/small-volume squeeze
- Progressive cleanup (stale muxcode artifacts, then old Claude Code session dirs) runs repeatedly for 0 B of effect

### Observed impact (measured 2026-08-12, live install before the fix)

- The check fired every 60s indefinitely, freeing 0 B each time (`/tmp=92% stale=0 claude=0 freed=0 B post=92%`).
- **813 of the last 1000 lifecycle entries (81%) were this one message.** Rotation caps the log at 1000 entries, so retained history had collapsed to ~13.7 hours and the spam was evicting the evidence needed to diagnose overnight incidents. Found while investigating an unrelated report of Claude Code sessions dropping overnight — the log could no longer answer the question.

After the fix, verified live: **0 disk-pressure entries** in the first ~5 minutes of the new daemon (previously ~5), with only real events recorded.

## Requirements

### Acceptance criteria

- [x] The check never alerts when muxcode's own footprint is trivial and the volume has ample absolute headroom — a 460Gi volume at 89% with 49Gi free is silent
- [x] The measured quantity relates to what cleanup can actually free (muxcode bus/artifact footprint) or to real risk (absolute free-bytes headroom), not the volume's percent-used
- [x] A genuine squeeze still alerts: small tmpfs `/tmp`, or free bytes below an absolute floor
- [x] An alert that fires and frees ~0 B does not immediately re-fire on the next 60s cycle (cooldown or state so the same non-actionable condition isn't re-alerted)
- [x] Existing `MUXCODE_TMP_CLEANUP_THRESHOLD`-style configuration keeps working or gets a documented migration — retained as the on/off switch, `0` still disables

### Suggested fix direction

Measure one (or both) of, instead of volume percent-used:

| Signal | Sketch |
|--------|--------|
| Muxcode footprint | `dirSize()` over `/tmp/muxcode-bus-*` + known artifact paths — alert when footprint exceeds a size threshold; cleanup can actually move this number |
| Free-bytes headroom | `Statfs` `Bavail * Bsize` — alert only when absolute free space drops below a floor (e.g. 1–5 Gi), which is a real risk signal on any volume size |

### Technical approach (Phase 1 decision, 2026-08-12)

Both suggested signals were adopted together, replacing volume percent-used as the trigger:

- **Free-bytes headroom** — `Statfs` `Bavail * Bsize` against an absolute floor, default 2 GiB, override `MUXCODE_TMP_FREE_FLOOR` (accepts K/M/G suffixes). This also fixes the `Bfree` vs `Bavail` nit flagged above: `Bfree` counts the reserved superuser margin that a normal process can never allocate.
- **Muxcode footprint** — total bytes under `/tmp/muxcode-*`, default limit 1 GiB, override `MUXCODE_TMP_FOOTPRINT_LIMIT`. This is the only quantity muxcode's own cleanup can actually move, so alerting on it is actionable.

Pressure is `lowHeadroom || bigFootprint`, in a new `TmpPressure()` returning `(pressured, freeBytes, footprintBytes)`. `TmpDiskUsage()` is retained and still reported for context, with a doc comment stating explicitly that it is NOT a pressure signal and why.

**Backward compatibility**: `MUXCODE_TMP_CLEANUP_THRESHOLD` is kept as the on/off switch — `0` still disables the check entirely — so existing configs keep working with no migration.

**Malformed size config** falls back to the default rather than parsing to `0`, because a zero floor would silently disable the signal.

**Re-alert suppression**: the daemon now writes the lifecycle `warn` only when the condition is actionable (something was actually cleaned) or newly alerted past the cooldown. The existing adaptive alert cooldown (600s effective / 3600s ineffective) is unchanged.

**Files changed:**

| File | Change |
|------|--------|
| `bus/cleanup.go` | `TmpFreeBytes()`, `MuxcodeTmpFootprint()`, `TmpPressure()` added; `CheckDiskPressure()` triggers on them; `DiskPressureResult` gained `FreeBytes` / `FootprintBytes` |
| `bus/config.go` | `TmpFreeFloorBytes()`, `TmpFootprintLimitBytes()`, `parseByteSize()` |
| `daemon/daemon.go` | `checkDiskPressure()` — new message format, lifecycle warn gated on actionable-or-newly-alerted |
| `bus/cleanup_test.go` | Two tests that triggered via a 1% threshold now trigger via footprint / headroom |
| `bus/disk_pressure_test.go` | New: healthy machine is silent, footprint alerts, low headroom alerts, size parsing, threshold=0 disables |

### Key files

| File | Role |
|------|------|
| `bus/cleanup.go` | `TmpDiskUsage()` (Statfs percent-used — the defect), `CheckDiskPressure()`, `dirSize()` |
| `daemon/daemon.go` | `checkDiskPressure()` — 60s poll, alert formatting |
| `bus/cleanup_test.go` | Existing disk-pressure tests to update alongside the new signal |

## Implementation

### Phase 1: Signal redesign

- [x] Decide the measured signal (footprint, headroom, or both) and thresholds; record the decision here — both adopted, see Technical approach
- [x] Implement the new measurement in `bus/cleanup.go`; keep `dirSize()`/cleanup stages intact
- [x] Re-alert suppression: don't re-fire the same non-actionable alert every 60s cycle
- [x] Unit tests: healthy-volume-high-percent is silent; low-headroom alerts; large muxcode footprint alerts; cleanup moves the measured number (`bus/disk_pressure_test.go`)

### Phase 2: Integration test

> **Outstanding.** `scripts/test-disk-pressure.sh` was not written. Verification was done live against the running session instead (see "Observed impact" above) — the scripted test remains outstanding.

- [ ] Create `scripts/test-disk-pressure.sh` with automated verification
- [ ] Test: on a healthy machine (high percent-used, large absolute headroom) → no alert fires
- [ ] Test: simulated footprint/headroom breach (env-injected threshold) → alert fires once, not every cycle
- [ ] Run the script and verify all checks pass

## Status

In Progress
