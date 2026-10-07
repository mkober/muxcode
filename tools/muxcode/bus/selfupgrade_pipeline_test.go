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
// $FAKE_INSTALLED_VERSION, else the VERSION it was given, and whose
// `upgrade-daemons` logs its call and the muxcode its PATH resolves — the
// binary a real one relaunches daemons with — to $FAKE_DAEMONS_LOG, answers
// `--session <name>` with that one session restarted, and otherwise prints
// $FAKE_DAEMONS_OUT, $FAKE_DAEMONS_ERR on stderr, and exits
// $FAKE_DAEMONS_EXIT. FAKE_MAKE_FAIL names a target to fail. With
// FAKE_MAKE_BLOCK set, build touches $FAKE_MAKE_BLOCK.started and waits for
// $FAKE_MAKE_BLOCK to exist.
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
case "\$1" in
  version)
    echo '{"version":"$v","commit":"unknown","date":"2026-10-06T00:00:00Z"}' ;;
  upgrade-daemons)
    echo "\$* resolved=\$(command -v muxcode)" >> "\$FAKE_DAEMONS_LOG"
    if [ "\$2" = "--session" ]; then printf '  %s: daemon restarted\n' "\$3"; exit 0; fi
    printf '%s' "\$FAKE_DAEMONS_OUT"
    [ -n "\$FAKE_DAEMONS_ERR" ] && printf '%s\n' "\$FAKE_DAEMONS_ERR" >&2
    exit "\${FAKE_DAEMONS_EXIT:-0}" ;;
esac
EOF
  chmod 755 "$bindir/muxcode"
fi
echo "make $target done"
`

// fakeTmuxScript records each call to $FAKE_TMUX_LOG, and with
// FAKE_TMUX_NO_SERVER set answers as tmux does with no server running. It
// must stand ahead of the real tmux, which would otherwise source a scratch
// tmux.conf into the user's live server.
const fakeTmuxScript = `#!/bin/sh
echo "$*" >> "$FAKE_TMUX_LOG"
if [ -n "$FAKE_TMUX_NO_SERVER" ]; then
  echo "no server running on /private/tmp/tmux-501/default" >&2
  exit 1
