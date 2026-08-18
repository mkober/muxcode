package bus

import (
	"strings"
	"testing"
)

func TestAvailableProviders_Count(t *testing.T) {
	providers := AvailableProviders()
	if len(providers) != 4 {
		t.Errorf("AvailableProviders() returned %d providers, want 4", len(providers))
	}
}

func TestAvailableProviders_CLIs(t *testing.T) {
	providers := AvailableProviders()
	expectedCLIs := []string{"claude", "opencode", "codex", "local"}
	for i, expected := range expectedCLIs {
		if i >= len(providers) {
			t.Fatalf("missing provider at index %d", i)
		}
		if providers[i].CLI != expected {
			t.Errorf("providers[%d].CLI = %q, want %q", i, providers[i].CLI, expected)
		}
	}
}

func TestAvailableProviders_Names(t *testing.T) {
	providers := AvailableProviders()
	expectedNames := []string{"Claude Code", "OpenCode", "Codex", "Local (Ollama)"}
	for i, expected := range expectedNames {
		if i >= len(providers) {
			t.Fatalf("missing provider at index %d", i)
		}
		if providers[i].Name != expected {
			t.Errorf("providers[%d].Name = %q, want %q", i, providers[i].Name, expected)
		}
	}
}

func TestAvailableProviders_Defaults(t *testing.T) {
	providers := AvailableProviders()
	tests := []struct {
		cli     string
		wantDef string
	}{
		{"claude", "claude-sonnet-5"},
		{"opencode", "opencode-go/minimax-m3"},
		{"codex", "gpt-5.5"},
	}
	for _, tt := range tests {
		p := ProviderByCLI(providers, tt.cli)
		if p == nil {
			t.Errorf("ProviderByCLI(%q) returned nil", tt.cli)
			continue
		}
		if p.Default != tt.wantDef {
			t.Errorf("ProviderByCLI(%q).Default = %q, want %q", tt.cli, p.Default, tt.wantDef)
		}
	}
}

// Asserted as properties rather than exact counts: AvailableProviders reads the
// user's models.conf, so a count pinned here fails on any machine whose model
// list has been edited — including every time the shipped list changes.
func TestAvailableProviders_Models(t *testing.T) {
	providers := AvailableProviders()
	for _, cli := range []string{"claude", "opencode"} {
		p := ProviderByCLI(providers, cli)
		if p == nil {
			t.Fatalf("%s provider not found", cli)
		}
		if len(p.Models) == 0 {
			t.Errorf("%s offers no models", cli)
		}
		if p.Default == "" {
			t.Errorf("%s has no default model", cli)
		}
	}

	// OpenCode ids are meaningless to the CLI without their provider prefix.
	opencode := ProviderByCLI(providers, "opencode")
	for _, m := range opencode.Models {
		if !strings.Contains(m, "/") {
			t.Errorf("opencode model %q is missing its provider prefix", m)
		}
	}
}

func TestProviderByIndex(t *testing.T) {
	providers := AvailableProviders()

	// Valid index
	p := ProviderByIndex(providers, 0)
	if p == nil {
		t.Fatal("ProviderByIndex(0) returned nil")
	}
	if p.CLI != "claude" {
		t.Errorf("ProviderByIndex(0).CLI = %q, want %q", p.CLI, "claude")
	}

	// Out of range
	if ProviderByIndex(providers, -1) != nil {
		t.Error("ProviderByIndex(-1) should return nil")
	}
	if ProviderByIndex(providers, 100) != nil {
		t.Error("ProviderByIndex(100) should return nil")
	}
}

func TestProviderByCLI(t *testing.T) {
	providers := AvailableProviders()

	// Found
	p := ProviderByCLI(providers, "opencode")
	if p == nil {
		t.Fatal("ProviderByCLI(opencode) returned nil")
	}
	if p.Name != "OpenCode" {
		t.Errorf("ProviderByCLI(opencode).Name = %q, want %q", p.Name, "OpenCode")
	}

	// Not found
	if ProviderByCLI(providers, "nonexistent") != nil {
		t.Error("ProviderByCLI(nonexistent) should return nil")
	}
}

func TestIsProviderInstalled_Claude(t *testing.T) {
	// claude should be installed in the dev environment
	installed := isProviderInstalled("claude")
	// We can't assert the value since it depends on the environment,
	// but we verify it doesn't panic
	_ = installed
}

func TestIsProviderInstalled_Local(t *testing.T) {
	// "local" maps to "ollama" binary
	installed := isProviderInstalled("local")
	_ = installed
}
