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
//
// hooks records whether the role runs on the hook road (MUX-159): read from
// the activation marker by ResolveProvider, decided by ConfigureLaunch and
// WriteAgentConfig through PrepareCodexHooks. A zero CodexProvider is the
// scrape road, byte-for-byte what it was before hooks existed.
type CodexProvider struct {
	hooks bool
}

// HooksEnabled reports whether this instance is on the hook road.
func (p *CodexProvider) HooksEnabled() bool { return p.hooks }

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

	// The road is decided first: SharedPrompt resolves the provider again and
	// must see the same capability this instance carries.
	if active, err := PrepareCodexHooks(BusSession(), role); err == nil {
		p.hooks = active
	}

	// Shared prompt (used in AGENTS.md generation)
	cfg.SharedPrompt = BuildSharedPrompt(role)
}

// BuildExecArgs constructs the Codex CLI launch command.
// Uses -a never for automatic approval and --no-alt-screen for
// tmux compatibility (inline mode preserves scrollback).
// Read-only roles (review, analyze) use -a on-request so Codex asks before
// escalating beyond its sandbox. This is an approval policy, NOT a sandbox or
// an allowlist: those roles get no -s flag, so in-sandbox builds and tests
// still run unprompted. What forbids them executing is their role
// instructions; on-request only surfaces the attempts that reach outside, and
// checkCodexApprovals answers those, since nobody is at the pane.
// Does NOT use -C (--cd) — that flag changes the agent's working root,
// which would prevent it from seeing the actual project files. Instead,
// WriteAgentConfig writes role-specific AGENTS.md to .codex/AGENTS.md
// at the repo root before each launch.
func (p *CodexProvider) BuildExecArgs(cfg *LaunchConfig) (string, []string) {
	args := []string{
		"--no-alt-screen",
	}

	// Read-only roles ask before escalating; everything else runs unprompted
	if isReadOnlyCodexRole(cfg.Role) {
		args = append(args, "-a", "on-request")
	} else {
		args = append(args, "-a", "never")
	}

	// Roles whose work ends outside the workspace need those roots granted
	// explicitly; the default policy refuses them.
	if roots := codexWritableRoots(cfg.Role); len(roots) > 0 {
		args = append(args, "-s", "workspace-write")
		for _, dir := range roots {
			args = append(args, "--add-dir", dir)
		}
	}

	// Hook trust: only a hooks.json that still hashes to what muxcode wrote
	// runs without Codex's own review (codex_hooks.go).
	if CodexHooksTrusted(BusSession(), cfg.Role) {
		args = append(args, "--dangerously-bypass-hook-trust")
	}

	// Model selection
	model := resolveCodexModel(cfg.Role)
	if model != "" {
		args = append(args, "-m", model)
	}

	return "codex", args
}

// codexWritableRoots returns the directories a role must write outside the
// repo, to be granted with --add-dir under the workspace-write policy.
//
// Codex takes a selectable sandbox policy and muxcode passed none, so every
// agent inherited the default: writes inside the workspace succeed, writes
// outside are refused. A build agent therefore compiled cleanly and then died
// in `make install` with "Operation not permitted" on ~/.local/bin — read as
// "Codex cannot build" until the flags were checked (2026-09-08).
//
// The paths track the Makefile's own PREFIX/BINDIR/CONFIGDIR variables, so a
// non-default install prefix stays writable instead of silently regressing to
// the failure this fixes. Only build is listed: it is the role whose failure
// was observed. Add a role here when its work is shown to write outside the
// workspace — never widen the policy itself.
func codexWritableRoots(role string) []string {
	if role != "build" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	prefix := stringEnvOrDefault("PREFIX", filepath.Join(home, ".local"))
	roots := []string{
		stringEnvOrDefault("BINDIR", filepath.Join(prefix, "bin")),
		stringEnvOrDefault("CONFIGDIR", filepath.Join(home, ".config", "muxcode")),
		// `make install` also drops slash-command files here (Makefile:92).
		filepath.Join(home, ".claude", "commands"),
	}
	return resolveWritableRoots(append(roots, goToolchainRoots()...))
}

