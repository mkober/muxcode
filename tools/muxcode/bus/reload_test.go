package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

	// Inactive mode agent (edit) should target its hold window
	// edit is index 0 with no HoldWindow, meaning it lives on the host window
	// when auto is active. The host window IS edit's pane.
	// Actually, looking at the mode.go defaults: edit has HoldWindow:"" (host), auto has HoldWindow:"auto".
	// When auto is active (index 1), edit (index 0, no HoldWindow) is the inactive one.
	// ReloadTarget checks Index != Current for inactive — edit is index 0, current is 1 → inactive.
	// But edit has no HoldWindow → it falls through to standard PaneTarget for the host window.
	// This is correct: the inactive edit agent's pane is the same edit window.
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

	session := "test-session"

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
}

func TestReloadTarget_ModeCycledPlanResearch(t *testing.T) {
	baseDir := t.TempDir()
	SetBusDirBase(baseDir)
	defer ResetBusDirBase()

	session := "test-session"

	// Plan window with research active (index 1)
	state := &ModeCycleState{
		Window:  "plan",
		Current: 1,
		Agents: []ModeAgent{
			{Index: 0, Mode: "plan", Role: "plan", HoldWindow: ""},
			{Index: 1, Mode: "research", Role: "research", HoldWindow: "research"},
		},
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	path := ModeCyclePath(session, "plan")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, append(data, '\n'), 0644)

	// Research is active — should target the host window (plan's pane)
	target := ReloadTarget(session, "research")
	expected := PaneTarget(session, "plan")
	if target != expected {
		t.Errorf("ReloadTarget for active research = %q, want %q", target, expected)
	}

	// Plan is inactive with no HoldWindow — should use standard PaneTarget
	target = ReloadTarget(session, "plan")
	expected = PaneTarget(session, "plan")
	if target != expected {
		t.Errorf("ReloadTarget for inactive plan = %q, want %q", target, expected)
	}
}
