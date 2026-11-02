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
// Skipped when the opencode CLI is absent, so the suite still runs anywhere, and
// when the catalog offers no model from any listed provider: on 2026-11-02 the
// newly unsandboxed Codex test agent reached `opencode models` on a machine with
// no opencode credentials, which lists only the free opencode/* models, and
// failed every opencode-go id (missingOpenCodeModels).
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

	var models []string
	for _, p := range AvailableProviders() {
		if p.CLI == "opencode" {
			models = append(models, p.Models...)
		}
	}
	missing, judged := missingOpenCodeModels(models, catalog)
	if judged == 0 {
		t.Skip("opencode models offers no model from any listed provider — none configured here")
	}
	for _, m := range missing {
		t.Errorf("model %q is not offered by `opencode models`", m)
	}
}

// missingOpenCodeModels returns the ids in models the catalog does not offer,
// and how many ids it judged. Only providers the catalog offers at least one
// model from are judged: a provider absent from it entirely is not configured
// on this machine, so its ids can be judged neither way.
func missingOpenCodeModels(models []string, catalog map[string]bool) ([]string, int) {
	offered := map[string]bool{}
	for id := range catalog {
		if provider, _, ok := strings.Cut(id, "/"); ok {
			offered[provider] = true
		}
	}
	var missing []string
	judged := 0
	for _, m := range models {
		provider, _, _ := strings.Cut(m, "/")
		if !offered[provider] {
			continue
		}
		judged++
		if !catalog[m] {
			missing = append(missing, m)
		}
	}
	return missing, judged
}

// A phantom id from a provider the catalog does offer is still caught, while
// ids from a provider it offers nothing from are not judged. Negative control
// for the skip above: without it, a filter that judged nothing would pass.
func TestMissingOpenCodeModels(t *testing.T) {
	catalog := map[string]bool{"opencode-go/real": true, "opencode/free": true}
	models := []string{"opencode-go/real", "opencode-go/phantom", "other-go/unlisted"}
	missing, judged := missingOpenCodeModels(models, catalog)
	if judged != 2 {
		t.Errorf("judged = %d, want 2 (the opencode-go ids only)", judged)
	}
	if len(missing) != 1 || missing[0] != "opencode-go/phantom" {
		t.Errorf("missing = %v, want [opencode-go/phantom]", missing)
	}
}
