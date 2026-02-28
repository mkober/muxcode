package harness

import (
	"encoding/json"
	"testing"
)

var defaultTools = []string{"bash", "read_file", "glob", "grep", "write_file", "edit_file"}

func TestExtractToolCalls_SingleInCodeBlock(t *testing.T) {
	text := "I'll run a command:\n```json\n{\"name\": \"bash\", \"arguments\": {\"command\": \"ls -la\"}}\n```\n"
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "bash" {
		t.Errorf("expected name 'bash', got %q", calls[0].Function.Name)
	}
	if calls[0].ID != "textcall_0" {
		t.Errorf("expected ID 'textcall_0', got %q", calls[0].ID)
	}
	if calls[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", calls[0].Type)
	}

	var args struct{ Command string }
	if err := json.Unmarshal(calls[0].Function.Arguments, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args.Command != "ls -la" {
		t.Errorf("expected command 'ls -la', got %q", args.Command)
	}
}

func TestExtractToolCalls_MultipleToolCalls(t *testing.T) {
	text := `Let me check the files:
{"name": "glob", "arguments": {"pattern": "*.go"}}
And read one:
{"name": "read_file", "arguments": {"path": "/tmp/test.go"}}
`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].Function.Name != "glob" {
		t.Errorf("call 0: expected 'glob', got %q", calls[0].Function.Name)
	}
	if calls[0].ID != "textcall_0" {
		t.Errorf("call 0: expected ID 'textcall_0', got %q", calls[0].ID)
	}
	if calls[1].Function.Name != "read_file" {
		t.Errorf("call 1: expected 'read_file', got %q", calls[1].Function.Name)
	}
	if calls[1].ID != "textcall_1" {
		t.Errorf("call 1: expected ID 'textcall_1', got %q", calls[1].ID)
	}
}

func TestExtractToolCalls_BareJSON(t *testing.T) {
	text := `{"name": "bash", "arguments": {"command": "echo hello"}}`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "bash" {
		t.Errorf("expected 'bash', got %q", calls[0].Function.Name)
	}
}

func TestExtractToolCalls_UnknownTool(t *testing.T) {
	text := `{"name": "unknown_tool", "arguments": {"foo": "bar"}}`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 0 {
		t.Errorf("expected 0 calls for unknown tool, got %d", len(calls))
	}
}

func TestExtractToolCalls_MalformedJSON(t *testing.T) {
	text := `{"name": "bash", "arguments": {"command": "echo hello"}`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 0 {
		t.Errorf("expected 0 calls for malformed JSON, got %d", len(calls))
	}
}

func TestExtractToolCalls_MixedTextAndJSON(t *testing.T) {
	text := `I need to search for the function.
The best approach is to use grep.
{"name": "grep", "arguments": {"pattern": "func main"}}
That should find it.`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "grep" {
		t.Errorf("expected 'grep', got %q", calls[0].Function.Name)
	}
}

func TestExtractToolCalls_NestedBraces(t *testing.T) {
	text := `{"name": "bash", "arguments": {"command": "echo '{\"key\": \"value\"}' | jq '.key'"}}`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "bash" {
		t.Errorf("expected 'bash', got %q", calls[0].Function.Name)
	}

	var args struct{ Command string }
	if err := json.Unmarshal(calls[0].Function.Arguments, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args.Command != `echo '{"key": "value"}' | jq '.key'` {
		t.Errorf("unexpected command: %q", args.Command)
	}
}

func TestExtractToolCalls_EmptyInput(t *testing.T) {
	for _, input := range []string{"", "   ", "\n\n"} {
		calls := ExtractToolCalls(input, defaultTools)
		if len(calls) != 0 {
			t.Errorf("expected 0 calls for %q, got %d", input, len(calls))
		}
	}
}

func TestExtractToolCalls_NonToolJSON(t *testing.T) {
	// Valid JSON but not a tool call (no "name"/"arguments" fields)
	text := `Here is the config: {"host": "localhost", "port": 8080}`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 0 {
		t.Errorf("expected 0 calls for non-tool JSON, got %d", len(calls))
	}
}

func TestExtractToolCalls_MissingArguments(t *testing.T) {
	// Has name but no arguments field
	text := `{"name": "bash"}`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 0 {
		t.Errorf("expected 0 calls when arguments missing, got %d", len(calls))
	}
}

func TestExtractToolCalls_ReadFile(t *testing.T) {
	text := "```json\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"/etc/hosts\"}}\n```"
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Errorf("expected 'read_file', got %q", calls[0].Function.Name)
	}
}

func TestExtractToolCalls_EditFile(t *testing.T) {
	text := `{"name": "edit_file", "arguments": {"path": "/tmp/foo.go", "old_string": "func old()", "new_string": "func new()"}}`
	calls := ExtractToolCalls(text, defaultTools)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}

	var args struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(calls[0].Function.Arguments, &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if args.OldString != "func old()" || args.NewString != "func new()" {
		t.Errorf("unexpected args: %+v", args)
	}
}

func TestToolNames(t *testing.T) {
	defs := []ToolDef{
		{Function: ToolDefFunction{Name: "bash"}},
		{Function: ToolDefFunction{Name: "read_file"}},
		{Function: ToolDefFunction{Name: "glob"}},
	}
	names := toolNames(defs)
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	expected := []string{"bash", "read_file", "glob"}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("names[%d] = %q, want %q", i, names[i], want)
		}
	}
}

func TestStripCodeFences(t *testing.T) {
	input := "text before\n```json\n{\"a\": 1}\n```\ntext after\n"
	result := stripCodeFences(input)
	if contains(result, "```") {
		t.Errorf("expected fences stripped, got: %q", result)
	}
	if !contains(result, `{"a": 1}`) {
		t.Errorf("expected JSON content preserved, got: %q", result)
	}
	if !contains(result, "text before") || !contains(result, "text after") {
		t.Errorf("expected surrounding text preserved, got: %q", result)
	}
}

func TestExtractJSONObjects_Nested(t *testing.T) {
	input := `{"outer": {"inner": "val"}}`
	objects := extractJSONObjects(input)
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
	if objects[0] != input {
		t.Errorf("expected %q, got %q", input, objects[0])
	}
}

func TestExtractJSONObjects_Multiple(t *testing.T) {
	input := `text {"a": 1} more {"b": 2} end`
	objects := extractJSONObjects(input)
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}
}

func TestExtractJSONObjects_StringWithBraces(t *testing.T) {
	input := `{"cmd": "echo '{}'"}`
	objects := extractJSONObjects(input)
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}
	if objects[0] != input {
		t.Errorf("expected %q, got %q", input, objects[0])
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
