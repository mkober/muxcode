package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Pane handles the "muxcode pane" subcommand: resolve a role's pane
// target by identity (MUX-117) and print it. The shell hooks are the
// intended consumer — they cannot reach the Go resolver directly, and
// this keeps them on the same three-way semantics (tag match / legacy
// fallback / loud failure) instead of a private index convention.
// Among resolution outcomes, only the loud-failure branch (marked
// window with a missing or duplicated tag, or an on-disk broken record)
// exits non-zero, so callers can skip rather than fire keystrokes at an
// index that may host an editor or a git TUI; argument errors exit
// non-zero as usual. The legacy-fallback branch prints an index target
// and exits zero — for an unmarked window that is the documented
// outcome, not a failure (PR #54 review clarification).
//
// Usage: muxcode pane <role> [agent|left|control] [--session <name>]
func Pane(args []string) {
	var role, tag, session string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session", "-s":
			if i+1 < len(args) {
				i++
				session = args[i]
			}
		case "-h", "--help":
			fmt.Println("Usage: muxcode pane <role> [agent|left|control] [--session <name>]")
			fmt.Println("  Resolve a role's tmux pane target by identity and print it.")
			fmt.Println("  Tag defaults to \"agent\". Exits non-zero when resolution fails.")
			return
		default:
			if role == "" {
				role = args[i]
			} else if tag == "" {
				tag = args[i]
			}
		}
	}
	if role == "" {
		fmt.Fprintln(os.Stderr, "Usage: muxcode pane <role> [agent|left|control] [--session <name>]")
		os.Exit(1)
	}
	if tag == "" {
		tag = bus.PaneTagAgent
	}
	switch tag {
	case bus.PaneTagAgent, bus.PaneTagLeft, bus.PaneTagControl:
	default:
		fmt.Fprintf(os.Stderr, "unknown pane tag %q (want agent, left, or control)\n", tag)
		os.Exit(1)
	}
	if session == "" {
		session = bus.BusSession()
	}

	target, err := bus.ResolvePane(session, bus.WindowForRole(role), tag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(target)
}
