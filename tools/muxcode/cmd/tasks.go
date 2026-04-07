package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Tasks handles the "muxcode tasks" subcommand.
// Usage: muxcode tasks [--all] [--status STATUS]
func Tasks(args []string) {
	session := bus.BusSession()

	showAll := false
	filterStatus := bus.TaskInFlight

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--all":
			showAll = true
		case "--status":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --status requires a value\n")
				os.Exit(1)
			}
			i++
			filterStatus = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\nUsage: muxcode tasks [--all] [--status STATUS]\n", args[i])
			os.Exit(1)
		}
	}

	if showAll {
		filterStatus = ""
	}

	tasks, err := bus.ListTasks(session, filterStatus)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing tasks: %v\n", err)
		os.Exit(1)
	}

	if len(tasks) == 0 {
		if showAll {
			fmt.Println("No tasks")
		} else {
			fmt.Println("No in-flight tasks")
		}
		return
	}

	for _, t := range tasks {
		fmt.Println(bus.FormatTask(t))
	}
}
