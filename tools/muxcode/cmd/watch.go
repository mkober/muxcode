package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/daemon"
)

// Watch handles the "muxcode watch" subcommand.
// Usage: muxcode watch [session] [--poll N] [--debounce N] [--monitor]
func Watch(args []string) {
	session := ""
	pollSecs := 2
	debounceSecs := 8
	monitor := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--monitor":
			monitor = true
		case "--poll":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --poll requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --poll must be a positive integer\n")
				os.Exit(1)
			}
			pollSecs = v
		case "--debounce":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --debounce requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.Atoi(args[i])
			if err != nil || v < 1 {
				fmt.Fprintf(os.Stderr, "Error: --debounce must be a positive integer\n")
				os.Exit(1)
			}
			debounceSecs = v
		default:
			// First non-flag argument is the session name
			if session == "" && len(args[i]) > 0 && args[i][0] != '-' {
				session = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
				os.Exit(1)
			}
		}
	}

	if session == "" {
		session = bus.BusSession()
	}

	if monitor {
		runDaemonMonitor(session)
		return
	}

	d := daemon.New(session, pollSecs, debounceSecs)
	if err := d.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runDaemonMonitor monitors the bus daemon and restarts it if stale.
// Runs as a background loop alongside the daemon, checking every 15 seconds.
func runDaemonMonitor(session string) {
	const maxAgeSecs int64 = 30
	const checkInterval = 15 * time.Second

	for {
		time.Sleep(checkInterval)

		// Exit if tmux session no longer exists
		if err := exec.Command("tmux", "has-session", "-t", session).Run(); err != nil {
			bus.LogLifecycle(session, "info", "monitor", "session-gone",
				fmt.Sprintf("tmux session %s no longer exists", session))
			return
		}

		// Check keepalive staleness
		if !bus.IsDaemonAlive(session, maxAgeSecs) {
			// Skip if keepalive file doesn't exist yet (daemon may be starting)
			if _, err := os.Stat(bus.DaemonKeepalivePath(session)); os.IsNotExist(err) {
				continue
			}

			fmt.Printf("  [monitor] Daemon keepalive stale — restarting\n")
			bus.LogLifecycle(session, "warn", "monitor", "stale-detected", "Keepalive stale")

			if err := bus.RestartDaemon(session); err != nil {
				bus.LogLifecycle(session, "error", "monitor", "restart-failed", err.Error())
			} else {
				bus.LogLifecycle(session, "info", "monitor", "daemon-restart", "Daemon restarted")
			}
		}
	}
}
