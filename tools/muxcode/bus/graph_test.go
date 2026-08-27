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
			t.Errorf("template %q has validation warning: %s", name, w)
		}
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
