package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultModeCycleState(t *testing.T) {
	state := DefaultModeCycleState()
	if state.Window != "edit" {
		t.Errorf("Window = %q, want %q", state.Window, "edit")
	}
	if state.Current != 0 {
		t.Errorf("Current = %d, want 0", state.Current)
	}
	if len(state.Agents) != 2 {
		t.Fatalf("Agents count = %d, want 2", len(state.Agents))
	}
	if state.Agents[0].Mode != "edit" {
		t.Errorf("Agent[0].Mode = %q, want %q", state.Agents[0].Mode, "edit")
	}
	if state.Agents[0].HoldWindow != "" {
		t.Errorf("Agent[0].HoldWindow = %q, want empty", state.Agents[0].HoldWindow)
	}
	if state.Agents[1].Mode != "agent" {
		t.Errorf("Agent[1].Mode = %q, want %q", state.Agents[1].Mode, "agent")
	}
	if state.Agents[1].HoldWindow != "agent" {
		t.Errorf("Agent[1].HoldWindow = %q, want %q", state.Agents[1].HoldWindow, "agent")
	}
}

func TestNextModeIndex(t *testing.T) {
	tests := []struct {
		current int
		count   int
		want    int
	}{
		{0, 2, 1},
		{1, 2, 0},
		{0, 3, 1},
		{1, 3, 2},
		{2, 3, 0},
		{0, 1, 0},
		{0, 0, 0},
	}
	for _, tt := range tests {
		got := NextModeIndex(tt.current, tt.count)
		if got != tt.want {
			t.Errorf("NextModeIndex(%d, %d) = %d, want %d", tt.current, tt.count, got, tt.want)
		}
	}
}

func TestFindModeAgent(t *testing.T) {
	state := DefaultModeCycleState()

	found := FindModeAgent(state, "edit")
	if found == nil {
		t.Fatal("FindModeAgent('edit') returned nil")
	}
	if found.Role != "edit" {
		t.Errorf("found.Role = %q, want %q", found.Role, "edit")
	}

	found = FindModeAgent(state, "agent")
	if found == nil {
		t.Fatal("FindModeAgent('agent') returned nil")
	}
	if found.Role != "agent" {
		t.Errorf("found.Role = %q, want %q", found.Role, "agent")
	}

	found = FindModeAgent(state, "nonexistent")
	if found != nil {
		t.Error("FindModeAgent('nonexistent') should return nil")
	}
}

func TestCurrentModeAgent(t *testing.T) {
	state := DefaultModeCycleState()
	current := CurrentModeAgent(state)
	if current == nil {
		t.Fatal("CurrentModeAgent returned nil")
	}
	if current.Mode != "edit" {
		t.Errorf("current.Mode = %q, want %q", current.Mode, "edit")
	}

	state.Current = 1
	current = CurrentModeAgent(state)
	if current == nil {
		t.Fatal("CurrentModeAgent returned nil for index 1")
	}
	if current.Mode != "agent" {
		t.Errorf("current.Mode = %q, want %q", current.Mode, "agent")
	}

	// Out of range.
	state.Current = 5
	current = CurrentModeAgent(state)
	if current != nil {
		t.Error("CurrentModeAgent should return nil for out-of-range index")
	}
}

func TestWriteReadModeCycleState(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	session := "test-mode"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0o755)

	state := DefaultModeCycleState()
	state.Current = 1

	err := WriteModeCycleState(session, state)
	if err != nil {
		t.Fatalf("WriteModeCycleState: %v", err)
	}

	// Verify file exists.
	path := ModeCyclePath(session, "edit")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	// Read back.
	got, err := ReadModeCycleState(session, "edit")
	if err != nil {
		t.Fatalf("ReadModeCycleState: %v", err)
	}
	if got.Current != 1 {
		t.Errorf("Current = %d, want 1", got.Current)
	}
	if len(got.Agents) != 2 {
		t.Errorf("Agents count = %d, want 2", len(got.Agents))
	}
	if got.Window != "edit" {
		t.Errorf("Window = %q, want %q", got.Window, "edit")
	}
}

func TestReadModeCycleState_MissingFileEdit(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Missing file for "edit" window returns default state.
	state, err := ReadModeCycleState("nonexistent", "edit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Current != 0 {
		t.Errorf("Current = %d, want 0", state.Current)
	}
	if len(state.Agents) != 2 {
		t.Errorf("Agents count = %d, want 2", len(state.Agents))
	}
}

