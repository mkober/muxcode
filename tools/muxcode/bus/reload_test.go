package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeReloadMarker writes a legacy bare `reloading` marker non-exclusively —
// a foreign holder's marker, or one predating the owner token. Test-only: no
// production code may write the marker except acquireReloadMarker (MUX-194).
func writeReloadMarker(session, role string) error {
	dir := filepath.Join(BusDir(session), "lock")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	return os.WriteFile(ReloadMarkerPath(session, role), []byte("reloading"), 0644)
}

func TestReloadMarkerPath(t *testing.T) {
	session := "test-session"
	role := "build"
	path := ReloadMarkerPath(session, role)
	expected := filepath.Join(BusDir(session), "lock", role+".reloading")
	if path != expected {
		t.Errorf("ReloadMarkerPath = %q, want %q", path, expected)
	}
}

func TestReloadMarkerPath_WithBusDirOverride(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	path := ReloadMarkerPath("test-session", "build")
	expected := filepath.Join(baseDir, "muxcode-bus-test-session", "lock", "build.reloading")
	if path != expected {
		t.Errorf("ReloadMarkerPath with override = %q, want %q", path, expected)
	}
}

func TestWriteAndClearReloadMarker(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	// No marker initially
	if IsReloading(session, role) {
		t.Error("IsReloading should be false before marker is written")
	}

	// Write marker
	if err := writeReloadMarker(session, role); err != nil {
		t.Fatalf("writeReloadMarker: %v", err)
	}

	// Marker should exist now
	if !IsReloading(session, role) {
		t.Error("IsReloading should be true after writing marker")
	}

	// Verify file content
	path := ReloadMarkerPath(session, role)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "reloading" {
		t.Errorf("marker content = %q, want %q", string(data), "reloading")
	}

	// Clear marker
	clearReloadMarker(session, role)

	// Marker should be gone
	if IsReloading(session, role) {
		t.Error("IsReloading should be false after clearing marker")
	}
}

func TestClearReloadMarker_Idempotent(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	// Clearing a non-existent marker should not error
	clearReloadMarker(session, role)
	clearReloadMarker(session, role) // twice is fine
}

func TestReloadTarget_StandardRole(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	target := ReloadTarget(session, role)
	expected := PaneTarget(session, role)
	if target != expected {
		t.Errorf("ReloadTarget for standard role = %q, want %q", target, expected)
	}
}

func TestReloadTarget_ModeCycledActive(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"

	// Create mode cycle state where edit is active (index 0)
	state := &ModeCycleState{
		Window:  "edit",
		Current: 0,
		Agents: []ModeAgent{
			{Index: 0, Mode: "edit", Role: "edit", HoldWindow: ""},
			{Index: 1, Mode: "auto", Role: "auto", HoldWindow: "auto"},
		},
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	path := ModeCyclePath(session, "edit")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, append(data, '\n'), 0644)

	// Active mode agent should target the host window
	target := ReloadTarget(session, "edit")
	expected := PaneTarget(session, "edit")
	if target != expected {
		t.Errorf("ReloadTarget for active mode agent = %q, want %q", target, expected)
	}
}

func TestReloadTarget_ModeCycledInactive(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"

	// Create mode cycle state where auto is active (index 1)
	state := &ModeCycleState{
		Window:  "edit",
		Current: 1,
		Agents: []ModeAgent{
			{Index: 0, Mode: "edit", Role: "edit", HoldWindow: ""},
			{Index: 1, Mode: "auto", Role: "auto", HoldWindow: "auto"},
		},
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	path := ModeCyclePath(session, "edit")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, append(data, '\n'), 0644)

	// edit hosts its mode group (no hold window), so it resolves to its own
	// window whether or not auto is the showing mode.
	target := ReloadTarget(session, "edit")
	expected := PaneTarget(session, "edit")
	if target != expected {
		t.Errorf("ReloadTarget for inactive mode agent (no hold window) = %q, want %q", target, expected)
	}
}

func TestReloadTarget_ModeCycledInactiveWithHoldWindow(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"

	// Create mode cycle state where edit is active (index 0), auto is inactive (index 1)
	state := &ModeCycleState{
		Window:  "edit",
		Current: 0,
		Agents: []ModeAgent{
			{Index: 0, Mode: "edit", Role: "edit", HoldWindow: ""},
			{Index: 1, Mode: "auto", Role: "auto", HoldWindow: "auto"},
		},
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	path := ModeCyclePath(session, "edit")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, append(data, '\n'), 0644)

	// Inactive mode agent (auto) with HoldWindow should target the holding window
	target := ReloadTarget(session, "auto")
	expected := session + ":auto.1"
	if target != expected {
		t.Errorf("ReloadTarget for inactive mode agent with hold window = %q, want %q", target, expected)
	}
}

