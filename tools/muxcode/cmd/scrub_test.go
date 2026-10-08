package cmd

import "testing"

// A malformed call must fail rather than fall to the empty, non-sensitive
// role: the Codex wrap and the OpenCode plugin withhold output on a non-zero
// exit, but an echoed result reaches the model unredacted (PR 159 review).
func TestParseScrubArgs_RejectsMalformedRole(t *testing.T) {
	for _, args := range [][]string{
		{"--role"},
		{"--role", ""},
		{"--role", "--role"},
		{"--role", "run", "extra"},
		{"--rol", "run"},
		{"run"},
	} {
		if role, byRole, err := parseScrubArgs(args); err == nil {
			t.Errorf("parseScrubArgs(%q) = (%q, %v, nil), want an error", args, role, byRole)
		}
	}
}

// Negative control: the two forms callers use still parse.
func TestParseScrubArgs_AcceptsBothForms(t *testing.T) {
	if role, byRole, err := parseScrubArgs(nil); err != nil || byRole || role != "" {
		t.Errorf("no args = (%q, %v, %v), want the plain scrub", role, byRole, err)
	}
	if role, byRole, err := parseScrubArgs([]string{"--role", "watch"}); err != nil || !byRole || role != "watch" {
		t.Errorf("--role watch = (%q, %v, %v), want role watch", role, byRole, err)
	}
}
