package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Deliver handles the "muxcode deliver" subcommand. It force-delivers an agent's
// pending inbox messages into its pane, bypassing the daemon's idle-detection —
// a recovery for the "active-with-stale-messages" wedge where a finished agent is
// misread as busy and never receives its inbox.
//
// Usage: muxcode deliver <role> [--force] [--session <name>]
//
//	--force           skip the idle-prompt check and inject regardless of pane state
//	--session <name>  target a different muxcode session (e.g. a subsession)
func Deliver(args []string) {
	var role, session string
	force := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--force", "-f":
			force = true
		case "--session", "-s":
			if i+1 < len(args) {
				i++
				session = args[i]
			}
		case "-h", "--help":
			fmt.Println("Usage: muxcode deliver <role> [--force] [--session <name>]")
			fmt.Println("  Force-deliver an agent's pending inbox messages into its pane.")
			fmt.Println("  --force           skip the idle-prompt check and inject regardless of pane state")
			fmt.Println("  --session <name>  target a different muxcode session (default: current)")
			return
		default:
			if role == "" {
				role = args[i]
			}
		}
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Usage: muxcode deliver <role> [--force] [--session <name>]")
		os.Exit(1)
	}

	if session == "" {
		var err error
		session, err = bus.TmuxCurrentSession()
		if err != nil || session == "" {
			session = os.Getenv("BUS_SESSION")
		}
	}
	if session == "" {
		fmt.Fprintln(os.Stderr, "deliver: could not determine session (set BUS_SESSION, use --session, or run inside tmux)")
		os.Exit(1)
	}

	res, err := bus.ForceDeliver(session, role, force)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deliver: %v\n", err)
		os.Exit(1)
	}
	if res.Delivered == 0 {
		fmt.Printf("deliver: nothing delivered to %s (%s)\n", res.Role, res.Skipped)
		return
	}
	fmt.Printf("deliver: woke %s with %d pending message(s)\n", res.Role, res.Delivered)
}
