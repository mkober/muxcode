package bus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// LaunchConfig holds all resolved configuration for launching an agent.
type LaunchConfig struct {
	Role      string   // Agent role (e.g. "build", "edit")
	CLI       string   // Agent CLI binary (e.g. "claude", "muxcode-llm-harness", "opencode")
	Provider  Provider // AI CLI provider (resolved from MUXCODE_{ROLE}_CLI env var)
	IsLocal   bool     // True if routing to local LLM (harness/bus agent)
	Agent     string   // Agent definition filename (without .md)
	AgentFile string   // Resolved path to agent definition file (empty if not found)

	// Claude Code flags
	ModelFlags   []string // --model flags
	PermFlags    []string // --dangerously-skip-permissions for non-edit roles
	ToolFlags    []string // --allowedTools flags
	SharedPrompt string   // Combined append-system-prompt content

	// For launch_agent_from_file (agent file outside .claude/agents/)
	AgentName string // Agent name for --agent flag
	AgentJSON string // JSON for --agents flag

	// Local LLM
	HarnessArgs []string // Args for muxcode-llm-harness or bus agent run

	// Python venv
	VenvDir string // Resolved venv directory (empty if none)
}

// AgentFrontmatter holds extracted YAML frontmatter fields from an agent file.
type AgentFrontmatter struct {
	Description string
}

// AgentFileName maps role names to agent definition filenames (without .md).
// Returns "" for unknown roles.
func AgentFileName(role string) string {
	switch role {
	case "plan", "planner":
		return "planner"
	case "edit":
		return "code-editor"
	case "build":
		return "code-builder"
	case "test":
		return "test-runner"
	case "review":
		return "code-reviewer"
	case "deploy":
		return "infra-deployer"
	case "run", "runner":
		return "command-runner"
	case "commit", "git":
		return "git-manager"
	case "analyze", "analyst":
		return "editor-analyst"
	case "docs":
		return "doc-writer"
	case "research":
		return "code-researcher"
	case "watch":
		return "log-watcher"
	case "pr-read":
		return "pr-reader"
	case "api":
		return "api-tester"
	case "serve":
		return "dev-server"
	case "auto":
		return "autonomous-agent"
	default:
		return ""
	}
}

// RoleCLIEnvVar returns the per-role CLI env var name.
// Maps role names to MUXCODE_{ROLE}_CLI env vars.
func RoleCLIEnvVar(role string) string {
	switch role {
	case "plan", "planner":
		return "MUXCODE_PLAN_CLI"
	case "commit", "git":
		return "MUXCODE_COMMIT_CLI"
	case "build":
		return "MUXCODE_BUILD_CLI"
	case "test":
		return "MUXCODE_TEST_CLI"
	case "review":
		return "MUXCODE_REVIEW_CLI"
	case "deploy":
		return "MUXCODE_DEPLOY_CLI"
	case "edit":
		return "MUXCODE_EDIT_CLI"
	case "analyze", "analyst":
		return "MUXCODE_ANALYZE_CLI"
	case "docs":
		return "MUXCODE_DOCS_CLI"
	case "research":
		return "MUXCODE_RESEARCH_CLI"
	case "watch":
		return "MUXCODE_WATCH_CLI"
	case "serve":
		return "MUXCODE_SERVE_CLI"
	case "pr-read":
		return "MUXCODE_PR_READ_CLI"
	case "run", "runner":
		return "MUXCODE_RUN_CLI"
	case "api":
		return "MUXCODE_API_CLI"
	case "webhook":
		return "MUXCODE_WEBHOOK_CLI"
	case "auto":
		return "MUXCODE_AUTO_CLI"
	default:
		return "MUXCODE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_")) + "_CLI"
	}
}

