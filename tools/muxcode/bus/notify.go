package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// IsHarnessActive returns true if a local LLM harness is running for the given role.
// It reads the harness PID marker file and validates the process is alive.
// Stale markers (dead PIDs) are cleaned up automatically.
func IsHarnessActive(session, role string) bool {
	path := HarnessMarkerPath(session, role)
	data, err := os.ReadFile(path)
	if err != nil {
		return false // common case: no marker file
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [harness] invalid PID in %s: %q — removing\n", path, strings.TrimSpace(string(data)))
		_ = os.Remove(path)
		return false
	}
	if !CheckProcAlive(pid) {
		fmt.Fprintf(os.Stderr, "  [harness] stale PID %d in %s — removing\n", pid, path)
		_ = os.Remove(path)
		return false
	}
	return true
}

// notifiedIDsPath returns the path to the message-level notification tracker.
// Contains newline-delimited message IDs that the agent has been notified about.
// Replaces the old size-based notified-{role}.size marker.
func notifiedIDsPath(session, role string) string {
	return filepath.Join(BusDir(session), "notified-"+role+".ids")
}

// NotifiedIDsPath is the exported version of notifiedIDsPath, for use in
// daemon tests that need to verify marker file state.
func NotifiedIDsPath(session, role string) string {
	return notifiedIDsPath(session, role)
}

// readNotifiedIDs loads the set of message IDs already notified from the IDs file.
func readNotifiedIDs(session, role string) map[string]bool {
	data, err := os.ReadFile(notifiedIDsPath(session, role))
	if err != nil {
		return make(map[string]bool)
	}
	ids := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		id := strings.TrimSpace(line)
		if id != "" {
			ids[id] = true
		}
	}
	return ids
}

