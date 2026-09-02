package bus

import (
	"fmt"
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

	for _, role := range []string{"build", "test", "edit", "review"} {
		t.Run(role, func(t *testing.T) {
			// Clear per-role model env so defaults apply
			envKey := "MUXCODE_" + strings.ToUpper(role) + "_MODEL"
			t.Setenv(envKey, "")

			cfg := &LaunchConfig{
				Role: role,
				CLI:  "opencode",
			}

			binary, args := p.BuildExecArgs(cfg)

			if binary != "opencode" {
				t.Errorf("binary = %q, want opencode", binary)
			}
			// Must always start with --agent <role>
			if len(args) < 2 || args[0] != "--agent" || args[1] != role {
				t.Errorf("args = %v, want --agent %s as first two args", args, role)
			}
			// All roles should get a --model flag via resolveOpenCodeModel:
			// roles with an OpenCode default get that, others fall back to
			// the Claude model mapped to anthropic/ prefix.
			expectedModel := resolveOpenCodeModel(role)
			if expectedModel != "" {
				if len(args) != 4 || args[2] != "--model" || args[3] != expectedModel {
					t.Errorf("args = %v, want [--agent %s --model %s]", args, role, expectedModel)
				}
			} else {
				if len(args) != 2 {
					t.Errorf("args = %v, want [--agent %s] (no model resolved)", args, role)
				}
			}
		})
	}
}

func TestOpenCodeBuildExecArgs_CustomCLI(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_MODEL", "")
	p := &OpenCodeProvider{}
	cfg := &LaunchConfig{
		Role: "build",
		CLI:  "/usr/local/bin/opencode",
	}

	binary, args := p.BuildExecArgs(cfg)

	if binary != "/usr/local/bin/opencode" {
		t.Errorf("binary = %q, want custom path", binary)
	}
	// build role has a default model, so expect --model flag too
	defaultModel := RoleOpenCodeModelDefault("build")
	if len(args) < 2 || args[0] != "--agent" || args[1] != "build" {
		t.Errorf("args = %v, want --agent build as first two args", args)
	}
	if defaultModel != "" && (len(args) != 4 || args[2] != "--model" || args[3] != defaultModel) {
		t.Errorf("args = %v, want [--agent build --model %s]", args, defaultModel)
	}
}

func TestOpenCodeBuildExecArgs_ExplicitModel(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_MODEL", "gemma4")
	p := &OpenCodeProvider{}
	cfg := &LaunchConfig{
		Role: "build",
		CLI:  "opencode",
	}

	binary, args := p.BuildExecArgs(cfg)

	if binary != "opencode" {
		t.Errorf("binary = %q, want opencode", binary)
	}
	if len(args) != 4 || args[0] != "--agent" || args[1] != "build" || args[2] != "--model" || args[3] != "gemma4" {
		t.Errorf("args = %v, want [--agent build --model gemma4]", args)
	}
}

// --- IsIdle ---

