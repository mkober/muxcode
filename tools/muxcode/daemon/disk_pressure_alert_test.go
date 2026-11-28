package daemon

import (
	"errors"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The disk-pressure check runs every 60s. Without a cooldown it alerted on every
// one of those cycles, which is the noise this guards against — 813 of the last
// 1000 lifecycle entries were unactionable repeats, evicting the history needed
// to diagnose overnight incidents.
//
// These tests exercise shouldAlertDiskPressure directly rather than through
// checkDiskPressure, because that path calls CheckDiskPressure -> CleanupStale,
// which deletes other muxcode sessions' /tmp artifacts on the machine running
// the suite. The decision is the part with the logic; the deletion is not
// something a test should ever perform.

func TestShouldAlertDiskPressure_FirstAlertAlwaysFires(t *testing.T) {
	// seen=false means no prior alert for this key. It must fire regardless of
	// the timestamps, or a fresh daemon would stay silent through a real
	// pressure event.
	if !shouldAlertDiskPressure(0, 0, false, false) {
		t.Error("first alert must fire when no prior alert is recorded")
	}
	if !shouldAlertDiskPressure(0, 0, false, true) {
		t.Error("first alert must fire even when cleanup was ineffective")
	}
}

func TestShouldAlertDiskPressure_SuppressedWithinCooldown(t *testing.T) {
	now := int64(1_000_000)

	// Effective: freed something, 600s window.
	if shouldAlertDiskPressure(now-1, now, true, false) {
		t.Error("alert 1s after the last one must be suppressed")
	}
	if shouldAlertDiskPressure(now-599, now, true, false) {
		t.Error("alert 599s after the last must still be suppressed (600s cooldown)")
	}

	// Ineffective: freed nothing, held back six times longer.
	if shouldAlertDiskPressure(now-3599, now, true, true) {
		t.Error("ineffective alert 3599s later must be suppressed (3600s cooldown)")
	}
	// An unactionable repeat must NOT slip through on the effective window.
	if shouldAlertDiskPressure(now-601, now, true, true) {
		t.Error("ineffective alert must use the 3600s cooldown, not 600s")
	}
}

func TestShouldAlertDiskPressure_FiresAfterCooldown(t *testing.T) {
	now := int64(1_000_000)

	if !shouldAlertDiskPressure(now-600, now, true, false) {
		t.Error("alert must fire exactly at the 600s boundary")
	}
	if !shouldAlertDiskPressure(now-601, now, true, false) {
		t.Error("alert must fire past the 600s cooldown")
	}
	if !shouldAlertDiskPressure(now-3600, now, true, true) {
		t.Error("ineffective alert must fire exactly at the 3600s boundary")
	}
}

// The whole point: repeated checks inside the window fire once, not every cycle.
// Simulates the daemon's real 60s poll over an hour of continuous pressure.
func TestShouldAlertDiskPressure_FiresOncePerWindowNotEveryCycle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ineffective bool
		wantAlerts  int // over 60 polls at 60s = 3600s elapsed
	}{
		// t=0 fires, then 600/1200/1800/2400/3000/3600 → 7 total.
		{"cleanup freed something", false, 7},
		// t=0 fires, next eligible at 3600 → 2 total.
		{"cleanup freed nothing", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lastTS int64
			seen := false
			alerts := 0

			for cycle := 0; cycle <= 60; cycle++ {
				now := int64(cycle) * 60
				if shouldAlertDiskPressure(lastTS, now, seen, tc.ineffective) {
					alerts++
					lastTS = now
					seen = true
				}
			}

			if alerts != tc.wantAlerts {
				t.Errorf("fired %d times over 61 polls, want %d — "+
					"a per-cycle alert is exactly the log spam this prevents",
					alerts, tc.wantAlerts)
			}
		})
	}
}

