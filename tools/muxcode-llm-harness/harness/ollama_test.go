package harness

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewOllamaClient(t *testing.T) {
	c := NewOllamaClient("http://localhost:11434", "test-model")
	if c.BaseURL != "http://localhost:11434" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.Model != "test-model" {
		t.Errorf("Model = %q", c.Model)
	}
	if c.Temperature != 0.1 {
		t.Errorf("Temperature = %f", c.Temperature)
	}
	if c.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d", c.MaxTokens)
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
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "test-model" {
			t.Errorf("model = %q, want test-model", req.Model)
		}
		if req.Stream {
			t.Error("stream should be false")
		}

		resp := ChatResponse{
			ID: "test-id",
			Choices: []ChatChoice{
				{
					Index:   0,
					Message: ChatMessage{Role: "assistant", Content: "Hello from Ollama!"},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "test-model")
	resp, err := client.ChatComplete(context.Background(), []ChatMessage{{Role: "user", Content: "Hello"}}, nil)
	if err != nil {
		t.Fatalf("ChatComplete: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello from Ollama!" {
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
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
								ID:   "call_1",
								Type: "function",
								Function: FunctionCall{
									Name:      "bash",
									Arguments: json.RawMessage(`{"command":"git status"}`),
								},
							},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "test-model")
	resp, err := client.ChatComplete(context.Background(), []ChatMessage{{Role: "user", Content: "test"}}, nil)
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
}

func TestChatComplete_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"message": "invalid model"}}`))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "bad-model")
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

	client := NewOllamaClient(server.URL, "test-model")
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
		t.Errorf("content = %q", resp.Choices[0].Message.Content)
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

	client := NewOllamaClient(server.URL, "test-model")
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
		w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:7b"},{"name":"llama3:8b"}]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "qwen2.5-coder:7b")
	if err := client.CheckHealth(context.Background()); err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
}

func TestCheckHealth_FuzzyMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:latest"}]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "qwen2.5-coder")
	if err := client.CheckHealth(context.Background()); err != nil {
		t.Fatalf("CheckHealth fuzzy match: %v", err)
	}
}

func TestCheckHealth_ModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"name":"llama3:8b"}]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "nonexistent-model")
	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("error should wrap ErrModelNotFound, got: %v", err)
	}
}

func TestCheckHealth_NoModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	client := NewOllamaClient(server.URL, "any-model")
	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("error should wrap ErrModelNotFound, got: %v", err)
	}
}

func TestCheckHealth_ConnectionError(t *testing.T) {
	client := NewOllamaClient("http://127.0.0.1:1", "any-model")
	err := client.CheckHealth(context.Background())
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
	if errors.Is(err, ErrModelNotFound) {
		t.Error("connection error should NOT be ErrModelNotFound")
	}
}
