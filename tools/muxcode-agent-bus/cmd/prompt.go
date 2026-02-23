package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Prompt handles the "muxcode-agent-bus prompt" subcommand.
// Usage: muxcode-agent-bus prompt <role>
//
// Outputs the shared agent coordination prompt for the given role.
func Prompt(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus prompt <role>\n")
		os.Exit(1)
	}

	role := args[0]
	fmt.Print(bus.SharedPrompt(role))
}
