package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

const resumeUsage = `Usage: muxcode resume <role> [--force]

Relaunch a dead Claude Code agent into the session its pane's exit banner
offers (claude --resume <id>), with the full launch flags. No banner → a fresh
flagged launch. Refuses a live agent unless --force (which exits it first), and
any non-Claude provider — use muxcode reload <role> for a fresh start.

Not muxcode session resume, which prints saved memory summaries.
`

// Resume handles "muxcode resume <role> [--force]" (MUX-126 Phase 6).
func Resume(args []string) {
	role, force := "", false
	for _, a := range args {
		switch {
		case a == "--force":
			force = true
		case a == "-h" || a == "--help":
			fmt.Print(resumeUsage)
			return
		case len(a) > 0 && a[0] == '-':
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n\n%s", a, resumeUsage)
			os.Exit(1)
		case role == "":
			role = a
		default:
			fmt.Fprint(os.Stderr, resumeUsage)
			os.Exit(1)
		}
	}
	if role == "" {
		fmt.Fprint(os.Stderr, resumeUsage)
		os.Exit(1)
	}

	session := bus.BusSession()
	if exec.Command("tmux", "has-session", "-t", "="+session).Run() != nil {
		fmt.Fprintf(os.Stderr, "Error: no tmux session %q — run inside a muxcode session or set BUS_SESSION\n", session)
		os.Exit(1)
	}
	if err := bus.ResumeAgent(session, role, force, bus.BusActor()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Relaunched %s — see `muxcode lifecycle show --source manual` for whether it resumed\n", role)
}
