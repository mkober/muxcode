package bus

import (
	"os"
	"path/filepath"
	"strings"
)

// Init creates the bus directory structure and initializes files.
// If the bus directory already exists from a previous session, stale data
// files (inboxes, log, history, cron, proc, spawn, session meta) are
// truncated so the watcher doesn't fire alerts based on old data.
func Init(session, memoryDir string) error {
	busDir := BusDir(session)

	// Detect re-init: if the bus dir already exists, purge stale data
	reInit := false
	if _, err := os.Stat(busDir); err == nil {
		reInit = true
	}

	// Create inbox, lock, and session directories
	if err := os.MkdirAll(filepath.Join(busDir, "inbox"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(busDir, "lock"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(busDir, "session"), 0755); err != nil {
		return err
	}

	// Create (or truncate on re-init) inbox files for all known roles
	for _, role := range KnownRoles {
		if err := resetFile(InboxPath(session, role), reInit); err != nil {
			return err
		}
	}

	// Create (or truncate) log file
	if err := resetFile(LogPath(session), reInit); err != nil {
		return err
	}

	// Create (or truncate) cron file
	if err := resetFile(CronPath(session), reInit); err != nil {
		return err
	}

	// Create proc directory and reset proc.jsonl
	if err := os.MkdirAll(ProcDir(session), 0755); err != nil {
		return err
	}
	if err := resetFile(ProcPath(session), reInit); err != nil {
		return err
	}

	// Create (or truncate) spawn.jsonl
	if err := resetFile(SpawnPath(session), reInit); err != nil {
		return err
	}

	// Create (or truncate) subscriptions.jsonl
	if err := resetFile(SubscriptionPath(session), reInit); err != nil {
		return err
	}

	// On re-init, also purge history files and session meta
	if reInit {
		if err := purgeStaleFiles(session); err != nil {
			return err
		}
	}

	// Create memory directory and shared.md if not exists
	if memoryDir == "" {
		memoryDir = MemoryDir()
	}
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return err
	}
	sharedPath := filepath.Join(memoryDir, "shared.md")
	if _, err := os.Stat(sharedPath); os.IsNotExist(err) {
		if err := touchFile(sharedPath); err != nil {
			return err
		}
	}

	return nil
}

// resetFile creates a file if it doesn't exist, or truncates it if truncate is true.
func resetFile(path string, truncate bool) error {
	flags := os.O_CREATE | os.O_WRONLY
	if truncate {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

// purgeStaleFiles removes ephemeral session data left over from a previous session.
// Strategy: files that will be recreated (inboxes, history) are truncated so the
// path still exists for writers. One-off artifacts (session meta, locks, proc logs,
// spawn inboxes) are removed outright since they won't be recreated until needed.
func purgeStaleFiles(session string) error {
	busDir := BusDir(session)

	// Truncate per-role history files ({role}-history.jsonl)
	for _, role := range KnownRoles {
		hp := HistoryPath(session, role)
		if _, err := os.Stat(hp); err == nil {
			if err := os.Truncate(hp, 0); err != nil {
				return err
			}
		}
	}

	// Remove all session meta files
	sessionDir := filepath.Join(busDir, "session")
	entries, err := os.ReadDir(sessionDir)
	if err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(sessionDir, e.Name()))
		}
	}

	// Remove all lock files
	lockDir := filepath.Join(busDir, "lock")
	entries, err = os.ReadDir(lockDir)
	if err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(lockDir, e.Name()))
		}
	}

	// Remove orphaned spawn inbox files (inbox/spawn-*.jsonl)
	inboxDir := filepath.Join(busDir, "inbox")
	entries, err = os.ReadDir(inboxDir)
	if err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "spawn-") {
				_ = os.Remove(filepath.Join(inboxDir, e.Name()))
			}
		}
	}

	// Truncate cron history
	cronHist := CronHistoryPath(session)
	if _, err := os.Stat(cronHist); err == nil {
		if err := os.Truncate(cronHist, 0); err != nil {
			return err
		}
	}

	// Clean proc logs
	procDir := ProcDir(session)
	entries, err = os.ReadDir(procDir)
	if err == nil {
		for _, e := range entries {
			if e.Name() != "proc.jsonl" {
				_ = os.Remove(filepath.Join(procDir, e.Name()))
			}
		}
	}

	// Remove trigger file
	_ = os.Remove(TriggerFile(session))

	// Remove webhook PID file
	_ = os.Remove(WebhookPidPath(session))

	// Remove harness marker PID files (harness-*.pid) and notify dedup markers (notified-*.size)
	entries, err = os.ReadDir(busDir)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, "harness-") && strings.HasSuffix(name, ".pid") {
				_ = os.Remove(filepath.Join(busDir, name))
			}
			if strings.HasPrefix(name, "notified-") && strings.HasSuffix(name, ".size") {
				_ = os.Remove(filepath.Join(busDir, name))
			}
		}
	}

	// Remove Ollama health state file
	_ = os.Remove(OllamaHealthPath(session))

	// Remove Ollama failure sentinels (lock/*.ollama-fail)
	// Already handled by the lock dir cleanup above, but explicit for clarity

	return nil
}
