package cmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestChainMessagesPassValidatePayload pins MUX-177 Phase 2 across every
// default chain that interpolates ${command}: an oversized multi-line heredoc
// still expands, through the hook's ExpandMessageWithContext, to a message
// validatePayload accepts — its leading literal ${changed_files} included,
// which a rescan would expand into the long file list after the bound. The
// raw heredoc is the control that the check can see the failure at all.
func TestChainMessagesPassValidatePayload(t *testing.T) {
	heredoc := "echo ${changed_files}\ncat <<'EOF' > /tmp/x.sh\n" + strings.Repeat("echo some scratch investigation line\n", 40) + "EOF"
	var files []string
	for i := 0; i < 10; i++ {
		files = append(files, "tools/muxcode/bus/a-rather-long-generated-file-name-"+strings.Repeat("x", 30)+".go")
	}
	ctx := &bus.ChainContext{Branch: "MUX-177", ChangedFiles: files}
	if len(validatePayload(heredoc)) == 0 {
		t.Fatal("validatePayload accepted the raw heredoc — the check cannot see the defect")
	}

	reached := map[string]bool{}
	for chain, ec := range bus.DefaultConfig().EventChains {
		for outcome, actions := range map[string]bus.ChainActions{"success": ec.OnSuccess, "failure": ec.OnFailure, "unknown": ec.OnUnknown} {
			for _, a := range actions {
				if !strings.Contains(a.Message, "${command}") {
					continue
				}
				reached[chain] = true
				msg := bus.ExpandMessageWithContext(a.Message, "1", heredoc, ctx)
				if w := validatePayload(msg); len(w) != 0 {
					t.Errorf("%s %s message fails validatePayload %v: %q", chain, outcome, w, msg)
				}
				if !strings.Contains(msg, "[truncated ") {
					t.Errorf("%s %s message truncated the command invisibly: %q", chain, outcome, msg)
				}
			}
		}
	}
	for _, chain := range []string{"run", "watch", "serve"} {
		if !reached[chain] {
			t.Errorf("no %s chain message interpolates ${command} — the audit missed it", chain)
		}
	}
}

// TestAnalystNoticeBounded pins the analyst's copy of a chain outcome to the
// same bound as the chain message: it was built from the raw command, so a
// failing heredoc reached analyze multi-line (MUX-177).
func TestAnalystNoticeBounded(t *testing.T) {
	heredoc := "cat <<'EOF' > /tmp/x.sh\n" + strings.Repeat("echo some scratch investigation line\n", 40) + "EOF"
	for _, outcome := range []string{"success", "failure", "unknown"} {
		msg := analystNotice("run", outcome, "3", heredoc)
		if w := validatePayload(msg); len(w) != 0 {
			t.Errorf("%s analyst notice fails validatePayload %v: %q", outcome, w, msg)
		}
		if !strings.Contains(msg, "[truncated ") {
			t.Errorf("%s analyst notice truncated the command invisibly: %q", outcome, msg)
		}
	}

	for _, tc := range []struct{ outcome, want string }{
		{"success", "Run succeeded: bash scripts/x.sh"},
		{"failure", "Run FAILED (exit 3): bash scripts/x.sh"},
		{"unknown", "Run completed (exit code unknown): bash scripts/x.sh"},
		{"skipped", ""},
	} {
		if got := analystNotice("run", tc.outcome, "3", "bash scripts/x.sh"); got != tc.want {
			t.Errorf("analystNotice(%s) = %q, want %q", tc.outcome, got, tc.want)
		}
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

// MUX-010 Phase 2: the payload's one source. A file and stdin are sent byte
// for byte — multi-line, trailing newline and CRLF included — and flagged by
// reference (which skips the newline and length warnings); an inline payload
// is taken as given and is not (the control). A missing file names its path;
// two sources, or none, are refused.
func TestResolvePayload(t *testing.T) {
	long := strings.Repeat("a long multi-line delegation\n", 30) + "last line, no newline"
	file := filepath.Join(t.TempDir(), "handoff.md")
	if err := os.WriteFile(file, []byte(long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		flags  sendFlags
		stdin  string
		want   string
		byRef  bool
		errHas string
	}{
		{"file, its trailing newline kept", sendFlags{payloadFile: file, payloadFileSet: true}, "", long + "\n", true, ""},
		{"stdin, no trailing newline", sendFlags{inline: "-", inlineSet: true}, long, long, true, ""},
		{"stdin, CRLF kept", sendFlags{inline: "-", inlineSet: true}, "one\r\ntwo\r\n", "one\r\ntwo\r\n", true, ""},
		{"control: inline", sendFlags{inline: "short and single-line", inlineSet: true}, "", "short and single-line", false, ""},
		{"missing file", sendFlags{payloadFile: file + ".gone", payloadFileSet: true}, "", "", false, file + ".gone"},
		{"two sources", sendFlags{inline: "inline too", inlineSet: true, payloadFile: file, payloadFileSet: true}, "", "", false, "give the payload once"},
		{"no source", sendFlags{}, "", "", false, "payload is required"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, byRef, err := resolvePayload(c.flags, strings.NewReader(c.stdin))
			if c.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), c.errHas) || got != "" {
					t.Fatalf("got %q, err %v — want an error naming %q", got, err, c.errHas)
				}
				return
			}
			if err != nil || got != c.want || byRef != c.byRef {
				t.Errorf("got %q byRef=%v err=%v, want %q byRef=%v", got, byRef, err, c.want, c.byRef)
			}
		})
	}
}

