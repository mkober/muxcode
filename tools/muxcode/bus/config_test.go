package bus

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestBusDirNotUnderTmp pins MUX-208: with no test override and no
// MUXCODE_BUS_BASE, the bus base must be absolute and outside /tmp and
// os.TempDir(), because macOS's tmp_cleaner deletes files there untouched for
// three days — a graph run parked on a wait_human gate lost its graph.json.
// The HOME is a fixed path outside every temp root, since TestMain's own
// XDG_STATE_HOME lives under one; nothing is written there.
func TestBusDirNotUnderTmp(t *testing.T) {
	saved := busDirOverride
	ResetBusDirBase()
	t.Cleanup(func() { busDirOverride = saved })
	t.Setenv(BusDirBaseEnv, "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/muxcode-test-home")

	base := BusDirBase()
	if !filepath.IsAbs(base) {
		t.Fatalf("BusDirBase() = %q, want an absolute path", base)
	}
	for _, root := range tmpRoots() {
		if pathWithin(base, root) {
			t.Errorf("BusDirBase() = %q lies under %q, which the OS tmp cleaner sweeps", base, root)
		}
	}
}

// TestDefaultBusBase_IsolatedFromRealState pins TestMain's XDG_STATE_HOME: a
// test that resolves a bus directory without SetBusDirBase must land in the
// run's temp root, never the user's real ~/.local/state/muxcode (MUX-208, the
// MUX-137 leak moved with the base). Removing the pin fails this test.
func TestDefaultBusBase_IsolatedFromRealState(t *testing.T) {
	saved := busDirOverride
	ResetBusDirBase()
	t.Cleanup(func() { busDirOverride = saved })
	t.Setenv(BusDirBaseEnv, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home to leak into")
	}
	realState := filepath.Join(home, ".local", "state", "muxcode")
	if base := BusDirBase(); pathWithin(base, realState) {
		t.Errorf("BusDirBase() = %q is the real state directory; TestMain must pin XDG_STATE_HOME", base)
	}
	if base := SpawnWorktreeBase("s"); pathWithin(base, realState) {
		t.Errorf("SpawnWorktreeBase = %q is in the real state directory", base)
	}
}

// TestStateDir_Resolution: an absolute XDG_STATE_HOME wins, a relative one is
// ignored for ~/.local/state, and the bus base sits beneath the state root.
func TestStateDir_Resolution(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)

	t.Setenv("XDG_STATE_HOME", xdg)
	if got, want := StateDir(), filepath.Join(xdg, "muxcode"); got != want {
		t.Errorf("absolute XDG_STATE_HOME: StateDir() = %q, want %q", got, want)
	}

	t.Setenv("XDG_STATE_HOME", "relative/state")
	if got, want := StateDir(), filepath.Join(home, ".local", "state", "muxcode"); got != want {
		t.Errorf("relative XDG_STATE_HOME: StateDir() = %q, want %q", got, want)
	}

	saved := busDirOverride
	ResetBusDirBase()
	t.Cleanup(func() { busDirOverride = saved })
	t.Setenv(BusDirBaseEnv, "")
	if got, want := BusDir("s"), filepath.Join(home, ".local", "state", "muxcode", "bus", "muxcode-bus-s"); got != want {
		t.Errorf("BusDir = %q, want %q", got, want)
	}
}

// TestStateDir_NoHomePanics: an unresolvable home fails loudly instead of
// falling back to /tmp.
func TestStateDir_NoHomePanics(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	defer func() {
		if recover() == nil {
			t.Error("StateDir() with no HOME returned instead of panicking")
		}
	}()
	t.Errorf("StateDir() = %q with no HOME", StateDir())
}

// TestSpawnWorktreeBase_OutOfTemp: worktrees live under the state root by
// default and beside the bus when the base is overridden — never os.TempDir().
func TestSpawnWorktreeBase_OutOfTemp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv(BusDirBaseEnv, "")
	saved := busDirOverride
	ResetBusDirBase()
	t.Cleanup(func() { busDirOverride = saved })

	if got, want := SpawnWorktreeBase("s"), filepath.Join(home, ".local", "state", "muxcode", "spawn", "s"); got != want {
		t.Errorf("default SpawnWorktreeBase = %q, want %q", got, want)
	}

	base := t.TempDir()
	SetBusDirBase(base)
	if got, want := SpawnWorktreeBase("s"), filepath.Join(base, "muxcode-spawn-s"); got != want {
		t.Errorf("overridden SpawnWorktreeBase = %q, want %q", got, want)
	}
}

