package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Console handles the "muxcode-agent-bus console" subcommand.
// Usage: muxcode-agent-bus console <role> [--interval N] [--once]
//
// Renders a Dracula-themed status display for the given role's left pane.
// Polls the role's history JSONL file and refreshes the terminal at the
// specified interval (default 5 seconds).
//
// --once renders a single frame and exits (useful for testing).
func Console(args []string) {
	if len(args) < 1 {
		roles := bus.ConsoleRoles()
		sort.Strings(roles)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus console <role> [--interval N] [--once]\n\nRoles: %s\n", strings.Join(roles, ", "))
		os.Exit(1)
	}

	role := args[0]
	remaining := args[1:]

	interval := 5
	once := false

	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--interval":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --interval requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(remaining[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --interval must be a positive integer\n")
				os.Exit(1)
			}
			interval = v
		case "--once":
			once = true
		default:
			// Try to parse bare number as interval (backwards compat with shell scripts)
			if v, err := strconv.Atoi(remaining[i]); err == nil && v > 0 {
				interval = v
			} else {
				fmt.Fprintf(os.Stderr, "Error: unknown flag: %s\n", remaining[i])
				os.Exit(1)
			}
		}
	}

	// Validate role
	configs := bus.DefaultConsoleConfigs()
	if _, ok := configs[role]; !ok {
		roles := bus.ConsoleRoles()
		sort.Strings(roles)
		fmt.Fprintf(os.Stderr, "Error: unknown role %q\nAvailable: %s\n", role, strings.Join(roles, ", "))
		os.Exit(1)
	}

	session := bus.BusSession()

	for {
		width := bus.TerminalWidth()

		// Build header
		header := bus.ConsoleHeader(configs[role].Title, interval, width)

		// Build body
		body := bus.RenderConsole(role, session, width)

		// Clear screen and render
		fmt.Print("\033[2J\033[H")
		fmt.Print(header)
		fmt.Print(body)

		if once {
			return
		}

		time.Sleep(time.Duration(interval) * time.Second)
	}
}
