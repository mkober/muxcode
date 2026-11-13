package bus

import (
	"os"
	"path/filepath"
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
	want := []string{"10-story-to-spec", "20-defect-to-spec", "30-build-test-review", "40-sync-main", "50-spec-to-pr", "55-defect-backlog-loop",
		"60-integration-suite", "70-pr-local-review", "80-pr-review-fix", "90-ci-fix", "100-docs-sync", "110-pr-merge", "120-deploy-verify"}
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
// commit and the issue both sit behind the one gate. The issue has exactly
// one creation point: plan's pre-gate draft is told to leave it to the gated
// node, or plan's standing defect-issue rule files it twice.
func TestDefectToSpecTemplate(t *testing.T) {
	g := mustTemplate(t, "20-defect-to-spec")
	if g.Start != "evidence" || !strings.Contains(g.node("evidence").Message, "WITHOUT changing anything") {
		t.Error("the run must start by gathering evidence read-only")
	}
	if !strings.Contains(g.node("draft").Message, "${output:evidence}") {
		t.Error("the draft must be grounded in the evidence node's report")
	}
	if !strings.Contains(g.node("draft").Message, "Do NOT search for or create its GitHub issue") {
		t.Error("the pre-gate draft must leave issue creation to the gated issue node")
	}
	issue := g.node("issue")
	if issue.Role != "plan" || issue.Action != "issue-write" {
		t.Errorf("issue node = %s/%s, want plan/issue-write", issue.Role, issue.Action)
	}
	onlyReachedFrom(t, g, "issue", "gate", OutcomeSuccess)
	onlyReachedFrom(t, g, "commit-spec", "issue", OutcomeSuccess)
}

// 50-spec-to-pr starts by checking the branch: on the spec's branch it goes
// straight to implement; off it, a human approves creating the branch before
// anything is implemented or committed, so phase commits never land on main.
func TestSpecToPRStartsOnSpecBranch(t *testing.T) {
	g := mustTemplate(t, "50-spec-to-pr")
	if g.Start != "branch-check" || g.node("branch-check").Conditions["spec_branch"] != true {
		t.Fatalf("start = %q, want the spec_branch check", g.Start)
	}
	onlyReachedFrom(t, g, "branch-gate", "branch-check", OutcomeFailure)
	onlyReachedFrom(t, g, "create-branch", "branch-gate", OutcomeSuccess)
	cb := g.node("create-branch")
	if cb.Role != "commit" || cb.Action != "checkout" || !NodeRequiresGate(cb) {
		t.Errorf("create-branch = %s/%s (gated %v), want a gated commit/checkout", cb.Role, cb.Action, NodeRequiresGate(cb))
	}
	for _, from := range []string{"branch-check", "create-branch"} {
		found := false
		for _, e := range g.Edges {
			if e.From == from && e.To == "implement" && edgeOutcome(e) == OutcomeSuccess {
				found = true
			}
		}
		if !found {
			t.Errorf("%s must reach implement on success", from)
		}
	}
}

// No builtin request carries a verdict of its own (MUX-198): parseExitSentinel
// reads the last EXIT=<digits> in a reply, so a request echoed back as its
// reply (MUX-154) would otherwise route on an outcome nobody reported. Checked
// as dispatched, with the verdict instruction a send node is seeded with.
func TestBuiltinRequestsCarryNoVerdict(t *testing.T) {
	for name, raw := range builtinGraphJSON {
		g, err := ParseGraph([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, n := range g.Nodes {
			msg := seedVerdictToken(n.Action, n.Message)
			if _, found := parseExitSentinel(msg); found {
				t.Errorf("%s: node %s request carries a parseable verdict: %q", name, n.ID, msg)
			}
		}
	}
}

// Plan owns GitHub issues: no builtin template may hand a gh issue command to
// another role, and every issue write sits behind a gate (the validator's
// gatedTrackerWriteActions rule, exercised here by mustTemplate).
func TestTemplatesRouteGitHubIssuesToPlan(t *testing.T) {
	for name, raw := range builtinGraphJSON {
		g, err := ParseGraph([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, n := range g.Nodes {
			if strings.Contains(n.Message, "gh issue") && NormalizeBusRole(n.Role) != "plan" {
				t.Errorf("%s: node %s sends a gh issue command to %q, want plan", name, n.ID, n.Role)
			}
		}
	}
	story := mustTemplate(t, "10-story-to-spec")
	if n := story.node("fetch"); n.Action != "issue-read" {
		t.Errorf("10-story-to-spec fetch action = %q, want issue-read", n.Action)
	}
	if n := story.node("issue-update"); n.Action != "issue-write" || !NodeRequiresGate(n) {
		t.Errorf("10-story-to-spec issue-update = %q (gated %v), want a gated issue-write", n.Action, NodeRequiresGate(n))
	}
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
	if fix := g.node("fix").Message; !strings.Contains(fix, "${output:suite}") || !strings.Contains(fix, "${failure_report}") {
		t.Error("the fix worker must get the suite's report and any rebuild failure")
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

// Nothing merges before the review is clear, CI is green and a human approves;
// the tracker moves only after the merge. Open comments end at a hold that
// lists them and routes nowhere (MUX-187).
func TestPRMergeTemplate(t *testing.T) {
	g := mustTemplate(t, "110-pr-merge")
	onlyReachedFrom(t, g, "read-comments", "pr-exists", OutcomeSuccess)
	onlyReachedFrom(t, g, "ci-watch", "no-comments", OutcomeSuccess)
	onlyReachedFrom(t, g, "open-comments", "no-comments", OutcomeFailure)
	for _, e := range g.Edges {
		if e.From == "open-comments" {
			t.Errorf("open-comments must end the run, not route to %s", e.To)
		}
	}
	if hold := g.node("open-comments"); hold == nil || !strings.Contains(hold.Message, "${output:read-comments}") {
		t.Error("the open-comments hold must list the comments the read found")
	}
	if gate := g.node("merge-gate").Message; !strings.Contains(gate, "CI is green") || !strings.Contains(gate, "no unresolved review comments") {
		t.Errorf("merge-gate must state both checks it was reached on, got %q", gate)
	}
	onlyReachedFrom(t, g, "merge-gate", "ci-green", OutcomeSuccess)
	onlyReachedFrom(t, g, "recheck-comments", "merge-gate", OutcomeSuccess)
	onlyReachedFrom(t, g, "merge", "still-clear", OutcomeSuccess)
	onlyReachedFrom(t, g, "new-comments", "still-clear", OutcomeFailure)
	for _, e := range g.Edges {
		if e.From == "new-comments" {
			t.Errorf("new-comments must end the run, not route to %s", e.To)
		}
	}
	if hold := g.node("new-comments"); hold == nil || !strings.Contains(hold.Message, "${output:recheck-comments}") {
		t.Error("the new-comments hold must list the comments the recheck found")
	}
	if r := g.node("recheck-comments"); r == nil || r.Message != g.node("read-comments").Message {
		t.Error("the pre-merge recheck must be the same review read as read-comments")
	}
	onlyReachedFrom(t, g, "tracker", "merge", OutcomeSuccess)
	if w := g.node("ci-watch"); w == nil || NormalizeBusRole(w.Role) != "watch" {
		t.Error("waiting on CI is a blocking watch — it belongs to the watch role, never an agent's own pane")
	}
}

// The defect loop runs dry into a terminal success, ships each defect only
// through its gate and a green, comment-free PR, and gives every defect the
// full budget of every inner loop. Its spec changes each pass, so no node may
// name the run's launch-time text. A phase's completion is read from the spec
// file: on 2026-11-13 plan answered verify-spec in its own format without the
// PHASE-COMPLETE token the loop then branched on, and a finished Phase 1
// parked at a stuck gate.
func TestDefectBacklogLoopTemplate(t *testing.T) {
	g := mustTemplate(t, "55-defect-backlog-loop")

	onlyReachedFrom(t, g, "finish", "activated", OutcomeFailure)
	for _, e := range g.Edges {
		if e.From == "finish" {
			t.Errorf("finish must end the run — a success with no edges completes it — not route to %s", e.To)
		}
	}

	for _, e := range g.Edges {
		if e.From == "update-spec" && e.To != "phases-left" {
			t.Errorf("update-spec routes to %s — a phase's completion is read from the spec, never from the reply", e.To)
		}
	}
	if c := g.node("phases-left"); c == nil || c.Conditions["spec_phases_remaining"] != true {
		t.Error("phases-left must branch on the spec file (spec_phases_remaining)")
	}

	onlyReachedFrom(t, g, "criteria", "close-spec", OutcomeFailure)
	onlyReachedFrom(t, g, "ship-gate", "close-spec", OutcomeSuccess)
	onlyReachedFrom(t, g, "merge", "no-comments", OutcomeSuccess)
	for _, e := range g.Edges {
		if e.To == "ship" && !(e.From == "ship-gate" || e.From == "ship-stuck-gate") {
			t.Errorf("ship reachable from %s — only its gates may release it", e.From)
		}
	}

	var outer *Edge
	for i := range g.Edges {
		if g.Edges[i].From == "merge" && g.Edges[i].To == "select" {
			outer = &g.Edges[i]
		}
	}
	if outer == nil || !outer.MaxIterationsFromBacklog {
		t.Fatal("merge->select must loop to the next defect, capped at the backlog's defect count")
	}
	for _, e := range g.Edges {
		if e.From == "phases-left" && e.To == "implement" && !e.MaxIterationsFromActiveSpec {
			t.Error("the phase loop must be capped by the active spec when it fires — each defect's spec differs, so any fixed number truncates a longer one")
		}
	}
	resets := map[string]bool{}
	for _, r := range outer.ResetsIterations {
		resets[r] = true
	}
	for _, e := range g.Edges {
		if e.Capped() && e.From+"->"+e.To != "merge->select" && !resets[e.From+"->"+e.To] {
			t.Errorf("merge->select does not reset %s->%s — the next defect would start on this one's spent budget", e.From, e.To)
		}
	}

	for _, n := range g.Nodes {
		for _, frozen := range []string{"${spec}", "${spec_title}", "${intent}"} {
			if strings.Contains(n.Message, frozen) {
				t.Errorf("node %s names %s, the run's launch text — the loop's spec changes every pass", n.ID, frozen)
			}
		}
	}

	if w, m := g.node("ci-watch"), mustTemplate(t, "110-pr-merge").node("ci-watch"); w == nil || m == nil || w.Message != m.Message || w.Role != m.Role {
		t.Error("the loop's CI watch must be 110-pr-merge's — one definition of green")
	}
}

// Driven through the executor with the real template: a selection that runs
// dry completes the run, one that broke fails it instead of reading as
// finished, and an activated defect starts the implement worker without
// reaching finish.
func TestDefectBacklogLoopEndsOnlyWhenTheBacklogRunsDry(t *testing.T) {
	for _, c := range []struct {
		name, selectReply, finishReply string
		wantRun                        string
		wantImplement                  bool
	}{
		{"backlog worked through", "Every remaining row is parked. NO-DEFECTS-REMAINING\nEXIT=0", "Confirmed.\nEXIT=0", GraphRunComplete, false},
		{"selection broke", "backlog.md could not be read\nEXIT=0", "The selection broke.\nEXIT=1", GraphRunFailed, false},
		{"defect activated", "DEFECT-ACTIVATED MUX-001 x docs/requirements/drafts/MUX-001-x.md\nEXIT=0", "", GraphRunRunning, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := t.TempDir()
			t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
			backlog := filepath.Join(repo, BacklogIndexPath)
			if err := os.MkdirAll(filepath.Dir(backlog), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(backlog, []byte("### Defects — prioritized\n\n| # | ID |\n|---|----|\n| 1 | MUX-001 |\n"), 0644); err != nil {
				t.Fatal(err)
			}
			g, err := ParseGraph([]byte(builtinGraphJSON["55-defect-backlog-loop"]))
			if err != nil {
				t.Fatal(err)
			}
			run := createTestRun(t, g)
			fakeLiveSpawns(t)
			settle := func() {
				for i := 0; i < 4; i++ {
					step(t, runTestSession, run.ID)
				}
			}

			settle()
			completeSendNodeSentinel(t, runTestSession, run.ID, "preflight", "Branch main, tree clean. ON-MAIN\nEXIT=0")
			settle()
			completeSendNodeSentinel(t, runTestSession, run.ID, "select", c.selectReply)
			settle()
			if c.finishReply != "" {
				completeSendNodeSentinel(t, runTestSession, run.ID, "finish", c.finishReply)
				settle()
			}

			if impl := nodeState(t, runTestSession, run.ID, "implement"); (impl != GraphNodePending) != c.wantImplement {
				t.Errorf("implement %q, want started=%v", impl, c.wantImplement)
			}
			if c.wantImplement {
				if f := nodeState(t, runTestSession, run.ID, "finish"); f != GraphNodePending {
					t.Errorf("finish %q — an activated defect must not reach the loop's end", f)
				}
			}
			if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != c.wantRun {
				t.Errorf("run %q, want %q", r.State, c.wantRun)
			}
		})
	}
}

// Driven through the executor with the real template: whatever plan's spec
// check says — a reply without a completion token, a failure verdict, no
// verdict at all — the loop reads the next phase from the spec and returns to
// implement, never parking at a gate or failing the run. Run 1794591418
// (2026-11-13) stopped at a stuck gate after every finished phase because
// plan's reply decided the routing.
func TestDefectBacklogLoopPhaseRoutingIgnoresTheVerdict(t *testing.T) {
	for _, c := range []struct{ name, reply string }{
		{"no completion token", "Phase 1 verified, 1/1. Phase 2 NOT complete: 0/1\nEXIT=0"},
		{"failure verdict", "Phase 2 NOT complete: 0/1\nEXIT=1"},
		{"no verdict", "Phase 1 verified, 1/1 checked."},
	} {
		t.Run(c.name, func(t *testing.T) {
			g, run := driveDefectLoopToSpecCheck(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n", c.reply)
			assertDefectLoopFired(t, g, run, "phases-left", "implement", OutcomeSuccess)
		})
	}
}

// With every phase done but an acceptance criterion open, the refused
// close-out sends a worker after the open items rather than parking at a
// gate: run 1794598296 (2026-11-13) parked on close-stuck-gate with three
// open criteria no phase step covered.
func TestDefectBacklogLoopRefusedCloseOutGoesToAWorker(t *testing.T) {
	spec := "### Phase 1: A\n- [x] a\n\n## Acceptance criteria\n\n- [ ] every script passes\n"
	g, run := driveDefectLoopToSpecCheck(t, spec, "Phase 1 verified, 1/1.\nEXIT=0")
	assertDefectLoopFired(t, g, run, "close-spec", "criteria", OutcomeFailure)
	if st := nodeState(t, runTestSession, run.ID, "criteria"); st == GraphNodePending {
		t.Errorf("criteria worker %q — the open acceptance item must be worked", st)
	}
}

// closeOutFixture is 50-spec-to-pr from close-spec on: the template's own
// close-out nodes and edges, with close-spec and the criteria worker as plain
// sends — close-spec unguarded — so a test's reply alone decides each.
func closeOutFixture(t *testing.T) *Graph {
	t.Helper()
	tmpl := mustTemplate(t, "50-spec-to-pr")
	g := &Graph{Name: "close-out", Start: "close-spec", Nodes: []Node{
		{ID: "close-spec", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "close out"},
		{ID: "criteria", Type: NodeSend, Role: "edit", Action: "criteria", Message: "work the open items"},
	}}
	for _, id := range []string{"close-build", "close-test", "close-review", "close-verify", "final-gate"} {
		n := tmpl.node(id)
		if n == nil {
			t.Fatalf("50-spec-to-pr has no %s node", id)
		}
		g.Nodes = append(g.Nodes, *n)
	}
	for _, e := range tmpl.Edges {
		if g.node(e.From) != nil && g.node(e.To) != nil {
			g.Edges = append(g.Edges, e)
		}
	}
	return g
}

// answerCloseOut completes a running close-out node: build and test by
// history row, the rest by the verdict in their reply.
func answerCloseOut(t *testing.T, runID, nodeID, reply string) {
	t.Helper()
	switch nodeID {
	case "close-build", "close-test":
		completeSendNode(t, runTestSession, runID, nodeID, OutcomeSuccess)
	default:
		completeSendNodeSentinel(t, runTestSession, runID, nodeID, reply)
	}
	step(t, runTestSession, runID)
	step(t, runTestSession, runID)
}

func workRefusedCloseOut(t *testing.T, runID string) {
	t.Helper()
	answerCloseOut(t, runID, "close-spec", "REFUSED: 1 acceptance criterion open. EXIT=1")
	if s := nodeState(t, runTestSession, runID, "criteria"); s != GraphNodeRunning {
		t.Fatalf("criteria %q after a refused close-out, want running", s)
	}
	answerCloseOut(t, runID, "criteria", "Ran scripts/test-x.sh: 12 passed. EXIT=0")
	answerCloseOut(t, runID, "close-build", "")
	answerCloseOut(t, runID, "close-test", "")
	answerCloseOut(t, runID, "close-review", reviewClean)
	answerCloseOut(t, runID, "close-verify", "Ticked 1 criterion. EXIT=0")
}

// 50-spec-to-pr carried the close-stuck-gate that parked run 1794598296
// (2026-11-13) twice: a refused close-out is now worked, rebuilt, retested,
// reviewed and re-verified, and a retry that succeeds reaches final-gate with
// no human asked on the way.
func TestSpecToPRRefusedCloseOutIsWorkedNotGated(t *testing.T) {
	run := createTestRun(t, closeOutFixture(t))
	step(t, runTestSession, run.ID)

	workRefusedCloseOut(t, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "final-gate"); s != GraphNodePending {
		t.Fatalf("final-gate %q before the retried close-out answered, want pending", s)
	}
	answerCloseOut(t, run.ID, "close-spec", "Closed out. EXIT=0")
	if s := nodeState(t, runTestSession, run.ID, "final-gate"); s != GraphNodeWaiting {
		t.Errorf("final-gate %q after a successful retry, want waiting", s)
	}
}

// Past the cap the run fails rather than parking or looping: two refusals are
// worked, a third has no live edge.
func TestSpecToPRCloseOutWorkerIsCapped(t *testing.T) {
	run := createTestRun(t, closeOutFixture(t))
	step(t, runTestSession, run.ID)

	workRefusedCloseOut(t, run.ID)
	workRefusedCloseOut(t, run.ID)
	answerCloseOut(t, run.ID, "close-spec", "REFUSED: still open. EXIT=1")
	if !runFailed(t, run.ID) {
		t.Errorf("run not failed after a third refusal; criteria state %q", nodeState(t, runTestSession, run.ID, "criteria"))
	}
	if s := nodeState(t, runTestSession, run.ID, "final-gate"); s == GraphNodeWaiting {
		t.Error("a refused close-out past the cap must never reach final-gate")
	}
}

// driveDefectLoopToSpecCheck runs the real 55-defect-backlog-loop through one
// pass of a defect whose active spec is specBody — preflight, select, a fake
// implement worker, build, test, review — and answers the spec check with
// reply, so callers can assert where the loop goes next.
func driveDefectLoopToSpecCheck(t *testing.T, specBody, reply string) (*Graph, *GraphRun) {
	t.Helper()
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	backlog := filepath.Join(repo, BacklogIndexPath)
	if err := os.MkdirAll(filepath.Dir(backlog), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlog, []byte("### Defects — prioritized\n\n| # | ID |\n|---|----|\n| 1 | MUX-001 |\n"), 0644); err != nil {
		t.Fatal(err)
	}
	g, err := ParseGraph([]byte(builtinGraphJSON["55-defect-backlog-loop"]))
	if err != nil {
		t.Fatal(err)
	}
	run := createTestRun(t, g)
	spec := filepath.Join(repo, "MUX-001-x.md")
	if err := os.WriteFile(spec, []byte(specBody), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, spec); err != nil {
		t.Fatal(err)
	}
	fakeSpawns(t, runTestSession)
	settle := func() {
		for i := 0; i < 6; i++ {
			step(t, runTestSession, run.ID)
		}
	}

	settle()
	completeSendNodeSentinel(t, runTestSession, run.ID, "preflight", "Branch main. ON-MAIN\nEXIT=0")
	settle()
	completeSendNodeSentinel(t, runTestSession, run.ID, "select", "DEFECT-ACTIVATED MUX-001 x "+spec+"\nEXIT=0")
	settle()
	completeSendNode(t, runTestSession, run.ID, "build", OutcomeSuccess)
	settle()
	completeSendNode(t, runTestSession, run.ID, "test", OutcomeSuccess)
	settle()
	completeSendNodeWithReply(t, runTestSession, run.ID, "review", "review", "0 must-fix, 0 should-fix, 0 nits. Approved.")
	settle()
	completeSendNodeWithReply(t, runTestSession, run.ID, "update-spec", "plan", reply)
	settle()
	return g, run
}

// assertDefectLoopFired asserts the run is still going, fired from->to once
// on outcome, and has no gate waiting.
func assertDefectLoopFired(t *testing.T, g *Graph, run *GraphRun, from, to, outcome string) {
	t.Helper()
	r, _ := ReadGraphRun(runTestSession, run.ID)
	if r.State != GraphRunRunning {
		t.Fatalf("run %q, want running", r.State)
	}
	if fires := r.EdgeFires[EdgeFireKey(Edge{From: from, To: to, Outcome: outcome})]; fires != 1 {
		var where []string
		for _, id := range []string{"implement", "build", "test", "review", "update-spec", "phases-left", "close-spec"} {
			where = append(where, id+"="+nodeState(t, runTestSession, run.ID, id))
		}
		t.Errorf("%s->%s fired %d times, want 1; nodes %v, fires %v", from, to, fires, where, r.EdgeFires)
	}
	for _, n := range g.Nodes {
		if n.Type == NodeWaitHuman && nodeState(t, runTestSession, run.ID, n.ID) == GraphNodeWaiting {
			t.Errorf("gate %s is waiting — the loop must not ask a human here", n.ID)
		}
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
			if c.fixStarted {
				st, _ := ReadNodeStatus(runTestSession, run.ID, "fix")
				e, ok := findSpawnByRole(runTestSession, st.TaskID)
				if !ok || !strings.Contains(e.Task, "SUITE-FAILED: test-x (check y)") {
					t.Errorf("the fix worker must be told which checks failed — the condition hop drops ${failure_report}; got task %q", e.Task)
				}
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
		{"55-defect-backlog-loop", "ci-watch"},
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
		if n == nil || !strings.Contains(n.Message, "exits zero EITHER WAY") || !strings.Contains(n.Message, "NO-PR-FOUND") {
			t.Errorf("%s find-pr must declare a zero exit either way and name NO-PR-FOUND", name)
		}
	}
}
