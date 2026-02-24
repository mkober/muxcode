package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Status handles the "muxcode-agent-bus status" subcommand.
// Usage: muxcode-agent-bus status [--json]
func Status(args []string) {
	jsonOutput := false

	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", arg)
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus status [--json]\n")
			os.Exit(1)
		}
	}

	session := bus.BusSession()
	statuses := bus.GetAllAgentStatus(session)

	if jsonOutput {
		out, err := bus.FormatStatusJSON(statuses)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(out)
	} else {
		fmt.Print(bus.FormatStatusTable(statuses))
	}
}
