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

func TestDaemonVersionPath(t *testing.T) {
	if got := DaemonVersionPath("test-session"); got != "/tmp/muxcode-bus-test-session/daemon.version" {
		t.Errorf("unexpected version path: %s", got)
	}
}

func TestWriteReadDaemonVersion_RoundTrip(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "version-roundtrip"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}

	want := Info{Version: "v0.1.0-3-gabc1234", Commit: "abc1234", Date: "2026-09-02T12:00:00Z", GoVersion: "go1.22.5", OS: "darwin", Arch: "arm64"}
	if err := WriteDaemonVersion(session, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, ok := ReadDaemonVersion(session)
	if !ok {
		t.Fatal("expected a recorded version")
	}
	if got != want {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !got.SameBuild(want) {
		t.Error("round-tripped identity should compare as the same build")
	}
}

func TestReadDaemonVersion_Missing(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	if _, ok := ReadDaemonVersion("never-started"); ok {
		t.Error("expected ok=false with no version file")
	}
}

func TestReadDaemonVersion_Unusable(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "version-garbage"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"not json":      "v0.1.0",
		"empty version": `{"commit":"abc1234"}`,
	} {
		if err := os.WriteFile(DaemonVersionPath(session), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if _, ok := ReadDaemonVersion(session); ok {
			t.Errorf("%s: expected ok=false for %q", name, body)
		}
	}
}
