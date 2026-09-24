package bus

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeSpawns replaces graphSpawnFn with an in-memory dispatcher that
// records tasks and immediately writes a "completed" spawn entry, so
// executor tests run without tmux.
func fakeSpawns(t *testing.T, session string) *[]string {
	t.Helper()
	var tasks []string
	orig := graphSpawnFn
	n := 0
	graphSpawnFn = func(sess, role, task, owner, runID, nodeID string) (string, error) {
		n++
		id := fmt.Sprintf("spawn-fake%04d", n)
		tasks = append(tasks, role+": "+task)
		entry := SpawnEntry{ID: id, Role: role, SpawnRole: id, Owner: owner,
			Task: task, Status: "completed", StartedAt: time.Now().Unix(),
			RunID: runID, NodeID: nodeID}
		if err := appendSpawnEntry(sess, entry); err != nil {
			t.Fatalf("append spawn entry: %v", err)
		}
		return id, nil
	}
	t.Cleanup(func() { graphSpawnFn = orig })
	return &tasks
}

// appendSpawnEntry writes a spawn entry the way the spawn store expects.
func appendSpawnEntry(session string, e SpawnEntry) error {
	entries, _ := ReadSpawnEntries(session)
	entries = append(entries, e)
	return WriteSpawnEntries(session, entries)
}

// completeSendNode fakes an agent answering a running send node: completes
// its task and optionally writes an authoritative history row carrying the
// verdict.
func completeSendNode(t *testing.T, session, runID, nodeID, rowOutcome string) {
	t.Helper()
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		t.Fatalf("read node %s: %v", nodeID, err)
	}
	if st.State != GraphNodeRunning || st.TaskID == "" {
		t.Fatalf("node %s not running with a task: %+v", nodeID, st)
	}
	CompleteTask(session, st.TaskID, "resp-"+st.TaskID)

	if rowOutcome != "" {
		g, err := ReadGraphRunGraph(session, runID)
		if err != nil {
			t.Fatal(err)
		}
		var role, action string
		for _, n := range g.Nodes {
			if n.ID == nodeID {
				role, action = NormalizeBusRole(n.Role), n.Action
			}
		}
		row := HookHistoryEntry{TS: time.Now().Unix() + 1, Command: fixtureCommandFor(action),
			ExitCode: "0", Outcome: rowOutcome}
		if rowOutcome == OutcomeFailure {
			row.ExitCode = "1"
		}
		if err := WriteHookHistory(HistoryPath(session, role), row, 100); err != nil {
			t.Fatal(err)
		}
	}
}

// completeSendNodeWithReply answers a running send node with a real bus
// message, so the executor derives the node's outcome from the reply body the
// way a live agent's verdict token is read. completeSendNode's synthetic
// response id resolves to nothing, which leaves the output empty — fine for a
// node evidenced by a history row, useless for one judged on what it said.
//
// The reply is addressed away from its own role because Send drops a
// self-addressed message (isLoopingSelfSend); the recipient is otherwise
// irrelevant, since deriveSendOutcome looks the reply up by id.
func completeSendNodeWithReply(t *testing.T, session, runID, nodeID, role, reply string) {
	t.Helper()
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		t.Fatalf("read node %s: %v", nodeID, err)
	}
	if st.State != GraphNodeRunning || st.TaskID == "" {
		t.Fatalf("node %s not running with a task: %+v", nodeID, st)
	}
	to := "edit" // an edit-role reply to edit is dropped as a self-send
	if NormalizeBusRole(role) == "edit" {
		to = "commit"
	}
	resp := NewMessage(role, to, "response", "response", reply, "")
	if err := Send(session, resp); err != nil {
		t.Fatal(err)
	}
	CompleteTask(session, st.TaskID, resp.ID)
}

// fixtureCommandFor is the command a fixture row carries so it can testify
// for the node's action. An action no command evidences keeps an unclassified
// one: such a node is attributed by its agent's token, and a fixture must not
// pretend otherwise.
//
// Callers writing a fixture row rely on this: the row stands for "the
// dispatched work was observed", and rowAttributesTo rejects a command the
// action cannot be evidenced by, so a mismatched command here would silently
// make the row testify for nothing.
func fixtureCommandFor(action string) string {
	switch action {
	case "build":
		return "./build.sh"
	case "test":
		return "./test.sh"
	case "deploy":
		return "cdk deploy"
	case "commit":
		return "git commit -m fixture"
	case "checkout":
		return "git checkout main"
	case "pr-checkout":
		return "gh pr checkout 161"
	default:
		return "./fake.sh"
	}
}

func step(t *testing.T, session, runID string) {
	t.Helper()
	if err := StepGraphRun(session, runID); err != nil {
		t.Fatalf("StepGraphRun: %v", err)
	}
}

func nodeState(t *testing.T, session, runID, nodeID string) string {
	t.Helper()
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		t.Fatalf("read node %s: %v", nodeID, err)
	}
	return st.State
}

func TestExecLinearRun(t *testing.T) {
	run := createTestRun(t, linearGraph())

	// Tick 1: start node dispatches.
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeRunning {
		t.Fatalf("a state %q, want running", s)
	}
	// The send landed in the target role's inbox.
	msgs, _ := Peek(runTestSession, "build")
	if len(msgs) != 1 || msgs[0].Action != "build" {
		t.Fatalf("build inbox: %+v", msgs)
	}

	// Agent answers with an authoritative success row.
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeDone {
		t.Fatalf("a state %q, want done", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Fatalf("b state %q, want running", s)
	}

	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID)

	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunComplete {
		t.Errorf("run state %q, want complete", got.State)
	}
	// Completion wake: exactly one graph-complete request to edit.
	edit, _ := Peek(runTestSession, "edit")
	var wakes int
	for _, m := range edit {
		if m.Action == "graph-complete" {
			wakes++
		}
	}
	if wakes != 1 {
		t.Errorf("edit received %d graph-complete wakes, want 1", wakes)
	}
}

// conditionOutputGraph is a send → condition(output_contains PR-CONFIRMED)
// fan: success routes to b, failure to fail-note.
func conditionOutputGraph() *Graph {
	return &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "chk", Type: NodeCondition, Conditions: map[string]any{"output_contains": "PR-CONFIRMED"}},
			{ID: "b", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
			{ID: "fail-note", Type: NodeSend, Role: "build", Action: "build", Message: "retry"},
		},
		Edges: []Edge{
			{From: "a", To: "chk"},
			{From: "chk", To: "b"},
			{From: "chk", To: "fail-note", Outcome: OutcomeFailure},
		},
	}
}

// completeSendNodeWithPayload answers a running send node with a real
// response message so the node's harvested Output carries the payload.
func completeSendNodeWithPayload(t *testing.T, session, runID, nodeID, payload string) {
	t.Helper()
	resp := NewMessage("build", "edit", "response", "response", payload, "")
	if err := Send(session, resp); err != nil {
		t.Fatal(err)
	}
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	CompleteTask(session, st.TaskID, resp.ID)
	row := HookHistoryEntry{TS: time.Now().Unix() + 1, Command: "./build.sh",
		ExitCode: "0", Outcome: OutcomeSuccess}
	if err := WriteHookHistory(HistoryPath(session, "build"), row, 100); err != nil {
		t.Fatal(err)
	}
}

// A condition node must see its predecessor's harvested output — the
// live 2026-08-31 incident had a run sail past a declined PR creation
// because conditions evaluated against an empty context.
func TestExecConditionSeesPredecessorOutput(t *testing.T) {
	run := createTestRun(t, conditionOutputGraph())
	step(t, runTestSession, run.ID)
	completeSendNodeWithPayload(t, runTestSession, run.ID, "a", "PR-CONFIRMED https://github.com/x/y/pull/1")
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Fatalf("b state %q, want running — condition did not see predecessor output", s)
	}
}

// Negative control: without the token the condition fails and routes the
// failure edge — a condition that always passes cannot survive this.
func TestExecConditionFailsWithoutToken(t *testing.T) {
	run := createTestRun(t, conditionOutputGraph())
	step(t, runTestSession, run.ID)
	completeSendNodeWithPayload(t, runTestSession, run.ID, "a", "NO-PR-FOUND for this branch")
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "fail-note"); s != GraphNodeRunning {
		t.Fatalf("fail-note state %q, want running — failure edge not routed", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s == GraphNodeRunning {
		t.Fatal("b dispatched despite missing confirmation token")
	}
}

func TestExecFailureRoutesFailureEdge(t *testing.T) {
	g := linearGraph()
	g.Nodes = append(g.Nodes, Node{ID: "fix", Type: NodeSpawn, Role: "edit", Message: "fix it"})
	g.Edges = append(g.Edges, Edge{From: "a", To: "fix", Outcome: OutcomeFailure})
	run := createTestRun(t, g)
	fakeSpawns(t, runTestSession)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeFailure)
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodePending {
		t.Errorf("b state %q, want pending — success edge must not fire on failure", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "fix"); s != GraphNodeRunning {
		t.Errorf("fix state %q, want running", s)
	}
}

func TestExecFailureWithNoEdgeFailsRun(t *testing.T) {
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeFailure)
	step(t, runTestSession, run.ID)

	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Errorf("run state %q, want failed — failure with no live edge", got.State)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodePending {
		t.Errorf("b state %q, want pending — failed run must not dispatch", s)
	}
}

// An unknown outcome is no proof of success, so it must not advance the graph
// on its own. This replaces the old fall-back-to-success contract, under which
// a run shipped a commit behind build/test/review nodes that recorded nothing.
func TestExecUnknownHoldsForApproval(t *testing.T) {
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	// Complete the task with NO authoritative history row → outcome unknown.
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.Outcome != OutcomeUnknown {
		t.Errorf("a outcome %q, want unknown", st.Outcome)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodePending {
		t.Errorf("b state %q, want pending — unknown must not pass as success", s)
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "a", "pending")); err != nil {
		t.Error("held node must leave a pending approval marker")
	}
	var alerts int
	msgs, _ := Peek(runTestSession, "edit")
	for _, m := range msgs {
		if m.Action == "graph-approval" && strings.Contains(m.Payload, "a") {
			alerts++
		}
	}
	if alerts == 0 {
		t.Error("holding a node must alert edit — a silent hold is a stalled run")
	}
}

// completeSendNodeSentinel completes a node with a response payload and
// leaves NO authoritative history row, reproducing a non-hook provider
// (Codex, OpenCode) where the sentinel is the only verdict.
//
// The role's history is truncated first: these tests share one session, so a
// row written by an earlier test within the same second would otherwise
// outrank the sentinel and decide the outcome instead.
func completeSendNodeSentinel(t *testing.T, session, runID, nodeID, payload string) {
	t.Helper()
	g, err := ReadGraphRunGraph(session, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range g.Nodes {
		if n.ID == nodeID {
			if err := os.Remove(HistoryPath(session, NormalizeBusRole(n.Role))); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
	}
	resp := NewMessage("build", "edit", "response", "response", payload, "")
	if err := Send(session, resp); err != nil {
		t.Fatal(err)
	}
	st, err := ReadNodeStatus(session, runID, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	CompleteTask(session, st.TaskID, resp.ID)
}

func TestParseExitSentinel(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
		found   bool
	}{
		{"zero is success", "EXIT=0", OutcomeSuccess, true},
		{"nonzero is failure", "EXIT=1", OutcomeFailure, true},
		{"padded zero is success", "EXIT=00", OutcomeSuccess, true},
		{"trailing prose", "Build green. EXIT=0", OutcomeSuccess, true},
		{"last sentinel wins", "report EXIT=0 when done\nEXIT=1", OutcomeFailure, true},

		// Negative controls. Without these a parser that always claims a
		// verdict — the very failure this replaces — would pass every case
		// above.
		{"empty", "", "", false},
		{"status line echo", "• Working (9s • esc to interrupt)", "", false},
		{"unfilled placeholder", "report the code as EXIT=<n>", "", false},
		{"not at word boundary", "PREEXIT=0", "", false},
		{"digits run into text", "EXIT=0abc", "", false},
		{"prose verdict only", "Build succeeded; all checks passed", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseExitSentinel(tc.payload)
			if ok != tc.found {
				t.Fatalf("found=%v, want %v (payload %q)", ok, tc.found, tc.payload)
			}
			if got != tc.want {
				t.Errorf("outcome %q, want %q", got, tc.want)
			}
		})
	}
}

// The user-facing fix: a build/test node on a non-hook provider must advance
// on its own exit code. Before the sentinel every such node derived "unknown"
// and stalled the run on a human approval no work node should ever need.
func TestExecSentinelAdvancesWithoutApproval(t *testing.T) {
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	completeSendNodeSentinel(t, runTestSession, run.ID, "a", "Build succeeded. EXIT=0")
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.Outcome != OutcomeSuccess {
		t.Errorf("a outcome %q, want success — sentinel is the verdict with no hook row", st.Outcome)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Errorf("b state %q, want running — a sentinel must advance the graph unattended", s)
	}
	for _, p := range gateRequestPayloads(t, run.ID) {
		t.Errorf("work node asked for approval: %q", p)
	}
}

func TestExecSentinelFailureRoutesFailure(t *testing.T) {
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	completeSendNodeSentinel(t, runTestSession, run.ID, "a", "compile error\nEXIT=2")
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.Outcome != OutcomeFailure {
		t.Errorf("a outcome %q, want failure — a nonzero sentinel must not pass as success", st.Outcome)
	}
}

// Precedence: a hook-recorded row is authoritative, a sentinel is only
// TestObservedRowOutranksSelfReport pins MUX-148 Decision 4: a row an agent
// wrote about itself through `muxcode log` must not overrule one the runtime
// observed, however much newer it is.
//
// The live case (2026-09-14): a commit agent self-logged exit 0 for its own
// `git checkout -b`, and nothing could tell that row from an observed one.
func TestObservedRowOutranksSelfReport(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Unix() - 1
	path := HistoryPath(runTestSession, "build")

	observed := HookHistoryEntry{TS: time.Now().Unix(), Command: "./build.sh",
		ExitCode: "1", Outcome: OutcomeFailure}
	if err := WriteHookHistory(path, observed, 100); err != nil {
		t.Fatal(err)
	}
	// Newer, and claiming success — the shape that used to win on recency.
	claim := HookHistoryEntry{TS: time.Now().Unix() + 5, Command: "./build.sh",
		ExitCode: "0", Outcome: OutcomeSuccess, Source: SourceSelfReported}
	if err := WriteHookHistory(path, claim, 100); err != nil {
		t.Fatal(err)
	}

	row, ok := latestAuthoritativeRow(runTestSession, "build", since)
	if !ok {
		t.Fatal("expected a verdict")
	}
	if row.Outcome != OutcomeFailure {
		t.Errorf("outcome = %q, want failure — a self-report overrode an observed row", row.Outcome)
	}
}

// TestSelfReportUsedWhenNothingObserved is the negative control criterion 139
// demands: the non-hook providers record work only through `muxcode log`, so a
// fix that discards self-reports entirely would hold every one of their nodes
// forever. Without this case, returning nothing at all would pass the test
// above.
func TestSelfReportUsedWhenNothingObserved(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Unix() - 1

	claim := HookHistoryEntry{TS: time.Now().Unix(), Command: "pnpm test",
		ExitCode: "0", Outcome: OutcomeSuccess, Source: SourceSelfReported}
	if err := WriteHookHistory(HistoryPath(runTestSession, "test"), claim, 100); err != nil {
		t.Fatal(err)
	}

	row, ok := latestAuthoritativeRow(runTestSession, "test", since)
	if !ok || row.Outcome != OutcomeSuccess {
		t.Fatalf("row = (%+v, %v), want the self-report to stand when nothing observed the work", row, ok)
	}
}

// TestRawRowWithoutHookSourceIsNotEvidence pins the fail-closed rule against
// rows written straight to the JSONL — which is both the forgery shape and the
// only way to reach this path at all: WriteHookHistory stamps a blank source
// as SourceHook, so no test using it can produce an absent one. Without a raw
// write the regression is invisible, and "absent source is evidence" could
// return unnoticed.
func TestRawRowWithoutHookSourceIsNotEvidence(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Unix() - 1
	path := HistoryPath(runTestSession, "build")
	now := time.Now().Unix()

	raw := fmt.Sprintf(
		"{\"ts\":%d,\"command\":\"./build.sh\",\"exit_code\":\"0\",\"outcome\":\"success\"}\n"+
			"{\"ts\":%d,\"command\":\"./build.sh\",\"exit_code\":\"0\",\"outcome\":\"success\",\"source\":\"hook-ish\"}\n",
		now, now+1)
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	if row, ok := latestAuthoritativeRow(runTestSession, "build", since); ok {
		t.Errorf("a raw row with %q source was accepted as evidence: %+v", row.Source, row)
	}

	// Positive control: with the same file and helper, a properly stamped row
	// IS evidence — so the rejection above is the rule working, not the
	// fixture failing to be read at all.
	observed := HookHistoryEntry{TS: now + 2, Command: "./build.sh",
		ExitCode: "0", Outcome: OutcomeSuccess}
	if err := WriteHookHistory(path, observed, 100); err != nil {
		t.Fatal(err)
	}
	row, ok := latestAuthoritativeRow(runTestSession, "build", since)
	if !ok || row.Outcome != OutcomeSuccess {
		t.Fatalf("stamped row = (%+v, %v), want it accepted — the file is readable", row, ok)
	}
}

// TestSeedVerdictToken pins which dispatches carry the token, in both
// directions. Seeding everything is as wrong as seeding nothing: a node with
// an evidencing row that is also asked for a token can produce two signals
// that disagree, which deriveSendOutcome resolves by holding.
func TestSeedVerdictToken(t *testing.T) {
	got := seedVerdictToken("review", "do the thing")
	if !strings.HasPrefix(got, "do the thing") || !strings.Contains(got, reviewCountsInstruction) || strings.Contains(got, verdictTokenInstruction) {
		t.Errorf("a review is seeded with the counts line, not the completion token: %q", got)
	}
	unevidenced := []string{"edit", "comment", "update-docs", "pr-read", "jira-write"}
	for _, action := range unevidenced {
		got := seedVerdictToken(action, "do the thing")
		if !strings.Contains(got, verdictTokenInstruction) {
			t.Errorf("seedVerdictToken(%q) carries no token instruction — the node has no signal at all", action)
		}
		if !strings.HasPrefix(got, "do the thing") {
			t.Errorf("seedVerdictToken(%q) = %q, want the message kept intact ahead of the seed", action, got)
		}
	}
	for _, action := range []string{"build", "test", "deploy", "commit", "checkout"} {
		if got := seedVerdictToken(action, "do the thing"); got != "do the thing" {
			t.Errorf("seedVerdictToken(%q) seeded a token onto an action a command evidences: %q", action, got)
		}
	}
}

// TestExecSendSeedsVerdictForUnevidencedAction is the executor-level proof:
// the seed must reach the agent's inbox, not merely exist as a function.
//
// commit-pr-review-loop's `c` is the node this phase exists for — edit:edit,
// which no command evidences and whose role definition carries no EXIT= line,
// so before the seed it held on every run.
func TestExecSendSeedsVerdictForUnevidencedAction(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "c",
		Nodes: []Node{
			{ID: "c", Type: NodeSend, Role: "edit", Action: "edit", Message: "Address the PR review comments"},
			{ID: "bld", Type: NodeSend, Role: "build", Action: "build", Message: "Run ./build.sh"},
		},
		Edges: []Edge{{From: "c", To: "bld"}},
	}
	run := createTestRun(t, g)
	step(t, runTestSession, run.ID)

	dispatch, ok := dispatchTo(t, "edit", "edit")
	if !ok {
		t.Fatal("no edit:edit dispatch reached the inbox")
	}
	if !strings.Contains(dispatch.Payload, verdictTokenInstruction) {
		t.Errorf("dispatch to edit carries no verdict instruction:\n%s", dispatch.Payload)
	}

	// Negative control: "seed everything" would pass the assertion above.
	completeSendNodeWithReply(t, runTestSession, run.ID, "c", "edit", "fixed them EXIT=0")
	for i := 0; i < 2; i++ {
		step(t, runTestSession, run.ID)
	}
	bdispatch, ok := dispatchTo(t, "build", "build")
	if !ok {
		t.Fatal("no build:build dispatch reached the inbox — the negative control never ran")
	}
	if strings.Contains(bdispatch.Payload, verdictTokenInstruction) {
		t.Errorf("build dispatch was seeded a token although its row evidences it:\n%s", bdispatch.Payload)
	}
}

