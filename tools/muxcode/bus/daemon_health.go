package bus

import (
	"encoding/json"
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

// DaemonVersionPath returns the path of the file holding the build identity
// the session's daemon recorded at startup.
func DaemonVersionPath(session string) string {
	return filepath.Join(BusDir(session), "daemon.version")
}

// WriteDaemonVersion records info as the session daemon's build identity.
// The daemon calls it once after winning the instance lock, so an
// upgrade-daemons relaunch refreshes the file simply by starting the new
// process. The content is the same JSON object `muxcode version --json`
// prints.
func WriteDaemonVersion(session string, info Info) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(DaemonVersionPath(session), data, 0644)
}

// ReadDaemonVersion returns the build identity the session's daemon
// recorded. ok is false when nothing usable is on disk: no daemon has
// started for the session, or the running one predates the stamp and never
// wrote the file.
func ReadDaemonVersion(session string) (Info, bool) {
	data, err := os.ReadFile(DaemonVersionPath(session))
	if err != nil {
		return Info{}, false
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil || info.Version == "" {
		return Info{}, false
	}
	return info, true
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
