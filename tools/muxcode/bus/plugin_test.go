package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePluginConfig_Basic(t *testing.T) {
	input := `
[claude-code]
# marketplace: claude-plugins-official

github
slack

# atlassian
# sentry
`
	cfg, err := ParsePluginConfig(input)
	if err != nil {
		t.Fatalf("ParsePluginConfig() error: %v", err)
	}
	pp := cfg.Providers["claude-code"]
	if pp.Marketplace != "claude-plugins-official" {
		t.Errorf("Marketplace = %q, want %q", pp.Marketplace, "claude-plugins-official")
	}
	if len(pp.Plugins) != 2 {
		t.Errorf("Plugins = %v, want 2 entries", pp.Plugins)
	}
	if len(pp.Disabled) != 2 {
		t.Errorf("Disabled = %v, want 2 entries", pp.Disabled)
	}
}

func TestParsePluginConfig_SkipsDecorative(t *testing.T) {
	input := `
[claude-code]
# ── Source Control ──────────────────
# This is a description comment
# github
`
	cfg, err := ParsePluginConfig(input)
	if err != nil {
		t.Fatalf("ParsePluginConfig() error: %v", err)
	}
	pp := cfg.Providers["claude-code"]
	// Decorative comments (with special chars, spaces) should be skipped
	// Only "github" should be in disabled list
	if len(pp.Disabled) != 1 {
		t.Errorf("Disabled = %v, want [github]", pp.Disabled)
	}
	if pp.Disabled[0] != "github" {
		t.Errorf("Disabled[0] = %q, want %q", pp.Disabled[0], "github")
	}
}

func TestParsePluginConfig_DefaultProvider(t *testing.T) {
	input := `
github
slack
`
	cfg, err := ParsePluginConfig(input)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	pp := cfg.Providers["claude-code"]
	if len(pp.Plugins) != 2 {
		t.Errorf("default provider should be claude-code, got %d plugins", len(pp.Plugins))
	}
}

func TestParsePluginConfig_MultipleProviders(t *testing.T) {
	input := `
[claude-code]
github

[other-provider]
marketplace: custom-marketplace
some-plugin
`
	cfg, err := ParsePluginConfig(input)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(cfg.Providers))
	}
	if cfg.Providers["other-provider"].Marketplace != "custom-marketplace" {
		t.Errorf("marketplace = %q, want %q", cfg.Providers["other-provider"].Marketplace, "custom-marketplace")
	}
}

func TestIsPluginName(t *testing.T) {
	valid := []string{"github", "aws-core", "42crunch-api-security-testing", "wordpress.com"}
	for _, name := range valid {
		if !isPluginName(name) {
			t.Errorf("isPluginName(%q) = false, want true", name)
		}
	}
	invalid := []string{
		"", "── Source Control", "This is a comment", "GitHub",
		"marketplace: foo", "Enabled plugins",
	}
	for _, name := range invalid {
		if isPluginName(name) {
			t.Errorf("isPluginName(%q) = true, want false", name)
		}
	}
}

func TestAddPlugin(t *testing.T) {
	cfg := &PluginConfig{Providers: make(map[string]ProviderPlugins)}

	if !AddPlugin(cfg, "claude-code", "atlassian") {
		t.Error("AddPlugin should return true for new plugin")
	}
	if len(cfg.Providers["claude-code"].Plugins) != 1 {
		t.Errorf("expected 1 plugin, got %d", len(cfg.Providers["claude-code"].Plugins))
	}

	// Duplicate
	if AddPlugin(cfg, "claude-code", "atlassian") {
		t.Error("AddPlugin should return false for duplicate")
	}

	// Second plugin
	if !AddPlugin(cfg, "claude-code", "github") {
		t.Error("AddPlugin should return true for new plugin")
	}

	// Verify sorted
	if cfg.Providers["claude-code"].Plugins[0] != "atlassian" {
		t.Errorf("plugins should be sorted, got %v", cfg.Providers["claude-code"].Plugins)
	}

	// Default marketplace
	if cfg.Providers["claude-code"].Marketplace != "claude-plugins-official" {
		t.Errorf("marketplace = %q, want %q", cfg.Providers["claude-code"].Marketplace, "claude-plugins-official")
	}
}

