package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRegisterModal(t *testing.T) {
	// Save and restore registry
	origRegistry := modalRegistry
	modalRegistry = map[string]ModalConfig{}
	t.Cleanup(func() { modalRegistry = origRegistry })

	cfg := ModalConfig{Name: "test-modal", Title: "Test", Width: "50%", Height: "50%"}
	RegisterModal(cfg)

	got, ok := GetModal("test-modal")
	if !ok {
		t.Fatal("expected to find registered modal")
	}
	if got.Name != "test-modal" {
		t.Errorf("expected name %q, got %q", "test-modal", got.Name)
	}
}

func TestDefaultModalConfigs(t *testing.T) {
	configs := DefaultModalConfigs()
	if len(configs) == 0 {
		t.Fatal("expected at least one default modal config")
	}

	// Verify api modal is present
	var found bool
	for _, cfg := range configs {
		if cfg.Name == "api" {
			found = true
			if cfg.Width != "62%" || cfg.Height != "62%" {
				t.Errorf("api modal size: got %sx%s, want 62%%x62%%", cfg.Width, cfg.Height)
			}
			if cfg.Split == nil {
				t.Error("api modal should have a split config")
			} else {
				if cfg.Split.Direction != "v" {
					t.Errorf("api split direction: got %q, want %q", cfg.Split.Direction, "v")
				}
				if cfg.Split.Size != "20%" {
					t.Errorf("api split size: got %q, want %q", cfg.Split.Size, "20%")
				}
				if cfg.Split.Primary != "top" {
					t.Errorf("api split primary: got %q, want %q", cfg.Split.Primary, "top")
				}
			}
			if cfg.Role != "api" {
				t.Errorf("api modal role: got %q, want %q", cfg.Role, "api")
			}
		}
	}
	if !found {
		t.Error("expected api modal in default configs")
	}
}

func TestListModals_Sorted(t *testing.T) {
	origRegistry := modalRegistry
	modalRegistry = map[string]ModalConfig{}
	t.Cleanup(func() { modalRegistry = origRegistry })

	RegisterModal(ModalConfig{Name: "zebra"})
	RegisterModal(ModalConfig{Name: "alpha"})
	RegisterModal(ModalConfig{Name: "mid"})

	configs := ListModals()
	if len(configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(configs))
	}
	if configs[0].Name != "alpha" || configs[1].Name != "mid" || configs[2].Name != "zebra" {
		t.Errorf("expected sorted order, got %s, %s, %s",
			configs[0].Name, configs[1].Name, configs[2].Name)
	}
}

func TestIsModalRole(t *testing.T) {
	origRegistry := modalRegistry
	modalRegistry = map[string]ModalConfig{}
	t.Cleanup(func() { modalRegistry = origRegistry })

	RegisterModal(ModalConfig{Name: "api", Role: "api"})

	if !IsModalRole("api") {
		t.Error("expected api to be a modal role")
	}
	if IsModalRole("build") {
		t.Error("expected build to not be a modal role")
	}
}

func TestGetModal_NotFound(t *testing.T) {
	_, ok := GetModal("nonexistent-modal-xyz")
	if ok {
		t.Error("expected not found for nonexistent modal")
	}
}

// --- Size resolution tests ---

func TestResolveSize_ExplicitWxH(t *testing.T) {
	cfg := ModalConfig{Name: "test", Width: "62%", Height: "62%"}
	w, h := ResolveSize(cfg, "80%x70%")
	if w != "80%" || h != "70%" {
		t.Errorf("expected 80%%x70%%, got %sx%s", w, h)
	}
}

func TestResolveSize_Preset(t *testing.T) {
	cfg := ModalConfig{
		Name: "test", Width: "62%", Height: "62%",
		Sizes: map[string][2]string{"compact": {"50%", "40%"}},
	}
	w, h := ResolveSize(cfg, "compact")
	if w != "50%" || h != "40%" {
		t.Errorf("expected 50%%x40%%, got %sx%s", w, h)
	}
}

