package bus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// upgradeEventsMaxAge bounds how long a detached run's events file is kept;
// older ones are pruned when the next run starts.
const upgradeEventsMaxAge = 24 * time.Hour

// UpgradeEvent is one line of a detached upgrade's events file: a finished
// step, or the run's end. The JSON names are the contract between
// `muxcode upgrade --events` and the modal that follows it.
type UpgradeEvent struct {
	Index   int         `json:"index"`
	Step    *StepResult `json:"step,omitempty"`
	Done    bool        `json:"done,omitempty"`
	Error   string      `json:"error,omitempty"`
	Summary string      `json:"summary,omitempty"`
}

// AppendUpgradeEvent appends ev to the events file at path as one JSON line,
// written in a single call so a reader never sees half of it complete.
func AppendUpgradeEvent(path string, ev UpgradeEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// ReadUpgradeEvents reads every complete line of the events file at path. A
// final line with no newline yet is a write in progress, left for the next
// read rather than reported as corrupt.
func ReadUpgradeEvents(path string) ([]UpgradeEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []UpgradeEvent
	for {
		line, rest, complete := bytes.Cut(data, []byte("\n"))
		if !complete {
			return events, nil
		}
		data = rest
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev UpgradeEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return events, fmt.Errorf("upgrade events %s: %w", path, err)
		}
		events = append(events, ev)
	}
}

// SelfUpgradeStepNames lists the pipeline's steps in order, for a view that
// shows them pending before they run.
func SelfUpgradeStepNames() []string {
	var names []string
	for _, step := range selfUpgradeSteps() {
		names = append(names, step.Name)
	}
	return names
}

// ResolveUpgradePaths returns the cache root, BINDIR and CONFIGDIR a run with
// opts would use — what a confirm must state before the run mutates them.
func ResolveUpgradePaths(opts SelfUpgradeOptions) (root, bin, config string, err error) {
	return upgradePaths(opts)
}

// upgradeDaemonSessionsFn is how a confirmed run finds daemons started since
// its confirm; a seam over ps, so a test sets the sessions it sees.
var upgradeDaemonSessionsFn = UpgradeDaemonSessions

// UpgradeDaemonSessions lists, sorted, the sessions with a daemon or monitor
// running — the ones the Restart daemons step acts on.
func UpgradeDaemonSessions() ([]string, error) {
	procs, err := ListDaemonProcs()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var sessions []string
	for _, p := range procs {
		if !seen[p.Session] {
			seen[p.Session] = true
			sessions = append(sessions, p.Session)
		}
	}
	sort.Strings(sessions)
	return sessions, nil
}

// StartSelfUpgradeDetached starts `muxcode upgrade --events <file>` from this
// binary in its own session, so the run outlives whatever started it —
// closing the modal never stops an upgrade mid-install. The run is bound to
// confirmed (--expect-tag, one --expect-session per session), so it upgrades
// to nothing and restarts no daemon its user was not shown. It returns the
// events file the run appends to and the child's pid. The file lives under
// <cache root>/runs, where files older than a day are pruned first so
// abandoned ones do not collect.
func StartSelfUpgradeDetached(force bool, confirmed UpgradeConfirmation) (string, int, error) {
	root, _, _, err := upgradePaths(SelfUpgradeOptions{})
	if err != nil {
		return "", 0, err
	}
	dir := filepath.Join(root, "runs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("upgrade events dir: %w", err)
	}
	pruneUpgradeEvents(dir, time.Now().Add(-upgradeEventsMaxAge))
	f, err := os.CreateTemp(dir, "run-*.jsonl")
	if err != nil {
		return "", 0, fmt.Errorf("upgrade events file: %w", err)
	}
	path := f.Name()
	_ = f.Close()
	exe, err := os.Executable()
	if err != nil {
		_ = os.Remove(path)
		return "", 0, fmt.Errorf("locating this binary: %w", err)
	}
	args := []string{"upgrade", "--events", path, "--expect-tag", confirmed.Tag}
	for _, session := range confirmed.Sessions {
		args = append(args, "--expect-session", session)
	}
	if force {
		args = append(args, "--force")
	}
	pid, err := startDetachedProcessIn("", exe, args...)
	if err != nil {
		_ = os.Remove(path)
		return "", 0, fmt.Errorf("starting the upgrade: %w", err)
	}
	return path, pid, nil
}

func pruneUpgradeEvents(dir string, before time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && info.Mode().IsRegular() && info.ModTime().Before(before) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
