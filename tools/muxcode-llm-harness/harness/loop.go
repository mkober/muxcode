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

	// maxChatToolOutput is the max bytes of tool output stored in persistent
	// chat history. Full output is available in the current conversation turn;
	// history only needs enough context for follow-up questions.
	maxChatToolOutput = 2000
)

// logTag is the prefix used in all harness log lines. Set to the model
// name at startup so the pane shows which LLM is running.
var logTag = "harness"

// isSingleShotRole returns true for roles that should auto-complete after
// one successful tool execution. This prevents small models from looping
// endlessly re-running the same command.
func isSingleShotRole(role string) bool {
	switch role {
	case "build", "test", "prompt":
		return true
	}
	return false
}

// capitalize returns s with the first letter uppercased.
// endpointKind names the inference endpoint truthfully for every log
// line that mentions it: a bearer key means a hosted gateway, and
// "calling Ollama" on a gateway fault sends whoever debugs it off to
// check a local daemon that isn't involved (plan's catch, 2026-08-27;
// the per-turn line repeated it, user's catch same day).
func endpointKind(cfg Config) string {
	if cfg.APIKey != "" {
		return "gateway"
	}
	return "Ollama"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

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
// When sink is nil, a LogSink writing to stderr is used (headless mode).
func Run(ctx context.Context, cfg Config, sink EventSink) error {
	// Default to stderr logging if no sink provided
	if sink == nil {
		sink = NewLogSink("harness")
	}

	// Suppress tmux send-keys echo — must be before any inbox polling.
	// Skip when TUI mode is active — the TUI manages terminal state.
	if !cfg.TUI {
		restoreEcho := suppressEcho()
		defer restoreEcho()
	}

	// Initialize bus client
	bus := NewBusClient(cfg)

	// Resolve tools once at startup (cached)
	patterns, err := bus.ResolveTools()
	if err != nil {
		sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: fmt.Sprintf("Warning: could not resolve tools: %v", err)})
	}

	// Build tool definitions for Ollama
	tools := BuildToolDefs(patterns)

	// Initialize executor
	executor := NewExecutor(patterns)

	// Set per-role bash timeout if configured
	if t := bus.ResolveBashTimeout(); t > 0 {
		executor.BashTimeout = time.Duration(t) * time.Second
		sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: fmt.Sprintf("Bash timeout: %ds", t)})
	}

	// Enable PII scrubbing for roles that handle external data
	if IsPIISensitiveRole(cfg.Role) || IsPIISensitiveRole(cfg.BusRole) {
		executor.ScrubPII = true
		sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: fmt.Sprintf("PII scrubbing enabled for role %s", cfg.Role)})
	}

	// Initialize the inference client (local Ollama or, with an API key,
	// a hosted OpenAI-compatible gateway — same dialect either way).
	ollama := NewOllamaClient(cfg.OllamaURL, cfg.OllamaModel)
	ollama.NoThink = cfg.NoThink
	ollama.APIKey = cfg.APIKey

	if cfg.APIKey == "" {
		// Verify Ollama connectivity — /api/tags is Ollama-specific, so a
		// gateway backend skips it (the first real call surfaces auth or
		// connectivity failures with a better error anyway).
		healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
		err = ollama.CheckHealth(healthCtx)
		healthCancel()

		if err != nil {
			return fmt.Errorf("Ollama health check failed: %w", err)
		}
	} else {
		sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: "Gateway mode — skipping local Ollama model check"})
	}

	// Set log prefix to the model name so the LogSink identifies the LLM
	logTag = cfg.OllamaModel
	if ls, ok := sink.(*LogSink); ok {
		ls.Tag = logTag
	}

	sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: fmt.Sprintf("Connected to %s (%s)", endpointKind(cfg), cfg.OllamaURL)})
	sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: fmt.Sprintf("Tools: %d patterns, %d tool defs", len(patterns), len(tools))})

	// Build system prompt once at startup
	agentDef := ReadAgentDefinition(cfg.Role)
	skills, _ := bus.SkillPrompt()
	contextPrompt, _ := bus.ContextPrompt()
	systemPrompt := applyNoThink(BuildSystemPrompt(cfg.Role, agentDef, skills, contextPrompt), cfg.NoThink)

	// Resolve bus identity — the window name used for inbox/lock/send
	busRole := cfg.BusRole
	if busRole == "" {
		busRole = cfg.Role
	}

	// Write harness marker so Notify() skips tmux send-keys for this pane
	markerPath := filepath.Join(cfg.BusDir, "harness-"+busRole+".pid")
	if err := os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: fmt.Sprintf("Warning: could not write marker %s: %v", markerPath, err)})
	} else {
		defer os.Remove(markerPath)
	}

	sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: fmt.Sprintf("System prompt: %d bytes", len(systemPrompt))})
	if cfg.BusRole != "" && cfg.BusRole != cfg.Role {
		sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: fmt.Sprintf("Agent role: %s, bus identity: %s", cfg.Role, cfg.BusRole)})
	}
	sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: fmt.Sprintf("Ready, polling inbox for %s...", busRole)})

	// Initialize filter — use bus identity for self-send detection
	filter := NewFilter(busRole)

	// Cross-batch stuck detection
	consecutiveFailures := 0
	var cooldownUntil time.Time

	// Persistent chat history for interactive user input (TUI mode)
	var chatHistory []ChatMessage

	// Main polling loop
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Re-apply echo suppression — subprocesses (bash tool calls) can
		// reset terminal attributes, re-enabling echo. Cheap: one exec per 3s.
		// Skip in TUI mode: TUI manages terminal state itself.
		if !cfg.TUI {
			_ = runStty("-echo")
		}

		// Cooldown: skip processing if we hit consecutive failures
		if consecutiveFailures >= MaxConsecutiveFailures && time.Now().Before(cooldownUntil) {
			remaining := time.Until(cooldownUntil).Round(time.Second)
			sink.Emit(Event{Kind: EventCooldown, Time: time.Now(), Message: fmt.Sprintf("Cooldown: %d failures, paused for %s", consecutiveFailures, remaining)})
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(PollInterval):
			}
			continue
		}
		// Reset after cooldown expires
		if consecutiveFailures >= MaxConsecutiveFailures && time.Now().After(cooldownUntil) {
			sink.Emit(Event{Kind: EventStartup, Time: time.Now(), Message: "Cooldown expired, resuming"})
			consecutiveFailures = 0
		}

		inboxPath := cfg.InboxPath()

		if bus.HasMessages(inboxPath) {
			if err := bus.Lock(); err != nil {
				sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: fmt.Sprintf("lock error: %v", err)})
			}

			msgs, err := bus.ConsumeInbox()
			if err != nil {
				sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: fmt.Sprintf("consume error: %v", err)})
				_ = bus.Unlock()
				continue
			}

			// Filter out file-change notify events — these are informational
			// routing from the analyze hook and not actionable tasks for the LLM.
			msgs = filterFileChangeNotify(msgs, busRole)

			if len(msgs) > 0 {
				filter.Reset()
				// The approve guard checks tool calls against the user's
				// own words — request payloads only, never system-authored
				// responses/events in the same batch (MUX-109).
				filter.TaskText = requestTaskText(msgs)

				// Run batch with timeout
				batchCtx, batchCancel := context.WithTimeout(ctx, BatchTimeout)
				success := processBatch(batchCtx, cfg, bus, ollama, executor, tools, systemPrompt, filter, msgs, sink)
				batchCancel()

				if success {
					consecutiveFailures = 0
				} else {
					consecutiveFailures++
					if consecutiveFailures >= MaxConsecutiveFailures {
						cooldownUntil = time.Now().Add(CooldownDuration)
						sink.Emit(Event{Kind: EventCooldown, Time: time.Now(), Message: fmt.Sprintf("%d consecutive failures — entering %s cooldown", consecutiveFailures, CooldownDuration)})
					}
				}
			}

			_ = bus.Unlock()
		}

		// Wait for next poll tick or user input (whichever comes first).
		// A nil UserInput channel (headless mode) is never selected.
		select {
		case <-ctx.Done():
			return nil
		case input := <-cfg.UserInput:
			// Interactive chat from TUI — process immediately with
			// persistent conversation history (no bus lock needed).
			chatCtx, chatCancel := context.WithTimeout(ctx, BatchTimeout)
			processUserChat(chatCtx, cfg, ollama, executor, tools, systemPrompt, &chatHistory, input, sink)
			chatCancel()
		case <-time.After(PollInterval):
		}
	}
}

