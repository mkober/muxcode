package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Model handles the "muxcode model" subcommand.
// Usage: muxcode model <list|add|remove|default> [args...]
func Model(args []string) {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, modelUsage)
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		modelList()
	case "add":
		modelAdd(args[1:])
	case "remove":
		modelRemove(args[1:])
	case "default":
		modelDefault(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown model subcommand: %s\n\n", args[0])
		fmt.Fprint(os.Stderr, modelUsage)
		os.Exit(1)
	}
}

const modelUsage = `Usage: muxcode model <command> [args...]

Commands:
  list                                List configured models per provider
  add <model> [--provider P]          Add a model (default provider: claude)
  remove <model> [--provider P]       Remove a model
  default <model> [--provider P]      Set the default model for a provider

Examples:
  muxcode model list
  muxcode model add claude-haiku-4-5
  muxcode model add opencode-go/deepseek-v4-pro --provider opencode
  muxcode model remove claude-sonnet-4-0
  muxcode model default claude-opus-5
`

func modelList() {
	cfg, err := bus.LoadModelConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatModelList(cfg))
	fmt.Printf("Config: %s\n", bus.ResolveModelConfigPath())
}

func modelAdd(args []string) {
	provider := "claude"
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
		fmt.Fprintf(os.Stderr, "Error: model name required\nUsage: muxcode model add <model> [model...] [--provider P]\n")
		os.Exit(1)
	}

	cfg, err := bus.LoadModelConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	var added, skipped []string
	for _, name := range names {
		if bus.AddModel(cfg, provider, name) {
			added = append(added, name)
		} else {
			skipped = append(skipped, name)
		}
	}

	if err := bus.SaveModelConfig(cfg); err != nil {
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

func modelRemove(args []string) {
	provider := "claude"
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
		fmt.Fprintf(os.Stderr, "Error: model name required\nUsage: muxcode model remove <model> [model...] [--provider P]\n")
		os.Exit(1)
	}

	cfg, err := bus.LoadModelConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	var removed, notFound []string
	for _, name := range names {
		if bus.RemoveModel(cfg, provider, name) {
			removed = append(removed, name)
		} else {
			notFound = append(notFound, name)
		}
	}

	if err := bus.SaveModelConfig(cfg); err != nil {
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

func modelDefault(args []string) {
	provider := "claude"
	model := ""

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
			if model == "" {
				model = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "Unexpected argument: %s\n", args[i])
				os.Exit(1)
			}
		}
	}

	if model == "" {
		fmt.Fprintf(os.Stderr, "Error: model name required\nUsage: muxcode model default <model> [--provider P]\n")
		os.Exit(1)
	}

	cfg, err := bus.LoadModelConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if !bus.SetDefaultModel(cfg, provider, model) {
		fmt.Fprintf(os.Stderr, "Error: model %q not found in %s enabled models\n", model, provider)
		fmt.Fprintf(os.Stderr, "Hint: add it first with: muxcode model add %s --provider %s\n", model, provider)
		os.Exit(1)
	}

	if err := bus.SaveModelConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Default for %s: %s\n", provider, model)
}