// resolveWritableRoots maps each root to its physical path, dropping any that
// cannot be resolved.
//
// Codex refuses a writable root containing a symlink component ("symlinked
// writable roots not supported"), and that refusal is fatal to the SANDBOX, not
// just to the offending root: the shell process fails before startup, so every
// command in the agent dies pre-execution with no output. A dotfiles setup that
// symlinks ~/.claude was enough to make the build agent look like a hung model —
// it accepted work, spun, and ran nothing (2026-09-08).
//
// A missing root is created first: these are install targets `make install`
// would create anyway, and an unresolvable root has to be dropped, which would
// silently reinstate the "Operation not permitted" failure the grants exist to
// prevent. Resolving also dedupes roots that share a physical path.
func resolveWritableRoots(roots []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range roots {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			if mkErr := os.MkdirAll(dir, 0755); mkErr != nil {
				continue
			}
			if resolved, err = filepath.EvalSymlinks(dir); err != nil {
				continue
			}
		}
		if resolved == "" || seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

// goToolchainRoots returns the Go build and module caches, asked of the
// toolchain rather than assumed, so a custom GOCACHE/GOMODCACHE is honoured.
//
// The compiler writes these on any build of changed code, and they sit outside
// the workspace. Without them a sandboxed build fails on a cache path the
// moment a source file changes — and passes while every package is already
// cached, which is why this surfaced one build after the roots were added
// rather than immediately. An absent toolchain yields nothing to grant.
func goToolchainRoots() []string {
	out, err := exec.Command("go", "env", "GOCACHE", "GOMODCACHE").Output()
	if err != nil {
		return nil
	}
	var roots []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if dir := strings.TrimSpace(line); dir != "" {
			roots = append(roots, dir)
		}
	}
	return roots
}

// stringEnvOrDefault is os.Getenv with a fallback for unset or empty values.
func stringEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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

// Codex's directory-trust prompt, shown for a project absent from
// ~/.codex/config.toml [projects]. "Yes, continue" is pre-selected, so Enter
// accepts it; codexTrustPromptTail is its last line.
const (
	codexTrustPromptMarker = "Do you trust the contents of this directory"
	codexTrustPromptTail   = "Press enter to continue"
)

// codexTrustPromptTailWindow is how many trailing non-blank lines may sit
// under the prompt's last line for it to still count as live — one footer
// line of tolerance.
const codexTrustPromptTailWindow = 2

// Codex's command-approval prompt, raised only by a role launched with
// `-a on-request` (isReadOnlyCodexRole). codexApprovalPromptTail is its last
// line — "confirm", not the trust prompt's "continue".
const (
	codexApprovalPromptMarker = "Would you like to run the following command?"
	codexApprovalPromptTail   = "Press enter to confirm or esc to cancel"
)

// codexApprovalPromptTailWindow is the trust prompt's tolerance, for the same
// reason: Codex draws this prompt inline too, so its text outlives the answer.
const codexApprovalPromptTailWindow = 2

// Codex's self-update prompt, drawn inline under the composer at launch when a
// newer release exists. "1. Update now" is pre-selected and runs
// `npm install -g @openai/codex`, after which Codex exits to the shell — so
// Enter, the one key every wake-up sends, is the one answer that must never
// reach it (2026-09-22, dps-data-services-pipelines: the startup wake
// updated build, test and review from 0.155.0 to 0.155.1 and all three died).
// Its tail is the trust prompt's, which is why codexPromptLive checks order.
const (
	codexUpdatePromptMarker = "Update available!"
	codexUpdatePromptTail   = "Press enter to continue"
	codexUpdateSkipOption   = 2
)

// codexUpdateOptionsWindow spans the prompt's three options and tail plus one
// footer line, counted from the bottom of the pane.
const codexUpdateOptionsWindow = 5

