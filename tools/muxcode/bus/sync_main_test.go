package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// syncFixture is a freshness fixture whose un-flagged git calls — the ones
// SyncOntoMain makes — have an identity to commit and stash with.
func syncFixture(t *testing.T) *freshnessFixture {
	t.Helper()
	fx := newFreshnessFixture(t)
	t.Setenv("AGENT_ROLE", "commit")
	t.Setenv("BUS_ROLE", "")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
	return fx
}

func TestSyncOntoMainClean(t *testing.T) {
	fx := syncFixture(t)
	fx.advance(2, "")
	writeFile(t, filepath.Join(fx.work, "feature.txt"), "uncommitted edit")

	r := SyncOntoMain(fx.work)
	if !r.OK || r.Behind != 0 || r.Ahead != 1 || !strings.HasSuffix(r.Report(), "EXIT=0\n") {
		t.Fatalf("clean sync: %+v\n%s", r, r.Report())
	}
	if got, _ := os.ReadFile(filepath.Join(fx.work, "feature.txt")); string(got) != "uncommitted edit" {
		t.Errorf("an unrelated uncommitted change must survive the autostash, got %q", got)
	}
	if stashCount(fx.work) != 0 {
		t.Error("a clean autostash must leave no stash behind")
	}
}

func TestSyncOntoMainAbortsConflictingRebase(t *testing.T) {
	fx := syncFixture(t)
	fx.commit(fx.work, "tools/muxcode/bus/graph_templates.go", "branch version")
	head := fx.git(fx.work, "rev-parse", "HEAD")
	fx.advance(1, "tools/muxcode/bus/graph_templates.go")

	r := SyncOntoMain(fx.work)
	if r.OK || !r.Aborted || !strings.HasSuffix(r.Report(), "EXIT=1\n") {
		t.Fatalf("a conflicting rebase must abort and report EXIT=1: %+v", r)
	}
	if len(r.Conflicts) != 1 || r.Conflicts[0] != "tools/muxcode/bus/graph_templates.go" {
		t.Errorf("conflicts = %v", r.Conflicts)
	}
	if rebaseInProgress(fx.work) || fx.git(fx.work, "rev-parse", "HEAD") != head {
		t.Error("an aborted sync must leave no rebase in progress and HEAD where it was")
	}
}

// The rebase replays cleanly, then restoring the uncommitted change
// conflicts: nothing is left to abort, so the result names the stash that
// still holds the change instead of reporting success.
func TestSyncOntoMainKeepsConflictingAutostash(t *testing.T) {
	fx := syncFixture(t)
	path := "student_data_query/database/query_builder.py"
	writeFile(t, filepath.Join(fx.work, path), "local uncommitted edit")
	fx.advance(1, path)

	r := SyncOntoMain(fx.work)
	if r.OK || r.Aborted || r.StashKept != "stash@{0}" || !strings.HasSuffix(r.Report(), "EXIT=1\n") {
		t.Fatalf("an autostash conflict must report the kept stash with EXIT=1: %+v\n%s", r, r.Report())
	}
	if !strings.Contains(fx.git(fx.work, "stash", "show", "-p", "stash@{0}"), "local uncommitted edit") {
		t.Error("the kept stash must still hold the uncommitted change")
	}
	if rebaseInProgress(fx.work) {
		t.Error("no rebase may be left in progress")
	}
}

// mainSyncFixture is a sync fixture checked out on main, as
// 55-defect-backlog-loop works.
func mainSyncFixture(t *testing.T) *freshnessFixture {
	t.Helper()
	fx := syncFixture(t)
	fx.git(fx.work, "checkout", "-q", "main")
	return fx
}

// On main the sync fast-forwards to origin/main and carries the run's
// uncommitted work — a modified tracked file and an untracked spec move —
// through untouched, with no stash made.
func TestSyncOntoMainFastForwardsMainCarryingUncommittedWork(t *testing.T) {
	fx := mainSyncFixture(t)
	edit := "student_data_query/database/query_builder.py"
	moved := "docs/requirements/drafts/MUX-001-x.md"
	writeFile(t, filepath.Join(fx.work, edit), "the run's phase 1")
	writeFile(t, filepath.Join(fx.work, moved), "activated spec")
	fx.advance(2, "")

	r := SyncOntoMain(fx.work)
	if !r.OK || r.Behind != 0 || r.Ahead != 0 || !strings.HasSuffix(r.Report(), "EXIT=0\n") {
		t.Fatalf("main with unrelated uncommitted work must fast-forward: %+v\n%s", r, r.Report())
	}
	if fx.git(fx.work, "rev-parse", "HEAD") != fx.git(fx.work, "rev-parse", FreshnessBase) {
		t.Error("main must now be origin/main")
	}
	for path, want := range map[string]string{edit: "the run's phase 1", moved: "activated spec"} {
		if got, _ := os.ReadFile(filepath.Join(fx.work, path)); string(got) != want {
			t.Errorf("%s = %q, want the uncommitted %q", path, got, want)
		}
	}
	if stashCount(fx.work) != 0 {
		t.Error("a fast-forward makes no stash")
	}
}

