package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// freshnessFixture is a bare origin, an "upstream" clone that advances main,
// and a work clone whose freshness is measured.
type freshnessFixture struct {
	t        *testing.T
	origin   string
	up, work string
	spec     string
}

func (fx *freshnessFixture) git(dir string, args ...string) string {
	fx.t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		fx.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (fx *freshnessFixture) commit(dir, file, content string) {
	fx.t.Helper()
	p := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fx.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		fx.t.Fatal(err)
	}
	fx.git(dir, "add", "-A")
	fx.git(dir, "commit", "-q", "-m", "change "+file)
}

// advance lands n commits on origin/main, each touching file (a distinct
// unrelated file when file is "").
func (fx *freshnessFixture) advance(n int, file string) {
	fx.t.Helper()
	for i := 0; i < n; i++ {
		f := file
		if f == "" {
			f = filepath.Join("other", "unrelated"+strings.Repeat("x", i)+".txt")
		}
		fx.commit(fx.up, f, fx.t.Name()+strings.Repeat("+", i))
	}
	fx.git(fx.up, "push", "-q", "origin", "main")
}

const freshnessSpec = "# Spec\n\n### Key files\n\n| File | Purpose |\n|------|---------|\n" +
	"| `tools/muxcode/bus/graph_templates.go` | templates (34–66) |\n" +
	"| `bus/conditions.go:89` | conditions |\n" +
	"| `docs/` | a directory, not a file |\n\n" +
	"## Implementation\n\n### Phase 1: Build\n\n- [ ] Write the builder into `student_data_query/database/query_builder.py`\n" +
	"- [ ] Call `BranchFreshness(repo, spec)` from the condition\n\n" +
	"### Phase 2: Later\n\n- [ ] Touch `later/only.go`\n"

