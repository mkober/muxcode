package bus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FreshnessBase is the ref a branch's freshness is measured against: the
// fetched main, never @{upstream}, which is the branch's own remote and read
// 0 behind on the 2026-09-09 run that built on a six-week-old base (MUX-166).
const FreshnessBase = "origin/main"

const (
	// DefaultFreshnessMaxBehind is the behind count that gates a branch whose
	// upstream commits touch no spec-named file (user decision, 2026-11-24).
	DefaultFreshnessMaxBehind = 5
	// DefaultFreshnessMaxFetchAge is how old origin/main may be before the
	// measure is not trusted.
	DefaultFreshnessMaxFetchAge = time.Hour
	freshnessFetchTimeout       = 30 * time.Second
)

// UpstreamChange is one file changed between the merge base and origin/main;
// Status is git's name-status letter (A, M, D, …).
type UpstreamChange struct {
	Status string
	Path   string
}

func (c UpstreamChange) String() string {
	if c.Status == "D" {
		return c.Path + " (deleted)"
	}
	return c.Path
}

// Overlap sources: what the upstream changes were intersected with.
const (
	OverlapSourceSpec   = "spec"   // the active spec's named files
	OverlapSourceBranch = "branch" // no spec: the files the branch itself changes
)

// Freshness is a branch's relation to a fetched origin/main. Stale is the
// verdict the graph gates on; Reasons names every rule that tripped.
// Unmeasured is the subset that makes the numbers untrustworthy — fetch,
// merge base, diff, spec read, fetch age — as distinct from a measured count
// or overlap; both freshness conditions fail while it is non-empty.
type Freshness struct {
	Ahead, Behind int
	MergeBase     string
	Upstream      []UpstreamChange // merge-base..origin/main
	Overlap       []UpstreamChange // the Upstream entries the overlap source names
	OverlapSource string           // OverlapSourceSpec or OverlapSourceBranch
	OverlapBy     []string         // "<sha> <subject>" of each upstream commit touching an Overlap file
	FetchErr      string
	FetchAge      time.Duration // -1 when no fetch has been recorded
	MaxBehind     int
	MaxFetchAge   time.Duration
	Stale         bool
	Reasons       []string
	Unmeasured    []string
}

// Summary is the one-line account used in gate text and lifecycle rows.
func (f Freshness) Summary() string {
	verdict := "fresh"
	if f.Stale {
		verdict = "STALE: " + strings.Join(f.Reasons, "; ")
	}
	age := "never"
	if f.FetchAge >= 0 {
		age = f.FetchAge.Round(time.Second).String() + " ago"
	}
	overlap := "none"
	if len(f.Overlap) > 0 {
		names := make([]string, len(f.Overlap))
		for i, c := range f.Overlap {
			names[i] = c.String()
		}
		overlap = strings.Join(names, ", ")
		if len(f.OverlapBy) > 0 {
			overlap += " (by " + strings.Join(f.OverlapBy, "; ") + ")"
		}
	}
	return fmt.Sprintf("%s: %d behind, %d ahead (gate at %d); upstream touches %s: %s; fetched %s — %s",
		FreshnessBase, f.Behind, f.Ahead, f.MaxBehind, f.overlapNoun(), overlap, age, verdict)
}

func (f Freshness) overlapNoun() string {
	if f.OverlapSource == OverlapSourceBranch {
		return "files this branch changes"
	}
	return "spec files"
}

