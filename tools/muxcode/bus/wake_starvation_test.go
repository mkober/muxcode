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

// codexIdleReviewFrame is the codex review pane at 10:58 — idle at an empty
// composer, with the warning footer that diagnose's wording pointed at.
const codexIdleReviewFrame = "• The inbox is empty; nothing is pending.\n\n" +
	"  Worked for 1m 7s · 11:01 AM\n\n" +
	"› Ask Codex to do anything\n\n" +
	"  GPT-6-Astra medium · ~/Repos/mkober/muxcode · Review new messages ⚠ 1 warning · f2 to view\n"

// codexWorkingReviewFrame is a codex review pane mid-turn.
const codexWorkingReviewFrame = "• Working (12s • esc to interrupt)\n\n" +
	"› Ask Codex to do anything\n\n" +
	"  GPT-6-Astra medium · ~/Repos/mkober/muxcode\n"

// starvedReviewFixture reproduces the MUX-192 state for a codex review role:
// edit's review request tracked in flight taskAge seconds, and a newer request
// pending in review's inbox. A non-empty replyAction logs review's reply to
// the chain's request id with that action, so with the task's own action the
// task is complete in fact and in-flight on disk. The pending request is
// force-sent, as edit's was at 11:06 — an unforced Send is suppressed by the
// in-flight task it shares a (from, to, action) with.
func starvedReviewFixture(t *testing.T, session string, taskAge int64, replyAction string) {
	t.Helper()
	t.Setenv("BUS_SESSION", session)
	useTempBusDir(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	t.Setenv(RoleCLIEnvVar("review"), "codex")
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	stale := Message{
		ID: starvedTaskID, From: "edit", To: "review", Type: "request",
		Action: "review", Payload: "review the branch", TS: time.Now().Unix() - taskAge,
	}
	if err := CreateTask(session, stale, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	pending := NewMessage("edit", "review", "request", "review", "review the fix", "")
	if err := SendForce(session, pending); err != nil {
		t.Fatalf("SendForce pending: %v", err)
	}
	if replyAction == "" {
		return
	}
	reply := NewMessage("review", "edit", "response", replyAction, "LGTM", "1790347737-test-chain")
	if err := Send(session, reply); err != nil {
		t.Fatalf("Send reply: %v", err)
	}
	if task, err := ReadTask(session, starvedTaskID); err != nil || task.Status != TaskInFlight {
		t.Fatalf("fixture: edit's task must still read in-flight, got %+v (%v)", task, err)
	}
}

// TestCodexSendWakeUp_StaleOrAnsweredTaskDoesNotStarveWake is the MUX-192
// delivery pin inverted: an unforced wake to an idle codex agent is no longer
// refused by an in-flight task past the send grace, nor by a fresh one whose
// request was answered under another id for the same action — the pending
// request is typed into the pane.
func TestCodexSendWakeUp_StaleOrAnsweredTaskDoesNotStarveWake(t *testing.T) {
	for _, tc := range []struct {
		name        string
		taskAge     int64
		replyAction string
	}{
		{"stale-unanswered", 300, ""},
		{"stale-answered-elsewhere", 300, "review"},
		{"fresh-answered-elsewhere", 1, "review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "mux192-wake-" + tc.name
			starvedReviewFixture(t, session, tc.taskAge, tc.replyAction)
			calls := stubInjectionPane(t, codexIdleReviewFrame, nil)

			err := (&CodexProvider{}).SendWakeUp(session, "review", false)
			if errors.Is(err, ErrInjectionSkipped) {
				t.Fatalf("the wake must not be skipped, got %v", err)
			}
			if !strings.Contains(strings.Join(sentKeys(*calls), "\n"), "review the fix") {
				t.Errorf("the pending request must be typed into the idle pane, sent %v", sentKeys(*calls))
			}
		})
	}
}

// TestCodexSendWakeUp_BusySignalsStillSkip is the MUX-192 negative control: a
// fresh unanswered task — or one answered only under a different action —
// still withholds the wake, and so does a mid-turn pane behind a stale task.
// Each skip types nothing and writes one wake-skipped row naming the role.
func TestCodexSendWakeUp_BusySignalsStillSkip(t *testing.T) {
	for _, tc := range []struct {
		name        string
		taskAge     int64
		replyAction string
		frame       string
		wantInRow   string
	}{
		{"fresh-unanswered", 1, "", codexIdleReviewFrame, starvedTaskID},
		{"fresh-answered-other-action", 1, "plan", codexIdleReviewFrame, starvedTaskID},
		{"stale-task-mid-turn", 300, "", codexWorkingReviewFrame, "mid-turn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "mux192-skip-" + tc.name
			starvedReviewFixture(t, session, tc.taskAge, tc.replyAction)
			calls := stubInjectionPane(t, tc.frame, nil)

			err := (&CodexProvider{}).SendWakeUp(session, "review", false)
			if !errors.Is(err, ErrInjectionSkipped) {
				t.Fatalf("expected the wake skipped (ErrInjectionSkipped), got %v", err)
			}
			if keys := sentKeys(*calls); len(keys) != 0 {
				t.Errorf("a skipped wake must type nothing, sent %v", keys)
			}
			rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "wake-skipped"})
			if len(rows) != 1 || !strings.Contains(rows[0].Detail, "review") || !strings.Contains(rows[0].Detail, tc.wantInRow) {
				t.Errorf("expected one wake-skipped row naming review and %q, got %+v", tc.wantInRow, rows)
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
