package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// BranchTime handles the "muxcode branch-time" subcommand — reporting the
// active working time accumulated per git branch by the daemon.
//
// Usage:
//
//	muxcode branch-time                 Current branch, formatted total
//	muxcode branch-time show [--branch <b>] [--json]
//	                                    A single branch, human or machine-readable
//	muxcode branch-time --all [--json]  All branches for this repo
//	muxcode branch-time --status        Compact status-bar string (e.g. "⏱ 4h12m")
//	muxcode branch-time --trailer       "Time-spent: <dur>" commit trailer line
//	muxcode branch-time --add <secs>    Manually add time to the current branch
//	muxcode branch-time record --secs <n> [--branch <b>]
//	                                    Mark <n> seconds as recorded into a requirements doc
//	muxcode branch-time seed --secs <n> [--branch <b>]
//	                                    Raise a branch to at least <n> seconds (never lowers)
//	muxcode branch-time reset [branch]  Zero a branch counter (default: current)
//	muxcode branch-time log-jira [--dry-run]
//	                                    Post the un-logged delta to the branch's Jira story
func BranchTime(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	// args[1:] panics on an empty slice, and the bare "muxcode branch-time"
	// form lands here with len(args) == 0.
	var rest []string
	if len(args) > 1 {
		rest = args[1:]
	}

	switch sub {
	case "--status":
		branchTimeStatus()
	case "--trailer":
		branchTimeTrailer()
	case "--all":
		branchTimeAll(rest)
	case "--add":
		branchTimeAdd(rest)
	case "record":
		branchTimeRecord(rest)
	case "seed":
		branchTimeSeed(rest)
	case "reset":
		branchTimeReset(rest)
	case "log-jira":
		branchTimeLogJira(rest)
	case "", "show":
		branchTimeShow(rest)
	case "--json", "--branch":
		// Bare flag form: `branch-time --json` means `show --json`. This is the
		// invocation automated consumers reach for first, so it must not fall
		// through to the unknown-subcommand error. args[0] is itself a flag
		// here, so the full slice is passed — not rest.
		branchTimeShow(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown branch-time subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "Usage: muxcode branch-time [show [--branch <b>] [--json]|--all [--json]|--status|--trailer|--add <secs>|record --secs <n>|seed --secs <n>|reset [branch]|log-jira [--dry-run]]")
		os.Exit(1)
	}
}

// branchTimeJSON is the stable machine-readable shape emitted by --json. It is
// a presentation struct, deliberately separate from bus.BranchTimeEntry, so the
// on-disk ledger can evolve without silently changing the CLI contract that the
// plan agent parses.
type branchTimeJSON struct {
	RepoKey string `json:"repoKey"`
	Branch  string `json:"branch"`
	Seconds int64  `json:"seconds"`
	// Formatted is the same total rendered for humans, so consumers writing a
	// doc row never need to reimplement duration formatting.
	Formatted string `json:"formatted"`
	// UnrecordedSeconds is Seconds - LastRecordedSeconds, floored at 0. It is
	// the staleness signal: 0 means the doc is already up to date.
	UnrecordedSeconds     int64 `json:"unrecordedSeconds"`
	LastRecordedSeconds   int64 `json:"lastRecordedSeconds"`
	LastRecordedAt        int64 `json:"lastRecordedAt"`
	LastJiraLoggedSeconds int64 `json:"lastJiraLoggedSeconds"`
	Updated               int64 `json:"updated"`
	Current               bool  `json:"current"`
	Ignored               bool  `json:"ignored"`
}

// newBranchTimeJSON projects a ledger entry into the CLI's output shape.
//
// UnrecordedSeconds floors at 0. A doc ahead of the ledger (lost or reset
// store) has nothing new to record, and reconciling that gap is `seed`'s job —
// a negative delta would invite a consumer to subtract time from a doc row.
func newBranchTimeJSON(repoKey, branch string, e bus.BranchTimeEntry, current bool) branchTimeJSON {
	unrecorded := e.Seconds - e.LastRecordedSeconds
	if unrecorded < 0 {
		unrecorded = 0
	}
	return branchTimeJSON{
		RepoKey:               repoKey,
		Branch:                branch,
		Seconds:               e.Seconds,
		Formatted:             bus.FormatDuration(e.Seconds),
		UnrecordedSeconds:     unrecorded,
		LastRecordedSeconds:   e.LastRecordedSeconds,
		LastRecordedAt:        e.LastRecordedAt,
		LastJiraLoggedSeconds: e.LastJiraLoggedSeconds,
		Updated:               e.Updated,
		Current:               current,
		Ignored:               bus.BranchTimeIgnored(branch),
	}
}

