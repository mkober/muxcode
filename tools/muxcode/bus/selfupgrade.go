package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// UpgradeVerdict is how the installed build stands against the latest
// release. The values are part of the `muxcode upgrade --check --json`
// output contract.
type UpgradeVerdict string

const (
	UpgradeCurrent UpgradeVerdict = "current"
	UpgradeAhead   UpgradeVerdict = "ahead"
	UpgradeNewer   UpgradeVerdict = "newer"
	UpgradeUnknown UpgradeVerdict = "unknown"
)

const (
	upgradeManifestName   = "download.json"
	upgradeTarballName    = "source.tar.gz"
	upgradeSourceName     = "src"
	upgradeLogName        = "build.log"
	upgradeLockName       = "upgrade.lock"
	upgradeMakeTimeout    = 15 * time.Minute
	upgradeDaemonsTimeout = 2 * time.Minute
	upgradeCommandTimeout = 10 * time.Second
	upgradeLogTailLines   = 5
)

// upgradeBuildTools are what the Build step runs; Check probes them so a
// machine without a toolchain fails before anything is downloaded.
var upgradeBuildTools = []string{"go", "make", "tar"}

// UpgradeCheck is the result of the self-upgrade Check step (MUX-202). The
// JSON names are the `muxcode upgrade --check --json` output contract.
type UpgradeCheck struct {
	Installed    Info           `json:"installed"`
	Latest       Release        `json:"latest"`
	Verdict      UpgradeVerdict `json:"verdict"`
	Reason       string         `json:"reason,omitempty"`
	MissingTools []string       `json:"missing_tools,omitempty"`
}

// CheckUpgrade runs the Check step: the installed BuildInfo, the latest
// release from rc, the verdict and the build-tool probe. Only a failed
// release lookup is an error, returned with Installed still filled. An
// unknown verdict and missing tools are reported on the result for the
// caller to decide whether they block — a forced upgrade proceeds past the
// first and never past the second.
func CheckUpgrade(ctx context.Context, rc ReleaseClient) (UpgradeCheck, error) {
	check := UpgradeCheck{Installed: BuildInfo()}
	latest, err := rc.LatestRelease(ctx)
	if err != nil {
		return check, err
	}
	check.Latest = latest
	check.Verdict, check.Reason = ClassifyUpgrade(check.Installed.Version, latest.Tag)
	check.MissingTools = missingTools(upgradeBuildTools)
	return check, nil
}

// ClassifyUpgrade compares an installed version with the latest release tag
// (MUX-202 Decision 2): newer only when the tag is strictly past the
// installed version. A `git describe` build past the tag reads ahead, so a
// hot fix never downgrades a tree that is ahead of the release. A version
// with no semver rank — "devel", a bare commit — is unknown, with the
// comparison error as the reason.
func ClassifyUpgrade(installed, latest string) (UpgradeVerdict, string) {
	c, err := CompareSemver(latest, installed)
	switch {
	case err != nil:
		return UpgradeUnknown, err.Error()
	case c > 0:
		return UpgradeNewer, ""
	case c < 0:
		return UpgradeAhead, ""
	}
	return UpgradeCurrent, ""
}

// Summary is the verdict line the CLI prints and the modal shows.
func (c UpgradeCheck) Summary() string {
	have, latest := c.Installed.Version, c.Latest.Tag
	switch c.Verdict {
	case UpgradeNewer:
		return fmt.Sprintf("installed %s → latest %s available", have, latest)
	case UpgradeAhead:
		return fmt.Sprintf("installed %s is ahead of the latest release %s", have, latest)
	case UpgradeCurrent:
		return fmt.Sprintf("installed %s is current (latest %s)", have, latest)
	}
	return fmt.Sprintf("installed %s cannot be compared with the latest release %s: %s", have, latest, c.Reason)
}

// ToolsErr names the build tools Check found missing, or is nil.
func (c UpgradeCheck) ToolsErr() error {
	if len(c.MissingTools) == 0 {
		return nil
	}
	return fmt.Errorf("missing build tools: %s — install them to upgrade", strings.Join(c.MissingTools, ", "))
}

func missingTools(names []string) []string {
	var missing []string
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}

