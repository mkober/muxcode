package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessBatch_SimpleResponse(t *testing.T) {
	// Set up mock Ollama that returns a text response (no tool calls)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Choices: []ChatChoice{
				{Message: ChatMessage{Role: "assistant", Content: "Task completed successfully"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Role:     "commit",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	executor := NewExecutor([]string{"Bash(git *)", "Read"})
	tools := BuildToolDefs([]string{"Bash(git *)", "Read"})
	filter := NewFilter("commit")

	// Create a fake bus client that captures send calls
	bus := &BusClient{BusDir: dir, Role: "commit", BinPath: "echo"} // echo as a no-op

	msgs := []Message{
		{ID: "1", From: "edit", To: "commit", Action: "status", Payload: "Show git status"},
	}

	// This will fail on bus.Send because echo isn't the real bus binary,
	// but we can verify the conversation logic works
	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs)

	// If we got here without panic, the conversation loop worked
}

func TestProcessBatch_WithToolCall(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)

		if callCount == 1 {
			// First call: return a tool call
			resp := ChatResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: FunctionCall{
										Name:      "bash",
										Arguments: json.RawMessage(`{"command":"echo hello"}`),
									},
								},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		} else {
			// Second call: return text response
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "Done: hello"}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Role:     "commit",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	executor := NewExecutor([]string{"Bash(echo *)"})
	tools := BuildToolDefs([]string{"Bash(echo *)"})
	filter := NewFilter("commit")
	bus := &BusClient{BusDir: dir, Role: "commit", BinPath: "echo"}

	msgs := []Message{
		{ID: "1", From: "edit", To: "commit", Action: "test", Payload: "Run echo hello"},
	}

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs)

	if callCount != 2 {
		t.Errorf("expected 2 Ollama calls, got %d", callCount)
	}
}

func TestProcessBatch_FilterBlocksInbox(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		if callCount == 1 {
			// LLM tries to check inbox
			resp := ChatResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: FunctionCall{
										Name:      "bash",
										Arguments: json.RawMessage(`{"command":"muxcode-agent-bus inbox"}`),
									},
								},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		} else {
			// After blocked, LLM should provide text response
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "I see, executing the task..."}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Role:     "commit",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	executor := NewExecutor([]string{"Bash(muxcode-agent-bus *)"})
	tools := BuildToolDefs([]string{"Bash(muxcode-agent-bus *)"})
	filter := NewFilter("commit")
	bus := &BusClient{BusDir: dir, Role: "commit", BinPath: "echo"}

	msgs := []Message{
		{ID: "1", From: "edit", To: "commit", Action: "status", Payload: "Show status"},
	}

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs)

	// Should have called Ollama twice: once with tool call, once after block
	if callCount < 2 {
		t.Errorf("expected at least 2 Ollama calls, got %d", callCount)
	}
}

func TestLogToolToHistory(t *testing.T) {
	dir := t.TempDir()
	bus := &BusClient{BusDir: dir, Role: "commit"}

	tc := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"git status"}`),
		},
	}

	logToolToHistory(bus, tc, "On branch main\nnothing to commit")

	data, err := os.ReadFile(filepath.Join(dir, "commit-history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	var entry map[string]interface{}
	json.Unmarshal(data[:len(data)-1], &entry)

	if entry["command"] != "git status" {
		t.Errorf("command = %q", entry["command"])
	}
	if entry["outcome"] != "success" {
		t.Errorf("outcome = %q", entry["outcome"])
	}
}

func TestLogToolToHistory_Failure(t *testing.T) {
	dir := t.TempDir()
	bus := &BusClient{BusDir: dir, Role: "commit"}

	tc := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"git push"}`),
		},
	}

	logToolToHistory(bus, tc, "error: failed to push\nExit code: 1")

	data, _ := os.ReadFile(filepath.Join(dir, "commit-history.jsonl"))
	var entry map[string]interface{}
	json.Unmarshal(data[:len(data)-1], &entry)

	if entry["outcome"] != "failure" {
		t.Errorf("outcome = %q, want failure", entry["outcome"])
	}
	if entry["exit_code"] != "1" {
		t.Errorf("exit_code = %q, want 1", entry["exit_code"])
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	// Verify Run exits cleanly when context is cancelled
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"test-model"}]}`))
			return
		}
	}))
	defer server.Close()

	cfg := Config{
		Role:        "commit",
		Session:     "test-cancel",
		OllamaURL:   server.URL,
		OllamaModel: "test-model",
		MaxTurns:    10,
		BusDir:      t.TempDir(),
		BusBin:      "echo", // no-op
	}

	// Create inbox dir so HasMessages works
	os.MkdirAll(filepath.Join(cfg.BusDir, "inbox"), 0755)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := Run(ctx, cfg)
	if err != nil {
		t.Errorf("Run should return nil on context cancel, got: %v", err)
	}
}

func TestRun_HarnessMarkerLifecycle(t *testing.T) {
	// Verify harness writes marker on startup and removes it on exit
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"test-model"}]}`))
			return
		}
	}))
	defer server.Close()

	busDir := t.TempDir()
	cfg := Config{
		Role:        "build",
		Session:     "test-marker",
		OllamaURL:   server.URL,
		OllamaModel: "test-model",
		MaxTurns:    10,
		BusDir:      busDir,
		BusBin:      "echo",
	}

	// Create inbox dir
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)

	markerPath := filepath.Join(busDir, "harness-build.pid")

	// Run with short timeout so it starts, writes marker, then exits
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Use a goroutine so we can check the marker while Run is active
	markerSeen := false
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, cfg)
	}()

	// Poll for marker creation
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(markerPath); err == nil {
			markerSeen = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for Run to finish
	<-done

	if !markerSeen {
		t.Error("harness marker was never created during Run")
	}

	// After Run returns, marker should be cleaned up via defer
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("harness marker should be removed after Run exits")
	}
}

