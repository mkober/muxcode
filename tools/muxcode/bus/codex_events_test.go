package bus

import (
	"strings"
	"testing"
)

// --- ParseCodexEvent ---

func TestParseCodexEvent_ThreadStarted(t *testing.T) {
	line := `{"type":"thread.started","thread_id":"abc-123","model":"gpt-5.4"}`
	ev := ParseCodexEvent(line)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	if ev.Type != CodexEventThreadStarted {
		t.Errorf("type = %q, want thread.started", ev.Type)
	}
	if ev.ThreadID != "abc-123" {
		t.Errorf("thread_id = %q, want abc-123", ev.ThreadID)
	}
}

func TestParseCodexEvent_TurnStarted(t *testing.T) {
	ev := ParseCodexEvent(`{"type":"turn.started"}`)
	if ev == nil || ev.Type != CodexEventTurnStarted {
		t.Error("expected turn.started event")
	}
}

func TestParseCodexEvent_ItemCompleted_AgentMessage(t *testing.T) {
	line := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Review complete: no issues found."}}`
	ev := ParseCodexEvent(line)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	if ev.Type != CodexEventItemCompleted {
		t.Errorf("type = %q, want item.completed", ev.Type)
	}
	if ev.Item == nil {
		t.Fatal("expected non-nil item")
	}
	if ev.Item.Type != "agent_message" {
		t.Errorf("item.type = %q, want agent_message", ev.Item.Type)
	}
	if ev.Item.Text != "Review complete: no issues found." {
		t.Errorf("item.text = %q", ev.Item.Text)
	}
}

func TestParseCodexEvent_ItemCompleted_CommandExecution(t *testing.T) {
	line := `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc 'echo hello'","aggregated_output":"hello\n","exit_code":0,"status":"completed"}}`
	ev := ParseCodexEvent(line)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	if ev.Item == nil {
		t.Fatal("expected non-nil item")
	}
	if ev.Item.Type != "command_execution" {
		t.Errorf("item.type = %q, want command_execution", ev.Item.Type)
	}
	if ev.Item.Command != "/bin/bash -lc 'echo hello'" {
		t.Errorf("item.command = %q", ev.Item.Command)
	}
	if ev.Item.AggregatedOutput != "hello\n" {
		t.Errorf("item.aggregated_output = %q", ev.Item.AggregatedOutput)
	}
	if ev.Item.ExitCode == nil || *ev.Item.ExitCode != 0 {
		t.Errorf("item.exit_code = %v", ev.Item.ExitCode)
	}
}

func TestParseCodexEvent_ItemStarted_InProgress(t *testing.T) {
	line := `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"make build","aggregated_output":"","exit_code":null,"status":"in_progress"}}`
	ev := ParseCodexEvent(line)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	if ev.Type != CodexEventItemStarted {
		t.Errorf("type = %q, want item.started", ev.Type)
	}
	if ev.Item.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", ev.Item.Status)
	}
	if ev.Item.ExitCode != nil {
		t.Errorf("exit_code should be nil for in_progress, got %d", *ev.Item.ExitCode)
	}
}

func TestParseCodexEvent_TurnCompleted_WithUsage(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":23454,"cached_input_tokens":18176,"output_tokens":71}}`
	ev := ParseCodexEvent(line)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	if ev.Type != CodexEventTurnCompleted {
		t.Errorf("type = %q, want turn.completed", ev.Type)
	}
	if ev.Usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if ev.Usage.InputTokens != 23454 {
		t.Errorf("input_tokens = %d, want 23454", ev.Usage.InputTokens)
	}
	if ev.Usage.CachedInputTokens != 18176 {
		t.Errorf("cached_input_tokens = %d, want 18176", ev.Usage.CachedInputTokens)
	}
	if ev.Usage.OutputTokens != 71 {
		t.Errorf("output_tokens = %d, want 71", ev.Usage.OutputTokens)
	}
}

func TestParseCodexEvent_TurnFailed(t *testing.T) {
	line := `{"type":"turn.failed","error":{"message":"model not supported"}}`
	ev := ParseCodexEvent(line)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	if ev.Type != CodexEventTurnFailed {
		t.Errorf("type = %q, want turn.failed", ev.Type)
	}
	if ev.Error == nil || ev.Error.Message != "model not supported" {
		t.Errorf("error = %+v", ev.Error)
	}
}

func TestParseCodexEvent_Error(t *testing.T) {
	line := `{"type":"error","message":"authentication failed"}`
	ev := ParseCodexEvent(line)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	if ev.Type != CodexEventError {
		t.Errorf("type = %q, want error", ev.Type)
	}
	if ev.Message != "authentication failed" {
		t.Errorf("message = %q", ev.Message)
	}
}

