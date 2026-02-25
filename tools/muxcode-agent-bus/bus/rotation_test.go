package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNeedsRotation_NoFile(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	if NeedsRotation("build") {
		t.Error("NeedsRotation should return false for non-existent file")
	}
}

func TestNeedsRotation_ModifiedToday(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Write today's file
	if err := AppendMemory("Test", "content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	if NeedsRotation("build") {
		t.Error("NeedsRotation should return false for file modified today")
	}
}

func TestNeedsRotation_ModifiedYesterday(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Write file and backdate it
	if err := AppendMemory("Test", "content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	yesterday := time.Now().AddDate(0, 0, -1)
	os.Chtimes(MemoryPath("build"), yesterday, yesterday)

	if !NeedsRotation("build") {
		t.Error("NeedsRotation should return true for file modified yesterday")
	}
}

func TestRotateMemory_MovesFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Test", "content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	// Backdate the file
	yesterday := time.Now().AddDate(0, 0, -1)
	os.Chtimes(MemoryPath("build"), yesterday, yesterday)

	cfg := DefaultRotationConfig()
	if err := RotateMemory("build", cfg); err != nil {
		t.Fatalf("RotateMemory: %v", err)
	}

	// Active file should be gone
	if _, err := os.Stat(MemoryPath("build")); !os.IsNotExist(err) {
		t.Error("active file should be removed after rotation")
	}
}

func TestRotateMemory_ArchiveDateCorrect(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Test", "content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	yesterday := time.Now().AddDate(0, 0, -1)
	os.Chtimes(MemoryPath("build"), yesterday, yesterday)

	cfg := DefaultRotationConfig()
	if err := RotateMemory("build", cfg); err != nil {
		t.Fatalf("RotateMemory: %v", err)
	}

	expectedDate := yesterday.Format("2006-01-02")
	archivePath := MemoryArchivePath("build", expectedDate)
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Errorf("archive file should exist at %s", archivePath)
	}
}

func TestRotateMemory_PreservesContent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Important", "preserve this data", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	yesterday := time.Now().AddDate(0, 0, -1)
	os.Chtimes(MemoryPath("build"), yesterday, yesterday)

	cfg := DefaultRotationConfig()
	if err := RotateMemory("build", cfg); err != nil {
		t.Fatalf("RotateMemory: %v", err)
	}

	expectedDate := yesterday.Format("2006-01-02")
	content, err := os.ReadFile(MemoryArchivePath("build", expectedDate))
	if err != nil {
		t.Fatalf("ReadFile archive: %v", err)
	}
	if !strings.Contains(string(content), "preserve this data") {
		t.Error("archive should contain original content")
	}
}

func TestRotateMemory_CreatesDirIfNeeded(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Test", "content", "newrole"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	yesterday := time.Now().AddDate(0, 0, -1)
	os.Chtimes(MemoryPath("newrole"), yesterday, yesterday)

	cfg := DefaultRotationConfig()
	if err := RotateMemory("newrole", cfg); err != nil {
		t.Fatalf("RotateMemory: %v", err)
	}

	archiveDir := MemoryArchiveDir("newrole")
	info, err := os.Stat(archiveDir)
	if err != nil {
		t.Fatalf("archive dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("archive path should be a directory")
	}
}

func TestRotateMemory_NoFileNoop(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	cfg := DefaultRotationConfig()
	if err := RotateMemory("nonexistent", cfg); err != nil {
		t.Fatalf("RotateMemory on missing file should not error: %v", err)
	}
}

func TestPurgeOldArchives_RemovesOld(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Create archive directory with old and recent files
	archiveDir := filepath.Join(tmp, "build")
	os.MkdirAll(archiveDir, 0755)

	oldDate := time.Now().AddDate(0, 0, -60).Format("2006-01-02")
	recentDate := time.Now().AddDate(0, 0, -5).Format("2006-01-02")

	os.WriteFile(filepath.Join(archiveDir, oldDate+".md"), []byte("old"), 0644)
	os.WriteFile(filepath.Join(archiveDir, recentDate+".md"), []byte("recent"), 0644)

	cfg := RotationConfig{RetentionDays: 30, ContextDays: 7}
	if err := PurgeOldArchives("build", cfg); err != nil {
		t.Fatalf("PurgeOldArchives: %v", err)
	}

	// Old file should be gone
	if _, err := os.Stat(filepath.Join(archiveDir, oldDate+".md")); !os.IsNotExist(err) {
		t.Error("old archive should be purged")
	}

	// Recent file should remain
	if _, err := os.Stat(filepath.Join(archiveDir, recentDate+".md")); os.IsNotExist(err) {
		t.Error("recent archive should remain")
	}
}

func TestPurgeOldArchives_KeepsRecent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	archiveDir := filepath.Join(tmp, "build")
	os.MkdirAll(archiveDir, 0755)

	for i := 0; i < 5; i++ {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		os.WriteFile(filepath.Join(archiveDir, date+".md"), []byte("content"), 0644)
	}

	cfg := RotationConfig{RetentionDays: 30, ContextDays: 7}
	if err := PurgeOldArchives("build", cfg); err != nil {
		t.Fatalf("PurgeOldArchives: %v", err)
	}

	// All 5 should remain (all within 30 days)
	dates, err := ListArchiveDates("build")
	if err != nil {
		t.Fatalf("ListArchiveDates: %v", err)
	}
	if len(dates) != 5 {
		t.Errorf("expected 5 archives, got %d", len(dates))
	}
}

