package bus

import (
	"errors"
	"strings"
	"testing"
)

func replyToNotices(t *testing.T, session string) []Message {
	t.Helper()
	msgs, _ := Peek(session, "edit")
	var out []Message
	for _, m := range msgs {
		if m.Action == "reply-to-unknown" || m.Action == "reply-to-repaired" {
			out = append(out, m)
		}
	}
	return out
}

// replyToSession is a scratch session with delivery acks on, its own
// lifecycle log and the consumer guard off.
func replyToSession(t *testing.T) string {
	t.Helper()
	useTempBusDir(t)
	isolateLifecycle(t)
	ackDeliveryOn(t)
	t.Setenv("MUXCODE_CONSUMER_GUARD", "0")
	return testSession(t)
}

// dispatchTracked sends a tracked request from `from` to test, as the daemon
// dispatches a graph test node. SendForce: a second tracked request on the
// same from, to and action is otherwise suppressed as a duplicate of the first.
func dispatchTracked(t *testing.T, session, from, payload string) Message {
	t.Helper()
	m := NewMessage(from, "test", "request", "test", payload, "")
	if err := SendForce(session, m); err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(session, m, 600); err != nil {
		t.Fatal(err)
	}
	return m
}

// MUX-225 negative controls: a response with no --reply-to, one whose id the
// bus knows (logged, or task-only), a stale id naming an already-answered
// request, and a request carrying --reply-to all pass through unchanged, with
// no row and no notice — the stale id is out of this spec's decided scope.
func TestResolveResponseReplyTo_PassThrough(t *testing.T) {
	session := replyToSession(t)
	dispatch := dispatchTracked(t, session, "daemon", "run the suite")
	taskOnly := NewMessage("daemon", "test", "request", "test", "never logged", "")
	if err := CreateTask(session, taskOnly, 600); err != nil {
		t.Fatal(err)
	}
	stale := dispatchTracked(t, session, "daemon", "answered earlier")
	if err := CreateDeliveryStatus(session, stale); err != nil {
		t.Fatal(err)
	}
	MarkResponded(session, stale.ID, "resp-old")

	for _, tc := range []struct{ name, msgType, replyTo string }{
		{"no --reply-to", "response", ""},
		{"the right id", "response", dispatch.ID},
		{"a task-only id", "response", taskOnly.ID},
		{"a stale, answered id", "response", stale.ID},
		{"a request carrying --reply-to", "request", "1794683890-da0a89855"},
	} {
		got, open, err := ResolveResponseReplyTo(session, "test", "edit", tc.msgType, tc.replyTo)
		if err != nil || got != tc.replyTo || open != nil {
			t.Errorf("%s: (%q, %v, %v), want it unchanged", tc.name, got, open, err)
		}
	}
	if log := lifecycleText(t); strings.Contains(log, "reply-to-unknown") || strings.Contains(log, "reply-to-repaired") {
		t.Errorf("a known or absent --reply-to wrote a row:\n%s", log)
	}
	if n := replyToNotices(t, session); len(n) != 0 {
		t.Errorf("a known or absent --reply-to told edit: %+v", n)
	}
}

// MUX-225 Decision 1, the 2026-11-14 shape: the daemon's one open test
// dispatch and a reply to edit, action `response`, whose id dropped six
// characters. The reply is attached to the dispatch — so the node closes
// rather than waiting out 600 s — with a reply-to-repaired row naming both
// ids, and edit is told once: a retry with the same id repairs again, silently.
func TestResolveResponseReplyTo_RepairsTheOneOpenRequest(t *testing.T) {
	session := replyToSession(t)
	dispatch := NewMessage("daemon", "test", "request", "test", "run the suite", "")
	dispatch.ID = "1794683890-daemon-30a89855"
	if err := SendNoCC(session, dispatch); err != nil {
		t.Fatal(err)
	}
	if err := CreateTask(session, dispatch, 600); err != nil {
		t.Fatal(err)
	}
	mangled := "1794683890-da0a89855"

	got, _, err := ResolveResponseReplyTo(session, "test", "edit", "response", mangled)
	if err != nil || got != dispatch.ID {
		t.Fatalf("resolved (%q, %v), want the one open request %s", got, err, dispatch.ID)
	}
	log := lifecycleText(t)
	if strings.Count(log, `"reply-to-repaired"`) != 1 || !strings.Contains(log, mangled) || !strings.Contains(log, "repaired to "+dispatch.ID) {
		t.Errorf("want one reply-to-repaired row naming %s and %s:\n%s", mangled, dispatch.ID, log)
	}
	if strings.Contains(log, `"reply-to-unknown"`) {
		t.Errorf("a repaired reply also logged reply-to-unknown:\n%s", log)
	}
	notices := replyToNotices(t, session)
	if len(notices) != 1 || notices[0].Action != "reply-to-repaired" || !strings.Contains(notices[0].Payload, dispatch.ID) {
		t.Errorf("edit notices %+v, want one reply-to-repaired naming %s", notices, dispatch.ID)
	}

	if got, _, err := ResolveResponseReplyTo(session, "test", "edit", "response", mangled); err != nil || got != dispatch.ID {
		t.Errorf("retry resolved (%q, %v)", got, err)
	}
	if n := replyToNotices(t, session); len(n) != 1 {
		t.Errorf("a retried id told edit again: %+v", n)
	}
}

