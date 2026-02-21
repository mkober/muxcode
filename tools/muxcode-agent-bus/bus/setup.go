package bus

import (
	"os"
	"path/filepath"
)

// Init creates the bus directory structure and initializes files.
func Init(session, memoryDir string) error {
	busDir := BusDir(session)

	// Create inbox and lock directories
	if err := os.MkdirAll(filepath.Join(busDir, "inbox"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(busDir, "lock"), 0755); err != nil {
		return err
	}

	// Touch inbox files for all known roles
	for _, role := range KnownRoles {
		if err := touchFile(InboxPath(session, role)); err != nil {
			return err
		}
	}

	// Touch log file
	if err := touchFile(LogPath(session)); err != nil {
		return err
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
