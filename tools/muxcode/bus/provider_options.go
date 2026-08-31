package bus

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
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
		Default: "claude-sonnet-5",
		Models:  []string{"claude-fable-5", "claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5"},
	},
	"opencode": {
		// Latest version of each opencode-go family, verified against
		// `opencode models`. Where a family ships parallel tiers at that same
		// newest version they are all kept (deepseek pro/flash, mimo pro/base,
		// qwen max/plus); superseded versions are not (glm 5.1/5.2, kimi
		// k2.6/k2.7-code, minimax m2.7, qwen 3.6-plus/3.7-max).
		//
		// Every id here must exist in `opencode models`. The previous list
		// carried opencode-go/qwen3.5-plus, which the catalog does not offer —
		// an agent pointed at a phantom model still launches and looks healthy,
		// then fails on its first request.
		Default: "opencode-go/minimax-m3",
		Models: []string{
			"opencode-go/grok-4.6",
			"opencode-go/gpt-5.6-luna",
			"opencode-go/glm-5.3",
			"opencode-go/kimi-k3",
			"opencode-go/minimax-m3",
			"opencode-go/mimo-v2.5-pro",
			"opencode-go/mimo-v2.5",
			"opencode-go/qwen3.8-max",
			"opencode-go/qwen3.7-plus",
			"opencode-go/deepseek-v4-pro",
			"opencode-go/deepseek-v4-flash",
			"opencode-go/hy3",
		},
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

// WindowFKey returns the F-key label for a window, derived from what
// the bindings actually select: F1–F10 map to window_index 1–10
// (bind -n F<N> → select-window -t:<N>), and F11/F12 map to the first
// and second spawn window by ascending index (MUX-128's slot order in
// NthSpawnWindowIndex). List position diverges from window_index
// whenever a window occupies index 0 (the research hold window), which
// put every label one too high and advertised commit as the then-
// unbound F11. Anything else — index 0, a third-or-later spawn, an
// 11+ non-spawn window — has no binding and returns "", the documented
// not-found value.
func WindowFKey(session, window string) string {
	out, err := TmuxOutput("list-windows", "-t", session, "-F", "#{window_index}:#W")
	if err != nil {
		return ""
	}
	matchIdx := -1
	var spawnIdxs []int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		idx, name, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		n, convErr := strconv.Atoi(idx)
		if convErr != nil {
			continue
		}
		if strings.HasPrefix(name, "spawn-") {
			spawnIdxs = append(spawnIdxs, n)
		}
		if name == window {
			matchIdx = n
		}
	}
	if matchIdx >= 1 && matchIdx <= 10 {
		return fmt.Sprintf("F%d", matchIdx)
	}
	if matchIdx > 10 && strings.HasPrefix(window, "spawn-") {
		sort.Ints(spawnIdxs)
		for slot, idx := range spawnIdxs {
			if idx == matchIdx && slot < 2 {
				return fmt.Sprintf("F%d", 11+slot)
			}
		}
	}
	return ""
}
