package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MuxcodeConfig holds tool profiles, event chains, auto-CC, and send policy config.
type MuxcodeConfig struct {
	SharedTools  map[string][]string    `json:"shared_tools"`
	ToolProfiles map[string]ToolProfile `json:"tool_profiles"`
	EventChains  map[string]EventChain  `json:"event_chains"`
	AutoCC       []string               `json:"auto_cc"`
	SendPolicy   map[string]SendPolicy  `json:"send_policy,omitempty"`
}

// SendPolicy defines send restrictions for a role.
type SendPolicy struct {
	Deny []string `json:"deny"`
}

// ToolProfile defines allowed tools for a role.
type ToolProfile struct {
	Include     []string `json:"include,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	DenyTools   []string `json:"deny_tools,omitempty"` // prohibited command patterns (for non-hook providers)
	CdPrefix    bool     `json:"cd_prefix,omitempty"`
	BashTimeout int      `json:"bash_timeout,omitempty"` // seconds, 0 = default (60s)
}

// EventChain defines actions triggered by command outcomes.
type EventChain struct {
	OnSuccess       ChainActions `json:"on_success,omitempty"`
	OnFailure       ChainActions `json:"on_failure,omitempty"`
	OnUnknown       ChainActions `json:"on_unknown,omitempty"`
	NotifyAnalyst   bool         `json:"notify_analyst"`
	NotifyAnalystOn []string     `json:"notify_analyst_on,omitempty"`
	NotifyPlanOn    []string     `json:"notify_plan_on,omitempty"`
}

// ChainActions wraps []ChainAction with custom JSON marshal/unmarshal
// to support both single-object and array forms in config.
type ChainActions []ChainAction

func (ca *ChainActions) UnmarshalJSON(data []byte) error {
	// Try array first
	var arr []ChainAction
	if err := json.Unmarshal(data, &arr); err == nil {
		*ca = arr
		return nil
	}
	// Fall back to single object
	var single ChainAction
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	*ca = ChainActions{single}
	return nil
}

func (ca ChainActions) MarshalJSON() ([]byte, error) {
	// Preserve single-object format when only one action (config readability)
	if len(ca) == 1 {
		return json.Marshal(ca[0])
	}
	return json.Marshal([]ChainAction(ca))
}

// ChainAction is a single action in an event chain.
type ChainAction struct {
	SendTo     string         `json:"send_to"`
	Action     string         `json:"action"`
	Message    string         `json:"message"`
	Type       string         `json:"type"`
	Conditions map[string]any `json:"conditions,omitempty"`
}

// configSingleton is the lazy-loaded config (single-goroutine safe).
var configSingleton *MuxcodeConfig

// Config returns the lazy-loaded config singleton.
func Config() *MuxcodeConfig {
	if configSingleton == nil {
		cfg, err := LoadConfig()
		if err != nil {
			cfg = DefaultConfig()
		}
		configSingleton = cfg
	}
	return configSingleton
}

// SetConfig overrides the config singleton (for tests).
func SetConfig(cfg *MuxcodeConfig) {
	configSingleton = cfg
	autoCCCache = nil
}

// LoadConfig resolves config from project > user > defaults.
func LoadConfig() (*MuxcodeConfig, error) {
	paths := []string{
		filepath.Join(".muxcode", "muxcode.json"),
		filepath.Join(configDir(), "muxcode.json"),
	}

	var loaded *MuxcodeConfig
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue // file doesn't exist — expected
		}
		var cfg MuxcodeConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", p, err)
			continue
		}
		if loaded == nil {
			loaded = &cfg
		} else {
			// Earlier files take priority — merge loaded (project) over cfg (user)
			loaded = mergeConfigs(&cfg, loaded)
		}
	}

	if loaded == nil {
		return DefaultConfig(), nil
	}

	// Merge over defaults so missing roles still work
	result := mergeConfigs(DefaultConfig(), loaded)

	// Validate conditions — emit warnings for unknown types
	for _, w := range ValidateConfig(result) {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	return result, nil
}

// configDir returns the user config directory.
func configDir() string {
	if v := os.Getenv("MUXCODE_CONFIG_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "muxcode")
}

// mergeConfigs overlays the override config on top of the base config.
// Override values replace base values at the role/key level.
func mergeConfigs(base, override *MuxcodeConfig) *MuxcodeConfig {
	result := &MuxcodeConfig{
		SharedTools:  make(map[string][]string),
		ToolProfiles: make(map[string]ToolProfile),
		EventChains:  make(map[string]EventChain),
		SendPolicy:   make(map[string]SendPolicy),
	}

	// Copy base shared tools
	for k, v := range base.SharedTools {
		result.SharedTools[k] = v
	}
	// Override shared tools
	for k, v := range override.SharedTools {
		result.SharedTools[k] = v
	}

	// Copy base tool profiles
	for k, v := range base.ToolProfiles {
		result.ToolProfiles[k] = v
	}
	// Merge override tool profiles field-by-field so partial overrides
	// don't drop fields like BashTimeout that only exist in defaults.
	for k, ov := range override.ToolProfiles {
		base, exists := result.ToolProfiles[k]
		if !exists {
			result.ToolProfiles[k] = ov
			continue
		}
		if len(ov.Include) > 0 {
			base.Include = ov.Include
		}
		if len(ov.Tools) > 0 {
			base.Tools = ov.Tools
		}
		if len(ov.DenyTools) > 0 {
			base.DenyTools = ov.DenyTools
		}
		// Always copy CdPrefix — false is a valid override value
		base.CdPrefix = ov.CdPrefix
		if ov.BashTimeout > 0 {
			base.BashTimeout = ov.BashTimeout
		}
		result.ToolProfiles[k] = base
	}

	// Copy base event chains
	for k, v := range base.EventChains {
		result.EventChains[k] = v
	}
	// Override event chains (entire chain replaced per event type)
	for k, v := range override.EventChains {
		result.EventChains[k] = v
	}

	// Auto-CC: override replaces entirely if present
	if len(override.AutoCC) > 0 {
		result.AutoCC = override.AutoCC
	} else {
		result.AutoCC = base.AutoCC
	}

	// Copy base send policies
	for k, v := range base.SendPolicy {
		result.SendPolicy[k] = v
	}
	// Override send policies (entire policy replaced per role)
	for k, v := range override.SendPolicy {
		result.SendPolicy[k] = v
	}

	return result
}

// resolveRoleAlias normalizes legacy profile key aliases to canonical role names.
// Canonical names match tmux window names: commit, analyze, run.
// Legacy aliases (git, analyst, runner) are accepted for backward compatibility.
func resolveRoleAlias(role string) string {
	switch role {
	case "git":
		return "commit"
	case "analyst":
		return "analyze"
	case "runner":
		return "run"
	case "planner":
		return "plan"
	case "agent":
		return "auto"
	default:
		return role
	}
}

// ResolveTools expands a role's tool profile into a flat tool list.
func ResolveTools(role string) []string {
	cfg := Config()
	profile, ok := cfg.ToolProfiles[resolveRoleAlias(role)]
	if !ok {
		return nil
	}
	return resolveProfile(cfg, profile)
}

// RoleBashTimeout returns the bash timeout in seconds for a role.
// Returns 0 if the role has no custom timeout (caller should use default).
func RoleBashTimeout(role string) int {
	cfg := Config()
	profile, ok := cfg.ToolProfiles[resolveRoleAlias(role)]
	if !ok {
		return 0
	}
	return profile.BashTimeout
}

// resolveProfile expands includes, tools, and cd-prefix variants.
func resolveProfile(cfg *MuxcodeConfig, profile ToolProfile) []string {
	seen := make(map[string]bool)
	var tools []string

	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			tools = append(tools, t)
		}
	}

	// Expand included shared tool groups
	for _, groupName := range profile.Include {
		if group, ok := cfg.SharedTools[groupName]; ok {
			for _, t := range group {
				add(t)
			}
		}
	}

	// Add direct tools
	for _, t := range profile.Tools {
		add(t)
		if profile.CdPrefix {
			if cd := expandCdPrefix(t); cd != "" {
				add(cd)
			}
		}
	}

	return tools
}

// expandCdPrefix generates a "Bash(cd * && ...)" variant of a Bash tool pattern.
// Returns "" for non-Bash tools, already-prefixed tools, and malformed patterns.
func expandCdPrefix(tool string) string {
	if !strings.HasPrefix(tool, "Bash(") || !strings.HasSuffix(tool, ")") {
		return ""
	}
	// Extract inner command: "Bash(git *)" -> "git *"
	inner := tool[5 : len(tool)-1]
	if strings.HasPrefix(inner, "cd ") {
		return "" // already has cd prefix
	}
	return "Bash(cd * && " + inner + ")"
}

// ResolveChain looks up the chain action for an event type and outcome.
// When the outcome has multiple actions (array), they are evaluated in order —
// first action whose conditions all pass is returned (first-match wins).
// An action with no conditions acts as an unconditional fallback.
// When ctx is nil, conditions are skipped (backward compatible) and the first
// action is returned.
func ResolveChain(eventType, outcome string, ctx *ChainContext) *ChainAction {
	cfg := Config()
	chain, ok := cfg.EventChains[eventType]
	if !ok {
		return nil
	}
	actions := resolveActions(chain, outcome)
	if len(actions) == 0 {
		return nil
	}
	for i := range actions {
		a := &actions[i]
		if ctx != nil && len(a.Conditions) > 0 {
			passed, _ := EvaluateConditions(a.Conditions, ctx)
			if !passed {
				continue
			}
		}
		return a
	}
	return nil
}

// resolveActions returns the action list for an outcome.
func resolveActions(chain EventChain, outcome string) ChainActions {
	switch outcome {
	case "success":
		return chain.OnSuccess
	case "failure":
		return chain.OnFailure
	case "unknown":
		return chain.OnUnknown
	}
	return nil
}

// ResolveChainVerbose is like ResolveChain but returns condition evaluation details.
// Used by the CLI --verbose flag. Returns all condition results across all actions
// evaluated (not just the matching one).
func ResolveChainVerbose(eventType, outcome string, ctx *ChainContext) (*ChainAction, []ConditionResult) {
	cfg := Config()
	chain, ok := cfg.EventChains[eventType]
	if !ok {
		return nil, nil
	}
	actions := resolveActions(chain, outcome)
	if len(actions) == 0 {
		return nil, nil
	}
	var allResults []ConditionResult
	for i := range actions {
		a := &actions[i]
		if ctx != nil && len(a.Conditions) > 0 {
			passed, results := EvaluateConditions(a.Conditions, ctx)
			allResults = append(allResults, results...)
			if !passed {
				continue
			}
			return a, allResults
		}
		// Unconditional action — matches immediately
		return a, allResults
	}
	return nil, allResults
}

// ChainNotifyAnalyst returns whether the chain should notify the analyst.
// Deprecated: Use ChainShouldNotifyAnalyst for outcome-conditional checks.
func ChainNotifyAnalyst(eventType string) bool {
	cfg := Config()
	chain, ok := cfg.EventChains[eventType]
	if !ok {
		return false
	}
	return chain.NotifyAnalyst
}

// ChainShouldNotifyAnalyst returns whether the chain should notify the analyst
// for the given outcome. If NotifyAnalystOn is set, the outcome must match an
// entry (or "*" wildcard). If NotifyAnalystOn is empty, falls back to the
// boolean NotifyAnalyst field for backward compatibility.
func ChainShouldNotifyAnalyst(eventType, outcome string) bool {
	cfg := Config()
	chain, ok := cfg.EventChains[eventType]
	if !ok {
		return false
	}

	// New field takes precedence
	if len(chain.NotifyAnalystOn) > 0 {
		for _, o := range chain.NotifyAnalystOn {
			if o == "*" || o == outcome {
				return true
			}
		}
		return false
	}

	// Legacy fallback
	return chain.NotifyAnalyst
}

// ChainShouldNotifyPlan returns whether the chain should notify the plan agent
// for the given outcome. The plan agent is notified to verify implementation
// progress against the active requirements spec. Only fires when NotifyPlanOn
// is set and the outcome matches an entry (or "*" wildcard).
func ChainShouldNotifyPlan(eventType, outcome string) bool {
	cfg := Config()
	chain, ok := cfg.EventChains[eventType]
	if !ok {
		return false
	}
	for _, o := range chain.NotifyPlanOn {
		if o == "*" || o == outcome {
			return true
		}
	}
	return false
}

// ExpandMessage substitutes template variables in a chain message.
// Supported: ${exit_code}, ${command}, ${branch}, ${changed_files}
func ExpandMessage(template, exitCode, command string) string {
	s := strings.ReplaceAll(template, "${exit_code}", exitCode)
	s = strings.ReplaceAll(s, "${command}", command)
	return s
}

// ExpandMessageWithContext substitutes all template variables including context-aware ones.
// Supported: ${exit_code}, ${command}, ${branch}, ${changed_files}, ${serve_url}
func ExpandMessageWithContext(template, exitCode, command string, ctx *ChainContext) string {
	s := ExpandMessage(template, exitCode, command)
	if ctx != nil {
		// Populate git info lazily if the template references it
		if strings.Contains(s, "${branch}") || strings.Contains(s, "${changed_files}") {
			ctx.PopulateGitInfo()
		}
		s = strings.ReplaceAll(s, "${branch}", ctx.Branch)
		s = strings.ReplaceAll(s, "${changed_files}", formatChangedFiles(ctx.ChangedFiles))
		// Expand ${serve_url} from the current serve state (first running server)
		if strings.Contains(s, "${serve_url}") {
			serveUrl := resolveServeURL(ctx.Session)
			s = strings.ReplaceAll(s, "${serve_url}", serveUrl)
		}
	}
	return s
}

// resolveServeURL returns the URL of the first running dev server, or "(unknown)" if none.
func resolveServeURL(session string) string {
	state := ReadServeState(session)
	if state == nil {
		return "(unknown)"
	}
	running := state.RunningServers()
	if len(running) == 0 {
		return "(unknown)"
	}
	return running[0].URL
}

// formatChangedFiles returns a comma-separated list of files, truncated to 10.
func formatChangedFiles(files []string) string {
	if len(files) == 0 {
		return ""
	}
	max := 10
	if len(files) <= max {
		return strings.Join(files, ", ")
	}
	return strings.Join(files[:max], ", ") + fmt.Sprintf(", ... (%d more)", len(files)-max)
}

// autoCCCache is the cached auto-CC role set.
var autoCCCache map[string]bool

// CheckSendPolicy returns an error message if the send is denied by policy,
// or "" if the send is allowed. Send policies only apply to hook-supporting
// providers (Claude Code) where chains fire automatically. Non-hook providers
// (OpenCode, local LLM) must send chain messages manually, so the policy
// is skipped for them.
func CheckSendPolicy(from, to string) string {
	cfg := Config()
	if cfg.SendPolicy == nil {
		return ""
	}

	// Skip policy enforcement for non-hook providers — they need to
	// manually chain (build→test, test→review) since hooks don't fire.
	provider := ResolveProvider(from)
	if !provider.SupportsHooks() {
		return ""
	}

	policy, ok := cfg.SendPolicy[from]
	if !ok {
		return ""
	}
	for _, denied := range policy.Deny {
		if denied == to {
			return fmt.Sprintf("send policy denies %s → %s (hook-driven chain handles this)", from, to)
		}
	}
	return ""
}

// GetAutoCC returns the set of roles whose messages are auto-CC'd to edit.
func GetAutoCC() map[string]bool {
	if autoCCCache != nil {
		return autoCCCache
	}
	cfg := Config()
	m := make(map[string]bool, len(cfg.AutoCC))
	for _, role := range cfg.AutoCC {
		m[role] = true
	}
	autoCCCache = m
	return m
}

// runWatchActions builds the run chain's OnSuccess allowlist: one watch
// action per command shape, evaluated first-match-wins. globMatch is
// full-string, so every pattern must anchor the invocation to the first
// token (interpreter prefix or script path) — a bare "*.sh *" also matched
// `ls -la x.sh 2>&1`, an incidental read of a script path.
func runWatchActions(patterns ...string) ChainActions {
	actions := make(ChainActions, 0, len(patterns))
	for _, p := range patterns {
		actions = append(actions, ChainAction{
			SendTo:  "watch",
			Action:  "watch",
			Message: "Run succeeded (${command}) — tail logs to verify deployed services are healthy and report findings to edit",
			Type:    "request",
			Conditions: map[string]any{
				"command_match":     p,
				"command_not_match": "muxcode *",
			},
		})
	}
	return actions
}

// DefaultConfig returns compiled-in defaults matching current bash/Go behavior.
func DefaultConfig() *MuxcodeConfig {
	return &MuxcodeConfig{
		SharedTools: map[string][]string{
			"bus": {
				"Bash(muxcode *)",
				"Bash(./bin/muxcode *)",
				"Bash(cd * && muxcode *)",
				"Bash(printf * | muxcode *)",
				"Bash(echo * | muxcode *)",
				"Bash(printf *)",
			},
			"readonly": {"Read", "Glob", "Grep"},
			"common": {
				"Bash(ls*)", "Bash(cat*)", "Bash(which*)",
				"Bash(command -v*)", "Bash(pwd*)", "Bash(wc*)",
				"Bash(head*)", "Bash(tail*)",
				"Bash(file *)", "Bash(stat *)", "Bash(dirname *)", "Bash(basename *)",
				"Bash(realpath *)", "Bash(date *)", "Bash(sort *)", "Bash(uniq *)",
				"Bash(tr *)", "Bash(cut *)", "Bash(diff *)", "Bash(test *)",
				"Bash([ *)", "Bash(true*)", "Bash(env *)", "Bash(xargs *)",
				"Bash(sed *)", "Bash(awk *)", "Bash(grep *)", "Bash(find *)",
				"Bash(tee *)",
				"Bash(tmux capture-pane *)", "Bash(tmux list-panes *)", "Bash(tmux list-windows *)",
				"Bash(tmux display-message *)", "Bash(tmux show-options *)",
			},
		},
		ToolProfiles: map[string]ToolProfile{
			"plan": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Write", "Edit",
					"Bash(git diff*)", "Bash(git log*)", "Bash(git status*)",
					"Bash(git rev-parse*)",
					"Bash(python3*)", "Bash(jq*)",
					"Bash(tree *)",
					// Atlassian: plan OWNS this integration — reads for spec context
					// and writes, because plan is the one role that
					// CheckAtlassianAuthority authorizes (bus/atlassian_authority.go).
					//
					// There is deliberately no write deny here. Plan previously
					// carried one, back when authority sat with edit; leaving it in
					// place would be inert on Claude Code (whose enforcement is the
					// PreToolUse guard) but would emit real bash deny rules on a
					// `muxcode reload plan --cli opencode`, locking plan out of the
					// integration it now owns.
					//
					// The rule that keeps writes user-initiated — relayed from edit,
					// never a side effect of docs work — lives in agents/planner.md.
					// It is a scope discipline, not something a tool profile can
					// express: the profile cannot tell who asked.
					//
					// The enumerated reads below are documentation of intent; the
					// "bus" include group already grants `Bash(muxcode *)`, which
					// matches every atlassian subcommand.
					// CLI only, never the Atlassian MCP.
					"Bash(muxcode atlassian jira read *)",
					"Bash(muxcode atlassian jira comments *)",
					"Bash(muxcode atlassian jira link-types*)",
					"Bash(muxcode atlassian jira transitions *)",
					"Bash(muxcode atlassian jira search *)",
					"Bash(muxcode atlassian confluence read *)",
					"Bash(muxcode atlassian confluence search *)",
				},
			},
			"build": {
				Include:     []string{"bus", "readonly", "common"},
				CdPrefix:    true,
				BashTimeout: 600,
				Tools: []string{
					"Bash(./build.sh*)", "Bash(make*)",
					"Bash(pnpm run build*)", "Bash(pnpm build*)", "Bash(npm run build*)",
					"Bash(npx *)", "Bash(go build*)", "Bash(cargo build*)",
					"Bash(gofmt*)", "Bash(go vet*)",
					"Bash(npx eslint*)", "Bash(npx prettier*)",
					"Bash(ruff*)", "Bash(black*)",
					"Bash(cargo clippy*)",
					"Bash(go mod tidy*)", "Bash(go mod download*)", "Bash(go generate*)", "Bash(golangci-lint*)",
					"Bash(cargo fmt --check*)", "Bash(tsc *)",
					"Bash(pnpm audit*)",
				},
			},
			"test": {
				Include:     []string{"bus", "readonly", "common"},
				CdPrefix:    true,
				BashTimeout: 600,
				Tools: []string{
					"Bash(./test.sh*)", "Bash(./scripts/muxcode-test-wrapper.sh*)",
					"Bash(./scripts/test-and-notify.sh*)",
					"Bash(go test*)", "Bash(go vet*)",
					"Bash(jest*)", "Bash(npx jest*)", "Bash(npx vitest*)",
					"Bash(pnpm test*)", "Bash(pnpm run test*)",
					"Bash(npm test*)", "Bash(npm run test*)",
					"Bash(pytest*)", "Bash(python -m pytest*)", "Bash(cargo test*)",
					"Bash(go tool cover*)", "Bash(go mod *)",
					"Bash(npx c8*)", "Bash(nyc *)", "Bash(coverage*)",
					"Bash(python -m coverage*)", "Bash(tox *)",
				},
			},
			"review": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(git diff*)", "Bash(git log*)", "Bash(git status*)",
					"Bash(git show*)", "Bash(git blame*)", "Bash(git branch*)",
					"Bash(git rev-parse*)", "Bash(git rev-list*)",
					"Bash(git shortlog*)", "Bash(git stash list*)", "Bash(git remote*)",
					"Bash(diff <(*)", "Bash(python3*)", "Bash(jq*)",
				},
			},
			"edit": {
				Include:     []string{"bus", "readonly", "common"},
				CdPrefix:    false,
				BashTimeout: 300,
				Tools: []string{
					"Write", "Edit", // required for OpenCode; no-op for Claude Code
					"Bash(tree *)", "Bash(python3*)", "Bash(jq*)",
					"Bash(tmux capture-pane *)", "Bash(tmux display-message *)",
				},
				DenyTools: []string{
					// Atlassian writes (delegated to the plan agent, which holds the
					// authority — bus/atlassian_authority.go). Edit stays the consent
					// boundary: it talks to the user and relays their request to plan,
					// but it does not perform the write itself.
					//
					// Only consumed by non-hook providers (OpenCode), which emit these
					// as bash deny rules; Claude Code enforcement runs through the
					// PreToolUse guard instead. Present so the rule survives a
					// `muxcode reload edit --cli opencode`.
					//
					// NOT redundant with the Tools allowlist: the "bus" include group
					// grants `Bash(muxcode *)`, which already matches every atlassian
					// subcommand, so without an explicit deny the narrowing does
					// nothing. Reads are deliberately left open.
					//
					// Trailing space matters — "comment *" must not match "comments",
					// and "transition *" must not match "transitions".
					"muxcode atlassian jira update *",
					"muxcode atlassian jira comment *",
					"muxcode atlassian jira link *",
					"muxcode atlassian jira transition *",
					"muxcode atlassian jira create-subtask *",
					"muxcode atlassian jira worklog *",
					"muxcode atlassian jira attach *",
					"muxcode atlassian confluence update *",
					"muxcode atlassian confluence attach *",
					// Git write operations (read-only delegated to commit agent)
					"git commit*", "git push*", "git pull*", "git rebase*",
					"git checkout*", "git branch*", "git merge*", "git stash*",
					"git tag*", "git reset*", "git cherry-pick*", "git revert*",
					"git am*", "git add*", "git rm*", "git mv*", "git restore*",
					// GitHub CLI (all operations delegated to commit agent)
					"gh *",
					// Build commands (delegated to build agent)
					"./build.sh*", "pnpm build*", "pnpm run build*", "make*",
					"go build*", "cargo build*",
					// Test commands (delegated to test agent)
					"pnpm test*", "pnpm run test*", "jest*", "pytest*",
					"go test*", "cargo test*",
					// Deploy commands (delegated to deploy agent)
					"cdk synth*", "cdk diff*", "cdk deploy*",
					// Log tailing (delegated to watch agent)
					"aws logs*", "tail -f*", "kubectl logs*", "docker logs*", "stern*",
					// AWS operations (delegated to run agent)
					"aws lambda*", "aws stepfunctions*", "aws s3*", "aws s3api*",
					"aws glue*", "aws dynamodb*", "aws kinesis*", "aws firehose*",
					"aws events*", "aws sqs*", "aws sns*", "aws ssm*", "aws ecs*",
					"aws secretsmanager*", "aws cloudformation*", "aws appflow*",
					// API requests (delegated to api agent)
					"curl*",
				},
			},
			"commit": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(git *)", "Bash(gh *)",
					"Bash(ssh-keyscan *)", "Bash(ssh-add *)",
					"Bash(curl*)",
				},
			},
			"deploy": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(cdk *)", "Bash(npx cdk *)",
					"Bash(envName=* cdk *)", "Bash(envName=* npx cdk *)",
					"Bash(export envName=* && cdk *)", "Bash(export envName=* && npx cdk *)",
					"Bash(export envName=*)", "Bash(source *)",
					"Bash(terraform *)", "Bash(pulumi *)",
					"Bash(aws *)", "Bash(sam *)",
					"Bash(./build.sh*)", "Bash(make*)",
					"Bash(git diff*)", "Bash(git log*)", "Bash(git status*)",
					"Bash(jq *)", "Bash(yq *)", "Bash(docker *)",
					"Bash(pnpm install*)", "Bash(npm install*)", "Bash(pip install*)",
					"Bash(cfn-lint*)", "Bash(tflint*)", "Bash(checkov*)",
					"Bash(curl*)", "Bash(wget*)",
				},
			},
			"run": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(curl*)", "Bash(wget*)",
					"Bash(aws *)", "Bash(gcloud *)", "Bash(az *)",
					"Bash(docker *)", "Bash(docker-compose *)",
					"Bash(jq*)", "Bash(yq*)",
					"Bash(python*)", "Bash(node*)", "Bash(bash*)",
					"Bash(ssh *)", "Bash(scp *)", "Bash(rsync *)",
					"Bash(nc *)", "Bash(dig *)", "Bash(nslookup *)",
					"Bash(ping *)", "Bash(telnet *)", "Bash(openssl *)",
					"Bash(kubectl *)", "Bash(helm *)",
					"Bash(psql *)", "Bash(mysql *)", "Bash(redis-cli *)",
					"Bash(mongosh *)", "Bash(sqlite3 *)",
					"Bash(gh *)", "Bash(brew *)", "Bash(pip *)",
					"Bash(pnpm *)", "Bash(npm *)", "Bash(npx *)", "Bash(yarn *)",
					"Bash(go run *)", "Bash(cargo run *)", "Bash(make *)",
					"Bash(mkdir *)", "Bash(rm *)", "Bash(chmod *)",
					"Bash(tar *)", "Bash(unzip *)", "Bash(gzip *)", "Bash(base64 *)",
					"Bash(export *)", "Bash(eval *)", "Bash(eval \"$(*))",
					"Bash(source *)",
					"Bash(grep *)", "Bash(head *)", "Bash(tail *)", "Bash(cat *)",
					"Bash(wc *)", "Bash(sort *)", "Bash(cut *)", "Bash(awk *)", "Bash(sed *)",
					"Bash(sleep *)", "Bash(echo *)", "Bash(printf *)", "Bash(date *)",
					"Bash(find *)", "Bash(ls *)", "Bash(diff *)", "Bash(env *)",
					"Bash(touch *)", "Bash(cp *)", "Bash(mv *)",
					"Read(/tmp/muxcode-bus-*)", "Read(/private/tmp/muxcode-bus-*)",
				},
			},
			"analyze": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(git diff*)", "Bash(git log*)", "Bash(git show*)",
					"Bash(git blame*)", "Bash(git status*)",
					"Bash(git rev-parse*)", "Bash(git shortlog*)", "Bash(git stash list*)",
					"Bash(python3*)", "Bash(jq*)",
				},
			},
			"docs": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Write", "Edit",
					"Bash(git diff*)", "Bash(git log*)", "Bash(git show*)",
					"Bash(git status*)", "Bash(git blame*)",
					"Bash(tree *)", "Bash(python3*)",
					"Bash(npx typedoc*)", "Bash(godoc*)", "Bash(pydoc*)",
				},
			},
			"research": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"WebSearch", "WebFetch",
					"Bash(git diff*)", "Bash(git log*)", "Bash(git show*)",
					"Bash(git status*)", "Bash(git blame*)",
					"Bash(python3*)", "Bash(node*)", "Bash(jq*)",
					"Bash(curl *)", "Bash(gh *)", "Bash(tree *)",
					"Bash(go doc *)", "Bash(go list *)",
					"Bash(pip show*)", "Bash(npm info*)", "Bash(pnpm info*)",
				},
			},
			"watch": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(tail *)", "Bash(journalctl *)",
					"Bash(aws logs*)", "Bash(aws cloudwatch*)",
					"Bash(gcloud logging*)", "Bash(az monitor*)",
					"Bash(kubectl logs*)", "Bash(kubectl get events*)",
					"Bash(docker logs*)", "Bash(docker-compose logs*)",
					"Bash(stern *)",
					"Bash(jq*)", "Bash(yq*)",
					"Bash(python3*)", "Bash(node*)",
					"Bash(zcat *)", "Bash(gunzip *)", "Bash(lnav *)",
					// Browser monitoring (Playwright)
					"Bash(npx playwright*)",
					"Bash(curl *)",
					"Read(/tmp/muxcode-bus-*/serve-state.json)",
					"Read(/private/tmp/muxcode-bus-*/serve-state.json)",
				},
			},
			"pr-read": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(gh pr view*)", "Bash(gh pr checks*)", "Bash(gh pr diff*)",
					"Bash(gh pr review*)", "Bash(gh api *)",
					"Bash(gh pr list*)", "Bash(gh pr status*)",
					"Bash(git diff*)", "Bash(git log*)", "Bash(git status*)",
					"Bash(git show*)", "Bash(git blame*)",
					"Bash(git rev-parse*)", "Bash(git branch --list*)",
					"Bash(git branch -a*)", "Bash(git branch -r*)",
					"Bash(jq *)", "Bash(jq*)",
				},
			},
			"api": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(curl*)", "Bash(wget*)", "Bash(http*)",
					"Bash(jq*)", "Bash(python*)", "Bash(node*)",
					"Bash(openssl*)", "Bash(base64*)",
					"Bash(dig*)", "Bash(nslookup*)",
					"Bash(echo *)",
					"Bash(RESP=*)", "Bash(BODY=*)", "Bash(STATUS=*)", "Bash(DURATION=*)",
					"Bash(START=*)", "Bash(END=*)", "Bash(ELAPSED=*)",
					"Bash(RESPONSE=*)", "Bash(RESULT=*)", "Bash(TIME=*)",
					"Bash(HTTP=*)", "Bash(HEADERS=*)", "Bash(CODE=*)",
					"Bash(mkdir *)", "Bash(rm *)",
				},
			},
			"serve": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(./run.sh*)", "Bash(./run-dev.sh*)", "Bash(./dev.sh*)",
					"Bash(bash run.sh*)", "Bash(bash run-dev.sh*)", "Bash(bash dev.sh*)",
					"Bash(curl*)", "Bash(wget*)",
					"Bash(lsof *)", "Bash(ss *)", "Bash(netstat *)",
					"Bash(kill *)", "Bash(pkill *)",
					"Bash(nohup *)",
					"Bash(node*)", "Bash(npx *)", "Bash(pnpm *)", "Bash(npm *)", "Bash(yarn *)",
					"Bash(python*)", "Bash(flask *)", "Bash(uvicorn *)", "Bash(gunicorn *)",
					"Bash(go run *)", "Bash(cargo run *)", "Bash(make *)",
					"Bash(docker *)", "Bash(docker-compose *)",
					"Bash(jq*)", "Bash(yq*)",
					"Bash(tail *)", "Bash(head *)", "Bash(cat *)",
					"Bash(grep *)", "Bash(wc *)", "Bash(ps *)",
					"Bash(sleep *)", "Bash(echo *)", "Bash(printf *)", "Bash(date *)",
					"Bash(find *)", "Bash(ls *)", "Bash(env *)",
					"Bash(mkdir *)", "Bash(rm *)", "Bash(touch *)",
					"Bash(source *)", "Bash(export *)",
					"Read(/tmp/muxcode-bus-*)", "Read(/private/tmp/muxcode-bus-*)",
				},
			},
			"auto": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Write(*)", "Edit(*)",
					"Bash(muxcode atlassian *)",
					"Bash(muxcode mode *)",
					"Bash(gh pr view *)", "Bash(gh pr checks *)",
					"Bash(gh pr list *)", "Bash(gh pr status *)",
					"Bash(git branch *)", "Bash(git checkout *)",
					"Bash(git status)", "Bash(git diff *)",
					"Bash(git log *)", "Bash(git rev-parse *)",
				},
			},
			// Deliberately minimal — no includes, no Write/Edit. Rationale in
			// bus/prompt_agent.go's doc comment; pinned by the profile test.
			"prompt": {
				CdPrefix: true,
				Tools: []string{
					"Bash(muxcode graph *)",
					"Bash(muxcode send *)", "Bash(muxcode inbox*)",
					"Read(.muxcode/graphs/*)",
				},
			},
		},
		EventChains: map[string]EventChain{
			"deploy": {
				OnSuccess: ChainActions{{
					SendTo:  "run",
					Action:  "run",
					Message: "Deployment succeeded (${command}) — run post-deploy verification and report results",
					Type:    "request",
				}},
				OnFailure: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Deployment FAILED (exit ${exit_code}): ${command} — check deploy window",
					Type:    "event",
				}},
				OnUnknown: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Deployment completed (exit code unknown): ${command}",
					Type:    "event",
				}},
				NotifyAnalystOn: []string{"*"},
			},
			"run": {
				// Watch fires only for verification-run shapes. A denylist gate
				// here fired watch on every successful command — including
				// incidental cat/ls reads — storming run→watch until relay
				// suppression capped it. Allowlist per the run-chain-watch-overfire
				// spec. Patterns anchor the script to the FIRST token (interpreter
				// prefix or script path): globMatch's * spans spaces, so a bare
				// "*.sh *" also matched `ls -la x.sh 2>&1` — reads of a .sh path,
				// not executions.
				OnSuccess: runWatchActions(
					"aws *",
					"bash *.sh", "bash *.sh *",
					"sh *.sh", "sh *.sh *",
					"./*.sh", "./*.sh *",
					"/*.sh", "/*.sh *",
					"scripts/*.sh", "scripts/*.sh *",
				),
				OnFailure: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Run FAILED (exit ${exit_code}): ${command} — check run window",
					Type:    "event",
				}},
				OnUnknown: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Run completed (exit code unknown): ${command}",
					Type:    "event",
				}},
				NotifyAnalystOn: []string{"failure", "unknown"},
			},
			"watch": {
				OnSuccess: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Watch completed — logs look healthy after deploy (${command})",
					Type:    "event",
				}},
				OnFailure: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Watch detected errors (exit ${exit_code}): ${command} — check watch window for log details",
					Type:    "event",
				}},
				OnUnknown: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Watch completed (exit code unknown): ${command}",
					Type:    "event",
				}},
				NotifyAnalystOn: []string{"failure"},
			},
			"build": {
				OnSuccess: ChainActions{{
					SendTo:  "test",
					Action:  "test",
					Message: "Build succeeded (${command}) — run tests and report results",
					Type:    "request",
				}},
				OnFailure: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Build FAILED (exit ${exit_code}): ${command} — check build window",
					Type:    "event",
				}},
				OnUnknown: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Build completed (exit code unknown): ${command}",
					Type:    "event",
				}},
				NotifyAnalystOn: []string{"failure", "unknown"},
			},
			"test": {
				OnSuccess: ChainActions{{
					SendTo:  "review",
					Action:  "review",
					Message: "Tests passed (${command}) — review the latest changes on this branch and report findings to edit",
					Type:    "request",
				}},
				OnFailure: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Tests FAILED (exit ${exit_code}): ${command} — check test window",
					Type:    "event",
				}},
				OnUnknown: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Tests completed (exit code unknown): ${command}",
					Type:    "event",
				}},
				NotifyAnalystOn: []string{"failure", "unknown"},
			},
			"review": {
				NotifyPlanOn: []string{"success"},
			},
			"serve": {
				OnSuccess: ChainActions{{
					SendTo:  "watch",
					Action:  "browser-check",
					Message: "Dev server started (${command}) — read serve-state.json to find the URL, then run a Playwright browser check for console errors and warnings",
					Type:    "request",
				}},
				OnFailure: ChainActions{{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Dev server FAILED (exit ${exit_code}): ${command} — check serve window",
					Type:    "event",
				}},
			},
		},
		AutoCC: []string{"build", "test", "review", "deploy", "analyze"},
		SendPolicy: map[string]SendPolicy{
			"build": {Deny: []string{"test"}},
			"test":  {Deny: []string{"review"}},
		},
	}
}

// ValidateConfig checks event chain conditions for unknown types.
// Returns warnings (not errors) for forward compatibility.
func ValidateConfig(cfg *MuxcodeConfig) []string {
	var warnings []string
	for event, chain := range cfg.EventChains {
		for _, actions := range []ChainActions{chain.OnSuccess, chain.OnFailure, chain.OnUnknown} {
			for _, action := range actions {
				for key := range action.Conditions {
					if !IsKnownCondition(key) {
						warnings = append(warnings, fmt.Sprintf("event_chains.%s: unknown condition type %q", event, key))
					}
				}
			}
		}
	}
	return warnings
}