// RoleClaudeModelEnvVar returns the per-role Claude model env var name.
func RoleClaudeModelEnvVar(role string) string {
	switch role {
	case "plan", "planner":
		return "MUXCODE_PLAN_CLAUDE_MODEL"
	case "commit", "git":
		return "MUXCODE_COMMIT_CLAUDE_MODEL"
	case "build":
		return "MUXCODE_BUILD_CLAUDE_MODEL"
	case "test":
		return "MUXCODE_TEST_CLAUDE_MODEL"
	case "review":
		return "MUXCODE_REVIEW_CLAUDE_MODEL"
	case "deploy":
		return "MUXCODE_DEPLOY_CLAUDE_MODEL"
	case "edit":
		return "MUXCODE_EDIT_CLAUDE_MODEL"
	case "analyze", "analyst":
		return "MUXCODE_ANALYZE_CLAUDE_MODEL"
	case "docs":
		return "MUXCODE_DOCS_CLAUDE_MODEL"
	case "research":
		return "MUXCODE_RESEARCH_CLAUDE_MODEL"
	case "watch":
		return "MUXCODE_WATCH_CLAUDE_MODEL"
	case "serve":
		return "MUXCODE_SERVE_CLAUDE_MODEL"
	case "pr-read":
		return "MUXCODE_PR_READ_CLAUDE_MODEL"
	case "run", "runner":
		return "MUXCODE_RUN_CLAUDE_MODEL"
	case "api":
		return "MUXCODE_API_CLAUDE_MODEL"
	case "webhook":
		return "MUXCODE_WEBHOOK_CLAUDE_MODEL"
	case "auto":
		return "MUXCODE_AUTO_CLAUDE_MODEL"
	default:
		return "MUXCODE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_")) + "_CLAUDE_MODEL"
	}
}

// RoleCodexModelEnvVar returns the per-role Codex model env var name.
func RoleCodexModelEnvVar(role string) string {
	switch role {
	case "commit", "git":
		return "MUXCODE_COMMIT_CODEX_MODEL"
	case "build":
		return "MUXCODE_BUILD_CODEX_MODEL"
	case "test":
		return "MUXCODE_TEST_CODEX_MODEL"
	case "review":
		return "MUXCODE_REVIEW_CODEX_MODEL"
	case "deploy":
		return "MUXCODE_DEPLOY_CODEX_MODEL"
	case "edit":
		return "MUXCODE_EDIT_CODEX_MODEL"
	case "analyze", "analyst":
		return "MUXCODE_ANALYZE_CODEX_MODEL"
	case "watch":
		return "MUXCODE_WATCH_CODEX_MODEL"
	case "serve":
		return "MUXCODE_SERVE_CODEX_MODEL"
	case "run", "runner":
		return "MUXCODE_RUN_CODEX_MODEL"
	default:
		return "MUXCODE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_")) + "_CODEX_MODEL"
	}
}

// RoleCodexModelDefault returns the default Codex model for a role.
// All roles default to gpt-5.5.
func RoleCodexModelDefault(role string) string {
	return "gpt-5.5"
}

// RoleClaudeModelDefault returns the default Claude model for a role.
// edit/auto → fable (flagship), plan/review/analyze → opus, everything else → sonnet.
func RoleClaudeModelDefault(role string) string {
	switch role {
	case "edit", "auto":
		return "claude-fable-5"
	case "plan", "planner", "review", "analyze", "analyst":
		return "claude-opus-5"
	case "build", "test", "api", "deploy", "run", "runner", "watch", "commit", "git", "serve":
		return "claude-sonnet-5"
	default:
		return ""
	}
}

// RoleOpenCodeModelDefault returns the default OpenCode model for a role.
// OpenCode model IDs require the provider prefix (e.g. "opencode-go/").
// review/analyze → Qwen 3.5 Plus (strong analytical model).
// build/test/deploy/run/watch → MiniMax M2.5 (fast command execution).
// commit → MiniMax M2.7 (git operations).
func RoleOpenCodeModelDefault(role string) string {
	switch role {
	case "edit":
		return "opencode-go/deepseek-v4-pro"
	case "review", "analyze", "analyst":
		return "opencode-go/qwen3.5-plus"
	case "research":
		return "opencode-go/deepseek-v4-pro"
	case "build", "test", "deploy", "run", "runner", "watch", "serve":
		return "opencode-go/minimax-m2.5"
	case "commit", "git":
		return "opencode-go/minimax-m2.7"
	default:
		return ""
	}
}