func TestReadModeCycleState_MissingFileOther(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Missing file for non-edit window returns error.
	_, err := ReadModeCycleState("nonexistent", "build")
	if err == nil {
		t.Error("expected error for non-edit missing file")
	}
}

func TestReadModeCycleState_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	session := "test-invalid"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0o755)
	os.WriteFile(filepath.Join(busDir, "mode-cycle-edit.json"), []byte("{bad json"), 0o644)

	_, err := ReadModeCycleState(session, "edit")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestFormatModeStatus(t *testing.T) {
	state := DefaultModeCycleState()
	out := FormatModeStatus(state)
	if out == "" {
		t.Error("FormatModeStatus returned empty string")
	}
	if !strings.Contains(out, "Active: edit") {
		t.Error("status should show active agent")
	}
	if !strings.Contains(out, "edit") {
		t.Error("status should show window name")
	}
}

func TestFormatModeList(t *testing.T) {
	state := DefaultModeCycleState()
	out := FormatModeList(state)
	if !strings.Contains(out, "*") {
		t.Error("list should show current indicator")
	}
	if !strings.Contains(out, "edit") {
		t.Error("list should show edit")
	}
	if !strings.Contains(out, "agent") {
		t.Error("list should show agent")
	}
}

func TestModeCyclePath(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	path := ModeCyclePath("mysession", "edit")
	if !filepath.IsAbs(path) {
		t.Errorf("path should be absolute: %s", path)
	}
	if filepath.Base(path) != "mode-cycle-edit.json" {
		t.Errorf("filename = %q, want %q", filepath.Base(path), "mode-cycle-edit.json")
	}

	// Different window gets different file.
	path2 := ModeCyclePath("mysession", "build")
	if filepath.Base(path2) != "mode-cycle-build.json" {
		t.Errorf("filename = %q, want %q", filepath.Base(path2), "mode-cycle-build.json")
	}
}

func TestModeCycleState_ThreeAgents(t *testing.T) {
	state := &ModeCycleState{
		Window:  "edit",
		Current: 0,
		Agents: []ModeAgent{
			{Index: 0, Mode: "edit", Role: "edit", HoldWindow: ""},
			{Index: 1, Mode: "agent", Role: "agent", HoldWindow: "agent"},
			{Index: 2, Mode: "design", Role: "design", HoldWindow: "design-hold"},
		},
	}

	// Cycle wraps through 3 agents.
	if NextModeIndex(0, 3) != 1 {
		t.Error("0 -> 1")
	}
	if NextModeIndex(1, 3) != 2 {
		t.Error("1 -> 2")
	}
	if NextModeIndex(2, 3) != 0 {
		t.Error("2 -> 0 (wrap)")
	}

	// FindModeAgent works for third agent.
	found := FindModeAgent(state, "design")
	if found == nil || found.Index != 2 {
		t.Error("should find design agent at index 2")
	}
}

func TestModeSwitchTo_StaleState(t *testing.T) {
	// When state says current=1 (agent mode) but the hold window doesn't exist
	// (e.g. after session restart), modeSwitchTo should auto-correct to index 0.
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	session := "test-stale"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0o755)

	// Write stale state: current=1 but agent window won't exist.
	state := DefaultModeCycleState()
	state.Current = 1
	err := WriteModeCycleState(session, state)
	if err != nil {
		t.Fatalf("write stale state: %v", err)
	}

	// Read it back and verify current is 1.
	got, err := ReadModeCycleState(session, "edit")
	if err != nil {
		t.Fatalf("read stale state: %v", err)
	}
	if got.Current != 1 {
		t.Fatalf("Current = %d, want 1", got.Current)
	}

	// modeSwitchTo with NextModeIndex (1→0) would try to swap agent to hold,
	// but tmuxWindowExists will return false (no tmux running in test).
	// The stale-state guard should auto-correct current to 0.
	// Since target (0) == corrected current (0), it should just write state.
	nextIdx := NextModeIndex(got.Current, len(got.Agents))
	if nextIdx != 0 {
		t.Fatalf("NextModeIndex(1, 2) = %d, want 0", nextIdx)
	}

	err = modeSwitchTo(session, got, nextIdx)
	if err != nil {
		t.Fatalf("modeSwitchTo with stale state: %v", err)
	}

	// State should now be corrected to 0.
	final, err := ReadModeCycleState(session, "edit")
	if err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if final.Current != 0 {
		t.Errorf("Current = %d, want 0 (auto-corrected)", final.Current)
	}
}
