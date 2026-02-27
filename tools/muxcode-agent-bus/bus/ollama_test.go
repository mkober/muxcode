package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultOllamaConfig(t *testing.T) {
	cfg := DefaultOllamaConfig()
	if cfg.BaseURL != "http://localhost:11434" {
		t.Errorf("BaseURL = %q, want http://localhost:11434", cfg.BaseURL)
	}
	if cfg.Model != "qwen2.5:7b" {
		t.Errorf("Model = %q, want qwen2.5:7b", cfg.Model)
	}
	if cfg.Temperature != 0.1 {
		t.Errorf("Temperature = %f, want 0.1", cfg.Temperature)
	}
	if cfg.Timeout != 120 {
		t.Errorf("Timeout = %d, want 120", cfg.Timeout)
	}
	if cfg.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", cfg.MaxTokens)
	}
}

func TestChatComplete_SimpleResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}

		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		if req.Model != "test-model" {
			t.Errorf("model = %q, want test-model", req.Model)
		}
		if req.Stream {
			t.Error("stream should be false")
		}
		if len(req.Messages) != 1 {
			t.Errorf("messages count = %d, want 1", len(req.Messages))
		}

		resp := ChatResponse{
			ID: "test-id",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Hello from Ollama!",
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL:     server.URL,
		Model:       "test-model",
		Temperature: 0.1,
		Timeout:     10,
		MaxTokens:   100,
	})

	messages := []ChatMessage{
		{Role: "user", Content: "Hello"},
	}

	resp, err := client.ChatComplete(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("ChatComplete: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello from Ollama!" {
		t.Errorf("content = %q, want 'Hello from Ollama!'", resp.Choices[0].Message.Content)
	}
}

func TestChatComplete_WithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			Choices: []ChatChoice{
				{
					Message: ChatMessage{
						Role: "assistant",
						ToolCalls: []ToolCall{
							{
								ID:   "call_123",
								Type: "function",
								Function: FunctionCall{
									Name:      "bash",
									Arguments: json.RawMessage(`{"command":"git status"}`),
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		Timeout: 10,
	})

	resp, err := client.ChatComplete(context.Background(), []ChatMessage{{Role: "user", Content: "run git status"}}, nil)
	if err != nil {
		t.Fatalf("ChatComplete: %v", err)
	}

	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %d, want 1", len(resp.Choices[0].Message.ToolCalls))
	}

	tc := resp.Choices[0].Message.ToolCalls[0]
	if tc.Function.Name != "bash" {
		t.Errorf("function name = %q, want bash", tc.Function.Name)
	}
	if tc.ID != "call_123" {
		t.Errorf("id = %q, want call_123", tc.ID)
	}
}

func TestChatComplete_WithToolDefs(t *testing.T) {
	var receivedTools []ToolDef

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		receivedTools = req.Tools

		resp := ChatResponse{
			Choices: []ChatChoice{
				{Message: ChatMessage{Role: "assistant", Content: "ok"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		Timeout: 10,
	})

	tools := []ToolDef{
		{
			Type: "function",
			Function: ToolDefFunction{
				Name:        "bash",
				Description: "Run a bash command",
				Parameters:  map[string]interface{}{"type": "object"},
			},
		},
	}

	_, err := client.ChatComplete(context.Background(), []ChatMessage{{Role: "user", Content: "test"}}, tools)
	if err != nil {
		t.Fatalf("ChatComplete: %v", err)
	}

	if len(receivedTools) != 1 {
		t.Fatalf("tools sent = %d, want 1", len(receivedTools))
	}
	if receivedTools[0].Function.Name != "bash" {
		t.Errorf("tool name = %q, want bash", receivedTools[0].Function.Name)
	}
}

func TestChatComplete_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"message": "invalid model"}}`))
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "bad-model",
		Timeout: 10,
	})

	_, err := client.ChatComplete(context.Background(), []ChatMessage{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q, want status 400", err.Error())
	}
}

func TestChatComplete_RetryOnServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`server error`))
			return
		}
		resp := ChatResponse{
			Choices: []ChatChoice{
				{Message: ChatMessage{Role: "assistant", Content: "retried ok"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		Timeout: 30,
	})

	// Use a context with a generous timeout since we'll have retries
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := client.ChatComplete(ctx, []ChatMessage{{Role: "user", Content: "test"}}, nil)
	if err != nil {
		t.Fatalf("ChatComplete: %v", err)
	}

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if resp.Choices[0].Message.Content != "retried ok" {
		t.Errorf("content = %q, want 'retried ok'", resp.Choices[0].Message.Content)
	}
}

func TestChatComplete_NoRetryOn400(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad request`))
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		Timeout: 10,
	})

	_, err := client.ChatComplete(context.Background(), []ChatMessage{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}

func TestCheckHealth_ModelAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %s, want /api/tags", r.URL.Path)
		}
		resp := `{"models":[{"name":"qwen2.5-coder:7b"},{"name":"llama3:8b"}]}`
		w.Write([]byte(resp))
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "qwen2.5-coder:7b",
		Timeout: 10,
	})

	err := client.CheckHealth(context.Background())
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
}

