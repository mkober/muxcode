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
	if u := gitOutput("config", "--get", "remote.origin.url"); u != "" {
		return stripURLUserinfo(u)
	}
	return gitOutput("rev-parse", "--show-toplevel")
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
	b := gitOutput("rev-parse", "--abbrev-ref", "HEAD")
	if b == "HEAD" {
		// Detached HEAD — no branch to attribute time to.
		return ""
	}
	return b
}

// gitOutput runs a git command and returns trimmed stdout, or "" on error.
func gitOutput(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

// ResetBranchTime zeroes the counters (and Jira watermark) for repoKey/branch.
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
				e.Updated = time.Now().Unix()
			}
		}
		return SaveBranchTime(l)
	})
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

// SortedBranchNames returns the branch names for repoKey sorted by descending
// accumulated time (most-worked first).
func SortedBranchNames(repoKey string) []string {
	m := AllBranchTimes(repoKey)
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return m[names[i]].Seconds > m[names[j]].Seconds
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
