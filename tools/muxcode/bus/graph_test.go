package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linearGraph returns a minimal valid send→send graph for mutation in tests.
func linearGraph() *Graph {
	return &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "b", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
}

func assertErrorContains(t *testing.T, v *GraphValidation, substr string) {
	t.Helper()
	for _, e := range v.Errors {
		if strings.Contains(e, substr) {
			return
		}
	}
	t.Errorf("expected an error containing %q, got errors: %v", substr, v.Errors)
}

func assertWarnContains(t *testing.T, v *GraphValidation, substr string) {
	t.Helper()
	for _, w := range v.Warnings {
		if strings.Contains(w, substr) {
			return
		}
	}
	t.Errorf("expected a warning containing %q, got warnings: %v", substr, v.Warnings)
}

func TestParseGraphRoundTrip(t *testing.T) {
	data := `{
		"name": "demo", "start": "a",
		"nodes": [{"id": "a", "type": "send", "role": "build", "action": "build", "message": "go"}],
		"edges": []
	}`
	g, err := ParseGraph([]byte(data))
	if err != nil {
		t.Fatalf("ParseGraph: %v", err)
	}
	if g.Name != "demo" || g.Start != "a" || len(g.Nodes) != 1 {
		t.Errorf("unexpected parse result: %+v", g)
	}
}

func TestParseGraphInvalidJSON(t *testing.T) {
	if _, err := ParseGraph([]byte("{not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidateLinearGraphOK(t *testing.T) {
	v := linearGraph().Validate()
	if !v.OK() {
		t.Errorf("expected valid, got errors: %v", v.Errors)
	}
}

func TestValidateStructuralErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Graph)
		wantErr string
	}{
		{"no name", func(g *Graph) { g.Name = "" }, "no name"},
		{"no start", func(g *Graph) { g.Start = "" }, "no start"},
		{"undefined start", func(g *Graph) { g.Start = "zz" }, "not defined"},
		{"duplicate node id", func(g *Graph) { g.Nodes = append(g.Nodes, g.Nodes[0]) }, "duplicate node id"},
		{"unknown node type", func(g *Graph) { g.Nodes[1].Type = "bogus" }, "unknown type"},
		{"send missing role", func(g *Graph) { g.Nodes[1].Role = "" }, "requires a role"},
		{"send unknown role", func(g *Graph) { g.Nodes[1].Role = "nosuchrole" }, "unknown role"},
		{"send missing action", func(g *Graph) { g.Nodes[1].Action = "" }, "requires an action"},
		{"send missing message", func(g *Graph) { g.Nodes[1].Message = "" }, "requires a message"},
		{"edge to undefined node", func(g *Graph) { g.Edges = append(g.Edges, Edge{From: "b", To: "zz"}) }, "undefined node"},
		{"duplicate edge", func(g *Graph) { g.Edges = append(g.Edges, Edge{From: "a", To: "b"}) }, "duplicate edge"},
		{"negative max_iterations", func(g *Graph) { g.Edges[0].MaxIterations = -1 }, "negative max_iterations"},
		{"negative timeout", func(g *Graph) { g.Nodes[0].TimeoutSec = -5 }, "negative timeout_secs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := linearGraph()
			tt.mutate(g)
			v := g.Validate()
			if v.OK() {
				t.Fatal("expected validation errors, got none")
			}
			assertErrorContains(t, v, tt.wantErr)
		})
	}
}

func TestValidateNoNodes(t *testing.T) {
	g := &Graph{Name: "t", Start: "a"}
	v := g.Validate()
	assertErrorContains(t, v, "no nodes")
}

func TestValidateNodeTypeFields(t *testing.T) {
	tests := []struct {
		name    string
		node    Node
		wantErr string
	}{
		{"spawn missing message", Node{ID: "b", Type: NodeSpawn, Role: "edit"}, "requires a message"},
		{"map missing items", Node{ID: "b", Type: NodeMap, Role: "edit", Message: "go"}, "requires an items source"},
		{"condition empty", Node{ID: "b", Type: NodeCondition}, "no conditions"},
		{"condition unknown type", Node{ID: "b", Type: NodeCondition, Conditions: map[string]any{"bogus_cond": "x"}}, "unknown condition type"},
		{"join missing policy", Node{ID: "b", Type: NodeJoin}, "requires a join policy"},
		{"wait_event missing event", Node{ID: "b", Type: NodeWaitEvent}, "requires an event name"},
		{"join policy on non-join", Node{ID: "b", Type: NodeWaitHuman, Join: "all"}, "has a join policy but type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := linearGraph()
			g.Nodes[1] = tt.node
			v := g.Validate()
			assertErrorContains(t, v, tt.wantErr)
		})
	}
}

