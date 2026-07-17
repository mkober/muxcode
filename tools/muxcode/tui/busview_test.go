package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTailFile_OversizedLine is the regression test for the dashboard's copy of
// the 64KB inbox bug. tailFile returns the *last* n lines, so when
// bufio.Scanner aborted on a line over bufio.MaxScanTokenSize, tailFile
// returned lines from *before* the oversized entry — pinning the activity view
// to stale output for as long as that message stayed in the log, rather than
// failing visibly.
func TestTailFile_OversizedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")

	oversized := strings.Repeat("x", 100*1024) // > 64KB
	content := strings.Join([]string{
		"first",
		oversized,
		"second-to-last",
		"last",
	}, "\n") + "\n"

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := tailFile(path, 2)
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(got), got)
	}
	if got[0] != "second-to-last" || got[1] != "last" {
		t.Errorf("tailFile pinned to stale lines past the oversized entry: %q", got)
	}
}

// TestTailFile_ShorterThanN covers the path where the file has fewer than n
// non-empty lines.
func TestTailFile_ShorterThanN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")

	if err := os.WriteFile(path, []byte("only\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := tailFile(path, 5)
	if len(got) != 1 || got[0] != "only" {
		t.Errorf("got %q, want [only]", got)
	}
}

// TestTailFile_SkipsBlankLines preserves the original blank-line filtering.
func TestTailFile_SkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")

	if err := os.WriteFile(path, []byte("a\n\n\nb\n\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := tailFile(path, 5)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %q, want [a b]", got)
	}
}
