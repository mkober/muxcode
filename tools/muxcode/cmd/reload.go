package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Reload handles the "muxcode reload" subcommand.
//
// Usage:
//
//	muxcode reload <role> [--cli <cli>] [--model <model>] [--compact]
//	muxcode reload --all [--compact]
//
// Stops a single agent (or all active agents), re-resolves configuration
// with optional CLI/model overrides, regenerates provider-specific files,
// and relaunches — preserving inbox, memory, and bus identity.
//
// Flags:
//
//	--cli <cli>      CLI provider override (claude, opencode, codex, local)
//	--model <model>  Model override (e.g. opencode-go/deepseek-v4-pro)
//	--compact        Compact agent context before stopping
//	--all            Reload all active agents sequentially (excludes edit)
//
// Examples:
//
//	muxcode reload build
//	muxcode reload build --cli opencode --model opencode-go/deepseek-v4-pro
//	muxcode reload edit --model claude-opus-4-6 --compact
//	muxcode reload --all
//	muxcode reload --all --compact
func Reload(args []string) {
	var cli, model string
	compact := false
	all := false
	role := ""

	// Manual flag parsing to match the rest of the cmd package style
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cli":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --cli requires a value")
				os.Exit(1)
			}
			i++
			cli = args[i]
		case "--model":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --model requires a value")
				os.Exit(1)
			}
			i++
			model = args[i]
		case "--compact":
			compact = true
		case "--all":
			all = true
		default:
			if strings.HasPrefix(args[i], "--") {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
				os.Exit(1)
			}
			if role == "" {
				role = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "Unexpected argument: %s\n", args[i])
				os.Exit(1)
			}
		}
	}

	// Validate: --all and role are mutually exclusive
	if all && role != "" {
		fmt.Fprintln(os.Stderr, "Error: --all cannot be used with a specific role")
		os.Exit(1)
	}
	if all && (cli != "" || model != "") {
		fmt.Fprintln(os.Stderr, "Error: --cli and --model cannot be used with --all")
		os.Exit(1)
	}
	if !all && role == "" {
		fmt.Fprintln(os.Stderr, "Usage: muxcode reload <role> [--cli <cli>] [--model <model>] [--compact]")
		fmt.Fprintln(os.Stderr, "       muxcode reload --all [--compact]")
		os.Exit(1)
	}

	session := bus.BusSession()
	if session == "" {
		fmt.Fprintln(os.Stderr, "Error: BUS_SESSION not set")
		os.Exit(1)
	}

	if all {
		reloaded, errs := bus.ReloadAll(session, compact)
		fmt.Printf("\n✓ %d agents reloaded", reloaded)
		if len(errs) > 0 {
			fmt.Printf(", %d failed", len(errs))
		}
		fmt.Println()
		if len(errs) > 0 {
			os.Exit(1)
		}
		return
	}

	// Single role reload
	if err := bus.ReloadAgent(session, role, cli, model, compact); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print summary
	newCLI := bus.ResolveProviderCLI(role)
	parts := []string{fmt.Sprintf("✓ %s reloaded", role)}
	if cli != "" {
		parts = append(parts, fmt.Sprintf("CLI: %s", newCLI))
	}
	if model != "" {
		parts = append(parts, fmt.Sprintf("Model: %s", model))
	}
	fmt.Println(strings.Join(parts, " — "))
}
