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
