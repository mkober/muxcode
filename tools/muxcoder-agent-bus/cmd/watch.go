package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/bus"
	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/watcher"
)

// Watch handles the "muxcoder-agent-bus watch" subcommand.
// Usage: muxcoder-agent-bus watch [session] [--poll N] [--debounce N]
func Watch(args []string) {
	session := ""
	pollSecs := 2
	debounceSecs := 8

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--poll":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --poll requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --poll must be a positive integer\n")
				os.Exit(1)
			}
			pollSecs = v
		case "--debounce":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --debounce requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --debounce must be a positive integer\n")
				os.Exit(1)
			}
			debounceSecs = v
		default:
			// First non-flag argument is the session name
			if session == "" && len(args[i]) > 0 && args[i][0] != '-' {
				session = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
				os.Exit(1)
			}
		}
	}

	if session == "" {
		session = bus.BusSession()
	}

	w := watcher.New(session, pollSecs, debounceSecs)
	if err := w.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
