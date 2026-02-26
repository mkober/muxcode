package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Context handles the "muxcode-agent-bus context" subcommand.
func Context(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus context <list|prompt> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "list":
		contextList(subArgs)
	case "prompt":
		contextPrompt(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown context subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus context <list|prompt> [args...]\n")
		os.Exit(1)
	}
}

func contextList(args []string) {
	roleFilter := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --role requires a value\n")
				os.Exit(1)
			}
			i++
			roleFilter = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus context list [--role ROLE]\n")
			os.Exit(1)
		}
	}

	var files []bus.ContextFile
	var err error
	if roleFilter != "" {
		files, err = bus.ContextFilesForRole(roleFilter)
	} else {
		files, err = bus.ReadContextFiles()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing context files: %v\n", err)
		os.Exit(1)
	}

	if len(files) > 0 {
		fmt.Print(bus.FormatContextList(files))
	}
}

func contextPrompt(args []string) {
	role := bus.BusRole()
	if len(args) > 0 {
		role = args[0]
	}

	files, err := bus.ContextFilesForRole(role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading context for role %s: %v\n", role, err)
		os.Exit(1)
	}

	output := bus.FormatContextPrompt(files)
	if output != "" {
		fmt.Print(output)
	}
}
