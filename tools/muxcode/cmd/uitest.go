package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// UITest handles the "muxcode uitest" subcommand.
// Runs integration tests that exercise tmux UI behavior in a live session.
func UITest(args []string) {
	suites := bus.AllUITestSuites()

	// Parse flags.
	var filter string
	list := false
	verbose := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--list", "-l":
			list = true
		case "--verbose", "-v":
			verbose = true
		default:
			if filter == "" {
				filter = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "Unknown argument: %s\n", args[i])
				fmt.Fprintf(os.Stderr, "Usage: muxcode uitest [suite] [--list] [--verbose]\n")
				os.Exit(1)
			}
		}
	}

	// List mode.
	if list {
		bus.ListSuites(suites)
		return
	}

	// Require a live session.
	session := bus.BusSession()
	if session == "" || session == "default" {
		fmt.Fprintf(os.Stderr, "Error: uitest requires a running muxcode session\n")
		fmt.Fprintf(os.Stderr, "Run from within a muxcode tmux session or set BUS_SESSION\n")
		os.Exit(1)
	}

	// Filter to a specific suite if requested.
	if filter != "" {
		var filtered []bus.UITestSuite
		for _, s := range suites {
			if s.Name == filter {
				filtered = append(filtered, s)
			}
		}
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stderr, "Unknown test suite: %s\n", filter)
			fmt.Fprintf(os.Stderr, "Available suites:\n")
			for _, s := range suites {
				fmt.Fprintf(os.Stderr, "  %s\n", s.Name)
			}
			os.Exit(1)
		}
		suites = filtered
	}

	// Run.
	runner := &bus.UITestRunner{
		Session: session,
		Verbose: verbose,
	}
	failures := runner.RunSuites(suites)
	if failures > 0 {
		os.Exit(1)
	}
}
