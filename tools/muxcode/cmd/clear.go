package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Clear handles the "muxcode clear" subcommand — run the guarded auto-clear
// path for one role on demand (MUX-103). The same guard matrix as the daemon
// trigger applies (edit/auto exclusion, Claude provider, idle, no pending
// inbox, no in-flight task); only the completed-task trigger and quiet window
// are skipped, since the human asking IS the trigger.
// Usage: muxcode clear <role>
func Clear(args []string) {
	if len(args) < 1 || args[0] == "" || args[0][0] == '-' {
		fmt.Fprintln(os.Stderr, "Usage: muxcode clear <role>")
		os.Exit(1)
	}
	// BusSession never returns "" — it falls back to "default" outside tmux —
	// so the target session must be validated for real. Without this, a clear
	// run outside any muxcode session walks the guard matrix against a
	// nonexistent session and fails with a misleading "agent not idle". The
	// "=" prefix forces exact-name matching (bare -t prefix-matches).
	session := bus.BusSession()
	if exec.Command("tmux", "has-session", "-t", "="+session).Run() != nil {
		fmt.Fprintf(os.Stderr, "Error: no tmux session %q — run inside a muxcode session or set BUS_SESSION\n", session)
		os.Exit(1)
	}
	role := args[0]

	ok, reason := bus.AutoClearEligible(session, role)
	if !ok {
		fmt.Fprintf(os.Stderr, "Cannot clear %s: %s\n", role, reason)
		os.Exit(1)
	}
	if err := bus.ClearAgent(session, role, "cli", "manual"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Cleared: %s\n", role)
}
