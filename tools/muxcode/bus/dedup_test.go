package bus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsDuplicateMessage_NoDuplicate(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a message to the log
	m1 := NewMessage("build", "edit", "event", "notify", "Build succeeded", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Different (from, to, action, type) — not a duplicate
	m2 := NewMessage("test", "edit", "event", "notify", "Tests passed", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("different from should not be duplicate")
	}

	m3 := NewMessage("build", "review", "event", "notify", "Build succeeded", "")
	if IsDuplicateMessage(session, m3) {
		t.Error("different to should not be duplicate")
	}

	m4 := NewMessage("build", "edit", "request", "review", "Review please", "")
	if IsDuplicateMessage(session, m4) {
		t.Error("different action/type should not be duplicate")
	}
}

func TestIsDuplicateMessage_Duplicate(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a request message to the log (responses are exempt from dedup)
	m1 := NewMessage("edit", "build", "request", "build", "Run build", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Same (from, to, action, type) — is a duplicate
	m2 := NewMessage("edit", "build", "request", "build", "Run build again", "")
	if !IsDuplicateMessage(session, m2) {
		t.Error("same from/to/action/type within window should be duplicate")
	}
}

func TestIsDuplicateMessage_ResponseExcluded(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a response to the log
	m1 := NewMessage("test", "edit", "response", "test", "Tests passed", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Same tuple — should NOT be deduped because responses are exempt
	m2 := NewMessage("test", "edit", "response", "test", "Tests passed again", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("response messages should never be deduped")
	}
}

func TestIsDuplicateMessage_ExpiredWindow(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a message with timestamp outside the dedup window
	m1 := Message{
		ID:      "old-msg",
		TS:      time.Now().Unix() - 60, // 60s ago, outside 30s default window
		From:    "review",
		To:      "test",
		Type:    "response",
		Action:  "review-complete",
		Payload: "LGTM",
	}
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Same tuple but original is expired — not a duplicate
	m2 := NewMessage("review", "test", "response", "review-complete", "LGTM", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("expired message should not count as duplicate")
	}
}

func TestIsDuplicateMessage_SystemActionExcluded(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	// Write a system action to the log
	m1 := NewMessage("watcher", "edit", "event", "loop-detected", "test<->review loop", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	// Same system action — should NOT be deduped
	m2 := NewMessage("watcher", "edit", "event", "loop-detected", "another loop", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("system actions should never be deduped")
	}
}

func TestIsDuplicateMessage_DisabledByEnv(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")

	session := "test-dedup"
	logDir := filepath.Dir(LogPath(session))
	os.MkdirAll(logDir, 0755)

	m1 := NewMessage("review", "test", "response", "review-complete", "LGTM", "")
	data, _ := EncodeMessage(m1)
	appendToFile(LogPath(session), append(data, '\n'))

	m2 := NewMessage("review", "test", "response", "review-complete", "LGTM", "")
	if IsDuplicateMessage(session, m2) {
		t.Error("dedup should be disabled when window is 0")
	}
}

func TestIsDuplicateMessage_EmptyLog(t *testing.T) {
	dir := t.TempDir()
	old := busDirOverride
	busDirOverride = dir
	defer func() { busDirOverride = old }()

	session := "test-dedup"
	// No log file exists

	m := NewMessage("review", "test", "response", "review-complete", "LGTM", "")
	if IsDuplicateMessage(session, m) {
		t.Error("no log file should mean no duplicate")
	}
}
