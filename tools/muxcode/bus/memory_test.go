package bus

import (
	"fmt"
	"strings"
	"testing"
)

func TestReadMemory_NotFound(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	content, err := ReadMemory("build")
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty, got %q", content)
	}
}

func TestAppendAndRead(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	if err := AppendMemory("Build Config", "use pnpm", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	content, err := ReadMemory("build")
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}
	if !strings.Contains(content, "## Build Config") {
		t.Errorf("missing section header in:\n%s", content)
	}
	if !strings.Contains(content, "use pnpm") {
		t.Errorf("missing content in:\n%s", content)
	}
}

func TestAppendMultiple(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	if err := AppendMemory("First", "one", "build"); err != nil {
		t.Fatalf("AppendMemory 1: %v", err)
	}
	if err := AppendMemory("Second", "two", "build"); err != nil {
		t.Fatalf("AppendMemory 2: %v", err)
	}

	content, err := ReadMemory("build")
	if err != nil {
		t.Fatalf("ReadMemory: %v", err)
	}

	firstIdx := strings.Index(content, "## First")
	secondIdx := strings.Index(content, "## Second")
	if firstIdx == -1 || secondIdx == -1 {
		t.Fatalf("missing sections in:\n%s", content)
	}
	if firstIdx >= secondIdx {
		t.Error("expected First before Second")
	}
}

func TestReadContext(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	if err := AppendMemory("Shared Insight", "always lint", "shared"); err != nil {
		t.Fatalf("AppendMemory shared: %v", err)
	}
	if err := AppendMemory("Build Tip", "use turbo", "build"); err != nil {
		t.Fatalf("AppendMemory build: %v", err)
	}

	ctx, err := ReadContext("build")
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	if !strings.Contains(ctx, "# Shared Memory") {
		t.Errorf("missing shared header in:\n%s", ctx)
	}
	if !strings.Contains(ctx, "# build Memory") {
		t.Errorf("missing build header in:\n%s", ctx)
	}
	if !strings.Contains(ctx, "always lint") {
		t.Errorf("missing shared content in:\n%s", ctx)
	}
	if !strings.Contains(ctx, "use turbo") {
		t.Errorf("missing build content in:\n%s", ctx)
	}
}

func TestReadContext_SharedOnly(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", t.TempDir())

	if err := AppendMemory("Note", "global rule", "shared"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	ctx, err := ReadContext("build")
	if err != nil {
		t.Fatalf("ReadContext: %v", err)
	}
	if !strings.Contains(ctx, "# Shared Memory") {
		t.Errorf("missing shared header in:\n%s", ctx)
	}
	if strings.Contains(ctx, "# build Memory") {
		t.Errorf("unexpected build header in:\n%s", ctx)
	}
}

func TestParseMemoryEntries(t *testing.T) {
	content := `
## Build Config
_2026-02-21 14:27_

use pnpm for all builds

## Deploy Notes
_2026-02-21 15:00_

always run cdk diff first
`
	entries := ParseMemoryEntries(content, "shared")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Section != "Build Config" {
		t.Errorf("entry 0 section: got %q, want %q", entries[0].Section, "Build Config")
	}
	if entries[0].Timestamp != "2026-02-21 14:27" {
		t.Errorf("entry 0 timestamp: got %q, want %q", entries[0].Timestamp, "2026-02-21 14:27")
	}
	if entries[0].Content != "use pnpm for all builds" {
		t.Errorf("entry 0 content: got %q, want %q", entries[0].Content, "use pnpm for all builds")
	}
	if entries[0].Role != "shared" {
		t.Errorf("entry 0 role: got %q, want %q", entries[0].Role, "shared")
	}

	if entries[1].Section != "Deploy Notes" {
		t.Errorf("entry 1 section: got %q, want %q", entries[1].Section, "Deploy Notes")
	}
	if entries[1].Content != "always run cdk diff first" {
		t.Errorf("entry 1 content: got %q, want %q", entries[1].Content, "always run cdk diff first")
	}
}

func TestParseMemoryEntries_Empty(t *testing.T) {
	entries := ParseMemoryEntries("", "build")
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}

	entries = ParseMemoryEntries("   \n\n  ", "build")
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for whitespace, got %d", len(entries))
	}
}