// codexPromptMarkers are the inline prompts that can share the bottom of a
// pane; the newest one drawn is the one a tail belongs to.
var codexPromptMarkers = []string{codexTrustPromptMarker, codexApprovalPromptMarker, codexUpdatePromptMarker}

// codexPromptLive reports whether content ends at the inline prompt named by
// marker: its tail holds one of the last window non-blank lines, and no other
// Codex prompt was drawn after it. Codex draws prompts inline
// (--no-alt-screen), so an answered one stays in scrollback — without the
// tail anchor every later classification would answer it again, and without
// the order check an answered trust prompt would claim the update prompt
// below it, whose tail is identical, and answer it with Enter.
func codexPromptLive(content, marker, tail string, window int) bool {
	at := strings.LastIndex(content, marker)
	if at < 0 {
		return false
	}
	for _, other := range codexPromptMarkers {
		if other != marker && strings.LastIndex(content, other) > at {
			return false
		}
	}
	for _, line := range lastNonEmptyLines(content, window) {
		if strings.Contains(line, tail) {
			return true
		}
	}
	return false
}

// codexApprovalPromptLive reports whether content ends at a command-approval
// prompt. Unanswered it shows neither spinner nor ❯, so no watchdog reads it as
// working or as recoverably idle and only the 600s task timeout fires — which
// records the node as "timed-out", naming the clock rather than the cause
// (2026-09-14, is-operations-gateway: a review node burned 602s here).
func codexApprovalPromptLive(content string) bool {
	return codexPromptLive(content, codexApprovalPromptMarker, codexApprovalPromptTail, codexApprovalPromptTailWindow)
}

// codexTrustPromptLive reports whether content ends at the directory-trust
// prompt; once accepted, a later Enter on it would submit an empty turn.
func codexTrustPromptLive(content string) bool {
	return codexPromptLive(content, codexTrustPromptMarker, codexTrustPromptTail, codexTrustPromptTailWindow)
}

// codexUpdatePromptLive reports whether content ends at the self-update
// prompt.
func codexUpdatePromptLive(content string) bool {
	return codexPromptLive(content, codexUpdatePromptMarker, codexUpdatePromptTail, codexTrustPromptTailWindow)
}

// codexUpdateHighlight returns the number of the update prompt's highlighted
// (›) option, or 0 when the prompt is not live or no option reads as
// highlighted.
func codexUpdateHighlight(content string) int {
	if !codexUpdatePromptLive(content) {
		return 0
	}
	for _, line := range lastNonEmptyLines(content, codexUpdateOptionsWindow) {
		rest, ok := strings.CutPrefix(line, "›")
		if !ok {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%d.", &n); err == nil {
			return n
		}
	}
	return 0
}

// codexUpdateMaxMoves bounds SkipCodexUpdate's arrow presses: from any of the
// three options, Skip is at most one away.
const codexUpdateMaxMoves = 2

// SkipCodexUpdate answers the self-update prompt with "2. Skip", moving the
// highlight with Up/Down and re-capturing after each move. Enter is sent only
// on a capture that shows Skip highlighted: on any other frame it would be the
// npm install and exit this exists to prevent, so an unreadable or unmoving
// highlight returns an error and presses nothing. Skip rather than "Skip until
// next version", which persists a preference in the user's own Codex config.
func SkipCodexUpdate(target string) error {
	for moves := 0; ; moves++ {
		content, err := TmuxCapturePaneLines(target, injectionGuardLines)
		if err != nil {
			return err
		}
		at := codexUpdateHighlight(content)
		switch {
		case at == codexUpdateSkipOption:
			return TmuxSendEnter(target)
		case at == 0 || moves == codexUpdateMaxMoves:
			return fmt.Errorf("update prompt: Skip not highlighted (option %d after %d moves), not confirming", at, moves)
		case at < codexUpdateSkipOption:
			err = TmuxSendKeys(target, "Down")
		default:
			err = TmuxSendKeys(target, "Up")
		}
		if err != nil {
			return err
		}
		time.Sleep(injectVerifyDelay)
	}
}

