package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Resize handles the "muxcode resize" subcommand. It resizes every window in
// every tmux session to fit the connected client. The tmux client-resized hook
// calls this so a monitor/terminal resize updates every subsession, not just the
// one the client is currently viewing.
func Resize(args []string) {
	if err := bus.ResizeAllWindows(); err != nil {
		fmt.Fprintf(os.Stderr, "resize: %v\n", err)
		os.Exit(1)
	}
}
