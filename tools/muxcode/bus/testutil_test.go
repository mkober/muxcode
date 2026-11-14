package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useTempBusDir points BusDir() at a fresh t.TempDir() and returns it, so a
// test's bus — and, passed to Init, its memory dir — lives under a directory
// it owns rather than the real state directory, preventing:
//   - Collisions with live muxcode sessions
//   - Stale artifacts from panicked/failed tests
//   - Real tmux commands targeting test session names
//
// This is the only isolation BusDir honors: an environment variable such as
// BUS_DIR_BASE is read by nothing, and Init(session, "") writes memory into
// the working directory's .muxcode/memory — the package source tree (MUX-137).
// Cleanup is automatic via t.Cleanup — no manual defer needed.
func useTempBusDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetBusDirBase(dir)
	t.Cleanup(func() { ResetBusDirBase() })
	return dir
}

// assertIsolatedBus fails unless session's bus and the memory Init seeded sit
// under dir, the directory the test owns. A test that sets the dead
// BUS_DIR_BASE writes its bus outside dir, and one that passes Init an empty
// memory dir writes shared.md into the source tree — while its own Peek
// assertions still pass, since they resolve the same path as the write
// (MUX-137). Call it after Init(session, dir).
func assertIsolatedBus(t *testing.T, session, dir string) {
	t.Helper()
	if bus := BusDir(session); !strings.HasPrefix(bus, dir+string(os.PathSeparator)) {
		t.Errorf("bus %s is outside the test's directory %s", bus, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "shared.md")); err != nil {
		t.Errorf("Init seeded no memory under the test's directory: %v", err)
	}
}

// waitForProc polls until a process exits, with a 5s timeout.
// Much faster than fixed time.Sleep since most test processes exit in <10ms.
func waitForProc(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !CheckProcAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d did not exit within 5s", pid)
}