// Pins the constants themselves: silently shortening the ineffective cooldown
// would reintroduce the spam without failing any behavioural test above.
func TestDiskPressureCooldownConstants(t *testing.T) {
	if diskPressureCooldownEffective != 600 {
		t.Errorf("effective cooldown = %d, want 600", diskPressureCooldownEffective)
	}
	if diskPressureCooldownIneffective != 3600 {
		t.Errorf("ineffective cooldown = %d, want 3600", diskPressureCooldownIneffective)
	}
	if diskPressureCooldownIneffective <= diskPressureCooldownEffective {
		t.Error("an unactionable alert must be held back longer than an actionable one")
	}
}

// TestDiskPressureFreed_StaleCountWithoutBytesFreesNothing is MUX-160's
// characterization, flipped: on 2026-09-08 every cycle logged
// `stale=12 … freed=0 B`, and the stale count alone read each one effective,
// so the row was written every 60 s on the short cooldown. A cycle is now
// judged on bytes, the stale stage's included.
func TestDiskPressureFreed_StaleCountWithoutBytesFreesNothing(t *testing.T) {
	staleOnly := &bus.DiskPressureResult{StaleResult: &bus.CleanupResult{BusDirs: []string{"/x/muxcode-bus-gone"}}}
	if got := diskPressureFreed(staleOnly); got != 0 {
		t.Errorf("stale=1 with no bytes removed: freed = %d, want 0 (ineffective)", got)
	}
	staleBytes := &bus.DiskPressureResult{StaleResult: &bus.CleanupResult{BytesFreed: 4096}}
	if got := diskPressureFreed(staleBytes); got != 4096 {
		t.Errorf("stale bytes missing from freed: %d, want 4096", got)
	}
	all := &bus.DiskPressureResult{
		StaleResult:    &bus.CleanupResult{BytesFreed: 1},
		ClaudeResult:   &bus.ClaudeCleanupResult{BytesFreed: 2},
		ArtifactResult: &bus.ArtifactPurgeResult{BytesFreed: 4},
		GoCacheResult:  &bus.ArtifactPurgeResult{BytesFreed: 8},
	}
	if got := diskPressureFreed(all); got != 15 {
		t.Errorf("freed across every stage = %d, want 15", got)
	}
}

const testFootprint = int64(4 << 30)

// backedOff drives b through the unclearable cycles from now, returning the
// record results.
func backedOff(b *diskPressureBackoff, now int64) []bool {
	var got []bool
	for i := 0; i < unclearableCycles; i++ {
		got = append(got, b.record(0, testFootprint, now+int64(i)*60))
	}
	return got
}

func TestDiskPressureBackoff_ThirdZeroCycleIsUnclearableOnceThenHolds(t *testing.T) {
	var b diskPressureBackoff
	now := int64(10_000)
	if got := backedOff(&b, now); got[0] || got[1] || !got[2] {
		t.Fatalf("record over three zero cycles = %v, want [false false true]", got)
	}
	last := now + 120
	for s := int64(60); s < 1800; s += 60 {
		if !b.holds(testFootprint+4096, last+s, 1800) {
			t.Fatalf("not held %ds into the back-off with an unmoved footprint", s)
		}
	}
	if b.holds(testFootprint, last+1800, 1800) {
		t.Error("still held at the 30 min ceiling")
	}
	if b.record(0, testFootprint, last+1800) {
		t.Error("the ceiling re-check logged unclearable a second time")
	}
	if !b.holds(testFootprint, last+1860, 1800) {
		t.Error("a re-check that freed nothing did not restart the hold")
	}
}

func TestDiskPressureBackoff_FootprintMoveRunsTheLadder(t *testing.T) {
	var b diskPressureBackoff
	backedOff(&b, 0)
	if b.holds(testFootprint+testFootprint/100, 180, 1800) {
		t.Error("a 1% footprint growth was still held")
	}
	if b.holds(testFootprint-testFootprint/50, 180, 1800) {
		t.Error("a 2% footprint drop was still held")
	}
	if footprintMoved(testFootprint, testFootprint+64<<10) {
		t.Error("a live bus directory's 64 KiB of growth counted as a move")
	}
}

