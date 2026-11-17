package bus

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// shippedAgentDefinition reads agents/<name>.md from the repository root.
func shippedAgentDefinition(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))))
	data, err := os.ReadFile(filepath.Join(repoRoot, "agents", name+".md"))
	if err != nil {
		t.Fatalf("read agents/%s.md: %v", name, err)
	}
	return string(data)
}

// runProcedureProblems returns what is wrong with the run definition's
// log-sourcing procedure — the text from "Procedure:" to "This ordering
// matters" — for a batch delivery. MUX-222: steps 1–2 once said to find "the
// most recent" request and execute only it while step 3 said to reply "one
// per request", so a two-request batch was executed once and answered twice.
func runProcedureProblems(def string) []string {
	start := strings.Index(def, "Procedure:")
	end := strings.Index(def, "This ordering matters")
	if start < 0 || end < start {
		return []string{"procedure section (\"Procedure:\" … \"This ordering matters\") not found"}
	}
	proc := strings.Join(strings.Fields(def[start:end]), " ")
	var problems []string
	if strings.Contains(proc, "most recent") {
		problems = append(problems, "procedure selects \"the most recent\" request — a batch's other requests go unexecuted")
	}
	for _, want := range []string{
		"every request the delivery lists",
		"Execute only what this request's logged payload says.",
		"this request's own** delivered reply command",
		"Never send a reply for a request you did not execute",
	} {
		if !strings.Contains(proc, want) {
			problems = append(problems, "procedure lacks "+strconv.Quote(want))
		}
	}
	return problems
}

// TestRunDefinitionExecutesEveryRequestOfABatch pins agents/command-runner.md:
// each request of a batch is taken from the log, executed and answered by its
// own reply command, and no step narrows the procedure to one request.
func TestRunDefinitionExecutesEveryRequestOfABatch(t *testing.T) {
	for _, p := range runProcedureProblems(shippedAgentDefinition(t, "command-runner")) {
		t.Errorf("agents/command-runner.md: %s", p)
	}
}

// TestRunProcedureProblemsRejectsTheMostRecentWording is the negative control:
// the pre-MUX-222 procedure — one request executed, one reply per request —
// must be rejected, so the pin above cannot pass on wording it exists to stop.
func TestRunProcedureProblemsRejectsTheMostRecentWording(t *testing.T) {
	old := "Procedure:\n\n" +
		"1. Read `muxcode history run` and find the most recent `[request:*]` addressed\n   to you from a known agent.\n" +
		"2. **Execute only what that logged payload says.** Run nothing that is not in\n   it.\n" +
		"3. Reply with the delivered reply command for that request, as written — one per request when several arrived together.\n\n" +
		"This ordering matters."
	got := runProcedureProblems(old)
	if len(got) == 0 || !strings.Contains(got[0], "most recent") {
		t.Errorf("old wording not rejected for selecting one request: %q", got)
	}
}

// replyToCount counts the reply instructions in text that name id.
func replyToCount(text, id string) int {
	return strings.Count(text, "--reply-to "+id)
}