// ClassifyPane determines the startup state of a Codex TUI pane. A live
// directory-trust prompt is checked first: its banner is already on screen,
// so the box-drawing test alone read the prompt as idle, AutoAccept marked
// the agent ready, and the wake-up was typed into the prompt as its answer
// (2026-09-09, is-advising-gateway — Codex quit without persisting trust and
// the daemon's relaunch loop repeated it to the restart cap). The update
// prompt shares that failure and its fix: it too sits under a rendered banner.
// The approval prompt is next, ahead of the error test because it is
// tail-anchored while that test matches "Error" anywhere in scrollback — below
// an old error line it would classify NotReady and be restarted rather than
// answered. Error text is
// checked next, since it often contains "codex"; then the TUI's rendering
// markers, and in inline mode (--no-alt-screen) the bare Codex text prompt.
func (p *CodexProvider) ClassifyPane(content string) PaneState {
	if codexTrustPromptLive(content) {
		return PaneTrustPrompt
	}
	if codexUpdatePromptLive(content) {
		return PaneUpdatePrompt
	}
	if codexApprovalPromptLive(content) {
		return PaneApprovalPrompt
	}
	if strings.Contains(content, "Error") || strings.Contains(content, "FATAL") || strings.Contains(content, "ERROR:") {
		return PaneNotReady
	}
	for _, ch := range []string{"─", "│", "╭", "╰", "┌", "└", "╹", "╻"} {
		if strings.Contains(content, ch) {
			return PaneIdle
		}
	}
	if strings.Contains(content, "codex") || strings.Contains(content, "Codex") {
		return PaneIdle
	}
	return PaneNotReady
}

// AcceptStartup answers a live directory-trust prompt with Enter — "Yes,
// continue" is pre-selected, and launching muxcode in the directory is the
// operator's trust decision, exactly as for Claude Code's folder prompt — and
// a live update prompt with Skip (SkipCodexUpdate), since installing software
// is not. Returns true once the TUI has rendered its composer (PaneIdle).
func (p *CodexProvider) AcceptStartup(session, pane string, state PaneState) bool {
	switch state {
	case PaneTrustPrompt:
		_ = TmuxSendEnter(pane)
		return false
	case PaneUpdatePrompt:
		if err := SkipCodexUpdate(pane); err != nil {
			LogLifecycle(session, "warn", "auto-accept", "update-skip-failed", pane+": "+err.Error())
		}
		return false
	}
	return state == PaneIdle
}

// guardInjection refuses to type into a pane that is not the Codex composer:
// a dead agent's shell (captureInjectionTarget), the directory-trust prompt or
// the update prompt. A prompt is answered here rather than merely refused
// because a daemon relaunch (RestartLocalAgent) runs no AutoAccept pass —
// without this the relaunched agent would sit at it until someone pressed a
// key. The injection itself is deferred to the next wake cycle via
// ErrInjectionSkipped.
func (p *CodexProvider) guardInjection(session, target, role string) error {
	content, err := captureInjectionTarget(session, target, role)
	if err != nil {
		return err
	}
	if codexTrustPromptLive(content) {
		p.AcceptStartup(session, target, PaneTrustPrompt)
		LogLifecycle(session, "info", "auto-accept", "trust-prompt", role)
		return fmt.Errorf("%s: pane at the directory-trust prompt, accepted; injection deferred: %w", role, ErrInjectionSkipped)
	}
	if codexUpdatePromptLive(content) {
		if err := SkipCodexUpdate(target); err != nil {
			LogLifecycle(session, "warn", "auto-accept", "update-skip-failed", role+": "+err.Error())
			return fmt.Errorf("%s: pane at the update prompt and the skip failed (%v); injection deferred: %w", role, err, ErrInjectionSkipped)
		}
		LogLifecycle(session, "info", "auto-accept", "update-prompt", role)
		return fmt.Errorf("%s: pane at the update prompt, skipped; injection deferred: %w", role, ErrInjectionSkipped)
	}
	if codexApprovalPromptLive(content) {
		if err := DenyCodexApproval(target); err != nil {
			LogLifecycle(session, "error", "auto-deny", "approval-deny-failed", fmt.Sprintf("%s: %v", role, err))
			return fmt.Errorf("%s: pane at a command-approval prompt and the deny failed (%v); injection deferred: %w", role, err, ErrInjectionSkipped)
		}
		LogLifecycle(session, "warn", "auto-deny", "approval-prompt", role)
		return fmt.Errorf("%s: pane at a command-approval prompt, denied; injection deferred: %w", role, ErrInjectionSkipped)
	}
	return nil
}

