package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// alreadyNotified returns true if the inbox size matches the last notified size.
// This prevents duplicate tmux send-keys when Notify is called from multiple
// sources (cmd/send.go, watcher, subscriptions) for the same unread messages.
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
	return currentSize == lastSize
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

// Notify sends a tmux notification to an agent's pane.
// Uses consolidated PaneTarget from config.go for pane targeting.
// Peeks at the inbox to include a summary of the latest message.
// Skips notification for panes running a local LLM harness (they poll directly).
// Deduplicates: skips if the inbox hasn't changed since the last notification.
func Notify(session, role string) error {
	// Skip tmux send-keys for harness panes — the harness polls inbox directly
	if IsHarnessActive(session, role) {
		return nil
	}

	// Skip if inbox hasn't changed since last notification
	if alreadyNotified(session, role) {
		return nil
	}

	// Mark notified BEFORE tmux commands to close the race window.
	// The watcher polls every 2s; the tmux send-keys sequence takes ~200ms.
	// Without this, a concurrent caller can see the old size and fire a duplicate.
	// Trade-off: if tmux fails after marking, the notification is "lost" until
	// the next inbox change — acceptable since a failed tmux usually means the
	// pane is gone.
	markNotified(session, role)

	pane := PaneTarget(session, role)

	// Verify the pane exists before sending
	check := exec.Command("tmux", "has-session", "-t", session)
	if err := check.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] session %q not found: %v\n", session, err)
		return err
	}

	msg := notifyText(session, role)

	// Send the message text
	cmd := exec.Command("tmux", "send-keys", "-t", pane, "-l", msg)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send-keys to %s failed: %v\n", pane, err)
		return err
	}

	time.Sleep(100 * time.Millisecond)

	// Send Enter to execute
	cmd = exec.Command("tmux", "send-keys", "-t", pane, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  [notify] send Enter to %s failed: %v\n", pane, err)
		return err
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
