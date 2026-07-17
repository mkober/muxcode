package bus

import (
	"os"
	"strings"
	"testing"
)

// oversizedPayload returns a payload comfortably larger than
// bufio.MaxScanTokenSize (64KB) — the cap that used to abort inbox parsing.
// A build agent replying with a full build log routinely exceeds this.
func oversizedPayload() string {
	return strings.Repeat("build log line: compiling package foo/bar/baz\n", 3000)
}

// TestReceive_OversizedMessage is the regression test for the inbox data-loss
// bug: a message larger than bufio.Scanner's 64KB token cap caused
// readMessages to return ErrTooLong, and Receive removed the .consuming file
// "regardless of read errors" — destroying the only copy. The agent saw
// "Error reading inbox" and the message was gone for good.
func TestReceive_OversizedMessage(t *testing.T) {
	session := testSession(t)

	payload := oversizedPayload()
	if len(payload) <= 64*1024 {
		t.Fatalf("test payload too small to exercise the bug: %d bytes", len(payload))
	}

	msg := NewMessage("build", "edit", "response", "build", payload, "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs, err := Receive(session, "edit")
	if err != nil {
		t.Fatalf("Receive returned error for oversized message: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 (oversized message was dropped)", len(msgs))
	}
	if msgs[0].Payload != payload {
		t.Errorf("payload corrupted: got %d bytes, want %d", len(msgs[0].Payload), len(payload))
	}
}

// TestReceive_OversizedMessageDoesNotDropOthers verifies that an oversized
// message does not take unrelated messages down with it. bufio.Scanner aborts
// the whole scan on ErrTooLong, so every message after the oversized line was
// lost too — and Receive then deleted all of them.
func TestReceive_OversizedMessageDoesNotDropOthers(t *testing.T) {
	session := testSession(t)

	before := NewMessage("test", "edit", "response", "test", "small before", "")
	big := NewMessage("build", "edit", "response", "build", oversizedPayload(), "")
	after := NewMessage("review", "edit", "response", "review", "small after", "")

	for _, m := range []Message{before, big, after} {
		if err := Send(session, m); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	msgs, err := Receive(session, "edit")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 — oversized message truncated the batch", len(msgs))
	}
	if msgs[0].Payload != "small before" || msgs[2].Payload != "small after" {
		t.Errorf("order or content mismatch: %q ... %q", msgs[0].Payload, msgs[2].Payload)
	}
}

// TestReceiveFromFunc_OversizedMessage covers the --wait delegation path.
// This one was worse than Receive: on a read error it removed .consuming and
// returned nil, destroying both matched and unmatched messages — so an
// oversized build reply would also wipe out pending messages from other agents.
func TestReceiveFromFunc_OversizedMessage(t *testing.T) {
	session := testSession(t)

	big := NewMessage("build", "edit", "response", "build", oversizedPayload(), "")
	other := NewMessage("review", "edit", "request", "review", "unrelated", "")
	for _, m := range []Message{big, other} {
		if err := Send(session, m); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	matched, err := ReceiveFromFunc(session, "edit", func(f string) bool { return f == "build" })
	if err != nil {
		t.Fatalf("ReceiveFromFunc: %v", err)
	}
	if len(matched) != 1 {
		t.Fatalf("got %d matched, want 1", len(matched))
	}

	// The unmatched message must still be in the inbox, not destroyed.
	rest, err := Receive(session, "edit")
	if err != nil {
		t.Fatalf("Receive rest: %v", err)
	}
	if len(rest) != 1 || rest[0].Payload != "unrelated" {
		t.Errorf("unmatched message lost: got %+v", rest)
	}
}

// TestReadLogHistory_OversizedMessage covers the recovery path. log.jsonl is
// append-only and is what an agent falls back to when a message is lost — but
// readLogForRole used the same 64KB scanner and silently truncated at the
// oversized line without even checking scanner.Err(), hiding the message and
// everything logged after it.
func TestReadLogHistory_OversizedMessage(t *testing.T) {
	session := testSession(t)

	big := NewMessage("build", "edit", "response", "build", oversizedPayload(), "")
	after := NewMessage("test", "edit", "response", "test", "logged after", "")
	for _, m := range []Message{big, after} {
		if err := Send(session, m); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	hist := ReadLogHistory(session, "edit", 10)
	if len(hist) < 2 {
		t.Fatalf("history truncated at oversized message: got %d entries, want >= 2", len(hist))
	}

	var sawAfter bool
	for _, m := range hist {
		if m.Payload == "logged after" {
			sawAfter = true
		}
	}
	if !sawAfter {
		t.Error("message logged after the oversized one is missing from history")
	}
}

// TestInboxCount_OversizedMessage guards against silent undercounting, which
// would make an inbox with pending work look emptier than it is.
func TestInboxCount_OversizedMessage(t *testing.T) {
	session := testSession(t)

	big := NewMessage("build", "edit", "response", "build", oversizedPayload(), "")
	small := NewMessage("test", "edit", "response", "test", "small", "")
	for _, m := range []Message{big, small} {
		if err := Send(session, m); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	if got := InboxCount(session, "edit"); got != 2 {
		t.Errorf("InboxCount = %d, want 2", got)
	}
}

// TestRestoreConsuming_PutsMessagesBack verifies the safety net directly: if a
// read fails after the inbox has been renamed to .consuming, the messages must
// end up back in the inbox rather than being deleted.
func TestRestoreConsuming_PutsMessagesBack(t *testing.T) {
	session := testSession(t)

	if err := Send(session, NewMessage("build", "edit", "response", "build", "recover me", "")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	inbox := InboxPath(session, "edit")
	consuming := inbox + ".consuming"

	// Simulate Receive's rename, then a failed read.
	if err := os.Rename(inbox, consuming); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := touchFile(inbox); err != nil {
		t.Fatalf("touchFile: %v", err)
	}

	restoreConsuming(inbox, consuming)

	if _, err := os.Stat(consuming); !os.IsNotExist(err) {
		t.Error(".consuming should be removed after a successful restore")
	}

	msgs, err := Receive(session, "edit")
	if err != nil {
		t.Fatalf("Receive after restore: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Payload != "recover me" {
		t.Fatalf("message not restored: got %+v", msgs)
	}
}

// TestRestoreConsuming_PreservesNewArrivals verifies that messages which
// arrive during a failed read are kept, and ordered after the restored ones.
func TestRestoreConsuming_PreservesNewArrivals(t *testing.T) {
	session := testSession(t)

	if err := Send(session, NewMessage("build", "edit", "response", "build", "original", "")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	inbox := InboxPath(session, "edit")
	consuming := inbox + ".consuming"

	if err := os.Rename(inbox, consuming); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := touchFile(inbox); err != nil {
		t.Fatalf("touchFile: %v", err)
	}

	// A message lands while the read is failing.
	if err := Send(session, NewMessage("test", "edit", "response", "test", "arrived during read", "")); err != nil {
		t.Fatalf("Send during read: %v", err)
	}

	restoreConsuming(inbox, consuming)

	msgs, err := Receive(session, "edit")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Payload != "original" || msgs[1].Payload != "arrived during read" {
		t.Errorf("wrong order: %q then %q", msgs[0].Payload, msgs[1].Payload)
	}
}
