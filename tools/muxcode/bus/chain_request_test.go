package bus

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func ackDeliveryOn(t *testing.T) {
	t.Helper()
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	t.Setenv("MUXCODE_DELIVERY_ACK", "")
}

// MUX-213: the test chain's review request names edit as its reply target,
// so review answers edit once and never wakes the test agent with a reply it
// re-ran the suite for. Every other chain request still answers its sender.
func TestNewChainRequest_TestChainRepliesToEdit(t *testing.T) {
	review := &ChainAction{SendTo: "review", Type: "request", Action: "review", Message: "Tests passed"}
	msg := NewChainRequest("test", "test", review, "Tests passed")
	if msg.ReplyRole != "edit" {
		t.Fatalf("test-chain request ReplyRole = %q, want edit", msg.ReplyRole)
	}
	if !strings.Contains(FormatMessage(msg), "To reply: muxcode send edit ") {
		t.Errorf("reply line does not name edit:\n%s", FormatMessage(msg))
	}

	testReq := &ChainAction{SendTo: "test", Type: "request", Action: "test", Message: "Build succeeded"}
	build := NewChainRequest("build", "build", testReq, "Build succeeded")
	if build.ReplyRole != "" || !strings.Contains(FormatMessage(build), "To reply: muxcode send build ") {
		t.Errorf("build-chain request must still answer build: ReplyRole %q\n%s", build.ReplyRole, FormatMessage(build))
	}

	notice := &ChainAction{SendTo: "edit", Type: "event", Action: "notify", Message: "Tests FAILED"}
	if n := NewChainRequest("test", "test", notice, "Tests FAILED"); n.ReplyRole != "" {
		t.Errorf("a test-chain event is not a request; ReplyRole = %q", n.ReplyRole)
	}
}

func TestReplyTarget_NormalizesBothRoads(t *testing.T) {
	if got := ReplyTarget(Message{From: "daemon"}); got != "edit" {
		t.Errorf("daemon sender: %q, want edit", got)
	}
	if got := ReplyTarget(Message{From: "test", ReplyRole: "daemon"}); got != "edit" {
		t.Errorf("daemon reply role: %q, want edit", got)
	}
	if got := ReplyTarget(Message{From: "build"}); got != "build" {
		t.Errorf("plain sender: %q, want build", got)
	}
}

// The 2026-11-13 loop: a review reply delivered to the test agent is consumed
// (acked) like any message, and the run it prompted must find no request
// behind it. A request the agent has received and not answered is the
// negative control: that run is answering it.
func TestOpenRequestFor(t *testing.T) {
	ackDeliveryOn(t)
	session := testSession(t)

	if id, ok := OpenRequestFor(session, "test"); ok {
		t.Fatalf("empty bus: open request %q", id)
	}

	req := NewMessage("edit", "test", "request", "test", "run the suite", "")
	if err := SendNoCC(session, req); err != nil {
		t.Fatal(err)
	}
	if id, ok := OpenRequestFor(session, "test"); ok {
		t.Errorf("an unread request stands behind no run, got %q", id)
	}

	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}
	if id, ok := OpenRequestFor(session, "test"); !ok || id != req.ID {
		t.Fatalf("received, unanswered request: (%q, %v), want (%q, true)", id, ok, req.ID)
	}
	if id, ok := OpenRequestFor(session, "review"); ok {
		t.Errorf("another role's request vouched for review: %q", id)
	}

	MarkResponded(session, req.ID, "resp-1")
	if id, ok := OpenRequestFor(session, "test"); ok {
		t.Errorf("answered request still open: %q", id)
	}

	reply := NewMessage("review", "test", "response", "review-complete", "Review: 0 must-fix, 0 should-fix, 0 nits. EXIT=0", "x")
	if err := SendNoCC(session, reply); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}
	if id, ok := OpenRequestFor(session, "test"); ok {
		t.Errorf("a consumed review reply vouched for a run: %q", id)
	}
}

func TestOpenRequestFor_StaleReceiptDoesNotVouch(t *testing.T) {
	ackDeliveryOn(t)
	session := testSession(t)
	req := NewMessage("edit", "test", "request", "test", "run the suite", "")
	if err := SendNoCC(session, req); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}
	ds, err := ReadDeliveryStatus(session, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	ds.AckedAt = time.Now().Add(-openRequestMaxAge - time.Minute).Unix()
	if err := writeDeliveryStatus(session, ds); err != nil {
		t.Fatal(err)
	}
	if id, ok := OpenRequestFor(session, "test"); ok {
		t.Errorf("a request received %v ago vouched for a run: %q", openRequestMaxAge, id)
	}
}

