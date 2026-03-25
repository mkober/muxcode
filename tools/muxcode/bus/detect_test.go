package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIndicatorFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectProject_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	types := DetectProject(dir)
	if len(types) != 0 {
		t.Fatalf("expected 0 types in empty dir, got %d", len(types))
	}
}

func TestDetectProject_Go(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.22\n")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "go" {
		t.Errorf("expected 'go', got '%s'", types[0].Name)
	}
	if types[0].Metadata["module"] != "example.com/foo" {
		t.Errorf("expected module 'example.com/foo', got '%s'", types[0].Metadata["module"])
	}
	if types[0].Metadata["go_version"] != "1.22" {
		t.Errorf("expected go_version '1.22', got '%s'", types[0].Metadata["go_version"])
	}
}

func TestDetectProject_NodeJS(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "package.json", `{
		"name": "my-app",
		"scripts": {
			"build": "tsc",
			"test": "jest"
		}
	}`)

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "nodejs" {
		t.Errorf("expected 'nodejs', got '%s'", types[0].Name)
	}
	if types[0].Metadata["name"] != "my-app" {
		t.Errorf("expected name 'my-app', got '%s'", types[0].Metadata["name"])
	}
	if types[0].Metadata["build"] != "tsc" {
		t.Errorf("expected build 'tsc', got '%s'", types[0].Metadata["build"])
	}
	if types[0].Metadata["test"] != "jest" {
		t.Errorf("expected test 'jest', got '%s'", types[0].Metadata["test"])
	}
}

func TestDetectProject_Python(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "requirements.txt", "flask\nrequests\n")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "python" {
		t.Errorf("expected 'python', got '%s'", types[0].Name)
	}
}

func TestDetectProject_Rust(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "Cargo.toml", "[package]\nname = \"myapp\"\n")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "rust" {
		t.Errorf("expected 'rust', got '%s'", types[0].Name)
	}
}

func TestDetectProject_CDK(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "cdk.json", `{"app": "npx ts-node bin/app.ts"}`)

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "cdk" {
		t.Errorf("expected 'cdk', got '%s'", types[0].Name)
	}
	if types[0].Metadata["app"] != "npx ts-node bin/app.ts" {
		t.Errorf("expected app command, got '%s'", types[0].Metadata["app"])
	}
}

func TestDetectProject_GlobDetection(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "main.tf", "resource \"aws_instance\" \"web\" {}\n")
	writeIndicatorFile(t, dir, "variables.tf", "variable \"region\" {}\n")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "terraform" {
		t.Errorf("expected 'terraform', got '%s'", types[0].Name)
	}
	// Glob patterns should record the pattern, not individual matches
	if len(types[0].Indicators) != 1 || types[0].Indicators[0] != "*.tf" {
		t.Errorf("expected indicator ['*.tf'], got %v", types[0].Indicators)
	}
}

func TestDetectProject_CSharpGlob(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "MyApp.csproj", "<Project></Project>")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "csharp" {
		t.Errorf("expected 'csharp', got '%s'", types[0].Name)
	}
}

func TestDetectProject_CppGlob(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "main.cpp", "#include <iostream>")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "cpp" {
		t.Errorf("expected 'cpp', got '%s'", types[0].Name)
	}
}

func TestDetectProject_Multiple(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "go.mod", "module example.com/foo\n\ngo 1.22\n")
	writeIndicatorFile(t, dir, "Makefile", "build:\n\tgo build\n")
	writeIndicatorFile(t, dir, "Dockerfile", "FROM golang:1.22\n")

	types := DetectProject(dir)
	if len(types) != 3 {
		t.Fatalf("expected 3 types, got %d", len(types))
	}
	// Should be sorted by name: docker, go, make
	names := make([]string, len(types))
	for i, pt := range types {
		names[i] = pt.Name
	}
	expected := "docker,go,make"
	if got := strings.Join(names, ","); got != expected {
		t.Errorf("expected order '%s', got '%s'", expected, got)
	}
}

func TestDetectProject_JavaMaven(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "pom.xml", "<project></project>")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "java-maven" {
		t.Errorf("expected 'java-maven', got '%s'", types[0].Name)
	}
}

func TestDetectProject_JavaGradle(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "build.gradle.kts", "plugins { kotlin(\"jvm\") }")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "java-gradle" {
		t.Errorf("expected 'java-gradle', got '%s'", types[0].Name)
	}
}

func TestDetectProject_Ruby(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "Gemfile", "source 'https://rubygems.org'")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "ruby" {
		t.Errorf("expected 'ruby', got '%s'", types[0].Name)
	}
}

func TestDetectProject_Docker(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "Dockerfile", "FROM alpine:3.18")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "docker" {
		t.Errorf("expected 'docker', got '%s'", types[0].Name)
	}
}

func TestDetectProject_Swift(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "Package.swift", "// swift-tools-version:5.9")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "swift" {
		t.Errorf("expected 'swift', got '%s'", types[0].Name)
	}
}

func TestDetectProject_Godot(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "project.godot", "[application]")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "gdscript" {
		t.Errorf("expected 'gdscript', got '%s'", types[0].Name)
	}
}

