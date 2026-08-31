package bus

import (
	"errors"
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
	if state.Agents[1].Mode != "auto" {
		t.Errorf("Agent[1].Mode = %q, want %q", state.Agents[1].Mode, "auto")
	}
	if state.Agents[1].HoldWindow != "auto" {
		t.Errorf("Agent[1].HoldWindow = %q, want %q", state.Agents[1].HoldWindow, "auto")
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

	found = FindModeAgent(state, "auto")
	if found == nil {
		t.Fatal("FindModeAgent('auto') returned nil")
	}
	if found.Role != "auto" {
		t.Errorf("found.Role = %q, want %q", found.Role, "auto")
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
	if current.Mode != "auto" {
		t.Errorf("current.Mode = %q, want %q", current.Mode, "auto")
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
	if !strings.Contains(out, "auto") {
		t.Error("list should show auto")
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
			{Index: 1, Mode: "auto", Role: "auto", HoldWindow: "auto"},
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

func TestDefaultPlanModeCycleState(t *testing.T) {
	state := DefaultPlanModeCycleState()
	if state.Window != "plan" {
		t.Errorf("Window = %q, want %q", state.Window, "plan")
	}
	if state.Current != 0 {
		t.Errorf("Current = %d, want 0", state.Current)
	}
	if len(state.Agents) != 2 {
		t.Fatalf("Agents count = %d, want 2", len(state.Agents))
	}
	if state.Agents[0].Mode != "plan" {
		t.Errorf("Agent[0].Mode = %q, want %q", state.Agents[0].Mode, "plan")
	}
	if state.Agents[0].Role != "plan" {
		t.Errorf("Agent[0].Role = %q, want %q", state.Agents[0].Role, "plan")
	}
	if state.Agents[0].HoldWindow != "" {
		t.Errorf("Agent[0].HoldWindow = %q, want empty", state.Agents[0].HoldWindow)
	}
	if state.Agents[1].Mode != "research" {
		t.Errorf("Agent[1].Mode = %q, want %q", state.Agents[1].Mode, "research")
	}
	if state.Agents[1].Role != "research" {
		t.Errorf("Agent[1].Role = %q, want %q", state.Agents[1].Role, "research")
	}
	if state.Agents[1].HoldWindow != "research" {
		t.Errorf("Agent[1].HoldWindow = %q, want %q", state.Agents[1].HoldWindow, "research")
	}
}

func TestReadModeCycleState_MissingFilePlan(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Missing file for "plan" window returns default plan state.
	state, err := ReadModeCycleState("nonexistent", "plan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Window != "plan" {
		t.Errorf("Window = %q, want %q", state.Window, "plan")
	}
	if state.Current != 0 {
		t.Errorf("Current = %d, want 0", state.Current)
	}
	if len(state.Agents) != 2 {
		t.Errorf("Agents count = %d, want 2", len(state.Agents))
	}
	if state.Agents[0].Role != "plan" {
		t.Errorf("Agents[0].Role = %q, want %q", state.Agents[0].Role, "plan")
	}
	if state.Agents[1].Role != "research" {
		t.Errorf("Agents[1].Role = %q, want %q", state.Agents[1].Role, "research")
	}
}

func TestReadModeCycleState_EmptyAgentsPlan(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	session := "test-empty-plan"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0o755)

	// Write empty agents array for plan window.
	os.WriteFile(filepath.Join(busDir, "mode-cycle-plan.json"),
		[]byte(`{"window":"plan","current":0,"agents":[]}`), 0o644)

	// Should return default plan state.
	state, err := ReadModeCycleState(session, "plan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Window != "plan" {
		t.Errorf("Window = %q, want %q", state.Window, "plan")
	}
	if len(state.Agents) != 2 {
		t.Errorf("Agents count = %d, want 2", len(state.Agents))
	}
}

func TestPlanModeCycleWrap(t *testing.T) {
	state := DefaultPlanModeCycleState()

	// Cycle from plan (0) to research (1).
	next := NextModeIndex(0, len(state.Agents))
	if next != 1 {
		t.Errorf("NextModeIndex(0, 2) = %d, want 1", next)
	}

	// Cycle from research (1) wraps back to plan (0).
	next = NextModeIndex(1, len(state.Agents))
	if next != 0 {
		t.Errorf("NextModeIndex(1, 2) = %d, want 0", next)
	}

	// FindModeAgent works for plan window agents.
	found := FindModeAgent(state, "plan")
	if found == nil || found.Index != 0 {
		t.Error("should find plan agent at index 0")
	}
	found = FindModeAgent(state, "research")
	if found == nil || found.Index != 1 {
		t.Error("should find research agent at index 1")
	}
}

func TestActiveModeRole_EditDefault(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	session := "test-active"

	// No file — edit window defaults to "edit" role active.
	role, err := ActiveModeRole(session, "edit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "edit" {
		t.Errorf("ActiveModeRole(edit) = %q, want %q", role, "edit")
	}
}

func TestActiveModeRole_PlanDefault(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	session := "test-active-plan"

	// No file — plan window defaults to "plan" role active.
	role, err := ActiveModeRole(session, "plan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "plan" {
		t.Errorf("ActiveModeRole(plan) = %q, want %q", role, "plan")
	}
}

func TestActiveModeRole_SwitchedToAuto(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	session := "test-active-switched"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0o755)

	// Write state with current=1 (auto mode on edit window).
	state := DefaultModeCycleState()
	state.Current = 1
	if err := WriteModeCycleState(session, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	role, err := ActiveModeRole(session, "edit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "auto" {
		t.Errorf("ActiveModeRole(edit) = %q, want %q", role, "auto")
	}
}

func TestActiveModeRole_SwitchedToResearch(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	session := "test-active-research"
	busDir := BusDir(session)
	os.MkdirAll(busDir, 0o755)

	// Write state with current=1 (research mode on plan window).
	state := DefaultPlanModeCycleState()
	state.Current = 1
	if err := WriteModeCycleState(session, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	role, err := ActiveModeRole(session, "plan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role != "research" {
		t.Errorf("ActiveModeRole(plan) = %q, want %q", role, "research")
	}
}

func TestActiveModeRole_UnknownWindow(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Unknown window with no state file should return error.
	_, err := ActiveModeRole("nonexistent", "build")
	if err == nil {
		t.Error("expected error for unknown window")
	}
}

// stubWindowCensus answers list-panes per window target, so windows can
// carry different pane censuses in one test — the shape mode cycling
// produces, where the host window and the hold window each keep their
// own panes and tags across swap-window. Unknown targets error so a
// mistargeted resolution fails the test instead of resolving as a
// silent legacy fallback.
func stubWindowCensus(t *testing.T, censusByTarget map[string]string) {
	t.Helper()
	origRun := tmuxRunner
	origOut := tmuxOutputRunner
	tmuxRunner = func(args ...string) error { return nil }
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) >= 3 && args[0] == "list-panes" && args[1] == "-t" {
			if census, ok := censusByTarget[args[2]]; ok {
				return census, nil
			}
		}
		return "", errors.New("stubWindowCensus: no census for " + strings.Join(args, " "))
	}
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxOutputRunner = origOut
	})
}

// A mode-cycled window resolves the ACTIVE agent by identity (MUX-117
// Phase 4). Mode cycling swaps window indices (swap-window), never
// window names or pane contents, so each mode role must resolve inside
// its own named window: auto's census below lists the agent pane FIRST
// — the order a swap-and-renumber produces — so an index-convention
// read (agent at .1) would return the left pane, and only tag-based
// resolution passes.
func TestModeCycledWindowResolvesActiveAgent(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-mode-resolve"
	if err := os.MkdirAll(BusDir(session), 0o755); err != nil {
		t.Fatal(err)
	}

	state := DefaultModeCycleState()
	state.Current = 1
	if err := WriteModeCycleState(session, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	stubWindowCensus(t, map[string]string{
		session + ":edit": "%0:left:1\n%1:agent:1",
		session + ":auto": "%11:agent:1\n%10:left:1",
	})

	role, err := ActiveModeRole(session, "edit")
	if err != nil {
		t.Fatalf("ActiveModeRole: %v", err)
	}
	if role != "auto" {
		t.Fatalf("active role = %q, want auto", role)
	}

	target, err := ResolvePaneTarget(session, role)
	if err != nil {
		t.Fatalf("ResolvePaneTarget(%s): %v", role, err)
	}
	if target != "%11" {
		t.Errorf("active agent target = %q, want %%11 (auto window's agent pane id)", target)
	}
	if strings.Contains(target, ".") {
		t.Errorf("active agent resolved to an index target, not a pane id: %q", target)
	}

	if target := PaneTarget(session, "edit"); target != "%1" {
		t.Errorf("displaced default agent target = %q, want %%1 (edit window's agent pane id)", target)
	}
}

// Cycle position never changes where a role delivers: with plan active
// or research active, each role resolves its own window's agent pane —
// delivery targets follow the role, not the cycle state. Pins that
// resolution must never couple to the active mode, which would redirect
// a delivery mid-flight when the user cycles.
func TestModeRoleResolutionIndependentOfCycleState(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-mode-cycle-independent"
	if err := os.MkdirAll(BusDir(session), 0o755); err != nil {
		t.Fatal(err)
	}

	stubWindowCensus(t, map[string]string{
		session + ":plan":     "%2:left:1\n%3:agent:1",
		session + ":research": "%21:agent:1\n%20:left:1",
	})

	for _, current := range []int{0, 1} {
		state := DefaultPlanModeCycleState()
		state.Current = current
		if err := WriteModeCycleState(session, state); err != nil {
			t.Fatalf("write state (current=%d): %v", current, err)
		}

		if target, err := ResolvePaneTarget(session, "plan"); err != nil || target != "%3" {
			t.Errorf("current=%d: plan target = %q, %v, want %%3", current, target, err)
		}
		if target, err := ResolvePaneTarget(session, "research"); err != nil || target != "%21" {
			t.Errorf("current=%d: research target = %q, %v, want %%21", current, target, err)
		}
	}
}
