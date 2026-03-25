package bus

import (
	"os"
	"testing"
)

func TestDefaultLauncherConfig(t *testing.T) {
	cfg := DefaultLauncherConfig()
	if cfg.Editor != "nvim" {
		t.Errorf("expected editor nvim, got %s", cfg.Editor)
	}
	if cfg.NvimAppName != "muxcode/nvim" {
		t.Errorf("expected nvim appname muxcode/nvim, got %s", cfg.NvimAppName)
	}
	if cfg.AgentCLI != "claude" {
		t.Errorf("expected agent CLI claude, got %s", cfg.AgentCLI)
	}
	if cfg.ScanDepth != 3 {
		t.Errorf("expected scan depth 3, got %d", cfg.ScanDepth)
	}
	if len(cfg.Windows) != 10 {
		t.Errorf("expected 10 windows, got %d", len(cfg.Windows))
	}
	if cfg.Windows[0] != "edit" {
		t.Errorf("expected first window edit, got %s", cfg.Windows[0])
	}
}

func TestLoadLauncherConfig_EnvOverrides(t *testing.T) {
	t.Setenv("MUXCODE_EDITOR", "vim")
	t.Setenv("MUXCODE_WINDOWS", "edit build test")
	t.Setenv("MUXCODE_SCAN_DEPTH", "5")
	t.Setenv("MUXCODE_SHELL_INIT", "source ~/.bashrc")
	t.Setenv("MUXCODE_NVIM_APPNAME", "custom/nvim")

	cfg := LoadLauncherConfig()
	if cfg.Editor != "vim" {
		t.Errorf("expected editor vim, got %s", cfg.Editor)
	}
	if len(cfg.Windows) != 3 {
		t.Errorf("expected 3 windows, got %d", len(cfg.Windows))
	}
	if cfg.ScanDepth != 5 {
		t.Errorf("expected scan depth 5, got %d", cfg.ScanDepth)
	}
	if cfg.ShellInit != "source ~/.bashrc" {
		t.Errorf("expected shell init, got %s", cfg.ShellInit)
	}
	if cfg.NvimAppName != "custom/nvim" {
		t.Errorf("expected custom/nvim, got %s", cfg.NvimAppName)
	}
}

func TestParseRoleMap(t *testing.T) {
	m := ParseRoleMap("run=runner commit=git analyze=analyst")
	if m["run"] != "runner" {
		t.Errorf("expected run=runner, got %s", m["run"])
	}
	if m["commit"] != "git" {
		t.Errorf("expected commit=git, got %s", m["commit"])
	}
	if m["analyze"] != "analyst" {
		t.Errorf("expected analyze=analyst, got %s", m["analyze"])
	}
	if len(m) != 3 {
		t.Errorf("expected 3 mappings, got %d", len(m))
	}
}

func TestParseRoleMap_Empty(t *testing.T) {
	m := ParseRoleMap("")
	if len(m) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(m))
	}
}

func TestAgentRole(t *testing.T) {
	cfg := DefaultLauncherConfig()
	tests := []struct {
		window string
		want   string
	}{
		{"run", "runner"},
		{"commit", "git"},
		{"analyze", "analyst"},
		{"build", "build"},
		{"edit", "edit"},
		{"test", "test"},
		{"custom", "custom"},
	}
	for _, tt := range tests {
		got := cfg.AgentRole(tt.window)
		if got != tt.want {
			t.Errorf("AgentRole(%q) = %q, want %q", tt.window, got, tt.want)
		}
	}
}

func TestIsSplitLeftWindow(t *testing.T) {
	cfg := DefaultLauncherConfig()
	// Standard split-left windows
	for _, w := range []string{"edit", "build", "test", "review", "deploy", "run", "analyze", "commit", "watch", "api"} {
		if !cfg.IsSplitLeftWindow(w) {
			t.Errorf("expected %s to be split-left", w)
		}
	}
	// Not split-left
	if cfg.IsSplitLeftWindow("custom") {
		t.Error("expected custom to not be split-left")
	}
}

func TestHasConsoleView(t *testing.T) {
	for _, w := range []string{"build", "test", "review", "deploy", "run", "watch", "commit", "analyze", "api"} {
		if !HasConsoleView(w) {
			t.Errorf("expected %s to have console view", w)
		}
	}
	if HasConsoleView("edit") {
		t.Error("expected edit to not have console view")
	}
	if HasConsoleView("custom") {
		t.Error("expected custom to not have console view")
	}
}

