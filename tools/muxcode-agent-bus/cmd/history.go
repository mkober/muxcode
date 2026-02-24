package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// History handles the "muxcode-agent-bus history" subcommand.
// Usage: muxcode-agent-bus history <role> [--limit N] [--context]
func History(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus history <role> [--limit N] [--context]\n")
		os.Exit(1)
	}

	role := args[0]
	limit := 20
	contextMode := false

	remaining := args[1:]
	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--limit":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --limit requires a value\n")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(remaining[i])
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "Error: --limit must be a positive integer\n")
				os.Exit(1)
			}
			limit = n
		case "--context":
			contextMode = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", remaining[i])
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus history <role> [--limit N] [--context]\n")
			os.Exit(1)
		}
	}

	session := bus.BusSession()

	if contextMode {
		ctx, err := bus.ExtractContext(session, role, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading context: %v\n", err)
			os.Exit(1)
		}
		if ctx == "" {
			fmt.Fprintf(os.Stderr, "No activity found for %s\n", role)
			return
		}
		fmt.Print(ctx)
	} else {
		msgs := bus.ReadLogHistory(session, role, limit)
		if len(msgs) == 0 {
			fmt.Fprintf(os.Stderr, "No messages found for %s\n", role)
			return
		}
		fmt.Print(bus.FormatHistory(msgs, role))
	}
}