// TestMigrateBusDirAt_MovesOnce: a legacy directory moves whole, logs one
// bus-dir-migrated row, and a second pass finds nothing to move.
func TestMigrateBusDirAt_MovesOnce(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	oldDir := filepath.Join(t.TempDir(), "muxcode-bus-mig")
	base := filepath.Join(t.TempDir(), "state", "bus")
	writeFile(t, filepath.Join(oldDir, "inbox", "edit.jsonl"), `{"id":"1"}`+"\n")
	writeFile(t, filepath.Join(oldDir, "graphs", "run1", "graph.json"), `{}`)

	got, err := migrateBusDirAt("mig", oldDir, base)
	if err != nil || got != BusDirMigrated {
		t.Fatalf("first migration = %v, %v; want BusDirMigrated", got, err)
	}
	newDir := filepath.Join(base, "muxcode-bus-mig")
	for _, rel := range []string{"inbox/edit.jsonl", "graphs/run1/graph.json"} {
		if _, err := os.Stat(filepath.Join(newDir, rel)); err != nil {
			t.Errorf("%s missing after migration: %v", rel, err)
		}
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("legacy dir still present after migration: %v", err)
	}

	if got, err := migrateBusDirAt("mig", oldDir, base); err != nil || got != BusDirNoLegacy {
		t.Errorf("second migration = %v, %v; want BusDirNoLegacy", got, err)
	}
	if n := countLifecycleEvents(t, "mig", "bus-dir-migrated"); n != 1 {
		t.Errorf("bus-dir-migrated rows = %d, want 1", n)
	}
}

