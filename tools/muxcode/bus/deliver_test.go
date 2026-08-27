package bus

import (
	"os"
	"strings"
	"testing"
)

// deliverTestSetup wires a temp bus dir and mocked tmux runners. captureContent
// is returned for every capture-pane/display-message query (control idle state).
// Returns a pointer to the captured send-keys/run calls.
func deliverTestSetup(t *testing.T, session, captureContent string) *[][]string {
	t.Helper()
	// Isolate provider resolution from the ambient environment. ResolveProvider
	// reads runtime overrides keyed off BUS_SESSION, then per-role CLI env vars
	// (MUXCODE_RUN_CLI), then the global one (MUXCODE_AGENT_CLI), then the role
	// default (run → opencode). Without pinning, a live session's override or a
	// leaked MUXCODE_RUN_CLI=opencode would route SendWakeUpWithText down a
	// non-hook provider path that never performs the `send-keys -l` injection
	// these tests assert. Point the override lookup at this test's (override-free)
	// session and force the Claude hook path. All ForceDeliver tests target the
	// "run" role, so pin its per-role CLI var (highest-precedence env source).
	t.Setenv("BUS_SESSION", session)
	t.Setenv("MUXCODE_AGENT_CLI", "claude")
	t.Setenv(RoleCLIEnvVar("run"), "claude")
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(BusDir(session)) })

	origRun := tmuxRunner
	origQuiet := tmuxQuietRunner
	origOutput := tmuxOutputRunner
	var calls [][]string
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	tmuxQuietRunner = func(args ...string) error { return nil } // has-session → exists
	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "window_active") {
			return "0", nil // not focused
		}
		return captureContent, nil // capture-pane content
	}
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxQuietRunner = origQuiet
		tmuxOutputRunner = origOutput
	})
	return &calls
}

func sendTestRequest(t *testing.T, session, to, id string) {
	t.Helper()
	m := Message{ID: id, From: "edit", To: to, Type: "request", Action: "run", Payload: "do the thing"}
	if err := SendNoCC(session, m); err != nil {
		t.Fatalf("SendNoCC: %v", err)
	}
}

func TestForceDeliver_NoMessages(t *testing.T) {
	session := "deliver-test-none"
	deliverTestSetup(t, session, "❯ \n")

	res, err := ForceDeliver(session, "run", true)
	if err != nil {
		t.Fatalf("ForceDeliver: %v", err)
	}
	if res.Delivered != 0 || res.Skipped == "" {
		t.Errorf("expected nothing delivered, got %+v", res)
	}
}

func TestForceDeliver_ForceInjectsAndMarksNotified(t *testing.T) {
	session := "deliver-test-force"
	calls := deliverTestSetup(t, session, "❯ \n") // idle, empty composer
	sendTestRequest(t, session, "run", "MSG-1")

	res, err := ForceDeliver(session, "run", true)
	if err != nil {
		t.Fatalf("ForceDeliver: %v", err)
	}
	if res.Delivered != 1 {
		t.Fatalf("expected 1 delivered, got %d (%+v)", res.Delivered, res)
	}

	// A send-keys text injection should have happened, in the dash-safe
	// form: `-l -- <payload>`. The -- separator is asserted at argv level
	// because MUX-104's failure was tmux flag parsing — a dash-leading
	// payload without -- is rejected as `invalid flag -`.
	sawText := false
	for _, c := range *calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "send-keys") && strings.Contains(j, "-l") {
			sawText = true
			if len(c) < 2 || c[len(c)-2] != "--" {
				t.Errorf("text injection lacks the -- separator before the payload: %v", c)
			}
		}
	}
	if !sawText {
		t.Errorf("expected a literal send-keys text injection, got %v", *calls)
	}

	// The message must now be marked notified (won't re-deliver).
	if got := UnnotifiedMessages(session, "run"); len(got) != 0 {
		t.Errorf("expected message marked notified, still unnotified: %d", len(got))
	}
}

