package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

var lifecycleUsage = `Usage: muxcode-agent-bus lifecycle <subcommand> [args...]

Subcommands:
  log <session> <level> <source> <event> [--detail TEXT] [--pid N]
      Write a lifecycle log entry

  show [session] [--limit N] [--source S] [--level L] [--event E] [--since DURATION]
      Show lifecycle log entries (default: current session, last 50)

  list
      List sessions with lifecycle logs

  purge [--days N]
      Remove log files older than N days (default: 30)
`

// Lifecycle handles the "muxcode-agent-bus lifecycle" subcommand.
func Lifecycle(args []string) {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, lifecycleUsage)
		os.Exit(1)
	}

	switch args[0] {
	case "log":
		lifecycleLog(args[1:])
	case "show":
		lifecycleShow(args[1:])
	case "list":
		lifecycleList()
	case "purge":
		lifecyclePurge(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown lifecycle subcommand: %s\n\n", args[0])
		fmt.Fprint(os.Stderr, lifecycleUsage)
		os.Exit(1)
	}
}

func lifecycleLog(args []string) {
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode-agent-bus lifecycle log <session> <level> <source> <event> [--detail TEXT] [--pid N]")
		os.Exit(1)
	}

	session := args[0]
	level := args[1]
	source := args[2]
	event := args[3]
	remaining := args[4:]

	detail := ""
	pid := 0

	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--detail":
			if i+1 >= len(remaining) {
				fmt.Fprintln(os.Stderr, "--detail requires a value")
				os.Exit(1)
			}
			i++
			detail = remaining[i]
		case "--pid":
			if i+1 >= len(remaining) {
				fmt.Fprintln(os.Stderr, "--pid requires a value")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(remaining[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid --pid value: %s\n", remaining[i])
				os.Exit(1)
			}
			pid = n
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", remaining[i])
			os.Exit(1)
		}
	}

	if pid > 0 {
		bus.LogLifecycleWithPID(session, level, source, event, detail, pid)
	} else {
		bus.LogLifecycle(session, level, source, event, detail)
	}
}

func lifecycleShow(args []string) {
	session := ""
	opts := bus.LifecycleFilterOpts{
		Limit: 50,
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--limit requires a value")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid --limit: %s\n", args[i])
				os.Exit(1)
			}
			opts.Limit = n
		case "--source":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--source requires a value")
				os.Exit(1)
			}
			i++
			opts.Source = args[i]
		case "--level":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--level requires a value")
				os.Exit(1)
			}
			i++
			opts.Level = args[i]
		case "--event":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--event requires a value")
				os.Exit(1)
			}
			i++
			opts.Event = args[i]
		case "--since":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--since requires a duration (e.g. 1h, 30m, 2d)")
				os.Exit(1)
			}
			i++
			d, err := parseDuration(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid --since: %s\n", err)
				os.Exit(1)
			}
			opts.Since = time.Now().Add(-d).Unix()
		case "--all":
			opts.Limit = 0
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
				os.Exit(1)
			}
			// Positional argument = session name
			session = args[i]
		}
	}

	if session == "" {
		session = bus.BusSession()
	}

	entries, err := bus.FilterLifecycleLog(session, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Printf("No lifecycle events for session %q\n", session)
		return
	}

	for _, e := range entries {
		fmt.Println(bus.FormatLifecycleEntry(e))
	}
}

func lifecycleList() {
	sessions, err := bus.ListLifecycleSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(sessions) == 0 {
		fmt.Println("No lifecycle logs found")
		return
	}

	for _, s := range sessions {
		// Show entry count and last modified
		entries, _ := bus.ReadLifecycleLog(s)
		path := bus.LifecycleLogPath(s)
		info, _ := os.Stat(path)
		modified := ""
		if info != nil {
			modified = info.ModTime().Format("2006-01-02 15:04")
		}
		fmt.Printf("  %-30s  %4d entries  %s\n", s, len(entries), modified)
	}
}

func lifecyclePurge(args []string) {
	days := 30
	for i := 0; i < len(args); i++ {
		if args[i] == "--days" && i+1 < len(args) {
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid --days: %s\n", args[i])
				os.Exit(1)
			}
			days = n
		}
	}

	removed, err := bus.PurgeLifecycleLogs(days)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Purged %d log file(s) older than %d days\n", removed, days)
}

// parseDuration parses a human-friendly duration string (e.g. "1h", "30m", "2d", "1h30m").
func parseDuration(s string) (time.Duration, error) {
	// Handle days suffix (not supported by time.ParseDuration)
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %s (use format like 1h, 30m, 2d)", s)
	}
	return d, nil
}
