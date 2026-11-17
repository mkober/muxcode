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

// MUX-220 review must-fix: an idle listenerless sender whose inbox holds only
// its own --track result is woken with the count notice alone — it has no poll,
// so without one it never learns the result arrived — and never the result's
// text. The MUX-009 response-only case (an uncorrelated response) still types
// nothing; see TestSendWakeUp_ResponsesNeverTypedAsPrompts.
func TestSendWakeUp_OwnResultOnlyTypesNotice(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider Provider
		pane     string
	}{
		{"opencode", &OpenCodeProvider{}, "╭──────╮\n│ >    │\n╰──────╯"},
		{"codex-scrape", &CodexProvider{}, codexIdleReviewFrame},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "mux220-notice-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)
			own := NewMessage("build", "test", "request", "test", "Run tests", "")
			if err := CreateTask(session, own, 600); err != nil {
				t.Fatal(err)
			}
			reply := NewMessage("test", "build", "response", "test", "All tests passed OWN-RESULT", own.ID)
			if err := SendNoCC(session, reply); err != nil {
				t.Fatal(err)
			}
			if err := tc.provider.SendWakeUp(session, "build", true); err != nil {
				t.Fatalf("SendWakeUp: %v", err)
			}
			typed := strings.Join(sentKeys(*calls), "\n")
			if !strings.Contains(typed, "1 result(s) of your own requests are waiting in your inbox as data") {
				t.Errorf("a response-only wake with build's own result typed no notice: %q", typed)
			}
			if strings.Contains(typed, "OWN-RESULT") || strings.Contains(typed, "--reply-to") {
				t.Errorf("the result's text or a reply instruction was typed: %q", typed)
			}
			if msgs := mustPeek(t, session, "build"); len(msgs) != 1 || msgs[0].ID != reply.ID {
				t.Errorf("build's inbox %+v, want its own result still queued", msgs)
			}
		})
	}
}

// MUX-220 Phase 3: cmd/send.go writes a request's task after Notify, so a fast
// reply can reach the wake before its task exists; the request Send already
// logged then decides, and the reply is kept. Negative controls: an id that was
// minted but never sent, a response's id, and another role's sent request are
// not build's.
func TestAnswersOwnDelegation_BeforeTaskWritten(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	send := func(m Message) string {
		t.Helper()
		if err := SendNoCC(session, m); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	for _, tc := range []struct {
		name, replyTo string
		want          bool
	}{
		{"sent, task not yet written", send(NewMessage("build", "test", "request", "test", "go", "")), true},
		{"minted, never sent", NewMessage("build", "test", "request", "test", "go", "").ID, false},
		{"a response's id", send(NewMessage("build", "test", "response", "test", "done", "")), false},
		{"another role's request", send(NewMessage("review", "test", "request", "test", "go", "")), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := NewMessage("test", "build", "response", "test", "done", tc.replyTo)
			if got := answersOwnDelegation(session, "build", reply); got != tc.want {
				t.Errorf("answersOwnDelegation(%s) = %v, want %v", tc.replyTo, got, tc.want)
			}
		})
	}
}

