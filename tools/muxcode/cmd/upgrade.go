package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// UpgradeDaemons handles the "muxcode upgrade-daemons" subcommand. It cycles
// every running session daemon (and monitor) that is not already on this
// binary's build, so they re-exec the freshly installed binary — long-lived
// daemons otherwise keep running the code loaded at their launch and never
// pick up fixes. build.sh calls this after `make install` so every install
// rolls out to all live sessions. Each line names the daemon's recorded
// build against the installed one, so a stale session is visible at a glance.
//
// Usage: muxcode upgrade-daemons [--dry-run] [--force] [--session <name>]
//
//	--dry-run         list what would happen per session without touching any process
//	--force           restart daemons already on the installed build too
//	--session <name>  act on that session's daemon only (default: every session)
func UpgradeDaemons(args []string) {
	var opts bus.UpgradeOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run", "-n":
			opts.DryRun = true
		case "--force", "-f":
			opts.Force = true
		case "--session":
			if i+1 >= len(args) || args[i+1] == "" || args[i+1][0] == '-' {
				fmt.Fprintln(os.Stderr, "Usage: muxcode upgrade-daemons [--dry-run] [--force] [--session <name>]")
				os.Exit(1)
			}
			opts.Session = args[i+1]
			i++
		case "-h", "--help":
			fmt.Println("Usage: muxcode upgrade-daemons [--dry-run] [--force] [--session <name>]")
			fmt.Println("  Restart running session daemons so they pick up the installed binary.")
			fmt.Println("  Daemons already on the installed build are skipped; orphan daemons")
			fmt.Println("  (tmux session gone) are killed without relaunch.")
			fmt.Println("  --dry-run         list what would happen per session without touching any process")
			fmt.Println("  --force           restart daemons already on the installed build too")
			fmt.Println("  --session <name>  act on that session's daemon only (default: every session)")
			return
		}
	}

	results, err := bus.UpgradeDaemons(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "upgrade-daemons: %v\n", err)
		os.Exit(1)
	}
	if len(results) == 0 {
		if opts.Session != "" {
			fmt.Printf("upgrade-daemons: no running daemon found for session %s\n", opts.Session)
			return
		}
		fmt.Println("upgrade-daemons: no running daemons found")
		return
	}

	failed := 0
	for _, r := range results {
		switch {
		case r.Err != nil:
			failed++
			fmt.Fprintf(os.Stderr, "  %s: FAILED — %v\n", r.Session, r.Err)
		case r.Orphan && opts.DryRun:
			fmt.Printf("  %s: orphan (tmux session gone) — would kill without relaunch\n", r.Session)
		case r.Orphan:
			fmt.Printf("  %s: orphan daemon killed (tmux session gone)\n", r.Session)
		case r.Skipped && opts.DryRun:
			fmt.Printf("  %s: %s — would skip (--force to restart)\n", r.Session, r.VersionDelta())
		case r.Skipped:
			fmt.Printf("  %s: %s — skipped\n", r.Session, r.VersionDelta())
		case opts.DryRun:
			fmt.Printf("  %s: %s — would restart\n", r.Session, r.VersionDelta())
		default:
			detail := "daemon restarted"
			if r.MonitorRestarted {
				detail = "daemon + monitor restarted"
			}
			fmt.Printf("  %s: %s — %s\n", r.Session, r.VersionDelta(), detail)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}