// InlineFallbackPrompt returns an inline system prompt for roles that have
// no agent definition file. Returns "" for unknown roles.
func InlineFallbackPrompt(role string) string {
	switch role {
	case "plan", "planner":
		return "You are the plan agent. Focus on maintaining project documentation — requirements specs, architecture docs, and planning artifacts. Read code changes, update docs, check off completed phases, record decisions. Scoped to docs/ directories only."
	case "edit":
		return "You are the edit agent. Focus on writing and modifying code. Make precise, minimal changes that follow existing patterns. One concern at a time."
	case "build":
		return "You are the build agent. Focus on building, compiling, and packaging. Run the project's build command. Diagnose and fix build failures."
	case "test":
		return "You are the test agent. Focus on writing, running, and debugging tests. Run the project's test command. Analyze failures and suggest fixes."
	case "review":
		return "You are the review agent. Focus on reviewing code for correctness, security, and quality. Run git diff and provide feedback organized by severity."
	case "deploy":
		return "You are the deploy agent. Focus on infrastructure as code and deployments. Write, review, and debug infrastructure definitions. Run deployment diffs. Check security and compliance."
	case "run", "runner":
		return "You are the run agent. Focus on executing commands and processes. Confirm target environment before running. Show command and parse responses. Report errors clearly."
	case "commit", "git":
		return "You are the commit agent. Focus on git operations: branches, commits, rebasing, PRs. Run git status, git diff, gh pr commands. Keep the repo clean."
	case "analyze", "analyst":
		return "You are the analyze agent. Evaluate code changes, builds, tests, reviews, deployments, and runs. Explain what happened, why it matters, and what to watch for. Highlight patterns and concepts. Be concise but informative."
	case "docs":
		return "You are the docs agent. Generate, update, and maintain project documentation. Read code changes, update READMEs, write doc comments, maintain changelogs. Keep docs accurate and in sync with the code."
	case "research":
		return "You are the research agent. Search the web, read documentation, explore codebases, and answer technical questions. Provide concise findings with sources. Summarize APIs, libraries, and patterns."
	case "watch":
		return "You are the watch agent. Monitor logs from local files, CloudWatch, Kubernetes, and Docker. Tail logs, detect errors, summarize patterns. Report findings to the edit agent via the bus."
	case "pr-read":
		return "You are the pr-read agent. Read GitHub PR reviews and CI check failures, then report suggested fixes to the edit agent. Use gh pr view, gh pr checks, gh api to read feedback. Never modify files directly — report suggestions only. The edit agent will prompt the user before making changes."
	case "api":
		return "You are the API testing agent. Manage API collections and environments using muxcode api subcommands. Execute requests via curl with jq formatting. Support variable substitution from environments. Log requests to history. Report results (status, timing, response) to the edit agent."
	case "auto":
		return "You are the autonomous agent. Poll Jira for assigned stories, create requirements docs, implement features, and submit PRs — all without user intervention. Delegate freely to build, test, review, deploy, run, watch, and commit agents via the message bus."
	default:
		return "You are a general-purpose coding assistant."
	}
}

// ExtractFrontmatter parses YAML frontmatter from agent definition content.
// Returns the frontmatter fields and the body (content after frontmatter).
func ExtractFrontmatter(content string) (AgentFrontmatter, string) {
	var fm AgentFrontmatter

	if !strings.HasPrefix(content, "---") {
		return fm, content
	}

	// Find second ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return fm, content
	}

	// Parse frontmatter fields
	fmBlock := rest[:idx]
	scanner := bufio.NewScanner(strings.NewReader(fmBlock))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "description:") {
			fm.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}

	// Body is everything after second --- + newline
	body := rest[idx+4:]
	if strings.HasPrefix(body, "\n") {
		body = body[1:]
	}

	return fm, body
}

