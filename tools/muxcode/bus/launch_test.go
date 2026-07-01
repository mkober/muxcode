package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentFileNameExported(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"plan", "planner"},
		{"planner", "planner"},
		{"edit", "code-editor"},
		{"build", "code-builder"},
		{"test", "test-runner"},
		{"review", "code-reviewer"},
		{"deploy", "infra-deployer"},
		{"run", "command-runner"},
		{"runner", "command-runner"},
		{"commit", "git-manager"},
		{"git", "git-manager"},
		{"analyze", "editor-analyst"},
		{"analyst", "editor-analyst"},
		{"docs", "doc-writer"},
		{"research", "code-researcher"},
		{"watch", "log-watcher"},
		{"pr-read", "pr-reader"},
		{"api", "api-tester"},
		{"auto", "autonomous-agent"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := AgentFileName(tt.role)
			if got != tt.want {
				t.Errorf("AgentFileName(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestRoleCLIEnvVar(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"plan", "MUXCODE_PLAN_CLI"},
		{"planner", "MUXCODE_PLAN_CLI"},
		{"commit", "MUXCODE_COMMIT_CLI"},
		{"git", "MUXCODE_COMMIT_CLI"},
		{"build", "MUXCODE_BUILD_CLI"},
		{"edit", "MUXCODE_EDIT_CLI"},
		{"analyze", "MUXCODE_ANALYZE_CLI"},
		{"analyst", "MUXCODE_ANALYZE_CLI"},
		{"runner", "MUXCODE_RUN_CLI"},
		{"run", "MUXCODE_RUN_CLI"},
		{"pr-read", "MUXCODE_PR_READ_CLI"},
		{"api", "MUXCODE_API_CLI"},
		{"auto", "MUXCODE_AUTO_CLI"},
		{"custom", "MUXCODE_CUSTOM_CLI"},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := RoleCLIEnvVar(tt.role)
			if got != tt.want {
				t.Errorf("RoleCLIEnvVar(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestRoleClaudeModelEnvVar(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"plan", "MUXCODE_PLAN_CLAUDE_MODEL"},
		{"commit", "MUXCODE_COMMIT_CLAUDE_MODEL"},
		{"edit", "MUXCODE_EDIT_CLAUDE_MODEL"},
		{"analyze", "MUXCODE_ANALYZE_CLAUDE_MODEL"},
		{"auto", "MUXCODE_AUTO_CLAUDE_MODEL"},
		{"custom", "MUXCODE_CUSTOM_CLAUDE_MODEL"},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := RoleClaudeModelEnvVar(tt.role)
			if got != tt.want {
				t.Errorf("RoleClaudeModelEnvVar(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestRoleClaudeModelDefault(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"edit", "claude-fable-5"},
		{"auto", "claude-fable-5"},
		{"plan", "claude-opus-4-8"},
		{"planner", "claude-opus-4-8"},
		{"review", "claude-opus-4-8"},
		{"analyze", "claude-opus-4-8"},
		{"analyst", "claude-opus-4-8"},
		{"build", "claude-sonnet-5"},
		{"test", "claude-sonnet-5"},
		{"commit", "claude-sonnet-5"},
		{"git", "claude-sonnet-5"},
		{"deploy", "claude-sonnet-5"},
		{"api", "claude-sonnet-5"},
		{"run", "claude-sonnet-5"},
		{"runner", "claude-sonnet-5"},
		{"watch", "claude-sonnet-5"},
		{"custom", ""},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := RoleClaudeModelDefault(tt.role)
			if got != tt.want {
				t.Errorf("RoleClaudeModelDefault(%q) = %q, want %q", tt.role, got, tt.want)
			}
		})
	}
}

func TestExtractFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		desc    string
		body    string
	}{
		{
			name:    "with frontmatter",
			content: "---\ndescription: Build agent\n---\n# Build Agent\nDo builds.",
			desc:    "Build agent",
			body:    "# Build Agent\nDo builds.",
		},
		{
			name:    "no frontmatter",
			content: "# Just a prompt\nNo frontmatter here.",
			desc:    "",
			body:    "# Just a prompt\nNo frontmatter here.",
		},
		{
			name:    "empty description",
			content: "---\ntags: build\n---\n# Agent\nBody.",
			desc:    "",
			body:    "# Agent\nBody.",
		},
		{
			name:    "frontmatter only",
			content: "---\ndescription: Test\n---\n",
			desc:    "Test",
			body:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body := ExtractFrontmatter(tt.content)
			if fm.Description != tt.desc {
				t.Errorf("description = %q, want %q", fm.Description, tt.desc)
			}
			if body != tt.body {
				t.Errorf("body = %q, want %q", body, tt.body)
			}
		})
	}
}