// dispatchTo returns the graph's request to a role for an action. An inbox
// also carries run-lifecycle events (graph-run-created lands in edit's), so a
// dispatch is selected by action rather than by being the only message there.
func dispatchTo(t *testing.T, role, action string) (Message, bool) {
	t.Helper()
	msgs, _ := Peek(runTestSession, role)
	for _, m := range msgs {
		if m.Action == action && m.Type == "request" {
			return m, true
		}
	}
	return Message{}, false
}

// TestCommitPrReviewLoopPrecheckRouting drives the real template through the
// executor. The structural test asserts which edges exist; this asserts where
// a run actually goes, which is where the defect lived.
//
// git-manager.md tells the commit role to end a reply EXIT=1 when the
// requested state does not hold, naming PR existence as the example. Read that
// way a precheck answering NO-PR-FOUND fails its own node, and since only a
// success edge leaves it, the run dies before the condition that routes "no
// PR" to the commit gate ever evaluates — the template's main path,
// unreachable, with the structural test still green. The node messages
// override that default; these cases pin the routing it produces.
//
// The lookup-failure case is the negative control: EXIT=1 must still fail,
// or "always succeed" would satisfy the two cases above.
func TestCommitPrReviewLoopPrecheckRouting(t *testing.T) {
	cases := []struct {
		name      string
		reply     string
		reached   string // node the run must arrive at
		unreached string // node it must not have touched
		wantRun   string
	}{
		{
			name:      "an existing PR skips the commit gate",
			reply:     "PR-CONFIRMED https://example.test/pull/99 EXIT=0",
			reached:   "b",
			unreached: "gate1",
			wantRun:   GraphRunRunning,
		},
		{
			name:      "no PR routes to the commit gate",
			reply:     "NO-PR-FOUND EXIT=0",
			reached:   "gate1",
			unreached: "b",
			wantRun:   GraphRunRunning,
		},
		{
			name:    "an incomplete lookup picks no branch",
			reply:   "gh is unavailable, could not determine EXIT=1",
			wantRun: GraphRunFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := ParseGraph([]byte(builtinGraphJSON["commit-pr-review-loop"]))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			run := createTestRun(t, g)

			step(t, runTestSession, run.ID)
			if s := nodeState(t, runTestSession, run.ID, "pr-precheck"); s != GraphNodeRunning {
				t.Fatalf("pr-precheck state %q, want running — the run does not start at the precheck", s)
			}
			completeSendNodeWithReply(t, runTestSession, run.ID, "pr-precheck", "commit", tc.reply)
			for i := 0; i < 3; i++ {
				step(t, runTestSession, run.ID)
			}

			if tc.reached != "" {
				if s := nodeState(t, runTestSession, run.ID, tc.reached); s == GraphNodePending {
					t.Errorf("%s still pending — the run never reached it", tc.reached)
				}
				if s := nodeState(t, runTestSession, run.ID, tc.unreached); s != GraphNodePending {
					t.Errorf("%s state = %q, want pending — that branch should not have been taken", tc.unreached, s)
				}
			}
			got, err := ReadGraphRun(runTestSession, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.wantRun {
				t.Errorf("run state = %q, want %q", got.State, tc.wantRun)
			}
		})
	}
}

// TestLatestAuthoritativeRowFuncMixedRows covers what the single-row
// attribution cases cannot: accept is applied per candidate inside the same
// walk that ranks sources, so a bug in either can hide behind the other. With
// one row in the file, "filtered out" and "outranked" produce the same answer.
//
// Each case pairs a row that attributes with one that does not, across the two
// source ranks. The third is the one that would regress silently: rejecting an
// unrelated hook row must not also discard the self-report behind it, or every
// node whose role ran an unrelated command would hold forever.
func TestLatestAuthoritativeRowFuncMixedRows(t *testing.T) {
	row := func(cmd, outcome, source string, offset int64) HookHistoryEntry {
		code := "0"
		if outcome == OutcomeFailure {
			code = "1"
		}
		return HookHistoryEntry{TS: time.Now().Unix() + offset, Command: cmd,
			ExitCode: code, Outcome: outcome, Source: source}
	}
	cases := []struct {
		name string
		rows []HookHistoryEntry
		want string
	}{
		{
			name: "a newer unrelated success cannot bury an older matching failure",
			rows: []HookHistoryEntry{
				row("./build.sh", OutcomeFailure, "", 1),
				row("git push origin main", OutcomeSuccess, "", 2),
			},
			want: OutcomeFailure,
		},
		{
			name: "an observed failure outranks a newer matching self-report",
			rows: []HookHistoryEntry{
				row("./build.sh", OutcomeFailure, "", 1),
				row("./build.sh", OutcomeSuccess, SourceSelfReported, 2),
			},
			want: OutcomeFailure,
		},
		{
			name: "an unrelated observed failure leaves the matching self-report standing",
			rows: []HookHistoryEntry{
				row("git push origin main", OutcomeFailure, "", 1),
				row("./build.sh", OutcomeSuccess, SourceSelfReported, 2),
			},
			want: OutcomeSuccess,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempBusDir(t)
			if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
				t.Fatal(err)
			}
			since := time.Now().Unix() - 1
			for _, r := range tc.rows {
				if err := WriteHookHistory(HistoryPath(runTestSession, "build"), r, 100); err != nil {
					t.Fatal(err)
				}
			}
			accept := func(e ConsoleEntry) bool { return rowAttributesTo("build", e) }
			got, ok := latestAuthoritativeRowFunc(runTestSession, "build", since, accept)
			if !ok {
				t.Fatalf("no verdict, want %q", tc.want)
			}
			if got.Outcome != tc.want {
				t.Errorf("outcome = %q (%s), want %q", got.Outcome, got.Command, tc.want)
			}
		})
	}
}

// TestLatestAuthoritativeRowFuncNilAcceptTakesAnyRow is the negative control
// for the case above: with no accept the unrelated newer row wins on recency,
// so the filtered answers are the filter working rather than the fixture
// happening to hold only one usable row.
func TestLatestAuthoritativeRowFuncNilAcceptTakesAnyRow(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Unix() - 1
	path := HistoryPath(runTestSession, "build")
	now := time.Now().Unix()

	for _, r := range []HookHistoryEntry{
		{TS: now + 1, Command: "./build.sh", ExitCode: "1", Outcome: OutcomeFailure},
		{TS: now + 2, Command: "git push origin main", ExitCode: "0", Outcome: OutcomeSuccess},
	} {
		if err := WriteHookHistory(path, r, 100); err != nil {
			t.Fatal(err)
		}
	}

	got, ok := latestAuthoritativeRowFunc(runTestSession, "build", since, nil)
	if !ok || got.Outcome != OutcomeSuccess {
		t.Fatalf("row = (%+v, %v), want the unrelated newer success — nil accept takes any row", got, ok)
	}
}

// TestDeriveSendOutcomeSignals is the first test of deriveSendOutcome, which
// nothing exercised: its precedence was free to be fixed and equally free to
// regress unnoticed.
//
// The discriminating case is the last pair — an observed row and an agent
// verdict that contradict each other. Either read alone is a live defect: the
// row alone produced Defect 4's false failure, the agent's alone reopens the
// forgery road. The rows either side of it are the negative controls, without
// which "hold on everything" would pass.
func TestDeriveSendOutcomeSignals(t *testing.T) {
	cases := []struct {
		name     string
		row      string // observed outcome, "" for no row
		reply    string
		want     string
		wantFail bool // response action "error"
	}{
		{name: "observed success, no claim", row: OutcomeSuccess, reply: "built it", want: OutcomeSuccess},
		{name: "observed failure, no claim", row: OutcomeFailure, reply: "broke", want: OutcomeFailure},
		{name: "claim alone when nothing observed", reply: "green. EXIT=0", want: OutcomeSuccess},
		{name: "nonzero claim alone", reply: "EXIT=2", want: OutcomeFailure},
		{name: "neither signal", reply: "I had a look around", want: OutcomeUnknown},
		{name: "agreeing signals still route", row: OutcomeSuccess, reply: "green. EXIT=0", want: OutcomeSuccess},

		// The mirror (Defect 4): the classified run failed, the unclassified
		// re-run passed, and the agent says so. Neither signal can be trusted
		// over the other, so the node holds instead of driving a fix loop.
		{name: "observed failure contradicted by claim", row: OutcomeFailure, reply: "suite passed. EXIT=0", want: OutcomeUnknown},
		{name: "observed success contradicted by claim", row: OutcomeSuccess, reply: "could not do it. EXIT=1", want: OutcomeUnknown},

		{name: "error response is failure whatever else says", row: OutcomeSuccess, reply: "EXIT=0", want: OutcomeFailure, wantFail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempBusDir(t)
			if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
				t.Fatal(err)
			}
			since := time.Now().Unix() - 1

			if tc.row != "" {
				code := "0"
				if tc.row == OutcomeFailure {
					code = "1"
				}
				entry := HookHistoryEntry{TS: time.Now().Unix(), Command: "./build.sh",
					ExitCode: code, Outcome: tc.row}
				if err := WriteHookHistory(HistoryPath(runTestSession, "build"), entry, 100); err != nil {
					t.Fatal(err)
				}
			}

			action := "response"
			if tc.wantFail {
				action = "error"
			}
			resp := NewMessage("build", "edit", "response", action, tc.reply, "")
			if err := Send(runTestSession, resp); err != nil {
				t.Fatal(err)
			}

			n := &Node{ID: "a", Role: "build", Action: "build"}
			st := &GraphNodeStatus{StartedAt: since}
			got, output := deriveSendOutcome(runTestSession, n, st, Task{ResponseID: resp.ID})
			if got != tc.want {
				t.Errorf("outcome = %q, want %q (row %q, reply %q)", got, tc.want, tc.row, tc.reply)
			}
			if output != tc.reply {
				t.Errorf("output = %q, want the reply body %q", output, tc.reply)
			}
		})
	}
}

// TestDeriveSendOutcomeAttributesRowToAction pins the constraint the conflict
// rule alone did not meet: the signal must be tied to the dispatched task, not
// to whichever commands happened to be recognised.
//
// Row 1 is the 2026-09-03 shape that opened this spec — a node asked to answer
// PR comments, recorded success because the commit role had run a git command
// after dispatch. Rows 2 and 5 are the negative controls without which
// "refuse every row" would pass: the agent's own token still attributes a
// comment node, and an action in neither table still routes on its row, so
// `run` and `watch` nodes do not become permanent holds.
func TestDeriveSendOutcomeAttributesRowToAction(t *testing.T) {
	cases := []struct {
		name    string
		action  string
		command string
		row     string
		reply   string
		want    string
	}{
		{name: "git row cannot answer for a comment node", action: "comment",
			command: "git commit -m 'wip'", row: OutcomeSuccess,
			reply: "I did not reply to the comments.", want: OutcomeUnknown},
		{name: "the agent's token still attributes a comment node", action: "comment",
			command: "git commit -m 'wip'", row: OutcomeSuccess,
			reply: "comments answered. EXIT=0", want: OutcomeSuccess},
		{name: "a git row cannot answer for a build node", action: "build",
			command: "git push origin main", row: OutcomeSuccess,
			reply: "no build was run", want: OutcomeUnknown},
		{name: "a build row answers for a build node", action: "build",
			command: "./build.sh", row: OutcomeSuccess, reply: "built", want: OutcomeSuccess},
		{name: "an unmapped action still routes on its row", action: "run",
			command: "cat notes.txt", row: OutcomeSuccess, reply: "ran it", want: OutcomeSuccess},
		{name: "a precheck row answers for a test node", action: "test",
			command: "go vet ./...", row: OutcomeFailure, reply: "vet broke", want: OutcomeFailure},
		{name: "a build row cannot answer for a test node", action: "test",
			command: "./build.sh", row: OutcomeFailure, reply: "nothing to say", want: OutcomeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempBusDir(t)
			if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
				t.Fatal(err)
			}
			since := time.Now().Unix() - 1

			code := "0"
			if tc.row == OutcomeFailure {
				code = "1"
			}
			entry := HookHistoryEntry{TS: time.Now().Unix(), Command: tc.command,
				ExitCode: code, Outcome: tc.row}
			if err := WriteHookHistory(HistoryPath(runTestSession, "commit"), entry, 100); err != nil {
				t.Fatal(err)
			}

			resp := NewMessage("commit", "edit", "response", "response", tc.reply, "")
			if err := Send(runTestSession, resp); err != nil {
				t.Fatal(err)
			}

			n := &Node{ID: "a", Role: "commit", Action: tc.action}
			st := &GraphNodeStatus{StartedAt: since}
			got, _ := deriveSendOutcome(runTestSession, n, st, Task{ResponseID: resp.ID})
			if got != tc.want {
				t.Errorf("outcome = %q, want %q (action %q, row %q from %q)",
					got, tc.want, tc.action, tc.row, tc.command)
			}
		})
	}
}

// TestWriteHookHistoryStampsSource pins the choke point. Promoting a declared
// source would silently re-authorise the bus-response rows that must never
// carry a verdict, so both directions are checked.
func TestWriteHookHistoryStampsSource(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	path := HistoryPath(runTestSession, "build")

	undeclared := HookHistoryEntry{TS: time.Now().Unix(), Command: "./build.sh",
		ExitCode: "0", Outcome: OutcomeSuccess}
	synthesized := HookHistoryEntry{TS: time.Now().Unix() + 1, Action: "build",
		Outcome: OutcomeSuccess, Source: SourceBusResponse}
	selfReported := HookHistoryEntry{TS: time.Now().Unix() + 2, Command: "pnpm test",
		ExitCode: "0", Outcome: OutcomeSuccess, Source: SourceSelfReported}
	for _, e := range []HookHistoryEntry{undeclared, synthesized, selfReported} {
		if err := WriteHookHistory(path, e, 100); err != nil {
			t.Fatal(err)
		}
	}

	entries := ReadConsoleEntries(path, 0)
	if len(entries) != 3 {
		t.Fatalf("read %d entries, want 3", len(entries))
	}
	want := []string{SourceHook, SourceBusResponse, SourceSelfReported}
	for i, w := range want {
		if entries[i].Source != w {
			t.Errorf("entry %d source = %q, want %q", i, entries[i].Source, w)
		}
	}
}

// TestExecSendOutcomeHoldsOnContradiction pins the precedence where it runs.
// TestDeriveSendOutcomeSignals calls the helper directly, so it stays green
// if routeFinishedNodes stops consulting it; only this one reads the outcome
// the executor actually recorded on the node.
//
// The uncontradicted row is the negative control: without it an executor that
// resolved every send node to unknown would pass.
func TestExecSendOutcomeHoldsOnContradiction(t *testing.T) {
	cases := []struct {
		name     string
		sentinel string
		want     string
	}{
		{"agent's verdict contradicts the row", "all good EXIT=0", OutcomeUnknown},
		{"agent claims no verdict of its own", "could not build it", OutcomeFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := createTestRun(t, linearGraph())

			step(t, runTestSession, run.ID)
			completeSendNodeSentinel(t, runTestSession, run.ID, "a", tc.sentinel)
			row := HookHistoryEntry{TS: time.Now().Unix() + 1, Command: "./build.sh",
				ExitCode: "1", Outcome: OutcomeFailure}
			if err := WriteHookHistory(HistoryPath(runTestSession, "build"), row, 100); err != nil {
				t.Fatal(err)
			}
			step(t, runTestSession, run.ID)

			st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
			if st.Outcome != tc.want {
				t.Errorf("a outcome %q, want %q (observed failure, reply %q)",
					st.Outcome, tc.want, tc.sentinel)
			}
		})
	}
}

// gateRequestPayloads returns every graph-approval request edit received for a
// run.
func gateRequestPayloads(t *testing.T, runID string) []string {
	t.Helper()
	msgs, _ := Peek(runTestSession, "edit")
	var out []string
	for _, m := range msgs {
		if m.Action == "graph-approval" && strings.Contains(m.Payload, runID) {
			out = append(out, m.Payload)
		}
	}
	return out
}

// An agent deciding whether to raise a gate with a person reads only this
// message. Leaving the launcher out of it is what made one refuse a gate as an
// "auto-launched run" the user had started by hand a minute earlier.
func TestExecGateRequestNamesLauncher(t *testing.T) {
	gated := &Graph{
		Name: "t", Start: "gate",
		Nodes: []Node{
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "b", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
		},
		Edges: []Edge{{From: "gate", To: "b"}},
	}
	cases := []struct {
		name  string
		actor string
		want  string
	}{
		{"manual launch", "", "launched by: the user, by hand"},
		// Negative control: without it, a runProvenance hardcoded to the manual
		// string would pass the case above and mislabel every autonomous run.
		{"autonomous launch", "auto", "launched by: auto (autonomous)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinActor(t, tc.actor)
			run := createTestRun(t, gated)
			step(t, runTestSession, run.ID)

			payloads := gateRequestPayloads(t, run.ID)
			if len(payloads) == 0 {
				t.Fatal("no graph-approval request reached edit")
			}
			for _, p := range payloads {
				if !strings.Contains(p, tc.want) {
					t.Errorf("gate request %q does not carry %q", p, tc.want)
				}
			}
		})
	}
}

func TestExecUnknownRoutesAfterApproval(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	if err := ApproveGraphGate(runTestSession, run.ID, "a"); err != nil {
		t.Fatalf("ApproveGraphGate: %v", err)
	}
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Errorf("b state %q, want running — approval must release the held node", s)
	}
	// Single-use, like a wait_human gate.
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "a", "approved")); !os.IsNotExist(err) {
		t.Error("approval marker must be purged on release so a re-entry asks again")
	}
}

