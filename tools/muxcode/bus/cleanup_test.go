package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupResult_TotalItems(t *testing.T) {
	r := &CleanupResult{
		BusDirs:      []string{"/tmp/a"},
		PreviewFiles: []string{"/tmp/b", "/tmp/c"},
		TriggerFiles: []string{"/tmp/d"},
		SpawnDirs:    []string{"/tmp/e"},
		LogFiles:     []string{"/tmp/f", "/tmp/g", "/tmp/h"},
	}
	if got := r.TotalItems(); got != 8 {
		t.Errorf("TotalItems: got %d, want 8", got)
	}
}

func TestCleanupResult_TotalItems_Empty(t *testing.T) {
	r := &CleanupResult{}
	if got := r.TotalItems(); got != 0 {
		t.Errorf("TotalItems: got %d, want 0", got)
	}
}

func TestCleanupStale_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Create stale session artifacts — session "stale123" has no tmux session
	busDir := filepath.Join(tmpDir, "muxcode-bus-stale123")
	os.MkdirAll(busDir, 0o755)
	previewFile := filepath.Join(tmpDir, "muxcode-preview-stale123.tmp")
	os.WriteFile(previewFile, []byte("preview"), 0o644)
	triggerFile := filepath.Join(tmpDir, "muxcode-analyze-stale123.trigger")
	os.WriteFile(triggerFile, []byte("trigger"), 0o644)
	spawnDir := filepath.Join(tmpDir, "muxcode-spawn-stale123")
	os.MkdirAll(spawnDir, 0o755)
	logFile := filepath.Join(tmpDir, "muxcode-log-ABCDEF.txt")
	os.WriteFile(logFile, []byte("log"), 0o644)

	result, err := CleanupStale("other-session", true, false)
	if err != nil {
		t.Fatalf("CleanupStale dry-run: %v", err)
	}

	// Dry run should find items but not remove them
	if len(result.BusDirs) != 1 {
		t.Errorf("BusDirs: got %d, want 1", len(result.BusDirs))
	}
	if len(result.PreviewFiles) != 1 {
		t.Errorf("PreviewFiles: got %d, want 1", len(result.PreviewFiles))
	}
	if len(result.TriggerFiles) != 1 {
		t.Errorf("TriggerFiles: got %d, want 1", len(result.TriggerFiles))
	}
	if len(result.SpawnDirs) != 1 {
		t.Errorf("SpawnDirs: got %d, want 1", len(result.SpawnDirs))
	}
	if len(result.LogFiles) != 1 {
		t.Errorf("LogFiles: got %d, want 1", len(result.LogFiles))
	}

	// Files should still exist after dry run
	if _, err := os.Stat(busDir); os.IsNotExist(err) {
		t.Error("bus dir was removed during dry run")
	}
	if _, err := os.Stat(previewFile); os.IsNotExist(err) {
		t.Error("preview file was removed during dry run")
	}
}

func TestCleanupStale_Removes(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Create stale artifacts
	busDir := filepath.Join(tmpDir, "muxcode-bus-gone456")
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0o755)
	os.WriteFile(filepath.Join(busDir, "inbox", "edit.jsonl"), []byte("msg"), 0o644)
	triggerFile := filepath.Join(tmpDir, "muxcode-analyze-gone456.trigger")
	os.WriteFile(triggerFile, []byte("trigger"), 0o644)

	result, err := CleanupStale("my-session", false, false)
	if err != nil {
		t.Fatalf("CleanupStale: %v", err)
	}

	if result.TotalItems() != 2 {
		t.Errorf("TotalItems: got %d, want 2", result.TotalItems())
	}

	// Files should be removed
	if _, err := os.Stat(busDir); !os.IsNotExist(err) {
		t.Error("bus dir was not removed")
	}
	if _, err := os.Stat(triggerFile); !os.IsNotExist(err) {
		t.Error("trigger file was not removed")
	}
}

func TestCleanupStale_SkipsCurrentSession(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Create artifacts for "current" session — should be skipped
	// (shouldClean returns false when session == currentSession and !includeActive,
	//  but since no tmux session exists, it would normally be cleaned.
	//  The current-session guard runs before the tmux check.)
	busDir := filepath.Join(tmpDir, "muxcode-bus-current")
	os.MkdirAll(busDir, 0o755)

	result, err := CleanupStale("current", true, false)
	if err != nil {
		t.Fatalf("CleanupStale: %v", err)
	}

	if len(result.BusDirs) != 0 {
		t.Errorf("BusDirs: got %d, want 0 (current session should be skipped)", len(result.BusDirs))
	}
}

func TestCleanupStale_AllIncludesCurrent(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	busDir := filepath.Join(tmpDir, "muxcode-bus-current")
	os.MkdirAll(busDir, 0o755)

	result, err := CleanupStale("current", true, true)
	if err != nil {
		t.Fatalf("CleanupStale: %v", err)
	}

	// With --all, current session is included (no real tmux session in tests)
	if len(result.BusDirs) != 1 {
		t.Errorf("BusDirs: got %d, want 1 (--all should include current)", len(result.BusDirs))
	}
}