func TestResolveAgentFile_ThreeTier(t *testing.T) {
	// Create temp directories for all 3 tiers
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	installDir := filepath.Join(tmpDir, "install")

	// Create tier 1: project-local
	tier1Dir := filepath.Join(projectDir, ".claude", "agents")
	os.MkdirAll(tier1Dir, 0o755)
	os.WriteFile(filepath.Join(tier1Dir, "test-agent.md"), []byte("tier1"), 0o644)

	// Create tier 3: install dir
	tier3Dir := filepath.Join(installDir, "agents")
	os.MkdirAll(tier3Dir, 0o755)
	os.WriteFile(filepath.Join(tier3Dir, "test-agent.md"), []byte("tier3"), 0o644)
	os.WriteFile(filepath.Join(tier3Dir, "install-only.md"), []byte("tier3-only"), 0o644)

	// Save and change to project dir
	origDir, _ := os.Getwd()
	os.Chdir(projectDir)
	defer os.Chdir(origDir)

	// Test tier 1 takes priority
	path, tier := ResolveAgentFile("test-agent", installDir)
	if tier != 1 {
		t.Errorf("expected tier 1, got %d (path: %s)", tier, path)
	}

	// Test tier 3 fallback
	path, tier = ResolveAgentFile("install-only", installDir)
	if tier != 3 {
		t.Errorf("expected tier 3, got %d (path: %s)", tier, path)
	}

	// Test not found
	_, tier = ResolveAgentFile("nonexistent", installDir)
	if tier != 0 {
		t.Errorf("expected tier 0 for nonexistent, got %d", tier)
	}

	// Test empty name
	_, tier = ResolveAgentFile("", installDir)
	if tier != 0 {
		t.Errorf("expected tier 0 for empty name, got %d", tier)
	}
}

func TestBuildAgentsJSON(t *testing.T) {
	jsonStr, err := BuildAgentsJSON("test-agent", "A test agent", "Do testing stuff.")
	if err != nil {
		t.Fatalf("BuildAgentsJSON error: %v", err)
	}

	// Verify it's valid JSON and contains expected fields
	if jsonStr == "" {
		t.Fatal("expected non-empty JSON")
	}

	// Check key substrings (avoid brittle exact JSON matching)
	for _, want := range []string{"test-agent", "A test agent", "Do testing stuff."} {
		if !strings.Contains(jsonStr, want) {
			t.Errorf("JSON missing %q: %s", want, jsonStr)
		}
	}
}

func TestInlineFallbackPrompt(t *testing.T) {
	// Known roles should return non-empty prompts
	for _, role := range []string{"plan", "planner", "edit", "build", "test", "review", "deploy",
		"run", "runner", "commit", "git", "analyze", "analyst",
		"docs", "research", "watch", "pr-read", "api"} {
		prompt := InlineFallbackPrompt(role)
		if prompt == "" {
			t.Errorf("InlineFallbackPrompt(%q) returned empty", role)
		}
	}

	// Default fallback
	prompt := InlineFallbackPrompt("unknown-role")
	if prompt == "" {
		t.Error("expected default fallback for unknown role")
	}
}

func TestResolveVenv(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// No venv — should return ""
	got := ResolveVenv()
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Create .venv
	os.MkdirAll(filepath.Join(tmpDir, ".venv", "bin"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, ".venv", "bin", "activate"), []byte(""), 0o644)

	got = ResolveVenv()
	if got != ".venv" {
		t.Errorf("expected .venv, got %q", got)
	}
}

