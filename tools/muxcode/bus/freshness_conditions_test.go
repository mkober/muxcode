package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var refreshConditions = map[string]any{
	CondBranchBehind:             map[string]any{},
	CondUpstreamTouchesSpecFiles: false,
}

func ctxWith(f Freshness) *ChainContext {
	return &ChainContext{Session: runTestSession, freshness: &f}
}

func TestBranchBehindCondition(t *testing.T) {
	ctx := ctxWith(Freshness{Behind: 4, MaxBehind: 5})
	if r := evalBranchBehind(map[string]any{}, ctx); !r.Passed {
		t.Errorf("4 behind, gate at the configured 5: must pass: %s", r.Detail)
	}
	if r := evalBranchBehind(map[string]any{"max": float64(4)}, ctx); r.Passed {
		t.Errorf("4 behind with max 4: the count that gates must fail: %s", r.Detail)
	}
	if r := evalBranchBehind(true, ctxWith(Freshness{Behind: 5, MaxBehind: 5})); r.Passed {
		t.Error("5 behind with the configured 5 must fail")
	}
	if r := evalBranchBehind(map[string]any{"max": float64(0)}, ctx); r.Passed || !strings.Contains(r.Detail, "N ≥ 1") {
		t.Errorf("max 0 is no threshold and must fail naming the shape: %q", r.Detail)
	}
	for _, typo := range []map[string]any{{"maximum": float64(5)}, {"max": float64(5), "min": float64(1)}} {
		if r := evalBranchBehind(typo, ctx); r.Passed || !strings.Contains(r.Detail, "N ≥ 1") {
			t.Errorf("%v must be refused, not read as the default threshold: %q", typo, r.Detail)
		}
	}
}

func TestUpstreamTouchesSpecFilesCondition(t *testing.T) {
	clean := ctxWith(Freshness{MaxBehind: 5})
	touched := ctxWith(Freshness{MaxBehind: 5, Overlap: []UpstreamChange{{Status: "D", Path: "a/b.py"}}})
	if !evalUpstreamTouchesSpecFiles(false, clean).Passed || evalUpstreamTouchesSpecFiles(true, clean).Passed {
		t.Error("no overlap: false passes, true fails")
	}
	r := evalUpstreamTouchesSpecFiles(false, touched)
	if r.Passed || !strings.Contains(r.Detail, "a/b.py (deleted)") {
		t.Errorf("overlap must fail false and name the file: %q", r.Detail)
	}
	if !evalUpstreamTouchesSpecFiles(true, touched).Passed {
		t.Error("negative control: overlap passes true")
	}
}

// Unknown is never fresh: an unmeasured branch fails both conditions for
// every value, so a refresh node routes it to its sync gate.
func TestFreshnessConditionsFailClosedWhenUnmeasured(t *testing.T) {
	ctx := ctxWith(unmeasuredFreshness("fetch failed: no route"))
	for _, v := range []any{map[string]any{}, map[string]any{"max": float64(100)}} {
		if r := evalBranchBehind(v, ctx); r.Passed || !strings.Contains(r.Detail, "unmeasured") {
			t.Errorf("branch_behind %v on an unmeasured branch must fail: %q", v, r.Detail)
		}
	}
	for _, v := range []bool{true, false} {
		if evalUpstreamTouchesSpecFiles(v, ctx).Passed {
			t.Errorf("upstream_touches_spec_files %v on an unmeasured branch must fail", v)
		}
	}
}

func TestValidateConditionsChecksFreshnessShapes(t *testing.T) {
	if w := ValidateConditions(refreshConditions); len(w) != 0 {
		t.Errorf("the refresh node's conditions must validate: %v", w)
	}
	bad := map[string]any{CondBranchBehind: map[string]any{"maximum": float64(5)}, CondUpstreamTouchesSpecFiles: "no"}
	if w := ValidateConditions(bad); len(w) != 2 {
		t.Errorf("a misspelled max and a non-boolean must both warn, got %v", w)
	}
}

// One evaluation measures once, writes one branch-freshness row naming the
// run, and hands the summary to the node as its output.
func TestRefreshConditionsMeasureOnceAndLog(t *testing.T) {
	fx := newFreshnessFixture(t)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", fx.work)
	if err := ClearActiveSpec(runTestSession); err != nil {
		t.Fatal(err)
	}
	rows := func(run string) int { return len(cancelEvents(t, branchFreshnessLifecycleEvent, run)) }

	ctx := &ChainContext{Session: runTestSession, GraphRun: &GraphRun{ID: "fresh-run-166"}}
	if ok, res := EvaluateConditions(refreshConditions, ctx); !ok {
		t.Fatalf("a fresh branch must pass refresh: %+v", res)
	}
	if n := rows("fresh-run-166"); n != 1 {
		t.Errorf("one evaluation of two freshness conditions wrote %d rows, want 1", n)
	}
	if out := ctx.freshnessOutput(); !strings.Contains(out, "0 behind") {
		t.Errorf("the node output must carry the numbers: %q", out)
	}

	fx.advance(5, "")
	stale := &ChainContext{Session: runTestSession, GraphRun: &GraphRun{ID: "stale-run-166"}}
	if ok, _ := EvaluateConditions(refreshConditions, stale); ok {
		t.Error("5 behind must fail refresh")
	}
	if n := rows("stale-run-166"); n != 1 || !strings.Contains(stale.freshnessOutput(), "5 behind") {
		t.Errorf("stale evaluation: %d rows, output %q", n, stale.freshnessOutput())
	}
	if (&ChainContext{}).freshnessOutput() != "" {
		t.Error("negative control: a condition that measured nothing records no output")
	}
}