func TestIsReloadMarkerStale_NoMarker(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	// No marker file → not stale
	if IsReloadMarkerStale("test-session", "build") {
		t.Error("IsReloadMarkerStale should return false when no marker exists")
	}
}

func TestIsReloadMarkerStale_FreshMarker(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	// Write a fresh marker
	if err := writeReloadMarker(session, role); err != nil {
		t.Fatalf("writeReloadMarker: %v", err)
	}

	// Fresh marker → not stale
	if IsReloadMarkerStale(session, role) {
		t.Error("IsReloadMarkerStale should return false for a fresh marker")
	}
}

func TestIsReloadMarkerStale_OldMarker(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"
	role := "build"

	// Write marker and backdate it to 2 minutes ago
	if err := writeReloadMarker(session, role); err != nil {
		t.Fatalf("writeReloadMarker: %v", err)
	}
	path := ReloadMarkerPath(session, role)
	oldTime := time.Now().Add(-2 * time.Minute)
	os.Chtimes(path, oldTime, oldTime)

	// Old marker → stale
	if !IsReloadMarkerStale(session, role) {
		t.Error("IsReloadMarkerStale should return true for a >60s old marker")
	}
}

func TestCleanStaleReloadMarkers(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-clean-stale"

	// Write markers for build and test, backdate build to make it stale
	writeReloadMarker(session, "build")
	writeReloadMarker(session, "test")

	buildPath := ReloadMarkerPath(session, "build")
	oldTime := time.Now().Add(-2 * time.Minute)
	os.Chtimes(buildPath, oldTime, oldTime)

	// Clean stale markers
	cleaned := CleanStaleReloadMarkers(session)
	if cleaned != 1 {
		t.Errorf("CleanStaleReloadMarkers = %d, want 1 (only build is stale)", cleaned)
	}

	// build marker should be removed
	if IsReloading(session, "build") {
		t.Error("build marker should have been cleaned")
	}

	// test marker should still exist (not stale)
	if !IsReloading(session, "test") {
		t.Error("test marker should still exist (it's fresh)")
	}

	// MUX-194: the removal, and only it, is a row naming role, age and content.
	rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "reload-marker-reclaimed"})
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Detail, "build: stale 1") || !strings.Contains(rows[0].Detail, `content "reloading"`) {
		t.Errorf("reload-marker-reclaimed rows = %+v, want one naming build, its age and its content", rows)
	}
}

// PR review (MUX-194 Phase 2): only a removal that happened is a
// reclamation. A stale marker the cleanup cannot delete (its lock directory
// read-only) is not counted and writes reload-marker-reclaim-failed with the
// error, never reload-marker-reclaimed; a renewed marker is left with no row
// at all. Control: once the directory is writable the same marker is removed,
// counted and logged reclaimed.
func TestCleanStaleReloadMarkers_CountsOnlyRemovals(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	prev := reloadMarkerRenewEvery
	reloadMarkerRenewEvery = 5 * time.Millisecond
	defer func() { reloadMarkerRenewEvery = prev }()
	session := "test-clean-checked"
	rows := func(event string) []LifecycleEntry {
		r, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: event})
		return r
	}

	release, err := acquireReloadMarker(session, "edit")
	if err != nil {
		t.Fatal(err)
	}
	ageReloadMarker(t, session, "edit")
	for deadline := time.Now().Add(2 * time.Second); IsReloadMarkerStale(session, "edit") && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if n := CleanStaleReloadMarkers(session); n != 0 || len(rows("reload-marker-reclaimed")) != 0 {
		t.Fatalf("a renewed hold was reclaimed (cleaned %d, rows %+v)", n, rows("reload-marker-reclaimed"))
	}
	release()

	if err := writeReloadMarker(session, "build"); err != nil {
		t.Fatal(err)
	}
	ageReloadMarker(t, session, "build")
	lock := filepath.Dir(ReloadMarkerPath(session, "build"))
	if err := os.Chmod(lock, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lock, 0o755) })
	if n := CleanStaleReloadMarkers(session); n != 0 || !IsReloading(session, "build") {
		t.Fatalf("a failed removal counted %d (marker present %v), want 0 and present", n, IsReloading(session, "build"))
	}
	if r := rows("reload-marker-reclaimed"); len(r) != 0 {
		t.Errorf("a failed removal was logged reclaimed: %+v", r)
	}
	if r := rows("reload-marker-reclaim-failed"); len(r) != 1 || !strings.HasPrefix(r[0].Detail, "build: stale") || !strings.Contains(r[0].Detail, "permission denied") {
		t.Errorf("reload-marker-reclaim-failed rows = %+v, want one naming build and the error", r)
	}

	if err := os.Chmod(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if n := CleanStaleReloadMarkers(session); n != 1 || IsReloading(session, "build") || len(rows("reload-marker-reclaimed")) != 1 {
		t.Errorf("control: writable removal cleaned %d, present %v, reclaimed rows %d — want 1, gone, 1",
			n, IsReloading(session, "build"), len(rows("reload-marker-reclaimed")))
	}
}