// enterGuard returns the check every Enter after a typed injection must pass:
// guardInjection again, on a fresh capture. The pre-type guard alone left a
// window — on 2026-09-23 (muxcode) Codex drew its update prompt after the
// startup wake was typed but before its Enter, which chose "Update now" and
// the build agent exited to bash. A live prompt is answered, not entered; the
// typed text stays parked and the caller defers via ErrInjectionSkipped.
func (p *CodexProvider) enterGuard(session, target, role string) func() error {
	return func() error {
		if err := p.guardInjection(session, target, role); err != nil {
			LogLifecycle(session, "warn", "notify", "enter-withheld", role+": "+err.Error())
			return err
		}
		return nil
	}
}

// DenyCodexApproval answers a command-approval prompt with its own "No" (esc).
//
// The prompt is an escalation request: `-a on-request` sets an approval policy,
// not a sandbox or an allowlist, so in-sandbox commands never reach it and only
// an attempt to work outside does. Its roles are told not to execute at all, so
// there is no case where yes is right and no human at the pane to say it.
// Callers send the Escape alone and defer their payload — an Escape adjacent to
// text fuses into a Meta chord (MUX-163).
func DenyCodexApproval(target string) error {
	return TmuxSendEscape(target)
}

// CodexApprovalPromptLive reports whether a captured pane ends at Codex's
// command-approval prompt, for callers outside this file.
func CodexApprovalPromptLive(content string) bool {
	return codexApprovalPromptLive(content)
}

