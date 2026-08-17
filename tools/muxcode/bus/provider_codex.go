package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CodexProvider implements the Provider interface for OpenAI Codex CLI
// in interactive TUI mode. Launches `codex -a never --no-alt-screen`
// and uses send-keys to inject prompts, matching the OpenCode pattern.
type CodexProvider struct{}

// --- Provider interface ---

func (p *CodexProvider) Name() string { return "codex" }

// ConfigureLaunch populates Codex-specific fields in the LaunchConfig.
// Resolves agent definition file and shared prompt for WriteAgentConfig.
func (p *CodexProvider) ConfigureLaunch(cfg *LaunchConfig, role string) {
	// Resolve agent file — used to build the AGENTS.md content
	agentName := AgentFileName(role)
	cfg.Agent = agentName
	if agentName != "" {
		installDir := resolveInstallDir()
		agentFile, _ := ResolveAgentFile(agentName, installDir)
		cfg.AgentFile = agentFile
	}

	// Shared prompt (used in AGENTS.md generation)
	cfg.SharedPrompt = BuildSharedPrompt(role)
}

// BuildExecArgs constructs the Codex CLI launch command.
// Uses -a never for automatic approval and --no-alt-screen for
// tmux compatibility (inline mode preserves scrollback).
// Read-only roles (review, analyze) use -a on-request so Codex prompts
// for approval on tool use — this prevents reviewers from running
// tests/builds and analysts from making unintended changes.
// Does NOT use -C (--cd) — that flag changes the agent's working root,
// which would prevent it from seeing the actual project files. Instead,
// WriteAgentConfig writes role-specific AGENTS.md to .codex/AGENTS.md
// at the repo root before each launch.
func (p *CodexProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) {
	args := []string{
		"--no-alt-screen",
	}

	// Read-only roles use on-request approval to enforce permission prompts
	if isReadOnlyCodexRole(cfg.Role) {
		args = append(args, "-a", "on-request")
	} else {
		args = append(args, "-a", "never")
	}

	// Model selection
	model := resolveCodexModel(cfg.Role)
	if model != "" {
		args = append(args, "-m", model)
	}

	return "codex", args
}

// isReadOnlyCodexRole returns true for roles that should use on-request
// approval instead of automatic (-a never). These roles only read code
// (diffs, files) and must not execute builds, tests, or deploys.
func isReadOnlyCodexRole(role string) bool {
	switch role {
	case "review", "analyze":
		return true
	default:
		return false
	}
}

// IsIdle always returns false for TUI mode.
// The TUI has no stable prompt character that can be matched via pane capture.
func (p *CodexProvider) IsIdle(session, role string) bool {
	return false
}

// IsAlive checks whether the Codex TUI is running via pane capture.
// Looks for Codex-specific text or TUI indicators. If the pane shows
// a bare shell prompt, the agent is dead.
func (p *CodexProvider) IsAlive(session, role string) bool {
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-8")
	out, err := cmd.Output()
	if err != nil {
		return true // indeterminate -> assume alive
	}
	lines := strings.Split(string(out), "\n")

	// Shell prompt check first — if at a bare shell prompt, agent is dead.
	// This must come before TUI marker checks because Codex's exit message
	// and error output contain "codex" text and box-drawing characters that
	// would false-positive the TUI checks.
	if isShellPrompt(lines) {
		return false
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Codex TUI markers
		if strings.Contains(trimmed, "codex") || strings.Contains(trimmed, "Codex") {
			return true
		}
		// Codex input prompt indicator — must be a line prefix to avoid
		// false positives from normal output (quotes, diffs, shell commands).
		if strings.HasPrefix(trimmed, "❯") || strings.HasPrefix(trimmed, "›") ||
			(trimmed == ">" || strings.HasPrefix(trimmed, "> ")) {
			return true
		}
		// Box-drawing characters indicate TUI is rendered
		for _, ch := range []string{"─", "│", "╭", "╰", "┌", "└", "╹", "╻"} {
			if strings.Contains(trimmed, ch) {
				return true
			}
		}
	}
	return true // indeterminate -> assume alive
}