func TestResolveVenv_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()

	// Create custom venv
	customVenv := filepath.Join(tmpDir, "custom-venv")
	os.MkdirAll(filepath.Join(customVenv, "bin"), 0o755)
	os.WriteFile(filepath.Join(customVenv, "bin", "activate"), []byte(""), 0o644)

	t.Setenv("MUXCODE_VENV_DIR", customVenv)

	got := ResolveVenv()
	if got != customVenv {
		t.Errorf("expected %q, got %q", customVenv, got)
	}
}

func TestResolveLaunchConfig_BuildRole(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	// Clear env to get defaults
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLAUDE_MODEL", "")
	t.Setenv("MUXCODE_CLAUDE_MODEL", "")

	cfg := ResolveLaunchConfig("build")

	if cfg.Role != "build" {
		t.Errorf("Role = %q, want build", cfg.Role)
	}
	if cfg.CLI != "opencode" {
		t.Errorf("CLI = %q, want opencode (default for build)", cfg.CLI)
	}
	if cfg.IsLocal {
		t.Error("expected IsLocal=false")
	}
	if cfg.Agent != "code-builder" {
		t.Errorf("Agent = %q, want code-builder", cfg.Agent)
	}

	// OpenCode provider doesn't set ModelFlags or PermFlags (handled in agent config)
	if len(cfg.ModelFlags) != 0 {
		t.Errorf("ModelFlags = %v, want empty (OpenCode handles model in agent config)", cfg.ModelFlags)
	}
	if len(cfg.PermFlags) != 0 {
		t.Errorf("PermFlags = %v, want empty (OpenCode doesn't use Claude permission flags)", cfg.PermFlags)
	}
}

func TestResolveLaunchConfig_EditRole(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_EDIT_CLI", "")
	t.Setenv("MUXCODE_EDIT_MODEL", "")
	t.Setenv("MUXCODE_EDIT_CLAUDE_MODEL", "")
	t.Setenv("MUXCODE_CLAUDE_MODEL", "")

	cfg := ResolveLaunchConfig("edit")

	// Edit should default to the fable model
	if len(cfg.ModelFlags) != 2 || cfg.ModelFlags[1] != "claude-fable-5" {
		t.Errorf("ModelFlags = %v, want [--model claude-fable-5]", cfg.ModelFlags)
	}

	// Edit should have --dangerously-skip-permissions (all roles use bypass)
	if len(cfg.PermFlags) == 0 {
		t.Error("expected PermFlags for edit role")
	}
}

func TestResolveLaunchConfig_LocalLLM(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_BUILD_CLI", "local")

	cfg := ResolveLaunchConfig("build")

	if !cfg.IsLocal {
		t.Error("expected IsLocal=true when CLI=local")
	}
	if len(cfg.HarnessArgs) == 0 {
		t.Error("expected HarnessArgs for local LLM")
	}
	if cfg.HarnessArgs[0] != "run" || cfg.HarnessArgs[1] != "build" {
		t.Errorf("HarnessArgs = %v, want [run build ...]", cfg.HarnessArgs)
	}
}

func TestResolveLaunchConfig_CustomCLI(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_AGENT_CLI", "my-claude")
	t.Setenv("MUXCODE_BUILD_CLI", "")

	cfg := ResolveLaunchConfig("build")

	if cfg.CLI != "my-claude" {
		t.Errorf("CLI = %q, want my-claude", cfg.CLI)
	}
}

func TestResolveLaunchConfig_ModelEnvOverride(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_BUILD_MODEL", "") // isolate from ambient session env (generic var wins over Claude-specific)
	t.Setenv("MUXCODE_BUILD_CLAUDE_MODEL", "claude-haiku-3")
	t.Setenv("MUXCODE_BUILD_CLI", "claude")

	cfg := ResolveLaunchConfig("build")

	if len(cfg.ModelFlags) != 2 || cfg.ModelFlags[1] != "claude-haiku-3" {
		t.Errorf("ModelFlags = %v, want [--model claude-haiku-3]", cfg.ModelFlags)
	}
}

