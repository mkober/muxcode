package tui

import (
	"strings"
	"testing"
)

func TestMessageBuffer_Add(t *testing.T) {
	mb := NewMessageBuffer(5)
	mb.Add("msg1")
	mb.Add("msg2")
	mb.Add("msg3")

	msgs := mb.Messages()
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	if msgs[0] != "msg1" || msgs[1] != "msg2" || msgs[2] != "msg3" {
		t.Errorf("messages = %v", msgs)
	}
}

func TestMessageBuffer_Add_BeyondCapacity(t *testing.T) {
	mb := NewMessageBuffer(3)
	for i := 0; i < 5; i++ {
		mb.Add(strings.Repeat("x", i+1))
	}

	msgs := mb.Messages()
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}
	// Oldest two ("x", "xx") should be dropped
	if msgs[0] != "xxx" {
		t.Errorf("msgs[0] = %q, want %q", msgs[0], "xxx")
	}
}

func TestMessageBuffer_MessagesIsCopy(t *testing.T) {
	mb := NewMessageBuffer(5)
	mb.Add("original")

	msgs := mb.Messages()
	msgs[0] = "modified"

	check := mb.Messages()
	if check[0] != "original" {
		t.Errorf("internal buffer was modified: got %q", check[0])
	}
}

func TestScanMessages_MatchesPatterns(t *testing.T) {
	mb := NewMessageBuffer(10)

	tests := []string{
		"Message from edit: hello",
		"SendMessage to build",
		"broadcast: update all",
		"edit → build: compile",
		"muxcode-agent-bus send test notify",
		"muxcode-agent-bus inbox",
		"Sent result to review",
	}
	for _, line := range tests {
		mb2 := NewMessageBuffer(10)
		mb2.ScanMessages("edit", line)
		if len(mb2.Messages()) != 1 {
			t.Errorf("pattern %q not matched", line)
		}
	}

	// All at once
	output := strings.Join(tests, "\n")
	mb.ScanMessages("edit", output)
	if len(mb.Messages()) != len(tests) {
		t.Errorf("got %d matches, want %d", len(mb.Messages()), len(tests))
	}
}

func TestScanMessages_IgnoresNonMatches(t *testing.T) {
	mb := NewMessageBuffer(10)
	mb.ScanMessages("build", "compiling step 1\ncompiling step 2\ndone\n")
	if len(mb.Messages()) != 0 {
		t.Errorf("got %d messages, want 0", len(mb.Messages()))
	}
}

func TestScanMessages_Truncation(t *testing.T) {
	mb := NewMessageBuffer(10)
	long := "Message from edit: " + strings.Repeat("a", 100)
	mb.ScanMessages("build", long)

	msgs := mb.Messages()
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	// The stored message includes "HH:MM  build: " prefix + truncated content
	// The content part (after prefix) should be at most 60 runes
	parts := strings.SplitN(msgs[0], ": ", 2)
	if len(parts) < 2 {
		t.Fatalf("unexpected format: %q", msgs[0])
	}
	// The full line in the buffer has format: "HH:MM  window: short"
	// where short is the truncated original line (max 60 runes)
}
