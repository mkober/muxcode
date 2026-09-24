package bus

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// Builtins are numbered in tens, so a plain string sort would put
// 100-docs-sync ahead of 20-defect-to-spec. Listings order by the stage
// number; a name without one sorts after every staged name.
func TestGraphTemplateLessOrdersByStage(t *testing.T) {
	names := []string{"zeta", "100-docs-sync", "20-defect-to-spec", "alpha", "120-deploy-verify", "10-story-to-spec", "9x-odd"}
	sort.Slice(names, func(i, j int) bool { return graphTemplateLess(names[i], names[j]) })
	want := []string{"10-story-to-spec", "20-defect-to-spec", "100-docs-sync", "120-deploy-verify", "9x-odd", "alpha", "zeta"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", names, want)
	}
}

// The builtin listing reads top-down as the dev workflow.
func TestBuiltinTemplatesListInWorkflowOrder(t *testing.T) {
	var names []string
	for name := range builtinGraphJSON {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return graphTemplateLess(names[i], names[j]) })
	want := []string{"10-story-to-spec", "20-defect-to-spec", "30-build-test-review", "40-sync-main", "50-spec-to-pr", "60-integration-suite",
		"70-pr-local-review", "80-pr-review-fix", "90-ci-fix", "100-docs-sync", "110-pr-merge", "120-deploy-verify"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("builtins = %v, want %v", names, want)
	}
}

