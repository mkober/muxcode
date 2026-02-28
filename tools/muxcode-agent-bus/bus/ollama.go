package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrModelNotFound is returned by CheckHealth when Ollama is reachable
// but the configured model is not pulled locally.
var ErrModelNotFound = errors.New("model not found")

// OllamaConfig holds configuration for connecting to Ollama's API.
type OllamaConfig struct {
	BaseURL     string  // default "http://localhost:11434"
	Model       string  // default "qwen2.5:7b" (must support tool calling)
	Temperature float64 // default 0.1
	Timeout     int     // seconds, default 120
	MaxTokens   int     // default 4096
}

// DefaultOllamaConfig returns the default Ollama configuration.
// Values can be overridden via environment variables.
func DefaultOllamaConfig() OllamaConfig {
	cfg := OllamaConfig{
		BaseURL:     "http://localhost:11434",
		Model:       "qwen2.5:7b",
		Temperature: 0.1,
		Timeout:     120,
		MaxTokens:   4096,
	}
	if v := os.Getenv("MUXCODE_OLLAMA_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("MUXCODE_OLLAMA_MODEL"); v != "" {
		cfg.Model = v
	}
	return cfg
}

// RoleModel returns the Ollama model for a specific role, checking
// per-role env vars before falling back to the default config model.
// Resolution order: MUXCODE_{ROLE}_MODEL → MUXCODE_OLLAMA_MODEL → default.
func RoleModel(role string) string {
	envVar := roleModelEnvVar(role)
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return DefaultOllamaConfig().Model
}

// roleModelEnvVar returns the per-role model env var name.
// Maps role names to env var names: commit→MUXCODE_GIT_MODEL, etc.
func roleModelEnvVar(role string) string {
	switch role {
	case "commit", "git":
		return "MUXCODE_GIT_MODEL"
	case "build":
		return "MUXCODE_BUILD_MODEL"
	case "test":
		return "MUXCODE_TEST_MODEL"
	case "review":
		return "MUXCODE_REVIEW_MODEL"
	case "deploy":
		return "MUXCODE_DEPLOY_MODEL"
	case "edit":
		return "MUXCODE_EDIT_MODEL"
	case "analyze", "analyst":
		return "MUXCODE_ANALYZE_MODEL"
	case "docs":
		return "MUXCODE_DOCS_MODEL"
	case "research":
		return "MUXCODE_RESEARCH_MODEL"
	case "watch":
		return "MUXCODE_WATCH_MODEL"
	default:
		return "MUXCODE_" + strings.ToUpper(role) + "_MODEL"
	}
}

// ChatMessage represents a message in the Ollama chat conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool invocation requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the function name and arguments for a tool call.
// Arguments is stored as json.RawMessage for flexible deserialization,
// but Ollama's API expects arguments as a JSON string when sent back
// in conversation history — see MarshalJSON.
type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// MarshalJSON implements custom marshaling for FunctionCall.
// Ollama's OpenAI-compatible API expects tool_calls.function.arguments
// to be a JSON-encoded string (e.g. "{\"command\":\"ls\"}"), but we
// store arguments as json.RawMessage (raw object). This converts the
// raw object to a string on serialization so Ollama can unmarshal it.
func (f FunctionCall) MarshalJSON() ([]byte, error) {
	// If arguments is already a JSON string, marshal as-is
	if len(f.Arguments) > 0 && f.Arguments[0] == '"' {
		type plain struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		return json.Marshal(plain{Name: f.Name, Arguments: f.Arguments})
	}
	// Convert raw JSON object/array to a JSON-encoded string
	return json.Marshal(struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{
		Name:      f.Name,
		Arguments: string(f.Arguments),
	})
}

// ToolDef defines a tool available to the model.
type ToolDef struct {
	Type     string         `json:"type"`
	Function ToolDefFunction `json:"function"`
}

// ToolDefFunction holds the function metadata for a tool definition.
type ToolDefFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ChatRequest is the request body for Ollama's OpenAI-compatible API.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Tools       []ToolDef     `json:"tools,omitempty"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