// MUX-225 Decision 1 negative control: two open requests from the same
// requester never resolve by guess — the reply is refused, both are returned
// for the caller to print a reply command each, with a reply-to-unknown row;
// zero open requests are refused the same way.
func TestResolveResponseReplyTo_RefusesWithoutOneOpenRequest(t *testing.T) {
	t.Run("two from the requester", func(t *testing.T) {
		session := replyToSession(t)
		a := dispatchTracked(t, session, "edit", "first")
		b := dispatchTracked(t, session, "edit", "second")
		got, open, err := ResolveResponseReplyTo(session, "test", "edit", "response", "1794683890-nothing")
		if !errors.Is(err, ErrReplyToUnknown) || got != "" {
			t.Fatalf("resolved (%q, %v), want ErrReplyToUnknown", got, err)
		}
		ids := map[string]bool{}
		for _, o := range open {
			ids[o.ID] = true
		}
		if !ids[a.ID] || !ids[b.ID] {
			t.Errorf("open %+v, want both %s and %s", open, a.ID, b.ID)
		}
		for _, o := range open {
			if cmd := ReplyCommandFor(o); !strings.Contains(cmd, "muxcode send edit ") || !strings.Contains(cmd, "--reply-to "+o.ID) {
				t.Errorf("reply command %q does not answer %s to edit", cmd, o.ID)
			}
		}
		log := lifecycleText(t)
		if strings.Count(log, `"reply-to-unknown"`) != 1 || strings.Contains(log, `"reply-to-repaired"`) {
			t.Errorf("want one reply-to-unknown row and no repair:\n%s", log)
		}
		if n := replyToNotices(t, session); len(n) != 1 || n[0].Action != "reply-to-unknown" {
			t.Errorf("edit notices %+v, want one reply-to-unknown", n)
		}
	})
	t.Run("none open", func(t *testing.T) {
		session := replyToSession(t)
		got, open, err := ResolveResponseReplyTo(session, "test", "edit", "response", "1794683890-nothing")
		if !errors.Is(err, ErrReplyToUnknown) || got != "" || len(open) != 0 {
			t.Errorf("resolved (%q, %v, %v), want a refusal with no open requests", got, open, err)
		}
		if !strings.Contains(lifecycleText(t), "open requests: none") {
			t.Errorf("row does not say test holds none:\n%s", lifecycleText(t))
		}
	})
	t.Run("one from another requester", func(t *testing.T) {
		session := replyToSession(t)
		dispatchTracked(t, session, "plan", "plan's")
		if _, _, err := ResolveResponseReplyTo(session, "test", "edit", "response", "1794683890-nothing"); !errors.Is(err, ErrReplyToUnknown) {
			t.Errorf("a reply to edit was attached to plan's request: %v", err)
		}
	})
}

// MUX-225 review should-fix: an untracked request — sent with no task, then
// received — is open only by its receipt, and is the one a mangled reply to
// its requester is attached to. Negative controls: a received request since
// answered, and one received by another role, are neither chosen nor named.
func TestResolveResponseReplyTo_ReceivedUntrackedRequest(t *testing.T) {
	session := replyToSession(t)
	send := func(to, payload string) Message {
		m := NewMessage("edit", to, "request", "test", payload, "")
		if err := SendNoCC(session, m); err != nil {
			t.Fatal(err)
		}
		if _, err := Receive(session, to); err != nil {
			t.Fatal(err)
		}
		return m
	}
	answered := send("test", "answered")
	MarkResponded(session, answered.ID, "resp-1")
	open := send("test", "open")
	other := send("build", "build's")

	got, _, err := ResolveResponseReplyTo(session, "test", "edit", "response", "1794683890-da0a89855")
	if err != nil || got != open.ID {
		t.Fatalf("resolved (%q, %v), want the received untracked request %s", got, err, open.ID)
	}
	log := lifecycleText(t)
	for _, id := range []string{answered.ID, other.ID} {
		if strings.Contains(log, id) {
			t.Errorf("row names %s, which test does not hold open:\n%s", id, log)
		}
	}
}

