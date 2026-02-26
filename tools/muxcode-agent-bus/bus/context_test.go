package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupContextDirs(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()

	os.Setenv("BUS_CONTEXT_DIR", filepath.Join(tmpDir, "project", "context.d"))
	os.Setenv("MUXCODE_CONFIG_DIR", filepath.Join(tmpDir, "user"))

	cleanup := func() {
		os.Unsetenv("BUS_CONTEXT_DIR")
		os.Unsetenv("MUXCODE_CONFIG_DIR")
	}

	return tmpDir, cleanup
}

func writeContextFile(t *testing.T, base, role, name, content string) {
	t.Helper()
	dir := filepath.Join(base, role)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestReadContextFiles_Empty(t *testing.T) {
	_, cleanup := setupContextDirs(t)
	defer cleanup()

	files, err := ReadContextFiles()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}

func TestReadContextFiles_SharedAndRole(t *testing.T) {
	tmpDir, cleanup := setupContextDirs(t)
	defer cleanup()

	projectDir := filepath.Join(tmpDir, "project", "context.d")
	writeContextFile(t, projectDir, "shared", "conventions", "Use 2-space indentation")
	writeContextFile(t, projectDir, "edit", "patterns", "Prefer minimal diffs")

	files, err := ReadContextFiles()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}

	// Sorted: edit/patterns before shared/conventions
	if files[0].Role != "edit" || files[0].Name != "patterns" {
		t.Errorf("expected edit/patterns first, got %s/%s", files[0].Role, files[0].Name)
	}
	if files[1].Role != "shared" || files[1].Name != "conventions" {
		t.Errorf("expected shared/conventions second, got %s/%s", files[1].Role, files[1].Name)
	}
	if files[0].Source != "project" {
		t.Errorf("expected source 'project', got '%s'", files[0].Source)
	}
}

func TestReadContextFiles_Shadowing(t *testing.T) {
	tmpDir, cleanup := setupContextDirs(t)
	defer cleanup()

	projectDir := filepath.Join(tmpDir, "project", "context.d")
	userDir := filepath.Join(tmpDir, "user", "context.d")

	writeContextFile(t, projectDir, "shared", "conventions", "Project conventions")
	writeContextFile(t, userDir, "shared", "conventions", "User conventions")
	writeContextFile(t, userDir, "shared", "personal", "User personal patterns")

	files, err := ReadContextFiles()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files (1 shadowed), got %d", len(files))
	}

	// Find the conventions file
	var conventions ContextFile
	for _, f := range files {
		if f.Name == "conventions" {
			conventions = f
			break
		}
	}
	if conventions.Source != "project" {
		t.Errorf("expected project to shadow user, got source '%s'", conventions.Source)
	}
	if conventions.Body != "Project conventions" {
		t.Errorf("expected project content, got '%s'", conventions.Body)
	}

	// The personal file from user dir should still be present
	var personal ContextFile
	for _, f := range files {
		if f.Name == "personal" {
			personal = f
			break
		}
	}
	if personal.Source != "user" {
		t.Errorf("expected user source for personal, got '%s'", personal.Source)
	}
}

func TestReadContextFiles_IgnoresNonMd(t *testing.T) {
	tmpDir, cleanup := setupContextDirs(t)
	defer cleanup()

	projectDir := filepath.Join(tmpDir, "project", "context.d")
	writeContextFile(t, projectDir, "shared", "conventions", "2-space indent")

	// Write a non-.md file
	dir := filepath.Join(projectDir, "shared")
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0644)

	files, err := ReadContextFiles()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (non-md ignored), got %d", len(files))
	}
	if files[0].Name != "conventions" {
		t.Errorf("expected conventions, got %s", files[0].Name)
	}
}

func TestReadContextFiles_IgnoresNestedSubdirs(t *testing.T) {
	tmpDir, cleanup := setupContextDirs(t)
	defer cleanup()

	projectDir := filepath.Join(tmpDir, "project", "context.d")
	writeContextFile(t, projectDir, "shared", "conventions", "2-space indent")

	// Create a nested subdirectory within shared/
	nested := filepath.Join(projectDir, "shared", "nested")
	os.MkdirAll(nested, 0755)
	os.WriteFile(filepath.Join(nested, "deep.md"), []byte("should be ignored"), 0644)

	files, err := ReadContextFiles()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (nested dir ignored), got %d", len(files))
	}
}

