package bus

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ModelConfig holds provider-keyed model lists.
// Each key is a CLI identifier (e.g. "claude", "opencode", "codex").
type ModelConfig struct {
	Providers map[string]ProviderModels
}

// ProviderModels holds model settings for a single provider.
type ProviderModels struct {
	Default  string   // default model for this provider
	Models   []string // enabled (visible) models
	Disabled []string // commented-out models (preserved for round-trip)
}

// DefaultModelConfigPath returns the user-global model config path.
func DefaultModelConfigPath() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "muxcode", "models.conf")
}

// ResolveModelConfigPath resolves the model config file path.
// Resolution: .muxcode/models.conf → ~/.config/muxcode/models.conf
func ResolveModelConfigPath() string {
	if _, err := os.Stat(".muxcode/models.conf"); err == nil {
		return ".muxcode/models.conf"
	}
	return DefaultModelConfigPath()
}

// LoadModelConfig reads the model config from the resolved path.
// Returns an empty config (not an error) if the file doesn't exist.
func LoadModelConfig() (*ModelConfig, error) {
	path := ResolveModelConfigPath()
	if path == "" {
		return &ModelConfig{Providers: make(map[string]ProviderModels)}, nil
	}
	return LoadModelConfigFromFile(path)
}

// LoadModelConfigFromFile reads the model config from a specific path.
func LoadModelConfigFromFile(path string) (*ModelConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ModelConfig{Providers: make(map[string]ProviderModels)}, nil
		}
		return nil, fmt.Errorf("read model config: %w", err)
	}

	return ParseModelConfig(string(data))
}

// ParseModelConfig parses the text-based model config format.
//
// Format:
//   - [provider-name] starts a provider section
//   - # default: <model> sets the default model for the current section
//   - Lines starting with # are comments (disabled models if they match a model name)
//   - Non-comment, non-empty lines are enabled model identifiers
func ParseModelConfig(content string) (*ModelConfig, error) {
	cfg := &ModelConfig{Providers: make(map[string]ProviderModels)}

	currentProvider := ""
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

		// Skip lines before any section header
		if currentProvider == "" {
			continue
		}

		// Comment line
		if strings.HasPrefix(line, "#") {
			inner := strings.TrimSpace(line[1:])

			// Default directive: # default: <model>
			if strings.HasPrefix(inner, "default:") {
				val := strings.TrimSpace(strings.TrimPrefix(inner, "default:"))
				if val != "" {
					pm := cfg.Providers[currentProvider]
					pm.Default = val
					cfg.Providers[currentProvider] = pm
				}
				continue
			}

			// Commented-out model (simple identifier check)
			if isModelName(inner) {
				pm := cfg.Providers[currentProvider]
				pm.Disabled = append(pm.Disabled, inner)
				cfg.Providers[currentProvider] = pm
			}
			continue
		}

		// Enabled model
		if isModelName(line) {
			pm := cfg.Providers[currentProvider]
			pm.Models = append(pm.Models, line)
			cfg.Providers[currentProvider] = pm
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan config: %w", err)
	}

	return cfg, nil
}

// isModelName returns true if s looks like a model identifier.
// Allows: lowercase letters, uppercase letters, digits, hyphens, dots,
// slashes (for provider prefixes like "opencode-go/"), underscores.
func isModelName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '/' || c == '_') {
			return false
		}
	}
	return true
}

// SaveModelConfig writes the model config to the resolved path.
func SaveModelConfig(cfg *ModelConfig) error {
	path := ResolveModelConfigPath()
	if path == "" {
		return fmt.Errorf("cannot determine config path")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	content := FormatModelConfigFile(cfg)
	return os.WriteFile(path, []byte(content), 0644)
}

// FormatModelConfigFile renders the config as the text-based file format.
func FormatModelConfigFile(cfg *ModelConfig) string {
	var b strings.Builder
	b.WriteString("# MuxCode Model Configuration\n")
	b.WriteString("# Configure available models per provider for the provider selector and hot reload.\n")
	b.WriteString("# Manage with: muxcode model list|add|remove\n\n")

	providers := make([]string, 0, len(cfg.Providers))
	for p := range cfg.Providers {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	for _, provider := range providers {
		pm := cfg.Providers[provider]
		b.WriteString(fmt.Sprintf("[%s]\n", provider))
		if pm.Default != "" {
			b.WriteString(fmt.Sprintf("# default: %s\n", pm.Default))
		}

		// Enabled models
		for _, m := range pm.Models {
			b.WriteString(m + "\n")
		}

		// Disabled models as comments
		if len(pm.Disabled) > 0 && len(pm.Models) > 0 {
			b.WriteString("\n")
		}
		for _, m := range pm.Disabled {
			b.WriteString("# " + m + "\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// AddModel adds a model to a provider's enabled list (no duplicates).
// If the model is in the disabled list, it is moved to enabled.
// Returns true if the model was added (not already enabled).
func AddModel(cfg *ModelConfig, provider, model string) bool {
	pm := cfg.Providers[provider]

	// Check if already enabled
	for _, m := range pm.Models {
		if m == model {
			return false
		}
	}

	// Remove from disabled if present
	for i, m := range pm.Disabled {
		if m == model {
			pm.Disabled = append(pm.Disabled[:i], pm.Disabled[i+1:]...)
			break
		}
	}

	pm.Models = append(pm.Models, model)
	cfg.Providers[provider] = pm
	return true
}

// RemoveModel removes a model from a provider's enabled list.
// The model is moved to the disabled list.
// Returns true if the model was found and removed.
func RemoveModel(cfg *ModelConfig, provider, model string) bool {
	pm, ok := cfg.Providers[provider]
	if !ok {
		return false
	}
	for i, m := range pm.Models {
		if m == model {
			pm.Models = append(pm.Models[:i], pm.Models[i+1:]...)
			// Add to disabled list if not already there
			found := false
			for _, d := range pm.Disabled {
				if d == model {
					found = true
					break
				}
			}
			if !found {
				pm.Disabled = append(pm.Disabled, model)
			}
			cfg.Providers[provider] = pm
			return true
		}
	}
	return false
}

// SetDefaultModel sets the default model for a provider.
// The model must be in the enabled list. Returns true if set.
func SetDefaultModel(cfg *ModelConfig, provider, model string) bool {
	pm, ok := cfg.Providers[provider]
	if !ok {
		return false
	}
	for _, m := range pm.Models {
		if m == model {
			pm.Default = model
			cfg.Providers[provider] = pm
			return true
		}
	}
	return false
}

// FormatModelList formats the model config for display.
func FormatModelList(cfg *ModelConfig) string {
	if len(cfg.Providers) == 0 {
		return "No models configured.\nRun: muxcode model add <model> --provider <provider> to add models.\n"
	}

	var b strings.Builder
	providers := make([]string, 0, len(cfg.Providers))
	for p := range cfg.Providers {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	for _, provider := range providers {
		pm := cfg.Providers[provider]
		b.WriteString(fmt.Sprintf("Provider: %s\n", provider))

		if len(pm.Models) == 0 {
			b.WriteString("  (none configured)\n")
		} else {
			for _, m := range pm.Models {
				if m == pm.Default {
					b.WriteString(fmt.Sprintf("  ✓ %s (default)\n", m))
				} else {
					b.WriteString(fmt.Sprintf("  ✓ %s\n", m))
				}
			}
		}
		if len(pm.Disabled) > 0 {
			b.WriteString(fmt.Sprintf("  (%d hidden)\n", len(pm.Disabled)))
		}
		b.WriteString("\n")
	}
	return b.String()
}