// processBatch handles a batch of inbox messages through the Ollama conversation loop.
// Returns true if the batch produced a meaningful response, false if it exhausted
// turns, timed out, or was blocked — used for cross-batch stuck detection.
func processBatch(ctx context.Context, cfg Config, bus *BusClient, ollama *OllamaClient, executor *Executor, tools []ToolDef, systemPrompt string, filter *Filter, msgs []Message, sink EventSink) bool {
	// Find last message for reply routing
	lastMsg := msgs[len(msgs)-1]

	// Build structured task content
	taskContent := FormatTask(msgs)

	// Display each incoming message once (replaces noisy tmux notifications)
	for _, m := range msgs {
		payload := m.Payload
		if runes := []rune(payload); len(runes) > 120 {
			payload = string(runes[:120]) + "…"
		}
		sink.Emit(Event{Kind: EventMessageReceived, Time: time.Now(), Message: fmt.Sprintf("[%s → %s] %s", m.From, m.Action, payload)})
	}
	sink.Emit(Event{Kind: EventBatchStart, Time: time.Now(), Message: fmt.Sprintf("Processing %d message(s) from %s: %s", len(msgs), lastMsg.From, lastMsg.Action)})

	// Fresh conversation: system + task
	conversation := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: taskContent},
	}

	// Tool-calling loop
	var finalResponse string
	var denials denialTracker // false-success guard: latches a refusal, clears on same-command recovery (MUX-109)
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
			sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: fmt.Sprintf("Batch context cancelled: %v", ctx.Err())})
			goto sendResponse
		default:
		}

		actionLabel := capitalize(lastMsg.Action)
		if actionLabel == "" {
			actionLabel = "Task"
		}
		sink.Emit(Event{Kind: EventOllamaCall, Time: time.Now(), Message: fmt.Sprintf("%s %d/%d — calling %s...", actionLabel, turn+1, maxTurns, endpointKind(cfg))})
		ollamaStart := time.Now()
		resp, err := ollama.ChatComplete(ctx, conversation, tools)
		ollamaDur := time.Since(ollamaStart)
		if err != nil {
			if ctx.Err() != nil {
				finalResponse = fmt.Sprintf("Error: batch timed out during %s call", endpointKind(cfg))
			} else {
				finalResponse = fmt.Sprintf("Error calling %s: %v", endpointKind(cfg), err)
			}
			break
		}

		if len(resp.Choices) == 0 {
			finalResponse = "Error: empty response from Ollama"
			break
		}

		choice := resp.Choices[0]
		sink.Emit(Event{Kind: EventOllamaResponse, Time: time.Now(), Message: fmt.Sprintf("Ollama responded in %.1fs, %d tool call(s)", ollamaDur.Seconds(), len(choice.Message.ToolCalls))})

		// Fallback: extract tool calls from text when model doesn't use structured API
		if len(choice.Message.ToolCalls) == 0 && choice.Message.Content != "" {
			extracted := ExtractToolCalls(choice.Message.Content, toolNames(tools))
			if len(extracted) > 0 {
				sink.Emit(Event{Kind: EventOllamaResponse, Time: time.Now(), Message: fmt.Sprintf("Extracted %d tool call(s) from text response", len(extracted))})
				choice.Message.ToolCalls = extracted
				choice.Message.Content = ""
			}
		}

		conversation = append(conversation, choice.Message)

		// If no tool calls, check for hallucination on first turn
		if len(choice.Message.ToolCalls) == 0 {
			// Show truncated text response for visibility
			text := choice.Message.Content
			if runes := []rune(text); len(runes) > 100 {
				text = string(runes[:100]) + "…"
			}
			if text != "" {
				sink.Emit(Event{Kind: EventTextResponse, Time: time.Now(), Message: fmt.Sprintf("Text: %s", text)})
			}

			if turn == 0 && !toolsExecuted && len(tools) > 0 {
				// First response with no tool calls and tools are available —
				// the LLM is likely hallucinating results instead of executing.
				// Inject a corrective message and retry.
				sink.Emit(Event{Kind: EventForceToolUse, Time: time.Now(), Message: "No tool calls on first turn — forcing tool use"})
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
		lastToolSucceeded := false
		for i, tc := range choice.Message.ToolCalls {
			result := filter.Check(tc)

			// Log tool invocation with key arguments
			toolLabel := toolCallLabel(tc)
			sink.Emit(Event{Kind: EventToolStart, Time: time.Now(), Message: fmt.Sprintf("%s [%d/%d]", toolLabel, i+1, len(choice.Message.ToolCalls))})

			var toolOutput string
			if result.Blocked {
				toolOutput = result.Reason
				sink.Emit(Event{Kind: EventToolBlocked, Time: time.Now(), Message: fmt.Sprintf("BLOCKED: %s", result.Reason)})
			} else {
				allBlocked = false
				toolsExecuted = true
				toolStart := time.Now()
				toolOutput = executor.Execute(ctx, tc)
				toolDur := time.Since(toolStart)

				// Log completion with timing and brief result
				exitInfo := toolExitInfo(tc, toolOutput)
				sink.Emit(Event{Kind: EventToolComplete, Time: time.Now(), Message: fmt.Sprintf("%s (%.1fs%s)", tc.Function.Name, toolDur.Seconds(), exitInfo)})

				// Track success for single-shot auto-complete
				lastToolSucceeded = !toolHasNonZeroExit(toolOutput)

				// Show truncated output preview
				if preview := toolOutputPreview(toolOutput); preview != "" {
					sink.Emit(Event{Kind: EventToolOutput, Time: time.Now(), Message: preview})
				}

				// Log bash commands to history
				if tc.Function.Name == "bash" {
					logToolToHistory(bus, tc, toolOutput)
				}
			}

			denials.observe(tc, toolOutput)

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
				sink.Emit(Event{Kind: EventAllBlocked, Time: time.Now(), Message: fmt.Sprintf("%d consecutive all-blocked turns — breaking out", consecutiveAllBlocked)})
				finalResponse = "(all tool calls blocked — agent stuck in loop)"
				break
			}
			conversation = append(conversation, ChatMessage{
				Role:    "user",
				Content: "All your tool calls were blocked. Your task is already in this conversation. Execute it using the appropriate commands (NOT muxcode inbox). If you have completed the task, provide your final response as text.",
			})
		} else {
			consecutiveAllBlocked = 0

			// Single-shot roles: after one successful tool execution,
			// break out and let the summary call below handle the response.
			// This prevents small models from looping endlessly.
			// Only triggers on success (exit 0) — failed commands let the
			// model retry with fallback commands.
			// Gateway-backed agents (APIKey set) keep their full turn
			// budget: the brake exists for small local models, and it
			// truncates legitimate two-step intents (check `graph list`,
			// then launch) that a hosted model executes correctly.
			if isSingleShotRole(cfg.Role) && cfg.APIKey == "" && toolsExecuted && lastToolSucceeded {
				sink.Emit(Event{Kind: EventOllamaResponse, Time: time.Now(), Message: "Single-shot role — auto-completing after tool execution"})
				break
			}
		}
	}

	// If tools were executed but no text response was produced (e.g. single-shot
	// auto-complete or model never stopped calling tools) or the response looks
	// like narration, do one more call with no tools to force a summary.
	if toolsExecuted && (finalResponse == "" || looksLikeNarration(finalResponse)) {
		sink.Emit(Event{Kind: EventNarrationRetry, Time: time.Now(), Message: "Final response looks like narration, requesting summary..."})
		conversation = append(conversation, ChatMessage{
			Role:    "user",
			Content: "You already executed the commands above. Now provide ONLY a short factual summary of the result. Start with the outcome: succeeded or failed. Do not describe what you plan to do — just summarize what already happened.",
		})
		summaryStart := time.Now()
		resp, err := ollama.ChatComplete(ctx, conversation, nil) // no tools — text only
		sink.Emit(Event{Kind: EventOllamaResponse, Time: time.Now(), Message: fmt.Sprintf("Summary call %.1fs", time.Since(summaryStart).Seconds())})
		if err == nil && len(resp.Choices) > 0 && resp.Choices[0].Message.Content != "" {
			finalResponse = resp.Choices[0].Message.Content
			batchSuccess = true
		}
	}

	// False-success guard (MUX-109): a refused command must never read as
	// success. The prompt role sits behind a free-text box, and its
	// answers land in the Prompt surface as the outcome of what the user
	// asked — "succeeded" over a denied command makes them believe a
	// graph ran. The agent definition instructs the model to lead with
	// BLOCKED, but a small model told not to report false success still
	// sometimes does (observed live twice, 2026-08-27) — this is the
	// guard outside the model, same reasoning as the approve guard.
	// Prompt role only: build/test outcomes are decided by exit codes and
	// hooks, never by summary text.
	if cfg.busRole() == "prompt" {
		finalResponse = enforceDenialPrefix(finalResponse, denials.line)
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

	sink.Emit(Event{Kind: EventBatchComplete, Time: time.Now(), Message: fmt.Sprintf("Response (%d bytes, success=%v) → %s", len(finalResponse), batchSuccess, lastMsg.From)})

	if err := bus.Send(lastMsg.From, lastMsg.Action, finalResponse, "response", lastMsg.ID); err != nil {
		sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: fmt.Sprintf("send error: %v", err)})
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
			// Silently skip file-change notify events
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
// toolHasNonZeroExit returns true if the tool output contains a non-zero exit code.
// Used by single-shot auto-complete to only trigger on successful executions.
func toolHasNonZeroExit(output string) bool {
	if idx := strings.LastIndex(output, "Exit code: "); idx >= 0 {
		code := strings.TrimSpace(output[idx+len("Exit code: "):])
		if nl := strings.IndexByte(code, '\n'); nl >= 0 {
			code = code[:nl]
		}
		return code != "0"
	}
	return false
}

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

