package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Tests for MUX-007 Phase 3: changed-files provenance (repo-scoping), the
// verify-movement suppression gate, and active-spec pointer reconciliation.

// seedRepoSpec pins MUXCODE_SESSION_REPO_DIR to a scratch repo dir holding
// the active spec file, so the fire path's spec-existence guard and movement
// fingerprint see deterministic state regardless of the invoking
// environment. The dir is not a git repo, so tree evidence is absent unless
// a test adds it.
func seedRepoSpec(t *testing.T, session string) string {
	t.Helper()
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	specRel := "docs/requirements/drafts/test-spec.md"
	if err := os.MkdirAll(filepath.Join(repo, "docs/requirements/drafts"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, specRel), []byte("# Test Spec\n\n- [ ] item\n"), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := bus.WriteActiveSpec(session, specRel); err != nil {
		t.Fatalf("write active spec: %v", err)
	}
	return repo
}

// gitIn runs one git command in dir, skipping the test if git is absent.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeRunFixture hand-writes a graph run store entry so tests control run
// state without building a full graph.
func writeRunFixture(t *testing.T, session, id, state string) {
	t.Helper()
	dir := filepath.Join(bus.GraphRunsDir(session), id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"id":"` + id + `","template":"t","state":"` + state + `","created_at":1,"updated_at":1}`)
	if err := os.WriteFile(filepath.Join(dir, "run.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// planVerifyContents returns the content of every verify-spec request in
// plan's inbox.
func planVerifyContents(t *testing.T, session string) []string {
	t.Helper()
	msgs, err := bus.Peek(session, "plan")
	if err != nil {
		t.Fatalf("peek plan: %v", err)
	}
	var contents []string
	for _, m := range msgs {
		if m.Action == "verify-spec" {
			contents = append(contents, m.Payload)
		}
	}
	return contents
}

func TestNotifyPlan_OutOfRepoPathsNeverPresented(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	repo := seedRepoSpec(t, session)
	d := New(session, 5, 2)

	// The raw changed-files signal carries writes from anywhere on disk:
	// a credentials-shaped config, a spawn-worktree copy of a repo file
	// (census B1), and the repo's own copy of that same relative path.
	outside := t.TempDir()
	credentials := filepath.Join(outside, "config")
	spawnCopy := filepath.Join(outside, "muxcode-spawn", "spawn-abc", "bus", "pane_test.go")
	repoCopy := filepath.Join(repo, "bus", "pane_test.go")
	bus.TransitionWorkflow(session, bus.StateEditing, "test:seed",
		bus.WithFiles([]string{credentials, spawnCopy, repoCopy}))

	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM", "req-1"))
	d.checkInboxes()

	contents := planVerifyContents(t, session)
	if len(contents) != 1 {
		t.Fatalf("expected 1 verify-spec, got %d", len(contents))
	}
	msg := contents[0]
	if strings.Contains(msg, outside) {
		t.Errorf("out-of-repo path presented to the verifier: %s", msg)
	}
	if !strings.Contains(msg, "bus/pane_test.go") {
		t.Errorf("repo's own copy of the file missing — scoping keyed on name, not location: %s", msg)
	}
}

// TestNotifyPlan_MovementGate walks the census shapes in one sequence:
// a fire with evidence records the fingerprint; nothing moved suppresses;
// a docs-only edit (the verifier's own write — item 10) suppresses; a
// run-state transition with a docs-only file change fires (fire 11 — the
// control that kills the filename-shape rule); and a source change that
// also touches the spec fires (the control that kills a "spec was touched"
// rule).
func TestNotifyPlan_MovementGate(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	repo := seedRepoSpec(t, session)
	specFile := filepath.Join(repo, "docs/requirements/drafts/test-spec.md")

	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "base")
	writeRunFixture(t, session, "r1", "running")

	d := New(session, 5, 2)

	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM", "req-1"))
	d.checkInboxes()
	if got := countVerifySpec(t, session); got != 1 {
		t.Fatalf("first fire with movement evidence: got %d verify-spec, want 1", got)
	}
	if bus.ReadVerifyMovementMarker(session) == "" {
		t.Fatal("fire did not record the movement fingerprint")
	}

	// Nothing moved → suppressed.
	drainPlanInbox(t, session)
	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM again", "req-2"))
	d.checkInboxes()
	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("completion with nothing moved fired %d verify-spec, want 0", got)
	}

	// The verifier's own doc edit is not movement (item 10's closed loop).
	if err := os.WriteFile(specFile, []byte("# Test Spec\n\n- [x] item\n"), 0644); err != nil {
		t.Fatal(err)
	}
	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM 3", "req-3"))
	d.checkInboxes()
	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("spec-only edit fired %d verify-spec, want 0", got)
	}

	// Fire 11: only changed file is the spec, but the run state moved.
	writeRunFixture(t, session, "r1", "failed")
	if err := os.WriteFile(specFile, []byte("# Test Spec\n\n- [x] item\n- [ ] more\n"), 0644); err != nil {
		t.Fatal(err)
	}
	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM 4", "req-4"))
	d.checkInboxes()
	if got := countVerifySpec(t, session); got != 1 {
		t.Errorf("run-state transition fired %d verify-spec, want 1 — a tree-only fingerprint suppresses fire 11", got)
	}

	// A genuine completion that changes source AND touches the spec fires.
	drainPlanInbox(t, session)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specFile, []byte("# Test Spec\n\n- [x] item\n- [x] more\n"), 0644); err != nil {
		t.Fatal(err)
	}
	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM 5", "req-5"))
	d.checkInboxes()
	if got := countVerifySpec(t, session); got != 1 {
		t.Errorf("source+spec completion fired %d verify-spec, want 1 — gate keys on 'spec touched', not movement", got)
	}
}

func TestNotifyPlan_DanglingSpecWithholdsFire(t *testing.T) {
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	session := testSession(t)
	repo := seedRepoSpec(t, session)
	if err := os.Remove(filepath.Join(repo, "docs/requirements/drafts/test-spec.md")); err != nil {
		t.Fatal(err)
	}
	d := New(session, 5, 2)

	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "LGTM", "req-1"))
	d.checkInboxes()

	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("verify-spec fired at a missing spec %d time(s)", got)
	}
	// The completion itself is real and consumed — only the verify is
	// withheld.
	if st := bus.ReadWorkflowState(session).State; st != bus.StateReviewed {
		t.Errorf("expected reviewed state, got %s", st)
	}
}

func TestCheckActiveSpec_FollowsCloseOutMove(t *testing.T) {
	session := testSession(t)
	repo := seedRepoSpec(t, session)
	d := New(session, 5, 2)

	moved := "docs/requirements/completed/test-spec.md"
	if err := os.MkdirAll(filepath.Join(repo, "docs/requirements/completed"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(repo, "docs/requirements/drafts/test-spec.md"),
		filepath.Join(repo, moved)); err != nil {
		t.Fatal(err)
	}

	d.checkActiveSpec()

	if got := bus.ReadActiveSpec(session); got != moved {
		t.Errorf("pointer = %q, want repointed to %q", got, moved)
	}
}

func TestCheckActiveSpec_DanglingAlertedOnce(t *testing.T) {
	session := testSession(t)
	repo := seedRepoSpec(t, session)
	if err := os.Remove(filepath.Join(repo, "docs/requirements/drafts/test-spec.md")); err != nil {
		t.Fatal(err)
	}
	d := New(session, 5, 2)

	d.checkActiveSpec()
	d.checkActiveSpec()

	msgs, err := bus.Peek(session, "edit")
	if err != nil {
		t.Fatalf("peek edit: %v", err)
	}
	alerts := 0
	for _, m := range msgs {
		if m.Action == "spec-dangling" {
			alerts++
		}
	}
	if alerts != 1 {
		t.Errorf("expected exactly 1 spec-dangling alert, got %d", alerts)
	}
	// Detected and reported, never silently cleared.
	if got := bus.ReadActiveSpec(session); got == "" {
		t.Error("dangling pointer was cleared instead of reported")
	}
}