func TestCleanupStale_IgnoresNonMuxcodeFiles(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Create non-muxcode files that should be ignored
	os.WriteFile(filepath.Join(tmpDir, "other-file.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(tmpDir, "other-dir"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "muxcode-other.tmp"), []byte("x"), 0o644)

	result, err := CleanupStale("test", true, false)
	if err != nil {
		t.Fatalf("CleanupStale: %v", err)
	}

	if result.TotalItems() != 0 {
		t.Errorf("TotalItems: got %d, want 0 (non-muxcode files should be ignored)", result.TotalItems())
	}
}

func TestFormatCleanupResult_Empty(t *testing.T) {
	r := &CleanupResult{}
	out := FormatCleanupResult(r, false)
	if out != "Nothing to clean up" {
		t.Errorf("FormatCleanupResult empty: got %q, want %q", out, "Nothing to clean up")
	}
}

func TestFormatCleanupResult_DryRun(t *testing.T) {
	r := &CleanupResult{
		BusDirs:  []string{"/tmp/muxcode-bus-old"},
		LogFiles: []string{"/tmp/muxcode-log-ABC.txt"},
	}
	out := FormatCleanupResult(r, true)
	if !strings.Contains(out, "Would remove") {
		t.Errorf("FormatCleanupResult dry-run: expected 'Would remove', got %q", out)
	}
	if !strings.Contains(out, "Total: 2 item(s)") {
		t.Errorf("FormatCleanupResult dry-run: expected total count, got %q", out)
	}
}

func TestFormatCleanupResult_Actual(t *testing.T) {
	r := &CleanupResult{
		PreviewFiles: []string{"/tmp/muxcode-preview-old.tmp"},
		SpawnDirs:    []string{"/tmp/muxcode-spawn-old"},
	}
	out := FormatCleanupResult(r, false)
	if !strings.Contains(out, "Removed") {
		t.Errorf("FormatCleanupResult: expected 'Removed', got %q", out)
	}
	if strings.Contains(out, "Would remove") {
		t.Errorf("FormatCleanupResult: should not contain 'Would remove' when not dry-run")
	}
}

func TestShouldClean_CurrentSession(t *testing.T) {
	// Current session without --all should not be cleaned
	if shouldClean("mysession", "mysession", false) {
		t.Error("shouldClean: current session without --all should return false")
	}
}

func TestShouldClean_CurrentSessionWithAll(t *testing.T) {
	// Current session with --all: cleaned if tmux session not alive
	// (in tests, no tmux session exists, so this returns true)
	if !shouldClean("mysession", "mysession", true) {
		t.Error("shouldClean: current session with --all should return true when tmux session not alive")
	}
}

func TestShouldClean_StaleSession(t *testing.T) {
	// Different session with no tmux session should be cleaned
	if !shouldClean("stale", "current", false) {
		t.Error("shouldClean: stale session should return true")
	}
}

// --- Claude Code /tmp cleanup tests ---

func TestIsUUID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"009c901b-7902-4d3a-abde-fa421bd0063a", true},
		{"a1b2c3d4-e5f6-7890-abcd-ef1234567890", true},
		{"A1B2C3D4-E5F6-7890-ABCD-EF1234567890", true},
		{"not-a-uuid", false},
		{"", false},
		{"009c901b-7902-4d3a-abde-fa421bd0063", false},   // too short
		{"009c901b-7902-4d3a-abde-fa421bd0063ab", false}, // too long
		{"009c901b79024d3aabdefa421bd0063a1234", false},  // no dashes
		{"009c901b-7902-4d3a-abde-fa421bd0063g", false},  // invalid hex char
	}
	for _, tt := range tests {
		if got := isUUID(tt.input); got != tt.want {
			t.Errorf("isUUID(%q): got %v, want %v", tt.input, got, tt.want)
		}
	}
}

// formatBytes is tested in compact_test.go — TestFormatBytes.

func TestCleanupClaudeTmp_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Create /tmp/claude-503/-Project-Path/uuid-session/tasks/ structure
	claudeDir := filepath.Join(tmpDir, "claude-503")
	projDir := filepath.Join(claudeDir, "-Users-test-Repos-myproject")
	sessDir := filepath.Join(projDir, "009c901b-7902-4d3a-abde-fa421bd0063a")
	tasksDir := filepath.Join(sessDir, "tasks")
	os.MkdirAll(tasksDir, 0o755)
	os.WriteFile(filepath.Join(tasksDir, "abc.output"), []byte("task output data"), 0o644)

	// Backdate the session dir to 10 days ago
	oldTime := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(sessDir, oldTime, oldTime)

	result, err := CleanupClaudeTmp(7*24*time.Hour, true)
	if err != nil {
		t.Fatalf("CleanupClaudeTmp dry-run: %v", err)
	}

	if len(result.Sessions) != 1 {
		t.Errorf("Sessions: got %d, want 1", len(result.Sessions))
	}
	if result.BytesFreed <= 0 {
		t.Errorf("BytesFreed: got %d, want > 0", result.BytesFreed)
	}

	// Files should still exist after dry run
	if _, err := os.Stat(sessDir); os.IsNotExist(err) {
		t.Error("session dir was removed during dry run")
	}
}