// MUX-225 review must-fix: a tracked dispatch that timed out keeps the receipt
// its consume wrote, but its task record says it is over — it is never a repair
// target. Alone it leaves nothing open, so the reply is refused; beside a live
// dispatch it is ignored and the reply is attached to the live one.
func TestResolveResponseReplyTo_TimedOutTaskIsNotOpen(t *testing.T) {
	session := replyToSession(t)
	expired := dispatchTracked(t, session, "daemon", "the expired run")
	if _, err := Receive(session, "test"); err != nil {
		t.Fatal(err)
	}
	TimeoutTask(session, expired.ID)

	if got, open, err := ResolveResponseReplyTo(session, "test", "edit", "response", "1794683890-da0a89855"); !errors.Is(err, ErrReplyToUnknown) || got != "" || len(open) != 0 {
		t.Errorf("a consumed timed-out dispatch alone resolved (%q, %+v, %v), want a refusal with nothing open", got, open, err)
	}

	live := dispatchTracked(t, session, "daemon", "the live run")
	if got, _, err := ResolveResponseReplyTo(session, "test", "edit", "response", "1794683890-da0a89856"); err != nil || got != live.ID {
		t.Errorf("resolved (%q, %v), want the live dispatch %s, not the timed-out %s", got, err, live.ID, expired.ID)
	}
}

// chainReviewRequest sends test's chain review request, which names edit as
// its reply target (NewChainRequest, MUX-213), and has review read it.
func chainReviewRequest(t *testing.T, session, payload string) Message {
	t.Helper()
	m := NewChainRequest("test", "test", &ChainAction{SendTo: "review", Type: "request", Action: "review"}, payload)
	if m.ReplyRole != "edit" {
		t.Fatalf("fixture: chain request ReplyRole %q, want edit", m.ReplyRole)
	}
	if err := SendForce(session, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(session, "review"); err != nil {
		t.Fatal(err)
	}
	return m
}

// MUX-225 review must-fix: a chain request from test is answered to edit, its
// ReplyRole. A mangled reply to edit is attached to it; the same reply sent to
// test, its sender, is refused — matching by sender would route the reply back
// to test and revive the reply-to-test cycle MUX-213 removed. With two chain
// requests open the reply is refused and every printed command targets edit.
func TestResolveResponseReplyTo_HonoursReplyRole(t *testing.T) {
	t.Run("one chain request", func(t *testing.T) {
		session := replyToSession(t)
		req := chainReviewRequest(t, session, "review the tree")
		if got, _, err := ResolveResponseReplyTo(session, "review", "edit", "response", "1794683890-nothing"); err != nil || got != req.ID {
			t.Errorf("reply to edit resolved (%q, %v), want the chain request %s", got, err, req.ID)
		}
		if _, _, err := ResolveResponseReplyTo(session, "review", "test", "response", "1794683890-other"); !errors.Is(err, ErrReplyToUnknown) {
			t.Errorf("a reply to test, the chain request's sender, was attached: %v", err)
		}
	})
	t.Run("two chain requests", func(t *testing.T) {
		session := replyToSession(t)
		chainReviewRequest(t, session, "first review")
		chainReviewRequest(t, session, "second review")
		_, open, err := ResolveResponseReplyTo(session, "review", "edit", "response", "1794683890-nothing")
		if !errors.Is(err, ErrReplyToUnknown) || len(open) != 2 {
			t.Fatalf("resolved (%+v, %v), want a refusal listing both", open, err)
		}
		for _, o := range open {
			if cmd := ReplyCommandFor(o); !strings.Contains(cmd, "muxcode send edit ") {
				t.Errorf("printed command %q does not target edit, the chain request's reply role", cmd)
			}
		}
	})
}

// Edit's own unknown reply id is logged but never echoed back to edit.
func TestResolveResponseReplyTo_EditSenderNotNotified(t *testing.T) {
	session := replyToSession(t)
	ResolveResponseReplyTo(session, "edit", "plan", "response", "1794683890-nothing")
	if !strings.Contains(lifecycleText(t), "edit replied to plan with --reply-to 1794683890-nothing") {
		t.Errorf("edit's unknown id wrote no row:\n%s", lifecycleText(t))
	}
	if n := replyToNotices(t, session); len(n) != 0 {
		t.Errorf("edit was told about its own reply: %+v", n)
	}
}

// OpenRequestsFor lists only the in-flight, unanswered requests addressed to
// the role's inbox, oldest first.
func TestOpenRequestsFor(t *testing.T) {
	useTempBusDir(t)
	t.Setenv("MUXCODE_CONSUMER_GUARD", "0")
	session := testSession(t)
	mk := func(to string) Message {
		m := NewMessage("daemon", to, "request", "test", "x", "")
		if err := CreateTask(session, m, 600); err != nil {
			t.Fatal(err)
		}
		return m
	}
	first, other, answered := mk("test"), mk("build"), mk("test")
	if err := CreateDeliveryStatus(session, answered); err != nil {
		t.Fatal(err)
	}
	MarkResponded(session, answered.ID, "resp")
	got := OpenRequestsFor(session, "test")
	if len(got) != 1 || got[0].ID != first.ID {
		t.Errorf("open requests %+v, want only %s (not build's %s or the answered %s)", got, first.ID, other.ID, answered.ID)
	}
}