// forgeApproval writes an approval marker directly, the way a process that
// cannot obtain one from ApproveGraphGate would.
//
// Since MUX-144 Phase 2 the approve path refuses every approver below, so
// driving these through it would test the outer gate three times and the
// daemon-side check not at all. Forgery is precisely the threat this inner
// layer exists for: the marker is a file, and anything that can run a shell can
// write one. Where the identities below come from — a role, an agent that
// stripped AGENT_ROLE, a probe that failed — is pinned in gate_authority_test.go.
func forgeApproval(t *testing.T, runID, nodeID, by string) {
	t.Helper()
	if err := os.MkdirAll(graphApprovalsDir(runTestSession, runID), 0755); err != nil {
		t.Fatalf("approvals dir: %v", err)
	}
	if err := atomicWriteJSON(graphApprovalPath(runTestSession, runID, nodeID, "approved"),
		map[string]any{"approved_at": time.Now().Unix(), "approved_by": by}); err != nil {
		t.Fatalf("forge approval: %v", err)
	}
}

// forgeAuditedApproval forges the marker AND the graph-gate-approved row
// ApproveGraphGate would have logged beside it, both on one second.
//
// This is the approval the daemon is meant to honour, reached without the
// approve path, so it isolates what the daemon actually checks.
func forgeAuditedApproval(t *testing.T, runID, nodeID, by string) {
	t.Helper()
	at := time.Now().Unix()
	LogLifecycleAt(runTestSession, "info", by, "graph-gate-approved",
		fmt.Sprintf("Graph run %s gate %q approved by %s", runID, nodeID, by), at)
	if err := os.MkdirAll(graphApprovalsDir(runTestSession, runID), 0755); err != nil {
		t.Fatalf("approvals dir: %v", err)
	}
	if err := atomicWriteJSON(graphApprovalPath(runTestSession, runID, nodeID, "approved"),
		map[string]any{"approved_at": at, "approved_by": by}); err != nil {
		t.Fatalf("forge audited approval: %v", err)
	}
}

// holdOutcome drives a node to an unverified hold, lets `grant` write whatever
// approval it likes, and reports the successor's state.
func holdOutcome(t *testing.T, grant func(runID string)) string {
	t.Helper()
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	grant(run.ID)
	step(t, runTestSession, run.ID)

	return nodeState(t, runTestSession, run.ID, "b")
}

// refuseForgedHold drives a node to an unverified hold, forges an uncorroborated
// approval from `by`, and reports the successor's state.
func refuseForgedHold(t *testing.T, by string) string {
	t.Helper()
	return holdOutcome(t, func(runID string) { forgeApproval(t, runID, "a", by) })
}

// The hold is only worth having if the agents that raised it cannot clear it:
// an autonomous run that cleared its own unverified work would let unknown pass
// as success again.
func TestExecUnverifiedHoldRefusesAgentApproval(t *testing.T) {
	if s := refuseForgedHold(t, "build"); s != GraphNodePending {
		t.Errorf("b state %q, want pending — an agent must not release its own unverified hold", s)
	}
}

// An agent that strips AGENT_ROLE is recorded by its runtime instead, so the
// marker names `claude` rather than a role. The comparison is against
// personhood, not against a list of roles, or that name would sail through it.
func TestExecUnverifiedHoldRefusesStrippedIdentityApproval(t *testing.T) {
	if s := refuseForgedHold(t, "claude"); s != GraphNodePending {
		t.Errorf("b state %q, want pending — stripping AGENT_ROLE must not launder an agent into a person", s)
	}
}

// The ancestry check runs `ps` off PATH, so the cheapest attack on it is not
// breaking the probe but supplying a failing one. It fails closed to
// ActorUnknown, and reading that as a person here would restore the whole
// bypass behind a check that looks present.
func TestExecUnverifiedHoldRefusesWhenAncestryUnreadable(t *testing.T) {
	if s := refuseForgedHold(t, ActorUnknown); s != GraphNodePending {
		t.Errorf("b state %q, want pending — an unidentified approver must not release a hold", s)
	}
}

// Naming a person is not enough either: an uncorroborated marker is refused
// whatever identity it claims, or forgery would only need the right string.
func TestExecUnverifiedHoldRefusesUnauditedUserApproval(t *testing.T) {
	if s := refuseForgedHold(t, ActorUser); s != GraphNodePending {
		t.Errorf("b state %q, want pending — a marker with no audit row must not release the hold", s)
	}
}

// Positive control for the four refusals above, and the one assertion that
// discriminates. They share forgeApproval and a path through the executor: a
// helper writing to the wrong path, or an approvalHasAudit hardcoded to false,
// leaves all four passing on a node that was never approved at all. This grant
// differs from the one directly above by exactly the audit row, so it fails if
// the marker never lands where the daemon reads it, and it fails if a
// corroborated person's approval cannot get through.
func TestExecUnverifiedHoldReleasedByAuditedUserApproval(t *testing.T) {
	s := holdOutcome(t, func(runID string) { forgeAuditedApproval(t, runID, "a", ActorUser) })
	if s != GraphNodeRunning {
		t.Errorf("b state %q, want running — a corroborated person's approval must release the hold", s)
	}
}

// gateSuccessorGraph sends into a human gate, the shape every read-only node
// feeding an approval takes (commit-pr-review-loop's b -> gate2).
func gateSuccessorGraph() *Graph {
	return &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
		},
		Edges: []Edge{{From: "a", To: "gate"}},
	}
}

// Holding a node whose only successor is a human gate charges two approvals to
// reach one decision, with nothing mutable in between. The gate is the hold.
func TestExecUnverifiedHoldExemptWhenNextIsHumanGate(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, gateSuccessorGraph())

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Errorf("gate state %q, want waiting — an unknown node feeding a gate must route without its own approval", s)
	}
}

// Negative control for the exemption: the same unknown outcome one node earlier
// in a chain that ends in a send still holds. Without this a helper that always
// exempted would pass the test above and silently restore unknown-as-success.
func TestExecUnverifiedHoldAppliesWhenNextIsSend(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodePending {
		t.Errorf("b state %q, want pending — a send successor is not a human gate", s)
	}
}

// A gate among the successors is not a gate on all of them: the send branch
// would advance unverified while the person was still looking at the gate.
func TestExecUnverifiedHoldAppliesWhenGateIsNotTheOnlySuccessor(t *testing.T) {
	pinActor(t, "")
	g := gateSuccessorGraph()
	g.Nodes = append(g.Nodes, Node{ID: "c", Type: NodeSend, Role: "test", Action: "test", Message: "go"})
	g.Edges = append(g.Edges, Edge{From: "a", To: "c"})
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Errorf("c state %q, want pending — a mixed fan-out must hold, not advance its send branch", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodePending {
		t.Errorf("gate state %q, want pending — the gate branch waits on the hold too", s)
	}
}

// Pins the `found` flag: a node with no success edge at all vacuously satisfies
// "every success edge lands on a gate". Exempting it would route nothing and end
// the run without a word, so it is held instead — asking beats stalling silently.
func TestExecUnverifiedHoldAppliesWhenNoSuccessEdge(t *testing.T) {
	pinActor(t, "")
	g := gateSuccessorGraph()
	g.Edges = []Edge{{From: "a", To: "gate", Outcome: OutcomeFailure}}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "a", "pending")); err != nil {
		t.Errorf("no pending hold marker for a: %v — a node with no success edge must ask, not vanish", err)
	}
}

// The hold parks a send node in Done, so a queue built from node type and state
// finds nothing to approve and the run stalls behind an empty screen. Listing
// the markers is what makes it reachable.
func TestListUnverifiedHolds(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	holds := ListUnverifiedHolds(runTestSession, run.ID)
	if len(holds) != 1 || holds[0].NodeID != "a" {
		t.Fatalf("holds = %+v, want exactly node a", holds)
	}
	if holds[0].Message == "" {
		t.Error("a hold with no message leaves the queue nothing to explain")
	}

	if err := ApproveGraphGate(runTestSession, run.ID, "a"); err != nil {
		t.Fatalf("ApproveGraphGate: %v", err)
	}
	if h := ListUnverifiedHolds(runTestSession, run.ID); len(h) != 0 {
		t.Errorf("holds = %+v after approval, want none — a released hold must leave the queue", h)
	}
}

// Negative control: a wait_human gate writes a pending marker of its own, and
// the queue already lists those by node type. Counting them here would show
// every gate twice. The exemption also means node a raises no hold at all.
func TestListUnverifiedHoldsExcludesWaitHumanGate(t *testing.T) {
	pinActor(t, "")
	run := createTestRun(t, gateSuccessorGraph())

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q — the fixture never armed a gate, so its marker cannot be tested", s)
	}
	if h := ListUnverifiedHolds(runTestSession, run.ID); len(h) != 0 {
		t.Errorf("holds = %+v, want none — a wait_human pending marker is not an unverified hold", h)
	}
}

// An explicit unknown edge is the graph author saying what unknown means here,
// so it routes directly and never holds.
func TestExecUnknownEdgeSkipsHold(t *testing.T) {
	g := linearGraph()
	g.Edges = []Edge{{From: "a", To: "b", Outcome: OutcomeUnknown}}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "")
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Errorf("b state %q, want running — an explicit unknown edge must route without approval", s)
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "a", "pending")); !os.IsNotExist(err) {
		t.Error("an explicitly routed unknown outcome must not be held")
	}
}

func TestExecFanOutJoinAll(t *testing.T) {
	g := joinGraph(JoinAll, 0)
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)

	// a dispatches; complete it; fan-out arms both spawns.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if len(*tasks) != 2 {
		t.Fatalf("expected 2 spawned workers, got %d: %v", len(*tasks), *tasks)
	}

	// Fake spawns complete instantly, so the next ticks harvest both,
	// pass the join barrier, and run the join + downstream send.
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodeDone {
		t.Fatalf("join state %q, want done", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Fatalf("c state %q, want running after join", s)
	}
}

// TestExecJoinQuorumBarrier drives a quorum join entirely through the
// executor: two send-node branches complete one at a time, and the join
// must hold at 1/2 and release at 2/2.
//
// The branches are attributed by different roads on purpose. b1 is a test
// node, which a classified command row can testify for; b2 is a review node,
// which none can (actionsWithoutCommandEvidence), so only its agent's token
// establishes the outcome. The barrier counts fires and must not care which
// road produced them.
func TestExecJoinQuorumBarrier(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "b1", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
			{ID: "b2", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
			{ID: "j", Type: NodeJoin, Join: JoinQuorum, Quorum: 2},
			{ID: "c", Type: NodeSend, Role: "deploy", Action: "deploy", Message: "go"},
		},
		Edges: []Edge{
			{From: "a", To: "b1"},
			{From: "a", To: "b2"},
			{From: "b1", To: "j"},
			{From: "b2", To: "j"},
			{From: "j", To: "c"},
		},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)

	// One branch completes: quorum 1/2 — the barrier must hold.
	completeSendNode(t, runTestSession, run.ID, "b1", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	// "j pending" is equally true at 0/2, so b1 must be shown to have routed.
	b1st, err := ReadNodeStatus(runTestSession, run.ID, "b1")
	if err != nil {
		t.Fatal(err)
	}
	if !b1st.Routed {
		t.Fatalf("b1 never routed (%+v) — the 1/2 check below would pass vacuously", b1st)
	}
	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodePending {
		t.Fatalf("join state %q with quorum 1/2, want pending", s)
	}
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunRunning {
		t.Fatalf("run state %q with a branch still running, want running", got.State)
	}

	// Second branch completes: quorum met, join runs, downstream fires.
	// Its agent's token, no row — a review node's work leaves no command
	// behind that could testify for it.
	completeSendNodeSentinel(t, runTestSession, run.ID, "b2", "Review: 0 must-fix, 0 should-fix, 0 nits — no findings. EXIT=0")
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodeDone {
		t.Errorf("join state %q, want done once quorum met", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("c state %q, want running after join", s)
	}
}

// TestExecJoinReleasesOnUnknownOutcomes pins the hookless-provider shape
// the integration test caught: branches finish with outcome "unknown"
// (no authoritative history rows), route via the unknown→success
// fallback, and the join barrier must count those fires and release —
// not re-derive outcomes and deadlock.
func TestExecJoinHoldsUntilUnknownMembersApproved(t *testing.T) {
	pinActor(t, "")
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "b1", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
			{ID: "b2", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
			{ID: "j", Type: NodeJoin, Join: JoinAll},
			{ID: "c", Type: NodeSend, Role: "deploy", Action: "deploy", Message: "go"},
		},
		Edges: []Edge{
			{From: "a", To: "b1"},
			{From: "a", To: "b2"},
			{From: "b1", To: "j"},
			{From: "b2", To: "j"},
			{From: "j", To: "c"},
		},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", "") // unknown outcome
	step(t, runTestSession, run.ID)
	if err := ApproveGraphGate(runTestSession, run.ID, "a"); err != nil {
		t.Fatalf("approve a: %v", err)
	}
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b1", "")
	completeSendNode(t, runTestSession, run.ID, "b2", "")
	step(t, runTestSession, run.ID)

	// The barrier must not release on unverified members alone.
	if s := nodeState(t, runTestSession, run.ID, "j"); s == GraphNodeDone {
		t.Error("join released while both members were unverified — unknown must not satisfy a barrier by itself")
	}

	for _, id := range []string{"b1", "b2"} {
		if err := ApproveGraphGate(runTestSession, run.ID, id); err != nil {
			t.Fatalf("approve %s: %v", id, err)
		}
	}
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodeDone {
		t.Errorf("join state %q, want done — approved unknown members must count toward the barrier", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("c state %q, want running after join release", s)
	}
}

func TestExecCappedLoopExhaustion(t *testing.T) {
	// a → b; b failure loops back to a, capped at 2 iterations.
	g := linearGraph()
	g.Edges = append(g.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure, MaxIterations: 2})
	run := createTestRun(t, g)

	for i := 0; i < 2; i++ {
		step(t, runTestSession, run.ID)
		completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
		step(t, runTestSession, run.ID)
		completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
		step(t, runTestSession, run.ID)
		if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeRunning {
			t.Fatalf("iteration %d: a state %q, want running (loop re-armed + dispatched)", i, s)
		}
	}

	// Third failure: loop edge exhausted → failure has no live edge → run fails.
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
	step(t, runTestSession, run.ID)

	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Errorf("run state %q, want failed after loop exhaustion", got.State)
	}
}

func TestExecCancelMidRun(t *testing.T) {
	run := createTestRun(t, linearGraph())
	step(t, runTestSession, run.ID)

	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunCanceled {
		t.Fatalf("run state %q, want canceled", got.State)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeSkipped {
		t.Errorf("b state %q, want skipped", s)
	}

	// Further ticks must not dispatch or settle a canceled run.
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	got, _ = ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunCanceled {
		t.Errorf("run state %q after tick, want canceled", got.State)
	}
}

func TestExecHumanGate(t *testing.T) {
	pinActor(t, "")
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve ${intent}"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q, want waiting", s)
	}
	// Gate notified edit.
	edit, _ := Peek(runTestSession, "edit")
	var gates int
	for _, m := range edit {
		if m.Action == "graph-approval" && strings.Contains(m.Payload, run.ID) {
			gates++
		}
	}
	if gates != 1 {
		t.Fatalf("edit received %d graph-approval requests, want 1", gates)
	}
	// The gated commit node must not have fired.
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q, want pending while gate blocks", s)
	}

	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeDone {
		t.Errorf("gate state %q, want done after approval", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("c state %q, want running after approval", s)
	}
}

// TestExecHumanGateRetryRequiresFreshApproval pins the stale-marker gate
// A suppressed re-dispatch with NO live task must adopt the QUEUED
// duplicate's message id — the agent answers that id, so a task keyed to
// the unsent fresh id would sit in-flight forever and time the node out
// (PR #38 Copilot finding).
func TestExecSuppressedDispatchAdoptsQueuedMessageID(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"}},
		Edges: []Edge{},
	}
	run := createTestRun(t, g)
	step(t, runTestSession, run.ID) // dispatch: message queued, task created

	msgs, _ := Peek(runTestSession, "build")
	if len(msgs) != 1 {
		t.Fatalf("expected the dispatched message queued, got %d", len(msgs))
	}
	queuedID := msgs[0].ID

	// Complete the first pass's task (no in-flight task remains), then
	// re-arm the node — the loop/retry shape.
	CompleteTask(runTestSession, queuedID, "resp-"+queuedID)
	mustNodeTransition(t, run.ID, "a", GraphNodeDone)
	mustNodeTransition(t, run.ID, "a", GraphNodeReady)

	step(t, runTestSession, run.ID) // re-dispatch: suppressed by the queued duplicate

	st, err := ReadNodeStatus(runTestSession, run.ID, "a")
	if err != nil {
		t.Fatalf("ReadNodeStatus: %v", err)
	}
	if st.TaskID != queuedID {
		t.Errorf("suppressed dispatch must adopt the queued id %s, got %s", queuedID, st.TaskID)
	}
	task, err := ReadTask(runTestSession, queuedID)
	if err != nil || task.Status != TaskInFlight {
		t.Errorf("adopted task must be re-armed in-flight, got %+v err %v", task, err)
	}
}

func mustNodeTransition(t *testing.T, runID, node, state string) {
	t.Helper()
	if err := TransitionGraphNode(runTestSession, runID, node, state, nil); err != nil {
		t.Fatalf("transition %s -> %s: %v", node, state, err)
	}
}

// Gate surfacing belongs to the control pane (MUX-108): dispatching a
// wait_human never opens a popup — the graph modals were removed with
// the pane's arrival, and the pane switches itself to Pending Gates.
func TestExecHumanGateNeverPopsModal(t *testing.T) {
	gated := &Graph{
		Name: "t", Start: "gate",
		Nodes: []Node{
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "b", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
		},
		Edges: []Edge{{From: "gate", To: "b"}},
	}

	orig := tmuxRunner
	var calls [][]string
	tmuxRunner = func(args ...string) error { calls = append(calls, args); return nil }
	t.Cleanup(func() { tmuxRunner = orig })

	run := createTestRun(t, gated)
	step(t, runTestSession, run.ID)
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), "display-popup") {
			t.Errorf("gate dispatch must never open a popup, calls: %v", calls)
		}
	}
}

// bypass (PR #34 Copilot must-fix): after a gate was approved once, a
// graph retry --from that gate must WAIT for a new approval — the old
// approved marker must not auto-release the fresh pass.
func TestExecHumanGateRetryRequiresFreshApproval(t *testing.T) {
	pinActor(t, "")
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	// First pass: run to completion through an approved gate.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "c", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunComplete {
		t.Fatalf("run state %q, want complete", got.State)
	}

	// Retry from the gate: it must wait for a NEW approval.
	if _, err := RetryGraphRun(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q after retry, want waiting — stale approval must not auto-release", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q after retry, want pending", s)
	}

	// A fresh approval releases it.
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("re-approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeDone {
		t.Errorf("gate state %q after fresh approval, want done", s)
	}
}

