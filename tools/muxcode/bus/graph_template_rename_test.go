package bus

import (
	"strings"
	"testing"
)

// A retired builtin name must fail naming its successor — never resolve
// silently as an alias, and never fail without the pointer.
func TestResolveGraphTemplateRetiredNameNamesSuccessor(t *testing.T) {
	g, _, err := ResolveGraphTemplate("req-code-pr")
	if err == nil || g != nil {
		t.Fatalf("retired name must not resolve, got graph=%v err=%v", g != nil, err)
	}
	if !strings.Contains(err.Error(), `"spec-to-pr"`) {
		t.Errorf("error must name the successor: %v", err)
	}

	if _, src, err := ResolveGraphTemplate("spec-to-pr"); err != nil || src != "builtin" {
		t.Fatalf("successor must resolve as builtin, got src=%q err=%v", src, err)
	}
	if g, _, err := ResolveGraphTemplate("story-lifecycle"); err == nil || g != nil || !strings.Contains(err.Error(), `"spec-to-pr"`) {
		t.Errorf("removed story-lifecycle must fail naming spec-to-pr, got graph=%v err=%v", g != nil, err)
	}
	if _, _, err := ResolveGraphTemplate("no-such-template"); err == nil || strings.Contains(err.Error(), "renamed") {
		t.Errorf("an unknown name that was never renamed must not claim a rename: %v", err)
	}
}
