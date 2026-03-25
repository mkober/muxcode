package bus

import (
	"testing"
	"time"
)

// useTempBusDir redirects BusDir() to use a subdirectory of t.TempDir()
// instead of /tmp. This isolates tests from the real /tmp/muxcode-bus-*
// namespace, preventing:
//   - Collisions with live muxcode sessions
//   - Stale artifacts from panicked/failed tests
//   - Real tmux commands targeting test session names
//
// Cleanup is automatic via t.Cleanup — no manual defer needed.
func useTempBusDir(t *testing.T) {
	t.Helper()
	SetBusDirBase(t.TempDir())
	t.Cleanup(func() { ResetBusDirBase() })
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
