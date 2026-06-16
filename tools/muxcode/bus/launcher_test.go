package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if cfg.Windows[0] != "plan" {
		t.Errorf("expected first window plan, got %s", cfg.Windows[0])
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
		// With empty default RoleMap, windows pass through as canonical names
		{"run", "run"},
		{"commit", "commit"},
		{"analyze", "analyze"},
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
	for _, w := range []string{"plan", "edit", "build", "test", "serve", "review", "deploy", "run", "commit", "watch"} {
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
	for _, w := range []string{"build", "test", "review", "deploy", "run", "watch", "commit", "api", "serve"} {
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

func TestFilterString(t *testing.T) {
	tests := []struct {
		input []string
		val   string
		want  int
	}{
		{[]string{"a", "b", "c"}, "b", 2},
		{[]string{"a", "b", "c"}, "d", 3},
		{[]string{"a", "a", "b"}, "a", 1},
		{[]string{}, "a", 0},
	}
	for _, tt := range tests {
		got := filterString(tt.input, tt.val)
		if len(got) != tt.want {
			t.Errorf("filterString(%v, %q) = %d items, want %d", tt.input, tt.val, len(got), tt.want)
		}
		for _, v := range got {
			if v == tt.val {
				t.Errorf("filterString(%v, %q) still contains %q", tt.input, tt.val, tt.val)
			}
		}
	}
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

func TestScanProjects_FindsGitDirs(t *testing.T) {
	tmp := t.TempDir()
	// Create a few git projects at varying depths
	os.MkdirAll(tmp+"/projectA/.git", 0755)
	os.MkdirAll(tmp+"/sub/projectB/.git", 0755)
	os.MkdirAll(tmp+"/sub/deep/projectC/.git", 0755)

	projects := ScanProjects(tmp, 4)
	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d: %v", len(projects), projects)
	}

	want := map[string]bool{
		tmp + "/projectA":          true,
		tmp + "/sub/projectB":      true,
		tmp + "/sub/deep/projectC": true,
	}
	for _, p := range projects {
		if !want[p] {
			t.Errorf("unexpected project: %s", p)
		}
	}
}

func TestScanProjects_RespectsMaxDepth(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(tmp+"/shallow/.git", 0755)
	os.MkdirAll(tmp+"/a/b/c/deep/.git", 0755)

	projects := ScanProjects(tmp, 2)
	if len(projects) != 1 {
		t.Fatalf("expected 1 project (depth-limited), got %d: %v", len(projects), projects)
	}
	if projects[0] != tmp+"/shallow" {
		t.Errorf("expected shallow project, got %s", projects[0])
	}
}

func TestScanProjects_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	projects := ScanProjects(tmp, 3)
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}
}

func TestScanProjects_CommaSeparatedDirs(t *testing.T) {
	tmp1 := t.TempDir()
	tmp2 := t.TempDir()
	os.MkdirAll(tmp1+"/projA/.git", 0755)
	os.MkdirAll(tmp2+"/projB/.git", 0755)

	projects := ScanProjects(tmp1+","+tmp2, 3)
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects from 2 dirs, got %d: %v", len(projects), projects)
	}
}

func TestScanProjects_NonexistentDir(t *testing.T) {
	projects := ScanProjects("/nonexistent/path/xyz", 3)
	if len(projects) != 0 {
		t.Errorf("expected 0 projects for nonexistent dir, got %d", len(projects))
	}
}

func TestPickProjectFallback_InvalidSelection(t *testing.T) {
	// pickProjectFallback reads from stdin — we can't easily test interactive input
	// but we can verify the function signature and error path
	projects := []string{"/a", "/b"}
	// idx -1 (parseInt("abc") returns 0, 0-1 = -1)
	idx := parseInt("abc", 0) - 1
	if idx >= 0 && idx < len(projects) {
		t.Error("expected invalid index for non-numeric input")
	}
}

func TestTransformStatusRight_RemovesPowerlineArrows(t *testing.T) {
	// Input with thin-right and right powerline arrows, no restyle triggers
	input := "prefix" + pwrThinRight + "middle" + pwrRight + "suffix"
	got := TransformStatusRight(input)
	if strings.Contains(got, pwrThinRight) {
		t.Error("expected thin-right powerline arrow to be removed")
	}
	// pwrRight is removed from original input (no restyle color triggers present)
	if strings.Contains(got, pwrRight) {
		t.Error("expected right powerline arrow to be removed")
	}
	if got != "prefixmiddlesuffix" {
		t.Errorf("expected 'prefixmiddlesuffix', got %q", got)
	}
}

