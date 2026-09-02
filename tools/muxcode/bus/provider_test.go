package bus

import (
	"path/filepath"
	"slices"
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

func TestResolveProvider_DefaultOpenCode(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	p := ResolveProvider("build")
	if p.Name() != "opencode" {
		t.Errorf("default provider for build = %q, want opencode", p.Name())
	}
}

func TestResolveProvider_DefaultClaude(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_EDIT_CLI", "")

	p := ResolveProvider("edit")
	if p.Name() != "claude" {
		t.Errorf("default provider for edit = %q, want claude", p.Name())
	}
}

func TestResolveProvider_PerRoleOpenCode(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_BUILD_CLI", "opencode")

	p := ResolveProvider("build")
	if p.Name() != "opencode" {
		t.Errorf("provider = %q, want opencode", p.Name())
	}
}

func TestResolveProvider_PerRoleLocal(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_BUILD_CLI", "local")

	p := ResolveProvider("build")
	if p.Name() != "local" {
		t.Errorf("provider = %q, want local", p.Name())
	}
}

func TestResolveProvider_SessionDefault(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "opencode")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	p := ResolveProvider("build")
	if p.Name() != "opencode" {
		t.Errorf("provider = %q, want opencode", p.Name())
	}
}

func TestResolveProvider_PerRoleOverridesSession(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "opencode")
	t.Setenv("MUXCODE_BUILD_CLI", "claude")

	p := ResolveProvider("build")
	if p.Name() != "claude" {
		t.Errorf("provider = %q, want claude", p.Name())
	}
}

func TestResolveProvider_UnknownCLIDefaultsToClaude(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "my-custom-claude")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	p := ResolveProvider("build")
	if p.Name() != "claude" {
		t.Errorf("unknown CLI should default to claude, got %q", p.Name())
	}
}

// --- ResolveProviderCLI ---

func TestResolveProviderCLI_Defaults(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")
	t.Setenv("MUXCODE_EDIT_CLI", "")

	// Command-execution roles default to opencode
	if got := ResolveProviderCLI("build"); got != "opencode" {
		t.Errorf("ResolveProviderCLI(build) = %q, want opencode", got)
	}
	// Orchestration roles default to claude
	if got := ResolveProviderCLI("edit"); got != "claude" {
		t.Errorf("ResolveProviderCLI(edit) = %q, want claude", got)
	}
}

func TestResolveProviderCLI_PerRoleOverride(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_BUILD_CLI", "opencode")

	if got := ResolveProviderCLI("build"); got != "opencode" {
		t.Errorf("ResolveProviderCLI(build) = %q, want opencode", got)
	}
}

