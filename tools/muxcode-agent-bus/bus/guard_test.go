package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeCommand(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"go build ./...", "go build ./..."},
		{"cd /tmp/foo && go build ./...", "go build ./..."},
		{"cd tools/muxcode-agent-bus && go test ./...", "go test ./..."},
		{"FOO=bar go build ./...", "go build ./..."},
		{"FOO=bar BAZ=qux go build ./...", "go build ./..."},
		{"bash -c go build ./...", "go build ./..."},
		{"go build ./... 2>&1", "go build ./..."},
		{"cd /tmp && FOO=bar go build ./... 2>&1", "go build ./..."},
		{"  go  build   ./...  ", "go build ./..."},
		{"", ""},
	}

	for _, tt := range tests {
		got := normalizeCommand(tt.input)
		if got != tt.want {
			t.Errorf("normalizeCommand(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDetectCommandLoop_Found(t *testing.T) {
	now := time.Now().Unix()
	entries := []HistoryEntry{
		{TS: now - 120, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now - 60, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
	}

	alert := DetectCommandLoop(entries, 3, 300)
	if alert == nil {
		t.Fatal("expected alert, got nil")
	}
	if alert.Type != "command" {
		t.Errorf("type = %q, want %q", alert.Type, "command")
	}
	if alert.Count != 3 {
		t.Errorf("count = %d, want 3", alert.Count)
	}
	if alert.Command != "go build ./..." {
		t.Errorf("command = %q, want %q", alert.Command, "go build ./...")
	}
}

func TestDetectCommandLoop_NormalizedMatch(t *testing.T) {
	now := time.Now().Unix()
	entries := []HistoryEntry{
		{TS: now - 60, Command: "cd /tmp && go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now - 30, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now, Command: "cd /foo && go build ./...", Outcome: "failure", ExitCode: "1"},
	}

	alert := DetectCommandLoop(entries, 3, 300)
	if alert == nil {
		t.Fatal("expected alert with normalized matching, got nil")
	}
	if alert.Count != 3 {
		t.Errorf("count = %d, want 3", alert.Count)
	}
}

func TestDetectCommandLoop_NoLoop_Success(t *testing.T) {
	now := time.Now().Unix()
	entries := []HistoryEntry{
		{TS: now - 60, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now - 30, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now, Command: "go build ./...", Outcome: "success", ExitCode: "0"},
	}

	alert := DetectCommandLoop(entries, 3, 300)
	if alert != nil {
		t.Errorf("expected no alert when last entry succeeded, got %+v", alert)
	}
}

func TestDetectCommandLoop_NoLoop_DifferentCommands(t *testing.T) {
	now := time.Now().Unix()
	entries := []HistoryEntry{
		{TS: now - 60, Command: "go test ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now - 30, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
	}

	alert := DetectCommandLoop(entries, 3, 300)
	if alert != nil {
		t.Errorf("expected no alert with different commands, got %+v", alert)
	}
}

func TestDetectCommandLoop_BelowThreshold(t *testing.T) {
	now := time.Now().Unix()
	entries := []HistoryEntry{
		{TS: now - 30, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
	}

	alert := DetectCommandLoop(entries, 3, 300)
	if alert != nil {
		t.Errorf("expected no alert below threshold, got %+v", alert)
	}
}

func TestDetectCommandLoop_OutsideWindow(t *testing.T) {
	now := time.Now().Unix()
	entries := []HistoryEntry{
		{TS: now - 600, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now - 30, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
		{TS: now, Command: "go build ./...", Outcome: "failure", ExitCode: "1"},
	}

	// Only 2 within the 300s window (the first is 600s ago)
	alert := DetectCommandLoop(entries, 3, 300)
	if alert != nil {
		t.Errorf("expected no alert outside window, got %+v", alert)
	}
}

func TestDetectCommandLoop_Empty(t *testing.T) {
	alert := DetectCommandLoop(nil, 3, 300)
	if alert != nil {
		t.Error("expected nil for empty entries")
	}
}

func TestDetectCommandLoop_EmptyCommand(t *testing.T) {
	now := time.Now().Unix()
	entries := []HistoryEntry{
		{TS: now - 60, Command: "", Outcome: "failure", ExitCode: "1"},
		{TS: now - 30, Command: "", Outcome: "failure", ExitCode: "1"},
		{TS: now, Command: "", Outcome: "failure", ExitCode: "1"},
	}

	alert := DetectCommandLoop(entries, 3, 300)
	if alert != nil {
		t.Error("expected nil for empty commands")
	}
}

func TestDetectMessageLoop_Repeated(t *testing.T) {
	now := time.Now().Unix()
	messages := []Message{
		{TS: now - 60, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now - 45, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now - 30, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now, From: "edit", To: "build", Action: "build", Type: "request"},
	}

	alert := DetectMessageLoop(messages, "build", 4, 300)
	if alert == nil {
		t.Fatal("expected alert for repeated messages, got nil")
	}
	if alert.Type != "message" {
		t.Errorf("type = %q, want %q", alert.Type, "message")
	}
	if alert.Count != 4 {
		t.Errorf("count = %d, want 4", alert.Count)
	}
	if alert.Peer != "edit" {
		t.Errorf("peer = %q, want %q", alert.Peer, "edit")
	}
	if alert.Action != "build" {
		t.Errorf("action = %q, want %q", alert.Action, "build")
	}
}

func TestDetectMessageLoop_PingPong(t *testing.T) {
	now := time.Now().Unix()
	// True ping-pong: agents sending requests back and forth
	messages := []Message{
		{TS: now - 60, From: "build", To: "test", Action: "test", Type: "request"},
		{TS: now - 45, From: "test", To: "build", Action: "test", Type: "request"},
		{TS: now - 30, From: "build", To: "test", Action: "test", Type: "request"},
		{TS: now, From: "test", To: "build", Action: "test", Type: "request"},
	}

	alert := DetectMessageLoop(messages, "build", 4, 300)
	if alert == nil {
		t.Fatal("expected alert for ping-pong pattern, got nil")
	}
	if alert.Count < 4 {
		t.Errorf("count = %d, want >= 4", alert.Count)
	}
}

func TestDetectMessageLoop_ChainTrafficIgnored(t *testing.T) {
	// Simulate two build→test→review chain cycles within the window.
	// These are response/event messages that should NOT trigger loop detection.
	now := time.Now().Unix()
	messages := []Message{
		// Cycle 1
		{TS: now - 120, From: "build", To: "test", Action: "test", Type: "request"},
		{TS: now - 110, From: "test", To: "review", Action: "review", Type: "request"},
		{TS: now - 100, From: "review", To: "test", Action: "review-complete", Type: "response"},
		{TS: now - 95, From: "review", To: "edit", Action: "review-complete", Type: "response"},
		{TS: now - 90, From: "test", To: "analyze", Action: "notify", Type: "event"},
		{TS: now - 85, From: "watcher", To: "analyze", Action: "analyze", Type: "event"},
		// Cycle 2
		{TS: now - 60, From: "build", To: "test", Action: "test", Type: "request"},
		{TS: now - 50, From: "test", To: "review", Action: "review", Type: "request"},
		{TS: now - 40, From: "review", To: "test", Action: "review-complete", Type: "response"},
		{TS: now - 35, From: "review", To: "edit", Action: "review-complete", Type: "response"},
		{TS: now - 30, From: "test", To: "analyze", Action: "notify", Type: "event"},
		{TS: now - 25, From: "watcher", To: "analyze", Action: "analyze", Type: "event"},
	}

	// None of these roles should trigger a loop alert — the responses and
	// events are expected chain traffic, not actual loops.
	for _, role := range []string{"test", "review", "analyze", "edit", "watcher"} {
		alert := DetectMessageLoop(messages, role, 4, 300)
		if alert != nil {
			t.Errorf("role %q: unexpected alert for chain traffic: %s", role, alert.Message)
		}
	}
}

func TestDetectMessageLoop_WatcherTrafficIgnored(t *testing.T) {
	// Watcher-originated messages repeat during active editing (file-change events,
	// loop alerts, compaction alerts). These should NOT trigger loop detection.
	now := time.Now().Unix()
	messages := []Message{
		{TS: now - 120, From: "watcher", To: "analyze", Action: "analyze", Type: "request"},
		{TS: now - 90, From: "watcher", To: "analyze", Action: "analyze", Type: "request"},
		{TS: now - 60, From: "watcher", To: "analyze", Action: "analyze", Type: "request"},
		{TS: now - 30, From: "watcher", To: "analyze", Action: "analyze", Type: "request"},
		{TS: now, From: "watcher", To: "analyze", Action: "analyze", Type: "request"},
	}

	// Neither analyze nor watcher should trigger a loop alert
	for _, role := range []string{"analyze", "watcher"} {
		alert := DetectMessageLoop(messages, role, 4, 300)
		if alert != nil {
			t.Errorf("role %q: unexpected alert for watcher traffic: %s", role, alert.Message)
		}
	}
}

func TestDetectMessageLoop_NoLoop(t *testing.T) {
	now := time.Now().Unix()
	messages := []Message{
		{TS: now - 60, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now - 30, From: "build", To: "edit", Action: "build", Type: "response"},
		{TS: now, From: "edit", To: "test", Action: "test", Type: "request"},
	}

	alert := DetectMessageLoop(messages, "build", 4, 300)
	if alert != nil {
		t.Errorf("expected no alert for varied messages, got %+v", alert)
	}
}

func TestDetectMessageLoop_BelowThreshold(t *testing.T) {
	now := time.Now().Unix()
	messages := []Message{
		{TS: now - 60, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now - 30, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now, From: "edit", To: "build", Action: "build", Type: "request"},
	}

	alert := DetectMessageLoop(messages, "build", 4, 300)
	if alert != nil {
		t.Errorf("expected no alert below threshold, got %+v", alert)
	}
}

func TestDetectMessageLoop_Empty(t *testing.T) {
	alert := DetectMessageLoop(nil, "build", 4, 300)
	if alert != nil {
		t.Error("expected nil for empty messages")
	}
}

func TestDetectMessageLoop_OutsideWindow(t *testing.T) {
	now := time.Now().Unix()
	messages := []Message{
		{TS: now - 600, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now - 500, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now - 30, From: "edit", To: "build", Action: "build", Type: "request"},
		{TS: now, From: "edit", To: "build", Action: "build", Type: "request"},
	}

	alert := DetectMessageLoop(messages, "build", 4, 300)
	if alert != nil {
		t.Errorf("expected no alert outside window, got %+v", alert)
	}
}

func TestReadHistory(t *testing.T) {
	session := testSession(t)
	histPath := HistoryPath(session, "build")

	// Write some history entries
	now := time.Now().Unix()
	entries := []HistoryEntry{
		{TS: now - 60, Command: "go build ./...", Summary: "build", ExitCode: "1", Outcome: "failure"},
		{TS: now - 30, Command: "go build ./...", Summary: "build", ExitCode: "1", Outcome: "failure"},
		{TS: now, Command: "go build ./...", Summary: "build", ExitCode: "0", Outcome: "success"},
	}

	f, err := os.Create(histPath)
	if err != nil {
		t.Fatalf("creating history file: %v", err)
	}
	for _, e := range entries {
		data, _ := json.Marshal(e)
		f.Write(append(data, '\n'))
	}
	f.Close()

	got := ReadHistory(session, "build", 10)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	if got[0].Outcome != "failure" {
		t.Errorf("first entry outcome = %q, want %q", got[0].Outcome, "failure")
	}
	if got[2].Outcome != "success" {
		t.Errorf("last entry outcome = %q, want %q", got[2].Outcome, "success")
	}
}

func TestReadHistory_Limit(t *testing.T) {
	session := testSession(t)
	histPath := HistoryPath(session, "build")

	now := time.Now().Unix()
	f, err := os.Create(histPath)
	if err != nil {
		t.Fatalf("creating history file: %v", err)
	}
	for i := 0; i < 10; i++ {
		e := HistoryEntry{TS: now - int64(10-i), Command: "cmd", Summary: "s", ExitCode: "0", Outcome: "success"}
		data, _ := json.Marshal(e)
		f.Write(append(data, '\n'))
	}
	f.Close()

	got := ReadHistory(session, "build", 3)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
}

func TestReadHistory_MissingFile(t *testing.T) {
	session := testSession(t)

	got := ReadHistory(session, "nonexistent", 10)
	if got != nil {
		t.Errorf("expected nil for missing file, got %d entries", len(got))
	}
}

func TestCheckLoops_Integration(t *testing.T) {
	session := testSession(t)

	// Write history entries with a command loop
	histPath := HistoryPath(session, "build")
	now := time.Now().Unix()
	f, err := os.Create(histPath)
	if err != nil {
		t.Fatalf("creating history file: %v", err)
	}
	for i := 0; i < 3; i++ {
		e := HistoryEntry{TS: now - int64(60*(2-i)), Command: "go build ./...", Summary: "build failed", ExitCode: "1", Outcome: "failure"}
		data, _ := json.Marshal(e)
		f.Write(append(data, '\n'))
	}
	f.Close()

	alerts := CheckLoops(session, "build")
	if len(alerts) == 0 {
		t.Fatal("expected at least one alert")
	}

	found := false
	for _, a := range alerts {
		if a.Type == "command" && a.Role == "build" {
			found = true
		}
	}
	if !found {
		t.Error("expected a command loop alert for build")
	}
}

func TestCheckAllLoops(t *testing.T) {
	session := testSession(t)

	// No history = no alerts
	alerts := CheckAllLoops(session)
	if len(alerts) != 0 {
		t.Errorf("expected no alerts for clean session, got %d", len(alerts))
	}
}

func TestFormatAlerts_NoAlerts(t *testing.T) {
	out := FormatAlerts(nil)
	if !strings.Contains(out, "No loops detected") {
		t.Errorf("expected 'No loops detected', got %q", out)
	}
}

func TestFormatAlerts_CommandLoop(t *testing.T) {
	alerts := []LoopAlert{
		{Role: "build", Type: "command", Count: 3, Command: "go build ./...", Window: 120, Message: "go build ./... failed 3x in 2m"},
	}

	out := FormatAlerts(alerts)
	if !strings.Contains(out, "LOOP DETECTED: build") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "command") {
		t.Error("missing type")
	}
	if !strings.Contains(out, "go build") {
		t.Error("missing command")
	}
}

func TestFormatAlerts_MessageLoop(t *testing.T) {
	alerts := []LoopAlert{
		{Role: "test", Type: "message", Count: 4, Peer: "build", Action: "test", Window: 180, Message: "ping-pong"},
	}

	out := FormatAlerts(alerts)
	if !strings.Contains(out, "LOOP DETECTED: test") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "message") {
		t.Error("missing type")
	}
	if !strings.Contains(out, "build") {
		t.Error("missing peer")
	}
}

func TestFormatAlertsJSON(t *testing.T) {
	alerts := []LoopAlert{
		{Role: "build", Type: "command", Count: 3, Command: "go build ./...", Window: 120},
	}

	out, err := FormatAlertsJSON(alerts)
	if err != nil {
		t.Fatalf("FormatAlertsJSON: %v", err)
	}

	var parsed []LoopAlert
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(parsed) != 1 {
		t.Errorf("got %d entries, want 1", len(parsed))
	}
	if parsed[0].Role != "build" {
		t.Errorf("role = %q, want %q", parsed[0].Role, "build")
	}
}

func TestFormatAlertsJSON_Empty(t *testing.T) {
	out, err := FormatAlertsJSON(nil)
	if err != nil {
		t.Fatalf("FormatAlertsJSON: %v", err)
	}
	if !strings.Contains(out, "[]") {
		t.Errorf("expected empty array, got %q", out)
	}
}

func TestAlertKey(t *testing.T) {
	cmd := LoopAlert{Role: "build", Type: "command", Command: "go build ./..."}
	if got := AlertKey(cmd); got != "build:command:go build ./..." {
		t.Errorf("AlertKey(cmd) = %q", got)
	}

	msg := LoopAlert{Role: "test", Type: "message", Peer: "build", Action: "test"}
	if got := AlertKey(msg); got != "test:message:build:test" {
		t.Errorf("AlertKey(msg) = %q", got)
	}
}

func TestFilterNewAlerts(t *testing.T) {
	alerts := []LoopAlert{
		{Role: "build", Type: "command", Command: "go build ./..."},
		{Role: "test", Type: "message", Peer: "build", Action: "test"},
	}

	lastSeen := make(map[string]int64)

	// First call: all alerts are new
	fresh := FilterNewAlerts(alerts, lastSeen, 300)
	if len(fresh) != 2 {
		t.Errorf("first call: got %d fresh, want 2", len(fresh))
	}

	// Second call immediately: all should be filtered
	fresh = FilterNewAlerts(alerts, lastSeen, 300)
	if len(fresh) != 0 {
		t.Errorf("second call: got %d fresh, want 0", len(fresh))
	}
}

func TestFilterNewAlerts_CooldownExpired(t *testing.T) {
	alerts := []LoopAlert{
		{Role: "build", Type: "command", Command: "go build ./..."},
	}

	lastSeen := map[string]int64{
		"build:command:go build ./...": time.Now().Unix() - 400, // 400s ago, cooldown is 300s
	}

	fresh := FilterNewAlerts(alerts, lastSeen, 300)
	if len(fresh) != 1 {
		t.Errorf("got %d fresh, want 1 (cooldown expired)", len(fresh))
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		secs int64
		want string
	}{
		{0, "0s"},
		{30, "30s"},
		{60, "1m"},
		{90, "1m30s"},
		{120, "2m"},
		{300, "5m"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.secs)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}

func TestReadHistory_RealHistoryFile(t *testing.T) {
	// Test with the actual format written by cmd/log.go
	session := testSession(t)
	histPath := HistoryPath(session, "build")

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(histPath), 0755); err != nil {
		t.Fatal(err)
	}

	// Write entries in the exact format cmd/log.go uses (map[string]interface{})
	entries := []map[string]interface{}{
		{"ts": time.Now().Unix(), "summary": "build failed", "exit_code": "1", "command": "go build ./...", "output": "error: ...", "outcome": "failure"},
		{"ts": time.Now().Unix(), "summary": "build ok", "exit_code": "0", "command": "go build ./...", "output": "", "outcome": "success"},
	}

	f, err := os.Create(histPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := json.Marshal(e)
		f.Write(append(data, '\n'))
	}
	f.Close()

	got := ReadHistory(session, "build", 10)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].Outcome != "failure" {
		t.Errorf("first entry outcome = %q, want %q", got[0].Outcome, "failure")
	}
	if got[0].Command != "go build ./..." {
		t.Errorf("first entry command = %q, want %q", got[0].Command, "go build ./...")
	}
}
