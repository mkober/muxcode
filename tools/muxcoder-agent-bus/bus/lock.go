package bus

import (
	"os"
	"path/filepath"
)

// Lock creates a lock file indicating the agent is busy.
func Lock(session, role string) error {
	lockDir := filepath.Dir(LockPath(session, role))
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(LockPath(session, role), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Unlock removes the lock file for an agent.
func Unlock(session, role string) error {
	err := os.Remove(LockPath(session, role))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsLocked returns true if the agent's lock file exists.
func IsLocked(session, role string) bool {
	_, err := os.Stat(LockPath(session, role))
	return err == nil
}