// writeNotifiedIDs persists the notified set to the IDs file.
func writeNotifiedIDs(session, role string, ids map[string]bool) {
	var lines []string
	for id := range ids {
		lines = append(lines, id)
	}
	_ = os.WriteFile(notifiedIDsPath(session, role),
		[]byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// addNotifiedIDs appends new IDs to the notified set.
func addNotifiedIDs(session, role string, newIDs []string) {
	existing := readNotifiedIDs(session, role)
	for _, id := range newIDs {
		existing[id] = true
	}
	writeNotifiedIDs(session, role, existing)
}

// AddNotifiedIDs is the exported version of addNotifiedIDs, for use in the
// daemon when marking messages as notified after combined delivery.
func AddNotifiedIDs(session, role string, newIDs []string) {
	addNotifiedIDs(session, role, newIDs)
}

// clearNotifiedIDs removes the IDs file (agent consumed inbox — clean slate).
func clearNotifiedIDs(session, role string) {
	_ = os.Remove(notifiedIDsPath(session, role))
	// Also remove legacy size-based marker if it exists (migration)
	_ = os.Remove(filepath.Join(BusDir(session), "notified-"+role+".size"))
}

// UnnotifiedMessages returns inbox messages whose IDs are NOT in the notified set.
// These are messages the agent has not been told about yet.
func UnnotifiedMessages(session, role string) []Message {
	msgs, _ := Peek(session, role)
	notified := readNotifiedIDs(session, role)
	var unnotified []Message
	for _, m := range msgs {
		// Skip accidental self-addressed messages — delivering them would wake
		// the sender about its own message, looping every idle cycle. The
		// startup self-send is exempt (see isLoopingSelfSend) so launch-time
		// context restoration still wakes the agent.
		if isLoopingSelfSend(m) {
			continue
		}
		if !notified[m.ID] {
			unnotified = append(unnotified, m)
		}
	}
	return unnotified
}

// notifyMaxSubjects is the largest inbox size for which the combined
// notification enumerates per-message subjects. Above this, the wake-up is a
// short fixed string so a large inbox can't produce a blob that, on a dropped
// Enter, parks in the composer and wraps past the idle-detection window — the
// root of the re-wake token-churn loop.
const notifyMaxSubjects = 3

// notifyMaxLen hard-caps the enumerated (<= notifyMaxSubjects) form so even a
// few long payloads stay short enough never to wrap the idle-detection capture.
const notifyMaxLen = 200

// truncRunes truncates s to at most max runes (not bytes), appending "..." when
// truncated — so multibyte glyphs common in payloads (✓ → ⏱) are never split
// into invalid UTF-8.
func truncRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}

// BuildCombinedNotification produces a single-line notification string from
// unnotified messages. For a single message, shows sender, type:action, and
// payload preview. For a small handful, combines compact per-message summaries
// so the agent can triage inline. For many messages it degrades to a short,
// fixed wake-up (no subject concatenation) that is bounded in length.
func BuildCombinedNotification(msgs []Message) string {
	if len(msgs) == 0 {
		return "You have new messages"
	}
	if len(msgs) == 1 {
		m := msgs[0]
		return fmt.Sprintf("New message from %s [%s:%s]: %s",
			m.From, m.Type, m.Action, truncRunes(m.Payload, 80))
	}

	// Many messages — short fixed wake-up. Enumerating every subject produces a
	// large blob; a dropped Enter then parks it in the composer where long
	// wrapped text reads as "active", driving the re-wake loop. Keep it tiny.
	if len(msgs) > notifyMaxSubjects {
		return fmt.Sprintf("You have %d new messages. Run: muxcode inbox", len(msgs))
	}

	// A handful of messages — enumerate compact subjects, hard-capped in length.
	var parts []string
	totalLen := 0
	shown := 0
	for _, m := range msgs {
		part := fmt.Sprintf("[%s>%s] %s", m.From, m.Action, truncRunes(m.Payload, 50))
		if totalLen+len(part) > notifyMaxLen && shown > 0 {
			// Cap reached — show "and N more"
			remaining := len(msgs) - shown
			parts = append(parts, fmt.Sprintf("and %d more", remaining))
			break
		}
		parts = append(parts, part)
		totalLen += len(part) + 3 // " | " separator
		shown++
	}
	return fmt.Sprintf("You have %d new messages: %s", len(msgs), strings.Join(parts, " | "))
}

// notifyRetryInterval is the maximum time before re-notifying an agent whose
// inbox still has unnotified messages. Handles the case where a previous
// send-keys injection was missed (e.g., Claude Code TUI redraw race). Without
// this, the agent could be stuck at the idle prompt if the IDs marker was
// written but send-keys didn't land.
const notifyRetryInterval = 15 * time.Second

// lockNotify acquires a per-role file lock for notification deduplication.
// Returns an unlock function. If lock acquisition fails, returns a no-op
// (graceful degradation — old behavior without locking).
func lockNotify(session, role string) func() {
	lockPath := filepath.Join(BusDir(session), "lock", "notify-"+role+".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

// alreadyNotified returns true if all current inbox messages have been notified
// about (their IDs are in the notified set). Content-aware: compares message IDs,
// not file size.
//
// Key behavior: if all messages are in the notified set AND the marker is older
// than notifyRetryInterval, returns false to allow a retry. This handles missed
// send-keys injections (TUI redraw race) where the agent remains idle with
// unread messages after a notification was "sent" but not received.
func alreadyNotified(session, role string) bool {
	unnotified := UnnotifiedMessages(session, role)
	if len(unnotified) == 0 {
		// All messages already notified (or inbox empty) — check for retry
		markerPath := notifiedIDsPath(session, role)
		markerInfo, err := os.Stat(markerPath)
		if err != nil {
			// No marker file → no prior notification → check if inbox is empty
			inboxPath := InboxPath(session, role)
			info, err := os.Stat(inboxPath)
			if err != nil || info.Size() == 0 {
				return true // nothing to notify about
			}
			return false // inbox has content but no marker → not notified
		}
		// Marker exists and all IDs are notified. Allow retry if marker is stale
		// (send-keys may have been dropped by TUI redraw race).
		if time.Since(markerInfo.ModTime()) >= notifyRetryInterval {
			return false // allow re-notification
		}
		return true // recently notified
	}
	return false // has unnotified messages
}

// SetWaiting creates a marker file indicating that a --wait polling loop is
// active for the given role. While the marker exists, Notify() skips
// display-message notifications since the --wait loop is already polling.
func SetWaiting(session, role string) {
	_ = os.WriteFile(WaitingMarkerPath(session, role), []byte(strconv.Itoa(os.Getpid())), 0644)
}

// ClearWaiting removes the --wait marker for a role.
func ClearWaiting(session, role string) {
	_ = os.Remove(WaitingMarkerPath(session, role))
}

// IsWaiting returns true if the given role has an active --wait polling loop.
// Validates that the PID in the marker is still alive to prevent stale markers
// from permanently suppressing notifications.
func IsWaiting(session, role string) bool {
	data, err := os.ReadFile(WaitingMarkerPath(session, role))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(WaitingMarkerPath(session, role))
		return false
	}
	if !CheckProcAlive(pid) {
		_ = os.Remove(WaitingMarkerPath(session, role))
		return false
	}
	return true
}

// IsNotifiedRecently returns true if the notified IDs marker for a role was
// updated within the given duration. Used by the daemon's idle transition
// logic to avoid clearing IDs when a recent notification caused the
// active→idle transition (send-keys echo, not genuine task completion).
func IsNotifiedRecently(session, role string, within time.Duration) bool {
	info, err := os.Stat(notifiedIDsPath(session, role))
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < within
}

// ClearNotifiedIDs removes the notified IDs marker for a role, forcing the
// next Notify() call to treat the inbox as unnotified. Called during agent
// hot reload so the daemon re-notifies the new agent about pending messages
// that the old (pre-reload) agent was already notified about.
func ClearNotifiedIDs(session, role string) {
	clearNotifiedIDs(session, role)
}

// ClearNotifiedSize is a legacy alias for ClearNotifiedIDs.
// Retained for backward compatibility during migration.
func ClearNotifiedSize(session, role string) {
	ClearNotifiedIDs(session, role)
}

// markNotified records the current inbox message IDs as notified.
func markNotified(session, role string) {
	msgs, _ := Peek(session, role)
	if len(msgs) == 0 {
		return
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	addNotifiedIDs(session, role, ids)
}

// idlePromptChar is the Unicode prompt character (❯) used by Claude Code
// when idle and waiting for input. Used to detect whether an agent pane is
// at the idle prompt vs actively executing.
const idlePromptChar = "\u276f"

// HasPendingInput returns true if the agent's tmux pane has text in the input
// buffer after the idle prompt. This indicates the user is mid-typing and
// send-keys injection would corrupt their input. Notifications are held until
// the user submits (presses Enter) or clears the input.
//
// Only meaningful for hook providers (Claude Code) \u2014 non-hook providers return
// false (safe to inject). Returns false on any error (graceful degradation).
func HasPendingInput(session, role string) bool {
	provider := ResolveProvider(role)
	if !provider.SupportsHooks() {
		return false
	}
	target := PaneTarget(session, role)
	content, err := TmuxCapturePaneLines(target, 8)
	if err != nil {
		return false
	}
	return paneHasPendingInput(content)
}

// IsWindowFocused returns true if the user is currently viewing the window
// for the given role. Used to distinguish user-typed input (window focused)
// from stale agent output left at the prompt (window not focused).
//
// A detached session always reports false: tmux tracks an "active window"
// even with no client attached, but nobody can be typing into a detached
// session — treating its active window as focused would hold parked-input
// clearing forever (the plan-agent wedge in background subsessions).
func IsWindowFocused(session, role string) bool {
	if !TmuxSessionAttached(session) {
		return false
	}
	window := WindowForRole(role)
	return TmuxIsWindowActive(session, window)
}

// ClearParkedInput peeks the agent's pane with a wide capture and clears any
// text parked at the ❯ prompt, returning true if it cleared something.
//
// Parked text is the residue of a dropped-Enter injection (or an abandoned
// manual prompt). It is doubly toxic: long parked text WRAPS in the input box
// and pushes the ❯ line above IsIdle's 8-line capture window, so the agent
// reads as "active" and every delivery path holds — while the text itself
// blocks the next injection from landing clean. Callers invoke this before
// delivering (or delegating) to guarantee the injection lands on an empty
// prompt.
//
// The wide capture is used for BOTH checks: the 8-line HasPendingInput misses
// wrapped parked text for the same reason IsIdle does.
//
// Skipped when the user is actually viewing the window in an attached client
// (they may be mid-typing) and when the pane shows no prompt at all (agent
// genuinely busy — nothing is parked, keys would land in a live composer).
func ClearParkedInput(session, role string) bool {
	provider := ResolveProvider(role)
	if !provider.SupportsHooks() {
		return false // non-hook providers manage their own input
	}
	if IsWindowFocused(session, role) {
		return false // user may be typing — never clear under their cursor
	}
	target := PaneTarget(session, role)
	content, err := TmuxCapturePaneLines(target, 200)
	if err != nil {
		return false
	}
	if !PaneHasIdlePrompt(content) || !paneHasPendingInput(content) {
		return false
	}
	if err := TmuxClearInput(target); err != nil {
		return false
	}
	time.Sleep(100 * time.Millisecond)
	return true
}

// PaneHasIdlePrompt checks whether captured pane content contains the idle
// prompt character (\u276f) as a line prefix. Uses the same matching logic as
// ClaudeCodeProvider.IsIdle but operates on pre-captured content, allowing
// callers to use a wider capture window (e.g. 30 lines) than the standard
// 8-line IsIdle check. Exported for use by the daemon watchdog.
func PaneHasIdlePrompt(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == idlePromptChar || strings.HasPrefix(trimmed, idlePromptChar+" ") {
			return true
		}
	}
	return false
}

// ParkedInputText returns the text parked after the \u276f prompt in captured pane
// content, or "" when the prompt is empty or absent. Used by the daemon's pane
// sweep to distinguish text that persists across sweeps (dropped-Enter residue)
// from a transient in-flight injection.
func ParkedInputText(content string) string {
	promptPrefix := idlePromptChar + " "
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, promptPrefix) {
			if after := strings.TrimSpace(trimmed[len(promptPrefix):]); after != "" {
				return after
			}
		}
	}
	return ""
}

