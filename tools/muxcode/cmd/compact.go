package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Compact handles the "muxcode compact" subcommand.
// Waits for an agent to become idle, then injects /compact via tmux send-keys.
// Usage: muxcode compact [role]
func Compact(args []string) {
	role := os.Getenv("AGENT_ROLE")
	if role == "" {
		role = "edit"
	}
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		role = args[0]
	}

	session := bus.BusSession()
	if session == "" {
		// No session — exit silently
		return
	}

	target := bus.PaneTarget(session, role)

	// Delegate to provider — handles idle wait, input clearing, and /compact injection
	provider := bus.ResolveProvider(role)
	if err := provider.Compact(session, role, target); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
