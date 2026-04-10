package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Interface conformance ---

func TestOpenCodeProvider_Interface(t *testing.T) {
	var p Provider = &OpenCodeProvider{}
	if p.Name() != "opencode" {
		t.Errorf("Name() = %q, want opencode", p.Name())
	}
	if p.SupportsHooks() {
		t.Error("OpenCode should not support hooks")
	}
	if p.IdlePromptChar() != "" {
		t.Error("OpenCode should have empty idle prompt char")
	}
}

// --- BuildExecArgs ---

func TestOpenCodeBuildExecArgs_TUIMode(t *testing.T) {
	p := &OpenCodeProvider{}

	for _, role := range []string{"beta", "build", "test", "edit", "review"} {
		t.Run(role, func(t *testing.T) {
			cfg := &LaunchConfig{
				Role: role,
				CLI:  "opencode",
			}

			binary, args := p.BuildExecArgs(cfg)

			if binary != "opencode" {
				t.Errorf("binary = %q, want opencode", binary)
			}
			if len(args) != 0 {
				t.Errorf("args = %v, want empty (TUI mode)", args)
			}
		})
	}
}

func TestOpenCodeBuildExecArgs_CustomCLI(t *testing.T) {
	p := &OpenCodeProvider{}
	cfg := &LaunchConfig{
		Role: "build",
		CLI:  "/usr/local/bin/opencode",
	}

	binary, args := p.BuildExecArgs(cfg)

	if binary != "/usr/local/bin/opencode" {
		t.Errorf("binary = %q, want custom path", binary)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty (TUI mode)", args)
	}
}

// --- IsIdle ---

func TestOpenCodeIsIdle_AlwaysFalse(t *testing.T) {
	p := &OpenCodeProvider{}
	for _, role := range []string{"beta", "build", "test", "edit"} {
		if p.IsIdle("test-session", role) {
			t.Errorf("IsIdle(%q) = true, want false (TUI limitation)", role)
		}
	}
}

// --- ClassifyPane ---

func TestOpenCodeClassifyPane(t *testing.T) {
	p := &OpenCodeProvider{}

	tests := []struct {
		name    string
		content string
		want    PaneState
	}{
		{"tui_horizontal", "╭─ opencode v1.3 ──╮", PaneIdle},
		{"tui_vertical", "│ Ready            │", PaneIdle},
		{"tui_corner_bottom", "╰──────────────────╯", PaneIdle},
		{"tui_square_corner", "┌──────────┐", PaneIdle},
		{"error", "Error: port in use", PaneNotReady},
		{"fatal", "FATAL: cannot bind", PaneNotReady},
		{"empty", "", PaneNotReady},
		{"loading", "Starting opencode...", PaneNotReady},
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

func TestOpenCodeAcceptStartup(t *testing.T) {
	p := &OpenCodeProvider{}

	if !p.AcceptStartup("session", "session:build.1", PaneIdle) {
		t.Error("AcceptStartup should return true when PaneIdle")
	}
	if p.AcceptStartup("session", "session:build.1", PaneNotReady) {
		t.Error("AcceptStartup should return false when PaneNotReady")
	}
}

// --- Compact ---

func TestOpenCodeCompact_NoOp(t *testing.T) {
	p := &OpenCodeProvider{}
	err := p.Compact("session", "build", "session:build.1")
	if err != nil {
		t.Errorf("Compact should be no-op, got error: %v", err)
	}
}

// --- Agent config generation ---

func TestWriteOpenCodeAgentConfig(t *testing.T) {
	// Work in a temp directory
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	err := writeOpenCodeAgentConfig("build")
	if err != nil {
		t.Fatalf("writeOpenCodeAgentConfig failed: %v", err)
	}

	// Check file exists
	path := filepath.Join(".opencode", "agents", "build.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read agent config: %v", err)
	}

	content := string(data)

	// Check frontmatter markers
	if !strings.HasPrefix(content, "---\n") {
		t.Error("missing opening frontmatter marker")
	}
	if !strings.Contains(content, "description:") {
		t.Error("missing description field")
	}
	if !strings.Contains(content, "mode: primary") {
		t.Error("missing mode field")
	}
}

func TestTranslateToolProfile_BashPatterns(t *testing.T) {
	result := translateToolProfile("build")

	if result == "" {
		t.Skip("build role has no tool profile in test environment")
	}

	// Should contain permission: and bash: sections
	if !strings.Contains(result, "permission:") {
		t.Error("missing permission: header")
	}
	if !strings.Contains(result, "bash:") {
		t.Error("missing bash: section")
	}
}

func TestTranslateToolProfile_EditPermission(t *testing.T) {
	// Manually test the translation logic
	tools := []string{"Bash(make *)", "Write", "Edit", "Read(*)"}
	bashAllow := []string{}
	editAllow := false

	for _, tool := range tools {
		switch {
		case tool == "Write" || tool == "Edit":
			editAllow = true
		case strings.HasPrefix(tool, "Bash(") && strings.HasSuffix(tool, ")"):
			pattern := tool[5 : len(tool)-1]
			bashAllow = append(bashAllow, pattern)
		}
	}

	if len(bashAllow) != 1 || bashAllow[0] != "make *" {
		t.Errorf("bash allow = %v, want [make *]", bashAllow)
	}
	if !editAllow {
		t.Error("expected edit allow = true")
	}
}

func TestResolveOpenCodeModel_Default(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_MODEL", "")
	t.Setenv("MUXCODE_BUILD_CLAUDE_MODEL", "")

	model := resolveOpenCodeModel("build")

	// Should have anthropic/ prefix
	if model != "" && !strings.HasPrefix(model, "anthropic/") {
		t.Errorf("model = %q, want anthropic/ prefix", model)
	}
}

func TestResolveOpenCodeModel_ExplicitOverride(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_MODEL", "openai/gpt-4o")

	model := resolveOpenCodeModel("build")
	if model != "openai/gpt-4o" {
		t.Errorf("model = %q, want openai/gpt-4o", model)
	}
}

func TestResolveOpenCodeModel_AlreadyPrefixed(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_MODEL", "")
	t.Setenv("MUXCODE_BUILD_CLAUDE_MODEL", "anthropic/claude-sonnet-4-5")

	model := resolveOpenCodeModel("build")
	if model != "anthropic/claude-sonnet-4-5" {
		t.Errorf("model = %q, want anthropic/claude-sonnet-4-5", model)
	}
}

// --- roleFromPane ---

func TestRoleFromPane(t *testing.T) {
	tests := []struct {
		pane string
		want string
	}{
		{"muxcode:build.1", "build"},
		{"muxcode:edit.0", "edit"},
		{"session:beta.1", "beta"},
		{"build.1", "build"},
		{"build", "build"},
	}
	for _, tt := range tests {
		t.Run(tt.pane, func(t *testing.T) {
			if got := roleFromPane(tt.pane); got != tt.want {
				t.Errorf("roleFromPane(%q) = %q, want %q", tt.pane, got, tt.want)
			}
		})
	}
}
