package watcher

import (
	"os"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

func TestCheckLoops_60sInterval(t *testing.T) {
	session := testSession(t)
	w := New(session, 5, 8)

	// lastLoopCheck is initialized to now in New()
	now := time.Now().Unix()

	// Immediately after creation, checkLoops should be a no-op (within 60s)
	if now-w.lastLoopCheck >= 60 {
		t.Fatal("expected lastLoopCheck to be recent after New()")
	}

	// Simulate 30s passing — should still skip (was 30s before, now 60s)
	w.lastLoopCheck = now - 30
	beforeCheck := w.lastLoopCheck
	w.checkLoops()
	if w.lastLoopCheck != beforeCheck {
		t.Error("checkLoops should have skipped at 30s interval (now requires 60s)")
	}

	// Simulate 60s passing — should run
	w.lastLoopCheck = now - 61
	w.checkLoops()
	if w.lastLoopCheck <= now-61 {
		t.Error("checkLoops should have updated lastLoopCheck after 60s interval")
	}
}

func TestCheckCron_SkipsEmptyFile(t *testing.T) {
	session := testSession(t)
	w := New(session, 5, 8)

	// Force cron reload by setting lastCronLoad to 0
	w.lastCronLoad = 0

	// Cron file should be empty after init — loadCron should set entries to nil
	w.loadCron()

	if w.cronEntries != nil {
		t.Errorf("expected nil cronEntries for empty cron file, got %d entries", len(w.cronEntries))
	}

	// lastCronLoad should have been updated
	if w.lastCronLoad == 0 {
		t.Error("expected lastCronLoad to be updated after loadCron")
	}
}

func TestCheckProcs_SkipsEmptyFile(t *testing.T) {
	session := testSession(t)
	w := New(session, 5, 8)

	// Initially hasRunningProcs is false and proc file is empty
	// checkProcs should skip entirely
	w.hasRunningProcs = false

	// Verify proc file is empty/missing
	info, err := os.Stat(bus.ProcPath(session))
	if err == nil && info.Size() > 0 {
		t.Skip("proc file not empty — test requires clean state")
	}

	// This should return immediately without error
	w.checkProcs()

	// hasRunningProcs should still be false
	if w.hasRunningProcs {
		t.Error("hasRunningProcs should remain false when proc file is empty")
	}
}

func TestCheckSpawns_SkipsEmptyFile(t *testing.T) {
	session := testSession(t)
	w := New(session, 5, 8)

	// Initially hasRunningSpawns is false and spawn file is empty
	w.hasRunningSpawns = false

	// Verify spawn file is empty/missing
	info, err := os.Stat(bus.SpawnPath(session))
	if err == nil && info.Size() > 0 {
		t.Skip("spawn file not empty — test requires clean state")
	}

	// This should return immediately without error
	w.checkSpawns()

	// hasRunningSpawns should still be false
	if w.hasRunningSpawns {
		t.Error("hasRunningSpawns should remain false when spawn file is empty")
	}
}

func TestWatcher_NewInitializesFields(t *testing.T) {
	w := New("test-session", 5, 8)

	if w.session != "test-session" {
		t.Errorf("session = %q, want %q", w.session, "test-session")
	}
	if w.pollInterval != 5*time.Second {
		t.Errorf("pollInterval = %v, want 5s", w.pollInterval)
	}
	if w.debounceSecs != 8 {
		t.Errorf("debounceSecs = %d, want 8", w.debounceSecs)
	}
	if w.inboxSizes == nil {
		t.Error("inboxSizes should be initialized")
	}
	if w.lastAlertKey == nil {
		t.Error("lastAlertKey should be initialized")
	}
	if w.hasRunningProcs {
		t.Error("hasRunningProcs should be false initially")
	}
	if w.hasRunningSpawns {
		t.Error("hasRunningSpawns should be false initially")
	}
}
