package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Guard handles the "muxcode-agent-bus guard" subcommand.
// Usage: muxcode-agent-bus guard [role] [--json] [--threshold N] [--window N]
func Guard(args []string) {
	role := ""
	jsonOutput := false
	threshold := 0 // 0 means use defaults (3 for commands, 4 for messages)
	windowSecs := int64(300)

	remaining := args
	for i := 0; i < len(remaining); i++ {
		switch remaining[i] {
		case "--json":
			jsonOutput = true
		case "--threshold":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --threshold requires a value\n")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(remaining[i])
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "Error: --threshold must be a positive integer\n")
				os.Exit(1)
			}
			threshold = n
		case "--window":
			if i+1 >= len(remaining) {
				fmt.Fprintf(os.Stderr, "Error: --window requires a value\n")
				os.Exit(1)
			}
			i++
			n, err := strconv.ParseInt(remaining[i], 10, 64)
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "Error: --window must be a positive integer (seconds)\n")
				os.Exit(1)
			}
			windowSecs = n
		default:
			if remaining[i][0] == '-' {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", remaining[i])
				fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus guard [role] [--json] [--threshold N] [--window N]\n")
				os.Exit(1)
			}
			role = remaining[i]
		}
	}

	session := bus.BusSession()

	var alerts []bus.LoopAlert
	if role != "" {
		alerts = checkRole(session, role, threshold, windowSecs)
	} else {
		for _, r := range bus.KnownRoles {
			alerts = append(alerts, checkRole(session, r, threshold, windowSecs)...)
		}
	}

	if jsonOutput {
		out, err := bus.FormatAlertsJSON(alerts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(out)
	} else {
		fmt.Print(bus.FormatAlerts(alerts))
	}

	if len(alerts) > 0 {
		os.Exit(1)
	}
}

// checkRole runs loop detection for a single role with optional threshold overrides.
func checkRole(session, role string, threshold int, windowSecs int64) []bus.LoopAlert {
	var alerts []bus.LoopAlert

	// Command loop detection
	cmdThreshold := 3
	if threshold > 0 {
		cmdThreshold = threshold
	}
	entries := bus.ReadHistory(session, role, 20)
	if alert := bus.DetectCommandLoop(entries, cmdThreshold, windowSecs); alert != nil {
		alert.Role = role
		alerts = append(alerts, *alert)
	}

	// Message loop detection
	msgThreshold := 4
	if threshold > 0 {
		msgThreshold = threshold
	}
	messages := bus.ReadLogHistory(session, role, 50)
	if alert := bus.DetectMessageLoop(messages, role, msgThreshold, windowSecs); alert != nil {
		alert.Role = role
		alerts = append(alerts, *alert)
	}

	return alerts
}
