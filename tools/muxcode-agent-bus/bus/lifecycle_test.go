package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogLifecycle(t *testing.T) {
	// Use a temp dir for logs
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	// Override HOME so LifecycleLogDir() resolves to temp
	t.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create the logs directory structure
	logDir := filepath.Join(tmpDir, ".config", "muxcode", "logs")
	os.MkdirAll(logDir, 0755)

	session := "test-session"
	LogLifecycle(session, "info", "launcher", "session-start", "Project: /tmp/test")

	// Read back
	entries, err := ReadLifecycleLog(session)
	if err != nil {
		t.Fatalf("ReadLifecycleLog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Level != "info" {
		t.Errorf("level = %q, want %q", e.Level, "info")
	}
	if e.Source != "launcher" {
		t.Errorf("source = %q, want %q", e.Source, "launcher")
	}
	if e.Event != "session-start" {
		t.Errorf("event = %q, want %q", e.Event, "session-start")
	}
	if e.Session != session {
		t.Errorf("session = %q, want %q", e.Session, session)
	}
	if e.Detail != "Project: /tmp/test" {
		t.Errorf("detail = %q, want %q", e.Detail, "Project: /tmp/test")
	}
}

func TestLogLifecycleWithPID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	session := "test-pid"
	LogLifecycleWithPID(session, "info", "launcher", "watcher-start", "started", 12345)

	entries, err := ReadLifecycleLog(session)
	if err != nil {
		t.Fatalf("ReadLifecycleLog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].PID != 12345 {
		t.Errorf("pid = %d, want 12345", entries[0].PID)
	}
}

func TestFilterLifecycleLog(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	session := "test-filter"
	LogLifecycle(session, "info", "launcher", "session-start", "")
	LogLifecycle(session, "warn", "watcher", "loop-detected", "edit")
	LogLifecycle(session, "info", "watcher", "inbox-notify", "build")
	LogLifecycle(session, "error", "monitor", "stale-detected", "age=35s")

	// Filter by source
	entries, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Source: "watcher"})
	if len(entries) != 2 {
		t.Errorf("source=watcher: expected 2 entries, got %d", len(entries))
	}

	// Filter by level
	entries, _ = FilterLifecycleLog(session, LifecycleFilterOpts{Level: "warn"})
	if len(entries) != 1 {
		t.Errorf("level=warn: expected 1 entry, got %d", len(entries))
	}

	// Filter by event
	entries, _ = FilterLifecycleLog(session, LifecycleFilterOpts{Event: "inbox-notify"})
	if len(entries) != 1 {
		t.Errorf("event=inbox-notify: expected 1 entry, got %d", len(entries))
	}

	// Filter with limit
	entries, _ = FilterLifecycleLog(session, LifecycleFilterOpts{Limit: 2})
	if len(entries) != 2 {
		t.Errorf("limit=2: expected 2 entries, got %d", len(entries))
	}
	// Should be the last 2
	if entries[0].Source != "watcher" || entries[1].Source != "monitor" {
		t.Errorf("limit=2: expected last 2 entries, got sources %s, %s", entries[0].Source, entries[1].Source)
	}
}

func TestFilterLifecycleLogSince(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	session := "test-since"
	now := time.Now().Unix()

	// Write entries manually with controlled timestamps
	logPath := LifecycleLogPath(session)
	os.MkdirAll(filepath.Dir(logPath), 0755)
	f, _ := os.Create(logPath)

	old := LifecycleEntry{TS: now - 3600, Level: "info", Source: "launcher", Session: session, Event: "old-event"}
	recent := LifecycleEntry{TS: now - 60, Level: "info", Source: "launcher", Session: session, Event: "recent-event"}

	d1, _ := json.Marshal(old)
	d2, _ := json.Marshal(recent)
	f.Write(append(d1, '\n'))
	f.Write(append(d2, '\n'))
	f.Close()

	// Filter since 30 minutes ago
	entries, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Since: now - 1800})
	if len(entries) != 1 {
		t.Errorf("since=30m: expected 1 entry, got %d", len(entries))
	}
	if len(entries) > 0 && entries[0].Event != "recent-event" {
		t.Errorf("expected recent-event, got %s", entries[0].Event)
	}
}

func TestRotateLifecycleLog(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_MAX", "5")

	session := "test-rotate"

	// Write 8 entries
	for i := 0; i < 8; i++ {
		LogLifecycle(session, "info", "test", "event", "")
	}

	entries, _ := ReadLifecycleLog(session)
	if len(entries) != 5 {
		t.Errorf("after rotation: expected 5 entries, got %d", len(entries))
	}
}

func TestListLifecycleSessions(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	LogLifecycle("session-a", "info", "test", "event", "")
	LogLifecycle("session-b", "info", "test", "event", "")

	sessions, err := ListLifecycleSessions()
	if err != nil {
		t.Fatalf("ListLifecycleSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestPurgeLifecycleLogs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create a log file and backdate it
	LogLifecycle("old-session", "info", "test", "event", "")
	logPath := LifecycleLogPath("old-session")
	oldTime := time.Now().AddDate(0, 0, -31)
	os.Chtimes(logPath, oldTime, oldTime)

	// Create a recent log
	LogLifecycle("new-session", "info", "test", "event", "")

	removed, err := PurgeLifecycleLogs(30)
	if err != nil {
		t.Fatalf("PurgeLifecycleLogs: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// New session should still exist
	sessions, _ := ListLifecycleSessions()
	if len(sessions) != 1 || sessions[0] != "new-session" {
		t.Errorf("expected only new-session remaining, got %v", sessions)
	}
}

func TestFormatLifecycleEntry(t *testing.T) {
	e := LifecycleEntry{
		TS:      1710244800,
		Level:   "info",
		Source:  "launcher",
		Session: "muxcode",
		Event:   "watcher-start",
		PID:     12345,
		Detail:  "nohup muxcode-agent-bus watch muxcode",
	}

	line := FormatLifecycleEntry(e)
	if line == "" {
		t.Error("expected non-empty formatted line")
	}
	// Should contain all key pieces
	for _, want := range []string{"info", "launcher", "watcher-start", "pid:12345"} {
		if !strings.Contains(line, want) {
			t.Errorf("formatted line missing %q: %s", want, line)
		}
	}
}

func TestReadLifecycleLogNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	entries, err := ReadLifecycleLog("nonexistent")
	if err != nil {
		t.Errorf("expected nil error for missing log, got %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}
