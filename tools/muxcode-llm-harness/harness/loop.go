package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// PollInterval is the sleep between inbox checks when idle.
	PollInterval = 3 * time.Second

	// BatchTimeout is the maximum time a single processBatch call can run.
	BatchTimeout = 5 * time.Minute

	// MaxConsecutiveFailures is how many failed batches before a cooldown.
	MaxConsecutiveFailures = 3

	// CooldownDuration is how long to pause after consecutive failures.
	CooldownDuration = 30 * time.Second

	// MaxAllBlockedTurns is how many consecutive all-blocked turns before
	// breaking out of the tool loop early.
	MaxAllBlockedTurns = 2
)

// logTag is the prefix used in all harness log lines. Set to the model
// name at startup so the pane shows which LLM is running.
var logTag = "harness"

// runStty runs stty with the given arguments, explicitly passing os.Stdin
// so stty can see the controlling terminal. Go's exec.Command defaults
// nil Stdin to /dev/null, which causes stty to silently fail.
func runStty(args ...string) error {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// suppressEcho disables terminal echo and drains stdin to prevent tmux
// send-keys notifications from displaying in the harness output. Notify()
// sends keystrokes to the pane's pty master; with ECHO enabled the pty
// driver echoes them to the terminal even though the harness isn't reading
// stdin. Returns a cleanup function to restore echo on exit.
func suppressEcho() func() {
	_ = runStty("-echo")
	go io.Copy(io.Discard, os.Stdin)
	return func() {
		_ = runStty("echo")
	}
}

// Run is the main entry point. It initializes the harness and enters the
// polling loop. Blocks until context is cancelled.
func Run(ctx context.Context, cfg Config) error {
	// Suppress tmux send-keys echo — must be before any inbox polling
	restoreEcho := suppressEcho()
	defer restoreEcho()

	// Initialize bus client
	bus := NewBusClient(cfg)

	// Resolve tools once at startup (cached)
	patterns, err := bus.ResolveTools()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[%s] Warning: could not resolve tools: %v\n", logTag, err)
	}

	// Build tool definitions for Ollama
	tools := BuildToolDefs(patterns)

	// Initialize executor
	executor := NewExecutor(patterns)

	// Set per-role bash timeout if configured
	if t := bus.ResolveBashTimeout(); t > 0 {
		executor.BashTimeout = time.Duration(t) * time.Second
		fmt.Fprintf(os.Stderr, "[%s] Bash timeout: %ds\n", logTag, t)
	}

	// Enable PII scrubbing for roles that handle external data
	if IsPIISensitiveRole(cfg.Role) || IsPIISensitiveRole(cfg.BusRole) {
		executor.ScrubPII = true
		fmt.Fprintf(os.Stderr, "[%s] PII scrubbing enabled for role %s\n", logTag, cfg.Role)
	}

	// Initialize Ollama client
	ollama := NewOllamaClient(cfg.OllamaURL, cfg.OllamaModel)

	// Verify Ollama connectivity
	healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
	err = ollama.CheckHealth(healthCtx)
	healthCancel()

	if err != nil {
		return fmt.Errorf("Ollama health check failed: %w", err)
	}

	// Set log prefix to the model name so the pane identifies the LLM
	logTag = cfg.OllamaModel

	fmt.Fprintf(os.Stderr, "[%s] Connected to Ollama (%s)\n", logTag, cfg.OllamaURL)
	fmt.Fprintf(os.Stderr, "[%s] Tools: %d patterns, %d tool defs\n", logTag, len(patterns), len(tools))

	// Build system prompt once at startup
	agentDef := ReadAgentDefinition(cfg.Role)
	skills, _ := bus.SkillPrompt()
	contextPrompt, _ := bus.ContextPrompt()
	systemPrompt := BuildSystemPrompt(cfg.Role, agentDef, skills, contextPrompt)

	// Resolve bus identity — the window name used for inbox/lock/send
	busRole := cfg.BusRole
	if busRole == "" {
		busRole = cfg.Role
	}

	// Write harness marker so Notify() skips tmux send-keys for this pane
	markerPath := filepath.Join(cfg.BusDir, "harness-"+busRole+".pid")
	if err := os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] Warning: could not write marker %s: %v\n", logTag, markerPath, err)
	} else {
		defer os.Remove(markerPath)
	}

	fmt.Fprintf(os.Stderr, "[%s] System prompt: %d bytes\n", logTag, len(systemPrompt))
	if cfg.BusRole != "" && cfg.BusRole != cfg.Role {
		fmt.Fprintf(os.Stderr, "[%s] Agent role: %s, bus identity: %s\n", logTag, cfg.Role, cfg.BusRole)
	}
	fmt.Fprintf(os.Stderr, "[%s] Ready, polling inbox for %s...\n", logTag, busRole)

	// Initialize filter — use bus identity for self-send detection
	filter := NewFilter(busRole)

	// Cross-batch stuck detection
	consecutiveFailures := 0
	var cooldownUntil time.Time

	// Main polling loop
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Re-apply echo suppression — subprocesses (bash tool calls) can
		// reset terminal attributes, re-enabling echo. Cheap: one exec per 3s.
		_ = runStty("-echo")

		// Cooldown: skip processing if we hit consecutive failures
		if consecutiveFailures >= MaxConsecutiveFailures && time.Now().Before(cooldownUntil) {
			remaining := time.Until(cooldownUntil).Round(time.Second)
			fmt.Fprintf(os.Stderr, "[%s] Cooldown: %d consecutive failures, paused for %s\n",
				logTag, consecutiveFailures, remaining)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(PollInterval):
			}
			continue
		}
		// Reset after cooldown expires
		if consecutiveFailures >= MaxConsecutiveFailures && time.Now().After(cooldownUntil) {
			fmt.Fprintf(os.Stderr, "[%s] Cooldown expired, resuming\n", logTag)
			consecutiveFailures = 0
		}

		inboxPath := cfg.InboxPath()

		if bus.HasMessages(inboxPath) {
			if err := bus.Lock(); err != nil {
				fmt.Fprintf(os.Stderr, "[%s] lock error: %v\n", logTag, err)
			}

			msgs, err := bus.ConsumeInbox()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%s] consume error: %v\n", logTag, err)
				_ = bus.Unlock()
				continue
			}

			// Filter out file-change notify events — these are informational
			// routing from the analyze hook and not actionable tasks for the LLM.
			msgs = filterFileChangeNotify(msgs, busRole)

			if len(msgs) > 0 {
				filter.Reset()

				// Run batch with timeout
				batchCtx, batchCancel := context.WithTimeout(ctx, BatchTimeout)
				success := processBatch(batchCtx, cfg, bus, ollama, executor, tools, systemPrompt, filter, msgs)
				batchCancel()

				if success {
					consecutiveFailures = 0
				} else {
					consecutiveFailures++
					if consecutiveFailures >= MaxConsecutiveFailures {
						cooldownUntil = time.Now().Add(CooldownDuration)
						fmt.Fprintf(os.Stderr, "[%s] %d consecutive failures — entering %s cooldown\n",
							logTag, consecutiveFailures, CooldownDuration)
					}
				}
			}

			_ = bus.Unlock()
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(PollInterval):
		}
	}
}

