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

func TestForceDeliver_UnknownRole(t *testing.T) {
	session := "deliver-test-unknown"
	deliverTestSetup(t, session, "❯ \n")
	if _, err := ForceDeliver(session, "nonsense", true); err == nil {
		t.Error("expected error for unknown role")
	}
}
