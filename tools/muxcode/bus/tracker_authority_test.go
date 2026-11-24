package bus

import (
	"os"
	"strings"
	"testing"
	"time"
)

// MUX-181: a graph-dispatched tracker write (jira-write, confluence-write,
// issue-write) is judged at the send on the wait_human gate that released it,
// as a graph commit is — not on the consent sentence its template writes.

// trackerWriteMessage is the consent sentence story-to-spec's jira-update node
// writes into its own dispatch — template text, not a record of anything.
const trackerWriteMessage = "The user approved the tracker update: sync the story to the spec"

// trackerNode is node "j" of gatedTrackerRun.
var trackerNode = Node{ID: "j", Type: NodeSend, Role: "plan", Action: "jira-write", Message: trackerWriteMessage}

// gatedTrackerRun builds a → gate → tracker-write node "j", created by an
// agent and unapproved.
func gatedTrackerRun(t *testing.T) *GraphRun {
	t.Helper()
	return createTestRun(t, &Graph{
		Name:  "tracker",
		Start: "a",
		Nodes: []Node{
			{ID: "a", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve the tracker update"},
			trackerNode,
		},
		Edges: []Edge{{From: "a", To: "gate"}, {From: "gate", To: "j"}},
	})
}

// trackerDispatch is the message the executor sends for node "j".
func trackerDispatch(run *GraphRun) Message {
	n := trackerNode
	return graphSendMessage(runTestSession, run, &n)
}

// trackerRefusals returns the details of the session's tracker-write-refused rows.
func trackerRefusals(t *testing.T) []string {
	t.Helper()
	entries, err := ReadLifecycleLog(runTestSession)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Event == "tracker-write-refused" {
			out = append(out, e.Detail)
		}
	}
	return out
}

// TestGraphTrackerWriteRefusedWithoutProvenance is MUX-181 Phase 1's first pin,
// inverted: a daemon-sent tracker write naming no run or node is refused for
// every action the validator gates, never reaches plan, and is logged.
func TestGraphTrackerWriteRefusedWithoutProvenance(t *testing.T) {
	pinCompiledAuthorities(t)
	for _, action := range []string{"jira-write", "confluence-write", "issue-write"} {
		useTempBusDir(t)
		before := len(trackerRefusals(t))
		m := NewMessage(graphSender, "plan", "request", action, trackerWriteMessage, "")
		if err := SendNoCC(runTestSession, m); err == nil {
			t.Errorf("%s: a daemon tracker write with no gate provenance was delivered", action)
		}
		if msgs, _ := Peek(runTestSession, "plan"); len(msgs) != 0 {
			t.Errorf("%s: plan inbox = %+v, want empty", action, msgs)
		}
		rows := trackerRefusals(t)
		if len(rows) != before+1 || !strings.Contains(rows[len(rows)-1], "daemon→plan:"+action) ||
			!strings.Contains(rows[len(rows)-1], "no graph provenance") {
			t.Errorf("%s: tracker-write-refused rows = %q, want one naming the send and the reason", action, rows[before:])
		}
	}
}

// TestGraphTrackerWriteJudgedOnGateApproval is MUX-181 Phase 1's second pin,
// inverted into the discriminating pair: the executor's own dispatch for a
// gated jira-write node, sender unchanged, is refused while its gate is
// unapproved and delivered with its run and node once a person approves. Without
// the second half a check that refused every graph tracker write would pass.
func TestGraphTrackerWriteJudgedOnGateApproval(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "auto")
	run := gatedTrackerRun(t)

	if err := SendNoCC(runTestSession, trackerDispatch(run)); err == nil {
		t.Fatal("a graph jira-write was delivered with its gate unapproved")
	}
	if msgs, _ := Peek(runTestSession, "plan"); len(msgs) != 0 {
		t.Fatalf("plan inbox = %+v, want empty while the gate is unapproved", msgs)
	}
	if rows := trackerRefusals(t); len(rows) == 0 || !strings.Contains(rows[len(rows)-1], "run="+run.ID+" node=j") {
		t.Errorf("tracker-write-refused rows = %q, want one naming the run and node", rows)
	}

	pinActor(t, "")
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("a person's approval was refused: %v", err)
	}
	if err := SendNoCC(runTestSession, trackerDispatch(run)); err != nil {
		t.Fatalf("after a person's audited approval the dispatch was refused: %v", err)
	}
	if msgs, _ := Peek(runTestSession, "plan"); len(msgs) != 1 || msgs[0].GraphRun != run.ID || msgs[0].GraphNode != "j" {
		t.Errorf("plan inbox = %+v, want the dispatch with its run and node", msgs)
	}
}