// TestExecRetryBelowGateRearmsGate is the MUX-132 Phase 2 acceptance
// test — the Phase 1 characterization test
// (TestExecRetryBelowGateConsumesStaleApproval) with its assertions
// inverted, updated rather than deleted so the hole cannot silently
// return. A retry whose --from target sits below a satisfied wait_human
// gate must re-target to the gate: re-arm it, purge the stale approval,
// leave the gated node un-fired, and ask edit for a SECOND approval —
// never resume on an approval granted for different content (observed
// 2026-08-31: retry --from commit after the tree changed post-approval).
// TestExecHumanGateRetryRequiresFreshApproval covers the retry that
// re-enters the gate itself; this is the sibling path below it.
func TestExecRetryBelowGateRearmsGate(t *testing.T) {
	pinActor(t, "")
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	// First pass: gate approved, then the gated node fails (the incident shape).
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	// Edit consumes the approval request before the human approves — the
	// live sequence. Left pending, the inbox dedup guard would rightly
	// suppress the identical re-ask (the pending one still asks for it).
	edit, _ := Peek(runTestSession, "edit")
	for _, m := range edit {
		if m.Action == "graph-approval" {
			_, _ = ConsumeByID(runTestSession, "edit", m.ID)
		}
	}
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "c", OutcomeFailure)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed after c fails", got.State)
	}

	// Retry from c — below the gate: the retry must re-target to the gate.
	res, err := RetryGraphRun(runTestSession, run.ID, "c")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(res.Rearmed) != 1 || res.Rearmed[0].Gate != "gate" || res.From != "gate" || res.Requested != "c" {
		t.Fatalf("retry result rearmed=%+v from=%q requested=%q, want re-target to the gate", res.Rearmed, res.From, res.Requested)
	}
	if res.Rearmed[0].ApprovedAt <= 0 {
		t.Errorf("retry result carries no original approval time — the re-target must name it")
	}
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeReady {
		t.Fatalf("gate state %q after retry below it, want ready — the gate re-arms", s)
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "gate", "approved")); !os.IsNotExist(err) {
		t.Fatalf("approved marker still present (err=%v) — the stale approval must be purged at retry time", err)
	}
	got, _ = ReadGraphRun(runTestSession, run.ID)
	if !strings.Contains(got.RetryNote, `"gate"`) {
		t.Errorf("run RetryNote %q does not name the re-armed gate — the decision must be visible in graph status", got.RetryNote)
	}

	// Next tick: the gate dispatches and waits; the gated node must NOT fire.
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q after tick, want waiting", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q after tick, want pending — it must not re-fire on the stale approval", s)
	}
	// The session log records every send: the re-armed gate must have
	// asked edit a SECOND time.
	logged, _ := readMessages(LogPath(runTestSession))
	var approvals int
	for _, m := range logged {
		if m.Action == "graph-approval" && strings.Contains(m.Payload, run.ID) {
			approvals++
		}
	}
	if approvals != 2 {
		t.Fatalf("edit received %d graph-approval requests for this run, want exactly 2 — the retry must ask again", approvals)
	}

	// Only a fresh approval releases the gated node.
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("re-approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Fatalf("c state %q after fresh approval, want running", s)
	}
}

// TestExecRetryPurgeFailureFailsClosed pins the purge's error contract
// (PR #56 review): when the stale approval marker cannot be removed, the
// retry must REFUSE — an error return, marker still on disk, gate not
// re-armed — never proceed while logging "purged". A silent failure
// leaves the marker to satisfy the re-armed gate: the laundered
// approval MUX-132 closed, reintroduced through the filesystem.
func TestExecRetryPurgeFailureFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — directory permissions cannot block os.Remove")
	}
	pinActor(t, "")
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "c", OutcomeFailure)
	step(t, runTestSession, run.ID)

	// Make the marker un-removable: unlinking needs write on the parent.
	approvals := graphApprovalsDir(runTestSession, run.ID)
	if err := os.Chmod(approvals, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(approvals, 0755) })

	if _, err := RetryGraphRun(runTestSession, run.ID, "c"); err == nil {
		t.Fatal("retry succeeded with an un-removable stale approval — must fail closed")
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "gate", "approved")); err != nil {
		t.Fatalf("approved marker missing after refused retry (err=%v) — refusal must leave state untouched", err)
	}
	if s := nodeState(t, runTestSession, run.ID, "gate"); s == GraphNodeReady {
		t.Fatal("gate re-armed despite the refused purge — the retry must leave the run untouched")
	}

	// Positive control: with the permission restored the same retry
	// purges and re-arms — the refusal above was the chmod, not a
	// broken re-arm path.
	if err := os.Chmod(approvals, 0755); err != nil {
		t.Fatalf("restore chmod: %v", err)
	}
	res, err := RetryGraphRun(runTestSession, run.ID, "c")
	if err != nil {
		t.Fatalf("retry after restore: %v", err)
	}
	if len(res.Rearmed) != 1 || res.Rearmed[0].Gate != "gate" {
		t.Fatalf("rearmed=%+v, want the gate — positive control", res.Rearmed)
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "gate", "approved")); !os.IsNotExist(err) {
		t.Fatalf("approved marker survived the successful retry (err=%v)", err)
	}
}

// TestExecDispatchPurgeFailureFailsClosed pins the dispatch-time purge's
// error contract (MUX-132 post-close finding): when a stale approved
// marker cannot be removed as the gate arms, the node must FAIL — never
// reach waiting, where harvestWaitingNode would release it on the
// surviving marker and relaunder the approval through the dispatch door.
func TestExecDispatchPurgeFailureFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — directory permissions cannot block os.Remove")
	}
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)

	// Plant a stale marker, then make it un-removable before the gate dispatches.
	approvals := graphApprovalsDir(runTestSession, run.ID)
	if err := os.MkdirAll(approvals, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(graphApprovalPath(runTestSession, run.ID, "gate", "approved"), []byte("{}"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if err := os.Chmod(approvals, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(approvals, 0755) })

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeFailed {
		t.Fatalf("gate state %q after failed purge, want failed — never waiting on a stale approval", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q, want pending — the gated node must not fire", s)
	}
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed — the refused purge must surface loudly", got.State)
	}

	// Positive control: restored permission → retry re-arms, dispatch purges and waits.
	if err := os.Chmod(approvals, 0755); err != nil {
		t.Fatalf("restore chmod: %v", err)
	}
	if _, err := RetryGraphRun(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q after restore, want waiting — positive control", s)
	}
	if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, "gate", "approved")); !os.IsNotExist(err) {
		t.Fatalf("approved marker survived dispatch (err=%v) — the purge must remove it", err)
	}
}

// TestExecRetryBelowParallelGateCutRearmsAll pins the cut form of the
// re-arm (review finding, 2026-08-31): with the target fed by two
// parallel branches each behind its own satisfied gate, NO single gate
// dominates every path — a dominator-only check finds nothing and both
// stale approvals stay usable. The re-arm set must be the nearest-gate
// cut: every satisfied gate whose territory contains the target, all
// re-armed, all markers purged, target left pending.
func TestExecRetryBelowParallelGateCutRearmsAll(t *testing.T) {
	pinActor(t, "")
	g := &Graph{
		Name:  "t",
		Start: "s",
		Nodes: []Node{
			{ID: "s", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "g1", Type: NodeWaitHuman, Message: "approve branch one commit"},
			{ID: "g2", Type: NodeWaitHuman, Message: "approve branch two commit"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{
			{From: "s", To: "g1"}, {From: "s", To: "g2"},
			{From: "g1", To: "c"}, {From: "g2", To: "c"},
		},
	}
	run := createTestRun(t, g)

	// Drive both branches through their gates, then fail the target.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "s", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if err := ApproveGraphGate(runTestSession, run.ID, "g1"); err != nil {
		t.Fatalf("approve g1: %v", err)
	}
	if err := ApproveGraphGate(runTestSession, run.ID, "g2"); err != nil {
		t.Fatalf("approve g2: %v", err)
	}
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "c", OutcomeFailure)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed after c fails", got.State)
	}

	res, err := RetryGraphRun(runTestSession, run.ID, "c")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	rearmed := map[string]bool{}
	for _, r := range res.Rearmed {
		rearmed[r.Gate] = true
	}
	if len(res.Rearmed) != 2 || !rearmed["g1"] || !rearmed["g2"] {
		t.Fatalf("rearmed=%+v, want both parallel gates — a dominator-only check re-arms neither", res.Rearmed)
	}
	for _, gate := range []string{"g1", "g2"} {
		if s := nodeState(t, runTestSession, run.ID, gate); s != GraphNodeReady {
			t.Errorf("%s state %q after retry, want ready", gate, s)
		}
		if _, err := os.Stat(graphApprovalPath(runTestSession, run.ID, gate, "approved")); !os.IsNotExist(err) {
			t.Errorf("%s approved marker still present (err=%v) — its stale approval remains usable", gate, err)
		}
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
		t.Fatalf("c state %q after retry, want pending — it must not fire on either stale approval", s)
	}
	got, _ = ReadGraphRun(runTestSession, run.ID)
	if !strings.Contains(got.RetryNote, `"g1"`) || !strings.Contains(got.RetryNote, `"g2"`) {
		t.Errorf("run RetryNote %q does not name both re-armed gates", got.RetryNote)
	}
}

// TestExecRetryBelowNeverApprovedGateUnaffected is the MUX-132 Phase 3
// negative control for the re-arm's precondition: the cut re-arms gates
// whose STALE approval a retry would consume — a gate that never reached
// done holds no approval to go stale, so a retry below it must not
// re-target, must leave the gate untouched, and must not demand an
// approval that was never part of the run. This is the only assertion on
// staleApprovalGates' done/success check: without it a re-arm-
// unconditional mutant passes the rest of the suite.
//
// It also pins a second laundering path that MUX-144 Phase 4 closed, and which
// this test previously asserted was open: `retry --from c` targets a commit node
// BELOW a gate nobody ever approved, so the re-arm correctly finds no stale
// approval to purge and the run walks straight past an unopened gate into an
// irreversible action. MUX-132 guards "is this approval current?"; nothing on
// the retry road asks "was there one at all?". The runtime backstop now refuses
// the dispatch, so the node fails rather than committing. Whether the retry
// should instead re-arm an unsatisfied gate — failing at retry time with a
// clearer message than a failed node — is an open design question recorded on
// MUX-144, not settled here.
func TestExecRetryBelowNeverApprovedGateUnaffected(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "c", Type: NodeSend, Role: "commit", Action: "commit", Message: "ship it"},
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "c"}},
	}
	run := createTestRun(t, g)

	// Drive to the gate and leave it waiting — no approval ever granted.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q before cancel, want waiting", s)
	}
	// Cancel: the only way a run stalled at an unanswered gate stops running.
	if err := CancelGraphRun(runTestSession, run.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	res, err := RetryGraphRun(runTestSession, run.ID, "c")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(res.Rearmed) != 0 || res.From != "c" {
		t.Fatalf("retry result rearmed=%+v from=%q — a never-approved gate must not re-arm (stale approvals, not missing ones)", res.Rearmed, res.From)
	}
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeSkipped {
		t.Fatalf("gate state %q after retry, want skipped (untouched) — the retry must not reset a gate that holds no approval", s)
	}
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.RetryNote != "" {
		t.Errorf("RetryNote %q on a retry with no stale approval, want empty", got.RetryNote)
	}

	// The retry resumes where asked and never re-asks the gate — but the commit
	// itself is refused at the backstop, because no human ever approved the gate
	// above it (MUX-144 Phase 4; before it, this dispatch went through).
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeFailed {
		t.Fatalf("c state %q after tick, want failed — a commit below a never-approved gate must not dispatch", s)
	}
	if msgs, _ := Peek(runTestSession, "commit"); len(msgs) != 0 {
		t.Errorf("commit inbox = %+v, want empty — the refused dispatch must not reach the agent", msgs)
	}
	logged, _ := readMessages(LogPath(runTestSession))
	var approvals int
	for _, m := range logged {
		if m.Action == "graph-approval" && strings.Contains(m.Payload, run.ID) {
			approvals++
		}
	}
	if approvals != 1 {
		t.Fatalf("edit received %d graph-approval requests for this run, want exactly 1 (the original dispatch) — the retry must not re-ask a never-approved gate", approvals)
	}
}

func TestExecConditionNode(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "cond",
		Nodes: []Node{
			{ID: "cond", Type: NodeCondition, Conditions: map[string]any{"env_set": "MUXCODE_GRAPH_TEST_ENV"}},
			{ID: "yes", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "no", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{
			{From: "cond", To: "yes", Outcome: OutcomeSuccess},
			{From: "cond", To: "no", Outcome: OutcomeFailure},
		},
	}
	t.Setenv("MUXCODE_GRAPH_TEST_ENV", "1")
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "yes"); s != GraphNodeRunning {
		t.Errorf("yes state %q, want running — condition passed", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "no"); s != GraphNodePending {
		t.Errorf("no state %q, want pending", s)
	}
}

// TestExecConditionFalseBranchIsNotAFailure pins the state/outcome split
// for a condition that takes its false branch (MUX-133 option B). The
// node is a branch selector choosing a branch, so its terminal STATE is
// done; the failure OUTCOME is retained because it is the routing key
// edgeOutcome matches, and every capped loop's terminating edge depends
// on it. Before option B this asserted GraphNodeFailed — the diff of
// this assertion is the model change.
func TestExecConditionFalseBranchIsNotAFailure(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "cond",
		Nodes: []Node{
			{ID: "cond", Type: NodeCondition, Conditions: map[string]any{"env_set": "MUXCODE_GRAPH_TEST_UNSET_ENV"}},
			{ID: "yes", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "no", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{
			{From: "cond", To: "yes", Outcome: OutcomeSuccess},
			{From: "cond", To: "no", Outcome: OutcomeFailure},
		},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "cond")
	if err != nil {
		t.Fatalf("read cond: %v", err)
	}
	if st.State != GraphNodeDone {
		t.Errorf("cond state %q, want %q — a false branch is control flow, not a broken node",
			st.State, GraphNodeDone)
	}
	if st.Outcome != OutcomeFailure {
		t.Fatalf("cond outcome %q, want %q — the failure outcome is the routing key the false edge matches; changing it breaks every capped loop",
			st.Outcome, OutcomeFailure)
	}

	// The routing invariant: the false edge must still have fired.
	if s := nodeState(t, runTestSession, run.ID, "no"); s != GraphNodeRunning {
		t.Errorf("no state %q, want running — the false edge must still route after the split", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "yes"); s != GraphNodePending {
		t.Errorf("yes state %q, want pending", s)
	}

	// The run must not be marked failed by a routine branch.
	got, err := ReadGraphRun(runTestSession, run.ID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if got.State == GraphRunFailed {
		t.Errorf("run state %q — a condition taking its false branch must never fail the run", got.State)
	}
}

// TestExecConditionUnevaluatableIsAFailure is the negative control for
// the split: a predicate that cannot be evaluated at all is a genuine
// error and must still persist GraphNodeFailed, so option B does not
// make every condition look done.
//
// Graph.Validate rejects an unknown condition type, so this state is
// unreachable through graph run|validate — it is reachable only by
// replaying a definition frozen before the rule existed, which is what
// this test constructs by rewriting the run's frozen graph.json. That
// is precisely why the executor branch is worth having: the run store
// replays frozen definitions without re-validating them.
func TestExecConditionUnevaluatableIsAFailure(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "cond",
		Nodes: []Node{
			{ID: "cond", Type: NodeCondition, Conditions: map[string]any{"env_set": "MUXCODE_GRAPH_TEST_UNSET_ENV"}},
			{ID: "no", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{
			{From: "cond", To: "no", Outcome: OutcomeFailure},
		},
	}
	run := createTestRun(t, g)

	// Freeze a definition the validator would now reject, as an older
	// binary could have written.
	frozen := *g
	frozen.Nodes = append([]Node(nil), g.Nodes...)
	frozen.Nodes[0].Conditions = map[string]any{"bogus_condition_type": "x"}
	blob, err := json.MarshalIndent(&frozen, "", "  ")
	if err != nil {
		t.Fatalf("marshal frozen graph: %v", err)
	}
	if err := os.WriteFile(graphDefPath(runTestSession, run.ID), blob, 0o644); err != nil {
		t.Fatalf("rewrite frozen graph: %v", err)
	}

	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "cond")
	if err != nil {
		t.Fatalf("read cond: %v", err)
	}
	if st.State != GraphNodeFailed {
		t.Errorf("cond state %q, want %q — an unevaluatable predicate is a real error, not a branch",
			st.State, GraphNodeFailed)
	}
	if st.Output == "" {
		t.Error("cond output empty — a genuine evaluation error must say what went wrong")
	}
	if ConditionTookBranch(NodeCondition, st.State, st.Outcome) {
		t.Error("ConditionTookBranch true for an unevaluatable predicate — it must render as a failure, not a branch")
	}
}

func TestRetryGraphRunFromNode(t *testing.T) {
	run := createTestRun(t, linearGraph())

	// Drive the run to completion.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunComplete {
		t.Fatalf("run state %q, want complete", got.State)
	}

	res, err := RetryGraphRun(runTestSession, run.ID, "b")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(res.Rearmed) != 0 || res.From != "b" {
		t.Fatalf("retry result rearmed=%+v from=%q — an ungated retry must not re-target", res.Rearmed, res.From)
	}
	// Upstream a keeps its result; b is re-armed; run is running again.
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeDone {
		t.Errorf("a state %q, want done (upstream preserved)", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeReady {
		t.Errorf("b state %q, want ready", s)
	}

	// The next tick dispatches ONLY b — a must not re-run.
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeDone {
		t.Errorf("a state %q after tick, want done", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Errorf("b state %q after tick, want running", s)
	}
}

func TestRetryGraphRunRefusesRunningRun(t *testing.T) {
	run := createTestRun(t, linearGraph())
	if _, err := RetryGraphRun(runTestSession, run.ID, "b"); err == nil {
		t.Error("retry must refuse a running run")
	}
}

func TestRetryGraphRunResetsLoopBudget(t *testing.T) {
	g := linearGraph()
	g.Edges = append(g.Edges, Edge{From: "b", To: "a", Outcome: OutcomeFailure, MaxIterations: 1})
	run := createTestRun(t, g)

	// Exhaust the loop: a ok, b fails, loop fires once, a ok, b fails again → run failed.
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed", got.State)
	}

	// Retry from a: the loop edge budget resets with the subtree.
	if _, err := RetryGraphRun(runTestSession, run.ID, "a"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	fresh, _ := ReadGraphRun(runTestSession, run.ID)
	if len(fresh.EdgeFires) != 0 {
		t.Errorf("edge fires not reset: %v", fresh.EdgeFires)
	}
}

func TestExecWaitEventRelease(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "ev", Type: NodeWaitEvent, Event: "deploy-finished"},
			{ID: "c", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{{From: "a", To: "ev"}, {From: "ev", To: "c"}},
	}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "ev"); s != GraphNodeWaiting {
		t.Fatalf("ev state %q, want waiting", s)
	}

	// A bus message with the event's action releases it.
	m := NewMessage("deploy", "edit", "event", "deploy-finished", "done", "")
	if err := SendNoCC(runTestSession, m); err != nil {
		t.Fatalf("send event: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "ev"); s != GraphNodeDone {
		t.Errorf("ev state %q, want done after event", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("c state %q, want running after event release", s)
	}
}

func TestExecMapNodeFansOutPerItem(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "m",
		Nodes: []Node{
			{ID: "m", Type: NodeMap, Role: "edit", Message: "process ${item}", Items: "one, two, three"},
		},
	}
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)

	step(t, runTestSession, run.ID)
	if len(*tasks) != 3 {
		t.Fatalf("expected 3 workers, got %d: %v", len(*tasks), *tasks)
	}
	for i, want := range []string{"process one", "process two", "process three"} {
		if !strings.Contains((*tasks)[i], want) {
			t.Errorf("worker %d task %q missing %q", i, (*tasks)[i], want)
		}
	}

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "m"); s != GraphNodeDone {
		t.Errorf("map state %q, want done once all workers completed", s)
	}
}

// specGuardGraph returns a one-node graph whose send carries the
// spec-complete guard (MUX-114).
func specGuardGraph() *Graph {
	return &Graph{
		Name:  "g",
		Start: "close",
		Nodes: []Node{{ID: "close", Type: NodeSend, Role: "plan", Action: "update-docs",
			Message: "close out the spec", Guard: GuardSpecComplete}},
	}
}

// writeSpecFixture writes a spec file inside a scratch repo dir, pins the
// session repo dir to it, and points the active-spec marker at the spec's
// absolute path. The pointer must live inside the pinned repo: every
// pointer, absolute included, now resolves through the containment
// boundary (review must-fix 2026-09-01).
func writeSpecFixture(t *testing.T, content string) string {
	t.Helper()
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	// An unborn HEAD: nothing of the spec is committed yet.
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	path := filepath.Join(repo, "spec.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecSpecGuardDeclinesOpenSpec(t *testing.T) {
	run := createTestRun(t, specGuardGraph())
	writeSpecFixture(t, "# S\n- [x] done\n- [ ] open item one\n- [ ] open item two\n")

	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "close")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || st.Outcome != OutcomeFailure {
		t.Fatalf("close state %q outcome %q, want failed/failure", st.State, st.Outcome)
	}
	if !strings.Contains(st.Output, "2 open items") || !strings.Contains(st.Output, "open item one") {
		t.Errorf("decline must name the count and the open items, got %q", st.Output)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 0 {
		t.Errorf("declined dispatch must never send — plan inbox: %+v", msgs)
	}

	// Failure routing runs on the next tick; with no failure edge it fails the run.
	step(t, runTestSession, run.ID)
	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Errorf("run state %q, want failed — the chain must stop before any downstream mutation", got.State)
	}
}

// TestExecSpecGuardAllowsCompleteSpec is the negative control: a guard
// that simply never closes anything cannot pass it.
func TestExecSpecGuardAllowsCompleteSpec(t *testing.T) {
	run := createTestRun(t, specGuardGraph())
	writeSpecFixture(t, "# S\n- [x] done\n- [X] loudly done\n")

	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "close"); s != GraphNodeRunning {
		t.Fatalf("close state %q, want running — a fully-checked spec must dispatch", s)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 1 {
		t.Errorf("plan inbox has %d messages, want the close-out send", len(msgs))
	}
}

func TestExecSpecGuardNoActiveSpecDispatches(t *testing.T) {
	run := createTestRun(t, specGuardGraph())

	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "close"); s != GraphNodeRunning {
		t.Fatalf("close state %q, want running — no active spec is the node's own nothing-to-do path", s)
	}
}

func TestExecSpecGuardUnreadableSpecDeclines(t *testing.T) {
	run := createTestRun(t, specGuardGraph())
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	if err := WriteActiveSpec(runTestSession, filepath.Join(repo, "absent.md")); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "close")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "cannot read active spec") {
		t.Errorf("unreadable spec must decline loudly, got state %q output %q", st.State, st.Output)
	}
}