func TestCleanupClaudeTmp_Removes(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	claudeDir := filepath.Join(tmpDir, "claude-503")
	projDir := filepath.Join(claudeDir, "-Users-test-Repos-myproject")
	sessDir := filepath.Join(projDir, "a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	os.MkdirAll(filepath.Join(sessDir, "tasks"), 0o755)
	os.WriteFile(filepath.Join(sessDir, "tasks", "out.txt"), []byte("data"), 0o644)

	// Backdate to 14 days
	oldTime := time.Now().Add(-14 * 24 * time.Hour)
	os.Chtimes(sessDir, oldTime, oldTime)

	result, err := CleanupClaudeTmp(7*24*time.Hour, false)
	if err != nil {
		t.Fatalf("CleanupClaudeTmp: %v", err)
	}

	if len(result.Sessions) != 1 {
		t.Errorf("Sessions: got %d, want 1", len(result.Sessions))
	}

	// Session dir should be removed
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Error("session dir was not removed")
	}
	// Empty project and claude dirs should be cleaned up too
	if _, err := os.Stat(projDir); !os.IsNotExist(err) {
		t.Error("empty project dir was not removed")
	}
}

func TestCleanupClaudeTmp_SkipsRecent(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	claudeDir := filepath.Join(tmpDir, "claude-503")
	projDir := filepath.Join(claudeDir, "-Users-test-Repos-myproject")
	sessDir := filepath.Join(projDir, "b2c3d4e5-f6a7-8901-bcde-f12345678901")
	os.MkdirAll(filepath.Join(sessDir, "tasks"), 0o755)
	// Don't backdate — session is fresh (mtime is now)

	result, err := CleanupClaudeTmp(7*24*time.Hour, true)
	if err != nil {
		t.Fatalf("CleanupClaudeTmp: %v", err)
	}

	if len(result.Sessions) != 0 {
		t.Errorf("Sessions: got %d, want 0 (recent sessions should be skipped)", len(result.Sessions))
	}
}

func TestCleanupClaudeTmp_IgnoresNonUUIDDirs(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	claudeDir := filepath.Join(tmpDir, "claude-503")
	projDir := filepath.Join(claudeDir, "-Users-test-Repos-myproject")
	// Create a non-UUID dir that should be ignored
	os.MkdirAll(filepath.Join(projDir, "not-a-uuid"), 0o755)
	os.MkdirAll(filepath.Join(projDir, "cache"), 0o755)

	result, err := CleanupClaudeTmp(0, true) // age=0 means clean everything
	if err != nil {
		t.Fatalf("CleanupClaudeTmp: %v", err)
	}

	if len(result.Sessions) != 0 {
		t.Errorf("Sessions: got %d, want 0 (non-UUID dirs should be ignored)", len(result.Sessions))
	}
}

func TestCleanupClaudeTmp_IgnoresNonClaudeDirs(t *testing.T) {
	tmpDir := t.TempDir()
	SetBusDirBase(tmpDir)
	defer ResetBusDirBase()

	// Create dirs that don't match the claude-* pattern
	os.MkdirAll(filepath.Join(tmpDir, "other-dir"), 0o755)
	os.WriteFile(filepath.Join(tmpDir, "claude-file.txt"), []byte("x"), 0o644)

	result, err := CleanupClaudeTmp(0, true)
	if err != nil {
		t.Fatalf("CleanupClaudeTmp: %v", err)
	}

	if len(result.Sessions) != 0 {
		t.Errorf("Sessions: got %d, want 0", len(result.Sessions))
	}
}

func TestFormatClaudeCleanupResult_Empty(t *testing.T) {
	r := &ClaudeCleanupResult{}
	out := FormatClaudeCleanupResult(r, false)
	if out != "No stale Claude Code sessions found" {
		t.Errorf("FormatClaudeCleanupResult empty: got %q", out)
	}
}

func TestFormatClaudeCleanupResult_WithSessions(t *testing.T) {
	r := &ClaudeCleanupResult{
		Sessions:   []string{"/tmp/claude-503/proj/uuid1", "/tmp/claude-503/proj/uuid2"},
		BytesFreed: 5 * 1048576, // 5 MB
	}
	out := FormatClaudeCleanupResult(r, true)
	if !strings.Contains(out, "Would remove 2") {
		t.Errorf("FormatClaudeCleanupResult: expected 'Would remove 2', got %q", out)
	}
	if !strings.Contains(out, "5.0 MB") {
		t.Errorf("FormatClaudeCleanupResult: expected '5.0 MB', got %q", out)
	}
}