// paneHasPendingInput checks captured pane content for text after the idle
// prompt character (\u276f). Extracted for testability (no tmux dependency).
func paneHasPendingInput(content string) bool {
	promptPrefix := idlePromptChar + " "
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		// Look for prompt line with text after it: "\u276f some user input"
		// Just "\u276f" or "\u276f " (whitespace only) means empty prompt \u2014 no pending input.
		if strings.HasPrefix(trimmed, promptPrefix) {
			afterPrompt := strings.TrimSpace(trimmed[len(promptPrefix):])
			if afterPrompt != "" {
				return true
			}
		}
	}
	return false
}

// IsAgentIdle returns true if the agent's tmux pane is sitting at the idle
// prompt. Delegates to the role's resolved Provider for CLI-specific idle
// detection (e.g. ❯ for Claude Code, API status check for OpenCode).
//
// Returns false on any error (no tmux, session doesn't exist, etc.) — safe
// to call unconditionally.
//
// Used by agent-health detection and compact command. Not used in the
// notification path — agents use trigger-file polling instead.
func IsAgentIdle(session, role string) bool {
	provider := ResolveProvider(role)
	return provider.IsIdle(session, role)
}

// Notify sends a notification to an agent that new messages are available.
//
// Strategy:
//   - Always writes the trigger file — agents running `muxcode inbox --poll`
//     detect this via stat() polling. This is the primary notification mechanism.
//   - Harness panes: skipped (they poll inbox directly)
//   - Waiting/polling roles: trigger file only (already polling)
//   - All other agents: trigger file + display-message (visual indicator)
//
// Deduplicates: skips if the inbox hasn't changed since the last notification.
func Notify(session, role string) error {
	// Hosted roles (docs, research, pr-read) resolve to their host agent.
	// The message is already in the host's inbox; notify the host's pane.
	role = WindowForRole(role)

	// Always write the trigger file — safe, non-intrusive, no tmux dependency.
	// Agents running `muxcode inbox --poll` detect this via stat() polling.
	// Written before the has-session guard because it's a pure file write that
	// works regardless of tmux state.
	writeTriggerNotify(session, role)

	// Guard: verify the tmux session exists before any tmux commands.
	// Without this, tmux display-message silently falls through to the
	// current pane, causing test/demo sessions to leak notifications
	// into the user's live session.
	if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
		return nil // session doesn't exist — nothing to notify
	}

	// Skip harness panes — the harness polls inbox directly
	if IsHarnessActive(session, role) {
		return nil
	}

	// Skip display-message when the role has an active --wait polling loop.
	// The --wait already polls the inbox every 2s, so display-message is
	// redundant noise.
	if IsWaiting(session, role) {
		return nil
	}

	// Skip display-message when the role has an active --poll loop.
	// The poll loop watches the trigger file we just wrote.
	if IsPolling(session, role) {
		return nil
	}

	// Non-hook providers (OpenCode TUI, local LLM) have no inbox polling or
	// hook-driven message consumption. The only way to deliver a message is
	// to inject it directly into the TUI input via send-keys. Do this
	// immediately — don't gate on IsIdle (which returns false for OpenCode).
	provider := ResolveProvider(role)
	if !provider.SupportsHooks() {
		return notifySendKeys(session, role)
	}

	// Peek the target pane and clear any text parked at its prompt BEFORE the
	// idle check. Long parked text (a dropped-Enter injection) wraps in the
	// input box and pushes the ❯ line above IsIdle's 8-line capture, making a
	// deliverable agent read as busy — without this clear, a delegation to a
	// wedged agent silently stalls until manual recovery.
	ClearParkedInput(session, role)

	// If the agent is idle (at ❯ prompt) and not polling/waiting, inject
	// combined notification via send-keys. This covers agents that launched
	// but never started their polling loop.
	if IsAgentIdle(session, role) {
		return notifySendKeys(session, role)
	}

	// Agent is busy — do NOT inject send-keys or display-message.
	// The daemon's checkIdleAgents() detects idle transitions and delivers
	// a combined notification with all accumulated messages at that point.
	// Injecting into a busy agent wastes context tokens and interrupts work.
	return nil
}

