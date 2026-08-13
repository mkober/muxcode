package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// BranchTimeEntry is the accumulated active-working-time record for a single
// branch within a repository.
type BranchTimeEntry struct {
	// Seconds is the total accumulated active seconds spent on the branch.
	Seconds int64 `json:"seconds"`
	// LastJiraLoggedSeconds is the watermark of seconds already posted to the
	// branch's Jira worklog, so log-jira only posts the un-logged delta.
	LastJiraLoggedSeconds int64 `json:"lastJiraLoggedSeconds"`
	// LastRecordedSeconds is the watermark of seconds already written into a
	// requirements doc's Time Tracking table. Unlike the Jira watermark it does
	// not gate the next write — doc rows carry absolute totals, so a re-record
	// is idempotent by construction. It exists so staleness is inspectable:
	// Seconds - LastRecordedSeconds is the time accrued since the last record.
	//
	// Omitted from JSON when zero so ledgers written before doc-recording
	// existed round-trip unchanged.
	LastRecordedSeconds int64 `json:"lastRecordedSeconds,omitempty"`
	// LastRecordedAt is the unix timestamp of the last doc record, or 0 if the
	// branch has never been recorded into a doc.
	LastRecordedAt int64 `json:"lastRecordedAt,omitempty"`
	// Updated is the unix timestamp of the last write to this entry.
	Updated int64 `json:"updated"`
}

// BranchTimeLedger maps repoKey -> branch name -> entry. It is the on-disk
// shape of the global cross-session ledger at BranchTimePath().
type BranchTimeLedger map[string]map[string]*BranchTimeEntry

// branchTimeMu guards ledger read-modify-write within a single process. Cross
// process safety is provided by a syscall.Flock on the ledger lock file.
var branchTimeMu sync.Mutex

// RepoKey returns a stable identity for the current repository, used to key the
// branch-time ledger. It prefers the origin remote URL (stable across clones on
// different paths) and falls back to the repo top-level path. Returns "" when
// the working directory is not inside a git repository. Any userinfo embedded in
// a remote URL (https://user:token@host/…) is stripped so credentials never land
// in the on-disk ledger.
func RepoKey() string {
	return RepoKeyIn("")
}

// RepoKeyIn is RepoKey resolved against an explicit directory. An empty dir
// falls back to the process working directory.
//
// Long-lived processes must pass a directory. The daemon is relaunched by
// `muxcode upgrade-daemons` via a detached exec that inherits the *caller's*
// working directory — so a build run from another repo silently re-parents
// every session's daemon into the building repo. Resolving git from the process
// cwd then attributes one session's time to another repo, or drops it entirely
// when the borrowed repo sits on an ignored branch.
func RepoKeyIn(dir string) string {
	if u := gitOutputIn(dir, "config", "--get", "remote.origin.url"); u != "" {
		return stripURLUserinfo(u)
	}
	return gitOutputIn(dir, "rev-parse", "--show-toplevel")
}

// stripURLUserinfo removes a "user:pass@" (or "user@") userinfo component from a
// URL-shaped remote so embedded credentials are not persisted. Non-URL remotes
// (e.g. scp-style git@host:path, which has no // and is a well-known host alias,
// not a secret) are returned unchanged.
func stripURLUserinfo(remote string) string {
	i := strings.Index(remote, "://")
	if i < 0 {
		return remote // not a URL scheme (e.g. scp-style or a path)
	}
	scheme := remote[:i+3]
	rest := remote[i+3:]
	if at := strings.Index(rest, "@"); at >= 0 {
		// Only strip if the '@' is in the authority (before the first '/').
		if slash := strings.Index(rest, "/"); slash < 0 || at < slash {
			rest = rest[at+1:]
		}
	}
	return scheme + rest
}

// CurrentBranch returns the current git branch name (git rev-parse
// --abbrev-ref HEAD), or "" when not in a git repository. Exported wrapper so
// callers outside the bus package (e.g. the daemon) can resolve the branch.
func CurrentBranch() string {
	return CurrentBranchIn("")
}

