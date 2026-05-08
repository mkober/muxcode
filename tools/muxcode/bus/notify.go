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
		if !notified[m.ID] {
			unnotified = append(unnotified, m)
		}
	}
	return unnotified
}

// BuildCombinedNotification produces a single-line notification string from
// unnotified messages. For a single message, shows sender, type:action, and
// payload preview. For multiple messages, combines per-message summaries so the
// agent can triage inline without running `muxcode inbox`.
//
// Total output is capped at ~500 chars (tmux send-keys practical limit).
func BuildCombinedNotification(msgs []Message) string {
	if len(msgs) == 0 {
		return "You have new messages"
	}
	if len(msgs) == 1 {
		m := msgs[0]
		preview := m.Payload
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		return fmt.Sprintf("New message from %s [%s:%s]: %s",
			m.From, m.Type, m.Action, preview)
	}

	// Multiple messages — combine summaries
	var parts []string
	totalLen := 0
	shown := 0
	for _, m := range msgs {
		preview := m.Payload
		if len(preview) > 50 {
			preview = preview[:50] + "..."
		}
		part := fmt.Sprintf("[%s>%s] %s", m.From, m.Action, preview)
		if totalLen+len(part) > 450 && shown > 0 {
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
const notifyRetryInterval = 30 * time.Second

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

// sendKeysVerifyDelay is the time to wait after send-keys before checking
// if the agent received the injection. Tests can override this to avoid
// 500ms sleeps per test case.
var sendKeysVerifyDelay = 500 * time.Millisecond

// notifySendKeys injects a wake-up message into the agent's tmux pane.
// Uses combined notification text from unnotified messages so the agent
// can triage inline without running `muxcode inbox`.
//
// For hook providers (Claude Code), verifies delivery after injection.
// If the agent is still idle after a brief delay, the send-keys was
// silently dropped (TUI redraw race). In that case, clears the
// notified IDs marker so the daemon retries on its next cycle (5s)
// instead of waiting notifyRetryInterval (30s).
func notifySendKeys(session, role string) error {
	unlock := lockNotify(session, role)
	defer unlock()

	if alreadyNotified(session, role) {
		return nil
	}

	// Build combined notification from unnotified messages
	unnotified := UnnotifiedMessages(session, role)
	text := BuildCombinedNotification(unnotified)

	markNotified(session, role)

	provider := ResolveProvider(role)
	err := SendWakeUpWithText(session, role, provider, text)

	// Verify delivery for hook providers — send-keys can be silently dropped
	// by Claude Code's TUI during redraws. If the agent is still idle after
	// a brief delay, the text didn't land. Clear the notified IDs marker so
	// the daemon's next checkIdleAgents() cycle (5s) retries immediately
	// instead of waiting notifyRetryInterval (30s).
	if err == nil && provider.SupportsHooks() {
		verifySendKeysDelivery(session, role, provider)
	}

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
	// Send text with -l (literal) to avoid tmux key interpretation
	cmd := exec.Command("tmux", "send-keys", "-t", target, "-l", text)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s failed: %v\n", role, err)
		return err
	}
	// 200ms delay gives Claude Code's TUI time to register the text
	time.Sleep(200 * time.Millisecond)
	// Send Enter separately (not literal — Enter is a tmux key name)
	cmd = exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s failed: %v\n", role, err)
		return err
	}
	return nil
}

// verifySendKeysDelivery checks whether a send-keys injection was actually
// received by the agent. Waits sendKeysVerifyDelay then checks IsIdle —
// if the agent is still idle, the injection was dropped. Retries once
// immediately (within the same Notify() call) before clearing the marker.
// This gives a second attempt without waiting for the next 5s daemon cycle.
func verifySendKeysDelivery(session, role string, provider Provider) {
	time.Sleep(sendKeysVerifyDelay)
	if !provider.IsIdle(session, role) {
		return // agent is processing — delivery succeeded
	}

	// First attempt failed — retry once immediately.
	provider.SendWakeUp(session, role)
	time.Sleep(sendKeysVerifyDelay)
	if provider.IsIdle(session, role) {
		// Still idle after retry — clear marker so the daemon's next
		// checkIdleAgents() cycle (5s) retries instead of waiting
		// notifyRetryInterval (30s).
		ClearNotifiedIDs(session, role)
	}
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
