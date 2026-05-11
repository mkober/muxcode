package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Plugin handles the "muxcode plugin" subcommand.
// Usage: muxcode plugin <list|add|remove|sync> [args...]
func Plugin(args []string) {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, pluginUsage)
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		pluginList()
	case "add":
		pluginAdd(args[1:])
	case "remove":
		pluginRemove(args[1:])
	case "sync":
		pluginSync(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown plugin subcommand: %s\n\n", args[0])
		fmt.Fprint(os.Stderr, pluginUsage)
		os.Exit(1)
	}
}

const pluginUsage = `Usage: muxcode plugin <command> [args...]

Commands:
  list                        List configured plugins
  add <name> [--provider P]   Add a plugin (default provider: claude-code)
  remove <name> [--provider P]  Remove a plugin
  sync [--provider P]         Sync plugins to provider settings

Examples:
  muxcode plugin add atlassian
  muxcode plugin add github
  muxcode plugin remove sentry
  muxcode plugin sync
  muxcode plugin list
`

func pluginList() {
	cfg, err := bus.LoadPluginConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatPluginList(cfg))
	fmt.Printf("Config: %s\n", bus.ResolvePluginConfigPath())
}

func pluginAdd(args []string) {
	provider := "claude-code"
	var names []string

	for i := 0; i < len(args); i++ {
		if args[i] == "--provider" {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --provider requires a value\n")
				os.Exit(1)
			}
			i++
			provider = args[i]
		} else if strings.HasPrefix(args[i], "--") {
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		} else {
			names = append(names, args[i])
		}
	}

	if len(names) == 0 {
		fmt.Fprintf(os.Stderr, "Error: plugin name required\nUsage: muxcode plugin add <name> [name...] [--provider P]\n")
		os.Exit(1)
	}

	cfg, err := bus.LoadPluginConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	var added, skipped []string
	for _, name := range names {
		if bus.AddPlugin(cfg, provider, name) {
			added = append(added, name)
		} else {
			skipped = append(skipped, name)
		}
	}

	if err := bus.SavePluginConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	if len(added) > 0 {
		fmt.Printf("Added to %s: %s\n", provider, strings.Join(added, ", "))
	}
	if len(skipped) > 0 {
		fmt.Printf("Already configured: %s\n", strings.Join(skipped, ", "))
	}
}

func pluginRemove(args []string) {
	provider := "claude-code"
	var names []string

	for i := 0; i < len(args); i++ {
		if args[i] == "--provider" {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --provider requires a value\n")
				os.Exit(1)
			}
			i++
			provider = args[i]
		} else if strings.HasPrefix(args[i], "--") {
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		} else {
			names = append(names, args[i])
		}
	}

	if len(names) == 0 {
		fmt.Fprintf(os.Stderr, "Error: plugin name required\nUsage: muxcode plugin remove <name> [name...] [--provider P]\n")
		os.Exit(1)
	}

	cfg, err := bus.LoadPluginConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	var removed, notFound []string
	for _, name := range names {
		if bus.RemovePlugin(cfg, provider, name) {
			removed = append(removed, name)
		} else {
			notFound = append(notFound, name)
		}
	}

	if err := bus.SavePluginConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	if len(removed) > 0 {
		fmt.Printf("Removed from %s: %s\n", provider, strings.Join(removed, ", "))
	}
	if len(notFound) > 0 {
		fmt.Printf("Not found: %s\n", strings.Join(notFound, ", "))
	}
}

func pluginSync(args []string) {
	provider := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--provider" {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --provider requires a value\n")
				os.Exit(1)
			}
			i++
			provider = args[i]
		}
	}

	cfg, err := bus.LoadPluginConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if provider != "" {
		// Sync single provider
		switch provider {
		case "claude-code":
			r, err := bus.SyncClaudeCodePlugins(cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(bus.FormatSyncResult(r))
		default:
			fmt.Fprintf(os.Stderr, "Unsupported provider: %s (supported: claude-code)\n", provider)
			os.Exit(1)
		}
		return
	}

	// Sync all providers
	results, err := bus.SyncAllPlugins(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(results) == 0 {
		fmt.Println("No providers configured. Run: muxcode plugin add <name>")
		return
	}
	for _, r := range results {
		fmt.Print(bus.FormatSyncResult(r))
	}
}
