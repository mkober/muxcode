package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Modal handles the "muxcode modal" subcommand.
func Modal(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode modal <open|list|status> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "open":
		modalOpen(subArgs)
	case "list":
		modalList(subArgs)
	case "status":
		modalStatus(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown modal subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode modal <open|list|status> [args...]\n")
		os.Exit(1)
	}
}

// modalOpen handles: modal open <name> [--size WxH|preset] [--dry-run]
func modalOpen(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode modal open <name> [--size WxH|preset] [--dry-run]\n")
		os.Exit(1)
	}

	var name string
	var sizeFlag string
	var dryRun bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--size":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --size requires a value\n")
				os.Exit(1)
			}
			i++
			sizeFlag = args[i]
		default:
			if name == "" {
				name = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "Unknown argument: %s\n", args[i])
				os.Exit(1)
			}
		}
	}

	if name == "" {
		fmt.Fprintf(os.Stderr, "Error: modal name is required\n")
		fmt.Fprintf(os.Stderr, "Usage: muxcode modal open <name> [--size WxH|preset]\n")
		os.Exit(1)
	}

	session := bus.BusSession()

	// Mirrors `popup --dry-run`: emit the arguments that would reach tmux, one
	// per line, so the size a modal resolves to can be asserted without opening
	// it. Modals had no dry-run, which is why the size tests could only ever
	// cover popups and the provider modal shipped unmeasured.
	if dryRun {
		cfg, ok := bus.GetModal(name)
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown modal: %s\n", name)
			os.Exit(1)
		}
		for _, a := range bus.BuildPopupArgs(cfg, session, sizeFlag) {
			fmt.Println(a)
		}
		return
	}

	if err := bus.OpenModal(session, name, sizeFlag); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// modalList handles: modal list
func modalList(args []string) {
	configs := bus.ListModals()
	fmt.Print(bus.FormatModalList(configs))
}

// modalStatus handles: modal status <name>
func modalStatus(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode modal status <name>\n")
		os.Exit(1)
	}

	name := args[0]
	cfg, ok := bus.GetModal(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: unknown modal: %s\n", name)
		os.Exit(1)
	}

	session := bus.BusSession()
	fmt.Print(bus.FormatModalStatus(session, cfg))
}