func TestCapitalizeWindow(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"edit", "Edit"},
		{"build", "Build"},
		{"", ""},
		{"a", "A"},
		{"API", "API"},
	}
	for _, tt := range tests {
		got := CapitalizeWindow(tt.input)
		if got != tt.want {
			t.Errorf("CapitalizeWindow(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input    string
		fallback int
		want     int
	}{
		{"3", 0, 3},
		{"10", 0, 10},
		{"abc", 5, 5},
		{"", 7, 0}, // empty string produces 0
		{"3x", 5, 5},
	}
	for _, tt := range tests {
		got := parseInt(tt.input, tt.fallback)
		if got != tt.want {
			t.Errorf("parseInt(%q, %d) = %d, want %d", tt.input, tt.fallback, got, tt.want)
		}
	}
}

func TestLoadShellConfig_Launcher(t *testing.T) {
	// Create a temp config file
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config"
	os.WriteFile(configFile, []byte("TEST_LAUNCHER_CFG_KEY=hello\n# comment\nexport TEST_LAUNCHER_CFG_QUOTED=\"world\"\n"), 0644)

	t.Setenv("MUXCODE_CONFIG", configFile)
	os.Unsetenv("TEST_LAUNCHER_CFG_KEY")
	os.Unsetenv("TEST_LAUNCHER_CFG_QUOTED")
	t.Cleanup(func() {
		os.Unsetenv("TEST_LAUNCHER_CFG_KEY")
		os.Unsetenv("TEST_LAUNCHER_CFG_QUOTED")
	})

	LoadShellConfig("")

	if v := os.Getenv("TEST_LAUNCHER_CFG_KEY"); v != "hello" {
		t.Errorf("expected hello, got %q", v)
	}
	if v := os.Getenv("TEST_LAUNCHER_CFG_QUOTED"); v != "world" {
		t.Errorf("expected world, got %q", v)
	}
}

func TestLoadShellConfig_Launcher_EnvTakesPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config"
	os.WriteFile(configFile, []byte("TEST_LAUNCHER_PREC_KEY=from_file\n"), 0644)

	t.Setenv("MUXCODE_CONFIG", configFile)
	t.Setenv("TEST_LAUNCHER_PREC_KEY", "from_env")
	t.Cleanup(func() { os.Unsetenv("TEST_LAUNCHER_PREC_KEY") })

	LoadShellConfig("")

	if v := os.Getenv("TEST_LAUNCHER_PREC_KEY"); v != "from_env" {
		t.Errorf("expected from_env, got %q", v)
	}
}

func TestStripQuotes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`hello`, "hello"},
		{`""`, ""},
		{`"`, `"`},
		{``, ``},
	}
	for _, tt := range tests {
		got := StripQuotes(tt.input)
		if got != tt.want {
			t.Errorf("StripQuotes(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEnsureOllama_NoLocalRoles(t *testing.T) {
	// Should return immediately without any env vars set
	// (no MUXCODE_*_CLI=local)
	EnsureOllama()
}

func TestLoadLauncherConfig_RoleMapOverride(t *testing.T) {
	t.Setenv("MUXCODE_ROLE_MAP", "build=compiler test=checker")
	cfg := LoadLauncherConfig()
	if cfg.RoleMap["build"] != "compiler" {
		t.Errorf("expected build=compiler, got %s", cfg.RoleMap["build"])
	}
	if cfg.RoleMap["test"] != "checker" {
		t.Errorf("expected test=checker, got %s", cfg.RoleMap["test"])
	}
}

func TestLoadLauncherConfig_SplitLeftOverride(t *testing.T) {
	t.Setenv("MUXCODE_SPLIT_LEFT", "edit build")
	cfg := LoadLauncherConfig()
	if len(cfg.SplitLeft) != 2 {
		t.Errorf("expected 2 split-left, got %d", len(cfg.SplitLeft))
	}
	if !cfg.IsSplitLeftWindow("edit") {
		t.Error("expected edit to be split-left")
	}
	if !cfg.IsSplitLeftWindow("build") {
		t.Error("expected build to be split-left")
	}
	if cfg.IsSplitLeftWindow("test") {
		t.Error("expected test to not be split-left")
	}
}
