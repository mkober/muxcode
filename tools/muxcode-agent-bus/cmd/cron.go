package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Cron handles the "muxcode-agent-bus cron" subcommand.
func Cron(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus cron <add|list|remove|enable|disable|history> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "add":
		cronAdd(subArgs)
	case "list":
		cronList(subArgs)
	case "remove":
		cronRemove(subArgs)
	case "enable":
		cronEnable(subArgs)
	case "disable":
		cronDisable(subArgs)
	case "history":
		cronHistory(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown cron subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus cron <add|list|remove|enable|disable|history> [args...]\n")
		os.Exit(1)
	}
}

// cronAdd handles: cron add "@every 5m" commit status "Run git status and report"
func cronAdd(args []string) {
	if len(args) < 4 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus cron add <schedule> <target> <action> <message>\n")
		fmt.Fprintf(os.Stderr, "  schedule: @every 30s, @every 5m, @hourly, @daily, @half-hourly\n")
		fmt.Fprintf(os.Stderr, "  target:   agent role (build, test, commit, etc.)\n")
		os.Exit(1)
	}

	schedule := args[0]
	target := args[1]
	action := args[2]
	message := strings.Join(args[3:], " ")

	session := bus.BusSession()

	entry, err := bus.AddCronEntry(session, bus.CronEntry{
		Schedule: schedule,
		Target:   target,
		Action:   action,
		Message:  message,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error adding cron entry: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Added cron entry: %s\n", entry.ID)
	fmt.Printf("  Schedule: %s  Target: %s  Action: %s\n", schedule, target, action)
	fmt.Printf("  Message: %s\n", message)
}

// cronList handles: cron list [--all]
func cronList(args []string) {
	showAll := false
	for _, arg := range args {
		switch arg {
		case "--all":
			showAll = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", arg)
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus cron list [--all]\n")
			os.Exit(1)
		}
	}

	session := bus.BusSession()
	entries, err := bus.ReadCronEntries(session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading cron entries: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatCronList(entries, showAll))
}

// cronRemove handles: cron remove <id>
func cronRemove(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus cron remove <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	if err := bus.RemoveCronEntry(session, args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing cron entry: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed cron entry: %s\n", args[0])
}

// cronEnable handles: cron enable <id>
func cronEnable(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus cron enable <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	if err := bus.SetCronEnabled(session, args[0], true); err != nil {
		fmt.Fprintf(os.Stderr, "Error enabling cron entry: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Enabled cron entry: %s\n", args[0])
}

// cronDisable handles: cron disable <id>
func cronDisable(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus cron disable <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	if err := bus.SetCronEnabled(session, args[0], false); err != nil {
		fmt.Fprintf(os.Stderr, "Error disabling cron entry: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Disabled cron entry: %s\n", args[0])
}

// cronHistory handles: cron history [--id CRON_ID] [--limit N]
func cronHistory(args []string) {
	cronID := ""
	limit := 0

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --id requires a value\n")
				os.Exit(1)
			}
			i++
			cronID = args[i]
		case "--limit":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --limit requires a value\n")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: --limit must be a number\n")
				os.Exit(1)
			}
			limit = n
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus cron history [--id CRON_ID] [--limit N]\n")
			os.Exit(1)
		}
	}

	session := bus.BusSession()
	entries, err := bus.ReadCronHistory(session, cronID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading cron history: %v\n", err)
		os.Exit(1)
	}

	// Apply limit (take last N entries)
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	fmt.Print(bus.FormatCronHistory(entries))
}
