package bus

import (
	"os"
	"path/filepath"
	"testing"
)

// specPointerFixture creates a repo dir holding a drafts/ spec, points the
// active-spec marker at it (repo-relative), and returns the repo dir and
// relative spec path.
func specPointerFixture(t *testing.T, session string) (string, string) {
	t.Helper()
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	rel := "docs/requirements/drafts/fixture-spec.md"
	if err := os.MkdirAll(filepath.Join(repo, "docs/requirements/drafts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, rel), []byte("# Fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(session, rel); err != nil {
		t.Fatal(err)
	}
	return repo, rel
}

func TestReconcileActiveSpec_OK(t *testing.T) {
	useTempBusDir(t)
	session := "spec-ptr-ok"
	repo, rel := specPointerFixture(t, session)

	res := ReconcileActiveSpec(session, repo)
	if res.Outcome != SpecPointerOK {
		t.Fatalf("outcome = %s, want ok", res.Outcome)
	}
	if got := ReadActiveSpec(session); got != rel {
		t.Errorf("pointer moved on an ok reconcile: %q", got)
	}
}

func TestReconcileActiveSpec_FollowsCloseOutMove(t *testing.T) {
	useTempBusDir(t)
	session := "spec-ptr-move"
	repo, rel := specPointerFixture(t, session)

	moved := "docs/requirements/completed/fixture-spec.md"
	if err := os.MkdirAll(filepath.Join(repo, "docs/requirements/completed"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repo, rel), filepath.Join(repo, moved)); err != nil {
		t.Fatal(err)
	}

	res := ReconcileActiveSpec(session, repo)
	if res.Outcome != SpecPointerRepointed {
		t.Fatalf("outcome = %s, want repointed", res.Outcome)
	}
	if res.NewPath != moved {
		t.Errorf("NewPath = %q, want %q", res.NewPath, moved)
	}
	if got := ReadActiveSpec(session); got != moved {
		t.Errorf("pointer = %q, want repointed to %q", got, moved)
	}
}

func TestReconcileActiveSpec_DanglingIsReportedNotCleared(t *testing.T) {
	useTempBusDir(t)
	session := "spec-ptr-dangling"
	repo, rel := specPointerFixture(t, session)
	if err := os.Remove(filepath.Join(repo, rel)); err != nil {
		t.Fatal(err)
	}

	res := ReconcileActiveSpec(session, repo)
	if res.Outcome != SpecPointerDangling {
		t.Fatalf("outcome = %s, want dangling", res.Outcome)
	}
	// The pointer is the only record of which spec was active — reporting
	// must not destroy the evidence.
	if got := ReadActiveSpec(session); got != rel {
		t.Errorf("dangling reconcile cleared the pointer: %q", got)
	}
}

func TestReconcileActiveSpec_UnresolvableAndUnset(t *testing.T) {
	useTempBusDir(t)
	session := "spec-ptr-edge"

	if res := ReconcileActiveSpec(session, t.TempDir()); res.Outcome != SpecPointerUnset {
		t.Errorf("no pointer: outcome = %s, want unset", res.Outcome)
	}

	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(session, "docs/requirements/drafts/x.md"); err != nil {
		t.Fatal(err)
	}
	if res := ReconcileActiveSpec(session, ""); res.Outcome != SpecPointerUnresolvable {
		t.Errorf("relative pointer without repo dir: outcome = %s, want unresolvable", res.Outcome)
	}
}