func TestValidateConditionNodeOK(t *testing.T) {
	g := linearGraph()
	g.Nodes[1] = Node{ID: "b", Type: NodeCondition, Conditions: map[string]any{"branch_match": "^main$"}}
	v := g.Validate()
	if !v.OK() {
		t.Errorf("expected valid condition node, got errors: %v", v.Errors)
	}
}

func TestValidateConditionsOnWrongTypeWarns(t *testing.T) {
	g := linearGraph()
	g.Nodes[1].Conditions = map[string]any{"branch_match": "^main$"}
	v := g.Validate()
	if !v.OK() {
		t.Fatalf("conditions on a send node must not be an error: %v", v.Errors)
	}
	assertWarnContains(t, v, "ignored")
}

func TestValidateUnreachableNode(t *testing.T) {
	g := linearGraph()
	g.Nodes = append(g.Nodes, Node{ID: "c", Type: NodeSend, Role: "review", Action: "review", Message: "go"})
	v := g.Validate()
	assertErrorContains(t, v, "unreachable")
}

func TestValidateUncappedCycleRejected(t *testing.T) {
	g := linearGraph()
	g.Edges = append(g.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure})
	v := g.Validate()
	assertErrorContains(t, v, "uncapped cycle")
}

func TestValidateCappedLoopAccepted(t *testing.T) {
	g := linearGraph()
	g.Edges = append(g.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure, MaxIterations: 3})
	v := g.Validate()
	if !v.OK() {
		t.Errorf("capped loop must validate, got errors: %v", v.Errors)
	}
}

// joinGraph builds a fan-out/join shape: a → (b1, b2) → j → c.
func joinGraph(policy string, quorum int) *Graph {
	return &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "b1", Type: NodeSpawn, Role: "edit", Message: "go"},
			{ID: "b2", Type: NodeSpawn, Role: "edit", Message: "go"},
			{ID: "j", Type: NodeJoin, Join: policy, Quorum: quorum},
			{ID: "c", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
		},
		Edges: []Edge{
			{From: "a", To: "b1"},
			{From: "a", To: "b2"},
			{From: "b1", To: "j"},
			{From: "b2", To: "j"},
			{From: "j", To: "c"},
		},
	}
}

func TestValidateJoinPolicies(t *testing.T) {
	if v := joinGraph(JoinAll, 0).Validate(); !v.OK() {
		t.Errorf("join all: %v", v.Errors)
	}
	if v := joinGraph(JoinAny, 0).Validate(); !v.OK() {
		t.Errorf("join any: %v", v.Errors)
	}
	if v := joinGraph(JoinQuorum, 2).Validate(); !v.OK() {
		t.Errorf("join quorum 2: %v", v.Errors)
	}
}

func TestValidateJoinQuorumOutOfRange(t *testing.T) {
	v := joinGraph(JoinQuorum, 3).Validate()
	assertErrorContains(t, v, "quorum 3 out of range")
	v = joinGraph(JoinQuorum, 0).Validate()
	assertErrorContains(t, v, "out of range")
}

func TestValidateJoinUnknownPolicy(t *testing.T) {
	v := joinGraph("most", 0).Validate()
	assertErrorContains(t, v, "unknown policy")
}

func TestValidateJoinQuorumOnAllWarns(t *testing.T) {
	v := joinGraph(JoinAll, 2).Validate()
	if !v.OK() {
		t.Fatalf("quorum field with all policy must not be an error: %v", v.Errors)
	}
	assertWarnContains(t, v, "quorum")
}

func TestValidateJoinSingleIncomingWarns(t *testing.T) {
	g := linearGraph()
	g.Nodes = append(g.Nodes, Node{ID: "j", Type: NodeJoin, Join: JoinAll})
	g.Edges = append(g.Edges, Edge{From: "b", To: "j"})
	v := g.Validate()
	if !v.OK() {
		t.Fatalf("single-incoming join must not be an error: %v", v.Errors)
	}
	assertWarnContains(t, v, "nothing to join")
}

// gateGraph builds a → [gate?] → commit-node shape.
func gateGraph(gated bool, commitNode Node) *Graph {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			commitNode,
		},
	}
	if gated {
		g.Nodes = append(g.Nodes, Node{ID: "gate", Type: NodeWaitHuman})
		g.Edges = []Edge{{From: "a", To: "gate"}, {From: "gate", To: commitNode.ID}}
	} else {
		g.Edges = []Edge{{From: "a", To: commitNode.ID}}
	}
	return g
}