func ageReloadMarker(t *testing.T, session, role string) {
	t.Helper()
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(ReloadMarkerPath(session, role), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

// A hold that outlives the 60s stale threshold — RestartAgent's wait for the
// daemon's verdict — is renewed, so the stale cleanup cannot reclaim it while
// its holder runs. Negative control: once released, a marker nobody holds
// ages and is reclaimed as before.
func TestAcquireReloadMarker_RenewedWhileHeld(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	prev := reloadMarkerRenewEvery
	reloadMarkerRenewEvery = 5 * time.Millisecond
	defer func() { reloadMarkerRenewEvery = prev }()
	session := "test-session"

	release, err := acquireReloadMarker(session, "edit")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	ageReloadMarker(t, session, "edit")
	deadline := time.Now().Add(2 * time.Second)
	for IsReloadMarkerStale(session, "edit") {
		if time.Now().After(deadline) {
			release()
			t.Fatal("a held marker aged past the stale threshold was never renewed")
		}
		time.Sleep(time.Millisecond)
	}
	if n := CleanStaleReloadMarkers(session); n != 0 || !IsReloading(session, "edit") {
		t.Fatalf("stale cleanup reclaimed a live hold (cleaned %d)", n)
	}
	release()

	if err := writeReloadMarker(session, "edit"); err != nil {
		t.Fatalf("writeReloadMarker: %v", err)
	}
	ageReloadMarker(t, session, "edit")
	time.Sleep(50 * time.Millisecond)
	if n := CleanStaleReloadMarkers(session); n != 1 || IsReloading(session, "edit") {
		t.Errorf("an unheld aged marker was not reclaimed (cleaned %d) — renewal outlived its release", n)
	}
}

// Release removes only its own hold. When the cleanup has reclaimed a marker
// and a second relaunch has acquired it, the first holder's release must leave
// the second's in place; the second's release still removes it.
func TestAcquireReloadMarker_ReleaseSparesNewOwner(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-session"

	first, err := acquireReloadMarker(session, "edit")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	clearReloadMarker(session, "edit")
	second, err := acquireReloadMarker(session, "edit")
	if err != nil {
		t.Fatalf("second acquire after reclaim: %v", err)
	}

	first()
	if !IsReloading(session, "edit") {
		t.Fatal("the first holder's release deleted the second holder's marker")
	}
	second()
	if IsReloading(session, "edit") {
		t.Error("the owner's release left its marker")
	}
}

func TestReloadTarget_ModeCycledPlanResearch(t *testing.T) {
	// A mode agent keeps its own window in both mode states: modeSwitchTo uses
	// swap-window, which trades indices only. Resolving an ACTIVE mode role to
	// the host window pointed research at plan's pane, so reloading research
	// sent /exit to plan — plan died and research was never signalled. Both
	// mode states run because the old bug was invisible in one of them.
	for _, tc := range []struct {
		name    string
		current int
	}{
		{"research active", 1},
		{"plan active", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			SetBusDirBase(baseDir)
			defer ResetBusDirBase()

			session := "test-session"
			state := &ModeCycleState{
				Window:  "plan",
				Current: tc.current,
				Agents: []ModeAgent{
					{Index: 0, Mode: "plan", Role: "plan", HoldWindow: ""},
					{Index: 1, Mode: "research", Role: "research", HoldWindow: "research"},
				},
			}
			data, _ := json.MarshalIndent(state, "", "  ")
			path := ModeCyclePath(session, "plan")
			os.MkdirAll(filepath.Dir(path), 0755)
			os.WriteFile(path, append(data, '\n'), 0644)

			research := ReloadTarget(session, "research")
			if want := session + ":research.1"; research != want {
				t.Errorf("ReloadTarget(research) = %q, want %q", research, want)
			}

			plan := ReloadTarget(session, "plan")
			if want := PaneTarget(session, "plan"); plan != want {
				t.Errorf("ReloadTarget(plan) = %q, want %q", plan, want)
			}

			// The defect in one line: two agents resolving to one pane means a
			// reload of either sends its exit sequence to the other.
			if research == plan {
				t.Errorf("research and plan share target %q — reloading one would stop the other", research)
			}
		})
	}
}
