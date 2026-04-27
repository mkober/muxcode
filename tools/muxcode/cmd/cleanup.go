package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Cleanup handles the "muxcode cleanup" subcommand.
// Usage: muxcode cleanup [--dry-run] [--all] [--age N] [--claude] [session]
//
// Without flags, removes stale muxcode session artifacts (bus dirs, preview
// files, trigger files, spawn dirs, log files) for sessions whose tmux
// session no longer exists. Skips the current session.
//
// Flags:
//
//	--dry-run  Preview what would be removed without deleting
//	--all      Include the current session's artifacts
//	--claude   Also clean stale Claude Code /tmp/claude-* session dirs
//	--age N    Max age in days for Claude Code sessions (default 7)
func Cleanup(args []string) {
	dryRun := false
	includeActive := false
	cleanClaude := false
	ageDays := 7
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--all":
			includeActive = true
		case "--claude":
			cleanClaude = true
		case "--age":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n >= 0 {
					ageDays = n
				} else {
					fmt.Fprintf(os.Stderr, "Invalid --age value: %s (must be a non-negative integer)\n", args[i])
					os.Exit(1)
				}
			}
		default:
			positional = append(positional, args[i])
		}
	}

	session := ""
	if len(positional) > 0 {
		session = positional[0]
	}
	if session == "" {
		session = bus.BusSession()
	}

	// Muxcode session cleanup
	result, err := bus.CleanupStale(session, dryRun, includeActive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(bus.FormatCleanupResult(result, dryRun))

	// Claude Code /tmp cleanup
	if cleanClaude {
		maxAge := time.Duration(ageDays) * 24 * time.Hour
		claudeResult, err := bus.CleanupClaudeTmp(maxAge, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Claude cleanup error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println(bus.FormatClaudeCleanupResult(claudeResult, dryRun))
	}
}
