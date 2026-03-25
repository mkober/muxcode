package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WatcherKeepalivePath returns the path to the watcher keepalive timestamp file.
func WatcherKeepalivePath(session string) string {
	return filepath.Join(BusDir(session), "watcher.keepalive")
}

// TouchKeepalive writes the current unix timestamp to the keepalive file.
func TouchKeepalive(session string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	_ = os.WriteFile(WatcherKeepalivePath(session), []byte(ts), 0644)
}

// IsWatcherAlive reads the keepalive file and returns true if the timestamp
// is within maxAgeSecs of the current time.
func IsWatcherAlive(session string, maxAgeSecs int64) bool {
	data, err := os.ReadFile(WatcherKeepalivePath(session))
	if err != nil {
		return false
	}

	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return false
	}

	age := time.Now().Unix() - ts
	return age <= maxAgeSecs
}

// RestartWatcher kills any existing watcher process for the session and starts
// a new one in the background.
func RestartWatcher(session string) error {
	// Kill existing watcher
	killPattern := fmt.Sprintf("muxcode watch %s", session)
	killCmd := exec.Command("pkill", "-f", killPattern)
	_ = killCmd.Run() // ignore error if no process found

	// Wait briefly for process to exit
	time.Sleep(200 * time.Millisecond)

	// Start new watcher in background
	cmd := exec.Command("muxcode", "watch", session)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting watcher: %w", err)
	}
	// Detach — don't wait for it
	go func() { _ = cmd.Wait() }()

	return nil
}
