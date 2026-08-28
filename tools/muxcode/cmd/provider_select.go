package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// ProviderSelect handles the "muxcode provider-select" subcommand.
//
// Launches an interactive TUI that lets the user pick a CLI provider and
// model. On confirmation the TUI performs the reload itself inside its
// progress view — single agent or batch alike — so nothing executes
// after it exits.
//
// Usage:
//
//	muxcode provider-select [--role <role>]
//
// If --role is not specified, the active tmux window's agent is targeted.
func ProviderSelect(args []string) {
	var roleFlag string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --role requires a value")
				os.Exit(1)
			}
			i++
			roleFlag = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown argument: %s\n", args[i])
			os.Exit(1)
		}
	}

	session := bus.BusSession()
	if session == "" {
		fmt.Fprintln(os.Stderr, "Error: BUS_SESSION not set")
		os.Exit(1)
	}

	var window, role string
	if roleFlag != "" {
		role = roleFlag
		window = bus.WindowForRole(role)
	} else {
		var err error
		window, role, err = bus.ResolveActiveAgentWindow(session)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving active window: %v\n", err)
			os.Exit(1)
		}
	}

	if !bus.IsKnownRole(role) {
		fmt.Fprintf(os.Stderr, "Error: unknown role: %s\n", role)
		os.Exit(1)
	}

	ui := tui.NewProviderSelectUI(session, role, window)
	// Every reload — single or batch — runs inside the TUI progress view
	// (user request 2026-08-28), so nothing remains to execute here.
	ui.Run()
}
