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

// notifiedSizePath returns the path to the marker file that records the inbox
// size at the time of the last notification. Used to deduplicate Notify calls.
func notifiedSizePath(session, role string) string {
	return filepath.Join(BusDir(session), "notified-"+role+".size")
}

// notifyCooldown is the minimum interval between notifications for the same role.
// Even if the inbox size changes, a notification within this window is suppressed.
// This is a defense-in-depth against rapid-fire duplicates if file locking fails.
const notifyCooldown = 2 * time.Second

// notifyRetryInterval is the maximum time before re-notifying an agent whose
// inbox still has the same unread messages. Handles the case where a previous
// send-keys injection was missed (e.g., Claude Code TUI redraw race). Without
// this, alreadyNotified() would permanently suppress re-notification since the
// inbox size hasn't changed, leaving the agent stuck at the idle prompt.
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

// alreadyNotified returns true if a notification was recently sent for the
// same inbox content. This prevents duplicate tmux display-message / send-keys
// when Notify is called from multiple sources (cmd/send.go, daemon, subscriptions).
//
// Key behavior: if the inbox size matches the last notified size AND the marker
// is older than notifyRetryInterval, returns false to allow a retry. This handles
// missed send-keys injections (e.g., Claude Code TUI redraw race) where the agent
// remains idle with unread messages after a notification was "sent" but not received.
func alreadyNotified(session, role string) bool {
	inboxPath := InboxPath(session, role)
	info, err := os.Stat(inboxPath)
	if err != nil {
		return false
	}
	currentSize := info.Size()
	if currentSize == 0 {
		return true // nothing to notify about
	}

	markerPath := notifiedSizePath(session, role)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return false
	}
	lastSize, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return false
	}

	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		return false
	}
	markerAge := time.Since(markerInfo.ModTime())

	if currentSize == lastSize {
		// Same inbox content — allow retry if marker is old enough.
		// This catches the case where send-keys was injected but the agent
		// missed it (TUI redraw, pty buffering). Without this, the agent
		// would be stuck at the idle prompt permanently.
		if markerAge >= notifyRetryInterval {
			return false // allow re-notification
		}
		return true // recently notified, suppress duplicate
	}

	// Different inbox size (new messages arrived since last notification).
	// Defense-in-depth: if the marker was written recently (within cooldown),
	// suppress even though the size differs. This catches TOCTOU races where
	// two callers both pass the size check before either writes the marker.
	return time.Since(markerInfo.ModTime()) < notifyCooldown
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

// ClearNotifiedSize removes the notified-size marker for a role, forcing the
// next Notify() call to treat the inbox as unnotified. Called during agent
// hot reload so the daemon re-notifies the new agent about pending messages
// that the old (pre-reload) agent was already notified about.
func ClearNotifiedSize(session, role string) {
	_ = os.Remove(notifiedSizePath(session, role))
}

// markNotified records the current inbox size as the last notified size.
func markNotified(session, role string) {
	inboxPath := InboxPath(session, role)
	info, err := os.Stat(inboxPath)
	if err != nil {
		return
	}
	_ = os.WriteFile(notifiedSizePath(session, role), []byte(strconv.FormatInt(info.Size(), 10)), 0644)
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
	// "You have new messages" via send-keys as a last resort. This covers
	// agents that launched but never started their polling loop — without
	// this fallback, messages pile up unread.
	if IsAgentIdle(session, role) {
		return notifySendKeys(session, role)
	}

	// Visual indicator via display-message (status bar flash).
	// The trigger file is the real notification; this is for human visibility.
	return notifyDisplayMessage(session, role)
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
// This is the last-resort notification for agents that are idle but not
// running a --poll or --wait loop. Delegates the actual pane interaction
// to the role's resolved Provider.
func notifySendKeys(session, role string) error {
	unlock := lockNotify(session, role)
	defer unlock()

	if alreadyNotified(session, role) {
		return nil
	}

	markNotified(session, role)

	provider := ResolveProvider(role)
	return provider.SendWakeUp(session, role)
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
