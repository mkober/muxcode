package bus

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDefaultConfig_HasAllRoles(t *testing.T) {
	cfg := DefaultConfig()

	// All known roles should have a tool profile
	want := []string{"build", "test", "review", "git", "deploy", "runner", "analyst", "edit", "docs", "research", "watch", "pr-fix"}
	for _, role := range want {
		if _, ok := cfg.ToolProfiles[role]; !ok {
			t.Errorf("DefaultConfig missing tool profile for role %q", role)
		}
	}
}

func TestResolveTools_Build(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	tools := ResolveTools("build")
	if len(tools) == 0 {
		t.Fatal("ResolveTools(build) returned empty list")
	}

	// Should include shared bus tools
	assertContains(t, tools, "Bash(muxcode-agent-bus *)")
	// Should include shared readonly tools
	assertContains(t, tools, "Read")
	assertContains(t, tools, "Glob")
	assertContains(t, tools, "Grep")
	// Should include shared common tools
	assertContains(t, tools, "Bash(ls*)")
	// Should include build-specific tools
	assertContains(t, tools, "Bash(./build.sh*)")
	assertContains(t, tools, "Bash(make*)")
	// Should include cd-prefix variants for build tools
	assertContains(t, tools, "Bash(cd * && ./build.sh*)")
	assertContains(t, tools, "Bash(cd * && make*)")
	assertContains(t, tools, "Bash(cd * && go build*)")
}

func TestResolveTools_Edit(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	tools := ResolveTools("edit")
	if len(tools) == 0 {
		t.Fatal("ResolveTools(edit) returned empty list")
	}

	// Should include shared readonly tools
	assertContains(t, tools, "Read")
	assertContains(t, tools, "Glob")
	assertContains(t, tools, "Grep")
	// Should include Write and Edit
	assertContains(t, tools, "Write")
	assertContains(t, tools, "Edit")
	// Should NOT include any git/gh commands (all delegated to commit agent)
	assertNotContains(t, tools, "Bash(git diff*)")
	assertNotContains(t, tools, "Bash(git log*)")
	assertNotContains(t, tools, "Bash(git status*)")
	assertNotContains(t, tools, "Bash(gh *)")
	// Should NOT include build/test/deploy commands
	assertNotContains(t, tools, "Bash(./build.sh*)")
	assertNotContains(t, tools, "Bash(go test*)")
	assertNotContains(t, tools, "Bash(cdk *)")
	// Should NOT have cd-prefix variants (CdPrefix: false)
	assertNotContains(t, tools, "Bash(cd * && tree *)")
}

func TestResolveTools_Git(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	tools := ResolveTools("git")
	if len(tools) == 0 {
		t.Fatal("ResolveTools(git) returned empty list")
	}

	// Should include broad git/gh patterns
	assertContains(t, tools, "Bash(git *)")
	assertContains(t, tools, "Bash(gh *)")
	// Should include Write and Edit for conflict resolution
	assertContains(t, tools, "Write")
	assertContains(t, tools, "Edit")
	// Should NOT have bare Bash (unrestricted)
	assertNotContains(t, tools, "Bash")
	// Should have cd-prefix variants (CdPrefix: true)
	assertContains(t, tools, "Bash(cd * && git *)")
	assertContains(t, tools, "Bash(cd * && gh *)")
}

func TestResolveTools_Watch(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	tools := ResolveTools("watch")
	if len(tools) == 0 {
		t.Fatal("ResolveTools(watch) returned empty list")
	}

	// Should include log-watching tools
	assertContains(t, tools, "Bash(tail *)")
	assertContains(t, tools, "Bash(aws logs*)")
	assertContains(t, tools, "Bash(kubectl logs*)")
	assertContains(t, tools, "Bash(docker logs*)")
	assertContains(t, tools, "Bash(stern *)")
	assertContains(t, tools, "Bash(journalctl *)")
	// Should include shared readonly tools
	assertContains(t, tools, "Read")
	assertContains(t, tools, "Glob")
	assertContains(t, tools, "Grep")
	// Should include shared bus tools
	assertContains(t, tools, "Bash(muxcode-agent-bus *)")
	// Should NOT include Write/Edit (read-only agent)
	assertNotContains(t, tools, "Write")
	assertNotContains(t, tools, "Edit")
	// Should NOT include git commands
	assertNotContains(t, tools, "Bash(git *)")
	// Should have cd-prefix variants (CdPrefix: true)
	assertContains(t, tools, "Bash(cd * && tail *)")
	assertContains(t, tools, "Bash(cd * && aws logs*)")
	assertContains(t, tools, "Bash(cd * && kubectl logs*)")
}

