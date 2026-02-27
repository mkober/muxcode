package bus

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckOllamaInference_Healthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		// Verify request body
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.MaxTokens != 1 {
			t.Errorf("expected max_tokens=1, got %d", req.MaxTokens)
		}
		if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}

		resp := ChatResponse{
			ID: "test",
			Choices: []ChatChoice{
				{Message: ChatMessage{Role: "assistant", Content: "hello"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	err := CheckOllamaInference(server.URL, "test-model", 5*time.Second)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

func TestCheckOllamaInference_Timeout(t *testing.T) {
	// Server that hangs forever
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Second) // hang
	}))
	defer server.Close()

	err := CheckOllamaInference(server.URL, "test-model", 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "probe failed") {
		t.Errorf("expected 'probe failed' in error, got: %v", err)
	}
}

func TestCheckOllamaInference_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	err := CheckOllamaInference(server.URL, "test-model", 5*time.Second)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("expected 'status 500' in error, got: %v", err)
	}
}

func TestCheckOllamaInference_ConnectionRefused(t *testing.T) {
	err := CheckOllamaInference("http://127.0.0.1:19999", "test-model", 2*time.Second)
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
	if !strings.Contains(err.Error(), "probe failed") {
		t.Errorf("expected 'probe failed' in error, got: %v", err)
	}
}

