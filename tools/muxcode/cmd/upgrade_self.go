package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

const upgradeUsage = "Usage: muxcode upgrade --check [--json]"

// upgradeNewerExit is the --check exit code for "a newer release is available
// and this machine can build it" — distinct from 1 so a cron or a script can
// poll for an upgrade without parsing output.
const upgradeNewerExit = 10

// Upgrade handles "muxcode upgrade" (MUX-202): the self-upgrade from the
// latest GitHub release. Only the Check step is built so far.
//
// Usage: muxcode upgrade --check [--json]
//
//	--check  look up the latest release and stop; exit 0 when nothing would
//	         be upgraded, 10 when a newer release is available, 1 on error
//	--json   print the check as JSON (the UpgradeCheck contract)
func Upgrade(args []string) {
	check, jsonOut := false, false
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Println(upgradeUsage)
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n%s\n", a, upgradeUsage)
			os.Exit(1)
		}
	}
	if !check {
		fmt.Fprintf(os.Stderr, "muxcode upgrade: the install pipeline is not built yet; only --check runs\n%s\n", upgradeUsage)
		os.Exit(1)
	}

	result, err := bus.CheckUpgrade(context.Background(), bus.DefaultReleaseClient())
	writeUpgradeCheck(os.Stdout, os.Stderr, result, err, jsonOut)
	os.Exit(upgradeCheckExit(result, err))
}

// upgradeCheckExit is the --check exit code. A newer release is 10 only when
// it could be built: with a tool missing the upgrade would fail at Check, so
// a poller must not be told to run it. An unknown verdict cannot tell newer
// from ahead, so it is an error rather than a guess.
func upgradeCheckExit(c bus.UpgradeCheck, err error) int {
	switch {
	case err != nil:
		return 1
	case c.Verdict == bus.UpgradeNewer && c.ToolsErr() != nil:
		return 1
	case c.Verdict == bus.UpgradeNewer:
		return upgradeNewerExit
	case c.Verdict == bus.UpgradeUnknown:
		return 1
	}
	return 0
}

// writeUpgradeCheck prints the Check step. A failed lookup has no latest
// release, so its JSON is the installed identity plus the error rather than
// an UpgradeCheck with empty release fields a script could misread.
func writeUpgradeCheck(stdout, stderr io.Writer, c bus.UpgradeCheck, err error, jsonOut bool) {
	if jsonOut {
		var v any = c
		if err != nil {
			v = struct {
				Installed bus.Info `json:"installed"`
				Error     string   `json:"error"`
			}{c.Installed, err.Error()}
		}
		out, merr := json.Marshal(v)
		if merr != nil {
			fmt.Fprintf(stderr, "Error formatting JSON: %v\n", merr)
			return
		}
		fmt.Fprintln(stdout, string(out))
		return
	}
	if err != nil {
		fmt.Fprintf(stderr, "Check: FAILED — %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Check: %s\n", c.Summary())
	if terr := c.ToolsErr(); terr != nil {
		label := "note"
		if c.Verdict == bus.UpgradeNewer {
			label = "FAILED"
		}
		fmt.Fprintf(stderr, "Check: %s — %v\n", label, terr)
	}
}
