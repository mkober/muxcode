package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Interface conformance ---

func TestCodexProvider_Interface(t *testing.T) {
	var p Provider = &CodexProvider{}
	if p.Name() != "codex" {
		t.Errorf("Name() = %q, want codex", p.Name())
	}
	if p.SupportsHooks() {
		t.Error("Codex should not support hooks")
	}
	if p.IdlePromptChar() != "" {
		t.Error("Codex should have empty idle prompt char")
	}
}

// --- ResolveProvider ---

func TestResolveProvider_PerRoleCodex(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_REVIEW_CLI", "codex")

	p := ResolveProvider("review")
	if p.Name() != "codex" {
		t.Errorf("provider = %q, want codex", p.Name())
	}
}

func TestResolveProvider_SessionDefaultCodex(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "codex")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	p := ResolveProvider("build")
	if p.Name() != "codex" {
		t.Errorf("provider = %q, want codex", p.Name())
	}
}

func TestResolveProvider_CodexNoHooks(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_REVIEW_CLI", "codex")

	p := ResolveProvider("review")
	if p.SupportsHooks() {
		t.Error("Codex provider should not support hooks")
	}
}

// --- BuildExecArgs ---

func TestCodexBuildExecArgs(t *testing.T) {
	p := &CodexProvider{}

	for _, role := range []string{"build", "test", "review", "analyze"} {
		t.Run(role, func(t *testing.T) {
			t.Setenv("MUXCODE_CODEX_MODEL", "")
			t.Setenv(RoleCodexModelEnvVar(role), "")

			cfg := &LaunchConfig{
				Role: role,
				CLI:  "codex",
			}

			binary, args := p.BuildExecArgs(cfg)

			if binary != "codex" {
				t.Errorf("binary = %q, want codex", binary)
			}
			// Should contain --no-alt-screen and -a flag but NOT -C
			// (-C changes the working directory away from the repo root)
			// -a never for execution roles, -a on-request for read-only
			// roles (review, analyze) to enforce permission prompts
			hasNoAltScreen := false
			approvalPolicy := ""
			for i, arg := range args {
				if arg == "--no-alt-screen" {
					hasNoAltScreen = true
				}
				if arg == "-a" && i+1 < len(args) {
					approvalPolicy = args[i+1]
				}
				if arg == "-C" {
					t.Error("-C flag should NOT be present (changes working root away from project)")
				}
			}
			if isReadOnlyCodexRole(role) {
				if approvalPolicy != "on-request" {
					t.Errorf("approval policy = %q, want %q for read-only role %q", approvalPolicy, "on-request", role)
				}
			} else {
				if approvalPolicy != "never" {
					t.Errorf("approval policy = %q, want %q for role %q", approvalPolicy, "never", role)
				}
			}
			if !hasNoAltScreen {
				t.Error("missing --no-alt-screen flag")
			}
		})
	}
}

func TestCodexBuildExecArgs_WithModel(t *testing.T) {
	p := &CodexProvider{}
	t.Setenv(RoleModelEnvVar("review"), "") // clear generic model env var
	t.Setenv("MUXCODE_REVIEW_CODEX_MODEL", "o3")

	cfg := &LaunchConfig{Role: "review", CLI: "codex"}
	binary, args := p.BuildExecArgs(cfg)

	if binary != "codex" {
		t.Errorf("binary = %q, want codex", binary)
	}
	// Should have -m o3
	hasModel := false
	for i, arg := range args {
		if arg == "-m" && i+1 < len(args) && args[i+1] == "o3" {
			hasModel = true
		}
	}
	if !hasModel {
		t.Errorf("missing -m o3 in args: %v", args)
	}
}

// --- ClassifyPane ---

