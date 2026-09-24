package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubSpecAtHEAD makes HEAD's copy of the active spec read as content.
func stubSpecAtHEAD(t *testing.T, content string) {
	t.Helper()
	prev := specAtHEADFn
	specAtHEADFn = func(string, string) (string, error) { return content, nil }
	t.Cleanup(func() { specAtHEADFn = prev })
}

// MUX-183 defect 2: complete means at least one box and none open. A stub
// phase and a narrative "Phase N findings" heading are empty, not done;
// boxes under a #### subheading belong to the enclosing phase.
func TestSpecPhaseCompleteness(t *testing.T) {
	phases := parseSpecPhases("### Phase 1: Done\n- [x] a\n- [X] b\n" +
		"### Phase 1 findings — recorded\nprose only\n" +
		"### Phase 2: Nested\n#### Steps\n- [ ] hidden open\n- [x] seen done\n" +
		"### Phase 3: Stub\n" +
		"## Open decisions\n- [ ] not a phase item\n")
	want := map[string]bool{"Phase 1: Done": true, "Phase 1 findings — recorded": false, "Phase 2: Nested": false, "Phase 3: Stub": false}
	if len(phases) != len(want) {
		t.Fatalf("phases = %+v", phases)
	}
	for _, p := range phases {
		if p.Complete() != want[p.Title] {
			t.Errorf("%q complete = %v, want %v (%+v)", p.Title, p.Complete(), want[p.Title], p)
		}
	}
	if n := phases[2]; len(n.Items) != 1 || n.Done != 1 {
		t.Errorf("boxes under a #### subheading must count for the phase, got %+v", n)
	}
	if completedPhaseCount(phases) != 1 {
		t.Errorf("only the all-ticked phase counts complete, got %d", completedPhaseCount(phases))
	}
}

func TestNewlyCompletedPhases(t *testing.T) {
	head := parseSpecPhases("### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n### Phase 3: C\n- [ ] c\n")
	tree := parseSpecPhases("### Phase 3: C\n- [x] c\n### Phase 1: A\n- [x] a\n### Phase 2: B\n- [x] b\n")
	got := newlyCompletedPhases(tree, head)
	if len(got) != 2 || got[0].Number != 2 || got[1].Number != 3 {
		t.Errorf("newly complete = %+v, want Phases 2 and 3 in order, never the shipped Phase 1", got)
	}
	if got := newlyCompletedPhases(head, head); len(got) != 0 {
		t.Errorf("an unchanged spec has nothing to ship, got %+v", got)
	}
}

// The MUX-183 regression, and the MUX-182 Phase 3 gate of 2026-09-24: a
// fresh run on a spec whose Phase 1 is committed must not open the commit
// gate for it, nor name it. Negative control: completing Phase 2 in the tree
// opens the gate and names Phase 2.
func TestPhaseCommitReadyAnchorsOnHEAD(t *testing.T) {
	g := &Graph{Name: "g", Start: "commit",
		Nodes: []Node{{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "commit", Guard: GuardPhaseProgress}}}
	run := createTestRun(t, g)
	ctx := &ChainContext{Session: runTestSession, GraphRun: run, Graph: g}
	cond := map[string]any{"spec_phase_committable": "commit"}
	committed := "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n"

	writeSpecFixture(t, committed)
	stubSpecAtHEAD(t, committed)
	if ok, res := EvaluateConditions(cond, ctx); ok || !strings.Contains(res[0].Detail, "1 phases complete in the tree, 1 at HEAD") {
		t.Errorf("a phase committed at HEAD must not be re-credited, got %+v", res)
	}
	if got := resolveCompletedPhaseText(runTestSession); strings.Contains(got, "Phase 1") {
		t.Errorf("the gate must not name the shipped Phase 1, got %q", got)
	}

	writeSpecFixture(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [x] b\n")
	if ok, res := EvaluateConditions(cond, ctx); !ok {
		t.Errorf("Phase 2 complete in the tree and open at HEAD must pass, got %+v", res)
	}
	if got := resolveCompletedPhaseText(runTestSession); got != "Phase 2: B" {
		t.Errorf("${completed_phase} = %q, want the phase being shipped", got)
	}

	run.EdgeFires = map[string]int{}
	if ok, _ := EvaluateConditions(cond, ctx); !ok {
		t.Error("a retry resetting the run's fires must not change the answer — it reads no run state")
	}
}

// gitRepo makes an isolated scratch repo and returns it with a git runner.
func gitRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	return repo, git
}