func TestExecSpecGuardResolvesRelativeSpecPath(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "spec.md"), []byte("- [ ] open\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	run := createTestRun(t, specGuardGraph())
	if err := WriteActiveSpec(runTestSession, "spec.md"); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "close")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "1 open items") {
		t.Errorf("relative spec path must resolve against the session repo dir, got state %q output %q", st.State, st.Output)
	}
}

// TestExecSpecGuardRefusesExternalPointer pins the pointer boundary at
// the guard (review must-fix 2026-09-01): an active-spec pointer
// resolving outside the repo fails the node loudly — it must NOT read as
// "no active spec", which would pass the guard through and close out
// against nothing (the inert-guard hazard), and the daemon must never
// read the external file.
func TestExecSpecGuardRefusesExternalPointer(t *testing.T) {
	run := createTestRun(t, specGuardGraph())
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	ext := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(ext, []byte("- [x] not a spec\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, ext); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	st, err := ReadNodeStatus(runTestSession, run.ID, "close")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "outside the repo") {
		t.Fatalf("external pointer must fail the node, got state %q output %q", st.State, st.Output)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 0 {
		t.Errorf("refused dispatch must never send — plan inbox: %+v", msgs)
	}
}

// TestActiveSpecFileBoundary pins the four pointer states as distinct.
// The one that must never collapse: an external pointer is refused, not
// unset — and with the repo dir unresolvable, containment is unprovable
// for EVERY pointer shape, absolute included, so all postpone as
// transient (the old code followed absolute pointers unconditionally).
func TestActiveSpecFileBoundary(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)

	if _, ok, transient, refused := activeSpecFile(runTestSession); ok || transient || refused {
		t.Errorf("unset: ok=%v transient=%v refused=%v, want all false", ok, transient, refused)
	}

	spec := filepath.Join(repo, "spec.md")
	if err := os.WriteFile(spec, []byte("# s\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, spec); err != nil {
		t.Fatal(err)
	}
	if path, ok, _, _ := activeSpecFile(runTestSession); !ok || path == "" {
		t.Errorf("in-repo absolute pointer: ok=%v path=%q, want resolved", ok, path)
	}

	ext := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(ext, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, ext); err != nil {
		t.Fatal(err)
	}
	if p, ok, transient, refused := activeSpecFile(runTestSession); !refused || ok || transient || p != "" {
		t.Errorf("external pointer: ok=%v transient=%v refused=%v path=%q, want refused only", ok, transient, refused, p)
	}

	t.Setenv("MUXCODE_SESSION_REPO_DIR", "")
	if _, ok, transient, refused := activeSpecFile(runTestSession); !transient || ok || refused {
		t.Errorf("no repo dir with absolute pointer: ok=%v transient=%v refused=%v, want transient only", ok, transient, refused)
	}
}

// phaseGuardGraph returns a one-node graph whose send carries the
// phase-complete guard.
func phaseGuardGraph() *Graph {
	return &Graph{
		Name:  "g",
		Start: "ship",
		Nodes: []Node{{ID: "ship", Type: NodeSend, Role: "plan", Action: "update-docs",
			Message: "commit the phase", Guard: GuardPhaseComplete}},
	}
}

// TestExecPhaseGuard pins the phase-scoped guard: the intent's phase with
// open items declines; the same phase fully checked dispatches even while
// OTHER phases are open (the discriminator against spec-complete); no
// phase in the intent passes through.
func TestExecPhaseGuard(t *testing.T) {
	spec := "# S\n### Phase 1: Now\n- [ ] open one\n### Phase 2: Later\n- [ ] later work\n"

	run := createTestRun(t, phaseGuardGraph())
	writeSpecFixture(t, spec)
	mutateRunIntent(t, run.ID, "Ship — Phase 1: Now")
	step(t, runTestSession, run.ID)
	st, err := ReadNodeStatus(runTestSession, run.ID, "ship")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "Phase 1 has 1 open items") {
		t.Fatalf("open phase must decline, got state %q output %q", st.State, st.Output)
	}

	run2 := createTestRun(t, phaseGuardGraph())
	writeSpecFixture(t, "# S\n### Phase 1: Now\n- [x] done\n### Phase 2: Later\n- [ ] later work\n")
	mutateRunIntent(t, run2.ID, "Ship — Phase 1: Now")
	step(t, runTestSession, run2.ID)
	if s := nodeState(t, runTestSession, run2.ID, "ship"); s != GraphNodeRunning {
		t.Fatalf("checked phase must dispatch despite open later phases, got %q", s)
	}

	run3 := createTestRun(t, phaseGuardGraph())
	writeSpecFixture(t, spec)
	mutateRunIntent(t, run3.ID, "no phase named")
	step(t, runTestSession, run3.ID)
	if s := nodeState(t, runTestSession, run3.ID, "ship"); s != GraphNodeRunning {
		t.Fatalf("intent without a phase must pass through, got %q", s)
	}
}

// TestExecCurrentPhaseInterpolation pins ${current_phase} resolution at
// dispatch: the message carries the spec's lowest OPEN phase, not the
// frozen intent's — the mechanism that stops a loop re-implementing a
// completed phase (MUX-121).
func TestExecCurrentPhaseInterpolation(t *testing.T) {
	g := &Graph{
		Name:  "g",
		Start: "impl",
		Nodes: []Node{{ID: "impl", Type: NodeSend, Role: "build", Action: "build",
			Message: "Implement ${current_phase}"}},
	}
	run := createTestRun(t, g)
	writeSpecFixture(t, "# S\n### Phase 1: Done\n- [x] a\n### Phase 2: Attribution\n- [ ] b\n")
	mutateRunIntent(t, run.ID, "MUX-115 — Phase 1: Turn trace")

	step(t, runTestSession, run.ID)

	msgs, _ := Peek(runTestSession, "build")
	if len(msgs) != 1 {
		t.Fatalf("build inbox: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Payload, "Phase 2: Attribution") ||
		strings.Contains(msgs[0].Payload, "Phase 1") {
		t.Errorf("dispatch must carry the derived open phase, not the frozen intent's: %q", msgs[0].Payload)
	}
}

// TestNodeOutputInterpolation pins ${output:<node-id>}: a downstream
// dispatch carries an upstream node's report even across several edges,
// which predecessorOutput cannot reach. A worker that records a decision
// and names the file it wrote is otherwise answering a dispatch nobody
// downstream can read (MUX-148 Phase 2, 2026-09-14).
func TestNodeOutputInterpolation(t *testing.T) {
	g := &Graph{
		Name:  "g",
		Start: "impl",
		Nodes: []Node{
			{ID: "impl", Type: NodeSend, Role: "build", Action: "build", Message: "do it"},
			{ID: "mid", Type: NodeSend, Role: "test", Action: "test", Message: "check"},
			{ID: "record", Type: NodeSend, Role: "plan", Action: "verify-spec", Message: "REPORT: ${output:impl}"},
		},
		Edges: []Edge{{From: "impl", To: "mid"}, {From: "mid", To: "record"}},
	}
	run := createTestRun(t, g)

	const report = "Decision recorded: /tmp/mux-148-phase2-decision.md"
	if err := TransitionGraphNode(runTestSession, run.ID, "impl", GraphNodeRunning, func(s *GraphNodeStatus) {
		s.Output = report
	}); err != nil {
		t.Fatal(err)
	}

	got := interpolateGraphMessage(runTestSession, run, "REPORT: ${output:impl}", "")
	if !strings.Contains(got, report) {
		t.Errorf("dispatch must carry impl's report across two edges, got %q", got)
	}
	if strings.Contains(got, "${output:") {
		t.Errorf("placeholder must not survive into a dispatch: %q", got)
	}

	// predecessorOutput is the mechanism this placeholder exists to go
	// beyond: record's only in-edge is mid, so it cannot see impl at all.
	if pred := predecessorOutput(runTestSession, run, g, "record"); strings.Contains(pred, report) {
		t.Errorf("predecessorOutput should not reach impl from record; got %q — "+
			"if it does, this placeholder is redundant and the test is vacuous", pred)
	}

	// Negative control: a gap must be visible, never a silent empty string.
	missing := interpolateGraphMessage(runTestSession, run, "REPORT: ${output:nosuch}", "")
	if !strings.Contains(missing, "no report recorded") || !strings.Contains(missing, "nosuch") {
		t.Errorf("unknown node must expand to a visible marker naming it, got %q", missing)
	}
	if missing == "REPORT: " {
		t.Error("unknown node expanded to silence — the failure this placeholder exists to stop")
	}

	// A node that exists but has not reported is the same gap.
	if unreported := interpolateGraphMessage(runTestSession, run, "${output:mid}", ""); !strings.Contains(unreported, "no report recorded") {
		t.Errorf("a node with no output must expand to the marker, got %q", unreported)
	}
}

// TestNodeOutputInterpolationTruncates pins the size bound, and that the
// head — where a report states its verdict and names its file — survives.
func TestNodeOutputInterpolationTruncates(t *testing.T) {
	g := &Graph{
		Name:  "g",
		Start: "impl",
		Nodes: []Node{{ID: "impl", Type: NodeSend, Role: "build", Action: "build", Message: "do it"}},
	}
	run := createTestRun(t, g)

	head := "VERDICT: see /tmp/decision.md"
	if err := TransitionGraphNode(runTestSession, run.ID, "impl", GraphNodeRunning, func(s *GraphNodeStatus) {
		s.Output = head + strings.Repeat("x", maxInterpolatedOutput*2)
	}); err != nil {
		t.Fatal(err)
	}

	got := interpolateGraphMessage(runTestSession, run, "${output:impl}", "")
	if !strings.Contains(got, head) {
		t.Errorf("truncation must keep the head of the report, got %q", got[:min(80, len(got))])
	}
	if !strings.Contains(got, "truncated") {
		t.Error("a truncated report must say so, or a reader treats a cut report as the whole one")
	}
	if len(got) > maxInterpolatedOutput+64 {
		t.Errorf("expanded output %d exceeds the cap %d", len(got), maxInterpolatedOutput)
	}
}

// TestTruncateAtRune pins that the cap never splits a rune: a cut inside a
// multibyte sequence yields invalid UTF-8, which JSON encoding silently
// replaces with U+FFFD.
func TestTruncateAtRune(t *testing.T) {
	// An em dash straddling the cap: 1 filler byte short of it, then 3 bytes.
	s := strings.Repeat("a", 9) + "—" + strings.Repeat("b", 20)
	got := truncateAtRune(s, 10)
	if !utf8.ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("truncation left a replacement char: %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 9)) {
		t.Errorf("truncation must keep whole runes before the cap, got %q", got)
	}

	// Negative control: under the cap, the string is returned untouched.
	if got := truncateAtRune("short", 10); got != "short" {
		t.Errorf("a string under the cap must be unchanged, got %q", got)
	}
}

// TestNodeOutputRefAcceptsValidIDs pins that the reference matches every id
// graph validation accepts — validation constrains an id only to nonempty
// and unique, so a dotted or spaced id must not be left literal.
func TestNodeOutputRefAcceptsValidIDs(t *testing.T) {
	g := &Graph{
		Name:  "g",
		Start: "impl.v2",
		Nodes: []Node{{ID: "impl.v2", Type: NodeSend, Role: "build", Action: "build", Message: "do it"}},
	}
	run := createTestRun(t, g)
	if err := TransitionGraphNode(runTestSession, run.ID, "impl.v2", GraphNodeRunning, func(s *GraphNodeStatus) {
		s.Output = "dotted report"
	}); err != nil {
		t.Fatal(err)
	}

	got := interpolateGraphMessage(runTestSession, run, "${output:impl.v2}", "")
	if !strings.Contains(got, "dotted report") {
		t.Errorf("a dotted node id must resolve, got %q", got)
	}
	if strings.Contains(got, "${output:") {
		t.Errorf("reference to a legally named node left literal: %q", got)
	}
}

// TestSpawnGroupReportsCarryWorkerReply pins the other half of the plumb:
// a spawn node recorded only the port summary, so ${output:<node-id>}
// would have expanded to "nothing to port" and delivered nothing — the
// placeholder would look wired while carrying no report at all.
//
// Both lookup roads are exercised separately: the delivery status first,
// then the bus log after that status is removed. The second case is what
// pins the fallback — with the status present it passes either way.
func TestSpawnGroupReportsCarryWorkerReply(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	// Init() makes this at session start; without it CreateDeliveryStatus
	// only warns, and a fixture with no status silently exercises the
	// fallback while claiming to test the delivery-status road.
	if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}

	seedAndReply := func(spawnRole, report string) string {
		t.Helper()
		seed := NewMessage("daemon", spawnRole, "request", "spawn-task", "do the work", "")
		if err := SendNoCC(runTestSession, seed); err != nil {
			t.Fatal(err)
		}
		reply := NewMessage(spawnRole, "daemon", "response", "spawn-task", report, seed.ID)
		if err := SendNoCC(runTestSession, reply); err != nil {
			t.Fatal(err)
		}
		MarkResponded(runTestSession, seed.ID, reply.ID)
		return seed.ID
	}

	const report = "Decision recorded: /tmp/mux-148-phase2-decision.md"
	seedID := seedAndReply("spawn-w1", report)
	if err := WriteSpawnEntries(runTestSession, []SpawnEntry{{
		ID: "w1", Role: "edit", SpawnRole: "spawn-w1",
		Status: "completed", SeedMsgID: seedID,
	}}); err != nil {
		t.Fatal(err)
	}

	// Road 1 — the delivery status carries the ResponseID.
	if ds, err := ReadDeliveryStatus(runTestSession, seedID); err != nil || ds.ResponseID == "" {
		t.Fatalf("fixture must have a status with a ResponseID, got (%+v, %v) — "+
			"without it this case cannot distinguish the two roads", ds, err)
	}
	if got := spawnGroupReports(runTestSession, "spawn-w1"); !strings.Contains(got, report) {
		t.Errorf("with a delivery status, spawn output must carry the reply, got %q", got)
	}

	// Road 2 — status gone, the bus log still has the reply. Without the
	// fallback this case returns empty, so it is what pins that branch.
	if err := os.Remove(DeliveryPath(runTestSession, seedID)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDeliveryStatus(runTestSession, seedID); err == nil {
		t.Fatal("delivery status still readable — the fallback case would be vacuous")
	}
	if got := spawnGroupReports(runTestSession, "spawn-w1"); !strings.Contains(got, report) {
		t.Errorf("with no delivery status, the log fallback must still find the reply, got %q", got)
	}

	// Negative control: no recorded response means no invented report.
	if err := WriteSpawnEntries(runTestSession, []SpawnEntry{{
		ID: "w2", Role: "edit", SpawnRole: "spawn-w2", Status: "completed",
	}}); err != nil {
		t.Fatal(err)
	}
	if got := spawnGroupReports(runTestSession, "spawn-w2"); got != "" {
		t.Errorf("a worker that never replied must contribute nothing, got %q", got)
	}

	// Reseed control: a new iteration's seed must not be satisfied by the
	// previous pass's reply, or a re-run reports work it never did.
	newSeed := NewMessage("daemon", "spawn-w1", "request", "spawn-task", "iteration 2", "")
	if err := SendNoCC(runTestSession, newSeed); err != nil {
		t.Fatal(err)
	}
	if err := WriteSpawnEntries(runTestSession, []SpawnEntry{{
		ID: "w1", Role: "edit", SpawnRole: "spawn-w1",
		Status: "completed", SeedMsgID: newSeed.ID,
	}}); err != nil {
		t.Fatal(err)
	}
	if got := spawnGroupReports(runTestSession, "spawn-w1"); got != "" {
		t.Errorf("an unanswered new seed must not inherit the prior reply, got %q", got)
	}
}

// TestSpawnHarvestPassesReportDownstream drives the whole plumb through the
// executor: a spawn worker answers, harvest records its report, and the
// downstream send's dispatch carries it. The helper-level tests above all
// still pass if harvest stops calling spawnGroupReports — this one does not.
func TestSpawnHarvestPassesReportDownstream(t *testing.T) {
	g := &Graph{
		Name:  "g",
		Start: "impl",
		Nodes: []Node{
			{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "decide it"},
			{ID: "record", Type: NodeSend, Role: "plan", Action: "verify-spec",
				Message: "WORKER REPORT: ${output:impl}"},
		},
		Edges: []Edge{{From: "impl", To: "record"}},
	}
	run := createTestRun(t, g)
	fakeSpawns(t, runTestSession)
	// See TestSpawnGroupReportsCarryWorkerReply: without this, no delivery
	// status is written, spawnHasResponded reads false, and the answered
	// worker is treated as lost instead of harvested.
	if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)
	st, err := ReadNodeStatus(runTestSession, run.ID, "impl")
	if err != nil || st.TaskID == "" {
		t.Fatalf("impl not running with a spawn id: (%+v, %v)", st, err)
	}

	// The verdict token is what attributes the worker. Without one the node
	// resolves unknown and holds, so nothing downstream is dispatched and the
	// report has nothing to travel on.
	const report = "Decision recorded: /tmp/mux-148-phase2-decision.md — EXIT=0"
	seed := NewMessage("daemon", st.TaskID, "request", "spawn-task", "decide it", "")
	if err := SendNoCC(runTestSession, seed); err != nil {
		t.Fatal(err)
	}
	reply := NewMessage(st.TaskID, "daemon", "response", "spawn-task", report, seed.ID)
	if err := SendNoCC(runTestSession, reply); err != nil {
		t.Fatal(err)
	}
	MarkResponded(runTestSession, seed.ID, reply.ID)
	if err := UpdateSpawnEntry(runTestSession, st.TaskID, func(e *SpawnEntry) {
		e.SeedMsgID = seed.ID
	}); err != nil {
		t.Fatal(err)
	}

	// Preconditions, asserted separately so a failure below names its cause
	// rather than surfacing only as the port summary downstream.
	entries, err := ReadSpawnEntries(runTestSession)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if e.SpawnRole == st.TaskID {
			found = true
			if e.SeedMsgID != seed.ID {
				t.Fatalf("entry %s SeedMsgID = %q, want %q", e.SpawnRole, e.SeedMsgID, seed.ID)
			}
		}
	}
	if !found {
		t.Fatalf("no spawn entry with SpawnRole %q: %+v", st.TaskID, entries)
	}
	if got := spawnReplyPayload(runTestSession, seed.ID); got != report {
		t.Fatalf("spawnReplyPayload = %q, want the worker's report", got)
	}
	if got := spawnGroupReports(runTestSession, st.TaskID); got != report {
		t.Fatalf("spawnGroupReports = %q, want the worker's report", got)
	}

	step(t, runTestSession, run.ID) // harvest impl
	implSt, err := ReadNodeStatus(runTestSession, run.ID, "impl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(implSt.Output, report) {
		t.Fatalf("harvest recorded %q — the report never reached the node's output", implSt.Output)
	}
	step(t, runTestSession, run.ID) // dispatch record

	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 1 {
		t.Fatalf("plan inbox: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Payload, report) {
		t.Errorf("plan's dispatch must carry the worker's report, got %q", msgs[0].Payload)
	}
}

// TestSpecToPRPassesWorkerReportToPlan guards the wiring itself: the
// mechanism above is inert unless the template actually uses it, and the
// run that failed on 2026-09-14 failed precisely because plan's dispatch
// carried nothing from the worker.
func TestSpecToPRPassesWorkerReportToPlan(t *testing.T) {
	data, ok := builtinGraphJSON["spec-to-pr"]
	if !ok {
		t.Fatal("builtin spec-to-pr template is missing")
	}
	g, err := ParseGraph([]byte(data))
	if err != nil {
		t.Fatalf("parse spec-to-pr: %v", err)
	}
	updateSpec := g.node("update-spec")
	if updateSpec == nil {
		t.Fatal("spec-to-pr has no update-spec node")
	}
	if !strings.Contains(updateSpec.Message, "${output:implement}") {
		t.Errorf("update-spec must carry the implement worker's report; message = %q", updateSpec.Message)
	}
}

// TestSpecPhasesRemainingCondition pins the loop-termination condition
// (MUX-121): true passes while an open phase remains, false passes once
// none do, and a missing spec counts as nothing remaining so a loop
// terminates rather than spins.
func TestSpecPhasesRemainingCondition(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	spec := filepath.Join(repo, "spec.md")
	if err := os.WriteFile(spec, []byte("### Phase 1: A\n- [ ] open\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteActiveSpec(runTestSession, spec); err != nil {
		t.Fatal(err)
	}
	ctx := &ChainContext{Session: runTestSession}

	if ok, _ := EvaluateConditions(map[string]any{"spec_phases_remaining": true}, ctx); !ok {
		t.Error("open phase: remaining=true must pass")
	}
	if ok, _ := EvaluateConditions(map[string]any{"spec_phases_remaining": false}, ctx); ok {
		t.Error("open phase: remaining=false must fail (negative control)")
	}

	if err := os.WriteFile(spec, []byte("### Phase 1: A\n- [x] done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := EvaluateConditions(map[string]any{"spec_phases_remaining": false}, ctx); !ok {
		t.Error("all complete: remaining=false must pass — the termination edge")
	}

	if err := ClearActiveSpec(runTestSession); err != nil {
		t.Fatal(err)
	}
	if ok, _ := EvaluateConditions(map[string]any{"spec_phases_remaining": false}, ctx); !ok {
		t.Error("no active spec must count as nothing remaining — a loop must terminate, not spin")
	}
}

// TestExecPhaseProgressGuard pins the per-phase commit guard (MUX-121):
// each commit must ship one newly-completed phase, counted against the
// guard node's own prior successful fires — fix-loop and retry fires
// must never distort the math (review catch 2026-08-28).
func TestExecPhaseProgressGuard(t *testing.T) {
	guardGraph := func() *Graph {
		return &Graph{Name: "g", Start: "commit",
			Nodes: []Node{
				{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs",
					Message: "commit the phase", Guard: GuardPhaseProgress},
				{ID: "next", Type: NodeCondition, Conditions: map[string]any{"spec_phases_remaining": true}},
			},
			Edges: []Edge{{From: "commit", To: "next"}}}
	}
	seedFires := func(runID string, fires map[string]int) {
		run, err := ReadGraphRun(runTestSession, runID)
		if err != nil {
			t.Fatal(err)
		}
		run.EdgeFires = fires
		if err := WriteGraphRun(runTestSession, run); err != nil {
			t.Fatal(err)
		}
	}

	// First commit, one phase complete — ships, and heavy fix-loop fires
	// must not inflate the requirement (the review-found bug).
	run := createTestRun(t, guardGraph())
	writeSpecFixture(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n")
	seedFires(run.ID, map[string]int{"fix->build:success": 3})
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "commit"); s != GraphNodeRunning {
		t.Fatalf("first commit with its phase complete must ship despite fix-loop fires, got %q", s)
	}

	// Phase 1 already committed at HEAD and Phase 2 open: a fresh run with no
	// fires of its own must still decline, naming the counts (MUX-183).
	run2 := createTestRun(t, guardGraph())
	writeSpecFixture(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n")
	stubSpecAtHEAD(t, "### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n")
	step(t, runTestSession, run2.ID)
	st, err := ReadNodeStatus(runTestSession, run2.ID, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "1 phases complete in the tree, 1 at HEAD") {
		t.Errorf("no-progress commit must decline with counts, got %q %q", st.State, st.Output)
	}
	stubSpecAtHEAD(t, "")

	// No active spec: decline, never commit blind.
	run3 := createTestRun(t, guardGraph())
	step(t, runTestSession, run3.ID)
	st3, err := ReadNodeStatus(runTestSession, run3.ID, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if st3.State != GraphNodeFailed || !strings.Contains(st3.Output, "no active spec") {
		t.Errorf("no-spec commit must decline, got %q %q", st3.State, st3.Output)
	}
}

// TestExecHumanGateLoopReArmRequiresFreshApproval covers the loop path
// the multi-phase template lives on (plan coverage gap 2026-08-28): a
// gate approved on iteration 1 and re-armed by a loop edge must WAIT
// again — without the dispatch purge, approving Phase 1's commit would
// silently release every later phase's commit.
func TestExecHumanGateLoopReArmRequiresFreshApproval(t *testing.T) {
	pinActor(t, "")
	g := &Graph{Name: "g", Start: "gate",
		Nodes: []Node{
			{ID: "gate", Type: NodeWaitHuman, Message: "approve the commit"},
			{ID: "work", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
		},
		Edges: []Edge{
			{From: "gate", To: "work"},
			{From: "work", To: "gate", MaxIterations: 2},
		}}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q, want waiting", s)
	}
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run.ID) // approval releases work
	completeSendNode(t, runTestSession, run.ID, "work", OutcomeSuccess)
	step(t, runTestSession, run.ID) // loop edge re-arms the gate
	step(t, runTestSession, run.ID) // gate re-dispatches

	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("re-armed gate state %q, want waiting — the loop pass must demand a fresh approval", s)
	}
	msgs, _ := Peek(runTestSession, "build")
	if len(msgs) != 1 {
		t.Errorf("work dispatched %d times, want 1 — the stale approval must not release iteration 2", len(msgs))
	}
}

// TestExecLoopExhaustionFailsRunLoudly pins the MUX-121 plan finding: a
// SUCCESS outcome whose only edge is an exhausted loop edge must fail the
// run — a cap shortfall must never settle as a run that looks complete.
func TestExecLoopExhaustionFailsRunLoudly(t *testing.T) {
	g := linearGraph()
	g.Edges = []Edge{{From: "a", To: "b"}, {From: "b", To: "a", MaxIterations: 1}}
	run := createTestRun(t, g)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID) // b succeeds, loop edge fires (1/1), a re-arms
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID) // b succeeds again, loop edge exhausted

	got, _ := ReadGraphRun(runTestSession, run.ID)
	if got.State != GraphRunFailed {
		t.Errorf("run state %q, want failed — exhaustion suppressed b's only edge", got.State)
	}
}

// TestExecRedriveStalledDispatch pins executor-owned stall resolution
// (MUX-123): an un-receipted in-flight dispatch past the stall threshold
// is redriven with persisted bookkeeping, rate-limited between attempts,
// and fails loudly as undeliverable after the cap; a receipted task is
// never touched (negative control — receipt means genuinely working).
func TestExecRedriveStalledDispatch(t *testing.T) {
	oneNode := func() *Graph {
		return &Graph{Name: "g", Start: "a",
			Nodes: []Node{{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"}}}
	}
	backdateTask := func(id string, secs int64) {
		task, err := ReadTask(runTestSession, id)
		if err != nil {
			t.Fatal(err)
		}
		task.SentAt -= secs
		if err := writeTask(runTestSession, task); err != nil {
			t.Fatal(err)
		}
	}
	nodeStatus := func(runID string) *GraphNodeStatus {
		st, err := ReadNodeStatus(runTestSession, runID, "a")
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	// Observe DELIVERIES through the seam, not just counters (review
	// should-fix: a counter test proves bookkeeping, not behavior).
	var driven []string
	origRedrive := graphRedriveFn
	graphRedriveFn = func(session, role string, task Task) int {
		driven = append(driven, role+":"+task.ID)
		return 1
	}
	t.Cleanup(func() { graphRedriveFn = origRedrive })
	idle := true
	origIdle := graphAgentIdleFn
	graphAgentIdleFn = func(string, string) bool { return idle }
	t.Cleanup(func() { graphAgentIdleFn = origIdle })

	run := createTestRun(t, oneNode())
	step(t, runTestSession, run.ID)
	st := nodeStatus(run.ID)
	if st.State != GraphNodeRunning || st.Redrives != 0 {
		t.Fatalf("fresh dispatch: state %q redrives %d", st.State, st.Redrives)
	}

	// Fresh task: no redrive even on another tick.
	step(t, runTestSession, run.ID)
	if st = nodeStatus(run.ID); st.Redrives != 0 || len(driven) != 0 {
		t.Fatalf("un-stalled task must not redrive, got %d (%v)", st.Redrives, driven)
	}

	// Busy pane: stalled and un-receipted but mid-turn — never redriven
	// (the 2026-09-09 commit redrive interrupted a working agent twice).
	backdateTask(st.TaskID, 120)
	idle = false
	step(t, runTestSession, run.ID)
	if st = nodeStatus(run.ID); st.Redrives != 0 || len(driven) != 0 {
		t.Fatalf("busy agent must not be redriven, got %d (%v)", st.Redrives, driven)
	}
	idle = true

	// Stalled + un-receipted at the prompt: one real delivery attempt,
	// bookkeeping persisted.
	step(t, runTestSession, run.ID)
	if st = nodeStatus(run.ID); st.Redrives != 1 || st.LastRedrive == 0 || len(driven) != 1 {
		t.Fatalf("stalled dispatch must redrive once, got %d (%v)", st.Redrives, driven)
	}

	// Rate limit: an immediate tick delivers nothing further.
	step(t, runTestSession, run.ID)
	if st = nodeStatus(run.ID); st.Redrives != 1 || len(driven) != 1 {
		t.Fatalf("redrive must rate-limit, got %d (%v)", st.Redrives, driven)
	}

	// Walk to the cap, then the node fails as undeliverable.
	for i := 0; i < 3; i++ {
		if err := MutateNodeStatus(runTestSession, run.ID, "a", func(s *GraphNodeStatus) {
			s.LastRedrive -= 61
		}); err != nil {
			t.Fatal(err)
		}
		step(t, runTestSession, run.ID)
	}
	st = nodeStatus(run.ID)
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "undeliverable") {
		t.Fatalf("capped redrives must fail undeliverable, got %q %q (redrives %d)", st.State, st.Output, st.Redrives)
	}

	// Negative control: a receipted task is working, never redriven.
	run2 := createTestRun(t, oneNode())
	step(t, runTestSession, run2.ID)
	st2 := nodeStatus(run2.ID)
	backdateTask(st2.TaskID, 120)
	if err := os.MkdirAll(filepath.Dir(DeliveryPath(runTestSession, st2.TaskID)), 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(DeliveryStatus{ID: st2.TaskID, AckedAt: time.Now().Unix()})
	if err := os.WriteFile(DeliveryPath(runTestSession, st2.TaskID), data, 0644); err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run2.ID)
	if st2 = nodeStatus(run2.ID); st2.Redrives != 0 {
		t.Fatalf("receipted task must never redrive, got %d", st2.Redrives)
	}
}

// TestExecRedriveStalledSpawns pins the spawn/map side of stall
// resolution: a worker whose seeded task sits unconsumed past the stall
// threshold is re-woken with persisted bookkeeping, and cap exhaustion
// FAILS the node — default spawn nodes carry no timeout, so a
// never-waking worker would otherwise leave the node running forever
// (review must-fix 2026-08-28). A drained inbox is the negative control.
func TestExecRedriveStalledSpawns(t *testing.T) {
	var woken []string
	origWake := graphSpawnWakeFn
	graphSpawnWakeFn = func(_, spawnRole string) { woken = append(woken, spawnRole) }
	t.Cleanup(func() { graphSpawnWakeFn = origWake })
	idle := true
	origIdle := graphAgentIdleFn
	graphAgentIdleFn = func(string, string) bool { return idle }
	t.Cleanup(func() { graphAgentIdleFn = origIdle })

	g := &Graph{Name: "g", Start: "w",
		Nodes: []Node{{ID: "w", Type: NodeSpawn, Role: "build", Message: "go"}}}
	run := createTestRun(t, g)
	n := &g.Nodes[0]
	now := time.Now().Unix()

	startWorker := func(runID, spawnRole string, seedInbox bool) {
		if err := TransitionGraphNode(runTestSession, runID, "w", GraphNodeRunning, func(s *GraphNodeStatus) {
			s.TaskID = spawnRole
			s.StartedAt = now - 120
		}); err != nil {
			t.Fatal(err)
		}
		if seedInbox {
			if err := SendNoCC(runTestSession, NewMessage("daemon", spawnRole, "request", "task", "go", "")); err != nil {
				t.Fatal(err)
			}
		}
	}
	status := func(runID string) *GraphNodeStatus {
		st, err := ReadNodeStatus(runTestSession, runID, "w")
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	startWorker(run.ID, "spawn-w1", true)

	// Busy worker: an unconsumed seed behind a running turn is not a stall.
	idle = false
	redriveStalledSpawns(runTestSession, run, n, status(run.ID), now)
	if st := status(run.ID); st.Redrives != 0 || len(woken) != 0 {
		t.Fatalf("busy worker must not be woken, got %d (%v)", st.Redrives, woken)
	}
	idle = true

	// Stalled + unconsumed at the prompt: one wake, bookkeeping persisted.
	redriveStalledSpawns(runTestSession, run, n, status(run.ID), now)
	st := status(run.ID)
	if st.Redrives != 1 || st.LastRedrive == 0 || len(woken) != 1 || woken[0] != "spawn-w1" {
		t.Fatalf("stalled worker must re-wake once, got %d (%v)", st.Redrives, woken)
	}

	// Rate limit: an immediate retry does nothing.
	redriveStalledSpawns(runTestSession, run, n, st, now)
	if st = status(run.ID); st.Redrives != 1 || len(woken) != 1 {
		t.Fatalf("spawn redrive must rate-limit, got %d (%v)", st.Redrives, woken)
	}

	// Walk to the cap; the still-stalled worker then fails the node.
	for i := 0; i < 3; i++ {
		if err := MutateNodeStatus(runTestSession, run.ID, "w", func(s *GraphNodeStatus) {
			s.LastRedrive -= 61
		}); err != nil {
			t.Fatal(err)
		}
		redriveStalledSpawns(runTestSession, run, n, status(run.ID), now)
	}
	st = status(run.ID)
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "undeliverable") {
		t.Fatalf("capped spawn redrives must fail undeliverable, got %q %q (redrives %d)", st.State, st.Output, st.Redrives)
	}

	// Negative control: a drained inbox means the worker consumed its
	// task and is working — never woken, never failed.
	woken = nil
	run2 := createTestRun(t, g)
	startWorker(run2.ID, "spawn-w2", false)
	redriveStalledSpawns(runTestSession, run2, n, status(run2.ID), now)
	st = status(run2.ID)
	if st.State != GraphNodeRunning || st.Redrives != 0 || len(woken) != 0 {
		t.Fatalf("working spawn must be untouched, got %q redrives %d (%v)", st.State, st.Redrives, woken)
	}
}

// TestExecSpawnLostWorkerReplaced pins replaceLostWorkers: a worker that
// ends before answering its seed — stopped by hand, or its window gone —
// is replaced by a fresh worker seeded with the same task under the same
// reuse key, the node stays running, and the replacement's answer
// completes it. Cap exhaustion fails the node with the lost-worker
// reason. Negative control: a worker stopped AFTER answering is a
// completion, never a loss (the 2026-09-09 run died because a stop
// before the answer was read as a verdict).
func TestExecSpawnLostWorkerReplaced(t *testing.T) {
	g := &Graph{Name: "lost", Start: "w",
		Nodes: []Node{{ID: "w", Type: NodeSpawn, Role: "edit", Message: "implement phase"}}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)
	status := func(runID string) *GraphNodeStatus {
		st, err := ReadNodeStatus(runTestSession, runID, "w")
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	stop := func(spawnRole string) {
		if err := UpdateSpawnEntry(runTestSession, spawnRole, func(e *SpawnEntry) {
			e.Status = "stopped"
			e.FinishedAt = time.Now().Unix()
		}); err != nil {
			t.Fatal(err)
		}
	}

	step(t, runTestSession, run.ID)
	first := status(run.ID).TaskID
	if first == "" || f.fresh != 1 {
		t.Fatalf("fresh worker expected: task %q, %d starts", first, f.fresh)
	}

	// Stopped before answering: replaced on a fresh worker, node still running.
	stop(first)
	step(t, runTestSession, run.ID)
	st := status(run.ID)
	if st.State != GraphNodeRunning || st.TaskID == first || f.fresh != 2 || st.Redrives != 1 {
		t.Fatalf("stopped worker must be replaced: state %q task %q starts %d redrives %d", st.State, st.TaskID, f.fresh, st.Redrives)
	}
	second := st.TaskID
	e1, _ := GetSpawnEntry(runTestSession, first)
	e2, _ := GetSpawnEntry(runTestSession, second)
	if e2.Task != e1.Task || e2.RunID != run.ID || e2.NodeID != "w" {
		t.Fatalf("replacement must carry the same task and reuse key: got %+v want task %q", e2, e1.Task)
	}

	// Window gone before answering (crash, kill-window): also lost, also replaced.
	f.deadWindows[second] = true
	step(t, runTestSession, run.ID)
	st = status(run.ID)
	if st.State != GraphNodeRunning || st.TaskID == second || f.fresh != 3 || st.Redrives != 2 {
		t.Fatalf("vanished worker must be replaced: state %q task %q starts %d redrives %d", st.State, st.TaskID, f.fresh, st.Redrives)
	}
	third := st.TaskID

	// The replacement answers: the node completes on it.
	answerSpawn(t, runTestSession, third)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeDone {
		t.Fatalf("w state %q, want done after the replacement answered", s)
	}

	// createTestRun moves to a fresh bus dir; seeds need the delivery
	// store to be answerable there.
	withDelivery := func(run *GraphRun) *GraphRun {
		if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
			t.Fatal(err)
		}
		return run
	}

	// Cap: a worker that keeps disappearing fails the node loudly.
	run2 := withDelivery(createTestRun(t, g))
	step(t, runTestSession, run2.ID)
	if err := MutateNodeStatus(runTestSession, run2.ID, "w", func(s *GraphNodeStatus) {
		s.Redrives = graphRedriveMax
	}); err != nil {
		t.Fatal(err)
	}
	stop(status(run2.ID).TaskID)
	starts := f.fresh
	step(t, runTestSession, run2.ID)
	st = status(run2.ID)
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "worker lost") || f.fresh != starts {
		t.Fatalf("capped replacement must fail the node, got %q %q (starts %d->%d)", st.State, st.Output, starts, f.fresh)
	}

	// Negative control: stopped AFTER answering is a completion, not a loss.
	run3 := withDelivery(createTestRun(t, g))
	step(t, runTestSession, run3.ID)
	worker := status(run3.ID).TaskID
	answerSpawn(t, runTestSession, worker)
	stop(worker)
	starts = f.fresh
	step(t, runTestSession, run3.ID)
	st = status(run3.ID)
	if st.State != GraphNodeDone || st.Outcome != OutcomeSuccess || f.fresh != starts {
		t.Fatalf("answered-then-stopped worker must complete the node, got %q/%q (starts %d->%d)", st.State, st.Outcome, starts, f.fresh)
	}
}

// TestExecMapReplacementFailsClosed pins the map side of replacement: with
// two lost members and the second replacement's start failing, the first
// replacement is recorded on the node AND stopped before the node fails —
// no worker keeps editing the shared checkout under a node that no longer
// names it (review must-fix 2026-09-09).
func TestExecMapReplacementFailsClosed(t *testing.T) {
	g := &Graph{Name: "map-lost", Start: "m",
		Nodes: []Node{{ID: "m", Type: NodeMap, Role: "edit", Items: "one,two", Message: "handle ${item}"}}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "m")
	members := strings.Split(st.TaskID, ",")
	if len(members) != 2 || f.fresh != 2 {
		t.Fatalf("two members expected: %q (%d starts)", st.TaskID, f.fresh)
	}
	for _, id := range members {
		if err := UpdateSpawnEntry(runTestSession, id, func(e *SpawnEntry) { e.Status = "stopped" }); err != nil {
			t.Fatal(err)
		}
	}

	// Second replacement start fails.
	inner := graphSpawnFn
	starts := 0
	graphSpawnFn = func(sess, role, task, owner, runID, nodeID string) (string, error) {
		starts++
		if starts == 2 {
			return "", errors.New("tmux new-window: no server")
		}
		return inner(sess, role, task, owner, runID, nodeID)
	}

	step(t, runTestSession, run.ID)
	st, _ = ReadNodeStatus(runTestSession, run.ID, "m")
	if st.State != GraphNodeFailed || !strings.Contains(st.Output, "replacement failed after 1 of 2") {
		t.Fatalf("partial replacement must fail the node, got %q %q", st.State, st.Output)
	}
	first := fmt.Sprintf("spawn-live%04d", 3)
	if !strings.Contains(st.TaskID, first) || strings.Contains(st.TaskID, members[0]) {
		t.Fatalf("the launched replacement must be recorded on the node: %q", st.TaskID)
	}
	if e, _ := GetSpawnEntry(runTestSession, first); e.Status != "stopped" {
		t.Fatalf("launched replacement must be stopped on failure, got %q", e.Status)
	}
	if len(f.killed) != 1 || f.killed[0] != first {
		t.Fatalf("exactly the launched replacement's window is killed, got %v", f.killed)
	}
	entries, _ := ReadSpawnEntries(runTestSession)
	for _, e := range entries {
		if e.RunID == run.ID && e.Status == "running" {
			t.Fatalf("no worker of the failed node may keep running: %+v", e)
		}
	}

	// Control: a kill that fails with the window still live leaves the
	// replacement RUNNING and names it in the node output — the failure
	// never claims a cleanup it could not verify.
	graphSpawnFn = inner
	run2 := createTestRun(t, g)
	if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	step(t, runTestSession, run2.ID)
	st2, _ := ReadNodeStatus(runTestSession, run2.ID, "m")
	for _, id := range strings.Split(st2.TaskID, ",") {
		if err := UpdateSpawnEntry(runTestSession, id, func(e *SpawnEntry) { e.Status = "stopped" }); err != nil {
			t.Fatal(err)
		}
	}
	starts = 0
	graphSpawnFn = func(sess, role, task, owner, runID, nodeID string) (string, error) {
		starts++
		if starts == 2 {
			return "", errors.New("tmux new-window: no server")
		}
		return inner(sess, role, task, owner, runID, nodeID)
	}
	f.killed = nil
	spawnKillWindowFn = func(_, w string) error {
		f.killed = append(f.killed, w)
		return errors.New("kill-window: no server")
	}
	step(t, runTestSession, run2.ID)
	st2, _ = ReadNodeStatus(runTestSession, run2.ID, "m")
	launched := f.killed
	if st2.State != GraphNodeFailed || len(launched) != 1 || !strings.Contains(st2.Output, "still live: "+launched[0]) {
		t.Fatalf("unverified cleanup must be named in the output, got %q %q (killed %v)", st2.State, st2.Output, launched)
	}
	if e, _ := GetSpawnEntry(runTestSession, launched[0]); e.Status != "running" {
		t.Fatalf("a replacement whose window is still live must stay running, got %q", e.Status)
	}
}

// TestGraphOwnsTask pins the executor-ownership lookup the daemon's
// idle-task watchdog defers to: a running node's dispatch is owned, a
// finished node's is not, and an unknown id is not.
func TestGraphOwnsTask(t *testing.T) {
	run := createTestRun(t, linearGraph())
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")

	runID, nodeID, ok := GraphOwnsTask(runTestSession, st.TaskID)
	if !ok || runID != run.ID || nodeID != "a" {
		t.Fatalf("running dispatch must be owned: %v %q %q", ok, runID, nodeID)
	}
	if _, _, ok := GraphOwnsTask(runTestSession, "no-such-task"); ok {
		t.Fatal("unknown task must not be owned")
	}

	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if _, _, ok := GraphOwnsTask(runTestSession, st.TaskID); ok {
		t.Fatal("a finished node's dispatch must no longer be owned")
	}
	stB, _ := ReadNodeStatus(runTestSession, run.ID, "b")
	if _, nodeID, ok := GraphOwnsTask(runTestSession, stB.TaskID); !ok || nodeID != "b" {
		t.Fatalf("successor dispatch must be owned by b: %v %q", ok, nodeID)
	}
}

// TestSpawnDisplayStatusParked pins the observer-facing status: a graph
// worker that answered while its run is in flight reads "parked", an
// unanswered one "running", and a terminal run drops the label.
func TestSpawnDisplayStatusParked(t *testing.T) {
	g := &Graph{Name: "parked", Start: "w",
		Nodes: []Node{{ID: "w", Type: NodeSpawn, Role: "edit", Message: "implement"}}}
	run := createTestRun(t, g)
	fakeLiveSpawns(t)

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	e, _ := GetSpawnEntry(runTestSession, st.TaskID)
	if got := SpawnDisplayStatus(runTestSession, e); got != "running" {
		t.Fatalf("unanswered worker reads %q, want running", got)
	}

	answerSpawn(t, runTestSession, st.TaskID)
	e, _ = GetSpawnEntry(runTestSession, st.TaskID)
	if got := SpawnDisplayStatus(runTestSession, e); got != "parked" {
		t.Fatalf("answered worker of a live run reads %q, want parked", got)
	}
	entries, _ := ReadSpawnEntries(runTestSession)
	if out := FormatSpawnList(AnnotateSpawnDisplay(runTestSession, entries), false); !strings.Contains(out, "parked") {
		t.Fatalf("spawn list must show the parked worker:\n%s", out)
	}

	step(t, runTestSession, run.ID)
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunComplete {
		t.Fatalf("run state %q, want complete", r.State)
	}
	e, _ = GetSpawnEntry(runTestSession, st.TaskID)
	if got := SpawnDisplayStatus(runTestSession, e); got == "parked" {
		t.Fatal("a terminal run's worker must not read parked")
	}
}

// mutateRunIntent rewrites a run's intent in the store.
func mutateRunIntent(t *testing.T, runID, intent string) {
	t.Helper()
	run, err := ReadGraphRun(runTestSession, runID)
	if err != nil {
		t.Fatal(err)
	}
	run.Intent = intent
	if err := WriteGraphRun(runTestSession, run); err != nil {
		t.Fatal(err)
	}
}

func TestExecSpecGuardPostponesWhenRepoDirUnknown(t *testing.T) {
	t.Setenv("MUXCODE_SESSION_REPO_DIR", "")
	run := createTestRun(t, specGuardGraph())
	if err := WriteActiveSpec(runTestSession, "docs/requirements/drafts/spec.md"); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "close"); s != GraphNodeReady {
		t.Fatalf("close state %q, want ready — an unresolvable repo dir is transient, not a decline", s)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 0 {
		t.Errorf("postponed dispatch must not send — plan inbox: %+v", msgs)
	}
}

// --- MUX-131 Defect B: spawn worker reuse ---

// liveSpawnFake mirrors the real graphSpawnFn closely enough for the
// reuse tests: a fresh worker gets a RUNNING entry, a real seeded inbox
// message, and the run+node stamp, so FindLiveSpawn, ReseedSpawn, and
// spawnGroupOutcome run their live paths without tmux. Windows listed in
// deadWindows read as gone; kill attempts are recorded. distinctIDs gives
// entries the production shape — ID differs from SpawnRole — which the
// default shape hides from any caller that confuses the two.
type liveSpawnFake struct {
	fresh       int
	killed      []string
	deadWindows map[string]bool
	distinctIDs bool
}

func fakeLiveSpawns(t *testing.T) *liveSpawnFake {
	t.Helper()
	if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
		t.Fatalf("delivery dir: %v", err)
	}
	f := &liveSpawnFake{deadWindows: map[string]bool{}}
	origSpawn, origExists, origKill, origWake := graphSpawnFn, spawnWindowExistsFn, spawnKillWindowFn, graphSpawnWakeFn
	t.Cleanup(func() {
		graphSpawnFn, spawnWindowExistsFn, spawnKillWindowFn, graphSpawnWakeFn = origSpawn, origExists, origKill, origWake
	})
	graphSpawnWakeFn = func(string, string) {}
	spawnWindowExistsFn = func(_, w string) bool { return !f.deadWindows[w] }
	spawnKillWindowFn = func(_, w string) error { f.killed = append(f.killed, w); return nil }
	graphSpawnFn = func(sess, role, task, owner, runID, nodeID string) (string, error) {
		f.fresh++
		id := fmt.Sprintf("spawn-live%04d", f.fresh)
		msg := NewMessage(owner, id, "request", "spawn-task", task, "")
		if err := Send(sess, msg); err != nil {
			t.Fatalf("seed send: %v", err)
		}
		entryID := id
		if f.distinctIDs {
			entryID = "1790000000-" + id
		}
		entry := SpawnEntry{ID: entryID, Role: role, SpawnRole: id, Owner: owner, Task: task,
			Status: "running", Window: id, StartedAt: time.Now().Unix(),
			SeedMsgID: msg.ID, RunID: runID, NodeID: nodeID}
		if err := appendSpawnEntry(sess, entry); err != nil {
			t.Fatalf("append spawn entry: %v", err)
		}
		return id, nil
	}
	return f
}

// answerSpawn fakes a worker completing its CURRENT seed: the same
// MarkResponded a real reply drives, which spawnHasResponded reads, and a
// reply body carrying the verdict token spawnGroupOutcome reads.
func answerSpawn(t *testing.T, session, spawnRole string) {
	t.Helper()
	answerSpawnWith(t, session, spawnRole, "phase implemented. EXIT=0")
}

// answerSpawnWith is answerSpawn with the reply body chosen — a decline, a
// non-zero verdict, or a success claim in prose alone.
func answerSpawnWith(t *testing.T, session, spawnRole, payload string) {
	t.Helper()
	entries, _ := ReadSpawnEntries(session)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].SpawnRole != spawnRole {
			continue
		}
		if entries[i].SeedMsgID == "" {
			t.Fatalf("worker %s has no seed", spawnRole)
		}
		resp := NewMessage(spawnRole, "daemon", "response", "spawn-task", payload, entries[i].SeedMsgID)
		if err := Send(session, resp); err != nil {
			t.Fatalf("reply send: %v", err)
		}
		MarkResponded(session, entries[i].SeedMsgID, resp.ID)
		return
	}
	t.Fatalf("no spawn entry for %s", spawnRole)
}

// TestSpawnGroupOutcomeReadsTheReplyNotTheFactOfReplying pins Defect 3. The
// answered branch was a bare continue, so any reply yielded success and a
// principled refusal was indistinguishable from work done — live on this
// spec's own run 1789399519, where a worker that reported "no code change,
// no spec edit" was recorded success.
//
// "Did the work" is the negative control the acceptance criteria demand: a
// fix that holds every spawn node is not a fix. "Success claimed in prose
// alone" is the other control — the distinction must not come from reading
// what the worker wrote.
func TestSpawnGroupOutcomeReadsTheReplyNotTheFactOfReplying(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string
	}{
		{"declined", "Phase 2 is a decision phase reserved for the user. No code change, no spec edit.", OutcomeUnknown},
		{"did the work", "implemented and verified. EXIT=0", OutcomeSuccess},
		{"could not", "blocked on a missing dependency. EXIT=1", OutcomeFailure},
		{"success claimed in prose alone", "All done — everything passes.", OutcomeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempBusDir(t)
			fakeLiveSpawns(t)

			id, err := graphSpawnFn(runTestSession, "edit", "implement phase 3", graphSender, "run1", "implement")
			if err != nil {
				t.Fatal(err)
			}
			answerSpawnWith(t, runTestSession, id, tc.reply)

			outcome, done := spawnGroupOutcome(runTestSession, id)
			if !done {
				t.Fatal("an answered worker completes the iteration")
			}
			if outcome != tc.want {
				t.Errorf("outcome = %q, want %q for reply %q", outcome, tc.want, tc.reply)
			}
		})
	}
}

