package bus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	case "agent":
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
	case "pr-read":
		return "MUXCODE_PR_READ_CLI"
	case "run", "runner":
		return "MUXCODE_RUN_CLI"
	case "api":
		return "MUXCODE_API_CLI"
	case "webhook":
		return "MUXCODE_WEBHOOK_CLI"
	case "agent":
		return "MUXCODE_AGENT_CLI"
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
	case "pr-read":
		return "MUXCODE_PR_READ_CLAUDE_MODEL"
	case "run", "runner":
		return "MUXCODE_RUN_CLAUDE_MODEL"
	case "api":
		return "MUXCODE_API_CLAUDE_MODEL"
	case "webhook":
		return "MUXCODE_WEBHOOK_CLAUDE_MODEL"
	case "agent":
		return "MUXCODE_AGENT_CLAUDE_MODEL"
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
	case "run", "runner":
		return "MUXCODE_RUN_CODEX_MODEL"
	default:
		return "MUXCODE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_")) + "_CODEX_MODEL"
	}
}

// RoleCodexModelDefault returns the default Codex model for a role.
// All roles default to gpt-5.3-codex.
func RoleCodexModelDefault(role string) string {
	return "gpt-5.3-codex"
}

// RoleClaudeModelDefault returns the default Claude model for a role.
// edit/review/analyze → opus, everything else → sonnet.
func RoleClaudeModelDefault(role string) string {
	switch role {
	case "plan", "planner", "edit", "review", "analyze", "analyst", "agent":
		return "claude-opus-4-6"
	case "build", "test", "api", "deploy", "run", "runner", "watch", "commit", "git":
		return "claude-sonnet-4-5"
	default:
		return ""
	}
}

// RoleOpenCodeModelDefault returns the default OpenCode model for a role.
// build/test/deploy/run/watch/commit → MiniMax M2.5 Free (via OpenCode Zen).
func RoleOpenCodeModelDefault(role string) string {
	switch role {
	case "build", "test", "deploy", "run", "runner", "watch", "commit", "git":
		return "minimax/m2.5-free"
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
	case "agent":
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
//  1. MUXCODE_AGENT_TASKS env var (explicit path)
//  2. .muxcode/agent-tasks.md (project-local)
//  3. ~/.config/muxcode/agent-tasks.md (user-global)
func ResolveTaskFile(role string) string {
	if role != "agent" {
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
	if v := os.Getenv("MUXCODE_AGENT_TASKS"); v != "" {
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
// It resolves the provider from environment variables, delegates CLI-specific
// configuration to the provider, and sets provider-independent fields.
func ResolveLaunchConfig(role string) *LaunchConfig {
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

// resolveClaudeModel resolves the Claude model for a role.
// Resolution: per-role env → global env → role default.
func resolveClaudeModel(role string) string {
	// Per-role env var
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
// Uses SendNoCC to avoid notification since the agent process isn't running yet —
// the daemon's startup check will notify once the agent is ready.
// cli is the resolved CLI binary name (e.g. "claude", "muxcode-llm-harness").
func PreLaunchSetup(role, session, cli string) {
	startupMsg := "Session started — review last saved context from memory to restore session state."

	// Pre-populate inbox for roles that need startup context restoration
	switch role {
	case "edit":
		m := Message{
			ID:      NewMsgID("edit"),
			TS:      time.Now().Unix(),
			From:    "edit",
			To:      "edit",
			Type:    "event",
			Action:  "notify",
			Payload: startupMsg,
		}
		_ = SendNoCC(session, m)
	case "analyze":
		m := Message{
			ID:      NewMsgID("analyze"),
			TS:      time.Now().Unix(),
			From:    "analyze",
			To:      "analyze",
			Type:    "event",
			Action:  "notify",
			Payload: startupMsg,
		}
		_ = SendNoCC(session, m)
	case "agent":
		agentStartupMsg := "Agent started — search Jira for available stories and present them to the user for selection."
		m := Message{
			ID:      NewMsgID("agent"),
			TS:      time.Now().Unix(),
			From:    "edit",
			To:      "agent",
			Type:    "request",
			Action:  "startup",
			Payload: agentStartupMsg,
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
