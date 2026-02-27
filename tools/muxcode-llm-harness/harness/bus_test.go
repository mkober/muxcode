package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHasMessages_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "test.jsonl")
	os.WriteFile(inbox, []byte(""), 0644)

	bc := &BusClient{}
	if bc.HasMessages(inbox) {
		t.Error("empty file should report no messages")
	}
}

func TestHasMessages_WithContent(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "test.jsonl")
	os.WriteFile(inbox, []byte(`{"id":"1"}`+"\n"), 0644)

	bc := &BusClient{}
	if !bc.HasMessages(inbox) {
		t.Error("non-empty file should report messages")
	}
}

func TestHasMessages_NoFile(t *testing.T) {
	bc := &BusClient{}
	if bc.HasMessages("/nonexistent/path.jsonl") {
		t.Error("missing file should report no messages")
	}
}

func TestLogHistory(t *testing.T) {
	dir := t.TempDir()
	bc := &BusClient{
		BusDir: dir,
		Role:   "test",
	}

	err := bc.LogHistory("git status", "On branch main", "0", "success")
	if err != nil {
		t.Fatalf("LogHistory: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "test-history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	var entry map[string]interface{}
	if err := json.Unmarshal(data[:len(data)-1], &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if entry["command"] != "git status" {
		t.Errorf("command = %q, want 'git status'", entry["command"])
	}
	if entry["outcome"] != "success" {
		t.Errorf("outcome = %q, want 'success'", entry["outcome"])
	}
	if entry["exit_code"] != "0" {
		t.Errorf("exit_code = %q, want '0'", entry["exit_code"])
	}
}

func TestLogHistory_TruncatesOutput(t *testing.T) {
	dir := t.TempDir()
	bc := &BusClient{
		BusDir: dir,
		Role:   "test",
	}

	longOutput := make([]byte, 5000)
	for i := range longOutput {
		longOutput[i] = 'x'
	}

	err := bc.LogHistory("cmd", string(longOutput), "0", "success")
	if err != nil {
		t.Fatalf("LogHistory: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "test-history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	var entry map[string]interface{}
	json.Unmarshal(data[:len(data)-1], &entry)

	output := entry["output"].(string)
	if len(output) > 2100 { // 2000 + "..." + margin
		t.Errorf("output length = %d, should be truncated to ~2000", len(output))
	}
}
