package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// MUX-154 Phase 1 pins, at the daemon: a pane showing only a progress line or
// the turn-separator rule never completes a tracked task and never drains the
// request — the 2026-09-08 incident drained five requests that way, disarming
// `deliver --force`. Each test re-runs the same session on a genuine pane as
// its negative control, so a guard gone inert (nothing ever completes) fails
// too. Fixture payloads are the incident rows: 14:12:55 and 20:31:51.

const workingLine = "• Working (13s • esc to interrupt)"

var ruleLine158 = strings.Repeat("─", 158)

// scrapeSession is a bus session with one role on the named scrape-road CLI.
func scrapeSession(t *testing.T, role, cli string) string {
	t.Helper()
	session := testSession(t)
	t.Setenv("BUS_SESSION", session)
	t.Setenv(bus.RoleCLIEnvVar(role), cli)
	if !bus.ResolveProvider(role).PaneIsEvidence() {
		t.Fatalf("fixture: %s on %s should be on the scrape road", role, cli)
	}
	return session
}

// pendingRequest sends an edit→to request into the target's inbox and tracks
// it, backdated past the send grace — the state the incident started from.
func pendingRequest(t *testing.T, d *Daemon, session, to string) bus.Message {
	t.Helper()
	m := bus.NewMessage("edit", to, "request", to, "run it", "")
	m.TS -= 30
	if err := bus.Send(session, m); err != nil {
		t.Fatal(err)
	}
	if err := bus.CreateTask(session, m, 600); err != nil {
		t.Fatal(err)
	}
	d.taskDeliveredAt[m.ID] = time.Now().Unix() - 10
	return m
}

func requestInInbox(t *testing.T, session, role, id string) bool {
	t.Helper()
	msgs, err := bus.Peek(session, role)
	if err != nil {
		t.Fatalf("Peek %s: %v", role, err)
	}
	for _, m := range msgs {
		if m.ID == id {
			return true
		}
	}
	return false
}

func requestResponded(session, id string) bool {
	ds, err := bus.ReadDeliveryStatus(session, id)
	return err == nil && ds.Status == bus.StatusResponded
}

// scrapeWith re-arms the 5s throttle and runs one scrape against pane.
func scrapeWith(d *Daemon, pane string) {
	d.capturePane = func(string, int) (string, error) { return pane, nil }
	d.lastTaskCheck = 0
	d.checkNonHookTasks()
}

// assertStillPending is the Phase 1 pin: the task is in flight, nothing was
// synthesized, and the request is still in the inbox for deliver --force.
func assertStillPending(t *testing.T, session, to string, req bus.Message) {
	t.Helper()
	if got := taskStatus(t, session, req.ID); got != bus.TaskInFlight {
		t.Errorf("task = %q, want still in-flight", got)
	}
	if _, ok := bus.FindResponseSince(session, to, "edit", req.TS); ok {
		t.Error("a response was synthesized from a non-result pane")
	}
	if !requestInInbox(t, session, to, req.ID) {
		t.Error("request drained from the inbox — deliver --force has nothing left to deliver")
	}
	if requestResponded(session, req.ID) {
		t.Error("request marked responded on a non-result")
	}
	if rows := lifecycleDetails(t, session, "task-detected"); len(rows) != 0 {
		t.Errorf("task-detected rows = %q, want none", rows)
	}
}

// assertCompletedAndDrained is the negative control: a genuine result still
// completes the task, answers the requester, and drains the request.
func assertCompletedAndDrained(t *testing.T, session, to string, req bus.Message) {
	t.Helper()
	if got := taskStatus(t, session, req.ID); got != bus.TaskCompleted {
		t.Fatalf("genuine pane: task = %q, want completed", got)
	}
	if _, ok := bus.FindResponseSince(session, to, "edit", req.TS); !ok {
		t.Error("genuine pane: no response reached the requester")
	}
	if requestInInbox(t, session, to, req.ID) {
		t.Error("genuine pane: request still in the inbox after it was answered")
	}
	if !requestResponded(session, req.ID) {
		t.Error("genuine pane: request not marked responded")
	}
}

// TestCheckNonHookTasks_CodexChromeLeavesRequestPending pins the detection
// layer on codex's scrape road: DetectTaskCompletion reads both incident panes
// as not complete, so the daemon never reaches a Send.
func TestCheckNonHookTasks_CodexChromeLeavesRequestPending(t *testing.T) {
	cases := []struct {
		name string
		pane string
	}{
		{"working line (14:12:55)", workingLine + "\n\n› \n"},
		{"rule line above composer (20:31:51)", "Running gofmt\n" + ruleLine158 + "\n\n› \n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := scrapeSession(t, "test", "codex")
			d := New(session, 5, 8)
			d.agentAlive = func(_, role string) bool { return role == "test" }
			req := pendingRequest(t, d, session, "test")

			scrapeWith(d, tc.pane)
			assertStillPending(t, session, "test", req)

			scrapeWith(d, "Tests finished: EXIT=0\nSent response:response to edit\n› \n")
			assertCompletedAndDrained(t, session, "test", req)
		})
	}
}

// TestCheckNonHookTasks_NonResultSummaryRefused pins the consumer layer: an
// OpenCode stop marker makes detection report complete, but the summary is the
// progress line, so the daemon refuses it with task-nonresult-ignored.
func TestCheckNonHookTasks_NonResultSummaryRefused(t *testing.T) {
	session := scrapeSession(t, "test", "opencode")
	d := New(session, 5, 8)
	d.agentAlive = func(_, role string) bool { return role == "test" }
	req := pendingRequest(t, d, session, "test")

	const stop = "▣  Test · gpt-5 · 3.2s"
	pane := workingLine + "\n" + stop + "\n"
	completed, _, summary := bus.ResolveProvider("test").DetectTaskCompletion(session, "test", pane)
	if !completed || !bus.LooksLikeNonResult(summary) {
		t.Fatalf("fixture must reach the consumer layer: completed=%v nonResult=%v summary=%q",
			completed, bus.LooksLikeNonResult(summary), summary)
	}

	scrapeWith(d, pane)
	assertStillPending(t, session, "test", req)
	if rows := lifecycleDetails(t, session, "task-nonresult-ignored"); len(rows) != 1 ||
		!strings.HasPrefix(rows[0], "test task test from edit") {
		t.Errorf("task-nonresult-ignored rows = %q, want exactly one, naming test", rows)
	}

	scrapeWith(d, "go test ./... passed EXIT=0\n"+stop+"\n")
	assertCompletedAndDrained(t, session, "test", req)
}
