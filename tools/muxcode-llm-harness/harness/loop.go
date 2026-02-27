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
)

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
		fmt.Fprintf(os.Stderr, "[harness] Warning: could not resolve tools: %v\n", err)
	}

	// Build tool definitions for Ollama
	tools := BuildToolDefs(patterns)

	// Initialize executor
	executor := NewExecutor(patterns)

	// Initialize Ollama client
	ollama := NewOllamaClient(cfg.OllamaURL, cfg.OllamaModel)

	// Verify Ollama connectivity
	healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
	err = ollama.CheckHealth(healthCtx)
	healthCancel()

	if err != nil {
		return fmt.Errorf("Ollama health check failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[harness] Connected to Ollama (%s), model: %s\n", cfg.OllamaURL, cfg.OllamaModel)
	fmt.Fprintf(os.Stderr, "[harness] Tools: %d patterns, %d tool defs\n", len(patterns), len(tools))

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
		fmt.Fprintf(os.Stderr, "[harness] Warning: could not write marker %s: %v\n", markerPath, err)
	} else {
		defer os.Remove(markerPath)
	}

	fmt.Fprintf(os.Stderr, "[harness] System prompt: %d bytes\n", len(systemPrompt))
	if cfg.BusRole != "" && cfg.BusRole != cfg.Role {
		fmt.Fprintf(os.Stderr, "[harness] Agent role: %s, bus identity: %s\n", cfg.Role, cfg.BusRole)
	}
	fmt.Fprintf(os.Stderr, "[harness] Ready, polling inbox for %s...\n", busRole)

	// Initialize filter — use bus identity for self-send detection
	filter := NewFilter(busRole)

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

		inboxPath := cfg.InboxPath()

		if bus.HasMessages(inboxPath) {
			if err := bus.Lock(); err != nil {
				fmt.Fprintf(os.Stderr, "[harness] lock error: %v\n", err)
			}

			msgs, err := bus.ConsumeInbox()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[harness] consume error: %v\n", err)
				_ = bus.Unlock()
				continue
			}

			if len(msgs) > 0 {
				filter.Reset()
				processBatch(ctx, cfg, bus, ollama, executor, tools, systemPrompt, filter, msgs)
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
func processBatch(ctx context.Context, cfg Config, bus *BusClient, ollama *OllamaClient, executor *Executor, tools []ToolDef, systemPrompt string, filter *Filter, msgs []Message) {
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
	fmt.Fprintf(os.Stderr, "[harness] Processing %d message(s) from %s: %s\n",
		len(msgs), lastMsg.From, lastMsg.Action)

	// Fresh conversation: system + task
	conversation := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: taskContent},
	}

	// Tool-calling loop
	var finalResponse string
	toolsExecuted := false
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	for turn := 0; turn < maxTurns; turn++ {
		resp, err := ollama.ChatComplete(ctx, conversation, tools)
		if err != nil {
			finalResponse = fmt.Sprintf("Error calling Ollama: %v", err)
			break
		}

		if len(resp.Choices) == 0 {
			finalResponse = "Error: empty response from Ollama"
			break
		}

		choice := resp.Choices[0]
		conversation = append(conversation, choice.Message)

		// If no tool calls, we have our final response
		if len(choice.Message.ToolCalls) == 0 {
			finalResponse = choice.Message.Content
			break
		}

		// Execute tool calls
		allBlocked := true
		for _, tc := range choice.Message.ToolCalls {
			result := filter.Check(tc)

			var toolOutput string
			if result.Blocked {
				toolOutput = result.Reason
				fmt.Fprintf(os.Stderr, "[harness] BLOCKED: %s\n", result.Reason)
			} else {
				allBlocked = false
				toolsExecuted = true
				toolOutput = executor.Execute(ctx, tc)

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

		// If ALL tool calls were blocked, inject a corrective user message
		// to strongly redirect the LLM
		if allBlocked {
			conversation = append(conversation, ChatMessage{
				Role:    "user",
				Content: "All your tool calls were blocked. Your task is already in this conversation. Execute it using the appropriate commands (NOT muxcode-agent-bus inbox). If you have completed the task, provide your final response as text.",
			})
		}
	}

	// If tools were executed but the final response looks like narration
	// instead of a summary, do one more call with no tools to force a summary.
	if toolsExecuted && looksLikeNarration(finalResponse) {
		fmt.Fprintf(os.Stderr, "[harness] Final response looks like narration, requesting summary...\n")
		conversation = append(conversation, ChatMessage{
			Role:    "user",
			Content: "You already executed the commands above. Now provide ONLY a short factual summary of the result. Start with the outcome: succeeded or failed. Do not describe what you plan to do — just summarize what already happened.",
		})
		resp, err := ollama.ChatComplete(ctx, conversation, nil) // no tools — text only
		if err == nil && len(resp.Choices) > 0 && resp.Choices[0].Message.Content != "" {
			finalResponse = resp.Choices[0].Message.Content
		}
	}

	// Send response
	if finalResponse == "" {
		finalResponse = "(no response generated — tool loop exhausted)"
	}

	// Truncate very long responses
	if len(finalResponse) > 4000 {
		finalResponse = finalResponse[:4000] + "\n... [truncated]"
	}

	fmt.Fprintf(os.Stderr, "[harness] Response (%d bytes) → %s\n", len(finalResponse), lastMsg.From)

	if err := bus.Send(lastMsg.From, lastMsg.Action, finalResponse, "response", lastMsg.ID); err != nil {
		fmt.Fprintf(os.Stderr, "[harness] send error: %v\n", err)
	}
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