// A collision between the run's uncommitted work and the upstream changes —
// a modified tracked file or an untracked one upstream adds — refuses with
// the files listed and nothing changed: HEAD, the work and the stash list.
func TestSyncOntoMainRefusesCollidingWorkOnMain(t *testing.T) {
	for _, c := range []struct{ name, path string }{
		{"modified tracked file", "student_data_query/database/query_builder.py"},
		{"untracked file upstream adds", "docs/requirements/drafts/MUX-001-x.md"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := mainSyncFixture(t)
			writeFile(t, filepath.Join(fx.work, c.path), "local work")
			head := fx.git(fx.work, "rev-parse", "HEAD")
			fx.advance(1, c.path)

			r := SyncOntoMain(fx.work)
			if r.OK || len(r.Conflicts) != 1 || r.Conflicts[0] != c.path || !strings.HasSuffix(r.Report(), "EXIT=1\n") {
				t.Fatalf("a collision must refuse naming %s with EXIT=1: %+v\n%s", c.path, r, r.Report())
			}
			if fx.git(fx.work, "rev-parse", "HEAD") != head || stashCount(fx.work) != 0 || rebaseInProgress(fx.work) {
				t.Error("a refused sync must leave HEAD, the stash list and the tree's state as they were")
			}
			if got, _ := os.ReadFile(filepath.Join(fx.work, c.path)); string(got) != "local work" {
				t.Errorf("the local work was touched: %q", got)
			}
		})
	}
}

// Commits on main that origin/main lacks cannot ride a fast-forward, and
// main is never rebased: the sync refuses and changes nothing.
func TestSyncOntoMainRefusesLocalCommitsOnMain(t *testing.T) {
	fx := mainSyncFixture(t)
	fx.commit(fx.work, "local.txt", "a commit only main has")
	head := fx.git(fx.work, "rev-parse", "HEAD")
	fx.advance(1, "")

	r := SyncOntoMain(fx.work)
	if r.OK || !strings.Contains(r.Detail, "cannot carry") {
		t.Fatalf("local commits on main must refuse the sync: %+v", r)
	}
	if fx.git(fx.work, "rev-parse", "HEAD") != head {
		t.Error("main must not move")
	}
}

// stubAncestry makes BusActorVerified see this process under parent name.
func stubAncestry(t *testing.T, parent string) {
	t.Helper()
	orig := psListRunner
	psListRunner = func() (string, error) {
		return fmt.Sprintf("%d 4242 muxcode\n4242 1 %s\n", os.Getpid(), parent), nil
	}
	t.Cleanup(func() { psListRunner = orig })
}

// Only the commit agent and a person may sync: a refused caller changes
// nothing — no fetch, no rebase, no stash.
func TestSyncOntoMainRefusesUnauthorizedCallers(t *testing.T) {
	fx := syncFixture(t)
	writeFile(t, filepath.Join(fx.work, "feature.txt"), "uncommitted edit")
	fx.advance(1, "")
	state := func() string {
		return fx.git(fx.work, "rev-parse", "HEAD", FreshnessBase) + "\n" + fx.git(fx.work, "status", "--porcelain") +
			"\n" + fx.git(fx.work, "stash", "list")
	}
	before := state()

	for _, role := range []string{"edit", "plan", "build", "auto"} {
		t.Setenv("AGENT_ROLE", role)
		if r := SyncOntoMain(fx.work); r.OK || !strings.Contains(r.Detail, role+" may not run muxcode sync-main") {
			t.Errorf("%s must be refused: %+v", role, r)
		}
		if after := state(); after != before {
			t.Errorf("a refused %s sync changed the repo:\nbefore %s\nafter %s", role, before, after)
		}
	}

	t.Setenv("AGENT_ROLE", "")
	stubAncestry(t, "claude")
	if r := SyncOntoMain(fx.work); r.OK || !strings.Contains(r.Detail, "claude may not run") {
		t.Errorf("an agent claiming to be the user by its environment must be refused: %+v", r)
	}
	stubAncestry(t, "zsh")
	if r := SyncOntoMain(fx.work); !r.OK {
		t.Errorf("control: a person at a shell may sync: %+v", r)
	}
}

func TestSyncMainGuardedForEditAndPlan(t *testing.T) {
	for _, role := range []string{"edit", "plan"} {
		if d := CheckGuard(role, "muxcode sync-main"); d == nil || !d.Blocked || !strings.Contains(d.Reason, "commit") {
			t.Errorf("%s: muxcode sync-main must be refused with the commit delegation, got %+v", role, d)
		}
	}
	if d := CheckGuard("edit", "muxcode spec get"); d != nil && d.Blocked {
		t.Errorf("control: other muxcode commands stay open to edit: %+v", d)
	}
}

// A tree mid-rebase is refused by the sync, untouched, and reads as stale.
func TestUnsettledTreeIsRefusedAndStale(t *testing.T) {
	fx := syncFixture(t)
	fx.commit(fx.work, "tools/muxcode/bus/graph_templates.go", "branch version")
	fx.advance(1, "tools/muxcode/bus/graph_templates.go")
	fx.git(fx.work, "fetch", "-q", "origin")
	rebase := exec.Command("git", "rebase", FreshnessBase)
	rebase.Dir = fx.work
	_ = rebase.Run()
	if !rebaseInProgress(fx.work) {
		t.Fatal("fixture: the rebase should have stopped on its conflict")
	}

	if r := SyncOntoMain(fx.work); r.OK || r.Aborted || !strings.Contains(r.Detail, "not settled") {
		t.Errorf("a sync over a rebase in progress must refuse without acting: %+v", r)
	}
	if !rebaseInProgress(fx.work) {
		t.Error("the refused sync must not have touched the rebase in progress")
	}
	f := BranchFreshness(fx.work, "")
	if !f.Stale || !strings.Contains(f.Summary(), "not settled") {
		t.Errorf("a tree mid-rebase must read stale: %s", f.Summary())
	}
}
