package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/bus"
)

// Send handles the "muxcoder-agent-bus send" subcommand.
// Usage: muxcoder-agent-bus send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify]
func Send(args []string) {
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: muxcoder-agent-bus send <to> <action> \"<payload>\" [--type TYPE] [--reply-to ID] [--no-notify]\n")
		os.Exit(1)
	}

	to := args[0]
	action := args[1]
	payload := args[2]
	remaining := args[3:]

	// Parse optional flags from remaining args
	msgType := "request"
	replyTo := ""
	noNotify := false

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
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", remaining[i])
			os.Exit(1)
		}
	}

	// Validate target role
	if !bus.IsKnownRole(to) {
		fmt.Fprintf(os.Stderr, "Error: unknown role '%s'. Known roles: %s\n", to, strings.Join(bus.KnownRoles, ", "))
		os.Exit(1)
	}

	session := bus.BusSession()
	from := bus.BusRole()

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
}