// emitJSON writes v as indented JSON to stdout.
func emitJSON(v any) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "json encode failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

// branchTimeFlags parses the small shared flag set used by the read and
// watermark subcommands. An unrecognised flag is an error rather than silently
// ignored, so a typo in an automated caller surfaces immediately.
//
// Flags that are valid here but irrelevant to a given subcommand (--secs on
// show, --branch on --all) are accepted and ignored: the parser is shared, and
// rejecting per-subcommand would need a table this small a surface does not
// justify.
func branchTimeFlags(args []string) (branch string, jsonOut bool, secs int64, secsSet bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--branch":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--branch requires a value")
				os.Exit(1)
			}
			i++
			branch = args[i]
		case "--secs":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "--secs requires a value")
				os.Exit(1)
			}
			i++
			v, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil || v < 0 {
				fmt.Fprintf(os.Stderr, "Invalid --secs %q (must be a non-negative integer)\n", args[i])
				os.Exit(1)
			}
			secs, secsSet = v, true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}
	return branch, jsonOut, secs, secsSet
}

// resolveBranchTimeTarget returns the repo key and the branch to operate on,
// defaulting the branch to the current checkout. Exits with a message when
// either cannot be determined.
func resolveBranchTimeTarget(branch string) (string, string) {
	if branch == "" {
		branch = bus.CurrentBranch()
	}
	if branch == "" {
		fmt.Fprintln(os.Stderr, "Not on a git branch (detached HEAD or not a repo) — pass --branch <b>")
		os.Exit(1)
	}
	repoKey := bus.RepoKey()
	if repoKey == "" {
		fmt.Fprintln(os.Stderr, "Not in a git repository")
		os.Exit(1)
	}
	return repoKey, branch
}

// branchTimeRecord marks a branch as recorded into a requirements doc at the
// given total. Bookkeeping only — it never changes accumulated time.
func branchTimeRecord(args []string) {
	branch, jsonOut, secs, secsSet := branchTimeFlags(args)
	if !secsSet {
		fmt.Fprintln(os.Stderr, "Usage: muxcode branch-time record --secs <n> [--branch <b>]")
		os.Exit(1)
	}
	repoKey, branch := resolveBranchTimeTarget(branch)
	if err := bus.SetRecordedWatermark(repoKey, branch, secs); err != nil {
		fmt.Fprintf(os.Stderr, "record failed: %v\n", err)
		os.Exit(1)
	}
	e, _ := bus.BranchTimeGet(repoKey, branch)
	if jsonOut {
		emitJSON(newBranchTimeJSON(repoKey, branch, e, branch == bus.CurrentBranch()))
		return
	}
	fmt.Printf("%s: recorded %s (total %s)\n", branch, bus.FormatDuration(secs), bus.FormatDuration(e.Seconds))
}

// branchTimeSeed raises a branch to at least the given total — the never-regress
// reconciliation path for a lost or reset ledger.
func branchTimeSeed(args []string) {
	branch, jsonOut, secs, secsSet := branchTimeFlags(args)
	// secs == 0 is rejected, not treated as a no-op: SeedBranchTime would
	// short-circuit to a 0 total and the success line would read
	// "seeded 5m -> 0m", claiming a reduction that never happened.
	if !secsSet || secs == 0 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode branch-time seed --secs <n> [--branch <b>]  (n must be > 0)")
		os.Exit(1)
	}
	repoKey, branch := resolveBranchTimeTarget(branch)
	before := bus.BranchTimeSeconds(repoKey, branch)
	total, err := bus.SeedBranchTime(repoKey, branch, secs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed failed: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		e, _ := bus.BranchTimeGet(repoKey, branch)
		emitJSON(newBranchTimeJSON(repoKey, branch, e, branch == bus.CurrentBranch()))
		return
	}
	if total == before {
		fmt.Printf("%s: already at %s, not lowered to %s\n", branch,
			bus.FormatDuration(before), bus.FormatDuration(secs))
		return
	}
	fmt.Printf("%s: seeded %s -> %s\n", branch, bus.FormatDuration(before), bus.FormatDuration(total))
}