func TestResolveLaunchConfig_GlobalModelOverride(t *testing.T) {
	SetBusDirBase(t.TempDir()) // isolate from live session override files
	defer ResetBusDirBase()
	t.Setenv("MUXCODE_BUILD_MODEL", "") // isolate from ambient session env (generic var wins over Claude-specific)
	t.Setenv("MUXCODE_BUILD_CLAUDE_MODEL", "")
	t.Setenv("MUXCODE_CLAUDE_MODEL", "claude-custom-99")
	t.Setenv("MUXCODE_BUILD_CLI", "claude")

	cfg := ResolveLaunchConfig("build")

	if len(cfg.ModelFlags) != 2 || cfg.ModelFlags[1] != "claude-custom-99" {
		t.Errorf("ModelFlags = %v, want [--model claude-custom-99]", cfg.ModelFlags)
	}
}

func TestBuildExecArgs_Claude(t *testing.T) {
	cfg := &LaunchConfig{
		Role:         "build",
		CLI:          "claude",
		AgentName:    "code-builder",
		ModelFlags:   []string{"--model", "claude-sonnet-5"},
		PermFlags:    []string{"--dangerously-skip-permissions"},
		ToolFlags:    []string{"--allowedTools", "Bash(make*)"},
		SharedPrompt: "You are part of a team.",
	}

	binary, args := cfg.BuildExecArgs()

	if binary != "claude" {
		t.Errorf("binary = %q, want claude", binary)
	}

	// Should contain --agent
	found := false
	for _, a := range args {
		if a == "code-builder" {
			found = true
		}
	}
	if !found {
		t.Errorf("args missing agent name: %v", args)
	}
}