func TestTransformStatusRight_RestyleDate(t *testing.T) {
	input := "#[fg=#6272a4, bg=#282a36] %b %d '%y"
	got := TransformStatusRight(input)
	// Date should get tab-color bg restyle
	if !strings.Contains(got, "#[fg=#44475a, bg=#282a36]") {
		t.Error("expected tab-color bg in date restyle")
	}
	if !strings.Contains(got, "#[fg=#f8f8f2, bg=#44475a]") {
		t.Error("expected f8f8f2 fg in date restyle")
	}
	// Padding around date
	if !strings.Contains(got, " %b") {
		t.Error("expected space before date month format")
	}
	if !strings.Contains(got, "'%y ") {
		t.Error("expected space after year format")
	}
}

func TestTransformStatusRight_RestyleTime(t *testing.T) {
	input := "#[fg=#50fa7b] %H:%M"
	got := TransformStatusRight(input)
	// Time should get comment-color bg restyle
	if !strings.Contains(got, "#[fg=#6272a4, bg=#44475a]") {
		t.Error("expected comment-color bg in time restyle")
	}
	// %H:%M replaced with padded %H:%M:%S
	if !strings.Contains(got, " %H:%M:%S ") {
		t.Error("expected padded %H:%M:%S")
	}
}

func TestTransformStatusRight_StripsMusicSegment(t *testing.T) {
	input := "before #[fg=#282a36, bg=#00ff00] #(~/dotfiles/tmux_scripts/music.sh) after"
	got := TransformStatusRight(input)
	if strings.Contains(got, "music.sh") {
		t.Error("expected music segment to be stripped")
	}
}

func TestTransformStatusLeft_HamburgerIcon(t *testing.T) {
	input := "#[fg=#f8f8f2]❐ #S"
	got := TransformStatusLeft(input)
	if strings.Contains(got, "❐") {
		t.Error("expected ❐ to be replaced")
	}
	if !strings.Contains(got, "☰") {
		t.Error("expected ☰ hamburger icon")
	}
	if !strings.Contains(got, "#S") {
		t.Error("expected session name placeholder preserved")
	}
}

func TestTransformStatusLeft_NoIcon(t *testing.T) {
	input := "no icon here"
	got := TransformStatusLeft(input)
	if got != input {
		t.Errorf("expected unchanged string, got %q", got)
	}
}

func TestWindowStatusFormat_ContainsDraculaColors(t *testing.T) {
	got := WindowStatusFormat()
	if !strings.Contains(got, "#44475a") {
		t.Error("expected Dracula current-line color")
	}
	if !strings.Contains(got, "#f8f8f2") {
		t.Error("expected Dracula foreground color")
	}
	if !strings.Contains(got, "#282a36") {
		t.Error("expected Dracula background color for powerline edges")
	}
	if !strings.Contains(got, pwrLeft) {
		t.Error("expected powerline left arrow")
	}
	if !strings.Contains(got, "@display-name") {
		t.Error("expected @display-name user option for window labels")
	}
	// Must NOT use #{?} conditional — breaks when #[] escapes contain commas
	if strings.Contains(got, "#{?") {
		t.Error("must not use #{?} conditional (breaks with commas in style escapes)")
	}
}

func TestWindowStatusCurrentFormat_ContainsGreenHighlight(t *testing.T) {
	got := WindowStatusCurrentFormat()
	if !strings.Contains(got, "#50fa7b") {
		t.Error("expected Dracula green (#50fa7b) for current window")
	}
	if !strings.Contains(got, "bold") {
		t.Error("expected bold for current window")
	}
	if !strings.Contains(got, "F#I*") {
		t.Error("expected F#I* (function key + asterisk) for current window")
	}
	if strings.Contains(got, "#{?") {
		t.Error("must not use #{?} conditional (breaks with commas in style escapes)")
	}
}

func TestClassifyPane_TrustPrompt(t *testing.T) {
	content := `Welcome to Claude Code!

Do you trust this folder and want to proceed?
  > Yes, I trust this folder
    No, exit`
	if got := ClassifyPane(content); got != PaneTrustPrompt {
		t.Errorf("expected PaneTrustPrompt, got %d", got)
	}
}

func TestClassifyPane_BypassPrompt(t *testing.T) {
	content := `Bypass Permissions mode lets Claude run commands without asking.
This can be dangerous. Do you accept the risk?
  > No, keep safe mode
    Yes, I accept`
	if got := ClassifyPane(content); got != PaneBypassPrompt {
		t.Errorf("expected PaneBypassPrompt, got %d", got)
	}
}

