package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Send handles the "muxcode-agent-bus send" subcommand.
// Usage: muxcode-agent-bus send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify] [--force] [--wait]
func Send(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus send <to> <action> \"<payload>\" [--type TYPE] [--reply-to ID] [--no-notify] [--force] [--wait]\n")
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
	if err := bus.Send(session, msg); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending message: %v\n", err)
		os.Exit(1)
	}

	if !noNotify {
		_ = bus.Notify(session, to)
		// Also notify edit when auto-CC fires (message from build/test/review
		// to a non-edit target). The watcher skips edit to prevent duplicates,
		// so cmd/send.go is responsible for all edit notifications.
		if bus.IsAutoCCRole(from) && to != "edit" {
			_ = bus.Notify(session, "edit")
		}
	}

	fmt.Printf("Sent %s:%s to %s\n", msgType, action, to)

	// --wait: poll own inbox until a response arrives or timeout
	if wait {
		waitForResponse(session, from)
	}
}

// waitForResponse polls the sender's inbox until messages arrive or timeout.
// Timeout is controlled by MUXCODE_INBOX_POLL_TIMEOUT (default 120s).
func waitForResponse(session, role string) {
	timeout := 120
	if v := os.Getenv("MUXCODE_INBOX_POLL_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = n
		}
	}

	const pollInterval = 2 // seconds
	for elapsed := 0; elapsed < timeout; elapsed += pollInterval {
		time.Sleep(time.Duration(pollInterval) * time.Second)

		if !bus.HasMessages(session, role) {
			continue
		}

		msgs, err := bus.Receive(session, role)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading inbox: %v\n", err)
			return
		}
		if len(msgs) == 0 {
			continue
		}

		fmt.Println()
		for _, m := range msgs {
			fmt.Print(bus.FormatMessage(m))
			fmt.Println()
		}
		return
	}

	fmt.Fprintf(os.Stderr, "\nNo response within %ds — check: muxcode-agent-bus inbox --peek\n", timeout)
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
