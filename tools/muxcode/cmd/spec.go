package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Spec handles the "muxcode spec" subcommand.
// Usage: muxcode spec set <path|id>  — set the active requirements spec
//
//	muxcode spec get           — show the active spec
//	muxcode spec clear         — remove the active spec
//
// "set" takes a path, a tracking id in any spelling ("144", "mux-144",
// "MUX-144"), or a slug fragment ("gate"), resolved by ResolveSpecQuery.
// Every message that sends a user here names an id, so demanding a path
// made them go and look one up to retype what they had already typed.
func Spec(args []string) {
	session := bus.BusSession()

	if len(args) == 0 {
		// Default to "get"
		specPath := bus.ReadActiveSpec(session)
		if specPath == "" {
			fmt.Println("No active spec set")
			return
		}
		fmt.Println(specPath)
		return
	}

	switch args[0] {
	case "get":
		specPath := bus.ReadActiveSpec(session)
		if specPath == "" {
			fmt.Println("No active spec set")
			return
		}
		fmt.Println(specPath)

	case "set":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode spec set <path|id>")
			os.Exit(1)
		}
		specPath := args[1]

		if _, err := os.Stat(specPath); err != nil {
			// Only absence means "try it as an id"; an unreadable file is an error.
			if !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Error: cannot read %s: %v\n", specPath, err)
				os.Exit(1)
			}
			pick, rerr := bus.ResolveSpecQuery(session, specPath)
			if rerr != nil {
				fmt.Fprintf(os.Stderr, "Error: %s is neither a file nor a spec id: %v\n", specPath, rerr)
				os.Exit(1)
			}
			specPath = pick.Path
		}

		// Warn if not under docs/requirements/drafts/ (but allow it)
		if !strings.Contains(specPath, "docs/requirements/drafts/") {
			fmt.Fprintf(os.Stderr, "Warning: spec is not in docs/requirements/drafts/ — verification may not trigger correctly\n")
		}

		// Normalize to a relative path if absolute
		if filepath.IsAbs(specPath) {
			if cwd, err := os.Getwd(); err == nil {
				if rel, err := filepath.Rel(cwd, specPath); err == nil {
					specPath = rel
				}
			}
		}

		if err := bus.WriteActiveSpec(session, specPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Active spec set: %s\n", specPath)

	case "clear":
		if err := bus.ClearActiveSpec(session); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Active spec cleared")

	default:
		fmt.Fprintf(os.Stderr, "Unknown spec subcommand: %s\nUsage: muxcode spec [set <path> | get | clear]\n", args[0])
		os.Exit(1)
	}
}