func TestParseCodexEvent_Empty(t *testing.T) {
	if ev := ParseCodexEvent(""); ev != nil {
		t.Error("expected nil for empty string")
	}
	if ev := ParseCodexEvent("  "); ev != nil {
		t.Error("expected nil for whitespace")
	}
}

func TestParseCodexEvent_InvalidJSON(t *testing.T) {
	if ev := ParseCodexEvent("{not json}"); ev != nil {
		t.Error("expected nil for invalid JSON")
	}
	if ev := ParseCodexEvent("plain text"); ev != nil {
		t.Error("expected nil for plain text")
	}
}

// --- ParseCodexEvents (multi-line) ---

func TestParseCodexEvents_FullSession(t *testing.T) {
	data := `{"type":"thread.started","thread_id":"019d82f1-1fae-7460-a3f3-2e6a7ebee554"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Running the command now."}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc 'echo hello world'","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc 'echo hello world'","aggregated_output":"hello world\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"The command printed: hello world"}}
{"type":"turn.completed","usage":{"input_tokens":23454,"cached_input_tokens":18176,"output_tokens":71}}`

	events := ParseCodexEvents(data)
	if len(events) != 7 {
		t.Errorf("got %d events, want 7", len(events))
	}

	// Verify event type sequence
	expectedTypes := []CodexEventType{
		CodexEventThreadStarted,
		CodexEventTurnStarted,
		CodexEventItemCompleted,
		CodexEventItemStarted,
		CodexEventItemCompleted,
		CodexEventItemCompleted,
		CodexEventTurnCompleted,
	}
	for i, want := range expectedTypes {
		if i >= len(events) {
			break
		}
		if events[i].Type != want {
			t.Errorf("events[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
}

func TestParseCodexEvents_SkipsEmptyLines(t *testing.T) {
	data := `{"type":"turn.started"}

{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":10}}
`
	events := ParseCodexEvents(data)
	if len(events) != 2 {
		t.Errorf("got %d events, want 2", len(events))
	}
}

func TestParseCodexEvents_Empty(t *testing.T) {
	events := ParseCodexEvents("")
	if len(events) != 0 {
		t.Error("expected 0 events for empty input")
	}
}

// --- AnalyzeCodexEvents ---

func TestAnalyzeCodexEvents_SuccessfulRun(t *testing.T) {
	events := ParseCodexEvents(`{"type":"thread.started","thread_id":"abc-123"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Running review..."}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"git diff","aggregated_output":"+ new line\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"Review complete: no issues found."}}
{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":500,"output_tokens":50}}`)

	result := AnalyzeCodexEvents(events)

	if result.ThreadID != "abc-123" {
		t.Errorf("ThreadID = %q, want abc-123", result.ThreadID)
	}
	if !result.Completed {
		t.Error("expected Completed=true")
	}
	if result.Errored {
		t.Error("expected Errored=false")
	}
	if len(result.Messages) != 2 {
		t.Errorf("got %d messages, want 2", len(result.Messages))
	}
	if result.Messages[1] != "Review complete: no issues found." {
		t.Errorf("last message = %q", result.Messages[1])
	}
	if len(result.Commands) != 1 {
		t.Errorf("got %d commands, want 1", len(result.Commands))
	}
	if !result.Commands[0].Success {
		t.Error("command should be successful")
	}
	if result.Usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if result.Usage.OutputTokens != 50 {
		t.Errorf("output_tokens = %d, want 50", result.Usage.OutputTokens)
	}
}

func TestAnalyzeCodexEvents_FailedCommand(t *testing.T) {
	exitCode := 1
	events := []CodexEvent{
		{Type: CodexEventThreadStarted, ThreadID: "t1"},
		{Type: CodexEventTurnStarted},
		{Type: CodexEventItemCompleted, Item: &CodexItem{
			Type:             "command_execution",
			Command:          "make build",
			AggregatedOutput: "error: undefined reference\n",
			ExitCode:         &exitCode,
			Status:           "completed",
		}},
		{Type: CodexEventItemCompleted, Item: &CodexItem{
			Type: "agent_message",
			Text: "Build failed with compilation errors.",
		}},
		{Type: CodexEventTurnCompleted},
	}

	result := AnalyzeCodexEvents(events)

	if !result.Completed {
		t.Error("expected Completed=true")
	}
	if !result.Errored {
		t.Error("expected Errored=true for non-zero exit code")
	}
	if result.ErrorMessage == "" {
		t.Error("expected non-empty error message")
	}
	if !result.Commands[0].Success {
		// exit code 1 = not success
	} else {
		t.Error("command with exit code 1 should not be successful")
	}
}

func TestAnalyzeCodexEvents_TurnFailed(t *testing.T) {
	events := []CodexEvent{
		{Type: CodexEventThreadStarted, ThreadID: "t1"},
		{Type: CodexEventTurnStarted},
		{Type: CodexEventTurnFailed, Error: &CodexError{Message: "model quota exceeded"}},
	}

	result := AnalyzeCodexEvents(events)

	if !result.Completed {
		t.Error("expected Completed=true for turn.failed")
	}
	if !result.Errored {
		t.Error("expected Errored=true for turn.failed")
	}
	if result.ErrorMessage != "model quota exceeded" {
		t.Errorf("error = %q, want 'model quota exceeded'", result.ErrorMessage)
	}
}

func TestAnalyzeCodexEvents_ErrorEvent(t *testing.T) {
	events := []CodexEvent{
		{Type: CodexEventThreadStarted, ThreadID: "t1"},
		{Type: CodexEventTurnStarted},
		{Type: CodexEventError, Message: "auth failed"},
		{Type: CodexEventTurnFailed, Error: &CodexError{Message: "auth failed"}},
	}

	result := AnalyzeCodexEvents(events)

	if !result.Errored {
		t.Error("expected Errored=true")
	}
	// First error message wins
	if result.ErrorMessage != "auth failed" {
		t.Errorf("error = %q, want 'auth failed'", result.ErrorMessage)
	}
}

func TestAnalyzeCodexEvents_MultipleCommands(t *testing.T) {
	exitZero := 0
	exitOne := 1
	events := []CodexEvent{
		{Type: CodexEventThreadStarted, ThreadID: "t1"},
		{Type: CodexEventItemCompleted, Item: &CodexItem{
			Type: "command_execution", Command: "go vet ./...",
			AggregatedOutput: "", ExitCode: &exitZero, Status: "completed",
		}},
		{Type: CodexEventItemCompleted, Item: &CodexItem{
			Type: "command_execution", Command: "go test ./...",
			AggregatedOutput: "FAIL\n", ExitCode: &exitOne, Status: "completed",
		}},
		{Type: CodexEventTurnCompleted},
	}

	result := AnalyzeCodexEvents(events)

	if len(result.Commands) != 2 {
		t.Fatalf("got %d commands, want 2", len(result.Commands))
	}
	if !result.Commands[0].Success {
		t.Error("first command should succeed")
	}
	if result.Commands[1].Success {
		t.Error("second command should fail")
	}
	if !result.Errored {
		t.Error("should be errored due to failed command")
	}
}

func TestAnalyzeCodexEvents_NoItems(t *testing.T) {
	events := []CodexEvent{
		{Type: CodexEventThreadStarted, ThreadID: "t1"},
		{Type: CodexEventTurnStarted},
		{Type: CodexEventTurnCompleted},
	}

	result := AnalyzeCodexEvents(events)
	if !result.Completed {
		t.Error("expected completed")
	}
	if result.Errored {
		t.Error("expected no error")
	}
	if len(result.Messages) != 0 {
		t.Error("expected no messages")
	}
	if len(result.Commands) != 0 {
		t.Error("expected no commands")
	}
}

func TestAnalyzeCodexEvents_NilItem(t *testing.T) {
	// item.completed with nil item should not panic
	events := []CodexEvent{
		{Type: CodexEventItemCompleted, Item: nil},
		{Type: CodexEventTurnCompleted},
	}

	result := AnalyzeCodexEvents(events)
	if !result.Completed {
		t.Error("expected completed")
	}
}

// --- FormatCodexResult ---

func TestFormatCodexResult_Success(t *testing.T) {
	result := CodexTaskResult{
		Completed: true,
		Messages:  []string{"Starting review", "Review complete: no issues."},
		Commands:  []CodexCmd{{Command: "git diff", Success: true}},
		Usage:     &CodexUsage{InputTokens: 1000, CachedInputTokens: 500, OutputTokens: 50},
	}

	output := FormatCodexResult(result)

	if !strings.Contains(output, "✅") {
		t.Error("expected success marker")
	}
	if !strings.Contains(output, "Review complete: no issues.") {
		t.Error("expected last message in output")
	}
	if !strings.Contains(output, "1 executed, all succeeded") {
		t.Error("expected command summary")
	}
	if !strings.Contains(output, "Tokens:") {
		t.Error("expected token usage")
	}
}

func TestFormatCodexResult_Error(t *testing.T) {
	result := CodexTaskResult{
		Completed:    true,
		Errored:      true,
		ErrorMessage: "build failed",
		Commands:     []CodexCmd{{Command: "make", Success: false}},
	}

	output := FormatCodexResult(result)

	if !strings.Contains(output, "❌") {
		t.Error("expected error marker")
	}
	if !strings.Contains(output, "build failed") {
		t.Error("expected error message")
	}
	if !strings.Contains(output, "1 failed") {
		t.Error("expected failed command count")
	}
}

func TestFormatCodexResult_LongErrorTruncation(t *testing.T) {
	result := CodexTaskResult{
		Errored:      true,
		ErrorMessage: strings.Repeat("x", 300),
	}

	output := FormatCodexResult(result)
	if !strings.Contains(output, "...") {
		t.Error("expected truncation indicator for long error")
	}
}

func TestFormatCodexResult_LongMessageTruncation(t *testing.T) {
	result := CodexTaskResult{
		Completed: true,
		Messages:  []string{strings.Repeat("y", 600)},
	}

	output := FormatCodexResult(result)
	if !strings.Contains(output, "...") {
		t.Error("expected truncation indicator for long message")
	}
}

func TestFormatCodexResult_NoUsage(t *testing.T) {
	result := CodexTaskResult{
		Completed: true,
		Messages:  []string{"Done"},
	}

	output := FormatCodexResult(result)
	if strings.Contains(output, "Tokens:") {
		t.Error("should not show tokens when usage is nil")
	}
}

func TestFormatCodexResult_Empty(t *testing.T) {
	result := CodexTaskResult{}
	output := FormatCodexResult(result)
	// Should not panic, might be empty
	_ = output
}

// --- End-to-end: parse real JSONL → analyze → format ---

func TestCodexEvents_EndToEnd(t *testing.T) {
	// Simulates the actual JSONL from `codex exec --json "echo hello"`
	jsonl := `{"type":"thread.started","thread_id":"019d82f1-1fae-7460-a3f3-2e6a7ebee554"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"I'm running the exact command you asked for and will report the output directly."}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc 'echo hello world'","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc 'echo hello world'","aggregated_output":"hello world\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"The command printed:\n\n` + "```" + `text\nhello world\n` + "```" + `"}}
{"type":"turn.completed","usage":{"input_tokens":23454,"cached_input_tokens":18176,"output_tokens":71}}`

	events := ParseCodexEvents(jsonl)
	if len(events) != 7 {
		t.Fatalf("parsed %d events, want 7", len(events))
	}

	result := AnalyzeCodexEvents(events)

	if result.ThreadID != "019d82f1-1fae-7460-a3f3-2e6a7ebee554" {
		t.Errorf("ThreadID = %q", result.ThreadID)
	}
	if !result.Completed {
		t.Error("should be completed")
	}
	if result.Errored {
		t.Error("should not be errored")
	}
	if len(result.Messages) != 2 {
		t.Errorf("got %d messages, want 2", len(result.Messages))
	}
	if len(result.Commands) != 1 {
		t.Errorf("got %d commands, want 1", len(result.Commands))
	}
	if result.Commands[0].Output != "hello world\n" {
		t.Errorf("command output = %q", result.Commands[0].Output)
	}

	formatted := FormatCodexResult(result)
	if !strings.Contains(formatted, "✅") {
		t.Error("formatted should contain success marker")
	}
	if !strings.Contains(formatted, "Tokens:") {
		t.Error("formatted should contain token usage")
	}
}

func TestCodexEvents_EndToEnd_AuthError(t *testing.T) {
	// Simulates the actual JSONL from an auth error
	jsonl := `{"type":"thread.started","thread_id":"019d82f1-078b-7001-a255-d17cb17174bf"}
{"type":"turn.started"}
{"type":"error","message":"{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The 'gpt-4.1-mini' model is not supported when using Codex with a ChatGPT account.\"}}"}
{"type":"turn.failed","error":{"message":"{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The 'gpt-4.1-mini' model is not supported when using Codex with a ChatGPT account.\"}}"}}`

	events := ParseCodexEvents(jsonl)
	result := AnalyzeCodexEvents(events)

	if !result.Errored {
		t.Error("should be errored")
	}
	if !result.Completed {
		t.Error("should be completed (turn.failed)")
	}
	if !strings.Contains(result.ErrorMessage, "not supported") {
		t.Errorf("error message = %q", result.ErrorMessage)
	}

	formatted := FormatCodexResult(result)
	if !strings.Contains(formatted, "❌") {
		t.Error("formatted should contain error marker")
	}
}