func TestValidateGateRule(t *testing.T) {
	commit := Node{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "commit it"}
	jiraWrite := Node{ID: "c", Type: NodeSend, Role: "plan", Action: "jira-write", Message: "update ticket"}
	prRead := Node{ID: "c", Type: NodeSend, Role: "commit", Action: "pr-read", Message: "read PR"}

	if v := gateGraph(false, commit).Validate(); v.OK() {
		t.Error("ungated commit node must be rejected")
	} else {
		assertErrorContains(t, v, "wait_human gate")
	}
	if v := gateGraph(true, commit).Validate(); !v.OK() {
		t.Errorf("gated commit node must validate: %v", v.Errors)
	}
	if v := gateGraph(false, jiraWrite).Validate(); v.OK() {
		t.Error("ungated jira-write node must be rejected")
	}
	if v := gateGraph(true, jiraWrite).Validate(); !v.OK() {
		t.Errorf("gated jira-write node must validate: %v", v.Errors)
	}
	if v := gateGraph(false, prRead).Validate(); !v.OK() {
		t.Errorf("pr-read is read-only and needs no gate: %v", v.Errors)
	}

	// Spawn and map nodes deliver work to a role too — a commit-role
	// worker is a mutation regardless of node type.
	spawnCommit := Node{ID: "c", Type: NodeSpawn, Role: "commit", Message: "commit it"}
	mapCommit := Node{ID: "c", Type: NodeMap, Role: "commit", Message: "commit ${item}", Items: "changed_files"}
	if v := gateGraph(false, spawnCommit).Validate(); v.OK() {
		t.Error("ungated commit-role spawn node must be rejected")
	}
	if v := gateGraph(false, mapCommit).Validate(); v.OK() {
		t.Error("ungated commit-role map node must be rejected")
	}
	if v := gateGraph(true, mapCommit).Validate(); !v.OK() {
		t.Errorf("gated commit-role map node must validate: %v", v.Errors)
	}
}

// TestValidateGateRuleMixedPaths pins that ONE ungated path is enough to
// reject, even when a gated path to the same node also exists.
func TestValidateGateRuleMixedPaths(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "commit it"},
		},
		Edges: []Edge{
			{From: "a", To: "gate"},
			{From: "gate", To: "c"},
			{From: "a", To: "c", Outcome: OutcomeFailure},
		},
	}
	v := g.Validate()
	assertErrorContains(t, v, "wait_human gate")
}

func TestBuiltinGraphTemplatesValidate(t *testing.T) {
	want := []string{"build-test-review", "commit-pr-review-loop", "deploy-verify", "pr-local-review", "req-code-pr", "story-lifecycle", "story-to-spec", "update-spec-docs"}
	if len(builtinGraphJSON) != len(want) {
		t.Errorf("expected %d builtin templates, got %d", len(want), len(builtinGraphJSON))
	}
	for _, name := range want {
		data, ok := builtinGraphJSON[name]
		if !ok {
			t.Errorf("missing builtin template %q", name)
			continue
		}
		g, err := ParseGraph([]byte(data))
		if err != nil {
			t.Errorf("template %q: parse: %v", name, err)
			continue
		}
		if g.Name != name {
			t.Errorf("template %q declares name %q", name, g.Name)
		}
		v := g.Validate()
		if !v.OK() {
			t.Errorf("template %q has validation errors: %v", name, v.Errors)
		}
		for _, w := range v.Warnings {
			if name == "deploy-verify" && strings.Contains(w, "launching the run is its only approval") {
				continue // recorded deliberate trade — presence pinned by TestBuiltinGateTextClean
			}
			t.Errorf("template %q has validation warning: %s", name, w)
		}
	}
}

