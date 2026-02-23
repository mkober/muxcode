package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Send handles the "muxcode-agent-bus send" subcommand.
// Usage: muxcode-agent-bus send <to> <action> "<payload>" [--type TYPE] [--reply-to ID] [--no-notify]
//
//	muxcode-agent-bus send <to> <action> --stdin [--type TYPE] [--reply-to ID] [--no-notify]
//
// When --stdin is used, the payload is read from stdin instead of a positional argument.
// This avoids multi-line command strings that break allowedTools glob patterns.
// Example: printf 'line1\nline2' | muxcode-agent-bus send test review-complete --stdin --type response
func Send(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus send <to> <action> \"<payload>\" [--type TYPE] [--reply-to ID] [--no-notify]\n")
		fmt.Fprintf(os.Stderr, "       muxcode-agent-bus send <to> <action> --stdin [--type TYPE] [--reply-to ID] [--no-notify]\n")
		os.Exit(1)
	}

	to := args[0]
	action := args[1]

	// Scan all remaining args for flags first, then determine payload source.
	payload := ""
	msgType := "request"
	replyTo := ""
	noNotify := false
	useStdin := false
	payloadSet := false

	remaining := args[2:]
	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--stdin":
			useStdin = true
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

	// Read payload from stdin if --stdin is set
	if useStdin && payloadSet {
		fmt.Fprintf(os.Stderr, "Error: --stdin and a positional payload are mutually exclusive\n")
		os.Exit(1)
	}
	if useStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
		payload = strings.TrimRight(string(data), "\n")
	} else if !payloadSet {
		fmt.Fprintf(os.Stderr, "Error: payload is required (provide as argument or use --stdin)\n")
		os.Exit(1)
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