func mustTemplate(t *testing.T, name string) *Graph {
	t.Helper()
	g, _, err := ResolveGraphTemplate(name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if v := g.Validate(); !v.OK() || len(v.Warnings) > 0 {
		t.Fatalf("%s must validate without warnings: %v %v", name, v.Errors, v.Warnings)
	}
	return g
}

// onlyReachedFrom asserts every edge into node comes from `from` on outcome.
func onlyReachedFrom(t *testing.T, g *Graph, node, from, outcome string) {
	t.Helper()
	found := false
	for _, e := range g.Edges {
		if e.To != node {
			continue
		}
		if e.From != from || edgeOutcome(e) != outcome {
			t.Errorf("%s: %s reachable from %s (%s) — only %s -[%s]-> may reach it", g.Name, node, e.From, edgeOutcome(e), from, outcome)
		}
		found = true
	}
	if !found {
		t.Errorf("%s: nothing reaches %s", g.Name, node)
	}
}

// Evidence is gathered read-only before anything is written, and the spec
// commit and the issue both sit behind the one gate.
func TestDefectToSpecTemplate(t *testing.T) {
	g := mustTemplate(t, "20-defect-to-spec")
	if g.Start != "evidence" || !strings.Contains(g.node("evidence").Message, "WITHOUT changing anything") {
		t.Error("the run must start by gathering evidence read-only")
	}
	if !strings.Contains(g.node("draft").Message, "${output:evidence}") {
		t.Error("the draft must be grounded in the evidence node's report")
	}
	onlyReachedFrom(t, g, "commit-spec", "gate", OutcomeSuccess)
	onlyReachedFrom(t, g, "issue", "commit-spec", OutcomeSuccess)
}

// Nothing is rebased or pushed without the gate, and the push waits on a
// green build and test of the rebased tree.
func TestSyncMainTemplate(t *testing.T) {
	g := mustTemplate(t, "40-sync-main")
	if g.Start != "gate" || g.node("gate").Type != NodeWaitHuman {
		t.Error("the rebase must be gated before anything runs")
	}
	if !strings.Contains(g.node("push").Message, "--force-with-lease") {
		t.Error("a rebased branch must be pushed with --force-with-lease, never a bare --force")
	}
	if !strings.Contains(g.node("rebase").Message, "rebase --abort") {
		t.Error("a conflict must abort the rebase, not leave it half-applied")
	}
	onlyReachedFrom(t, g, "push", "test", OutcomeSuccess)
}

// A failing suite loops through a capped fix and rebuild back into the suite.
func TestIntegrationSuiteTemplate(t *testing.T) {
	g := mustTemplate(t, "60-integration-suite")
	if !strings.Contains(g.node("suite").Message, "scripts/test-all.sh") {
		t.Error("the suite node must run the serial runner")
	}
	if !templateEdgeOn(g, "suite", "suite-failed", OutcomeFailure) || !templateEdgeOn(g, "build", "suite", OutcomeSuccess) {
		t.Error("a failing suite must loop suite-failed -> fix -> build -> suite")
	}
	if templateEdge(g, "suite", "fix") {
		t.Error("the suite must not reach fix directly — only a reported failure (SUITE-FAILED) may")
	}
	capped := false
	for _, e := range g.Edges {
		if e.From == "fix" && e.To == "build" && e.MaxIterations > 0 {
			capped = true
		}
	}
	if !capped {
		t.Error("the fix loop must be capped")
	}
	if !strings.Contains(g.node("fix").Message, "${failure_report}") {
		t.Error("the fix worker must be told what failed")
	}
}

// Green CI ends the run; failing CI goes through the gate into the fix loop,
// and only a clean review reaches the push.
func TestCIFixTemplate(t *testing.T) {
	g := mustTemplate(t, "90-ci-fix")
	for _, e := range g.Edges {
		if e.From == "ci-green" && edgeOutcome(e) == OutcomeSuccess {
			t.Errorf("green CI must end the run, not route to %s", e.To)
		}
	}
	onlyReachedFrom(t, g, "fix-gate", "ci-green", OutcomeFailure)
	onlyReachedFrom(t, g, "push-fixes", "review", OutcomeSuccess)
	if !templateEdgeOn(g, "review", "fix", OutcomeFailure) {
		t.Error("review findings must loop back to fix")
	}
	fix := g.node("fix")
	if !strings.Contains(fix.Message, "${output:read-ci}") || !strings.Contains(fix.Message, "${failure_report}") {
		t.Error("the fix worker must get the CI read and the failure report")
	}
}

// Nothing merges before CI is green and a human approves; the tracker moves
// only after the merge.
func TestPRMergeTemplate(t *testing.T) {
	g := mustTemplate(t, "110-pr-merge")
	onlyReachedFrom(t, g, "merge-gate", "ci-green", OutcomeSuccess)
	onlyReachedFrom(t, g, "merge", "merge-gate", OutcomeSuccess)
	onlyReachedFrom(t, g, "tracker", "merge", OutcomeSuccess)
	if w := g.node("ci-watch"); w == nil || NormalizeBusRole(w.Role) != "watch" {
		t.Error("waiting on CI is a blocking watch — it belongs to the watch role, never an agent's own pane")
	}
}

// A suite that outlives its budget may still be running, so the timeout must
// stop the run rather than start a fix worker beside it; a suite that reports
// its failures (SUITE-FAILED) is what releases the fix. Driven through the
// executor with the real template.
func TestIntegrationSuiteTimeoutNeverReleasesFix(t *testing.T) {
	for _, c := range []struct {
		name        string
		timeout     bool
		fixStarted  bool
		wantRunDone string
	}{
		{"timed out mid-run", true, false, GraphRunFailed},
		{"reported failing checks", false, true, GraphRunRunning},
	} {
		t.Run(c.name, func(t *testing.T) {
			g, err := ParseGraph([]byte(builtinGraphJSON["60-integration-suite"]))
			if err != nil {
				t.Fatal(err)
			}
			run := createTestRun(t, g)
			fakeLiveSpawns(t)
			step(t, runTestSession, run.ID)
			if s := nodeState(t, runTestSession, run.ID, "suite"); s != GraphNodeRunning {
				t.Fatalf("suite %q, want running", s)
			}
			if c.timeout {
				past := time.Now().Unix() - int64(nodeTimeoutSecs(g.node("suite"))) - 60
				if err := MutateNodeStatus(runTestSession, run.ID, "suite", func(s *GraphNodeStatus) { s.StartedAt = past }); err != nil {
					t.Fatal(err)
				}
			} else {
				completeSendNodeSentinel(t, runTestSession, run.ID, "suite", "2 scripts ran. SUITE-FAILED: test-x (check y)\nEXIT=1")
			}
			for i := 0; i < 3; i++ {
				step(t, runTestSession, run.ID)
			}

			fix := nodeState(t, runTestSession, run.ID, "fix")
			if started := fix != GraphNodePending && fix != GraphNodeSkipped; started != c.fixStarted {
				t.Errorf("fix state %q, want started=%v", fix, c.fixStarted)
			}
			if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != c.wantRunDone {
				t.Errorf("run %q, want %q", r.State, c.wantRunDone)
			}
		})
	}
}

// A node that waits on long work gets a budget beyond the 600s default: an
// expired task fails the node, so the full serial suite would route to its fix
// worker while still running, and a CI wait would fail a green run.
func TestLongRunningNodesOutlastTheDefaultBudget(t *testing.T) {
	for _, c := range []struct{ tpl, node string }{
		{"60-integration-suite", "suite"},
		{"110-pr-merge", "ci-watch"},
	} {
		n := mustTemplate(t, c.tpl).node(c.node)
		if got := nodeTimeoutSecs(n); got < 3600 {
			t.Errorf("%s %s budget = %ds, want at least an hour — the default 600s expires mid-run", c.tpl, c.node, got)
		}
	}
	if got := nodeTimeoutSecs(&Node{}); got != 600 {
		t.Errorf("default budget = %d, want 600 (control: the override is what raises it)", got)
	}
}

// Every PR-lookup node declares the EXIT convention and names the token its
// condition branches on (see TestPRReviewFixQuestionNodesDeclareExitConvention).
func TestWorkflowQuestionNodesDeclareExitConvention(t *testing.T) {
	for _, name := range []string{"90-ci-fix", "110-pr-merge"} {
		n := mustTemplate(t, name).node("find-pr")
		if n == nil || !strings.Contains(n.Message, "EXIT=0 EITHER WAY") || !strings.Contains(n.Message, "NO-PR-FOUND") {
			t.Errorf("%s find-pr must declare EXIT=0 either way and name NO-PR-FOUND", name)
		}
	}
}
