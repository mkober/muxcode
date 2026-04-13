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

// DaemonKeepalivePath returns the path to the daemon keepalive timestamp file.
func DaemonKeepalivePath(session string) string {
	return filepath.Join(BusDir(session), "daemon.keepalive")
}

// TouchKeepaliveDaemon writes the current unix timestamp to the keepalive file.
func TouchKeepaliveDaemon(session string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	_ = os.WriteFile(DaemonKeepalivePath(session), []byte(ts), 0644)
}

// IsDaemonAlive reads the keepalive file and returns true if the timestamp
// is within maxAgeSecs of the current time.
func IsDaemonAlive(session string, maxAgeSecs int64) bool {
	data, err := os.ReadFile(DaemonKeepalivePath(session))
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

// RestartDaemon kills any existing daemon process for the session and starts
// a new one in the background.
func RestartDaemon(session string) error {
	// Kill existing daemon
	killPattern := fmt.Sprintf("muxcode watch %s", session)
	killCmd := exec.Command("pkill", "-f", killPattern)
	_ = killCmd.Run() // ignore error if no process found

	// Wait briefly for process to exit
	time.Sleep(200 * time.Millisecond)

	// Start new daemon in background
	cmd := exec.Command("muxcode", "watch", session)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting daemon: %w", err)
	}
	// Detach — don't wait for it
	go func() { _ = cmd.Wait() }()

	return nil
}