func TestResolveSize_EnvVar(t *testing.T) {
	cfg := ModalConfig{Name: "test", Width: "62%", Height: "62%"}
	t.Setenv("MUXCODE_MODAL_SIZE_TEST", "80%x70%")

	w, h := ResolveSize(cfg, "")
	if w != "80%" || h != "70%" {
		t.Errorf("expected 80%%x70%% from env, got %sx%s", w, h)
	}
}

func TestResolveSize_EnvVarHyphenatedName(t *testing.T) {
	cfg := ModalConfig{Name: "log-viewer", Width: "62%", Height: "62%"}
	t.Setenv("MUXCODE_MODAL_SIZE_LOG_VIEWER", "75%x60%")

	w, h := ResolveSize(cfg, "")
	if w != "75%" || h != "60%" {
		t.Errorf("expected 75%%x60%% from env, got %sx%s", w, h)
	}
}

func TestResolveSize_Default(t *testing.T) {
	cfg := ModalConfig{Name: "test", Width: "62%", Height: "62%"}
	w, h := ResolveSize(cfg, "")
	if w != "62%" || h != "62%" {
		t.Errorf("expected 62%%x62%%, got %sx%s", w, h)
	}
}

func TestResolveSize_Priority(t *testing.T) {
	cfg := ModalConfig{
		Name: "test", Width: "62%", Height: "62%",
		Sizes: map[string][2]string{"compact": {"50%", "40%"}},
	}
	t.Setenv("MUXCODE_MODAL_SIZE_TEST", "80%x70%")

	// CLI explicit WxH wins over everything
	w, h := ResolveSize(cfg, "90%x85%")
	if w != "90%" || h != "85%" {
		t.Errorf("explicit WxH should win: got %sx%s", w, h)
	}

	// CLI preset wins over env
	w, h = ResolveSize(cfg, "compact")
	if w != "50%" || h != "40%" {
		t.Errorf("preset should win over env: got %sx%s", w, h)
	}
}

func TestResolveSize_UnknownPreset(t *testing.T) {
	cfg := ModalConfig{
		Name: "test", Width: "62%", Height: "62%",
		Sizes: map[string][2]string{"compact": {"50%", "40%"}},
	}
	// Unknown preset falls through to env/default
	w, h := ResolveSize(cfg, "large")
	if w != "62%" || h != "62%" {
		t.Errorf("unknown preset should fall to default: got %s x %s", w, h)
	}
}

// --- Tmux version parsing tests ---

func TestParseTmuxVersionAtLeast(t *testing.T) {
	tests := []struct {
		version string
		major   int
		minor   int
		want    bool
	}{
		{"tmux 3.4", 3, 3, true},
		{"tmux 3.3", 3, 3, true},
		{"tmux 3.3a", 3, 3, true},
		{"tmux 3.2", 3, 3, false},
		{"tmux 3.2a", 3, 3, false},
		{"tmux 4.0", 3, 3, true},
		{"tmux 2.9", 3, 3, false},
		{"tmux next-3.5", 3, 3, true},
		{"tmux 3.3-rc", 3, 3, true},
		{"tmux 3.0", 3, 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := parseTmuxVersionAtLeast(tt.version, tt.major, tt.minor)
			if got != tt.want {
				t.Errorf("parseTmuxVersionAtLeast(%q, %d, %d) = %v, want %v",
					tt.version, tt.major, tt.minor, got, tt.want)
			}
		})
	}
}

func TestTmuxSupportsPopupStyle(t *testing.T) {
	orig := tmuxVersionRunner
	t.Cleanup(func() { tmuxVersionRunner = orig })

	tmuxVersionRunner = func() (string, error) { return "tmux 3.4", nil }
	if !TmuxSupportsPopupStyle() {
		t.Error("expected popup style support for tmux 3.4")
	}

	tmuxVersionRunner = func() (string, error) { return "tmux 3.2", nil }
	if TmuxSupportsPopupStyle() {
		t.Error("expected no popup style support for tmux 3.2")
	}
}

