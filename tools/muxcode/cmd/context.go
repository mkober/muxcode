package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Context handles the "muxcode context" subcommand.
func Context(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode context <list|prompt|detect|init> [args...]\n")
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
	case "init":
		contextInit(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown context subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode context <list|prompt|detect|init> [args...]\n")
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
			fmt.Fprintf(os.Stderr, "Usage: muxcode context list [--role ROLE] [--no-auto]\n")
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

// contextInit runs session-start init on dir (default ".") for the current
// bus session — the launcher's own road, announcement included — and prints
// one "created <path>" or "skipped <path>: <reason>" line per file, or
// "nothing to create". Scripts parse these lines (scripts/test-xcode-compat.sh).
func contextInit(args []string) {
	if len(args) > 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode context init [dir]\n")
		os.Exit(1)
	}
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Error: not a directory: %s\n", absDir)
		os.Exit(1)
	}

	res := bus.InitSessionProject(bus.BusSession(), absDir)
	for _, p := range res.Created {
		fmt.Printf("created %s\n", p)
	}
	for _, s := range res.Skipped {
		fmt.Printf("skipped %s\n", s)
	}
	if len(res.Created) == 0 && len(res.Skipped) == 0 {
		fmt.Println("nothing to create")
	}
}