func freshnessGraph(edges ...Edge) *Graph {
	return &Graph{Name: "fresh", Start: "check", RequiresSpec: true,
		Nodes: []Node{
			{ID: "check", Type: NodeCondition, Conditions: map[string]any{"spec_branch": true}},
			{ID: "refresh", Type: NodeCondition, Conditions: refreshConditions},
			{ID: "sync-gate", Type: NodeWaitHuman, Message: "approve the rebase"},
			{ID: "implement", Type: NodeSpawn, Role: "edit", Message: "implement"},
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "build"},
			{ID: "fix", Type: NodeSpawn, Role: "edit", Message: "fix"},
			{ID: "loop", Type: NodeCondition, Conditions: map[string]any{"spec_phases_remaining": true}},
		},
		Edges: edges}
}

func freshnessErrors(g *Graph) []string {
	v := &GraphValidation{}
	g.validateFreshness(v)
	return v.Errors
}

func TestValidateFreshnessRule(t *testing.T) {
	lap := []Edge{
		{From: "implement", To: "build"},
		{From: "build", To: "fix", Outcome: OutcomeFailure},
		{From: "fix", To: "build", MaxIterations: 3},
		{From: "build", To: "loop"},
		{From: "refresh", To: "sync-gate", Outcome: OutcomeFailure},
		{From: "refresh", To: "implement"},
	}
	good := freshnessGraph(append(lap,
		Edge{From: "check", To: "refresh"},
		Edge{From: "check", To: "sync-gate", Outcome: OutcomeFailure},
		Edge{From: "loop", To: "refresh", MaxIterations: 5})...)
	if errs := freshnessErrors(good); len(errs) != 0 {
		t.Errorf("implement behind refresh on both roads, fix only behind a failure: %v", errs)
	}

	fromStart := freshnessGraph(append(lap,
		Edge{From: "check", To: "implement"},
		Edge{From: "loop", To: "refresh", MaxIterations: 5})...)
	if errs := freshnessErrors(fromStart); len(errs) != 1 || !strings.Contains(errs[0], `"implement"`) {
		t.Errorf("start reaching implement directly must be named: %v", errs)
	}

	reentry := freshnessGraph(append(lap,
		Edge{From: "check", To: "refresh"},
		Edge{From: "loop", To: "implement", MaxIterations: 5})...)
	if errs := freshnessErrors(reentry); len(errs) != 1 || !strings.Contains(errs[0], `from "implement"`) {
		t.Errorf("a loop re-entering implement directly must be named: %v", errs)
	}

	reentry.RequiresSpec = false
	if errs := freshnessErrors(reentry); len(errs) != 0 {
		t.Errorf("negative control: a user template without requires_spec is not held to the rule: %v", errs)
	}
	reentry.Name = "80-pr-review-fix"
	if errs := freshnessErrors(reentry); len(errs) != 1 {
		t.Errorf("a template carrying a builtin's name is held to the rule without requires_spec: %v", errs)
	}

	behindFailure := freshnessGraph(
		Edge{From: "check", To: "build"},
		Edge{From: "build", To: "fix", Outcome: OutcomeFailure},
		Edge{From: "fix", To: "build", MaxIterations: 3})
	if errs := freshnessErrors(behindFailure); len(errs) != 1 || !strings.Contains(errs[0], `spawn node "fix"`) {
		t.Errorf("a worker released by a failing send (60's suite -> fix) must be named: %v", errs)
	}
	checkedFailure := freshnessGraph(
		Edge{From: "check", To: "build"},
		Edge{From: "build", To: "refresh", Outcome: OutcomeFailure},
		Edge{From: "refresh", To: "fix"},
		Edge{From: "refresh", To: "sync-gate", Outcome: OutcomeFailure},
		Edge{From: "fix", To: "build", MaxIterations: 3})
	if errs := freshnessErrors(checkedFailure); len(errs) != 0 {
		t.Errorf("control: the same worker behind refresh passes, its fix -> build lap no re-entry: %v", errs)
	}

	sendImpl := Node{ID: "implement", Type: NodeSend, Role: "edit", Action: "implement", Message: "implement"}
	bypass := freshnessGraph(append(lap,
		Edge{From: "check", To: "implement"},
		Edge{From: "loop", To: "refresh", MaxIterations: 5})...)
	bypass.Nodes[3] = sendImpl
	if errs := freshnessErrors(bypass); len(errs) != 1 || !strings.Contains(errs[0], `send node "implement"`) {
		t.Errorf("an implementation send to edit reached without a check must be named: %v", errs)
	}
	checked := freshnessGraph(append(lap,
		Edge{From: "check", To: "refresh"},
		Edge{From: "loop", To: "refresh", MaxIterations: 5})...)
	checked.Nodes[3] = sendImpl
	if errs := freshnessErrors(checked); len(errs) != 0 {
		t.Errorf("control: the same send behind refresh passes: %v", errs)
	}
}

