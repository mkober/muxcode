package daemon

import "github.com/mkober/muxcode/tools/muxcode/bus"

// unclearableCycles is how many consecutive pressure cycles that freed no byte
// make the footprint unclearable (MUX-160).
const unclearableCycles = 3

// The cleanup ladder and the footprint walk are the filesystem boundary of
// checkDiskPressure: the ladder deletes other sessions' artifacts on the
// machine running it, so tests replace both rather than ever calling them.
var (
	checkDiskPressureFn = bus.CheckDiskPressure
	tmpPressureFn       = bus.TmpPressure
)

// diskPressureFreed is every byte a pressure cycle reclaimed, the stale stage
// included. Before MUX-160 the stale stage reported only an item count, so
// `stale=12 … freed=0 B` sat on one row and the count alone read the cycle as
// effective.
func diskPressureFreed(r *bus.DiskPressureResult) int64 {
	var freed int64
	if r.StaleResult != nil {
		freed += r.StaleResult.BytesFreed
	}
	if r.ClaudeResult != nil {
		freed += r.ClaudeResult.BytesFreed
	}
	if r.ArtifactResult != nil {
		freed += r.ArtifactResult.BytesFreed
	}
	if r.GoCacheResult != nil {
		freed += r.GoCacheResult.BytesFreed
	}
	return freed
}

// diskPressureBackoff is the Decision 3 back-off: after unclearableCycles
// zero-byte cycles under pressure the ladder is held until the footprint moves
// or the ceiling passes, so CleanupStale is not run every 60 s against a
// footprint it cannot move. A cycle that frees bytes, or pressure lifting,
// clears it.
type diskPressureBackoff struct {
	zeroCycles int
	active     bool
	footprint  int64 // footprint at the last cycle that ran while backed off
	since      int64 // when that cycle ran
}

// record notes one cycle that ran the ladder and reports whether it made the
// footprint unclearable — true once per back-off episode, on the cycle that
// starts it.
func (b *diskPressureBackoff) record(freed, footprint, now int64) bool {
	if freed > 0 {
		*b = diskPressureBackoff{}
		return false
	}
	b.zeroCycles++
	b.footprint, b.since = footprint, now
	if b.active || b.zeroCycles < unclearableCycles {
		return false
	}
	b.active = true
	return true
}

// holds reports whether the ladder is skipped this cycle: backed off, the
// footprint has not moved, and the ceiling has not passed. A ceiling of 0
// disables the back-off.
func (b *diskPressureBackoff) holds(footprint, now, ceiling int64) bool {
	return b.active && ceiling > 0 && !footprintMoved(b.footprint, footprint) && now-b.since < ceiling
}

// footprintMoved reports whether the footprint changed by at least 1% (and at
// least 1 MiB). Live sessions' bus directories grow by kilobytes a minute, so
// "changed" cannot mean any byte, or the back-off would never hold.
func footprintMoved(was, now int64) bool {
	diff := now - was
	if diff < 0 {
		diff = -diff
	}
	tolerance := was / 100
	if tolerance < 1<<20 {
		tolerance = 1 << 20
	}
	return diff >= tolerance
}
