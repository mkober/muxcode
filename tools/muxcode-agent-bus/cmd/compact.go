package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Compact handles the "muxcode-agent-bus compact" subcommand.
// Waits for an agent to become idle, then injects /compact via tmux send-keys.
// Usage: muxcode-agent-bus compact [role]
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

	// Wait for agent to reach idle (❯ prompt), max 30 seconds
	idle := false
	for i := 0; i < 30; i++ {
		if bus.IsAgentIdle(session, role) {
			idle = true
			break
		}
		time.Sleep(1 * time.Second)
	}

	if !idle {
		// Agent never became idle — exit silently
		return
	}

	// Clear any residual input
	_ = exec.Command("tmux", "send-keys", "-t", target, "Escape").Run()
	time.Sleep(100 * time.Millisecond)
	_ = exec.Command("tmux", "send-keys", "-t", target, "C-u").Run()
	time.Sleep(100 * time.Millisecond)

	// Inject /compact + Enter (separate calls per tmux send-keys convention)
	if err := exec.Command("tmux", "send-keys", "-t", target, "/compact").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending /compact: %v\n", err)
		os.Exit(1)
	}
	time.Sleep(200 * time.Millisecond)
	_ = exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
}
