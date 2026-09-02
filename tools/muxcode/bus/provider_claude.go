package bus

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// ClaudeCodeProvider implements the Provider interface for Claude Code CLI.
type ClaudeCodeProvider struct{}

func (p *ClaudeCodeProvider) Name() string { return "claude" }

// ConfigureLaunch populates Claude Code-specific fields in the LaunchConfig:
// agent file resolution, model flags, permission flags, tool profiles, shared prompt.
//
// AgentName and AgentJSON are set together or not at all (MUX-136). Every
// resolved tier is read and forwarded as --agents JSON — including a
// project-local .claude/agents/ file Claude could resolve by name on its own,
// which used to travel as a bare name. Claude resolves a bare name against
// the project dir and its own ~/.claude/agents/, never against muxcode's
// user tier, so a name without its definition comes up with default tools.
func (p *ClaudeCodeProvider) ConfigureLaunch(cfg *LaunchConfig, role string) {
	agentName := AgentFileName(role)
	cfg.Agent = agentName
	installDir := resolveInstallDir()

	if agentName != "" {
		agentFile, _ := ResolveAgentFile(agentName, installDir)
		cfg.AgentFile = agentFile
		if agentFile != "" {
			cfg.AgentName, cfg.AgentJSON, cfg.AgentJSONErr = boundDefinition(agentName, agentFile)
		}
	}

	// Claude model selection
	model := resolveClaudeModel(role)
	if model != "" {
		cfg.ModelFlags = []string{"--model", model}
	}

	// Permission mode
	cfg.PermFlags = []string{"--dangerously-skip-permissions"}

	// Tool profiles
	tools := ResolveTools(role)
	for _, tool := range tools {
		cfg.ToolFlags = append(cfg.ToolFlags, "--allowedTools", tool)
	}

	// Shared prompt
	cfg.SharedPrompt = BuildSharedPrompt(role)
}

// boundDefinition reads a resolved definition file into the --agent/--agents
// pair. Any failure — unreadable file, or a frontmatter key the forwarder
// cannot carry — leaves both empty and returns why, so refuseWithoutDefinition
// names the cause instead of the launcher falling back to a bare name or a
// reduced JSON.
func boundDefinition(agentName, agentFile string) (name, agentJSON string, err error) {
	data, err := os.ReadFile(agentFile)
	if err != nil {
		return "", "", err
	}
	fm, body := ExtractFrontmatter(string(data))
	if fm.Description == "" {
		fm.Description = agentName
	}
	if agentJSON, err = BuildAgentsJSON(agentName, fm, body); err != nil {
		return "", "", err
	}
	return agentName, agentJSON, nil
}

// BuildExecArgs constructs Claude Code CLI arguments.
//
// --agent <name> is emitted only alongside --agents <json>. A config carrying
// a name without its definition is launched as definition-less — inline
// fallback prompt, no agent flags — never as a bare name for Claude to resolve
// on its own (MUX-136; pinned by TestClaudeBuildExecArgs_NoBareAgentFlag).
func (p *ClaudeCodeProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) {
	var args []string

	hasDefinition := cfg.AgentName != "" && cfg.AgentJSON != ""
	if hasDefinition {
		args = append(args, "--agent", cfg.AgentName, "--agents", cfg.AgentJSON)
	}

	args = append(args, cfg.ModelFlags...)
	args = append(args, cfg.PermFlags...)
	args = append(args, cfg.ToolFlags...)

	if cfg.SharedPrompt != "" {
		args = append(args, "--append-system-prompt", cfg.SharedPrompt)
	}

	if !hasDefinition {
		if prompt := InlineFallbackPrompt(cfg.Role); prompt != "" {
			args = append(args, "--append-system-prompt", prompt)
		}
	}

	return cfg.CLI, args
}