func TestLocalLLMRoles(t *testing.T) {
	// Save and restore env
	saved := map[string]string{}
	envVars := []string{"MUXCODE_GIT_CLI", "MUXCODE_BUILD_CLI", "MUXCODE_TEST_CLI", "MUXCODE_CUSTOM_CLI"}
	for _, k := range envVars {
		saved[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	// Clear all
	for _, k := range envVars {
		os.Unsetenv(k)
	}

	// No roles set
	roles := LocalLLMRoles()
	if len(roles) != 0 {
		t.Errorf("expected 0 roles, got %d: %v", len(roles), roles)
	}

	// Set some roles
	os.Setenv("MUXCODE_GIT_CLI", "local")
	os.Setenv("MUXCODE_BUILD_CLI", "local")
	os.Setenv("MUXCODE_TEST_CLI", "claude") // not local
	os.Setenv("MUXCODE_CUSTOM_CLI", "local") // custom role

	roles = LocalLLMRoles()

	// Check that commit and build are included
	hasCommit := false
	hasBuild := false
	hasCustom := false
	for _, r := range roles {
		switch r {
		case "commit":
			hasCommit = true
		case "build":
			hasBuild = true
		case "custom":
			hasCustom = true
		}
	}
	if !hasCommit {
		t.Error("expected 'commit' in roles")
	}
	if !hasBuild {
		t.Error("expected 'build' in roles")
	}
	if !hasCustom {
		t.Error("expected 'custom' in roles (generic pattern)")
	}

	// Test should not be included (set to "claude")
	for _, r := range roles {
		if r == "test" {
			t.Error("'test' should not be in roles (not set to local)")
		}
	}
}

func TestLocalLLMRoles_Dedup(t *testing.T) {
	// Both MUXCODE_GIT_CLI and MUXCODE_COMMIT_CLI map to "commit"
	saved := map[string]string{
		"MUXCODE_GIT_CLI":    os.Getenv("MUXCODE_GIT_CLI"),
		"MUXCODE_COMMIT_CLI": os.Getenv("MUXCODE_COMMIT_CLI"),
	}
	defer func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	os.Setenv("MUXCODE_GIT_CLI", "local")
	os.Setenv("MUXCODE_COMMIT_CLI", "local")

	roles := LocalLLMRoles()
	commitCount := 0
	for _, r := range roles {
		if r == "commit" {
			commitCount++
		}
	}
	if commitCount != 1 {
		t.Errorf("expected 'commit' exactly once, got %d", commitCount)
	}
}

func TestFormatOllamaAlert_Down(t *testing.T) {
	result := FormatOllamaAlert("down", []string{"commit", "build"}, "Inference probe timed out after 10s")
	if !strings.Contains(result, "OLLAMA DOWN") {
		t.Error("expected 'OLLAMA DOWN' in output")
	}
	if !strings.Contains(result, "commit, build") {
		t.Error("expected roles in output")
	}
	if !strings.Contains(result, "timed out") {
		t.Error("expected message in output")
	}
}

func TestFormatOllamaAlert_Recovered(t *testing.T) {
	result := FormatOllamaAlert("recovered", []string{"commit"}, "Ollama is responsive again")
	if !strings.Contains(result, "OLLAMA RECOVERED") {
		t.Error("expected 'OLLAMA RECOVERED' in output")
	}
}

func TestFormatOllamaAlert_Restarting(t *testing.T) {
	result := FormatOllamaAlert("restarting", nil, "Attempting restart")
	if !strings.Contains(result, "OLLAMA RESTARTING") {
		t.Error("expected 'OLLAMA RESTARTING' in output")
	}
}

func TestOllamaFailSentinel(t *testing.T) {
	tmpDir := t.TempDir()
	session := "test-sentinel"
	busDir := "/tmp/muxcode-bus-" + session
	lockDir := filepath.Join(busDir, "lock")

	// Create lock dir
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(busDir)
	_ = tmpDir

	// No sentinel initially
	if HasOllamaFailSentinel(session) {
		t.Error("expected no sentinel initially")
	}

	// Write sentinel
	if err := WriteOllamaFailSentinel(session, "commit", 3); err != nil {
		t.Fatalf("failed to write sentinel: %v", err)
	}

	// Should detect sentinel
	if !HasOllamaFailSentinel(session) {
		t.Error("expected sentinel to be detected")
	}

	// Verify path
	path := OllamaFailSentinelPath(session, "commit")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read sentinel: %v", err)
	}
	if !strings.HasPrefix(string(data), "3 ") {
		t.Errorf("expected sentinel to start with '3 ', got: %s", string(data))
	}

	// Clear sentinel
	ClearOllamaFailSentinel(session, "commit")
	if HasOllamaFailSentinel(session) {
		t.Error("expected sentinel to be cleared")
	}
}

func TestOllamaHealthAlertKey(t *testing.T) {
	key := OllamaHealthAlertKey("down")
	if key != "ollama:down" {
		t.Errorf("expected 'ollama:down', got %q", key)
	}

	key = OllamaHealthAlertKey("recovered")
	if key != "ollama:recovered" {
		t.Errorf("expected 'ollama:recovered', got %q", key)
	}
}

func TestFormatOllamaAlert_NoRoles(t *testing.T) {
	result := FormatOllamaAlert("down", nil, "Connection refused")
	if strings.Contains(result, "Affected roles") {
		t.Error("should not show roles when nil")
	}
	if !strings.Contains(result, "Connection refused") {
		t.Error("expected message in output")
	}
}

func TestFormatOllamaAlert_NoMessage(t *testing.T) {
	result := FormatOllamaAlert("recovered", []string{"commit"}, "")
	lines := strings.Split(strings.TrimSpace(result), "\n")
	// Should have header + roles, no message line
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d: %q", len(lines), result)
	}
}

func TestCheckOllamaInference_ZeroTimeout(t *testing.T) {
	// Verify default timeout is used when 0 is passed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatResponse{
			ID:      "test",
			Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: "ok"}}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Should use OllamaProbeTimeout (10s) as default — will succeed quickly
	err := CheckOllamaInference(server.URL, "model", 0)
	if err != nil {
		t.Errorf("expected nil with zero timeout (uses default), got: %v", err)
	}
}

func TestOllamaFailSentinelPath(t *testing.T) {
	path := OllamaFailSentinelPath("mysession", "commit")
	expected := "/tmp/muxcode-bus-mysession/lock/commit.ollama-fail"
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

// TestFormatOllamaAlert_UnknownStatus verifies the fallback formatting.
func TestFormatOllamaAlert_UnknownStatus(t *testing.T) {
	result := FormatOllamaAlert("degraded", []string{"build"}, "Slow responses")
	expected := fmt.Sprintf("ℹ OLLAMA DEGRADED\n  Affected roles: build\n  Slow responses\n")
	if result != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result)
	}
}