// ResolveAgentFile searches for an agent definition file in 3-tier priority order:
//  1. .claude/agents/<name>.md (project-local)
//  2. ~/.config/muxcode/agents/<name>.md (user config)
//  3. <installDir>/agents/<name>.md (muxcode defaults)
//
// Returns (path, tier) where tier is 1/2/3, or ("", 0) if not found.
// installDir is the install directory for muxcode defaults (e.g. from MUXCODE_INSTALL_DIR).
func ResolveAgentFile(name, installDir string) (string, int) {
	if name == "" {
		return "", 0
	}

	// Tier 1: project-local
	p := filepath.Join(".claude", "agents", name+".md")
	if _, err := os.Stat(p); err == nil {
		return p, 1
	}

	// Tier 2: user config
	home, _ := os.UserHomeDir()
	if home != "" {
		p = filepath.Join(home, ".config", "muxcode", "agents", name+".md")
		if _, err := os.Stat(p); err == nil {
			return p, 2
		}
	}

	// Tier 3: install dir defaults
	if installDir != "" {
		p = filepath.Join(installDir, "agents", name+".md")
		if _, err := os.Stat(p); err == nil {
			return p, 3
		}
	}

	return "", 0
}

// BuildAgentsJSON constructs the JSON value for Claude Code's --agents flag.
// This is used when the agent file is outside .claude/agents/ (tiers 2 and 3).
func BuildAgentsJSON(name, description, prompt string) (string, error) {
	type agentDef struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}

	agents := map[string]agentDef{
		name: {
			Description: description,
			Prompt:      prompt,
		},
	}

	data, err := json.Marshal(agents)
	if err != nil {
		return "", fmt.Errorf("marshal agents JSON: %w", err)
	}
	return string(data), nil
}

// ResolveVenv finds an active Python venv directory.
// Checks MUXCODE_VENV_DIR env, then .venv, then venv.
// Returns "" if no venv found.
func ResolveVenv() string {
	if v := os.Getenv("MUXCODE_VENV_DIR"); v != "" {
		activate := filepath.Join(v, "bin", "activate")
		if _, err := os.Stat(activate); err == nil {
			return v
		}
	}
	for _, candidate := range []string{".venv", "venv"} {
		activate := filepath.Join(candidate, "bin", "activate")
		if _, err := os.Stat(activate); err == nil {
			return candidate
		}
	}
	return ""
}

