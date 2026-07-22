package bus

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// liveForeignPID starts a real child process and returns its PID, giving tests
// a PID that is genuinely alive but is not the test process. PID 1 cannot be
// used: CheckProcAlive uses kill(pid, 0), which returns EPERM (not nil) for
// init when the test runs unprivileged, so PID 1 reads as dead.
func liveForeignPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn helper process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func markerTestPath(t *testing.T, session, role string) string {
	t.Helper()
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return PollingMarkerPath(session, role)
}

// TestSetPolling_RefusesSecondLiveListener is the core guard against duplicate
// inbox consumers. Every --poll tick consumes destructively and writes an
// "acked" receipt, so a second listener silently eats messages into a pipe no
// agent runtime reads — while the receipt convinces the daemon it was
// delivered, muting the receipt-gap backstop.
func TestSetPolling_RefusesSecondLiveListener(t *testing.T) {
	session, role := "test-marker-exclusive", "build"
	path := markerTestPath(t, session, role)

	// An incumbent listener owns the marker.
	other := liveForeignPID(t)
	if err := os.WriteFile(path, []byte(strconv.Itoa(other)), 0644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	if SetPolling(session, role) {
		t.Error("SetPolling must refuse the claim while another live listener holds the marker")
	}

	// The incumbent's marker must survive the refused claim.
	pid, ok := readMarkerPID(path)
	if !ok || pid != other {
		t.Errorf("incumbent marker must be left intact, got pid=%d ok=%v (want %d)", pid, ok, other)
	}
}

// TestSetPolling_TakesOverStaleMarker ensures a dead listener never blocks a
// new one — otherwise a crashed agent could never re-register.
func TestSetPolling_TakesOverStaleMarker(t *testing.T) {
	session, role := "test-marker-stale-takeover", "test"
	path := markerTestPath(t, session, role)

	if err := os.WriteFile(path, []byte("999999999"), 0644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	if !SetPolling(session, role) {
		t.Fatal("SetPolling must take over a marker whose owner is dead")
	}
	if pid, ok := readMarkerPID(path); !ok || pid != os.Getpid() {
		t.Errorf("marker must record the new owner, got pid=%d ok=%v (want %d)", pid, ok, os.Getpid())
	}
}

// TestSetPolling_ClaimsFreeMarker covers the ordinary path.
func TestSetPolling_ClaimsFreeMarker(t *testing.T) {
	session, role := "test-marker-free", "review"
	path := markerTestPath(t, session, role)

	if !SetPolling(session, role) {
		t.Fatal("SetPolling must succeed when no marker exists")
	}
	if !IsPolling(session, role) {
		t.Error("IsPolling must report true after a successful claim")
	}
	if pid, ok := readMarkerPID(path); !ok || pid != os.Getpid() {
		t.Errorf("marker must record our PID, got pid=%d ok=%v", pid, ok)
	}
}

// TestClearPolling_OnlyOwnerMayRelease guards the self-amplifying leak: with an
// unconditional remove, the first of several listeners to exit un-registers
// every survivor, IsPolling goes false while a live listener is still
// consuming, and the Stop hook launches yet another duplicate.
func TestClearPolling_OnlyOwnerMayRelease(t *testing.T) {
	session, role := "test-marker-release", "deploy"
	path := markerTestPath(t, session, role)

	other := liveForeignPID(t)
	if err := os.WriteFile(path, []byte(strconv.Itoa(other)), 0644); err != nil {
		t.Fatalf("seed marker: %v", err)
	}

	ClearPolling(session, role)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("ClearPolling must not release a marker owned by another live listener")
	}
	if !IsPolling(session, role) {
		t.Error("the incumbent listener must still register as polling")
	}

	// The owner can release its own marker.
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatalf("reseed marker: %v", err)
	}
	ClearPolling(session, role)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("ClearPolling must release a marker this process owns")
	}
}

// TestWriteMarkerAtomic_NoTruncationWindow pins the fix for the vanishing
// marker. os.WriteFile truncates in place, so a concurrent IsPolling could read
// zero bytes, judge the marker corrupt, and delete it — permanently
// un-registering a live listener, since nothing ever rewrites the marker.
func TestWriteMarkerAtomic_NoTruncationWindow(t *testing.T) {
	session, role := "test-marker-atomic", "watch"
	path := markerTestPath(t, session, role)

	if !writeMarkerAtomic(path, os.Getpid()) {
		t.Fatal("initial writeMarkerAtomic failed")
	}

	var (
		wg      sync.WaitGroup
		torn    int64
		stop    = make(chan struct{})
		writes  = 500
		readers = 4
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < writes; i++ {
			if !writeMarkerAtomic(path, os.Getpid()) {
				atomic.AddInt64(&torn, 1)
			}
		}
	}()

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, ok := readMarkerPID(path); !ok {
					atomic.AddInt64(&torn, 1)
				}
			}
		}()
	}
	wg.Wait()

	if torn != 0 {
		t.Errorf("readers observed %d truncated/unparseable markers during concurrent writes", torn)
	}
}
