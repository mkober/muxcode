package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStripFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
	}{
		{
			name:  "with frontmatter",
			input: "---\ntitle: Test\n---\nBody content here",
			want:  "Body content here",
		},
		{
			name:  "no frontmatter",
			input: "Just plain text",
			want:  "Just plain text",
		},
		{
			name:  "empty frontmatter",
			input: "---\n---\nBody",
			want:  "Body",
		},
		{
			name:  "unclosed frontmatter",
			input: "---\ntitle: Test\nno closing",
			want:  "---\ntitle: Test\nno closing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripFrontmatter(tt.input)
			if got != tt.want {
				t.Errorf("stripFrontmatter(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAgentFileName(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"edit", "code-editor"},
		{"build", "code-builder"},
		{"test", "test-runner"},
		{"review", "code-reviewer"},
		{"deploy", "infra-deployer"},
		{"runner", "command-runner"},
		{"git", "git-manager"},
		{"commit", "git-manager"},
		{"analyst", "editor-analyst"},
		{"analyze", "editor-analyst"},
		{"docs", "doc-writer"},
		{"research", "code-researcher"},
		{"watch", "log-watcher"},
		{"pr-read", "pr-reader"},
		{"custom", "custom"},
	}

	for _, tt := range tests {
		got := agentFileName(tt.role)
		if got != tt.want {
			t.Errorf("agentFileName(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}

func TestProcessMessages_SimpleResponse(t *testing.T) {
	session := fmt.Sprintf("test-agent-%d", rand.Int())
	memDir := t.TempDir()
	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Cleanup(session) }()

	// Mock Ollama server that returns a simple text response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Choices: []ChatChoice{
				{
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Status: clean working tree",
					},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := AgentConfig{
		Role:    "commit",
		Session: session,
		Ollama: OllamaConfig{
			BaseURL: server.URL,
			Model:   "test-model",
			Timeout: 10,
		},
	}

	client := NewOllamaClient(cfg.Ollama)
	executor := NewToolExecutor(cfg.Role)
	tools := BuildToolDefs(cfg.Role)

	msgs := []Message{
		NewMessage("edit", "commit", "request", "status", "Show git status", ""),
	}

	state := &agentState{}
	processMessages(context.Background(), cfg, client, executor, tools, "You are a test agent", msgs, state)

	// Verify response was sent to edit's inbox
	editMsgs, err := Peek(session, "edit")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if len(editMsgs) == 0 {
		t.Fatal("expected response message in edit inbox")
	}

	found := false
	for _, m := range editMsgs {
		if m.From == "commit" && m.Type == "response" {
			found = true
			if m.Payload != "Status: clean working tree" {
				t.Errorf("payload = %q, want 'Status: clean working tree'", m.Payload)
			}
		}
	}
	if !found {
		t.Error("did not find response from commit in edit inbox")
	}
}

func TestProcessMessages_WithToolCall(t *testing.T) {
	session := fmt.Sprintf("test-agent-tc-%d", rand.Int())
	memDir := t.TempDir()
	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Cleanup(session) }()

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)

		var resp ChatResponse
		if callCount == 1 {
			// First call: request tool execution
			resp = ChatResponse{
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
										Arguments: json.RawMessage(`{"command":"echo test-output"}`),
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			}
		} else {
			// Second call: final response after tool result
			resp = ChatResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role:    "assistant",
							Content: "Command output: test-output",
						},
						FinishReason: "stop",
					},
				},
			}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Save and restore config singleton for test tool profiles
	oldCfg := configSingleton
	defer func() { configSingleton = oldCfg }()

	SetConfig(&MuxcodeConfig{
		SharedTools: DefaultConfig().SharedTools,
		ToolProfiles: map[string]ToolProfile{
			"commit": {
				Include: []string{"bus", "readonly", "common"},
				Tools:   []string{"Bash(echo *)", "Bash(git *)"},
			},
		},
		EventChains: DefaultConfig().EventChains,
		AutoCC:      DefaultConfig().AutoCC,
	})

	cfg := AgentConfig{
		Role:    "commit",
		Session: session,
		Ollama: OllamaConfig{
			BaseURL: server.URL,
			Model:   "test-model",
			Timeout: 10,
		},
	}

	client := NewOllamaClient(cfg.Ollama)
	executor := NewToolExecutor(cfg.Role)
	tools := BuildToolDefs(cfg.Role)

	msgs := []Message{
		NewMessage("edit", "commit", "request", "test", "Run echo test-output", ""),
	}

	state := &agentState{}
	processMessages(context.Background(), cfg, client, executor, tools, "You are a test agent", msgs, state)

	if callCount != 2 {
		t.Errorf("Ollama calls = %d, want 2 (tool call + final)", callCount)
	}

	// Verify response
	editMsgs, _ := Peek(session, "edit")
	found := false
	for _, m := range editMsgs {
		if m.From == "commit" && m.Type == "response" {
			found = true
		}
	}
	if !found {
		t.Error("did not find response from commit in edit inbox")
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	prompt := buildSystemPrompt("commit")
	// Should at least include the shared coordination prompt
	if prompt == "" {
		t.Error("expected non-empty system prompt")
	}
	if len(prompt) < 100 {
		t.Errorf("system prompt too short (%d chars), expected substantial content", len(prompt))
	}
}

func TestAgentLoop_ContextCancel(t *testing.T) {
	session := fmt.Sprintf("test-agent-cancel-%d", rand.Int())
	memDir := t.TempDir()
	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Cleanup(session) }()

	// Mock server for health check
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"test-model"}]}`))
			return
		}
		// Chat endpoint — shouldn't be called if we cancel quickly
		time.Sleep(10 * time.Second)
	}))
	defer server.Close()

	cfg := AgentConfig{
		Role:    "commit",
		Session: session,
		Ollama: OllamaConfig{
			BaseURL: server.URL,
			Model:   "test-model",
			Timeout: 10,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := AgentLoop(ctx, cfg)
	if err != nil {
		t.Errorf("AgentLoop error: %v (expected clean exit)", err)
	}
}

func TestLogBashToHistory(t *testing.T) {
	session := fmt.Sprintf("test-agent-log-%d", rand.Int())
	memDir := t.TempDir()
	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Cleanup(session) }()

	cfg := AgentConfig{
		Role:    "commit",
		Session: session,
	}

	tc := ToolCall{
		Function: FunctionCall{
			Name:      "bash",
			Arguments: json.RawMessage(`{"command":"git status"}`),
		},
	}

	logBashToHistory(cfg, tc, "On branch main\nnothing to commit")

	// Verify history file was written
	historyPath := HistoryPath(session, "commit")
	data, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatalf("history file not created: %v", err)
	}
	if len(data) == 0 {
		t.Error("history file is empty")
	}
	if !strings.Contains(string(data), "git status") {
		t.Errorf("history should contain 'git status', got: %s", string(data))
	}
}
