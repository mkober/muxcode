package bus

import (
	"fmt"
	"os/exec"
	"strings"
)

// ProviderOption describes a CLI provider for the provider selector TUI.
type ProviderOption struct {
	Name      string   // display name (e.g. "Claude Code")
	CLI       string   // CLI identifier ("claude", "opencode", "codex", "local")
	Models    []string // known model IDs
	Default   string   // default model for this provider
	Installed bool     // true if the CLI binary is found on PATH
}

// providerDisplayName maps CLI identifiers to display names.
var providerDisplayName = map[string]string{
	"claude":   "Claude Code",
	"opencode": "OpenCode",
	"codex":    "Codex",
	"local":    "Local (Ollama)",
}

// providerOrder defines the display order for providers.
var providerOrder = []string{"claude", "opencode", "codex", "local"}

// hardcodedFallbackModels provides built-in defaults when no config file exists.
var hardcodedFallbackModels = map[string]ProviderModels{
	"claude": {
		Default: "claude-sonnet-4-6",
		Models:  []string{"claude-fable-5", "claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"},
	},
	"opencode": {
		Default: "opencode-go/minimax-m2.5",
		Models:  []string{"opencode-go/minimax-m2.5", "opencode-go/minimax-m2.7", "opencode-go/minimax-m3", "opencode-go/mimo-v2.5", "opencode-go/qwen3.5-plus", "opencode-go/deepseek-v4-pro"},
	},
	"codex": {
		Default: "gpt-5.5",
		Models:  []string{"gpt-5.5", "gpt-5.4-mini", "gpt-5.3-codex-spark"},
	},
}

// AvailableProviders returns the list of known providers with installed status.
// Model lists and defaults are read from models.conf (config-driven). Falls back
// to hardcoded defaults if the config file is missing or empty for a provider.
func AvailableProviders() []ProviderOption {
	// Load model config (returns empty config on missing file, never errors)
	modelCfg, _ := LoadModelConfig()

	var providers []ProviderOption
	for _, cli := range providerOrder {
		name := providerDisplayName[cli]
		if name == "" {
			name = cli
		}

		p := ProviderOption{
			Name: name,
			CLI:  cli,
		}

		// Try config-driven models first, then hardcoded fallback
		if pm, ok := modelCfg.Providers[cli]; ok && len(pm.Models) > 0 {
			p.Models = pm.Models
			p.Default = pm.Default
		} else if fb, ok := hardcodedFallbackModels[cli]; ok {
			p.Models = fb.Models
			p.Default = fb.Default
		}
		// Local (Ollama) models are always populated dynamically
		if cli == "local" {
			p.Models = listOllamaModels()
			if len(p.Models) > 0 && p.Default == "" {
				p.Default = p.Models[0]
			}
		}

		providers = append(providers, p)
	}

	// Check installed status
	for i := range providers {
		providers[i].Installed = isProviderInstalled(providers[i].CLI)
	}

	return providers
}

// isProviderInstalled checks if the CLI binary for a provider is on PATH.
func isProviderInstalled(cli string) bool {
	binary := cli
	switch cli {
	case "local":
		binary = "ollama"
	}
	_, err := exec.LookPath(binary)
	return err == nil
}

// listOllamaModels runs `ollama list` and parses model names.
// Returns nil if ollama is not available or has no models.
func listOllamaModels() []string {
	out, err := exec.Command("ollama", "list").Output()
	if err != nil {
		return nil
	}
	var models []string
	for i, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i == 0 {
			continue // skip header line
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			models = append(models, fields[0])
		}
	}
	return models
}

// ProviderByIndex returns the provider at the given index, or nil if out of range.
func ProviderByIndex(providers []ProviderOption, idx int) *ProviderOption {
	if idx < 0 || idx >= len(providers) {
		return nil
	}
	return &providers[idx]
}

// ProviderByCLI returns the provider matching the CLI identifier, or nil.
func ProviderByCLI(providers []ProviderOption, cli string) *ProviderOption {
	for i := range providers {
		if providers[i].CLI == cli {
			return &providers[i]
		}
	}
	return nil
}

// ResolveActiveAgentWindow determines the agent role for the currently focused
// tmux window. For mode-cycled windows (edit/plan), returns the active mode role.
func ResolveActiveAgentWindow(session string) (window, role string, err error) {
	out, execErr := exec.Command("tmux", "display-message", "-p", "#{window_name}").Output()
	if execErr != nil {
		return "", "", execErr
	}
	window = strings.TrimSpace(string(out))

	// Resolve active mode for mode-cycled windows
	role, modeErr := ActiveModeRole(session, window)
	if modeErr != nil {
		role = window // fallback to window name
	}

	return window, role, nil
}

// WindowFKey returns the F-key label for a window based on its position.
// Returns empty string if the window is not found.
func WindowFKey(session, window string) string {
	out, err := exec.Command("tmux", "list-windows", "-t", session, "-F", "#W").Output()
	if err != nil {
		return ""
	}
	for i, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == window {
			return fmt.Sprintf("F%d", i+1) // 1-indexed
		}
	}
	return ""
}
