package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Scrub handles the "muxcode pii-scrub" subcommand: stdin redacted to stdout,
// the PIIScrubNotice heading the output. With --role the role decides
// (bus.ConversationScrub): input is redacted, ANSI stripped first, only for a
// PII-sensitive role and otherwise echoed byte-for-byte — the road the Codex
// scrub wrap and the OpenCode plugin take (MUX-203).
// Usage: echo "data" | muxcode pii-scrub [--role <role>]
func Scrub(args []string) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}

	var out string
	var n int
	if len(args) >= 1 && args[0] == "--role" {
		role := ""
		if len(args) >= 2 {
			role = args[1]
		}
		out, n = bus.ConversationScrub(os.Getenv("BUS_SESSION"), role, string(data))
	} else {
		out, n = bus.ScrubPIIWithNotice(string(data))
	}
	if n > 0 {
		fmt.Fprintf(os.Stderr, "pii-scrub: %d redaction(s) applied\n", n)
	}
	fmt.Print(out)
}
