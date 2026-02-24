package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Session handles the "muxcode-agent-bus session" subcommand.
func Session(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus session <compact|resume|status> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "compact":
		sessionCompact(subArgs)
	case "resume":
		sessionResume(subArgs)
	case "status":
		sessionStatus()
	default:
		fmt.Fprintf(os.Stderr, "Unknown session subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus session <compact|resume|status> [args...]\n")
		os.Exit(1)
	}
}

func sessionCompact(args []string) {
	if len(args) < 1 || args[0] == "" {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus session compact \"<summary>\"\n")
		os.Exit(1)
	}

	summary := args[0]
	session := bus.BusSession()
	role := bus.BusRole()

	if err := bus.CompactSession(session, role, summary); err != nil {
		fmt.Fprintf(os.Stderr, "Error compacting session: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Session compacted for %s\n", role)
}

func sessionResume(args []string) {
	role := bus.BusRole()
	if len(args) > 0 {
		role = args[0]
	}

	content, err := bus.ResumeContext(role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resuming session: %v\n", err)
		os.Exit(1)
	}
	if content != "" {
		fmt.Print(content)
	}
}

func sessionStatus() {
	session := bus.BusSession()
	role := bus.BusRole()

	meta, err := bus.ReadSessionMeta(session, role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading session meta: %v\n", err)
		os.Exit(1)
	}

	if meta == nil {
		fmt.Println("Session: not started")
		return
	}

	// Count inbox messages for status display
	inboxPath := bus.InboxPath(session, role)
	msgCount := 0
	data, err := os.ReadFile(inboxPath)
	if err == nil {
		for _, line := range splitLines(data) {
			if len(line) > 0 {
				msgCount++
			}
		}
	}

	fmt.Print(bus.FormatSessionStatus(meta, role, msgCount))
}