// TestGraphTrackerWriteRefusals: with the gate approved, each way a dispatch can
// fail to be the work that approval released is still refused — so the approval
// authorizes one node's one payload, not the run.
func TestGraphTrackerWriteRefusals(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "auto")
	run := gatedTrackerRun(t)
	pinActor(t, "")
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatal(err)
	}
	with := func(edit func(*Message)) Message {
		m := trackerDispatch(run)
		edit(&m)
		return m
	}
	cases := map[string]struct {
		m    Message
		want string
	}{
		"unknown run":            {with(func(m *Message) { m.GraphRun = "no-such-run" }), "cannot be read"},
		"node not in graph":      {with(func(m *Message) { m.GraphNode = "ghost" }), "not in the frozen graph"},
		"node of another kind":   {with(func(m *Message) { m.GraphNode = "a" }), `dispatches to "build"`},
		"payload rewritten":      {with(func(m *Message) { m.Payload = "Transition every open story to Done" }), "payload is not what node"},
		"template sentence only": {with(func(m *Message) { m.Payload = trackerWriteMessage }), "payload is not what node"},
	}
	for name, tc := range cases {
		deny := CheckTrackerWriteForMessage(runTestSession, tc.m)
		if !strings.Contains(deny, tc.want) {
			t.Errorf("%s: deny = %q, want a refusal naming %q", name, deny, tc.want)
		}
	}
	if deny := CheckTrackerWriteForMessage(runTestSession, trackerDispatch(run)); deny != "" {
		t.Errorf("the genuine dispatch beside them was refused: %q", deny)
	}
}

// TestGraphTrackerWriteRefusesForgedApproval: a hand-written approval marker
// with no graph-gate-approved audit row releases nothing.
func TestGraphTrackerWriteRefusesForgedApproval(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "auto")
	run := gatedTrackerRun(t)
	if err := os.MkdirAll(graphApprovalsDir(runTestSession, run.ID), 0755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(graphApprovalPath(runTestSession, run.ID, "gate", "approved"),
		map[string]any{"approved_at": time.Now().Unix(), "approved_by": ActorUser}); err != nil {
		t.Fatal(err)
	}
	if deny := CheckTrackerWriteForMessage(runTestSession, trackerDispatch(run)); deny == "" {
		t.Error("a forged approval marker released a graph jira-write")
	}
}

// TestTrackerWriteCheckSharesValidatorList: the send-time check and the
// validator's gate rule answer alike for every tracker action, read or write,
// because both read gatedTrackerWriteActions — an action gated by one and not
// the other fails here.
func TestTrackerWriteCheckSharesValidatorList(t *testing.T) {
	pinCompiledAuthorities(t)
	useTempBusDir(t)
	actions := []string{"jira-read", "confluence-read", "issue-read", "update-docs", "story-read"}
	for a := range gatedTrackerWriteActions {
		actions = append(actions, a)
	}
	for _, a := range actions {
		gated := nodeRequiresGate(&Node{Type: NodeSend, Role: "plan", Action: a})
		checked := CheckTrackerWriteForMessage(runTestSession, NewMessage(graphSender, "plan", "request", a, "x", "")) != ""
		if gated != checked {
			t.Errorf("%s: validator requires a gate = %v, send-time check refuses an ungated dispatch = %v", a, gated, checked)
		}
	}
}

// TestTrackerWriteNegativeControls: edit relays the user, so its tracker writes
// pass untouched, and a graph read stays open.
func TestTrackerWriteNegativeControls(t *testing.T) {
	pinCompiledAuthorities(t)
	for _, m := range []Message{
		NewMessage("edit", "plan", "request", "jira-write", "User asked to update PROMGT-118", ""),
		NewMessage("edit", "plan", "request", "issue-write", "User asked to file an issue for MUX-196", ""),
		NewMessage(graphSender, "plan", "request", "jira-read", "Read PROMGT-118", ""),
	} {
		useTempBusDir(t)
		if err := SendNoCC(runTestSession, m); err != nil {
			t.Errorf("%s→plan:%s refused: %v", m.From, m.Action, err)
		}
		if msgs, _ := Peek(runTestSession, "plan"); len(msgs) != 1 {
			t.Errorf("%s→plan:%s: plan inbox = %+v, want it delivered", m.From, m.Action, msgs)
		}
	}
}

