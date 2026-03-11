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

// alreadyNotified returns true if the inbox size matches the last notified size,
// or if the marker was written within the cooldown window (defense-in-depth).
// This prevents duplicate tmux display-message when Notify is called from
// multiple sources (cmd/send.go, watcher, subscriptions) for the same unread messages.
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

	data, err := os.ReadFile(notifiedSizePath(session, role))
	if err != nil {
		return false
	}
	lastSize, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return false
	}
	if currentSize == lastSize {
		return true
	}

	// Defense-in-depth: if the marker was written recently (within cooldown),
	// suppress even though the size differs. This catches TOCTOU races where
	// two callers both pass the size check before either writes the marker.
	markerPath := notifiedSizePath(session, role)
	markerInfo, err := os.Stat(markerPath)
	if err != nil {
		return false
	}
	return time.Since(markerInfo.ModTime()) < notifyCooldown
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
// prompt (❯). It captures the last 8 lines of the pane and checks whether
// ANY line is an exact match for the idle prompt character.
//
// Claude Code renders decorative UI elements (borders, "? for shortcuts")
// below the ❯ prompt, so the last non-empty line is often NOT the prompt.
// Scanning all captured lines avoids false negatives from this footer.
//
// Returns false on any error (no tmux, session doesn't exist, etc.) — safe
// to call unconditionally.
func IsAgentIdle(session, role string) bool {
	target := PaneTarget(session, role)
	cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-8")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	lines := strings.Split(string(out), "\n")
	// Scan all lines — the ❯ prompt may not be the last non-empty line
	// due to Claude Code's decorative footer (borders, help text).
	// When the agent is active, ❯ only appears as part of a longer line
	// (e.g. "❯ You have new messages") which won't match the exact check.
	for _, line := range lines {
		if strings.TrimSpace(line) == idlePromptChar {
			return true
		}
	}
	return false
}

// notifySendKeys injects "You have new messages" + Enter into the agent's
// tmux pane via send-keys. This wakes up idle Claude Code agents that are
// sitting at the ❯ prompt. The shared agent prompt instructs them to run
// `muxcode-agent-bus inbox` when they see this text.
//
// Text and Enter are sent as separate tmux send-keys calls with a brief
// delay between them. Claude Code's TUI can drop the Enter key when it
// arrives in the same pty write as the preceding text characters.
//
// ONLY safe when the agent is confirmed idle (at ❯). Active agents must
// use display-message instead — send-keys to an active pane disrupts
// tool execution, pollutes conversation history, and causes stalls.
func notifySendKeys(session, role string) error {
	target := PaneTarget(session, role)

	// Step 0: clear any residual input so the text starts at column 0
	// (prevents extra left-padding from stale whitespace in the TUI)
	clr := exec.Command("tmux", "send-keys", "-t", target, "C-u")
	_ = clr.Run()

	// Step 1: send the text
	cmd := exec.Command("tmux", "send-keys", "-t", target, "You have new messages")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys text for %s failed: %v\n", role, err)
		return nil
	}

	// Brief delay so Claude Code's TUI processes the text before Enter
	time.Sleep(100 * time.Millisecond)

	// Step 2: send Enter separately
	cmd = exec.Command("tmux", "send-keys", "-t", target, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys Enter for %s failed: %v\n", role, err)
	}
	return nil
}

// notifyIdleSendKeys wraps notifySendKeys with the same deduplication logic
// used by notifyDisplayMessage (file lock + inbox size + cooldown).
func notifyIdleSendKeys(session, role string) error {
	unlock := lockNotify(session, role)
	defer unlock()

	if alreadyNotified(session, role) {
		return nil
	}

	markNotified(session, role)
	return notifySendKeys(session, role)
}

// Notify sends a tmux notification to an agent's pane.
//
// Dual-path strategy:
//   - Harness panes: skipped entirely (they poll inbox directly)
//   - Edit role: always uses display-message (user types there, send-keys would disrupt)
//   - Idle agents (at ❯ prompt): uses send-keys to wake them up. The shared
//     agent prompt instructs them to run `muxcode-agent-bus inbox` on seeing
//     "You have new messages".
//   - Active agents: uses display-message (passive status bar flash) to avoid
//     disrupting in-progress tool execution
//
// Deduplicates: skips if the inbox hasn't changed since the last notification.
func Notify(session, role string) error {
	// Hosted roles (docs, research, pr-read) resolve to their host agent.
	// The message is already in the host's inbox; notify the host's pane.
	role = WindowForRole(role)

	// Skip harness panes — the harness polls inbox directly
	if IsHarnessActive(session, role) {
		return nil
	}

	// All roles (including edit): wake idle agents via send-keys,
	// fall back to display-message for active agents.
	// Edit was previously excluded (display-message only) because the user
	// types there. But when Claude Code is idle at ❯, send-keys is safe
	// and necessary — display-message is only visible to the human and
	// Claude Code never sees it, so responses go unnoticed after --wait
	// times out.
	if IsAgentIdle(session, role) {
		return notifyIdleSendKeys(session, role)
	}
	return notifyDisplayMessage(session, role)
}

// notifyDisplayMessage sends a passive notification via tmux display-message
// (status bar flash). Used for:
//   - Edit role (always — user types there, send-keys would disrupt)
//   - Active agents (not at idle prompt — send-keys would interrupt tool execution)
//
// Best-effort: errors are logged but not returned, since the message is
// already in the inbox and will be seen on the next inbox read.
func notifyDisplayMessage(session, role string) error {
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
		fmt.Sprintf("\U0001f4ec [%s] %s", role, msg))
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
		return "You have new messages. Run: muxcode-agent-bus inbox"
	}

	last := msgs[len(msgs)-1]
	payload := last.Payload
	if len(payload) > 100 {
		payload = payload[:100] + "\u2026"
	}

	return fmt.Sprintf("[%s \u2192 %s] %s \u2192 Run: muxcode-agent-bus inbox", last.From, last.Action, payload)
}
