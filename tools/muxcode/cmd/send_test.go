package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// setupConsumeSession creates an isolated bus session holding one logged
// request from edit to plan plus plan's unread reply to it in edit's inbox.
// Returns the session name and the two messages.
func setupConsumeSession(t *testing.T, reqPayload string) (string, bus.Message, bus.Message) {
	t.Helper()
	dir := t.TempDir()
	bus.SetBusDirBase(dir)
	t.Cleanup(bus.ResetBusDirBase)

	session := "test-consume"
	if err := bus.Init(session, filepath.Join(dir, "memory")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	req := bus.NewMessage("edit", "plan", "request", "update-docs", reqPayload, "")
	if err := bus.Send(session, req); err != nil {
		t.Fatalf("Send request: %v", err)
	}

	resp := bus.NewMessage("plan", "edit", "response", "update-docs", "done", req.ID)
	if err := bus.AppendToInbox(session, "edit", resp); err != nil {
		t.Fatalf("AppendToInbox: %v", err)
	}
	return session, req, resp
}

// inboxHas reports whether the role's inbox still holds the given message ID.
func inboxHas(t *testing.T, session, role, id string) bool {
	t.Helper()
	msgs, err := bus.Peek(session, role)
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	for _, m := range msgs {
		if m.ID == id {
			return true
		}
	}
	return false
}

// A stale reply to a DIFFERENT question must not swallow the new request.
// This is the regression: matching on (sender, action) alone meant any unread
// update-docs reply suppressed the next update-docs send, and because the old
// reply was printed, the vanished send looked like a successful round-trip.
func TestConsumeExistingResponses_DifferentPayloadStillSends(t *testing.T) {
	session, _, resp := setupConsumeSession(t, "document the reload fix")

	if consumeExistingResponses(session, "edit", "plan", "update-docs", "document the dedup fix") {
		t.Error("different payload was suppressed — the send would have vanished")
	}
	if !inboxHas(t, session, "edit", resp.ID) {
		t.Error("unrelated response was consumed; it must stay in the inbox")
	}
}

// The genuinely redundant case still short-circuits: same action, same ask.
func TestConsumeExistingResponses_SamePayloadSuppressed(t *testing.T) {
	session, _, resp := setupConsumeSession(t, "document the reload fix")

	if !consumeExistingResponses(session, "edit", "plan", "update-docs", "document the reload fix") {
		t.Error("identical payload should be suppressed as already answered")
	}
	if inboxHas(t, session, "edit", resp.ID) {
		t.Error("answered response should have been consumed")
	}
}

// A response that cannot be correlated (no ReplyTo, or a request aged out of
// the log) must fail open toward sending — a suppressed send is unrecoverable.
func TestConsumeExistingResponses_UnresolvableReplyToSends(t *testing.T) {
	dir := t.TempDir()
	bus.SetBusDirBase(dir)
	t.Cleanup(bus.ResetBusDirBase)

	session := "test-consume-orphan"
	if err := bus.Init(session, filepath.Join(dir, "memory")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, tc := range []struct {
		name    string
		replyTo string
	}{
		{"empty ReplyTo", ""},
		{"request not in log", "1234567890-edit-deadbeef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orphan := bus.NewMessage("plan", "edit", "response", "update-docs", "done", tc.replyTo)
			if err := bus.AppendToInbox(session, "edit", orphan); err != nil {
				t.Fatalf("AppendToInbox: %v", err)
			}
			if consumeExistingResponses(session, "edit", "plan", "update-docs", "anything") {
				t.Error("uncorrelatable response suppressed the send")
			}
			if !inboxHas(t, session, "edit", orphan.ID) {
				t.Error("uncorrelatable response should remain in the inbox")
			}
		})
	}
}

func TestRelaySuppressLimits_Default(t *testing.T) {
	t.Setenv("MUXCODE_RELAY_SUPPRESS_THRESHOLD", "")
	t.Setenv("MUXCODE_RELAY_SUPPRESS_WINDOW", "")
	threshold, window := relaySuppressLimits()
	if threshold != 4 {
		t.Errorf("default threshold = %d, want 4", threshold)
	}
	if window != 300 {
		t.Errorf("default window = %d, want 300", window)
	}
}

func TestRelaySuppressLimits_EnvOverride(t *testing.T) {
	t.Setenv("MUXCODE_RELAY_SUPPRESS_THRESHOLD", "7")
	t.Setenv("MUXCODE_RELAY_SUPPRESS_WINDOW", "120")
	threshold, window := relaySuppressLimits()
	if threshold != 7 {
		t.Errorf("threshold = %d, want 7", threshold)
	}
	if window != 120 {
		t.Errorf("window = %d, want 120", window)
	}
}

func TestRelaySuppressLimits_ZeroDisables(t *testing.T) {
	t.Setenv("MUXCODE_RELAY_SUPPRESS_THRESHOLD", "0")
	threshold, _ := relaySuppressLimits()
	if threshold != 0 {
		t.Errorf("threshold = %d, want 0 (disabled)", threshold)
	}
}

func TestDegradeWaitSecs_Default(t *testing.T) {
	t.Setenv("MUXCODE_WAIT_DEGRADE_SECS", "")
	if got := degradeWaitSecs(); got != 90 {
		t.Errorf("default = %d, want 90", got)
	}
}

func TestDegradeWaitSecs_EnvOverride(t *testing.T) {
	t.Setenv("MUXCODE_WAIT_DEGRADE_SECS", "30")
	if got := degradeWaitSecs(); got != 30 {
		t.Errorf("override = %d, want 30", got)
	}
}

func TestDegradeWaitSecs_ZeroDisables(t *testing.T) {
	t.Setenv("MUXCODE_WAIT_DEGRADE_SECS", "0")
	if got := degradeWaitSecs(); got != 0 {
		t.Errorf("disabled = %d, want 0", got)
	}
}

func TestDegradeWaitSecs_GarbageFallsBack(t *testing.T) {
	t.Setenv("MUXCODE_WAIT_DEGRADE_SECS", "notanint")
	if got := degradeWaitSecs(); got != 90 {
		t.Errorf("garbage fallback = %d, want 90", got)
	}
}

func TestDefaultsToTrack(t *testing.T) {
	t.Setenv("MUXCODE_SEND_DEFAULT_FIRE_AND_FORGET", "")
	// Bare request → defaults to tracked.
	if !defaultsToTrack("request", false, false) {
		t.Error("bare request should default to tracked")
	}
	// Explicit flags or non-request → never overridden.
	if defaultsToTrack("request", true, false) {
		t.Error("--wait request must not be coerced to track")
	}
	if defaultsToTrack("request", false, true) {
		t.Error("--track request is already tracked, not overridden")
	}
	if defaultsToTrack("response", false, false) {
		t.Error("response must stay fire-and-forget")
	}
	if defaultsToTrack("event", false, false) {
		t.Error("event must stay fire-and-forget")
	}
}

func TestDefaultsToTrack_FireAndForgetEscapeHatch(t *testing.T) {
	t.Setenv("MUXCODE_SEND_DEFAULT_FIRE_AND_FORGET", "1")
	if defaultsToTrack("request", false, false) {
		t.Error("escape hatch should restore fire-and-forget default")
	}
}

func TestValidatePayload_Clean(t *testing.T) {
	warnings := validatePayload("Build succeeded: all tests pass")
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for clean payload, got %v", warnings)
	}
}

