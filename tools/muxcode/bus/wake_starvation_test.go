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

// starvedDiagnosticReport is the 10:58 `diagnose review` state: an idle codex
// agent reported not-idle by its constant IsIdle, one actionable request 181s
// old and un-receipted, and the given in-flight tasks.
func starvedDiagnosticReport(provider string, tasks []InFlightTaskEvidence) *DiagnosticReport {
	return &DiagnosticReport{
		Role: "review",
		AgentState: AgentStateEvidence{
			IsIdle: false, IsAlive: true, WiderCaptureIdle: false,
			Provider: provider, SupportsHooks: true,
			PaneLastLine: "GPT-6-Astra medium · ~/Repos/mkober/muxcode · Review new messages ⚠ 1 warning · f2 to view",
		},
		InboxState: InboxStateEvidence{MessageCount: 1, ActionableCount: 1, OldestMessageAge: 181},
		NotifyState: NotifyStateEvidence{
			UnnotifiedCount: 1, AckDelivery: true,
			ReceiptGapCount: 1, ReceiptGapAge: 181,
		},
		InFlightTasks: tasks,
	}
}

func findingByMode(report *DiagnosticReport, mode string) *DiagnosticFinding {
	for i := range report.Findings {
		if report.Findings[i].FailureMode == mode {
			return &report.Findings[i]
		}
	}
	return nil
}

// blockingTaskID is a fresh, unanswered request the wake gate still honours.
const blockingTaskID = "1790348497-edit-7dfa92fa"

// TestDiagnose_CodexStarvedNamesBlockingTask is the MUX-192 diagnosis pin
// inverted: for a starved codex role diagnose names the task the wake gate
// blocks on and the listenerless road, keeps the other in-flight tasks as
// context marked non-blocking, its remediation names the blocker and deliver
// --force, and no finding reports the constant IsIdle as "IsAgentIdle: false".
func TestDiagnose_CodexStarvedNamesBlockingTask(t *testing.T) {
	report := starvedDiagnosticReport("codex", []InFlightTaskEvidence{
		{ID: starvedTaskID, From: "edit", Action: "review", AgeSecs: 300, AnsweredBy: "1790347804-review-af895fd9"},
		{ID: blockingTaskID, From: "edit", Action: "review", AgeSecs: 2, BlocksWake: true},
	})
	RunDiagnostics(report)

	f := findingByMode(report, "wake-blocked-by-task")
	if f == nil {
		t.Fatalf("expected a wake-blocked-by-task finding, got %+v", report.Findings)
	}
	if f.Severity != "critical" {
		t.Errorf("an un-receipted request behind the task is confirmed stuck delivery, want critical, got %s", f.Severity)
	}
	if !strings.Contains(f.Summary, blockingTaskID) || strings.Contains(f.Summary, starvedTaskID) {
		t.Errorf("summary must name the blocking task, not the answered one, got %q", f.Summary)
	}
	evidence := strings.Join(f.Evidence, "\n")
	for _, want := range []string{"Listenerless", "SendWakeUp", blockingTaskID, starvedTaskID, "1790347804-review-af895fd9", "does not block the wake"} {
		if !strings.Contains(evidence, want) {
			t.Errorf("evidence must contain %q, got %v", want, f.Evidence)
		}
	}
	remediation := strings.Join(f.Remediation, "\n")
	if !strings.Contains(remediation, "muxcode deliver review --force") || !strings.Contains(remediation, blockingTaskID) {
		t.Errorf("remediation must name deliver --force and the blocking task, got %v", f.Remediation)
	}
	if findingByMode(report, "active-with-stale-messages") != nil {
		t.Error("active-with-stale-messages must not be emitted for a constant-IsIdle provider")
	}
	for _, other := range report.Findings {
		if strings.Contains(strings.Join(other.Evidence, "\n"), "IsAgentIdle: false") {
			t.Errorf("%s still reports the constant IsIdle as evidence", other.FailureMode)
		}
	}
}

// TestDiagnose_WakeBlockedByTaskControls holds the new finding to its scope:
// a codex agent with no in-flight task gets no wake-blocked-by-task finding,
// and a Claude agent — whose IsIdle is a real pane reading — keeps
// active-with-stale-messages.
func TestDiagnose_WakeBlockedByTaskControls(t *testing.T) {
	noTask := starvedDiagnosticReport("codex", nil)
	RunDiagnostics(noTask)
	if findingByMode(noTask, "wake-blocked-by-task") != nil {
		t.Error("no in-flight task means nothing to name — wake-blocked-by-task must stay silent")
	}
	if findingByMode(noTask, "receipt-gap") == nil {
		t.Errorf("the un-receipted request must still be explained by receipt-gap, got %+v", noTask.Findings)
	}

	claude := starvedDiagnosticReport("claude", []InFlightTaskEvidence{
		{ID: starvedTaskID, From: "edit", Action: "review", AgeSecs: 300},
	})
	RunDiagnostics(claude)
	if findingByMode(claude, "wake-blocked-by-task") != nil {
		t.Error("a provider with a real idle probe must not get the listenerless finding")
	}
	if findingByMode(claude, "active-with-stale-messages") == nil {
		t.Errorf("a Claude agent must keep active-with-stale-messages, got %+v", claude.Findings)
	}
}

