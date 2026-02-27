package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// AgentPollInterval is the sleep between inbox checks when idle.
	AgentPollInterval = 3 * time.Second
	// AgentMaxToolTurns is the maximum tool-calling loop iterations.
	AgentMaxToolTurns = 20
)

// AgentConfig holds configuration for the local LLM agent loop.
type AgentConfig struct {
	Role    string       // agent definition role (git, build, etc.) — for tools, skills, agent def
	BusRole string       // bus identity role (commit, build, etc.) — for inbox, lock, send, history
	Session string
	Ollama  OllamaConfig
}

// busRole returns the bus identity role, falling back to Role if BusRole is empty.
func (c AgentConfig) busRole() string {
	if c.BusRole != "" {
		return c.BusRole
	}
	return c.Role
}

// agentState tracks mutable state across the agent's main loop iterations.
type agentState struct {
	consecutiveFailures int
}

// ollamaFailSentinelThreshold is the number of consecutive Ollama failures
// before writing a sentinel file for the watcher to detect.
const ollamaFailSentinelThreshold = 3

// runStty runs stty with the given arguments, explicitly passing os.Stdin
// so stty can see the controlling terminal. Go's exec.Command defaults
// nil Stdin to /dev/null, which causes stty to silently fail.
func runStty(args ...string) error {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// suppressAgentEcho disables terminal echo and drains stdin to prevent tmux
// send-keys notifications from displaying in the agent output.
func suppressAgentEcho() func() {
	_ = runStty("-echo")
	go io.Copy(io.Discard, os.Stdin)
	return func() {
		_ = runStty("echo")
	}
}

// AgentLoop runs the main agent loop: polls inbox, calls Ollama, executes tools,
// sends responses back via the bus. Blocks until context is cancelled.
func AgentLoop(ctx context.Context, cfg AgentConfig) error {
	// Suppress tmux send-keys echo
	restoreEcho := suppressAgentEcho()
	defer restoreEcho()

	client := NewOllamaClient(cfg.Ollama)
	executor := NewToolExecutor(cfg.Role)
	tools := BuildToolDefs(cfg.Role)

	// Build system prompt once
	systemPrompt := buildSystemPrompt(cfg.Role)

	// Verify Ollama connectivity and model availability
	healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
	err := client.CheckHealth(healthCtx)
	healthCancel()

	if err != nil {
		if errors.Is(err, ErrModelNotFound) {
			// Auto-pull the model
			fmt.Fprintf(os.Stderr, "[agent] Model not found, pulling automatically...\n")
			pullCtx, pullCancel := context.WithTimeout(ctx, 10*time.Minute)
			if pullErr := client.PullModel(pullCtx); pullErr != nil {
				pullCancel()
				return fmt.Errorf("auto-pull failed: %w", pullErr)
			}
			pullCancel()
		} else {
			return fmt.Errorf("Ollama health check failed: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "[agent] Connected to Ollama (%s), model: %s\n", cfg.Ollama.BaseURL, cfg.Ollama.Model)

	state := &agentState{}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Lock while processing — use bus identity for inbox/lock operations
		busID := cfg.busRole()
		if HasMessages(cfg.Session, busID) {
			if err := Lock(cfg.Session, busID); err != nil {
				fmt.Fprintf(os.Stderr, "[agent] lock error: %v\n", err)
			}

			msgs, err := Receive(cfg.Session, busID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[agent] receive error: %v\n", err)
				_ = Unlock(cfg.Session, busID)
				continue
			}

			if len(msgs) > 0 {
				processMessages(ctx, cfg, client, executor, tools, systemPrompt, msgs, state)
			}

			_ = Unlock(cfg.Session, busID)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(AgentPollInterval):
		}
	}
}

// processMessages handles a batch of inbox messages through the Ollama conversation loop.
func processMessages(ctx context.Context, cfg AgentConfig, client *OllamaClient, executor *ToolExecutor, tools []ToolDef, systemPrompt string, msgs []Message, state *agentState) {
	// Build user content from all messages
	var userContent strings.Builder
	var lastMsg Message
	for _, m := range msgs {
		lastMsg = m
		userContent.WriteString(fmt.Sprintf("[%s → %s] %s\n", m.From, m.Action, m.Payload))
	}

	// Fresh conversation each time (system + user)
	conversation := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent.String()},
	}

	// Tool-calling loop
	var finalResponse string
	ollamaError := false
	for turn := 0; turn < AgentMaxToolTurns; turn++ {
		resp, err := client.ChatComplete(ctx, conversation, tools)
		if err != nil {
			finalResponse = fmt.Sprintf("Error calling Ollama: %v", err)
			ollamaError = true
			break
		}

		if len(resp.Choices) == 0 {
			finalResponse = "Error: empty response from Ollama"
			ollamaError = true
			break
		}

		choice := resp.Choices[0]
		conversation = append(conversation, choice.Message)

		// If no tool calls, we have our final response
		if len(choice.Message.ToolCalls) == 0 {
			finalResponse = choice.Message.Content
			break
		}

		// Execute tool calls and feed results back
		for _, tc := range choice.Message.ToolCalls {
			result := executor.Execute(ctx, tc)

			// Log bash commands to history
			if tc.Function.Name == "bash" {
				logBashToHistory(cfg, tc, result)
			}

			conversation = append(conversation, ChatMessage{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	// Track consecutive Ollama failures for health monitoring
	if ollamaError {
		state.consecutiveFailures++
		fmt.Fprintf(os.Stderr, "[agent] Ollama failure #%d\n", state.consecutiveFailures)
		if state.consecutiveFailures >= ollamaFailSentinelThreshold {
			if err := WriteOllamaFailSentinel(cfg.Session, cfg.busRole(), state.consecutiveFailures); err != nil {
				fmt.Fprintf(os.Stderr, "[agent] failed to write failure sentinel: %v\n", err)
			}
		}
	} else {
		if state.consecutiveFailures > 0 {
			fmt.Fprintf(os.Stderr, "[agent] Ollama recovered after %d failures\n", state.consecutiveFailures)
			ClearOllamaFailSentinel(cfg.Session, cfg.busRole())
		}
		state.consecutiveFailures = 0
	}

	// Send response back via bus
	if finalResponse == "" {
		finalResponse = "(no response generated)"
	}

	// Truncate very long responses for the bus message
	if len(finalResponse) > 4000 {
		finalResponse = finalResponse[:4000] + "\n... [truncated]"
	}

	replyMsg := NewMessage(cfg.busRole(), lastMsg.From, "response", lastMsg.Action, finalResponse, lastMsg.ID)
	if err := Send(cfg.Session, replyMsg); err != nil {
		fmt.Fprintf(os.Stderr, "[agent] send response error: %v\n", err)
	}
}

// buildSystemPrompt assembles the system prompt from agent definition,
// shared prompt, skills, context, and session resume.
func buildSystemPrompt(role string) string {
	var parts []string

	// 1. Agent definition — read from the same places muxcode-agent.sh looks
	if def := readAgentDefinition(role); def != "" {
		parts = append(parts, def)
	}

	// 2. Shared coordination prompt
	parts = append(parts, SharedPrompt(role))

	// 3. Skills (if available)
	if skills := agentSkillPrompt(role); skills != "" {
		parts = append(parts, skills)
	}

	// 4. Context.d files
	if ctxPrompt := agentContextPrompt(role); ctxPrompt != "" {
		parts = append(parts, ctxPrompt)
	}

	// 5. Session resume (memory)
	if resume := agentResumeContext(role); resume != "" {
		parts = append(parts, resume)
	}

	return strings.Join(parts, "\n\n")
}

// readAgentDefinition reads the agent definition markdown for a role.
// Searches: .claude/agents/<name>.md → ~/.config/muxcode/agents/<name>.md → install dir
func readAgentDefinition(role string) string {
	name := agentFileName(role)
	if name == "" {
		return ""
	}

	// Search paths in priority order
	paths := []string{
		fmt.Sprintf(".claude/agents/%s.md", name),
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		paths = append(paths, fmt.Sprintf("%s/.config/muxcode/agents/%s.md", home, name))
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// Strip YAML frontmatter (---...---)
		content := string(data)
		return stripFrontmatter(content)
	}

	return ""
}

// agentFileName maps role names to agent definition filenames.
// Mirrors agent_name() in muxcode-agent.sh.
func agentFileName(role string) string {
	switch role {
	case "edit":
		return "code-editor"
	case "build":
		return "code-builder"
	case "test":
		return "test-runner"
	case "review":
		return "code-reviewer"
	case "deploy":
		return "infra-deployer"
	case "runner":
		return "command-runner"
	case "git", "commit":
		return "git-manager"
	case "analyst", "analyze":
		return "editor-analyst"
	case "docs":
		return "doc-writer"
	case "research":
		return "code-researcher"
	case "watch":
		return "log-watcher"
	case "pr-read":
		return "pr-reader"
	default:
		return role
	}
}

// stripFrontmatter removes YAML frontmatter (--- delimited) from markdown.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	// Find second ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return content
	}
	// Skip past the second --- and the newline
	after := rest[idx+4:]
	if len(after) > 0 && after[0] == '\n' {
		after = after[1:]
	}
	return after
}

// logBashToHistory appends a bash command execution to the role's history JSONL.
func logBashToHistory(cfg AgentConfig, tc ToolCall, result string) {
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

	// Determine outcome and exit code from result
	outcome := "success"
	exitCode := "0"
	if strings.Contains(result, "timed out") {
		outcome = "failure"
		exitCode = "124" // timeout
	} else if strings.Contains(result, "not allowed") {
		outcome = "failure"
		exitCode = "126" // permission denied
	} else if idx := strings.LastIndex(result, "Exit code: "); idx >= 0 {
		outcome = "failure"
		code := strings.TrimSpace(result[idx+len("Exit code: "):])
		if nl := strings.IndexByte(code, '\n'); nl >= 0 {
			code = code[:nl]
		}
		exitCode = code
	}

	// Truncate output for history
	output := result
	if len(output) > 2000 {
		output = output[:2000] + "..."
	}

	entry := map[string]interface{}{
		"ts":        time.Now().Unix(),
		"summary":   args.Command,
		"exit_code": exitCode,
		"command":   args.Command,
		"output":    output,
		"outcome":   outcome,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	historyPath := HistoryPath(cfg.Session, cfg.busRole())
	f, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// agentSkillPrompt returns the skills prompt for a role.
func agentSkillPrompt(role string) string {
	skills, err := SkillsForRole(role)
	if err != nil || len(skills) == 0 {
		return ""
	}
	return FormatSkillsPrompt(skills)
}

// agentContextPrompt returns the context.d prompt for a role.
func agentContextPrompt(role string) string {
	files, err := AllContextFilesForRole(role)
	if err != nil || len(files) == 0 {
		return ""
	}
	return FormatContextPrompt(files)
}

// agentResumeContext returns session resume content from memory.
func agentResumeContext(role string) string {
	ctx, err := ResumeContext(role)
	if err != nil || ctx == "" {
		return ""
	}
	return "## Session Resume\n\n" + ctx
}