// TestSpawnGroupOutcomeKeepsFailureSemantics guards the paths the verdict
// read must not disturb: missing, stopped and still-running workers behaved
// correctly before Defect 3 and must not decay into holds. The group rows
// pin the precedence — a hold must never mask another worker's failure.
func TestSpawnGroupOutcomeKeepsFailureSemantics(t *testing.T) {
	useTempBusDir(t)
	fakeLiveSpawns(t)

	spawn := func(node string) string {
		t.Helper()
		id, err := graphSpawnFn(runTestSession, "edit", "work", graphSender, "run1", node)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	running := spawn("running")
	if _, done := spawnGroupOutcome(runTestSession, running); done {
		t.Error("a worker still running must not report the iteration done")
	}

	if outcome, done := spawnGroupOutcome(runTestSession, "spawn-nosuch"); !done || outcome != OutcomeFailure {
		t.Errorf("missing worker = (%q, %v), want failure and done", outcome, done)
	}

	stopped := spawn("stopped")
	if err := UpdateSpawnEntry(runTestSession, stopped, func(e *SpawnEntry) { e.Status = "stopped" }); err != nil {
		t.Fatal(err)
	}
	if outcome, done := spawnGroupOutcome(runTestSession, stopped); !done || outcome != OutcomeFailure {
		t.Errorf("stopped unanswered worker = (%q, %v), want failure and done", outcome, done)
	}

	declined := spawn("declined")
	answerSpawnWith(t, runTestSession, declined, "I did not do this.")
	failed := spawn("failed")
	answerSpawnWith(t, runTestSession, failed, "EXIT=1")
	succeeded := spawn("succeeded")
	answerSpawnWith(t, runTestSession, succeeded, "EXIT=0")

	if outcome, _ := spawnGroupOutcome(runTestSession, declined+","+failed); outcome != OutcomeFailure {
		t.Errorf("declined+failed group = %q, want failure — a hold must not mask a failure", outcome)
	}
	if outcome, _ := spawnGroupOutcome(runTestSession, succeeded+","+declined); outcome != OutcomeUnknown {
		t.Errorf("succeeded+declined group = %q, want unknown — one unattributed worker holds the group", outcome)
	}
	if outcome, _ := spawnGroupOutcome(runTestSession, succeeded+","+succeeded); outcome != OutcomeSuccess {
		t.Errorf("all-succeeded group = %q, want success", outcome)
	}

	if silent := unattributedWorkers(runTestSession, succeeded+","+declined); len(silent) != 1 || silent[0] != declined {
		t.Errorf("unattributedWorkers = %v, want just %s — the hold must name who to ask", silent, declined)
	}
}

// TestGraphWorkerTaskSeedsTheVerdictToken pins the other half of the sentinel
// road: a token the seed never asks for is a token no worker emits, and every
// spawn node would then hold. The placeholder form is checked because a
// literal code in the request can be captured as the reply (MUX-154) and read
// back as a verdict the worker never gave.
func TestGraphWorkerTaskSeedsTheVerdictToken(t *testing.T) {
	g := &Graph{Name: "seed", Start: "w", Nodes: []Node{{ID: "w", Type: NodeSpawn, Role: "edit"}}}

	msg := graphWorkerTask(g, "run1", "w", "implement phase 3")
	if !strings.Contains(msg, "EXIT=<n>") {
		t.Errorf("seed does not ask for the verdict token: %q", msg)
	}
	if _, found := parseExitSentinel(msg); found {
		t.Errorf("seed carries a parseable verdict of its own: %q", msg)
	}

	// A node the graph owns no roles for still needs attributing, and that
	// branch returns before the preamble is built.
	withEdges := &Graph{Name: "seed2", Start: "w",
		Nodes: []Node{{ID: "w", Type: NodeSpawn, Role: "edit"}, {ID: "b", Type: NodeSend, Role: "build"}},
		Edges: []Edge{{From: "w", To: "b"}}}
	if !strings.Contains(graphWorkerTask(withEdges, "run1", "w", "do it"), "EXIT=<n>") {
		t.Error("a node with owned successors lost the verdict instruction")
	}
}

func spawnCountForRun(t *testing.T, session, runID string) int {
	t.Helper()
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		t.Fatalf("read spawn entries: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.RunID == runID {
			n++
		}
	}
	return n
}