func TestResolveTools_PrFix(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	tools := ResolveTools("pr-fix")
	if len(tools) == 0 {
		t.Fatal("ResolveTools(pr-fix) returned empty list")
	}

	// Should include Write and Edit for code fixes
	assertContains(t, tools, "Write")
	assertContains(t, tools, "Edit")
	// Should include scoped gh pr commands
	assertContains(t, tools, "Bash(gh pr view*)")
	assertContains(t, tools, "Bash(gh pr checks*)")
	assertContains(t, tools, "Bash(gh pr diff*)")
	assertContains(t, tools, "Bash(gh api *)")
	// Should include read-only git commands
	assertContains(t, tools, "Bash(git diff*)")
	assertContains(t, tools, "Bash(git log*)")
	assertContains(t, tools, "Bash(git status*)")
	assertContains(t, tools, "Bash(git show*)")
	assertContains(t, tools, "Bash(git blame*)")
	// Should include shared readonly tools
	assertContains(t, tools, "Read")
	assertContains(t, tools, "Glob")
	assertContains(t, tools, "Grep")
	// Should include shared bus tools
	assertContains(t, tools, "Bash(muxcode-agent-bus *)")
	// Should NOT include broad gh or git patterns
	assertNotContains(t, tools, "Bash(gh *)")
	assertNotContains(t, tools, "Bash(git *)")
	// Should NOT include git commit/add (commit agent's job)
	assertNotContains(t, tools, "Bash(git commit*)")
	assertNotContains(t, tools, "Bash(git add*)")
	assertNotContains(t, tools, "Bash(git push*)")
	// Should have cd-prefix variants (CdPrefix: true)
	assertContains(t, tools, "Bash(cd * && gh pr view*)")
	assertContains(t, tools, "Bash(cd * && git diff*)")
}

func TestResolveTools_NoCdPrefix(t *testing.T) {
	cfg := &MuxcodeConfig{
		SharedTools: map[string][]string{
			"bus": {"Bash(muxcode-agent-bus *)"},
		},
		ToolProfiles: map[string]ToolProfile{
			"custom": {
				Include:  []string{"bus"},
				Tools:    []string{"Bash(echo *)"},
				CdPrefix: false,
			},
		},
	}
	SetConfig(cfg)
	defer SetConfig(nil)

	tools := ResolveTools("custom")
	assertContains(t, tools, "Bash(muxcode-agent-bus *)")
	assertContains(t, tools, "Bash(echo *)")
	// Should NOT have cd-prefix variant
	assertNotContains(t, tools, "Bash(cd * && echo *)")
}

func TestResolveTools_UnknownRole(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	tools := ResolveTools("nonexistent")
	if tools != nil {
		t.Errorf("expected nil for unknown role, got %v", tools)
	}
}

func TestResolveTools_NoDuplicates(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	tools := ResolveTools("build")
	seen := make(map[string]bool)
	for _, tool := range tools {
		if seen[tool] {
			t.Errorf("duplicate tool in resolved list: %q", tool)
		}
		seen[tool] = true
	}
}

func TestExpandCdPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Bash(git *)", "Bash(cd * && git *)"},
		{"Bash(./build.sh*)", "Bash(cd * && ./build.sh*)"},
		{"Bash(go test*)", "Bash(cd * && go test*)"},
		// Non-Bash tools return empty
		{"Read", ""},
		{"Glob", ""},
		// Already-prefixed tools return empty
		{"Bash(cd * && git *)", ""},
		{"Bash(cd /tmp && ls)", ""},
		// Malformed patterns return empty (no panic)
		{"Bash(missing paren", ""},
		{"Bash(", ""},
	}
	for _, tt := range tests {
		got := expandCdPrefix(tt.input)
		if got != tt.want {
			t.Errorf("expandCdPrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLoadConfig_FallbackToDefault(t *testing.T) {
	// Set config dir to a temp dir with no files
	tmpDir := t.TempDir()
	os.Setenv("MUXCODE_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("MUXCODE_CONFIG_DIR")

	// Save and restore working dir to avoid .muxcode/muxcode.json interference
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	SetConfig(nil) // clear singleton

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Should have all default profiles (12 roles: build, test, review, git, deploy, runner, analyst, edit, docs, research, watch, pr-fix)
	if len(cfg.ToolProfiles) < 12 {
		t.Errorf("expected at least 12 tool profiles, got %d", len(cfg.ToolProfiles))
	}
}

func TestLoadConfig_ProjectOverride(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("MUXCODE_CONFIG_DIR", tmpDir)
	defer os.Unsetenv("MUXCODE_CONFIG_DIR")

	// Create project-local config
	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(projectDir, ".muxcode"), 0755)
	projectConfig := `{
		"tool_profiles": {
			"build": {
				"include": ["bus"],
				"tools": ["Bash(custom-build*)"],
				"cd_prefix": false
			}
		}
	}`
	os.WriteFile(filepath.Join(projectDir, ".muxcode", "muxcode.json"), []byte(projectConfig), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(projectDir)
	defer os.Chdir(origDir)

	SetConfig(nil)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Build profile should be overridden
	profile := cfg.ToolProfiles["build"]
	assertContainsStr(t, profile.Tools, "Bash(custom-build*)")
	if profile.CdPrefix {
		t.Error("expected CdPrefix=false from project override")
	}

	// Other profiles should still exist from defaults
	if _, ok := cfg.ToolProfiles["test"]; !ok {
		t.Error("expected default test profile to survive merge")
	}
}

func TestMergeConfigs(t *testing.T) {
	base := &MuxcodeConfig{
		SharedTools: map[string][]string{
			"bus": {"Bash(bus *)"},
		},
		ToolProfiles: map[string]ToolProfile{
			"build": {Tools: []string{"Bash(make*)"}},
			"test":  {Tools: []string{"Bash(jest*)"}},
		},
		EventChains: map[string]EventChain{
			"build": {NotifyAnalyst: true},
		},
		AutoCC: []string{"build"},
	}

	override := &MuxcodeConfig{
		SharedTools: map[string][]string{
			"bus": {"Bash(custom-bus *)"},
		},
		ToolProfiles: map[string]ToolProfile{
			"build": {Tools: []string{"Bash(custom-build*)"}},
		},
		EventChains: map[string]EventChain{},
		AutoCC:      []string{"build", "test"},
	}

	result := mergeConfigs(base, override)

	// Bus shared tools should be overridden
	if !reflect.DeepEqual(result.SharedTools["bus"], []string{"Bash(custom-bus *)"}) {
		t.Errorf("shared_tools.bus not overridden: %v", result.SharedTools["bus"])
	}

	// Build profile overridden
	if !reflect.DeepEqual(result.ToolProfiles["build"].Tools, []string{"Bash(custom-build*)"}) {
		t.Errorf("build profile not overridden: %v", result.ToolProfiles["build"].Tools)
	}

	// Test profile preserved from base
	if !reflect.DeepEqual(result.ToolProfiles["test"].Tools, []string{"Bash(jest*)"}) {
		t.Errorf("test profile not preserved: %v", result.ToolProfiles["test"].Tools)
	}

	// Build chain preserved from base
	if !result.EventChains["build"].NotifyAnalyst {
		t.Error("build chain not preserved from base")
	}

	// AutoCC overridden
	sort.Strings(result.AutoCC)
	if !reflect.DeepEqual(result.AutoCC, []string{"build", "test"}) {
		t.Errorf("auto_cc not overridden: %v", result.AutoCC)
	}
}

func TestResolveChain_BuildSuccess(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	action := ResolveChain("build", "success")
	if action == nil {
		t.Fatal("expected chain action for build success")
	}
	if action.SendTo != "test" {
		t.Errorf("send_to = %q, want test", action.SendTo)
	}
	if action.Action != "test" {
		t.Errorf("action = %q, want test", action.Action)
	}
	if action.Type != "request" {
		t.Errorf("type = %q, want request", action.Type)
	}
}

func TestResolveChain_TestFailure(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	action := ResolveChain("test", "failure")
	if action == nil {
		t.Fatal("expected chain action for test failure")
	}
	if action.SendTo != "edit" {
		t.Errorf("send_to = %q, want edit", action.SendTo)
	}
	if action.Type != "event" {
		t.Errorf("type = %q, want event", action.Type)
	}
}

func TestResolveChain_NoChain(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	action := ResolveChain("deploy", "success")
	if action != nil {
		t.Errorf("expected nil for deploy success, got %+v", action)
	}
}

func TestChainNotifyAnalyst(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	if !ChainNotifyAnalyst("build") {
		t.Error("expected notify_analyst=true for build")
	}
	if !ChainNotifyAnalyst("test") {
		t.Error("expected notify_analyst=true for test")
	}
	if ChainNotifyAnalyst("deploy") {
		t.Error("expected notify_analyst=false for deploy (no chain)")
	}
}

func TestExpandMessage(t *testing.T) {
	tests := []struct {
		template string
		exitCode string
		command  string
		want     string
	}{
		{
			"Build FAILED (exit ${exit_code}): ${command} — check build window",
			"1",
			"./build.sh",
			"Build FAILED (exit 1): ./build.sh — check build window",
		},
		{
			"Build succeeded — run tests",
			"0",
			"make",
			"Build succeeded — run tests",
		},
		{
			"${command} exited ${exit_code}",
			"2",
			"go test ./...",
			"go test ./... exited 2",
		},
	}
	for _, tt := range tests {
		got := ExpandMessage(tt.template, tt.exitCode, tt.command)
		if got != tt.want {
			t.Errorf("ExpandMessage(%q, %q, %q) = %q, want %q",
				tt.template, tt.exitCode, tt.command, got, tt.want)
		}
	}
}

func TestGetAutoCC(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	cc := GetAutoCC()
	if !cc["build"] {
		t.Error("expected build in auto_cc")
	}
	if !cc["test"] {
		t.Error("expected test in auto_cc")
	}
	if !cc["review"] {
		t.Error("expected review in auto_cc")
	}
	if cc["edit"] {
		t.Error("edit should not be in auto_cc")
	}
}

func TestGetAutoCC_Custom(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoCC = []string{"build", "deploy"}
	SetConfig(cfg)
	defer SetConfig(nil)

	cc := GetAutoCC()
	if !cc["build"] {
		t.Error("expected build in custom auto_cc")
	}
	if !cc["deploy"] {
		t.Error("expected deploy in custom auto_cc")
	}
	if cc["review"] {
		t.Error("review should not be in custom auto_cc")
	}
}

func TestCheckSendPolicy_Denied(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	msg := CheckSendPolicy("build", "test")
	if msg == "" {
		t.Error("expected deny message for build → test, got empty")
	}

	msg = CheckSendPolicy("test", "review")
	if msg == "" {
		t.Error("expected deny message for test → review, got empty")
	}
}

func TestCheckSendPolicy_Allowed(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	msg := CheckSendPolicy("build", "edit")
	if msg != "" {
		t.Errorf("expected empty for build → edit, got %q", msg)
	}

	msg = CheckSendPolicy("test", "edit")
	if msg != "" {
		t.Errorf("expected empty for test → edit, got %q", msg)
	}

	msg = CheckSendPolicy("edit", "build")
	if msg != "" {
		t.Errorf("expected empty for edit → build, got %q", msg)
	}
}

func TestCheckSendPolicy_NilPolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SendPolicy = nil
	SetConfig(cfg)
	defer SetConfig(nil)

	msg := CheckSendPolicy("build", "test")
	if msg != "" {
		t.Errorf("expected empty for nil policy, got %q", msg)
	}
}

// helpers

func assertContains(t *testing.T, tools []string, want string) {
	t.Helper()
	for _, tool := range tools {
		if tool == want {
			return
		}
	}
	t.Errorf("tools list missing %q", want)
}

func assertNotContains(t *testing.T, tools []string, unwanted string) {
	t.Helper()
	for _, tool := range tools {
		if tool == unwanted {
			t.Errorf("tools list should not contain %q", unwanted)
			return
		}
	}
}

func assertContainsStr(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("slice missing %q, got %v", want, slice)
}
