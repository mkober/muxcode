package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

const upgradeUsage = "Usage: muxcode upgrade [--check] [--force] [--json]"

// upgradeNewerExit is the --check exit code for "a newer release is available
// and this machine can build it" — distinct from 1 so a cron or a script can
// poll for an upgrade without parsing output.
const upgradeNewerExit = 10

// Upgrade handles "muxcode upgrade" (MUX-202): the self-upgrade from the
// latest GitHub release — the modal's pipeline, run non-interactively.
//
// Usage: muxcode upgrade [--check] [--force] [--json]
//
//	--check  look up the latest release and stop; exit 0 when nothing would
//	         be upgraded, 10 when a newer release is available, 1 on error
//	--force  rebuild and reinstall the latest release even when current
//	--json   print the result as JSON (UpgradeCheck with --check, else the
//	         upgradeReport) instead of one line per step
//
// Without --check every step runs, each printed as it finishes; the exit is
// 0 on success, an up-to-date install included, and 1 on a failed step.
func Upgrade(args []string) {
	var check, force, jsonOut bool
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "--force", "-f":
			force = true
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

	client := bus.DefaultReleaseClient()
	if check {
		result, err := bus.CheckUpgrade(context.Background(), client)
		writeUpgradeCheck(os.Stdout, os.Stderr, result, err, jsonOut)
		os.Exit(upgradeCheckExit(result, err))
	}

	var progress bus.UpgradeProgress
	if !jsonOut {
		progress = func(_ int, r bus.StepResult) { writeStepResult(os.Stdout, r) }
	}
	s, err := bus.RunSelfUpgrade(context.Background(), bus.SelfUpgradeOptions{Force: force, Client: client}, progress)
	switch {
	case jsonOut:
		writeUpgradeReport(os.Stdout, os.Stderr, s, err)
	case err != nil:
		fmt.Fprintf(os.Stderr, "muxcode upgrade failed at %v\n", err)
	case !s.Stopped:
		fmt.Println(s.DoneSummary())
	}
	if err != nil {
		os.Exit(1)
	}
}

// writeStepResult prints one step line, its sub-rows indented beneath it.
func writeStepResult(w io.Writer, r bus.StepResult) {
	fmt.Fprintln(w, stepLine(r))
	for _, sub := range r.Sub {
		fmt.Fprintln(w, "  "+stepLine(sub))
	}
}

func stepLine(r bus.StepResult) string {
	switch {
	case !r.Success:
		return r.Name + ": FAILED — " + r.Error
	case r.Note == "":
		return r.Name + ": done"
	}
	return r.Name + ": " + r.Note
}

// upgradeReport is the `muxcode upgrade --json` output contract. Upgraded is
// false both for a failed run and for one that found nothing to upgrade.
type upgradeReport struct {
	Installed string             `json:"installed"`
	Latest    string             `json:"latest,omitempty"`
	Verdict   bus.UpgradeVerdict `json:"verdict,omitempty"`
	Upgraded  bool               `json:"upgraded"`
	Steps     []bus.StepResult   `json:"steps"`
	Error     string             `json:"error,omitempty"`
}

func writeUpgradeReport(stdout, stderr io.Writer, s *bus.UpgradeState, err error) {
	report := upgradeReport{
		Installed: s.Check.Installed.Version,
		Latest:    s.Check.Latest.Tag,
		Verdict:   s.Check.Verdict,
		Upgraded:  err == nil && !s.Stopped,
		Steps:     s.Results,
	}
	if err != nil {
		report.Error = err.Error()
	}
	out, merr := json.Marshal(report)
	if merr != nil {
		fmt.Fprintf(stderr, "Error formatting JSON: %v\n", merr)
		return
	}
	fmt.Fprintln(stdout, string(out))
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
