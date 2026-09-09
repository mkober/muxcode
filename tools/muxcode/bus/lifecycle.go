package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// LifecycleEntry represents a single lifecycle log event.
type LifecycleEntry struct {
	TS      int64  `json:"ts"`
	Level   string `json:"level"`
	Source  string `json:"source"`
	Session string `json:"session"`
	Event   string `json:"event"`
	PID     int    `json:"pid,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// LifecycleFilterOpts controls filtering when reading lifecycle logs.
type LifecycleFilterOpts struct {
	Source string
	Level  string
	Event  string
	Since  int64 // unix timestamp — entries before this are excluded
	Limit  int   // max entries to return (0 = all)
}

// LifecycleLogDir returns the directory holding persistent lifecycle logs.
//
// MUXCODE_LIFECYCLE_LOG_DIR overrides the location. Tests MUST set it (see
// TestMain in this package) — lifecycle logging is a side effect of a great
// many code paths, and without an override a test run writes one real log file
// per synthetic session name into the user's actual ~/.config/muxcode/logs.
// That leaked 41,789 stray test-*.log files into a live install before this
// override existed.
func LifecycleLogDir() string {
	if dir := os.Getenv("MUXCODE_LIFECYCLE_LOG_DIR"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "muxcode", "logs")
}

// LifecycleLogPath returns the lifecycle log file path for a session.
func LifecycleLogPath(session string) string {
	return filepath.Join(LifecycleLogDir(), session+".log")
}

// lifecycleMaxEntries is the default max entries before rotation.
// Override via MUXCODE_LIFECYCLE_LOG_MAX env var.
//
// 5000, up from 1000: at the 2026-09-02 write rate a 1000-entry log rotated
// past both mass-death waves within ~2 hours of each, before anyone read it
// (MUX-136 Phase 4). 5000 entries is roughly a working day at ~150 bytes each.
func lifecycleMaxEntries() int {
	if v := os.Getenv("MUXCODE_LIFECYCLE_LOG_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 5000
}

// LogLifecycle appends a lifecycle event to the persistent log.
// Safe for concurrent use via flock. Errors are silently ignored
// to avoid breaking callers — logging should never cause failures.
func LogLifecycle(session, level, source, event, detail string) {
	LogLifecycleWithPID(session, level, source, event, detail, 0)
}

// LogLifecycleWithPID appends a lifecycle event with an explicit PID.
func LogLifecycleWithPID(session, level, source, event, detail string, pid int) {
	writeLifecycleEntry(LifecycleEntry{
		TS:      time.Now().Unix(),
		Level:   level,
		Source:  source,
		Session: session,
		Event:   event,
		PID:     pid,
		Detail:  detail,
	})
}

// LogLifecycleAt appends an event stamped with ts rather than the current
// second, for a caller that must record one instant in two places.
//
// gateApprovalHolds matches an approval marker against its graph-gate-approved
// row on the second, so reading the clock once per writer made a genuine
// approval racy: a bus send and a rotation pass sit between the two writes in
// ApproveGraphGate, and an approval that straddled a second boundary was
// refused as forged and its marker purged, sending the user back to approve
// again into the same race (MUX-144 Phase 2).
func LogLifecycleAt(session, level, source, event, detail string, ts int64) {
	writeLifecycleEntry(LifecycleEntry{
		TS:      ts,
		Level:   level,
		Source:  source,
		Session: session,
		Event:   event,
		Detail:  detail,
	})
}

func writeLifecycleEntry(entry LifecycleEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	logDir := LifecycleLogDir()
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return
	}

	logPath := LifecycleLogPath(entry.Session)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}

	// Blocking flock — wait for lock rather than skip
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)

	_, _ = f.Write(append(data, '\n'))

	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()

	// Rotate if needed — rotateLifecycleLog has an early return when
	// the file is under maxEntries, so this is cheap in the common case.
	rotateLifecycleLog(logPath, lifecycleMaxEntries())
}

// ReadLifecycleLog reads all entries from a session's lifecycle log.
func ReadLifecycleLog(session string) ([]LifecycleEntry, error) {
	logPath := LifecycleLogPath(session)
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	return parseLifecycleEntries(data), nil
}

// FilterLifecycleLog reads and filters lifecycle entries.
func FilterLifecycleLog(session string, opts LifecycleFilterOpts) ([]LifecycleEntry, error) {
	entries, err := ReadLifecycleLog(session)
	if err != nil {
		return nil, err
	}

	var filtered []LifecycleEntry
	for _, e := range entries {
		if opts.Source != "" && e.Source != opts.Source {
			continue
		}
		if opts.Level != "" && e.Level != opts.Level {
			continue
		}
		if opts.Event != "" && e.Event != opts.Event {
			continue
		}
		if opts.Since > 0 && e.TS < opts.Since {
			continue
		}
		filtered = append(filtered, e)
	}

	// Apply limit (take last N entries)
	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[len(filtered)-opts.Limit:]
	}

	return filtered, nil
}

// ListLifecycleSessions returns session names that have lifecycle logs.
func ListLifecycleSessions() ([]string, error) {
	logDir := LifecycleLogDir()
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".log") {
			sessions = append(sessions, strings.TrimSuffix(name, ".log"))
		}
	}
	return sessions, nil
}

// PurgeLifecycleLogs removes log files older than maxAgeDays.
// Returns the number of files removed.
func PurgeLifecycleLogs(maxAgeDays int) (int, error) {
	logDir := LifecycleLogDir()
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	removed := 0

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(logDir, e.Name())
			if os.Remove(path) == nil {
				removed++
			}
		}
	}

	return removed, nil
}

// FormatLifecycleEntry formats an entry as a human-readable line.
func FormatLifecycleEntry(e LifecycleEntry) string {
	ts := time.Unix(e.TS, 0).Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("%s  %-5s  %-13s  %-22s", ts, e.Level, e.Source, e.Event)
	if e.PID > 0 {
		line += fmt.Sprintf("  pid:%d", e.PID)
	}
	if e.Detail != "" {
		line += "  " + e.Detail
	}
	return line
}

// parseLifecycleEntries parses JSONL data into lifecycle entries.
func parseLifecycleEntries(data []byte) []LifecycleEntry {
	var entries []LifecycleEntry
	for _, line := range splitLifecycleLines(data) {
		var e LifecycleEntry
		if json.Unmarshal(line, &e) == nil {
			entries = append(entries, e)
		}
	}
	return entries
}

// splitLifecycleLines splits data into non-empty lines.
func splitLifecycleLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		line := data[start:]
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

// lifecycleAuditEvents name the rows an authorization decision reads back, so
// rotation may not evict them.
//
// gateApprovalHolds refuses an approval whose graph-gate-approved row is
// missing and treats it as forged. That made a rotating debug log an input to a
// live decision, and rotation runs in whichever process appends, under a cap
// that process supplies: one low-valued append from anything on the box
// (`MUXCODE_LIFECYCLE_LOG_MAX=1 muxcode …`) invalidated every outstanding
// approval and erased the record of it happening, in one move. Preserving these
// rows is what keeps the audit horizon out of a caller's hands (MUX-144).
var lifecycleAuditEvents = map[string]bool{"graph-gate-approved": true}

// rotateLifecycleLog truncates the log file to keep the last maxEntries lines,
// plus every older audit row. Audit rows cannot be capped by count: an older
// row may still be the evidence for a pending approval.
func rotateLifecycleLog(path string, maxEntries int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := splitLifecycleLines(data)
	if len(lines) <= maxEntries {
		return
	}

	cut := len(lines) - maxEntries
	keep := append(retainAuditLines(lines[:cut]), lines[cut:]...)
	var out []byte
	for _, line := range keep {
		out = append(out, line...)
		out = append(out, '\n')
	}

	_ = os.WriteFile(path, out, 0644)
}

// retainAuditLines returns every audit row among evicted lines. These rows are
// authorization evidence, so retaining only the newest max rows could evict
// evidence for an older approval that is still pending.
func retainAuditLines(lines [][]byte) [][]byte {
	kept := [][]byte{}
	for _, line := range lines {
		var e struct {
			Event string `json:"event"`
		}
		if json.Unmarshal(line, &e) == nil && lifecycleAuditEvents[e.Event] {
			kept = append(kept, line)
		}
	}
	return kept
}