func TestResolveProviderCLI_SessionDefault(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
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
		AgentJSON:    `{"code-builder":{"description":"Build","prompt":"Do builds."}}`,
		ModelFlags:   []string{"--model", "claude-sonnet-5"},
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
		if a == "--model" && i+1 < len(args) && args[i+1] == "claude-sonnet-5" {
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

// --- MUX-136 Phase 1: pin the downgrade ---

// hasFlagValue reports whether args carries flag immediately followed by value.
func hasFlagValue(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// A name without its definition must never reach Claude as a bare --agent:
// Claude resolves a bare name against the project dir and, finding nothing,
// continues with default tools. The name-only config is launched as
// definition-less instead — no agent flags at all, inline fallback prompt.
func TestClaudeBuildExecArgs_NoBareAgentFlag(t *testing.T) {
	p := &ClaudeCodeProvider{}
	cfg := &LaunchConfig{Role: "plan", CLI: "claude", AgentName: "planner", AgentJSON: ""}

	_, args := p.BuildExecArgs(cfg)

	for i, a := range args {
		if a == "--agent" || a == "--agents" {
			t.Fatalf("name-only config emitted %s at args[%d]: %v", a, i, args)
		}
	}
	if !hasFlagValue(args, "--append-system-prompt", InlineFallbackPrompt("plan")) {
		t.Errorf("definition-less launch missing the inline fallback prompt: %v", args)
	}
}

// The name and the definition travel as one unit — --agent <name> immediately
// followed by --agents <json> — and no shape of launch carries a resume flag:
// the launcher only ever starts fresh sessions, so a resumed session found in
// an agent pane is by construction not a launcher product.
func TestClaudeBuildExecArgs_AgentFlagsTravelTogether(t *testing.T) {
	p := &ClaudeCodeProvider{}
	json := `{"planner":{"description":"Docs","prompt":"Maintain docs."}}`
	cfg := &LaunchConfig{Role: "plan", CLI: "claude", AgentName: "planner", AgentJSON: json}

	_, args := p.BuildExecArgs(cfg)

	want := []string{"--agent", "planner", "--agents", json}
	idx := slices.Index(args, "--agent")
	if idx < 0 || idx+len(want) > len(args) || !slices.Equal(args[idx:idx+len(want)], want) {
		t.Fatalf("agent flags not paired as %v in %v", want, args)
	}
	if hasFlagValue(args, "--append-system-prompt", InlineFallbackPrompt("plan")) {
		t.Errorf("definition present but the fallback prompt was emitted: %v", args)
	}

	shapes := []*LaunchConfig{
		cfg,
		{Role: "plan", CLI: "claude", AgentName: "planner"},
		{Role: "plan", CLI: "claude"},
	}
	for _, shape := range shapes {
		_, a := p.BuildExecArgs(shape)
		for _, flag := range []string{"--resume", "-r", "--continue", "-c"} {
			if slices.Contains(a, flag) {
				t.Errorf("launch carries resume flag %s: %v", flag, a)
			}
		}
	}
}

// ConfigureLaunch sets the name and the definition together or not at all, at
// every tier. The user tier is the live layout (no .claude/agents/ in the
// project); the project tier is the shape that used to be name-only, on the
// grounds that Claude could resolve the file itself.
func TestClaudeConfigureLaunch_NameAndDefinitionPaired(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MUXCODE_INSTALL_DIR", t.TempDir())
	t.Setenv("MUXCODE_PLAN_CLI", "")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	SetConfig(DefaultConfig())
	defer SetConfig(nil)
	chdir(t, project)

	p := &ClaudeCodeProvider{}
	configure := func() *LaunchConfig {
		cfg := &LaunchConfig{Role: "plan"}
		p.ConfigureLaunch(cfg, "plan")
		return cfg
	}

	if cfg := configure(); cfg.AgentName != "" || cfg.AgentJSON != "" {
		t.Fatalf("no definition anywhere, got AgentName=%q AgentJSON=%q", cfg.AgentName, cfg.AgentJSON)
	}

	writeFile(t, filepath.Join(home, ".config", "muxcode", "agents", "planner.md"),
		"---\ndescription: Docs\n---\nUser-tier body.\n")
	if cfg := configure(); cfg.AgentName != "planner" || !strings.Contains(cfg.AgentJSON, "User-tier body") {
		t.Fatalf("user tier: got AgentName=%q AgentJSON=%q", cfg.AgentName, cfg.AgentJSON)
	}

	writeFile(t, filepath.Join(project, ".claude", "agents", "planner.md"),
		"---\ndescription: Project docs\n---\nProject-tier body.\n")
	if cfg := configure(); cfg.AgentName != "planner" || !strings.Contains(cfg.AgentJSON, "Project-tier body") {
		t.Fatalf("project tier: got AgentName=%q AgentJSON=%q — name-only launch", cfg.AgentName, cfg.AgentJSON)
	}
}

// A project-tier definition's restrictions survive into the --agents JSON.
// Before Phase 1 the project tier launched name-only and Claude read the
// file's `tools:`/`model:`/`permissionMode:` itself; --agents outranks
// .claude/agents/, so a reduced JSON would strip them (review must-fix).
func TestClaudeConfigureLaunch_ProjectTierRestrictionsSurvive(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MUXCODE_INSTALL_DIR", t.TempDir())
	t.Setenv("MUXCODE_PLAN_CLI", "")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	SetConfig(DefaultConfig())
	defer SetConfig(nil)
	chdir(t, project)
	writeFile(t, filepath.Join(project, ".claude", "agents", "planner.md"),
		"---\ndescription: Docs\ntools: Read, Edit\nmodel: claude-opus-5\npermissionMode: plan\n---\nDocs only.\n")

	cfg := &LaunchConfig{Role: "plan"}
	(&ClaudeCodeProvider{}).ConfigureLaunch(cfg, "plan")

	for _, want := range []string{`"tools":["Read","Edit"]`, `"model":"claude-opus-5"`, `"permissionMode":"plan"`, `"prompt":"Docs only.\n"`} {
		if !strings.Contains(cfg.AgentJSON, want) {
			t.Errorf("AgentJSON missing %s: %s", want, cfg.AgentJSON)
		}
	}
}

// --- Phase 3: graceful degradation ---

func TestSupportsHooks_ClaudeOnly(t *testing.T) {
	// Only Claude Code supports hooks; all others should return false
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

func TestResolveProvider_HookGating(t *testing.T) {
	// Verify that providers resolved via env vars have correct hook support
	tests := []struct {
		name     string
		envVar   string
		envVal   string
		role     string
		wantHook bool
	}{
		{"default opencode no hooks", "MUXCODE_BUILD_CLI", "", "build", false},
		{"opencode no hooks", "MUXCODE_BUILD_CLI", "opencode", "build", false},
		{"codex no hooks", "MUXCODE_BUILD_CLI", "codex", "build", false},
		{"local no hooks", "MUXCODE_BUILD_CLI", "local", "build", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetBusDirBase(t.TempDir()) // isolate from live session override files
			defer ResetBusDirBase()
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

// --- Claude thinking detection ---

func TestIsClaudeThinking_Ideating(t *testing.T) {
	content := "  ⏺ Running 1 shell command…\n  ⎿  $ cdk deploy --all\n\n✢ Ideating… (11m 18s · ↓ 634 tokens)\n\n❯ "
	if !isClaudeThinking(content) {
		t.Error("should detect ✢ Ideating… as thinking state")
	}
}

func TestIsClaudeThinking_CogitatedRecapIsIdle(t *testing.T) {
	// "✻ Cogitated for 20s" is the COMPLETED recap line (past tense, no live
	// spinner) — the agent is idle at the ❯ prompt, not thinking. Treating it
	// as thinking blocks message delivery to an idle agent.
	content := "✻ Cogitated for 20s\n\n❯ "
	if isClaudeThinking(content) {
		t.Error("completed recap '✻ Cogitated for 20s' must NOT be detected as thinking")
	}
}

func TestIsClaudeThinking_CookedRecapIsIdle(t *testing.T) {
	// Claude Code's recap feature prints "✻ Cooked for 1m 47s" after a turn
	// completes while the agent sits idle. This is the exact line that made the
	// run agent look perpetually busy and stop receiving editor messages.
	content := "✻ Cooked for 1m 47s\n\n※ recap: Investigated the thing. (disable recaps in /config)\n\n❯ "
	if isClaudeThinking(content) {
		t.Error("completed recap '✻ Cooked for 1m 47s' must NOT be detected as thinking")
	}
}

func TestIsClaudeThinking_ActiveSpinnerWithInterruptHint(t *testing.T) {
	// In-progress spinner carries the "esc to interrupt" hint even if the
	// ellipsis is rendered elsewhere — must still be detected as thinking.
	content := "✻ Cogitating (12s · esc to interrupt)\n\n❯ "
	if !isClaudeThinking(content) {
		t.Error("active spinner with 'esc to interrupt' must be detected as thinking")
	}
}

func TestIsClaudeThinking_CombobulatingNonStandardGlyph(t *testing.T) {
	// The spinner glyph animates across many code points. "✽" (U+273D) is NOT
	// in the old ✢/✻ set, so this frame slipped past detection and made a busy
	// agent look idle — causing a "You have N new messages" notification storm.
	// Detection must be glyph-independent (match the live-spinner signature).
	content := "✽ Combobulating… (13m 9s · ↓ 17.8k tokens · thinking with high effort)\n\n❯ "
	if !isClaudeThinking(content) {
		t.Error("active spinner '✽ Combobulating…' (non-✢/✻ glyph) must be detected as thinking")
	}
}

func TestIsClaudeThinking_Spelunking(t *testing.T) {
	content := "✢ Spelunking… (3m 42s · ↓ 200 tokens)\n\n❯ "
	if !isClaudeThinking(content) {
		t.Error("should detect ✢ Spelunking… as thinking state")
	}
}

func TestIsClaudeThinking_IdlePromptOnly(t *testing.T) {
	content := "  some output\n\n❯ "
	if isClaudeThinking(content) {
		t.Error("should not detect thinking when only idle prompt is present")
	}
}

func TestIsClaudeThinking_ToolExecution(t *testing.T) {
	content := "  ⏺ Running 1 shell command…\n  ⎿  $ ./build.sh\n     Build succeeded\n\n❯ "
	if isClaudeThinking(content) {
		t.Error("should not detect thinking during normal tool execution")
	}
}

// Regression: the status footer carries an "esc to interrupt" hint even when the
// agent is idle at the ❯ prompt (Claude Code renders it whenever text is parked
// in the composer). Treating the footer as a working signature pinned IsIdle to
// false, so the daemon never delivered the agent's inbox and it wedged with
// actionable messages — observed live on the review agent, which had to be
// recovered by hand with `muxcode deliver review --force` twice.
func TestIsClaudeThinking_StatusFooterIsNotThinking(t *testing.T) {
	content := "✻ Cooked for 1m 47s\n──── code-reviewer ──\n❯ \n" +
		"⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← for agents"
	if isClaudeThinking(content) {
		t.Error("status footer 'esc to interrupt' must not count as thinking — idle agent would never be delivered")
	}
}

// The footer must be ignored, but a genuine spinner ABOVE that same footer must
// still register — otherwise the fix would trade a wedged agent for a spammed one.
func TestIsClaudeThinking_SpinnerAboveStatusFooter(t *testing.T) {
	content := "✻ Befuddling… (2m 3s · ↓ 8.1k tokens · esc to interrupt)\n──── code-reviewer ──\n❯ \n" +
		"⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← for agents"
	if !isClaudeThinking(content) {
		t.Error("active spinner above the status footer must still be detected as thinking")
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
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	// Default: command-execution roles show opencode
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")
	t.Setenv("MUXCODE_TEST_CLI", "")
	session := t.TempDir()

	status := GetAgentStatus(session, "build")
	if status.Provider != "opencode" {
		t.Errorf("build provider = %q, want opencode", status.Provider)
	}
}

func TestAgentStatus_MixedProviders(t *testing.T) {
	// Simulate mixed session: edit=claude, build=opencode, test=local
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_EDIT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "opencode")
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
	if providerMap["test"] != "local" {
		t.Errorf("test provider = %q, want local", providerMap["test"])
	}
}

func TestFormatStatusTable_ShowsProvider(t *testing.T) {
	statuses := []AgentStatus{
		{Role: "edit", Provider: "claude", Health: "alive"},
		{Role: "build", Provider: "opencode", Health: "alive"},
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

// --- buildChainInstruction ---

func TestBuildChainInstruction_NilConfig(t *testing.T) {
	if got := buildChainInstruction("build", nil); got != "" {
		t.Errorf("nil config should return empty, got %q", got)
	}
}

func TestBuildChainInstruction_NoChainForRole(t *testing.T) {
	cfg := &MuxcodeConfig{EventChains: map[string]EventChain{}}
	if got := buildChainInstruction("build", cfg); got != "" {
		t.Errorf("missing chain should return empty, got %q", got)
	}
}

func TestBuildChainInstruction_DefaultBuildChain(t *testing.T) {
	// Default config: build → test on success, notify edit on failure
	cfg := DefaultConfig()
	got := buildChainInstruction("build", cfg)

	// Should mention sending to test on success
	if !strings.Contains(got, "SUCCESS") {
		t.Errorf("expected SUCCESS in instruction, got %q", got)
	}
	if !strings.Contains(got, "muxcode send test test") {
		t.Errorf("expected 'muxcode send test test' in instruction, got %q", got)
	}
	// Should NOT mention failure (edit notifications are filtered out)
	if strings.Contains(got, "FAILURE") {
		t.Errorf("expected failure actions (edit events) to be filtered, got %q", got)
	}
}

func TestBuildChainInstruction_DefaultTestChain(t *testing.T) {
	cfg := DefaultConfig()
	got := buildChainInstruction("test", cfg)

	if !strings.Contains(got, "SUCCESS") {
		t.Errorf("expected SUCCESS in instruction, got %q", got)
	}
	if !strings.Contains(got, "muxcode send review review") {
		t.Errorf("expected 'muxcode send review review' in instruction, got %q", got)
	}
}

func TestBuildChainInstruction_DefaultDeployChain(t *testing.T) {
	cfg := DefaultConfig()
	got := buildChainInstruction("deploy", cfg)

	if !strings.Contains(got, "SUCCESS") {
		t.Errorf("expected SUCCESS in instruction, got %q", got)
	}
	if !strings.Contains(got, "muxcode send run run") {
		t.Errorf("expected 'muxcode send run run' in instruction, got %q", got)
	}
}

func TestBuildChainInstruction_ConditionalActions(t *testing.T) {
	cfg := &MuxcodeConfig{
		EventChains: map[string]EventChain{
			"build": {
				OnSuccess: ChainActions{
					{
						SendTo:  "deploy",
						Action:  "deploy",
						Message: "Deploy infra changes",
						Type:    "request",
						Conditions: map[string]any{
							"files_match":  "lib/**/*.ts",
							"branch_match": "^main$",
						},
					},
					{
						SendTo:  "test",
						Action:  "test",
						Message: "Run tests",
						Type:    "request",
					},
				},
			},
		},
	}

	got := buildChainInstruction("build", cfg)

	// Should describe conditions
	if !strings.Contains(got, "files match") {
		t.Errorf("expected 'files match' condition description, got %q", got)
	}
	if !strings.Contains(got, "branch matches") {
		t.Errorf("expected 'branch matches' condition description, got %q", got)
	}
	// Should have fallback
	if !strings.Contains(got, "otherwise") {
		t.Errorf("expected 'otherwise' fallback, got %q", got)
	}
	// Should mention both targets
	if !strings.Contains(got, "deploy") {
		t.Errorf("expected deploy target, got %q", got)
	}
	if !strings.Contains(got, "test") {
		t.Errorf("expected test target, got %q", got)
	}
}

func TestBuildChainInstruction_OnlyEditEvents(t *testing.T) {
	// A chain that only notifies edit with events should produce no instruction
	cfg := &MuxcodeConfig{
		EventChains: map[string]EventChain{
			"build": {
				OnSuccess: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Build done",
					Type:    "event",
				}},
				OnFailure: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Build failed",
					Type:    "event",
				}},
			},
		},
	}

	got := buildChainInstruction("build", cfg)
	if got != "" {
		t.Errorf("expected empty for edit-only events, got %q", got)
	}
}

func TestBuildChainInstruction_AllConditionTypes(t *testing.T) {
	cfg := &MuxcodeConfig{
		EventChains: map[string]EventChain{
			"build": {
				OnSuccess: ChainActions{
					{
						SendTo:  "deploy",
						Action:  "deploy",
						Message: "Deploy",
						Type:    "request",
						Conditions: map[string]any{
							"files_not_match":  "docs/**",
							"branch_not_match": "^hotfix/",
							"env_set":          "DEPLOY_ENABLED",
							"env_equals":       map[string]any{"name": "ENV", "value": "prod"},
							"output_contains":  "success",
							"exit_code":        0,
						},
					},
				},
			},
		},
	}

	got := buildChainInstruction("build", cfg)

	checks := []string{
		"no changed files match",
		"branch does not match",
		"env var DEPLOY_ENABLED is set",
		"env var ENV equals",
		"output contains",
		"exit code is",
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("expected %q in instruction, got %q", check, got)
		}
	}
}

func TestDescribeOutcome_Empty(t *testing.T) {
	got := describeOutcome("SUCCESS", nil)
	if got != "" {
		t.Errorf("expected empty for nil actions, got %q", got)
	}
}

func TestFilterMeaningfulActions(t *testing.T) {
	actions := ChainActions{
		{SendTo: "edit", Action: "notify", Type: "event"},
		{SendTo: "test", Action: "test", Type: "request"},
		{SendTo: "edit", Action: "notify", Type: "event"},
	}

	result := filterMeaningfulActions(actions)
	if len(result) != 1 {
		t.Fatalf("expected 1 meaningful action, got %d", len(result))
	}
	if result[0].SendTo != "test" {
		t.Errorf("expected test action, got %q", result[0].SendTo)
	}
}

func TestActionType_Default(t *testing.T) {
	a := ChainAction{Type: ""}
	if got := actionType(a); got != "request" {
		t.Errorf("empty type should default to request, got %q", got)
	}

	a.Type = "event"
	if got := actionType(a); got != "event" {
		t.Errorf("explicit type should be preserved, got %q", got)
	}
}

// --- OpenCodeProvider ---
// Full OpenCode provider tests are in provider_opencode_test.go

// Regression (interrupt race): a turn renders its spinner gerund BEFORE the
// "(elapsed · tokens · esc to interrupt)" counter appears. In that window the
// line has no " · " and no interrupt hint, while the ❯ prompt sits in the input
// box as always — so a WORKING agent read as idle and the daemon injected a
// wake-up into the running turn, killing it ("Interrupted · What should Claude
// do instead?"). Captured live from the review agent mid-review.
func TestIsClaudeThinking_BareSpinnerWithoutCounter(t *testing.T) {
	content := "❯ New message from test [request:review]: Tests passed\n" +
		"✶ Hullaballooing…\n──── code-reviewer ──\n❯ \n" +
		"⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← for agents"
	if !isClaudeThinking(content) {
		t.Error("bare spinner (counter not yet rendered) must read as thinking — else the daemon interrupts the running turn")
	}
}

// The spinner animates across code points; every frame must register, not just
// the ones someone happened to hardcode.
func TestIsClaudeSpinnerLine_AllAnimationFrames(t *testing.T) {
	for _, glyph := range []string{"✢", "✳", "✶", "✺", "✻", "✽"} {
		if !isClaudeSpinnerLine(glyph + " Combobulating…") {
			t.Errorf("spinner frame %q must be detected as a live spinner", glyph)
		}
	}
}

// The past-tense recap carries a spinner glyph but no ellipsis, and marks an
// IDLE agent. Misreading it as thinking would mean the agent is never delivered.
func TestIsClaudeSpinnerLine_CompletedRecapIsNotSpinner(t *testing.T) {
	if isClaudeSpinnerLine("✻ Cooked for 1m 47s") {
		t.Error("completed recap must not read as a live spinner — the agent is idle and must be delivered")
	}
}

// The tool bullet ⏺ (U+23FA) is outside the spinner glyph range: completed tool
// output ends with an ellipsis too, but the agent is back at the prompt.
func TestIsClaudeSpinnerLine_ToolBulletIsNotSpinner(t *testing.T) {
	if isClaudeSpinnerLine("⏺ Running 1 shell command…") {
		t.Error("tool bullet line must not read as a live spinner — agent is deliverable")
	}
}

// The status footer differs per permission mode, and only the "(shift+tab to
// cycle)" hint is common to all of them. Keying the footer signature on the
// bypass-mode glyph/text alone left every other mode (plan, accept-edits)
// carrying an unfiltered "esc to interrupt" — which pins IsIdle false and wedges
// the agent, the exact failure isClaudeStatusFooter exists to prevent.
func TestIsClaudeThinking_StatusFooterAcrossPermissionModes(t *testing.T) {
	footers := []string{
		"⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← for agents",
		"⏸ plan mode on (shift+tab to cycle) · esc to interrupt · ← for agents",
		"⏵⏵ accept edits on (shift+tab to cycle) · esc to interrupt · ← for agents",
	}
	for _, footer := range footers {
		content := "✻ Cooked for 39s\n──── code-reviewer ──\n❯ \n" + footer
		if isClaudeThinking(content) {
			t.Errorf("idle agent must not read as thinking under footer: %q", footer)
		}
	}
}