func TestCheckHealth_ModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := `{"models":[{"name":"llama3:8b"}]}`
		w.Write([]byte(resp))
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "nonexistent-model",
		Timeout: 10,
	})

	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error for missing model")
	}
	if !strings.Contains(err.Error(), "nonexistent-model") {
		t.Errorf("error = %q, want model name in message", err.Error())
	}
	if !strings.Contains(err.Error(), "llama3:8b") {
		t.Errorf("error = %q, want available models listed", err.Error())
	}
}

func TestCheckHealth_NoModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "any-model",
		Timeout: 10,
	})

	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error when no models available")
	}
	if !strings.Contains(err.Error(), "no models available") {
		t.Errorf("error = %q, want 'no models available'", err.Error())
	}
}

func TestCheckHealth_ConnectionError(t *testing.T) {
	client := NewOllamaClient(OllamaConfig{
		BaseURL: "http://127.0.0.1:1", // invalid port
		Model:   "any-model",
		Timeout: 1,
	})

	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

func TestChatComplete_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // slow response
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "test-model",
		Timeout: 10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.ChatComplete(ctx, []ChatMessage{{Role: "user", Content: "test"}}, nil)
	if err == nil {
		t.Fatal("expected error when context cancelled")
	}
}

func TestCheckHealth_ModelNotFound_IsErrModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"name":"llama3:8b"}]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "missing-model",
		Timeout: 10,
	})

	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("error should wrap ErrModelNotFound, got: %v", err)
	}
}

func TestCheckHealth_NoModels_IsErrModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(OllamaConfig{
		BaseURL: server.URL,
		Model:   "any-model",
		Timeout: 10,
	})

	err := client.CheckHealth(context.Background())
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("error should wrap ErrModelNotFound, got: %v", err)
	}
}

func TestCheckHealth_ConnectionError_IsNotErrModelNotFound(t *testing.T) {
	client := NewOllamaClient(OllamaConfig{
		BaseURL: "http://127.0.0.1:1",
		Model:   "any-model",
		Timeout: 1,
	})

	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrModelNotFound) {
		t.Error("connection error should NOT be ErrModelNotFound")
	}
}

func TestRoleModel_Default(t *testing.T) {
	// With no env vars set, should return default model
	t.Setenv("MUXCODE_OLLAMA_MODEL", "")
	t.Setenv("MUXCODE_GIT_MODEL", "")
	model := RoleModel("commit")
	if model != "qwen2.5:7b" {
		t.Errorf("RoleModel(commit) = %q, want qwen2.5:7b", model)
	}
}

func TestRoleModel_GlobalOverride(t *testing.T) {
	t.Setenv("MUXCODE_OLLAMA_MODEL", "llama3.1:8b")
	t.Setenv("MUXCODE_GIT_MODEL", "")
	model := RoleModel("commit")
	if model != "llama3.1:8b" {
		t.Errorf("RoleModel(commit) = %q, want llama3.1:8b", model)
	}
}

func TestRoleModel_PerRoleOverride(t *testing.T) {
	t.Setenv("MUXCODE_OLLAMA_MODEL", "llama3.1:8b")
	t.Setenv("MUXCODE_GIT_MODEL", "mistral-nemo")
	model := RoleModel("commit")
	if model != "mistral-nemo" {
		t.Errorf("RoleModel(commit) = %q, want mistral-nemo (per-role takes priority)", model)
	}
}

func TestRoleModel_RoleMapping(t *testing.T) {
	tests := []struct {
		role   string
		envVar string
	}{
		{"commit", "MUXCODE_GIT_MODEL"},
		{"git", "MUXCODE_GIT_MODEL"},
		{"build", "MUXCODE_BUILD_MODEL"},
		{"test", "MUXCODE_TEST_MODEL"},
		{"review", "MUXCODE_REVIEW_MODEL"},
		{"deploy", "MUXCODE_DEPLOY_MODEL"},
		{"edit", "MUXCODE_EDIT_MODEL"},
		{"analyze", "MUXCODE_ANALYZE_MODEL"},
		{"analyst", "MUXCODE_ANALYZE_MODEL"},
		{"docs", "MUXCODE_DOCS_MODEL"},
		{"research", "MUXCODE_RESEARCH_MODEL"},
		{"watch", "MUXCODE_WATCH_MODEL"},
		{"custom", "MUXCODE_CUSTOM_MODEL"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			t.Setenv("MUXCODE_OLLAMA_MODEL", "")
			t.Setenv(tt.envVar, "role-specific-model")
			model := RoleModel(tt.role)
			if model != "role-specific-model" {
				t.Errorf("RoleModel(%s) = %q, want role-specific-model (via %s)", tt.role, model, tt.envVar)
			}
		})
	}
}

// Suppress unused import warning for rand and fmt in this test file
var _ = rand.Int
var _ = fmt.Sprintf