// MUX-220 Phase 2: MarkResponded drains an answered row only when it finds the
// request in the bus log; a row queued without one (AppendToInbox writes no
// log line) outlives its answer. Reproduced through the real MarkResponded:
// the row stays queued and responded. Neither listenerless wake types it — it
// is consumed with one wake-answered-dropped row — while an open request beside
// it is still typed (negative control).
func TestSendWakeUp_AnsweredRequestNotReinjected(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider Provider
		pane     string
	}{
		{"opencode", &OpenCodeProvider{}, "╭──────╮\n│ >    │\n╰──────╯"},
		{"codex-scrape", &CodexProvider{}, codexIdleReviewFrame},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "mux220-answered-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)

			done := NewMessage("edit", "build", "request", "build", "Run the build ANSWERED-REQUEST", "")
			if err := AppendToInbox(session, "build", done); err != nil {
				t.Fatal(err)
			}
			if err := CreateDeliveryStatus(session, done); err != nil {
				t.Fatal(err)
			}
			MarkResponded(session, done.ID, "resp-1")
			queued := false
			for _, m := range mustPeek(t, session, "build") {
				queued = queued || m.ID == done.ID
			}
			if !queued || !IsResponded(session, done.ID) {
				t.Fatalf("reproduction: the answered row must survive MarkResponded (queued=%v responded=%v)", queued, IsResponded(session, done.ID))
			}
			open := NewMessage("edit", "build", "request", "build", "Run it again OPEN-REQUEST", "")
			if err := SendNoCC(session, open); err != nil {
				t.Fatal(err)
			}

			_ = tc.provider.SendWakeUp(session, "build", true)
			typed := strings.Join(sentKeys(*calls), "\n")
			if strings.Contains(typed, "ANSWERED-REQUEST") {
				t.Errorf("an answered request was re-injected: %q", typed)
			}
			if !strings.Contains(typed, "OPEN-REQUEST") {
				t.Errorf("the open request beside it was not typed: %q", typed)
			}
			for _, m := range mustPeek(t, session, "build") {
				if m.ID == done.ID {
					t.Error("the answered row was left queued for a later wake")
				}
			}
			if ds, _ := ReadReceipt(session, done.ID); ds.AckedAt != 0 {
				t.Error("a receipt was written for an answered row the agent never saw")
			}
			rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "wake-answered-dropped"})
			if len(rows) != 1 || !strings.Contains(rows[0].Detail, "build: 1 answered request(s)") {
				t.Errorf("wake-answered-dropped rows = %+v, want one for build", rows)
			}
		})
	}
}

// MUX-220 review must-fix: a hosted role's delegation is its host's inbox's.
// docs's request and its reply both route to plan's inbox, so plan's wake
// keeps the reply, untyped, beside a dropped unrelated response. Negative
// control: a reply to docs correlated to a task another inbox (build) sent is
// not plan's and is dropped.
func TestSendWakeUp_KeepsHostedRoleDelegationReply(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider Provider
		pane     string
	}{
		{"opencode", &OpenCodeProvider{}, "╭──────╮\n│ >    │\n╰──────╯"},
		{"codex-scrape", &CodexProvider{}, codexIdleReviewFrame},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "mux220-hosted-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)

			own := NewMessage("docs", "research", "request", "research", "Find references", "")
			foreign := NewMessage("build", "research", "request", "research", "Look this up", "")
			for _, m := range []Message{own, foreign} {
				if err := CreateTask(session, m, 600); err != nil {
					t.Fatal(err)
				}
			}
			reply := NewMessage("research", "docs", "response", "research", "Found three HOSTED-REPLY", own.ID)
			misrouted := NewMessage("research", "docs", "response", "research", "Not yours FOREIGN-REPLY", foreign.ID)
			stray := NewMessage("review", "docs", "response", "review", "LGTM STRAY-RESPONSE", "")
			for _, m := range []Message{reply, misrouted, stray} {
				if err := SendNoCC(session, m); err != nil {
					t.Fatal(err)
				}
			}
			if msgs := mustPeek(t, session, "plan"); len(msgs) != 3 {
				t.Fatalf("fixture: docs's mail must route to plan's inbox, got %+v", msgs)
			}

			_ = tc.provider.SendWakeUp(session, "plan", true)
			typed := strings.Join(sentKeys(*calls), "\n")
			for _, text := range []string{"HOSTED-REPLY", "FOREIGN-REPLY", "STRAY-RESPONSE"} {
				if strings.Contains(typed, text) {
					t.Errorf("response %s was typed into plan's pane: %q", text, typed)
				}
			}
			queued := map[string]bool{}
			for _, m := range mustPeek(t, session, "plan") {
				queued[m.ID] = true
			}
			if !queued[reply.ID] {
				t.Error("the reply to docs's own request was consumed from plan's inbox")
			}
			if queued[misrouted.ID] || queued[stray.ID] {
				t.Errorf("a reply plan's inbox does not own was left queued: %v", queued)
			}
			rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "wake-response-dropped"})
			if len(rows) != 1 || !strings.Contains(rows[0].Detail, "plan: 2 response(s)") || !strings.Contains(rows[0].Detail, "1 kept") {
				t.Errorf("wake-response-dropped rows = %+v, want one dropping 2 and keeping 1", rows)
			}
		})
	}
}

