package bus

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func countPayload(t *testing.T, session, role, payload string) int {
	t.Helper()
	msgs, _ := Peek(session, role)
	n := 0
	for _, m := range msgs {
		if m.Payload == payload {
			n++
		}
	}
	return n
}

func sendResponse(session, from, to, action, payload, replyTo string) error {
	return Send(session, NewMessage(from, to, "response", action, payload, replyTo))
}

// TestNormalizeResponse folds case, whitespace and punctuation but keeps
// numbers and ids significant (the user's Decision 2).
func TestNormalizeResponse(t *testing.T) {
	same := [][2]string{
		{"Acknowledged.", "acknowledged"},
		{"Confirmed: done", "confirmed  —  DONE!"},
		{"  ok\n", "OK"},
	}
	for _, c := range same {
		if normalizeResponse(c[0]) != normalizeResponse(c[1]) {
			t.Errorf("%q and %q must match", c[0], c[1])
		}
	}
	differ := [][2]string{
		{"Phase 2: 10/23 ticked", "Phase 2: 11/23 ticked"},
		{"10/23", "102/3"},
		{"task 1794-edit-aa done", "task 1794-edit-ab done"},
	}
	for _, c := range differ {
		if normalizeResponse(c[0]) == normalizeResponse(c[1]) {
			t.Errorf("%q and %q must differ", c[0], c[1])
		}
	}
}

// TestResponseBrakeStopsRepeatedAck is the 2026-09-10 build↔test shape: the
// first `Acknowledged.` is delivered, and a repeat — including one differing
// only in case, spacing or punctuation — is refused with ErrResponseSuppressed
// on the first repeat, reaching neither test's inbox nor edit's auto-CC copy,
// with one response-suppressed row per refusal naming the pair and action.
func TestResponseBrakeStopsRepeatedAck(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	if err := sendResponse(session, "build", "test", "test", "Acknowledged.", ""); err != nil {
		t.Fatalf("first ack: %v", err)
	}
	editBefore := countPayload(t, session, "edit", "Acknowledged.")
	for _, repeat := range []string{"Acknowledged.", "acknowledged", " ACKNOWLEDGED !"} {
		if err := sendResponse(session, "build", "test", "test", repeat, ""); !errors.Is(err, ErrResponseSuppressed) {
			t.Errorf("repeat %q: err %v, want ErrResponseSuppressed", repeat, err)
		}
	}
	if n := len(peekAll(t, session, "test")); n != 1 {
		t.Errorf("test's inbox holds %d responses, want only the first", n)
	}
	if after := countPayload(t, session, "edit", "Acknowledged.") + countPayload(t, session, "edit", "acknowledged") +
		countPayload(t, session, "edit", " ACKNOWLEDGED !"); after != editBefore {
		t.Errorf("a suppressed repeat reached edit's auto-CC copy: %d copies, was %d", after, editBefore)
	}
	rows, err := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "response-suppressed"})
	if err != nil || len(rows) != 3 || !strings.HasPrefix(rows[0].Detail, "build→test:test ") {
		t.Errorf("want 3 response-suppressed rows naming build→test:test, got %+v (err %v)", rows, err)
	}
}

func peekAll(t *testing.T, session, role string) []Message {
	t.Helper()
	msgs, err := Peek(session, role)
	if err != nil {
		t.Fatalf("Peek %s: %v", role, err)
	}
	return msgs
}

// TestResponseBrakeLetsSubstantiveExchangeThrough is the negative control, the
// 2026-09-09 10:42:37 plan↔edit verify-spec shape the frequency detector also
// flagged: every round carries new content — evidence, a correction, its
// acceptance — and every response is delivered, as are two back-to-back
// progress reports differing only in a number (10/23, then 11/23).
func TestResponseBrakeLetsSubstantiveExchangeThrough(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	exchange := []struct{ from, to, text string }{
		{"plan", "edit", "Phase 2: 10/23 ticked — build 12:01 exit 0, test 12:03 exit 0"},
		{"edit", "plan", "Phase 2's review was 12:02, before the test — re-check the order"},
		{"plan", "edit", "Corrected: review 12:05 after test 12:03; Phase 2: 11/23 ticked"},
		{"edit", "plan", "Accepted — the corrected order holds"},
		{"plan", "edit", "Phase 3: 12/23 ticked"},
	}
	for _, s := range exchange {
		if err := sendResponse(session, s.from, s.to, "verify-spec", s.text, ""); err != nil {
			t.Errorf("%s→%s %q: %v", s.from, s.to, s.text, err)
		}
	}
	if n := len(peekAll(t, session, "edit")) + len(peekAll(t, session, "plan")); n != len(exchange) {
		t.Errorf("delivered %d of %d substantive responses", n, len(exchange))
	}
	for _, progress := range []string{"Phase 4: 10/23 ticked", "Phase 4: 11/23 ticked"} {
		if err := sendResponse(session, "plan", "edit", "verify-spec", progress, ""); err != nil {
			t.Errorf("back-to-back progress %q differing only in a number was refused: %v", progress, err)
		}
	}
}

