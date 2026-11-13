package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// BusDir handles "muxcode bus-dir [session]": it prints the session's absolute
// bus directory — the same answer bus.BusDir gives the binary — for scripts,
// hooks and editors that must not compute the path themselves (MUX-208). The
// session defaults to $BUS_SESSION; with neither it exits 2. The directory is
// not created, though a pre-MUX-208 /tmp copy is moved into place first.
func BusDir(args []string) {
	session := os.Getenv("BUS_SESSION")
	switch {
	case len(args) == 1 && args[0] != "-h" && args[0] != "--help":
		session = args[0]
	case len(args) > 0:
		fmt.Fprintln(os.Stderr, "Usage: muxcode bus-dir [session]")
		os.Exit(2)
	}
	if session == "" {
		fmt.Fprintln(os.Stderr, "muxcode bus-dir: no session given and BUS_SESSION is unset")
		os.Exit(2)
	}
	fmt.Println(bus.BusDir(session))
}
