package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// Dashboard handles the "muxcode dashboard" subcommand.
// Usage: muxcode dashboard [--refresh N]
func Dashboard(args []string) {
	refresh := 5

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--refresh":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --refresh requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --refresh must be a positive integer\n")
				os.Exit(1)
			}
			refresh = v
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			fmt.Fprintf(os.Stderr, "Usage: muxcode dashboard [--refresh N]\n")
			os.Exit(1)
		}
	}

	// Guard: must be inside tmux
	if os.Getenv("TMUX") == "" {
		fmt.Fprintln(os.Stderr, "muxcode dashboard must run inside a tmux session.")
		fmt.Fprintln(os.Stderr, "Use the 'muxcode' command to launch an editor session.")
		os.Exit(1)
	}

	session := bus.BusSession()
	if session == "" {
		fmt.Fprintln(os.Stderr, "Could not determine tmux session name.")
		os.Exit(1)
	}

	d := tui.NewDashboard(session, refresh)
	if err := d.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
