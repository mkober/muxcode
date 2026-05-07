package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Launch handles the "muxcode agent launch <role>" subcommand.
// Delegates to bus.RunAgentLaunch which performs the complete agent bootstrap:
// config loading, provider resolution, pre-launch setup, venv activation,
// and exec into the agent CLI.
func Launch(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode agent launch <role>\n")
		os.Exit(1)
	}

	if err := bus.RunAgentLaunch(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
