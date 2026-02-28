package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrModelNotFound is returned when the configured model is not available.
var ErrModelNotFound = errors.New("model not found")

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
	ID      string       `json:"id"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
	Error   *OllamaError `json:"error,omitempty"`
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
	BaseURL     string
	Model       string
	Temperature float64
	MaxTokens   int
	HTTP        *http.Client
}

// NewOllamaClient creates a new Ollama client.
func NewOllamaClient(url, model string) *OllamaClient {
	return &OllamaClient{
		BaseURL:     url,
		Model:       model,
		Temperature: 0.1,
		MaxTokens:   4096,
		HTTP: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// ChatComplete sends a chat completion request with tool definitions.
// Retries up to 3 times with exponential backoff on connection errors.
func (c *OllamaClient) ChatComplete(ctx context.Context, messages []ChatMessage, tools []ToolDef) (*ChatResponse, error) {
	req := ChatRequest{
		Model:       c.Model,
		Messages:    messages,
		Tools:       tools,
		Stream:      false,
		Temperature: c.Temperature,
		MaxTokens:   c.MaxTokens,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	url := c.BaseURL + "/v1/chat/completions"
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
func (c *OllamaClient) CheckHealth(ctx context.Context) error {
	url := c.BaseURL + "/api/tags"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating health request: %w", err)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("connecting to Ollama at %s: %w", c.BaseURL, err)
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

	// Check if configured model is available
	for _, m := range tags.Models {
		if m.Name == c.Model {
			return nil
		}
		// Fuzzy: config "qwen2.5" matches "qwen2.5:latest"
		if !strings.Contains(c.Model, ":") && strings.Contains(m.Name, ":") {
			if strings.SplitN(m.Name, ":", 2)[0] == c.Model {
				return nil
			}
		}
	}

	var names []string
	for _, m := range tags.Models {
		names = append(names, m.Name)
	}
	if len(names) == 0 {
		return fmt.Errorf("%w: %q — no models available", ErrModelNotFound, c.Model)
	}
	return fmt.Errorf("%w: %q (available: %v)", ErrModelNotFound, c.Model, names)
}
