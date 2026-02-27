package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteBash_AllowedCommand(t *testing.T) {
	e := &Executor{Patterns: []string{"Bash(echo *)"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"echo hello"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "hello") {
		t.Errorf("result = %q, want 'hello'", result)
	}
}

func TestExecuteBash_DeniedCommand(t *testing.T) {
	e := &Executor{Patterns: []string{"Bash(echo *)"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"rm -rf /"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "not allowed") {
		t.Errorf("result = %q, want 'not allowed'", result)
	}
}

func TestExecuteBash_EmptyCommand(t *testing.T) {
	e := &Executor{Patterns: []string{"Bash(*)"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":""}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "required") {
		t.Errorf("result = %q, want 'required'", result)
	}
}

func TestExecuteBash_StringFallback(t *testing.T) {
	e := &Executor{Patterns: []string{"Bash(echo *)"}}

	// Small LLMs sometimes send arguments as a plain string instead of {"command":"..."}
	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`"echo hello"`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "hello") {
		t.Errorf("result = %q, want 'hello'", result)
	}
}

func TestExecuteRead_StringFallback(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("fallback content"), 0644)

	e := &Executor{Patterns: []string{"Read"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "read_file",
			Arguments: json.RawMessage(`"` + testFile + `"`),
		},
	}

	result := e.Execute(context.Background(), call)
	if result != "fallback content" {
		t.Errorf("result = %q, want 'fallback content'", result)
	}
}

func TestExecuteGlob_StringFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)

	e := &Executor{Patterns: []string{"Glob"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "glob",
			Arguments: json.RawMessage(`"` + dir + `/*.txt"`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "a.txt") {
		t.Errorf("result = %q, want 'a.txt'", result)
	}
}

func TestExecuteGrep_StringFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world"), 0644)

	e := &Executor{Patterns: []string{"Grep"}, WorkDir: dir}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "grep",
			Arguments: json.RawMessage(`"hello"`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "hello world") {
		t.Errorf("result = %q, want 'hello world'", result)
	}
}

func TestExecuteBash_DoubleEncodedJSON(t *testing.T) {
	e := &Executor{Patterns: []string{"Bash(echo *)"}}

	// Small LLMs sometimes double-encode: arguments is a JSON string containing JSON
	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`"{\"command\":\"echo hello\"}"`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "hello") {
		t.Errorf("result = %q, want 'hello' (double-encoded should be unwrapped)", result)
	}
}

func TestUnwrapCommand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain command", "echo hello", "echo hello"},
		{"JSON object", `{"command":"ls -la"}`, "ls -la"},
		{"JSON no command", `{"foo":"bar"}`, `{"foo":"bar"}`},
		{"empty command", `{"command":""}`, `{"command":""}`},
		{"not JSON", "not json {", "not json {"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unwrapCommand(tt.in)
			if got != tt.want {
				t.Errorf("unwrapCommand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnwrapPath(t *testing.T) {
	got := unwrapPath(`{"path":"/tmp/test.txt"}`)
	if got != "/tmp/test.txt" {
		t.Errorf("unwrapPath = %q, want /tmp/test.txt", got)
	}
	got = unwrapPath("/tmp/test.txt")
	if got != "/tmp/test.txt" {
		t.Errorf("unwrapPath plain = %q", got)
	}
}

func TestUnwrapPattern(t *testing.T) {
	got := unwrapPattern(`{"pattern":"*.go"}`)
	if got != "*.go" {
		t.Errorf("unwrapPattern = %q, want *.go", got)
	}
	got = unwrapPattern("*.go")
	if got != "*.go" {
		t.Errorf("unwrapPattern plain = %q", got)
	}
}

func TestExecuteBash_InvalidJSON(t *testing.T) {
	e := &Executor{Patterns: []string{"Bash(*)"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`not json`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "invalid arguments") {
		t.Errorf("result = %q, want 'invalid arguments'", result)
	}
}

func TestExecuteBash_OutputTruncation(t *testing.T) {
	e := &Executor{Patterns: []string{"Bash(python3 *)"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"python3 -c \"print('x' * 20000)\""}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if len(result) > MaxOutputLen+100 {
		t.Errorf("result length = %d, should be truncated", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Error("should contain truncation notice")
	}
}

func TestExecuteRead(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.txt")
	os.WriteFile(testFile, []byte("file contents here"), 0644)

	e := &Executor{Patterns: []string{"Read"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"` + testFile + `"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if result != "file contents here" {
		t.Errorf("result = %q", result)
	}
}

func TestExecuteRead_NotAllowed(t *testing.T) {
	e := &Executor{Patterns: []string{"Bash(echo *)"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "read_file",
			Arguments: json.RawMessage(`{"path":"/etc/hosts"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "not allowed") {
		t.Errorf("result = %q, want 'not allowed'", result)
	}
}

func TestExecuteGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(dir, "c.go"), []byte("c"), 0644)

	e := &Executor{Patterns: []string{"Glob"}}

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
		t.Error("should not contain .go file")
	}
}

func TestExecuteGrep(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world\nfoo bar\nhello again"), 0644)

	e := &Executor{Patterns: []string{"Grep"}, WorkDir: dir}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "grep",
			Arguments: json.RawMessage(`{"pattern":"hello","path":"."}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "hello world") {
		t.Errorf("result = %q, want 'hello world'", result)
	}
}

func TestExecuteGrep_NoMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world"), 0644)

	e := &Executor{Patterns: []string{"Grep"}, WorkDir: dir}

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

	e := &Executor{Patterns: []string{"Write"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"` + testFile + `","content":"written content"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "Wrote") {
		t.Errorf("result = %q, want 'Wrote'", result)
	}

	data, _ := os.ReadFile(testFile)
	if string(data) != "written content" {
		t.Errorf("file content = %q", string(data))
	}
}

func TestExecuteEdit(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "edit.txt")
	os.WriteFile(testFile, []byte("hello world"), 0644)

	e := &Executor{Patterns: []string{"Edit"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "edit_file",
			Arguments: json.RawMessage(`{"path":"` + testFile + `","old_string":"hello","new_string":"goodbye"}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "Replaced") {
		t.Errorf("result = %q, want 'Replaced'", result)
	}

	data, _ := os.ReadFile(testFile)
	if string(data) != "goodbye world" {
		t.Errorf("file content = %q", string(data))
	}
}

func TestExecuteEdit_NotUnique(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "edit.txt")
	os.WriteFile(testFile, []byte("aaa bbb aaa"), 0644)

	e := &Executor{Patterns: []string{"Edit"}}

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
	e := &Executor{Patterns: []string{"Read"}}

	call := ToolCall{
		Function: FunctionCall{
			Name:      "unknown_tool",
			Arguments: json.RawMessage(`{}`),
		},
	}

	result := e.Execute(context.Background(), call)
	if !strings.Contains(result, "unknown tool") {
		t.Errorf("result = %q, want 'unknown tool'", result)
	}
}
