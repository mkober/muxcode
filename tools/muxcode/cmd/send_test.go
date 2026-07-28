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