// CodexRoleIsReadOnly reports whether a role runs Codex under `-a on-request`,
// which is the only configuration that can raise a command-approval prompt.
func CodexRoleIsReadOnly(role string) bool {
	return isReadOnlyCodexRole(role)
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
func (p *CodexProvider) SendWakeUp(session, role string, force bool) error {
	target := PaneTarget(session, role)

	// Same skip contract as the OpenCode guard (see sentinel doc).
	if !force {
		tasks, _ := ListTasks(session, TaskInFlight)
		for _, t := range tasks {
			if t.To == role && time.Now().Unix()-t.SentAt > 5 {
				age := time.Now().Unix() - t.SentAt
				fmt.Fprintf(os.Stderr, "  [wakeup] skipping %s injection — in-flight task %s:%s exists (%ds old)\n",
					role, t.Action, shortID(t.ID), age)
				return fmt.Errorf("%s: in-flight task %s (%ds old): %w", role, shortID(t.ID), age, ErrInjectionSkipped)
			}
		}
	}

	if p.hooks {
		if err := p.guardInjection(session, target, role); err != nil {
			return err
		}
		return p.injectWakeSentence(session, target, role)
	}

	// Read pending messages to build the prompt text (non-destructive peek)
	msgs, err := Peek(session, role)
	if err != nil || len(msgs) == 0 {
		return nil // nothing to inject
	}
	if err := p.guardInjection(session, target, role); err != nil {
		return err
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
	var lastFrom, lastRequestID, lastRequestFrom string
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
			lastRequestID, lastRequestFrom = msg.ID, msg.From
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
	// The reply belongs to whoever asked, not whoever spoke last.
	replyTarget := NormalizeBusRole(lastFrom)
	if lastRequestFrom != "" {
		replyTarget = NormalizeBusRole(lastRequestFrom)
	}
	if replyTarget == "" || !IsKnownRole(replyTarget) {
		replyTarget = "edit"
	}
	// Prepend AND append reply instructions for request messages.
	// Smaller models lose trailing instructions after long tool-use
	// sequences, so the reply command appears both at the start (as a
	// priority directive) and at the end (as a reminder).
	// Response-only wake-ups skip this to avoid infinite echo loops.
	if hasRequest {
		replyCmd := buildReplyCommand(replyTarget, lastRequestID)
		prompt = fmt.Sprintf("IMPORTANT: After completing this task, you MUST run this bash command: %s — ", replyCmd) + prompt
		prompt += fmt.Sprintf(" — REMINDER: Your FINAL step MUST be to EXECUTE (not print): %s", replyCmd)
		prompt += chainInstructionForRole(role)
	}

	// Send text first — do NOT consume inbox until both send-keys succeed.
	// TmuxSendLiteral: this payload is dynamic message text, so it needs
	// the -l -- form or a dash-leading line is rejected (MUX-104).
	if err := TmuxSendLiteral(target, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s/%s failed: %v\n", role, "codex", err)
		return err
	}
	time.Sleep(150 * time.Millisecond)
	guard := p.enterGuard(session, target, role)
	if err := guard(); err != nil {
		return err
	}
	if err := TmuxSendEnter(target); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s/%s failed: %v\n", role, "codex", err)
		return err
	}

	// send-keys "succeeding" only means tmux accepted the keys — not that the TUI
	// submitted them (a dropped Enter parks the prompt unsent). Confirm the prompt
	// actually left the composer (re-sending Enter if it parked), then consume with
	// a verified-inject `delivered` receipt. If it can't be confirmed, the inbox is
	// left for the daemon's next wake cycle — no drop on a dropped Enter, replacing
	// the old fire-and-hope drain.
	confirmInjectionAndConsume(session, role, target, injectionNeedle(prompt), batchIDs, guard)
	return nil
}

// injectWakeSentence types the fixed wake sentence into a hook-road codex
// pane. Nothing is consumed and no receipt is written here: the
// UserPromptSubmit hook consumes the inbox in the agent's own process when
// the sentence is submitted, which is the true ack. The reply reminder that
// wraps a scrape-road injection is deliberately absent — the hook carries the
// reply instruction once, as context, so a payload is never a prompt
// (MUX-009). Text and Enter are separate writes with a delay, as everywhere,
// through the tmux runner seam so `deliver --force` can be pinned hermetically.
// The Enter is guarded like the text (enterGuard): Codex draws its prompts
// asynchronously, so one can arrive between the two.
func (p *CodexProvider) injectWakeSentence(session, target, role string) error {
	if err := TmuxSendLiteral(target, WakeSentence); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s/%s failed: %v\n", role, "codex", err)
		return err
	}
	time.Sleep(200 * time.Millisecond)
	if err := p.enterGuard(session, target, role)(); err != nil {
		return err
	}
	if err := TmuxSendKeys(target, "Enter"); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s/%s failed: %v\n", role, "codex", err)
		return err
	}
	return nil
}

// Compact is a no-op — the Codex TUI manages its own context.
func (p *CodexProvider) Compact(session, role, target string) error {
	return nil
}

// SupportsHooks is true on the hook road (MUX-159); the scrape road degrades
// gracefully, as OpenCode does.
func (p *CodexProvider) SupportsHooks() bool { return p.hooks }

// SelfPollsInbox is always false: a Codex TUI runs no background listener.
// On the hook road its Stop and UserPromptSubmit hooks deliver instead.
func (p *CodexProvider) SelfPollsInbox() bool { return false }

