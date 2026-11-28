package bus

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSized(t *testing.T, dir string, n int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, n), 0644); err != nil {
		t.Fatal(err)
	}
}

// The pressure ladder's first stage reports the bytes it removed, so a
// `stale=N` count can no longer stand in for freed space (MUX-160) — and the
// live session's bus directory is never among them, the dba6f93 pin on this
// path. Run under the scratch override, never the real /tmp.
func TestCleanupStaleReportsBytesFreedAndSparesALiveSession(t *testing.T) {
	dir := t.TempDir()
	withTmpOverride(t, dir)
	withAnsweredSessions(t, "live")
	gone, live := filepath.Join(dir, "muxcode-bus-gone"), filepath.Join(dir, "muxcode-bus-live")
	writeSized(t, gone, 4096)
	writeSized(t, live, 8192)

	dry, err := CleanupStale("other", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.BytesFreed != 0 || len(dry.BusDirs) != 1 || dry.BusDirs[0] != gone {
		t.Fatalf("dry run: bus dirs %v, bytes %d; want only %s and 0 bytes", dry.BusDirs, dry.BytesFreed, gone)
	}

	res, err := CleanupStale("other", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.BytesFreed != 4096 {
		t.Errorf("BytesFreed = %d, want the 4096 bytes of the stale bus dir", res.BytesFreed)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("stale bus dir survived: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(live, "f")); err != nil || len(data) != 8192 {
		t.Errorf("the live session's bus dir was touched: %v, %d bytes", err, len(data))
	}
}

func TestLargestFootprintEntriesNamesTheBiggestCountedPaths(t *testing.T) {
	dir := t.TempDir()
	withTmpOverride(t, dir)
	for name, n := range map[string]int{"muxcode-a": 1024, "muxcode-b": 8192, "muxcode-c": 4096, "muxcode-d": 2048, "other": 1 << 20} {
		writeSized(t, filepath.Join(dir, name), n)
	}

	top := LargestFootprintEntries(3)
	want := []FootprintEntry{
		{filepath.Join(dir, "muxcode-b"), 8192},
		{filepath.Join(dir, "muxcode-c"), 4096},
		{filepath.Join(dir, "muxcode-d"), 2048},
	}
	if len(top) != len(want) {
		t.Fatalf("top = %v, want %v", top, want)
	}
	for i := range want {
		if top[i] != want[i] {
			t.Errorf("top[%d] = %v, want %v", i, top[i], want[i])
		}
	}
	if got := MuxcodeTmpFootprint(); got != 1024+8192+4096+2048 {
		t.Errorf("footprint = %d, want the sum of every muxcode-* entry", got)
	}
}
