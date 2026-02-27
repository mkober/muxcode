package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// OllamaProbeTimeout is the default timeout for inference health probes.
	OllamaProbeTimeout = 10 * time.Second
	// OllamaRestartReadyTimeout is how long to wait for Ollama readiness after restart.
	OllamaRestartReadyTimeout = 15 * time.Second
	// OllamaRestartReadyPoll is the poll interval when waiting for Ollama readiness.
	OllamaRestartReadyPoll = 500 * time.Millisecond
)

// OllamaHealthStatus represents the result of an Ollama health check.
type OllamaHealthStatus struct {
	Healthy   bool     `json:"healthy"`
	Roles     []string `json:"roles"`
	Error     string   `json:"error,omitempty"`
	ProbeTime int64    `json:"probe_time_ms"`
}

// CheckOllamaInference sends a minimal chat completion to distinguish
// "process alive but stuck" from "process healthy". Uses a fresh HTTP client
// with a short timeout to avoid sharing the agent's long-timeout client.
func CheckOllamaInference(baseURL, model string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = OllamaProbeTimeout
	}

	client := &http.Client{Timeout: timeout}

	req := ChatRequest{
		Model: model,
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
		Stream:    false,
		MaxTokens: 1,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encoding probe request: %w", err)
	}

	url := baseURL + "/v1/chat/completions"
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating probe request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("inference probe failed after %dms: %w",
			time.Since(start).Milliseconds(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inference probe returned status %d after %dms",
			resp.StatusCode, time.Since(start).Milliseconds())
	}

	return nil
}

// roleEnvMap maps MUXCODE_{NAME}_CLI env var names to agent roles.
var roleEnvMap = map[string]string{
	"MUXCODE_GIT_CLI":      "commit",
	"MUXCODE_BUILD_CLI":    "build",
	"MUXCODE_TEST_CLI":     "test",
	"MUXCODE_REVIEW_CLI":   "review",
	"MUXCODE_DEPLOY_CLI":   "deploy",
	"MUXCODE_RUN_CLI":      "run",
	"MUXCODE_ANALYZE_CLI":  "analyze",
	"MUXCODE_DOCS_CLI":     "docs",
	"MUXCODE_RESEARCH_CLI": "research",
	"MUXCODE_WATCH_CLI":    "watch",
	"MUXCODE_COMMIT_CLI":   "commit",
}

// LocalLLMRoles returns the list of agent roles configured to use a local LLM.
// Reads MUXCODE_*_CLI=local environment variables to determine which roles
// are using Ollama instead of Claude Code.
func LocalLLMRoles() []string {
	seen := make(map[string]bool)
	var roles []string

	// Check known mappings
	for envVar, role := range roleEnvMap {
		if os.Getenv(envVar) == "local" {
			if !seen[role] {
				seen[role] = true
				roles = append(roles, role)
			}
		}
	}

	// Check generic MUXCODE_{ROLE}_CLI pattern for custom roles
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 || parts[1] != "local" {
			continue
		}
		key := parts[0]
		if !strings.HasPrefix(key, "MUXCODE_") || !strings.HasSuffix(key, "_CLI") {
			continue
		}
		// Already handled above
		if _, ok := roleEnvMap[key]; ok {
			continue
		}
		// Extract role name: MUXCODE_FOOBAR_CLI → foobar
		rolePart := key[len("MUXCODE_") : len(key)-len("_CLI")]
		role := strings.ToLower(rolePart)
		if !seen[role] {
			seen[role] = true
			roles = append(roles, role)
		}
	}

	return roles
}

// RestartOllama kills the current Ollama process and starts a new one.
// Polls /api/tags to verify readiness before returning.
func RestartOllama(ctx context.Context, ollamaURL string) error {
	// Kill existing Ollama processes
	killCmd := exec.CommandContext(ctx, "pkill", "-f", "ollama serve")
	_ = killCmd.Run() // ignore error if no process found

	// Wait for process to die
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}

	// Start Ollama in background, detached from this process
	serveCmd := exec.Command("ollama", "serve")
	serveCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	serveCmd.Stdout = nil
	serveCmd.Stderr = nil
	if err := serveCmd.Start(); err != nil {
		return fmt.Errorf("starting ollama serve: %w", err)
	}
	// Detach — don't wait for it
	go func() { _ = serveCmd.Wait() }()

	// Poll for readiness
	readyURL := ollamaURL + "/api/tags"
	deadline := time.Now().Add(OllamaRestartReadyTimeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(OllamaRestartReadyPoll)
	}

	return fmt.Errorf("Ollama did not become ready within %s", OllamaRestartReadyTimeout)
}

// RestartLocalAgent sends C-c to interrupt a stuck agent and relaunches it.
// Uses tmux send-keys to target the agent's pane.
func RestartLocalAgent(session, role string) error {
	target := PaneTarget(session, role)

	// Send C-c to interrupt
	interruptCmd := exec.Command("tmux", "send-keys", "-t", target, "C-c", "")
	if err := interruptCmd.Run(); err != nil {
		return fmt.Errorf("interrupting agent %s: %w", role, err)
	}

	// Wait for process to exit
	time.Sleep(500 * time.Millisecond)

	// Relaunch agent
	launchCmd := fmt.Sprintf("muxcode-agent.sh %s", role)
	relaunchCmd := exec.Command("tmux", "send-keys", "-t", target, launchCmd, "Enter")
	if err := relaunchCmd.Run(); err != nil {
		return fmt.Errorf("relaunching agent %s: %w", role, err)
	}

	return nil
}

// OllamaFailSentinelPath returns the path for a role's Ollama failure sentinel.
func OllamaFailSentinelPath(session, role string) string {
	return filepath.Join(BusDir(session), "lock", role+".ollama-fail")
}

// WriteOllamaFailSentinel writes a failure sentinel for a role.
// Format: "{failCount} {unix_timestamp}"
func WriteOllamaFailSentinel(session, role string, failCount int) error {
	path := OllamaFailSentinelPath(session, role)
	content := fmt.Sprintf("%d %d", failCount, time.Now().Unix())
	return os.WriteFile(path, []byte(content), 0644)
}

// ClearOllamaFailSentinel removes a role's failure sentinel.
func ClearOllamaFailSentinel(session, role string) {
	_ = os.Remove(OllamaFailSentinelPath(session, role))
}

// HasOllamaFailSentinel checks if any role has a failure sentinel.
func HasOllamaFailSentinel(session string) bool {
	lockDir := filepath.Join(BusDir(session), "lock")
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".ollama-fail") {
			return true
		}
	}
	return false
}

// FormatOllamaAlert formats an Ollama health alert for the edit agent.
func FormatOllamaAlert(status string, roles []string, message string) string {
	var b strings.Builder
	switch status {
	case "down":
		b.WriteString("⚠ OLLAMA DOWN\n")
	case "restarting":
		b.WriteString("🔄 OLLAMA RESTARTING\n")
	case "recovered":
		b.WriteString("✅ OLLAMA RECOVERED\n")
	default:
		b.WriteString(fmt.Sprintf("ℹ OLLAMA %s\n", strings.ToUpper(status)))
	}
	if len(roles) > 0 {
		b.WriteString(fmt.Sprintf("  Affected roles: %s\n", strings.Join(roles, ", ")))
	}
	if message != "" {
		b.WriteString(fmt.Sprintf("  %s\n", message))
	}
	return b.String()
}

// OllamaHealthAlertKey returns a dedup key for an Ollama health alert.
func OllamaHealthAlertKey(status string) string {
	return fmt.Sprintf("ollama:%s", status)
}