// TestGraphTrackerWriteReachesPlanThroughExecutor is the positive control on
// the machinery that runs it: an agent's run, released by a person, dispatches
// its jira-write to plan carrying run and node, refused by nothing.
func TestGraphTrackerWriteReachesPlanThroughExecutor(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "auto")
	run := gatedTrackerRun(t)

	step(t, runTestSession, run.ID)
	completeSendNode(t, runTestSession, run.ID, "a", OutcomeSuccess)
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q, want waiting", s)
	}
	pinActor(t, "")
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("a person's approval was refused: %v", err)
	}
	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "j"); s != GraphNodeRunning {
		t.Fatalf("tracker node state %q, want running — an approved gate must still release the dispatch", s)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 1 || msgs[0].Action != "jira-write" || msgs[0].GraphRun != run.ID || msgs[0].GraphNode != "j" {
		t.Fatalf("plan inbox = %+v, want one jira-write carrying run %s and node j", msgs, run.ID)
	}
	if rows := trackerRefusals(t); len(rows) != 0 && strings.Contains(rows[len(rows)-1], run.ID) {
		t.Errorf("the executor's approved dispatch was refused: %q", rows)
	}
}

// TestBuiltinTrackerWritesDispatchAfterGate reads every tracker-write send node
// from the builtin templates: each one's real dispatch — interpolated and
// seeded as the executor builds it — is refused before its gates are approved
// and passes once a person approves them.
func TestBuiltinTrackerWritesDispatchAfterGate(t *testing.T) {
	pinCompiledAuthorities(t)
	found := 0
	for name, raw := range builtinGraphJSON {
		g, err := ParseGraph([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var writes []string
		for _, n := range g.Nodes {
			if n.Type == NodeSend && gatedTrackerWriteActions[n.Action] {
				writes = append(writes, n.ID)
			}
		}
		if len(writes) == 0 {
			continue
		}
		pinActor(t, "auto")
		run := createTestRun(t, g)
		frozen, err := ReadGraphRunGraph(runTestSession, run.ID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		byID := make(map[string]*Node, len(frozen.Nodes))
		for i := range frozen.Nodes {
			byID[frozen.Nodes[i].ID] = &frozen.Nodes[i]
		}
		found += len(writes)
		for _, id := range writes {
			if deny := CheckTrackerWriteForMessage(runTestSession, graphSendMessage(runTestSession, run, byID[id])); deny == "" {
				t.Errorf("%s/%s: dispatched before any gate was approved", name, id)
			}
		}
		pinActor(t, "")
		for _, gate := range frozen.Nodes {
			if gate.Type != NodeWaitHuman {
				continue
			}
			for _, id := range writes {
				if frozen.gateTerritory(byID, gate.ID)[id] {
					if err := ApproveGraphGate(runTestSession, run.ID, gate.ID); err != nil {
						t.Fatalf("%s: approving %s: %v", name, gate.ID, err)
					}
					break
				}
			}
		}
		for _, id := range writes {
			if deny := CheckTrackerWriteForMessage(runTestSession, graphSendMessage(runTestSession, run, byID[id])); deny != "" {
				t.Errorf("%s/%s: refused after its gates were approved: %s", name, id, deny)
			}
		}
	}
	if found == 0 {
		t.Fatal("no builtin template sends a tracker write — the check above tested nothing")
	}
}

// TestGraphDispatchOriginNamesRunAndNode is MUX-165 Phase 1: the line plan's
// consent rule reads. A gate-approved tracker write delivered to plan renders
// `Origin: graph run <run> · node <node>`; a daemon message carrying no run or
// node renders no graph run at all — the tracker write is refused at the send,
// and the delivered non-tracker message says it carries no user request.
func TestGraphDispatchOriginNamesRunAndNode(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "auto")
	run := gatedTrackerRun(t)
	pinActor(t, "")
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatal(err)
	}
	if err := SendNoCC(runTestSession, trackerDispatch(run)); err != nil {
		t.Fatalf("the approved dispatch was refused: %v", err)
	}
	msgs, _ := Peek(runTestSession, "plan")
	if len(msgs) != 1 {
		t.Fatalf("plan inbox = %+v, want the dispatch", msgs)
	}
	if out := FormatMessage(msgs[0]); !strings.Contains(out, "Origin: graph run "+run.ID+" · node j ·") {
		t.Errorf("the dispatch's origin line does not name run and node:\n%s", out)
	}

	bare := NewMessage(graphSender, "plan", "request", "update-docs", "The user approved the tracker update", "")
	if err := SendNoCC(runTestSession, bare); err != nil {
		t.Fatalf("a daemon update-docs was refused: %v", err)
	}
	for _, m := range mustPeek(t, runTestSession, "plan") {
		if m.ID != bare.ID {
			continue
		}
		out := FormatMessage(m)
		if strings.Contains(out, "graph run") || !strings.Contains(out, "Origin: sent by daemon, not by the user") {
			t.Errorf("an unprovenanced daemon message renders a graph origin, or not its sender:\n%s", out)
		}
		return
	}
	t.Fatal("the unprovenanced daemon message did not reach plan")
}
