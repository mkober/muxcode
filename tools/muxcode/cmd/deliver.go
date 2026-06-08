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
// Usage: muxcode deliver <role> [--force]
//
//	--force  skip the idle-prompt check and inject regardless of pane state
func Deliver(args []string) {
	var role string
	force := false
	for _, a := range args {
		switch a {
		case "--force", "-f":
			force = true
		case "-h", "--help":
			fmt.Println("Usage: muxcode deliver <role> [--force]")
			fmt.Println("  Force-deliver an agent's pending inbox messages into its pane.")
			fmt.Println("  --force  skip the idle-prompt check and inject regardless of pane state")
			return
		default:
			if role == "" {
				role = a
			}
		}
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Usage: muxcode deliver <role> [--force]")
		os.Exit(1)
	}

	session, err := bus.TmuxCurrentSession()
	if err != nil || session == "" {
		session = os.Getenv("BUS_SESSION")
	}
	if session == "" {
		fmt.Fprintln(os.Stderr, "deliver: could not determine session (set BUS_SESSION or run inside tmux)")
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
