package bus

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReadAgentDefHash_RoundtripAndEmpty(t *testing.T) {
	base := t.TempDir()
	SetBusDirBase(base)
	defer ResetBusDirBase()

	session := "tdef"
	role := "build"
	if err := os.MkdirAll(BusDir(session), 0o755); err != nil {
		t.Fatal(err)
	}

	// No stamp yet → empty.
	if got := ReadAgentDefHash(session, role); got != "" {
		t.Fatalf("ReadAgentDefHash before stamp: got %q, want empty", got)
	}

	// Write a stamp via the path helper and read it back.
	want := "deadbeefcafe"
	if err := os.WriteFile(agentDefHashPath(session, role), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadAgentDefHash(session, role); got != want {
		t.Fatalf("ReadAgentDefHash roundtrip: got %q, want %q", got, want)
	}
}

func TestAgentDefHash_Deterministic(t *testing.T) {
	// AgentDefHash may be empty if no definition resolves in this environment;
	// when present it must be a stable 64-char hex string.
	h1 := AgentDefHash("build")
	h2 := AgentDefHash("build")
	if h1 != h2 {
		t.Fatalf("AgentDefHash not deterministic: %q vs %q", h1, h2)
	}
	if h1 != "" && len(h1) != 64 {
		t.Fatalf("AgentDefHash length: got %d, want 64 (hash=%q)", len(h1), h1)
	}
}

func TestResolvedAgentFileForRole_UnknownRole(t *testing.T) {
	if got := ResolvedAgentFileForRole("nonexistent-role"); got != "" {
		t.Fatalf("ResolvedAgentFileForRole(unknown): got %q, want empty", got)
	}
}

func TestStampAgentDefHash_NoDefIsNoop(t *testing.T) {
	base := t.TempDir()
	SetBusDirBase(base)
	defer ResetBusDirBase()

	session := "tdef2"
	role := "nonexistent-role" // AgentDefHash → "" so stamp is a no-op
	if err := os.MkdirAll(BusDir(session), 0o755); err != nil {
		t.Fatal(err)
	}

	StampAgentDefHash(session, role)
	if got := ReadAgentDefHash(session, role); got != "" {
		t.Fatalf("StampAgentDefHash for unknown role wrote %q, want no stamp", got)
	}
}

const codexIdlePane = "• Build succeeded. EXIT=0\n\n› Ask Codex to do anything\n"

// fakeReloadPane answers capture-pane with content and err at the tmux boundary.
func fakeReloadPane(t *testing.T, content string, err error) {
	t.Helper()
	orig := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return content, err
		}
		return orig(args...)
	}
	t.Cleanup(func() { tmuxOutputRunner = orig })
}

// Codex's IsIdle is constantly false, so gating on it deferred every Codex
// reload forever: an idle pane must read safe. Everything else must not — a
// failed capture says nothing, and an approval prompt parks a live turn with no
// working indicator.
func TestAutoReloadSafe_CodexPaneStates(t *testing.T) {
	cases := []struct {
		name, pane, reason string
		err                error
	}{
		{name: "idle", pane: codexIdlePane},
		{name: "working", pane: "• Working (12s • esc to interrupt)\n", reason: "agent working"},
		{name: "approval prompt", pane: codexApprovalPromptPane, reason: "prompt awaiting input"},
		{name: "capture failed", err: errors.New("no such pane"), reason: "pane unreadable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			session := sandboxTestEnv(t)
			t.Setenv("MUXCODE_BUILD_CLI", "codex")
			fakeReloadPane(t, c.pane, c.err)
			ok, reason := AutoReloadSafe(session, "build")
			if ok != (c.reason == "") || !strings.Contains(reason, c.reason) {
				t.Errorf("AutoReloadSafe = %v %q, want safe=%v reason %q", ok, reason, c.reason == "", c.reason)
			}
		})
	}
}

// buildThenTestRun creates a running build→test graph run with build running
// and test next to dispatch, the test role an idle Codex pane with no inbox or
// task — the daemon's view at the check on 2026-11-13 20:42:16.
func buildThenTestRun(t *testing.T) *GraphRun {
	t.Helper()
	run := createTestRun(t, &Graph{
		Name:  "t",
		Start: "build",
		Nodes: []Node{
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "test", Type: NodeSend, Role: "test", Action: "test", Message: "run the suite"},
		},
		Edges: []Edge{{From: "build", To: "test"}},
	})
	useEmptyShellConfig(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	t.Setenv("MUXCODE_TEST_CLI", "codex")
	fakeReloadPane(t, codexIdlePane, nil)
	if err := TransitionGraphNode(runTestSession, run.ID, "build", GraphNodeRunning, nil); err != nil {
		t.Fatalf("transition build (the start node, already ready) to running: %v", err)
	}
	return run
}

