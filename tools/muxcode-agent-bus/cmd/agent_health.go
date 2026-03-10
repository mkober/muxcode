package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// AgentHealth handles the "muxcode-agent-bus agent-health" subcommand.
// Usage: muxcode-agent-bus agent-health [--stop <role>] [--start <role>] [--check <role>]
func AgentHealth(args []string) {
	session := bus.BusSession()

	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus agent-health [--stop <role>] [--start <role>] [--check <role>]\n")
		os.Exit(1)
	}

	flag := args[0]
	role := args[1]

	switch flag {
	case "--stop":
		if err := bus.MarkAgentStopped(session, role); err != nil {
			fmt.Fprintf(os.Stderr, "Error marking %s as stopped: %v\n", role, err)
			os.Exit(1)
		}
		fmt.Printf("Agent %s marked as stopped (auto-restart suppressed)\n", role)

	case "--start":
		bus.ClearAgentStopped(session, role)
		fmt.Printf("Agent %s stop marker cleared (auto-restart enabled)\n", role)

	case "--check":
		if bus.IsAgentHealthExcluded(role) {
			fmt.Printf("Agent %s is excluded from health monitoring\n", role)
			return
		}
		if bus.IsAgentStopped(session, role) {
			fmt.Printf("Agent %s is intentionally stopped\n", role)
			return
		}
		alive := bus.IsAgentAlive(session, role)
		if alive {
			fmt.Printf("Agent %s is alive\n", role)
		} else {
			fmt.Printf("Agent %s appears dead\n", role)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", flag)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus agent-health [--stop <role>] [--start <role>] [--check <role>]\n")
		os.Exit(1)
	}
}