// IsIdle returns true if the agent's tmux pane shows the Claude Code idle prompt (❯).
// Scans the last 8 lines because Claude Code renders decorative UI elements
// (borders, "? for shortcuts") below the ❯ prompt.
func (p *ClaudeCodeProvider) IsIdle(session, role string) bool {
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-8")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	content := string(out)

	// Check for thinking/ideating state first — during Claude Code's extended
	// thinking phase, the ❯ prompt is visible but the agent cannot accept input.
	// Injecting send-keys during this state wastes context tokens and causes
	// notification spam.
	if isClaudeThinking(content) {
		return false
	}

	lines := strings.Split(content, "\n")
	// Scan all lines — the ❯ prompt may not be the last non-empty line
	// due to Claude Code's decorative footer (borders, help text).
	// Accept lines that are exactly ❯ OR start with "❯ " (prompt with
	// stale text in the input buffer). An agent at "❯ push it" is still
	// idle — it's at the prompt with leftover text, not actively executing.
	// When the agent is truly active, ❯ appears mid-line in tool output
	// or status text, not as the line prefix after trimming.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == idlePromptChar || strings.HasPrefix(trimmed, idlePromptChar+" ") {
			return true
		}
	}
	return false
}

// isClaudeThinking returns true if the pane content indicates Claude Code is
// actively working (extended thinking, e.g. Ideating/Combobulating/Spelunking,
// or a long-running tool). During these phases the ❯ prompt is visible but the
// agent cannot process input — send-keys injections pile into the input buffer
// and cause a notification storm.
//
// Detection is GLYPH-INDEPENDENT. The leading spinner character animates across
// many code points (✢ U+2722, ✳ U+2733, ✶ U+2736, ✺ U+273A, ✻ U+273B,
// ✽ U+273D, …), so keying off a fixed glyph set misses frames — e.g. a
// "✽ Combobulating…" frame slipped past a ✢/✻-only check and made a busy agent
// look idle, triggering repeated "You have N new messages" injections. Instead
// we match the live-spinner SIGNATURE, which is stable across animation frames
// and Claude Code versions:
//
//	in-progress (agent CANNOT accept input):
//	  "✽ Combobulating… (13m 9s · ↓ 17.8k tokens · esc to interrupt)"
//	  "✢ Ideating… (11m 18s · ↓ 634 tokens)"
//	  "✻ Cogitating (12s · esc to interrupt)"
//
//	completed (agent IS idle) — the recap feature's past-tense summary:
//	  "✻ Cooked for 1m 47s"   "✻ Cogitated for 20s"
//
// The signature: an explicit "esc to interrupt" hint, OR a gerund ellipsis "…"
// alongside the "(elapsed · tokens · …)" counter separator " · ". Completed
// recap lines have neither, so an idle agent is correctly seen as idle (else the
// daemon would never deliver its pending inbox messages).
func isClaudeThinking(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if isClaudeStatusFooter(trimmed) {
			continue
		}
		if strings.Contains(trimmed, "esc to interrupt") {
			return true
		}
		if strings.Contains(trimmed, "…") && strings.Contains(trimmed, " · ") {
			return true
		}
		// Bare spinner, counter not yet rendered: "✶ Hullaballooing…".
		//
		// A turn renders the spinner gerund BEFORE its "(elapsed · tokens · esc
		// to interrupt)" counter appears, so for the first seconds of a turn the
		// line matches neither check above — no " · ", no interrupt hint. The ❯
		// prompt is always present in the input box, so IsIdle read a working
		// agent as IDLE, and the daemon's 5s poll injected a wake-up straight
		// into the running turn and killed it:
		//
		//	⏺ Process
		//	  ⎿  Interrupted · What should Claude do instead?
		//
		// That is the startup/interrupt loop that mangled review and plan.
		if isClaudeSpinnerLine(trimmed) {
			return true
		}
	}
	return false
}

