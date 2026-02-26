package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Context handles the "muxcode-agent-bus context" subcommand.
func Context(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus context <list|prompt|detect> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "list":
		contextList(subArgs)
	case "prompt":
		contextPrompt(subArgs)
	case "detect":
		contextDetect(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown context subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus context <list|prompt|detect> [args...]\n")
		os.Exit(1)
	}
}

func contextList(args []string) {
	roleFilter := ""
	noAuto := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --role requires a value\n")
				os.Exit(1)
			}
			i++
			roleFilter = args[i]
		case "--no-auto":
			noAuto = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus context list [--role ROLE] [--no-auto]\n")
			os.Exit(1)
		}
	}

	var files []bus.ContextFile
	var err error

	if noAuto {
		// Manual files only (original behavior)
		if roleFilter != "" {
			files, err = bus.ContextFilesForRole(roleFilter)
		} else {
			files, err = bus.ReadContextFiles()
		}
	} else {
		// Manual + auto-detected (new default)
		if roleFilter != "" {
			files, err = bus.AllContextFilesForRole(roleFilter)
		} else {
			files, err = bus.ReadAllContextFiles()
		}
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
	noAuto := false

	// Parse args: first positional is role, flags anywhere
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--no-auto" {
			noAuto = true
		} else {
			positional = append(positional, args[i])
		}
	}
	if len(positional) > 0 {
		role = positional[0]
	}

	var files []bus.ContextFile
	var err error
	if noAuto {
		files, err = bus.ContextFilesForRole(role)
	} else {
		files, err = bus.AllContextFilesForRole(role)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading context for role %s: %v\n", role, err)
		os.Exit(1)
	}

	output := bus.FormatContextPrompt(files)
	if output != "" {
		fmt.Print(output)
	}
}

func contextDetect(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	// Resolve to absolute path for cleaner output
	absDir, err := os.Getwd()
	if err == nil && dir == "." {
		dir = absDir
	}

	types := bus.DetectProject(dir)
	fmt.Print(bus.FormatDetectOutput(types))
}
