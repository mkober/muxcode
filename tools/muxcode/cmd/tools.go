package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Tools handles the "muxcode tools" subcommand.
// Usage: muxcode tools <role> [--json]
func Tools(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode tools <role> [--json]\n")
		os.Exit(1)
	}

	role := args[0]
	asJSON := false
	bashTimeout := false
	for _, a := range args[1:] {
		switch a {
		case "--json":
			asJSON = true
		case "--bash-timeout":
			bashTimeout = true
		}
	}

	if bashTimeout {
		t := bus.RoleBashTimeout(role)
		fmt.Println(t)
		return
	}

	tools := bus.ResolveTools(role)
	if tools == nil {
		// No profile for this role — silent exit (bash caller checks for empty)
		return
	}

	if asJSON {
		data, err := json.Marshal(tools)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	} else {
		fmt.Println(strings.Join(tools, "\n"))
	}
}
