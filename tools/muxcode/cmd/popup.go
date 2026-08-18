package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Popup handles `muxcode popup <name> [arg...]`, opening a tmux popup whose
// size is resolved from its content rather than hard-coded as a percentage.
func Popup(args []string) {
	var sizeFlag string
	var dryRun bool
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--size" && i+1 < len(args):
			sizeFlag = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--size="):
			sizeFlag = strings.TrimPrefix(args[i], "--size=")
		case args[i] == "--dry-run":
			dryRun = true
		default:
			rest = append(rest, args[i])
		}
	}

	if len(rest) == 0 {
		fmt.Println("Usage: muxcode popup <name> [arg...] [--size WxH] [--dry-run]")
		fmt.Println("\nRegistered popups:")
		for _, name := range bus.PopupNames() {
			cfg, _ := bus.GetPopup(name)
			tier := "cap"
			if cfg.Measurer != nil {
				tier = "fit"
			}
			fmt.Printf("  %-18s %-22s [%s]\n", name, strings.TrimSpace(cfg.Title), tier)
		}
		return
	}

	if dryRun {
		cfg, ok := bus.GetPopup(rest[0])
		if !ok {
			fmt.Fprintf(os.Stderr, "Error: unknown popup: %s\n", rest[0])
			os.Exit(1)
		}
		// One argument per line: titles and commands contain spaces, so a
		// space-joined line cannot be parsed back into arguments.
		for _, a := range bus.BuildPopupCommand(cfg, bus.BusSession(), sizeFlag, rest[1:]) {
			fmt.Println(a)
		}
		return
	}

	if err := bus.OpenPopup(bus.BusSession(), rest[0], sizeFlag, rest[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