// TestResponseBrakeExemptions: a response answering an open tracked request is
// delivered however similar; so is a spawn worker's identical answer to each
// loop iteration's untracked seed, while a repeat naming a request already
// answered is still braked; and a request is never braked.
func TestResponseBrakeExemptions(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	if err := sendResponse(session, "build", "edit", "build", "done", ""); err != nil {
		t.Fatal(err)
	}
	req := NewMessage("edit", "build", "request", "build", "run the build", "")
	if err := CreateTask(session, req, 600); err != nil {
		t.Fatal(err)
	}
	if err := sendResponse(session, "build", "edit", "build", "done", req.ID); err != nil {
		t.Errorf("a response answering the open tracked request %s was refused: %v", req.ID, err)
	}

	for i := 0; i < 2; i++ {
		seed := NewMessage("daemon", "spawn-abc12345", "request", "spawn-task", "implement the phase", "")
		if err := SendNoCC(session, seed); err != nil {
			t.Fatal(err)
		}
		if err := sendResponse(session, "spawn-abc12345", "daemon", "spawn-task", "phase implemented. EXIT=0", seed.ID); err != nil {
			t.Errorf("loop iteration %d's answer to its untracked seed was refused: %v", i, err)
		}
		MarkResponded(session, seed.ID, "")
	}
	answered := NewMessage("daemon", "spawn-abc12345", "request", "spawn-task", "already answered", "")
	if err := SendNoCC(session, answered); err != nil {
		t.Fatal(err)
	}
	MarkResponded(session, answered.ID, "")
	if err := sendResponse(session, "spawn-abc12345", "daemon", "spawn-task", "phase implemented. EXIT=0", answered.ID); !errors.Is(err, ErrResponseSuppressed) {
		t.Errorf("a repeat naming an answered request must be braked: %v", err)
	}

	for i := 0; i < 2; i++ {
		r := NewMessage("edit", "review", "request", "review", "review it", "")
		if err := SendNoCC(session, r); errors.Is(err, ErrResponseSuppressed) {
			t.Fatalf("request %d braked: %v", i, err)
		}
		if _, err := Receive(session, "review"); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResponseBrakeTupleKeysDoNotCollide (MUX-175 review): actions are free
// form, so `a/b` and `a_b` once shared one comparison point. Each now has its
// own: identical text on both delivers twice, and each repeat is braked on its
// own action.
func TestResponseBrakeTupleKeysDoNotCollide(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	for _, action := range []string{"a/b", "a_b"} {
		if err := sendResponse(session, "plan", "edit", action, "Acknowledged.", ""); err != nil {
			t.Errorf("first response on action %q refused: %v", action, err)
		}
	}
	for _, action := range []string{"a/b", "a_b"} {
		if err := sendResponse(session, "plan", "edit", action, "Acknowledged.", ""); !errors.Is(err, ErrResponseSuppressed) {
			t.Errorf("repeat on action %q: err %v, want ErrResponseSuppressed", action, err)
		}
	}
}

// TestResponseBrakeHoldsTheLock is the proof that the check, the delivery and
// the comparison-point update are serialized (MUX-175 review): a response send
// waits while another holder has the session's response-brake lock and
// completes once it is released, while a request — never braked — does not
// wait. In-process races are too narrow to fail without the lock, so
// TestResponseBrakeSerializesConcurrentSends pins outcomes, not the locking.
func TestResponseBrakeHoldsTheLock(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	unlock, err := lockResponseBrake(session)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sendResponse(session, "plan", "edit", "note", "Acknowledged.", "") }()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("a response send completed (%v) while the brake lock was held: check and delivery are not serialized", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := SendNoCC(session, NewMessage("edit", "review", "request", "review", "review it", "")); err != nil {
		t.Errorf("a request must not wait on the brake lock: %v", err)
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the response send failed once the lock was released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the response send never completed after the lock was released")
	}
}

// TestResponseBrakeSerializesConcurrentSends pins the outcomes of concurrent
// sends (MUX-175 review). Eight identical concurrent responses on one tuple
// deliver exactly once; eight on distinct tuples, and eight identical replies
// each answering its own open request, all deliver; and after eight different
// texts race on one tuple, the comparison point is the last one delivered.
func TestResponseBrakeSerializesConcurrentSends(t *testing.T) {
	const n = 8
	race := func(send func(i int) error) (delivered, braked int) {
		errs := make(chan error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) { defer wg.Done(); errs <- send(i) }(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			switch {
			case err == nil:
				delivered++
			case errors.Is(err, ErrResponseSuppressed):
				braked++
			default:
				t.Errorf("unexpected send error: %v", err)
			}
		}
		return delivered, braked
	}

	t.Run("same tuple delivers once", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		d, b := race(func(int) error { return sendResponse(session, "plan", "edit", "note", "Acknowledged.", "") })
		if d != 1 || b != n-1 {
			t.Errorf("delivered %d, braked %d; want 1 and %d", d, b, n-1)
		}
	})
	t.Run("distinct tuples all deliver", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		d, _ := race(func(i int) error {
			return sendResponse(session, "plan", "edit", "note-"+strconv.Itoa(i), "Acknowledged.", "")
		})
		if d != n {
			t.Errorf("delivered %d of %d distinct tuples", d, n)
		}
	})
	t.Run("replies to open requests all deliver", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		if err := sendResponse(session, "build", "edit", "build", "done", ""); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, n)
		for i := range ids {
			req := NewMessage("edit", "build", "request", "build", "run the build "+strconv.Itoa(i), "")
			if err := CreateTask(session, req, 600); err != nil {
				t.Fatal(err)
			}
			ids[i] = req.ID
		}
		d, _ := race(func(i int) error { return sendResponse(session, "build", "edit", "build", "done", ids[i]) })
		if d != n {
			t.Errorf("delivered %d of %d replies to open requests", d, n)
		}
	})
	t.Run("the comparison point is the last delivered", func(t *testing.T) {
		useTempBusDir(t)
		session := testSession(t)
		race(func(i int) error {
			return sendResponse(session, "plan", "edit", "note", "progress "+strconv.Itoa(i), "")
		})
		msgs := peekAll(t, session, "edit")
		if len(msgs) != n {
			t.Fatalf("delivered %d of %d distinct texts", len(msgs), n)
		}
		data, err := os.ReadFile(lastResponsePath(session, msgs[len(msgs)-1]))
		if err != nil {
			t.Fatal(err)
		}
		var last lastResponse
		if err := json.Unmarshal(data, &last); err != nil {
			t.Fatal(err)
		}
		if want := normalizeResponse(msgs[len(msgs)-1].Payload); last.Text != want {
			t.Errorf("comparison point %q, want the last delivered %q", last.Text, want)
		}
	})
}

// TestResponseBrakeClearsAfterWindow: the same text is delivered again once the
// window has passed since the last delivered copy — even though the pair kept
// retrying inside it, since a suppressed attempt neither replaces the
// comparison text nor restarts the window. A window of 0 turns the brake off.
func TestResponseBrakeClearsAfterWindow(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	t.Setenv(responseSuppressWindowEnv, "2")
	if err := sendResponse(session, "build", "test", "test", "Acknowledged.", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := sendResponse(session, "build", "test", "test", "Acknowledged.", ""); !errors.Is(err, ErrResponseSuppressed) {
		t.Fatalf("a retry inside the window: err %v, want ErrResponseSuppressed", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := sendResponse(session, "build", "test", "test", "Acknowledged.", ""); err != nil {
		t.Errorf("2.2 s after the delivered copy, with a retry at 1.1 s, the response must be delivered: %v", err)
	}

	t.Setenv(responseSuppressWindowEnv, "0")
	if err := sendResponse(session, "build", "test", "test", "Acknowledged.", ""); err != nil {
		t.Errorf("a window of 0 must turn the brake off: %v", err)
	}
}