// With receipts off nothing can be read, so the gate fails open and chains
// behave as before MUX-213 rather than going silent.
func TestOpenRequestFor_FailsOpenWithoutReceipts(t *testing.T) {
	ackDeliveryOn(t)
	session := testSession(t)
	if err := SetAckDeliveryOff(session, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := OpenRequestFor(session, "test"); !ok {
		t.Error("delivery-ack off: want fail-open (ok true)")
	}
}

// The wake-sentence road consumed a request-free inbox under "Handle every
// request below now"; the Codex test agent re-ran the suite for a review reply
// and after timeout notices (MUX-213). For the test role alone, an inbox with
// no request — replies, events, or both — is framed as "run no tests". A test
// inbox carrying a request, and every other role (edit acting on a delegated
// result, build receiving an event), keep the request preamble.
func TestCodexPromptSubmitContext_TestRequestFreeInboxRunsNothing(t *testing.T) {
	session := codexBusEnv(t)
	review := func(to string) Message {
		return NewMessage("review", to, "response", "review-complete", "Review: 0 must-fix, 0 should-fix, 0 nits. EXIT=0", "")
	}
	event := func(to string) Message {
		return NewMessage("daemon", to, "event", "no-answer",
			"[daemon — NOT A RESPONSE: deploy went idle without answering task 1794602233-test-90c2a979]", "")
	}
	cases := []struct {
		name     string
		role     string
		msgs     []Message
		wantTest bool
	}{
		{"test, response only", "test", []Message{review("test")}, true},
		{"test, timeout notice only", "test", []Message{event("test")}, true},
		{"test, response and event", "test", []Message{review("test"), event("test")}, true},
		{"test, response and request", "test", []Message{review("test"), NewMessage("edit", "test", "request", "test", "run the suite", "")}, false},
		{"edit, delegated result arrives", "edit", []Message{NewMessage("run", "edit", "response", "run", "script passed: 12 passed. EXIT=0", "")}, false},
		{"build, event only", "build", []Message{event("build")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, m := range tc.msgs {
				if err := SendNoCC(session, m); err != nil {
					t.Fatal(err)
				}
			}
			ctx, ok := CodexPromptSubmitContext(session, tc.role, WakeSentence)
			if !ok {
				t.Fatal("wake not expanded")
			}
			if got := strings.HasPrefix(ctx, codexTestNoRequestPreamble); got != tc.wantTest {
				t.Errorf("no-work framing = %v, want %v: %q", got, tc.wantTest, ctx)
			}
			if !tc.wantTest && !strings.HasPrefix(ctx, codexDeliveryPreamble) {
				t.Errorf("want the request preamble: %q", ctx)
			}
			if msgs, _ := Peek(session, tc.role); len(msgs) != 0 {
				t.Errorf("inbox not consumed: %d left", len(msgs))
			}
		})
	}
}

func provenanceEvent(id, command string) *ToolEvent {
	return &ToolEvent{HookEventName: "PreToolUse", ToolName: "Bash", ToolUseID: id, ToolInput: ToolInput{Command: command}}
}

// The review must-fix: Claude's `hook bash` is async, so a requested suite's
// reply can mark its request responded before the hook asks. Provenance taken
// at PreToolUse still credits that run; a rerun started after the reply is the
// negative control and is not credited.
func TestTakeTestRunOrigin_SurvivesReplyBeforeHook(t *testing.T) {
	ackDeliveryOn(t)
	session := testSession(t)
	req := NewMessage("edit", "test", "request", "test", "run the suite", "")
	if err := SendNoCC(session, req); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}

	RecordRunProvenance(session, "test", provenanceEvent("toolu_requested", "./test.sh 2>&1"))
	MarkResponded(session, req.ID, "resp-1")
	RecordRunProvenance(session, "test", provenanceEvent("toolu_rerun", "./test.sh 2>&1"))

	if !TakeTestRunOrigin(session, "test", "toolu_requested").Requested {
		t.Error("a requested run whose reply landed before its async hook was withheld")
	}
	if TakeTestRunOrigin(session, "test", "toolu_rerun").Requested {
		t.Error("a rerun started after the reply was credited with the answered request")
	}
	if TakeTestRunOrigin(session, "test", "toolu_requested").Requested {
		t.Error("the record was not consumed: a second read must fall back, and nothing is open now")
	}
}