// MUX-217, the Phase 1 pin inverted: the watchdog's check ran in the poll
// iteration that dispatched test, before the dispatch, so only the graph can
// say the role is about to be used. A role a running run dispatches to next,
// and one whose send node runs before its dispatch task exists, defer with the
// run and node named; once the run is done with the role the deferred reload
// is safe again, so the definition change is not lost.
func TestAutoReloadSafe_DefersWhileGraphNeedsRole(t *testing.T) {
	run := buildThenTestRun(t)
	wantDeferred := func(when string) {
		t.Helper()
		ok, reason := AutoReloadSafe(runTestSession, "test")
		if ok || !strings.HasPrefix(reason, GraphReloadDeferralPrefix+run.ID) || !strings.HasSuffix(reason, "as node test") {
			t.Errorf("%s: AutoReloadSafe = %v %q, want deferred naming %s and node test", when, ok, reason, run.ID)
		}
	}
	wantDeferred("build running, test next")

	// Routed: the executor fires build's edge before test can become ready.
	if err := TransitionGraphNode(runTestSession, run.ID, "build", GraphNodeDone,
		func(s *GraphNodeStatus) { s.Outcome, s.Routed = OutcomeSuccess, true }); err != nil {
		t.Fatalf("finish build: %v", err)
	}
	for _, state := range []string{GraphNodeReady, GraphNodeRunning} {
		if err := TransitionGraphNode(runTestSession, run.ID, "test", state, nil); err != nil {
			t.Fatalf("transition test to %s: %v", state, err)
		}
	}
	wantDeferred("test running before its dispatch task exists")

	// PR #196 review: with the dispatch queued and in flight the reason must
	// still name the run and node, not the generic inbox or task clause.
	dispatch := NewMessage(graphSender, "test", "request", "test", "run the suite", "")
	if err := Send(runTestSession, dispatch); err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(runTestSession, dispatch, 600); err != nil {
		t.Fatal(err)
	}
	if err := MutateNodeStatus(runTestSession, run.ID, "test", func(s *GraphNodeStatus) { s.TaskID = dispatch.ID }); err != nil {
		t.Fatal(err)
	}
	wantDeferred("test's dispatch queued and in flight")

	if _, err := Receive(runTestSession, "test"); err != nil {
		t.Fatal(err)
	}
	CompleteTask(runTestSession, dispatch.ID, "resp-1")
	if err := TransitionGraphNode(runTestSession, run.ID, "test", GraphNodeDone,
		func(s *GraphNodeStatus) { s.Outcome = OutcomeSuccess }); err != nil {
		t.Fatalf("finish test: %v", err)
	}
	if ok, reason := AutoReloadSafe(runTestSession, "test"); !ok {
		t.Errorf("the run is done with test: the deferred reload must be safe, got %q", reason)
	}
}

// A loop re-entering build leaves check and test done and routed from the last
// lap; build running again still defers test, found only by looking through
// the routed condition. A feeder parked on a human gate does not —
// a run waiting for approval must not hold a reload for hours.
func TestGraphNeedsRole_LoopReentryAndParkedGate(t *testing.T) {
	run := createTestRun(t, &Graph{
		Name:  "t",
		Start: "build",
		Nodes: []Node{
			{ID: "build", Type: NodeSend, Role: "build", Action: "build", Message: "go"},
			{ID: "check", Type: NodeCondition, Conditions: map[string]any{"output_contains": "EXIT=0"}},
			{ID: "test", Type: NodeSend, Role: "test", Action: "test", Message: "run"},
			{ID: "gate", Type: NodeWaitHuman, Message: "approve"},
			{ID: "review", Type: NodeSend, Role: "review", Action: "review", Message: "review"},
		},
		Edges: []Edge{{From: "build", To: "check"}, {From: "check", To: "test"}, {From: "test", To: "gate"}, {From: "gate", To: "review"}},
	})
	for _, id := range []string{"check", "test"} {
		finish(t, run.ID, id, GraphNodeDone, OutcomeSuccess)
		if err := MutateNodeStatus(runTestSession, run.ID, id, func(s *GraphNodeStatus) { s.Routed = true }); err != nil {
			t.Fatalf("route prior-lap %s: %v", id, err)
		}
	}
	if err := TransitionGraphNode(runTestSession, run.ID, "build", GraphNodeRunning, nil); err != nil {
		t.Fatalf("re-enter build: %v", err)
	}
	if runID, nodeID, ok := GraphNeedsRole(runTestSession, "test"); !ok || runID != run.ID || nodeID != "test" {
		t.Errorf("build re-running through a condition: GraphNeedsRole(test) = %q %q %v, want %s test true", runID, nodeID, ok, run.ID)
	}

	for _, state := range []string{GraphNodeReady, GraphNodeWaiting} {
		if err := TransitionGraphNode(runTestSession, run.ID, "gate", state, nil); err != nil {
			t.Fatalf("gate to %s: %v", state, err)
		}
	}
	if _, nodeID, ok := GraphNeedsRole(runTestSession, "review"); ok {
		t.Errorf("a feeder parked on a human gate deferred review's reload (node %s)", nodeID)
	}
}

// The MUX-217 negative control, green before and after the fix: with no graph
// run, no inbox and no task, an idle pane reads safe, so a definition change
// still reloads.
func TestAutoReloadSafe_IdleRoleWithNoRunIsSafe(t *testing.T) {
	session := sandboxTestEnv(t)
	t.Setenv("MUXCODE_TEST_CLI", "codex")
	fakeReloadPane(t, codexIdlePane, nil)
	if ok, reason := AutoReloadSafe(session, "test"); !ok {
		t.Errorf("an idle role with no run and no pending work must reload, got unsafe %q", reason)
	}
}

func TestAutoReloadSafe_PendingWorkBlocks(t *testing.T) {
	cases := map[string]func(session string) error{
		"pending request": func(s string) error {
			return Send(s, NewMessage("edit", "build", "request", "build", "go", ""))
		},
		"in-flight task": func(s string) error {
			return CreateTask(s, NewMessage("edit", "build", "request", "build", "go", ""), 600)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			session := sandboxTestEnv(t)
			t.Setenv("MUXCODE_BUILD_CLI", "codex")
			fakeReloadPane(t, codexIdlePane, nil)
			if err := setup(session); err != nil {
				t.Fatalf("setup: %v", err)
			}
			if ok, _ := AutoReloadSafe(session, "build"); ok {
				t.Errorf("%s did not block the reload", name)
			}
		})
	}
}
