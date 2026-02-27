package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// PollInterval is the sleep between inbox checks when idle.
	PollInterval = 3 * time.Second
)

// Run is the main entry point. It initializes the harness and enters the
// polling loop. Blocks until context is cancelled.
func Run(ctx context.Context, cfg Config) error {
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

	fmt.Fprintf(os.Stderr, "[harness] Processing %d message(s) from %s: %s\n",
		len(msgs), lastMsg.From, lastMsg.Action)

	// Fresh conversation: system + task
	conversation := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: taskContent},
	}

	// Tool-calling loop
	var finalResponse string
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

// logToolToHistory extracts command info and logs to the role's history JSONL.
func logToolToHistory(bus *BusClient, tc ToolCall, result string) {
	var args struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(tc.Function.Arguments, &args)

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
