package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsHarnessActive_LivePID(t *testing.T) {
	dir := t.TempDir()
	session := "test-harness"
	role := "build"

	// Override BusDir for test by writing directly to expected path
	busDir := filepath.Join(dir, "muxcode-bus-"+session)
	os.MkdirAll(busDir, 0755)

	// Use our own PID (guaranteed alive)
	pid := os.Getpid()
	markerPath := filepath.Join(busDir, "harness-"+role+".pid")
	os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", pid)), 0644)

	// Temporarily override the bus dir via env
	old := os.Getenv("MUXCODE_BUS_DIR")
	defer os.Setenv("MUXCODE_BUS_DIR", old)

	// IsHarnessActive uses HarnessMarkerPath which uses BusDir
	// We need to check with a session that maps to our temp dir
	// Instead, test the logic directly by writing to the real path
	got := IsHarnessActive(session, role)
	if got {
		// It will be false because BusDir doesn't point to our temp dir.
		// Let's test with the actual path directly instead.
		t.Log("Unexpectedly got true — BusDir matched")
	}

	// Test with actual BusDir path: create marker at the real location
	realDir := BusDir("test-notify-live")
	os.MkdirAll(realDir, 0755)
	defer os.RemoveAll(realDir)

	realMarker := HarnessMarkerPath("test-notify-live", role)
	os.WriteFile(realMarker, []byte(fmt.Sprintf("%d", pid)), 0644)

	if !IsHarnessActive("test-notify-live", role) {
		t.Error("IsHarnessActive should return true for live PID")
	}
}

func TestIsHarnessActive_MissingFile(t *testing.T) {
	// No marker file at all
	if IsHarnessActive("nonexistent-session-xyz", "build") {
		t.Error("IsHarnessActive should return false when marker file does not exist")
	}
}

func TestIsHarnessActive_StalePID(t *testing.T) {
	session := "test-notify-stale"
	role := "review"

	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	markerPath := HarnessMarkerPath(session, role)

	// Write a PID that is almost certainly dead (very high number)
	os.WriteFile(markerPath, []byte("999999999"), 0644)

	if IsHarnessActive(session, role) {
		t.Error("IsHarnessActive should return false for dead PID")
	}

	// Verify stale marker was cleaned up
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("stale marker file should have been removed")
	}
}

func TestIsHarnessActive_InvalidContent(t *testing.T) {
	session := "test-notify-invalid"
	role := "commit"

	busDir := BusDir(session)
	os.MkdirAll(busDir, 0755)
	defer os.RemoveAll(busDir)

	markerPath := HarnessMarkerPath(session, role)

	// Write garbage content
	os.WriteFile(markerPath, []byte("not-a-pid"), 0644)

	if IsHarnessActive(session, role) {
		t.Error("IsHarnessActive should return false for invalid PID content")
	}

	// Verify invalid marker was cleaned up
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("invalid marker file should have been removed")
	}
}

func TestAlreadyNotified_NoMarker(t *testing.T) {
	session := "test-dedup-nomarker"
	role := "build"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	defer os.RemoveAll(busDir)

	// Write a message to the inbox
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)

	// No notified marker yet — should not be considered already notified
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when no marker exists")
	}
}

func TestAlreadyNotified_SameSize(t *testing.T) {
	session := "test-dedup-same"
	role := "test"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	defer os.RemoveAll(busDir)

	// Write a message to the inbox
	inboxData := []byte(`{"from":"edit"}` + "\n")
	os.WriteFile(InboxPath(session, role), inboxData, 0644)

	// Mark as notified
	markNotified(session, role)

	// Same size — should be deduplicated
	if !alreadyNotified(session, role) {
		t.Error("alreadyNotified should return true when inbox size matches marker")
	}
}

func TestAlreadyNotified_DifferentSize(t *testing.T) {
	session := "test-dedup-diff"
	role := "review"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	defer os.RemoveAll(busDir)

	// Write initial message and mark notified
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)
	markNotified(session, role)

	// Backdate marker beyond the cooldown window so the size change is detected
	markerPath := notifiedSizePath(session, role)
	past := time.Now().Add(-3 * time.Second)
	os.Chtimes(markerPath, past, past)

	// Add a second message — inbox grew
	f, _ := os.OpenFile(InboxPath(session, role), os.O_APPEND|os.O_WRONLY, 0644)
	f.Write([]byte(`{"from":"build"}` + "\n"))
	f.Close()

	// Inbox changed and cooldown expired — should NOT be considered already notified
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when inbox grew since last notification")
	}
}

func TestAlreadyNotified_EmptyInbox(t *testing.T) {
	session := "test-dedup-empty"
	role := "deploy"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	defer os.RemoveAll(busDir)

	// Empty inbox — nothing to notify
	os.WriteFile(InboxPath(session, role), []byte{}, 0644)

	if !alreadyNotified(session, role) {
		t.Error("alreadyNotified should return true for empty inbox")
	}
}

func TestMarkNotified_WritesSize(t *testing.T) {
	session := "test-dedup-mark"
	role := "commit"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	defer os.RemoveAll(busDir)

	// Write some data
	data := []byte(`{"from":"edit","action":"commit"}` + "\n")
	os.WriteFile(InboxPath(session, role), data, 0644)

	markNotified(session, role)

	// Verify marker file was created with correct size
	markerData, err := os.ReadFile(notifiedSizePath(session, role))
	if err != nil {
		t.Fatalf("markNotified should create marker file: %v", err)
	}

	expected := fmt.Sprintf("%d", len(data))
	if string(markerData) != expected {
		t.Errorf("marker size = %q, want %q", string(markerData), expected)
	}
}

func TestAlreadyNotified_Cooldown(t *testing.T) {
	session := "test-dedup-cooldown"
	role := "build"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	defer os.RemoveAll(busDir)

	// Write initial message and mark notified
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)
	markNotified(session, role)

	// Grow the inbox — size now differs from marker
	f, _ := os.OpenFile(InboxPath(session, role), os.O_APPEND|os.O_WRONLY, 0644)
	f.Write([]byte(`{"from":"build"}` + "\n"))
	f.Close()

	// Marker was just written (within cooldown) — should still be suppressed
	if !alreadyNotified(session, role) {
		t.Error("alreadyNotified should return true within cooldown window even when inbox size differs")
	}
}

func TestAlreadyNotified_CooldownExpired(t *testing.T) {
	session := "test-dedup-cooldown-exp"
	role := "test"

	busDir := BusDir(session)
	os.MkdirAll(filepath.Join(busDir, "inbox"), 0755)
	defer os.RemoveAll(busDir)

	// Write initial message and mark notified
	os.WriteFile(InboxPath(session, role), []byte(`{"from":"edit"}`+"\n"), 0644)
	markNotified(session, role)

	// Backdate the marker file mtime to exceed the cooldown
	markerPath := notifiedSizePath(session, role)
	past := time.Now().Add(-3 * time.Second)
	os.Chtimes(markerPath, past, past)

	// Grow the inbox
	f, _ := os.OpenFile(InboxPath(session, role), os.O_APPEND|os.O_WRONLY, 0644)
	f.Write([]byte(`{"from":"review"}` + "\n"))
	f.Close()

	// Cooldown expired and size differs — should allow notification
	if alreadyNotified(session, role) {
		t.Error("alreadyNotified should return false when cooldown has expired and inbox size differs")
	}
}
