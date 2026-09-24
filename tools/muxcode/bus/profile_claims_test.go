package bus

import (
	"strings"
	"testing"
)

// TestChainNoticesAssertNoFindings audits every default chain notice (MUX-182
// defect 4a): an event reports an exit code, never what the command found. A
// request may still ask for a finding ("verify … healthy"), which is an
// instruction, not a claim — the negative control that the audit reads type.
func TestChainNoticesAssertNoFindings(t *testing.T) {
	findings := []string{"healthy", "detected", "look good", "no errors", "verified"}
	var events, requestsAskingForFindings int
	for chain, ec := range DefaultConfig().EventChains {
		for outcome, actions := range map[string]ChainActions{"success": ec.OnSuccess, "failure": ec.OnFailure, "unknown": ec.OnUnknown} {
			for _, a := range actions {
				msg := strings.ToLower(a.Message)
				if a.Type != "event" {
					if strings.Contains(msg, "healthy") {
						requestsAskingForFindings++
					}
					continue
				}
				events++
				for _, f := range findings {
					if strings.Contains(msg, f) {
						t.Errorf("%s %s notice asserts a finding (%q): %q", chain, outcome, f, a.Message)
					}
				}
			}
		}
	}
	if events == 0 || requestsAskingForFindings == 0 {
		t.Fatalf("audit read %d event notices and %d finding requests — it must reach both", events, requestsAskingForFindings)
	}
	if got := DefaultConfig().EventChains["watch"].OnSuccess[0].Message; !strings.Contains(got, "exited 0") {
		t.Errorf("watch success notice must state the exit code, got %q", got)
	}
}
