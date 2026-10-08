package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Scrub handles the "muxcode pii-scrub" subcommand: stdin redacted to stdout,
// the PIIScrubNotice heading the output. With --role the role decides
// (bus.ConversationScrub): input is redacted, ANSI stripped first, only for a
// PII-sensitive role and otherwise echoed byte-for-byte — the road the Codex
// scrub wrap and the OpenCode plugin take (MUX-203). Any other argument list
// exits 2 before reading stdin: both callers withhold the output on a non-zero
// exit, where a misparsed role would echo it unredacted.
// Usage: echo "data" | muxcode pii-scrub [--role <role>]
func Scrub(args []string) {
	role, byRole, err := parseScrubArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pii-scrub: %v\nUsage: muxcode pii-scrub [--role <role>]\n", err)
		os.Exit(2)
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}

	var out string
	var n int
	if byRole {
		out, n = bus.ConversationScrub(os.Getenv("BUS_SESSION"), role, string(data))
	} else {
		out, n = bus.ScrubPIIWithNotice(string(data))
	}
	if n > 0 {
		fmt.Fprintf(os.Stderr, "pii-scrub: %d redaction(s) applied\n", n)
	}
	fmt.Print(out)
}

// parseScrubArgs accepts no arguments, or exactly `--role <role>` naming a
// role; byRole reports the second form.
func parseScrubArgs(args []string) (role string, byRole bool, err error) {
	switch {
	case len(args) == 0:
		return "", false, nil
	case args[0] != "--role":
		return "", false, fmt.Errorf("unknown argument %q", args[0])
	case len(args) < 2 || args[1] == "" || strings.HasPrefix(args[1], "-"):
		return "", false, errors.New("--role needs a role name")
	case len(args) > 2:
		return "", false, fmt.Errorf("unexpected argument %q after --role %s", args[2], args[1])
	}
	return args[1], true, nil
}
