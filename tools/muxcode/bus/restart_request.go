package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Restart requests are how `upgrade-daemons` cycles a daemon or monitor it
// cannot run ps beside (MUX-161): it kills nothing and asks the process to
// re-exec itself. Live-checked on 2026-11-14 inside a Codex build sandbox: the
// kill road killed both, and the replacements it started there died with the
// sandbox, leaving the session with no daemon. A re-exec keeps the process —
// its pid, its place outside any sandbox, its arguments and environment.
//
// A request names nothing. The bus directory is writable by sandboxed roles,
// so a request that named a binary would let any of them run code as the
// unsandboxed daemon. The consumer re-execs the executable it was started
// from — the path `make install` overwrites with the new build — so a writer
// gains nothing it could not already do by replacing that file.

// DaemonRestartRequestPath is the request the session's daemon consumes.
func DaemonRestartRequestPath(session string) string {
	return filepath.Join(BusDir(session), "lock", "daemon.restart")
}

// MonitorRestartRequestPath is the request the session's monitor consumes.
func MonitorRestartRequestPath(session string) string {
	return filepath.Join(BusDir(session), "lock", "monitor.restart")
}

// RequestRestart asks the process consuming path to re-exec itself. The
// content is a note for a human reader; the consumer never reads it.
func RequestRestart(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	note := fmt.Sprintf("requested by pid %d at %s\n", os.Getpid(), time.Now().Format(time.RFC3339))
	return os.WriteFile(path, []byte(note), 0644)
}

// TakeRestartTarget consumes a pending request and returns what to re-exec:
// this process's own executable, never anything the request says.
func TakeRestartTarget(path string) (string, bool) {
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	_ = os.Remove(path)
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return exe, true
}

// ReexecForRestart replaces this process's image with binary, keeping its
// pid, arguments and environment. Descriptors are close-on-exec, so the
// instance and owner locks are released and the new image takes them again.
// It returns only on failure.
func ReexecForRestart(binary string) error {
	args := append([]string{binary}, os.Args[1:]...)
	return syscall.Exec(binary, args, os.Environ())
}