func TestReadContextFiles_SkipsLargeFiles(t *testing.T) {
	tmpDir, cleanup := setupContextDirs(t)
	defer cleanup()

	projectDir := filepath.Join(tmpDir, "project", "context.d")
	writeContextFile(t, projectDir, "shared", "small", "small content")

	// Write a file larger than 100KB
	dir := filepath.Join(projectDir, "shared")
	bigContent := strings.Repeat("x", 101*1024)
	os.WriteFile(filepath.Join(dir, "big.md"), []byte(bigContent), 0644)

	files, err := ReadContextFiles()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file (large file skipped), got %d", len(files))
	}
	if files[0].Name != "small" {
		t.Errorf("expected small, got %s", files[0].Name)
	}
}

func TestContextFilesForRole(t *testing.T) {
	tmpDir, cleanup := setupContextDirs(t)
	defer cleanup()

	projectDir := filepath.Join(tmpDir, "project", "context.d")
	writeContextFile(t, projectDir, "shared", "conventions", "Use 2-space indentation")
	writeContextFile(t, projectDir, "edit", "patterns", "Prefer minimal diffs")
	writeContextFile(t, projectDir, "build", "troubleshooting", "Check build.sh first")

	// edit role should get shared + edit files
	editFiles, err := ContextFilesForRole("edit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(editFiles) != 2 {
		t.Fatalf("expected 2 files for edit (shared + edit), got %d", len(editFiles))
	}

	// build role should get shared + build files
	buildFiles, err := ContextFilesForRole("build")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(buildFiles) != 2 {
		t.Fatalf("expected 2 files for build (shared + build), got %d", len(buildFiles))
	}

	// review role should get only shared files
	reviewFiles, err := ContextFilesForRole("review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviewFiles) != 1 {
		t.Fatalf("expected 1 file for review (shared only), got %d", len(reviewFiles))
	}
	if reviewFiles[0].Name != "conventions" {
		t.Errorf("expected conventions for review, got %s", reviewFiles[0].Name)
	}
}

func TestFormatContextPrompt_Empty(t *testing.T) {
	result := FormatContextPrompt(nil)
	if result != "" {
		t.Errorf("expected empty string for nil files, got '%s'", result)
	}
}

func TestFormatContextPrompt_Output(t *testing.T) {
	files := []ContextFile{
		{Name: "conventions", Body: "Use 2-space indentation"},
		{Name: "architecture", Body: "Event-driven microservices"},
	}

	result := FormatContextPrompt(files)
	if !strings.HasPrefix(result, "## Project Context\n") {
		t.Errorf("expected '## Project Context' header, got: %s", result[:40])
	}
	if !strings.Contains(result, "### conventions\n") {
		t.Error("expected '### conventions' section")
	}
	if !strings.Contains(result, "Use 2-space indentation") {
		t.Error("expected conventions body")
	}
	if !strings.Contains(result, "### architecture\n") {
		t.Error("expected '### architecture' section")
	}
	if !strings.Contains(result, "Event-driven microservices") {
		t.Error("expected architecture body")
	}
}

func TestFormatContextList(t *testing.T) {
	files := []ContextFile{
		{Name: "conventions", Role: "shared", Source: "project"},
		{Name: "patterns", Role: "edit", Source: "user"},
	}

	result := FormatContextList(files)
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 data), got %d", len(lines))
	}
	if !strings.Contains(lines[0], "NAME") || !strings.Contains(lines[0], "ROLE") || !strings.Contains(lines[0], "SOURCE") {
		t.Errorf("unexpected header line: %s", lines[0])
	}
	if !strings.Contains(lines[1], "conventions") || !strings.Contains(lines[1], "shared") || !strings.Contains(lines[1], "project") {
		t.Errorf("unexpected first data line: %s", lines[1])
	}
	if !strings.Contains(lines[2], "patterns") || !strings.Contains(lines[2], "edit") || !strings.Contains(lines[2], "user") {
		t.Errorf("unexpected second data line: %s", lines[2])
	}
}

func TestContextDir_EnvOverride(t *testing.T) {
	defer os.Unsetenv("BUS_CONTEXT_DIR")

	os.Setenv("BUS_CONTEXT_DIR", "/custom/context")
	if got := ContextDir(); got != "/custom/context" {
		t.Errorf("expected /custom/context, got %s", got)
	}

	os.Unsetenv("BUS_CONTEXT_DIR")
	if got := ContextDir(); got != filepath.Join(".muxcode", "context.d") {
		t.Errorf("expected default path, got %s", got)
	}
}

func TestUserContextDir_EnvOverride(t *testing.T) {
	defer os.Unsetenv("MUXCODE_CONFIG_DIR")

	os.Setenv("MUXCODE_CONFIG_DIR", "/custom/config")
	if got := UserContextDir(); got != "/custom/config/context.d" {
		t.Errorf("expected /custom/config/context.d, got %s", got)
	}
}
