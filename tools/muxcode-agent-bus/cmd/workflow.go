package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Workflow handles the "muxcode-agent-bus workflow" subcommand.
// Usage: muxcode-agent-bus workflow [--json]
//
//	muxcode-agent-bus workflow reset
func Workflow(args []string) {
	session := bus.BusSession()

	if len(args) > 0 && args[0] == "reset" {
		bus.TransitionWorkflow(session, bus.StateIdle, "manual:reset")
		fmt.Println("Workflow state reset to idle")
		return
	}

	entry := bus.ReadWorkflowState(session)

	jsonMode := false
	for _, a := range args {
		if a == "--json" {
			jsonMode = true
		}
	}

	if jsonMode {
		data, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling state: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}

	fmt.Println(bus.FormatWorkflowState(entry))
}