// writeTriggerNotify writes the current unix timestamp to the role's trigger
// file. This is always safe — no pane interaction, no TOCTOU race. Agents
// running `muxcode inbox --poll` detect the mtime change and read their inbox.
func writeTriggerNotify(session, role string) {
	ts := strconv.FormatInt(time.Now().UnixNano(), 10)
	_ = os.WriteFile(TriggerNotifyPath(session, role), []byte(ts), 0644)
}

// SetPolling creates a marker file indicating that a --poll loop is active
// for the given role. Stores the PID for stale-marker detection.
func SetPolling(session, role string) {
	_ = os.WriteFile(PollingMarkerPath(session, role), []byte(strconv.Itoa(os.Getpid())), 0644)
}

// ClearPolling removes the --poll marker for a role.
func ClearPolling(session, role string) {
	_ = os.Remove(PollingMarkerPath(session, role))
}

// IsPolling returns true if the given role has an active --poll loop.
// Validates that the PID in the marker is still alive to prevent stale markers
// from permanently suppressing notifications.
func IsPolling(session, role string) bool {
	data, err := os.ReadFile(PollingMarkerPath(session, role))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(PollingMarkerPath(session, role))
		return false
	}
	if !CheckProcAlive(pid) {
		_ = os.Remove(PollingMarkerPath(session, role))
		return false
	}
	return true
}