// BranchFreshness fetches origin main in repo, measures HEAD against it from
// their merge base, and intersects the upstream changes with the files the
// spec at specPath names (SpecNamedFiles); a relative specPath is resolved
// against repo. With no spec (specPath "", as for 60-integration-suite and
// the PR-fix templates) the overlap is measured against the files the branch
// itself changes since the merge base, committed or not (branchChangedFiles)
// — never skipped, so "untouched" is always a measurement.
//
// It fails closed: a failed fetch, an unmeasurable origin/main, a fetch
// older than MUXCODE_FRESHNESS_MAX_FETCH_AGE, an unreadable spec, an
// upstream diff that cannot be listed, or a tree left with unmerged paths or
// a rebase under way (a sync whose autostash conflicted) reads as stale. Any
// overlap is stale whatever the count; with none, a branch is stale at
// MUXCODE_FRESHNESS_MAX_BEHIND (default 5) behind.
func BranchFreshness(repo, specPath string) Freshness {
	f := Freshness{
		FetchAge:    -1,
		MaxBehind:   freshnessMaxBehind(repo),
		MaxFetchAge: freshnessMaxFetchAge(repo),
	}
	if err := fetchOriginMain(repo); err != nil {
		f.FetchErr = err.Error()
		f.Unmeasured = append(f.Unmeasured, "fetch failed: "+f.FetchErr)
	}
	f.FetchAge = lastFetchAge(repo)
	if why := treeUnsettled(repo); why != "" {
		f.Unmeasured = append(f.Unmeasured, "working tree not settled: "+why)
	}

	ahead, behind, base, err := BranchDivergence(repo)
	if err != nil {
		f.Unmeasured = append(f.Unmeasured, err.Error())
		f.Reasons, f.Stale = f.Unmeasured, true
		return f
	}
	f.Ahead, f.Behind, f.MergeBase = ahead, behind, base
	if f.Upstream, err = upstreamChanges(repo, base); err != nil {
		f.Unmeasured = append(f.Unmeasured, err.Error())
	}
	var named []string
	if specPath != "" {
		f.OverlapSource = OverlapSourceSpec
		if !filepath.IsAbs(specPath) {
			specPath = filepath.Join(repo, specPath)
		}
		if named, err = SpecNamedFiles(specPath); err != nil {
			f.Unmeasured = append(f.Unmeasured, "spec-named files unknown: "+err.Error())
		}
	} else {
		f.OverlapSource = OverlapSourceBranch
		if named, err = branchChangedFiles(repo, base); err != nil {
			f.Unmeasured = append(f.Unmeasured, "branch-changed files unknown: "+err.Error())
		}
	}
	f.Overlap = specOverlap(f.Upstream, named)
	f.OverlapBy = overlapCommits(repo, base, f.Overlap)
	if f.FetchErr == "" && (f.FetchAge < 0 || f.FetchAge > f.MaxFetchAge) {
		f.Unmeasured = append(f.Unmeasured, fmt.Sprintf("%s last fetched longer ago than %s", FreshnessBase, f.MaxFetchAge))
	}

	f.Reasons = append([]string(nil), f.Unmeasured...)
	if len(f.Overlap) > 0 {
		f.Reasons = append(f.Reasons, fmt.Sprintf("%d upstream change(s) touch %s", len(f.Overlap), f.overlapNoun()))
	}
	if f.Behind >= f.MaxBehind {
		f.Reasons = append(f.Reasons, fmt.Sprintf("%d behind ≥ %d", f.Behind, f.MaxBehind))
	}
	f.Stale = len(f.Reasons) > 0
	return f
}

// BranchDivergence reports HEAD's ahead and behind counts against
// origin/main as last fetched, and their merge base. It never fetches, so
// the console can call it on every render. repo "" is the working directory.
func BranchDivergence(repo string) (ahead, behind int, mergeBase string, err error) {
	mergeBase = gitOutputIn(repo, "merge-base", "HEAD", FreshnessBase)
	if mergeBase == "" {
		return 0, 0, "", fmt.Errorf("no merge base between HEAD and %s to measure against", FreshnessBase)
	}
	counts := strings.Fields(gitOutputIn(repo, "rev-list", "--left-right", "--count", FreshnessBase+"...HEAD"))
	if len(counts) != 2 {
		return 0, 0, "", fmt.Errorf("could not count HEAD against %s", FreshnessBase)
	}
	behind, _ = strconv.Atoi(counts[0])
	ahead, _ = strconv.Atoi(counts[1])
	return ahead, behind, mergeBase, nil
}

// fetchOriginMain updates refs/remotes/origin/main through an explicit,
// forced destination: a bare `fetch origin main` updates it only through
// remote.origin.fetch, so a narrowed mapping left the cached ref measured as
// if fresh. Forced, because a remote-tracking ref follows the remote even
// when main was rewritten.
func fetchOriginMain(repo string) error {
	ctx, cancel := context.WithTimeout(context.Background(), freshnessFetchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "--quiet", "origin", "+refs/heads/main:refs/remotes/origin/main")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("git fetch origin main timed out after %s", freshnessFetchTimeout)
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return fmt.Errorf("git fetch origin main: %v: %s", err, msg)
	}
	return nil
}

// lastFetchAge is the age of FETCH_HEAD, or -1 when there is none.
func lastFetchAge(repo string) time.Duration {
	p := gitOutputIn(repo, "rev-parse", "--git-path", "FETCH_HEAD")
	if p == "" {
		return -1
	}
	if !filepath.IsAbs(p) && repo != "" {
		p = filepath.Join(repo, p)
	}
	st, err := os.Stat(p)
	if err != nil {
		return -1
	}
	return time.Since(st.ModTime())
}

// upstreamChanges lists files changed from base to origin/main. Renames are
// split into a delete and an add, so a file moved away upstream is named as
// deleted at the path the branch still writes. -z keeps paths literal: git
// otherwise quotes and escapes non-ASCII ones, which never equal a spec token.
func upstreamChanges(repo, base string) ([]UpstreamChange, error) {
	cmd := exec.Command("git", "diff", "--name-status", "--no-renames", "-z", base, FreshnessBase)
	cmd.Dir = repo
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("could not list upstream changes %s..%s: %v", base, FreshnessBase, err)
	}
	fields := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	var out []UpstreamChange
	for i := 0; i+1 < len(fields); i += 2 {
		out = append(out, UpstreamChange{Status: fields[i], Path: fields[i+1]})
	}
	return out, nil
}