// TestMigrateBusDirAt_ConflictKeepsBoth: when both directories exist the new
// one is untouched and the old one is left for the user.
func TestMigrateBusDirAt_ConflictKeepsBoth(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	oldDir := filepath.Join(t.TempDir(), "muxcode-bus-c")
	base := t.TempDir()
	writeFile(t, filepath.Join(oldDir, "old.txt"), "old")
	writeFile(t, filepath.Join(base, "muxcode-bus-c", "new.txt"), "new")

	if got, err := migrateBusDirAt("c", oldDir, base); err != nil || got != BusDirConflict {
		t.Fatalf("migration = %v, %v; want BusDirConflict", got, err)
	}
	if _, err := os.Stat(filepath.Join(oldDir, "old.txt")); err != nil {
		t.Errorf("legacy dir disturbed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "muxcode-bus-c", "old.txt")); !os.IsNotExist(err) {
		t.Errorf("legacy content merged into the new dir")
	}
}

// TestMigrateBusDirAt_NoLegacy: with nothing in /tmp no directory is created.
func TestMigrateBusDirAt_NoLegacy(t *testing.T) {
	base := filepath.Join(t.TempDir(), "bus")
	got, err := migrateBusDirAt("none", filepath.Join(t.TempDir(), "absent"), base)
	if err != nil || got != BusDirNoLegacy {
		t.Fatalf("migration = %v, %v; want BusDirNoLegacy", got, err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Errorf("base created with nothing to migrate: %v", err)
	}
}

// TestMigrateLegacyBusDir_InertInTests: a test binary never moves a real
// session's /tmp directory, whatever the session is called.
func TestMigrateLegacyBusDir_InertInTests(t *testing.T) {
	saved := busDirOverride
	ResetBusDirBase()
	t.Cleanup(func() { busDirOverride = saved })
	t.Setenv(BusDirBaseEnv, "")
	if got, err := MigrateLegacyBusDir("muxcode"); err != nil || got != BusDirNoLegacy {
		t.Errorf("MigrateLegacyBusDir in a test binary = %v, %v; want BusDirNoLegacy", got, err)
	}
}

// enableMigration turns on BusDir's migration road against scratch roots — a
// temp HOME for the new base and a temp legacy root standing in for /tmp — and
// returns the legacy root. Sessions must be unique per test: the in-process
// migration memo is package-wide.
func enableMigration(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv(BusDirBaseEnv, "")
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	savedOverride, savedRoot, savedEnabled, savedRename := busDirOverride, legacyBusDirRoot, migrationEnabled, renameDir
	ResetBusDirBase()
	legacyBusDirRoot = t.TempDir()
	migrationEnabled = func() bool { return true }
	t.Cleanup(func() {
		busDirOverride, legacyBusDirRoot, migrationEnabled, renameDir = savedOverride, savedRoot, savedEnabled, savedRename
	})
	return legacyBusDirRoot
}

func exdevRename(oldpath, newpath string) error {
	return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
}

func busDirPanic(session string) (dir string, recovered any) {
	defer func() { recovered = recover() }()
	return BusDir(session), nil
}

// TestBusDir_ConcurrentResolutionWaitsForMigration: a second resolution of a
// session mid-move blocks until the move is done, so no caller can write into
// an empty destination the move then collides with.
func TestBusDir_ConcurrentResolutionWaitsForMigration(t *testing.T) {
	legacy := enableMigration(t)
	writeFile(t, filepath.Join(legacy, "muxcode-bus-conc", "inbox", "edit.jsonl"), "row\n")
	entered, release := make(chan struct{}), make(chan struct{})
	renameDir = func(a, b string) error {
		close(entered)
		<-release
		return os.Rename(a, b)
	}

	first := make(chan string)
	go func() { first <- BusDir("conc") }()
	<-entered
	second := make(chan string)
	go func() { second <- BusDir("conc") }()
	select {
	case dir := <-second:
		t.Fatalf("second resolution returned %s before the move finished", dir)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	dir := <-first
	if got := <-second; got != dir {
		t.Errorf("resolutions disagree: %s vs %s", got, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox", "edit.jsonl")); err != nil {
		t.Errorf("migrated inbox missing: %v", err)
	}
}

// TestBusDir_FailedCopyStrandsNothing: a cross-device copy that fails leaves
// the legacy directory intact and no destination or staging directory; BusDir
// panics rather than hand back a path a caller would initialize, the launcher
// road returns the error, and the failure is retried, not remembered.
func TestBusDir_FailedCopyStrandsNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	legacy := enableMigration(t)
	renameDir = exdevRename
	unreadable := filepath.Join(legacy, "muxcode-bus-fail", "graphs", "run", "graph.json")
	writeFile(t, unreadable, "{}")
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(StateDir(), "bus", "muxcode-bus-fail")

	if err := ReportLegacyBusDirConflict("fail"); err == nil {
		t.Error("ReportLegacyBusDirConflict returned nil for a failed migration")
	}
	for i := 0; i < 2; i++ {
		if dir, r := busDirPanic("fail"); r == nil {
			t.Fatalf("resolution %d returned %s instead of failing", i+1, dir)
		}
	}
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Errorf("a replacement bus exists after a failed migration: %v", err)
	}
	if staged, _ := filepath.Glob(filepath.Join(filepath.Dir(newDir), ".muxcode-bus-fail.migrating-*")); len(staged) != 0 {
		t.Errorf("staging left behind: %v", staged)
	}
	if _, err := os.Lstat(unreadable); err != nil {
		t.Errorf("legacy state disturbed: %v", err)
	}

	if err := os.Chmod(unreadable, 0o644); err != nil {
		t.Fatal(err)
	}
	if dir, r := busDirPanic("fail"); r != nil {
		t.Fatalf("retry after the cause was fixed still failed: %v", r)
	} else if _, err := os.Stat(filepath.Join(dir, "graphs", "run", "graph.json")); err != nil {
		t.Errorf("retried migration lost graph.json: %v", err)
	}
}

// TestBusDir_CrossDeviceCopyPublishesWhole: the EXDEV road publishes the copy
// by rename and removes the legacy directory, leaving no staging behind.
func TestBusDir_CrossDeviceCopyPublishesWhole(t *testing.T) {
	legacy := enableMigration(t)
	renameDir = exdevRename
	writeFile(t, filepath.Join(legacy, "muxcode-bus-xdev", "spawn.jsonl"), "{}\n")

	dir := BusDir("xdev")
	if _, err := os.Stat(filepath.Join(dir, "spawn.jsonl")); err != nil {
		t.Errorf("copied registry missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "muxcode-bus-xdev")); !os.IsNotExist(err) {
		t.Errorf("legacy dir left after a successful copy: %v", err)
	}
	if staged, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), ".muxcode-bus-xdev.migrating-*")); len(staged) != 0 {
		t.Errorf("staging left behind: %v", staged)
	}
	if n := countLifecycleEvents(t, "xdev", "bus-dir-migrated"); n != 1 {
		t.Errorf("bus-dir-migrated rows = %d, want 1", n)
	}
}

// TestCopyTree_PreservesContent covers the cross-device road moveDir takes
// when os.Rename returns EXDEV (Linux's tmpfs /tmp).
func TestCopyTree_PreservesContent(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	writeFile(t, filepath.Join(src, "a", "b.jsonl"), "row\n")
	if err := os.Chmod(filepath.Join(src, "a", "b.jsonl"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a/b.jsonl", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "a", "b.jsonl"))
	if err != nil || string(data) != "row\n" {
		t.Errorf("copied file = %q, %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(dst, "a", "b.jsonl")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("copied mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	if link, err := os.Readlink(filepath.Join(dst, "link")); err != nil || link != "a/b.jsonl" {
		t.Errorf("copied symlink = %q, %v", link, err)
	}
}

// tmpRoots returns the scratch roots an OS cleaner may sweep, in both their
// spelled and symlink-resolved forms (/tmp is /private/tmp on macOS).
func tmpRoots() []string {
	roots := []string{"/tmp", os.TempDir()}
	for _, r := range []string{"/tmp", os.TempDir()} {
		if resolved, err := filepath.EvalSymlinks(r); err == nil {
			roots = append(roots, resolved)
		}
	}
	return roots
}

// pathWithin reports whether path is root or lies beneath it.
func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
