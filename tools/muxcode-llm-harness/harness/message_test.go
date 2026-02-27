package harness

import (
	"strings"
	"testing"
)

func TestParseMessages_Single(t *testing.T) {
	input := `{"id":"123","ts":1000,"from":"edit","to":"commit","type":"request","action":"commit","payload":"Commit changes","reply_to":""}`
	msgs, err := ParseMessages(input)
	if err != nil {
		t.Fatalf("ParseMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if msgs[0].From != "edit" {
		t.Errorf("From = %q, want edit", msgs[0].From)
	}
	if msgs[0].Action != "commit" {
		t.Errorf("Action = %q, want commit", msgs[0].Action)
	}
	if msgs[0].Payload != "Commit changes" {
		t.Errorf("Payload = %q, want 'Commit changes'", msgs[0].Payload)
	}
}

func TestParseMessages_Multiple(t *testing.T) {
	input := `{"id":"1","ts":1000,"from":"edit","to":"commit","type":"request","action":"status","payload":"Show status","reply_to":""}
{"id":"2","ts":1001,"from":"build","to":"commit","type":"request","action":"commit","payload":"Commit now","reply_to":""}`
	msgs, err := ParseMessages(input)
	if err != nil {
		t.Fatalf("ParseMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].From != "edit" {
		t.Errorf("msgs[0].From = %q, want edit", msgs[0].From)
	}
	if msgs[1].From != "build" {
		t.Errorf("msgs[1].From = %q, want build", msgs[1].From)
	}
}

func TestParseMessages_EmptyInput(t *testing.T) {
	msgs, err := ParseMessages("")
	if err != nil {
		t.Fatalf("ParseMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("got %d messages, want 0", len(msgs))
	}
}

func TestParseMessages_BlankLines(t *testing.T) {
	input := `
{"id":"1","ts":1000,"from":"edit","to":"commit","type":"request","action":"status","payload":"test","reply_to":""}

`
	msgs, err := ParseMessages(input)
	if err != nil {
		t.Fatalf("ParseMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
}

func TestParseMessages_InvalidJSON(t *testing.T) {
	input := `not valid json`
	_, err := ParseMessages(input)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFormatTask_Single(t *testing.T) {
	msgs := []Message{
		{From: "edit", Action: "commit", Payload: "Stage and commit all changes"},
	}
	result := FormatTask(msgs)
	if !strings.Contains(result, "## Task\n") {
		t.Error("should contain '## Task' without number for single message")
	}
	if !strings.Contains(result, "**Action**: commit") {
		t.Error("should contain action")
	}
	if !strings.Contains(result, "**From**: edit") {
		t.Error("should contain from")
	}
	if !strings.Contains(result, "Stage and commit all changes") {
		t.Error("should contain payload")
	}
	if !strings.Contains(result, "Do NOT run") {
		t.Error("should contain inbox warning")
	}
}

func TestFormatTask_Multiple(t *testing.T) {
	msgs := []Message{
		{From: "edit", Action: "status", Payload: "Show git status"},
		{From: "build", Action: "commit", Payload: "Commit now"},
	}
	result := FormatTask(msgs)
	if !strings.Contains(result, "## Task 1") {
		t.Error("should contain numbered tasks")
	}
	if !strings.Contains(result, "## Task 2") {
		t.Error("should contain task 2")
	}
}

func TestFormatTask_Empty(t *testing.T) {
	result := FormatTask(nil)
	if result != "" {
		t.Errorf("empty messages should return empty string, got %q", result)
	}
}