// BuildSharedPrompt assembles the combined --append-system-prompt content
// from shared prompt, skills, context, task file, and session resume for a role.
func BuildSharedPrompt(role string) string {
	var parts []string

	// 1. Shared coordination prompt
	if prompt := SharedPrompt(role); prompt != "" {
		parts = append(parts, prompt)
	}

	// 2. Skills
	skills, _ := SkillsForRole(role)
	if skillPrompt := FormatSkillsPrompt(skills); skillPrompt != "" {
		parts = append(parts, skillPrompt)
	}

	// 3. Context files
	ctxFiles, _ := AllContextFilesForRole(role)
	if ctxPrompt := FormatContextPrompt(ctxFiles); ctxPrompt != "" {
		parts = append(parts, ctxPrompt)
	}

	// 4. Task file (agent role only)
	if taskPrompt := ResolveTaskFile(role); taskPrompt != "" {
		parts = append(parts, taskPrompt)
	}

	// 5. Session resume
	resume, _ := ResumeContext(role)
	if resume != "" {
		parts = append(parts, resume)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// ResolveTaskFile reads the natural-language task file for the agent role.
// Returns empty string for non-agent roles or if no task file is found.
//
// Resolution order:
//  1. MUXCODE_AUTO_TASKS env var (explicit path)
//  2. .muxcode/agent-tasks.md (project-local)
//  3. ~/.config/muxcode/agent-tasks.md (user-global)
func ResolveTaskFile(role string) string {
	if role != "auto" {
		return ""
	}
	path := resolveTaskFilePath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	return "## Agent task configuration\n\n" +
		"The following task configuration was loaded from `" + path + "`.\n" +
		"Environment variables override these values when set.\n\n" +
		content
}

// resolveTaskFilePath returns the path to the agent task file, or empty if none found.
func resolveTaskFilePath() string {
	// 1. Explicit env var
	if v := os.Getenv("MUXCODE_AUTO_TASKS"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	// 2. Project-local
	local := filepath.Join(".muxcode", "agent-tasks.md")
	if _, err := os.Stat(local); err == nil {
		return local
	}
	// 3. User-global
	home, _ := os.UserHomeDir()
	if home != "" {
		global := filepath.Join(home, ".config", "muxcode", "agent-tasks.md")
		if _, err := os.Stat(global); err == nil {
			return global
		}
	}
	return ""
}

// ResolveLaunchConfig resolves all configuration needed to launch an agent.
// It loads runtime overrides first (highest priority), then resolves the
// provider from environment variables, delegates CLI-specific configuration
// to the provider, and sets provider-independent fields.
func ResolveLaunchConfig(role string) *LaunchConfig {
	// Load runtime overrides first (highest priority).
	// Override files set env vars that are picked up by the existing
	// os.Getenv resolution in ResolveProvider, resolveClaudeModel,
	// resolveOpenCodeModel, etc.
	_ = LoadRuntimeOverrides(BusSession(), role)

	cfg := &LaunchConfig{
		Role: role,
	}

	// Resolve provider and CLI from environment variables
	cfg.Provider = ResolveProvider(role)
	cfg.CLI = ResolveProviderCLI(role)

	// Delegate CLI-specific configuration to provider
	// (agent file resolution, model flags, permissions, tools, prompt)
	cfg.Provider.ConfigureLaunch(cfg, role)

	// Python venv (provider-independent)
	cfg.VenvDir = ResolveVenv()

	return cfg
}

// BuildExecArgs constructs the final CLI arguments for launching an agent.
// Returns (binary, args) suitable for syscall.Exec or exec.Command.
// Delegates to the Provider when set; falls back to type-based dispatch
// for manually constructed LaunchConfig (e.g. tests).
func (c *LaunchConfig) BuildExecArgs() (string, []string) {
	if c.Provider != nil {
		return c.Provider.BuildExecArgs(c)
	}
	// Legacy fallback for manually constructed LaunchConfig (e.g. tests)
	if c.IsLocal {
		p := &LocalProvider{}
		return p.BuildExecArgs(c)
	}
	p := &ClaudeCodeProvider{}
	return p.BuildExecArgs(c)
}

// buildHarnessArgs constructs args for the local LLM harness.
func buildHarnessArgs(role string) []string {
	args := []string{"run", role}

	// Per-role model: MUXCODE_{ROLE}_MODEL → MUXCODE_OLLAMA_MODEL → default
	model := RoleModel(role)
	defaultModel := DefaultOllamaConfig().Model
	if model != defaultModel {
		args = append(args, "--model", model)
	}

	// Custom Ollama URL
	ollamaURL := os.Getenv("MUXCODE_OLLAMA_URL")
	if ollamaURL != "" && ollamaURL != "http://localhost:11434" {
		args = append(args, "--url", ollamaURL)
	}

	// TUI mode: enabled by default, disable with MUXCODE_HARNESS_TUI=0
	if os.Getenv("MUXCODE_HARNESS_TUI") != "0" {
		args = append(args, "--tui")
	}

	return args
}

// RoleConfig holds the effective CLI, model, and resolution source for a role.
type RoleConfig struct {
	Role      string // agent role name
	CLI       string // effective CLI (claude, opencode, codex, local)
	CLISource string // where CLI was resolved from
	Model     string // effective model
	ModelSrc  string // where model was resolved from
}

// EffectiveConfig returns the resolved CLI, model, and resolution source
// for a role. Does not mutate process environment — reads override files
// and env vars without calling os.Setenv.
func EffectiveConfig(role string) RoleConfig {
	rc := RoleConfig{Role: role}
	session := BusSession()

	// --- CLI resolution ---
	cliKey := RoleCLIEnvVar(role)

	// 1. Runtime override (session-scoped)
	if session != "" {
		if overrides, err := ReadRuntimeOverrides(session, role); err == nil && overrides != nil {
			if v, ok := overrides[cliKey]; ok && v != "" {
				rc.CLI = v
				rc.CLISource = "runtime override"
			}
		}
	}
	// 2. Per-role env var
	if rc.CLI == "" {
		if v := os.Getenv(cliKey); v != "" {
			rc.CLI = v
			rc.CLISource = "env: " + cliKey
		}
	}
	// 3. Session-wide env var
	if rc.CLI == "" {
		if v := os.Getenv("MUXCODE_AGENT_CLI"); v != "" {
			rc.CLI = v
			rc.CLISource = "env: MUXCODE_AGENT_CLI"
		}
	}
	// 4. Config file
	if rc.CLI == "" {
		cfg := GetShellConfig("")
		if v, ok := cfg[cliKey]; ok && v != "" {
			rc.CLI = v
			rc.CLISource = "config file"
		}
	}
	// 5. Built-in default
	if rc.CLI == "" {
		rc.CLI = roleDefaultCLI(role)
		rc.CLISource = "default"
	}

	// --- Model resolution ---
	modelKey := RoleModelEnvVar(role)

	// 1. Runtime override (session-scoped)
	if session != "" {
		if overrides, err := ReadRuntimeOverrides(session, role); err == nil && overrides != nil {
			if v, ok := overrides[modelKey]; ok && v != "" {
				rc.Model = v
				rc.ModelSrc = "runtime override"
			}
		}
	}
	// 2. Generic per-role env var (MUXCODE_{ROLE}_MODEL)
	if rc.Model == "" {
		if v := os.Getenv(modelKey); v != "" {
			rc.Model = v
			rc.ModelSrc = "env: " + modelKey
		}
	}
	// 3. Provider-specific per-role env var (e.g. MUXCODE_{ROLE}_CLAUDE_MODEL)
	if rc.Model == "" {
		var providerModelKey string
		switch rc.CLI {
		case "opencode":
			// OpenCode uses generic model key only (no separate _OPENCODE_MODEL)
		case "codex":
			providerModelKey = RoleCodexModelEnvVar(role)
		default:
			providerModelKey = RoleClaudeModelEnvVar(role)
		}
		if providerModelKey != "" {
			if v := os.Getenv(providerModelKey); v != "" {
				rc.Model = v
				rc.ModelSrc = "env: " + providerModelKey
			}
		}
	}
	// 4. Config file
	if rc.Model == "" {
		cfg := GetShellConfig("")
		if v, ok := cfg[modelKey]; ok && v != "" {
			rc.Model = v
			rc.ModelSrc = "config file"
		}
	}
	// 5. Provider-specific default
	if rc.Model == "" {
		switch rc.CLI {
		case "opencode":
			if m := RoleOpenCodeModelDefault(role); m != "" {
				rc.Model = m
				rc.ModelSrc = "default"
			}
		case "codex":
			rc.Model = RoleCodexModelDefault(role)
			rc.ModelSrc = "default"
		default:
			rc.Model = RoleClaudeModelDefault(role)
			rc.ModelSrc = "default"
		}
	}

	return rc
}

// RoleModelEnvVar returns the generic model env var key for a role.
// This is used by all providers as a common override mechanism.
func RoleModelEnvVar(role string) string {
	return "MUXCODE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_")) + "_MODEL"
}

// resolveClaudeModel resolves the Claude model for a role.
// Resolution: generic per-role env → Claude-specific per-role env → global Claude env → role default.
func resolveClaudeModel(role string) string {
	// Generic per-role env var (MUXCODE_{ROLE}_MODEL) - shared across providers
	if v := os.Getenv(RoleModelEnvVar(role)); v != "" {
		return v
	}

	// Per-role env var (MUXCODE_{ROLE}_CLAUDE_MODEL)
	envVar := RoleClaudeModelEnvVar(role)
	if v := os.Getenv(envVar); v != "" {
		return v
	}

	// Global env override
	if v := os.Getenv("MUXCODE_CLAUDE_MODEL"); v != "" {
		return v
	}

	// Role default
	return RoleClaudeModelDefault(role)
}

// resolveInstallDir determines the install directory for muxcode defaults.
// Checks MUXCODE_INSTALL_DIR env, then derives from binary location.
func resolveInstallDir() string {
	if v := os.Getenv("MUXCODE_INSTALL_DIR"); v != "" {
		return v
	}

	// Derive from binary location: ~/.local/bin/muxcode → ~/.config/muxcode
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	binDir := filepath.Dir(exe)
	// If binary is in ~/.local/bin, config is in ~/.config/muxcode
	// Check both: parent is ".local" AND leaf is "bin" (i.e. ~/.local/bin/)
	if filepath.Base(filepath.Dir(binDir)) == ".local" && filepath.Base(binDir) == "bin" {
		home, _ := os.UserHomeDir()
		if home != "" {
			return filepath.Join(home, ".config", "muxcode")
		}
	}
	return ""
}

// lookPath is a testable wrapper around exec.LookPath.
var lookPath = exec.LookPath

// PreLaunchSetup performs pre-launch actions: startup inbox message, lifecycle log.
// Uses SendNoCC to avoid notifying before the agent process exists. The startup
// message is request-type so the daemon's actionable-message wake-up paths
// (which ignore event/response messages) will re-notify the agent if the
// launch-time send-keys wake-up is missed.
// cli is the resolved CLI binary name (e.g. "claude", "muxcode-llm-harness").
func PreLaunchSetup(role, session, cli string) {
	startupMsg := "Session started — review last saved context from memory to restore session state."

	// Auto agent gets a special startup message (task-oriented, not context restoration)
	if role == "auto" {
		agentStartupMsg := "Agent started — search Jira for available stories and present them to the user for selection."
		m := Message{
			ID:      NewMsgID("auto"),
			TS:      time.Now().Unix(),
			From:    "edit",
			To:      "auto",
			Type:    "request",
			Action:  "startup",
			Payload: agentStartupMsg,
		}
		_ = SendNoCC(session, m)
	} else {
		// All agents get a startup inbox message so they check inbox on launch,
		// read memory, and restore session context. Without this, agents that
		// launch into an empty inbox sit idle and never restore prior state.
		//
		// Type MUST be "request" (not "event"): the daemon's wake-up paths gate
		// on HasActionableMessages(), which only counts request-type messages.
		// An event-type startup message is invisible to the daemon, so if the
		// one-shot launch-time send-keys wake-up is dropped (Claude Code's TUI
		// can ignore keystrokes during its post-prompt init phase), there is no
		// recovery and the agent never restores context. As a request, the
		// daemon's safety-net re-wakes the agent until the inbox is consumed.
		// This mirrors the auto agent's startup message above.
		m := Message{
			ID:      NewMsgID(role),
			TS:      time.Now().Unix(),
			From:    role,
			To:      role,
			Type:    "request",
			Action:  "startup",
			Payload: startupMsg,
		}
		_ = SendNoCC(session, m)
	}

	// Log agent launch to persistent lifecycle log
	if session != "" {
		logCLI := cli
		if logCLI == "" {
			logCLI = "claude"
		}
		LogLifecycle(session, "info", "agent", "launch",
			fmt.Sprintf("role=%s cli=%s", role, logCLI))
	}
}

// ActivateVenv sets PATH and VIRTUAL_ENV environment variables to activate
// a Python venv. This is equivalent to `source <venv>/bin/activate`.
// Returns an error if the venv directory path cannot be resolved.
func ActivateVenv(venvDir string) error {
	absVenv, err := filepath.Abs(venvDir)
	if err != nil {
		return fmt.Errorf("resolve venv path %q: %w", venvDir, err)
	}
	binDir := filepath.Join(absVenv, "bin")
	os.Setenv("VIRTUAL_ENV", absVenv)
	// Prepend venv bin to PATH
	os.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Remove PYTHONHOME if set (matches activate script behavior)
	os.Unsetenv("PYTHONHOME")
	return nil
}

// ResolveExecPath finds the absolute path for a binary name.
func ResolveExecPath(binary string) (string, error) {
	if filepath.IsAbs(binary) {
		return binary, nil
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// execSyscall is a testable wrapper around syscall.Exec.
// Tests replace this to capture the exec call without replacing the process.
var execSyscall = syscall.Exec

// RunAgentLaunch performs the complete agent launch sequence:
// load config, resolve provider/CLI/model/tools, pre-launch setup,
// activate venv, set AGENT_ROLE, clear terminal, and exec into the CLI.
// On success, syscall.Exec replaces the process — this function does not return.
// This is the Go-native agent launcher.
func RunAgentLaunch(role string) error {
	// Load shell-sourceable config (same resolution as LoadShellConfig)
	LoadShellConfig("")

	// Resolve all launch configuration
	cfg := ResolveLaunchConfig(role)

	// Pre-launch: generate agent config for non-Claude providers
	if cfg.Provider != nil {
		if err := cfg.Provider.WriteAgentConfig(role); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: WriteAgentConfig(%s): %v\n", role, err)
		}
	}

	// Pre-launch: startup inbox message + lifecycle log
	session := BusSession()
	binary, launchArgs := cfg.BuildExecArgs()
	PreLaunchSetup(role, session, binary)

	// Clear stale in-flight tasks addressed to this role. This launch is a fresh
	// agent instance, which cannot be mid-processing a task delivered to a prior
	// instance — so any task still in-flight is stale and would otherwise block
	// every new send of the same (to, action) via the dedup guard until the 600s
	// TaskExpired grace. This is the durable fix for the crashed/restarted agent
	// going silently undeliverable (a stuck chain task dropping re-sent requests).
	if n := ClearInFlightTasksForRole(session, role); n > 0 {
		LogLifecycle(session, "info", "launch", "cleared-inflight-tasks",
			fmt.Sprintf("%s: %d stale in-flight task(s) cleared on launch", role, n))
	}

	// Stamp the definition hash this agent is launching with so the daemon's
	// agent-defs watchdog can detect a later on-disk change and auto-reload.
	// Done before exec (below) replaces this process.
	StampAgentDefHash(session, role)

	// Activate Python venv if found
	if cfg.VenvDir != "" {
		if err := ActivateVenv(cfg.VenvDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: venv activation: %v\n", err)
		}
	}

	// Export AGENT_ROLE so child processes (e.g. `muxcode send`) can identify
	// the sender. Respect pre-set values (spawn agents set AGENT_ROLE to
	// their spawn-specific identity before calling RunAgentLaunch).
	if os.Getenv("AGENT_ROLE") == "" {
		os.Setenv("AGENT_ROLE", NormalizeBusRole(role))
	}

	// Clear terminal for clean agent startup
	fmt.Print("\033[2J\033[H")

	// Resolve binary to absolute path for exec
	binPath, err := ResolveExecPath(binary)
	if err != nil {
		return fmt.Errorf("cannot find %s: %w", binary, err)
	}

	// Build argv for exec (argv[0] must be the binary name)
	argv := append([]string{binary}, launchArgs...)

	// Replace this process with the agent CLI
	if err := execSyscall(binPath, argv, os.Environ()); err != nil {
		return fmt.Errorf("exec %s: %w", binary, err)
	}

	return nil // unreachable after successful exec
}
