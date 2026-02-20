package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcoder/tools/muxcoder-agent-bus/bus"
)

// Memory handles the "muxcoder-agent-bus memory" subcommand.
func Memory(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcoder-agent-bus memory <read|write|write-shared|context> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "read":
		memoryRead(subArgs)
	case "write":
		memoryWrite(subArgs)
	case "write-shared":
		memoryWriteShared(subArgs)
	case "context":
		memoryContext()
	default:
		fmt.Fprintf(os.Stderr, "Unknown memory subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcoder-agent-bus memory <read|write|write-shared|context> [args...]\n")
		os.Exit(1)
	}
}

func memoryRead(args []string) {
	role := "shared"
	if len(args) > 0 {
		role = args[0]
	}

	content, err := bus.ReadMemory(role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading memory: %v\n", err)
		os.Exit(1)
	}
	if content != "" {
		fmt.Print(content)
	}
}

func memoryWrite(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcoder-agent-bus memory write \"<section>\" \"<text>\"\n")
		os.Exit(1)
	}

	section := args[0]
	text := args[1]
	role := bus.BusRole()

	if err := bus.AppendMemory(section, text, role); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing memory: %v\n", err)
		os.Exit(1)
	}
}

func memoryWriteShared(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcoder-agent-bus memory write-shared \"<section>\" \"<text>\"\n")
		os.Exit(1)
	}

	section := args[0]
	text := args[1]

	if err := bus.AppendMemory(section, text, "shared"); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing shared memory: %v\n", err)
		os.Exit(1)
	}
}

func memoryContext() {
	role := bus.BusRole()
	content, err := bus.ReadContext(role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading context: %v\n", err)
		os.Exit(1)
	}
	if content != "" {
		fmt.Print(content)
	}
}