func TestBuildExecArgs_FallbackPrompt(t *testing.T) {
	// No agent file — should include inline fallback prompt
	cfg := &LaunchConfig{
		Role:      "build",
		CLI:       "claude",
		AgentName: "", // no agent file found
	}

	_, args := cfg.BuildExecArgs()

	// Should have --append-system-prompt with fallback
	found := false
	for i, a := range args {
		if a == "--append-system-prompt" && i+1 < len(args) {
			if args[i+1] != "" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected fallback prompt in args: %v", args)
	}
}

func TestBuildExecArgs_Local(t *testing.T) {
	// Mock lookPath to not find harness
	origLookPath := lookPath
	lookPath = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	defer func() { lookPath = origLookPath }()

	cfg := &LaunchConfig{
		Role:        "build",
		IsLocal:     true,
		HarnessArgs: []string{"run", "build"},
	}

	binary, args := cfg.BuildExecArgs()

	if binary != "muxcode" {
		t.Errorf("binary = %q, want muxcode", binary)
	}
	if args[0] != "agent" || args[1] != "run" || args[2] != "build" {
		t.Errorf("args = %v, want [agent run build]", args)
	}
}

func TestBuildExecArgs_LocalWithHarness(t *testing.T) {
	// Mock lookPath to find harness
	origLookPath := lookPath
	lookPath = func(file string) (string, error) {
		if file == "muxcode-llm-harness" {
			return "/usr/local/bin/muxcode-llm-harness", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	defer func() { lookPath = origLookPath }()

	cfg := &LaunchConfig{
		Role:        "build",
		IsLocal:     true,
		HarnessArgs: []string{"run", "build"},
	}

	binary, args := cfg.BuildExecArgs()

	if binary != "muxcode-llm-harness" {
		t.Errorf("binary = %q, want muxcode-llm-harness", binary)
	}
	if args[0] != "run" || args[1] != "build" {
		t.Errorf("args = %v, want [run build]", args)
	}
}

func TestBuildExecArgs_AgentJSON(t *testing.T) {
	cfg := &LaunchConfig{
		Role:      "build",
		CLI:       "claude",
		AgentName: "code-builder",
		AgentJSON: `{"code-builder":{"description":"Build","prompt":"Do builds."}}`,
	}

	_, args := cfg.BuildExecArgs()

	// Should contain --agents
	foundAgent := false
	foundAgents := false
	for i, a := range args {
		if a == "--agent" && i+1 < len(args) && args[i+1] == "code-builder" {
			foundAgent = true
		}
		if a == "--agents" && i+1 < len(args) {
			foundAgents = true
		}
	}
	if !foundAgent {
		t.Errorf("args missing --agent: %v", args)
	}
	if !foundAgents {
		t.Errorf("args missing --agents: %v", args)
	}
}

func TestResolveTaskFile_NonAgentRole(t *testing.T) {
	// Non-agent roles should return empty
	for _, role := range []string{"edit", "build", "test", "review", "commit"} {
		if got := ResolveTaskFile(role); got != "" {
			t.Errorf("ResolveTaskFile(%q) = %q, want empty", role, got)
		}
	}
}

func TestResolveTaskFile_AgentWithProjectLocal(t *testing.T) {
	// Create a temp project-local task file
	dir := t.TempDir()
	muxDir := filepath.Join(dir, ".muxcode")
	os.MkdirAll(muxDir, 0o755)
	taskFile := filepath.Join(muxDir, "agent-tasks.md")
	os.WriteFile(taskFile, []byte("# Agent tasks\n\n- Check Jira every 30 minutes\n"), 0o644)

	// Change to temp dir so project-local resolution works
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	got := ResolveTaskFile("auto")
	if got == "" {
		t.Fatal("expected task file content, got empty")
	}
	if !strings.Contains(got, "Agent task configuration") {
		t.Error("expected header wrapper")
	}
	if !strings.Contains(got, "Check Jira every 30 minutes") {
		t.Error("expected task file content")
	}
	if !strings.Contains(got, "agent-tasks.md") {
		t.Error("expected file path in output")
	}
}

func TestResolveTaskFile_EnvVarOverride(t *testing.T) {
	dir := t.TempDir()
	taskFile := filepath.Join(dir, "custom-tasks.md")
	os.WriteFile(taskFile, []byte("# Custom tasks\n\n- Poll every 5 minutes\n"), 0o644)

	t.Setenv("MUXCODE_AUTO_TASKS", taskFile)

	got := ResolveTaskFile("auto")
	if got == "" {
		t.Fatal("expected task file content, got empty")
	}
	if !strings.Contains(got, "Poll every 5 minutes") {
		t.Error("expected custom task content")
	}
	if !strings.Contains(got, taskFile) {
		t.Error("expected custom path in output")
	}
}

func TestResolveTaskFile_NoFileExists(t *testing.T) {
	// Point to nonexistent paths
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	t.Setenv("MUXCODE_AUTO_TASKS", "/nonexistent/tasks.md")

	got := ResolveTaskFile("auto")
	if got != "" {
		t.Errorf("expected empty when no task file exists, got %q", got)
	}
}

func TestResolveTaskFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	muxDir := filepath.Join(dir, ".muxcode")
	os.MkdirAll(muxDir, 0o755)
	taskFile := filepath.Join(muxDir, "agent-tasks.md")
	os.WriteFile(taskFile, []byte(""), 0o644)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	got := ResolveTaskFile("auto")
	if got != "" {
		t.Errorf("expected empty for empty task file, got %q", got)
	}
}

func TestPreLaunchSetup_AutoStartupMessage(t *testing.T) {
	dir := t.TempDir()
	session := "test-prelaunch-auto"
	os.Setenv("BUS_DIR_BASE", dir)
	defer os.Unsetenv("BUS_DIR_BASE")

	// Init the bus directory so inbox paths exist
	Init(session, dir)

	// Run PreLaunchSetup for auto role
	PreLaunchSetup("auto", session, "claude")

	// Read the auto inbox and verify the startup message
	msgs, err := Peek(session, "auto")
	if err != nil {
		t.Fatalf("Peek auto inbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in auto inbox, got %d", len(msgs))
	}

	m := msgs[0]
	if m.Type != "request" {
		t.Errorf("expected type 'request', got %q", m.Type)
	}
	if m.Action != "startup" {
		t.Errorf("expected action 'startup', got %q", m.Action)
	}
	if m.From != "edit" {
		t.Errorf("expected from 'edit', got %q", m.From)
	}
	if m.To != "auto" {
		t.Errorf("expected to 'auto', got %q", m.To)
	}
	if !strings.Contains(m.Payload, "Jira") {
		t.Errorf("expected payload to mention Jira, got %q", m.Payload)
	}
}

func TestPreLaunchSetup_EditStartupMessage(t *testing.T) {
	dir := t.TempDir()
	session := "test-prelaunch-edit"
	os.Setenv("BUS_DIR_BASE", dir)
	defer os.Unsetenv("BUS_DIR_BASE")

	Init(session, dir)

	PreLaunchSetup("edit", session, "claude")

	msgs, err := Peek(session, "edit")
	if err != nil {
		t.Fatalf("Peek edit inbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in edit inbox, got %d", len(msgs))
	}

	m := msgs[0]
	if m.Type != "request" {
		t.Errorf("expected type 'request', got %q", m.Type)
	}
	if m.Action != "startup" {
		t.Errorf("expected action 'startup', got %q", m.Action)
	}
	// The startup message MUST be actionable so the daemon's wake-up paths
	// (which gate on HasActionableMessages) can re-notify if the launch-time
	// send-keys wake-up is dropped.
	if !HasActionableMessages(session, "edit") {
		t.Error("expected edit startup message to be actionable, but it is not")
	}
}

func TestPreLaunchSetup_AllRolesGetStartupMessage(t *testing.T) {
	// All non-auto roles should receive a startup request/startup message
	// so they check inbox on launch and restore session context. It must be
	// request-type (actionable) so the daemon can re-wake a missed agent.
	roles := []string{"build", "test", "commit", "review", "deploy", "plan", "run", "watch", "serve"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			dir := t.TempDir()
			session := "test-prelaunch-" + role
			os.Setenv("BUS_DIR_BASE", dir)
			defer os.Unsetenv("BUS_DIR_BASE")

			Init(session, dir)

			PreLaunchSetup(role, session, "claude")

			msgs, err := Peek(session, role)
			if err != nil {
				t.Fatalf("Peek %s inbox: %v", role, err)
			}
			if len(msgs) != 1 {
				t.Fatalf("expected 1 message in %s inbox, got %d", role, len(msgs))
			}

			m := msgs[0]
			if m.Type != "request" {
				t.Errorf("expected type 'request', got %q", m.Type)
			}
			if m.Action != "startup" {
				t.Errorf("expected action 'startup', got %q", m.Action)
			}
			if m.From != role {
				t.Errorf("expected from %q, got %q", role, m.From)
			}
			if m.To != role {
				t.Errorf("expected to %q, got %q", role, m.To)
			}
			if !strings.Contains(m.Payload, "Session started") {
				t.Errorf("expected payload to contain 'Session started', got %q", m.Payload)
			}
			if !HasActionableMessages(session, role) {
				t.Errorf("expected %s startup message to be actionable, but it is not", role)
			}
		})
	}
}