func TestAddPlugin_MovesFromDisabled(t *testing.T) {
	cfg := &PluginConfig{Providers: map[string]ProviderPlugins{
		"claude-code": {
			Marketplace: "claude-plugins-official",
			Disabled:    []string{"github", "slack"},
		},
	}}

	if !AddPlugin(cfg, "claude-code", "github") {
		t.Error("AddPlugin should return true")
	}
	pp := cfg.Providers["claude-code"]
	if len(pp.Plugins) != 1 || pp.Plugins[0] != "github" {
		t.Errorf("Plugins = %v, want [github]", pp.Plugins)
	}
	if len(pp.Disabled) != 1 || pp.Disabled[0] != "slack" {
		t.Errorf("Disabled = %v, want [slack]", pp.Disabled)
	}
}

func TestRemovePlugin(t *testing.T) {
	cfg := &PluginConfig{Providers: map[string]ProviderPlugins{
		"claude-code": {
			Marketplace: "claude-plugins-official",
			Plugins:     []string{"atlassian", "github", "slack"},
		},
	}}

	if !RemovePlugin(cfg, "claude-code", "github") {
		t.Error("RemovePlugin should return true for existing plugin")
	}
	pp := cfg.Providers["claude-code"]
	if len(pp.Plugins) != 2 {
		t.Errorf("expected 2 plugins after removal, got %d", len(pp.Plugins))
	}
	// Should be moved to disabled
	if len(pp.Disabled) != 1 || pp.Disabled[0] != "github" {
		t.Errorf("Disabled = %v, want [github]", pp.Disabled)
	}

	// Non-existent plugin
	if RemovePlugin(cfg, "claude-code", "nonexistent") {
		t.Error("RemovePlugin should return false for non-existent plugin")
	}

	// Non-existent provider
	if RemovePlugin(cfg, "other-provider", "atlassian") {
		t.Error("RemovePlugin should return false for non-existent provider")
	}
}

func TestSaveAndLoadPluginConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "plugins.conf")

	cfg := &PluginConfig{Providers: map[string]ProviderPlugins{
		"claude-code": {
			Marketplace: "claude-plugins-official",
			Plugins:     []string{"atlassian", "github"},
			Disabled:    []string{"slack", "sentry"},
		},
	}}

	// Write config
	content := FormatPluginConfigFile(cfg)
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read it back
	loaded, err := LoadPluginConfigFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadPluginConfigFromFile: %v", err)
	}

	pp := loaded.Providers["claude-code"]
	if len(pp.Plugins) != 2 {
		t.Errorf("expected 2 enabled plugins, got %d", len(pp.Plugins))
	}
	if len(pp.Disabled) != 2 {
		t.Errorf("expected 2 disabled plugins, got %d", len(pp.Disabled))
	}
}

func TestSyncClaudeCodePlugins(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	claudeDir := filepath.Join(tmpDir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	// Write existing settings with one plugin already enabled
	existing := map[string]any{
		"enabledPlugins": map[string]any{
			"atlassian@claude-plugins-official": true,
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)

	cfg := &PluginConfig{Providers: map[string]ProviderPlugins{
		"claude-code": {
			Marketplace: "claude-plugins-official",
			Plugins:     []string{"atlassian", "github", "slack"},
		},
	}}

	result, err := SyncClaudeCodePlugins(cfg)
	if err != nil {
		t.Fatalf("SyncClaudeCodePlugins() error: %v", err)
	}

	if result.Total != 3 {
		t.Errorf("Total = %d, want 3", result.Total)
	}
	if len(result.Added) != 2 {
		t.Errorf("Added = %v, want 2 entries", result.Added)
	}
	if len(result.Kept) != 1 {
		t.Errorf("Kept = %v, want 1 entry", result.Kept)
	}

	// Verify settings file
	readData, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]any
	json.Unmarshal(readData, &settings)

	plugins := settings["enabledPlugins"].(map[string]any)
	if len(plugins) != 3 {
		t.Errorf("enabledPlugins has %d entries, want 3", len(plugins))
	}
}

func TestSyncClaudeCodePlugins_NoSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg := &PluginConfig{Providers: map[string]ProviderPlugins{
		"claude-code": {
			Marketplace: "claude-plugins-official",
			Plugins:     []string{"sentry"},
		},
	}}

	result, err := SyncClaudeCodePlugins(cfg)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if result.Total != 1 || len(result.Added) != 1 {
		t.Errorf("Total=%d Added=%v, want 1/[sentry]", result.Total, result.Added)
	}

	settingsPath := filepath.Join(tmpDir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Error("settings.json should have been created")
	}
}

