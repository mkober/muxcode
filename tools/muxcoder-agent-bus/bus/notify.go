package bus

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Notify sends a tmux notification to an agent's pane.
// Uses consolidated PaneTarget from config.go for pane targeting.
// Peeks at the inbox to include a summary of the latest message.
func Notify(session, role string) error {
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
		return "You have new messages. Run: muxcoder-agent-bus inbox"
	}

	last := msgs[len(msgs)-1]
	payload := last.Payload
	if len(payload) > 100 {
		payload = payload[:100] + "\u2026"
	}

	return fmt.Sprintf("[%s \u2192 %s] %s \u2192 Run: muxcoder-agent-bus inbox", last.From, last.Action, payload)
}