// isClaudeSpinnerLine reports whether a line is Claude Code's live activity
// spinner — a spinner glyph followed by a gerund ellipsis, e.g. "✶ Ideating…".
//
// Matched by GLYPH RANGE, not a fixed glyph set: the spinner animates across
// many dingbat code points (✢ U+2722, ✳ U+2733, ✶ U+2736, ✺ U+273A, ✻ U+273B,
// ✽ U+273D, …) and a hardcoded set silently misses frames. The range excludes
// the tool bullet ⏺ (U+23FA) and the result glyph ⎿ (U+23BF), so completed tool
// output like "⏺ Running 1 shell command…" is NOT a spinner — the agent is back
// at the prompt there and must stay deliverable.
//
// The "…" requirement is what separates a live spinner from the past-tense recap
// an IDLE agent leaves behind ("✻ Cooked for 1m 47s"), which carries the same
// glyph but no ellipsis. Without that, an idle agent would never be delivered.
func isClaudeSpinnerLine(trimmed string) bool {
	r, size := utf8.DecodeRuneInString(trimmed)
	if size == 0 || r < 0x2720 || r > 0x273F {
		return false
	}
	return strings.Contains(trimmed, "…")
}

// isClaudeStatusFooter reports whether a line is Claude Code's persistent status
// footer, e.g.
//
//	"⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← for agents"
//
// The footer is NOT a working signature: it renders the "esc to interrupt" hint
// even when the agent sits idle at the ❯ prompt (notably whenever text is parked
// in the composer, where Esc would clear the buffer). Counting it as "thinking"
// wedged agents hard — IsIdle went permanently false, so the daemon never
// delivered the pending inbox, the force-wake path re-injected, that parked more
// text, and the footer kept the hint alive. Both the "review force-woken 3×
// without draining its inbox" churn alert and the daemon's idle-delivery stall
// trace back here. Skip the footer and judge only the spinner line above it.
// The mode-cycle hint is the signature: it is the one part of the footer present
// in EVERY permission mode. Keying only on "⏵⏵"/"bypass permissions on" matched
// the bypass-mode footer and missed the others —
//
//	⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · …
//	⏸  plan mode on (shift+tab to cycle) · esc to interrupt · …
//
// so an agent in any non-bypass mode fell straight back into the wedge this
// function exists to prevent. The glyph and the mode name both vary; the
// "(shift+tab to cycle)" hint does not.
func isClaudeStatusFooter(trimmed string) bool {
	return strings.Contains(trimmed, "shift+tab to cycle") ||
		strings.HasPrefix(trimmed, "⏵⏵") ||
		strings.Contains(trimmed, "bypass permissions on")
}

// IsAlive checks whether the agent's tmux pane is running Claude Code.
//
// Detection heuristic (in order):
//  1. IsIdle sees ❯ → alive (idle Claude Code)
//  2. Last non-empty line ends with shell prompt ($, %, ->, >) and no ❯ → dead
//  3. "muxcode agent launch" or "claude" or "opencode" in capture → alive (starting up)
//  4. Default: assume alive if indeterminate
//
// Shell prompt check (2) must come before startup text check (3) because
// Claude Code's exit message contains "claude" in "Resume this session
// with: claude --resume ..." which would false-positive the startup check.
func (p *ClaudeCodeProvider) IsAlive(session, role string) bool {
	// 1. Idle check (❯ present = Claude Code is running)
	if p.IsIdle(session, role) {
		return true
	}

	// Capture pane content for heuristic checks
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-5")
	out, err := cmd.Output()
	if err != nil {
		// Can't reach pane — assume alive (indeterminate)
		return true
	}

	lines := strings.Split(string(out), "\n")

	// 2. Shell prompt check — bare shell prompt with no ❯ → dead.
	// This must come BEFORE the startup text check because Claude Code's
	// exit message ("Resume this session with: claude --resume ...") contains
	// the word "claude", which would false-positive the startup check.
	if isShellPrompt(lines) {
		return false
	}

	// 3. Startup check — look for agent launcher or CLI text
	// Only reached if we're NOT at a shell prompt (agent is mid-startup).
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "muxcode agent launch") ||
			strings.Contains(trimmed, "claude") ||
			strings.Contains(trimmed, "opencode") {
			return true
		}
	}

	// 4. Default: assume alive
	return true
}