// ClassifyPane determines the startup state of a Codex TUI pane.
// Checks for error states first (higher priority), then looks for
// TUI rendering indicators (box-drawing, prompt chars, Codex text).
func (p *CodexProvider) ClassifyPane(content string) PaneState {
	// Check for error states first — these take priority over text matches
	// because error messages often contain "codex" (e.g. "ERROR: codex CLI not found")
	if strings.Contains(content, "Error") || strings.Contains(content, "FATAL") || strings.Contains(content, "ERROR:") {
		return PaneNotReady
	}
	// TUI rendered: look for box-drawing or Codex-specific markers
	for _, ch := range []string{"─", "│", "╭", "╰", "┌", "└", "╹", "╻"} {
		if strings.Contains(content, ch) {
			return PaneIdle
		}
	}
	// Inline mode (--no-alt-screen) may show a text prompt
	if strings.Contains(content, "codex") || strings.Contains(content, "Codex") {
		return PaneIdle
	}
	return PaneNotReady
}

// AcceptStartup handles Codex TUI startup — no action needed once
// the TUI has rendered (ClassifyPane returns PaneIdle).
func (p *CodexProvider) AcceptStartup(session, pane string, state PaneState) bool {
	return state == PaneIdle
}

// SendWakeUp reads the latest pending message from the inbox and injects
// it as text into the Codex TUI input via tmux send-keys. Since Codex
// has no hooks or inbox polling, the message content must be typed directly
// into the prompt. Text and Enter are sent as separate send-keys calls with
// a brief delay to avoid the TUI dropping the Enter key.
//
// IMPORTANT: Uses Peek (not Receive) so the inbox is NOT consumed before the
// injection is verified. If pane injection fails (pane restarting, wrong target,
// tmux error) OR the injected text is confirmed still parked (dropped Enter), the
// message stays in the inbox for retry on the next wake-up cycle. The message is
// consumed — with a verified-inject `delivered` receipt — only after the text is
// confirmed to have left the composer (see confirmInjectionAndConsume).
func (p *CodexProvider) SendWakeUp(session, role string) error {
	target := PaneTarget(session, role)

	// Guard: skip injection if the agent already has an in-flight task.
	// The message was already consumed and injected on a prior wake-up.
	// Re-injecting would create duplicate prompts, wasting tokens and
	// confusing the agent.
	tasks, _ := ListTasks(session, TaskInFlight)
	for _, t := range tasks {
		if t.To == role && time.Now().Unix()-t.SentAt > 5 {
			fmt.Fprintf(os.Stderr, "  [wakeup] skipping %s injection — in-flight task %s:%s exists (%ds old)\n",
				role, t.Action, t.ID[:8], time.Now().Unix()-t.SentAt)
			return nil
		}
	}

	// Read pending messages to build the prompt text (non-destructive peek)
	msgs, err := Peek(session, role)
	if err != nil || len(msgs) == 0 {
		return nil // nothing to inject
	}

	// Deliver a bounded batch so a large inbox cannot build an argv that
	// send-keys rejects outright; the remainder drains on later cycles.
	batch := BoundWakeUpBatch(msgs)
	batchIDs := make(map[string]bool, len(batch))
	for _, msg := range batch {
		batchIDs[msg.ID] = true
	}

	// Build a combined prompt from the batch so none are dropped.
	// Earlier implementations only used the last message and consumed the
	// entire inbox, silently dropping earlier requests.
	// Filter out self-addressed messages to prevent infinite loops where
	// the agent sends a response to itself, which triggers a wake-up,
	// which injects the self-message, which triggers another response.
	var parts []string
	var lastFrom string
	hasRequest := false
	for _, msg := range batch {
		// Skip messages from self — these are loop artifacts
		if NormalizeBusRole(msg.From) == role {
			continue
		}
		text := msg.Payload
		if text == "" {
			text = fmt.Sprintf("[%s request from %s]", msg.Action, msg.From)
		}
		parts = append(parts, text)
		if msg.From != "" {
			lastFrom = msg.From
		}
		if msg.Type == "request" {
			hasRequest = true
		}
	}
	// If the whole batch was self-addressed, consume and discard it (daemon path
	// uses the delivered-kind consume; self-sends are ignored by receipt readers).
	if len(parts) == 0 {
		_, _ = ReceiveDeliveredIDs(session, role, batchIDs)
		return nil
	}
	prompt := strings.Join(parts, " | ")

	// Append reply instruction — Codex agents don't have hooks so they must
	// be explicitly told to reply via the bus after completing the task.
	replyTarget := NormalizeBusRole(lastFrom)
	if replyTarget == "" || !IsKnownRole(replyTarget) {
		replyTarget = "edit"
	}
	// Prepend AND append reply instructions for request messages.
	// Smaller models lose trailing instructions after long tool-use
	// sequences, so the reply command appears both at the start (as a
	// priority directive) and at the end (as a reminder).
	// Response-only wake-ups skip this to avoid infinite echo loops.
	if hasRequest {
		replyCmd := fmt.Sprintf("muxcode send %s response \"<your one-line summary>\" --type response", replyTarget)
		prompt = fmt.Sprintf("IMPORTANT: After completing this task, you MUST run this bash command: %s — ", replyCmd) + prompt
		prompt += fmt.Sprintf(" — REMINDER: Your FINAL step MUST be to EXECUTE (not print): %s", replyCmd)
		prompt += chainInstructionForRole(role)
	}

	// Send text first — do NOT consume inbox until both send-keys succeed
	cmd := exec.Command("tmux", "send-keys", "-t", target, prompt)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s/%s failed: %v\n", role, "codex", err)
		return err
	}
	// Brief delay so the TUI registers the text before Enter
	time.Sleep(150 * time.Millisecond)
	// Send Enter
	cmd = exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s/%s failed: %v\n", role, "codex", err)
		return err
	}

	// send-keys "succeeding" only means tmux accepted the keys — not that the TUI
	// submitted them (a dropped Enter parks the prompt unsent). Confirm the prompt
	// actually left the composer (re-sending Enter if it parked), then consume with
	// a verified-inject `delivered` receipt. If it can't be confirmed, the inbox is
	// left for the daemon's next wake cycle — no drop on a dropped Enter, replacing
	// the old fire-and-hope drain.
	confirmInjectionAndConsume(session, role, target, injectionNeedle(prompt), batchIDs)
	return nil
}

