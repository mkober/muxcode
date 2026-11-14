package cmd

import (
	"errors"
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
// It holds the session's monitor locks for its lifetime — one monitor per
// session, and the owner `upgrade-daemons` discovers when it cannot run ps
// (MUX-161) — and exits when another monitor holds them. Every second it
// consumes an `upgrade-daemons` restart request by re-exec'ing its own
// executable; a failed exec is logged and the monitor carries on.
func runDaemonMonitor(session string) {
	const maxAgeSecs int64 = 30
	const checkEveryTicks = 15

	unlock, err := bus.AcquireInstanceLock(bus.MonitorLockPath(session), bus.MonitorOwnerPath(session))
	if errors.Is(err, bus.ErrLockHeld) {
		fmt.Fprintf(os.Stderr, "  another daemon monitor is already running for session %s\n", session)
		return
	}
	if err != nil {
		bus.LogLifecycle(session, "warn", "monitor", "lock-failed", err.Error())
	} else {
		defer unlock()
	}
	if err := bus.WriteMonitorVersion(session, bus.BuildInfo()); err != nil {
		bus.LogLifecycle(session, "warn", "monitor", "version-write-failed", err.Error())
	}

	for tick := 1; ; tick++ {
		time.Sleep(time.Second)
		if binary, ok := bus.TakeRestartTarget(bus.MonitorRestartRequestPath(session)); ok {
			bus.LogLifecycle(session, "info", "monitor", "monitor-reexec", "re-exec "+binary+" on an upgrade-daemons restart request")
			if err := bus.ReexecForRestart(binary); err != nil {
				bus.LogLifecycle(session, "error", "monitor", "monitor-reexec-failed", err.Error())
			}
		}
		if tick%checkEveryTicks != 0 {
			continue
		}

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
