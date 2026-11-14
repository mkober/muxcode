package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// MUX-155: the CLI's in-flight dedup keys on the sender as well as (to,
// action), so only a sender's own retry is deduplicated, and a send the bus
// did not write reports NOT SENT and exits 1 instead of a success line.

// setupDedupSession gives the test its own bus session, with the CLI's
// session resolution pointed at it and request sends on their default
// (tracked) road.
func setupDedupSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bus.SetBusDirBase(dir)
	t.Cleanup(bus.ResetBusDirBase)
	session := "test-send-dedup"
	if err := bus.Init(session, filepath.Join(dir, "memory")); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Setenv("BUS_SESSION", session)
	t.Setenv("MUXCODE_SEND_DEFAULT_FIRE_AND_FORGET", "")
	return session
}

// sendInFlight delivers a request and records its tracked task, as a CLI send
// that is still awaiting its reply leaves it.
func sendInFlight(t *testing.T, session, from, to, action, payload string) bus.Message {
	t.Helper()
	m := bus.NewMessage(from, to, "request", action, payload, "")
	if err := bus.Send(session, m); err != nil {
		t.Fatalf("Send %s→%s:%s: %v", from, to, action, err)
	}
	if err := bus.CreateTask(session, m, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return m
}

// sendExited carries the code a send passed to sendExit.
type sendExited int

// runSendAs runs the real `muxcode send` as role and returns its stdout and
// exit code: 0 when Send returns, else the code it handed sendExit.
func runSendAs(t *testing.T, role string, args ...string) (out string, code int) {
	t.Helper()
	t.Setenv("AGENT_ROLE", role)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	savedOut, savedExit := os.Stdout, sendExit
	os.Stdout = w
	sendExit = func(c int) { panic(sendExited(c)) }
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		r.Close()
		done <- string(b)
	}()
	defer func() {
		os.Stdout, sendExit = savedOut, savedExit
		w.Close()
		out = <-done
		if p := recover(); p != nil {
			c, ok := p.(sendExited)
			if !ok {
				panic(p)
			}
			code = int(c)
		}
	}()
	Send(args)
	return "", 0
}

// inboxPayloadCount counts rows in role's inbox carrying payload.
func inboxPayloadCount(t *testing.T, session, role, payload string) int {
	t.Helper()
	msgs, err := bus.Peek(session, role)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Payload == payload {
			n++
		}
	}
	return n
}

// TestSendCLI_CrossSenderInFlightDelivered is 2026-09-08 fixed: an unanswered
// watch→edit notify used to make plan's notify print "already tracking" and
// write nothing. Plan's send is now delivered and tracked as its own task,
// and watch's task is left as it was.
func TestSendCLI_CrossSenderInFlightDelivered(t *testing.T) {
	session := setupDedupSession(t)
	watch := sendInFlight(t, session, "watch", "edit", "notify", "watch: CI green")

	out, code := runSendAs(t, "plan", "edit", "notify", "plan: MUX-153 updated", "--no-notify")

	if code != 0 || strings.Contains(out, "NOT SENT") || !strings.Contains(out, "Sent request:notify to edit") {
		t.Errorf("plan's notify must be sent with exit 0, not matched to watch's task — exit %d, %q", code, out)
	}
	if n := inboxPayloadCount(t, session, "edit", "plan: MUX-153 updated"); n != 1 {
		t.Errorf("edit's inbox holds %d copies of plan's notify, want 1", n)
	}
	own, ok := bus.FindInFlightTask(session, "edit", "plan", "notify")
	if !ok || own.ID == watch.ID {
		t.Errorf("plan's send must be tracked as its own task, got %+v found=%v", own, ok)
	}
	if got, ok := bus.FindInFlightTask(session, "edit", "watch", "notify"); !ok || got.ID != watch.ID {
		t.Errorf("watch's task must stay in flight, got %+v found=%v", got, ok)
	}
}

// TestSendCLI_ForceBypassesSameSenderDedup: --force still delivers a request
// the sender's own in-flight task would otherwise deduplicate.
func TestSendCLI_ForceBypassesSameSenderDedup(t *testing.T) {
	session := setupDedupSession(t)
	sendInFlight(t, session, "plan", "edit", "notify", "plan: MUX-153 updated")

	out, code := runSendAs(t, "plan", "edit", "notify", "plan: MUX-153 updated", "--force", "--no-notify")

	if code != 0 || !strings.Contains(out, "Sent request:notify to edit") {
		t.Errorf("--force must send past the dedup, got exit %d, %q", code, out)
	}
	if n := inboxPayloadCount(t, session, "edit", "plan: MUX-153 updated"); n != 2 {
		t.Errorf("edit's inbox holds %d copies after --force, want 2", n)
	}
}

