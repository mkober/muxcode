package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverSessions(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	// Create two session bus directories
	os.MkdirAll(filepath.Join(dir, "muxcode-bus-session-a", "inbox"), 0755)
	os.MkdirAll(filepath.Join(dir, "muxcode-bus-session-b", "inbox"), 0755)

	// Create a non-session directory (should be ignored)
	os.MkdirAll(filepath.Join(dir, "other-dir"), 0755)

	// Add an inbox file to session-a
	os.WriteFile(filepath.Join(dir, "muxcode-bus-session-a", "inbox", "edit.jsonl"), []byte("{}"), 0644)

	sessions, err := DiscoverSessions("", false)
	if err != nil {
		t.Fatalf("DiscoverSessions failed: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Check names
	names := make(map[string]bool)
	for _, s := range sessions {
		names[s.Name] = true
	}
	if !names["session-a"] {
		t.Error("missing session-a")
	}
	if !names["session-b"] {
		t.Error("missing session-b")
	}
}

func TestDiscoverSessions_ExcludeSelf(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	os.MkdirAll(filepath.Join(dir, "muxcode-bus-current", "inbox"), 0755)
	os.MkdirAll(filepath.Join(dir, "muxcode-bus-other", "inbox"), 0755)

	sessions, err := DiscoverSessions("current", true)
	if err != nil {
		t.Fatalf("DiscoverSessions failed: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session (self excluded), got %d", len(sessions))
	}
	if sessions[0].Name != "other" {
		t.Errorf("expected 'other', got %q", sessions[0].Name)
	}
}

func TestGetRemoteInbox(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-remote-inbox"
	Init(session, dir)

	// Empty inbox
	summary := GetRemoteInbox(session, "build")
	if summary.Count != 0 {
		t.Errorf("expected 0 messages, got %d", summary.Count)
	}
	if summary.Actionable != 0 {
		t.Errorf("expected 0 actionable, got %d", summary.Actionable)
	}

	// Write some messages
	m1 := NewMessage("edit", "build", "request", "build", "Run build", "")
	Send(session, m1)

	m2 := NewMessage("test", "build", "response", "test", "Tests passed", "")
	Send(session, m2)

	summary = GetRemoteInbox(session, "build")
	if summary.Count != 2 {
		t.Errorf("expected 2 messages, got %d", summary.Count)
	}
	if summary.Actionable != 1 {
		t.Errorf("expected 1 actionable, got %d", summary.Actionable)
	}
	if summary.Role != "build" {
		t.Errorf("expected role 'build', got %q", summary.Role)
	}
}

func TestFormatSessionList(t *testing.T) {
	sessions := []RemoteSession{
		{Name: "project-a", TmuxAlive: true, AgentCount: 5, LogSize: 2048, DaemonVersion: "v0.1.0-3-gabc1234"},
		{Name: "project-b", TmuxAlive: false, AgentCount: 3, LogSize: 1024*1024 + 512*1024},
	}

	output := FormatSessionList(sessions, "project-a")
	if output == "" {
		t.Error("expected non-empty output")
	}
	if !strings.Contains(output, "DAEMON") {
		t.Error("missing DAEMON column header")
	}
	if !strings.Contains(output, "v0.1.0-3-gabc1234") {
		t.Error("stamped session should show its daemon version")
	}
	lines := strings.Split(output, "\n")
	var unstamped string
	for _, l := range lines {
		if strings.Contains(l, "project-b") {
			unstamped = l
		}
	}
	if unstamped == "" || !strings.Contains(unstamped, "—") {
		t.Errorf("unstamped session should render a dash in the DAEMON column, got %q", unstamped)
	}
}

func TestFormatSessionList_Empty(t *testing.T) {
	output := FormatSessionList(nil, "")
	if output != "No muxcode sessions found.\n" {
		t.Errorf("unexpected output for empty list: %q", output)
	}
}

func TestFormatDurationLong(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0s"},
		{30, "30s"},
		{90, "1m30s"},
		{3661, "1h1m"},
	}

	for _, tt := range tests {
		got := formatDurationLong(tt.input)
		if got != tt.want {
			t.Errorf("formatDurationLong(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatRemoteInbox_Empty(t *testing.T) {
	summary := RemoteInboxSummary{Role: "build"}
	output := FormatRemoteInbox(summary)
	if output != "  build: empty inbox\n" {
		t.Errorf("unexpected output: %q", output)
	}
}

func TestRemoteOverview(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-overview"
	Init(session, dir)

	output := RemoteOverview(session)
	if output == "" {
		t.Error("expected non-empty overview")
	}
	// Should contain the session name
	if !strings.Contains(output, session) {
		t.Error("overview should contain session name")
	}
}
