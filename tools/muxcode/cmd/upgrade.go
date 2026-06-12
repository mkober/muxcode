package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// UpgradeDaemons handles the "muxcode upgrade-daemons" subcommand. It cycles
// every running session daemon (and monitor) so they re-exec the freshly
// installed binary — long-lived daemons otherwise keep running the code loaded
// at their launch and never pick up fixes. build.sh calls this after
// `make install` so every install rolls out to all live sessions.
//
// Usage: muxcode upgrade-daemons [--dry-run]
//
//	--dry-run  list daemons that would be restarted without touching them
func UpgradeDaemons(args []string) {
	dryRun := false
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dryRun = true
		case "-h", "--help":
			fmt.Println("Usage: muxcode upgrade-daemons [--dry-run]")
			fmt.Println("  Restart all running session daemons so they pick up the installed binary.")
			fmt.Println("  Orphan daemons (tmux session gone) are killed without relaunch.")
			fmt.Println("  --dry-run  list daemons that would be restarted without touching them")
			return
		}
	}

	results, err := bus.UpgradeDaemons(dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "upgrade-daemons: %v\n", err)
		os.Exit(1)
	}
	if len(results) == 0 {
		fmt.Println("upgrade-daemons: no running daemons found")
		return
	}

	failed := 0
	for _, r := range results {
		switch {
		case dryRun && r.Orphan:
			fmt.Printf("  %s: orphan (tmux session gone) — would kill without relaunch\n", r.Session)
		case dryRun:
			fmt.Printf("  %s: would restart\n", r.Session)
		case r.Err != nil:
			failed++
			fmt.Fprintf(os.Stderr, "  %s: FAILED — %v\n", r.Session, r.Err)
		case r.Orphan:
			fmt.Printf("  %s: orphan daemon killed (tmux session gone)\n", r.Session)
		default:
			detail := "daemon restarted"
			if r.MonitorRestarted {
				detail = "daemon + monitor restarted"
			}
			fmt.Printf("  %s: %s\n", r.Session, detail)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}