// TestSendCLI_DistinctActionsIndependent: a sender's in-flight notify does not
// hold back its spec-update — varying the action is the workaround agents
// used under the old key, and it must stay deliverable.
func TestSendCLI_DistinctActionsIndependent(t *testing.T) {
	session := setupDedupSession(t)
	sendInFlight(t, session, "plan", "edit", "notify", "plan: MUX-153 updated")

	out, code := runSendAs(t, "plan", "edit", "spec-update", "plan: MUX-153 updated", "--no-notify")

	if code != 0 || !strings.Contains(out, "Sent request:spec-update to edit") {
		t.Errorf("a different action must be delivered, got exit %d, %q", code, out)
	}
}

// TestSendCLI_SameSenderRetryDeduplicated pins the retry the guard exists for:
// a sender re-issuing its own in-flight request is not delivered twice, and is
// told so — NOT SENT naming its task, exit 1 — rather than "already tracking"
// and exit 0 (MUX-155 Phase 3).
func TestSendCLI_SameSenderRetryDeduplicated(t *testing.T) {
	session := setupDedupSession(t)
	orig := sendInFlight(t, session, "plan", "edit", "notify", "plan: MUX-153 updated")

	out, code := runSendAs(t, "plan", "edit", "notify", "plan: MUX-153 updated", "--no-notify")

	if code != 1 || !strings.Contains(out, "NOT SENT") || !strings.Contains(out, orig.ID) || strings.Contains(out, "already tracking") {
		t.Errorf("a same-sender retry must report NOT SENT naming %s and exit 1, got exit %d, %q", orig.ID, code, out)
	}
	if n := inboxPayloadCount(t, session, "edit", "plan: MUX-153 updated"); n != 1 {
		t.Errorf("edit's inbox holds %d copies of the retried request, want 1", n)
	}
}

// TestSendCLI_SameSenderWaitReattaches pins the --wait half: a retry after a
// killed --wait reattaches to the sender's existing task instead of sending,
// and degrades to tracking that same task.
func TestSendCLI_SameSenderWaitReattaches(t *testing.T) {
	session := setupDedupSession(t)
	t.Setenv("MUXCODE_WAIT_DEGRADE_SECS", "1")
	orig := sendInFlight(t, session, "plan", "edit", "notify", "plan: MUX-153 updated")

	out, code := runSendAs(t, "plan", "edit", "notify", "plan: MUX-153 updated", "--wait", "--no-notify")

	if code != 0 || !strings.Contains(out, "reattaching --wait") || !strings.Contains(out, "converted to tracked task "+orig.ID) {
		t.Errorf("--wait retry must reattach to %s with exit 0, got exit %d, %q", orig.ID, code, out)
	}
	if n := inboxPayloadCount(t, session, "edit", "plan: MUX-153 updated"); n != 1 {
		t.Errorf("edit's inbox holds %d copies after the reattach, want 1", n)
	}
}

// TestSendCLI_DuplicateReplyReportedNotSent: a second reply to an
// already-answered task is still suppressed before any write, and the CLI now
// says so — NOT SENT, exit 1 — where it printed "Sent response:…" and exited 0.
func TestSendCLI_DuplicateReplyReportedNotSent(t *testing.T) {
	session := setupDedupSession(t)
	req := sendInFlight(t, session, "edit", "plan", "update-docs", "add the override to configuration.md")
	first := bus.NewMessage("plan", "edit", "response", "update-docs", "configuration.md updated with the new row", req.ID)
	if err := bus.Send(session, first); err != nil {
		t.Fatalf("first reply: %v", err)
	}
	bus.CompleteTask(session, req.ID, first.ID)

	out, code := runSendAs(t, "plan", "edit", "update-docs", "configuration.md updated again",
		"--type", "response", "--reply-to", req.ID, "--no-notify")

	if code != 1 || strings.Contains(out, "Sent response") || !strings.Contains(out, "NOT SENT") || !strings.Contains(out, "already answered") {
		t.Errorf("a suppressed reply must report NOT SENT and exit 1, got exit %d, %q", code, out)
	}
	if n := inboxPayloadCount(t, session, "edit", "configuration.md updated again"); n != 0 {
		t.Errorf("the duplicate reply to a completed task was delivered (%d rows) — the suppression itself must stay", n)
	}
}

