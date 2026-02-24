package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Demo handles the "muxcode-agent-bus demo" subcommand.
func Demo(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus demo <run|list> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "run":
		demoRun(subArgs)
	case "list":
		demoList(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown demo subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus demo <run|list> [args...]\n")
		os.Exit(1)
	}
}

// demoRun handles: demo run [SCENARIO] [--speed FACTOR] [--dry-run] [--no-switch]
func demoRun(args []string) {
	speed := 1.0
	dryRun := false
	noSwitch := false
	scenarioName := "build-test-review"

	var positionals []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--speed":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --speed requires a value\n")
				os.Exit(1)
			}
			i++
			v, err := strconv.ParseFloat(args[i], 64)
			if err != nil || v <= 0 {
				fmt.Fprintf(os.Stderr, "Error: --speed must be a positive number\n")
				os.Exit(1)
			}
			speed = v
		case "--dry-run":
			dryRun = true
		case "--no-switch":
			noSwitch = true
		default:
			if args[i][0] == '-' {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
				fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus demo run [SCENARIO] [--speed FACTOR] [--dry-run] [--no-switch]\n")
				os.Exit(1)
			}
			positionals = append(positionals, args[i])
		}
	}

	if len(positionals) > 0 {
		scenarioName = positionals[0]
	}

	scenario, err := bus.GetScenario(scenarioName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "Run 'muxcode-agent-bus demo list' to see available scenarios.\n")
		os.Exit(1)
	}

	session := bus.BusSession()

	opts := bus.DemoOptions{
		Speed:    speed,
		DryRun:   dryRun,
		NoSwitch: noSwitch,
	}

	if _, err := bus.RunDemo(session, scenario, opts); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// demoList handles: demo list
func demoList(args []string) {
	scenarios := bus.BuiltinScenarios()
	fmt.Print(bus.FormatScenarioList(scenarios))
}