// notifySendKeys injects a wake-up message into the agent's tmux pane.
// Uses combined notification text from unnotified messages so the agent
// can triage inline without running `muxcode inbox`.
//
// Dedup: marks message IDs as notified to prevent re-injection. If the
// send-keys injection is dropped (TUI redraw race), the notifyRetryInterval
// (15s) in alreadyNotified() eventually allows a retry on a subsequent call.
func notifySendKeys(session, role string) error {
	unlock := lockNotify(session, role)
	defer unlock()

	if alreadyNotified(session, role) {
		return nil
	}

	// Check for text in the input buffer after the prompt.
	// If the user has the window focused, they may be actively typing —
	// hold the notification to avoid corrupting their input.
	// If the window is NOT focused, the text is stale agent output (e.g.
	// Claude printing a partial response that landed in the input buffer).
	// Clear it with C-u and proceed with injection.
	if HasPendingInput(session, role) {
		if IsWindowFocused(session, role) {
			// User might be typing — hold notification for next cycle (5s)
			return nil
		}
		// Stale agent output — clear it before injecting
		target := PaneTarget(session, role)
		if err := TmuxClearInput(target); err != nil {
			fmt.Fprintf(os.Stderr, "  [notify] clear input for %s failed: %v\n", role, err)
			return nil // can't clear stale input — hold for next cycle
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Build combined notification from unnotified messages
	unnotified := UnnotifiedMessages(session, role)
	text := BuildCombinedNotification(unnotified)

	markNotified(session, role)

	provider := ResolveProvider(role)
	err := SendWakeUpWithText(session, role, provider, text)

	// No in-line delivery verification. Claude Code's TUI takes 1-3 seconds
	// to process send-keys input — the old 500ms verifySendKeysDelivery()
	// falsely concluded the injection was dropped, cleared notified IDs, and
	// let subsequent daemon cycles (checkInboxes, checkIdleAgents) re-inject
	// the same message 3-5 times. Instead, rely on notifyRetryInterval (15s)
	// in alreadyNotified() as the safety net for truly dropped injections.

	return err
}

// SendWakeUpWithText injects custom notification text into an agent's tmux pane.
// For hook providers (Claude Code), injects the text directly via send-keys.
// For non-hook providers, delegates to the provider's SendWakeUp (which reads
// the inbox and builds its own message).
func SendWakeUpWithText(session, role string, provider Provider, text string) error {
	if !provider.SupportsHooks() {
		// Non-hook providers build their own injection from inbox content
		return provider.SendWakeUp(session, role)
	}

	target := PaneTarget(session, role)

	// Clean the composer before injecting. Claude Code periodically pops an
	// overlay (the "How is Claude doing this session?" feedback survey,
	// autocomplete popups) that consumes the next Enter instead of submitting
	// the composer — so the injected text parks unsent and even a re-sent Enter
	// is eaten by the overlay. Pressing Escape dismisses the overlay; clearing
	// the input box removes any dropped-Enter residue. This path only runs for
	// agents at their prompt (the daemon's idle paths and force-deliver), so
	// Escape never interrupts live generation.
	_ = TmuxSendEscape(target)
	time.Sleep(100 * time.Millisecond)
	if err := TmuxClearInput(target); err == nil {
		time.Sleep(100 * time.Millisecond)
	}

	// Send text with -l (literal) to avoid tmux key interpretation
	if err := TmuxRun("send-keys", "-t", target, "-l", text); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s failed: %v\n", role, err)
		return err
	}
	// 200ms delay gives Claude Code's TUI time to register the text
	time.Sleep(200 * time.Millisecond)
	// Send Enter separately (not literal — Enter is a tmux key name)
	if err := TmuxSendKeys(target, "Enter"); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s failed: %v\n", role, err)
		return err
	}

	// Verify the Enter actually submitted the text. Claude Code's TUI can drop
	// the Enter keystroke when it lands in the same redraw window as the
	// preceding literal text, leaving the message parked unsent in the input
	// buffer — the agent then looks idle-with-pending-input and never processes
	// the message until a manual restart. Re-check the pane and re-send Enter if
	// the text is still parked. Submitting an empty prompt is a no-op, so a
	// redundant Enter (text already submitted) is harmless.
	verifyEnterDelivery(target)

	return nil
}