func TestSyncClaudeCodePlugins_PreservesExistingSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	claudeDir := filepath.Join(tmpDir, ".claude")
	os.MkdirAll(claudeDir, 0755)

	existing := map[string]any{
		"env": map[string]any{"EDITOR": "nvim"},
		"enabledPlugins": map[string]any{
			"existing@other-marketplace": true,
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0644)

	cfg := &PluginConfig{Providers: map[string]ProviderPlugins{
		"claude-code": {
			Marketplace: "claude-plugins-official",
			Plugins:     []string{"github"},
		},
	}}

	if _, err := SyncClaudeCodePlugins(cfg); err != nil {
		t.Fatalf("error: %v", err)
	}

	readData, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]any
	json.Unmarshal(readData, &settings)

	env, _ := settings["env"].(map[string]any)
	if env["EDITOR"] != "nvim" {
		t.Error("existing env should be preserved")
	}

	plugins := settings["enabledPlugins"].(map[string]any)
	if _, ok := plugins["existing@other-marketplace"]; !ok {
		t.Error("existing plugin should be preserved")
	}
	if _, ok := plugins["github@claude-plugins-official"]; !ok {
		t.Error("new plugin should be added")
	}
}

func TestFormatPluginList(t *testing.T) {
	cfg := &PluginConfig{Providers: map[string]ProviderPlugins{
		"claude-code": {
			Marketplace: "claude-plugins-official",
			Plugins:     []string{"atlassian", "github"},
			Disabled:    []string{"slack"},
		},
	}}

	output := FormatPluginList(cfg)
	if !strings.Contains(output, "claude-code") {
		t.Error("output should contain provider name")
	}
	if !strings.Contains(output, "atlassian") {
		t.Error("output should contain enabled plugin names")
	}
	if !strings.Contains(output, "1 disabled") {
		t.Error("output should show disabled count")
	}
}

func TestFormatPluginList_Empty(t *testing.T) {
	cfg := &PluginConfig{Providers: make(map[string]ProviderPlugins)}
	output := FormatPluginList(cfg)
	if !strings.Contains(output, "No plugins configured") {
		t.Errorf("empty config should show 'No plugins configured', got: %s", output)
	}
}

func TestParsePluginConfig_FullDefaultConfig(t *testing.T) {
	// Verify the actual default config file parses correctly
	data, err := os.ReadFile("../../../config/plugins.conf")
	if err != nil {
		t.Skipf("default config not found at expected path: %v", err)
	}

	cfg, err := ParsePluginConfig(string(data))
	if err != nil {
		t.Fatalf("ParsePluginConfig() error: %v", err)
	}

	pp := cfg.Providers["claude-code"]
	// Default config has all plugins commented out
	if len(pp.Plugins) != 0 {
		t.Errorf("default config should have 0 enabled plugins, got %d: %v", len(pp.Plugins), pp.Plugins)
	}
	// Should have many disabled plugins
	if len(pp.Disabled) < 100 {
		t.Errorf("default config should have 100+ disabled plugins, got %d", len(pp.Disabled))
	}
}