// The default seam reads the committed copy through git: nothing when HEAD
// is unborn or holds no such file, the committed text otherwise — not the
// working tree's.
func TestSpecAtHEADReadsCommittedCopy(t *testing.T) {
	repo, git := gitRepo(t)
	spec := filepath.Join(repo, "docs", "spec.md")
	writeFile(t, spec, "committed\n")

	if got, err := specAtHEAD(repo, spec); err != nil || got != "" {
		t.Errorf("unborn HEAD: got %q, %v — want empty", got, err)
	}
	git("add", "docs/spec.md")
	git("commit", "-q", "-m", "spec")
	writeFile(t, spec, "edited in the tree\n")
	if got, err := specAtHEAD(repo, spec); err != nil || got != "committed\n" {
		t.Errorf("committed copy = %q, %v — want HEAD's text, not the tree's", got, err)
	}
	if got, err := specAtHEAD(repo, filepath.Join(repo, "docs", "new.md")); err != nil || got != "" {
		t.Errorf("a file absent at HEAD must read empty, got %q, %v", got, err)
	}
}

// A committed spec moved to another directory keeps its baseline: the
// shipped Phase 1 is not re-credited under the new path, and the gate
// opens once Phase 2 completes.
func TestSpecAtHEADFollowsAMovedSpec(t *testing.T) {
	createTestRun(t, &Graph{Name: "g", Start: "commit",
		Nodes: []Node{{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "commit", Guard: GuardPhaseProgress}}})
	repo, git := gitRepo(t)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	old := filepath.Join(repo, "docs", "backlog", "MUX-9-x.md")
	moved := filepath.Join(repo, "docs", "drafts", "MUX-9-x.md")
	spec := "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n"
	writeFile(t, old, spec)
	git("add", "docs")
	git("commit", "-q", "-m", "spec")
	writeFile(t, moved, spec)
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, moved); err != nil {
		t.Fatal(err)
	}

	if v := phaseCommitReady(runTestSession); v.readErr != nil || v.ready {
		t.Errorf("a moved spec must keep its committed Phase 1, got %+v", v)
	}
	writeFile(t, moved, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [x] b\n")
	if v := phaseCommitReady(runTestSession); !v.ready || v.phase.Number != 2 {
		t.Errorf("Phase 2 completing under the new path must be committable, got %+v", v)
	}
}

// Anything short of a verified absence is an error, which holds the gate:
// an unreadable baseline must never read as an empty one.
func TestSpecAtHEADFailsClosed(t *testing.T) {
	repo, git := gitRepo(t)
	writeFile(t, filepath.Join(repo, "a", "MUX-9-x.md"), "one\n")
	writeFile(t, filepath.Join(repo, "b", "MUX-9-x.md"), "two\n")
	git("add", ".")
	git("commit", "-q", "-m", "two specs sharing a name")
	if _, err := specAtHEAD(repo, filepath.Join(repo, "c", "MUX-9-x.md")); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("an ambiguous file name must be an error, got %v", err)
	}

	notRepo := t.TempDir()
	if _, err := specAtHEAD(notRepo, filepath.Join(notRepo, "spec.md")); err == nil {
		t.Error("a directory that is not a repo must be an error, not an empty baseline")
	}

	t.Setenv("PATH", "")
	if _, err := specAtHEAD(repo, filepath.Join(repo, "a", "MUX-9-x.md")); err == nil {
		t.Error("git missing from PATH must be an error, not an empty baseline")
	}
}

func TestReviewFindingsOutcome(t *testing.T) {
	for _, c := range []struct {
		payload, want string
		ok            bool
	}{
		{"Review completed: 2 must-fix, 1 should-fix, 0 nits — changes required. EXIT=0", OutcomeFailure, true},
		{"Review: 0 must-fix, 1 should-fix, 0 nits — provenance changes look sound. EXIT=0", OutcomeFailure, true},
		{"Review: 0 must-fix, 0 should-fix, 0 nits — LGTM. EXIT=0", OutcomeSuccess, true},
		{"Review: 0 must-fix, 0 should-fix, 3 nits — LGTM", OutcomeSuccess, true},
		{"Review completed; changes required: 1 must-fix, 1 should-fix, 1 nit.", OutcomeFailure, true},
		{"reviewed, looks fine. EXIT=0", "", false},
		{"0 must-fix found so far; review incomplete", "", false},
		{"Review: 0 must-fix, 0 nits — should-fix count missing", "", false},
		{"Review: 99999 must-fix, 0 should-fix, 0 nits", "", false},
		{"3 must-fix, 1 should-fix, 0 nits — changes required", OutcomeFailure, true},
		{"\n  Review: 0 must-fix, 0 should-fix, 0 nits — LGTM", OutcomeSuccess, true},
		{"Previous review said:\n> 0 must-fix, 0 should-fix, 0 nits\nCurrent review is incomplete", "", false},
		{"> 0 must-fix, 0 should-fix, 0 nits\nReview: 2 must-fix, 0 should-fix, 0 nits", "", false},
		{"```\n0 must-fix, 0 should-fix, 0 nits\n```", "", false},
		{"Reviewed the diff; see below.\nReview: 0 must-fix, 0 should-fix, 0 nits", "", false},
		{"Start your reply with the findings count line: <n> must-fix, <n> should-fix, <n> nits.", "", false},
	} {
		got, ok := reviewFindingsOutcome(c.payload)
		if got != c.want || ok != c.ok {
			t.Errorf("%q = %q, %v — want %q, %v", c.payload, got, ok, c.want, c.ok)
		}
	}
}

