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
	"strconv"
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

// OllamaProbeSecs returns the inference probe timeout. Configurable via
// MUXCODE_OLLAMA_PROBE_SECS; default is OllamaProbeTimeout (10s). The
// probe blocks the daemon poll loop, so raising this trades slower
// daemon ticks for tolerance of slower hardware.
func OllamaProbeSecs() time.Duration {
	if v := os.Getenv("MUXCODE_OLLAMA_PROBE_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return OllamaProbeTimeout
}

// healthHTTPClient builds the client every Ollama probe uses.
//
// It is a variable so a test can route probes to an in-process handler instead
// of a loopback socket, which the Codex sandbox refuses — a socket-bound test
// panics before any assertion and takes every later module with it (MUX-153).
var healthHTTPClient = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// CheckOllamaInference sends a minimal generation to distinguish "process
// alive but stuck" from "process healthy". Uses a fresh HTTP client with a
// short timeout to avoid sharing the agent's long-timeout client.
//
// The probe uses /api/generate with think:false rather than the OpenAI
// chat endpoint: thinking models (qwen3) reason for 30-90s before their
// first token even when warm, so a chat probe capped at 10s declared a
// healthy server down every cycle and the ladder killed it (observed live
// 2026-08-26). With thinking off, a warm one-token generation answers in
// ~1s; servers predating the think parameter ignore the unknown field.
func CheckOllamaInference(baseURL, model string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = OllamaProbeSecs()
	}

	client := healthHTTPClient(timeout)

	think := false
	req := struct {
		Model   string         `json:"model"`
		Prompt  string         `json:"prompt"`
		Stream  bool           `json:"stream"`
		Think   *bool          `json:"think"`
		Options map[string]any `json:"options"`
	}{
		Model:   model,
		Prompt:  "hi",
		Stream:  false,
		Think:   &think,
		Options: map[string]any{"num_predict": 1},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encoding probe request: %w", err)
	}

	url := baseURL + "/api/generate"
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

// OllamaWarmupGraceSecs returns how long a responsive-but-still-loading
// Ollama is tolerated before the restart ladder treats it as stuck.
// Loading several GB of weights routinely outlasts the 10s inference
// probe, and a restart discards the load in progress — so without this
// grace the ladder kills every load attempt and loops (observed live
// 2026-08-26, first cold start after qwen3:4b became the default).
// Configurable via MUXCODE_OLLAMA_WARMUP_GRACE_SECS; default 300.
func OllamaWarmupGraceSecs() int64 {
	if v := os.Getenv("MUXCODE_OLLAMA_WARMUP_GRACE_SECS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return 300
}

// OllamaModelLoaded reports (via GET /api/ps) whether the server responds
// and whether the model currently occupies memory. The pair distinguishes
// the three states the restart ladder must treat differently: dead server
// (false, false — ladder), loaded-but-wedged inference (true, true — the
// original disease, ladder), and warming (true, false — tolerate).
// Matching mirrors CheckHealth: exact, or base-name when the configured
// model has no explicit tag.
func OllamaModelLoaded(baseURL, model string, timeout time.Duration) (responsive, loaded bool) {
	client := healthHTTPClient(timeout)
	resp, err := client.Get(baseURL + "/api/ps")
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false
	}

	var ps struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		return true, false
	}
	for _, m := range ps.Models {
		if m.Name == model {
			return true, true
		}
		if !strings.Contains(model, ":") && strings.Contains(m.Name, ":") {
			if strings.SplitN(m.Name, ":", 2)[0] == model {
				return true, true
			}
		}
	}
	return true, false
}

