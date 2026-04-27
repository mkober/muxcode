package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CleanupResult tracks what was found and removed during cleanup.
type CleanupResult struct {
	BusDirs      []string // /tmp/muxcode-bus-{session}/
	PreviewFiles []string // /tmp/muxcode-preview-{session}.tmp
	TriggerFiles []string // /tmp/muxcode-analyze-{session}.trigger
	SpawnDirs    []string // /tmp/muxcode-spawn-{session}/
	LogFiles     []string // /tmp/muxcode-log-*.txt
}

// ClaudeCleanupResult tracks Claude Code /tmp session cleanup.
type ClaudeCleanupResult struct {
	Sessions   []string // UUID session dirs removed
	BytesFreed int64    // total bytes reclaimed
}

// TotalItems returns the total number of items found for cleanup.
func (r *CleanupResult) TotalItems() int {
	return len(r.BusDirs) + len(r.PreviewFiles) + len(r.TriggerFiles) + len(r.SpawnDirs) + len(r.LogFiles)
}

// Cleanup removes the bus directory and trigger file for a session.
func Cleanup(session string) error {
	LogLifecycle(session, "info", "cleanup", "session-cleanup", BusDir(session))
	if err := os.RemoveAll(BusDir(session)); err != nil {
		return err
	}
	err := os.Remove(TriggerFile(session))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// CleanupStale finds and optionally removes temp artifacts from stale
// muxcode sessions (sessions whose tmux session no longer exists).
// When dryRun is true, items are collected but not removed.
// When includeActive is true, artifacts from the current session are included.
func CleanupStale(currentSession string, dryRun, includeActive bool) (*CleanupResult, error) {
	result := &CleanupResult{}
	tmpDir := "/tmp"
	if busDirOverride != "" {
		tmpDir = busDirOverride
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", tmpDir, err)
	}

	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(tmpDir, name)

		switch {
		case strings.HasPrefix(name, "muxcode-bus-") && e.IsDir():
			session := strings.TrimPrefix(name, "muxcode-bus-")
			if shouldClean(session, currentSession, includeActive) {
				result.BusDirs = append(result.BusDirs, full)
			}

		case strings.HasPrefix(name, "muxcode-preview-") && strings.HasSuffix(name, ".tmp"):
			session := strings.TrimPrefix(name, "muxcode-preview-")
			session = strings.TrimSuffix(session, ".tmp")
			if shouldClean(session, currentSession, includeActive) {
				result.PreviewFiles = append(result.PreviewFiles, full)
			}

		case strings.HasPrefix(name, "muxcode-analyze-") && strings.HasSuffix(name, ".trigger"):
			session := strings.TrimPrefix(name, "muxcode-analyze-")
			session = strings.TrimSuffix(session, ".trigger")
			if shouldClean(session, currentSession, includeActive) {
				result.TriggerFiles = append(result.TriggerFiles, full)
			}

		case strings.HasPrefix(name, "muxcode-spawn-") && e.IsDir():
			session := strings.TrimPrefix(name, "muxcode-spawn-")
			if shouldClean(session, currentSession, includeActive) {
				result.SpawnDirs = append(result.SpawnDirs, full)
			}

		case strings.HasPrefix(name, "muxcode-log-") && strings.HasSuffix(name, ".txt"):
			// Orphan log files don't have session names — always clean them
			// unless they're very recent (skip if modified in last 60 seconds
			// to avoid removing files still being written).
			if info, err := e.Info(); err == nil {
				result.LogFiles = append(result.LogFiles, full)
				_ = info
			}
		}
	}

	if dryRun {
		return result, nil
	}

	// Remove everything collected
	var errs []string
	for _, items := range [][]string{
		result.BusDirs, result.SpawnDirs,
	} {
		for _, path := range items {
			if err := os.RemoveAll(path); err != nil {
				errs = append(errs, fmt.Sprintf("remove %s: %v", path, err))
			}
		}
	}
	for _, items := range [][]string{
		result.PreviewFiles, result.TriggerFiles, result.LogFiles,
	} {
		for _, path := range items {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("remove %s: %v", path, err))
			}
		}
	}

	if len(errs) > 0 {
		return result, fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
	}
	return result, nil
}