// End to end: findings route the review to its fix node even though the
// reviewer's token says EXIT=0; a clean review proceeds; a reply with no
// counts line routes neither way.
func TestExecReviewFindingsRouteToFix(t *testing.T) {
	graph := func() *Graph {
		return &Graph{Name: "g", Start: "review",
			Nodes: []Node{
				{ID: "review", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
				{ID: "next", Type: NodeSend, Role: "plan", Action: "verify-spec", Message: "verify"},
				{ID: "fix", Type: NodeSend, Role: "build", Action: "build", Message: "fix"},
			},
			Edges: []Edge{
				{From: "review", To: "next"},
				{From: "review", To: "fix", Outcome: OutcomeFailure},
			}}
	}
	for _, c := range []struct {
		reply, running, pending string
	}{
		{"Review completed: 1 must-fix, 1 should-fix, 0 nits — changes required. EXIT=0", "fix", "next"},
		{"Review: 0 must-fix, 1 should-fix, 0 nits. EXIT=0", "fix", "next"},
		{"Review: 0 must-fix, 0 should-fix, 2 nits — LGTM. EXIT=0", "next", "fix"},
	} {
		run := createTestRun(t, graph())
		step(t, runTestSession, run.ID)
		if s := nodeState(t, runTestSession, run.ID, "review"); s != GraphNodeRunning {
			t.Fatalf("review node %q, want running", s)
		}
		completeSendNodeSentinel(t, runTestSession, run.ID, "review", c.reply)
		step(t, runTestSession, run.ID)
		step(t, runTestSession, run.ID)
		if s := nodeState(t, runTestSession, run.ID, c.running); s != GraphNodeRunning {
			t.Errorf("%q: %s = %q, want running", c.reply, c.running, s)
		}
		if s := nodeState(t, runTestSession, run.ID, c.pending); s != GraphNodePending {
			t.Errorf("%q: %s = %q, want pending", c.reply, c.pending, s)
		}
	}

	run := createTestRun(t, graph())
	step(t, runTestSession, run.ID)
	completeSendNodeSentinel(t, runTestSession, run.ID, "review", "looked at it. EXIT=0")
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	for _, id := range []string{"next", "fix"} {
		if s := nodeState(t, runTestSession, run.ID, id); s != GraphNodePending {
			t.Errorf("an uncounted review must route nowhere, %s = %q", id, s)
		}
	}
}

// A fix node entered from several failure edges is told about the latest
// failure, not a stale one from an earlier lap.
func TestExpandFailureReport(t *testing.T) {
	g := &Graph{Name: "g", Start: "build",
		Nodes: []Node{
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "b"},
			{ID: "review", Type: NodeSend, Role: "review", Action: "review", Message: "r"},
			{ID: "fix", Type: NodeSpawn, Role: "edit", Message: "FIX: ${failure_report}"},
		},
		Edges: []Edge{
			{From: "build", To: "review"},
			{From: "build", To: "fix", Outcome: OutcomeFailure},
			{From: "review", To: "fix", Outcome: OutcomeFailure},
			{From: "fix", To: "build", MaxIterations: 3},
		}}
	run := createTestRun(t, g)
	fix := g.node("fix")
	set := func(id, outcome, output string, doneAt int64) {
		if err := MutateNodeStatus(runTestSession, run.ID, id, func(s *GraphNodeStatus) {
			s.Outcome, s.Output, s.DoneAt = outcome, output, doneAt
		}); err != nil {
			t.Fatal(err)
		}
	}

	if got := expandFailureReport(runTestSession, run, g, fix); !strings.Contains(got, "no failing upstream node") {
		t.Errorf("nothing failed yet: got %q", got)
	}
	set("build", OutcomeFailure, "old build break", 100)
	set("review", OutcomeFailure, "Review: 1 must-fix, 0 should-fix — see /tmp/r.txt", 200)
	if got := expandFailureReport(runTestSession, run, g, fix); got != "FIX: review reported: Review: 1 must-fix, 0 should-fix — see /tmp/r.txt" {
		t.Errorf("latest failure must win, got %q", got)
	}
	set("build", OutcomeFailure, "new build break", 300)
	set("review", OutcomeSuccess, "Review: 0 must-fix", 250)
	if got := expandFailureReport(runTestSession, run, g, fix); !strings.Contains(got, "build reported: new build break") {
		t.Errorf("a succeeded review is not a failure to fix, got %q", got)
	}
}
