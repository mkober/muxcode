package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// specBranchRepo builds a session repo whose active spec is specName, with one
// commit on branch main, and returns a switcher onto a new branch.
func specBranchRepo(t *testing.T, specName string) func(branch string) {
	t.Helper()
	createTestRun(t, &Graph{Name: "g", Start: "a",
		Nodes: []Node{{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"}}})
	repo, git := gitRepo(t)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	spec := filepath.Join(repo, "docs", "requirements", "drafts", specName)
	writeFile(t, spec, "### Phase 1: A\n- [ ] a\n")
	git("checkout", "-q", "-b", "main")
	git("add", "docs")
	git("commit", "-q", "-m", "spec")
	if err := WriteActiveSpec(runTestSession, spec); err != nil {
		t.Fatal(err)
	}
	return func(branch string) { git("checkout", "-q", "-b", branch) }
}

func specBranchPasses(t *testing.T, value any) bool {
	t.Helper()
	r := evalSpecBranch(value, &ChainContext{Session: runTestSession})
	return r.Passed
}

// The 50-spec-to-pr branch check: on main it routes to the branch gate, on
// the spec's <id>-<slug> branch it passes, and a branch whose id merely shares
// a prefix (MUX-1780 against MUX-178) does not count as the spec's.
func TestSpecBranchCondition(t *testing.T) {
	switchTo := specBranchRepo(t, "MUX-178-spawn-node.md")

	if specBranchPasses(t, true) {
		t.Error("main is not the spec's branch: spec_branch true must fail")
	}
	if !specBranchPasses(t, false) {
		t.Error("negative control: spec_branch false must pass on main")
	}

	switchTo("MUX-1780-other")
	if specBranchPasses(t, true) {
		t.Error("MUX-1780-other only shares a prefix with MUX-178 and must not count")
	}

	switchTo("MUX-178-spawn-node")
	if !specBranchPasses(t, true) {
		t.Error("MUX-178-spawn-node is the spec's branch: spec_branch true must pass")
	}
	if specBranchPasses(t, false) {
		t.Error("negative control: spec_branch false must fail on the spec's branch")
	}
}

// A spec filename with no id falls back to the whole filename as the prefix.
func TestSpecBranchConditionWithoutID(t *testing.T) {
	switchTo := specBranchRepo(t, "notes.md")
	switchTo("notes-draft")
	if !specBranchPasses(t, true) {
		t.Error("an id-less spec's branch starts with its filename: notes-draft must pass")
	}
}

// Every state that cannot prove the branch fails, so a run asks a human
// rather than committing to whatever is checked out.
func TestSpecBranchConditionFailsClosed(t *testing.T) {
	switchTo := specBranchRepo(t, "MUX-178-spawn-node.md")
	switchTo("MUX-178-spawn-node")

	if specBranchPasses(t, "yes") {
		t.Error("a non-boolean value must fail")
	}
	if err := ClearActiveSpec(runTestSession); err != nil {
		t.Fatal(err)
	}
	if specBranchPasses(t, true) || specBranchPasses(t, false) {
		t.Error("with no active spec, spec_branch must fail either way")
	}
	if !IsKnownCondition("spec_branch") {
		t.Error("spec_branch must be a known condition, or graph validation rejects the template")
	}
}

// An unknown branch is neither on nor off the spec's branch: detached HEAD
// and a failed git lookup fail both values, while main (a known branch off
// the spec) still passes false.
func TestSpecBranchConditionUnknownBranch(t *testing.T) {
	specBranchRepo(t, "MUX-178-spawn-node.md")
	if !specBranchPasses(t, false) {
		t.Fatal("negative control: spec_branch false must pass on main")
	}

	repo := os.Getenv("MUXCODE_SESSION_REPO_DIR")
	if out, err := exec.Command("git", "-C", repo, "checkout", "-q", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("detach: %v\n%s", err, out)
	}
	if specBranchPasses(t, true) || specBranchPasses(t, false) {
		t.Error("detached HEAD: spec_branch must fail either way")
	}

	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(repo))
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	if specBranchPasses(t, true) || specBranchPasses(t, false) {
		t.Error("failed git lookup: spec_branch must fail either way")
	}
}
