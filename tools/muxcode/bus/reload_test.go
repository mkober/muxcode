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
