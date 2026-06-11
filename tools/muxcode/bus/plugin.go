package bus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PluginConfig holds provider-keyed plugin lists.
// Each key is a provider name (e.g. "claude-code") mapping to its plugin config.
type PluginConfig struct {
	Providers map[string]ProviderPlugins
}

// ProviderPlugins holds plugin settings for a single provider.
type ProviderPlugins struct {
	Marketplace string
	Plugins     []string // enabled plugins
	Disabled    []string // commented-out plugins (preserved for round-trip)
}

// DefaultPluginConfigPath returns the user-global plugin config path.
func DefaultPluginConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "muxcode", "plugins.conf")
}

// ResolvePluginConfigPath resolves the plugin config file path.
// Resolution: .muxcode/plugins.conf → ~/.config/muxcode/plugins.conf
func ResolvePluginConfigPath() string {
	if _, err := os.Stat(".muxcode/plugins.conf"); err == nil {
		return ".muxcode/plugins.conf"
	}
	return DefaultPluginConfigPath()
}

// LoadPluginConfig reads the plugin config from the resolved path.
// Returns an empty config (not an error) if the file doesn't exist.
func LoadPluginConfig() (*PluginConfig, error) {
	path := ResolvePluginConfigPath()
	if path == "" {
		return &PluginConfig{Providers: make(map[string]ProviderPlugins)}, nil
	}
	return LoadPluginConfigFromFile(path)
}

// LoadPluginConfigFromFile reads the plugin config from a specific path.
func LoadPluginConfigFromFile(path string) (*PluginConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &PluginConfig{Providers: make(map[string]ProviderPlugins)}, nil
		}
		return nil, fmt.Errorf("read plugin config: %w", err)
	}

	return ParsePluginConfig(string(data))
}

// ParsePluginConfig parses the text-based plugin config format.
//
// Format:
//   - [provider-name] starts a provider section (default: claude-code)
//   - marketplace: <name> sets the marketplace for the current section
//   - Lines starting with # are comments (disabled plugins if they match a plugin name)
//   - Non-comment, non-empty lines are enabled plugin names
//   - Section headers and pure comments (with spaces/special chars) are ignored
func ParsePluginConfig(content string) (*PluginConfig, error) {
	cfg := &PluginConfig{Providers: make(map[string]ProviderPlugins)}

	currentProvider := "claude-code"
	scanner := bufio.NewScanner(strings.NewReader(content))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Section header: [provider-name]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentProvider = line[1 : len(line)-1]
			continue
		}

		// Comment line
		if strings.HasPrefix(line, "#") {
			inner := strings.TrimSpace(line[1:])

			// Marketplace directive: # marketplace: <name>
			if strings.HasPrefix(inner, "marketplace:") {
				val := strings.TrimSpace(strings.TrimPrefix(inner, "marketplace:"))
				if val != "" {
					pp := cfg.Providers[currentProvider]
					pp.Marketplace = val
					cfg.Providers[currentProvider] = pp
				}
				continue
			}

			// Decorative comments (section headers, descriptions) — skip.
			// A commented-out plugin is a simple identifier: lowercase, digits, hyphens.
			if isPluginName(inner) {
				pp := cfg.Providers[currentProvider]
				pp.Disabled = append(pp.Disabled, inner)
				cfg.Providers[currentProvider] = pp
			}
			continue
		}

		// Marketplace directive (uncommented): marketplace: <name>
		if strings.HasPrefix(line, "marketplace:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "marketplace:"))
			if val != "" {
				pp := cfg.Providers[currentProvider]
				pp.Marketplace = val
				cfg.Providers[currentProvider] = pp
			}
			continue
		}

		// Enabled plugin
		if isPluginName(line) {
			pp := cfg.Providers[currentProvider]
			pp.Plugins = append(pp.Plugins, line)
			cfg.Providers[currentProvider] = pp
		}
	}

	return cfg, nil
}