func TestEveryBuiltinPassesFreshnessRule(t *testing.T) {
	for name, raw := range builtinGraphJSON {
		g, err := ParseGraph([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if errs := freshnessErrors(g); len(errs) != 0 {
			t.Errorf("%s: %v", name, errs)
		}
	}
}

// bypassCheck splices a freshness check out of g: every edge into it goes
// to the work it guarded instead, and its own edges are dropped. Reset lists
// are cleared — they would name the dropped edges, and that structural error
// stops validation before the freshness rule runs.
func bypassCheck(t *testing.T, g *Graph, check string) string {
	t.Helper()
	work := ""
	for _, e := range g.Edges {
		if e.From == check && edgeOutcome(e) == OutcomeSuccess {
			work = e.To
		}
	}
	if work == "" {
		t.Fatalf("%s: no success edge out of %q", g.Name, check)
	}
	var edges []Edge
	for _, e := range g.Edges {
		if e.From == check {
			continue
		}
		if e.To == check {
			e.To = work
		}
		e.ResetsIterations = nil
		edges = append(edges, e)
	}
	g.Edges = edges
	return work
}

// Negative control for the widened rule: each check the widening added is
// load-bearing — removed, validation fails naming the work it guarded.
func TestWidenedBuiltinsFailWithoutTheirCheck(t *testing.T) {
	for _, c := range []struct{ name, check string }{
		{"55-defect-backlog-loop", "refresh"},
		{"55-defect-backlog-loop", "pr-refresh"},
		{"60-integration-suite", "refresh"},
		{"80-pr-review-fix", "refresh"},
		{"90-ci-fix", "refresh"},
	} {
		g, err := ParseGraph([]byte(builtinGraphJSON[c.name]))
		if err != nil {
			t.Fatal(err)
		}
		if !isFreshnessCheck(g.node(c.check)) {
			t.Fatalf("%s: %q is not a freshness check", c.name, c.check)
		}
		work := bypassCheck(t, g, c.check)
		v := g.Validate()
		named := false
		for _, e := range v.Errors {
			named = named || (strings.Contains(e, `"`+work+`"`) && strings.Contains(e, "freshness"))
		}
		if !named {
			t.Errorf("%s without %s: validation must name %q, got %v", c.name, c.check, work, v.Errors)
		}
	}
}

// freshSessionCheckout makes dir (a new one when "") a checkout of main with
// a bare origin it has pushed to, and points the session repo at it: a
// freshness check there measures 0 behind with no overlap.
func freshSessionCheckout(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	if dir == "" {
		dir = t.TempDir()
	}
	origin := filepath.Join(t.TempDir(), "origin.git")
	git := func(args ...string) {
		t.Helper()
		full := append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "--bare", "-b", "main", origin)
	git("-C", dir, "init", "-q", "-b", "main")
	git("-C", dir, "add", "-A")
	git("-C", dir, "commit", "-q", "--allow-empty", "-m", "base")
	git("-C", dir, "remote", "add", "origin", origin)
	git("-C", dir, "push", "-q", "-u", "origin", "main")
	t.Setenv("MUXCODE_SESSION_REPO_DIR", dir)
	return dir
}

// With no active spec — 60, 80 and 90 hold none — the overlap is measured
// against the files the branch itself changes: an upstream change the branch
// does not touch passes refresh, one to a file it has uncommitted work in
// fails it naming the file. Neither a blanket gate nor a vacuous pass.
func TestRefreshWithoutSpecMeasuresBranchFiles(t *testing.T) {
	fx := newFreshnessFixture(t)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", fx.work)
	if err := ClearActiveSpec(runTestSession); err != nil {
		t.Fatal(err)
	}
	path := "student_data_query/database/query_builder.py"
	writeFile(t, filepath.Join(fx.work, path), "uncommitted fix")

	fx.advance(1, "")
	clean := &ChainContext{Session: runTestSession}
	if ok, res := EvaluateConditions(refreshConditions, clean); !ok {
		t.Fatalf("an upstream change the branch does not touch must pass with no spec: %+v", res)
	}
	if out := clean.freshnessOutput(); !strings.Contains(out, "files this branch changes: none") {
		t.Errorf("the output must name what the overlap was measured against: %q", out)
	}

	fx.advance(1, path)
	touched := &ChainContext{Session: runTestSession}
	if ok, _ := EvaluateConditions(refreshConditions, touched); ok {
		t.Fatal("an upstream change to a file the branch has uncommitted work in must fail refresh")
	}
	if len(touched.freshness.Unmeasured) != 0 || !strings.Contains(touched.freshnessOutput(), path) {
		t.Errorf("the failure must be a measured overlap naming %s: %q", path, touched.freshnessOutput())
	}
}
