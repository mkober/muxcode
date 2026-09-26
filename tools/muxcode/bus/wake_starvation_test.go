package bus

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// starvedTaskID is edit's tracked review request from the MUX-192 10:57
// incident — answered by review under the chain's id, so it stayed in flight.
const starvedTaskID = "1790347799-edit-dfae26e9"

// starvedReviewFixture reproduces the MUX-192 state for a codex review role:
// edit's review request tracked in flight for 300s, and a newer request
// pending in review's inbox. With answered, review's reply to the chain's
// request id is in the session log, so the task is complete in fact and
// in-flight on disk. The pending request is force-sent, as edit's was at
// 11:06 — an unforced Send is suppressed by the in-flight task it shares a
// (from, to, action) with.
func starvedReviewFixture(t *testing.T, session string, answered bool) {
	t.Helper()
	t.Setenv("BUS_SESSION", session)
	useTempBusDir(t)
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	stale := Message{
		ID: starvedTaskID, From: "edit", To: "review", Type: "request",
		Action: "review", Payload: "review the branch", TS: time.Now().Unix() - 300,
	}
	if err := CreateTask(session, stale, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	pending := NewMessage("edit", "review", "request", "review", "review the fix", "")
	if err := SendForce(session, pending); err != nil {
		t.Fatalf("SendForce pending: %v", err)
	}
	if !answered {
		return
	}
	reply := NewMessage("review", "edit", "response", "review", "LGTM", "1790347737-test-chain")
	if err := Send(session, reply); err != nil {
		t.Fatalf("Send reply: %v", err)
	}
	if _, ok := FindResponseSince(session, "review", "edit", stale.TS); !ok {
		t.Fatal("fixture: review's answer must be findable in the log")
	}
	if task, err := ReadTask(session, starvedTaskID); err != nil || task.Status != TaskInFlight {
		t.Fatalf("fixture: edit's task must still read in-flight, got %+v (%v)", task, err)
	}
}

// TestCodexSendWakeUp_StaleTaskStarvesWake_Pin pins the MUX-192 delivery
// defect as it stands: an unforced wake to a codex agent is refused by an
// in-flight task of any age, answered or not. Phase 2 inverts both halves —
// a stale or answered task must no longer block the wake.
func TestCodexSendWakeUp_StaleTaskStarvesWake_Pin(t *testing.T) {
	for _, answered := range []bool{false, true} {
		name := "unanswered"
		if answered {
			name = "answered-elsewhere"
		}
		t.Run(name, func(t *testing.T) {
			session := "mux192-pin-wake-" + name
			starvedReviewFixture(t, session, answered)

			err := (&CodexProvider{}).SendWakeUp(session, "review", false)
			if !errors.Is(err, ErrInjectionSkipped) {
				t.Fatalf("pin: expected today's skip (ErrInjectionSkipped), got %v", err)
			}
			if !strings.Contains(err.Error(), shortID(starvedTaskID)) {
				t.Errorf("pin: the skip should name the blocking task %s, got %v", shortID(starvedTaskID), err)
			}
		})
	}
}

// TestDiagnose_CodexStarvedReportsConstantIdle_Pin pins the MUX-192 diagnosis
// defect from the 10:58 report: for a codex role, whose IsIdle is a constant
// false, diagnose emits active-with-stale-messages on "IsAgentIdle: false" and
// never names the in-flight task that is actually blocking the wake. Phase 3
// inverts it — a wake-blocked-by-task finding naming the task.
func TestDiagnose_CodexStarvedReportsConstantIdle_Pin(t *testing.T) {
	report := &DiagnosticReport{
		Role: "review",
		AgentState: AgentStateEvidence{
			IsIdle: false, IsAlive: true, WiderCaptureIdle: false,
			Provider: "codex", SupportsHooks: true,
			PaneLastLine: "GPT-6-Astra medium · ~/Repos/mkober/muxcode · Review new messages ⚠ 1 warning · f2 to view",
		},
		InboxState: InboxStateEvidence{MessageCount: 1, ActionableCount: 1, OldestMessageAge: 181},
		NotifyState: NotifyStateEvidence{
			UnnotifiedCount: 1, AckDelivery: true,
			ReceiptGapCount: 1, ReceiptGapAge: 181,
		},
	}
	RunDiagnostics(report)

	var stale *DiagnosticFinding
	for i := range report.Findings {
		if report.Findings[i].FailureMode == "active-with-stale-messages" {
			stale = &report.Findings[i]
		}
	}
	if stale == nil {
		t.Fatalf("pin: expected today's active-with-stale-messages finding, got %+v", report.Findings)
	}
	if !strings.Contains(strings.Join(stale.Evidence, "\n"), "IsAgentIdle: false") {
		t.Errorf("pin: expected the constant-idle evidence line, got %v", stale.Evidence)
	}
	for _, f := range report.Findings {
		all := strings.Join(append(append([]string{f.Summary}, f.Evidence...), f.Remediation...), "\n")
		if strings.Contains(all, starvedTaskID) || strings.Contains(all, shortID(starvedTaskID)) {
			t.Errorf("pin: today no finding names the blocking task, but %s does", f.FailureMode)
		}
	}
}