func TestRecordRunProvenance_OnlyTestCommandsWithAnID(t *testing.T) {
	ackDeliveryOn(t)
	session := testSession(t)
	RecordRunProvenance(session, "test", provenanceEvent("toolu_ls", "ls -la"))
	RecordRunProvenance(session, "test", provenanceEvent("", "./test.sh"))
	RecordRunProvenance(session, "test", provenanceEvent("../..", "./test.sh"))
	entries, _ := os.ReadDir(runProvenanceDir(session))
	if len(entries) != 0 {
		t.Errorf("recorded %d entries for a non-test command or an unusable id", len(entries))
	}
	RecordRunProvenance(session, "test", provenanceEvent("toolu_a/b", "./test.sh"))
	entries, _ = os.ReadDir(runProvenanceDir(session))
	if len(entries) != 1 || strings.ContainsAny(entries[0].Name(), "/") {
		t.Errorf("a test command with an id records one safely named entry, got %v", entries)
	}
}

// sendWithAutoCC sends m through Send with a fresh auto-CC limiter (it is
// package-level, one CC per role per 60s) and fails unless edit holds the copy.
func sendWithAutoCC(t *testing.T, session string, m Message) {
	t.Helper()
	saved := autoCCLastSent
	autoCCLastSent = make(map[string]int64)
	t.Cleanup(func() { autoCCLastSent = saved })
	if err := Send(session, m); err != nil {
		t.Fatal(err)
	}
	for _, cc := range mustPeek(t, session, "edit") {
		if cc.ID == m.ID {
			return
		}
	}
	t.Fatal("no auto-CC copy in edit's inbox — the test cannot prove anything")
}

// MUX-216 finding B, the Phase 1 pin inverted: an auto-CC copy shares its
// original's id, and edit consuming it once rewrote the receipt's AckedBy, so
// a request test had received no longer read as open. The receipt is the
// addressee's alone — edit consuming its copy first must not make the request
// read as received either, or a real delivery gap would be hidden.
func TestOpenRequestFor_CCConsumeLeavesAddresseeReceipt(t *testing.T) {
	ackDeliveryOn(t)
	useTempBusDir(t)
	session := testSession(t)
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	req := NewMessage("build", "test", "request", "test", "run the suite", "")
	sendWithAutoCC(t, session, req)
	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(session, "edit"); err != nil {
		t.Fatal(err)
	}
	if ds, err := ReadDeliveryStatus(session, req.ID); err != nil || ds.AckedBy != "test" {
		t.Errorf("edit's CC consume rewrote the addressee's receipt: AckedBy %q (err %v)", ds.AckedBy, err)
	}
	if id, ok := OpenRequestFor(session, "test"); !ok || id != req.ID {
		t.Errorf("after edit consumed its copy: (%q, %v), want (%q, true)", id, ok, req.ID)
	}
	MarkResponded(session, req.ID, "resp-1")

	early := NewMessage("build", "test", "request", "test", "run it again", "")
	sendWithAutoCC(t, session, early)
	if _, err := Receive(session, "edit"); err != nil {
		t.Fatal(err)
	}
	if _, acked := ReadReceipt(session, early.ID); acked {
		t.Error("edit consuming its copy first receipted a request test has not read")
	}
	if id, ok := OpenRequestFor(session, "test"); ok {
		t.Errorf("an unread request stood behind a run after edit read its copy: %q", id)
	}
}

