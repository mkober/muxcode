package bus

import (
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
