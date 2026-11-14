package bus

import (
	"strings"
	"testing"
)

// MUX-009 Phase 2: a request whose delivery status records a response stops
// being actionable even while its row is still queued — the row
// MarkResponded's drain did not reach kept the daemon waking build for work it
// had finished. Negative controls: the same request unanswered is actionable,
// and a second, unanswered request beside the answered one still is.
func TestHasActionableMessages_AnsweredRequestDoesNotWake(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	req := NewMessage("edit", "build", "request", "build", "make build", "")
	if err := AppendToInbox(session, "build", req); err != nil {
		t.Fatal(err)
	}
	if err := CreateDeliveryStatus(session, req); err != nil {
		t.Fatal(err)
	}
	if !HasActionableMessages(session, "build") {
		t.Fatal("an unanswered request must be actionable")
	}

	ds, err := ReadDeliveryStatus(session, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	ds.Status, ds.ResponseID = StatusResponded, "resp-1"
	if err := writeDeliveryStatus(session, ds); err != nil {
		t.Fatal(err)
	}
	if msgs := mustPeek(t, session, "build"); len(msgs) != 1 {
		t.Fatalf("fixture: the answered row must still be queued, inbox %+v", msgs)
	}
	if HasActionableMessages(session, "build") {
		t.Error("an answered-but-unconsumed request kept build actionable")
	}

	next := NewMessage("edit", "build", "request", "build", "make build again", "")
	if err := AppendToInbox(session, "build", next); err != nil {
		t.Fatal(err)
	}
	if !HasActionableMessages(session, "build") {
		t.Error("an unanswered request beside the answered one must still be actionable")
	}
}

// MUX-009: on the listenerless roads the wake-up is the delivery, so a
// response typed into the pane reads as an instruction — build re-fired
// request:test five times on 2026-08-17 for answers to its own delegation. For
// a non-orchestrator a response-only batch types nothing and is consumed with
// no receipt and one wake-response-dropped row, and a mixed batch types its
// request alone. edit's results are never typed either: they stay queued, and
// the wake types only a notice of how many wait.
func TestSendWakeUp_ResponsesNeverTypedAsPrompts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider Provider
		pane     string
	}{
		{"opencode", &OpenCodeProvider{}, "╭──────╮\n│ >    │\n╰──────╯"},
		{"codex-scrape", &CodexProvider{}, codexIdleReviewFrame},
	} {
		t.Run(tc.name+"/response-only", func(t *testing.T) {
			session := "mux009-ro-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)
			resp := NewMessage("test", "build", "response", "test", "All 4 packages passed RESP-ONLY", "")
			if err := SendNoCC(session, resp); err != nil {
				t.Fatal(err)
			}
			if err := tc.provider.SendWakeUp(session, "build", true); err != nil {
				t.Fatalf("SendWakeUp: %v", err)
			}
			if keys := sentKeys(*calls); len(keys) != 0 {
				t.Errorf("a response-only batch was typed into the pane: %v", keys)
			}
			if msgs, _ := Peek(session, "build"); len(msgs) != 0 {
				t.Errorf("the dropped response is still queued: %+v", msgs)
			}
			if _, acked := ReadReceipt(session, resp.ID); acked {
				t.Error("a receipt was written for a response the agent never saw")
			}
			rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "wake-response-dropped"})
			if len(rows) != 1 || !strings.Contains(rows[0].Detail, "build: 1 response(s)") || !strings.Contains(rows[0].Detail, "test>test") {
				t.Errorf("wake-response-dropped rows = %+v, want one naming test>test", rows)
			}
		})

		t.Run(tc.name+"/mixed", func(t *testing.T) {
			session := "mux009-mixed-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)
			req := NewMessage("edit", "build", "request", "build", "Run the build MIXED-REQUEST", "")
			resp := NewMessage("test", "build", "response", "test", "All tests passed MIXED-RESPONSE", "")
			for _, m := range []Message{req, resp} {
				if err := SendNoCC(session, m); err != nil {
					t.Fatal(err)
				}
			}
			_ = tc.provider.SendWakeUp(session, "build", true)
			typed := strings.Join(sentKeys(*calls), "\n")
			if !strings.Contains(typed, "MIXED-REQUEST") {
				t.Errorf("the request was not typed in: %q", typed)
			}
			if strings.Contains(typed, "MIXED-RESPONSE") {
				t.Errorf("the response was typed in beside the request: %q", typed)
			}
			for _, m := range mustPeek(t, session, "build") {
				if m.ID == resp.ID {
					t.Error("the response was left queued to be typed in on a later wake")
				}
			}
		})

		// edit's responses are its delegated results: never typed in, kept
		// queued as data for `muxcode inbox`. A response-only wake types the
		// count notice alone — else a listenerless edit never wakes for a
		// result — and a request's prompt carries it too; never their text.
		t.Run(tc.name+"/orchestrator", func(t *testing.T) {
			session := "mux009-edit-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)
			resp := NewMessage("test", "edit", "response", "test", "All tests passed EDIT-RESPONSE", "")
			if err := SendNoCC(session, resp); err != nil {
				t.Fatal(err)
			}
			_ = tc.provider.SendWakeUp(session, "edit", true)
			alone := strings.Join(sentKeys(*calls), "\n")
			if !strings.Contains(alone, "1 delegated result(s) are waiting in your inbox") {
				t.Errorf("a response-only wake must still tell edit a result waits: typed %q", alone)
			}
			if strings.Contains(alone, "EDIT-RESPONSE") {
				t.Errorf("edit's result text was typed in: %q", alone)
			}
			*calls = nil
			queued := false
			for _, m := range mustPeek(t, session, "edit") {
				queued = queued || m.ID == resp.ID
			}
			if !queued {
				t.Error("edit's delegated result was consumed instead of kept for `muxcode inbox`")
			}
			if rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "wake-response-dropped"}); len(rows) != 0 {
				t.Errorf("edit's result was logged as dropped: %+v", rows)
			}

			req := NewMessage("plan", "edit", "request", "review-spec", "Review the spec EDIT-REQUEST", "")
			if err := SendNoCC(session, req); err != nil {
				t.Fatal(err)
			}
			_ = tc.provider.SendWakeUp(session, "edit", true)
			typed := strings.Join(sentKeys(*calls), "\n")
			if !strings.Contains(typed, "EDIT-REQUEST") || !strings.Contains(typed, "1 delegated result(s) are waiting in your inbox") {
				t.Errorf("edit's request must be typed with a notice of its waiting result: %q", typed)
			}
			if strings.Contains(typed, "EDIT-RESPONSE") {
				t.Errorf("edit's result text was typed in as a prompt: %q", typed)
			}
		})
	}
}