// branchTimeAdd manually adds seconds of time to the current branch. Useful for
// recording work done while no session was running, and as the daemon-free path
// exercised by the integration test.
func branchTimeAdd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode branch-time --add <seconds>")
		os.Exit(1)
	}
	secs, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || secs <= 0 {
		fmt.Fprintf(os.Stderr, "Invalid seconds %q (must be a positive integer)\n", args[0])
		os.Exit(1)
	}
	branch := bus.CurrentBranch()
	if branch == "" {
		fmt.Fprintln(os.Stderr, "Not on a git branch (detached HEAD or not a repo)")
		os.Exit(1)
	}
	repoKey := bus.RepoKey()
	if repoKey == "" {
		fmt.Fprintln(os.Stderr, "Not in a git repository")
		os.Exit(1)
	}
	// maxDelta 0 = no cap; manual entries are explicit and trusted.
	total, err := bus.AccumulateBranchTime(repoKey, branch, secs, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "add failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s: %s\n", branch, bus.FormatDuration(total))
}

// branchTimeStatus prints a compact status-bar segment. Output is empty when
// the feature is disabled, the working dir is not a git repo, or the current
// branch is an ignored integration branch (main/master), so tmux keeps a clean
// status bar.
func branchTimeStatus() {
	if os.Getenv("MUXCODE_BRANCH_TIME_DISABLE") == "1" {
		return
	}
	branch := bus.CurrentBranch()
	repoKey := bus.RepoKey()
	if bus.BranchTimeIgnored(branch) || repoKey == "" {
		return
	}
	secs := bus.BranchTimeSeconds(repoKey, branch)
	if secs <= 0 {
		return
	}
	fmt.Printf("⏱ %s", bus.FormatDurationCompact(secs))
}

// branchTimeTrailer prints a `Time-spent: <duration>` commit trailer line for
// the current branch, or nothing when disabled, not in a repo, on an ignored
// integration branch (main/master), or no time has accrued. Consumed by the
// prepare-commit-msg hook.
func branchTimeTrailer() {
	if os.Getenv("MUXCODE_BRANCH_TIME_DISABLE") == "1" {
		return
	}
	branch := bus.CurrentBranch()
	repoKey := bus.RepoKey()
	if bus.BranchTimeIgnored(branch) || repoKey == "" {
		return
	}
	secs := bus.BranchTimeSeconds(repoKey, branch)
	if secs <= 0 {
		return
	}
	fmt.Printf("Time-spent: %s\n", bus.FormatDuration(secs))
}

// branchTimeShow prints one branch's total — the current checkout by default,
// or --branch <b>. With --json it emits the machine-readable shape consumed by
// the plan agent when recording time into a requirements doc.
//
// An untracked branch is not an error: it reports a zero total, so an automated
// caller gets a well-formed object instead of having to special-case a failure.
func branchTimeShow(args []string) {
	branch, jsonOut, _, _ := branchTimeFlags(args)
	repoKey, branch := resolveBranchTimeTarget(branch)
	e, _ := bus.BranchTimeGet(repoKey, branch)
	if jsonOut {
		emitJSON(newBranchTimeJSON(repoKey, branch, e, branch == bus.CurrentBranch()))
		return
	}
	fmt.Printf("%s: %s\n", branch, bus.FormatDuration(e.Seconds))
}

