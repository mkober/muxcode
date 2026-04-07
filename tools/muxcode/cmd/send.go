package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Send handles the "muxcode send" subcommand.
// Usage: muxcode send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify] [--force] [--wait]
func Send(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode send <to> <action> \"<payload>\" [--type TYPE] [--reply-to ID] [--no-notify] [--force] [--wait]\n")
		os.Exit(1)
	}

	to := args[0]
	action := args[1]

	// Scan all remaining args for flags first, then determine payload source.
	payload := ""
	msgType := "request"
	replyTo := ""
	noNotify := false
	force := false
	wait := false
	payloadSet := false

	remaining := args[2:]
	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--type":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --type requires a value\n")
				os.Exit(1)
			}
			i++
			msgType = remaining[i]
		case "--reply-to":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --reply-to requires a value\n")
				os.Exit(1)
			}
			i++
			replyTo = remaining[i]
		case "--no-notify":
			noNotify = true
		case "--force":
			force = true
		case "--wait":
			wait = true
		default:
			if strings.HasPrefix(remaining[i], "--") {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", remaining[i])
				os.Exit(1)
			}
			// First non-flag argument is the payload
			if !payloadSet {
				payload = remaining[i]
				payloadSet = true
			} else {
				fmt.Fprintf(os.Stderr, "Unexpected argument: %s\n", remaining[i])
				os.Exit(1)
			}
		}
	}

	if !payloadSet {
		fmt.Fprintf(os.Stderr, "Error: payload is required\n")
		os.Exit(1)
	}

	// Validate payload content
	for _, w := range validatePayload(payload) {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}

	// Validate target role
	if !bus.IsKnownRole(to) {
		fmt.Fprintf(os.Stderr, "Error: unknown role '%s'. Known roles: %s\n", to, strings.Join(bus.KnownRoles, ", "))
		os.Exit(1)
	}

	session := bus.BusSession()
	from := bus.BusRole()

	// Check send policy (hard error)
	if deny := bus.CheckSendPolicy(from, to); deny != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", deny)
		os.Exit(1)
	}

	// Pre-commit safeguard: block sends to commit agent unless all agents are idle
	if to == "commit" && isCommitAction(action) && !force {
		if err := bus.PreCommitCheck(session); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
	}

	msg := bus.NewMessage(from, to, msgType, action, payload, replyTo)

	// Atomic dedup check + send under file lock to avoid TOCTOU race
	sent, err := bus.SendIfNotDuplicate(session, msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending message: %v\n", err)
		os.Exit(1)
	}
	if !sent {
		fmt.Printf("Suppressed duplicate %s:%s to %s (within dedup window)\n", msgType, action, to)
		return
	}

	if !noNotify {
		// For modal roles, auto-open the modal (or spawn headless if no client).
		// The modal agent reads its inbox on startup — no send-keys needed.
		if bus.IsModalRole(to) {
			_ = bus.OpenOrSpawn(session, to, "")
		} else {
			_ = bus.Notify(session, to)
		}
		// Also notify edit when auto-CC fires (message from build/test/review
		// to a non-edit target). The watcher skips edit to prevent duplicates,
		// so cmd/send.go is responsible for all edit notifications.
		if bus.IsAutoCCRole(from) && to != "edit" && bus.WindowForRole(to) != "edit" {
			_ = bus.Notify(session, "edit")
		}
	}

	fmt.Printf("Sent %s:%s to %s\n", msgType, action, to)

	// --wait: poll own inbox until a response from the target arrives or timeout.
	// Set a waiting marker so Notify() skips send-keys for our role — the
	// --wait loop is already polling and send-keys would interrupt the
	// running Bash tool in Claude Code's TUI.
	if wait {
		// Create task entry for orchestrator tracking
		waitTimeout := resolveWaitTimeout()
		_ = bus.CreateTask(session, msg, waitTimeout)

		bus.SetWaiting(session, from)
		responded := waitForResponse(session, from, to, msg.ID)

		// Update task status based on outcome
		if responded {
			// Task completed — find the response message ID from delivery status
			ds, err := bus.ReadDeliveryStatus(session, msg.ID)
			if err == nil && ds.ResponseID != "" {
				bus.CompleteTask(session, msg.ID, ds.ResponseID)
			} else {
				bus.CompleteTask(session, msg.ID, "")
			}
		} else {
			bus.TimeoutTask(session, msg.ID)
		}

		// Keep the waiting marker alive briefly after --wait completes.
		// Between --wait finishing and the agent's next tool call, the agent
		// pane shows ❯ momentarily. The grace period prevents unnecessary
		// display-message notifications during this window.
		go func() {
			time.Sleep(5 * time.Second)
			bus.ClearWaiting(session, from)
		}()
	}
}

// waitForResponse watches the delivery status file for the sent message,
// waiting for it to transition to "responded". This avoids racing with
// the background --poll loop which also reads the inbox — previously both
// competed for the same inbox file, causing --wait to miss responses.
//
// When a response is detected, attempts to consume the response message
// from the inbox for display. If the background --poll already consumed
// it (and printed it as a task result), prints a short confirmation.
//
// Timeout is controlled by MUXCODE_INBOX_POLL_TIMEOUT (default 600s).
// Returns true if a response was received, false on timeout.
func waitForResponse(session, role, target, msgID string) bool {
	timeout := resolveWaitTimeout()

	// For hosted roles, also accept responses from the host agent
	host := bus.WindowForRole(target)
	acceptFrom := func(from string) bool {
		return from == target || from == host
	}

	// Poll delivery status at 500ms — just stat + read a small JSON file,
	// no inbox locking or contention with --poll.
	const pollMs = 500
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(pollMs) * time.Millisecond)

		ds, err := bus.ReadDeliveryStatus(session, msgID)
		if err != nil {
			continue // status file not yet written or read error
		}
		if ds.Status != bus.StatusResponded {
			continue
		}

		// Response detected — try to consume it from inbox for display.
		// The background --poll may have already consumed it, which is fine.
		if bus.HasMessages(session, role) {
			msgs, err := bus.ReceiveFromFunc(session, role, acceptFrom)
			if err == nil && len(msgs) > 0 {
				fmt.Println()
				for _, m := range msgs {
					fmt.Print(bus.FormatMessage(m))
					fmt.Println()
				}
				return true
			}
		}

		// Response was already consumed by --poll — print confirmation
		fmt.Printf("\nResponse from %s received (delivered via poll)\n", target)
		return true
	}

	fmt.Fprintf(os.Stderr, "\nNo response from %s within %ds — check: muxcode inbox --peek\n", target, timeout)
	return false
}

// resolveWaitTimeout returns the --wait timeout in seconds.
func resolveWaitTimeout() int {
	timeout := 600
	if v := os.Getenv("MUXCODE_INBOX_POLL_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = n
		}
	}
	return timeout
}

// isCommitAction returns true for actions that trigger actual git commits.
// Read-only operations (status, log, diff, pr-read) are not blocked.
func isCommitAction(action string) bool {
	switch action {
	case "commit", "stage", "push", "merge", "rebase", "tag":
		return true
	}
	return false
}

// validatePayload returns warning strings for payload issues.
func validatePayload(payload string) []string {
	var warnings []string
	if strings.Contains(payload, "\n") {
		warnings = append(warnings, "payload contains newlines — this may break allowedTools glob matching")
	}
	if len(payload) > 500 {
		warnings = append(warnings, fmt.Sprintf("payload is %d chars (>500) — consider using shorter messages", len(payload)))
	}
	return warnings
}