func TestPopupStyleArgs(t *testing.T) {
	orig := tmuxVersionRunner
	t.Cleanup(func() { tmuxVersionRunner = orig })

	// tmux 3.4 — should return Dracula style args
	tmuxVersionRunner = func() (string, error) { return "tmux 3.4", nil }
	args := PopupStyleArgs()
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d", len(args))
	}
	if args[0] != "-b" || args[1] != PopupBorderStyle {
		t.Errorf("expected -b %s, got %s %s", PopupBorderStyle, args[0], args[1])
	}
	if args[2] != "-S" || args[3] != PopupBorderColor {
		t.Errorf("expected -S %s, got %s %s", PopupBorderColor, args[2], args[3])
	}

	// tmux 3.2 — should return nil
	tmuxVersionRunner = func() (string, error) { return "tmux 3.2", nil }
	if PopupStyleArgs() != nil {
		t.Error("expected nil for tmux 3.2")
	}
}

// --- Command building tests ---

func TestBuildModalCommand_NoSplit(t *testing.T) {
	cfg := ModalConfig{Command: "muxcode dashboard"}
	got := BuildModalCommand(cfg)
	if got != "muxcode dashboard" {
		t.Errorf("expected raw command, got %q", got)
	}
}

func TestBuildModalCommand_VerticalSplit(t *testing.T) {
	cfg := ModalConfig{
		Name:    "api",
		Command: "muxcode agent launch api",
		Split: &ModalSplit{
			Direction: "v",
			Size:      "20%",
			Command:   "muxcode console api",
			Primary:   "top",
		},
	}
	got := BuildModalCommand(cfg)

	// Uses separate tmux server for split panes inside popup
	if !strings.Contains(got, "-L muxcode-modal") {
		t.Errorf("expected separate tmux server, got %q", got)
	}
	if !strings.Contains(got, "new-session -d") {
		t.Errorf("expected new-session, got %q", got)
	}
	if !strings.Contains(got, "split-window") && !strings.Contains(got, "-v -l 20%") {
		t.Errorf("expected vertical split, got %q", got)
	}
	if !strings.Contains(got, "'muxcode console api'") {
		t.Errorf("expected secondary command, got %q", got)
	}
	if !strings.Contains(got, "select-pane") || !strings.Contains(got, "{top}") {
		t.Errorf("expected primary pane selection ({top} for vertical split, primary=top), got %q", got)
	}
	if !strings.Contains(got, "TMUX= tmux -L muxcode-modal attach") {
		t.Errorf("expected attach with TMUX unset, got %q", got)
	}
	if !strings.Contains(got, "kill-session") {
		t.Errorf("expected cleanup kill-session, got %q", got)
	}
}

func TestBuildModalCommand_HorizontalSplit(t *testing.T) {
	cfg := ModalConfig{
		Name:    "test",
		Command: "primary-cmd",
		Split: &ModalSplit{
			Direction: "h",
			Size:      "30%",
			Command:   "secondary-cmd",
			Primary:   "left",
		},
	}
	got := BuildModalCommand(cfg)

	if !strings.Contains(got, "split-window") || !strings.Contains(got, "-h -l 30%") {
		t.Errorf("expected horizontal split, got %q", got)
	}
	// Primary=left means select {left} pane
	if !strings.Contains(got, "select-pane") || !strings.Contains(got, "{left}") {
		t.Errorf("expected left pane selection ({left}), got %q", got)
	}
}

func TestBuildModalCommand_SplitPrimaryBottom(t *testing.T) {
	cfg := ModalConfig{
		Name:    "test",
		Command: "primary-cmd",
		Split: &ModalSplit{
			Direction: "v",
			Size:      "30%",
			Command:   "secondary-cmd",
			Primary:   "bottom",
		},
	}
	got := BuildModalCommand(cfg)
	// Primary=bottom means select {bottom} pane
	if !strings.Contains(got, "{bottom}") {
		t.Errorf("expected bottom pane selection ({bottom}), got %q", got)
	}
}

