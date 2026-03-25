package cmd

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// printWithLineClears prints text with \033[K (clear-to-EOL) appended after
// each line. This prevents stale characters from lingering when a line becomes
// shorter between refreshes (e.g. timestamp changes, entry count changes).
func printWithLineClears(text string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i < len(lines)-1 {
			fmt.Print(line + "\033[K\n")
		} else if line != "" {
			// Last segment without trailing newline
			fmt.Print(line + "\033[K")
		}
	}
}

// Console handles the "muxcode console" subcommand.
// Usage: muxcode console <role> [--interval N] [--once]
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
		fmt.Fprintf(os.Stderr, "Usage: muxcode console <role> [--interval N] [--once]\n\nRoles: %s\n", strings.Join(roles, ", "))
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

	// Initial clear to remove shell startup content
	fmt.Print("\033[2J\033[H")

	for {
		width := bus.TerminalWidth()

		// Build header
		header := bus.ConsoleHeader(configs[role].Title, interval, width)

		// Workflow state line
		wfEntry := bus.ReadWorkflowState(session)
		wfLine := ""
		if wfEntry.State != bus.StateIdle || wfEntry.Since > 0 {
			wfLine = fmt.Sprintf("%s%sworkflow:%s %s\n\n",
				bus.Pad, bus.ColorDim, bus.ColorReset,
				bus.FormatWorkflowStateCompact(wfEntry, width))
		}

		// Build body
		body := bus.RenderConsole(role, session, width)

		// Move cursor to home and overwrite in place (no full clear).
		// This keeps the header visually fixed — no flicker between refreshes.
		// Each line gets \033[K (clear-to-EOL) to remove stale characters when
		// content shrinks. \033[J after the body clears any stale trailing lines.
		fmt.Print("\033[H")
		printWithLineClears(header)
		if wfLine != "" {
			printWithLineClears(wfLine)
		}
		printWithLineClears(body)
		fmt.Print("\033[J")

		if once {
			return
		}

		time.Sleep(time.Duration(interval) * time.Second)
	}
}