// TestSendWakeUp_EveryRequestInABatchGetsItsReply is MUX-189 on the
// listenerless injection roads: a batch of two requests from different senders
// used to be typed with a reply instruction for the last request alone, so the
// first one's task never saw a --reply-to and expired at task-timeout though
// the agent answered it. Each request now gets its own reply line, to its own
// sender, before and after the payload. Controls: a single request renders
// exactly as before the fix, and an event in the batch earns no reply line.
func TestSendWakeUp_EveryRequestInABatchGetsItsReply(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider Provider
		pane     string
	}{
		{"opencode", &OpenCodeProvider{}, "╭──────╮\n│ >    │\n╰──────╯"},
		{"codex-scrape", &CodexProvider{}, codexIdleReviewFrame},
	} {
		t.Run(tc.name+"/two-requests", func(t *testing.T) {
			session := "mux189-two-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)
			first := NewMessage("plan", "build", "request", "build", "Build the docs site FIRST-REQUEST", "")
			second := NewMessage("edit", "build", "request", "build", "Run the build SECOND-REQUEST", "")
			for _, m := range []Message{first, second} {
				if err := SendNoCC(session, m); err != nil {
					t.Fatal(err)
				}
			}
			if err := tc.provider.SendWakeUp(session, "build", true); err != nil {
				t.Fatalf("SendWakeUp: %v", err)
			}
			typed := strings.Join(sentKeys(*calls), "\n")
			if !strings.Contains(typed, "FIRST-REQUEST") || !strings.Contains(typed, "SECOND-REQUEST") {
				t.Fatalf("fixture: both requests must be typed in one payload: %q", typed)
			}
			for _, want := range []struct{ id, cmd string }{
				{first.ID, "muxcode send plan response \"<your one-line summary>\" --type response --reply-to " + first.ID},
				{second.ID, "muxcode send edit response \"<your one-line summary>\" --type response --reply-to " + second.ID},
			} {
				if n := strings.Count(typed, want.cmd); n != 2 {
					t.Errorf("want %q before and after the payload, found %d time(s) in %q", want.cmd, n, typed)
				}
			}
			if !strings.Contains(typed, "holds 2 requests") {
				t.Errorf("a batch must say how many replies it owes: %q", typed)
			}
		})

		t.Run(tc.name+"/one-request", func(t *testing.T) {
			session := "mux189-one-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)
			only := NewMessage("plan", "build", "request", "build", "Build the docs site ONLY-REQUEST", "")
			if err := SendNoCC(session, only); err != nil {
				t.Fatal(err)
			}
			if err := tc.provider.SendWakeUp(session, "build", true); err != nil {
				t.Fatalf("SendWakeUp: %v", err)
			}
			typed := strings.Join(sentKeys(*calls), "\n")
			cmd := buildReplyCommand("plan", only.ID)
			if !strings.Contains(typed, "IMPORTANT: After completing this task, you MUST run this bash command: "+cmd+" — ") ||
				!strings.Contains(typed, " — REMINDER: Your FINAL step MUST be to EXECUTE (not print): "+cmd) {
				t.Errorf("a single request must render its reply exactly as before MUX-189: %q", typed)
			}
			if strings.Contains(typed, "holds 1 request") {
				t.Errorf("a batch of one must not use the multi-request wording: %q", typed)
			}
		})

		t.Run(tc.name+"/request-and-event", func(t *testing.T) {
			session := "mux189-event-" + tc.name
			injectionTestSession(t, session)
			calls := stubInjectionPane(t, tc.pane, nil)
			req := NewMessage("plan", "build", "request", "build", "Build the docs site", "")
			ev := NewMessage("review", "build", "event", "notice", "Review window rotated EVENT-ROW", "")
			for _, m := range []Message{req, ev} {
				if err := SendNoCC(session, m); err != nil {
					t.Fatal(err)
				}
			}
			_ = tc.provider.SendWakeUp(session, "build", true)
			typed := strings.Join(sentKeys(*calls), "\n")
			if !strings.Contains(typed, "EVENT-ROW") {
				t.Fatalf("fixture: the event must be typed in beside the request: %q", typed)
			}
			if replyToCount(typed, ev.ID) != 0 || strings.Contains(typed, "muxcode send review response") {
				t.Errorf("an event earned a reply instruction: %q", typed)
			}
			if replyToCount(typed, req.ID) != 2 || strings.Contains(typed, "holds 2 requests") {
				t.Errorf("the lone request must keep the single-request form: %q", typed)
			}
		})
	}
}

// TestConsumeInboxForHook_EveryRequestCarriesItsOwnReply records MUX-189's
// Phase 1 finding for the Codex hook road: hook stop and prompt-submit hand
// the agent the whole inbox, not one request per prompt, but each message is
// rendered by FormatMessage with its own "To reply" line naming its id and
// reply target, so a batch of two is answerable twice. Unaffected by the
// defect; this guards that it stays so.
func TestConsumeInboxForHook_EveryRequestCarriesItsOwnReply(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	first := NewMessage("plan", "build", "request", "build", "Build the docs site", "")
	second := NewMessage("edit", "build", "request", "build", "Run the build", "")
	for _, m := range []Message{first, second} {
		if err := AppendToInbox(session, "build", m); err != nil {
			t.Fatal(err)
		}
	}
	d := ConsumeInboxForHook(session, "build", true)
	if d.Requests != 2 {
		t.Fatalf("the hook road must deliver both requests in one prompt, got %d (%q)", d.Requests, d.Text)
	}
	for _, want := range []string{
		"To reply: muxcode send plan <action> \"<message>\" --type response --reply-to " + first.ID,
		"To reply: muxcode send edit <action> \"<message>\" --type response --reply-to " + second.ID,
	} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("missing per-request reply line %q in %q", want, d.Text)
		}
	}
}
