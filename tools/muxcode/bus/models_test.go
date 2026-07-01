package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModelConfig_Basic(t *testing.T) {
	content := `
[claude]
# default: claude-sonnet-5
claude-opus-4-8
claude-sonnet-5
claude-haiku-4-5

[opencode]
# default: opencode-go/minimax-m2.5
opencode-go/minimax-m2.5
opencode-go/deepseek-v4-pro
`
	cfg, err := ParseModelConfig(content)
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}

	// Claude provider
	claude := cfg.Providers["claude"]
	if claude.Default != "claude-sonnet-5" {
		t.Errorf("claude default = %q, want %q", claude.Default, "claude-sonnet-5")
	}
	if len(claude.Models) != 3 {
		t.Errorf("claude models count = %d, want 3", len(claude.Models))
	}
	if claude.Models[0] != "claude-opus-4-8" {
		t.Errorf("claude models[0] = %q, want %q", claude.Models[0], "claude-opus-4-8")
	}

	// OpenCode provider
	oc := cfg.Providers["opencode"]
	if oc.Default != "opencode-go/minimax-m2.5" {
		t.Errorf("opencode default = %q, want %q", oc.Default, "opencode-go/minimax-m2.5")
	}
	if len(oc.Models) != 2 {
		t.Errorf("opencode models count = %d, want 2", len(oc.Models))
	}
}

func TestParseModelConfig_DisabledModels(t *testing.T) {
	content := `
[claude]
# default: claude-sonnet-5
claude-sonnet-5
# claude-opus-4-8
# claude-haiku-4-5
`
	cfg, err := ParseModelConfig(content)
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}

	claude := cfg.Providers["claude"]
	if len(claude.Models) != 1 {
		t.Errorf("enabled models = %d, want 1", len(claude.Models))
	}
	if len(claude.Disabled) != 2 {
		t.Errorf("disabled models = %d, want 2", len(claude.Disabled))
	}
	if claude.Disabled[0] != "claude-opus-4-8" {
		t.Errorf("disabled[0] = %q, want %q", claude.Disabled[0], "claude-opus-4-8")
	}
}

func TestParseModelConfig_EmptyAndComments(t *testing.T) {
	content := `
# MuxCode Model Configuration
# This is a header comment that should be ignored

[claude]
# default: claude-sonnet-5

# ── Section header comment ──
claude-sonnet-5
`
	cfg, err := ParseModelConfig(content)
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}

	claude := cfg.Providers["claude"]
	if len(claude.Models) != 1 {
		t.Errorf("models = %d, want 1", len(claude.Models))
	}
	// Decorative comments with spaces/special chars should not be treated as disabled models
	if len(claude.Disabled) != 0 {
		t.Errorf("disabled = %d, want 0 (decorative comments should be ignored)", len(claude.Disabled))
	}
}

func TestParseModelConfig_NoSection(t *testing.T) {
	content := `
# Lines before any section header are ignored
claude-sonnet-5
`
	cfg, err := ParseModelConfig(content)
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}

	if len(cfg.Providers) != 0 {
		t.Errorf("providers = %d, want 0 (lines before section header ignored)", len(cfg.Providers))
	}
}

func TestParseModelConfig_Empty(t *testing.T) {
	cfg, err := ParseModelConfig("")
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("providers = %d, want 0", len(cfg.Providers))
	}
}