// Compact is a no-op — the Codex TUI manages its own context.
func (p *CodexProvider) Compact(session, role, target string) error {
	return nil
}

// SupportsHooks returns false — Codex CLI's hook system is not integrated.
// Uses graceful degradation (same as OpenCode).
func (p *CodexProvider) SupportsHooks() bool { return false }

// IdlePromptChar returns empty — Codex TUI idle detection is not
// based on a single character.
func (p *CodexProvider) IdlePromptChar() string { return "" }

// WriteAgentConfig writes .codex/{role}/AGENTS.md with shared bus protocol
// instructions and role-specific agent body content. Each role gets its own
// subdirectory to prevent multiple Codex agents from overwriting each other's
// instructions in a mixed or all-Codex session.
func (p *CodexProvider) WriteAgentConfig(role string) error {
	return writeCodexAgentConfig(role)
}

// DetectTaskCompletion analyzes captured pane content from the Codex TUI
// to determine if the agent has finished processing a task.
//
// Detection heuristics (checked in order):
//  1. Active signals (spinners, "thinking") → still running, return false.
//  2. Bus reply output — `muxcode send` in recent lines means the agent
//     already replied to the requester, so the task is done.
//  3. TUI idle prompt — the › or > character reappearing at the bottom
//     of the pane indicates the TUI is ready for new input.
func (p *CodexProvider) DetectTaskCompletion(session, role, paneContent string) (completed bool, errored bool, summary string) {
	if paneContent == "" {
		return false, false, ""
	}

	lines := strings.Split(paneContent, "\n")

	// Check for active signals first — if the agent is still working, don't report
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Spinner/activity indicators
		if strings.Contains(trimmed, "⠋") || strings.Contains(trimmed, "⠙") ||
			strings.Contains(trimmed, "⠹") || strings.Contains(trimmed, "⠸") ||
			strings.Contains(trimmed, "▸") || strings.Contains(trimmed, "thinking") {
			return false, false, "" // still running
		}
	}

	// Scan recent lines (last 10) for bus reply output.
	// When the agent runs `muxcode send <target> response "..."`, the
	// command output appears in the pane — this is a reliable completion
	// signal since it means the agent already sent its result.
	scanStart := len(lines) - 10
	if scanStart < 0 {
		scanStart = 0
	}
	for i := len(lines) - 1; i >= scanStart; i-- {
		trimmed := strings.TrimSpace(lines[i])
		// Bus send output: "Sent response:response to edit"
		if strings.HasPrefix(trimmed, "Sent ") && strings.Contains(trimmed, " to ") {
			// Extract a summary from the preceding agent message
			var lastContentLine string
			for j := i - 1; j >= 0; j-- {
				t := strings.TrimSpace(lines[j])
				if t != "" && !strings.HasPrefix(t, "muxcode ") && !strings.HasPrefix(t, "$") {
					lastContentLine = t
					break
				}
			}
			if lastContentLine == "" {
				lastContentLine = "codex task completed"
			}
			// Check if the send was an error response
			isError := strings.Contains(trimmed, "error") || strings.Contains(trimmed, "failed")
			return true, isError, lastContentLine
		}
	}

	// TUI mode: check if the Codex input prompt (› character) reappeared
	// at the bottom of the pane, indicating the TUI finished processing
	// and is ready for new input. Only check the last 3 lines.
	tuiScanStart := len(lines) - 3
	if tuiScanStart < 0 {
		tuiScanStart = 0
	}
	for i := len(lines) - 1; i >= tuiScanStart; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "›") || strings.HasPrefix(trimmed, ">") {
			// Prompt reappeared — find a summary from the preceding content
			var lastContentLine string
			for j := i - 1; j >= 0; j-- {
				t := strings.TrimSpace(lines[j])
				if t != "" {
					lastContentLine = t
					break
				}
			}
			if lastContentLine == "" {
				lastContentLine = "Task completed"
			}
			return true, false, lastContentLine
		}
	}

	return false, false, ""
}

