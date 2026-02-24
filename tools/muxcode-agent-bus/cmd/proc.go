package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Proc handles the "muxcode-agent-bus proc" subcommand.
func Proc(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus proc <start|list|status|log|stop|clean> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "start":
		procStart(subArgs)
	case "list":
		procList(subArgs)
	case "status":
		procStatus(subArgs)
	case "log":
		procLog(subArgs)
	case "stop":
		procStop(subArgs)
	case "clean":
		procClean(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown proc subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus proc <start|list|status|log|stop|clean> [args...]\n")
		os.Exit(1)
	}
}

// procStart handles: proc start "<command>" [--dir DIR]
func procStart(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus proc start \"<command>\" [--dir DIR]\n")
		os.Exit(1)
	}

	dir, _ := os.Getwd()
	var command string
	var positionals []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --dir requires a value\n")
				os.Exit(1)
			}
			i++
			dir = args[i]
		default:
			positionals = append(positionals, args[i])
		}
	}

	if len(positionals) == 0 {
		fmt.Fprintf(os.Stderr, "Error: command is required\n")
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus proc start \"<command>\" [--dir DIR]\n")
		os.Exit(1)
	}

	command = strings.Join(positionals, " ")
	session := bus.BusSession()
	owner := bus.BusRole()

	entry, err := bus.StartProc(session, command, dir, owner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting process: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Started process: %s\n", entry.ID)
	fmt.Printf("  PID: %d  Owner: %s\n", entry.PID, entry.Owner)
	fmt.Printf("  Command: %s\n", entry.Command)
	fmt.Printf("  Log: %s\n", entry.LogFile)
}

// procList handles: proc list [--all]
func procList(args []string) {
	showAll := false
	for _, arg := range args {
		switch arg {
		case "--all":
			showAll = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", arg)
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus proc list [--all]\n")
			os.Exit(1)
		}
	}

	session := bus.BusSession()

	// Refresh status before listing
	_, _ = bus.RefreshProcStatus(session)

	entries, err := bus.ReadProcEntries(session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading proc entries: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatProcList(entries, showAll))
}

// procStatus handles: proc status <id>
func procStatus(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus proc status <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()

	// Refresh before checking status
	_, _ = bus.RefreshProcStatus(session)

	entry, err := bus.GetProcEntry(session, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatProcStatus(entry))
}

// procLog handles: proc log <id> [--tail N]
func procLog(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus proc log <id> [--tail N]\n")
		os.Exit(1)
	}

	id := args[0]
	tail := 0

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--tail":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --tail requires a value\n")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: --tail must be a number\n")
				os.Exit(1)
			}
			tail = n
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	session := bus.BusSession()
	entry, err := bus.GetProcEntry(session, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	data, err := os.ReadFile(entry.LogFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading log: %v\n", err)
		os.Exit(1)
	}

	content := string(data)

	if tail > 0 {
		lines := strings.Split(content, "\n")
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		content = strings.Join(lines, "\n")
	}

	fmt.Print(content)
}

// procStop handles: proc stop <id>
func procStop(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus proc stop <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	if err := bus.StopProc(session, args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping process: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Stopped process: %s\n", args[0])
}

// procClean handles: proc clean
func procClean(args []string) {
	session := bus.BusSession()
	removed, err := bus.CleanFinished(session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error cleaning finished processes: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Cleaned %d finished process(es).\n", removed)
}