func TestDetectProject_PHP(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "composer.json", `{"name": "vendor/mylib"}`)

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "php" {
		t.Errorf("expected 'php', got '%s'", types[0].Name)
	}
	if types[0].Metadata["name"] != "vendor/mylib" {
		t.Errorf("expected name 'vendor/mylib', got '%s'", types[0].Metadata["name"])
	}
}

func TestExtractGoMod_MissingFile(t *testing.T) {
	dir := t.TempDir()
	m := extractGoMod(dir)
	if m != nil {
		t.Errorf("expected nil for missing go.mod, got %v", m)
	}
}

func TestExtractPackageJSON_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "package.json", "not json")

	m := extractPackageJSON(dir)
	if m != nil {
		t.Errorf("expected nil for invalid JSON, got %v", m)
	}
}

func TestExtractPackageJSON_NoScripts(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "package.json", `{"name": "bare-pkg"}`)

	m := extractPackageJSON(dir)
	if m == nil {
		t.Fatal("expected non-nil map")
	}
	if m["name"] != "bare-pkg" {
		t.Errorf("expected name 'bare-pkg', got '%s'", m["name"])
	}
	if _, ok := m["build"]; ok {
		t.Error("expected no build key")
	}
}

func TestExtractCdkJSON_EmptyApp(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "cdk.json", `{"context": {}}`)

	m := extractCdkJSON(dir)
	if m != nil {
		t.Errorf("expected nil for empty app, got %v", m)
	}
}

func TestAutoContextFiles_Output(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.22\n")
	writeIndicatorFile(t, dir, "Makefile", "build:\n\tgo build\n")

	files := AutoContextFiles(dir)
	if len(files) != 2 {
		t.Fatalf("expected 2 auto context files, got %d", len(files))
	}

	// Sorted: go, make
	if files[0].Name != "go" {
		t.Errorf("expected first file name 'go', got '%s'", files[0].Name)
	}
	if files[0].Source != "auto" {
		t.Errorf("expected source 'auto', got '%s'", files[0].Source)
	}
	if files[0].Role != "shared" {
		t.Errorf("expected role 'shared', got '%s'", files[0].Role)
	}
	if !strings.Contains(files[0].Body, "Go Project") {
		t.Error("expected Go convention text in body")
	}
	if !strings.Contains(files[0].Body, "example.com/test") {
		t.Error("expected module name in Go convention text")
	}

	if files[1].Name != "make" {
		t.Errorf("expected second file name 'make', got '%s'", files[1].Name)
	}
}

func TestAutoContextFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	files := AutoContextFiles(dir)
	if len(files) != 0 {
		t.Fatalf("expected 0 files for empty dir, got %d", len(files))
	}
}

func TestFormatDetectOutput_Empty(t *testing.T) {
	output := FormatDetectOutput(nil)
	if !strings.Contains(output, "No project types detected") {
		t.Errorf("expected 'no types detected' message, got '%s'", output)
	}
}

func TestFormatDetectOutput_WithTypes(t *testing.T) {
	types := []ProjectType{
		{Name: "go", Indicators: []string{"go.mod"}, Metadata: map[string]string{"module": "example.com/foo"}},
		{Name: "make", Indicators: []string{"Makefile"}},
	}

	output := FormatDetectOutput(types)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 data), got %d", len(lines))
	}
	if !strings.Contains(lines[0], "TYPE") || !strings.Contains(lines[0], "INDICATORS") || !strings.Contains(lines[0], "METADATA") {
		t.Errorf("unexpected header: %s", lines[0])
	}
	if !strings.Contains(lines[1], "go") || !strings.Contains(lines[1], "go.mod") || !strings.Contains(lines[1], "module=example.com/foo") {
		t.Errorf("unexpected go line: %s", lines[1])
	}
	if !strings.Contains(lines[2], "make") || !strings.Contains(lines[2], "Makefile") {
		t.Errorf("unexpected make line: %s", lines[2])
	}
}

func TestConventionText_AllTypesNonEmpty(t *testing.T) {
	// Every type in the indicators list should produce non-empty convention text
	for _, ind := range indicators {
		pt := ProjectType{Name: ind.Name}
		text := conventionText(pt)
		if text == "" {
			t.Errorf("conventionText for '%s' returned empty string", ind.Name)
		}
	}
}

func TestDetectProject_PythonMultipleIndicators(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "pyproject.toml", "[tool.poetry]\nname = \"myapp\"")
	writeIndicatorFile(t, dir, "requirements.txt", "flask")

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type (python detected once), got %d", len(types))
	}
	if types[0].Name != "python" {
		t.Errorf("expected 'python', got '%s'", types[0].Name)
	}
	// Both indicators should be recorded
	if len(types[0].Indicators) != 2 {
		t.Errorf("expected 2 indicators, got %d: %v", len(types[0].Indicators), types[0].Indicators)
	}
}

func TestDetectProject_TypescriptFile(t *testing.T) {
	dir := t.TempDir()
	writeIndicatorFile(t, dir, "tsconfig.json", `{"compilerOptions": {}}`)

	types := DetectProject(dir)
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types[0].Name != "typescript" {
		t.Errorf("expected 'typescript', got '%s'", types[0].Name)
	}
}
