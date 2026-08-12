package bus

import (
	"os"
	"path/filepath"
	"testing"
)

// The bug these pin: pressure was decided by the volume's percent-used, so a
// normal dev box at 90% full triggered cleanup every 60s forever, freed 0 B
// each time, and buried the lifecycle log in unactionable warnings.

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"", 0, false},
		{"1024", 1024, true},
		{"512K", 512 << 10, true},
		{"64M", 64 << 20, true},
		{"2G", 2 << 30, true},
		{"2g", 2 << 30, true},
		{" 2G ", 2 << 30, true},
		{"abc", 0, false},
		{"-5", 0, false},
		{"5X", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseByteSize(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseByteSize(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestByteSizeDefaults_MalformedFallsBackNotZero(t *testing.T) {
	// A zero floor would silently disable the signal, so malformed config must
	// fall back to the default rather than parse to 0.
	t.Setenv("MUXCODE_TMP_FREE_FLOOR", "not-a-size")
	if got := TmpFreeFloorBytes(); got != defaultTmpFreeFloor {
		t.Errorf("TmpFreeFloorBytes() = %d, want default %d", got, defaultTmpFreeFloor)
	}
	t.Setenv("MUXCODE_TMP_FOOTPRINT_LIMIT", "")
	if got := TmpFootprintLimitBytes(); got != defaultTmpFootprintLimit {
		t.Errorf("TmpFootprintLimitBytes() = %d, want default %d", got, defaultTmpFootprintLimit)
	}
}

func TestByteSizeOverrides(t *testing.T) {
	t.Setenv("MUXCODE_TMP_FREE_FLOOR", "512M")
	if got, want := TmpFreeFloorBytes(), int64(512<<20); got != want {
		t.Errorf("TmpFreeFloorBytes() = %d, want %d", got, want)
	}
	t.Setenv("MUXCODE_TMP_FOOTPRINT_LIMIT", "4G")
	if got, want := TmpFootprintLimitBytes(), int64(4<<30); got != want {
		t.Errorf("TmpFootprintLimitBytes() = %d, want %d", got, want)
	}
}

// withTmpOverride points muxcode's /tmp scanning at a temp dir.
func withTmpOverride(t *testing.T, dir string) {
	t.Helper()
	prev := busDirOverride
	busDirOverride = dir
	t.Cleanup(func() { busDirOverride = prev })
}

func TestMuxcodeTmpFootprint(t *testing.T) {
	dir := t.TempDir()
	withTmpOverride(t, dir)

	// Only muxcode-* dirs count toward the footprint.
	mux := filepath.Join(dir, "muxcode-bus-somesession")
	if err := os.MkdirAll(mux, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mux, "f"), make([]byte, 4096), 0644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "something-else")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "big"), make([]byte, 1<<20), 0644); err != nil {
		t.Fatal(err)
	}

	got := MuxcodeTmpFootprint()
	if got != 4096 {
		t.Errorf("MuxcodeTmpFootprint() = %d, want 4096 — only muxcode-* dirs should count", got)
	}
}

func TestTmpPressure_HealthyMachineIsSilent(t *testing.T) {
	dir := t.TempDir()
	withTmpOverride(t, dir)

	// A tiny footprint and a generous headroom floor: this is the everyday
	// case that used to alert forever because the volume read 90% used.
	t.Setenv("MUXCODE_TMP_FREE_FLOOR", "1")       // 1 byte — any real disk clears it
	t.Setenv("MUXCODE_TMP_FOOTPRINT_LIMIT", "1G") // footprint is ~0 here

	pressured, _, footprint := TmpPressure()
	if pressured {
		t.Errorf("TmpPressure() = true on a healthy machine (footprint %d); percent-used must not trigger pressure", footprint)
	}
}

func TestTmpPressure_LargeFootprintAlerts(t *testing.T) {
	dir := t.TempDir()
	withTmpOverride(t, dir)

	mux := filepath.Join(dir, "muxcode-bus-fat")
	if err := os.MkdirAll(mux, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mux, "blob"), make([]byte, 8192), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUXCODE_TMP_FREE_FLOOR", "1")         // headroom is fine
	t.Setenv("MUXCODE_TMP_FOOTPRINT_LIMIT", "4096") // footprint exceeds it

	pressured, _, footprint := TmpPressure()
	if !pressured {
		t.Errorf("TmpPressure() = false with footprint %d over a 4096 limit; a large muxcode footprint must alert", footprint)
	}
}

func TestTmpPressure_LowHeadroomAlerts(t *testing.T) {
	dir := t.TempDir()
	withTmpOverride(t, dir)

	// An absurdly high floor stands in for a genuinely full disk: whatever the
	// real free space is, it is below this.
	t.Setenv("MUXCODE_TMP_FREE_FLOOR", "9000000000000000000")
	t.Setenv("MUXCODE_TMP_FOOTPRINT_LIMIT", "1G")

	pressured, free, _ := TmpPressure()
	if !pressured {
		t.Errorf("TmpPressure() = false with free=%d below the floor; low headroom must alert", free)
	}
}

func TestCheckDiskPressure_DisabledByThreshold(t *testing.T) {
	t.Setenv("MUXCODE_TMP_CLEANUP_THRESHOLD", "0")
	res, err := CheckDiskPressure("test-disk-pressure-disabled")
	if err != nil {
		t.Fatalf("CheckDiskPressure() error: %v", err)
	}
	if res != nil {
		t.Error("CheckDiskPressure() returned a result with the threshold disabled; 0 must remain an off switch")
	}
}

func TestCheckDiskPressure_HealthyReturnsNil(t *testing.T) {
	dir := t.TempDir()
	withTmpOverride(t, dir)
	t.Setenv("MUXCODE_TMP_CLEANUP_THRESHOLD", "90")
	t.Setenv("MUXCODE_TMP_FREE_FLOOR", "1")
	t.Setenv("MUXCODE_TMP_FOOTPRINT_LIMIT", "1G")

	res, err := CheckDiskPressure("test-disk-pressure-healthy")
	if err != nil {
		t.Fatalf("CheckDiskPressure() error: %v", err)
	}
	if res != nil {
		t.Errorf("CheckDiskPressure() = %+v on a healthy machine, want nil (no cleanup, no log, no alert)", res)
	}
}

func TestLifecycleLogDir_HonorsOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", dir)
	if got := LifecycleLogDir(); got != dir {
		t.Errorf("LifecycleLogDir() = %q, want %q — without this override tests write into the user's real install", got, dir)
	}
}