func TestIsModelName(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"claude-sonnet-5", true},
		{"claude-opus-4-8", true},
		{"opencode-go/deepseek-v4-pro", true},
		{"gpt-5.5", true},
		{"gpt-5.4-mini", true},
		{"claude_test_model", true},
		{"", false},
		{"has spaces", false},
		{"── Section ──", false},
		{"Model Configuration", false},
		{"default: claude-sonnet", false}, // contains colon + space
	}

	for _, tt := range tests {
		got := isModelName(tt.input)
		if got != tt.want {
			t.Errorf("isModelName(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestAddModel(t *testing.T) {
	cfg := &ModelConfig{Providers: map[string]ProviderModels{
		"claude": {
			Default: "claude-sonnet-5",
			Models:  []string{"claude-sonnet-5"},
		},
	}}

	// Add new model
	if !AddModel(cfg, "claude", "claude-opus-4-8") {
		t.Error("AddModel returned false for new model")
	}
	if len(cfg.Providers["claude"].Models) != 2 {
		t.Errorf("models count = %d, want 2", len(cfg.Providers["claude"].Models))
	}

	// Add duplicate — should return false
	if AddModel(cfg, "claude", "claude-opus-4-8") {
		t.Error("AddModel returned true for duplicate")
	}
}

func TestAddModel_FromDisabled(t *testing.T) {
	cfg := &ModelConfig{Providers: map[string]ProviderModels{
		"claude": {
			Models:   []string{"claude-sonnet-5"},
			Disabled: []string{"claude-opus-4-8"},
		},
	}}

	if !AddModel(cfg, "claude", "claude-opus-4-8") {
		t.Error("AddModel returned false for disabled model")
	}

	claude := cfg.Providers["claude"]
	if len(claude.Models) != 2 {
		t.Errorf("models count = %d, want 2", len(claude.Models))
	}
	if len(claude.Disabled) != 0 {
		t.Errorf("disabled count = %d, want 0", len(claude.Disabled))
	}
}

func TestAddModel_NewProvider(t *testing.T) {
	cfg := &ModelConfig{Providers: make(map[string]ProviderModels)}

	if !AddModel(cfg, "claude", "claude-sonnet-5") {
		t.Error("AddModel returned false for new provider")
	}

	claude := cfg.Providers["claude"]
	if len(claude.Models) != 1 {
		t.Errorf("models count = %d, want 1", len(claude.Models))
	}
}

func TestRemoveModel(t *testing.T) {
	cfg := &ModelConfig{Providers: map[string]ProviderModels{
		"claude": {
			Default: "claude-sonnet-5",
			Models:  []string{"claude-opus-4-8", "claude-sonnet-5"},
		},
	}}

	if !RemoveModel(cfg, "claude", "claude-opus-4-8") {
		t.Error("RemoveModel returned false for existing model")
	}

	claude := cfg.Providers["claude"]
	if len(claude.Models) != 1 {
		t.Errorf("models count = %d, want 1", len(claude.Models))
	}
	if len(claude.Disabled) != 1 {
		t.Errorf("disabled count = %d, want 1", len(claude.Disabled))
	}
	if claude.Disabled[0] != "claude-opus-4-8" {
		t.Errorf("disabled[0] = %q, want %q", claude.Disabled[0], "claude-opus-4-8")
	}
}

func TestRemoveModel_NotFound(t *testing.T) {
	cfg := &ModelConfig{Providers: map[string]ProviderModels{
		"claude": {Models: []string{"claude-sonnet-5"}},
	}}

	if RemoveModel(cfg, "claude", "nonexistent") {
		t.Error("RemoveModel returned true for nonexistent model")
	}

	if RemoveModel(cfg, "nonexistent-provider", "model") {
		t.Error("RemoveModel returned true for nonexistent provider")
	}
}

func TestSetDefaultModel(t *testing.T) {
	cfg := &ModelConfig{Providers: map[string]ProviderModels{
		"claude": {
			Default: "claude-sonnet-5",
			Models:  []string{"claude-opus-4-8", "claude-sonnet-5"},
		},
	}}

	if !SetDefaultModel(cfg, "claude", "claude-opus-4-8") {
		t.Error("SetDefaultModel returned false for enabled model")
	}
	if cfg.Providers["claude"].Default != "claude-opus-4-8" {
		t.Errorf("default = %q, want %q", cfg.Providers["claude"].Default, "claude-opus-4-8")
	}

	// Not in enabled list
	if SetDefaultModel(cfg, "claude", "claude-haiku-4-5") {
		t.Error("SetDefaultModel returned true for model not in enabled list")
	}

	// Unknown provider
	if SetDefaultModel(cfg, "unknown", "model") {
		t.Error("SetDefaultModel returned true for unknown provider")
	}
}

func TestFormatModelConfigFile_RoundTrip(t *testing.T) {
	cfg := &ModelConfig{Providers: map[string]ProviderModels{
		"claude": {
			Default:  "claude-sonnet-5",
			Models:   []string{"claude-opus-4-8", "claude-sonnet-5"},
			Disabled: []string{"claude-haiku-4-5"},
		},
	}}

	output := FormatModelConfigFile(cfg)

	// Parse the output back
	parsed, err := ParseModelConfig(output)
	if err != nil {
		t.Fatalf("ParseModelConfig round-trip: %v", err)
	}

	claude := parsed.Providers["claude"]
	if claude.Default != "claude-sonnet-5" {
		t.Errorf("round-trip default = %q, want %q", claude.Default, "claude-sonnet-5")
	}
	if len(claude.Models) != 2 {
		t.Errorf("round-trip models = %d, want 2", len(claude.Models))
	}
	if len(claude.Disabled) != 1 {
		t.Errorf("round-trip disabled = %d, want 1", len(claude.Disabled))
	}
}

func TestFormatModelList(t *testing.T) {
	cfg := &ModelConfig{Providers: map[string]ProviderModels{
		"claude": {
			Default:  "claude-sonnet-5",
			Models:   []string{"claude-opus-4-8", "claude-sonnet-5"},
			Disabled: []string{"claude-haiku-4-5"},
		},
	}}

	output := FormatModelList(cfg)

	if !strings.Contains(output, "Provider: claude") {
		t.Error("output missing provider name")
	}
	if !strings.Contains(output, "claude-sonnet-5 (default)") {
		t.Error("output missing default marker")
	}
	if !strings.Contains(output, "claude-opus-4-8") {
		t.Error("output missing enabled model")
	}
	if !strings.Contains(output, "1 hidden") {
		t.Error("output missing hidden count")
	}
}

func TestFormatModelList_Empty(t *testing.T) {
	cfg := &ModelConfig{Providers: make(map[string]ProviderModels)}
	output := FormatModelList(cfg)
	if !strings.Contains(output, "No models configured") {
		t.Error("empty config should show 'No models configured'")
	}
}

func TestLoadModelConfigFromFile_Missing(t *testing.T) {
	cfg, err := LoadModelConfigFromFile("/nonexistent/path/models.conf")
	if err != nil {
		t.Fatalf("missing file should return empty config, got error: %v", err)
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("missing file providers = %d, want 0", len(cfg.Providers))
	}
}

func TestLoadModelConfigFromFile_Real(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.conf")

	content := `[claude]
# default: claude-sonnet-5
claude-opus-4-8
claude-sonnet-5
claude-haiku-4-5

[codex]
# default: gpt-5.5
gpt-5.5
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	cfg, err := LoadModelConfigFromFile(path)
	if err != nil {
		t.Fatalf("LoadModelConfigFromFile: %v", err)
	}

	if len(cfg.Providers) != 2 {
		t.Errorf("providers = %d, want 2", len(cfg.Providers))
	}
	if len(cfg.Providers["claude"].Models) != 3 {
		t.Errorf("claude models = %d, want 3", len(cfg.Providers["claude"].Models))
	}
	if cfg.Providers["codex"].Default != "gpt-5.5" {
		t.Errorf("codex default = %q, want %q", cfg.Providers["codex"].Default, "gpt-5.5")
	}
}

func TestParseModelConfig_MultipleProviders(t *testing.T) {
	content := `
[claude]
# default: claude-sonnet-5
claude-opus-4-8
claude-sonnet-5

[opencode]
# default: opencode-go/minimax-m2.5
opencode-go/minimax-m2.5
opencode-go/deepseek-v4-pro

[codex]
# default: gpt-5.5
gpt-5.5
gpt-5.4-mini
`
	cfg, err := ParseModelConfig(content)
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}

	if len(cfg.Providers) != 3 {
		t.Errorf("providers = %d, want 3", len(cfg.Providers))
	}

	tests := []struct {
		provider string
		count    int
		def      string
	}{
		{"claude", 2, "claude-sonnet-5"},
		{"opencode", 2, "opencode-go/minimax-m2.5"},
		{"codex", 2, "gpt-5.5"},
	}

	for _, tt := range tests {
		pm := cfg.Providers[tt.provider]
		if len(pm.Models) != tt.count {
			t.Errorf("%s models = %d, want %d", tt.provider, len(pm.Models), tt.count)
		}
		if pm.Default != tt.def {
			t.Errorf("%s default = %q, want %q", tt.provider, pm.Default, tt.def)
		}
	}
}