// toolOutputPreview returns the last non-empty line of tool output, trimmed
// and truncated for display. Returns "" if the output is empty or whitespace.
func toolOutputPreview(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	// Take the last non-empty line (most relevant for command output)
	lines := strings.Split(output, "\n")
	var last string
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		// Skip exit code and truncation markers — already shown elsewhere
		if strings.HasPrefix(line, "Exit code:") || strings.HasPrefix(line, "... [output truncated]") {
			continue
		}
		if line != "" {
			last = line
			break
		}
	}
	if last == "" {
		return ""
	}
	// Replace tabs with spaces to prevent terminal width miscounting
	last = strings.ReplaceAll(last, "\t", "  ")
	if runes := []rune(last); len(runes) > 100 {
		last = string(runes[:100]) + "…"
	}
	return last
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

// processUserChat handles an interactive chat message from the TUI input.
// Unlike processBatch, it maintains a persistent conversation history across
// messages so the user gets a continuous chat experience with context carryover.
func processUserChat(ctx context.Context, cfg Config, ollama *OllamaClient, executor *Executor, tools []ToolDef, systemPrompt string, chatHistory *[]ChatMessage, input string, sink EventSink) {
	// Display the user's input in the activity log
	displayInput := input
	if runes := []rune(displayInput); len(runes) > 120 {
		displayInput = string(runes[:120]) + "…"
	}
	sink.Emit(Event{Kind: EventUserInput, Time: time.Now(), Message: displayInput})

	// Append user message to persistent chat history
	*chatHistory = append(*chatHistory, ChatMessage{Role: "user", Content: input})

	// Build conversation: system prompt + full chat history
	conversation := make([]ChatMessage, 0, len(*chatHistory)+1)
	conversation = append(conversation, ChatMessage{Role: "system", Content: systemPrompt})
	conversation = append(conversation, *chatHistory...)

	// Limit chat history to avoid unbounded growth (keep last 50 messages)
	if len(*chatHistory) > 50 {
		*chatHistory = (*chatHistory)[len(*chatHistory)-50:]
	}

	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-ctx.Done():
			sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: "Chat timed out"})
			return
		default:
		}

		sink.Emit(Event{Kind: EventOllamaCall, Time: time.Now(), Message: fmt.Sprintf("Chat %d/%d — calling model...", turn+1, maxTurns)})
		ollamaStart := time.Now()
		resp, err := ollama.ChatComplete(ctx, conversation, tools)
		ollamaDur := time.Since(ollamaStart)

		if err != nil {
			sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: fmt.Sprintf("Model error: %v", err)})
			*chatHistory = append(*chatHistory, ChatMessage{Role: "assistant", Content: "Error: " + err.Error()})
			return
		}
		if len(resp.Choices) == 0 {
			sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: "Empty response from model"})
			return
		}

		choice := resp.Choices[0]

		// Fallback: extract tool calls from text when model doesn't use structured API
		if len(choice.Message.ToolCalls) == 0 && choice.Message.Content != "" {
			extracted := ExtractToolCalls(choice.Message.Content, toolNames(tools))
			if len(extracted) > 0 {
				choice.Message.ToolCalls = extracted
				choice.Message.Content = ""
			}
		}

		sink.Emit(Event{Kind: EventOllamaResponse, Time: time.Now(), Message: fmt.Sprintf("Response in %.1fs, %d tool call(s)", ollamaDur.Seconds(), len(choice.Message.ToolCalls))})

		// No tool calls → pure text response, we're done
		if len(choice.Message.ToolCalls) == 0 {
			text := choice.Message.Content
			*chatHistory = append(*chatHistory, ChatMessage{Role: "assistant", Content: text})

			// Show response in activity log (truncated for display)
			displayText := text
			if runes := []rune(displayText); len(runes) > 200 {
				displayText = string(runes[:200]) + "…"
			}
			sink.Emit(Event{Kind: EventChatResponse, Time: time.Now(), Message: displayText})
			return
		}

		// Record assistant message with tool calls
		conversation = append(conversation, choice.Message)
		*chatHistory = append(*chatHistory, choice.Message)

		// Execute tool calls
		for i, tc := range choice.Message.ToolCalls {
			toolLabel := toolCallLabel(tc)
			sink.Emit(Event{Kind: EventToolStart, Time: time.Now(), Message: fmt.Sprintf("%s [%d/%d]", toolLabel, i+1, len(choice.Message.ToolCalls))})

			toolStart := time.Now()
			toolOutput := executor.Execute(ctx, tc)
			toolDur := time.Since(toolStart)

			exitInfo := toolExitInfo(tc, toolOutput)
			sink.Emit(Event{Kind: EventToolComplete, Time: time.Now(), Message: fmt.Sprintf("%s (%.1fs%s)", tc.Function.Name, toolDur.Seconds(), exitInfo)})

			if preview := toolOutputPreview(toolOutput); preview != "" {
				sink.Emit(Event{Kind: EventToolOutput, Time: time.Now(), Message: preview})
			}

			toolMsg := ChatMessage{
				Role:       "tool",
				Content:    toolOutput,
				ToolCallID: tc.ID,
			}
			conversation = append(conversation, toolMsg)

			// Store truncated output in chat history to prevent context bloat.
			// The full output is in the current conversation; history only needs
			// enough context for future turns.
			histOutput := toolOutput
			if len(histOutput) > maxChatToolOutput {
				histOutput = histOutput[:maxChatToolOutput] + "\n... [truncated for history]"
			}
			*chatHistory = append(*chatHistory, ChatMessage{
				Role:       "tool",
				Content:    histOutput,
				ToolCallID: tc.ID,
			})
		}
	}

	sink.Emit(Event{Kind: EventError, Time: time.Now(), Message: fmt.Sprintf("Max turns (%d) reached for chat", maxTurns)})
}
