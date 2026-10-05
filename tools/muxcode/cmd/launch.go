package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

const launchUsage = "Usage: muxcode agent launch <role> [--reason <reason>] [--resume [<session-id>]]\n"

// Launch handles the "muxcode agent launch <role> [--reason <reason>]
// [--resume [<session-id>]]" subcommand. Delegates to bus.RunAgentLaunchResume
// which performs the complete agent bootstrap: config loading, provider
// resolution, pre-launch setup, venv activation, and exec into the agent CLI.
// --resume continues a Claude Code session with the full launch flag set;
// without an id it is found from the pane's exit banner, then — in a spawn
// worktree the agent owns alone — the cwd's transcript (bus.FindResumeID),
// else the launch is fresh. Other providers
// launch fresh. --reason says why the agent is launching
// (bus.LaunchReason); omitted, the launch is treated as a restart.
func Launch(args []string) {
	role, resumeID, reason, err := bus.ParseLaunchArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n%s", err, launchUsage)
		os.Exit(1)
	}

	if err := bus.RunAgentLaunchResume(role, resumeID, reason); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
