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
	SharedTools  map[string][]string     `json:"shared_tools"`
	ToolProfiles map[string]ToolProfile  `json:"tool_profiles"`
	EventChains  map[string]EventChain   `json:"event_chains"`
	AutoCC       []string                `json:"auto_cc"`
	SendPolicy   map[string]SendPolicy   `json:"send_policy,omitempty"`
}

// SendPolicy defines send restrictions for a role.
type SendPolicy struct {
	Deny []string `json:"deny"`
}

// ToolProfile defines allowed tools for a role.
type ToolProfile struct {
	Include  []string `json:"include,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	CdPrefix bool     `json:"cd_prefix,omitempty"`
}

// EventChain defines actions triggered by command outcomes.
type EventChain struct {
	OnSuccess       *ChainAction `json:"on_success,omitempty"`
	OnFailure       *ChainAction `json:"on_failure,omitempty"`
	OnUnknown       *ChainAction `json:"on_unknown,omitempty"`
	NotifyAnalyst   bool         `json:"notify_analyst"`
	NotifyAnalystOn []string     `json:"notify_analyst_on,omitempty"`
}

// ChainAction is a single action in an event chain.
type ChainAction struct {
	SendTo  string `json:"send_to"`
	Action  string `json:"action"`
	Message string `json:"message"`
	Type    string `json:"type"`
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
	return mergeConfigs(DefaultConfig(), loaded), nil
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
	// Override tool profiles (entire profile replaced per role)
	for k, v := range override.ToolProfiles {
		result.ToolProfiles[k] = v
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

// resolveRoleAlias maps window-name roles to their canonical tool profile names.
// Window names (commit, analyze, run) differ from profile keys (git, analyst, runner).
func resolveRoleAlias(role string) string {
	switch role {
	case "commit":
		return "git"
	case "analyze":
		return "analyst"
	case "run":
		return "runner"
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
func ResolveChain(eventType, outcome string) *ChainAction {
	cfg := Config()
	chain, ok := cfg.EventChains[eventType]
	if !ok {
		return nil
	}
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

// ExpandMessage substitutes template variables in a chain message.
// Supported: ${exit_code}, ${command}
func ExpandMessage(template, exitCode, command string) string {
	s := strings.ReplaceAll(template, "${exit_code}", exitCode)
	s = strings.ReplaceAll(s, "${command}", command)
	return s
}

// autoCCCache is the cached auto-CC role set.
var autoCCCache map[string]bool

// CheckSendPolicy returns an error message if the send is denied by policy,
// or "" if the send is allowed.
func CheckSendPolicy(from, to string) string {
	cfg := Config()
	if cfg.SendPolicy == nil {
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

// DefaultConfig returns compiled-in defaults matching current bash/Go behavior.
func DefaultConfig() *MuxcodeConfig {
	return &MuxcodeConfig{
		SharedTools: map[string][]string{
			"bus": {
				"Bash(muxcode-agent-bus *)",
				"Bash(./bin/muxcode-agent-bus *)",
				"Bash(cd * && muxcode-agent-bus *)",
				"Bash(printf * | muxcode-agent-bus *)",
				"Bash(echo * | muxcode-agent-bus *)",
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
			},
		},
		ToolProfiles: map[string]ToolProfile{
			"build": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(./build.sh*)", "Bash(make*)",
					"Bash(pnpm run build*)", "Bash(pnpm build*)", "Bash(npm run build*)",
					"Bash(npx *)", "Bash(go build*)", "Bash(cargo build*)",
					"Bash(gofmt*)", "Bash(go vet*)",
					"Bash(npx eslint*)", "Bash(npx prettier*)",
					"Bash(ruff*)", "Bash(black*)",
					"Bash(cargo clippy*)",
					"Bash(go mod *)", "Bash(go generate*)", "Bash(golangci-lint*)",
					"Bash(cargo fmt*)", "Bash(tsc *)",
					"Bash(pnpm install*)", "Bash(pnpm add*)", "Bash(pnpm audit*)",
					"Bash(npm install*)", "Bash(pip install*)", "Bash(pip *)",
					"Bash(mkdir *)", "Bash(rm *)", "Bash(cp *)", "Bash(chmod *)",
					"Bash(tar *)", "Bash(zip *)",
				},
			},
			"test": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
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
					"Write",
					"Bash(git diff*)", "Bash(git log*)", "Bash(git status*)",
					"Bash(git show*)", "Bash(git blame*)", "Bash(git branch*)",
					"Bash(git rev-parse*)", "Bash(git rev-list*)",
					"Bash(git shortlog*)", "Bash(git stash list*)", "Bash(git remote*)",
					"Bash(diff <(*)", "Bash(python3*)", "Bash(jq*)",
				},
			},
			"edit": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: false,
				Tools: []string{
					"Write", "Edit",
					"Bash(tree *)", "Bash(python3*)", "Bash(jq*)",
				},
			},
			"git": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Write", "Edit",
					"Bash(git *)", "Bash(gh *)",
					"Bash(ssh-keyscan *)", "Bash(ssh-add *)",
				},
			},
			"deploy": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(cdk *)", "Bash(npx cdk *)",
					"Bash(envName=* cdk *)", "Bash(envName=* npx cdk *)",
					"Bash(terraform *)", "Bash(pulumi *)",
					"Bash(aws *)", "Bash(sam *)",
					"Bash(./build.sh*)", "Bash(make*)",
					"Bash(git diff*)", "Bash(git log*)", "Bash(git status*)",
					"Bash(jq *)", "Bash(yq *)", "Bash(docker *)",
					"Bash(pnpm install*)", "Bash(npm install*)", "Bash(pip install*)",
					"Bash(cfn-lint*)", "Bash(tflint*)", "Bash(checkov*)",
					"Bash(curl*)", "Bash(wget*)",
					"Write", "Edit",
				},
			},
			"runner": {
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
					"Bash(go run *)", "Bash(cargo run *)", "Bash(make *)",
					"Bash(mkdir *)", "Bash(rm *)", "Bash(chmod *)",
					"Bash(tar *)", "Bash(unzip *)", "Bash(gzip *)", "Bash(base64 *)",
					"Bash(export *)",
				},
			},
			"analyst": {
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
					"Bash(curl *)", "Bash(ssh *)",
					"Bash(zcat *)", "Bash(gunzip *)", "Bash(lnav *)",
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
		},
		EventChains: map[string]EventChain{
			"deploy": {
				OnSuccess: &ChainAction{
					SendTo:  "deploy",
					Action:  "verify",
					Message: "Deployment succeeded (${command}) — verify deployed resources and report results to edit",
					Type:    "request",
				},
				OnFailure: &ChainAction{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Deployment FAILED (exit ${exit_code}): ${command} — check deploy window",
					Type:    "event",
				},
				OnUnknown: &ChainAction{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Deployment completed (exit code unknown): ${command}",
					Type:    "event",
				},
				NotifyAnalystOn: []string{"*"},
			},
			"build": {
				OnSuccess: &ChainAction{
					SendTo:  "test",
					Action:  "test",
					Message: "Build succeeded — run tests and report results",
					Type:    "request",
				},
				OnFailure: &ChainAction{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Build FAILED (exit ${exit_code}): ${command} — check build window",
					Type:    "event",
				},
				OnUnknown: &ChainAction{
					SendTo:  "analyze",
					Action:  "notify",
					Message: "Build completed (exit code unknown): ${command}",
					Type:    "event",
				},
				NotifyAnalystOn: []string{"failure", "unknown"},
			},
			"test": {
				OnSuccess: &ChainAction{
					SendTo:  "review",
					Action:  "review",
					Message: "Tests passed — review the changes and report results to edit",
					Type:    "request",
				},
				OnFailure: &ChainAction{
					SendTo:  "edit",
					Action:  "notify",
					Message: "Tests FAILED (exit ${exit_code}): ${command} — check test window",
					Type:    "event",
				},
				OnUnknown: &ChainAction{
					SendTo:  "analyze",
					Action:  "notify",
					Message: "Tests completed (exit code unknown): ${command}",
					Type:    "event",
				},
				NotifyAnalystOn: []string{"failure", "unknown"},
			},
		},
		AutoCC: []string{"build", "test", "review", "deploy", "analyze"},
		SendPolicy: map[string]SendPolicy{
			"build": {Deny: []string{"test"}},
			"test":  {Deny: []string{"review"}},
		},
	}
}
