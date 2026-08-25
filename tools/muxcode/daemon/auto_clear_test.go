package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The guard matrix, quiet window, and marker idempotence are unit-tested in
// bus/clear_test.go; these tests cover the daemon wrapper: off-by-default,
// interval throttling, and the not-due fast path.

// useEmptyShellConfig points MUXCODE_CONFIG at an empty file so config-file
// fallbacks never read the developer's real config (a missing path would fall
// through to ~/.config/muxcode/config).
func useEmptyShellConfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	t.Setenv("MUXCODE_CONFIG", path)
}

func TestCheckAutoClear_OffByDefault(t *testing.T) {
	bus.SetBusDirBase(t.TempDir())
	defer bus.ResetBusDirBase()
	t.Setenv("MUXCODE_AUTO_CLEAR_ROLES", "")
	useEmptyShellConfig(t)

	session := testSession(t)
	d := New(session, 5, 8)
	d.checkAutoClear()
	if d.lastAutoClearCheck != 0 {
		t.Error("disabled feature must not consume the check interval")
	}
}

func TestCheckAutoClear_NotDueWritesNoMarker(t *testing.T) {
	bus.SetBusDirBase(t.TempDir())
	defer bus.ResetBusDirBase()
	t.Setenv("MUXCODE_AUTO_CLEAR_ROLES", "review")
	useEmptyShellConfig(t)

	session := testSession(t)
	d := New(session, 5, 8)
	// Enrolled but no completed work — the scan runs and does nothing.
	d.checkAutoClear()
	if d.lastAutoClearCheck == 0 {
		t.Error("enabled feature should stamp the check interval")
	}
	if bus.ReadAutoClearMarker(session, "review") != 0 {
		t.Error("no clear must fire without completed work")
	}
}

func TestCheckAutoClear_IntervalThrottled(t *testing.T) {
	bus.SetBusDirBase(t.TempDir())
	defer bus.ResetBusDirBase()
	t.Setenv("MUXCODE_AUTO_CLEAR_ROLES", "review")
	useEmptyShellConfig(t)

	session := testSession(t)
	d := New(session, 5, 8)
	d.lastAutoClearCheck = time.Now().Unix()
	stamp := d.lastAutoClearCheck
	d.checkAutoClear()
	if d.lastAutoClearCheck != stamp {
		t.Error("check within the throttle interval must be skipped")
	}
}
