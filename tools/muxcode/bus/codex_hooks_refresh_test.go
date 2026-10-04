package bus

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// The 2026-10-02 loss: markers untouched since launch aged out of /tmp and
// build silently fell to the scrape road. A refresh must make an old marker
// recent again — and must never create one, since a marker's presence is
// what switches a role onto the hook road.
func TestRefreshCodexHooksMarkers(t *testing.T) {
	session := fmt.Sprintf("refresh-markers-%d", time.Now().UnixNano())
	t.Cleanup(func() { os.RemoveAll(BusDir(session)) })

	RefreshCodexHooksMarkers(session) // no marker dir yet: must not panic or create one
	if _, err := os.Stat(codexHooksMarkerDir(session)); !os.IsNotExist(err) {
		t.Fatalf("refresh created the marker dir: %v", err)
	}

	if err := os.MkdirAll(codexHooksMarkerDir(session), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := CodexHooksMarkerPath(session, "build")
	if err := os.WriteFile(marker, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * 24 * time.Hour)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}

	RefreshCodexHooksMarkers(session)

	info, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Errorf("marker mtime = %v, want refreshed to now", info.ModTime())
	}
	if !CodexHooksActive(session, "build") {
		t.Error("the refreshed role must stay on the hook road")
	}
	if CodexHooksActive(session, "review") {
		t.Error("negative control: refresh must not activate a role that had no marker")
	}
}
