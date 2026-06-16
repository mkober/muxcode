package daemon

import (
	"os"
	"strings"
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
	if d.idleTaskRetried == nil {
		t.Error("idleTaskRetried should be initialized")
	}
}

func TestActiveWatchdogSecs_Default(t *testing.T) {
	t.Setenv("MUXCODE_ACTIVE_WATCHDOG_SECS", "")
	if got := activeWatchdogSecs(); got != 600 {
		t.Errorf("default = %d, want 600", got)
	}
}

func TestActiveWatchdogSecs_EnvOverride(t *testing.T) {
	t.Setenv("MUXCODE_ACTIVE_WATCHDOG_SECS", "120")
	if got := activeWatchdogSecs(); got != 120 {
		t.Errorf("override = %d, want 120", got)
	}
	// 0 disables; must be honored (not coerced to default).
	t.Setenv("MUXCODE_ACTIVE_WATCHDOG_SECS", "0")
	if got := activeWatchdogSecs(); got != 0 {
		t.Errorf("disabled = %d, want 0", got)
	}
	// Garbage falls back to default.
	t.Setenv("MUXCODE_ACTIVE_WATCHDOG_SECS", "notanint")
	if got := activeWatchdogSecs(); got != 600 {
		t.Errorf("garbage fallback = %d, want 600", got)
	}
}

func TestCheckActiveWatchdog_DisabledNoCrash(t *testing.T) {
	t.Setenv("MUXCODE_ACTIVE_WATCHDOG_SECS", "0")
	d := New("test-session", 5, 8)
	// Disabled threshold must early-return without touching tmux/panes.
	d.checkActiveWatchdog()
}

func TestCheckActiveWatchdog_60sInterval(t *testing.T) {
	t.Setenv("MUXCODE_ACTIVE_WATCHDOG_SECS", "600")
	d := New("test-session", 5, 8)
	// Simulate a check that just ran — the next call should be gated out.
	d.lastActiveWatchdogCheck = time.Now().Unix()
	before := d.lastActiveWatchdogCheck
	d.checkActiveWatchdog()
	if d.lastActiveWatchdogCheck != before {
		t.Error("checkActiveWatchdog should be gated within the 60s interval")
	}
}

func TestDaemon_NewInitializesWatchdogFields(t *testing.T) {
	d := New("test-session", 5, 8)
	if d.activeSince == nil {
		t.Error("activeSince should be initialized")
	}
	if d.lastActiveNudge == nil {
		t.Error("lastActiveNudge should be initialized")
	}
	if d.stuckSeen == nil || d.stuckReloads == nil || d.lastStuckReload == nil || d.stuckGaveUp == nil {
		t.Error("stuck-provider watchdog maps should be initialized")
	}
}

func TestCheckStuckProviders_DisabledNoCrash(t *testing.T) {
	t.Setenv("MUXCODE_STUCK_RELOAD_DISABLE", "1")
	d := New("test-session", 5, 8)
	d.checkStuckProviders() // disabled — must early-return without touching panes
}

func TestCheckStuckProviders_60sInterval(t *testing.T) {
	t.Setenv("MUXCODE_STUCK_RELOAD_DISABLE", "")
	d := New("test-session", 5, 8)
	d.lastStuckCheck = time.Now().Unix()
	before := d.lastStuckCheck
	d.checkStuckProviders()
	if d.lastStuckCheck != before {
		t.Error("checkStuckProviders should be gated within the 60s interval")
	}
}

func TestFormatWatchdogDuration(t *testing.T) {
	cases := map[int64]string{
		45:  "45s",
		60:  "1m",
		90:  "1m 30s",
		600: "10m",
	}
	for secs, want := range cases {
		if got := formatWatchdogDuration(secs); got != want {
			t.Errorf("formatWatchdogDuration(%d) = %q, want %q", secs, got, want)
		}
	}
}

func TestShouldWakeIdleOrActionable(t *testing.T) {
	cases := []struct {
		hasActionable, isIdle, want bool
	}{
		{true, true, true},    // actionable always wakes
		{true, false, true},   // actionable wakes even when active
		{false, true, true},   // response/event delivered to idle agent
		{false, false, false}, // response/event must NOT interrupt active agent
	}
	for _, c := range cases {
		if got := shouldWakeIdleOrActionable(c.hasActionable, c.isIdle); got != c.want {
			t.Errorf("shouldWakeIdleOrActionable(actionable=%v, idle=%v) = %v, want %v",
				c.hasActionable, c.isIdle, got, c.want)
		}
	}
}

func TestExtractDiffFiles(t *testing.T) {
	diffStat := ` bus/profile.go   | 30 ++++++++++++++++---------
 bus/config.go    |  5 ++++-
 daemon/daemon.go | 12 ++++++++++++
 3 files changed, 35 insertions(+), 12 deletions(-)`

	files := extractDiffFiles(diffStat)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(files), files)
	}
	if files[0] != "bus/profile.go" {
		t.Errorf("files[0] = %q, want bus/profile.go", files[0])
	}
	if files[1] != "bus/config.go" {
		t.Errorf("files[1] = %q, want bus/config.go", files[1])
	}
	if files[2] != "daemon/daemon.go" {
		t.Errorf("files[2] = %q, want daemon/daemon.go", files[2])
	}
}

func TestExtractDiffFiles_Empty(t *testing.T) {
	files := extractDiffFiles("")
	if len(files) != 0 {
		t.Errorf("expected 0 files for empty input, got %d", len(files))
	}
}

