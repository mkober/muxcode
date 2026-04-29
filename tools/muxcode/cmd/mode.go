package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Mode handles the "muxcode mode" subcommand.
func Mode(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode mode <cycle|status|switch|list|active> [--window <name>]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "cycle":
		modeCycle(subArgs)
	case "status":
		modeStatus(subArgs)
	case "switch":
		modeSwitch(subArgs)
	case "list":
		modeList(subArgs)
	case "active":
		modeActive(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode mode <cycle|status|switch|list|active> [--window <name>]\n")
		os.Exit(1)
	}
}

// parseWindowFlag extracts --window <name> from args, defaulting to "edit".
// Returns the window name and remaining args.
func parseWindowFlag(args []string) (string, []string) {
	window := "edit"
	var remaining []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--window" && i+1 < len(args) {
			window = args[i+1]
			i++
		} else {
			remaining = append(remaining, args[i])
		}
	}
	return window, remaining
}

// modeCycle advances to the next registered mode on the window.
func modeCycle(args []string) {
	window, _ := parseWindowFlag(args)
	session := bus.BusSession()
	if err := bus.ModeCycle(session, window); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// No output — this runs via tmux run-shell where stdout becomes
	// an overlay message that blocks the view until a keypress.
}

// modeStatus shows the current mode cycle state.
func modeStatus(args []string) {
	window, _ := parseWindowFlag(args)
	session := bus.BusSession()
	state, err := bus.ReadModeCycleState(session, window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(bus.FormatModeStatus(state))
}

// modeSwitch jumps to a specific mode by name.
func modeSwitch(args []string) {
	window, remaining := parseWindowFlag(args)
	if len(remaining) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode mode switch <mode> [--window <name>]\n")
		os.Exit(1)
	}

	mode := remaining[0]
	session := bus.BusSession()
	if err := bus.ModeSwitch(session, window, mode); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// modeList shows all registered modes for a window.
func modeList(args []string) {
	window, _ := parseWindowFlag(args)
	session := bus.BusSession()
	state, err := bus.ReadModeCycleState(session, window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(bus.FormatModeList(state))
}

// modeActive prints the currently active role for a window.
func modeActive(args []string) {
	window, _ := parseWindowFlag(args)
	session := bus.BusSession()
	role, err := bus.ActiveModeRole(session, window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(role)
}