// MUX-010 Phase 3: the warning is one prefixed block naming each cause, the
// consequence and both remedies — every line prefixed, so none reads as
// ordinary output.
func TestPayloadWarningBlock(t *testing.T) {
	for _, c := range []struct {
		name, payload string
		causes        []string
	}{
		{"newline", "line one\nline two", []string{"payload contains newlines"}},
		{"long", strings.Repeat("x", 501), []string{"payload is 501 chars (>500)"}},
		{"both", strings.Repeat("x", 300) + "\n" + strings.Repeat("y", 300), []string{"payload contains newlines", "payload is 601 chars (>500)"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			block := payloadWarningBlock(validatePayload(c.payload))
			for _, line := range strings.Split(strings.TrimSuffix(block, "\n"), "\n") {
				if !strings.HasPrefix(line, "!! ") {
					t.Errorf("unprefixed line %q in:\n%s", line, block)
				}
			}
			want := append([]string{"WARNING", "Bash(muxcode *) permission glob", "--payload-file <path>", "/tmp/<name>.md", sendStrictEnv}, c.causes...)
			for _, w := range want {
				if !strings.Contains(block, w) {
					t.Errorf("block does not name %q:\n%s", w, block)
				}
			}
		})
	}
}

// MUXCODE_SEND_STRICT's off spellings read as off; anything else is on.
func TestSendStrict(t *testing.T) {
	for v, want := range map[string]bool{
		"": false, "0": false, "false": false, "FALSE": false, " no ": false, "Off": false,
		"1": true, "true": true, "yes": true, "on": true,
	} {
		t.Setenv(sendStrictEnv, v)
		if got := sendStrict(); got != want {
			t.Errorf("%s=%q: strict = %v, want %v", sendStrictEnv, v, got, want)
		}
	}
}

// MUX-010 Phase 3: the real send's stderr and outcome. A payload the warning
// names — too long, or carrying a newline — prints the warning block, before
// the refusal when MUXCODE_SEND_STRICT is set: NOT SENT, exit 1, written to
// neither inbox nor log. With strict off (unset or 0) the same payload prints
// the block and is delivered as before. Control: a compliant payload prints
// no block and is delivered, under either setting.
func TestSendCLI_StrictRefusesWhatTheWarningNames(t *testing.T) {
	for _, c := range []struct {
		name, strict, payload string
		sent, warned          bool
	}{
		{"strict: too long", "1", strings.Repeat("x", 501), false, true},
		{"strict: newline", "1", "line one\nline two", false, true},
		{"strict off: warned and sent", "", strings.Repeat("x", 501), true, true},
		{"strict=0: warned and sent", "0", "line one\nline two", true, true},
		{"strict=false: warned and sent", "false", "line one\nline two", true, true},
		{"strict=true: refused", "true", strings.Repeat("x", 501), false, true},
		{"control: compliant under strict", "1", "short and single-line", true, false},
		{"control: compliant, strict off", "", "short and single-line", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			session := setupDedupSession(t)
			t.Setenv(sendStrictEnv, c.strict)
			out, errOut, code := runSendCapture(t, "edit", "plan", "notify", c.payload, "--type", "event", "--no-notify")
			if warned := strings.Contains(errOut, "!! muxcode send: WARNING"); warned != c.warned {
				t.Errorf("warning block printed = %v, want %v; stderr %q", warned, c.warned, errOut)
			}
			if strings.Contains(out, "!! ") {
				t.Errorf("the warning reached stdout: %q", out)
			}

			logged, _ := os.ReadFile(filepath.Join(bus.BusDir(session), "log.jsonl"))
			inLog := strings.Contains(string(logged), `"action":"notify"`)
			if c.sent {
				if code != 0 || inboxPayloadCount(t, session, "plan", c.payload) != 1 || !inLog {
					t.Errorf("exit %d, inbox %d, logged %v — want delivered; out %q", code, inboxPayloadCount(t, session, "plan", c.payload), inLog, out)
				}
				return
			}
			if code != 1 || !strings.Contains(out, "NOT SENT") || !strings.Contains(out, sendStrictEnv) {
				t.Errorf("exit %d, out %q — want NOT SENT naming %s, exit 1", code, out, sendStrictEnv)
			}
			if n := inboxPayloadCount(t, session, "plan", c.payload); n != 0 || inLog {
				t.Errorf("a refused send wrote inbox %d, log %v", n, inLog)
			}
		})
	}
}