// processBatch handles a batch of inbox messages through the Ollama conversation loop.
// Returns true if the batch produced a meaningful response, false if it exhausted
// turns, timed out, or was blocked — used for cross-batch stuck detection.
func processBatch(ctx context.Context, cfg Config, bus *BusClient, ollama *OllamaClient, executor *Executor, tools []ToolDef, systemPrompt string, filter *Filter, msgs []Message) bool {
	// Find last message for reply routing
	lastMsg := msgs[len(msgs)-1]

	// Build structured task content
	taskContent := FormatTask(msgs)

	// Display each incoming message once (replaces noisy tmux notifications)
	for _, m := range msgs {
		payload := m.Payload
		if len(payload) > 120 {
			payload = payload[:120] + "…"
		}
		fmt.Fprintf(os.Stderr, "\n[%s → %s] %s\n", m.From, m.Action, payload)
	}
	fmt.Fprintf(os.Stderr, "[%s] Processing %d message(s) from %s: %s\n",
		logTag, len(msgs), lastMsg.From, lastMsg.Action)

	// Fresh conversation: system + task
	conversation := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: taskContent},
	}

	// Tool-calling loop
	var finalResponse string
	batchSuccess := false
	toolsExecuted := false
	consecutiveAllBlocked := 0
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	for turn := 0; turn < maxTurns; turn++ {
		// Check context (batch timeout or parent cancellation)
		select {
		case <-ctx.Done():
			finalResponse = "Error: batch timed out"
			fmt.Fprintf(os.Stderr, "[%s] Batch context cancelled: %v\n", logTag, ctx.Err())
			goto sendResponse
		default:
		}

		fmt.Fprintf(os.Stderr, "[%s] Turn %d/%d — calling Ollama...\n", logTag, turn+1, maxTurns)
		ollamaStart := time.Now()
		resp, err := ollama.ChatComplete(ctx, conversation, tools)
		ollamaDur := time.Since(ollamaStart)
		if err != nil {
			if ctx.Err() != nil {
				finalResponse = "Error: batch timed out during Ollama call"
			} else {
				finalResponse = fmt.Sprintf("Error calling Ollama: %v", err)
			}
			break
		}

		if len(resp.Choices) == 0 {
			finalResponse = "Error: empty response from Ollama"
			break
		}

		choice := resp.Choices[0]
		fmt.Fprintf(os.Stderr, "[%s] Ollama responded in %.1fs, %d tool call(s)\n",
			logTag, ollamaDur.Seconds(), len(choice.Message.ToolCalls))

		// Fallback: extract tool calls from text when model doesn't use structured API
		if len(choice.Message.ToolCalls) == 0 && choice.Message.Content != "" {
			extracted := ExtractToolCalls(choice.Message.Content, toolNames(tools))
			if len(extracted) > 0 {
				fmt.Fprintf(os.Stderr, "[%s] Extracted %d tool call(s) from text response\n", logTag, len(extracted))
				choice.Message.ToolCalls = extracted
				choice.Message.Content = ""
			}
		}

		conversation = append(conversation, choice.Message)

		// If no tool calls, check for hallucination on first turn
		if len(choice.Message.ToolCalls) == 0 {
			// Show truncated text response for visibility
			text := choice.Message.Content
			if len(text) > 100 {
				text = text[:100] + "…"
			}
			if text != "" {
				fmt.Fprintf(os.Stderr, "[%s] Text: %s\n", logTag, text)
			}

			if turn == 0 && !toolsExecuted && len(tools) > 0 {
				// First response with no tool calls and tools are available —
				// the LLM is likely hallucinating results instead of executing.
				// Inject a corrective message and retry.
				fmt.Fprintf(os.Stderr, "[%s] No tool calls on first turn — forcing tool use\n", logTag)
				conversation = append(conversation, ChatMessage{
					Role:    "user",
					Content: "You did NOT execute any commands. You MUST use the bash tool to run the actual commands before responding. Do NOT describe results from memory — call the bash tool now to execute the task.",
				})
				continue
			}
			finalResponse = choice.Message.Content
			batchSuccess = true
			break
		}

		// Execute tool calls
		allBlocked := true
		for i, tc := range choice.Message.ToolCalls {
			result := filter.Check(tc)

			// Log tool invocation with key arguments
			toolLabel := toolCallLabel(tc)
			fmt.Fprintf(os.Stderr, "[%s] → %s [%d/%d]\n", logTag, toolLabel, i+1, len(choice.Message.ToolCalls))

			var toolOutput string
			if result.Blocked {
				toolOutput = result.Reason
				fmt.Fprintf(os.Stderr, "[%s] ✗ BLOCKED: %s\n", logTag, result.Reason)
			} else {
				allBlocked = false
				toolsExecuted = true
				toolStart := time.Now()
				toolOutput = executor.Execute(ctx, tc)
				toolDur := time.Since(toolStart)

				// Log completion with timing and brief result
				exitInfo := toolExitInfo(tc, toolOutput)
				fmt.Fprintf(os.Stderr, "[%s] ✓ %s (%.1fs%s)\n", logTag, tc.Function.Name, toolDur.Seconds(), exitInfo)

				// Log bash commands to history
				if tc.Function.Name == "bash" {
					logToolToHistory(bus, tc, toolOutput)
				}
			}

			// Add tool result to conversation
			conversation = append(conversation, ChatMessage{
				Role:       "tool",
				Content:    toolOutput,
				ToolCallID: tc.ID,
			})
		}

		// Track consecutive all-blocked turns
		if allBlocked {
			consecutiveAllBlocked++
			if consecutiveAllBlocked >= MaxAllBlockedTurns {
				fmt.Fprintf(os.Stderr, "[%s] %d consecutive all-blocked turns — breaking out\n",
					logTag, consecutiveAllBlocked)
				finalResponse = "(all tool calls blocked — agent stuck in loop)"
				break
			}
			conversation = append(conversation, ChatMessage{
				Role:    "user",
				Content: "All your tool calls were blocked. Your task is already in this conversation. Execute it using the appropriate commands (NOT muxcode-agent-bus inbox). If you have completed the task, provide your final response as text.",
			})
		} else {
			consecutiveAllBlocked = 0
		}
	}

	// If tools were executed but the final response looks like narration
	// instead of a summary, do one more call with no tools to force a summary.
	if toolsExecuted && looksLikeNarration(finalResponse) {
		fmt.Fprintf(os.Stderr, "[%s] Final response looks like narration, requesting summary...\n", logTag)
		conversation = append(conversation, ChatMessage{
			Role:    "user",
			Content: "You already executed the commands above. Now provide ONLY a short factual summary of the result. Start with the outcome: succeeded or failed. Do not describe what you plan to do — just summarize what already happened.",
		})
		summaryStart := time.Now()
		resp, err := ollama.ChatComplete(ctx, conversation, nil) // no tools — text only
		fmt.Fprintf(os.Stderr, "[%s] Summary call %.1fs\n", logTag, time.Since(summaryStart).Seconds())
		if err == nil && len(resp.Choices) > 0 && resp.Choices[0].Message.Content != "" {
			finalResponse = resp.Choices[0].Message.Content
			batchSuccess = true
		}
	}

	// Mark success if tools executed and we got a real response
	if toolsExecuted && finalResponse != "" && finalResponse != "(no response generated — tool loop exhausted)" && finalResponse != "(all tool calls blocked — agent stuck in loop)" {
		batchSuccess = true
	}

