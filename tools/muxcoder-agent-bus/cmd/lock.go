package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/bus"
)

// Lock handles the "muxcoder-agent-bus lock" subcommand.
func Lock(args []string) {
	session := bus.BusSession()
	role := bus.BusRole()
	if len(args) > 0 {
		role = args[0]
	}

	if err := bus.Lock(session, role); err != nil {
		fmt.Fprintf(os.Stderr, "Error locking: %v\n", err)
		os.Exit(1)
	}
}

// Unlock handles the "muxcoder-agent-bus unlock" subcommand.
func Unlock(args []string) {
	session := bus.BusSession()
	role := bus.BusRole()
	if len(args) > 0 {
		role = args[0]
	}

	if err := bus.Unlock(session, role); err != nil {
		fmt.Fprintf(os.Stderr, "Error unlocking: %v\n", err)
		os.Exit(1)
	}
}

// IsLocked handles the "muxcoder-agent-bus is-locked" subcommand.
func IsLocked(args []string) {
	session := bus.BusSession()
	role := bus.BusRole()
	if len(args) > 0 {
		role = args[0]
	}

	if bus.IsLocked(session, role) {
		os.Exit(0)
	}
	os.Exit(1)
}
