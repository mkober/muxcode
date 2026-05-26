package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// ProviderSelect handles the "muxcode provider-select" subcommand.
//
// Launches an interactive TUI that lets the user pick a CLI provider
// and model for the currently active agent window. On confirmation,
// writes a reload trigger file that the modal wrapper executes after
// the TUI exits.
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
	cli, model, compact, roles, cancelled := ui.Run()

	if cancelled {
		return
	}

	// Multi-agent reloads are handled by the TUI progress view — nothing to do here.
	// Single-agent reloads still use ExecuteReload for subprocess output visibility.
	if len(roles) <= 1 {
		if err := tui.ExecuteReload(session, role, cli, model, compact, roles); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing reload trigger: %v\n", err)
			os.Exit(1)
		}
	}
}