func TestExtractDiffFiles_SingleFile(t *testing.T) {
	diffStat := ` main.go | 5 ++---
 1 file changed, 2 insertions(+), 3 deletions(-)`

	files := extractDiffFiles(diffStat)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
	if files[0] != "main.go" {
		t.Errorf("files[0] = %q, want main.go", files[0])
	}
}

func TestCheckNonHookEdits_Debounce(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	// Set lastEditDiffCheck to now — should skip
	now := time.Now().Unix()
	d.lastEditDiffCheck = now

	// Should not update lastEditDiffCheck (within 10s debounce)
	d.checkNonHookEdits()
	if d.lastEditDiffCheck != now {
		t.Error("checkNonHookEdits should have been debounced")
	}
}

func TestCheckNonHookEdits_SkipsHookProvider(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	// Force past debounce
	d.lastEditDiffCheck = 0

	// Isolate from live session override files. Without this, the test
	// inherits BUS_SESSION=muxcode from the environment, and tmuxVar("#S")
	// returns "muxcode" even when BUS_SESSION is cleared — causing
	// ResolveProviderCLI to read the real session's override file
	// (/tmp/muxcode-bus-muxcode/config/edit.env) which contains
	// MUXCODE_EDIT_CLI=opencode, making the test fail.
	bus.SetBusDirBase(t.TempDir())
	defer bus.ResetBusDirBase()

	// Force Claude Code edit provider (hooks supported) — the default may be
	// overridden by a global MUXCODE_EDIT_CLI env var (e.g., set to opencode).
	t.Setenv("MUXCODE_EDIT_CLI", "claude")

	// Default edit provider is Claude Code (hooks supported)
	// Should skip without updating lastEditDiffHash
	d.checkNonHookEdits()
	if d.lastEditDiffHash != "" {
		t.Error("checkNonHookEdits should skip for hook-supporting providers")
	}
}

func TestEditDiffHashPath(t *testing.T) {
	path := bus.EditDiffHashPath("test-session")
	if path == "" {
		t.Error("EditDiffHashPath returned empty string")
	}
	if !strings.Contains(path, "edit-diff-hash") {
		t.Errorf("path %q should contain edit-diff-hash", path)
	}
}

func TestNew_InitializesLastIdleState(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	if d.lastIdleState == nil {
		t.Fatal("lastIdleState should be initialized")
	}
	if len(d.lastIdleState) != 0 {
		t.Error("lastIdleState should be empty on creation")
	}
}

func TestIdleTransition_ClearsNotifiedSize(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	role := "build"

	// Create a notified-size marker to simulate stale dedup state
	markerPath := bus.NotifiedIDsPath(session, role)
	if err := os.WriteFile(markerPath, []byte("1234"), 0644); err != nil {
		t.Fatalf("writing marker: %v", err)
	}

	// Set a non-hook cooldown to verify it gets reset
	d.lastNonHookWake[role] = time.Now().Unix() - 10

	// Simulate transition: was not-idle, now idle
	d.lastIdleState[role] = false

	// After the transition logic runs, marker should be cleared.
	// We test the transition logic directly by simulating what
	// checkIdleAgents does for idle transitions.
	isIdle := true // simulate agent becoming idle
	wasIdle := d.lastIdleState[role]
	if isIdle && !wasIdle {
		bus.ClearNotifiedIDs(session, role)
		d.lastNonHookWake[role] = 0
	}
	d.lastIdleState[role] = isIdle

	// Verify marker was cleared
	if _, err := os.Stat(markerPath); err == nil {
		t.Error("notified-size marker should be cleared on idle transition")
	}

	// Verify non-hook cooldown was reset
	if d.lastNonHookWake[role] != 0 {
		t.Errorf("lastNonHookWake should be 0, got %d", d.lastNonHookWake[role])
	}

	// Verify state was updated
	if !d.lastIdleState[role] {
		t.Error("lastIdleState should be true after transition")
	}
}

func TestIdleTransition_NoopWhenAlreadyIdle(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	role := "build"

	// Create a notified-size marker
	markerPath := bus.NotifiedIDsPath(session, role)
	if err := os.WriteFile(markerPath, []byte("1234"), 0644); err != nil {
		t.Fatalf("writing marker: %v", err)
	}

	// Was already idle — no transition
	d.lastIdleState[role] = true

	isIdle := true
	wasIdle := d.lastIdleState[role]
	if isIdle && !wasIdle {
		bus.ClearNotifiedIDs(session, role)
		d.lastNonHookWake[role] = 0
	}
	d.lastIdleState[role] = isIdle

	// Marker should NOT be cleared (no transition)
	if _, err := os.Stat(markerPath); err != nil {
		t.Error("notified-size marker should be preserved when already idle (no transition)")
	}
}

func TestIdleTransition_NoopWhenBecomingNonIdle(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	role := "build"

	// Create a notified-size marker
	markerPath := bus.NotifiedIDsPath(session, role)
	if err := os.WriteFile(markerPath, []byte("1234"), 0644); err != nil {
		t.Fatalf("writing marker: %v", err)
	}

	// Was idle, now not-idle (agent is processing)
	d.lastIdleState[role] = true

	isIdle := false
	wasIdle := d.lastIdleState[role]
	if isIdle && !wasIdle {
		bus.ClearNotifiedIDs(session, role)
		d.lastNonHookWake[role] = 0
	}
	d.lastIdleState[role] = isIdle

	// Marker should NOT be cleared (wrong direction transition)
	if _, err := os.Stat(markerPath); err != nil {
		t.Error("notified-size marker should be preserved when becoming non-idle")
	}
}