func TestActivateVenv(t *testing.T) {
	tmpDir := t.TempDir()
	venvDir := filepath.Join(tmpDir, ".venv")
	os.MkdirAll(filepath.Join(venvDir, "bin"), 0o755)

	// Save original env
	origPath := os.Getenv("PATH")
	origVenv := os.Getenv("VIRTUAL_ENV")
	origPyHome := os.Getenv("PYTHONHOME")
	os.Setenv("PYTHONHOME", "/some/python")
	defer func() {
		os.Setenv("PATH", origPath)
		if origVenv != "" {
			os.Setenv("VIRTUAL_ENV", origVenv)
		} else {
			os.Unsetenv("VIRTUAL_ENV")
		}
		if origPyHome != "" {
			os.Setenv("PYTHONHOME", origPyHome)
		} else {
			os.Unsetenv("PYTHONHOME")
		}
	}()

	if err := ActivateVenv(venvDir); err != nil {
		t.Fatalf("ActivateVenv: %v", err)
	}

	// Check VIRTUAL_ENV is set to absolute path
	gotVenv := os.Getenv("VIRTUAL_ENV")
	if gotVenv != venvDir {
		t.Errorf("VIRTUAL_ENV = %q, want %q", gotVenv, venvDir)
	}

	// Check PATH has venv bin prepended
	gotPath := os.Getenv("PATH")
	wantPrefix := filepath.Join(venvDir, "bin")
	if !strings.HasPrefix(gotPath, wantPrefix) {
		t.Errorf("PATH should start with %q, got %q", wantPrefix, gotPath)
	}

	// Check PYTHONHOME is unset
	if v := os.Getenv("PYTHONHOME"); v != "" {
		t.Errorf("PYTHONHOME should be unset, got %q", v)
	}
}