func TestLooksLikeNarration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty", "", false},
		{"clean success", "Build succeeded: compiled muxcode-agent-bus binary", false},
		{"clean failure", "Build FAILED: missing dependency in bus/agent.go", false},
		{"lets narration", "Let's try running ./build.sh directly and capture its output.", true},
		{"let me narration", "Let me execute the build command now.", true},
		{"i will narration", "I will now run the build script.", true},
		{"ill narration", "I'll run ./build.sh to build the project.", true},
		{"code block narration", "Here's the command:\n```bash\n./build.sh 2>&1\n```\nThis will build the project.", true},
		{"code block with succeeded", "Build succeeded:\n```\nok muxcode-agent-bus\n```", false},
		{"code block with failed", "Build failed:\n```\nerror in main.go\n```", false},
		{"now i need to", "Now I need to run the build command.", true},
		{"going to", "I'm going to execute the build.", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeNarration(tt.input)
			if got != tt.expected {
				t.Errorf("looksLikeNarration(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestProcessBatch_NarrationRecovery(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		switch callCount {
		case 1:
			// First call: tool call to run a command
			resp := ChatResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_1",
									Type: "function",
									Function: FunctionCall{
										Name:      "bash",
										Arguments: json.RawMessage(`{"command":"echo build ok"}`),
									},
								},
							},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case 2:
			// Second call: narration response (the bug)
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "Let's try running the build again."}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		case 3:
			// Third call: summarization recovery (no tools passed)
			resp := ChatResponse{
				Choices: []ChatChoice{
					{Message: ChatMessage{Role: "assistant", Content: "Build succeeded: compiled successfully"}},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	cfg := Config{
		Role:     "build",
		Session:  "test",
		BusDir:   dir,
		MaxTurns: 10,
	}

	ollama := NewOllamaClient(server.URL, "test-model")
	executor := NewExecutor([]string{"Bash(echo *)"})
	tools := BuildToolDefs([]string{"Bash(echo *)"})
	filter := NewFilter("build")
	bus := &BusClient{BusDir: dir, Role: "build", BinPath: "echo"}

	msgs := []Message{
		{ID: "1", From: "edit", To: "build", Action: "build", Payload: "Run build"},
	}

	processBatch(context.Background(), cfg, bus, ollama, executor, tools, "system prompt", filter, msgs)

	// Should have 3 calls: tool call, narration, then summarization recovery
	if callCount != 3 {
		t.Errorf("expected 3 Ollama calls (tool + narration + recovery), got %d", callCount)
	}
}

func TestFormatTask_Integration(t *testing.T) {
	msgs := []Message{
		{
			ID:      "123",
			From:    "edit",
			To:      "commit",
			Action:  "commit",
			Payload: "Stage and commit all current changes with message 'Add feature X'",
		},
	}

	result := FormatTask(msgs)
	if !strings.Contains(result, "commit") {
		t.Error("should contain action")
	}
	if !strings.Contains(result, "edit") {
		t.Error("should contain from")
	}
	if !strings.Contains(result, "Stage and commit") {
		t.Error("should contain payload")
	}
	if !strings.Contains(result, "Do NOT run") {
		t.Error("should contain inbox warning")
	}
}