fi
`

// fakeDaemonsOut is two sessions in cmd/upgrade.go's upgrade-daemons shape.
const fakeDaemonsOut = "  alpha: daemon v0.1.20 → installed v0.1.21 — daemon restarted\n" +
	"  beta: daemon v0.1.20 → installed v0.1.21 — daemon + monitor restarted\n"

type upgradeFixture struct {
	dir, cache, bindir, configdir, makeLog, tarball string
	daemonsLog, tmuxLog, session, fakebin           string
	opts                                            SelfUpgradeOptions
}

// newUpgradeFixture stands up a hermetic upgrade from installed to tag: a
// local releases/latest file and source tarball reached through file://
// URLs, the fake make, tmux and a stub go ahead of the real PATH, BINDIR
// first on PATH so the installed stub is the muxcode PATH resolves, HOME in
// the scratch dir so the per-user upgrade lock is the fixture's own, and a
// unique BUS_SESSION naming the lifecycle log the run writes.
func newUpgradeFixture(t *testing.T, installed, tag string) *upgradeFixture {
	t.Helper()
	setInstalledVersion(t, installed)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	f := &upgradeFixture{
		dir:        dir,
		cache:      filepath.Join(dir, "cache"),
		bindir:     filepath.Join(dir, "bin"),
		configdir:  filepath.Join(dir, "config"),
		makeLog:    filepath.Join(dir, "make.log"),
		tarball:    filepath.Join(dir, "muxcode-src.tar.gz"),
		daemonsLog: filepath.Join(dir, "daemons.log"),
		tmuxLog:    filepath.Join(dir, "tmux.log"),
		session:    fmt.Sprintf("selfupgrade-test-%d", time.Now().UnixNano()),
		fakebin:    filepath.Join(dir, "fakebin"),
	}
	latest := filepath.Join(dir, "latest.json")
	writeUpgradeFile(t, latest, fmt.Sprintf(`{"tag_name":%q,"published_at":"2026-10-06T12:00:00Z"}`, tag), 0o644)
	writeSourceTarball(t, f.tarball, "muxcode-"+strings.TrimPrefix(tag, "v"))
	fakebin := f.fakebin
	writeUpgradeFile(t, filepath.Join(fakebin, "make"), fakeMakeScript, 0o755)
	writeUpgradeFile(t, filepath.Join(fakebin, "tmux"), fakeTmuxScript, 0o755)
	writeUpgradeFile(t, filepath.Join(fakebin, "go"), "#!/bin/sh\nexit 0\n", 0o755)
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", f.bindir+sep+fakebin+sep+os.Getenv("PATH"))
	t.Setenv("BUS_SESSION", f.session)
	t.Setenv("FAKE_MAKE_LOG", f.makeLog)
	t.Setenv("FAKE_MAKE_FAIL", "")
	t.Setenv("FAKE_MAKE_BLOCK", "")
	t.Setenv("FAKE_INSTALLED_VERSION", "")
	t.Setenv("FAKE_DAEMONS_LOG", f.daemonsLog)
	t.Setenv("FAKE_DAEMONS_OUT", fakeDaemonsOut)
	t.Setenv("FAKE_DAEMONS_ERR", "")
	t.Setenv("FAKE_DAEMONS_EXIT", "0")
	t.Setenv("FAKE_TMUX_LOG", f.tmuxLog)
	t.Setenv("FAKE_TMUX_NO_SERVER", "")
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
	want := []string{"Check", "Download", "Build", "Install", "Verify", "Restart daemons", "Reload tmux config"}
	if got := stepNames(s.Results); !reflect.DeepEqual(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	for _, r := range s.Results {
		if !r.Success {
			t.Errorf("%s failed: %s", r.Name, r.Error)
		}
	}
	if !reflect.DeepEqual(indexes, []int{0, 1, 2, 3, 4, 5, 6}) {
		t.Errorf("progress indexes = %v, want 0..6", indexes)
	}

	daemons := s.Results[5]
	if got := stepNames(daemons.Sub); !reflect.DeepEqual(got, []string{"alpha", "beta"}) || !daemons.Sub[1].Success ||
		daemons.Sub[1].Note != "daemon v0.1.20 → installed v0.1.21 — daemon + monitor restarted" {
		t.Errorf("daemon sub-rows = %+v, want one per scripted session", daemons.Sub)
	}
	wantDaemons := "upgrade-daemons resolved=" + filepath.Join(f.bindir, "muxcode")
	if got, err := os.ReadFile(f.daemonsLog); err != nil || strings.TrimSpace(string(got)) != wantDaemons {
		t.Errorf("installed binary's upgrade-daemons calls = %q (err %v), want one: %q", got, err, wantDaemons)
	}
	if got, err := os.ReadFile(f.tmuxLog); err != nil || strings.TrimSpace(string(got)) != "source-file "+filepath.Join(f.configdir, "tmux.conf") {
		t.Errorf("tmux calls = %q (err %v), want source-file of the installed tmux.conf", got, err)
	}
	if done := s.DoneSummary(); !strings.Contains(done, "v0.1.20 → v0.1.21") || !strings.Contains(done, "Restart Agents (prefix + b, A)") {
		t.Errorf("DoneSummary() = %q, want the version delta and the agent follow-up", done)
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
		{"v0.1.21", true, 7, false},
		{"devel", true, 7, false},
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

// A failed Verify never reaches the daemon step: restarting daemons onto a
// binary that does not report the release would spread a broken install to
// every session. The install test, where Verify passes, is the half where
// upgrade-daemons does run.
func TestSelfUpgradeVerifyMismatchStopsPipeline(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	t.Setenv("FAKE_INSTALLED_VERSION", "v0.1.20")

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	if err == nil {
		t.Fatal("RunSelfUpgrade succeeded with a mismatched install")
	}
	last := s.Results[len(s.Results)-1]
	if last.Name != "Verify" || !strings.Contains(last.Error, "reports v0.1.20, want v0.1.21") {
		t.Errorf("last step %+v, want Verify naming both versions", last)
	}
	for _, log := range []string{f.daemonsLog, f.tmuxLog} {
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Errorf("%s exists — a step after the failed Verify ran", log)
		}
	}
}

// The daemon step is run through the new binary's upgrade-daemons, so its
// failures arrive as that command's output: an unreadable ps is its own
// message, surfaced verbatim, and a session that failed is a failed sub-row
// beside the ones that restarted. Either fails the step and stops the run.
func TestSelfUpgradeDaemonStepSurfacesFailures(t *testing.T) {
	cases := []struct {
		name, out, errOut, want string
		sub                     []bool
	}{
		{
			name:   "ps unreadable",
			errOut: "upgrade-daemons: listing daemon processes: ps: operation not permitted",
			want:   "upgrade-daemons: listing daemon processes: ps: operation not permitted",
		},
		{
			name: "one session failed",
			out:  "  alpha: daemon v0.1.20 → installed v0.1.21 — daemon restarted\n  beta: FAILED — kill 4242: no such process\n",
			want: "1 of 2 daemons failed to restart",
			sub:  []bool{true, false},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
			t.Setenv("FAKE_DAEMONS_OUT", c.out)
			t.Setenv("FAKE_DAEMONS_ERR", c.errOut)
			t.Setenv("FAKE_DAEMONS_EXIT", "1")

			s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
			last := s.Results[len(s.Results)-1]
			if err == nil || last.Name != "Restart daemons" || last.Error != c.want {
				t.Fatalf("err %v last %+v, want Restart daemons to fail with %q", err, last, c.want)
			}
			var got []bool
			for _, sub := range last.Sub {
				got = append(got, sub.Success)
			}
			if !reflect.DeepEqual(got, c.sub) {
				t.Errorf("sub-row outcomes = %v, want %v", got, c.sub)
			}
			if len(c.sub) > 0 && last.Sub[1].Error != "kill 4242: no such process" {
				t.Errorf("failed sub-row error %q, want the session's cause", last.Sub[1].Error)
			}
		})
	}
}

// With BINDIR off PATH, which Verify allows, upgrade-daemons must still
// resolve the installed muxcode when it relaunches each daemon as bare
// `muxcode` — or it kills every daemon and relaunches none. The PATH here
// holds no muxcode at all; the install test, BINDIR already first, is the
// passing control.
func TestSelfUpgradeDaemonRelaunchResolvesInstalledBinary(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	t.Setenv("PATH", strings.Join([]string{f.fakebin, "/usr/bin", "/bin"}, string(os.PathListSeparator)))

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	if err != nil {
		t.Fatalf("RunSelfUpgrade: %v (results %+v)", err, s.Results)
	}
	if !strings.Contains(s.Results[4].Note, "is not on PATH") {
		t.Errorf("Verify note %q, want BINDIR reported off PATH", s.Results[4].Note)
	}
	want := "upgrade-daemons resolved=" + filepath.Join(f.bindir, "muxcode")
	if got, err := os.ReadFile(f.daemonsLog); err != nil || strings.TrimSpace(string(got)) != want {
		t.Errorf("upgrade-daemons saw %q (err %v), want %q", got, err, want)
	}
}

func stubDaemonSessions(t *testing.T, sessions []string) {
	t.Helper()
	orig := upgradeDaemonSessionsFn
	upgradeDaemonSessionsFn = func() ([]string, error) { return sessions, nil }
	t.Cleanup(func() { upgradeDaemonSessionsFn = orig })
}

// A run bound to a confirmed release refuses any other before anything
// mutates: a release published after the confirm is never installed unasked.
// The same run against the release it confirmed is the passing half.
func TestSelfUpgradeRefusesAReleaseOtherThanConfirmed(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.22")
	stubDaemonSessions(t, []string{"alpha"})
	f.opts.Confirmed = &UpgradeConfirmation{Tag: "v0.1.21", Sessions: []string{"alpha"}}

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	if err == nil || len(s.Results) != 1 || !strings.Contains(s.Results[0].Error, "now v0.1.22, not the confirmed v0.1.21") {
		t.Fatalf("err %v results %+v, want Check to refuse the unconfirmed release", err, s.Results)
	}
	for _, p := range []string{f.cache, f.bindir, f.makeLog, f.daemonsLog} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists after the refusal — something mutated", p)
		}
	}

	f.opts.Confirmed.Tag = "v0.1.22"
	if s, err := RunSelfUpgrade(context.Background(), f.opts, nil); err != nil {
		t.Errorf("run against the confirmed release: %v (results %+v)", err, s.Results)
	}
}

// A confirmed run restarts only the sessions it was shown: a daemon started
// during the build stays on its build and is named, never restarted unasked.
// With no such daemon the note names none — the negative control.
func TestSelfUpgradeRestartsOnlyConfirmedDaemons(t *testing.T) {
	for _, c := range []struct {
		name    string
		running []string
		left    string
	}{
		{"daemon started after the confirm", []string{"alpha", "gamma"}, "left gamma on the old build"},
		{"unchanged", []string{"alpha"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
			stubDaemonSessions(t, c.running)
			f.opts.Confirmed = &UpgradeConfirmation{Tag: "v0.1.21", Sessions: []string{"alpha"}}

			s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
			if err != nil {
				t.Fatalf("RunSelfUpgrade: %v (results %+v)", err, s.Results)
			}
			want := "upgrade-daemons --session alpha resolved=" + filepath.Join(f.bindir, "muxcode")
			if got, err := os.ReadFile(f.daemonsLog); err != nil || strings.TrimSpace(string(got)) != want {
				t.Errorf("upgrade-daemons calls %q (err %v), want only %q", got, err, want)
			}
			daemons := s.Results[5]
			if got := stepNames(daemons.Sub); !reflect.DeepEqual(got, []string{"alpha"}) {
				t.Errorf("daemon sub-rows %v, want the confirmed session only", got)
			}
			if c.left != "" && !strings.Contains(daemons.Note, c.left) {
				t.Errorf("note %q, want it to name %q", daemons.Note, c.left)
			}
			if c.left == "" && strings.Contains(daemons.Note, "left") {
				t.Errorf("note %q names a session left behind with none started", daemons.Note)
			}
		})
	}
}

// No tmux server is a skip, not a failure: there is nothing to reload. The
// install test, with a server answering, is the half that sources the file.
func TestSelfUpgradeTmuxReloadSkipsWithoutServer(t *testing.T) {
	f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
	t.Setenv("FAKE_TMUX_NO_SERVER", "1")

	s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
	if err != nil {
		t.Fatalf("RunSelfUpgrade: %v", err)
	}
	last := s.Results[len(s.Results)-1]
	if last.Name != "Reload tmux config" || !last.Success || last.Note != "skipped — no tmux server is running" {
		t.Errorf("last step %+v, want a skipped tmux reload", last)
	}
}

// Every step writes its row with both versions, closed by upgrade-done; a
// failed run's last rows are the failing step and upgrade-failed naming it
// and the build log.
func TestSelfUpgradeWritesLifecycleRows(t *testing.T) {
	events := func(t *testing.T, session string) ([]string, []LifecycleEntry) {
		t.Helper()
		entries, err := ReadLifecycleLog(session)
		if err != nil {
			t.Fatalf("ReadLifecycleLog: %v", err)
		}
		var names []string
		var rows []LifecycleEntry
		for _, e := range entries {
			if e.Source == "upgrade" {
				names = append(names, e.Event)
				rows = append(rows, e)
			}
		}
		return names, rows
	}

	t.Run("done", func(t *testing.T) {
		f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
		if _, err := RunSelfUpgrade(context.Background(), f.opts, nil); err != nil {
			t.Fatalf("RunSelfUpgrade: %v", err)
		}
		names, rows := events(t, f.session)
		want := []string{"upgrade-check", "upgrade-download", "upgrade-build", "upgrade-install",
			"upgrade-verify", "upgrade-daemons", "upgrade-tmux", "upgrade-done"}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("lifecycle events = %v, want %v", names, want)
		}
		for _, e := range rows {
			if !strings.HasPrefix(e.Detail, "installed=v0.1.20 target=v0.1.21 ") {
				t.Errorf("%s detail %q does not name both versions", e.Event, e.Detail)
			}
		}
	})

	t.Run("failed", func(t *testing.T) {
		f := newUpgradeFixture(t, "v0.1.20", "v0.1.21")
		t.Setenv("FAKE_MAKE_FAIL", "build")
		s, err := RunSelfUpgrade(context.Background(), f.opts, nil)
		if err == nil {
			t.Fatal("RunSelfUpgrade succeeded with a failing build")
		}
		names, rows := events(t, f.session)
		if n := len(names); n < 2 || names[n-2] != "upgrade-build" || names[n-1] != "upgrade-failed" {
			t.Fatalf("lifecycle events = %v, want upgrade-build then upgrade-failed last", names)
		}
		failed := rows[len(rows)-1]
		if failed.Level != "error" || !strings.Contains(failed.Detail, "Build: ") || !strings.Contains(failed.Detail, s.LogPath()) {
			t.Errorf("upgrade-failed %+v, want an error naming Build and %s", failed, s.LogPath())
		}
	})
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
