package bus

import (
	"regexp"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	orig := Message{
		ID:      "123-edit-abcd1234",
		TS:      1700000000,
		From:    "edit",
		To:      "build",
		Type:    "request",
		Action:  "compile",
		Payload: "please build",
		ReplyTo: "999-build-deadbeef",
	}

	data, err := EncodeMessage(orig)
	if err != nil {
		t.Fatalf("EncodeMessage: %v", err)
	}

	got, err := DecodeMessage(data)
	if err != nil {
		t.Fatalf("DecodeMessage: %v", err)
	}

	if got != orig {
		t.Errorf("round-trip mismatch:\n  got  %+v\n  want %+v", got, orig)
	}
}

func TestDecodeMessage_Malformed(t *testing.T) {
	_, err := DecodeMessage([]byte(`{not valid json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestDecodeMessage_EmptyFields(t *testing.T) {
	input := `{"id":"1","ts":0,"from":"","to":"","type":"","action":"","payload":"","reply_to":""}`
	m, err := DecodeMessage([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ID != "1" {
		t.Errorf("ID = %q, want %q", m.ID, "1")
	}
	if m.ReplyTo != "" {
		t.Errorf("ReplyTo = %q, want empty", m.ReplyTo)
	}
}

func TestFormatMessage(t *testing.T) {
	m := Message{
		ID:      "100-edit-aabb",
		TS:      1700000000,
		From:    "edit",
		To:      "build",
		Type:    "request",
		Action:  "compile",
		Payload: "run build",
	}

	out := FormatMessage(m)

	for _, want := range []string{
		"Message from edit",
		"Action: compile",
		"Content: run build",
		"muxcoder-agent-bus send edit",
		"--reply-to 100-edit-aabb",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatMessage missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatMessage_WithReplyTo(t *testing.T) {
	m := Message{
		ID:      "100-edit-aabb",
		TS:      1700000000,
		From:    "edit",
		To:      "build",
		Type:    "response",
		Action:  "result",
		Payload: "done",
		ReplyTo: "99-build-ccdd",
	}

	out := FormatMessage(m)
	if !strings.Contains(out, "Reply to: 99-build-ccdd") {
		t.Errorf("expected Reply to line, got:\n%s", out)
	}
}

func TestFormatMessage_WithoutReplyTo(t *testing.T) {
	m := Message{
		ID:      "100-edit-aabb",
		TS:      1700000000,
		From:    "edit",
		To:      "build",
		Type:    "request",
		Action:  "compile",
		Payload: "build it",
	}

	out := FormatMessage(m)
	if strings.Contains(out, "Reply to:") {
		t.Errorf("unexpected Reply to line in:\n%s", out)
	}
}

func TestNewMsgID_Format(t *testing.T) {
	id := NewMsgID("edit")
	// Format: {digits}-edit-{8hex}
	re := regexp.MustCompile(`^\d+-edit-[0-9a-f]{8}$`)
	if !re.MatchString(id) {
		t.Errorf("NewMsgID(%q) = %q, does not match expected format", "edit", id)
	}
}

func TestNewMsgID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := NewMsgID("test")
		if ids[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		ids[id] = true
	}
}
