package daemon

import "testing"

// The disk-pressure check runs every 60s. Without a cooldown it alerted on every
// one of those cycles, which is the noise this guards against — 813 of the last
// 1000 lifecycle entries were unactionable repeats, evicting the history needed
// to diagnose overnight incidents.
//
// These test shouldAlertDiskPressure directly rather than through
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