// verifyEnterDelivery re-checks the pane after a wake-up injection and re-sends
// Enter if the injected text is still parked at the prompt (dropped-Enter race).
// Best-effort and bounded: on capture failure it returns immediately and relies
// on the 15s notifyRetryInterval safety net.
//
// Uses a wide (200-line) capture and inspects only the LIVE composer line (the
// last ❯ prompt in the pane). A narrow 8-line capture can miss the composer when
// an overlay (feedback survey) inflates the layout, and a naive any-line scan
// would false-positive on stale "❯ <submitted text>" scrollback. If the composer
// is still occupied across retries, an overlay is likely re-eating the Enter, so
// each retry re-dismisses with Escape before re-sending Enter.
func verifyEnterDelivery(target string) {
	for attempt := 0; attempt < 3; attempt++ {
		time.Sleep(250 * time.Millisecond)
		content, err := TmuxCapturePaneLines(target, 200)
		if err != nil {
			return // can't verify — rely on notifyRetryInterval retry
		}
		if !composerHasText(content) {
			return // text was submitted — done
		}
		// Still parked — the Enter was dropped or an overlay ate it. Dismiss any
		// overlay, then re-send Enter to submit the composer.
		_ = TmuxSendEscape(target)
		time.Sleep(50 * time.Millisecond)
		_ = TmuxSendKeys(target, "Enter")
	}
}