func newFreshnessFixture(t *testing.T) *freshnessFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("MUXCODE_FRESHNESS_MAX_BEHIND", "")
	t.Setenv("MUXCODE_FRESHNESS_MAX_FETCH_AGE", "")
	empty := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_CONFIG", empty)

	root := t.TempDir()
	fx := &freshnessFixture{t: t, origin: filepath.Join(root, "origin.git"), up: filepath.Join(root, "up"), work: filepath.Join(root, "work")}
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", fx.origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "clone", "-q", fx.origin, fx.up).CombinedOutput(); err != nil {
		t.Fatalf("clone up: %v\n%s", err, out)
	}
	fx.git(fx.up, "checkout", "-q", "-b", "main")
	fx.commit(fx.up, "tools/muxcode/bus/graph_templates.go", "base")
	fx.commit(fx.up, "student_data_query/database/query_builder.py", "base")
	fx.git(fx.up, "push", "-q", "-u", "origin", "main")
	if out, err := exec.Command("git", "clone", "-q", fx.origin, fx.work).CombinedOutput(); err != nil {
		t.Fatalf("clone work: %v\n%s", err, out)
	}
	fx.git(fx.work, "checkout", "-q", "-b", "feature")
	fx.commit(fx.work, "feature.txt", "branch work")
	fx.spec = filepath.Join(root, "spec.md")
	if err := os.WriteFile(fx.spec, []byte(freshnessSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	return fx
}

func TestBranchFreshnessFreshBranch(t *testing.T) {
	fx := newFreshnessFixture(t)
	f := BranchFreshness(fx.work, fx.spec)
	if f.Stale || f.Behind != 0 || f.Ahead != 1 || f.FetchErr != "" {
		t.Fatalf("fresh branch read %+v", f)
	}
	if f.FetchAge < 0 {
		t.Fatalf("a successful fetch recorded no age: %s", f.Summary())
	}
	if !strings.Contains(f.Summary(), "0 behind") || !strings.Contains(f.Summary(), "fresh") {
		t.Fatalf("summary %q", f.Summary())
	}
}

func TestBranchFreshnessThresholdWithoutOverlap(t *testing.T) {
	fx := newFreshnessFixture(t)
	fx.advance(4, "")
	if f := BranchFreshness(fx.work, fx.spec); f.Stale || f.Behind != 4 || len(f.Overlap) != 0 {
		t.Fatalf("4 behind with no overlap should be fresh: %s", f.Summary())
	}
	fx.advance(1, "other/fifth.txt")
	f := BranchFreshness(fx.work, fx.spec)
	if !f.Stale || f.Behind != 5 {
		t.Fatalf("5 behind should be stale: %s", f.Summary())
	}
	if !strings.Contains(f.Summary(), "5 behind ≥ 5") {
		t.Fatalf("the reason does not name the threshold: %s", f.Summary())
	}
}

// One upstream commit touching a spec-named file gates. The branch's own
// @{upstream} reads 0 behind, so a measure taken from it would pass this case.
func TestBranchFreshnessOverlapGatesAtOneBehind(t *testing.T) {
	fx := newFreshnessFixture(t)
	fx.git(fx.work, "push", "-q", "-u", "origin", "feature")
	fx.advance(1, "tools/muxcode/bus/graph_templates.go")
	fx.git(fx.work, "fetch", "-q", "origin")

	if n := fx.git(fx.work, "rev-list", "--count", "HEAD..@{upstream}"); n != "0" {
		t.Fatalf("fixture: @{upstream} should read 0 behind, got %s", n)
	}
	f := BranchFreshness(fx.work, fx.spec)
	if !f.Stale || f.Behind != 1 {
		t.Fatalf("1 behind with overlap should be stale: %s", f.Summary())
	}
	if len(f.Overlap) != 1 || f.Overlap[0].Path != "tools/muxcode/bus/graph_templates.go" {
		t.Fatalf("overlap %+v", f.Overlap)
	}
}

func TestBranchFreshnessNamesUpstreamDelete(t *testing.T) {
	fx := newFreshnessFixture(t)
	fx.git(fx.up, "rm", "-q", "student_data_query/database/query_builder.py")
	fx.git(fx.up, "commit", "-q", "-m", "move the builder")
	fx.commit(fx.up, "resources/lambda/shared/dynamo.py", "moved")
	fx.git(fx.up, "push", "-q", "origin", "main")

	f := BranchFreshness(fx.work, fx.spec)
	if !f.Stale {
		t.Fatalf("an upstream delete of a phase file should be stale: %s", f.Summary())
	}
	if !strings.Contains(f.Summary(), "student_data_query/database/query_builder.py (deleted)") {
		t.Fatalf("summary does not name the deleted file: %s", f.Summary())
	}
	if len(f.OverlapBy) != 1 || !strings.HasSuffix(f.OverlapBy[0], " move the builder") ||
		!strings.Contains(f.Summary(), "move the builder") {
		t.Fatalf("the upstream commit touching the file must be named, got %q in %s", f.OverlapBy, f.Summary())
	}
	if strings.Contains(f.Summary(), "change resources/lambda/shared/dynamo.py") {
		t.Errorf("negative control: a commit touching no spec file must not be named: %s", f.Summary())
	}
}

// A no-ff merge that brings a spec-file change onto main is named by the
// merge commit itself; an unrelated merge in the same range is not.
func TestBranchFreshnessNamesTheMergeOnMain(t *testing.T) {
	fx := newFreshnessFixture(t)
	merge := func(branch, file, subject string) {
		fx.git(fx.up, "checkout", "-q", "-b", branch)
		fx.commit(fx.up, file, branch+" work")
		fx.git(fx.up, "checkout", "-q", "main")
		fx.git(fx.up, "merge", "-q", "--no-ff", "-m", subject, branch)
	}
	merge("side", "tools/muxcode/bus/graph_templates.go", "Merge side: rework the templates")
	merge("other", "other/unrelated.txt", "Merge other: unrelated")
	fx.git(fx.up, "push", "-q", "origin", "main")

	f := BranchFreshness(fx.work, fx.spec)
	if len(f.OverlapBy) != 1 || !strings.HasSuffix(f.OverlapBy[0], " Merge side: rework the templates") {
		t.Fatalf("the merge on main must be named, not the side-branch commit: %q", f.OverlapBy)
	}
	if strings.Contains(f.Summary(), "Merge other") {
		t.Errorf("negative control: a merge touching no spec file must not be named: %s", f.Summary())
	}
}

func TestBranchFreshnessFetchFailureIsStale(t *testing.T) {
	fx := newFreshnessFixture(t)
	fx.git(fx.work, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	f := BranchFreshness(fx.work, fx.spec)
	if f.Behind != 0 || len(f.Overlap) != 0 {
		t.Fatalf("fixture: the cached origin/main should measure 0 behind, got %s", f.Summary())
	}
	if !f.Stale || f.FetchErr == "" || !strings.Contains(f.Summary(), "fetch failed") {
		t.Fatalf("a failed fetch must read stale: %s", f.Summary())
	}
}

func TestBranchFreshnessNoOriginMainIsStale(t *testing.T) {
	fx := newFreshnessFixture(t)
	fx.git(fx.work, "remote", "remove", "origin")
	if f := BranchFreshness(fx.work, fx.spec); !f.Stale {
		t.Fatalf("a repo with no origin/main must read stale: %s", f.Summary())
	}
}

func TestBranchFreshnessMaxBehindSetting(t *testing.T) {
	fx := newFreshnessFixture(t)
	fx.advance(2, "")

	t.Setenv("MUXCODE_FRESHNESS_MAX_BEHIND", "2")
	if f := BranchFreshness(fx.work, fx.spec); !f.Stale || f.MaxBehind != 2 {
		t.Fatalf("an override of 2 should gate at 2 behind: %s", f.Summary())
	}

	t.Setenv("MUXCODE_FRESHNESS_MAX_BEHIND", "")
	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(cfg, []byte("MUXCODE_FRESHNESS_MAX_BEHIND=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCODE_CONFIG", cfg)
	if f := BranchFreshness(fx.work, fx.spec); !f.Stale || f.MaxBehind != 2 {
		t.Fatalf("the config file's 2 should gate at 2 behind: %s", f.Summary())
	}

	t.Setenv("MUXCODE_FRESHNESS_MAX_BEHIND", "100")
	fx.advance(1, "tools/muxcode/bus/graph_templates.go")
	f := BranchFreshness(fx.work, fx.spec)
	if !f.Stale || f.MaxBehind != 100 || len(f.Overlap) != 1 {
		t.Fatalf("an override of 100 must still gate on overlap: %s", f.Summary())
	}
}

func TestSpecNamedFiles(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(spec, []byte(freshnessSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SpecNamedFiles(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tools/muxcode/bus/graph_templates.go",
		"bus/conditions.go",
		"student_data_query/database/query_builder.py",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SpecNamedFiles = %q, want %q (directories, calls and later phases excluded)", got, want)
	}
}

// A fetch mapping narrowed to the feature branch must not leave the cached
// origin/main measured as current.
func TestBranchFreshnessNarrowedFetchMappingStillUpdatesMain(t *testing.T) {
	fx := newFreshnessFixture(t)
	fx.git(fx.work, "config", "remote.origin.fetch", "+refs/heads/feature:refs/remotes/origin/feature")
	fx.advance(1, "tools/muxcode/bus/graph_templates.go")
	f := BranchFreshness(fx.work, fx.spec)
	if !f.Stale || f.Behind != 1 || len(f.Overlap) != 1 {
		t.Fatalf("main advanced on a spec file behind a narrowed mapping should read stale: %s", f.Summary())
	}
}

func TestBranchFreshnessUnreadableSpecIsStale(t *testing.T) {
	fx := newFreshnessFixture(t)
	if f := BranchFreshness(fx.work, ""); f.Stale {
		t.Fatalf("control: no spec and 0 behind should be fresh: %s", f.Summary())
	}
	f := BranchFreshness(fx.work, filepath.Join(t.TempDir(), "missing.md"))
	if !f.Stale || !strings.Contains(f.Summary(), "spec-named files unknown") {
		t.Fatalf("a named spec that cannot be read must read stale: %s", f.Summary())
	}
}

func TestBranchFreshnessResolvesRelativeSpecAgainstRepo(t *testing.T) {
	fx := newFreshnessFixture(t)
	if err := os.MkdirAll(filepath.Join(fx.work, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.work, "docs", "spec.md"), []byte(freshnessSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.advance(1, "tools/muxcode/bus/graph_templates.go")
	if f := BranchFreshness(fx.work, "docs/spec.md"); !f.Stale || len(f.Overlap) != 1 {
		t.Fatalf("a repo-relative spec path should find the overlap: %s", f.Summary())
	}
}

func TestUpstreamChangesReportsDiffFailure(t *testing.T) {
	fx := newFreshnessFixture(t)
	if _, err := upstreamChanges(fx.work, "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("a diff git cannot run must return an error, not an empty list")
	}
	if got, err := upstreamChanges(fx.work, fx.git(fx.work, "merge-base", "HEAD", FreshnessBase)); err != nil || len(got) != 0 {
		t.Fatalf("control: a valid base with no upstream change = %v, %v", got, err)
	}
}

// Paths git would quote (non-ASCII) or that hold spaces still match the
// spec's literal tokens, one commit below the threshold.
func TestBranchFreshnessLiteralPathOverlap(t *testing.T) {
	for _, path := range []string{"src/naïve façade.go", "docs/spaced name.md"} {
		t.Run(path, func(t *testing.T) {
			fx := newFreshnessFixture(t)
			spec := "### Key files\n\n| File | Purpose |\n|---|---|\n| `" + path + "` | target |\n| `other/x.go` | unrelated |\n"
			if err := os.WriteFile(fx.spec, []byte(spec), 0o644); err != nil {
				t.Fatal(err)
			}
			fx.advance(1, "")
			if f := BranchFreshness(fx.work, fx.spec); f.Stale {
				t.Fatalf("control: an unrelated upstream change should be fresh: %s", f.Summary())
			}
			fx.advance(1, path)
			f := BranchFreshness(fx.work, fx.spec)
			if !f.Stale || len(f.Overlap) != 1 || f.Overlap[0].Path != path {
				t.Fatalf("upstream touched %q: %s", path, f.Summary())
			}
		})
	}
}

func TestBranchDivergenceMeasuresOriginMainNotUpstream(t *testing.T) {
	fx := newFreshnessFixture(t)
	fx.git(fx.work, "push", "-q", "-u", "origin", "feature")
	fx.advance(3, "")
	fx.git(fx.work, "fetch", "-q", "origin")
	ahead, behind, base, err := BranchDivergence(fx.work)
	if err != nil || ahead != 1 || behind != 3 || base == "" {
		t.Fatalf("BranchDivergence = %d ahead, %d behind, base %q, err %v; want 1, 3", ahead, behind, base, err)
	}
}
