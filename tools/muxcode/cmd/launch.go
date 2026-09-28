package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

const launchUsage = "Usage: muxcode agent launch <role> [--resume <session-id>]\n"

// Launch handles the "muxcode agent launch <role> [--resume <session-id>]"
// subcommand. Delegates to bus.RunAgentLaunchResume which performs the complete
// agent bootstrap: config loading, provider resolution, pre-launch setup, venv
// activation, and exec into the agent CLI. --resume continues a Claude Code
// session with the full launch flag set; other providers launch fresh.
func Launch(args []string) {
	role, resumeID := "", ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--resume":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --resume requires a session id\n")
				os.Exit(1)
			}
			i++
			resumeID = args[i]
		case role == "" && len(args[i]) > 0 && args[i][0] != '-':
			role = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown argument: %s\n%s", args[i], launchUsage)
			os.Exit(1)
		}
	}
	if role == "" {
		fmt.Fprint(os.Stderr, launchUsage)
		os.Exit(1)
	}

	if err := bus.RunAgentLaunchResume(role, resumeID); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