func TestOpenCodeIsIdle_AlwaysFalse(t *testing.T) {
	p := &OpenCodeProvider{}
	for _, role := range []string{"build", "test", "edit"} {
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

func TestTranslateToolProfile_EditDenyRules(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	result := translateToolProfile("edit")

	if result == "" {
		t.Fatal("translateToolProfile(edit) returned empty string")
	}

	// Should have permission header and bash section
	if !strings.Contains(result, "permission:") {
		t.Error("missing permission: header")
	}
	if !strings.Contains(result, "bash:") {
		t.Error("missing bash: section")
	}

	// Should have edit: allow (Write/Edit in profile)
	if !strings.Contains(result, "edit: allow") {
		t.Error("missing edit: allow — Write/Edit tools should produce edit permission")
	}

	// Should have deny entries for prohibited commands
	denyPatterns := []string{
		"git commit*", "git push*", "gh *",
		"./build.sh*", "make*",
		"go test*", "pytest*",
		"cdk synth*",
		"aws lambda*", "aws s3*",
		"curl*",
	}
	for _, pattern := range denyPatterns {
		expected := fmt.Sprintf("\"%s\": deny", pattern)
		if !strings.Contains(result, expected) {
			t.Errorf("missing deny entry for %q", pattern)
		}
	}

	// Should have allow entries for legitimate edit tools
	allowPatterns := []string{"muxcode *", "tree *"}
	for _, pattern := range allowPatterns {
		expected := fmt.Sprintf("\"%s\": allow", pattern)
		if !strings.Contains(result, expected) {
			t.Errorf("missing allow entry for %q", pattern)
		}
	}
}

func TestTranslateToolProfile_NoDenyForBuild(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	result := translateToolProfile("build")

	// Build role should not have deny entries (no DenyTools configured)
	if strings.Contains(result, ": deny") {
		t.Error("build role should not have deny entries")
	}
}

func TestResolveOpenCodeModel_Default(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_MODEL", "")
	t.Setenv("MUXCODE_BUILD_CLAUDE_MODEL", "")

	model := resolveOpenCodeModel("build")

	// Command-execution roles default to MiniMax M3
	if model != "opencode-go/minimax-m3" {
		t.Errorf("model = %q, want opencode-go/minimax-m3", model)
	}
}

func TestResolveOpenCodeModel_CommitDefault(t *testing.T) {
	t.Setenv("MUXCODE_COMMIT_MODEL", "")
	t.Setenv("MUXCODE_COMMIT_CLAUDE_MODEL", "")

	model := resolveOpenCodeModel("commit")

	// Commit role defaults to MiniMax M3
	if model != "opencode-go/minimax-m3" {
		t.Errorf("model = %q, want opencode-go/minimax-m3", model)
	}
}

func TestResolveOpenCodeModel_ExplicitOverride(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_MODEL", "openai/gpt-4o")

	model := resolveOpenCodeModel("build")
	if model != "openai/gpt-4o" {
		t.Errorf("model = %q, want openai/gpt-4o", model)
	}
}

func TestResolveOpenCodeModel_ReviewDefault(t *testing.T) {
	t.Setenv("MUXCODE_REVIEW_MODEL", "")
	t.Setenv("MUXCODE_REVIEW_CLAUDE_MODEL", "")

	model := resolveOpenCodeModel("review")

	if model != "opencode-go/qwen3.7-plus" {
		t.Errorf("model = %q, want opencode-go/qwen3.7-plus", model)
	}
}

func TestResolveOpenCodeModel_AnalyzeDefault(t *testing.T) {
	t.Setenv("MUXCODE_ANALYZE_MODEL", "")
	t.Setenv("MUXCODE_ANALYZE_CLAUDE_MODEL", "")

	model := resolveOpenCodeModel("analyze")

	if model != "opencode-go/qwen3.7-plus" {
		t.Errorf("model = %q, want opencode-go/qwen3.7-plus", model)
	}
}

func TestResolveOpenCodeModel_AlreadyPrefixed(t *testing.T) {
	// Use "plan" role which has no OpenCode default, so Claude fallback applies
	t.Setenv("MUXCODE_PLAN_MODEL", "")
	t.Setenv("MUXCODE_PLAN_CLAUDE_MODEL", "anthropic/claude-sonnet-4-5")

	model := resolveOpenCodeModel("plan")
	if model != "anthropic/claude-sonnet-4-5" {
		t.Errorf("model = %q, want anthropic/claude-sonnet-4-5", model)
	}
}

func TestResolveOpenCodeModel_EditDefault(t *testing.T) {
	t.Setenv("MUXCODE_EDIT_MODEL", "")
	t.Setenv("MUXCODE_EDIT_CLAUDE_MODEL", "")

	model := resolveOpenCodeModel("edit")
	if model != "opencode-go/deepseek-v4-pro" {
		t.Errorf("model = %q, want opencode-go/deepseek-v4-pro", model)
	}
}

func TestResolveOpenCodeModel_EditOverride(t *testing.T) {
	t.Setenv("MUXCODE_EDIT_MODEL", "opencode-go/custom-model")

	model := resolveOpenCodeModel("edit")
	if model != "opencode-go/custom-model" {
		t.Errorf("model = %q, want opencode-go/custom-model", model)
	}
}

func TestAdaptBodyForNonHookProvider_Edit(t *testing.T) {
	hookGuard := "A PreToolUse hook (`muxcode hook guard`) enforces this at the tool level — prohibited commands are blocked before execution. Always delegate on the first attempt."
	chainRef := "**The automated chain stops at review.** After review completes, report the results and wait for the user."
	body := "Some preamble text.\n" + hookGuard + "\nMiddle text.\n" + chainRef + "\nEnd text."

	result := adaptBodyForNonHookProvider(body, "edit")

	// Original hook guard sentence should be replaced
	if strings.Contains(result, "enforces this at the tool level") {
		t.Error("original hook guard sentence was not replaced")
	}
	if !strings.Contains(result, "MUST self-enforce delegation rules") {
		t.Error("missing self-enforcement instruction")
	}

	// Automated chain reference should be replaced
	if strings.Contains(result, "The automated chain stops at review.") {
		t.Error("automated chain reference was not replaced")
	}
	if !strings.Contains(result, "MUST manually orchestrate") {
		t.Error("missing manual orchestration instruction")
	}

	// Surrounding text should be preserved
	if !strings.Contains(result, "Some preamble text.") {
		t.Error("preamble text was removed")
	}
	if !strings.Contains(result, "End text.") {
		t.Error("end text was removed")
	}
}

func TestAdaptBodyForNonHookProvider_EditNoEffect(t *testing.T) {
	// Body without the target strings should be unchanged
	body := "Just some regular agent body text without hook references."
	result := adaptBodyForNonHookProvider(body, "edit")
	if result != body {
		t.Errorf("body was modified when it shouldn't have been:\n%s", result)
	}
}

func TestAdaptBodyForNonHookProvider_BuildUnchanged(t *testing.T) {
	// Edit replacements should not affect the build role
	body := "A PreToolUse hook (`muxcode hook guard`) enforces this at the tool level."
	result := adaptBodyForNonHookProvider(body, "build")
	if result != body {
		t.Errorf("build body was incorrectly modified by edit replacements")
	}
}

func TestAdaptBodyForNonHookProvider_BuildNoContradiction(t *testing.T) {
	// The canonical agents/code-builder.md:77 line. For non-hook providers the
	// build agent MUST send the test request manually, so the whole clause must be
	// rewritten — a naive substring swap left "do NOT send a test request — send a
	// test request manually", a self-contradiction. Guard against its return.
	body := "- **Do NOT run tests yourself and do NOT send a test request — the bash hook auto-chains build->test on success. No `pnpm test`/`jest`/`go test`/`pytest`, not even to debug.**"
	result := adaptBodyForNonHookProvider(body, "build")

	if strings.Contains(result, "do NOT send a test request") {
		t.Errorf("contradictory 'do NOT send a test request' survived adaptation:\n%s", result)
	}
	if strings.Contains(result, "the bash hook auto-chains") {
		t.Errorf("hook-chain reference was not rewritten:\n%s", result)
	}
	if !strings.Contains(result, "send the test request manually") {
		t.Errorf("missing manual test-request instruction:\n%s", result)
	}
	if !strings.Contains(result, "muxcode send test test") {
		t.Errorf("missing concrete manual send command:\n%s", result)
	}
}

func TestAdaptBodyForNonHookProvider_TestNoContradiction(t *testing.T) {
	// The canonical agents/test-runner.md:16 + :21 lines. Both end in the same
	// hook-chain clause; a bare substring rule would non-deterministically mangle
	// the bolded :21 line into "do NOT send a review request — send a review
	// request manually". Verify full-line rewrites keep both coherent.
	line16 := "**Send exactly ONE reply per request. Do NOT send additional messages to edit or review — the bash hook auto-chains test->review on success.**"
	line21 := "- **Do NOT send a review request — the bash hook auto-chains test->review on success.**"
	result := adaptBodyForNonHookProvider(line16+"\n"+line21, "test")

	if strings.Contains(result, "the bash hook auto-chains") {
		t.Errorf("hook-chain reference was not rewritten:\n%s", result)
	}
	if strings.Contains(result, "Do NOT send a review request — send a review request manually") {
		t.Errorf("contradictory review instruction produced:\n%s", result)
	}
	if !strings.Contains(result, "muxcode send review review") {
		t.Errorf("missing concrete manual review send command:\n%s", result)
	}
}

func TestConfigureLaunch_EditStartupContext(t *testing.T) {
	provider := &OpenCodeProvider{}
	cfg := &LaunchConfig{Role: "edit"}

	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	provider.ConfigureLaunch(cfg, "edit")

	if !strings.Contains(cfg.SharedPrompt, "## Startup") {
		t.Error("missing startup section in edit SharedPrompt")
	}
	if !strings.Contains(cfg.SharedPrompt, "muxcode memory context") {
		t.Error("missing memory context command in startup instruction")
	}
}

func TestConfigureLaunch_NonEditNoStartup(t *testing.T) {
	provider := &OpenCodeProvider{}
	cfg := &LaunchConfig{Role: "build"}

	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	provider.ConfigureLaunch(cfg, "build")

	if strings.Contains(cfg.SharedPrompt, "## Startup") {
		t.Error("non-edit role should not have startup section")
	}
}

// --- Phase 4: mixed-provider config coexistence ---

func TestConfigCoexistence_ClaudeAndOpenCode(t *testing.T) {
	// Both .claude/ and .opencode/ directories should coexist without conflicts.
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	// Create .claude/agents/ (Claude Code's config directory)
	claudeDir := filepath.Join(".claude", "agents")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude/agents: %v", err)
	}
	// Write a Claude agent file
	claudeAgent := filepath.Join(claudeDir, "code-builder.md")
	os.WriteFile(claudeAgent, []byte("# Claude build agent\n"), 0o644)

	// Generate OpenCode agent config
	err := writeOpenCodeAgentConfig("build")
	if err != nil {
		t.Fatalf("writeOpenCodeAgentConfig: %v", err)
	}

	// Verify both directories exist independently
	opencodePath := filepath.Join(".opencode", "agents", "build.md")

	claudeData, err := os.ReadFile(claudeAgent)
	if err != nil {
		t.Fatalf("read .claude agent: %v", err)
	}
	if string(claudeData) != "# Claude build agent\n" {
		t.Error("Claude agent file was modified by OpenCode config generation")
	}

	opencodeData, err := os.ReadFile(opencodePath)
	if err != nil {
		t.Fatalf("read .opencode agent: %v", err)
	}
	if !strings.HasPrefix(string(opencodeData), "---\n") {
		t.Error("OpenCode agent missing frontmatter")
	}
}

func TestMultipleOpenCodeRoles(t *testing.T) {
	// Verify multiple roles can each get their own OpenCode agent config.
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	roles := []string{"build", "test", "review"}
	for _, role := range roles {
		if err := writeOpenCodeAgentConfig(role); err != nil {
			t.Fatalf("writeOpenCodeAgentConfig(%s): %v", role, err)
		}
	}

	// Verify each role has its own file
	for _, role := range roles {
		path := filepath.Join(".opencode", "agents", role+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("missing agent config for %s: %v", role, err)
			continue
		}
		content := string(data)
		if !strings.Contains(content, "description:") {
			t.Errorf("%s agent config missing description field", role)
		}
		if !strings.Contains(content, "mode: primary") {
			t.Errorf("%s agent config missing mode field", role)
		}
	}
}

func TestWriteAgentConfig_ProviderDispatch(t *testing.T) {
	// Claude: no-op. OpenCode: writes file. Local: no-op.
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	// Claude Code — should not create .opencode/
	claude := &ClaudeCodeProvider{}
	if err := claude.WriteAgentConfig("build"); err != nil {
		t.Errorf("Claude WriteAgentConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".opencode", "agents", "build.md")); err == nil {
		t.Error("Claude WriteAgentConfig should not create .opencode/ files")
	}

	// OpenCode — should create .opencode/agents/build.md
	opencode := &OpenCodeProvider{}
	if err := opencode.WriteAgentConfig("build"); err != nil {
		t.Errorf("OpenCode WriteAgentConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".opencode", "agents", "build.md")); err != nil {
		t.Error("OpenCode WriteAgentConfig should create .opencode/agents/build.md")
	}

	// Local — should not create any config files
	local := &LocalProvider{}
	if err := local.WriteAgentConfig("test"); err != nil {
		t.Errorf("Local WriteAgentConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".opencode", "agents", "test.md")); err == nil {
		t.Error("Local WriteAgentConfig should not create config files")
	}
}

func TestOpenCodeWakeUp_DisplayMessage(t *testing.T) {
	// SendWakeUp should not panic — it executes tmux display-message.
	// In test environment without tmux, it will fail gracefully.
	p := &OpenCodeProvider{}
	// We only verify it doesn't panic; the tmux command will error in CI.
	_ = p.SendWakeUp("test-session", "build", false)
}

// --- DetectTaskCompletion ---

func TestOpenCodeDetectTaskCompletion_Completed(t *testing.T) {
	p := &OpenCodeProvider{}

	// Simulate a completed build — real captured pane output
	pane := `
     Build completed successfully. Let me verify the outputs:

  ┃  $ ls -la bin/
  ┃  total 47288
  ┃  -rwxr-xr-x  1 user  staff  7715680 Apr 10 01:14 muxcode
  ┃  -rwxr-xr-x  1 user  staff  6134848 Apr 10 01:14 muxcode-llm-harness

     Build Succeeded

     Lint Status: ✅ All clean
     - gofmt -l .: No issues
     - go vet ./...: No issues

     ▣  Build · MiniMax M2.5 Free · 12.9s

  Build  MiniMax M2.5 Free OpenCode Zen
╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
                                19.2K (7%) · $0.01  ctrl+p commands
`
	completed, errored, summary := p.DetectTaskCompletion("session", "build", pane)
	if !completed {
		t.Error("expected completed=true for pane with stop marker")
	}
	if errored {
		t.Error("expected errored=false for successful build")
	}
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !strings.Contains(summary, "▣") {
		t.Error("summary should contain stop marker")
	}
}

func TestOpenCodeDetectTaskCompletion_ActiveRunning(t *testing.T) {
	p := &OpenCodeProvider{}

	// Simulate an active (still running) task
	pane := `
     Running build...

  ┃  $ make build
  ┃  go build -o bin/muxcode ./...

     ▸  Building... 5.2s

  Build  MiniMax M2.5 Free OpenCode Zen
╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
`
	completed, errored, _ := p.DetectTaskCompletion("session", "build", pane)
	if completed {
		t.Error("expected completed=false when running marker ▸ present")
	}
	if errored {
		t.Error("expected errored=false when still running")
	}
}

func TestOpenCodeDetectTaskCompletion_CompletedWithErrors(t *testing.T) {
	p := &OpenCodeProvider{}

	// Simulate a build that completed but had errors
	pane := `
     Running build...

  ┃  $ make build
  ┃  go build -o bin/muxcode ./...
  ┃  Error: compilation failed
  ┃  exit code 1

     Build Failed

     ▣  Build · MiniMax M2.5 Free · 8.1s

  Build  MiniMax M2.5 Free OpenCode Zen
╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
                                12.5K (5%) · $0.01  ctrl+p commands
`
	completed, errored, summary := p.DetectTaskCompletion("session", "build", pane)
	if !completed {
		t.Error("expected completed=true — stop marker present")
	}
	if !errored {
		t.Error("expected errored=true — error indicators in output")
	}
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestOpenCodeDetectTaskCompletion_EmptyPane(t *testing.T) {
	p := &OpenCodeProvider{}
	completed, errored, summary := p.DetectTaskCompletion("session", "build", "")
	if completed || errored || summary != "" {
		t.Error("empty pane should return all zero values")
	}
}

func TestOpenCodeDetectTaskCompletion_NoStopMarker(t *testing.T) {
	p := &OpenCodeProvider{}

	// Pane with content but no stop marker
	pane := `
  Build  MiniMax M2.5 Free OpenCode Zen
╹▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀
                                0K (0%) · $0.00  ctrl+p commands
`
	completed, _, _ := p.DetectTaskCompletion("session", "build", pane)
	if completed {
		t.Error("expected completed=false — no stop marker in pane")
	}
}

// --- isUIChrome ---

func TestIsUIChrome(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"─────────────", true},
		{"╭── header ──╮", true},
		{"╰────────────╯", true},
		{"╹▀▀▀▀▀▀▀▀▀▀▀▀", true},
		{"19.2K (7%) · $0.01  ctrl+p commands", true},
		{"Build succeeded", false},
		{"     ▣  Build · MiniMax M2.5 Free · 12.9s", false},
		{"", false},
		// Verticals and tees: OpenCode's most common frame runes. These were
		// absent from the original literal list, so whole transcript lines and
		// table rows leaked into synthesized task responses.
		{"│Hosted role guard  │Correctness fix│  Context", true},
		{"┃  $ muxcode send test response \"LGTM\"", true},
		{"├──────────────┼──────────────┤", true},
		{"┤ right tee", true},
		{"┬ top tee", true},
		{"┼ cross", true},
		{"▀ block element", true},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := isUIChrome(tt.line); got != tt.want {
				t.Errorf("isUIChrome(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

// --- Provider interface conformance for DetectTaskCompletion ---

func TestClaudeProvider_DetectTaskCompletion_NoOp(t *testing.T) {
	p := &ClaudeCodeProvider{}
	completed, errored, summary := p.DetectTaskCompletion("session", "build", "some content")
	if completed || errored || summary != "" {
		t.Error("Claude provider DetectTaskCompletion should be no-op")
	}
}

func TestLocalProvider_DetectTaskCompletion_NoOp(t *testing.T) {
	p := &LocalProvider{}
	completed, errored, summary := p.DetectTaskCompletion("session", "build", "some content")
	if completed || errored || summary != "" {
		t.Error("Local provider DetectTaskCompletion should be no-op")
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
		{"session:deploy.1", "deploy"},
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

// TestDetectTaskCompletion_ShellPromptIsNotCompletion guards against
// fabricating a result from a pane whose agent has exited. A dead pane still
// shows the previous task's stop marker, so without this the daemon reported a
// macOS login banner as a successful build.
func TestDetectTaskCompletion_ShellPromptIsNotCompletion(t *testing.T) {
	p := &OpenCodeProvider{}
	pane := strings.Join([]string{
		"To update your account to use zsh, please run `chsh -s /bin/zsh`.",
		"For more details, please visit https://support.apple.com/kb/HT208050.",
		"▣  Build · MiniMax M2.5 Free · 12.9s",
		"mkoberlein@PKHM-MKOBE /Users/mkoberlein/Repos/mkober/muxcode (main)",
		"->",
	}, "\n")

	completed, _, summary := p.DetectTaskCompletion("s", "build", pane)
	if completed {
		t.Errorf("a bare shell prompt must not report completion (summary=%q)", summary)
	}
}
