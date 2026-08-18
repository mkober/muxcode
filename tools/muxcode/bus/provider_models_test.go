package bus

import (
	"os/exec"
	"strings"
	"testing"
)

// TestProviderDefaultIsSelectable pins each provider's default to its own model
// list. A default that is not offered cannot be chosen back once the user picks
// something else, and reads as a typo the selector can never surface.
func TestProviderDefaultIsSelectable(t *testing.T) {
	for _, p := range AvailableProviders() {
		if p.Default == "" {
			continue
		}
		found := false
		for _, m := range p.Models {
			if m == p.Default {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("provider %q default %q is not in its model list %v",
				p.CLI, p.Default, p.Models)
		}
	}
}

// TestOpenCodeModelsExist checks the configured opencode ids against the
// catalog the CLI actually offers. The list previously carried
// opencode-go/qwen3.5-plus, which does not exist: an agent pointed at a phantom
// model launches, shows a healthy pane, and only fails on its first request.
//
// Skipped when the opencode CLI is absent, so the suite still runs anywhere.
func TestOpenCodeModelsExist(t *testing.T) {
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode CLI not installed")
	}
	out, err := exec.Command(bin, "models").Output()
	if err != nil {
		t.Skipf("opencode models unavailable: %v", err)
	}
	catalog := map[string]bool{}
	for _, ln := range strings.Split(string(out), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			catalog[ln] = true
		}
	}
	if len(catalog) == 0 {
		t.Skip("opencode models returned nothing")
	}

	for _, p := range AvailableProviders() {
		if p.CLI != "opencode" {
			continue
		}
		for _, m := range p.Models {
			if !catalog[m] {
				t.Errorf("model %q is not offered by `opencode models`", m)
			}
		}
	}
}
