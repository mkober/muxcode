package bus

import (
	"strings"
	"testing"
)

// --- Interface conformance ---

func TestProviderInterface_Claude(t *testing.T) {
	var p Provider = &ClaudeCodeProvider{}
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want claude", p.Name())
	}
	if !p.SupportsHooks() {
		t.Error("Claude Code should support hooks")
	}
	if p.IdlePromptChar() != "❯" {
		t.Errorf("IdlePromptChar() = %q, want ❯", p.IdlePromptChar())
	}
}

func TestProviderInterface_OpenCode(t *testing.T) {
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

func TestProviderInterface_Local(t *testing.T) {
	var p Provider = &LocalProvider{}
	if p.Name() != "local" {
		t.Errorf("Name() = %q, want local", p.Name())
	}
	if p.SupportsHooks() {
		t.Error("Local should not support hooks")
	}
}

// --- ResolveProvider ---

func TestResolveProvider_DefaultClaude(t *testing.T) {
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	p := ResolveProvider("build")
	if p.Name() != "claude" {
		t.Errorf("default provider = %q, want claude", p.Name())
	}
}

func TestResolveProvider_PerRoleOpenCode(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_CLI", "opencode")

	p := ResolveProvider("build")
	if p.Name() != "opencode" {
		t.Errorf("provider = %q, want opencode", p.Name())
	}
}

func TestResolveProvider_PerRoleLocal(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_CLI", "local")

	p := ResolveProvider("build")
	if p.Name() != "local" {
		t.Errorf("provider = %q, want local", p.Name())
	}
}

func TestResolveProvider_SessionDefault(t *testing.T) {
	t.Setenv("MUXCODE_AGENT_CLI", "opencode")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	p := ResolveProvider("build")
	if p.Name() != "opencode" {
		t.Errorf("provider = %q, want opencode", p.Name())
	}
}

func TestResolveProvider_PerRoleOverridesSession(t *testing.T) {
	t.Setenv("MUXCODE_AGENT_CLI", "opencode")
	t.Setenv("MUXCODE_BUILD_CLI", "claude")

	p := ResolveProvider("build")
	if p.Name() != "claude" {
		t.Errorf("provider = %q, want claude", p.Name())
	}
}

func TestResolveProvider_BetaDefaultsToOpenCode(t *testing.T) {
	t.Setenv("MUXCODE_BETA_CLI", "")
	t.Setenv("MUXCODE_AGENT_CLI", "")

	p := ResolveProvider("beta")
	if p.Name() != "opencode" {
		t.Errorf("beta provider = %q, want opencode", p.Name())
	}
}

func TestResolveProvider_BetaOverrideToClaude(t *testing.T) {
	t.Setenv("MUXCODE_BETA_CLI", "claude")

	p := ResolveProvider("beta")
	if p.Name() != "claude" {
		t.Errorf("beta provider = %q, want claude", p.Name())
	}
}

func TestResolveProvider_UnknownCLIDefaultsToClaude(t *testing.T) {
	t.Setenv("MUXCODE_AGENT_CLI", "my-custom-claude")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	p := ResolveProvider("build")
	if p.Name() != "claude" {
		t.Errorf("unknown CLI should default to claude, got %q", p.Name())
	}
}

// --- ResolveProviderCLI ---

func TestResolveProviderCLI_Defaults(t *testing.T) {
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	if got := ResolveProviderCLI("build"); got != "claude" {
		t.Errorf("ResolveProviderCLI(build) = %q, want claude", got)
	}
}

func TestResolveProviderCLI_BetaDefault(t *testing.T) {
	t.Setenv("MUXCODE_BETA_CLI", "")
	t.Setenv("MUXCODE_AGENT_CLI", "")

	if got := ResolveProviderCLI("beta"); got != "opencode" {
		t.Errorf("ResolveProviderCLI(beta) = %q, want opencode", got)
	}
}

func TestResolveProviderCLI_PerRoleOverride(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_CLI", "opencode")

	if got := ResolveProviderCLI("build"); got != "opencode" {
		t.Errorf("ResolveProviderCLI(build) = %q, want opencode", got)
	}
}

func TestResolveProviderCLI_SessionDefault(t *testing.T) {
	t.Setenv("MUXCODE_AGENT_CLI", "opencode")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	if got := ResolveProviderCLI("build"); got != "opencode" {
		t.Errorf("ResolveProviderCLI(build) = %q, want opencode", got)
	}
}

// --- ClaudeCodeProvider ---

func TestClaudeClassifyPane(t *testing.T) {
	p := &ClaudeCodeProvider{}

	tests := []struct {
		name    string
		content string
		want    PaneState
	}{
		{"trust", "Do you trust this folder?", PaneTrustPrompt},
		{"bypass", "Bypass Permissions mode", PaneBypassPrompt},
		{"idle", "❯", PaneIdle},
		{"not ready", "Loading...", PaneNotReady},
		{"empty", "", PaneNotReady},
		{"trust takes precedence", "trust this folder\n❯", PaneTrustPrompt},
		{"bypass takes precedence", "Bypass Permissions\n❯", PaneBypassPrompt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.ClassifyPane(tt.content); got != tt.want {
				t.Errorf("ClassifyPane(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}

func TestClaudeWriteAgentConfig_NoOp(t *testing.T) {
	p := &ClaudeCodeProvider{}
	if err := p.WriteAgentConfig("build"); err != nil {
		t.Errorf("WriteAgentConfig should be no-op, got error: %v", err)
	}
}

func TestClaudeBuildExecArgs(t *testing.T) {
	p := &ClaudeCodeProvider{}
	cfg := &LaunchConfig{
		Role:         "build",
		CLI:          "claude",
		AgentName:    "code-builder",
		ModelFlags:   []string{"--model", "claude-sonnet-4-5"},
		PermFlags:    []string{"--dangerously-skip-permissions"},
		ToolFlags:    []string{"--allowedTools", "Bash(make*)"},
		SharedPrompt: "You are part of a team.",
	}

	binary, args := p.BuildExecArgs(cfg)

	if binary != "claude" {
		t.Errorf("binary = %q, want claude", binary)
	}

	// Check --agent
	found := false
	for _, a := range args {
		if a == "code-builder" {
			found = true
		}
	}
	if !found {
		t.Errorf("args missing agent name: %v", args)
	}

	// Check --model
	found = false
	for i, a := range args {
		if a == "--model" && i+1 < len(args) && args[i+1] == "claude-sonnet-4-5" {
			found = true
		}
	}
	if !found {
		t.Errorf("args missing --model: %v", args)
	}
}

func TestClaudeBuildExecArgs_FallbackPrompt(t *testing.T) {
	p := &ClaudeCodeProvider{}
	cfg := &LaunchConfig{
		Role:      "build",
		CLI:       "claude",
		AgentName: "", // no agent file
	}

	_, args := p.BuildExecArgs(cfg)

	found := false
	for i, a := range args {
		if a == "--append-system-prompt" && i+1 < len(args) && args[i+1] != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fallback prompt in args: %v", args)
	}
}

// --- Phase 3: graceful degradation ---

func TestSupportsHooks_ClaudeOnly(t *testing.T) {
	// Only Claude Code supports hooks; all others should return false
	providers := map[string]Provider{
		"claude":   &ClaudeCodeProvider{},
		"opencode": &OpenCodeProvider{},
		"local":    &LocalProvider{},
	}

	for name, p := range providers {
		expectHooks := name == "claude"
		if p.SupportsHooks() != expectHooks {
			t.Errorf("%s.SupportsHooks() = %v, want %v", name, p.SupportsHooks(), expectHooks)
		}
	}
}

func TestResolveProvider_HookGating(t *testing.T) {
	// Verify that providers resolved via env vars have correct hook support
	tests := []struct {
		name     string
		envVar   string
		envVal   string
		role     string
		wantHook bool
	}{
		{"default claude has hooks", "MUXCODE_BUILD_CLI", "", "build", true},
		{"opencode no hooks", "MUXCODE_BUILD_CLI", "opencode", "build", false},
		{"local no hooks", "MUXCODE_BUILD_CLI", "local", "build", false},
		{"beta defaults to opencode, no hooks", "MUXCODE_BETA_CLI", "", "beta", false},
		{"beta override to claude has hooks", "MUXCODE_BETA_CLI", "claude", "beta", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MUXCODE_AGENT_CLI", "")
			t.Setenv(tt.envVar, tt.envVal)
			p := ResolveProvider(tt.role)
			if p.SupportsHooks() != tt.wantHook {
				t.Errorf("ResolveProvider(%q).SupportsHooks() = %v, want %v",
					tt.role, p.SupportsHooks(), tt.wantHook)
			}
		})
	}
}

func TestIsAgentIdle_OpenCode_AlwaysFalse(t *testing.T) {
	// OpenCode TUI has no reliable idle detection — IsIdle always returns false.
	// This ensures checkIdleAgents() never tries to wake OpenCode agents via send-keys.
	p := &OpenCodeProvider{}
	if p.IsIdle("test-session", "build") {
		t.Error("OpenCode IsIdle should always return false")
	}
}

func TestIsAgentIdle_Local_AlwaysFalse(t *testing.T) {
	p := &LocalProvider{}
	if p.IsIdle("test-session", "build") {
		t.Error("Local IsIdle should always return false")
	}
}

// --- Phase 4: mixed-provider session testing ---

func TestAgentStatus_ProviderField(t *testing.T) {
	// Default: all roles show claude
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")
	t.Setenv("MUXCODE_TEST_CLI", "")
	t.Setenv("MUXCODE_BETA_CLI", "")

	session := t.TempDir()

	status := GetAgentStatus(session, "build")
	if status.Provider != "claude" {
		t.Errorf("build provider = %q, want claude", status.Provider)
	}

	// Beta defaults to opencode
	status = GetAgentStatus(session, "beta")
	if status.Provider != "opencode" {
		t.Errorf("beta provider = %q, want opencode", status.Provider)
	}
}

func TestAgentStatus_MixedProviders(t *testing.T) {
	// Simulate mixed session: edit=claude, build=opencode, test=local
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_EDIT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "opencode")
	t.Setenv("MUXCODE_TEST_CLI", "local")
	t.Setenv("MUXCODE_BETA_CLI", "")

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
	if providerMap["test"] != "local" {
		t.Errorf("test provider = %q, want local", providerMap["test"])
	}
	if providerMap["beta"] != "opencode" {
		t.Errorf("beta provider = %q, want opencode", providerMap["beta"])
	}
}

func TestFormatStatusTable_ShowsProvider(t *testing.T) {
	statuses := []AgentStatus{
		{Role: "edit", Provider: "claude", Health: "alive"},
		{Role: "build", Provider: "opencode", Health: "alive"},
		{Role: "beta", Provider: "opencode", Health: "alive"},
		{Role: "test", Provider: "local", Health: "alive"},
	}

	table := FormatStatusTable(statuses)

	// Header should include PROVIDER column
	if !strings.Contains(table, "PROVIDER") {
		t.Error("status table missing PROVIDER header")
	}

	// Each row should show its provider
	if !strings.Contains(table, "claude") {
		t.Error("status table missing claude provider")
	}
	if !strings.Contains(table, "opencode") {
		t.Error("status table missing opencode provider")
	}
	if !strings.Contains(table, "local") {
		t.Error("status table missing local provider")
	}
}

func TestFormatStatusJSON_IncludesProvider(t *testing.T) {
	statuses := []AgentStatus{
		{Role: "edit", Provider: "claude"},
		{Role: "build", Provider: "opencode"},
	}

	out, err := FormatStatusJSON(statuses)
	if err != nil {
		t.Fatalf("FormatStatusJSON error: %v", err)
	}

	if !strings.Contains(out, `"provider": "claude"`) {
		t.Error("JSON output missing claude provider")
	}
	if !strings.Contains(out, `"provider": "opencode"`) {
		t.Error("JSON output missing opencode provider")
	}
}

// --- OpenCodeProvider ---
// Full OpenCode provider tests are in provider_opencode_test.go
