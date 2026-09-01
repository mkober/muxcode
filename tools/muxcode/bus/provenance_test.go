package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo creates a git repo with one commit containing a source file
// and a docs file, returning its path. Skips the test when git is absent.
func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "docs/requirements/drafts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs/requirements/drafts/spec.md"), []byte("# Spec\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn("add", "-A")
	gitIn("-c", "user.email=test@test", "-c", "user.name=test", "commit", "-q", "-m", "base")
	return repo
}

// writeFixtureRun writes a hand-made graph run store entry so fingerprint
// tests control run state without building a full graph.
func writeFixtureRun(t *testing.T, session, id, state string) {
	t.Helper()
	dir := filepath.Join(GraphRunsDir(session), id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"id":"` + id + `","template":"t","state":"` + state + `","created_at":1,"updated_at":1}`)
	if err := os.WriteFile(filepath.Join(dir, "run.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRepoScopedFiles(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	spawnWT := filepath.Join(outside, "muxcode-spawn", "spawn-abc123")

	cases := []struct {
		name    string
		repoDir string
		in      []string
		want    []string
	}{
		{
			name:    "absolute path inside repo becomes repo-relative",
			repoDir: repo,
			in:      []string{filepath.Join(repo, "bus/pane_test.go")},
			want:    []string{"bus/pane_test.go"},
		},
		{
			name:    "absolute path outside repo is dropped",
			repoDir: repo,
			in:      []string{filepath.Join(outside, "muxcode-config")},
			want:    nil,
		},
		{
			// The B1 control: scoping keys on location, not name — the
			// spawn-worktree copy is rejected while the repo's own copy of
			// the same relative path passes.
			name:    "spawn-worktree path rejected while same relative path in repo accepted",
			repoDir: repo,
			in: []string{
				filepath.Join(spawnWT, "bus/pane_test.go"),
				filepath.Join(repo, "bus/pane_test.go"),
			},
			want: []string{"bus/pane_test.go"},
		},
		{
			name:    "relative traversal escaping the root is dropped",
			repoDir: repo,
			in:      []string{"../secrets/config", "bus/keep.go"},
			want:    []string{"bus/keep.go"},
		},
		{
			name:    "no resolvable repo dir presents nothing",
			repoDir: "",
			in:      []string{filepath.Join(repo, "bus/pane_test.go"), "bus/keep.go"},
			want:    nil,
		},
		{
			name:    "duplicates collapse",
			repoDir: repo,
			in:      []string{"bus/keep.go", filepath.Join(repo, "bus/keep.go")},
			want:    []string{"bus/keep.go"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RepoScopedFiles(tc.repoDir, tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestVerifyMovementFingerprint_NoEvidenceIsEmpty(t *testing.T) {
	useTempBusDir(t)
	if fp := VerifyMovementFingerprint("prov-test", ""); fp != "" {
		t.Errorf("no repo dir and no runs must yield no fingerprint, got %q", fp)
	}
	// A resolvable dir that is not a git repo yields no tree evidence either.
	if fp := VerifyMovementFingerprint("prov-test", t.TempDir()); fp != "" {
		t.Errorf("non-git repo dir must yield no fingerprint, got %q", fp)
	}
}

func TestVerifyMovementFingerprint_TreeMovement(t *testing.T) {
	useTempBusDir(t)
	repo := initGitRepo(t)
	session := "prov-tree-test"

	base := VerifyMovementFingerprint(session, repo)
	if base == "" {
		t.Fatal("git repo must yield a fingerprint")
	}

	// Docs-only movement is invisible: the verifier's own spec edit must not
	// read as progress (the census echoes' one write).
	if err := os.WriteFile(filepath.Join(repo, "docs/requirements/drafts/spec.md"), []byte("# Spec\n- [x] done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if fp := VerifyMovementFingerprint(session, repo); fp != base {
		t.Error("docs-only change moved the fingerprint")
	}

	// A source change is movement.
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if fp := VerifyMovementFingerprint(session, repo); fp == base {
		t.Error("source change did not move the fingerprint")
	}
}

func TestVerifyMovementFingerprint_RunStateMovement(t *testing.T) {
	useTempBusDir(t)
	session := "prov-run-test"

	writeFixtureRun(t, session, "r1", "running")
	fp1 := VerifyMovementFingerprint(session, "")
	if fp1 == "" {
		t.Fatal("graph run must yield a fingerprint even with no repo dir")
	}

	// The fire-11 shape: a run-state transition is movement on its own.
	writeFixtureRun(t, session, "r1", "failed")
	if fp2 := VerifyMovementFingerprint(session, ""); fp2 == fp1 {
		t.Error("run state transition did not move the fingerprint")
	}
}

func TestVerifyMovementMarkerRoundtripAndPurge(t *testing.T) {
	useTempBusDir(t)
	session := "prov-marker-test"
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := WriteVerifyMovementMarker(session, "abc123"); err != nil {
		t.Fatalf("write movement marker: %v", err)
	}
	if err := WriteReviewedMarker(session, "msg-1"); err != nil {
		t.Fatalf("write reviewed marker: %v", err)
	}
	if got := ReadVerifyMovementMarker(session); got != "abc123" {
		t.Errorf("movement marker roundtrip: got %q", got)
	}

	// Re-init purges both gate markers: session-scoped state must not
	// outlive its session.
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	if got := ReadVerifyMovementMarker(session); got != "" {
		t.Errorf("movement marker survived re-init: %q", got)
	}
	if got := ReadReviewedMarker(session); got != "" {
		t.Errorf("reviewed marker survived re-init: %q", got)
	}
}

// TestRepoScopedFilesRejectsSymlinkEscape pins the resolution-based
// boundary (review must-fix, 2026-09-01): a path lexically inside the
// repo that traverses a symlink pointing outside must be rejected —
// containment is proven on resolved locations, never path text. The
// positive controls keep the guard from over-rejecting: a genuine repo
// file still scopes, and a deleted file still scopes via its parent.
func TestRepoScopedFilesRejectsSymlinkEscape(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("external\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "inside.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "linkdir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The first two escape via the symlink (relative and absolute forms);
	// inside.go is the positive control; gone.go is deleted, resolving
	// via its parent.
	got := RepoScopedFiles(repo, []string{
		"linkdir/secret.txt",
		filepath.Join(repo, "linkdir/secret.txt"),
		"inside.go",
		"gone.go",
	})
	for _, p := range got {
		if filepath.Base(p) == "secret.txt" {
			t.Fatalf("symlink escape passed the boundary: %v", got)
		}
	}
	want := map[string]bool{"inside.go": true, "gone.go": true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("positive controls must still scope, got %v", got)
	}
}

// TestResolveSpecPathRefusesExternal pins the pointer boundary: an
// active-spec pointer is agent-written data, and one resolving outside
// the repo must return "" rather than hand the verifier an external
// file to read — the credentials-file echo, refused by construction.
// In-repo pointers still resolve.
func TestResolveSpecPathRefusesExternal(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	ext := filepath.Join(outside, "creds")
	if err := os.WriteFile(ext, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(repo, "docs", "spec.md")
	if err := os.MkdirAll(filepath.Dir(spec), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec, []byte("# spec\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := ResolveSpecPath(repo, ext); got != "" {
		t.Fatalf("external absolute pointer must refuse, got %q", got)
	}
	// No repo dir: containment is unprovable for every pointer shape, so
	// nothing resolves — the escape the 2026-09-01 must-fix closed rode an
	// absolute pointer past a caller that only checked resolvABLE ones.
	if got := ResolveSpecPath("", ext); got != "" {
		t.Fatalf("no repo dir: absolute pointer must refuse, got %q", got)
	}
	if got := ResolveSpecPath("", "docs/spec.md"); got != "" {
		t.Fatalf("no repo dir: relative pointer must refuse, got %q", got)
	}
	if got := ResolveSpecPath(repo, "../escape.md"); got != "" {
		t.Fatalf("relative escape must refuse, got %q", got)
	}
	if err := os.Symlink(ext, filepath.Join(repo, "link.md")); err == nil {
		if got := ResolveSpecPath(repo, "link.md"); got != "" {
			t.Fatalf("symlinked external pointer must refuse, got %q", got)
		}
	}
	if got := ResolveSpecPath(repo, "docs/spec.md"); got != spec {
		t.Fatalf("in-repo pointer must resolve, got %q want %q", got, spec)
	}
}