func TestReadMemoryWithHistory_ActiveOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Today", "today content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	content, err := ReadMemoryWithHistory("build", 7)
	if err != nil {
		t.Fatalf("ReadMemoryWithHistory: %v", err)
	}
	if !strings.Contains(content, "today content") {
		t.Error("should contain today's active content")
	}
}

func TestReadMemoryWithHistory_WithArchives(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Create archive
	archiveDir := filepath.Join(tmp, "build")
	os.MkdirAll(archiveDir, 0755)
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	os.WriteFile(filepath.Join(archiveDir, yesterday+".md"), []byte("\n## Old\n_2026-02-23 10:00_\n\nold content\n"), 0644)

	// Create active file
	if err := AppendMemory("Today", "today content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	content, err := ReadMemoryWithHistory("build", 7)
	if err != nil {
		t.Fatalf("ReadMemoryWithHistory: %v", err)
	}
	if !strings.Contains(content, "old content") {
		t.Error("should contain archive content")
	}
	if !strings.Contains(content, "today content") {
		t.Error("should contain today's content")
	}
}

func TestReadMemoryWithHistory_RespectsDays(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	archiveDir := filepath.Join(tmp, "build")
	os.MkdirAll(archiveDir, 0755)

	// Archive from 2 days ago (within window)
	recent := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	os.WriteFile(filepath.Join(archiveDir, recent+".md"), []byte("recent archive"), 0644)

	// Archive from 20 days ago (outside window)
	old := time.Now().AddDate(0, 0, -20).Format("2006-01-02")
	os.WriteFile(filepath.Join(archiveDir, old+".md"), []byte("old archive"), 0644)

	content, err := ReadMemoryWithHistory("build", 7)
	if err != nil {
		t.Fatalf("ReadMemoryWithHistory: %v", err)
	}
	if !strings.Contains(content, "recent archive") {
		t.Error("should contain recent archive (within 7 days)")
	}
	if strings.Contains(content, "old archive") {
		t.Error("should not contain old archive (outside 7 days)")
	}
}

func TestListArchiveDates(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	archiveDir := filepath.Join(tmp, "build")
	os.MkdirAll(archiveDir, 0755)

	os.WriteFile(filepath.Join(archiveDir, "2026-02-20.md"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(archiveDir, "2026-02-22.md"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(archiveDir, "2026-02-21.md"), []byte("c"), 0644)
	os.WriteFile(filepath.Join(archiveDir, "not-a-date.md"), []byte("d"), 0644)

	dates, err := ListArchiveDates("build")
	if err != nil {
		t.Fatalf("ListArchiveDates: %v", err)
	}
	if len(dates) != 3 {
		t.Fatalf("expected 3 valid dates, got %d: %v", len(dates), dates)
	}
	// Should be sorted
	if dates[0] != "2026-02-20" || dates[1] != "2026-02-21" || dates[2] != "2026-02-22" {
		t.Errorf("dates not sorted correctly: %v", dates)
	}
}

func TestListArchiveDates_NoDirNoop(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	dates, err := ListArchiveDates("nonexistent")
	if err != nil {
		t.Fatalf("ListArchiveDates: %v", err)
	}
	if len(dates) != 0 {
		t.Errorf("expected 0 dates, got %d", len(dates))
	}
}

func TestAllMemoryEntriesWithArchives(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Active file
	if err := AppendMemory("Active Entry", "active content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	// Archive file
	archiveDir := filepath.Join(tmp, "build")
	os.MkdirAll(archiveDir, 0755)
	os.WriteFile(filepath.Join(archiveDir, "2026-02-20.md"),
		[]byte("\n## Archived Entry\n_2026-02-20 10:00_\n\narchived content\n"), 0644)

	entries, err := AllMemoryEntriesWithArchives()
	if err != nil {
		t.Fatalf("AllMemoryEntriesWithArchives: %v", err)
	}

	found := map[string]bool{}
	for _, e := range entries {
		found[e.Section] = true
	}
	if !found["Active Entry"] {
		t.Error("missing active entry")
	}
	if !found["Archived Entry"] {
		t.Error("missing archived entry")
	}
}

func TestArchiveTotalSize(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	archiveDir := filepath.Join(tmp, "build")
	os.MkdirAll(archiveDir, 0755)
	os.WriteFile(filepath.Join(archiveDir, "2026-02-20.md"), []byte("twelve bytes"), 0644)
	os.WriteFile(filepath.Join(archiveDir, "2026-02-21.md"), []byte("twelve bytes"), 0644)

	size := ArchiveTotalSize("build")
	if size != 24 {
		t.Errorf("expected 24 bytes, got %d", size)
	}
}

func TestArchiveTotalSize_NoDirZero(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	size := ArchiveTotalSize("nonexistent")
	if size != 0 {
		t.Errorf("expected 0 bytes for nonexistent dir, got %d", size)
	}
}

func TestListMemoryRoles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Active file
	if err := AppendMemory("Test", "content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	// Archive-only role (no active file, just archive dir)
	archiveDir := filepath.Join(tmp, "deploy")
	os.MkdirAll(archiveDir, 0755)
	os.WriteFile(filepath.Join(archiveDir, "2026-02-20.md"), []byte("archived"), 0644)

	roles, err := ListMemoryRoles()
	if err != nil {
		t.Fatalf("ListMemoryRoles: %v", err)
	}

	found := map[string]bool{}
	for _, r := range roles {
		found[r] = true
	}
	if !found["build"] {
		t.Error("missing 'build' role (active file)")
	}
	if !found["deploy"] {
		t.Error("missing 'deploy' role (archive-only)")
	}
}

func TestRotateMemory_AppendToExistingArchive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Create an existing archive for yesterday
	yesterday := time.Now().AddDate(0, 0, -1)
	archiveDir := filepath.Join(tmp, "build")
	os.MkdirAll(archiveDir, 0755)
	dateStr := yesterday.Format("2006-01-02")
	os.WriteFile(filepath.Join(archiveDir, dateStr+".md"), []byte("existing archive\n"), 0644)

	// Create active file backdated to yesterday
	if err := AppendMemory("New", "new content", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	os.Chtimes(MemoryPath("build"), yesterday, yesterday)

	cfg := DefaultRotationConfig()
	if err := RotateMemory("build", cfg); err != nil {
		t.Fatalf("RotateMemory: %v", err)
	}

	// Archive should contain both old and new content
	content, err := os.ReadFile(filepath.Join(archiveDir, dateStr+".md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "existing archive") {
		t.Error("should preserve existing archive content")
	}
	if !strings.Contains(string(content), "new content") {
		t.Error("should append new content to archive")
	}
}

func TestDefaultRotationConfig(t *testing.T) {
	cfg := DefaultRotationConfig()
	if cfg.RetentionDays != 30 {
		t.Errorf("RetentionDays: got %d, want 30", cfg.RetentionDays)
	}
	if cfg.ContextDays != 7 {
		t.Errorf("ContextDays: got %d, want 7", cfg.ContextDays)
	}
}
