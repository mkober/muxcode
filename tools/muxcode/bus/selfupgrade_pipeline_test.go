package bus

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeMakeScript records each call as "<physical cwd> <args>" and, for
// install, writes a stub muxcode into BINDIR whose `version --json` reports
// $FAKE_INSTALLED_VERSION, else the VERSION it was given. FAKE_MAKE_FAIL
// names a target to fail. With FAKE_MAKE_BLOCK set, build touches
// $FAKE_MAKE_BLOCK.started and waits for $FAKE_MAKE_BLOCK to exist.
const fakeMakeScript = `#!/bin/sh
echo "$(pwd -P) $*" >> "$FAKE_MAKE_LOG"
target=$1
for a in "$@"; do
  case "$a" in
    BINDIR=*) bindir=${a#BINDIR=} ;;
    VERSION=*) version=${a#VERSION=} ;;
  esac
done
if [ -n "$FAKE_MAKE_BLOCK" ] && [ "$target" = build ]; then
  : > "$FAKE_MAKE_BLOCK.started"
  while [ ! -e "$FAKE_MAKE_BLOCK" ]; do sleep 0.05; done
fi
if [ "$FAKE_MAKE_FAIL" = "$target" ]; then
  echo "compile error: boom" >&2
  exit 2
fi
if [ "$target" = install ]; then
  mkdir -p "$bindir"
  v=${FAKE_INSTALLED_VERSION:-$version}
  cat > "$bindir/muxcode" <<EOF
#!/bin/sh
echo '{"version":"$v","commit":"unknown","date":"2026-10-06T00:00:00Z"}'
EOF
  chmod 755 "$bindir/muxcode"
fi
echo "make $target done"
`

type upgradeFixture struct {
	dir, cache, bindir, configdir, makeLog, tarball string
	opts                                            SelfUpgradeOptions
}

// newUpgradeFixture stands up a hermetic upgrade from installed to tag: a
// local releases/latest file and source tarball reached through file://
// URLs, the fake make and a stub go ahead of the real PATH, BINDIR first on
// PATH so the installed stub is the muxcode PATH resolves, and HOME in the
// scratch dir so the per-user upgrade lock is the fixture's own.
func newUpgradeFixture(t *testing.T, installed, tag string) *upgradeFixture {
	t.Helper()
	setInstalledVersion(t, installed)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	f := &upgradeFixture{
		dir:       dir,
		cache:     filepath.Join(dir, "cache"),
		bindir:    filepath.Join(dir, "bin"),
		configdir: filepath.Join(dir, "config"),
		makeLog:   filepath.Join(dir, "make.log"),
		tarball:   filepath.Join(dir, "muxcode-src.tar.gz"),
	}
	latest := filepath.Join(dir, "latest.json")
	writeUpgradeFile(t, latest, fmt.Sprintf(`{"tag_name":%q,"published_at":"2026-10-06T12:00:00Z"}`, tag), 0o644)
	writeSourceTarball(t, f.tarball, "muxcode-"+strings.TrimPrefix(tag, "v"))
	fakebin := filepath.Join(dir, "fakebin")
	writeUpgradeFile(t, filepath.Join(fakebin, "make"), fakeMakeScript, 0o755)
	writeUpgradeFile(t, filepath.Join(fakebin, "go"), "#!/bin/sh\nexit 0\n", 0o755)
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", f.bindir+sep+fakebin+sep+os.Getenv("PATH"))
	t.Setenv("FAKE_MAKE_LOG", f.makeLog)
	t.Setenv("FAKE_MAKE_FAIL", "")
	t.Setenv("FAKE_MAKE_BLOCK", "")
	t.Setenv("FAKE_INSTALLED_VERSION", "")
	f.opts = SelfUpgradeOptions{
		Client:    ReleaseClient{APIURL: "file://" + latest, TarballURL: "file://" + f.tarball},
		CacheRoot: f.cache,
		BinDir:    f.bindir,
		ConfigDir: f.configdir,
	}
	return f
}

func writeUpgradeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// writeSourceTarball writes a GitHub-shaped source archive: everything under
// one top-level directory, a Makefile at its root.
func writeSourceTarball(t *testing.T, path, top string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, file := range []struct{ name, body string }{
		{top + "/Makefile", "install:\n"},
		{top + "/tools/muxcode/go.mod", "module github.com/mkober/muxcode/tools/muxcode\n"},
	} {
		hdr := &tar.Header{Name: file.name, Mode: 0o644, Size: int64(len(file.body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(file.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	writeUpgradeFile(t, path, buf.String(), 0o644)
}

func makeCalls(t *testing.T, log string) [][]string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		calls = append(calls, strings.Fields(line))
	}
	return calls
}

func stepNames(results []StepResult) []string {
	var names []string
	for _, r := range results {
		names = append(names, r.Name)
	}
	return names
}

func TestSelfUpgradeInstallsNewerRelease(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	var indexes []int
	s, err := RunSelfUpgrade(context.Background(), f.opts, func(i int, _ StepResult) { indexes = append(indexes, i) })
	if err != nil {
		t.Fatalf("RunSelfUpgrade: %v (results %+v)", err, s.Results)
	}
	if got := stepNames(s.Results); !reflect.DeepEqual(got, []string{"Check", "Download", "Build", "Install", "Verify"}) {
		t.Fatalf("steps = %v", got)
	}
	for _, r := range s.Results {
		if !r.Success {
			t.Errorf("%s failed: %s", r.Name, r.Error)
		}
	}
	if !reflect.DeepEqual(indexes, []int{0, 1, 2, 3, 4}) {
		t.Errorf("progress indexes = %v, want 0..4", indexes)
	}

	src, err := filepath.EvalSymlinks(filepath.Join(f.cache, "v0.1.21", "src"))
	if err != nil {
		t.Fatalf("extracted tree: %v", err)
	}
	calls := makeCalls(t, f.makeLog)
	if len(calls) != 2 {
		t.Fatalf("make calls = %v, want build then install", calls)
	}
	for i, target := range []string{"build", "install"} {
		want := []string{src, target, "VERSION=v0.1.21", "DATE=" + s.Date, "BINDIR=" + f.bindir, "CONFIGDIR=" + f.configdir}
		if !reflect.DeepEqual(calls[i], want) {
			t.Errorf("make call %d = %v, want %v", i, calls[i], want)
		}
	}

	data, err := os.ReadFile(f.tarball)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	wantSum := hex.EncodeToString(sum[:])
	if s.Tarball.SHA256 != wantSum || s.Tarball.Size != int64(len(data)) || s.Tarball.Cached {
		t.Errorf("tarball = %+v, want sha256 %s size %d fetched", s.Tarball, wantSum, len(data))
	}
	if !strings.Contains(s.Results[1].Note, wantSum) {
		t.Errorf("Download note %q does not record the sha256", s.Results[1].Note)
	}
	if s.Verified.Version != "v0.1.21" {
		t.Errorf("verified %q, want v0.1.21", s.Verified.Version)
	}
}

// A second run against the same tag must not fetch: the origin tarball is
// deleted, so a fetch would fail. Removing the download record is the
// negative control — an incomplete cache is refetched, and fails here.
func TestSelfUpgradeReusesCompleteCache(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	if _, err := RunSelfUpgrade(context.Background(), f.opts, nil); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := os.Remove(f.tarball); err != nil {
		t.Fatal(err)
	}

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	if err != nil {
		t.Fatalf("cached run: %v", err)
	}
	if !s.Tarball.Cached || !strings.HasPrefix(s.Results[1].Note, "cache hit") {
		t.Errorf("Download = %+v, want a cache hit", s.Results[1])
	}

	if err := os.Remove(filepath.Join(f.cache, "v0.1.21", upgradeManifestName)); err != nil {
		t.Fatal(err)
	}
	s, err = RunSelfUpgrade(context.Background(), f.opts, nil)
	if err == nil || len(s.Results) != 2 || s.Results[1].Success || !strings.Contains(s.Results[1].Error, "HTTP 404") {
		t.Errorf("run without a download record: err %v results %+v, want a refetch failing at Download", err, s.Results)
	}
}

// Nothing newer stops after Check and touches no file; forcing the same
// install is the positive half and runs every step.
func TestSelfUpgradeStopsAtCheckUnlessNewerOrForced(t *testing.T) {
	cases := []struct {
		installed string
		force     bool
		steps     int
		wantErr   bool
	}{
		{"v0.1.21", false, 1, false},
		{"v0.1.21-3-gabc1234", false, 1, false},
		{"devel", false, 1, true},
		{"v0.1.21", true, 5, false},
		{"devel", true, 5, false},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s force=%v", c.installed, c.force), func(t *testing.T) {
			f := newUpgradeFixture(t, c.installed, "v0.1.21")
			f.opts.Force = c.force
			s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
			if (err != nil) != c.wantErr || len(s.Results) != c.steps {
				t.Fatalf("err %v, %d steps %+v; want error=%v and %d steps", err, len(s.Results), s.Results, c.wantErr, c.steps)
			}
			if c.force {
				if !strings.HasSuffix(s.Results[0].Note, "— forced") {
					t.Errorf("Check note %q, want it marked forced", s.Results[0].Note)
				}
				return
			}
			for _, p := range []string{f.cache, f.bindir, f.makeLog} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Errorf("%s exists after a run that stopped at Check", p)
				}
			}
		})
	}
}

