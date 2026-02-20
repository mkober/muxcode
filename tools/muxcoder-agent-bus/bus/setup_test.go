package bus

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestInit_CreatesStructure(t *testing.T) {
	session := fmt.Sprintf("test-init-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	busDir := BusDir(session)

	// Inbox dir exists
	if _, err := os.Stat(filepath.Join(busDir, "inbox")); err != nil {
		t.Errorf("inbox dir: %v", err)
	}

	// Lock dir exists
	if _, err := os.Stat(filepath.Join(busDir, "lock")); err != nil {
		t.Errorf("lock dir: %v", err)
	}

	// Log file exists
	if _, err := os.Stat(LogPath(session)); err != nil {
		t.Errorf("log file: %v", err)
	}

	// Inbox files for all known roles
	for _, role := range KnownRoles {
		if _, err := os.Stat(InboxPath(session, role)); err != nil {
			t.Errorf("inbox for %s: %v", role, err)
		}
	}

	// Shared memory file
	if _, err := os.Stat(filepath.Join(memDir, "shared.md")); err != nil {
		t.Errorf("shared.md: %v", err)
	}
}

func TestInit_Idempotent(t *testing.T) {
	session := fmt.Sprintf("test-idem-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := Init(session, memDir); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}
