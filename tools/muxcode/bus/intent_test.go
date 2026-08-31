package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIntentSpec(t *testing.T, root, dir, name, content string) {
	t.Helper()
	full := filepath.Join(root, "docs", "requirements", dir)
	if err := os.MkdirAll(full, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestExpandIntentKey(t *testing.T) {
	root := t.TempDir()
	writeIntentSpec(t, root, "drafts", "MUX-115-turn-budget.md",
		"# The Prompt Agent Burns Its Turn Budget\n\n"+
			"### Phase 1: Instrumentation\n- [x] done step\n\n"+
			"### Phase 2: Fix\n- [ ] open step\n\n"+
			"```markdown\n### Phase 9: fenced example\n- [ ] not real\n```\n")

	for _, in := range []string{"115", "MUX-115", "mux-115", " mux115 "} {
		got, ok := expandIntentKeyIn(root, in)
		if !ok {
			t.Fatalf("%q must resolve", in)
		}
		if !strings.Contains(got, "MUX-115") || !strings.Contains(got, "Turn Budget") {
			t.Errorf("%q → %q, want key + title", in, got)
		}
		if !strings.Contains(got, "Phase 2") || strings.Contains(got, "Phase 1") || strings.Contains(got, "Phase 9") {
			t.Errorf("%q → %q, want the first OPEN phase only (not done, not fenced)", in, got)
		}
	}
}

func TestExpandIntentKeyPassthrough(t *testing.T) {
	root := t.TempDir()
	for _, in := range []string{"999", "fix the tui", "MUX-115 already expanded intent"} {
		got, ok := expandIntentKeyIn(root, in)
		if ok || got != in {
			t.Errorf("%q must pass through unchanged, got %q ok=%v", in, got, ok)
		}
	}
}

// TestExpandIntentKeyForResolvesSessionRepoDir covers the exported
// wrapper: resolution must go through SessionRepoDir, not the cwd (plan
// finding 2026-08-28 — the inner tests bypass the wrapper entirely).
func TestExpandIntentKeyForResolvesSessionRepoDir(t *testing.T) {
	root := t.TempDir()
	writeIntentSpec(t, root, "drafts", "MUX-42-wrapper.md",
		"# Wrapper Title\n### Phase 1: Go\n- [ ] step\n")
	t.Setenv("MUXCODE_SESSION_REPO_DIR", root)
	got, ok := ExpandIntentKeyFor("no-such-session", "42")
	if !ok || !strings.Contains(got, "Wrapper Title") || !strings.Contains(got, "Phase 1") {
		t.Errorf("wrapper must resolve via SessionRepoDir, got %q ok=%v", got, ok)
	}
}

func TestExpandIntentKeyDraftsBeatBacklog(t *testing.T) {
	root := t.TempDir()
	writeIntentSpec(t, root, "backlog", "MUX-7-old.md", "# Backlog Title\n")
	writeIntentSpec(t, root, "drafts", "MUX-7-new.md", "# Drafts Title\n")
	got, ok := expandIntentKeyIn(root, "7")
	if !ok || !strings.Contains(got, "Drafts Title") {
		t.Errorf("drafts must win resolution, got %q ok=%v", got, ok)
	}
}
