package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Compact handles the "muxcode compact" subcommand.
// Waits for an agent to become idle, then injects /compact via tmux send-keys.
// Usage: muxcode compact [--all] [role]
func Compact(args []string) {
	session := bus.BusSession()
	if session == "" {
		return
	}

	// Check for --all flag
	if len(args) > 0 && args[0] == "--all" {
		compactAll(session)
		return
	}

	role := os.Getenv("AGENT_ROLE")
	if role == "" {
		role = "edit"
	}
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		role = args[0]
	}

	target := bus.PaneTarget(session, role)

	// Delegate to provider — handles idle wait, input clearing, and /compact injection
	provider := bus.ResolveProvider(role)
	if err := provider.Compact(session, role, target); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// compactAll iterates over all active agents and triggers /compact for each.
// Skips hosted roles (they share a window), stopped agents, and dead agents.
func compactAll(session string) {
	roles := bus.CompactableRoles(session)
	if len(roles) == 0 {
		fmt.Fprintln(os.Stderr, "No active agents to compact")
		return
	}

	for _, role := range roles {
		target := bus.PaneTarget(session, role)
		provider := bus.ResolveProvider(role)
		if err := provider.Compact(session, role, target); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: compact %s: %v\n", role, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "Compacted: %s\n", role)
	}
}
