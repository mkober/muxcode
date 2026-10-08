package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStripFrontmatter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
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
		{"plan", "planner"},
		{"planner", "planner"},
		{"edit", "code-editor"},
		{"build", "code-builder"},
		{"test", "test-runner"},
		{"review", "code-reviewer"},
		{"deploy", "infra-deployer"},
		{"run", "command-runner"},
		{"runner", "command-runner"},
		{"commit", "git-manager"},
		{"git", "git-manager"},
		{"analyze", "editor-analyst"},
		{"analyst", "editor-analyst"},
		{"docs", "doc-writer"},
		{"research", "code-researcher"},
		{"watch", "log-watcher"},
		{"pr-read", "pr-reader"},
		{"auto", "autonomous-agent"},
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
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			BaseURL:    server.URL,
			HTTPClient: server.Client(),
			Model:      "test-model",
			Timeout:    10,
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
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			BaseURL:    server.URL,
			HTTPClient: server.Client(),
			Model:      "test-model",
			Timeout:    10,
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

// toolResultSeenByModel runs one bash tool call through processMessages as
// role and returns the tool message the model receives on its next turn.
func toolResultSeenByModel(t *testing.T, role, command string) string {
	t.Helper()
	session := fmt.Sprintf("test-agent-copy-%d", rand.Int())
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = Cleanup(session) })

	args, _ := json.Marshal(map[string]string{"command": command})
	calls, seen := 0, ""
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		msg := ChatMessage{Role: "assistant", Content: "done"}
		if calls == 1 {
			msg = ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Type: "function",
				Function: FunctionCall{Name: "bash", Arguments: args}}}}
		} else if n := len(req.Messages); n > 0 && req.Messages[n-1].Role == "tool" {
			seen = req.Messages[n-1].Content
		}
		_ = json.NewEncoder(w).Encode(ChatResponse{Choices: []ChatChoice{{Message: msg}}})
	}))
	defer server.Close()

	oldCfg := configSingleton
	t.Cleanup(func() { configSingleton = oldCfg })
	SetConfig(&MuxcodeConfig{
		SharedTools:  DefaultConfig().SharedTools,
		ToolProfiles: map[string]ToolProfile{role: {Include: []string{"bus", "readonly", "common"}, Tools: []string{"Bash(echo *)"}}},
		EventChains:  DefaultConfig().EventChains,
		AutoCC:       DefaultConfig().AutoCC,
	})

	cfg := AgentConfig{Role: role, Session: session, Ollama: OllamaConfig{
		BaseURL: server.URL, HTTPClient: server.Client(), Model: "test-model", Timeout: 10,
	}}
	msgs := []Message{NewMessage("edit", role, "request", "test", "run it", "")}
	processMessages(context.Background(), cfg, NewOllamaClient(cfg.Ollama), NewToolExecutor(role), BuildToolDefs(role),
		"You are a test agent", msgs, &agentState{})
	if calls != 2 {
		t.Fatalf("%s: Ollama calls = %d, want 2 (tool call + final)", role, calls)
	}
	return seen
}

// muxcode agent is the one conversation road muxcode owns outright (MUX-203):
// the model reads a tool result redacted for its role under the notice, as the
// history row is. commit is the non-sensitive control — its credential still
// goes, its email stays — and clean output reaches the model unannotated.
func TestProcessMessages_ModelCopyScrubbed(t *testing.T) {
	const secret, email = "sk0123456789abcdefXYZ", "jane.doe@example.com"
	cmd := "echo api_key=" + secret + " " + email

	run := toolResultSeenByModel(t, "run", cmd)
	if strings.Contains(run, secret) || strings.Contains(run, email) || !strings.Contains(run, "[muxcode pii-scrub: 2 value(s)") {
		t.Errorf("run: model copy not scrubbed under the notice: %q", run)
	}
	commit := toolResultSeenByModel(t, "commit", cmd)
	if strings.Contains(commit, secret) || !strings.Contains(commit, email) || !strings.Contains(commit, "[muxcode pii-scrub: 1 value(s)") {
		t.Errorf("commit: want the credential redacted and the email kept: %q", commit)
	}
	clean := toolResultSeenByModel(t, "commit", "echo test-output")
	if strings.Contains(clean, "[muxcode pii-scrub") || !strings.Contains(clean, "test-output") {
		t.Errorf("clean output: want it unannotated, got %q", clean)
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

	// Mock server for health check — done channel unblocks handlers on close
	done := make(chan struct{})
	server := newPipeServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"test-model"}]}`))
			return
		}
		// Chat endpoint — block until test completes
		<-done
	}))
	defer func() {
		close(done)
		server.Close()
	}()

	cfg := AgentConfig{
		Role:    "commit",
		Session: session,
		Ollama: OllamaConfig{
			BaseURL:    server.URL,
			HTTPClient: server.Client(),
			Model:      "test-model",
			Timeout:    10,
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

// The `muxcode agent` loop is a history road too (MUX-179): a key in a bash
// result is stored redacted for the role.
func TestLogBashToHistory_ScrubsCredentials(t *testing.T) {
	session := fmt.Sprintf("test-agent-scrub-%d", rand.Int())
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Cleanup(session) }()

	tc := ToolCall{Function: FunctionCall{Name: "bash", Arguments: json.RawMessage(`{"command":"env"}`)}}
	const key = "sk-fake-0123456789abcdef"
	logBashToHistory(AgentConfig{Role: "commit", Session: session}, tc, "HOME=/tmp\nOPENAI_API_KEY="+key)

	data, err := os.ReadFile(HistoryPath(session, "commit"))
	if err != nil {
		t.Fatalf("history file not created: %v", err)
	}
	if strings.Contains(string(data), key) {
		t.Errorf("key reached commit-history: %s", data)
	}
	if !strings.Contains(string(data), "[muxcode pii-scrub:") {
		t.Errorf("redacted row lacks the notice banner: %s", data)
	}
}
