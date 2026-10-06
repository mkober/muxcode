package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// RestartSelect handles "muxcode restart-select", the TUI behind the
// `Restart Agents` modal (prefix + b menu). The restart runs inside the TUI's
// progress view; `muxcode reload --all [--provider <cli>] --resume` is the
// non-interactive form.
func RestartSelect(args []string) {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "Unknown argument: %s\n", args[0])
		os.Exit(1)
	}
	session := bus.BusSession()
	if session == "" {
		fmt.Fprintln(os.Stderr, "Error: BUS_SESSION not set")
		os.Exit(1)
	}
	tui.NewRestartSelectUI(session).Run()
}