func TestParseMemoryEntries_MultilineContent(t *testing.T) {
	content := `
## Multi Line
_2026-02-21 14:27_

line one
line two
line three
`
	entries := ParseMemoryEntries(content, "edit")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Content, "line one") {
		t.Errorf("missing line one in content: %q", entries[0].Content)
	}
	if !strings.Contains(entries[0].Content, "line two") {
		t.Errorf("missing line two in content: %q", entries[0].Content)
	}
	if !strings.Contains(entries[0].Content, "line three") {
		t.Errorf("missing line three in content: %q", entries[0].Content)
	}
}

func TestListMemoryFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// Write two memory files
	if err := AppendMemory("Section", "text", "shared"); err != nil {
		t.Fatalf("AppendMemory shared: %v", err)
	}
	if err := AppendMemory("Section", "text", "build"); err != nil {
		t.Fatalf("AppendMemory build: %v", err)
	}

	roles, err := ListMemoryFiles()
	if err != nil {
		t.Fatalf("ListMemoryFiles: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("expected 2 roles, got %d: %v", len(roles), roles)
	}

	found := map[string]bool{}
	for _, r := range roles {
		found[r] = true
	}
	if !found["shared"] {
		t.Error("missing 'shared' role")
	}
	if !found["build"] {
		t.Error("missing 'build' role")
	}
}

func TestListMemoryFiles_EmptyDir(t *testing.T) {
	t.Setenv("BUS_MEMORY_DIR", "/tmp/nonexistent-muxcode-test-dir-12345")

	roles, err := ListMemoryFiles()
	if err != nil {
		t.Fatalf("ListMemoryFiles: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("expected 0 roles, got %d", len(roles))
	}
}

func TestSearchMemory_BasicMatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Build Config", "use pnpm for all builds", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	if err := AppendMemory("Deploy Notes", "always run cdk diff first", "shared"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemory("pnpm", "", 0)
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Entry.Section != "Build Config" {
		t.Errorf("expected 'Build Config', got %q", results[0].Entry.Section)
	}
}

func TestSearchMemory_HeaderBoost(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	// "deploy" in header — should score higher
	if err := AppendMemory("Deploy Guide", "run cdk diff", "shared"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}
	// "deploy" only in body
	if err := AppendMemory("Notes", "remember to deploy after merge", "shared"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemory("deploy", "", 0)
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Header match (Deploy Guide) should rank first
	if results[0].Entry.Section != "Deploy Guide" {
		t.Errorf("expected 'Deploy Guide' first, got %q (score %.1f)",
			results[0].Entry.Section, results[0].Score)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("header match score (%.1f) should exceed body match score (%.1f)",
			results[0].Score, results[1].Score)
	}
}

func TestSearchMemory_RoleFilter(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Config", "use pnpm", "build"); err != nil {
		t.Fatalf("AppendMemory build: %v", err)
	}
	if err := AppendMemory("Config", "use pnpm too", "shared"); err != nil {
		t.Fatalf("AppendMemory shared: %v", err)
	}

	results, err := SearchMemory("pnpm", "build", 0)
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with role filter, got %d", len(results))
	}
	if results[0].Entry.Role != "build" {
		t.Errorf("expected role 'build', got %q", results[0].Entry.Role)
	}
}

func TestSearchMemory_Limit(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	for i := 0; i < 5; i++ {
		section := fmt.Sprintf("Entry %d", i)
		if err := AppendMemory(section, "common keyword here", "shared"); err != nil {
			t.Fatalf("AppendMemory %d: %v", i, err)
		}
	}

	results, err := SearchMemory("keyword", "", 2)
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with limit, got %d", len(results))
	}
}

func TestSearchMemory_NoMatch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Build Config", "use pnpm", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemory("nonexistent", "", 0)
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchMemory_CaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("BUS_MEMORY_DIR", tmp)

	if err := AppendMemory("Build Config", "use PNPM always", "build"); err != nil {
		t.Fatalf("AppendMemory: %v", err)
	}

	results, err := SearchMemory("pnpm", "", 0)
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for case-insensitive match, got %d", len(results))
	}
	if results[0].Entry.Section != "Build Config" {
		t.Errorf("expected 'Build Config', got %q", results[0].Entry.Section)
	}
}
