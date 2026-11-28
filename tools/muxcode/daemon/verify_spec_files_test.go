package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// verifyChangedFiles returns the "Changed files (repo-relative): …" list of a
// verify-spec payload.
func verifyChangedFiles(t *testing.T, payload string) string {
	t.Helper()
	const label = "Changed files (repo-relative): "
	i := strings.Index(payload, label)
	if i < 0 {
		t.Fatalf("verify-spec payload has no changed-files list: %s", payload)
	}
	rest := payload[i+len(label):]
	if j := strings.Index(rest, ". Read the spec"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestNotifyPlan_ChangedFilesIsTheGitChangeSet is MUX-150's Phase 1
// characterization, flipped: "Changed files" was the workflow entry's
// LastFiles, and on 2026-09-08 the third verify-spec named plan's own spec
// write and none of the eight dirty Go files. With no verification on record
// the list is every dirty non-docs path — all eight, uncapped — and never the
// spec, whatever LastFiles holds.
func TestNotifyPlan_ChangedFilesIsTheGitChangeSet(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	repo := seedRepoSpec(t, session)
	specRel := "docs/requirements/drafts/test-spec.md"

	code := []string{
		"cmd/launcher.go", "cmd/launcher_test.go", "bus/intent.go", "bus/intent_test.go",
		"bus/launcher.go", "tui/graph.go", "tui/graph_ui.go", "tui/model.go",
	}
	for _, f := range code {
		if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(f)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, f), []byte("package x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "base")
	for _, f := range code {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("package x\n\n// changed\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	bus.TransitionWorkflow(session, bus.StateEditing, "hook:analyze:edit",
		bus.WithFiles([]string{filepath.Join(repo, specRel)}))

	d := New(session, 5, 2)
	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "EXIT=0 LGTM", "req-1"))
	d.checkInboxes()

	contents := planVerifyContents(t, session)
	if len(contents) != 1 {
		t.Fatalf("expected 1 verify-spec, got %d", len(contents))
	}
	got := verifyChangedFiles(t, contents[0])
	if strings.Contains(got, specRel) || strings.Contains(got, "docs/") {
		t.Errorf("changed files name a docs path: %q", got)
	}
	for _, f := range code {
		if !strings.Contains(got, f) {
			t.Errorf("changed files %q omit dirty code file %s", got, f)
		}
	}
}

// writeRepoFile writes content to rel under repo, creating its directory.
func writeRepoFile(t *testing.T, repo, rel, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(rel)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, rel), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// Each verification names only what moved since the previous one: a file that
// changed after it, and a path whose content no longer matches what it saw —
// but not a file whose same content was merely committed.
func TestNotifyPlan_ChangedFilesSinceLastVerification(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	repo := seedRepoSpec(t, session)
	for _, f := range []string{"a.go", "b.go", "c.go", "d.go"} {
		writeRepoFile(t, repo, f, "package x\n")
	}
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "base")
	d := New(session, 5, 2)
	review := func(id string) string {
		t.Helper()
		drainPlanInbox(t, session)
		seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "EXIT=0 LGTM", id))
		d.checkInboxes()
		contents := planVerifyContents(t, session)
		if len(contents) != 1 {
			t.Fatalf("%s: expected 1 verify-spec, got %d", id, len(contents))
		}
		return verifyChangedFiles(t, contents[0])
	}

	for _, f := range []string{"a.go", "b.go", "c.go"} {
		writeRepoFile(t, repo, f, "package x\n\n// "+f+"\n")
	}
	if got := review("req-1"); got != "a.go, b.go, c.go" {
		t.Errorf("first verification = %q, want every dirty code file", got)
	}

	writeRepoFile(t, repo, "d.go", "package x\n\n// d\n")
	if got := review("req-2"); got != "d.go" {
		t.Errorf("second verification = %q, want only d.go — the rest were already verified", got)
	}

	gitIn(t, repo, "add", "a.go")
	gitIn(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "a")
	gitIn(t, repo, "checkout", "--", "b.go")
	if got := review("req-3"); got != "b.go" {
		t.Errorf("third verification = %q, want only the reverted b.go — committing a.go's verified content is no change", got)
	}

	writeRepoFile(t, repo, "a.go", "package x\n\n// a, edited after it was verified clean\n")
	gitIn(t, repo, "add", "a.go", "c.go")
	gitIn(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "a and c")
	if got := review("req-4"); got != "a.go" {
		t.Errorf("fourth verification = %q, want a.go — clean at the last verification, committed since; c.go's commit of verified content is no change", got)
	}

	writeRepoFile(t, repo, "pkg/x.go", "package pkg\n")
	writeRepoFile(t, repo, "pkg/y.go", "package pkg\n")
	if got := review("req-5"); got != "pkg/x.go, pkg/y.go" {
		t.Errorf("fifth verification = %q, want each file of the new untracked directory, never pkg/", got)
	}
}