// branchTimeAll prints a table of all tracked branches for the current repo,
// most-worked first.
func branchTimeAll(args []string) {
	_, jsonOut, _, _ := branchTimeFlags(args)
	repoKey := bus.RepoKey()
	if repoKey == "" {
		fmt.Fprintln(os.Stderr, "Not in a git repository")
		os.Exit(1)
	}
	// One load, sorted locally: a second read could race a daemon flush and
	// return a name absent from entries — a nil dereference on index.
	entries := bus.AllBranchTimes(repoKey)
	names := bus.SortBranchNames(entries)
	current := bus.CurrentBranch()

	if jsonOut {
		// Always an array, empty included, so consumers can iterate without a
		// nil check. MarshalIndent renders a nil slice as "null", hence the
		// explicit non-nil initialisation.
		rows := make([]branchTimeJSON, 0, len(names))
		for _, name := range names {
			rows = append(rows, newBranchTimeJSON(repoKey, name, *entries[name], name == current))
		}
		emitJSON(rows)
		return
	}

	if len(entries) == 0 {
		fmt.Println("No branch time tracked yet for this repo")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "BRANCH\tTIME\tLOGGED")
	for _, name := range names {
		e := entries[name]
		marker := ""
		if name == current {
			marker = " *"
		}
		fmt.Fprintf(w, "%s%s\t%s\t%s\n", name, marker,
			bus.FormatDuration(e.Seconds), bus.FormatDuration(e.LastJiraLoggedSeconds))
	}
	w.Flush()
}

// branchTimeReset zeroes the counter for the given branch (default: current).
func branchTimeReset(args []string) {
	branch := ""
	if len(args) > 0 {
		branch = args[0]
	} else {
		branch = bus.CurrentBranch()
	}
	if branch == "" {
		fmt.Fprintln(os.Stderr, "No branch specified and not on a git branch")
		os.Exit(1)
	}
	repoKey := bus.RepoKey()
	if repoKey == "" {
		fmt.Fprintln(os.Stderr, "Not in a git repository")
		os.Exit(1)
	}
	if err := bus.ResetBranchTime(repoKey, branch); err != nil {
		fmt.Fprintf(os.Stderr, "reset failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Reset branch time for %s\n", branch)
}

// jiraWorklogMinSeconds is Jira's minimum worklog duration (1 minute).
const jiraWorklogMinSeconds int64 = 60

// branchTimeLogJira posts the un-logged time delta for the current branch to the
// Jira story parsed from the branch name, then advances the watermark so a
// re-run only posts newly-accrued time. With --dry-run it reports the computed
// delta without posting or advancing the watermark.
func branchTimeLogJira(args []string) {
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" {
			dryRun = true
		}
	}

	branch := bus.CurrentBranch()
	if branch == "" {
		fmt.Fprintln(os.Stderr, "Not on a git branch (detached HEAD or not a repo)")
		os.Exit(1)
	}
	repoKey := bus.RepoKey()
	if repoKey == "" {
		fmt.Fprintln(os.Stderr, "Not in a git repository")
		os.Exit(1)
	}

	key := bus.JiraKeyFromString(branch)
	if key == "" {
		fmt.Fprintf(os.Stderr, "No Jira key found in branch name %q — cannot log worklog\n", branch)
		os.Exit(1)
	}

	entry, _ := bus.BranchTimeGet(repoKey, branch)
	delta := entry.Seconds - entry.LastJiraLoggedSeconds
	if delta <= 0 {
		fmt.Printf("%s: nothing new to log (%s already logged of %s)\n",
			key, bus.FormatDuration(entry.LastJiraLoggedSeconds), bus.FormatDuration(entry.Seconds))
		return
	}
	if delta < jiraWorklogMinSeconds {
		fmt.Printf("%s: un-logged delta %s is below Jira's 1m minimum — waiting for more time to accrue\n",
			key, bus.FormatDuration(delta))
		return
	}

	if dryRun {
		fmt.Printf("[dry-run] would log %s (%ds) to %s (branch %s)\n",
			bus.FormatDuration(delta), delta, key, branch)
		return
	}

	cfg, err := bus.LoadAtlassianConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	comment := fmt.Sprintf("Automated worklog from muxcode branch %s", branch)
	result, err := bus.JiraAddWorklog(cfg, key, delta, comment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	// Advance the watermark to the total logged so far so re-runs don't double-post.
	if err := bus.SetJiraWatermark(repoKey, branch, entry.Seconds); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: worklog posted but watermark update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
}
