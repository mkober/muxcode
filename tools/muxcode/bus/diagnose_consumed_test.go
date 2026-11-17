package bus

import (
	"strings"
	"testing"
	"time"
)

// consumedTask records a graph dispatch to commit as an in-flight task sent
// ageSecs ago, with a delivery receipt when received — commit's listener read
// it — and returns its id.
func consumedTask(t *testing.T, session string, ageSecs int64, received bool) string {
	t.Helper()
	m := NewMessage("daemon", "commit", "request", "pr-read", "Preflight check, read-only", "")
	if err := CreateTask(session, m, 600); err != nil {
		t.Fatal(err)
	}
	task, err := ReadTask(session, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.SentAt = time.Now().Unix() - ageSecs
	if err := writeTask(session, task); err != nil {
		t.Fatal(err)
	}
	if received {
		WriteReceipt(session, m.ID, "commit", ReceiptKindAck)
	}
	return m.ID
}

func diagnoseConsumed(session string) *DiagnosticFinding {
	report := DiagnosticReport{Role: "commit", Session: session, InFlightTasks: CollectInFlightTasks(session, "commit")}
	return checkConsumedUnanswered(&report)
}

// MUX-232 Phase 3, the 2026-11-17 shape: commit read a graph dispatch (a true
// ack receipt), never answered it, the stall watchdog re-drove it twice and
// gave up. Diagnose names the task and the re-drives observed as a critical
// consumed-unanswered finding instead of "No issues detected" — critical on
// the watchdog's own give-up row. The count reads the rows naming the task: a
// role-wide force-redrive row names none and is not counted again.
func TestCheckConsumedUnanswered_ReportsReadUnansweredStalledRequest(t *testing.T) {
	useTempBusDir(t)
	isolateLifecycle(t)
	session := testSession(t)
	id := consumedTask(t, session, 200, true)
	LogLifecycle(session, "info", "daemon", "task-stall-redrive", "daemon→commit:pr-read redrive 1/2 (task "+id+")")
	LogLifecycle(session, "info", "deliver", "force-redrive", "commit: 1 of 1 in-flight task(s)")
	LogLifecycle(session, "info", "daemon", "task-stall-redrive", "daemon→commit:pr-read redrive 2/2 (task "+id+")")
	LogLifecycle(session, "warn", "daemon", "task-stall-giveup", "daemon→commit:pr-read still stalled after 2 redrives — task timeout owns it (task "+id+")")

	f := diagnoseConsumed(session)
	if f == nil {
		t.Fatal("a read, unanswered, stalled request raised no finding — the 2026-11-17 'No issues detected'")
	}
	if f.FailureMode != "consumed-unanswered" || f.Severity != "critical" {
		t.Errorf("finding %s/%s, want consumed-unanswered/critical once the watchdog gave up", f.FailureMode, f.Severity)
	}
	if !strings.Contains(f.Summary, id) || !strings.Contains(f.Summary, "2 re-drive(s) observed") {
		t.Errorf("summary must name the task and its 2 observed re-drives: %q", f.Summary)
	}
	ev := CollectInFlightTasks(session, "commit")
	if len(ev) != 1 || !ev[0].Received || !ev[0].Stalled || ev[0].Redrives != 2 || !ev[0].WatchdogGaveUp {
		t.Errorf("in_flight_tasks evidence %+v, want received, stalled, 2 redrives, watchdog gave up", ev)
	}
}

// MUX-232 review control: the executor and the watchdog keep separate
// re-drive budgets, so one targeted executor re-drive plus one watchdog
// re-drive — two observed — is not exhaustion: with no give-up row the
// finding stays a warning.
func TestCheckConsumedUnanswered_MixedRedrivesWithoutGiveUpWarn(t *testing.T) {
	useTempBusDir(t)
	isolateLifecycle(t)
	session := testSession(t)
	id := consumedTask(t, session, 200, true)
	LogLifecycle(session, "info", "deliver", "force-redrive", "commit: task "+id+" (targeted)")
	LogLifecycle(session, "info", "daemon", "task-stall-redrive", "daemon→commit:pr-read redrive 1/2 (task "+id+")")
	f := diagnoseConsumed(session)
	if f == nil || f.Severity != "warning" || !strings.Contains(f.Summary, "2 re-drive(s) observed") {
		t.Errorf("want a warning naming 2 observed re-drives, got %+v", f)
	}
}

// MUX-232 review control: the watchdog's re-drive budget lives in daemon memory
// and resets when the daemon starts, so a give-up followed by a daemon start
// is no longer exhaustion — the finding stays a warning, and a give-up written
// by the new daemon makes it critical again.
func TestCheckConsumedUnanswered_GiveUpScopedToRunningDaemon(t *testing.T) {
	useTempBusDir(t)
	isolateLifecycle(t)
	session := testSession(t)
	id := consumedTask(t, session, 200, true)
	giveUp := func() {
		LogLifecycle(session, "warn", "daemon", "task-stall-giveup", "daemon→commit:pr-read still stalled after 2 redrives — task timeout owns it (task "+id+")")
	}
	giveUp()
	LogLifecycle(session, "info", "daemon", "started", "pid 4242")
	LogLifecycle(session, "info", "daemon", "task-stall-redrive", "daemon→commit:pr-read redrive 1/2 (task "+id+")")
	if f := diagnoseConsumed(session); f == nil || f.Severity != "warning" {
		t.Errorf("a give-up before the daemon restarted must not read as exhaustion now, got %+v", f)
	}
	if ev := CollectInFlightTasks(session, "commit"); len(ev) != 1 || ev[0].WatchdogGaveUp {
		t.Errorf("watchdog_gave_up must be false after a daemon start: %+v", ev)
	}
	giveUp()
	if f := diagnoseConsumed(session); f == nil || f.Severity != "critical" {
		t.Errorf("a give-up by the running daemon must read critical, got %+v", f)
	}
}

// Negative controls: a request answered, one still inside the stall threshold,
// and one never read (no receipt — the receipt-gap checks' business) raise no
// consumed-unanswered finding.
func TestCheckConsumedUnanswered_NegativeControls(t *testing.T) {
	for _, tc := range []struct {
		name     string
		age      int64
		received bool
		answer   bool
	}{
		{"answered", 200, true, true},
		{"inside the threshold", 5, true, false},
		{"never read", 200, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempBusDir(t)
			isolateLifecycle(t)
			session := testSession(t)
			id := consumedTask(t, session, tc.age, tc.received)
			if tc.answer {
				MarkResponded(session, id, "1794939500-commit-0000aaaa")
			}
			if f := diagnoseConsumed(session); f != nil {
				t.Errorf("raised %s: %s", f.FailureMode, f.Summary)
			}
		})
	}
}