func TestClassifyPane_Idle(t *testing.T) {
	content := `Claude Code v1.2.3

/home/user/project

❯`
	if got := ClassifyPane(content); got != PaneIdle {
		t.Errorf("expected PaneIdle, got %d", got)
	}
}

func TestClassifyPane_NotReady(t *testing.T) {
	content := `Loading Claude Code...
Initializing agent...`
	if got := ClassifyPane(content); got != PaneNotReady {
		t.Errorf("expected PaneNotReady, got %d", got)
	}
}

func TestClassifyPane_Empty(t *testing.T) {
	if got := ClassifyPane(""); got != PaneNotReady {
		t.Errorf("expected PaneNotReady for empty, got %d", got)
	}
}

func TestClassifyPane_TrustTakesPrecedence(t *testing.T) {
	// If both trust and ❯ appear (unlikely but defensive), trust wins
	content := "trust this folder\n❯"
	if got := ClassifyPane(content); got != PaneTrustPrompt {
		t.Errorf("expected PaneTrustPrompt to take precedence, got %d", got)
	}
}

func TestClassifyPane_BypassTakesPrecedence(t *testing.T) {
	// Bypass before idle check
	content := "Bypass Permissions\n❯"
	if got := ClassifyPane(content); got != PaneBypassPrompt {
		t.Errorf("expected PaneBypassPrompt to take precedence, got %d", got)
	}
}

func TestNeedsWakeUp(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()

	session := "test-needs-wakeup"
	if err := Init(session, os.Getenv("BUS_DIR_BASE")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// No messages — should not need wake-up
	if NeedsWakeUp(session, "edit") {
		t.Error("NeedsWakeUp(edit) should be false with empty inbox")
	}
	if NeedsWakeUp(session, "build") {
		t.Error("NeedsWakeUp(build) should be false with empty inbox")
	}

	// Send a request message to build inbox
	msg := NewMessage("edit", "build", "request", "build", "Run build", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// build should need wake-up, edit should not
	if !NeedsWakeUp(session, "build") {
		t.Error("NeedsWakeUp(build) should be true with pending request")
	}
	if NeedsWakeUp(session, "edit") {
		t.Error("NeedsWakeUp(edit) should be false with no messages")
	}

	// Send an event (startup notification) to edit inbox. Sender must differ
	// from the recipient — a self-addressed message is filtered as a loop.
	evt := NewMessage("daemon", "edit", "event", "notify", "Session started", "")
	if err := Send(session, evt); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// edit should need wake-up — any non-empty inbox triggers startup wake
	// (even events/responses, since the agent needs to check inbox and restore context)
	if !NeedsWakeUp(session, "edit") {
		t.Error("NeedsWakeUp(edit) should be true with startup event in inbox")
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

func TestFindRecentMarkdown(t *testing.T) {
	dir := t.TempDir()
	docsDir := filepath.Join(dir, "docs")
	os.MkdirAll(filepath.Join(docsDir, "requirements"), 0o755)

	// Create two .md files with different mtimes
	older := filepath.Join(docsDir, "architecture.md")
	newer := filepath.Join(docsDir, "requirements", "feature.md")
	os.WriteFile(older, []byte("# Arch"), 0o644)
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(newer, []byte("# Feature"), 0o644)

	got := findRecentMarkdown(dir)
	want := "docs/requirements/feature.md"
	if got != want {
		t.Errorf("findRecentMarkdown() = %q, want %q", got, want)
	}
}

func TestFindRecentMarkdown_Empty(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)

	got := findRecentMarkdown(dir)
	if got != "" {
		t.Errorf("findRecentMarkdown() on empty docs = %q, want empty", got)
	}
}

func TestFindRecentMarkdown_NoDocs(t *testing.T) {
	dir := t.TempDir()

	got := findRecentMarkdown(dir)
	if got != "" {
		t.Errorf("findRecentMarkdown() with no docs dir = %q, want empty", got)
	}
}

func TestFindRecentMarkdown_IgnoresNonMd(t *testing.T) {
	dir := t.TempDir()
	docsDir := filepath.Join(dir, "docs")
	os.MkdirAll(docsDir, 0o755)

	// Create a non-.md file and an .md file
	os.WriteFile(filepath.Join(docsDir, "notes.txt"), []byte("text"), 0o644)
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(filepath.Join(docsDir, "readme.md"), []byte("# Readme"), 0o644)

	got := findRecentMarkdown(dir)
	if got != "docs/readme.md" {
		t.Errorf("findRecentMarkdown() = %q, want %q", got, "docs/readme.md")
	}
}
