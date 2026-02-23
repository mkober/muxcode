package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Memory handles the "muxcode-agent-bus memory" subcommand.
func Memory(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus memory <read|write|write-shared|context|search|list> [args...]\n")
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
	case "search":
		memorySearch(subArgs)
	case "list":
		memoryList(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown memory subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus memory <read|write|write-shared|context|search|list> [args...]\n")
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
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus memory write \"<section>\" \"<text>\"\n")
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
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus memory write-shared \"<section>\" \"<text>\"\n")
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

func memorySearch(args []string) {
	// Collect positional words as the query, parse --role and --limit flags
	var queryParts []string
	roleFilter := ""
	limit := 0

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --role requires a value\n")
				os.Exit(1)
			}
			i++
			roleFilter = args[i]
		case "--limit":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --limit requires a value\n")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: --limit must be a number\n")
				os.Exit(1)
			}
			limit = n
		default:
			queryParts = append(queryParts, args[i])
		}
	}

	query := strings.Join(queryParts, " ")
	if query == "" {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus memory search <query> [--role ROLE] [--limit N]\n")
		os.Exit(1)
	}

	results, err := bus.SearchMemory(query, roleFilter, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error searching memory: %v\n", err)
		os.Exit(1)
	}

	if len(results) > 0 {
		fmt.Print(bus.FormatSearchResults(results))
	}
}

func memoryList(args []string) {
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
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus memory list [--role ROLE]\n")
			os.Exit(1)
		}
	}

	entries, err := bus.AllMemoryEntries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing memory: %v\n", err)
		os.Exit(1)
	}

	if roleFilter != "" {
		var filtered []bus.MemoryEntry
		for _, e := range entries {
			if e.Role == roleFilter {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if len(entries) > 0 {
		fmt.Print(bus.FormatMemoryList(entries))
	}
}
