package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Spawn handles the "muxcode-agent-bus spawn" subcommand.
func Spawn(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus spawn <start|list|status|result|stop|clean> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "start":
		spawnStart(subArgs)
	case "list":
		spawnList(subArgs)
	case "status":
		spawnStatus(subArgs)
	case "result":
		spawnResult(subArgs)
	case "stop":
		spawnStop(subArgs)
	case "clean":
		spawnClean(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown spawn subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus spawn <start|list|status|result|stop|clean> [args...]\n")
		os.Exit(1)
	}
}

// spawnStart handles: spawn start <role> "<task>"
func spawnStart(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus spawn start <role> \"<task>\"\n")
		os.Exit(1)
	}

	role := args[0]
	task := strings.Join(args[1:], " ")
	session := bus.BusSession()
	owner := bus.BusRole()

	entry, err := bus.StartSpawn(session, role, task, owner)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting spawn: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Started spawn: %s\n", entry.ID)
	fmt.Printf("  Role: %s  Spawn Role: %s  Owner: %s\n", entry.Role, entry.SpawnRole, entry.Owner)
	fmt.Printf("  Window: %s\n", entry.Window)
	fmt.Printf("  Task: %s\n", entry.Task)
}

// spawnList handles: spawn list [--all]
func spawnList(args []string) {
	showAll := false
	for _, arg := range args {
		switch arg {
		case "--all":
			showAll = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", arg)
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus spawn list [--all]\n")
			os.Exit(1)
		}
	}

	session := bus.BusSession()

	entries, err := bus.ReadSpawnEntries(session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading spawn entries: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatSpawnList(entries, showAll))
}

// spawnStatus handles: spawn status <id>
func spawnStatus(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus spawn status <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()

	entry, err := bus.GetSpawnEntry(session, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatSpawnStatus(entry))
}

// spawnResult handles: spawn result <id>
func spawnResult(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus spawn result <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()

	entry, err := bus.GetSpawnEntry(session, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	result, ok := bus.GetSpawnResult(session, entry.SpawnRole)
	if !ok {
		fmt.Println("No result available — spawn has not sent any messages.")
		return
	}

	fmt.Print(bus.FormatMessage(result))
}

// spawnStop handles: spawn stop <id>
func spawnStop(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus spawn stop <id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	if err := bus.StopSpawn(session, args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "Error stopping spawn: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Stopped spawn: %s\n", args[0])
}

// spawnClean handles: spawn clean
func spawnClean(args []string) {
	session := bus.BusSession()
	removed, err := bus.CleanFinishedSpawns(session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error cleaning finished spawns: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Cleaned %d finished spawn(s).\n", removed)
}
