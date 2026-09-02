package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Version handles "muxcode version" and the "--version"/"-v" flags.
// Usage: muxcode version [--json | --at-least vX.Y.Z]
//
// --at-least exits 0 when this binary is at or past the given version and 1
// when it is older. Usage errors and an uncomparable version — an untagged
// dev build reports a bare commit, which has no semver rank — exit 2 so a
// script can tell "too old" from "could not decide".
func Version(args []string) {
	jsonOutput := false
	atLeast := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--at-least":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Usage: muxcode version --at-least vX.Y.Z")
				os.Exit(2)
			}
			atLeast = args[i+1]
			i++
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\nUsage: muxcode version [--json | --at-least vX.Y.Z]\n", args[i])
			os.Exit(2)
		}
	}

	info := bus.BuildInfo()
	if atLeast != "" {
		os.Exit(atLeastExit(info.Version, atLeast, os.Stderr))
	}
	if jsonOutput {
		out, err := json.Marshal(info)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting JSON: %v\n", err)
			os.Exit(2)
		}
		fmt.Println(string(out))
		return
	}
	fmt.Println(info.String())
}

// atLeastExit is the --at-least exit code for a binary at version have,
// writing the reason for any non-zero result to w.
func atLeastExit(have, want string, w io.Writer) int {
	c, err := bus.CompareSemver(have, want)
	if err != nil {
		fmt.Fprintf(w, "muxcode version: cannot compare: %v\n", err)
		return 2
	}
	if c < 0 {
		fmt.Fprintf(w, "muxcode %s is older than required %s\n", have, want)
		return 1
	}
	return 0
}