// TestDiagnose_BlocksWakeFollowsTheGate drives diagnose from real bus state
// through CollectInFlightTasks: only a fresh, unanswered task whose request
// has left the inbox is reported as the wake blocker. Negative controls — a
// stale task, a fresh answered one, and a fresh one still pending in the inbox
// — produce no wake-blocked-by-task finding, because the gate lets each wake
// through.
func TestDiagnose_BlocksWakeFollowsTheGate(t *testing.T) {
	for _, tc := range []struct {
		name        string
		taskAge     int64
		replyAction string
		pending     bool
		wantBlocks  bool
	}{
		{"fresh-unanswered", 1, "", false, true},
		{"stale-unanswered", 300, "", false, false},
		{"fresh-answered-elsewhere", 1, "review", false, false},
		{"fresh-pending-in-inbox", 1, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "mux192-diag-" + tc.name
			starvedReviewFixture(t, session, tc.taskAge, tc.replyAction)
			if tc.pending {
				own := Message{
					ID: starvedTaskID, From: "edit", To: "review", Type: "request",
					Action: "review", Payload: "review the branch", TS: time.Now().Unix() - tc.taskAge,
				}
				if err := SendForce(session, own); err != nil {
					t.Fatalf("SendForce own request: %v", err)
				}
			}

			tasks := CollectInFlightTasks(session, "review")
			if len(tasks) != 1 || tasks[0].ID != starvedTaskID || tasks[0].BlocksWake != tc.wantBlocks {
				t.Fatalf("expected task %s with blocks_wake=%v, got %+v", starvedTaskID, tc.wantBlocks, tasks)
			}
			report := starvedDiagnosticReport("codex", tasks)
			RunDiagnostics(report)

			f := findingByMode(report, "wake-blocked-by-task")
			if !tc.wantBlocks {
				if f != nil {
					t.Errorf("a task the gate does not block on must not be reported as the blocker, got %+v", *f)
				}
				if findingByMode(report, "receipt-gap") == nil {
					t.Errorf("the un-receipted request must still be explained, got %+v", report.Findings)
				}
				return
			}
			if f == nil || !strings.Contains(f.Summary, starvedTaskID) {
				t.Errorf("expected wake-blocked-by-task naming %s, got %+v", starvedTaskID, report.Findings)
			}
		})
	}
}

// TestTaskAnsweredElsewhere_IncidentShapeAndControls replays the MUX-192 log:
// review answered the chain's request (from test) to edit with
// "review-complete", which answers edit's tracked "review" task. Controls: a
// response to an unrelated action, and a reply to a sibling request edit
// itself sent, answer nothing.
//
// Task, request and reply share one pinned second unless replyDelta shifts the
// reply, so log order alone decides the logged-request cases: a same-second
// reply after the request is the answer, one before it is not. With the
// request absent from the log, only a strictly later reply answers — the
// equal-second case fails if the fallback compare is loosened to <.
func TestTaskAnsweredElsewhere_IncidentShapeAndControls(t *testing.T) {
	const (
		requestBeforeReply = iota
		requestAfterReply
		requestAbsent
	)
	chainReply := func(t *testing.T, session string) string {
		chain := NewMessage("test", "review", "request", "review", "Tests passed — review the changes", "")
		if err := SendNoCC(session, chain); err != nil {
			t.Fatalf("SendNoCC chain: %v", err)
		}
		return chain.ID
	}
	noReplyTo := func(*testing.T, string) string { return "" }
	for _, tc := range []struct {
		name       string
		action     string
		replyToFn  func(t *testing.T, session string) string
		request    int
		replyDelta int64
		want       bool
	}{
		{"incident-chain-reply", "review-complete", chainReply, requestBeforeReply, 0, true},
		{"unrelated-action", "plan", func(*testing.T, string) string { return "1790347737-test-chain" }, requestBeforeReply, 0, false},
		{"sibling-edit-request", "review-complete", func(t *testing.T, session string) string {
			sibling := NewMessage("edit", "review", "request", "review", "review the other change", "")
			if err := SendForce(session, sibling); err != nil {
				t.Fatalf("SendForce sibling: %v", err)
			}
			return sibling.ID
		}, requestBeforeReply, 0, false},
		{"same-second-reply-after-request", "review-complete", noReplyTo, requestBeforeReply, 0, true},
		{"same-second-reply-before-request", "review-complete", noReplyTo, requestAfterReply, 0, false},
		{"absent-request-same-second", "review-complete", noReplyTo, requestAbsent, 0, false},
		{"absent-request-earlier", "review-complete", noReplyTo, requestAbsent, -1, false},
		{"absent-request-later", "review-complete", noReplyTo, requestAbsent, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "mux192-answered-" + tc.name
			t.Setenv("BUS_SESSION", session)
			useTempBusDir(t)
			if err := Init(session, t.TempDir()); err != nil {
				t.Fatalf("Init: %v", err)
			}
			sentAt := time.Now().Unix() - 5
			task := Task{ID: starvedTaskID, From: "edit", To: "review", Action: "review",
				Status: TaskInFlight, SentAt: sentAt}
			tracked := NewMessage("edit", "review", "request", "review", "review the change", "")
			tracked.ID = starvedTaskID
			tracked.TS = sentAt
			logTracked := func() {
				if err := SendForce(session, tracked); err != nil {
					t.Fatalf("SendForce tracked: %v", err)
				}
			}

			if tc.request == requestBeforeReply {
				logTracked()
			}
			reply := NewMessage("review", "edit", "response", tc.action, "1 must-fix", tc.replyToFn(t, session))
			reply.TS = sentAt + tc.replyDelta
			if err := Send(session, reply); err != nil {
				t.Fatalf("Send reply: %v", err)
			}
			if tc.request == requestAfterReply {
				logTracked()
			}

			id, ok := TaskAnsweredElsewhere(session, task)
			if ok != tc.want {
				t.Fatalf("TaskAnsweredElsewhere = (%q, %v), want answered=%v", id, ok, tc.want)
			}
			if ok && id != reply.ID {
				t.Errorf("answer id = %q, want the reply %q", id, reply.ID)
			}
		})
	}
}