func TestCodexClassifyPane(t *testing.T) {
	p := &CodexProvider{}

	tests := []struct {
		name    string
		content string
		want    PaneState
	}{
		{"tui_box_drawing", "╭─ codex prompt ─╮", PaneIdle},
		{"codex_text", "codex -a never", PaneIdle},
		{"codex_uppercase", "Codex is ready", PaneIdle},
		{"error", "ERROR: codex CLI not found in PATH", PaneNotReady},
		{"fatal", "FATAL: authentication failed", PaneNotReady},
		{"empty", "", PaneNotReady},
		{"loading", "Starting...", PaneNotReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.ClassifyPane(tt.content); got != tt.want {
				t.Errorf("ClassifyPane(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}

// --- AcceptStartup ---

func TestCodexAcceptStartup(t *testing.T) {
	p := &CodexProvider{}

	if !p.AcceptStartup("session", "session:review.1", PaneIdle) {
		t.Error("AcceptStartup should return true when PaneIdle")
	}
	if p.AcceptStartup("session", "session:review.1", PaneNotReady) {
		t.Error("AcceptStartup should return false when PaneNotReady")
	}
}

// --- Compact ---

func TestCodexCompact_NoOp(t *testing.T) {
	p := &CodexProvider{}
	err := p.Compact("session", "review", "session:review.1")
	if err != nil {
		t.Errorf("Compact should be no-op, got error: %v", err)
	}
}

// --- DetectTaskCompletion ---

func TestCodexDetectTaskCompletion_BusReplySuccess(t *testing.T) {
	p := &CodexProvider{}

	pane := `Running code review...
Review complete: 3 findings
$ muxcode send edit response "Review done: 3 findings"
Sent response:response to edit`

	completed, errored, summary := p.DetectTaskCompletion("session", "review", pane)
	if !completed {
		t.Error("expected completed=true for bus reply output")
	}
	if errored {
		t.Error("expected errored=false for successful bus reply")
	}
	if !strings.Contains(summary, "3 findings") {
		t.Errorf("summary should contain preceding content, got: %q", summary)
	}
}

func TestCodexDetectTaskCompletion_BusReplyError(t *testing.T) {
	p := &CodexProvider{}

	pane := `Running build...
compilation failed
$ muxcode send edit response "Build failed: compilation errors"
Sent error:response to edit`

	completed, errored, summary := p.DetectTaskCompletion("session", "review", pane)
	if !completed {
		t.Error("expected completed=true for bus error reply")
	}
	if !errored {
		t.Error("expected errored=true for error reply")
	}
	if !strings.Contains(summary, "compilation failed") {
		t.Errorf("summary should contain preceding content, got: %q", summary)
	}
}

func TestCodexDetectTaskCompletion_TUIPromptReappears(t *testing.T) {
	p := &CodexProvider{}

	pane := `Running review...
Found 3 issues in provider_codex.go
›`

	completed, errored, summary := p.DetectTaskCompletion("session", "review", pane)
	if !completed {
		t.Error("expected completed=true when TUI prompt reappears")
	}
	if errored {
		t.Error("expected errored=false")
	}
	if !strings.Contains(summary, "3 issues") {
		t.Errorf("summary should contain preceding content, got: %q", summary)
	}
}

func TestCodexDetectTaskCompletion_IntermediateOutput_NoFalsePositive(t *testing.T) {
	p := &CodexProvider{}

	// "Done", "completed", "Applied", "✓" in intermediate output should NOT
	// trigger false completion — only explicit markers count.
	pane := `Applied patch to file1.go
Done with step 1
✓ completed step 2
Processing step 3...`

	completed, _, _ := p.DetectTaskCompletion("session", "review", pane)
	if completed {
		t.Error("expected completed=false — intermediate tokens should not trigger completion")
	}
}

func TestCodexDetectTaskCompletion_ActiveSpinner(t *testing.T) {
	p := &CodexProvider{}

	pane := `Running analysis...
⠋ Processing files...`

	completed, _, _ := p.DetectTaskCompletion("session", "review", pane)
	if completed {
		t.Error("expected completed=false when spinner is active")
	}
}

func TestCodexDetectTaskCompletion_Empty(t *testing.T) {
	p := &CodexProvider{}
	completed, errored, summary := p.DetectTaskCompletion("session", "review", "")
	if completed || errored || summary != "" {
		t.Error("empty pane should return all zero values")
	}
}

func TestCodexDetectTaskCompletion_NoMarker(t *testing.T) {
	p := &CodexProvider{}

	pane := `codex -a never
Loading project...`

	completed, _, _ := p.DetectTaskCompletion("session", "review", pane)
	if completed {
		t.Error("expected completed=false when no completion marker")
	}
}

func TestCodexDetectTaskCompletion_BusReplyAfterSuccess(t *testing.T) {
	p := &CodexProvider{}

	// Bus reply after successful work — the "Sent" line triggers completion
	pane := `Tests: 42 passed, 0 failed
✓ All checks passed
$ muxcode send edit response "All 42 tests passed"
Sent response:response to edit`

	completed, errored, _ := p.DetectTaskCompletion("session", "review", pane)
	if !completed {
		t.Error("expected completed=true")
	}
	if errored {
		t.Error("expected errored=false for successful reply")
	}
}

// --- Model resolution ---

func TestRoleCodexModelEnvVar(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"review", "MUXCODE_REVIEW_CODEX_MODEL"},
		{"build", "MUXCODE_BUILD_CODEX_MODEL"},
		{"commit", "MUXCODE_COMMIT_CODEX_MODEL"},
		{"git", "MUXCODE_COMMIT_CODEX_MODEL"},
		{"deploy", "MUXCODE_DEPLOY_CODEX_MODEL"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if got := RoleCodexModelEnvVar(tt.role); got != tt.want {
				t.Errorf("RoleCodexModelEnvVar(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestRoleCodexModelDefault(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"review", "gpt-5.5"},
		{"analyze", "gpt-5.5"},
		{"build", "gpt-5.5"},
		{"test", "gpt-5.5"},
		{"commit", "gpt-5.5"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if got := RoleCodexModelDefault(tt.role); got != tt.want {
				t.Errorf("RoleCodexModelDefault(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestResolveCodexModel_Default(t *testing.T) {
	t.Setenv(RoleModelEnvVar("review"), "") // clear generic model env var
	t.Setenv("MUXCODE_REVIEW_CODEX_MODEL", "")
	t.Setenv("MUXCODE_CODEX_MODEL", "")

	model := resolveCodexModel("review")
	if model != "gpt-5.5" {
		t.Errorf("model = %q, want gpt-5.5", model)
	}
}

func TestResolveCodexModel_PerRole(t *testing.T) {
	t.Setenv(RoleModelEnvVar("review"), "") // clear generic model env var
	t.Setenv("MUXCODE_REVIEW_CODEX_MODEL", "o3")

	model := resolveCodexModel("review")
	if model != "o3" {
		t.Errorf("model = %q, want o3", model)
	}
}

func TestResolveCodexModel_Global(t *testing.T) {
	t.Setenv(RoleModelEnvVar("review"), "") // clear generic model env var
	t.Setenv("MUXCODE_REVIEW_CODEX_MODEL", "")
	t.Setenv("MUXCODE_CODEX_MODEL", "gpt-4.1")

	model := resolveCodexModel("review")
	if model != "gpt-4.1" {
		t.Errorf("model = %q, want gpt-4.1", model)
	}
}

// --- WriteAgentConfig ---

func TestWriteCodexAgentConfig(t *testing.T) {
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	err := writeCodexAgentConfig("review")
	if err != nil {
		t.Fatalf("writeCodexAgentConfig failed: %v", err)
	}

	// Repo root: .codex/AGENTS.md (not per-role subdirectory)
	path := filepath.Join(".codex", "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	content := string(data)

	if !strings.Contains(content, "MuxCode Agent Instructions") {
		t.Error("missing header")
	}
	if !strings.Contains(content, "muxcode send") {
		t.Error("missing bus command reference")
	}
	if !strings.Contains(content, "edit") {
		t.Error("missing edit target")
	}
}

func TestWriteAgentConfig_CodexDispatch(t *testing.T) {
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	codex := &CodexProvider{}
	if err := codex.WriteAgentConfig("review"); err != nil {
		t.Errorf("Codex WriteAgentConfig: %v", err)
	}

	// Should create .codex/AGENTS.md at repo root (not per-role subdirectory)
	agentsPath := filepath.Join(".codex", "AGENTS.md")
	if _, err := os.Stat(agentsPath); err != nil {
		t.Error("Codex WriteAgentConfig should create .codex/AGENTS.md")
	}

	// Content should include role name and reply protocol
	data, _ := os.ReadFile(agentsPath)
	content := string(data)
	if !strings.Contains(content, "**review**") {
		t.Error("AGENTS.md should mention the role name")
	}
	if !strings.Contains(content, "CRITICAL: Reply Protocol") {
		t.Error("AGENTS.md should include reply protocol instructions")
	}

	// Should NOT create .opencode/ or .claude/ directories
	if _, err := os.Stat(filepath.Join(".opencode", "agents")); err == nil {
		t.Error("Codex WriteAgentConfig should not create .opencode/ files")
	}
	if _, err := os.Stat(filepath.Join(".claude", "agents")); err == nil {
		t.Error("Codex WriteAgentConfig should not create .claude/ files")
	}
}

// --- Config coexistence ---

func TestConfigCoexistence_AllThreeProviders(t *testing.T) {
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	// Create Claude agent dir
	claudeDir := filepath.Join(".claude", "agents")
	os.MkdirAll(claudeDir, 0o755)
	os.WriteFile(filepath.Join(claudeDir, "code-editor.md"), []byte("# Claude edit\n"), 0o644)

	// Generate OpenCode agent config
	if err := writeOpenCodeAgentConfig("build"); err != nil {
		t.Fatalf("writeOpenCodeAgentConfig: %v", err)
	}

	// Generate Codex agent config
	if err := writeCodexAgentConfig("review"); err != nil {
		t.Fatalf("writeCodexAgentConfig: %v", err)
	}

	// All three directories should coexist
	if _, err := os.Stat(filepath.Join(".claude", "agents", "code-editor.md")); err != nil {
		t.Error("Claude agent file missing")
	}
	if _, err := os.Stat(filepath.Join(".opencode", "agents", "build.md")); err != nil {
		t.Error("OpenCode agent file missing")
	}
	if _, err := os.Stat(filepath.Join(".codex", "AGENTS.md")); err != nil {
		t.Error("Codex .codex/AGENTS.md missing")
	}

	// Claude file should be unmodified
	data, _ := os.ReadFile(filepath.Join(".claude", "agents", "code-editor.md"))
	if string(data) != "# Claude edit\n" {
		t.Error("Claude agent file was modified")
	}
}

func TestWriteCodexAgentConfig_LastWriterWins(t *testing.T) {
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	agentsPath := filepath.Join(".codex", "AGENTS.md")

	// Write config for review first
	if err := writeCodexAgentConfig("review"); err != nil {
		t.Fatalf("writeCodexAgentConfig(review): %v", err)
	}
	reviewData, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md after review write: %v", err)
	}
	if !strings.Contains(string(reviewData), "**review**") {
		t.Error("AGENTS.md should mention review role after first write")
	}
	if !strings.Contains(string(reviewData), "muxcode send") {
		t.Error("AGENTS.md missing bus commands")
	}

	// Write config for build — should overwrite review content
	if err := writeCodexAgentConfig("build"); err != nil {
		t.Fatalf("writeCodexAgentConfig(build): %v", err)
	}
	buildData, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md after build write: %v", err)
	}
	if !strings.Contains(string(buildData), "**build**") {
		t.Error("AGENTS.md should mention build role after second write")
	}
	// Last writer wins — review content should be replaced
	if strings.Contains(string(buildData), "**review**") {
		t.Error("AGENTS.md should not contain review role after build overwrite")
	}
}

func TestCodexAgentConfigDir(t *testing.T) {
	if got := CodexAgentConfigDir("review"); got != filepath.Join(".codex", "review") {
		t.Errorf("CodexAgentConfigDir(review) = %q, want .codex/review", got)
	}
	if got := CodexAgentConfigDir("build"); got != filepath.Join(".codex", "build") {
		t.Errorf("CodexAgentConfigDir(build) = %q, want .codex/build", got)
	}
}

// --- Mixed provider status ---

func TestAgentStatus_CodexProvider(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_REVIEW_CLI", "codex")
	session := t.TempDir()

	status := GetAgentStatus(session, "review")
	if status.Provider != "codex" {
		t.Errorf("review provider = %q, want codex", status.Provider)
	}
}

func TestAgentStatus_MixedWithCodex(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_EDIT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "opencode")
	t.Setenv("MUXCODE_REVIEW_CLI", "codex")
	t.Setenv("MUXCODE_TEST_CLI", "local")
	session := t.TempDir()

	statuses := GetAllAgentStatus(session)
	providerMap := make(map[string]string)
	for _, s := range statuses {
		providerMap[s.Role] = s.Provider
	}

	if providerMap["edit"] != "claude" {
		t.Errorf("edit provider = %q, want claude", providerMap["edit"])
	}
	if providerMap["build"] != "opencode" {
		t.Errorf("build provider = %q, want opencode", providerMap["build"])
	}
	if providerMap["review"] != "codex" {
		t.Errorf("review provider = %q, want codex", providerMap["review"])
	}
	if providerMap["test"] != "local" {
		t.Errorf("test provider = %q, want local", providerMap["test"])
	}
}

// --- Hook gating ---

func TestSupportsHooks_CodexFalse(t *testing.T) {
	providers := map[string]Provider{
		"claude":   &ClaudeCodeProvider{},
		"opencode": &OpenCodeProvider{},
		"codex":    &CodexProvider{},
		"local":    &LocalProvider{},
	}

	for name, p := range providers {
		expectHooks := name == "claude"
		if p.SupportsHooks() != expectHooks {
			t.Errorf("%s.SupportsHooks() = %v, want %v", name, p.SupportsHooks(), expectHooks)
		}
	}
}

// --- SendWakeUp ---

func TestCodexWakeUp_NoTmux(t *testing.T) {
	// SendWakeUp tries send-keys — should fail gracefully without tmux session
	p := &CodexProvider{}
	// Should not panic
	_ = p.SendWakeUp("nonexistent-session", "review", false)
}

// --- IsIdle ---

func TestCodexIsIdle_AlwaysFalse(t *testing.T) {
	// TUI mode always returns false for IsIdle
	p := &CodexProvider{}
	if p.IsIdle("nonexistent-session", "review") {
		t.Error("IsIdle should always return false for TUI mode")
	}
}
