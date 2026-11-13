package bus

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestInit_CreatesStructure(t *testing.T) {
	useTempBusDir(t)

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
	useTempBusDir(t)

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

func TestInit_ReInit_PurgesStaleData(t *testing.T) {
	useTempBusDir(t)

	session := fmt.Sprintf("test-reinit-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	// First init
	if err := Init(session, memDir); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	// Write stale data to simulate a previous session
	staleData := []byte(`{"id":"old","from":"build","to":"edit","type":"response","action":"build","payload":"old build result","ts":1000}` + "\n")
	if err := os.WriteFile(LogPath(session), staleData, 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.WriteFile(InboxPath(session, "edit"), staleData, 0644); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
	if err := os.WriteFile(HistoryPath(session, "build"), staleData, 0644); err != nil {
		t.Fatalf("write history: %v", err)
	}
	if err := os.WriteFile(CronHistoryPath(session), staleData, 0644); err != nil {
		t.Fatalf("write cron history: %v", err)
	}

	// Write a session meta file
	sessionMetaPath := filepath.Join(BusDir(session), "session", "edit.json")
	if err := os.WriteFile(sessionMetaPath, []byte(`{"start_ts":1000}`), 0644); err != nil {
		t.Fatalf("write session meta: %v", err)
	}

	// Write a lock file
	lockFile := LockPath(session, "build")
	if err := os.WriteFile(lockFile, []byte("locked"), 0644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	// Write an orphaned spawn inbox file
	spawnInbox := filepath.Join(BusDir(session), "inbox", "spawn-abc123.jsonl")
	if err := os.WriteFile(spawnInbox, staleData, 0644); err != nil {
		t.Fatalf("write spawn inbox: %v", err)
	}

	const spec = "docs/requirements/drafts/MUX-168-example.md"
	if err := WriteActiveSpec(session, spec); err != nil {
		t.Fatalf("write active spec: %v", err)
	}

	// Re-init should purge stale data
	if err := Init(session, memDir); err != nil {
		t.Fatalf("re-Init: %v", err)
	}

	// Log file should be empty
	data, err := os.ReadFile(LogPath(session))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("log should be empty after re-init, got %d bytes", len(data))
	}

	// Inbox should be empty
	data, err = os.ReadFile(InboxPath(session, "edit"))
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("inbox should be empty after re-init, got %d bytes", len(data))
	}

	// History should be empty
	data, err = os.ReadFile(HistoryPath(session, "build"))
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("history should be empty after re-init, got %d bytes", len(data))
	}

	// Cron history should be empty
	data, err = os.ReadFile(CronHistoryPath(session))
	if err != nil {
		t.Fatalf("read cron history: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("cron history should be empty after re-init, got %d bytes", len(data))
	}

	// Session meta should be removed
	if _, err := os.Stat(sessionMetaPath); !os.IsNotExist(err) {
		t.Errorf("session meta should be removed after re-init")
	}

	// Lock file should be removed
	if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
		t.Errorf("lock file should be removed after re-init")
	}

	// Spawn inbox should be removed
	if _, err := os.Stat(spawnInbox); !os.IsNotExist(err) {
		t.Errorf("spawn inbox should be removed after re-init")
	}

	if got := ReadActiveSpec(session); got != spec {
		t.Errorf("active spec after re-init = %q, want %q kept (MUX-168)", got, spec)
	}
	if detail, ok := lastLifecycleDetail(t, session, "spec-kept"); !ok || detail != spec+" live-runs=0" {
		t.Errorf("spec-kept row = %q (found %v), want %q", detail, ok, spec+" live-runs=0")
	}

	// Known role inboxes should still exist (truncated, not removed)
	for _, role := range KnownRoles {
		if _, err := os.Stat(InboxPath(session, role)); err != nil {
			t.Errorf("inbox for %s should still exist: %v", role, err)
		}
	}

	// Structure should still be intact
	busDir := BusDir(session)
	if _, err := os.Stat(filepath.Join(busDir, "inbox")); err != nil {
		t.Errorf("inbox dir should still exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(busDir, "lock")); err != nil {
		t.Errorf("lock dir should still exist: %v", err)
	}
}

// lastLifecycleDetail returns the detail of the session's last lifecycle row
// carrying event.
func lastLifecycleDetail(t *testing.T, session, event string) (string, bool) {
	t.Helper()
	entries, err := ReadLifecycleLog(session)
	if err != nil {
		t.Fatalf("read lifecycle: %v", err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Event == event {
			return entries[i].Detail, true
		}
	}
	return "", false
}

func TestInit_ReInit_CountsLiveRunsBesideKeptSpec(t *testing.T) {
	useTempBusDir(t)

	session := fmt.Sprintf("test-reinit-liveruns-%d", rand.Int())
	t.Cleanup(func() { _ = Cleanup(session) })
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	const spec = "docs/requirements/drafts/MUX-168-example.md"
	if err := WriteActiveSpec(session, spec); err != nil {
		t.Fatalf("write active spec: %v", err)
	}
	if _, err := CreateGraphRun(session, linearGraph(), "t", ""); err != nil {
		t.Fatal(err)
	}
	done, err := CreateGraphRun(session, linearGraph(), "t", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateGraphRunState(session, done.ID, GraphRunComplete); err != nil {
		t.Fatal(err)
	}

	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("re-Init: %v", err)
	}

	if got := ReadActiveSpec(session); got != spec {
		t.Errorf("active spec after re-init = %q, want %q", got, spec)
	}
	if detail, _ := lastLifecycleDetail(t, session, "spec-kept"); detail != spec+" live-runs=1" {
		t.Errorf("spec-kept detail = %q, want %q", detail, spec+" live-runs=1")
	}
}

// Negative controls: re-init preserves a pointer, it never invents one.
func TestInit_ReInit_NoPointerStaysUnset(t *testing.T) {
	useTempBusDir(t)

	session := fmt.Sprintf("test-reinit-nospec-%d", rand.Int())
	t.Cleanup(func() { _ = Cleanup(session) })
	for i := 0; i < 2; i++ {
		if err := Init(session, t.TempDir()); err != nil {
			t.Fatalf("Init %d: %v", i, err)
		}
	}

	if _, err := os.Stat(ActiveSpecPath(session)); !os.IsNotExist(err) {
		t.Errorf("re-init created an active-spec marker (stat err %v)", err)
	}
	if _, ok := lastLifecycleDetail(t, session, "spec-none"); !ok {
		t.Error("re-init with no pointer wrote no spec-none row")
	}
	if detail, ok := lastLifecycleDetail(t, session, "spec-kept"); ok {
		t.Errorf("re-init with no pointer wrote spec-kept %q", detail)
	}
}

func TestInit_ReInit_HonoursClearBeforeIt(t *testing.T) {
	useTempBusDir(t)

	session := fmt.Sprintf("test-reinit-cleared-%d", rand.Int())
	t.Cleanup(func() { _ = Cleanup(session) })
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := WriteActiveSpec(session, "docs/requirements/drafts/MUX-168-example.md"); err != nil {
		t.Fatalf("write active spec: %v", err)
	}
	if err := ClearActiveSpec(session); err != nil {
		t.Fatalf("clear active spec: %v", err)
	}

	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("re-Init: %v", err)
	}

	if got := ReadActiveSpec(session); got != "" {
		t.Errorf("active spec after clear + re-init = %q, want empty (re-init must not resurrect)", got)
	}
}

func TestLogSpecPointerChange_Actor(t *testing.T) {
	useTempBusDir(t)

	cases := []struct {
		name, busRole, path, want string
	}{
		{"bus role names the actor", "plan", "docs/requirements/drafts/MUX-168-x.md", "actor=plan path=docs/requirements/drafts/MUX-168-x.md"},
		{"no role is the user", "", "docs/requirements/drafts/MUX-168-x.md", "actor=user path=docs/requirements/drafts/MUX-168-x.md"},
		{"empty path reads none", "", "", "actor=user path=none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_ROLE", "")
			t.Setenv("BUS_ROLE", tc.busRole)
			session := fmt.Sprintf("test-spec-row-%d", rand.Int())
			LogSpecPointerChange(session, "spec-set", tc.path, "")
			if detail, ok := lastLifecycleDetail(t, session, "spec-set"); !ok || detail != tc.want {
				t.Errorf("spec-set detail = %q (found %v), want %q", detail, ok, tc.want)
			}
		})
	}
}

func TestInit_ReInit_PreservesMemory(t *testing.T) {
	useTempBusDir(t)

	session := fmt.Sprintf("test-reinit-mem-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	// First init
	if err := Init(session, memDir); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	// Write to shared memory (should survive re-init)
	sharedPath := filepath.Join(memDir, "shared.md")
	if err := os.WriteFile(sharedPath, []byte("# Important learnings\n"), 0644); err != nil {
		t.Fatalf("write shared.md: %v", err)
	}

	// Re-init
	if err := Init(session, memDir); err != nil {
		t.Fatalf("re-Init: %v", err)
	}

	// Memory should be preserved
	data, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("read shared.md: %v", err)
	}
	if string(data) != "# Important learnings\n" {
		t.Errorf("shared.md should be preserved, got: %q", string(data))
	}
}
