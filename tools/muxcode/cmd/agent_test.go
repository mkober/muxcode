package cmd

import "testing"

// The definition probe answers only for roles it can attribute a pane to — a
// known window role or a spawn worker. A typo is rejected up front rather than
// answered with "unknown", which would read like a verdict about a real agent.
func TestDefinitionRole(t *testing.T) {
	for _, role := range []string{"plan", "commit", "spawn-ea804fff"} {
		if !definitionRole(role) {
			t.Errorf("definitionRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"", "plna", "spawnless"} {
		if definitionRole(role) {
			t.Errorf("definitionRole(%q) = true, want false", role)
		}
	}
}
