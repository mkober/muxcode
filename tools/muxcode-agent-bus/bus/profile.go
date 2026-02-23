package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MuxcodeConfig holds tool profiles, event chains, and auto-CC config.
type MuxcodeConfig struct {
	SharedTools  map[string][]string    `json:"shared_tools"`
	ToolProfiles map[string]ToolProfile `json:"tool_profiles"`
	EventChains  map[string]EventChain  `json:"event_chains"`
	AutoCC       []string               `json:"auto_cc"`
}

// ToolProfile defines allowed tools for a role.
type ToolProfile struct {
	Include  []string `json:"include,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	CdPrefix bool     `json:"cd_prefix,omitempty"`
}

// EventChain defines actions triggered by command outcomes.
type EventChain struct {
	OnSuccess     *ChainAction `json:"on_success,omitempty"`
	OnFailure     *ChainAction `json:"on_failure,omitempty"`
	OnUnknown     *ChainAction `json:"on_unknown,omitempty"`
	NotifyAnalyst bool         `json:"notify_analyst"`
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

	return result
}

// ResolveTools expands a role's tool profile into a flat tool list.
func ResolveTools(role string) []string {
	cfg := Config()
	profile, ok := cfg.ToolProfiles[role]
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
func ChainNotifyAnalyst(eventType string) bool {
	cfg := Config()
	chain, ok := cfg.EventChains[eventType]
	if !ok {
		return false
	}
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
			},
			"readonly": {"Read", "Glob", "Grep"},
			"common": {
				"Bash(ls*)", "Bash(cat*)", "Bash(which*)",
				"Bash(command -v*)", "Bash(pwd*)", "Bash(wc*)",
				"Bash(head*)", "Bash(tail*)",
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
				},
			},
			"review": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(git diff*)", "Bash(git log*)", "Bash(git status*)",
					"Bash(git show*)", "Bash(git blame*)", "Bash(git branch*)",
				},
			},
			"git": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: false,
				Tools: []string{
					"Bash",
				},
			},
			"deploy": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(cdk *)", "Bash(npx cdk *)",
					"Bash(terraform *)", "Bash(pulumi *)",
					"Bash(aws *)", "Bash(sam *)",
					"Bash(./build.sh*)", "Bash(make*)",
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
				},
			},
			"analyst": {
				Include:  []string{"bus", "readonly", "common"},
				CdPrefix: true,
				Tools: []string{
					"Bash(git diff*)", "Bash(git log*)", "Bash(git show*)",
					"Bash(git blame*)", "Bash(git status*)",
				},
			},
		},
		EventChains: map[string]EventChain{
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
				NotifyAnalyst: true,
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
				NotifyAnalyst: true,
			},
		},
		AutoCC: []string{"build", "test", "review"},
	}
}