func TestSelfUpgradeMissingToolFailsAtCheck(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	only := filepath.Join(f.dir, "only-go")
	writeUpgradeFile(t, filepath.Join(only, "go"), "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", only)

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	if err == nil || len(s.Results) != 1 || !strings.Contains(s.Results[0].Error, "missing build tools: make, tar") {
		t.Fatalf("err %v results %+v, want Check to name make and tar", err, s.Results)
	}
	if _, err := os.Stat(f.cache); !os.IsNotExist(err) {
		t.Errorf("cache exists — a machine without a toolchain downloaded")
	}
}

func TestSelfUpgradeBuildFailureKeepsInstalledBinary(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	previous := "#!/bin/sh\necho previous build\n"
	writeUpgradeFile(t, filepath.Join(f.bindir, "muxcode"), previous, 0o755)
	t.Setenv("FAKE_MAKE_FAIL", "build")

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	if err == nil {
		t.Fatal("RunSelfUpgrade succeeded with a failing build")
	}
	last := s.Results[len(s.Results)-1]
	if last.Name != "Build" || last.Success {
		t.Fatalf("last step %+v, want a failed Build", last)
	}
	if !strings.Contains(last.Error, "compile error: boom") || !strings.Contains(last.Error, s.LogPath()) {
		t.Errorf("Build error %q, want the log's last lines and its path", last.Error)
	}
	got, err := os.ReadFile(filepath.Join(f.bindir, "muxcode"))
	if err != nil || string(got) != previous {
		t.Errorf("installed binary = %q (err %v), want the previous bytes", got, err)
	}
	for _, call := range makeCalls(t, f.makeLog) {
		if call[1] == "install" {
			t.Errorf("make install ran after a failed build: %v", call)
		}
	}
}

func TestSelfUpgradeVerifyMismatchStopsPipeline(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	t.Setenv("FAKE_INSTALLED_VERSION", "v0.1.20")
	reached := false
	steps := append(selfUpgradeSteps(), upgradeStep{Name: "After Verify", Run: func(context.Context, *UpgradeState) (string, error) {
		reached = true
		return "", nil
	}})

	s := newUpgradeState(f.opts)
	err := runUpgradeSteps(context.Background(), s, steps, nil)
	if err == nil || reached {
		t.Fatalf("err %v, later step reached %v; want Verify to stop the pipeline", err, reached)
	}
	last := s.Results[len(s.Results)-1]
	if last.Name != "Verify" || !strings.Contains(last.Error, "reports v0.1.20, want v0.1.21") {
		t.Errorf("last step %+v, want Verify naming both versions", last)
	}
}

// A muxcode earlier on PATH than BINDIR keeps every later call on the old
// build, so Verify refuses it; the install test, with BINDIR first, is the
// passing half.
func TestSelfUpgradeVerifyRefusesShadowedInstall(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	shadow := filepath.Join(f.dir, "shadow", "muxcode")
	writeUpgradeFile(t, shadow, "#!/bin/sh\necho stale\n", 0o755)
	t.Setenv("PATH", filepath.Dir(shadow)+string(os.PathListSeparator)+os.Getenv("PATH"))

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	last := s.Results[len(s.Results)-1]
	if err == nil || last.Name != "Verify" || !strings.Contains(last.Error, "muxcode on PATH is "+shadow) {
		t.Errorf("err %v last %+v, want Verify to name the shadowing %s", err, last, shadow)
	}
}

