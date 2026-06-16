package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Scrub handles the "muxcode pii-scrub" subcommand.
// Reads stdin, scrubs PII/secrets, writes to stdout.
// Usage: echo "data" | muxcode pii-scrub
func Scrub(args []string) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}

	out, n := bus.ScrubPIIWithNotice(string(data))
	if n > 0 {
		fmt.Fprintf(os.Stderr, "pii-scrub: %d redaction(s) applied\n", n)
	}
	fmt.Print(out)
}