// branchChangedFiles lists the files the branch changes since base: tracked
// files differing between base and the working tree (committed, staged or
// not) and untracked files git does not ignore — what a rebase or
// fast-forward could collide with.
func branchChangedFiles(repo, base string) ([]string, error) {
	var out []string
	for _, args := range [][]string{
		{"diff", "--name-only", "--no-renames", "-z", base},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		raw, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %v", args[0], err)
		}
		for _, p := range strings.Split(string(raw), "\x00") {
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// overlapCommits names the mainline commits, base..origin/main, that touch
// the overlapping files, newest first, as "<short sha> <subject>" — the
// merges a person approving the sync gate needs to see. --first-parent
// attributes a no-ff merge to the merge commit on main, judged against its
// first parent; the default path-limited walk follows the side branch and
// names the commit inside it instead. A squash or direct commit is on main
// and is named either way.
func overlapCommits(repo, base string, overlap []UpstreamChange) []string {
	if len(overlap) == 0 {
		return nil
	}
	args := []string{"log", "--first-parent", "--format=%h %s", base + ".." + FreshnessBase, "--"}
	for _, c := range overlap {
		args = append(args, ":(literal)"+c.Path)
	}
	out := gitOutputIn(repo, args...)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// specOverlap keeps the upstream changes a spec-named path matches, whole or
// as a path suffix (`bus/x.go` names `tools/muxcode/bus/x.go`).
func specOverlap(upstream []UpstreamChange, named []string) []UpstreamChange {
	var out []UpstreamChange
	for _, c := range upstream {
		for _, n := range named {
			if c.Path == n || strings.HasSuffix(c.Path, "/"+n) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

var (
	backtickRe     = regexp.MustCompile("`([^`\n]+)`")
	lineSuffixRe   = regexp.MustCompile(`:[0-9][0-9,–-]*$`)
	keyFilesHeadRe = regexp.MustCompile(`^#{2,4} Key files\s*$`)
)

// SpecNamedFiles returns the file paths a spec names for the work ahead: the
// first column of its Key files table and the backticked paths in the
// current phase's open items, read from one copy of the file. A whole
// backtick span counts as a path — spaces and non-ASCII included — when it
// holds a `/` or a file extension, does not end in `/` and carries no call or
// quoting syntax; a `:line` suffix is dropped.
func SpecNamedFiles(specPath string) ([]string, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(text string) {
		for _, m := range backtickRe.FindAllStringSubmatch(text, -1) {
			p := lineSuffixRe.ReplaceAllString(m[1], "")
			if seen[p] || !looksLikePath(p) {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	inKeyFiles := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if keyFilesHeadRe.MatchString(trimmed) {
			inKeyFiles = true
			continue
		}
		if inKeyFiles && strings.HasPrefix(trimmed, "#") {
			inKeyFiles = false
		}
		if inKeyFiles && strings.HasPrefix(trimmed, "|") {
			if cols := strings.Split(trimmed, "|"); len(cols) > 2 {
				add(cols[1])
			}
		}
	}
	current, _ := currentSpecPhase(parseSpecPhases(string(data)))
	for _, item := range current.Items {
		add(item)
	}
	return out, nil
}

func looksLikePath(p string) bool {
	if strings.HasSuffix(p, "/") || strings.ContainsAny(p, "(){}<>=$*\"'") {
		return false
	}
	return strings.Contains(p, "/") || filepath.Ext(p) != ""
}

// freshnessMaxBehind reads MUXCODE_FRESHNESS_MAX_BEHIND from the environment,
// then the config files; a missing or non-positive value is the default.
func freshnessMaxBehind(repo string) int {
	if n, ok := positiveSetting(repo, "MUXCODE_FRESHNESS_MAX_BEHIND"); ok {
		return n
	}
	return DefaultFreshnessMaxBehind
}

// freshnessMaxFetchAge reads MUXCODE_FRESHNESS_MAX_FETCH_AGE (seconds) the
// same way.
func freshnessMaxFetchAge(repo string) time.Duration {
	if n, ok := positiveSetting(repo, "MUXCODE_FRESHNESS_MAX_FETCH_AGE"); ok {
		return time.Duration(n) * time.Second
	}
	return DefaultFreshnessMaxFetchAge
}

func positiveSetting(repo, key string) (int, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		v = strings.TrimSpace(GetShellConfig(repo)[key])
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && n > 0
}