// TestReseedSpawnIdentityFirstFailClosed pins the reseed ordering (review
// must-fix, 2026-09-01): SeedMsgID persists BEFORE the seed is sent, so a
// failure between the two leaves an id whose message does not exist — a
// state that can never read as responded — rather than a sent seed whose
// entry still carries the previous iteration's responded id (a false
// completion). The discriminator: a failing entry update must mean NO
// seed reaches the worker's inbox; send-first ordering delivers one.
func TestReseedSpawnIdentityFirstFailClosed(t *testing.T) {
	useTempBusDir(t)

	bogus := SpawnEntry{ID: "spawn-doesnotexist", SpawnRole: "spawn-doesnotexist", Owner: "daemon"}
	if _, err := ReseedSpawn(runTestSession, bogus, "phase 2"); err == nil {
		t.Fatal("reseed of a missing entry must error")
	}
	msgs, _ := Peek(runTestSession, "spawn-doesnotexist")
	for _, m := range msgs {
		if m.Action == "spawn-task" {
			t.Fatalf("seed was sent despite the entry update failing — identity must persist first, got %q", m.Payload)
		}
	}
}

// TestAcquireSpawnWorkerReusesLiveWorker pins the reuse core: a second
// acquire for the same run+node reseeds the live worker instead of
// starting a fresh one, and the entry's SeedMsgID moves to the new seed.
func TestAcquireSpawnWorkerReusesLiveWorker(t *testing.T) {
	useTempBusDir(t)
	f := fakeLiveSpawns(t)

	id1, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if f.fresh != 1 {
		t.Fatalf("first acquire must start fresh, got %d", f.fresh)
	}
	answerSpawn(t, runTestSession, id1)
	seed1, _ := GetSpawnEntry(runTestSession, id1)

	id2, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 2")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("re-entry must reuse the live worker: got %s, want %s", id2, id1)
	}
	if f.fresh != 1 {
		t.Fatalf("re-entry must not start a fresh worker, got %d starts", f.fresh)
	}
	e, err := GetSpawnEntry(runTestSession, id1)
	if err != nil {
		t.Fatal(err)
	}
	if e.SeedMsgID == "" || e.SeedMsgID == seed1.SeedMsgID {
		t.Fatalf("reseed must move SeedMsgID to the new iteration, got %q (was %q)", e.SeedMsgID, seed1.SeedMsgID)
	}
	if e.Task != "phase 2" {
		t.Fatalf("reseed must update the task, got %q", e.Task)
	}
	msgs, _ := Peek(runTestSession, id1)
	if len(msgs) != 1 || msgs[0].Payload != "phase 2" || msgs[0].ID != e.SeedMsgID {
		t.Fatalf("new seed must be the one pending row in the worker inbox: %+v", msgs)
	}
}