func TestForceDeliver_NoForceRequiresIdlePrompt(t *testing.T) {
	session := "deliver-test-busy"
	// Capture shows an active spinner line, no idle ❯ prompt.
	deliverTestSetup(t, session, "Combobulating… (3s · esc to interrupt)\n")
	sendTestRequest(t, session, "run", "MSG-2")

	_, err := ForceDeliver(session, "run", false)
	if err == nil {
		t.Fatal("expected error when agent is not at an idle prompt without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should suggest --force, got: %v", err)
	}
}

// TestTaskStalled pins the stall watchdog's age gate: graph-dispatched
// tasks (From daemon) stall at HALF the threshold — graph runs take
// delivery priority (user rule 2026-08-27) — expired tasks belong to
// the timeout path, and young tasks are left alone.
func TestTaskStalled(t *testing.T) {
	now := int64(10_000)
	mk := func(from string, age int64) Task {
		return Task{From: from, To: "plan", Status: TaskInFlight, SentAt: now - age, Timeout: 600}
	}
	if TaskStalled(mk("edit", 60), now, 90) {
		t.Error("a 60s-old task must not stall at a 90s threshold")
	}
	if !TaskStalled(mk("edit", 90), now, 90) {
		t.Error("a 90s-old task must stall at a 90s threshold")
	}
	if !TaskStalled(mk("daemon", 45), now, 90) {
		t.Error("a graph-dispatched task must stall at HALF the threshold — graph runs take priority")
	}
	if TaskStalled(mk("daemon", 30), now, 90) {
		t.Error("a graph task younger than half the threshold must not stall")
	}
	expired := mk("edit", 700)
	if TaskStalled(expired, now, 90) {
		t.Error("an expired task is the timeout path's business, never a stall")
	}
	// Copilot catch (PR #40): halving a threshold of 1 must clamp to 1,
	// not truncate to 0 (which would stall every graph task instantly).
	if TaskStalled(mk("daemon", 0), now, 1) {
		t.Error("a zero-age graph task must not stall at a clamped 1s threshold")
	}
	if !TaskStalled(mk("daemon", 1), now, 1) {
		t.Error("the clamped 1s threshold must still fire at age 1")
	}
	done := mk("edit", 200)
	done.Status = TaskCompleted
	if TaskStalled(done, now, 90) {
		t.Error("only in-flight tasks can stall")
	}
}

// TestRedriveMessages pins the consumed-but-never-started recovery: a
// role's live in-flight tasks map back to injectable requests, expired
// tasks and other roles' tasks are excluded, and hosted roles resolve
// to their host window.
func TestRedriveMessages(t *testing.T) {
	now := int64(10_000)
	tasks := []Task{
		{ID: "t1", From: "daemon", To: "plan", Action: "verify-spec", Payload: "check the spec", Status: TaskInFlight, SentAt: now - 60, Timeout: 600},
		{ID: "t2", From: "edit", To: "build", Action: "build", Payload: "other role", Status: TaskInFlight, SentAt: now - 60, Timeout: 600},
		{ID: "t3", From: "daemon", To: "plan", Action: "update-docs", Payload: "expired", Status: TaskInFlight, SentAt: now - 5000, Timeout: 600},
		{ID: "t4", From: "edit", To: "docs", Action: "update-docs", Payload: "hosted → plan", Status: TaskInFlight, SentAt: now - 60, Timeout: 600},
	}
	msgs := redriveMessages(tasks, "plan", now)
	if len(msgs) != 2 {
		t.Fatalf("expected the live plan task + hosted docs task, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].ID != "t1" || msgs[0].Action != "verify-spec" || msgs[0].Payload != "check the spec" || msgs[0].Type != "request" {
		t.Errorf("t1 mapped wrong: %+v", msgs[0])
	}
	if msgs[1].ID != "t4" {
		t.Errorf("hosted role's task must resolve to the host window, got %+v", msgs[1])
	}
	if got := redriveMessages(tasks, "review", now); len(got) != 0 {
		t.Errorf("a role with no in-flight tasks must re-drive nothing, got %+v", got)
	}
}

func TestForceDeliver_UnknownRole(t *testing.T) {
	session := "deliver-test-unknown"
	deliverTestSetup(t, session, "❯ \n")
	if _, err := ForceDeliver(session, "nonsense", true); err == nil {
		t.Error("expected error for unknown role")
	}
}