// A review failure must route to the fix worker, not kill the run —
// the missing edge failed a live req-code-pr at its review node
// (2026-08-31, run 1788195259) while build/test failures routed fine.
func TestReviewFailureRoutesToFix(t *testing.T) {
	for _, name := range []string{"req-code-pr", "story-lifecycle"} {
		g, err := ParseGraph([]byte(builtinGraphJSON[name]))
		if err != nil {
			t.Fatalf("template %q: parse: %v", name, err)
		}
		found := false
		for _, e := range g.Edges {
			if e.From == "review" && e.To == "fix" && e.Outcome == OutcomeFailure {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("template %q lacks a review -[failure]-> fix edge", name)
		}
	}
}

// The commit-pr-review-loop must CONFIRM the PR exists before watching
// it: a live run (2026-08-31, is-advising-gateway) reached its close
// gate with no PR because the commit node's decline derived
// unknown→success. verify-pr demands a literal token, pr-check branches
// on it, and the failure edge loops back to the commit node — capped.
func TestCommitPrReviewLoopVerifiesPr(t *testing.T) {
	g, err := ParseGraph([]byte(builtinGraphJSON["commit-pr-review-loop"]))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var check *Node
	for i := range g.Nodes {
		if g.Nodes[i].ID == "pr-check" {
			check = &g.Nodes[i]
		}
	}
	if check == nil || check.Type != NodeCondition {
		t.Fatal("pr-check condition node missing")
	}
	if v, ok := check.Conditions["output_contains"]; !ok || v != "PR-CONFIRMED" {
		t.Errorf("pr-check conditions = %v, want output_contains PR-CONFIRMED", check.Conditions)
	}
	var success, cappedRetry bool
	for _, e := range g.Edges {
		if e.From == "pr-check" && e.To == "b" && e.Outcome == "" {
			success = true
		}
		if e.From == "pr-check" && e.To == "a" && e.Outcome == OutcomeFailure && e.MaxIterations > 0 {
			cappedRetry = true
		}
	}
	if !success || !cappedRetry {
		t.Errorf("pr-check edges incomplete: success=%v cappedRetry=%v", success, cappedRetry)
	}
}

func TestResolveGraphTemplateBuiltin(t *testing.T) {
	g, source, err := ResolveGraphTemplate("build-test-review")
	if err != nil {
		t.Fatalf("resolve builtin: %v", err)
	}
	if source != "builtin" || g.Name != "build-test-review" {
		t.Errorf("got source %q name %q", source, g.Name)
	}
}

func TestResolveGraphTemplateUnknown(t *testing.T) {
	if _, _, err := ResolveGraphTemplate("no-such-template"); err == nil {
		t.Error("expected error for unknown template")
	}
}

func TestResolveGraphTemplateProjectOverride(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	gdir := filepath.Join(dir, projectGraphDir)
	if err := os.MkdirAll(gdir, 0o755); err != nil {
		t.Fatal(err)
	}
	override := `{"name": "build-test-review", "description": "project override", "start": "a",
		"nodes": [{"id": "a", "type": "send", "role": "build", "action": "build", "message": "go"}], "edges": []}`
	if err := os.WriteFile(filepath.Join(gdir, "build-test-review.json"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	g, source, err := ResolveGraphTemplate("build-test-review")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if source != "project" || g.Description != "project override" {
		t.Errorf("expected project override, got source %q desc %q", source, g.Description)
	}
}

func TestListGraphTemplatesIncludesBuiltins(t *testing.T) {
	// Neutral cwd/home so project- and user-tier templates cannot shadow.
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	t.Setenv("HOME", dir)

	infos := ListGraphTemplates()
	if len(infos) != len(builtinGraphJSON) {
		t.Fatalf("expected %d templates, got %d: %+v", len(builtinGraphJSON), len(infos), infos)
	}
	for i := 1; i < len(infos); i++ {
		if infos[i-1].Name > infos[i].Name {
			t.Errorf("templates not sorted: %q before %q", infos[i-1].Name, infos[i].Name)
		}
	}
	for _, info := range infos {
		if info.Source != "builtin" {
			t.Errorf("template %q resolved from %q, want builtin", info.Name, info.Source)
		}
		if info.Description == "" {
			t.Errorf("template %q has no description", info.Name)
		}
	}
}

// TestCancelGraphRunExpiresTasks pins the zombie-redrive fix: canceling
// a run times out its nodes' correlated in-flight tasks, so the stall
// watchdog cannot re-drive requests nobody wants anymore (observed live
// 2026-08-27: a canceled loop's edit node re-driven repeatedly). An
// in-flight task the run does NOT reference must survive — cancel kills
// only its own (negative control).
func TestCancelGraphRunExpiresTasks(t *testing.T) {
	session := "graph-cancel-expiry-test"
	t.Cleanup(func() { _ = os.RemoveAll(BusDir(session)) })
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}

	run, err := CreateGraphRun(session, writableTestGraph("cancelable"), "cancelable", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(session, Message{ID: "zombie-1", From: "daemon", To: "edit", Action: "edit", Payload: "p"}, 600); err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(session, Message{ID: "bystander-1", From: "edit", To: "run", Action: "run", Payload: "p"}, 600); err != nil {
		t.Fatal(err)
	}
	if err := MutateNodeStatus(session, run.ID, "a", func(st *GraphNodeStatus) {
		st.State = GraphNodeRunning
		st.TaskID = "zombie-1"
	}); err != nil {
		t.Fatal(err)
	}

	if err := CancelGraphRun(session, run.ID); err != nil {
		t.Fatal(err)
	}
	zombie, err := ReadTask(session, "zombie-1")
	if err != nil || zombie.Status != TaskTimedOut {
		t.Errorf("cancel must time out the node's in-flight task, got %+v err %v", zombie, err)
	}
	bystander, err := ReadTask(session, "bystander-1")
	if err != nil || bystander.Status != TaskInFlight {
		t.Errorf("an unrelated in-flight task must survive the cancel (negative control), got %+v err %v", bystander, err)
	}
}

// TestCreateGraphRunRequiresSpec pins the requires_spec gate at the
// run-creation chokepoint: a spec-driven graph refuses to start with no
// active requirements spec, and starts once one is set (negative
// control). req-code-pr carries the flag builtin.
func TestCreateGraphRunRequiresSpec(t *testing.T) {
	session := "graph-requires-spec-test"
	t.Cleanup(func() { _ = os.RemoveAll(BusDir(session)) })

	g := writableTestGraph("spec-driven")
	g.RequiresSpec = true
	if _, err := CreateGraphRun(session, g, "spec-driven", "x"); err == nil {
		t.Fatal("a requires_spec graph must refuse to run with no active spec")
	}

	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(session, "docs/requirements/drafts/some-spec.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateGraphRun(session, g, "spec-driven", "x"); err != nil {
		t.Fatalf("with an active spec set the run must start: %v", err)
	}

	tpl, _, err := ResolveGraphTemplate("req-code-pr")
	if err != nil {
		t.Fatalf("req-code-pr builtin missing: %v", err)
	}
	if !tpl.RequiresSpec {
		t.Error("req-code-pr must carry requires_spec — implementing against no spec is the case the gate exists for")
	}
}

// TestValidateNodeGuard pins the guard field rules: unknown values and
// non-dispatch node types are errors; a guarded send validates clean.
func TestValidateNodeGuard(t *testing.T) {
	g := linearGraph()
	g.Nodes[0].Guard = "no-such-guard"
	assertErrorContains(t, g.Validate(), "unknown guard")

	g = linearGraph()
	g.Nodes = append(g.Nodes, Node{ID: "gate", Type: NodeWaitHuman, Guard: GuardSpecComplete})
	g.Edges = append(g.Edges, Edge{From: "b", To: "gate"})
	assertErrorContains(t, g.Validate(), "guards are dispatch-time")

	g = linearGraph()
	g.Nodes[0].Guard = GuardSpecComplete
	if v := g.Validate(); !v.OK() {
		t.Errorf("guard on a send node must validate: %v", v.Errors)
	}
}

// TestCommitPRReviewLoopCloseSpecGuarded pins the builtin's close-spec
// controls: the daemon-side spec-complete guard (MUX-114 — wording alone
// is an instruction to a model, not a control) and the dedicated
// close-gate (user request 2026-08-28: the close-out is its own local
// approval, not a clause riding gate2's tail).
func TestCommitPRReviewLoopCloseSpecGuarded(t *testing.T) {
	tpl, _, err := ResolveGraphTemplate("commit-pr-review-loop")
	if err != nil {
		t.Fatalf("commit-pr-review-loop builtin missing: %v", err)
	}
	found := false
	for _, n := range tpl.Nodes {
		if n.ID == "close-spec" {
			found = true
			if n.Guard != GuardSpecComplete {
				t.Errorf("close-spec guard %q, want %q", n.Guard, GuardSpecComplete)
			}
		}
	}
	if !found {
		t.Fatal("commit-pr-review-loop has no close-spec node")
	}
	if !templateEdge(tpl, "close-gate", "close-spec") {
		t.Error("close-spec must sit behind its own close-gate")
	}
	for _, e := range tpl.Edges {
		if e.To == "close-spec" && e.From != "close-gate" {
			t.Errorf("close-spec reachable around its gate via %s", e.From)
		}
	}
}

// TestBuiltinGateMessagesNonEmpty pins that every builtin wait_human gate
// carries approval text. A human approving a blank gate cannot see what
// the approval releases — found live in update-spec-docs, whose gate used
// a "prompt" key that json silently dropped (MUX-114 Phase 2).
func TestBuiltinGateMessagesNonEmpty(t *testing.T) {
	for name, raw := range builtinGraphJSON {
		g, err := ParseGraph([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, n := range g.Nodes {
			if n.Type == NodeWaitHuman && strings.TrimSpace(n.Message) == "" {
				t.Errorf("%s: gate %q has no message — the approval text must state what it releases", name, n.ID)
			}
		}
	}
}

// TestValidateGateTextWarnsUnnamedMutation pins the gate-text heuristic:
// a gate releasing a commit node without naming the mutation warns; the
// same gate naming it does not (negative control).
func TestValidateGateTextWarnsUnnamedMutation(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "gate",
		Nodes: []Node{
			{ID: "gate", Type: NodeWaitHuman, Message: "Approve the thing"},
			{ID: "ship", Type: NodeSend, Role: "commit", Action: "commit", Message: "commit it"},
		},
		Edges: []Edge{{From: "gate", To: "ship"}},
	}
	v := g.Validate()
	if !v.OK() {
		t.Fatalf("unexpected errors: %v", v.Errors)
	}
	found := false
	for _, w := range v.Warnings {
		if strings.Contains(w, "does not name the mutation") {
			found = true
		}
	}
	if !found {
		t.Errorf("unnamed mutation must warn, got %v", v.Warnings)
	}

	g.Nodes[0].Message = "Approve commit and push"
	for _, w := range g.Validate().Warnings {
		if strings.Contains(w, "does not name the mutation") {
			t.Errorf("named mutation must not warn: %s", w)
		}
	}
}

// TestValidateGateTextIgnoresGateFailureEdges pins the sweep origin rule
// (PR #49 Copilot): a wait_human node only ever produces success, so a
// mutation behind the gate's failure edge never fires from that approval
// and must not warn — while the success-path mutation still does.
func TestValidateGateTextIgnoresGateFailureEdges(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "gate",
		Nodes: []Node{
			{ID: "gate", Type: NodeWaitHuman, Message: "Approve commit and push"},
			{ID: "ship", Type: NodeSend, Role: "commit", Action: "commit", Message: "commit it"},
			{ID: "dead", Type: NodeSend, Role: "commit", Action: "comment", Message: "never fires"},
		},
		Edges: []Edge{
			{From: "gate", To: "ship"},
			{From: "gate", To: "dead", Outcome: OutcomeFailure},
		},
	}
	for _, w := range g.Validate().Warnings {
		if strings.Contains(w, `node "dead"`) {
			t.Errorf("mutation behind a gate failure edge must not warn: %s", w)
		}
	}

	g.Nodes[0].Message = "Approve the thing"
	found := false
	for _, w := range g.Validate().Warnings {
		if strings.Contains(w, `node "ship"`) {
			found = true
		}
	}
	if !found {
		t.Error("success-path mutation must still warn when unnamed (negative control)")
	}
}

// TestValidateUngatedDeployWarns pins the deploy advisory: an ungated
// deploy node warns, a gated one does not (negative control).
func TestValidateUngatedDeployWarns(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "deploy",
		Nodes: []Node{{ID: "deploy", Type: NodeSend, Role: "deploy", Action: "deploy", Message: "go"}},
	}
	found := false
	for _, w := range g.Validate().Warnings {
		if strings.Contains(w, "launching the run is its only approval") {
			found = true
		}
	}
	if !found {
		t.Error("ungated deploy node must warn")
	}

	gated := &Graph{
		Name:  "t",
		Start: "gate",
		Nodes: []Node{
			{ID: "gate", Type: NodeWaitHuman, Message: "Approve the deployment"},
			{ID: "deploy", Type: NodeSend, Role: "deploy", Action: "deploy", Message: "go"},
		},
		Edges: []Edge{{From: "gate", To: "deploy"}},
	}
	for _, w := range gated.Validate().Warnings {
		if strings.Contains(w, "launching the run is its only approval") {
			t.Errorf("gated deploy must not warn: %s", w)
		}
	}
}

// TestBuiltinGateTextClean holds shipped templates to zero gate-text
// warnings, and pins deploy-verify's ungated-deploy warning as the one
// recorded deliberate trade — asserted present so the check cannot go
// inert.
func TestBuiltinGateTextClean(t *testing.T) {
	for name, raw := range builtinGraphJSON {
		g, err := ParseGraph([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, w := range g.Validate().Warnings {
			if strings.Contains(w, "does not name the mutation") {
				t.Errorf("%s: %s", name, w)
			}
			if strings.Contains(w, "launching the run is its only approval") && name != "deploy-verify" {
				t.Errorf("%s: unexpected ungated-deploy warning: %s", name, w)
			}
		}
	}

	g, err := ParseGraph([]byte(builtinGraphJSON["deploy-verify"]))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range g.Validate().Warnings {
		if strings.Contains(w, "launching the run is its only approval") {
			found = true
		}
	}
	if !found {
		t.Error("deploy-verify must carry the ungated-deploy warning — the deliberate trade is recorded, not silent")
	}
}

// TestValidateDerivedLoopCap pins the max_iterations_from_spec edge: it
// exempts its cycle from the DAG check like a numeric cap, setting both
// forms is an error, and an uncapped cycle still fails (negative
// control).
func TestValidateDerivedLoopCap(t *testing.T) {
	g := linearGraph()
	g.Edges = append(g.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure, MaxIterationsFromSpec: true})
	if v := g.Validate(); !v.OK() {
		t.Errorf("spec-derived loop cap must satisfy the cycle rule: %v", v.Errors)
	}

	g.Edges[len(g.Edges)-1].MaxIterations = 3
	assertErrorContains(t, g.Validate(), "choose one")

	g2 := linearGraph()
	g2.Edges = append(g2.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure})
	if v := g2.Validate(); v.OK() {
		t.Error("uncapped cycle must still fail validation (negative control)")
	}
}

// templateEdge reports whether a template has a from->to edge.
func templateEdge(g *Graph, from, to string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// TestShipTemplatesUpdateSpecBeforeGate pins the user requirement
// (2026-08-28): both ship templates update the spec DURING the run, on
// the ONLY path to their commit gate (a direct review->gate edge would
// silently bypass it — plan finding).
func TestShipTemplatesUpdateSpecBeforeGate(t *testing.T) {
	for name, gate := range map[string]string{"req-code-pr": "phase-gate", "story-lifecycle": "ship-gate"} {
		tpl, _, err := ResolveGraphTemplate(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var us *Node
		for i := range tpl.Nodes {
			if tpl.Nodes[i].ID == "update-spec" {
				us = &tpl.Nodes[i]
			}
		}
		if us == nil || NormalizeBusRole(us.Role) != "plan" || us.Action != "verify-spec" {
			t.Errorf("%s: update-spec node missing or misrouted: %+v", name, us)
		}
		if !templateEdge(tpl, "review", "update-spec") || !templateEdge(tpl, "update-spec", gate) {
			t.Errorf("%s: update-spec must sit between review and %s", name, gate)
		}
		if templateEdge(tpl, "review", gate) {
			t.Errorf("%s: direct review->%s edge bypasses update-spec", name, gate)
		}
	}
}

// TestReqCodePRMultiPhaseLoop pins the MUX-121 Phase 4 shape: per-phase
// gated commit with the phase-progress guard, spec-derived loop caps on
// both loop-closing edges, gate-and-ask on a stuck phase via the commit
// failure edge, termination to a final gate that alone releases push+PR.
func TestReqCodePRMultiPhaseLoop(t *testing.T) {
	tpl, _, err := ResolveGraphTemplate("req-code-pr")
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]*Node{}
	for i := range tpl.Nodes {
		nodes[tpl.Nodes[i].ID] = &tpl.Nodes[i]
	}
	if c := nodes["commit"]; c == nil || c.Guard != GuardPhaseProgress {
		t.Errorf("commit must carry the phase-progress guard, got %+v", c)
	} else if !strings.Contains(c.Message, "${completed_phase}") {
		t.Error("commit must name ${completed_phase} — ${current_phase} is one ahead by commit time")
	}
	if pg := nodes["phase-gate"]; pg == nil || !strings.Contains(pg.Message, "${completed_phase}") {
		t.Error("phase-gate must name ${completed_phase} so the human approves the right phase")
	}
	if !strings.Contains(nodes["implement"].Message, "${current_phase}") {
		t.Error("implement must target the derived ${current_phase}, not the frozen intent")
	}
	for _, loop := range [][2]string{{"loop-check", "implement"}, {"stuck-gate", "implement"}} {
		found := false
		for _, e := range tpl.Edges {
			if e.From == loop[0] && e.To == loop[1] && e.MaxIterationsFromSpec {
				found = true
			}
		}
		if !found {
			t.Errorf("loop edge %s->%s must derive its cap from the spec", loop[0], loop[1])
		}
	}
	if !templateEdge(tpl, "commit", "stuck-gate") {
		t.Error("a declined commit must route to the stuck gate (gate-and-ask), not dead-end")
	}
	if !templateEdge(tpl, "loop-check", "final-gate") || !templateEdge(tpl, "final-gate", "push-pr") {
		t.Error("termination must run through the final gate before push+PR")
	}
	// Negative control: nothing reaches push-pr except through final-gate.
	for _, e := range tpl.Edges {
		if e.To == "push-pr" && e.From != "final-gate" {
			t.Errorf("push-pr reachable around the final gate via %s", e.From)
		}
	}
	if v := tpl.Validate(); !v.OK() {
		t.Errorf("multi-phase req-code-pr must validate: %v", v.Errors)
	}
}

// TestStoryLifecycleCommitGuard pins story-lifecycle's single-phase
// contract: its commit keeps the intent-scoped phase-complete guard.
func TestStoryLifecycleCommitGuard(t *testing.T) {
	tpl, _, err := ResolveGraphTemplate("story-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	for i := range tpl.Nodes {
		if tpl.Nodes[i].ID == "commit" {
			if tpl.Nodes[i].Guard != GuardPhaseComplete {
				t.Errorf("story-lifecycle commit guard = %q, want phase-complete", tpl.Nodes[i].Guard)
			}
			return
		}
	}
	t.Error("story-lifecycle has no commit node")
}

// writableTestGraph returns a minimal graph that passes Validate() and
// the write-time description requirement.
func writableTestGraph(name string) *Graph {
	return &Graph{
		Name:        name,
		Description: "test graph",
		Start:       "a",
		Nodes:       []Node{{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"}},
	}
}

// TestWriteGraphDefinitionRequiresDescription pins the creation
// chokepoint: a new template without a description is refused and no
// file is written — the launcher lists templates by description, and an
// unlabeled row helps nobody. Running existing description-less files
// stays legal (the rule lives in the write path, not Validate()).
func TestWriteGraphDefinitionRequiresDescription(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	bare := writableTestGraph("no-desc")
	bare.Description = "  "
	if _, _, err := WriteGraphDefinition(bare, GraphScopeProject); err == nil {
		t.Fatal("a description-less graph must be refused at write time")
	}
	if _, err := os.Stat(filepath.Join(projectGraphDir, "no-desc.json")); err == nil {
		t.Error("the refused graph must leave no file behind")
	}
	if bare.Validate().OK() != true {
		t.Error("negative control: the same graph still VALIDATES — only the write path requires a description")
	}
}

func TestWriteGraphDefinitionFreshCheckout(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	path, v, err := WriteGraphDefinition(writableTestGraph("my-graph"), GraphScopeProject)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !v.OK() {
		t.Fatalf("unexpected validation errors: %v", v.Errors)
	}
	if path != filepath.Join(projectGraphDir, "my-graph.json") {
		t.Errorf("unexpected path %q", path)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file left behind")
	}

	g, source, err := ResolveGraphTemplate("my-graph")
	if err != nil {
		t.Fatalf("resolve written graph: %v", err)
	}
	if source != "project" || g.Name != "my-graph" {
		t.Errorf("round-trip: got source %q name %q", source, g.Name)
	}
}

func TestWriteGraphDefinitionInvalidWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	bad := writableTestGraph("bad-graph")
	bad.Start = "missing"
	_, v, err := WriteGraphDefinition(bad, GraphScopeProject)
	if err == nil {
		t.Fatal("expected error for invalid graph")
	}
	if v == nil || v.OK() {
		t.Error("expected validation errors to be returned")
	}
	if _, statErr := os.Stat(projectGraphDir); !os.IsNotExist(statErr) {
		t.Error("failure path created the graph directory")
	}
}

func TestWriteGraphDefinitionUnsafeNameWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	for _, name := range []string{"../escape", "a/b", `a\b`, ".hidden"} {
		if _, _, err := WriteGraphDefinition(writableTestGraph(name), GraphScopeProject); err == nil {
			t.Errorf("name %q: expected error", name)
		}
	}
	if _, err := os.Stat(projectGraphDir); !os.IsNotExist(err) {
		t.Error("unsafe-name path created the graph directory")
	}
}

func TestWriteGraphDefinitionUserScope(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	t.Setenv("HOME", dir)

	path, _, err := WriteGraphDefinition(writableTestGraph("user-graph"), GraphScopeUser)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	want := filepath.Join(dir, ".config", "muxcode", "graphs", "user-graph.json")
	if path != want {
		t.Errorf("path %q, want %q", path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("written file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, projectGraphDir)); !os.IsNotExist(err) {
		t.Error("user-scope write touched the project directory")
	}
}

func TestWriteGraphDefinitionUnknownScope(t *testing.T) {
	if _, _, err := WriteGraphDefinition(writableTestGraph("g"), "builtin"); err == nil {
		t.Error("expected error for non-writable scope")
	}
}

// TestWriteGraphDefinitionUngatedCommitRejected pins the Phase 5 (MUX-109)
// authority criterion: a composed graph placing a commit node outside a
// wait_human gate is rejected by the existing validator rule on the write
// path — no file appears, and there is no bypass. The gated variant is
// the positive control proving the rule rejects the missing gate, not
// commit nodes as such.
func TestWriteGraphDefinitionUngatedCommitRejected(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	ungated := &Graph{
		Name:  "bad-commit",
		Start: "c",
		Nodes: []Node{{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "commit it"}},
	}
	_, v, err := WriteGraphDefinition(ungated, GraphScopeProject)
	if err == nil {
		t.Fatal("ungated commit node must be rejected")
	}
	if v == nil || v.OK() {
		t.Fatal("expected validation errors")
	}
	found := false
	for _, e := range v.Errors {
		if strings.Contains(e, "wait_human") {
			found = true
		}
	}
	if !found {
		t.Errorf("rejection must cite the gate rule: %v", v.Errors)
	}
	if _, statErr := os.Stat(projectGraphDir); !os.IsNotExist(statErr) {
		t.Error("rejected graph left the directory behind")
	}

	gated := &Graph{
		Name:        "gated-commit",
		Description: "human-gated commit",
		Start:       "g",
		Nodes: []Node{
			{ID: "g", Type: NodeWaitHuman},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "commit it"},
		},
		Edges: []Edge{{From: "g", To: "c"}},
	}
	if _, _, err := WriteGraphDefinition(gated, GraphScopeProject); err != nil {
		t.Fatalf("gated commit graph must write: %v", err)
	}
}