// MUX-010 Phase 2 review: --payload-file is given once, with a path. A repeat
// once kept the last path silently and an empty path vanished beside an
// inline payload; both are now refused at the parser, before any send, as are
// the existing malformed lines. Control: a well-formed line parses its flags.
func TestParseSendFlags(t *testing.T) {
	for _, c := range []struct {
		name   string
		args   []string
		errHas string
	}{
		{"--payload-file twice", []string{"--payload-file", "a.md", "--payload-file", "b.md"}, "--payload-file given twice"},
		{"empty path", []string{"--payload-file", ""}, "non-empty path"},
		{"empty path beside an inline payload", []string{"inline", "--payload-file", ""}, "non-empty path"},
		{"path missing", []string{"--payload-file"}, "--payload-file requires a path"},
		{"unknown flag", []string{"x", "--bogus"}, "Unknown flag: --bogus"},
		{"second inline argument", []string{"x", "y"}, "Unexpected argument: y"},
		{"--wait with --track", []string{"x", "--wait", "--track"}, "mutually exclusive"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseSendFlags(c.args); err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("parseSendFlags(%q) = %v, want an error naming %q", c.args, err, c.errHas)
			}
		})
	}
	f, err := parseSendFlags([]string{"--payload-file", "h.md", "--type", "response", "--reply-to", "1-edit-a", "--force"})
	if err != nil || f.payloadFile != "h.md" || !f.payloadFileSet || f.inlineSet || f.msgType != "response" || f.replyTo != "1-edit-a" || !f.force {
		t.Errorf("control: parsed %+v (%v)", f, err)
	}
}

// A by-reference payload is the same message to the bus: at the inbox cap it
// is refused like any other, with nothing appended.
func TestResolvedPayloadIsStillCapped(t *testing.T) {
	bus.SetBusDirBase(t.TempDir())
	t.Cleanup(bus.ResetBusDirBase)
	session := "test-payload-cap"
	if err := bus.Init(session, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < bus.InboxCap; i++ {
		if err := bus.SendNoCC(session, bus.NewMessage("edit", "plan", "event", "fill", "x", "")); err != nil {
			t.Fatalf("filling the inbox: %v", err)
		}
	}
	payload, byRef, err := resolvePayload(sendFlags{inline: "-", inlineSet: true}, strings.NewReader("line one\nline two\n"))
	if err != nil || !byRef {
		t.Fatalf("resolvePayload: %q %v %v", payload, byRef, err)
	}
	if err := bus.Send(session, bus.NewMessage("edit", "plan", "request", "plan", payload, "")); !errors.Is(err, bus.ErrInboxFull) {
		t.Errorf("send at the cap = %v, want ErrInboxFull", err)
	}
	if msgs, _ := bus.Peek(session, "plan"); len(msgs) != bus.InboxCap {
		t.Errorf("inbox holds %d, want %d — nothing appended", len(msgs), bus.InboxCap)
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

// MUX-010 Phase 4: --wait's heartbeat. With the interval shortened, a reply
// that lands after several intervals is preceded by at least one
// `still waiting on build (Ns elapsed)` line on the heartbeat writer (stderr in
// production) and none on stdout, which still carries the answer. Control: a
// reply already waiting is returned with no heartbeat at all.
func TestWaitForResponseHeartbeat(t *testing.T) {
	saved, savedOut := waitHeartbeatEvery, waitHeartbeatOut
	t.Cleanup(func() { waitHeartbeatEvery, waitHeartbeatOut = saved, savedOut })
	waitHeartbeatEvery = 300 * time.Millisecond

	for _, c := range []struct {
		name       string
		delay      time.Duration
		heartbeats bool
	}{
		{"late reply", 1600 * time.Millisecond, true},
		{"control: reply already waiting", 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			session, req, _ := setupWaitSession(t, "build")
			var beats strings.Builder
			waitHeartbeatOut = &beats
			answer := func() {
				a := bus.NewMessage("build", "edit", "response", "build", "exit 0", req.ID)
				if err := bus.AppendToInbox(session, "edit", a); err == nil {
					bus.MarkResponded(session, req.ID, a.ID)
				}
			}
			if c.delay == 0 {
				answer()
			} else {
				time.AfterFunc(c.delay, answer)
			}

			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			savedStdout := os.Stdout
			os.Stdout = w
			ok, payload := waitForResponse(session, "edit", "build", "build", req.ID, 10)
			os.Stdout = savedStdout
			w.Close()
			stdout, _ := io.ReadAll(r)

			if !ok || payload != "exit 0" {
				t.Fatalf("wait = %v %q, want the answer", ok, payload)
			}
			got := strings.Count(beats.String(), "still waiting on build (")
			if c.heartbeats && got < 1 || !c.heartbeats && got != 0 {
				t.Errorf("heartbeats = %d (%q), want at least one: %v", got, beats.String(), c.heartbeats)
			}
			if strings.Contains(string(stdout), "still waiting") || !strings.Contains(string(stdout), "exit 0") {
				t.Errorf("stdout %q: want the answer and no heartbeat", stdout)
			}
		})
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