// A run that overlaps one holding the upgrade lock is refused at its first
// mutating step, before it touches any cache or the install — whether it
// shares the first run's cache or only its BINDIR and CONFIGDIR. Once the
// first run finishes, the same call succeeds: the sequential control. The
// overlapping run carries a deadline so that, without the lock, it fails at
// the blocked Build instead of hanging the test.
func TestSelfUpgradeRefusesOverlappingRun(t *testing.T) {
	for _, c := range []struct{ name, secondCache string }{
		{"same cache", ""},
		{"distinct cache, shared install", "cache-b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
			second := f.opts
			if c.secondCache != "" {
				second.CacheRoot = filepath.Join(f.dir, c.secondCache)
			}
			release := filepath.Join(f.dir, "release-build")
			t.Setenv("FAKE_MAKE_BLOCK", release)

			var firstErr error
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, firstErr = RunSelfUpgrade(context.Background(), f.opts, nil)
			}()
			t.Cleanup(func() {
				_ = os.WriteFile(release, nil, 0o644)
				<-done
			})
			waitForUpgradeFile(t, release+".started")

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s, err := RunSelfUpgrade(ctx, second, nil)
			if err == nil || len(s.Results) != 2 || s.Results[1].Name != "Download" ||
				!strings.Contains(s.Results[1].Error, "another muxcode upgrade is running") {
				t.Fatalf("overlapping run: err %v results %+v, want refusal at Download", err, s.Results)
			}
			if calls := makeCalls(t, f.makeLog); len(calls) != 1 {
				t.Errorf("make calls %v, want only the first run's build", calls)
			}
			if c.secondCache != "" {
				if _, err := os.Stat(second.CacheRoot); !os.IsNotExist(err) {
					t.Errorf("refused run created its cache %s", second.CacheRoot)
				}
			}

			writeUpgradeFile(t, release, "", 0o644)
			<-done
			if firstErr != nil {
				t.Fatalf("first run: %v", firstErr)
			}
			if _, err := RunSelfUpgrade(context.Background(), second, nil); err != nil {
				t.Errorf("run after the first finished: %v", err)
			}
		})
	}
}

func waitForUpgradeFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Relative overrides resolve against the caller's directory, not make's: the
// stub must land where Verify runs it, and nothing under the source tree.
func TestSelfUpgradeRelativePathsResolveAgainstCaller(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	caller := filepath.Join(f.dir, "caller")
	if err := os.MkdirAll(caller, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(caller); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	bin, err := filepath.Abs("rel-bin")
	if err != nil {
		t.Fatal(err)
	}
	config, err := filepath.Abs("rel-config")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.opts.CacheRoot, f.opts.BinDir, f.opts.ConfigDir = "rel-cache", "rel-bin", "rel-config"

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	if err != nil {
		t.Fatalf("RunSelfUpgrade: %v (results %+v)", err, s.Results)
	}
	install := makeCalls(t, f.makeLog)[1]
	if install[4] != "BINDIR="+bin || install[5] != "CONFIGDIR="+config {
		t.Errorf("install call %v, want BINDIR=%s CONFIGDIR=%s", install, bin, config)
	}
	if _, err := os.Stat(filepath.Join(bin, "muxcode")); err != nil {
		t.Errorf("stub not installed at %s: %v", bin, err)
	}
	if !strings.Contains(s.Results[4].Note, filepath.Join(bin, "muxcode")) {
		t.Errorf("Verify note %q, want it to run %s", s.Results[4].Note, filepath.Join(bin, "muxcode"))
	}
	if _, err := os.Stat(filepath.Join(s.SourceDir(), "rel-bin")); !os.IsNotExist(err) {
		t.Errorf("source tree gained rel-bin — make resolved BINDIR against its own cwd")
	}
	if root, err := filepath.Abs("rel-cache"); err != nil || s.Dir != filepath.Join(root, "v0.1.21") {
		t.Errorf("cache dir %q, want %s/v0.1.21 (err %v)", s.Dir, root, err)
	}
}

func TestUpgradePathsFollowMakefileDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CACHE_HOME", "BINDIR", "PREFIX", "CONFIGDIR"} {
		t.Setenv(k, "")
	}
	check := func(label string, opts SelfUpgradeOptions, root, bin, config string) {
		t.Helper()
		gr, gb, gc, err := upgradePaths(opts)
		if err != nil || gr != root || gb != bin || gc != config {
			t.Errorf("%s: (%q, %q, %q, %v), want (%q, %q, %q)", label, gr, gb, gc, err, root, bin, config)
		}
	}
	defaultRoot := filepath.Join(home, ".cache", "muxcode", "upgrade")
	check("defaults", SelfUpgradeOptions{}, defaultRoot, filepath.Join(home, ".local", "bin"), filepath.Join(home, ".config", "muxcode"))

	t.Setenv("PREFIX", "/opt/mux")
	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	check("PREFIX, relative XDG ignored", SelfUpgradeOptions{}, defaultRoot, "/opt/mux/bin", filepath.Join(home, ".config", "muxcode"))

	t.Setenv("BINDIR", "/usr/local/bin")
	t.Setenv("CONFIGDIR", "/etc/muxcode")
	t.Setenv("XDG_CACHE_HOME", "/var/cache")
	check("environment", SelfUpgradeOptions{}, "/var/cache/muxcode/upgrade", "/usr/local/bin", "/etc/muxcode")

	check("options", SelfUpgradeOptions{CacheRoot: "/c", BinDir: "/b", ConfigDir: "/d"}, "/c", "/b", "/d")
}