func TestValidatePayload_Newlines(t *testing.T) {
	warnings := validatePayload("line1\nline2")
	if len(warnings) == 0 {
		t.Fatal("expected warning for newlines")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "newlines") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected newline warning, got %v", warnings)
	}
}

func TestValidatePayload_TooLong(t *testing.T) {
	long := strings.Repeat("x", 501)
	warnings := validatePayload(long)
	if len(warnings) == 0 {
		t.Fatal("expected warning for long payload")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, ">500") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected length warning, got %v", warnings)
	}
}

func TestValidatePayload_BothIssues(t *testing.T) {
	long := strings.Repeat("x", 250) + "\n" + strings.Repeat("y", 251)
	warnings := validatePayload(long)
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestValidatePayload_ExactlyAtLimit(t *testing.T) {
	exact := strings.Repeat("x", 500)
	warnings := validatePayload(exact)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for exactly 500 chars, got %v", warnings)
	}
}

// TestResponseAnswers pins --wait correlation. The chrome case is the
// 2026-09-14 defect verbatim: a `response:notify` from the target closed four
// build and test waits and was printed as their result.
//
// The positive cases are the negative controls — a predicate that simply
// rejected everything would silence the bug and break every wait.
func TestResponseAnswers(t *testing.T) {
	const (
		target = "build"
		host   = "build"
		action = "build"
		msgID  = "1789400000-edit-aaaa"
	)

	cases := []struct {
		name string
		msg  bus.Message
		want bool
	}{
		{"reply naming this request answers it",
			bus.Message{Type: "response", From: "build", Action: "build", ReplyTo: msgID}, true},
		{"unlinked reply with matching action answers it",
			bus.Message{Type: "response", From: "build", Action: "build"}, true},
		{"chrome notify naming an older request does NOT answer",
			bus.Message{Type: "response", From: "build", Action: "notify", ReplyTo: "1789399999-edit-bbbb"}, false},
		{"unlinked reply with a different action does NOT answer",
			bus.Message{Type: "response", From: "build", Action: "notify"}, false},
		{"reply naming a different request does NOT answer, even with matching action",
			bus.Message{Type: "response", From: "build", Action: "build", ReplyTo: "1789399999-edit-bbbb"}, false},
		{"a request is never an answer",
			bus.Message{Type: "request", From: "build", Action: "build", ReplyTo: msgID}, false},
		{"a response from another role does NOT answer",
			bus.Message{Type: "response", From: "test", Action: "build", ReplyTo: msgID}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := responseAnswers(tc.msg, target, host, action, msgID); got != tc.want {
				t.Errorf("responseAnswers = %v, want %v", got, tc.want)
			}
		})
	}
}

