package daemon

import (
	"os"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

func TestCheckLoops_60sInterval(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	// lastLoopCheck is initialized to now in New()
	now := time.Now().Unix()

	// Immediately after creation, checkLoops should be a no-op (within 60s)
	if now-d.lastLoopCheck >= 60 {
		t.Fatal("expected lastLoopCheck to be recent after New()")
	}

	// Simulate 30s passing — should still skip (was 30s before, now 60s)
	d.lastLoopCheck = now - 30
	beforeCheck := d.lastLoopCheck
	d.checkLoops()
	if d.lastLoopCheck != beforeCheck {
		t.Error("checkLoops should have skipped at 30s interval (now requires 60s)")
	}

	// Simulate 60s passing — should run
	d.lastLoopCheck = now - 61
	d.checkLoops()
	if d.lastLoopCheck <= now-61 {
		t.Error("checkLoops should have updated lastLoopCheck after 60s interval")
	}
}

func TestCheckCron_SkipsEmptyFile(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	// Force cron reload by setting lastCronLoad to 0
	d.lastCronLoad = 0

	// Cron file should be empty after init — loadCron should set entries to nil
	d.loadCron()

	if d.cronEntries != nil {
		t.Errorf("expected nil cronEntries for empty cron file, got %d entries", len(d.cronEntries))
	}

	// lastCronLoad should have been updated
	if d.lastCronLoad == 0 {
		t.Error("expected lastCronLoad to be updated after loadCron")
	}
}

func TestCheckProcs_SkipsEmptyFile(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	// Initially hasRunningProcs is false and proc file is empty
	// checkProcs should skip entirely
	d.hasRunningProcs = false

	// Verify proc file is empty/missing
	info, err := os.Stat(bus.ProcPath(session))
	if err == nil && info.Size() > 0 {
		t.Skip("proc file not empty — test requires clean state")
	}

	// This should return immediately without error
	d.checkProcs()

	// hasRunningProcs should still be false
	if d.hasRunningProcs {
		t.Error("hasRunningProcs should remain false when proc file is empty")
	}
}

func TestCheckSpawns_SkipsEmptyFile(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	// Initially hasRunningSpawns is false and spawn file is empty
	d.hasRunningSpawns = false

	// Verify spawn file is empty/missing
	info, err := os.Stat(bus.SpawnPath(session))
	if err == nil && info.Size() > 0 {
		t.Skip("spawn file not empty — test requires clean state")
	}

	// This should return immediately without error
	d.checkSpawns()

	// hasRunningSpawns should still be false
	if d.hasRunningSpawns {
		t.Error("hasRunningSpawns should remain false when spawn file is empty")
	}
}

func TestDaemon_NewInitializesFields(t *testing.T) {
	d := New("test-session", 5, 8)

	if d.session != "test-session" {
		t.Errorf("session = %q, want %q", d.session, "test-session")
	}
	if d.pollInterval != 5*time.Second {
		t.Errorf("pollInterval = %v, want 5s", d.pollInterval)
	}
	if d.debounceSecs != 8 {
		t.Errorf("debounceSecs = %d, want 8", d.debounceSecs)
	}
	if d.inboxSizes == nil {
		t.Error("inboxSizes should be initialized")
	}
	if d.lastAlertKey == nil {
		t.Error("lastAlertKey should be initialized")
	}
	if d.hasRunningProcs {
		t.Error("hasRunningProcs should be false initially")
	}
	if d.hasRunningSpawns {
		t.Error("hasRunningSpawns should be false initially")
	}
}
