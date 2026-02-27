package bus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteBash_AllowedCommand(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Bash(echo *)"},
	}

	call := ToolCall{
		ID:   "test-1",
		Type: "function",
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"echo hello"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "hello") {
		t.Errorf("result = %q, want to contain 'hello'", result)
	}
}

func TestExecuteBash_DeniedCommand(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Bash(echo *)"},
	}

	call := ToolCall{
		ID:   "test-2",
		Type: "function",
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"rm -rf /"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "not allowed") {
		t.Errorf("result = %q, want 'not allowed' error", result)
	}
}

func TestExecuteBash_OutputTruncation(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Bash(python3 *)"},
	}

	// Generate output longer than MaxOutputLen
	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"python3 -c \"print('x' * 20000)\""}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if len(result) > MaxOutputLen+100 { // allow margin for truncation message
		t.Errorf("result length = %d, want <= %d", len(result), MaxOutputLen+100)
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("long output should contain truncation notice")
	}
}

func TestExecuteBash_EmptyCommand(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Bash(*)"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":""}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "required") {
		t.Errorf("result = %q, want 'required' error", result)
	}
}

func TestExecuteRead(t *testing.T) {
	// Create a temp file
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(testFile, []byte("file contents here"), 0644); err != nil {
		t.Fatal(err)
	}

	e := &ToolExecutor{
		Patterns: []string{"Read"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"` + testFile + `"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if result != "file contents here" {
		t.Errorf("result = %q, want 'file contents here'", result)
	}
}

func TestExecuteRead_NotAllowed(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Bash(echo *)"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"/etc/hosts"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "not allowed") {
		t.Errorf("result = %q, want 'not allowed' error", result)
	}
}

func TestExecuteRead_FileNotFound(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Read"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"/nonexistent/file.txt"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "Error") {
		t.Errorf("result = %q, want error for missing file", result)
	}
}

func TestExecuteGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(dir, "c.go"), []byte("c"), 0644)

	e := &ToolExecutor{
		Patterns: []string{"Glob"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "glob",
			Arguments: json.RawMessage(`{"pattern":"` + dir + `/*.txt"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "a.txt") || !strings.Contains(result, "b.txt") {
		t.Errorf("result = %q, want both .txt files", result)
	}
	if strings.Contains(result, "c.go") {
		t.Errorf("result should not contain .go file")
	}
}

func TestExecuteGlob_NoMatches(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Glob"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "glob",
			Arguments: json.RawMessage(`{"pattern":"/nonexistent/*.xyz"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "No matches") {
		t.Errorf("result = %q, want 'No matches'", result)
	}
}

func TestExecuteGrep(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world\nfoo bar\nhello again"), 0644)

	e := &ToolExecutor{
		Patterns: []string{"Grep"},
		WorkDir:  dir,
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "grep",
			Arguments: json.RawMessage(`{"pattern":"hello","path":"."}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "hello world") {
		t.Errorf("result = %q, want 'hello world' match", result)
	}
	if !strings.Contains(result, "hello again") {
		t.Errorf("result = %q, want 'hello again' match", result)
	}
}

func TestExecuteGrep_NoMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world"), 0644)

	e := &ToolExecutor{
		Patterns: []string{"Grep"},
		WorkDir:  dir,
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "grep",
			Arguments: json.RawMessage(`{"pattern":"zzzzz","path":"."}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "No matches") {
		t.Errorf("result = %q, want 'No matches'", result)
	}
}

func TestExecuteWrite(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "output.txt")

	e := &ToolExecutor{
		Patterns: []string{"Write"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"` + testFile + `","content":"written content"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "Wrote") {
		t.Errorf("result = %q, want 'Wrote' confirmation", result)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "written content" {
		t.Errorf("file content = %q, want 'written content'", string(data))
	}
}

func TestExecuteEdit(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "edit.txt")
	os.WriteFile(testFile, []byte("hello world"), 0644)

	e := &ToolExecutor{
		Patterns: []string{"Edit"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "edit_file",
			Arguments: json.RawMessage(`{"path":"` + testFile + `","old_string":"hello","new_string":"goodbye"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "Replaced") {
		t.Errorf("result = %q, want 'Replaced' confirmation", result)
	}

	data, _ := os.ReadFile(testFile)
	if string(data) != "goodbye world" {
		t.Errorf("file content = %q, want 'goodbye world'", string(data))
	}
}

func TestExecuteEdit_NotUnique(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "edit.txt")
	os.WriteFile(testFile, []byte("aaa bbb aaa"), 0644)

	e := &ToolExecutor{
		Patterns: []string{"Edit"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "edit_file",
			Arguments: json.RawMessage(`{"path":"` + testFile + `","old_string":"aaa","new_string":"xxx"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "2 times") {
		t.Errorf("result = %q, want uniqueness error", result)
	}
}

func TestExecuteUnknownTool(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Read"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "unknown_tool",
			Arguments: json.RawMessage(`{}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "unknown tool") {
		t.Errorf("result = %q, want 'unknown tool' error", result)
	}
}

func TestExecuteBash_InvalidJSON(t *testing.T) {
	e := &ToolExecutor{
		Patterns: []string{"Bash(*)"},
	}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`not json`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "invalid arguments") {
		t.Errorf("result = %q, want 'invalid arguments' error", result)
	}
}
