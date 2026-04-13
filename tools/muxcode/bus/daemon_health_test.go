package bus

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestDaemonKeepalivePath(t *testing.T) {
	path := DaemonKeepalivePath("test-session")
	if path != "/tmp/muxcode-bus-test-session/daemon.keepalive" {
		t.Errorf("unexpected keepalive path: %s", path)
	}
}

func TestIsDaemonAlive_Fresh(t *testing.T) {
	tmpDir := t.TempDir()
	session := filepath.Base(tmpDir) // use tmpDir name as session

	// Create the keepalive file with a fresh timestamp
	busDir := "/tmp/muxcode-bus-" + session
	if err := os.MkdirAll(busDir, 0755); err != nil {
		t.Fatalf("failed to create bus dir: %v", err)
	}
	defer os.RemoveAll(busDir)

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	keepalivePath := filepath.Join(busDir, "daemon.keepalive")
	if err := os.WriteFile(keepalivePath, []byte(ts), 0644); err != nil {
		t.Fatalf("failed to write keepalive: %v", err)
	}

	if !IsDaemonAlive(session, 30) {
		t.Error("expected daemon to be alive with fresh timestamp")
	}
}

func TestIsDaemonAlive_Stale(t *testing.T) {
	tmpDir := t.TempDir()
	session := filepath.Base(tmpDir)

	busDir := "/tmp/muxcode-bus-" + session
	if err := os.MkdirAll(busDir, 0755); err != nil {
		t.Fatalf("failed to create bus dir: %v", err)
	}
	defer os.RemoveAll(busDir)

	// Write a stale timestamp (60 seconds ago)
	ts := strconv.FormatInt(time.Now().Unix()-60, 10)
	keepalivePath := filepath.Join(busDir, "daemon.keepalive")
	if err := os.WriteFile(keepalivePath, []byte(ts), 0644); err != nil {
		t.Fatalf("failed to write keepalive: %v", err)
	}

	if IsDaemonAlive(session, 30) {
		t.Error("expected daemon to be stale with 60s-old timestamp and 30s max age")
	}
}

func TestIsDaemonAlive_Missing(t *testing.T) {
	if IsDaemonAlive("nonexistent-session-xyz", 30) {
		t.Error("expected daemon to be not alive with missing keepalive file")
	}
}