// TestSendCLI_DedupWindowReportsNotSent: the window dedup keys on (from, to,
// action, type), not payload, so a different second notify inside the window
// is dropped — and must say so with exit 1. With the window off, the same
// second send is delivered (negative control: the window is what suppressed).
func TestSendCLI_DedupWindowReportsNotSent(t *testing.T) {
	for _, tc := range []struct {
		window    string
		delivered bool
	}{{"30", false}, {"0", true}} {
		t.Run("window="+tc.window, func(t *testing.T) {
			session := setupDedupSession(t)
			t.Setenv("MUXCODE_SEND_DEFAULT_FIRE_AND_FORGET", "1")
			t.Setenv("MUXCODE_DEDUP_WINDOW", tc.window)
			if out, code := runSendAs(t, "plan", "edit", "notify", "plan: first report", "--no-notify"); code != 0 {
				t.Fatalf("first send: exit %d, %q", code, out)
			}

			out, code := runSendAs(t, "plan", "edit", "notify", "plan: second report", "--no-notify")

			got := inboxPayloadCount(t, session, "edit", "plan: second report") == 1
			if got != tc.delivered {
				t.Fatalf("second report delivered = %v, want %v (exit %d, %q)", got, tc.delivered, code, out)
			}
			if tc.delivered && code != 0 {
				t.Errorf("a delivered send must exit 0, got %d, %q", code, out)
			}
			if !tc.delivered && (code != 1 || !strings.Contains(out, "NOT SENT") || !strings.Contains(out, "dedup window")) {
				t.Errorf("a window-suppressed send must report NOT SENT and exit 1, got exit %d, %q", code, out)
			}
		})
	}
}

// TestSendCLI_RelayLoopReportsNotSent: a non-edit sender's request tuple past
// the relay threshold is dropped with NOT SENT and exit 1; --force still
// delivers it.
func TestSendCLI_RelayLoopReportsNotSent(t *testing.T) {
	session := setupDedupSession(t)
	t.Setenv("MUXCODE_SEND_DEFAULT_FIRE_AND_FORGET", "1")
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	t.Setenv("MUXCODE_RELAY_SUPPRESS_THRESHOLD", "2")
	for _, p := range []string{"relay 1", "relay 2"} {
		if err := bus.Send(session, bus.NewMessage("run", "watch", "request", "watch", p, "")); err != nil {
			t.Fatalf("seed %q: %v", p, err)
		}
	}

	out, code := runSendAs(t, "run", "watch", "watch", "relay 3", "--no-notify")
	if code != 1 || !strings.Contains(out, "NOT SENT") || !strings.Contains(out, "loop guard") {
		t.Errorf("a relay past the threshold must report NOT SENT and exit 1, got exit %d, %q", code, out)
	}
	if n := inboxPayloadCount(t, session, "watch", "relay 3"); n != 0 {
		t.Errorf("the suppressed relay reached watch's inbox (%d rows)", n)
	}

	out, code = runSendAs(t, "run", "watch", "watch", "relay 3", "--force", "--no-notify")
	if code != 0 || inboxPayloadCount(t, session, "watch", "relay 3") != 1 {
		t.Errorf("--force must deliver past the loop guard, got exit %d, %q", code, out)
	}
}

// TestSendCLI_ExistingAnswerReportsNotSent: a request whose answer already
// sits unread is not re-sent; the CLI prints that answer under NOT SENT and
// exits 1, since nothing was written.
func TestSendCLI_ExistingAnswerReportsNotSent(t *testing.T) {
	session := setupDedupSession(t)
	req := bus.NewMessage("edit", "plan", "request", "update-docs", "add the override row", "")
	if err := bus.Send(session, req); err != nil {
		t.Fatalf("Send request: %v", err)
	}
	resp := bus.NewMessage("plan", "edit", "response", "update-docs", "row added", req.ID)
	if err := bus.AppendToInbox(session, "edit", resp); err != nil {
		t.Fatalf("AppendToInbox: %v", err)
	}

	out, code := runSendAs(t, "edit", "plan", "update-docs", "add the override row", "--no-notify")

	if code != 1 || !strings.Contains(out, "NOT SENT") || !strings.Contains(out, "row added") {
		t.Errorf("an already-answered request must print its answer under NOT SENT and exit 1, got exit %d, %q", code, out)
	}
	if n := inboxPayloadCount(t, session, "plan", "add the override row"); n != 1 {
		t.Errorf("plan's inbox holds %d copies of the request, want the original 1", n)
	}
}
