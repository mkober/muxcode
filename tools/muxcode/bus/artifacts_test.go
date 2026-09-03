package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// artifactRepo builds a real git repo with a gitignored cdk.out holding one
// file of the given size. Real git, because the guard is git's own answer.
func artifactRepo(t *testing.T, size int) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("cdk.out\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "cdk.out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "asset.zip"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPurgeBuildArtifacts_RemovesIgnoredOutput(t *testing.T) {
	repo := artifactRepo(t, 2048)

	res, err := PurgeBuildArtifacts(repo, false)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(res.Paths) != 1 || res.BytesFreed < 2048 {
		t.Fatalf("expected cdk.out removed with its bytes counted, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "cdk.out")); !os.IsNotExist(err) {
		t.Error("cdk.out must be gone")
	}
}

// TestPurgeBuildArtifacts_KeepsTrackedDir is the negative control that matters:
// a repo which deliberately commits a path named cdk.out keeps it. Deleting
// tracked work to reclaim disk is worse than staying full.
func TestPurgeBuildArtifacts_KeepsTrackedDir(t *testing.T) {
	repo := artifactRepo(t, 128)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("nothing-here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := PurgeBuildArtifacts(repo, false)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(res.Paths) != 0 {
		t.Errorf("un-ignored cdk.out must be preserved, removed %v", res.Paths)
	}
	if _, err := os.Stat(filepath.Join(repo, "cdk.out")); err != nil {
		t.Error("cdk.out must still exist")
	}
}

// TestPurgeBuildArtifacts_DryRunMeasuresOnly pins that a dry run reports the
// same reclaim without deleting.
func TestPurgeBuildArtifacts_DryRunMeasuresOnly(t *testing.T) {
	repo := artifactRepo(t, 4096)

	res, err := PurgeBuildArtifacts(repo, true)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if len(res.Paths) != 1 || res.BytesFreed < 4096 {
		t.Fatalf("dry run must still measure, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "cdk.out")); err != nil {
		t.Error("dry run must not delete")
	}
}

func TestPurgeSessionArtifacts_RespectsOptOut(t *testing.T) {
	repo := artifactRepo(t, 512)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	t.Setenv("MUXCODE_ARTIFACT_PURGE_DISABLE", "1")

	if res := PurgeSessionArtifacts("s", "test"); res != nil {
		t.Errorf("opt-out must purge nothing, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "cdk.out")); err != nil {
		t.Error("cdk.out must survive the opt-out")
	}
}

// TestPurgeSessionArtifacts_PurgesWhenEnabled is the opt-out's positive
// control: without the env var the same call does remove the directory.
func TestPurgeSessionArtifacts_PurgesWhenEnabled(t *testing.T) {
	useTempBusDir(t)
	repo := artifactRepo(t, 512)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)

	res := PurgeSessionArtifacts("s", "test")
	if res == nil || len(res.Paths) != 1 {
		t.Fatalf("expected one purged path, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "cdk.out")); !os.IsNotExist(err) {
		t.Error("cdk.out must be gone")
	}
}

// fakeGoCache points GOCACHE at a temp dir holding one file of the given size
// and PROVES the redirect took effect before any test acts on it. Without that
// proof a test calling the live purge path would run `go clean -cache` against
// the developer's real multi-GB cache.
func fakeGoCache(t *testing.T, size int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob"), make([]byte, size), 0644); err != nil {
		t.Fatalf("seeding cache: %v", err)
	}
	t.Setenv("GOCACHE", dir)
	t.Setenv("MUXCODE_GOCACHE_PURGE_DISABLE", "")
	if got := goCacheDir(); got != dir {
		t.Skipf("GOCACHE redirect did not take (go env reports %q) — refusing to touch the real cache", got)
	}
	return dir
}

func TestPurgeGoBuildCacheDryRunMeasuresWithoutDeleting(t *testing.T) {
	t.Setenv("MUXCODE_GOCACHE_FLOOR_MB", "0")
	dir := fakeGoCache(t, 4096)

	res, err := PurgeGoBuildCache(true)
	if err != nil {
		t.Fatalf("PurgeGoBuildCache: %v", err)
	}
	if len(res.Paths) != 1 || res.BytesFreed < 4096 {
		t.Fatalf("dry run reported %+v, want the cache dir and its size", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "blob")); err != nil {
		t.Error("dry run must not delete cache contents")
	}
}

// Negative control: without a floor check a purge would clear tiny caches,
// paying a full rebuild to reclaim nothing.
func TestPurgeGoBuildCacheRespectsFloor(t *testing.T) {
	t.Setenv("MUXCODE_GOCACHE_FLOOR_MB", "64")
	fakeGoCache(t, 4096)

	res, err := PurgeGoBuildCache(true)
	if err != nil {
		t.Fatalf("PurgeGoBuildCache: %v", err)
	}
	if len(res.Paths) != 0 {
		t.Fatalf("cache below the floor must be left alone, got %+v", res)
	}
}

// Negative control for the opt-out: a disabled purge must report nothing even
// when the cache is far above the floor.
func TestPurgeGoBuildCacheDisabledOptOut(t *testing.T) {
	t.Setenv("MUXCODE_GOCACHE_FLOOR_MB", "0")
	fakeGoCache(t, 4096)
	t.Setenv("MUXCODE_GOCACHE_PURGE_DISABLE", "1")

	res, err := PurgeGoBuildCache(false)
	if err != nil {
		t.Fatalf("PurgeGoBuildCache: %v", err)
	}
	if len(res.Paths) != 0 {
		t.Fatalf("disabled purge must do nothing, got %+v", res)
	}
}

func TestSameDeviceMatchesAndRejects(t *testing.T) {
	dir := t.TempDir()
	if !sameDevice(dir, dir) {
		t.Error("a path must share a device with itself")
	}
	// Negative control: an unstattable path must answer false, never inherit a
	// match — the caller purges on a true.
	if sameDevice(filepath.Join(dir, "no-such-path"), dir) {
		t.Error("a missing path must not report a shared device")
	}
}

// A Go cache on another filesystem cannot relieve /tmp pressure, so the ladder
// must not count it. Proven via the GOCACHE redirect rather than a real second
// volume, which a test cannot portably create.
func TestGoCacheRelievesTmpFalseWhenUnresolvable(t *testing.T) {
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "absent"))
	if goCacheDir() == "" {
		t.Skip("go env GOCACHE unavailable")
	}
	if GoCacheRelievesTmp() {
		t.Error("an unstattable cache dir must not claim it can relieve /tmp")
	}
}

func TestGoCacheFloorBytesDefaultAndOverride(t *testing.T) {
	t.Setenv("MUXCODE_GOCACHE_FLOOR_MB", "")
	if got := GoCacheFloorBytes(); got != goCacheFloorDefault {
		t.Errorf("default floor = %d, want %d", got, goCacheFloorDefault)
	}
	t.Setenv("MUXCODE_GOCACHE_FLOOR_MB", "2048")
	if got := GoCacheFloorBytes(); got != 2048<<20 {
		t.Errorf("override floor = %d, want %d", got, int64(2048)<<20)
	}
}