// TestAcquireSpawnWorkerDeadWorkerFreshStart is the spec's negative
// control: reuse must never wedge a run behind a corpse — a gone window
// falls back to a fresh worker.
func TestAcquireSpawnWorkerDeadWorkerFreshStart(t *testing.T) {
	useTempBusDir(t)
	f := fakeLiveSpawns(t)

	id1, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 1")
	if err != nil {
		t.Fatal(err)
	}
	answerSpawn(t, runTestSession, id1)
	f.deadWindows[id1] = true

	id2, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "phase 2")
	if err != nil {
		t.Fatal(err)
	}
	if id2 == id1 || f.fresh != 2 {
		t.Fatalf("dead worker must trigger a fresh start: got %s after %s, %d starts", id2, id1, f.fresh)
	}
}

// TestAcquireSpawnWorkerDistinctNodesDistinctWorkers is the spec's other
// negative control: reuse is keyed per run+node, never global — a second
// node (and a second run) must not adopt the first node's worker.
func TestAcquireSpawnWorkerDistinctNodesDistinctWorkers(t *testing.T) {
	useTempBusDir(t)
	f := fakeLiveSpawns(t)

	id1, err := acquireSpawnWorker(runTestSession, "run-1", "implement", "edit", "task a")
	if err != nil {
		t.Fatal(err)
	}
	idOtherNode, err := acquireSpawnWorker(runTestSession, "run-1", "fanout#0", "edit", "task b")
	if err != nil {
		t.Fatal(err)
	}
	idOtherRun, err := acquireSpawnWorker(runTestSession, "run-2", "implement", "edit", "task c")
	if err != nil {
		t.Fatal(err)
	}
	if id1 == idOtherNode || id1 == idOtherRun || idOtherNode == idOtherRun {
		t.Fatalf("distinct nodes/runs must get distinct workers: %s %s %s", id1, idOtherNode, idOtherRun)
	}
	if f.fresh != 3 {
		t.Fatalf("expected 3 fresh workers, got %d", f.fresh)
	}
}

// TestExecSpawnLoopReusesWorker walks a loop over a spawn node end to end:
// one worker serves both iterations (the assertion that would have caught
// the three-worker run in the MUX-131 report), the worker survives
// RefreshSpawnStatus mid-run, and is released by it once the run is
// terminal.
func TestExecSpawnLoopReusesWorker(t *testing.T) {
	g := &Graph{Name: "spawn-loop", Start: "w",
		Nodes: []Node{
			{ID: "w", Type: NodeSpawn, Role: "edit", Message: "implement"},
			{ID: "b", Type: NodeSend, Role: "build", Action: "build", Message: "build it"},
		},
		Edges: []Edge{
			{From: "w", To: "b"},
			{From: "b", To: "w", Outcome: OutcomeFailure, MaxIterations: 2},
		}}
	run := createTestRun(t, g)
	f := fakeLiveSpawns(t)

	// Iteration 1: worker starts fresh, answers, node completes.
	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	worker := st.TaskID
	if worker == "" || f.fresh != 1 {
		t.Fatalf("fresh worker expected on first dispatch: task %q, %d starts", worker, f.fresh)
	}
	answerSpawn(t, runTestSession, worker)

	// Mid-run persistence: the responded worker is NOT reaped while its
	// run is in flight — reaping here is exactly what forced a fresh
	// worker per iteration.
	if _, err := RefreshSpawnStatus(runTestSession); err != nil {
		t.Fatal(err)
	}
	if e, _ := GetSpawnEntry(runTestSession, worker); e.Status != "running" {
		t.Fatalf("responded worker of an in-flight run must stay running, got %q", e.Status)
	}
	if len(f.killed) != 0 {
		t.Fatalf("responded worker of an in-flight run must not be killed: %v", f.killed)
	}

	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeDone {
		t.Fatalf("w state %q, want done after worker answered", s)
	}

	// Build fails -> loop edge re-arms the spawn node.
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeFailure)
	step(t, runTestSession, run.ID)

	// Iteration 2: same worker, no fresh start.
	st, _ = ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeRunning || st.TaskID != worker {
		t.Fatalf("re-entry must reuse worker %s: state %q task %q", worker, st.State, st.TaskID)
	}
	if f.fresh != 1 {
		t.Fatalf("re-entry started a fresh worker: %d starts", f.fresh)
	}
	answerSpawn(t, runTestSession, worker)
	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
	step(t, runTestSession, run.ID)

	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunComplete {
		t.Fatalf("run state %q, want complete", r.State)
	}
	// THE Defect B assertion: one worker for the whole multi-iteration
	// run, counted from the spawn store, not read off the code.
	if n := spawnCountForRun(t, runTestSession, run.ID); n != 1 {
		t.Fatalf("run must have exactly 1 spawn worker, got %d", n)
	}

	// Run terminal: the normal reap path now releases the worker.
	if _, err := RefreshSpawnStatus(runTestSession); err != nil {
		t.Fatal(err)
	}
	e, _ := GetSpawnEntry(runTestSession, worker)
	if e.Status != "completed" || len(f.killed) != 1 || f.killed[0] != worker {
		t.Fatalf("terminal run must release the worker: status %q killed %v", e.Status, f.killed)
	}
}

// TestExecSpawnTaskNamesOwnedRoles pins the ownership preamble a graph-owned
// worker receives. Without it the worker runs on its base definition alone,
// whose Orchestration Role section tells every edit-shaped agent to delegate
// build/test/review — a second pipeline racing the graph's own parked nodes.
func TestExecSpawnTaskNamesOwnedRoles(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "impl",
		Nodes: []Node{
			{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "Implement phase 4"},
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "test", Type: NodeSend, Role: "test", Action: "test", Message: "go"},
		},
		Edges: []Edge{{From: "impl", To: "build"}, {From: "build", To: "test"}},
	}
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)
	step(t, runTestSession, run.ID)

	if len(*tasks) != 1 {
		t.Fatalf("expected 1 spawned worker, got %d: %v", len(*tasks), *tasks)
	}
	task := (*tasks)[0]
	for _, want := range []string{"build, test", "Do NOT delegate", run.ID, "node impl", "Implement phase 4"} {
		if !strings.Contains(task, want) {
			t.Errorf("worker task missing %q:\n%s", want, task)
		}
	}
	if strings.Contains(task, "muxcode send edit") {
		t.Errorf("preamble must not claim the graph owns the worker's own role:\n%s", task)
	}
}

// TestExecSpawnTaskNamesOnlyReachableRoles pins the reachability narrowing: a
// send the worker's node can never reach is not its succession, so claiming it
// would forbid a delegation nothing was going to duplicate.
func TestExecSpawnTaskNamesOnlyReachableRoles(t *testing.T) {
	// A branch node fans to two arms: only the worker's own arm is its
	// succession. Both arms stay reachable from start, so the graph is valid.
	g := &Graph{
		Name:  "t",
		Start: "fork",
		Nodes: []Node{
			{ID: "fork", Type: NodeCondition, Conditions: map[string]any{"env_set": "HOME"}},
			{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "work"},
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "other", Type: NodeSend, Role: "deploy", Action: "deploy", Message: "go"},
		},
		Edges: []Edge{
			{From: "fork", To: "impl"},
			{From: "fork", To: "other", Outcome: OutcomeFailure},
			{From: "impl", To: "build"},
		},
	}

	roles := graphOwnedRoles(g, "impl")
	if len(roles) != 1 || roles[0] != "build" {
		t.Errorf("worker at impl owns only its downstream build, got %v", roles)
	}
	if forkRoles := graphOwnedRoles(g, "fork"); len(forkRoles) != 2 {
		t.Errorf("both arms are downstream of the fork, got %v", forkRoles)
	}
}

// TestExecSpawnTaskUnprefixedWithoutSendNodes is the negative control: a graph
// that dispatches nothing but workers owns no delegations, so nothing may
// precede the worker's message. An implementation that always prefixed would
// pass the positive case above and fail here.
//
// Asserted as a prefix, not an equality: every seed carries
// verdictTokenInstruction appended after the message, and a node with no
// successors needs attributing like any other. That prefix assertion passes
// with or without the seed, which is why the seed is asserted separately
// below — otherwise it could be deleted with the suite green.
func TestExecSpawnTaskUnprefixedWithoutSendNodes(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "impl",
		Nodes: []Node{{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "Just do it"}},
	}
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)
	step(t, runTestSession, run.ID)

	if len(*tasks) != 1 {
		t.Fatalf("expected 1 spawned worker, got %d: %v", len(*tasks), *tasks)
	}
	got := (*tasks)[0]
	if !strings.HasPrefix(got, "edit: Just do it") {
		t.Errorf("task %q, want the message unprefixed — no send nodes means nothing is owned", got)
	}
	if strings.Contains(got, "Do NOT delegate") {
		t.Errorf("task %q carries an ownership preamble for a graph that owns nothing", got)
	}
	if !strings.Contains(got, verdictTokenInstruction) {
		t.Errorf("task %q carries no verdict instruction — the worker has no way to attribute itself", got)
	}
}

// TestExecMapTaskCarriesOwnership covers the second call site: map fans out
// through its own dispatch path, which regresses independently of NodeSpawn.
func TestExecMapTaskCarriesOwnership(t *testing.T) {
	g := &Graph{
		Name:  "t",
		Start: "fan",
		Nodes: []Node{
			{ID: "fan", Type: NodeMap, Role: "edit", Items: "one,two", Message: "Handle ${item}"},
			{ID: "review", Type: NodeSend, Role: "review", Action: "review", Message: "go"},
		},
		Edges: []Edge{{From: "fan", To: "review"}},
	}
	run := createTestRun(t, g)
	tasks := fakeSpawns(t, runTestSession)
	step(t, runTestSession, run.ID)

	if len(*tasks) != 2 {
		t.Fatalf("expected 2 map workers, got %d: %v", len(*tasks), *tasks)
	}
	for i, task := range *tasks {
		if !strings.Contains(task, "review") || !strings.Contains(task, "Do NOT delegate") {
			t.Errorf("map worker %d missing ownership preamble:\n%s", i, task)
		}
	}
	if !strings.Contains((*tasks)[0], "Handle one") || !strings.Contains((*tasks)[1], "Handle two") {
		t.Errorf("preamble must not displace per-item interpolation: %v", *tasks)
	}
}

// TestRowAttributesTo covers the git-evidenced actions in both directions, as
// the Phase 3 constraints require.
//
// One CmdGit spans commit, push, merge, rebase and `gh pr create`, so a
// commit node that accepts any of them is "a git command ran" standing in for
// "the commit was made" — and a commit node sits downstream of a human gate,
// the worst place to accept another command's verdict. The checkout rows are
// the opposite failure: before this, none of them attributed to anything.
func TestRowAttributesTo(t *testing.T) {
	cases := []struct {
		name    string
		action  string
		command string
		want    bool
	}{
		{"commit by a commit", "commit", "git commit -m 'work'", true},
		{"commit behind a cd prefix", "commit", `cd /repo && git commit -m "work"`, true},
		{"commit by a push", "commit", "git push origin HEAD", false},
		{"commit by a rebase", "commit", "git rebase main", false},
		{"commit by a pr create", "commit", "gh pr create --fill", false},
		{"commit by a build", "commit", "./build.sh", false},

		{"checkout by a checkout", "checkout", "git checkout -", true},
		{"checkout by a switch", "checkout", "git switch main", true},
		{"checkout by a commit", "checkout", "git commit -m 'work'", false},
		{"pr-checkout by gh", "pr-checkout", "gh pr checkout 161", true},
		{"pr-checkout by a pr create", "pr-checkout", "gh pr create --fill", false},

		// The type-evidenced actions are unchanged by the command branch.
		{"build by a build", "build", "./build.sh", true},
		{"build by a commit", "build", "git commit -m 'work'", false},
		{"test by a test", "test", "./test.sh", true},
		{"deploy by a deploy", "deploy", "cdk deploy", true},

		// No command evidences a review, whatever the agent happened to run.
		{"review by a commit", "review", "git commit -m 'work'", false},
		{"review by a test", "review", "./test.sh", false},

		// An action in neither table keeps the pre-attribution behaviour, so
		// run and watch nodes do not become permanent holds.
		{"unlisted action", "run", "aws s3 ls", true},

		// Known residual, pinned rather than blessed: the glob matches
		// "commit" anywhere in a git-headed command, so "uncommitted" counts.
		// Tightening it needs word-boundary matching across every pattern
		// list, which is wider than this phase — recorded in the spec.
		{"known-loose substring", "commit", "git status | grep uncommitted", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rowAttributesTo(tc.action, ConsoleEntry{Command: tc.command})
			if got != tc.want {
				t.Errorf("rowAttributesTo(%q, %q) = %v, want %v", tc.action, tc.command, got, tc.want)
			}
		})
	}
}

// TestGitPatternsClassifyCheckout pins the row's existence, not its reading.
// For the commit role an unclassified command writes no history row at all —
// ProcessBashHook's CmdUnknown branch covers run/runner/watch only — so a
// checkout classified as anything but CmdGit leaves the graph's checkout
// nodes with no evidence they could ever be attributed by.
func TestGitPatternsClassifyCheckout(t *testing.T) {
	for _, cmd := range []string{"git checkout -", "git checkout main", "git switch main", "gh pr checkout 161"} {
		if got := ClassifyCommand(cmd); got != CmdGit {
			t.Errorf("ClassifyCommand(%q) = %v, want CmdGit — an unclassified checkout writes no row", cmd, got)
		}
	}
	// Negative control: the list stays mutating-only, so a read-only git
	// command still mints nothing a node could be attributed by.
	for _, cmd := range []string{"git status", "git log --oneline"} {
		if got := ClassifyCommand(cmd); got == CmdGit {
			t.Errorf("ClassifyCommand(%q) = CmdGit — read-only git must write no row", cmd)
		}
	}
}
