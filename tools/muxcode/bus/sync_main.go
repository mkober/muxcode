package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SyncResult is the outcome of SyncOntoMain. OK is true only when the branch
// was rebased onto origin/main and the working tree is settled.
type SyncResult struct {
	OK            bool
	Aborted       bool     // a conflicting rebase was aborted; HEAD is where it was
	Conflicts     []string // conflicting or unmerged paths
	StashKept     string   // the stash holding uncommitted changes whose restore conflicted
	Ahead, Behind int
	Detail        string
}

// Report is the text the commit agent replies with; it ends with the
// EXIT=<n> sentinel the graph's sync node routes on.
func (r SyncResult) Report() string {
	var b strings.Builder
	if r.OK {
		fmt.Fprintf(&b, "Synced onto %s: %d behind, %d ahead.\n", FreshnessBase, r.Behind, r.Ahead)
	} else {
		fmt.Fprintf(&b, "Sync onto %s did not complete: %s\n", FreshnessBase, r.Detail)
	}
	if len(r.Conflicts) > 0 {
		fmt.Fprintf(&b, "Conflicting files: %s\n", strings.Join(r.Conflicts, ", "))
	}
	if r.StashKept != "" {
		fmt.Fprintf(&b, "Uncommitted changes are kept in %s — resolve the files above, then git stash drop %s once they are restored.\n", r.StashKept, r.StashKept)
	}
	if r.OK {
		b.WriteString("EXIT=0\n")
	} else {
		b.WriteString("EXIT=1\n")
	}
	return b.String()
}

// SyncOntoMain rebases repo's current branch onto a freshly fetched
// origin/main with --autostash (MUX-166's sync node, behind a human gate).
// It never leaves a rebase in progress and never drops a stash:
//   - an unsettled tree (unmerged paths, a rebase under way) is refused untouched;
//   - a conflicting rebase is aborted and its files reported;
//   - a rebase that replays cleanly but whose autostash conflicts on restore —
//     where there is no rebase left to abort — is reported with the stash that
//     still holds the changes.
//
// Only a clean rebase with a settled tree is OK. The caller's authority
// (CheckSyncMainAuthority) is checked before anything runs, fetch included.
//
// On main itself — 55-defect-backlog-loop, which implements uncommitted on
// main — the sync is fastForwardMain instead: main is moved, never rebased.
func SyncOntoMain(repo string) SyncResult {
	if deny := CheckSyncMainAuthority(); deny != "" {
		return SyncResult{Detail: deny}
	}
	if why := treeUnsettled(repo); why != "" {
		return SyncResult{Detail: "the working tree is not settled, nothing was changed: " + why}
	}
	if err := fetchOriginMain(repo); err != nil {
		return SyncResult{Detail: err.Error()}
	}
	if gitOutputIn(repo, "symbolic-ref", "--short", "-q", "HEAD") == "main" {
		return fastForwardMain(repo)
	}
	stashesBefore := stashCount(repo)
	cmd := exec.Command("git", "rebase", "--autostash", FreshnessBase)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()

	if err != nil && rebaseInProgress(repo) {
		r := SyncResult{Aborted: true, Conflicts: unmergedPaths(repo), Detail: "the rebase conflicted and was aborted"}
		abort := exec.Command("git", "rebase", "--abort")
		abort.Dir = repo
		if aout, aerr := abort.CombinedOutput(); aerr != nil || rebaseInProgress(repo) {
			r.Detail = fmt.Sprintf("the rebase conflicted and could not be aborted: %v %s", aerr, strings.TrimSpace(string(aout)))
		}
		r.StashKept = keptAutostash(repo, stashesBefore)
		return r
	}
	if stash, unmerged := keptAutostash(repo, stashesBefore), unmergedPaths(repo); stash != "" || len(unmerged) > 0 {
		return SyncResult{Conflicts: unmerged, StashKept: stash,
			Detail: "the branch was rebased, but restoring the uncommitted changes conflicted"}
	}
	if err != nil {
		return SyncResult{Detail: "git rebase failed: " + firstLine(string(out))}
	}
	ahead, behind, _, derr := BranchDivergence(repo)
	if derr != nil {
		return SyncResult{Detail: derr.Error()}
	}
	return SyncResult{OK: true, Ahead: ahead, Behind: behind}
}

