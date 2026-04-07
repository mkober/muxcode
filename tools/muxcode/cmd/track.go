package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Track handles the "muxcode track" subcommand.
// Usage: muxcode track <msg-id>
func Track(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode track <msg-id>\n")
		os.Exit(1)
	}

	session := bus.BusSession()
	msgID := args[0]

	ds, err := bus.ReadDeliveryStatus(session, msgID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No delivery status for %s\n", msgID)
		os.Exit(1)
	}

	fmt.Println(bus.FormatDeliveryStatus(ds))
}
