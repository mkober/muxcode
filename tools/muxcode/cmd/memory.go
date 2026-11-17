package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Memory handles the "muxcode memory" subcommand.
func Memory(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode memory <read|write|write-shared|write-global|context|search|list|stranded> [args...]\n")
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
	case "write-global":
		memoryWriteGlobal(subArgs)
	case "context":
		memoryContext(subArgs)
	case "search":
		memorySearch(subArgs)
	case "list":
		memoryList(subArgs)
	case "stranded":
		memoryStranded(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown memory subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode memory <read|write|write-shared|write-global|context|search|list|stranded> [args...]\n")
		os.Exit(1)
	}
}

// memoryStranded lists `.muxcode/memory` directories stranded below the
// checkout root by a pre-MUX-223 muxcode; --merge merges them entry by entry
// into the project memory and keeps each original (bus.MergeStrandedMemory).
// Exit 0 when the listing or merge completed, 1 on error.
func memoryStranded(args []string) {
	merge := false
	for _, a := range args {
		if a != "--merge" {
			fmt.Fprintf(os.Stderr, "Usage: muxcode memory stranded [--merge]\n")
			os.Exit(1)
		}
		merge = true
	}
	memDir, err := bus.ProjectMemoryDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	root, ok := bus.MainCheckoutRoot(wd)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: not inside a git checkout — nothing to scan for stranded memory\n")
		os.Exit(1)
	}
	stranded, err := bus.FindStrandedMemory(root, memDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Project memory: %s\n", memDir)
	if len(stranded) == 0 {
		fmt.Println("No stranded memory directories.")
		return
	}
	for _, s := range stranded {
		fmt.Printf("stranded: %s (%s)\n", s.Rel, strings.Join(s.Files, ", "))
	}
	if !merge {
		fmt.Println("Run `muxcode memory stranded --merge` to merge them; originals are kept.")
		return
	}
	results, err := bus.MergeStrandedMemory(memDir, stranded, time.Now())
	for _, r := range results {
		for _, f := range r.Files {
			line := fmt.Sprintf("merged %s/%s: +%d entries, %d duplicates skipped", r.Rel, f.File, f.Added, f.Duplicates)
			if f.Backup != "" {
				line += ", backup " + f.Backup
			}
			fmt.Println(line)
		}
		fmt.Printf("kept original %s at %s\n", r.Rel, r.KeptAt)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "Usage: muxcode memory write \"<section>\" \"<text>\"\n")
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
		fmt.Fprintf(os.Stderr, "Usage: muxcode memory write-shared \"<section>\" \"<text>\"\n")
		os.Exit(1)
	}

	section := args[0]
	text := args[1]

	if err := bus.AppendMemory(section, text, "shared"); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing shared memory: %v\n", err)
		os.Exit(1)
	}
}

func memoryWriteGlobal(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode memory write-global \"<section>\" \"<text>\"\n")
		os.Exit(1)
	}
	section := args[0]
	text := args[1]
	role := bus.BusRole()
	// Default to shared if role is unknown
	if role == "unknown" {
		role = "shared"
	}
	if err := bus.AppendGlobalMemory(section, text, role); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing global memory: %v\n", err)
		os.Exit(1)
	}
}

func memoryContext(args []string) {
	role := bus.BusRole()
	days := bus.DefaultRotationConfig().ContextDays
	includeGlobal := true

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--days":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --days requires a value\n")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: --days must be a number\n")
				os.Exit(1)
			}
			days = n
		case "--no-global":
			includeGlobal = false
		}
	}

	content, err := bus.ReadContextFull(role, days, includeGlobal)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading context: %v\n", err)
		os.Exit(1)
	}
	if content != "" {
		fmt.Print(content)
	}
}

func memorySearch(args []string) {
	// Collect positional words as the query, parse --role, --limit, --mode flags
	var queryParts []string
	roleFilter := ""
	limit := 0
	mode := bus.SearchModeBM25 // default to BM25

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
		case "--mode":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --mode requires a value (keyword|bm25)\n")
				os.Exit(1)
			}
			i++
			switch args[i] {
			case "keyword":
				mode = bus.SearchModeKeyword
			case "bm25":
				mode = bus.SearchModeBM25
			default:
				fmt.Fprintf(os.Stderr, "Error: --mode must be 'keyword' or 'bm25'\n")
				os.Exit(1)
			}
		default:
			queryParts = append(queryParts, args[i])
		}
	}

	query := strings.Join(queryParts, " ")
	if query == "" {
		fmt.Fprintf(os.Stderr, "Usage: muxcode memory search <query> [--role ROLE] [--limit N] [--mode keyword|bm25]\n")
		os.Exit(1)
	}

	results, err := bus.SearchMemoryWithOptions(bus.SearchOptions{
		Query:      query,
		RoleFilter: roleFilter,
		Limit:      limit,
		Mode:       mode,
	})
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
			fmt.Fprintf(os.Stderr, "Usage: muxcode memory list [--role ROLE]\n")
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
