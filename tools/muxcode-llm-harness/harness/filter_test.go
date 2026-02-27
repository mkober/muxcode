package harness

import (
	"encoding/json"
	"testing"
)

func makeToolCall(command string) ToolCall {
	args, _ := json.Marshal(map[string]string{"command": command})
	return ToolCall{
		ID:   "test",
		Type: "function",
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(args),
		},
	}
}

func TestFilter_BlocksInbox(t *testing.T) {
	f := NewFilter("commit")

	tests := []struct {
		command string
		blocked bool
	}{
		{"muxcode-agent-bus inbox", true},
		{"muxcode-agent-bus inbox --raw", true},
		{"/usr/local/bin/muxcode-agent-bus inbox", true},
		{"git status", false},
		{"muxcode-agent-bus send edit status \"done\"", false},
	}

	for _, tt := range tests {
		f.Reset()
		result := f.Check(makeToolCall(tt.command))
		if result.Blocked != tt.blocked {
			t.Errorf("Check(%q).Blocked = %v, want %v", tt.command, result.Blocked, tt.blocked)
		}
	}
}

func TestFilter_BlocksSelfSend(t *testing.T) {
	f := NewFilter("commit")

	tests := []struct {
		command string
		blocked bool
	}{
		{"muxcode-agent-bus send commit status \"test\"", true},
		{"muxcode-agent-bus send commit", true},
		{"muxcode-agent-bus send edit status \"test\"", false},
		{"muxcode-agent-bus send build build \"test\"", false},
	}

	for _, tt := range tests {
		f.Reset()
		result := f.Check(makeToolCall(tt.command))
		if result.Blocked != tt.blocked {
			t.Errorf("Check(%q).Blocked = %v, want %v", tt.command, result.Blocked, tt.blocked)
		}
	}
}

func TestFilter_BlocksRepetition(t *testing.T) {
	f := NewFilter("commit")

	tc := makeToolCall("git status")

	// First two should pass
	r1 := f.Check(tc)
	if r1.Blocked {
		t.Error("first call should not be blocked")
	}

	r2 := f.Check(tc)
	if r2.Blocked {
		t.Error("second call should not be blocked")
	}

	// Third should be blocked (MaxRepeat = 3)
	r3 := f.Check(tc)
	if !r3.Blocked {
		t.Error("third call should be blocked (repetition)")
	}
	if r3.Reason == "" {
		t.Error("blocked result should have a reason")
	}
}

func TestFilter_Reset(t *testing.T) {
	f := NewFilter("commit")

	tc := makeToolCall("git status")
	f.Check(tc)
	f.Check(tc)
	f.Check(tc)

	// After reset, should start counting from 0
	f.Reset()
	result := f.Check(tc)
	if result.Blocked {
		t.Error("after reset, first call should not be blocked")
	}
}

func TestFilter_DifferentCommandsDontConflict(t *testing.T) {
	f := NewFilter("commit")

	tc1 := makeToolCall("git status")
	tc2 := makeToolCall("git diff")

	f.Check(tc1)
	f.Check(tc1)
	f.Check(tc2)
	f.Check(tc2)

	// Third of tc1 should be blocked, but tc2 at 2 should not
	r1 := f.Check(tc1)
	if !r1.Blocked {
		t.Error("third git status should be blocked")
	}

	r2 := f.Check(tc2)
	if !r2.Blocked {
		t.Error("third git diff should be blocked")
	}
}

func TestFilter_NonBashToolsNotFiltered(t *testing.T) {
	f := NewFilter("commit")

	tc := ToolCall{
		Function: FunctionCall{
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"/etc/hosts"}`),
		},
	}

	// Non-bash tools should never be blocked by the filter
	for i := 0; i < 10; i++ {
		result := f.Check(tc)
		if result.Blocked {
			t.Errorf("non-bash tool should not be blocked on call %d", i+1)
		}
	}
}

func TestFilter_InboxBlockedReason(t *testing.T) {
	f := NewFilter("commit")
	result := f.Check(makeToolCall("muxcode-agent-bus inbox"))
	if !result.Blocked {
		t.Fatal("inbox should be blocked")
	}
	if result.Reason == "" {
		t.Error("should have corrective reason")
	}
}

func TestFilter_SelfSendBlockedReason(t *testing.T) {
	f := NewFilter("commit")
	result := f.Check(makeToolCall("muxcode-agent-bus send commit status \"test\""))
	if !result.Blocked {
		t.Fatal("self-send should be blocked")
	}
	if result.Reason == "" {
		t.Error("should have corrective reason")
	}
}

func TestCommandHash_NormalizesWhitespace(t *testing.T) {
	h1 := commandHash("git  status")
	h2 := commandHash("git status")
	if h1 != h2 {
		t.Error("should normalize whitespace in hash")
	}
}

func TestIsInboxCommand(t *testing.T) {
	if !isInboxCommand("muxcode-agent-bus inbox") {
		t.Error("should detect exact inbox command")
	}
	if !isInboxCommand("muxcode-agent-bus inbox --raw") {
		t.Error("should detect inbox with flags")
	}
	if isInboxCommand("muxcode-agent-bus send edit status") {
		t.Error("should not match send command")
	}
}

func TestIsSelfSend(t *testing.T) {
	if !isSelfSend("muxcode-agent-bus send commit status \"test\"", "commit") {
		t.Error("should detect self-send")
	}
	if isSelfSend("muxcode-agent-bus send edit status \"test\"", "commit") {
		t.Error("should not detect send to other role")
	}
}