// ChatResponse is the response from Ollama's OpenAI-compatible API.
type ChatResponse struct {
	ID      string         `json:"id"`
	Choices []ChatChoice   `json:"choices"`
	Usage   *ChatUsage     `json:"usage,omitempty"`
	Error   *OllamaError   `json:"error,omitempty"`
}

// ChatChoice is a single choice in the chat response.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage reports token usage for the request.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OllamaError is an error response from the API.
type OllamaError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// OllamaClient wraps HTTP calls to Ollama's OpenAI-compatible API.
type OllamaClient struct {
	Config OllamaConfig
	HTTP   *http.Client
}

// NewOllamaClient creates a new Ollama client with the given config.
func NewOllamaClient(cfg OllamaConfig) *OllamaClient {
	return &OllamaClient{
		Config: cfg,
		HTTP: &http.Client{
			Timeout: time.Duration(cfg.Timeout) * time.Second,
		},
	}
}

// ChatComplete sends a chat completion request with tool definitions.
// Retries up to 3 times with exponential backoff on connection errors.
func (c *OllamaClient) ChatComplete(ctx context.Context, messages []ChatMessage, tools []ToolDef) (*ChatResponse, error) {
	req := ChatRequest{
		Model:       c.Config.Model,
		Messages:    messages,
		Tools:       tools,
		Stream:      false,
		Temperature: c.Config.Temperature,
		MaxTokens:   c.Config.MaxTokens,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	url := c.Config.BaseURL + "/v1/chat/completions"
	var lastErr error
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := 0; attempt <= len(backoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff[attempt-1]):
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			lastErr = err
			continue // retry on connection errors
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("reading response: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
			// Don't retry on 4xx client errors
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return nil, lastErr
			}
			continue
		}

		var chatResp ChatResponse
		if err := json.Unmarshal(respBody, &chatResp); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}

		if chatResp.Error != nil {
			return nil, fmt.Errorf("API error: %s", chatResp.Error.Message)
		}

		return &chatResp, nil
	}

	return nil, fmt.Errorf("all retries exhausted: %w", lastErr)
}

// CheckHealth verifies that Ollama is reachable and the configured model is available.
// Uses GET /api/tags to list models, then checks if the configured model is present.
func (c *OllamaClient) CheckHealth(ctx context.Context) error {
	url := c.Config.BaseURL + "/api/tags"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating health request: %w", err)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("connecting to Ollama at %s: %w", c.Config.BaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama health check returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading health response: %w", err)
	}

	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &tags); err != nil {
		return fmt.Errorf("decoding tags response: %w", err)
	}

	// Check if configured model is available (exact match or prefix match for tag variants)
	// e.g. "qwen2.5:7b" matches "qwen2.5:7b", and "qwen2.5" matches "qwen2.5:latest"
	for _, m := range tags.Models {
		if m.Name == c.Config.Model {
			return nil
		}
		// Match base name without tag only when config has NO explicit tag
		// e.g. config "qwen2.5" matches "qwen2.5:latest" but
		// config "qwen2.5:7b" does NOT match "qwen2.5:0.5b"
		if !strings.Contains(c.Config.Model, ":") && strings.Contains(m.Name, ":") {
			if strings.SplitN(m.Name, ":", 2)[0] == c.Config.Model {
				return nil
			}
		}
	}

	// List available models in error message
	var names []string
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	if len(names) == 0 {
		return fmt.Errorf("%w: %q — no models available", ErrModelNotFound, c.Config.Model)
	}
	return fmt.Errorf("%w: %q (available: %v)", ErrModelNotFound, c.Config.Model, names)
}

// PullModel pulls the configured model via `ollama pull`. Streams progress
// to stderr so the user can see download status. Returns nil on success.
func (c *OllamaClient) PullModel(ctx context.Context) error {
	model := c.Config.Model
	fmt.Fprintf(os.Stderr, "[agent] Pulling model %q...\n", model)

	cmd := exec.CommandContext(ctx, "ollama", "pull", model)
	cmd.Stdout = os.Stderr // progress goes to stderr (stdout is for bus protocol)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ollama pull %s failed: %w", model, err)
	}

	fmt.Fprintf(os.Stderr, "[agent] Model %q pulled successfully\n", model)
	return nil
}
