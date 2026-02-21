package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Cleanup handles the "muxcode-agent-bus cleanup" subcommand.
func Cleanup(args []string) {
	session := ""
	if len(args) > 0 {
		session = args[0]
	}
	if session == "" {
		session = bus.BusSession()
	}

	busDir := bus.BusDir(session)
	if err := bus.Cleanup(session); err != nil {
		fmt.Fprintf(os.Stderr, "Error cleaning up: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Cleaned up: %s\n", busDir)
}