// SelfUpgradeOptions configures RunSelfUpgrade. Empty paths resolve the way
// the Makefile resolves them (see upgradePaths), so the pipeline installs
// where a manual `make install` would and Verify reads that same directory.
type SelfUpgradeOptions struct {
	Force     bool // proceed when Check finds nothing newer, or cannot tell
	Client    ReleaseClient
	CacheRoot string
	BinDir    string
	ConfigDir string
	Confirmed *UpgradeConfirmation // nil when no one confirmed a target — the CLI acting on what it finds
}

// UpgradeConfirmation binds a run to what its user confirmed: the release
// and the session daemons they were shown. Check refuses any other release
// before anything mutates, and Restart daemons restarts only these sessions —
// a release published, or a daemon started, after the confirm is never
// upgraded onto unasked.
type UpgradeConfirmation struct {
	Tag      string
	Sessions []string
}

// SourceTarball is the downloaded release source. It is recorded in the
// cache's download.json only after the tree is fully extracted, so the
// record's presence is what makes a later run a cache hit.
type SourceTarball struct {
	Tag    string `json:"tag"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Cached bool   `json:"-"`
}

// StepResult is one pipeline row: the modal renders it and the CLI prints
// it. Sub holds a step's own rows — Restart daemons has one per session.
// JSON names are the `muxcode upgrade --json` contract.
type StepResult struct {
	Name     string        `json:"name"`
	Success  bool          `json:"success"`
	Note     string        `json:"note,omitempty"`
	Error    string        `json:"error,omitempty"`
	Sub      []StepResult  `json:"sub,omitempty"`
	Duration time.Duration `json:"duration_ns"`
}

// UpgradeProgress is called as each step finishes, as ReloadProgress is
// called per agent; the step after index is the one now running.
type UpgradeProgress func(index int, result StepResult)

// UpgradeState is one self-upgrade run, filled in step by step. Dir is the
// release's cache directory, <cache root>/<tag>. Stopped reports a run that
// ended successfully at Check: nothing newer, and not forced. sub holds the
// running step's sub-rows until the runner moves them onto its row.
type UpgradeState struct {
	Options   SelfUpgradeOptions
	Check     UpgradeCheck
	Tarball   SourceTarball
	Verified  Info
	Dir       string
	BinDir    string
	ConfigDir string
	Date      string
	Results   []StepResult
	Stopped   bool
	sub       []StepResult
}

// DoneSummary is a finished run's closing line: the version delta, and the
// follow-up the upgrade does not do — running agents keep the old build
// until restarted.
func (s *UpgradeState) DoneSummary() string {
	if s.Stopped {
		return "nothing to upgrade — " + s.Check.Summary()
	}
	return fmt.Sprintf("upgraded %s → %s; agents keep running until restarted — Restart Agents (prefix + b, A)",
		s.Check.Installed.Version, s.Check.Latest.Tag)
}

// SourceDir is the extracted release tree make runs in.
func (s *UpgradeState) SourceDir() string { return filepath.Join(s.Dir, upgradeSourceName) }

// LogPath is the build and install output, kept for a failed step to name.
func (s *UpgradeState) LogPath() string { return filepath.Join(s.Dir, upgradeLogName) }

// upgradeStep is one pipeline step. Event names its lifecycle row. Mutates
// marks a step that writes the cache, the install destinations or running
// processes, and so must run under the upgrade lock.
type upgradeStep struct {
	Name    string
	Event   string
	Mutates bool
	Run     func(context.Context, *UpgradeState) (string, error)
}

func selfUpgradeSteps() []upgradeStep {
	return []upgradeStep{
		{Name: "Check", Event: "upgrade-check", Run: runCheckStep},
		{Name: "Download", Event: "upgrade-download", Mutates: true, Run: runDownloadStep},
		{Name: "Build", Event: "upgrade-build", Mutates: true, Run: runBuildStep},
		{Name: "Install", Event: "upgrade-install", Mutates: true, Run: runInstallStep},
		{Name: "Verify", Event: "upgrade-verify", Mutates: true, Run: runVerifyStep},
		{Name: "Restart daemons", Event: "upgrade-daemons", Mutates: true, Run: runDaemonsStep},
		{Name: "Reload tmux config", Event: "upgrade-tmux", Mutates: true, Run: runTmuxStep},
	}
}

// RunSelfUpgrade runs the self-upgrade pipeline (MUX-202): Check, Download,
// Build, Install, Verify, Restart daemons, Reload tmux config. It stops at
// the first failed step, returning that step's error, and stops with
// success after Check when nothing is newer and the run is not forced — an
// up-to-date install touches no file. The state carries every finished
// step's row in Results.
//
// The steps after Check run under the cross-process upgrade lock
// (lockSelfUpgrade); a run that finds it held fails at its first mutating
// step without touching the cache or the install. Every step writes a
// lifecycle row, and the run ends with upgrade-done or upgrade-failed.
func RunSelfUpgrade(ctx context.Context, opts SelfUpgradeOptions, progress UpgradeProgress) (*UpgradeState, error) {
	s := newUpgradeState(opts)
	return s, runUpgradeSteps(ctx, s, selfUpgradeSteps(), progress)
}

// newUpgradeState stamps the run's build date once, so Build and Install
// pass make the same DATE and install's rebuild of the phony build target
// links the binary Build already compiled.
func newUpgradeState(opts SelfUpgradeOptions) *UpgradeState {
	return &UpgradeState{Options: opts, Date: time.Now().UTC().Format("2006-01-02T15:04:05Z")}
}

// runUpgradeSteps takes the upgrade lock before the first mutating step and
// holds it until the pipeline returns, and writes each step's lifecycle row,
// so the lock and the log live at this boundary rather than in each step.
func runUpgradeSteps(ctx context.Context, s *UpgradeState, steps []upgradeStep, progress UpgradeProgress) error {
	session := BusSession()
	var unlock func()
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	for i, step := range steps {
		start := time.Now()
		var note string
		var err error
		if step.Mutates && unlock == nil {
			unlock, err = lockSelfUpgrade()
		}
		if err == nil {
			note, err = step.Run(ctx, s)
		}
		r := StepResult{Name: step.Name, Success: err == nil, Note: note, Sub: s.sub, Duration: time.Since(start)}
		s.sub = nil
		level, detail := "info", r.Note
		if err != nil {
			r.Error = err.Error()
			level, detail = "error", r.Error
		}
		s.Results = append(s.Results, r)
		s.logLifecycle(session, level, step.Event, detail)
		if progress != nil {
			progress(i, r)
		}
		if err != nil {
			s.logLifecycle(session, "error", "upgrade-failed", step.Name+": "+r.Error)
			return fmt.Errorf("%s: %w", step.Name, err)
		}
		if s.Stopped {
			break
		}
	}
	s.logLifecycle(session, "info", "upgrade-done", s.DoneSummary())
	return nil
}

// logLifecycle writes one upgrade row carrying the installed and target
// versions, so the log alone tells which upgrade a row belongs to.
func (s *UpgradeState) logLifecycle(session, level, event, detail string) {
	LogLifecycle(session, level, "upgrade", event,
		fmt.Sprintf("installed=%s target=%s %s", s.Check.Installed.Version, s.Check.Latest.Tag, detail))
}

// lockSelfUpgrade takes the exclusive per-user upgrade lock,
// ~/.config/muxcode/upgrade.lock, without waiting, and returns its release.
// Two runs sharing a cache would otherwise both miss the cache record, one
// removing the tree the other is extracting, and two runs with different
// cache roots but one BINDIR or CONFIGDIR would make install over each other
// and invalidate the other's Verify. So the lock's path depends on none of the
// overrides: one lock per user covers every cache and every destination. Two
// users installing into one shared prefix are not serialized. A held lock is
// refused rather than waited on: a build can take minutes, and the modal or
// CLI caller should say so at once.
func lockSelfUpgrade() (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("upgrade lock: %w", err)
	}
	dir := filepath.Join(home, ".config", "muxcode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("upgrade lock: %w", err)
	}
	path := filepath.Join(dir, upgradeLockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("upgrade lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another muxcode upgrade is running (%s is locked) — wait for it to finish", path)
		}
		return nil, fmt.Errorf("upgrade lock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// runCheckStep decides whether the run proceeds. Nothing newer stops the
// run successfully unless forced; an unknown verdict cannot tell newer from
// ahead, so unforced it fails rather than guess. A proceeding run needs
// every build tool and resolvable install paths.
func runCheckStep(ctx context.Context, s *UpgradeState) (string, error) {
	check, err := CheckUpgrade(ctx, s.Options.Client)
	s.Check = check
	if err != nil {
		return "", err
	}
	if c := s.Options.Confirmed; c != nil && check.Latest.Tag != c.Tag {
		return "", fmt.Errorf("the latest release is now %s, not the confirmed %s — nothing was changed; confirm again", check.Latest.Tag, c.Tag)
	}
	note := check.Summary()
	if check.Verdict != UpgradeNewer && !s.Options.Force {
		if check.Verdict == UpgradeUnknown {
			return "", fmt.Errorf("%s — force to install it anyway", note)
		}
		s.Stopped = true
		return note, nil
	}
	if err := check.ToolsErr(); err != nil {
		return note, err
	}
	root, bin, config, err := upgradePaths(s.Options)
	if err != nil {
		return note, err
	}
	s.Dir, s.BinDir, s.ConfigDir = filepath.Join(root, check.Latest.Tag), bin, config
	if check.Verdict != UpgradeNewer {
		note += " — forced"
	}
	return note, nil
}

// upgradePaths resolves the cache root, BINDIR and CONFIGDIR, options
// first: the cache under an absolute $XDG_CACHE_HOME, else ~/.cache; BINDIR
// from the environment, else $PREFIX/bin, else ~/.local/bin; CONFIGDIR from
// the environment, else ~/.config/muxcode — the Makefile's own defaults.
//
// Every path is made absolute against the caller's working directory before
// anything mutates. make runs in the extracted tree and Verify in the caller's
// directory, so a relative BINDIR=out would install into <cache>/<tag>/src/out
// while Verify ran ./out/muxcode, and a relative CONFIGDIR would write
// muxcode's config into the source cache.
func upgradePaths(o SelfUpgradeOptions) (root, bin, config string, err error) {
	root, bin, config = o.CacheRoot, o.BinDir, o.ConfigDir
	if x := os.Getenv("XDG_CACHE_HOME"); root == "" && filepath.IsAbs(x) {
		root = filepath.Join(x, "muxcode", "upgrade")
	}
	if bin == "" {
		bin = os.Getenv("BINDIR")
	}
	if p := os.Getenv("PREFIX"); bin == "" && p != "" {
		bin = filepath.Join(p, "bin")
	}
	if config == "" {
		config = os.Getenv("CONFIGDIR")
	}
	if root == "" || bin == "" || config == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", "", fmt.Errorf("resolving install paths: %w", herr)
		}
		if root == "" {
			root = filepath.Join(home, ".cache", "muxcode", "upgrade")
		}
		if bin == "" {
			bin = filepath.Join(home, ".local", "bin")
		}
		if config == "" {
			config = filepath.Join(home, ".config", "muxcode")
		}
	}
	for _, p := range []*string{&root, &bin, &config} {
		if *p, err = filepath.Abs(*p); err != nil {
			return "", "", "", fmt.Errorf("resolving install paths: %w", err)
		}
	}
	return root, bin, config, nil
}

func runDownloadStep(ctx context.Context, s *UpgradeState) (string, error) {
	tb, err := fetchSource(ctx, s.Options.Client, s.Check.Latest, s.Dir)
	if err != nil {
		return "", err
	}
	s.Tarball = tb
	note := fmt.Sprintf("%s, sha256 %s", formatBytes(tb.Size), tb.SHA256)
	if tb.Cached {
		note = "cache hit — " + note
	}
	return note, nil
}

// fetchSource makes <dir>/src the extracted release tree, downloading only
// when the cache does not already hold it complete. The download.json
// record is removed first and written last, so a run interrupted anywhere
// between leaves no record and the next run refetches.
func fetchSource(ctx context.Context, rc ReleaseClient, rel Release, dir string) (SourceTarball, error) {
	if tb, ok := cachedSource(dir, rel.Tag); ok {
		return tb, nil
	}
	manifest := filepath.Join(dir, upgradeManifestName)
	src := filepath.Join(dir, upgradeSourceName)
	for _, p := range []string{manifest, src, src + ".partial"} {
		if err := os.RemoveAll(p); err != nil {
			return SourceTarball{}, fmt.Errorf("clearing stale cache: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SourceTarball{}, err
	}
	tarball := filepath.Join(dir, upgradeTarballName)
	size, sum, err := rc.Download(ctx, rel.TarballURL, tarball)
	if err != nil {
		return SourceTarball{}, err
	}
	if err := extractSource(ctx, tarball, src); err != nil {
		return SourceTarball{}, err
	}
	tb := SourceTarball{Tag: rel.Tag, URL: rel.TarballURL, Size: size, SHA256: sum}
	data, err := json.Marshal(tb)
	if err == nil {
		err = os.WriteFile(manifest, data, 0o644)
	}
	if err != nil {
		return SourceTarball{}, fmt.Errorf("recording download: %w", err)
	}
	return tb, nil
}

func cachedSource(dir, tag string) (SourceTarball, bool) {
	data, err := os.ReadFile(filepath.Join(dir, upgradeManifestName))
	if err != nil {
		return SourceTarball{}, false
	}
	var tb SourceTarball
	if json.Unmarshal(data, &tb) != nil || tb.Tag != tag {
		return SourceTarball{}, false
	}
	if _, err := os.Stat(filepath.Join(dir, upgradeSourceName, "Makefile")); err != nil {
		return SourceTarball{}, false
	}
	tb.Cached = true
	return tb, true
}

// extractSource unpacks tarball into dest through dest+".partial", dropping
// the archive's single top-level directory (GitHub's muxcode-<version>/). A
// tree with no top-level Makefile is not a muxcode source and fails here
// rather than as a confusing make error.
func extractSource(ctx context.Context, tarball, dest string) error {
	partial := dest + ".partial"
	if err := os.MkdirAll(partial, 0o755); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "tar", "-xzf", tarball, "-C", partial, "--strip-components=1").CombinedOutput()
	if err != nil {
		err = fmt.Errorf("tar: %v: %s", err, bodyExcerpt(out))
	} else if _, serr := os.Stat(filepath.Join(partial, "Makefile")); serr != nil {
		err = errors.New("no Makefile at the archive's top level — not a muxcode source tree")
	} else {
		err = os.Rename(partial, dest)
	}
	if err != nil {
		os.RemoveAll(partial)
		return fmt.Errorf("extracting %s: %w", tarball, err)
	}
	return nil
}

func runBuildStep(ctx context.Context, s *UpgradeState) (string, error) {
	if err := runMake(ctx, s, "build"); err != nil {
		return "", err
	}
	return "make build VERSION=" + s.Check.Latest.Tag + " — log: " + s.LogPath(), nil
}

// runInstallStep runs only after a successful Build, so a tree that does not
// compile never reaches `install -m 755` and the previous binary stays.
func runInstallStep(ctx context.Context, s *UpgradeState) (string, error) {
	if err := runMake(ctx, s, "install"); err != nil {
		return "", err
	}
	return fmt.Sprintf("installed to %s and %s", s.BinDir, s.ConfigDir), nil
}

// runMake runs one make target in the extracted tree with VERSION, DATE,
// BINDIR and CONFIGDIR on the command line, so make installs exactly where
// Verify looks. Output goes to build.log, truncated by build and appended
// by install; a failure names the log and quotes its last lines.
//
// GIT_CEILING_DIRECTORIES and -buildvcs=false keep git and the Go toolchain
// from stamping the build with whatever repository encloses the cache: a
// $HOME under version control would otherwise lend the binary its commit,
// or fail the build on that repository's VCS status.
func runMake(ctx context.Context, s *UpgradeState, target string) error {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if target == "build" {
		flags |= os.O_TRUNC
	}
	log, err := os.OpenFile(s.LogPath(), flags, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()

	ctx, cancel := context.WithTimeout(ctx, upgradeMakeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", target,
		"VERSION="+s.Check.Latest.Tag, "DATE="+s.Date, "BINDIR="+s.BinDir, "CONFIGDIR="+s.ConfigDir)
	cmd.Dir = s.SourceDir()
	cmd.Env = append(os.Environ(),
		"PWD="+cmd.Dir,
		"GIT_CEILING_DIRECTORIES="+s.Dir,
		"GOFLAGS="+strings.TrimSpace(os.Getenv("GOFLAGS")+" -buildvcs=false"))
	cmd.Stdout, cmd.Stderr = log, log
	fmt.Fprintf(log, "$ make %s\n", strings.Join(cmd.Args[1:], " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make %s failed (%v): %s — log: %s",
			target, err, strings.Join(tailLines(s.LogPath(), upgradeLogTailLines), " | "), s.LogPath())
	}
	return nil
}

// tailLines returns the last n non-blank lines of the file at path.
func tailLines(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// runVerifyStep runs the freshly installed binary by path and requires it to
// report the release tag, then requires `muxcode` on PATH to be that same
// file. A different BINDIR leaves the old binary where Verify looks, and a
// PATH entry ahead of BINDIR keeps every later `muxcode` call — tmux
// bindings, daemon relaunches — on the old build; either fails here rather
// than as a daemon restart onto stale code. BINDIR absent from PATH is a
// note, not a failure: nothing on PATH shadows the install.
func runVerifyStep(ctx context.Context, s *UpgradeState) (string, error) {
	bin := filepath.Join(s.BinDir, "muxcode")
	ctx, cancel := context.WithTimeout(ctx, upgradeCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version", "--json").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			err = fmt.Errorf("%v: %s", err, bodyExcerpt(ee.Stderr))
		}
		return "", fmt.Errorf("running %s version --json: %w", bin, err)
	}
	var info Info
	if err := json.Unmarshal(out, &info); err != nil {
		return "", fmt.Errorf("%s version --json: %v (output: %s)", bin, err, bodyExcerpt(out))
	}
	s.Verified = info
	if info.Version != s.Check.Latest.Tag {
		return "", fmt.Errorf("%s reports %s, want %s", bin, info.Version, s.Check.Latest.Tag)
	}
	note := fmt.Sprintf("%s reports %s", bin, info.Version)
	onPath, err := exec.LookPath("muxcode")
	if err != nil {
		return note + " — " + s.BinDir + " is not on PATH", nil
	}
	if !sameFile(onPath, bin) {
		return "", fmt.Errorf("muxcode on PATH is %s, not the installed %s — put %s ahead of it on PATH", onPath, bin, s.BinDir)
	}
	return note, nil
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// runDaemonsStep runs the freshly installed binary's upgrade-daemons, never
// bus.UpgradeDaemons in-process: this process is the old build, whose
// BuildInfo would read every daemon as current and skip them all (MUX-202
// Decision 3). It covers every session on the machine. Each session becomes
// a sub-row; a non-zero exit fails the step with the command's own message
// lines verbatim — an unreadable ps among them.
//
// A confirmed run (SelfUpgradeOptions.Confirmed) restarts only its confirmed
// sessions; see runConfirmedDaemons.
func runDaemonsStep(ctx context.Context, s *UpgradeState) (string, error) {
	if c := s.Options.Confirmed; c != nil {
		return runConfirmedDaemons(ctx, s, c.Sessions)
	}
	rows, messages, runErr := runUpgradeDaemons(ctx, s)
	s.sub = rows
	if err := daemonsError(rows, messages, runErr); err != nil {
		return "", err
	}
	if len(messages) > 0 {
		return strings.Join(messages, "; "), nil
	}
	return fmt.Sprintf("%d session daemon(s) via %s upgrade-daemons", len(rows), filepath.Join(s.BinDir, "muxcode")), nil
}

// runConfirmedDaemons restarts exactly the confirmed sessions' daemons, one
// `upgrade-daemons --session` each, so a daemon started after the confirm is
// never restarted onto a build its user did not agree to. Such a daemon is
// named in the note, left for `muxcode upgrade-daemons`.
func runConfirmedDaemons(ctx context.Context, s *UpgradeState, confirmed []string) (string, error) {
	var rows []StepResult
	var notes, failures []string
	for _, session := range confirmed {
		r, messages, runErr := runUpgradeDaemons(ctx, s, "--session", session)
		rows = append(rows, r...)
		if err := daemonsError(r, messages, runErr); err != nil {
			failures = append(failures, session+": "+err.Error())
			continue
		}
		notes = append(notes, messages...)
	}
	s.sub = rows
	if len(failures) > 0 {
		return "", errors.New(strings.Join(failures, "; "))
	}
	notes = append(notes, fmt.Sprintf("restarted the %d confirmed session daemon(s)", len(confirmed)))
	running, err := upgradeDaemonSessionsFn()
	if err != nil {
		notes = append(notes, "could not check for daemons started since the confirm: "+err.Error())
	}
	var unconfirmed []string
	for _, session := range running {
		if !slices.Contains(confirmed, session) {
			unconfirmed = append(unconfirmed, session)
		}
	}
	if len(unconfirmed) > 0 {
		notes = append(notes, fmt.Sprintf("left %s on the old build — started after the confirm; `muxcode upgrade-daemons` restarts it",
			strings.Join(unconfirmed, ", ")))
	}
	return strings.Join(notes, "; "), nil
}

// runUpgradeDaemons runs the installed binary's upgrade-daemons with args and
// splits its output into session rows and messages.
//
// BINDIR goes first on the helper's PATH: upgrade-daemons kills each daemon
// and relaunches it as bare `muxcode`, so with BINDIR off PATH — which Verify
// allows — it would relaunch the old build, or nothing, and leave every
// session without a daemon.
func runUpgradeDaemons(ctx context.Context, s *UpgradeState, args ...string) ([]StepResult, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, upgradeDaemonsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(s.BinDir, "muxcode"), append([]string{"upgrade-daemons"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+s.BinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	rows, messages := parseDaemonLines(string(out))
	return rows, messages, err
}

// daemonsError is an upgrade-daemons failure in its own words when it gave
// any, else the count of sessions that failed, else the exit; nil on success.
func daemonsError(rows []StepResult, messages []string, runErr error) error {
	if runErr == nil {
		return nil
	}
	if len(messages) > 0 {
		return errors.New(strings.Join(messages, "; "))
	}
	failed := 0
	for _, r := range rows {
		if !r.Success {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d daemons failed to restart", failed, len(rows))
	}
	return fmt.Errorf("upgrade-daemons: %w", runErr)
}

// parseDaemonLines splits upgrade-daemons output (cmd/upgrade.go's contract):
// an indented "<session>: <detail>" line is that session's sub-row, failed
// when the detail starts "FAILED — "; any other line is a message about the
// run as a whole. tmux forbids ':' in a session name, so the first ": "
// ends it.
func parseDaemonLines(out string) (rows []StepResult, messages []string) {
	for _, line := range strings.Split(out, "\n") {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		session, detail, ok := strings.Cut(text, ": ")
		if !ok || !strings.HasPrefix(line, "  ") {
			messages = append(messages, text)
			continue
		}
		if cause, failed := strings.CutPrefix(detail, "FAILED — "); failed {
			rows = append(rows, StepResult{Name: session, Error: cause})
		} else {
			rows = append(rows, StepResult{Name: session, Success: true, Note: detail})
		}
	}
	return rows, messages
}

// runTmuxStep sources the reinstalled tmux.conf into the running tmux server,
// so the upgraded menu and bindings apply without restarting tmux. No tmux
// server, or no tmux at all, is a skip with a note: there is nothing to
// reload.
func runTmuxStep(ctx context.Context, s *UpgradeState) (string, error) {
	conf := filepath.Join(s.ConfigDir, "tmux.conf")
	if _, err := exec.LookPath("tmux"); err != nil {
		return "skipped — tmux is not installed", nil
	}
	ctx, cancel := context.WithTimeout(ctx, upgradeCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "source-file", conf).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to") {
			return "skipped — no tmux server is running", nil
		}
		return "", fmt.Errorf("tmux source-file %s: %v: %s", conf, err, msg)
	}
	return "sourced " + conf, nil
}