// roleEnvMap maps MUXCODE_{NAME}_CLI env var names to agent roles.
// Includes legacy env var names (MUXCODE_GIT_CLI) for backward compatibility.
var roleEnvMap = map[string]string{
	"MUXCODE_COMMIT_CLI":   "commit",
	"MUXCODE_BUILD_CLI":    "build",
	"MUXCODE_TEST_CLI":     "test",
	"MUXCODE_REVIEW_CLI":   "review",
	"MUXCODE_DEPLOY_CLI":   "deploy",
	"MUXCODE_RUN_CLI":      "run",
	"MUXCODE_ANALYZE_CLI":  "analyze",
	"MUXCODE_DOCS_CLI":     "docs",
	"MUXCODE_RESEARCH_CLI": "research",
	"MUXCODE_WATCH_CLI":    "watch",
	"MUXCODE_GIT_CLI":      "commit", // legacy alias
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
	client := healthHTTPClient(2 * time.Second)

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

// restartInterruptDelay is how long RestartLocalAgent waits after C-c for the
// process to exit before typing the relaunch. Tests shorten it.
var restartInterruptDelay = 500 * time.Millisecond

// RestartLocalAgent sends C-c to interrupt a stuck agent and relaunches it.
// Uses tmux send-keys to target the agent's pane. The relaunch command uses
// `muxcode agent launch` which resolves the correct provider (Claude Code,
// OpenCode, Codex, or local LLM) via environment variables.
//
// The pane is scraped for Claude Code's exit banner BEFORE the C-c: the
// relaunch types over the banner, so a scrape after it reads the launch
// command and falls back to fresh every time (MUX-126). A hit relaunches with
// `--resume <id>`, which the launcher appends to the full flag set; a miss or
// a stale banner relaunches fresh — never a flagless resume.
//
// The role's reload marker is acquired exclusively for the relaunch, the same
// lock `muxcode resume` holds, so the two never drive one pane at once; a held
// marker returns ErrReloadMarkerHeld with nothing typed.
func RestartLocalAgent(session, role string) error {
	release, err := acquireReloadMarker(session, role)
	if err != nil {
		return err
	}
	defer release()
	return scrapeAndRelaunch(session, role, PaneTarget(session, role), "daemon", "")
}

// scrapeAndRelaunch is the one scrape-then-relaunch body, shared by the
// daemon's RestartLocalAgent and the manual ResumeAgent so neither can grow a
// second scrape, pattern or flag-assembly path. source is the lifecycle source
// recorded on every row; a non-empty actor is appended to each row's detail.
func scrapeAndRelaunch(session, role, target, source, actor string) error {
	content, captureErr := captureResumePane(target)
	id, event := "", "resume-scrape-miss"
	if captureErr == nil {
		id, event = restartResumeTarget(content)
	}
	detail := role
	if id != "" {
		detail += ": session " + id
	} else if captureErr != nil {
		detail += ": capture failed: " + captureErr.Error()
	}
	by := ""
	if actor != "" {
		by = " (by " + actor + ")"
	}
	LogLifecycle(session, "info", source, event, detail+by)

	if err := TmuxRun("send-keys", "-t", target, "C-c", ""); err != nil {
		return fmt.Errorf("interrupting agent %s: %w", role, err)
	}

	time.Sleep(restartInterruptDelay)

	launchCmd := fmt.Sprintf("muxcode agent launch %s", role)
	if id != "" {
		launchCmd += " --resume " + id
	}
	if err := TmuxRun("send-keys", "-t", target, launchCmd, "Enter"); err != nil {
		return fmt.Errorf("relaunching agent %s: %w", role, err)
	}
	LogLifecycle(session, "info", source, "agent-relaunch", role+": "+launchCmd+by)

	return nil
}

// restartResumeTarget picks the session a restart resumes from a pane capture
// and names the lifecycle event recording the choice: resume-scrape-hit,
// resume-scrape-miss, or resume-scrape-stale. Stale is a banner already
// followed by a relaunch: that relaunch died without drawing a new banner, so
// the resume never reached a session, and repeating it would spend the whole
// restart cap on the same failure instead of coming back fresh.
func restartResumeTarget(content string) (id, event string) {
	id, ok := ScrapeResumeSessionID(content)
	if !ok {
		return "", "resume-scrape-miss"
	}
	banners := agentExitBannerPattern.FindAllStringIndex(content, -1)
	if strings.Contains(stripWhitespace(content[banners[len(banners)-1][1]:]), "agentlaunch") {
		return "", "resume-scrape-stale"
	}
	return id, "resume-scrape-hit"
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
