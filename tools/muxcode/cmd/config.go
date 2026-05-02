package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Config handles the "muxcode config" subcommand.
//
// Usage:
//
//	muxcode config set <role>.<field> <value> [--reload]
//	muxcode config get <role>
//	muxcode config list
//
// Subcommands:
//
//	set    Write a config value to the persistent config file
//	get    Show effective CLI, model, and resolution source for a role
//	list   Show all roles with their effective CLI and model
//
// Fields for set:
//
//	cli    CLI provider (claude, opencode, codex, local)
//	model  Model identifier (e.g. opencode-go/deepseek-v4-pro)
//
// Flags:
//
//	--reload  Trigger agent reload after writing config (set only)
//
// Examples:
//
//	muxcode config set build.cli opencode
//	muxcode config set build.model opencode-go/deepseek-v4-pro --reload
//	muxcode config get build
//	muxcode config list
func Config(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode config <set|get|list> [args...]")
		os.Exit(1)
	}

	switch args[0] {
	case "set":
		configSet(args[1:])
	case "get":
		configGet(args[1:])
	case "list":
		configList()
	default:
		fmt.Fprintf(os.Stderr, "Unknown config subcommand: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Usage: muxcode config <set|get|list> [args...]")
		os.Exit(1)
	}
}

// configSet handles "muxcode config set <role>.<field> <value> [--reload]".
func configSet(args []string) {
	reload := false
	var positional []string

	for i := 0; i < len(args); i++ {
		if args[i] == "--reload" {
			reload = true
		} else if strings.HasPrefix(args[i], "--") {
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		} else {
			positional = append(positional, args[i])
		}
	}

	if len(positional) != 2 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode config set <role>.<field> <value> [--reload]")
		fmt.Fprintln(os.Stderr, "  Fields: cli, model")
		fmt.Fprintln(os.Stderr, "  Example: muxcode config set build.cli opencode")
		os.Exit(1)
	}

	// Parse role.field
	parts := strings.SplitN(positional[0], ".", 2)
	if len(parts) != 2 {
		fmt.Fprintf(os.Stderr, "Invalid key format: %q (expected <role>.<field>)\n", positional[0])
		os.Exit(1)
	}
	role := parts[0]
	field := parts[1]
	value := positional[1]

	// Validate role
	if !bus.IsKnownRole(role) {
		fmt.Fprintf(os.Stderr, "Unknown role: %s\n", role)
		os.Exit(1)
	}

	// Map field to env var key
	var envKey string
	switch field {
	case "cli":
		envKey = bus.RoleCLIEnvVar(role)
	case "model":
		envKey = bus.RoleModelEnvVar(role)
	default:
		fmt.Fprintf(os.Stderr, "Unknown field: %q (expected cli or model)\n", field)
		os.Exit(1)
	}

	// Write to persistent config file
	if err := bus.SetShellConfigValue(envKey, value); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
		os.Exit(1)
	}

	configPath := bus.ResolveConfigPath()
	fmt.Printf("Set %s=%s in %s\n", envKey, value, configPath)

	// Optionally trigger reload
	if reload {
		session := bus.BusSession()
		if session == "" {
			fmt.Fprintln(os.Stderr, "Warning: BUS_SESSION not set, skipping reload")
			return
		}
		var cli, model string
		if field == "cli" {
			cli = value
		} else {
			model = value
		}
		if err := bus.ReloadAgent(session, role, cli, model, false); err != nil {
			fmt.Fprintf(os.Stderr, "Config saved but reload failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ %s reloaded\n", role)
	}
}

// configGet handles "muxcode config get <role>".
func configGet(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode config get <role>")
		os.Exit(1)
	}
	role := args[0]

	if !bus.IsKnownRole(role) {
		fmt.Fprintf(os.Stderr, "Unknown role: %s\n", role)
		os.Exit(1)
	}

	rc := bus.EffectiveConfig(role)

	fmt.Printf("=== %s ===\n", rc.Role)
	fmt.Printf("CLI:     %-30s (%s)\n", rc.CLI, rc.CLISource)
	fmt.Printf("Model:   %-30s (%s)\n", rc.Model, rc.ModelSrc)

	// Show built-in defaults for comparison when overridden
	defaultCLI := bus.ResolveProviderCLI(role)
	var defaultModel string
	switch defaultCLI {
	case "opencode":
		defaultModel = bus.RoleOpenCodeModelDefault(role)
	case "codex":
		defaultModel = bus.RoleCodexModelDefault(role)
	default:
		defaultModel = bus.RoleClaudeModelDefault(role)
	}
	if rc.CLISource != "default" || rc.ModelSrc != "default" {
		fmt.Printf("Default: %s / %s\n", defaultCLI, defaultModel)
	}
}

// configList handles "muxcode config list".
func configList() {
	fmt.Printf("%-12s %-12s %s\n", "ROLE", "CLI", "MODEL")
	fmt.Printf("%-12s %-12s %s\n", "----", "---", "-----")

	for _, role := range bus.KnownRoles {
		// Skip hosted roles that share a window with their host
		// (e.g. docs→plan, pr-read→commit)
		if bus.WindowForRole(role) != role {
			continue
		}
		rc := bus.EffectiveConfig(role)
		fmt.Printf("%-12s %-12s %s\n", rc.Role, rc.CLI, rc.Model)
	}
}