// --- Agent config generation ---

// CodexAgentConfigDir returns the per-role Codex agent config directory.
// Each role gets .codex/{role}/ to prevent AGENTS.md collisions when
// multiple roles use the Codex provider in the same session.
func CodexAgentConfigDir(role string) string {
	return filepath.Join(".codex", role)
}

// writeCodexAgentConfig generates .codex/AGENTS.md at the repo root with
// shared bus protocol instructions and role-specific agent body content.
// The file is written to .codex/AGENTS.md (not a per-role subdirectory)
// because Codex discovers AGENTS.md relative to its working directory,
// and we do NOT use -C (which would change the working root away from
// the project). WriteAgentConfig is called before each agent launch, so
// the file contains the correct role's instructions when Codex reads it
// at startup. If multiple Codex agents run simultaneously, the last
// writer's role instructions win — the core bus protocol is identical
// across roles and role-specific behavior is also injected via SendWakeUp
// prompts, so the AGENTS.md race is low-impact.
func writeCodexAgentConfig(role string) error {
	dir := ".codex"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// Read source agent definition for role-specific body
	agentName := AgentFileName(role)
	var agentBody string
	if agentName != "" {
		installDir := resolveInstallDir()
		agentFile, _ := ResolveAgentFile(agentName, installDir)
		if agentFile != "" {
			data, err := os.ReadFile(agentFile)
			if err == nil {
				_, body := ExtractFrontmatter(string(data))
				agentBody = adaptBodyForNonHookProvider(body, role)
			}
		}
	}

	// Build shared prompt
	sharedPrompt := BuildSharedPrompt(role)

	var buf strings.Builder
	buf.WriteString("# MuxCode Agent Instructions\n\n")
	buf.WriteString("You are the **" + role + "** agent in a multi-agent coding environment coordinated via a message bus.\n\n")

	buf.WriteString("## CRITICAL: Reply Protocol\n\n")
	buf.WriteString("**Your work is WORTHLESS unless you send the result back.** After completing ANY task, you MUST execute this bash command:\n\n")
	buf.WriteString("```bash\n")
	buf.WriteString("muxcode send edit response \"<summary of what you found or did>\" --type response\n")
	buf.WriteString("```\n\n")
	buf.WriteString("If a different agent (not edit) requested the task, reply to that agent instead.\n\n")
	buf.WriteString("**This is a bash command. You MUST run it using your shell/bash/terminal tool. ")
	buf.WriteString("If you write it as text output instead of executing it, the message is silently lost ")
	buf.WriteString("and the requester hangs forever waiting for your response. EXECUTE IT.**\n\n")

	buf.WriteString("## Bus Commands\n\n")
	buf.WriteString("- Send messages: `muxcode send <target> <action> \"<message>\"`\n")
	buf.WriteString("- Read inbox: `muxcode inbox`\n")
	buf.WriteString("- Read memory: `muxcode memory context`\n\n")

	buf.WriteString("## Targets\n\n")
	buf.WriteString("- `edit` - orchestrator, code editor\n")
	buf.WriteString("- `build` - build runner\n")
	buf.WriteString("- `test` - test runner\n")
	buf.WriteString("- `review` - code reviewer\n")
	buf.WriteString("- `commit` - git operations\n")
	buf.WriteString("- `deploy` - infrastructure deployer\n\n")

	buf.WriteString("## Rules\n\n")
	buf.WriteString("- Process the task immediately, do not ask for confirmation\n")
	buf.WriteString("- ALWAYS reply to the requesting agent when done using `muxcode send`\n")
	buf.WriteString("- Do not run commands outside your role's scope\n")

	// Role-specific restrictions
	switch role {
	case "review":
		buf.WriteString("- **NEVER run tests, builds, or any command that executes code.** You are a reviewer — analyze code by reading it, not by running it.\n")
		buf.WriteString("- Do NOT run `go test`, `pytest`, `jest`, `pnpm test`, `make`, `./build.sh`, or any build/test command.\n")
		buf.WriteString("- Your only allowed commands are: `git diff`, `git log`, `git status`, `git show`, `git blame`, `muxcode`, and file reading tools.\n")
	case "build":
		buf.WriteString("- Your role is to run builds and report results. Do not run tests.\n")
	case "test":
		buf.WriteString("- Your role is to run tests and report results. Do not run builds unless needed for testing.\n")
	}
	buf.WriteString("\n")

	// Role-specific body from agent definition
	if agentBody != "" {
		buf.WriteString("## Role Instructions\n\n")
		buf.WriteString(agentBody)
		buf.WriteString("\n\n")
	}

	// Shared prompt
	if sharedPrompt != "" {
		buf.WriteString(sharedPrompt)
		buf.WriteString("\n")
	}

	outPath := filepath.Join(dir, "AGENTS.md")
	return os.WriteFile(outPath, []byte(buf.String()), 0o644)
}

// resolveCodexModel returns the Codex model for a role.
// Resolution: generic per-role env → Codex-specific per-role env → global Codex env → role default.
func resolveCodexModel(role string) string {
	// Generic per-role env var (MUXCODE_{ROLE}_MODEL) - shared across providers
	if v := os.Getenv(RoleModelEnvVar(role)); v != "" {
		return v
	}

	// Codex-specific per-role env var (MUXCODE_{ROLE}_CODEX_MODEL)
	envVar := RoleCodexModelEnvVar(role)
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	if v := os.Getenv("MUXCODE_CODEX_MODEL"); v != "" {
		return v
	}
	return RoleCodexModelDefault(role)
}
