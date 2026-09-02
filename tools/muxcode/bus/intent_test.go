package bus

import (
	"errors"
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

// A spec whose H1 already leads with its key ("# MUX-138: Title", the
// repo's spec format) must not describe as "MUX-138 MUX-138: Title" —
// seen live on the first branch-derived launch (run 64c5fe4b).
func TestDescribeSpecIntentDoesNotDoubleKey(t *testing.T) {
	root := t.TempDir()
	writeIntentSpec(t, root, "backlog", "MUX-138-x.md", "# MUX-138: GitHub versioning\n### Phase 1: Stamp\n- [ ] s\n")
	got, ok := expandIntentKeyIn(root, "138")
	if !ok || got != "MUX-138 GitHub versioning — Phase 1: Stamp" {
		t.Errorf("got %q ok=%v", got, ok)
	}
	writeIntentSpec(t, root, "backlog", "MUX-139-y.md", "# Plain Title\n")
	if got, _ := expandIntentKeyIn(root, "139"); got != "MUX-139 Plain Title" {
		t.Errorf("a title without the key keeps the prefix once, got %q", got)
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

func TestBranchSpecKey(t *testing.T) {
	cases := map[string]string{
		"MUX-138-github-versioning-releases": "MUX-138",
		"MUX-138":                            "MUX-138",
		"feature/MUX-138-slug":               "MUX-138",
		"MUX-138/wip":                        "MUX-138",
		"mux-138-lowercase":                  "MUX-138",
		"PBP1-456-add-validation":            "PBP1-456",
		"main":                               "",
		"fix-MUX-138-typo":                   "",
		"MUX138-no-hyphen":                   "",
		"":                                   "",
	}
	for branch, want := range cases {
		if got := BranchSpecKey(branch); got != want {
			t.Errorf("BranchSpecKey(%q) = %q, want %q", branch, got, want)
		}
	}
}

func TestResolveBranchSpecIn(t *testing.T) {
	root := t.TempDir()
	writeIntentSpec(t, root, "backlog", "MUX-138-github-versioning-releases.md",
		"# GitHub Versioning & Releases\n### Phase 1: Stamp\n- [x] done\n### Phase 2: Daemon\n- [ ] open\n")
	spec, err := resolveBranchSpecIn(root, "MUX-138-github-versioning-releases")
	if err != nil {
		t.Fatalf("must resolve: %v", err)
	}
	wantPath := filepath.Join("docs", "requirements", "backlog", "MUX-138-github-versioning-releases.md")
	if spec.Key != "MUX-138" || spec.Dir != "backlog" || spec.Path != wantPath {
		t.Errorf("got %+v, want key MUX-138 dir backlog path %s", spec, wantPath)
	}
	if !strings.Contains(spec.Intent, "GitHub Versioning") || !strings.Contains(spec.Intent, "Phase 2") || strings.Contains(spec.Intent, "Phase 1") {
		t.Errorf("intent %q must carry the title and the first OPEN phase", spec.Intent)
	}
}

// Every failure names its reason: the launcher shows it in place of a
// blank prompt, so an unexplained miss would defeat the fallback.
func TestResolveBranchSpecInReasons(t *testing.T) {
	root := t.TempDir()
	writeIntentSpec(t, root, "backlog", "MUX-138-x.md", "# X\n")
	for branch, want := range map[string]string{
		"":          "no current branch",
		"main":      "carries no spec key",
		"MUX-999-x": "MUX-999",
	} {
		_, err := resolveBranchSpecIn(root, branch)
		if err == nil {
			t.Errorf("branch %q must not resolve", branch)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("branch %q: error %q must name %q", branch, err, want)
		}
	}
}

func TestResolveBranchSpecInDraftsBeatBacklog(t *testing.T) {
	root := t.TempDir()
	writeIntentSpec(t, root, "backlog", "MUX-7-old.md", "# Backlog Title\n")
	writeIntentSpec(t, root, "drafts", "MUX-7-new.md", "# Drafts Title\n")
	spec, err := resolveBranchSpecIn(root, "MUX-7-anything")
	if err != nil || spec.Dir != "drafts" || !strings.Contains(spec.Intent, "Drafts Title") {
		t.Errorf("drafts must win, got %+v err=%v", spec, err)
	}
}

// The exported wrapper reads the SESSION repo's branch, not the process
// cwd's — the TUI is not guaranteed to run from the repo.
func TestResolveBranchSpecReadsSessionRepoBranch(t *testing.T) {
	dir := makeBranchRepo(t, []string{"main", "MUX-9-live"})
	writeIntentSpec(t, dir, "drafts", "MUX-9-live.md", "# Live\n### Phase 1: A\n- [ ] s\n")
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_SESSION_REPO_DIR", dir)
	spec, err := ResolveBranchSpec("no-such-session")
	if err != nil || spec.Key != "MUX-9" || spec.Branch != "MUX-9-live" {
		t.Fatalf("got %+v err=%v", spec, err)
	}
}

func TestActiveSpecRelationIn(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join("docs", "requirements", "backlog", "MUX-1-a.md")
	if r := activeSpecRelationIn(root, "", p); r.Current != "" || r.Matches {
		t.Errorf("unset pointer must be empty and not match: %+v", r)
	}
	if r := activeSpecRelationIn(root, p, p); !r.Matches {
		t.Errorf("same relative pointer must match: %+v", r)
	}
	if r := activeSpecRelationIn(root, filepath.Join(root, p), p); !r.Matches {
		t.Errorf("absolute pointer to the same file must match: %+v", r)
	}
	other := filepath.Join("docs", "requirements", "drafts", "MUX-2-b.md")
	if r := activeSpecRelationIn(root, other, p); r.Matches || r.Current != other {
		t.Errorf("different pointer must not match and must be reported: %+v", r)
	}
}

// LaunchIntentFromBranch sets an unset pointer, leaves a matching one,
// and REFUSES to switch a differing one — the negative control being
// that the pointer is untouched after the refusal.
func TestLaunchIntentFromBranch(t *testing.T) {
	dir := makeBranchRepo(t, []string{"MUX-9-live"})
	writeIntentSpec(t, dir, "drafts", "MUX-9-live.md", "# Live\n### Phase 1: A\n- [ ] s\n")
	t.Setenv("MUXCODE_SESSION_REPO_DIR", dir)
	SetBusDirBase(t.TempDir())
	t.Cleanup(ResetBusDirBase)
	session := "intent-launch"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("docs", "requirements", "drafts", "MUX-9-live.md")

	spec, set, err := LaunchIntentFromBranch(session)
	if err != nil || !set || spec.Path != want || ReadActiveSpec(session) != want {
		t.Fatalf("unset pointer must be set to the branch spec: %+v set=%v err=%v pointer=%q", spec, set, err, ReadActiveSpec(session))
	}
	if _, set, err = LaunchIntentFromBranch(session); err != nil || set {
		t.Fatalf("matching pointer must be left alone: set=%v err=%v", set, err)
	}

	other := filepath.Join("docs", "requirements", "drafts", "MUX-2-b.md")
	if err := WriteActiveSpec(session, other); err != nil {
		t.Fatal(err)
	}
	_, set, err = LaunchIntentFromBranch(session)
	if !errors.Is(err, ErrActiveSpecMismatch) || set {
		t.Fatalf("differing pointer must refuse with ErrActiveSpecMismatch: set=%v err=%v", set, err)
	}
	if got := ReadActiveSpec(session); got != other {
		t.Errorf("refusal must not touch the pointer: got %q", got)
	}
	if !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), want) {
		t.Errorf("refusal must name both paths: %v", err)
	}
}