// fastForwardMain moves main to origin/main carrying the run's uncommitted
// work — the activated spec move, earlier phases — or changes nothing. It
// refuses, untouched, when main holds commits origin/main lacks (a
// fast-forward cannot carry them) or when an uncommitted or untracked file
// is one the upstream changes touch, listing those files as the conflicts.
// What remains is `git merge --ff-only`, which itself refuses rather than
// overwrite a local change, so nothing is ever half-applied and no stash is
// made.
func fastForwardMain(repo string) SyncResult {
	ahead, _, base, err := BranchDivergence(repo)
	if err != nil {
		return SyncResult{Detail: err.Error()}
	}
	if ahead > 0 {
		return SyncResult{Detail: fmt.Sprintf("main holds %d commit(s) %s lacks — a fast-forward cannot carry them; nothing was changed", ahead, FreshnessBase)}
	}
	upstream, err := upstreamChanges(repo, base)
	if err != nil {
		return SyncResult{Detail: err.Error()}
	}
	local, err := branchChangedFiles(repo, base)
	if err != nil {
		return SyncResult{Detail: "could not list the uncommitted work: " + err.Error()}
	}
	localSet := map[string]bool{}
	for _, p := range local {
		localSet[p] = true
	}
	var conflicts []string
	for _, c := range upstream {
		if localSet[c.Path] {
			conflicts = append(conflicts, c.Path)
		}
	}
	if len(conflicts) > 0 {
		return SyncResult{Conflicts: conflicts,
			Detail: "uncommitted work on main touches files the upstream changes; nothing was changed"}
	}
	cmd := exec.Command("git", "merge", "--ff-only", "--quiet", FreshnessBase)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		return SyncResult{Detail: "git merge --ff-only refused, nothing was changed: " + firstLine(string(out))}
	}
	ahead, behind, _, err := BranchDivergence(repo)
	if err != nil {
		return SyncResult{Detail: err.Error()}
	}
	return SyncResult{OK: true, Ahead: ahead, Behind: behind}
}

// CheckSyncMainAuthority refuses a sync to every caller but the commit agent
// and a person: it rebases the checkout, and git mutations are the commit
// agent's — edit and plan are refused git at their guard, which this command
// would otherwise route around. The actor is BusActorVerified, so an agent
// cannot pass as the user by unsetting AGENT_ROLE, and an actor that cannot
// be established is refused. A set AGENT_ROLE is believed, as in every role
// check; the tool-profile guard is what keeps an agent from typing one.
func CheckSyncMainAuthority() string {
	switch actor := BusActorVerified(); actor {
	case "commit", ActorUser:
		return ""
	default:
		return fmt.Sprintf("git mutations are the commit agent's: %s may not run muxcode sync-main. "+
			`Delegate: muxcode send commit rebase "<describe the sync>"`, actor)
	}
}

// treeUnsettled names why repo's working tree cannot be measured or synced —
// a rebase under way or unmerged paths — or returns "".
func treeUnsettled(repo string) string {
	if rebaseInProgress(repo) {
		return "a rebase is in progress"
	}
	if u := unmergedPaths(repo); len(u) > 0 {
		return "unmerged paths: " + strings.Join(u, ", ")
	}
	return ""
}

func rebaseInProgress(repo string) bool {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		p := gitOutputIn(repo, "rev-parse", "--git-path", name)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) && repo != "" {
			p = filepath.Join(repo, p)
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func unmergedPaths(repo string) []string {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=U", "-z")
	cmd.Dir = repo
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range strings.Split(string(raw), "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stashCount(repo string) int {
	list := gitOutputIn(repo, "stash", "list")
	if list == "" {
		return 0
	}
	return len(strings.Split(list, "\n"))
}

// keptAutostash returns "stash@{0}" when the stash grew during the sync —
// git stores the autostash there when restoring it conflicts.
func keptAutostash(repo string, before int) string {
	if stashCount(repo) > before {
		return "stash@{0}"
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