// Negative control: a cycle that frees bytes clears the counter and the hold.
func TestDiskPressureBackoff_FreedBytesReset(t *testing.T) {
	var b diskPressureBackoff
	b.record(0, testFootprint, 0)
	b.record(0, testFootprint, 60)
	b.record(1, testFootprint, 120)
	if b.record(0, testFootprint, 180) || b.record(0, testFootprint, 240) {
		t.Error("zero cycles before a freeing cycle still counted toward unclearable")
	}
	backedOff(&b, 300)
	b.record(4096, testFootprint, 600)
	if b.active || b.holds(testFootprint, 660, 1800) {
		t.Error("a freeing cycle left the back-off in place")
	}
}

// stubDiskPressure replaces checkDiskPressure's filesystem boundary: every
// ladder run returns a partial result with *freed bytes and a removal error,
// as CleanupStale does when a RemoveAll fails, and the footprint walk reports
// *footprint under pressure. It returns the ladder-run counter.
func stubDiskPressure(t *testing.T, freed, footprint *int64) *int {
	t.Helper()
	runs := 0
	origCheck, origPressure := checkDiskPressureFn, tmpPressureFn
	checkDiskPressureFn = func(string) (*bus.DiskPressureResult, error) {
		runs++
		return &bus.DiskPressureResult{
			FootprintBytes: *footprint,
			StaleResult:    &bus.CleanupResult{BusDirs: []string{"/x/muxcode-bus-a", "/x/muxcode-bus-b"}, BytesFreed: *freed},
		}, errors.New("cleanup errors: remove /x/muxcode-bus-a: permission denied")
	}
	tmpPressureFn = func() (bool, int64, int64) { return true, 0, *footprint }
	t.Cleanup(func() { checkDiskPressureFn, tmpPressureFn = origCheck, origPressure })
	return &runs
}

// A stage whose removal fails returns its partial result with the error, and
// checkDiskPressure must still account it: when it returned on the error, a
// persistent removal failure retried every 60 s and never read unclearable.
func TestCheckDiskPressure_RepeatedRemovalErrorsBecomeUnclearableThenHold(t *testing.T) {
	t.Setenv("MUXCODE_TMP_CLEANUP_THRESHOLD", "90")
	t.Setenv("MUXCODE_DISK_PRESSURE_BACKOFF", "1800")
	session := testSession(t)
	d := &Daemon{session: session, lastAlertKey: map[string]int64{}, inboxSizes: map[string]int64{}}
	freed, footprint := int64(0), testFootprint
	runs := stubDiskPressure(t, &freed, &footprint)
	cycle := func() { d.lastDiskPressureCheck = 0; d.checkDiskPressure() }

	for i := 0; i < unclearableCycles+2; i++ {
		cycle()
	}
	rows, _ := bus.FilterLifecycleLog(session, bus.LifecycleFilterOpts{Event: "disk-pressure-unclearable"})
	if len(rows) != 1 {
		t.Errorf("disk-pressure-unclearable rows = %d, want exactly 1", len(rows))
	}
	if *runs != unclearableCycles {
		t.Errorf("ladder ran %d times, want %d — held once unclearable", *runs, unclearableCycles)
	}

	// Negative control: the footprint moves, the ladder runs, and a partial
	// result that freed bytes despite its error clears the back-off.
	footprint, freed = testFootprint*2, 4096
	cycle()
	if *runs != unclearableCycles+1 || d.diskBackoff.active || d.diskBackoff.zeroCycles != 0 {
		t.Errorf("after a freeing partial cycle: runs %d, back-off %+v; want the ladder run and no back-off", *runs, d.diskBackoff)
	}
}

func TestDiskPressureBackoff_CeilingZeroDisablesTheHold(t *testing.T) {
	var b diskPressureBackoff
	backedOff(&b, 0)
	if b.holds(testFootprint, 180, 0) {
		t.Error("MUXCODE_DISK_PRESSURE_BACKOFF=0 still held the ladder")
	}
}