// FormatCleanupResult returns a human-readable summary of cleanup results.
func FormatCleanupResult(r *CleanupResult, dryRun bool) string {
	if r.TotalItems() == 0 {
		return "Nothing to clean up"
	}

	var b strings.Builder
	verb := "Removed"
	if dryRun {
		verb = "Would remove"
	}

	if len(r.BusDirs) > 0 {
		fmt.Fprintf(&b, "%s %d bus directory(s):\n", verb, len(r.BusDirs))
		for _, p := range r.BusDirs {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	if len(r.PreviewFiles) > 0 {
		fmt.Fprintf(&b, "%s %d preview file(s):\n", verb, len(r.PreviewFiles))
		for _, p := range r.PreviewFiles {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	if len(r.TriggerFiles) > 0 {
		fmt.Fprintf(&b, "%s %d trigger file(s):\n", verb, len(r.TriggerFiles))
		for _, p := range r.TriggerFiles {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	if len(r.SpawnDirs) > 0 {
		fmt.Fprintf(&b, "%s %d spawn directory(s):\n", verb, len(r.SpawnDirs))
		for _, p := range r.SpawnDirs {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	if len(r.LogFiles) > 0 {
		fmt.Fprintf(&b, "%s %d log file(s):\n", verb, len(r.LogFiles))
		for _, p := range r.LogFiles {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}

	total := r.TotalItems()
	fmt.Fprintf(&b, "\nTotal: %d item(s)", total)
	return b.String()
}

// shouldClean returns true if artifacts for the given session should be removed.
// A session is stale if its tmux session no longer exists.
// If includeActive is true, even active sessions are cleaned.
func shouldClean(session, currentSession string, includeActive bool) bool {
	if !includeActive && session == currentSession {
		return false
	}
	return !isTmuxSessionAlive(session)
}

// isTmuxSessionAlive checks if a tmux session exists.
func isTmuxSessionAlive(session string) bool {
	return exec.Command("tmux", "has-session", "-t", session).Run() == nil
}

// CleanupClaudeTmp finds and removes stale Claude Code session directories
// under /tmp/claude-*/. Each /tmp/claude-{UID}/ contains path-encoded project
// subdirs (e.g. -Users-mkoberlein-Repos-...) with UUID session dirs inside.
// Sessions older than maxAge are removed. When dryRun is true, items are
// collected but not deleted.
func CleanupClaudeTmp(maxAge time.Duration, dryRun bool) (*ClaudeCleanupResult, error) {
	result := &ClaudeCleanupResult{}
	cutoff := time.Now().Add(-maxAge)

	tmpDir := "/tmp"
	if busDirOverride != "" {
		tmpDir = busDirOverride
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", tmpDir, err)
	}

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "claude-") {
			continue
		}
		claudeDir := filepath.Join(tmpDir, e.Name())
		if err := cleanupClaudeDir(claudeDir, cutoff, dryRun, result); err != nil {
			// Log but continue — don't fail entire cleanup for one dir
			fmt.Fprintf(os.Stderr, "Warning: cleaning %s: %v\n", claudeDir, err)
		}
	}

	return result, nil
}

// cleanupClaudeDir processes a single /tmp/claude-{UID}/ directory.
func cleanupClaudeDir(claudeDir string, cutoff time.Time, dryRun bool, result *ClaudeCleanupResult) error {
	projects, err := os.ReadDir(claudeDir)
	if err != nil {
		return err
	}

	for _, proj := range projects {
		if !proj.IsDir() {
			continue
		}
		projDir := filepath.Join(claudeDir, proj.Name())
		sessions, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}

		for _, sess := range sessions {
			if !sess.IsDir() || !isUUID(sess.Name()) {
				continue
			}
			sessDir := filepath.Join(projDir, sess.Name())
			info, err := sess.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				size := dirSize(sessDir)
				result.Sessions = append(result.Sessions, sessDir)
				result.BytesFreed += size
				if !dryRun {
					os.RemoveAll(sessDir)
				}
			}
		}

		// Remove empty project dirs after cleaning sessions
		if !dryRun {
			remaining, _ := os.ReadDir(projDir)
			if len(remaining) == 0 {
				os.Remove(projDir)
			}
		}
	}

	// Remove empty claude-{UID} dir after cleaning projects
	if !dryRun {
		remaining, _ := os.ReadDir(claudeDir)
		if len(remaining) == 0 {
			os.Remove(claudeDir)
		}
	}

	return nil
}

// FormatClaudeCleanupResult returns a human-readable summary.
func FormatClaudeCleanupResult(r *ClaudeCleanupResult, dryRun bool) string {
	if len(r.Sessions) == 0 {
		return "No stale Claude Code sessions found"
	}

	var b strings.Builder
	verb := "Removed"
	if dryRun {
		verb = "Would remove"
	}

	fmt.Fprintf(&b, "%s %d Claude Code session(s)", verb, len(r.Sessions))
	fmt.Fprintf(&b, ", %s freed", formatBytes(r.BytesFreed))

	if len(r.Sessions) <= 20 {
		b.WriteString(":\n")
		for _, s := range r.Sessions {
			fmt.Fprintf(&b, "  %s\n", s)
		}
	} else {
		b.WriteString("\n")
		// Show a summary by project dir
		projects := make(map[string]int)
		for _, s := range r.Sessions {
			proj := filepath.Dir(s)
			projects[proj]++
		}
		for proj, count := range projects {
			fmt.Fprintf(&b, "  %s: %d session(s)\n", filepath.Base(proj), count)
		}
	}
	return b.String()
}

// isUUID checks if a string looks like a UUID (8-4-4-4-12 hex pattern).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// dirSize calculates the total size of all files in a directory tree.
func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

// formatBytes is defined in compact.go — reused here for byte formatting.