// composerHasText reports whether the LIVE input composer holds text, by
// inspecting only the last ❯ prompt line in the captured pane content. The live
// composer is always the last prompt occurrence — everything below it is the box
// border and status hints. This avoids the false positive paneHasPendingInput
// hits when earlier "❯ <submitted text>" scrollback lines are in view.
func composerHasText(content string) bool {
	promptPrefix := idlePromptChar + " "
	last := ""
	found := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == idlePromptChar || strings.HasPrefix(trimmed, promptPrefix) {
			last = trimmed
			found = true
		}
	}
	if !found || last == idlePromptChar {
		return false
	}
	return strings.TrimSpace(last[len(promptPrefix):]) != ""
}

// notifyDisplayMessage sends a passive notification via tmux display-message
// (status bar flash). Used as a visual indicator for humans — the trigger
// file is the primary mechanism for agent notification.
//
// Best-effort: errors are logged but not returned, since the message is
// already in the inbox and will be seen on the next inbox read or poll.
func notifyDisplayMessage(session, role string) error {
	if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
		return nil
	}

	unlock := lockNotify(session, role)
	defer unlock()

	if alreadyNotified(session, role) {
		return nil
	}

	markNotified(session, role)

	// Passive: display-message shows in the tmux status bar.
	// -d 5000 keeps it visible for 5 seconds (default is often too brief).
	// Target the window (session:role) so the message appears on the correct pane.
	// This does NOT inject text into the pane — safe at all times.
	msg := notifyText(session, role)
	target := fmt.Sprintf("%s:%s", session, WindowForRole(role))
	cmd := exec.Command("tmux", "display-message", "-t", target, "-d", "5000",
		fmt.Sprintf("\U0001f4ec %s [%s] %s", session, role, msg))
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] display-message for %s failed: %v\n", role, err)
	}
	return nil
}

// notifyText builds the notification string, including a summary of the
// most recent unread message when available.
func notifyText(session, role string) string {
	msgs, err := Peek(session, role)
	if err != nil || len(msgs) == 0 {
		return "You have new messages. Run: muxcode inbox"
	}

	last := msgs[len(msgs)-1]
	payload := last.Payload
	if len(payload) > 100 {
		payload = payload[:100] + "\u2026"
	}

	return fmt.Sprintf("[%s \u2192 %s] %s \u2192 Run: muxcode inbox", last.From, last.Action, payload)
}