func TestBuildModalCommand_PrimaryExitTearsDown(t *testing.T) {
	cfg := ModalConfig{
		Name:    "api",
		Command: "muxcode agent launch api",
		Split: &ModalSplit{
			Direction: "v",
			Size:      "20%",
			Command:   "muxcode console api",
			Primary:   "top",
		},
	}
	got := BuildModalCommand(cfg)

	// Primary command should be wrapped to kill the nested session on exit,
	// so closing the LLM agent tears down the entire modal.
	expected := `muxcode agent launch api; tmux -L muxcode-modal kill-session -t "muxcode-modal-api"`
	if !strings.Contains(got, expected) {
		t.Errorf("expected primary command wrapped with kill-session teardown.\nwant substring: %s\ngot: %s", expected, got)
	}

	// The primary command in new-session should contain the wrapped version
	if !strings.Contains(got, "new-session -d -s \"$MSESS\" '"+expected) {
		t.Errorf("expected new-session to use wrapped primary command, got %q", got)
	}
}

func TestBuildPopupArgs_Basic(t *testing.T) {
	// Mock tmux version to control border behavior
	orig := tmuxVersionRunner
	tmuxVersionRunner = func() (string, error) { return "tmux 3.2", nil }
	t.Cleanup(func() { tmuxVersionRunner = orig })

	// Use temp dir for PID paths
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	cfg := ModalConfig{
		Name:    "test",
		Title:   " Test Modal ",
		Width:   "62%",
		Height:  "62%",
		Command: "echo hello",
	}

	args := BuildPopupArgs(cfg, "session", "")
	joined := strings.Join(args, " ")

	if args[0] != "display-popup" {
		t.Errorf("expected display-popup, got %q", args[0])
	}
	if !strings.Contains(joined, "-E") {
		t.Error("expected -E flag")
	}
	if !strings.Contains(joined, "-w 62% -h 62%") {
		t.Errorf("expected size flags, got %q", joined)
	}
	if !strings.Contains(joined, "-T  Test Modal ") {
		t.Errorf("expected title flag, got %q", joined)
	}
	// Should NOT have border flags (tmux 3.2)
	if strings.Contains(joined, "rounded") {
		t.Error("should not have border flags for tmux 3.2")
	}
}

func TestBuildPopupArgs_DraculaBorder(t *testing.T) {
	orig := tmuxVersionRunner
	tmuxVersionRunner = func() (string, error) { return "tmux 3.4", nil }
	t.Cleanup(func() { tmuxVersionRunner = orig })

	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	cfg := ModalConfig{
		Name:    "test",
		Width:   "62%",
		Height:  "62%",
		Command: "echo hello",
	}

	args := BuildPopupArgs(cfg, "session", "")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-b rounded -S fg=colour141") {
		t.Errorf("expected Dracula border flags, got %q", joined)
	}
}

func TestBuildPopupArgs_ModalEnvAndPid(t *testing.T) {
	orig := tmuxVersionRunner
	tmuxVersionRunner = func() (string, error) { return "tmux 3.2", nil }
	t.Cleanup(func() { tmuxVersionRunner = orig })

	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	cfg := ModalConfig{
		Name:    "test",
		Width:   "50%",
		Height:  "50%",
		Command: "my-command",
	}

	args := BuildPopupArgs(cfg, "mysession", "")
	// Last arg is the wrapped command
	cmd := args[len(args)-1]

	if !strings.Contains(cmd, "MUXCODE_MODAL=1") {
		t.Errorf("expected MUXCODE_MODAL=1 in command, got %q", cmd)
	}
	if !strings.Contains(cmd, "echo $$") {
		t.Errorf("expected PID write in command, got %q", cmd)
	}
	expectedPidPath := ModalPidPath("mysession", "test")
	if !strings.Contains(cmd, expectedPidPath) {
		t.Errorf("expected PID path %q in command, got %q", expectedPidPath, cmd)
	}
	if !strings.Contains(cmd, "rm -f") {
		t.Errorf("expected PID cleanup in command, got %q", cmd)
	}
}

func TestBuildPopupArgs_SizeFlag(t *testing.T) {
	orig := tmuxVersionRunner
	tmuxVersionRunner = func() (string, error) { return "tmux 3.2", nil }
	t.Cleanup(func() { tmuxVersionRunner = orig })

	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	cfg := ModalConfig{
		Name:   "test",
		Width:  "62%",
		Height: "62%",
		Sizes:  map[string][2]string{"full": {"95%", "95%"}},
	}

	args := BuildPopupArgs(cfg, "s", "full")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-w 95% -h 95%") {
		t.Errorf("expected full preset size, got %q", joined)
	}
}

