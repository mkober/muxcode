package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Reload handles the "muxcode reload" subcommand.
//
// Usage:
//
//	muxcode reload <role> [--cli <cli>] [--model <model>] [--compact]
//	muxcode reload <role1> <role2> ... [--cli <cli>] [--model <model>] [--compact]
//	muxcode reload --all [--cli <cli>] [--model <model>] [--compact] [--provider <cli>]
//
// Stops one or more agents, re-resolves configuration with optional CLI/model
// overrides, regenerates provider-specific files, and relaunches — preserving
// inbox, memory, and bus identity.
//
// Flags:
//
//	--cli <cli>          CLI provider override (claude, opencode, codex, local)
//	--model <model>      Model override (e.g. opencode-go/deepseek-v4-pro)
//	--compact            Compact agent context before stopping
//	--all                Reload all active agents sequentially (excludes edit/auto)
//	--provider <cli>     Only reload agents currently on this CLI (requires --all)
//
// Examples:
//
//	muxcode reload build
//	muxcode reload build --cli opencode --model opencode-go/deepseek-v4-pro
//	muxcode reload build test review --cli opencode --model opencode-go/minimax-m3
//	muxcode reload --all --cli opencode --model opencode-go/minimax-m3
//	muxcode reload --all --provider claude --cli opencode --model opencode-go/minimax-m3
func Reload(args []string) {
	var cli, model, providerFilter string
	compact := false
	all := false
	var roles []string

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
		case "--provider":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --provider requires a value")
				os.Exit(1)
			}
			i++
			providerFilter = args[i]
		case "--compact":
			compact = true
		case "--all":
			all = true
		default:
			if strings.HasPrefix(args[i], "--") {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
				os.Exit(1)
			}
			roles = append(roles, args[i])
		}
	}

	// Validate: --all and roles are mutually exclusive
	if all && len(roles) > 0 {
		fmt.Fprintln(os.Stderr, "Error: --all cannot be used with specific roles")
		os.Exit(1)
	}
	if providerFilter != "" && !all {
		fmt.Fprintln(os.Stderr, "Error: --provider requires --all")
		os.Exit(1)
	}
	if !all && len(roles) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode reload <role> [<role>...] [--cli <cli>] [--model <model>] [--compact]")
		fmt.Fprintln(os.Stderr, "       muxcode reload --all [--cli <cli>] [--model <model>] [--compact] [--provider <cli>]")
		os.Exit(1)
	}

	session := bus.BusSession()
	if session == "" {
		fmt.Fprintln(os.Stderr, "Error: BUS_SESSION not set")
		os.Exit(1)
	}

	if all {
		fmt.Printf("Reloading all agents")
		if providerFilter != "" {
			fmt.Printf(" (current provider: %s)", providerFilter)
		}
		if cli != "" {
			fmt.Printf(" → %s", cli)
		}
		if model != "" {
			fmt.Printf(" / %s", bus.AbbreviateModel(model))
		}
		fmt.Println()

		reloaded, errs := bus.ReloadAll(session, cli, model, providerFilter, compact)
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

	// Single or multi-role reload
	if len(roles) == 1 {
		// Single role — use ReloadAgent directly (existing behavior)
		role := roles[0]
		if err := bus.ReloadAgent(session, role, cli, model, compact); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		newCLI := bus.ResolveProviderCLI(role)
		parts := []string{fmt.Sprintf("✓ %s reloaded", role)}
		if cli != "" {
			parts = append(parts, fmt.Sprintf("CLI: %s", newCLI))
		}
		if model != "" {
			parts = append(parts, fmt.Sprintf("Model: %s", model))
		}
		fmt.Println(strings.Join(parts, " — "))
		return
	}

	// Multi-role batch reload
	fmt.Printf("Reloading %d agents", len(roles))
	if cli != "" {
		fmt.Printf(" → %s", cli)
	}
	if model != "" {
		fmt.Printf(" / %s", bus.AbbreviateModel(model))
	}
	fmt.Println()

	results := bus.ReloadBatch(session, roles, cli, model, compact, func(i int, r bus.ReloadResult) {
		if r.Success {
			fmt.Printf("  ✓ %-10s %s → %s  (%s)\n", r.Role, r.OldCLI, r.NewCLI, r.Duration.Round(time.Second))
		} else {
			fmt.Printf("  ✗ %-10s %v\n", r.Role, r.Error)
		}
	})

	succeeded := 0
	failed := 0
	for _, r := range results {
		if r.Success {
			succeeded++
		} else {
			failed++
		}
	}

	fmt.Printf("\n✓ %d/%d agents reloaded successfully", succeeded, len(results))
	if cli != "" || model != "" {
		fmt.Printf(" (%s / %s)", cli, bus.AbbreviateModel(model))
	}
	fmt.Println()
	if failed > 0 {
		os.Exit(1)
	}
}