func TestActivateVenv_RelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	venvDir := filepath.Join(tmpDir, "myenv")
	os.MkdirAll(filepath.Join(venvDir, "bin"), 0o755)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)

	if err := ActivateVenv("myenv"); err != nil {
		t.Fatalf("ActivateVenv: %v", err)
	}

	gotVenv := os.Getenv("VIRTUAL_ENV")
	if !filepath.IsAbs(gotVenv) {
		t.Errorf("VIRTUAL_ENV should be absolute, got %q", gotVenv)
	}
	// Normalize to handle /var -> /private/var symlink on macOS
	gotNorm, _ := filepath.EvalSymlinks(gotVenv)
	wantNorm, _ := filepath.EvalSymlinks(venvDir)
	if gotNorm != wantNorm {
		t.Errorf("VIRTUAL_ENV = %q, want %q", gotVenv, venvDir)
	}
}

func TestResolveExecPath_Absolute(t *testing.T) {
	got, err := ResolveExecPath("/usr/bin/true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/usr/bin/true" {
		t.Errorf("got %q, want /usr/bin/true", got)
	}
}

func TestResolveExecPath_LookPath(t *testing.T) {
	// "sh" should be findable on any Unix system
	got, err := ResolveExecPath("sh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestResolveExecPath_NotFound(t *testing.T) {
	_, err := ResolveExecPath("nonexistent-binary-xyz-123")
	if err == nil {
		t.Error("expected error for nonexistent binary")
	}
}

func TestRunAgentLaunch_ExecArgs(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()

	// Clear env to get defaults
	t.Setenv("AGENT_ROLE", "")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLAUDE_MODEL", "")
	t.Setenv("MUXCODE_CLAUDE_MODEL", "")
	t.Setenv("BUS_SESSION", "test-run-agent-launch")

	// Init bus directory
	Init("test-run-agent-launch", os.Getenv("BUS_DIR_BASE"))

	// Capture the exec call instead of actually exec-ing
	var capturedPath string
	var capturedArgv []string
	var capturedEnv []string
	origExec := execSyscall
	execSyscall = func(path string, argv []string, env []string) error {
		capturedPath = path
		capturedArgv = argv
		capturedEnv = env
		return nil // pretend exec succeeded (control returns for test)
	}
	defer func() { execSyscall = origExec }()

	err := RunAgentLaunch("build")

	// On systems without the default CLI binary, ResolveExecPath may fail.
	// That's OK — we test the exec path separately. Just check if we got
	// the exec call or a "cannot find" error.
	if err != nil {
		if strings.Contains(err.Error(), "cannot find") {
			// Expected on systems without the CLI installed — the exec
			// setup logic still ran correctly up to that point
			return
		}
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify exec was called
	if capturedPath == "" {
		t.Fatal("execSyscall was not called")
	}

	// Verify AGENT_ROLE was set
	agentRole := os.Getenv("AGENT_ROLE")
	if agentRole != "build" {
		t.Errorf("AGENT_ROLE = %q, want build", agentRole)
	}

	// Verify argv[0] is the binary name
	if len(capturedArgv) == 0 {
		t.Fatal("argv is empty")
	}

	// Verify env was passed
	if len(capturedEnv) == 0 {
		t.Fatal("env is empty")
	}

	// Verify AGENT_ROLE is in the env
	found := false
	for _, e := range capturedEnv {
		if strings.HasPrefix(e, "AGENT_ROLE=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("AGENT_ROLE not found in exec env")
	}
}

func TestRunAgentLaunch_VenvActivation(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Create a venv in the working directory
	origDir, _ := os.Getwd()
	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(projectDir, ".venv", "bin"), 0o755)
	os.WriteFile(filepath.Join(projectDir, ".venv", "bin", "activate"), []byte(""), 0o644)
	os.Chdir(projectDir)
	defer os.Chdir(origDir)

	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_BUILD_CLI", "")
	t.Setenv("BUS_SESSION", "test-venv-launch")

	Init("test-venv-launch", tmpDir)

	// Save original env
	origPath := os.Getenv("PATH")
	defer os.Setenv("PATH", origPath)

	// Capture exec
	origExec := execSyscall
	execSyscall = func(path string, argv []string, env []string) error {
		return nil
	}
	defer func() { execSyscall = origExec }()

	_ = RunAgentLaunch("build")

	// Check venv was activated
	gotVenv := os.Getenv("VIRTUAL_ENV")
	if gotVenv == "" {
		t.Error("VIRTUAL_ENV not set — venv activation didn't happen")
	} else if !strings.HasSuffix(gotVenv, ".venv") {
		t.Errorf("VIRTUAL_ENV = %q, expected to end with .venv", gotVenv)
	}
}

func TestRunAgentLaunch_SetsAgentRole(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()

	t.Setenv("AGENT_ROLE", "")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_COMMIT_CLI", "")
	t.Setenv("BUS_SESSION", "test-role-env")

	Init("test-role-env", os.Getenv("BUS_DIR_BASE"))

	origExec := execSyscall
	execSyscall = func(path string, argv []string, env []string) error {
		return nil
	}
	defer func() { execSyscall = origExec }()

	_ = RunAgentLaunch("commit")

	// "commit" normalizes to "commit" (not "git")
	got := os.Getenv("AGENT_ROLE")
	if got != "commit" {
		t.Errorf("AGENT_ROLE = %q, want commit", got)
	}
}

func TestRunAgentLaunch_LegacyRoleNormalization(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()

	t.Setenv("AGENT_ROLE", "")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_RUN_CLI", "")
	t.Setenv("BUS_SESSION", "test-legacy-role")

	Init("test-legacy-role", os.Getenv("BUS_DIR_BASE"))

	origExec := execSyscall
	execSyscall = func(path string, argv []string, env []string) error {
		return nil
	}
	defer func() { execSyscall = origExec }()

	_ = RunAgentLaunch("runner")

	// "runner" normalizes to "run"
	got := os.Getenv("AGENT_ROLE")
	if got != "run" {
		t.Errorf("AGENT_ROLE = %q, want run", got)
	}
}

func TestRunAgentLaunch_PresetAgentRolePreserved(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()

	// Simulate spawn agent: AGENT_ROLE is pre-set to the spawn identity
	t.Setenv("AGENT_ROLE", "spawn-edit-1")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_EDIT_CLI", "")
	t.Setenv("BUS_SESSION", "test-spawn-role")

	Init("test-spawn-role", os.Getenv("BUS_DIR_BASE"))

	origExec := execSyscall
	execSyscall = func(path string, argv []string, env []string) error {
		return nil
	}
	defer func() { execSyscall = origExec }()

	_ = RunAgentLaunch("edit")

	// Pre-set AGENT_ROLE should be preserved (not overwritten to "edit")
	got := os.Getenv("AGENT_ROLE")
	if got != "spawn-edit-1" {
		t.Errorf("AGENT_ROLE = %q, want spawn-edit-1 (pre-set value should be preserved)", got)
	}
}