// --- PID-based state tests ---

func TestIsModalOpen_NoPidFile(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	if IsModalOpen("test-session", "api") {
		t.Error("expected false when no PID file exists")
	}
}

func TestIsModalOpen_StalePid(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	// Create modal dir and write a stale PID
	dir := ModalDir("test-session")
	os.MkdirAll(dir, 0755)
	pidPath := ModalPidPath("test-session", "api")
	os.WriteFile(pidPath, []byte("999999999\n"), 0644)

	if IsModalOpen("test-session", "api") {
		t.Error("expected false for dead PID")
	}

	// PID file should be cleaned up
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("expected stale PID file to be removed")
	}
}

func TestIsModalOpen_CorruptPid(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	dir := ModalDir("test-session")
	os.MkdirAll(dir, 0755)
	pidPath := ModalPidPath("test-session", "api")
	os.WriteFile(pidPath, []byte("not-a-number\n"), 0644)

	if IsModalOpen("test-session", "api") {
		t.Error("expected false for corrupt PID")
	}

	// Corrupt PID file should be cleaned up
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("expected corrupt PID file to be removed")
	}
}

func TestIsModalOpen_AlivePid(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	dir := ModalDir("test-session")
	os.MkdirAll(dir, 0755)

	// Use our own PID — guaranteed alive
	pidPath := ModalPidPath("test-session", "api")
	os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0644)

	if !IsModalOpen("test-session", "api") {
		t.Error("expected true for alive PID")
	}
}

func TestCloseModal_NoPidFile(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	// Should not error when no PID file exists
	err := CloseModal("test-session", "api")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCloseModal_CleansUpPidFile(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	dir := ModalDir("test-session")
	os.MkdirAll(dir, 0755)

	// Write a dead PID
	pidPath := ModalPidPath("test-session", "api")
	os.WriteFile(pidPath, []byte("999999999\n"), 0644)

	err := CloseModal("test-session", "api")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("expected PID file to be removed after close")
	}
}

// --- Toggle tests ---

func TestOpenModal_UnknownModal(t *testing.T) {
	cap := setupTmuxCapture(t)
	_ = cap

	err := OpenModal("session", "nonexistent-xyz", "")
	if err == nil {
		t.Error("expected error for unknown modal")
	}
	if !strings.Contains(err.Error(), "unknown modal") {
		t.Errorf("expected 'unknown modal' error, got %q", err.Error())
	}
}

func TestOpenModal_OpensPopup(t *testing.T) {
	cap := setupTmuxCapture(t)

	orig := tmuxVersionRunner
	tmuxVersionRunner = func() (string, error) { return "tmux 3.4", nil }
	t.Cleanup(func() { tmuxVersionRunner = orig })

	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	// Register a simple modal
	origRegistry := modalRegistry
	modalRegistry = map[string]ModalConfig{}
	t.Cleanup(func() { modalRegistry = origRegistry })

	RegisterModal(ModalConfig{
		Name: "test", Title: " Test ", Width: "50%", Height: "50%",
		Command: "echo hello",
	})

	err := OpenModal("session", "test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cap.calls) != 1 {
		t.Fatalf("expected 1 tmux call, got %d", len(cap.calls))
	}
	if cap.calls[0][0] != "display-popup" {
		t.Errorf("expected display-popup, got %q", cap.calls[0][0])
	}
}

func TestOpenModal_ToggleClose(t *testing.T) {
	cap := setupTmuxCapture(t)

	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	origRegistry := modalRegistry
	modalRegistry = map[string]ModalConfig{}
	t.Cleanup(func() { modalRegistry = origRegistry })

	RegisterModal(ModalConfig{
		Name: "test", Width: "50%", Height: "50%", Command: "echo hi",
	})

	// Spawn a short-lived subprocess whose PID we can safely SIGTERM
	proc := exec.Command("sleep", "30")
	if err := proc.Start(); err != nil {
		t.Fatalf("failed to start subprocess: %v", err)
	}
	childPid := proc.Process.Pid
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })

	// Simulate modal already open by writing the child PID
	dir := ModalDir("session")
	os.MkdirAll(dir, 0755)
	pidPath := ModalPidPath("session", "test")
	os.WriteFile(pidPath, []byte(strconv.Itoa(childPid)+"\n"), 0644)

	// OpenModal should toggle (close) — should NOT call display-popup
	err := OpenModal("session", "test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PID file should be removed (close action)
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("expected PID file to be removed after toggle close")
	}

	// Should not have called display-popup (toggle closed, not opened)
	for _, call := range cap.calls {
		if call[0] == "display-popup" {
			t.Error("should not call display-popup when toggling closed")
		}
	}
}

