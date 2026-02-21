package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Notify handles the "muxcode-agent-bus notify" subcommand.
func Notify(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus notify <role>\n")
		os.Exit(1)
	}

	role := args[0]
	if !bus.IsKnownRole(role) {
		fmt.Fprintf(os.Stderr, "Error: unknown role '%s'. Known roles: %s\n", role, strings.Join(bus.KnownRoles, ", "))
		os.Exit(1)
	}

	session := bus.BusSession()
	_ = bus.Notify(session, role)
}