// setupWaitSession builds a session where edit has sent target a request and
// edit's inbox holds one chrome notify from that same target. The chrome is
// what closed four real waits on 2026-09-14, so every case below carries it.
func setupWaitSession(t *testing.T, target string) (string, bus.Message, bus.Message) {
	t.Helper()
	dir := t.TempDir()
	bus.SetBusDirBase(dir)
	t.Cleanup(bus.ResetBusDirBase)

	session := "test-wait"
	if err := bus.Init(session, filepath.Join(dir, "memory")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	req := bus.NewMessage("edit", target, "request", "build", "build it", "")
	if err := bus.Send(session, req); err != nil {
		t.Fatalf("Send request: %v", err)
	}

	chrome := bus.NewMessage(target, "edit", "response", "notify", "Acknowledged file change", "")
	if err := bus.AppendToInbox(session, "edit", chrome); err != nil {
		t.Fatalf("AppendToInbox chrome: %v", err)
	}
	return session, req, chrome
}

// TestWaitForResponsePrimaryBranch drives the delivery-status road: the answer
// is consumed and returned, and the unrelated chrome stays readable.
func TestWaitForResponsePrimaryBranch(t *testing.T) {
	session, req, chrome := setupWaitSession(t, "build")

	answer := bus.NewMessage("build", "edit", "response", "build", "exit 0", req.ID)
	if err := bus.AppendToInbox(session, "edit", answer); err != nil {
		t.Fatalf("AppendToInbox answer: %v", err)
	}
	bus.MarkResponded(session, req.ID, answer.ID)

	ok, payload := waitForResponse(session, "edit", "build", "build", req.ID, 10)
	if !ok {
		t.Fatal("expected the correlated answer to satisfy the wait")
	}
	if payload != "exit 0" {
		t.Errorf("payload = %q, want the answer's payload", payload)
	}
	if inboxHas(t, session, "edit", answer.ID) {
		t.Error("the answer must be consumed")
	}
	if !inboxHas(t, session, "edit", chrome.ID) {
		t.Error("unrelated chrome must be left in the inbox, not drained")
	}
}

// TestWaitForResponseFallbackIgnoresChrome is the regression proper: with no
// delivery-status transition, the fallback previously accepted any response
// from the target. Here only an unlinked reply carrying the request's action
// may satisfy it, and the chrome must neither answer nor be consumed.
func TestWaitForResponseFallbackIgnoresChrome(t *testing.T) {
	session, req, chrome := setupWaitSession(t, "build")

	answer := bus.NewMessage("build", "edit", "response", "build", "exit 0", "")
	if err := bus.AppendToInbox(session, "edit", answer); err != nil {
		t.Fatalf("AppendToInbox answer: %v", err)
	}

	ok, payload := waitForResponse(session, "edit", "build", "build", req.ID, 10)
	if !ok {
		t.Fatal("an unlinked reply with the request's action must satisfy the wait")
	}
	if payload != "exit 0" {
		t.Errorf("payload = %q, want the answer's payload — chrome must never be returned", payload)
	}
	if !inboxHas(t, session, "edit", chrome.ID) {
		t.Error("chrome must survive: it was never this request's answer")
	}
	ds, err := bus.ReadDeliveryStatus(session, req.ID)
	if err != nil {
		t.Fatalf("ReadDeliveryStatus: %v", err)
	}
	if ds.ResponseID != answer.ID {
		t.Errorf("recorded ResponseID = %q, want the answer's id %q", ds.ResponseID, answer.ID)
	}
}

// TestWaitForResponseChromeAloneTimesOut is the negative control: chrome by
// itself must NOT satisfy a wait. Without this, a predicate that accepted
// everything would still pass both cases above.
func TestWaitForResponseChromeAloneTimesOut(t *testing.T) {
	session, req, chrome := setupWaitSession(t, "build")

	ok, payload := waitForResponse(session, "edit", "build", "build", req.ID, 4)
	if ok {
		t.Errorf("chrome alone must not satisfy the wait, got payload %q", payload)
	}
	if !inboxHas(t, session, "edit", chrome.ID) {
		t.Error("chrome must remain for the agent to read normally")
	}
}

// TestResponseAnswersHostedRole covers a hosted role answering through its
// host window (docs is hosted by plan), which the sender check must allow.
func TestResponseAnswersHostedRole(t *testing.T) {
	const msgID = "1789400000-edit-cccc"
	reply := bus.Message{Type: "response", From: "plan", Action: "update-docs", ReplyTo: msgID}

	if !responseAnswers(reply, "docs", "plan", "update-docs", msgID) {
		t.Error("a reply from the host agent must answer a request sent to the hosted role")
	}
	if responseAnswers(reply, "docs", "review", "update-docs", msgID) {
		t.Error("a reply from an unrelated role must not answer merely because it is hosted")
	}
}

// The reply a listenerless provider is told to send must correlate to the
// request it answers.
//
// The injected instruction names the action "response", which no caller ever
// sends as a request action, so before the --reply-to was added an unlinked
// reply from OpenCode or scrape-road Codex could never satisfy responseAnswers:
// every --wait on those targets blocked the full 90 seconds and degraded to a
// tracked task even though the agent had already answered (PR #86 review).
func TestInjectedReplyInstructionCorrelates(t *testing.T) {
	const msgID = "1789400000-edit-dddd"

	// That the instruction carries --reply-to is pinned in the bus package by
	// TestBuildReplyCommandCorrelatesToTheRequest; this pins the other half —
	// that a reply in the shape it produces actually correlates here.
	linked := bus.Message{Type: "response", From: "build", Action: "response", ReplyTo: msgID}
	if !responseAnswers(linked, "build", "", "build", msgID) {
		t.Error("a reply following the injected instruction must answer its request")
	}

	// The negative control — the same reply without the correlation is the
	// shape that used to hang, and must still not answer some other request.
	unlinked := bus.Message{Type: "response", From: "build", Action: "response"}
	if responseAnswers(unlinked, "build", "", "build", msgID) {
		t.Error("an uncorrelated response-action reply must not answer a build request")
	}
	if responseAnswers(linked, "build", "", "build", "1789400000-edit-eeee") {
		t.Error("a reply naming a different request must never answer this one")
	}
}