// --- Modal status tests ---

func TestModalStatus(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	if ModalStatus("session", "api") != "closed" {
		t.Error("expected closed status")
	}
}

// --- Formatter tests ---

func TestFormatModalList_Empty(t *testing.T) {
	out := FormatModalList(nil)
	if !strings.Contains(out, "No modals registered") {
		t.Errorf("expected empty message, got %q", out)
	}
}

func TestFormatModalList_WithConfigs(t *testing.T) {
	configs := []ModalConfig{
		{Name: "api", Title: " API Testing ", Width: "62%", Height: "62%",
			Split: &ModalSplit{Direction: "v"}, Role: "api"},
		{Name: "logs", Title: " Log Viewer ", Width: "80%", Height: "70%", Role: ""},
	}
	out := FormatModalList(configs)

	if !strings.Contains(out, "NAME") {
		t.Error("expected header")
	}
	if !strings.Contains(out, "api") {
		t.Error("expected api in output")
	}
	if !strings.Contains(out, "logs") {
		t.Error("expected logs in output")
	}
}

func TestFormatModalStatus(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	cfg := ModalConfig{
		Name: "api", Title: " API Testing ", Width: "62%", Height: "62%",
		Command: "muxcode agent launch api",
		Split:   &ModalSplit{Direction: "v", Size: "20%", Command: "muxcode console api"},
		Role:    "api",
		Sizes:   map[string][2]string{"compact": {"50%", "40%"}},
	}
	out := FormatModalStatus("session", cfg)

	if !strings.Contains(out, "Modal: api") {
		t.Error("expected modal name")
	}
	if !strings.Contains(out, "closed") {
		t.Error("expected closed status")
	}
	if !strings.Contains(out, "62% x 62%") {
		t.Error("expected size")
	}
	if !strings.Contains(out, "v (20%)") {
		t.Error("expected split info")
	}
	if !strings.Contains(out, "compact") {
		t.Error("expected preset name")
	}
}

// --- Path helper tests ---

func TestModalDir(t *testing.T) {
	dir := ModalDir("mysession")
	if !strings.HasSuffix(dir, filepath.Join("muxcode-bus-mysession", "modals")) {
		t.Errorf("unexpected modal dir: %s", dir)
	}
}

func TestModalPidPath(t *testing.T) {
	p := ModalPidPath("mysession", "api")
	if !strings.HasSuffix(p, filepath.Join("modals", "api.pid")) {
		t.Errorf("unexpected modal pid path: %s", p)
	}
}

// --- Cleanup test ---

func TestCleanupModalPids(t *testing.T) {
	tmp := t.TempDir()
	SetBusDirBase(tmp)
	t.Cleanup(ResetBusDirBase)

	dir := ModalDir("test-session")
	os.MkdirAll(dir, 0755)

	// Create some PID files
	os.WriteFile(filepath.Join(dir, "api.pid"), []byte("123\n"), 0644)
	os.WriteFile(filepath.Join(dir, "logs.pid"), []byte("456\n"), 0644)

	cleanupModalPids("test-session")

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected 0 files after cleanup, got %d", len(entries))
	}
}

// --- ExtractDigits tests ---

func TestExtractDigits(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"3", "3"},
		{"34", "34"},
		{"3a", "3"},
		{"3-rc", "3"},
		{"", ""},
		{"abc", ""},
	}
	for _, tt := range tests {
		got := extractDigits(tt.input)
		if got != tt.want {
			t.Errorf("extractDigits(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