// MUX-220: a reply to a request build itself sent survives the wake, untyped,
// for build's own poll, while an uncorrelated response in the same batch is
// still consumed and logged (negative control) and the open request is typed.
// Consuming the reply had left build's --wait to degrade to a task never
// answered. A second wake neither types the kept reply nor logs it again.
func TestSendWakeUp_KeepsReplyToOwnDelegation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider Provider
		pane     string
	}{
		{"opencode", &OpenCodeProvider{}, "╭──────╮\n│ >    │\n╰──────╯"},
		{"codex-scrape", &CodexProvider{}, codexIdleReviewFrame},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := "mux220-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)

			own := NewMessage("build", "test", "request", "test", "Run tests", "")
			if err := CreateTask(session, own, 600); err != nil {
				t.Fatal(err)
			}
			open := NewMessage("edit", "build", "request", "build", "Run the build OPEN-REQUEST", "")
			reply := NewMessage("test", "build", "response", "test", "All tests passed OWN-REPLY", own.ID)
			stray := NewMessage("review", "build", "response", "review", "LGTM STRAY-RESPONSE", "")
			for _, m := range []Message{open, reply, stray} {
				if err := SendNoCC(session, m); err != nil {
					t.Fatal(err)
				}
			}

			_ = tc.provider.SendWakeUp(session, "build", true)
			typed := strings.Join(sentKeys(*calls), "\n")
			if !strings.Contains(typed, "OPEN-REQUEST") {
				t.Errorf("the open request was not typed in: %q", typed)
			}
			for _, text := range []string{"OWN-REPLY", "STRAY-RESPONSE"} {
				if strings.Contains(typed, text) {
					t.Errorf("response %s was typed into the pane: %q", text, typed)
				}
			}
			if !strings.Contains(typed, "1 result(s) of your own requests are waiting in your inbox") {
				t.Errorf("the prompt does not tell build its own result waits: %q", typed)
			}
			var kept, strayQueued bool
			for _, m := range mustPeek(t, session, "build") {
				kept = kept || (m.ID == reply.ID && m.ReplyTo == own.ID)
				strayQueued = strayQueued || m.ID == stray.ID
			}
			if !kept {
				t.Error("the reply to build's own request was consumed before its poll could find it")
			}
			if strayQueued {
				t.Error("the uncorrelated response was left queued")
			}
			rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "wake-response-dropped"})
			if len(rows) != 1 || !strings.Contains(rows[0].Detail, "build: 1 response(s)") ||
				!strings.Contains(rows[0].Detail, "1 kept") || !strings.Contains(rows[0].Detail, "review>review") ||
				strings.Contains(rows[0].Detail, "test>test") {
				t.Errorf("wake-response-dropped rows = %+v, want one dropping review>review and counting 1 kept", rows)
			}

			*calls = nil
			_ = tc.provider.SendWakeUp(session, "build", true)
			if typed := strings.Join(sentKeys(*calls), "\n"); strings.Contains(typed, "OWN-REPLY") {
				t.Errorf("a later wake typed the kept reply: %q", typed)
			}
			if rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "wake-response-dropped"}); len(rows) != 1 {
				t.Errorf("a later wake logged the kept reply as dropped: %+v", rows)
			}
		})
	}
}
