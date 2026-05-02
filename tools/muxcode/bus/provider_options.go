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

// AvailableProviders returns the list of known providers with installed status.
func AvailableProviders() []ProviderOption {
	providers := []ProviderOption{
		{
			Name:    "Claude Code",
			CLI:     "claude",
			Models:  []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-0"},
			Default: "claude-sonnet-4-6",
		},
		{
			Name:    "OpenCode",
			CLI:     "opencode",
			Models:  []string{"opencode-go/deepseek-v4-pro", "opencode-go/gpt-4.1", "opencode-go/gemini-2.5-pro"},
			Default: "opencode-go/deepseek-v4-pro",
		},
		{
			Name:    "Codex",
			CLI:     "codex",
			Models:  []string{"codex-mini-latest", "o4-mini", "o3"},
			Default: "codex-mini-latest",
		},
		{
			Name:    "Local (Ollama)",
			CLI:     "local",
			Models:  nil, // populated dynamically
			Default: "",
		},
	}

	// Check installed status and populate local models
	for i := range providers {
		providers[i].Installed = isProviderInstalled(providers[i].CLI)
		if providers[i].CLI == "local" {
			providers[i].Models = listOllamaModels()
			if len(providers[i].Models) > 0 {
				providers[i].Default = providers[i].Models[0]
			}
		}
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