// PaneIsEvidence is true only on the scrape road.
func (p *CodexProvider) PaneIsEvidence() bool { return !p.hooks }

// IdlePromptChar returns empty — Codex TUI idle detection is not
// based on a single character.
func (p *CodexProvider) IdlePromptChar() string { return "" }

// WriteAgentConfig writes .codex/{role}/AGENTS.md with shared bus protocol
// instructions and role-specific agent body content. Each role gets its own
// subdirectory to prevent multiple Codex agents from overwriting each other's
// instructions in a mixed or all-Codex session.
func (p *CodexProvider) WriteAgentConfig(role string) error {
	active, err := PrepareCodexHooks(BusSession(), role)
	if err != nil {
		return err
	}
	p.hooks = active
	return writeCodexAgentConfig(role, active)
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
//
// A completion is only ever reported alongside a line the agent composed.
// Rule 3 is a guess about a redrawn screen, not evidence of work, so when
// nothing but chrome sits above the composer this reports *not complete* and
// waits. The former fallback — summarizing such a pane as "Task completed" —
// is precisely how a horizontal rule became a passing build: on 2026-09-08 the
// nearest non-empty line above the composer was the rule codex draws between
// turns, and 158 dashes were sent as build's and test's answers 33s and 22s
// after dispatch, closing two graph nodes before either agent had a result
// (MUX-154). Reporting "no result yet" costs one more poll; reporting a false
// one fabricates evidence that outlives the session.
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
		if LooksLikeWorkingLine(trimmed) {
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
			// A real send happened, so the task IS done even if the pane
			// shows no quotable line — unlike the composer branch below.
			lastContentLine := lastComposedLine(lines, i)
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
			lastContentLine := lastComposedLine(lines, i)
			if lastContentLine == "" {
				return false, false, "" // only chrome above the composer — see doc comment
			}
			return true, false, lastContentLine
		}
	}

	return false, false, ""
}

// lastComposedLine returns the nearest line above before that the agent
// actually wrote, skipping blanks and anything the TUI drew itself.
//
// It returns "" when no such line exists, which callers must read as "no
// result", never as an empty success: a pane holding only chrome is the shape
// that closed two graph nodes on work that had not finished (MUX-154).
//
// A rule line is a turn boundary, not merely noise. Skipping one and reading on
// reaches prose belonging to the PREVIOUS turn, so the search only crosses a
// rule for a line carrying an explicit EXIT= sentinel — the one structured
// result whose meaning does not depend on which turn produced it. Without that
// guard the helper trades a fabricated summary for a stale one.
func lastComposedLine(lines []string, before int) string {
	passedRule := false
	for j := before - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		if isRuleLine(t) {
			passedRule = true
			continue
		}
		if LooksLikeNonResult(t) {
			continue
		}
		if strings.HasPrefix(t, "muxcode ") || strings.HasPrefix(t, "$") {
			continue
		}
		if passedRule && !strings.Contains(t, "EXIT=") {
			continue
		}
		return t
	}
	return ""
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
//
// hooks selects the role body: on the hook road the definition's chain and
// guard references stand as written, because those hooks now fire for codex
// too; the scrape road rewrites them into manual instructions.
func writeCodexAgentConfig(role string, hooks bool) error {
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
				agentBody = body
				if !hooks {
					agentBody = adaptBodyForNonHookProvider(body, role)
				}
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
	buf.WriteString("muxcode send edit response \"<summary of what you found or did>\" --type response --reply-to <request id>\n")
	buf.WriteString("```\n\n")
	buf.WriteString("If a different agent (not edit) requested the task, reply to that agent instead. ")
	buf.WriteString("Always pass `--reply-to` with the id of the request you are answering — `muxcode inbox` ")
	buf.WriteString("prints it. Without it the requester's `--wait` cannot match your reply to its request ")
	buf.WriteString("and blocks for 90 seconds before giving up, even though you answered.\n\n")
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