// isPluginName returns true if s looks like a plugin identifier
// (lowercase letters, digits, hyphens, dots).
func isPluginName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// SavePluginConfig writes the plugin config to the user-global path.
// Writes the text-based format preserving disabled plugins as comments.
func SavePluginConfig(cfg *PluginConfig) error {
	path := DefaultPluginConfigPath()
	if path == "" {
		return fmt.Errorf("cannot determine config path")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	content := FormatPluginConfigFile(cfg)
	return os.WriteFile(path, []byte(content), 0644)
}

// FormatPluginConfigFile renders the config as the text-based file format.
func FormatPluginConfigFile(cfg *PluginConfig) string {
	var b strings.Builder
	b.WriteString("# MuxCode Plugin Configuration\n")
	b.WriteString("# Uncomment plugins to enable. Synced to provider settings on session launch.\n")
	b.WriteString("# Manage with: muxcode plugin list|add|remove|sync\n\n")

	providers := make([]string, 0, len(cfg.Providers))
	for p := range cfg.Providers {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	for _, provider := range providers {
		pp := cfg.Providers[provider]
		b.WriteString(fmt.Sprintf("[%s]\n", provider))
		if pp.Marketplace != "" {
			b.WriteString(fmt.Sprintf("# marketplace: %s\n", pp.Marketplace))
		}
		b.WriteString("\n")

		// Enabled plugins
		for _, p := range pp.Plugins {
			b.WriteString(p + "\n")
		}

		// Disabled plugins as comments
		if len(pp.Disabled) > 0 && len(pp.Plugins) > 0 {
			b.WriteString("\n")
		}
		for _, p := range pp.Disabled {
			b.WriteString("# " + p + "\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// AddPlugin adds a plugin to a provider's enabled list (no duplicates).
// If the plugin is in the disabled list, it is moved to enabled.
// Returns true if the plugin was added (not already enabled).
func AddPlugin(cfg *PluginConfig, provider, plugin string) bool {
	pp := cfg.Providers[provider]

	// Check if already enabled
	for _, p := range pp.Plugins {
		if p == plugin {
			return false
		}
	}

	// Remove from disabled if present
	for i, p := range pp.Disabled {
		if p == plugin {
			pp.Disabled = append(pp.Disabled[:i], pp.Disabled[i+1:]...)
			break
		}
	}

	pp.Plugins = append(pp.Plugins, plugin)
	sort.Strings(pp.Plugins)
	if pp.Marketplace == "" {
		pp.Marketplace = defaultMarketplace(provider)
	}
	cfg.Providers[provider] = pp
	return true
}

// RemovePlugin removes a plugin from a provider's enabled list.
// The plugin is moved to the disabled list.
// Returns true if the plugin was found and removed.
func RemovePlugin(cfg *PluginConfig, provider, plugin string) bool {
	pp, ok := cfg.Providers[provider]
	if !ok {
		return false
	}
	for i, p := range pp.Plugins {
		if p == plugin {
			pp.Plugins = append(pp.Plugins[:i], pp.Plugins[i+1:]...)
			// Add to disabled list if not already there
			found := false
			for _, d := range pp.Disabled {
				if d == plugin {
					found = true
					break
				}
			}
			if !found {
				pp.Disabled = append(pp.Disabled, plugin)
				sort.Strings(pp.Disabled)
			}
			cfg.Providers[provider] = pp
			return true
		}
	}
	return false
}

// defaultMarketplace returns the default marketplace for a provider.
func defaultMarketplace(provider string) string {
	switch provider {
	case "claude-code":
		return "claude-plugins-official"
	default:
		return ""
	}
}

// SyncPluginsResult holds the outcome of a plugin sync operation.
type SyncPluginsResult struct {
	Provider string
	Added    []string // plugins that were newly enabled
	Kept     []string // plugins that were already enabled
	Total    int
}

// SyncClaudeCodePlugins syncs the claude-code plugin list to ~/.claude/settings.json.
// Reads the existing settings, merges the configured plugins into enabledPlugins,
// and writes the file back. Does not remove plugins that are in settings but not
// in the muxcode config (additive only).
func SyncClaudeCodePlugins(cfg *PluginConfig) (*SyncPluginsResult, error) {
	pp, ok := cfg.Providers["claude-code"]
	if !ok || len(pp.Plugins) == 0 {
		return &SyncPluginsResult{Provider: "claude-code"}, nil
	}

	marketplace := pp.Marketplace
	if marketplace == "" {
		marketplace = "claude-plugins-official"
	}

	// Read existing ~/.claude/settings.json
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil, fmt.Errorf("cannot determine home directory")
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	var settings map[string]any
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			settings = make(map[string]any)
		} else {
			return nil, fmt.Errorf("read %s: %w", settingsPath, err)
		}
	} else {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("parse %s: %w", settingsPath, err)
		}
	}

	// Get or create enabledPlugins map
	enabledRaw, ok := settings["enabledPlugins"]
	var enabled map[string]any
	if ok {
		enabled, _ = enabledRaw.(map[string]any)
	}
	if enabled == nil {
		enabled = make(map[string]any)
	}

	result := &SyncPluginsResult{
		Provider: "claude-code",
		Total:    len(pp.Plugins),
	}

	// Merge configured plugins (additive only)
	for _, plugin := range pp.Plugins {
		key := plugin + "@" + marketplace
		if _, exists := enabled[key]; exists {
			result.Kept = append(result.Kept, plugin)
		} else {
			enabled[key] = true
			result.Added = append(result.Added, plugin)
		}
	}

	settings["enabledPlugins"] = enabled

	// Write back
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return nil, fmt.Errorf("create settings dir: %w", err)
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal settings: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", settingsPath, err)
	}

	return result, nil
}

// SyncAllPlugins syncs plugins for all configured providers.
// Currently only supports claude-code.
func SyncAllPlugins(cfg *PluginConfig) ([]*SyncPluginsResult, error) {
	var results []*SyncPluginsResult

	if _, ok := cfg.Providers["claude-code"]; ok {
		r, err := SyncClaudeCodePlugins(cfg)
		if err != nil {
			return results, fmt.Errorf("claude-code sync: %w", err)
		}
		results = append(results, r)
	}

	return results, nil
}

// FormatPluginList formats the plugin config for display.
func FormatPluginList(cfg *PluginConfig) string {
	if len(cfg.Providers) == 0 {
		return "No plugins configured.\nRun: muxcode plugin add <name> to add plugins.\n"
	}

	var b strings.Builder
	providers := make([]string, 0, len(cfg.Providers))
	for p := range cfg.Providers {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	for _, provider := range providers {
		pp := cfg.Providers[provider]
		marketplace := pp.Marketplace
		if marketplace == "" {
			marketplace = defaultMarketplace(provider)
		}

		b.WriteString(fmt.Sprintf("Provider: %s", provider))
		if marketplace != "" {
			b.WriteString(fmt.Sprintf("  (marketplace: %s)", marketplace))
		}
		b.WriteString("\n")

		if len(pp.Plugins) == 0 {
			b.WriteString("  (none enabled)\n")
		} else {
			for _, p := range pp.Plugins {
				b.WriteString(fmt.Sprintf("  ✓ %s\n", p))
			}
		}
		if len(pp.Disabled) > 0 {
			b.WriteString(fmt.Sprintf("  (%d disabled)\n", len(pp.Disabled)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FormatSyncResult formats a sync result for display.
func FormatSyncResult(r *SyncPluginsResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Synced %d plugin(s) to %s\n", r.Total, r.Provider))
	if len(r.Added) > 0 {
		b.WriteString(fmt.Sprintf("  Added: %s\n", strings.Join(r.Added, ", ")))
	}
	if len(r.Kept) > 0 {
		b.WriteString(fmt.Sprintf("  Already enabled: %s\n", strings.Join(r.Kept, ", ")))
	}
	if len(r.Added) == 0 && len(r.Kept) == 0 {
		b.WriteString("  No plugins configured\n")
	}
	return b.String()
}
