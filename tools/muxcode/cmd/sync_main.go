package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// SyncMain handles "muxcode sync-main": it rebases the current branch onto a
// freshly fetched origin/main — on main itself, fast-forwards it carrying the
// uncommitted work (bus.SyncOntoMain) — and prints the report the commit
// agent replies with, ending in EXIT=<n>. Exit 0 only on a clean sync with a
// settled tree; 1 when the rebase was aborted, restoring uncommitted changes
// conflicted (their stash is kept and named), main could not fast-forward
// (nothing changed, collisions listed), or nothing could run.
func SyncMain(args []string) {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode sync-main")
		os.Exit(2)
	}
	r := bus.SyncOntoMain("")
	fmt.Print(r.Report())
	if !r.OK {
		os.Exit(1)
	}
}