// ClassifyPane determines the startup state of a Claude Code agent pane.
func (p *ClaudeCodeProvider) ClassifyPane(content string) PaneState {
	if strings.Contains(content, "trust this folder") {
		return PaneTrustPrompt
	}
	if strings.Contains(content, "Bypass Permissions") {
		return PaneBypassPrompt
	}
	if strings.Contains(content, "❯") {
		return PaneIdle
	}
	return PaneNotReady
}

// AcceptStartup handles Claude Code startup prompts (trust folder, bypass permissions).
// Returns true if all startup prompts have been handled.
func (p *ClaudeCodeProvider) AcceptStartup(session, pane string, state PaneState) bool {
	switch state {
	case PaneTrustPrompt:
		// Trust prompt — default selection is correct, just confirm
		TmuxSendEnter(pane)
		return false // bypass prompt may follow
	case PaneBypassPrompt:
		// Bypass permissions — move to "Yes, I accept" and confirm
		TmuxSendKeys(pane, "Down")
		time.Sleep(200 * time.Millisecond)
		TmuxSendEnter(pane)
		return true
	case PaneIdle:
		return true
	default:
		return false
	}
}

// SendWakeUp injects "You have new messages" into the agent's tmux pane
// via send-keys. Uses -l (literal) flag for the text to avoid tmux
// interpreting special characters. Text and Enter are sent as separate
// calls with a 200ms delay to give the TUI time to register the text.
//
// No Escape/C-u preamble — stale buffer text is handled by the daemon's
// notifyRetryInterval (15s). The multi-command preamble was the primary
// cause of dropped injections during TUI redraws.
func (p *ClaudeCodeProvider) SendWakeUp(session, role string, force bool) error {
	target := PaneTarget(session, role)
	// Send text with -l (literal) to avoid tmux key interpretation
	cmd := exec.Command("tmux", "send-keys", "-t", target, "-l", "You have new messages")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s failed: %v\n", role, err)
		return err
	}
	// 200ms delay gives Claude Code's TUI time to register the text
	// before the Enter keypress (increased from 100ms for reliability)
	time.Sleep(200 * time.Millisecond)
	// Send Enter separately (not literal — Enter is a tmux key name)
	cmd = exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s failed: %v\n", role, err)
		return err
	}
	return nil
}

// Compact triggers context compaction by injecting /compact into the pane.
// Waits for the agent to become idle first (up to 30 seconds).
func (p *ClaudeCodeProvider) Compact(session, role, target string) error {
	// Wait for agent to reach idle (❯ prompt), max 30 seconds
	idle := false
	for i := 0; i < 30; i++ {
		if p.IsIdle(session, role) {
			idle = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !idle {
		// Agent never became idle — skip silently
		return nil
	}

	// Clear any residual input
	_ = exec.Command("tmux", "send-keys", "-t", target, "Escape").Run()
	time.Sleep(100 * time.Millisecond)
	_ = exec.Command("tmux", "send-keys", "-t", target, "C-u").Run()
	time.Sleep(100 * time.Millisecond)

	// Inject /compact + Enter (separate calls per tmux send-keys convention)
	if err := exec.Command("tmux", "send-keys", "-t", target, "/compact").Run(); err != nil {
		return fmt.Errorf("send /compact: %w", err)
	}
	time.Sleep(200 * time.Millisecond)
	_ = exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
	return nil
}

func (p *ClaudeCodeProvider) SupportsHooks() bool             { return true }
func (p *ClaudeCodeProvider) IdlePromptChar() string          { return idlePromptChar }
func (p *ClaudeCodeProvider) WriteAgentConfig(_ string) error { return nil }

// DetectTaskCompletion is a no-op for Claude Code — hooks handle completion.
func (p *ClaudeCodeProvider) DetectTaskCompletion(_, _, _ string) (bool, bool, string) {
	return false, false, ""
}
