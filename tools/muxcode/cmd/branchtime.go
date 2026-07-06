package cmd

import (
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
//	muxcode branch-time --all           Table of all branches for this repo
//	muxcode branch-time --status        Compact status-bar string (e.g. "⏱ 4h12m")
//	muxcode branch-time --trailer       "Time-spent: <dur>" commit trailer line
//	muxcode branch-time --add <secs>    Manually add time to the current branch
//	muxcode branch-time reset [branch]  Zero a branch counter (default: current)
//	muxcode branch-time log-jira [--dry-run]
//	                                    Post the un-logged delta to the branch's Jira story
func BranchTime(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "--status":
		branchTimeStatus()
	case "--trailer":
		branchTimeTrailer()
	case "--all":
		branchTimeAll()
	case "--add":
		branchTimeAdd(args[1:])
	case "reset":
		branchTimeReset(args[1:])
	case "log-jira":
		branchTimeLogJira(args[1:])
	case "", "show":
		branchTimeShow()
	default:
		fmt.Fprintf(os.Stderr, "Unknown branch-time subcommand: %s\n", sub)
		fmt.Fprintln(os.Stderr, "Usage: muxcode branch-time [--all|--status|--trailer|--add <secs>|reset [branch]|log-jira [--dry-run]]")
		os.Exit(1)
	}
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
// the feature is disabled or the working dir is not a git repo, so tmux keeps a
// clean status bar.
func branchTimeStatus() {
	if os.Getenv("MUXCODE_BRANCH_TIME_DISABLE") == "1" {
		return
	}
	branch := bus.CurrentBranch()
	repoKey := bus.RepoKey()
	if branch == "" || repoKey == "" {
		return
	}
	secs := bus.BranchTimeSeconds(repoKey, branch)
	if secs <= 0 {
		return
	}
	fmt.Printf("⏱ %s", bus.FormatDurationCompact(secs))
}

// branchTimeTrailer prints a `Time-spent: <duration>` commit trailer line for
// the current branch, or nothing when disabled, not in a repo, or no time has
// accrued. Consumed by the prepare-commit-msg hook.
func branchTimeTrailer() {
	if os.Getenv("MUXCODE_BRANCH_TIME_DISABLE") == "1" {
		return
	}
	branch := bus.CurrentBranch()
	repoKey := bus.RepoKey()
	if branch == "" || repoKey == "" {
		return
	}
	secs := bus.BranchTimeSeconds(repoKey, branch)
	if secs <= 0 {
		return
	}
	fmt.Printf("Time-spent: %s\n", bus.FormatDuration(secs))
}

// branchTimeShow prints the current branch's formatted total.
func branchTimeShow() {
	branch := bus.CurrentBranch()
	if branch == "" {
		fmt.Fprintln(os.Stderr, "Not on a git branch (detached HEAD or not a repo)")
		os.Exit(1)
	}
	repoKey := bus.RepoKey()
	secs := bus.BranchTimeSeconds(repoKey, branch)
	fmt.Printf("%s: %s\n", branch, bus.FormatDuration(secs))
}

// branchTimeAll prints a table of all tracked branches for the current repo,
// most-worked first.
func branchTimeAll() {
	repoKey := bus.RepoKey()
	if repoKey == "" {
		fmt.Fprintln(os.Stderr, "Not in a git repository")
		os.Exit(1)
	}
	entries := bus.AllBranchTimes(repoKey)
	if len(entries) == 0 {
		fmt.Println("No branch time tracked yet for this repo")
		return
	}
	names := bus.SortedBranchNames(repoKey)
	current := bus.CurrentBranch()

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