sendResponse:
	// Send response
	if finalResponse == "" {
		finalResponse = "(no response generated — tool loop exhausted)"
	}

	// Truncate very long responses
	if len(finalResponse) > 4000 {
		finalResponse = finalResponse[:4000] + "\n... [truncated]"
	}

	fmt.Fprintf(os.Stderr, "[%s] Response (%d bytes, success=%v) → %s\n",
		logTag, len(finalResponse), batchSuccess, lastMsg.From)

	if err := bus.Send(lastMsg.From, lastMsg.Action, finalResponse, "response", lastMsg.ID); err != nil {
		fmt.Fprintf(os.Stderr, "[%s] send error: %v\n", logTag, err)
	}

	return batchSuccess
}

// filterFileChangeNotify removes "File changed:" notify events from the message
// batch. These are informational routing from the analyze hook — useful for
// Claude Code agents that can decide what to do, but wasteful for local LLMs
// that treat every message as an actionable task. Direct action messages
// (e.g. action=build) pass through unchanged.
func filterFileChangeNotify(msgs []Message, role string) []Message {
	var filtered []Message
	for _, m := range msgs {
		if m.Action == "notify" && strings.HasPrefix(m.Payload, "File changed:") {
			fmt.Fprintf(os.Stderr, "[%s] Skipping file-change notify: %s\n", logTag, m.Payload)
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered
}

// looksLikeNarration detects when the LLM generated a planning/narration
// response instead of summarizing tool results. Common with smaller models
// that describe what they'll do instead of reporting what happened.
func looksLikeNarration(response string) bool {
	if response == "" {
		return false
	}
	lower := strings.ToLower(response)
	// Narration markers: LLM describes future actions instead of past results
	markers := []string{
		"let's ", "let me ", "i will ", "i'll ",
		"let us ", "we can ", "we should ",
		"now i need to", "now let's",
		"i'm going to", "i am going to",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	// Contains markdown code blocks suggesting the LLM is showing commands
	// it wants to run rather than reporting results
	if strings.Count(response, "```") >= 2 && !strings.Contains(lower, "succeeded") && !strings.Contains(lower, "failed") {
		return true
	}
	return false
}

// toolCallLabel returns a human-readable label for a tool call, e.g. "bash: ./build.sh".
func toolCallLabel(tc ToolCall) string {
	name := tc.Function.Name
	switch name {
	case "bash":
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(tc.Function.Arguments, &args) == nil && args.Command != "" {
			cmd := args.Command
			if len(cmd) > 80 {
				cmd = cmd[:77] + "..."
			}
			return fmt.Sprintf("bash: %s", cmd)
		}
	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(tc.Function.Arguments, &args) == nil && args.Path != "" {
			return fmt.Sprintf("read: %s", args.Path)
		}
	case "glob":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal(tc.Function.Arguments, &args) == nil && args.Pattern != "" {
			return fmt.Sprintf("glob: %s", args.Pattern)
		}
	case "grep":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal(tc.Function.Arguments, &args) == nil && args.Pattern != "" {
			pat := args.Pattern
			if len(pat) > 40 {
				pat = pat[:37] + "..."
			}
			return fmt.Sprintf("grep: %s", pat)
		}
	case "write_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(tc.Function.Arguments, &args) == nil && args.Path != "" {
			return fmt.Sprintf("write: %s", args.Path)
		}
	case "edit_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(tc.Function.Arguments, &args) == nil && args.Path != "" {
			return fmt.Sprintf("edit: %s", args.Path)
		}
	}
	return name
}

// toolExitInfo returns a suffix string with exit code info for tool results.
// Returns empty string for success, ", exit N" for failures.
func toolExitInfo(tc ToolCall, output string) string {
	if tc.Function.Name != "bash" {
		return ""
	}
	if strings.Contains(output, "timed out") {
		return ", TIMEOUT"
	}
	if idx := strings.LastIndex(output, "Exit code: "); idx >= 0 {
		code := strings.TrimSpace(output[idx+len("Exit code: "):])
		if nl := strings.IndexByte(code, '\n'); nl >= 0 {
			code = code[:nl]
		}
		return fmt.Sprintf(", exit %s", code)
	}
	return ""
}

// logToolToHistory extracts command info and logs to the role's history JSONL.
func logToolToHistory(bus *BusClient, tc ToolCall, result string) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
		// Fallback: small LLMs sometimes send arguments as a plain string
		var cmdStr string
		if json.Unmarshal(tc.Function.Arguments, &cmdStr) == nil {
			args.Command = cmdStr
		}
	}

	outcome := "success"
	exitCode := "0"
	if strings.Contains(result, "timed out") {
		outcome = "failure"
		exitCode = "124"
	} else if strings.Contains(result, "not allowed") {
		outcome = "failure"
		exitCode = "126"
	} else if idx := strings.LastIndex(result, "Exit code: "); idx >= 0 {
		outcome = "failure"
		code := strings.TrimSpace(result[idx+len("Exit code: "):])
		if nl := strings.IndexByte(code, '\n'); nl >= 0 {
			code = code[:nl]
		}
		exitCode = code
	}

	_ = bus.LogHistory(args.Command, result, exitCode, outcome)
}