// CurrentBranchIn is CurrentBranch resolved against an explicit directory. An
// empty dir falls back to the process working directory. See RepoKeyIn for why
// long-lived processes must pass one.
func CurrentBranchIn(dir string) string {
	b := gitOutputIn(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if b == "HEAD" {
		// Detached HEAD — no branch to attribute time to.
		return ""
	}
	return b
}

// defaultIgnoredBranches are the branch names excluded from time tracking by
// default. These are shared integration branches where attributing "active
// working time" is not meaningful — real work happens on feature branches.
// Overridable via MUXCODE_BRANCH_TIME_IGNORE.
var defaultIgnoredBranches = []string{"main", "master"}

// BranchTimeIgnoredBranches returns the set of branch names excluded from time
// tracking. It is configured by MUXCODE_BRANCH_TIME_IGNORE (comma-separated
// branch names); when that variable is set — even to an empty string — it fully
// replaces the built-in {main, master} default. So MUXCODE_BRANCH_TIME_IGNORE=
// (empty) tracks every branch including main, and MUXCODE_BRANCH_TIME_IGNORE=
// main,develop ignores exactly those two.
func BranchTimeIgnoredBranches() map[string]bool {
	names := defaultIgnoredBranches
	if v, ok := os.LookupEnv("MUXCODE_BRANCH_TIME_IGNORE"); ok {
		names = splitTrimmed(v)
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return set
}

// splitTrimmed splits a comma-separated env value into its non-empty, trimmed
// entries. A blank value yields an empty (not nil) slice, which lets callers
// distinguish "set to empty — replace the default with nothing" from "unset".
func splitTrimmed(v string) []string {
	out := []string{}
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// BranchTimeIgnored reports whether branch should be excluded from time
// tracking. An empty branch (detached HEAD or not a git repository) is always
// ignored, as are the configured integration branches (main/master by default).
func BranchTimeIgnored(branch string) bool {
	if branch == "" {
		return true
	}
	return BranchTimeIgnoredBranches()[branch]
}

// defaultActivityRoles are the agent windows whose visible pane output counts as
// active work for branch-time. Ambient-output roles are deliberately excluded:
// watch (log tailing) and serve (dev server) emit output continuously even when
// no one is working, so counting them would keep the clock running forever.
// Hosted/duplicate roles (docs/research/pr-read) and non-interactive roles
// (webhook/api/auto) are omitted because they either share a host pane or have
// no meaningful agent activity.
//
// analyze is included but is not one of the default session windows, so in most
// sessions its capture fails and it simply reads as "not working". That is
// harmless — it keeps the role counted in the sessions that do open the window,
// at the cost of one failed capture per poll in those that do not.
var defaultActivityRoles = []string{
	"plan", "edit", "build", "test", "review", "deploy", "run", "commit", "analyze",
}

// BranchTimeActivityRoles returns the agent windows sampled to decide whether an
// agent is actively working. Configurable via MUXCODE_BRANCH_TIME_ACTIVITY_ROLES
// (comma-separated); when set it replaces the default set entirely.
func BranchTimeActivityRoles() []string {
	if v, ok := os.LookupEnv("MUXCODE_BRANCH_TIME_ACTIVITY_ROLES"); ok {
		return splitTrimmed(v)
	}
	return defaultActivityRoles
}

// openCodeWorkingMarker is OpenCode's TUI "task still running" indicator (its
// completion marker is "▣"). Used to tell a busy OpenCode pane from an idle one,
// since OpenCode has no stable idle prompt for capture-based detection.
const openCodeWorkingMarker = "▸"

// paneShowsAgentWorking reports whether captured pane content shows an agent
// actively processing a turn. It keys off POSITIVE, provider-specific working
// markers rather than raw content change, so an idle pane that merely flickers —
// a cost/context counter refresh, a periodic TUI redraw, or the daemon injecting
// "You have new messages" wake-up text — is NOT mistaken for real work (which
// would otherwise accrue unbounded phantom time on a detached idle session):
//
//   - Hook provider (Claude Code): isClaudeThinking matches the live spinner
//     signature ("esc to interrupt", or a gerund ellipsis with the
//     "(elapsed · tokens · …)" counter) even while the ❯ prompt is visible. A
//     completed recap ("Cooked for 1m") and a plain idle prompt do not match.
//   - Non-hook TUI (OpenCode): the "▸" running marker (flips to "▣" on
//     completion). isClaudeThinking is NOT applied here — OpenCode truncates paths
//     with "…" and uses " · " separators, which would false-positive its heuristic.
func paneShowsAgentWorking(content string, hookProvider bool) bool {
	if hookProvider {
		return isClaudeThinking(content)
	}
	return strings.Contains(content, openCodeWorkingMarker)
}

// AgentIsWorking reports whether the agent in the given role's pane is actively
// processing a turn. Returns false when the pane can't be captured (window
// closed, no tmux) so a missing pane never counts as work.
func AgentIsWorking(session, role string) bool {
	out, err := TmuxCapturePaneLines(PaneTarget(session, role), 12)
	if err != nil {
		return false
	}
	return paneShowsAgentWorking(out, ResolveProvider(role).SupportsHooks())
}

// AnyAgentWorking reports whether any worker agent (BranchTimeActivityRoles) is
// actively processing. Used by branch-time so a session accrues while an agent
// is working even when the user isn't typing — including a detached background
// session whose agents keep working.
func AnyAgentWorking(session string) bool {
	for _, role := range BranchTimeActivityRoles() {
		if AgentIsWorking(session, role) {
			return true
		}
	}
	return false
}

// BranchTimeUserActive reports whether the user counts as active for branch-time,
// given the seconds since the last keyboard interaction (idle, -1 when detached)
// and the idle threshold (idleMax, <= 0 disables idle detection). The user is
// active when a client is attached AND either idle detection is off or they have
// interacted within idleMax. When this is false the caller falls back to the
// agent-activity signal, so a detached or keyboard-idle session still accrues
// while a worker agent is producing output. Pure for testing.
func BranchTimeUserActive(idle, idleMax int64) bool {
	return idle >= 0 && (idleMax <= 0 || idle <= idleMax)
}

// gitOutput runs a git command in the process working directory and returns
// trimmed stdout, or "" on error.
func gitOutput(args ...string) string {
	return gitOutputIn("", args...)
}

// gitOutputIn runs a git command in dir (process working directory when dir is
// empty) and returns trimmed stdout, or "" on error.
func gitOutputIn(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SessionRepoDir returns the working directory of a session's agents, so
// callers can resolve git state for the session rather than for whatever
// directory their own process happens to be running in.
//
// Resolution order:
//
//  1. MUXCODE_SESSION_REPO_DIR — explicit override, and the seam tests use.
//  2. The session's tmux panes — authoritative, requires no persisted state,
//     and works for sessions that were already running before this existed.
//     The most common pane path wins, so one agent that has cd'd elsewhere
//     cannot outvote the session.
//  3. "" — caller falls back to its own working directory, preserving the
//     previous behaviour rather than failing closed.
func SessionRepoDir(session string) string {
	if v := os.Getenv("MUXCODE_SESSION_REPO_DIR"); v != "" {
		return v
	}
	out, err := TmuxOutput("list-panes", "-s", "-t", session, "-F", "#{pane_current_path}")
	if err != nil {
		return ""
	}
	counts := map[string]int{}
	best, bestN := "", 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		counts[p]++
		// Ties resolve to the lexically smaller path so the result is
		// deterministic rather than dependent on tmux's listing order.
		if counts[p] > bestN || (counts[p] == bestN && p < best) {
			best, bestN = p, counts[p]
		}
	}
	return best
}

// LoadBranchTime reads and parses the ledger. A missing file yields an empty
// (non-nil) ledger and no error.
func LoadBranchTime() (BranchTimeLedger, error) {
	data, err := os.ReadFile(BranchTimePath())
	if err != nil {
		if os.IsNotExist(err) {
			return BranchTimeLedger{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return BranchTimeLedger{}, nil
	}
	var l BranchTimeLedger
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l == nil {
		l = BranchTimeLedger{}
	}
	return l, nil
}

// SaveBranchTime writes the ledger atomically (temp file + rename).
func SaveBranchTime(l BranchTimeLedger) error {
	path := BranchTimePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".branch-time-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// withBranchTimeLock runs fn while holding both the in-process mutex and a
// cross-process advisory flock on the ledger lock file, so the daemon
// accumulator and CLI commands never corrupt the ledger via interleaved writes.
func withBranchTimeLock(fn func() error) error {
	branchTimeMu.Lock()
	defer branchTimeMu.Unlock()

	lockPath := BranchTimePath() + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// entry returns (creating if needed) the entry for repoKey/branch in l.
func (l BranchTimeLedger) entry(repoKey, branch string) *BranchTimeEntry {
	if l[repoKey] == nil {
		l[repoKey] = map[string]*BranchTimeEntry{}
	}
	if l[repoKey][branch] == nil {
		l[repoKey][branch] = &BranchTimeEntry{}
	}
	return l[repoKey][branch]
}

// AccumulateBranchTime adds deltaSecs of active time to repoKey/branch under
// lock. It implements the clock-jump guard: a non-positive delta is a no-op,
// and a delta larger than maxDelta (when maxDelta > 0) is clamped to maxDelta so
// a laptop sleep or clock change cannot add spurious time. Returns the new total.
func AccumulateBranchTime(repoKey, branch string, deltaSecs, maxDelta int64) (int64, error) {
	if repoKey == "" || branch == "" || deltaSecs <= 0 {
		return 0, nil
	}
	if maxDelta > 0 && deltaSecs > maxDelta {
		deltaSecs = maxDelta
	}
	var total int64
	err := withBranchTimeLock(func() error {
		l, err := LoadBranchTime()
		if err != nil {
			return err
		}
		e := l.entry(repoKey, branch)
		e.Seconds += deltaSecs
		e.Updated = time.Now().Unix()
		total = e.Seconds
		return SaveBranchTime(l)
	})
	return total, err
}

// BranchTimeSeconds returns the accumulated seconds for repoKey/branch (0 if unknown).
func BranchTimeSeconds(repoKey, branch string) int64 {
	e, _ := BranchTimeGet(repoKey, branch)
	return e.Seconds
}

// BranchTimeGet returns a copy of the entry for repoKey/branch and whether it exists.
func BranchTimeGet(repoKey, branch string) (BranchTimeEntry, bool) {
	l, err := LoadBranchTime()
	if err != nil {
		return BranchTimeEntry{}, false
	}
	if m := l[repoKey]; m != nil {
		if e := m[branch]; e != nil {
			return *e, true
		}
	}
	return BranchTimeEntry{}, false
}

// AllBranchTimes returns all branch entries for repoKey (nil if none).
func AllBranchTimes(repoKey string) map[string]*BranchTimeEntry {
	l, err := LoadBranchTime()
	if err != nil {
		return nil
	}
	return l[repoKey]
}

// ResetBranchTime zeroes the counters (Jira and doc-record watermarks included)
// for repoKey/branch.
func ResetBranchTime(repoKey, branch string) error {
	return withBranchTimeLock(func() error {
		l, err := LoadBranchTime()
		if err != nil {
			return err
		}
		if m := l[repoKey]; m != nil {
			if e := m[branch]; e != nil {
				e.Seconds = 0
				e.LastJiraLoggedSeconds = 0
				e.LastRecordedSeconds = 0
				e.LastRecordedAt = 0
				e.Updated = time.Now().Unix()
			}
		}
		return SaveBranchTime(l)
	})
}

// SetRecordedWatermark marks repoKey/branch as recorded into a requirements doc
// at the given total, stamping LastRecordedAt with the current time.
//
// This is bookkeeping for staleness only — it deliberately does NOT gate the
// next doc write. Doc rows carry absolute cumulative totals, so re-recording an
// unchanged branch rewrites an identical row; idempotency comes from the value
// being absolute, not from this watermark.
func SetRecordedWatermark(repoKey, branch string, seconds int64) error {
	if repoKey == "" || branch == "" {
		return nil
	}
	return withBranchTimeLock(func() error {
		l, err := LoadBranchTime()
		if err != nil {
			return err
		}
		e := l.entry(repoKey, branch)
		now := time.Now().Unix()
		e.LastRecordedSeconds = seconds
		e.LastRecordedAt = now
		e.Updated = now
		return SaveBranchTime(l)
	})
}

// SeedBranchTime raises repoKey/branch to at least seconds, returning the total
// after seeding. It is a floor, never a setter: if the stored total already
// meets or exceeds seconds the entry is left untouched.
//
// This is the never-regress reconciliation path. When the ledger is lost or
// reset but a requirements doc still shows a larger total, the doc is the
// survivor — plan seeds the ledger back up from the doc's value so the two
// re-converge without the doc ever displaying less time than it already did.
// Because it only ever raises, a stale or duplicated seed cannot deflate a
// ledger that has since accrued past the seeded value.
func SeedBranchTime(repoKey, branch string, seconds int64) (int64, error) {
	if repoKey == "" || branch == "" || seconds <= 0 {
		return 0, nil
	}
	var total int64
	err := withBranchTimeLock(func() error {
		l, err := LoadBranchTime()
		if err != nil {
			return err
		}
		e := l.entry(repoKey, branch)
		if e.Seconds >= seconds {
			total = e.Seconds
			return nil
		}
		e.Seconds = seconds
		e.Updated = time.Now().Unix()
		total = e.Seconds
		return SaveBranchTime(l)
	})
	return total, err
}

// SetJiraWatermark advances the Jira-logged watermark for repoKey/branch to
// seconds, so subsequent log-jira runs only post newly-accrued time.
func SetJiraWatermark(repoKey, branch string, seconds int64) error {
	return withBranchTimeLock(func() error {
		l, err := LoadBranchTime()
		if err != nil {
			return err
		}
		e := l.entry(repoKey, branch)
		e.LastJiraLoggedSeconds = seconds
		e.Updated = time.Now().Unix()
		return SaveBranchTime(l)
	})
}

// SortBranchNames returns the names in m sorted by descending accumulated time
// (most-worked first), breaking ties by name so output is deterministic.
//
// It takes the ledger map rather than a repo key on purpose: a variant that
// re-read the ledger from disk would have to be paired with a separate
// AllBranchTimes load at the call site, and a daemon flush landing between the
// two reads could return a name absent from the map — a nil dereference when
// the caller indexes it.
func SortBranchNames(m map[string]*BranchTimeEntry) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]].Seconds != m[names[j]].Seconds {
			return m[names[i]].Seconds > m[names[j]].Seconds
		}
		return names[i] < names[j]
	})
	return names
}

// FormatDuration renders seconds as a compact spaced string: "2d 5h", "4h 12m",
// "46m", or "0m" for anything under a minute.
func FormatDuration(secs int64) string {
	if secs < 0 {
		secs = 0
	}
	d := secs / 86400
	h := (secs % 86400) / 3600
	m := (secs % 3600) / 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// FormatDurationCompact renders seconds without spaces for the status bar:
// "2d5h", "4h12m", "46m".
func FormatDurationCompact(secs int64) string {
	return strings.ReplaceAll(FormatDuration(secs), " ", "")
}
