package bus

import (
	"strings"
	"testing"
)

// A retired builtin name must fail naming its successor — never resolve
// silently as an alias, and never fail without the pointer. The successor
// itself must resolve, so no retired name points at a dead end.
func TestResolveGraphTemplateRetiredNameNamesSuccessor(t *testing.T) {
	for retired, successor := range map[string]string{
		"story-to-spec":         "10-story-to-spec",
		"build-test-review":     "30-build-test-review",
		"spec-to-pr":            "50-spec-to-pr",
		"req-code-pr":           "50-spec-to-pr",
		"story-lifecycle":       "50-spec-to-pr",
		"pr-local-review":       "70-pr-local-review",
		"commit-pr-review-loop": "80-pr-review-fix",
		"update-spec-docs":      "100-docs-sync",
		"deploy-verify":         "120-deploy-verify",
	} {
		g, _, err := ResolveGraphTemplate(retired)
		if err == nil || g != nil {
			t.Errorf("retired %q must not resolve, got graph=%v err=%v", retired, g != nil, err)
			continue
		}
		if !strings.Contains(err.Error(), `"`+successor+`"`) {
			t.Errorf("retired %q must name its successor %q: %v", retired, successor, err)
		}
		if _, src, err := ResolveGraphTemplate(successor); err != nil || src != "builtin" {
			t.Errorf("successor %q must resolve as builtin, got src=%q err=%v", successor, src, err)
		}
	}
	if _, _, err := ResolveGraphTemplate("no-such-template"); err == nil || strings.Contains(err.Error(), "renamed") {
		t.Errorf("an unknown name that was never renamed must not claim a rename: %v", err)
	}
}