// MUX-216 finding C, the Phase 1 pin inverted: two runs started under one open
// request both carry it, and only the first claims its review; a release lets
// an unsent dispatch's request stand behind a later run. Negative control: a
// second, distinct request is preferred over the spent one and claims its own.
func TestClaimRequestReview_OneReviewPerRequest(t *testing.T) {
	ackDeliveryOn(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	session := testSession(t)
	req := NewMessage("edit", "test", "request", "test", "run the suite", "")
	if err := SendNoCC(session, req); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}

	RecordRunProvenance(session, "test", provenanceEvent("toolu_first", "./test.sh 2>&1"))
	RecordRunProvenance(session, "test", provenanceEvent("toolu_second", "./test.sh 2>&1"))
	first := TakeTestRunOrigin(session, "test", "toolu_first")
	second := TakeTestRunOrigin(session, "test", "toolu_second")
	if !first.Requested || first.Request != req.ID || second.Request != req.ID {
		t.Fatalf("both runs must carry the request: first %+v, second %+v", first, second)
	}

	if !ClaimRequestReview(session, first.Request) {
		t.Fatal("the first run's review must claim the request")
	}
	if ClaimRequestReview(session, second.Request) {
		t.Error("a second run under the same request claimed a second review")
	}
	ReleaseRequestReview(session, req.ID)
	if !ClaimRequestReview(session, req.ID) {
		t.Error("a released claim must be claimable again")
	}

	LogChainRequestSpent(session, "test", "./test.sh 2>&1", "test", req.ID)
	if rows := chainWithheldRows(t, session); len(rows) != 1 || !strings.Contains(rows[0].Detail, "request "+req.ID) {
		t.Errorf("chain-withheld rows = %+v, want one naming request %s", rows, req.ID)
	}

	other := NewMessage("edit", "test", "request", "test", "run the other change", "")
	if err := SendNoCC(session, other); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}
	if id, ok := OpenRequestFor(session, "test"); !ok || id != other.ID {
		t.Fatalf("a distinct open request must be preferred over the spent one: (%q, %v), want %q", id, ok, other.ID)
	}
	if !ClaimRequestReview(session, other.ID) {
		t.Error("a distinct request must claim its own review")
	}
	MarkResponded(session, other.ID, "resp-2")
	if id, ok := OpenRequestFor(session, "test"); !ok || id != req.ID {
		t.Errorf("with only the spent request open it is still named: (%q, %v), want %q", id, ok, req.ID)
	}
}

// MUX-216 finding A: the listenerless roads typed a reply instruction built
// from the sender, so a test-chain review answered test on OpenCode and
// scrape-road Codex. Negative control: a request with no reply role still
// answers its sender.
func TestSendWakeUp_ReplyInstructionNamesReplyTarget(t *testing.T) {
	review := &ChainAction{SendTo: "review", Type: "request", Action: "review", Message: "Tests passed"}
	for _, tc := range []struct {
		name     string
		provider Provider
		pane     string
	}{
		{"opencode", &OpenCodeProvider{}, "╭──────╮\n│ >    │\n╰──────╯"},
		{"codex-scrape", &CodexProvider{}, codexIdleReviewFrame},
	} {
		for _, req := range []struct {
			msg  Message
			want string
		}{
			{NewChainRequest("test", "test", review, "Tests passed"), "muxcode send edit response"},
			{NewMessage("build", "review", "request", "review", "review the build", ""), "muxcode send build response"},
		} {
			t.Run(tc.name+"/"+req.msg.From, func(t *testing.T) {
				session := "mux216-reply-" + tc.name + "-" + req.msg.From
				injectionTestSession(t, session)
				calls := stubInjectionPane(t, tc.pane, nil)
				if err := SendNoCC(session, req.msg); err != nil {
					t.Fatal(err)
				}
				if err := tc.provider.SendWakeUp(session, "review", true); errors.Is(err, ErrInjectionSkipped) {
					t.Fatalf("the wake must inject, got %v", err)
				}
				typed := strings.Join(sentKeys(*calls), "\n")
				if !strings.Contains(typed, req.want+" ") || !strings.Contains(typed, "--reply-to "+req.msg.ID) {
					t.Errorf("reply instruction must be %q correlated to %s, typed %q", req.want, req.msg.ID, typed)
				}
			})
		}
	}
}

// With no record (no guard hook ran), the check falls back to asking now.
func TestTakeTestRunOrigin_FallsBackWithoutRecord(t *testing.T) {
	ackDeliveryOn(t)
	session := testSession(t)
	if TakeTestRunOrigin(session, "test", "toolu_none").Requested {
		t.Error("no record and no open request: want false")
	}
	req := NewMessage("edit", "test", "request", "test", "run the suite", "")
	if err := SendNoCC(session, req); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}
	if o := TakeTestRunOrigin(session, "test", "toolu_none"); !o.Requested || o.GraphRun != "" {
		t.Errorf("no record but an open request: want Requested and no graph run, got %+v", o)
	}
}
