package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitSessionMeta_Creates(t *testing.T) {
	busDir := t.TempDir()
	session := "test-session"
	t.Setenv("BUS_SESSION", session)

	// Point BusDir to our temp dir by creating session dir manually
	sessDir := filepath.Join(busDir, "session")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Override BusDir by using a known session and pre-creating the path
	// We'll use the real BusDir function, so set up the actual path
	realBusDir := BusDir(session)
	realSessDir := SessionDir(session)
	if err := os.MkdirAll(realSessDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer os.RemoveAll(realBusDir)

	before := time.Now().Unix()
	meta, err := InitSessionMeta(session, "edit")
	if err != nil {
		t.Fatalf("InitSessionMeta: %v", err)
	}
	after := time.Now().Unix()

	if meta.StartTS < before || meta.StartTS > after {
		t.Errorf("StartTS %d not in range [%d, %d]", meta.StartTS, before, after)
	}
	if meta.CompactCount != 0 {
		t.Errorf("expected CompactCount 0, got %d", meta.CompactCount)
	}
}

func TestInitSessionMeta_Idempotent(t *testing.T) {
	session := "test-idempotent"
	realBusDir := BusDir(session)
	realSessDir := SessionDir(session)
	if err := os.MkdirAll(realSessDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer os.RemoveAll(realBusDir)

	meta1, err := InitSessionMeta(session, "build")
	if err != nil {
		t.Fatalf("InitSessionMeta 1: %v", err)
	}

	// Small delay to ensure time would differ
	time.Sleep(10 * time.Millisecond)

	meta2, err := InitSessionMeta(session, "build")
	if err != nil {
		t.Fatalf("InitSessionMeta 2: %v", err)
	}

	if meta1.StartTS != meta2.StartTS {
		t.Errorf("StartTS changed: %d → %d", meta1.StartTS, meta2.StartTS)
	}
}

func TestReadSessionMeta_NotFound(t *testing.T) {
	session := "test-notfound"
	realBusDir := BusDir(session)
	realSessDir := SessionDir(session)
	if err := os.MkdirAll(realSessDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer os.RemoveAll(realBusDir)

	meta, err := ReadSessionMeta(session, "nonexistent")
	if err != nil {
		t.Fatalf("ReadSessionMeta: %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil, got %+v", meta)
	}
}

func TestCompactSession_FirstCompact(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	session := "test-compact1"
	realBusDir := BusDir(session)
	realSessDir := SessionDir(session)
	if err := os.MkdirAll(realSessDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer os.RemoveAll(realBusDir)

	if err := CompactSession(session, "edit", "completed auth refactor"); err != nil {
		t.Fatalf("CompactSession: %v", err)
	}

	// Check memory was written
	content, err := ReadMemory("edit")
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if !strings.Contains(content, "## Session Summary") {
		t.Errorf("missing Session Summary header in:\n%s", content)
	}
	if !strings.Contains(content, "completed auth refactor") {
		t.Errorf("missing summary content in:\n%s", content)
	}

	// Check meta was updated
	meta, err := ReadSessionMeta(session, "edit")
	if err != nil {
		t.Fatalf("ReadSessionMeta: %v", err)
	}
	if meta.CompactCount != 1 {
		t.Errorf("expected CompactCount 1, got %d", meta.CompactCount)
	}
	if meta.LastCompactTS == 0 {
		t.Error("expected LastCompactTS to be set")
	}
}

func TestCompactSession_MultipleCompacts(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	session := "test-compact-multi"
	realBusDir := BusDir(session)
	realSessDir := SessionDir(session)
	if err := os.MkdirAll(realSessDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer os.RemoveAll(realBusDir)

	if err := CompactSession(session, "build", "first summary"); err != nil {
		t.Fatalf("CompactSession 1: %v", err)
	}
	if err := CompactSession(session, "build", "second summary"); err != nil {
		t.Fatalf("CompactSession 2: %v", err)
	}
	if err := CompactSession(session, "build", "third summary"); err != nil {
		t.Fatalf("CompactSession 3: %v", err)
	}

	meta, err := ReadSessionMeta(session, "build")
	if err != nil {
		t.Fatalf("ReadSessionMeta: %v", err)
	}
	if meta.CompactCount != 3 {
		t.Errorf("expected CompactCount 3, got %d", meta.CompactCount)
	}

	// Verify all summaries are in memory
	content, err := ReadMemory("build")
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if !strings.Contains(content, "first summary") {
		t.Error("missing first summary")
	}
	if !strings.Contains(content, "second summary") {
		t.Error("missing second summary")
	}
	if !strings.Contains(content, "third summary") {
		t.Error("missing third summary")
	}
}

func TestSessionUptime(t *testing.T) {
	meta := &SessionMeta{
		StartTS: time.Now().Add(-2 * time.Hour).Unix(),
	}

	uptime := SessionUptime(meta)
	// Allow some tolerance
	if uptime < 1*time.Hour+59*time.Minute || uptime > 2*time.Hour+1*time.Minute {
		t.Errorf("expected ~2h uptime, got %v", uptime)
	}
}

func TestResumeContext_NoEntries(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	content, err := ResumeContext("edit")
	if err != nil {
		t.Fatalf("ResumeContext: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty, got %q", content)
	}
}

func TestResumeContext_WithEntries(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	// Write a session summary entry
	if err := AppendMemory("Session Summary", "refactored the auth module", "edit"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	content, err := ResumeContext("edit")
	if err != nil {
		t.Fatalf("ResumeContext: %v", err)
	}
	if !strings.Contains(content, "## Session Resume") {
		t.Errorf("missing Session Resume header in:\n%s", content)
	}
	if !strings.Contains(content, "refactored the auth module") {
		t.Errorf("missing summary content in:\n%s", content)
	}
}

func TestResumeContext_LimitsToThree(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	// Write 5 session summaries
	for i := 1; i <= 5; i++ {
		summary := strings.Repeat("x", i) // unique content per entry
		if err := AppendMemory("Session Summary", "summary-"+summary, "edit"); err != nil {
			t.Fatalf("AppendMemory %d: %v", i, err)
		}
	}

	content, err := ResumeContext("edit")
	if err != nil {
		t.Fatalf("ResumeContext: %v", err)
	}

	// Should only contain last 3 (xxx, xxxx, xxxxx)
	if strings.Contains(content, "summary-x\n") {
		t.Error("should not contain first summary (x)")
	}
	if strings.Contains(content, "summary-xx\n") {
		t.Error("should not contain second summary (xx)")
	}
	if !strings.Contains(content, "summary-xxx") {
		t.Error("missing third summary (xxx)")
	}
	if !strings.Contains(content, "summary-xxxx") {
		t.Error("missing fourth summary (xxxx)")
	}
	if !strings.Contains(content, "summary-xxxxx") {
		t.Error("missing fifth summary (xxxxx)")
	}
}

func TestResumeContext_IgnoresNonSessionEntries(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	// Write a non-session entry and a session entry
	if err := AppendMemory("Build Config", "use pnpm", "edit"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	if err := AppendMemory("Session Summary", "session work done", "edit"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	content, err := ResumeContext("edit")
	if err != nil {
		t.Fatalf("ResumeContext: %v", err)
	}
	if strings.Contains(content, "use pnpm") {
		t.Error("should not contain non-session entry")
	}
	if !strings.Contains(content, "session work done") {
		t.Error("missing session summary")
	}
}

func TestFormatSessionStatus(t *testing.T) {
	meta := &SessionMeta{
		StartTS:       time.Now().Add(-1 * time.Hour).Unix(),
		CompactCount:  3,
		LastCompactTS: time.Now().Add(-10 * time.Minute).Unix(),
	}

	output := FormatSessionStatus(meta, "edit", 42)
	if !strings.Contains(output, "edit") {
		t.Errorf("missing role in output:\n%s", output)
	}
	if !strings.Contains(output, "1h") {
		t.Errorf("missing uptime in output:\n%s", output)
	}
	if !strings.Contains(output, "3") {
		t.Errorf("missing compact count in output:\n%s", output)
	}
	if !strings.Contains(output, "42") {
		t.Errorf("missing message count in output:\n%s", output)
	}
}
