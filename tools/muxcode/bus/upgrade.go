package bus

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DaemonProc describes a running daemon or monitor process, discovered from
// its bus directory's pid stamp or from ps.
type DaemonProc struct {
	PID     int
	Session string
	Monitor bool
}

// UpgradePlan describes what UpgradeDaemons will do for one session.
type UpgradePlan struct {
	Session     string
	DaemonPID   int  // 0 if no daemon process found
	MonitorPID  int  // 0 if no monitor process found
	Orphan      bool // tmux session gone — kill without relaunch
	DaemonBuild Info // identity the daemon recorded at startup; zero Version when it never did
	Installed   Info // identity of the binary running this upgrade
	Current     bool // daemon already runs Installed — skipped unless forced; never set on an orphan
}

// UpgradeOptions controls UpgradeDaemons.
type UpgradeOptions struct {
	DryRun  bool   // report the plan without touching any process
	Force   bool   // cycle daemons already on the installed build too
	Session string // act on this session's daemon only; empty means every session
}

// UpgradeResult records the outcome of cycling one session's daemon.
type UpgradeResult struct {
	UpgradePlan
	Skipped          bool // daemon was current and Force was not set
	DaemonRestarted  bool
	MonitorRestarted bool
	Err              error
}

// VersionDelta renders the daemon-versus-installed comparison for one line
// of output: "daemon v0.1.0 → installed v0.2.0". Equal version strings that
// are still different builds (a dirty-tree rebuild) show the build dates,
// since "v0.1.0-dirty → v0.1.0-dirty" would read as a no-op.
func (p UpgradePlan) VersionDelta() string {
	installed := p.Installed.Version
	switch {
	case p.DaemonBuild.Version == "":
		return fmt.Sprintf("daemon (unstamped) → installed %s", installed)
	case p.Current:
		return fmt.Sprintf("daemon %s → installed %s (current)", p.DaemonBuild.Version, installed)
	case p.DaemonBuild.Version == installed:
		return fmt.Sprintf("daemon %s (built %s) → installed %s (built %s)",
			p.DaemonBuild.Version, p.DaemonBuild.Date, installed, p.Installed.Date)
	}
	return fmt.Sprintf("daemon %s → installed %s", p.DaemonBuild.Version, installed)
}

// psOutputFn is the seam tests replace; production lists every process.
var psOutputFn = func() ([]byte, error) {
	return exec.Command("ps", "-axo", "pid=,command=").Output()
}

// ListDaemonProcs discovers all running "muxcode watch" daemon and monitor
// processes across every session on this machine. ps is authoritative when it
// runs. A Codex build sandbox refuses to exec it (MUX-161), and discovery was
// ps alone, so every build there skipped the rollout; there the holders of
// the bus directories' daemon and monitor locks are used instead — identity
// the kernel attests, never a pid file's say-so — and the ps failure is
// logged as info to the caller's session rather than returned. An orphan
// whose bus directory is gone is then missed.
func ListDaemonProcs() ([]DaemonProc, error) {
	procs, _ := discoverDaemonProcs()
	return procs, nil
}

// discoverDaemonProcs is ListDaemonProcs also returning why ps could not run,
// which a rollout that missed a live daemon names.
func discoverDaemonProcs() ([]DaemonProc, error) {
	out, err := psOutputFn()
	if err == nil {
		return parseDaemonProcs(string(out)), nil
	}
	procs := listLockHolderProcs()
	if session := os.Getenv("BUS_SESSION"); session != "" {
		LogLifecycle(session, "info", "upgrade", "daemon-discovery-ps-failed",
			fmt.Sprintf("ps: %v — %d daemon/monitor process(es) found as lock holders", err, len(procs)))
	}
	return procs, err
}

// unrolledReason says why a live session's daemon was left on an older build
// by a rollout, or returns "" when the rollout accounted for it: a plan found
// the session's daemon (cycled, current, or failed on its own), no daemon is
// alive there, or the one that is already runs the installed build. A plan
// that found only the monitor does not count — an older daemon holding no
// owner lock beside a newer monitor would otherwise stay stale unreported. Before MUX-161 a
// codex build sandbox's ps refusal skipped the rollout with nothing but a
// stderr line in the build pane, while the build read green.
func unrolledReason(session string, plans []UpgradePlan, psErr error, alive bool, build Info, built bool, installed Info) string {
	if session == "" || !alive || (built && build.SameBuild(installed)) {
		return ""
	}
	for _, p := range plans {
		if p.Session == session && p.DaemonPID != 0 {
			return ""
		}
	}
	found := "ps lists no daemon for it"
	if psErr != nil {
		found = fmt.Sprintf("ps could not run (%v) and no process holds its daemon.owner lock", psErr)
	}
	delta := UpgradePlan{DaemonBuild: build, Installed: installed}.VersionDelta()
	return fmt.Sprintf("daemon for %s is live but was not found — %s; it stays on the old build (%s). Run `muxcode upgrade-daemons --session %s` from a shell that can see it",
		session, found, delta, session)
}

// liveSessionFor is the session a rollout must account for: the one it was
// scoped to, else the caller's own.
func liveSessionFor(opts UpgradeOptions) string {
	if opts.Session != "" {
		return opts.Session
	}
	return os.Getenv("BUS_SESSION")
}

// failRollout records a session's daemon left on an older build: a
// daemon-upgrade-failed row, and the result's error, which the CLI prints and
// exits 1 on — the road by which build.sh's warning reaches the build output.
func failRollout(res UpgradeResult, reason string) UpgradeResult {
	LogLifecycle(res.Session, "warn", "upgrade", "daemon-upgrade-failed", reason)
	res.Err = errors.New(reason)
	return res
}

// listLockHolderProcs walks BusDirBase() for each session's daemon.owner and
// monitor.owner and names the process the kernel reports as holding each
// (LockHolderPid). Nothing is read from the lock file, so neither a holder that
// has not yet written its pid nor a crashed one's leftover pid is ever named.
func listLockHolderProcs() []DaemonProc {
	base, err := busDirBase()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var procs []DaemonProc
	for _, e := range entries {
		session, ok := strings.CutPrefix(e.Name(), "muxcode-bus-")
		if !ok || !e.IsDir() || session == "" {
			continue
		}
		lockDir := filepath.Join(base, e.Name(), "lock")
		if pid := LockHolderPid(filepath.Join(lockDir, "daemon.owner")); pid > 0 {
			procs = append(procs, DaemonProc{PID: pid, Session: session})
		}
		if pid := LockHolderPid(filepath.Join(lockDir, "monitor.owner")); pid > 0 {
			procs = append(procs, DaemonProc{PID: pid, Session: session, Monitor: true})
		}
	}
	return procs
}

// parseDaemonProcs extracts daemon/monitor processes from ps output lines of
// the form "<pid> <binary> watch [--monitor] <session> [--poll N]...".
// Only commands whose binary basename is exactly "muxcode" are matched, so
// grep/editor processes mentioning "muxcode watch" in arguments are ignored.
func parseDaemonProcs(psOutput string) []DaemonProc {
	var procs []DaemonProc
	for _, line := range strings.Split(psOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		if filepath.Base(fields[1]) != "muxcode" || fields[2] != "watch" {
			continue
		}
		session := ""
		monitor := false
		args := fields[3:]
		for i := 0; i < len(args); i++ {
			switch a := args[i]; {
			case a == "--monitor":
				monitor = true
			case a == "--poll" || a == "--debounce":
				i++ // skip flag value
			case strings.HasPrefix(a, "-"):
				// unknown flag — ignore
			default:
				// Last positional wins: a future unknown value-taking flag
				// (e.g. "--some-flag 123 my-session") would otherwise have
				// its value mistaken for the session name.
				session = a
			}
		}
		if session == "" {
			continue
		}
		procs = append(procs, DaemonProc{PID: pid, Session: session, Monitor: monitor})
	}
	return procs
}

// FilterDaemonProcs keeps the processes that belong to session, or all of
// them when session is empty. It is what scopes a rollout to one session:
// the version integration test cycles a scratch daemon from a scratch
// binary, and without the filter that run would restart every stale daemon
// on the machine, live sessions included.
func FilterDaemonProcs(procs []DaemonProc, session string) []DaemonProc {
	if session == "" {
		return procs
	}
	var kept []DaemonProc
	for _, p := range procs {
		if p.Session == session {
			kept = append(kept, p)
		}
	}
	return kept
}

// PlanUpgrades groups discovered processes by session, marks sessions whose
// tmux session no longer exists as orphans (their daemons are killed without
// relaunch), and marks sessions whose daemon already runs the installed
// build as Current. A daemon that recorded no identity — one launched from a
// binary older than the stamp — is never current. sessionExists and
// daemonBuild are injectable for tests; pass TmuxHasSession and
// ReadDaemonVersion.
func PlanUpgrades(procs []DaemonProc, sessionExists func(string) bool, daemonBuild func(string) (Info, bool), installed Info) []UpgradePlan {
	bySession := map[string]*UpgradePlan{}
	for _, p := range procs {
		plan, ok := bySession[p.Session]
		if !ok {
			plan = &UpgradePlan{Session: p.Session, Installed: installed}
			bySession[p.Session] = plan
		}
		if p.Monitor {
			plan.MonitorPID = p.PID
		} else {
			plan.DaemonPID = p.PID
		}
	}
	var plans []UpgradePlan
	for _, plan := range bySession {
		plan.Orphan = !sessionExists(plan.Session)
		if build, ok := daemonBuild(plan.Session); ok {
			plan.DaemonBuild = build
			plan.Current = !plan.Orphan && build.SameBuild(installed)
		}
		plans = append(plans, *plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Session < plans[j].Session })
	return plans
}

// UpgradeDaemons cycles every running daemon/monitor that is not already on
// this binary's build, so they re-exec the freshly installed binary. Run
// after `make install` (build.sh calls this) — long-lived daemons otherwise
// keep executing the code loaded at their launch and never pick up fixes.
// When ps runs, each session's processes are killed and relaunched from here
// (relaunchFromHere, orphans killed without relaunch); when it cannot — a
// Codex build sandbox — they are asked to re-exec themselves and nothing is
// killed (restartByRequest). Either way a restart counts only once the
// replacements prove the installed build (awaitRestarted). A daemon whose
// recorded build matches this binary (see Info.SameBuild) is skipped unless
// Force is set. Session, when set, restricts all of this to that one session.
// A live daemon in the rollout's own session (Session, else BUS_SESSION) left
// on an older build that no plan accounts for, or a restart that cannot be
// proved, is a result carrying Err and a daemon-upgrade-failed row.
func UpgradeDaemons(opts UpgradeOptions) ([]UpgradeResult, error) {
	procs, psErr := discoverDaemonProcs()
	installed := BuildInfo()
	sessionExists, cycle := TmuxHasSession, relaunchFromHere
	if psErr != nil {
		sessionExists, cycle = func(string) bool { return true }, restartByRequest
	}
	plans := PlanUpgrades(FilterDaemonProcs(procs, opts.Session), sessionExists, ReadDaemonVersion, installed)
	var results []UpgradeResult
	for _, plan := range plans {
		res := UpgradeResult{UpgradePlan: plan}
		switch {
		case plan.Current && !opts.Force:
			res.Skipped = true
			if !opts.DryRun {
				LogLifecycle(plan.Session, "info", "upgrade", "daemon-current",
					fmt.Sprintf("Daemon for %s already on %s — not restarted", plan.Session, plan.Installed.Version))
			}
		case !opts.DryRun:
			res = cycle(plan)
		}
		results = append(results, res)
	}
	if opts.DryRun {
		return results, nil
	}
	if session := liveSessionFor(opts); session != "" {
		build, built := ReadDaemonVersion(session)
		if reason := unrolledReason(session, plans, psErr, IsDaemonAlive(session, 30), build, built, installed); reason != "" {
			res := UpgradeResult{UpgradePlan: UpgradePlan{Session: session, DaemonBuild: build, Installed: installed}}
			results = append(results, failRollout(res, reason))
		}
	}
	return results, nil
}

// rolloutVerifyTimeout bounds how long a rollout waits for a restarted daemon
// and monitor to prove they run the installed build.
var rolloutVerifyTimeout = 20 * time.Second

// relaunchFromHere is the ps road: kill the monitor (first, so it cannot
// resurrect the old daemon mid-cycle) and the daemon, then start both from the
// binary on PATH in the session's own directory, their output appended to the
// bus directory's upgrade-relaunch.log. An orphan (tmux session gone) is
// killed without relaunch. Success is the replacements' proof, not a started
// process: on 2026-11-14 a relaunch that started fine died at once and the
// rollout still reported "restarted".
func relaunchFromHere(plan UpgradePlan) UpgradeResult {
	res := UpgradeResult{UpgradePlan: plan}
	if plan.MonitorPID != 0 {
		killProcess(plan.MonitorPID)
	}
	if plan.DaemonPID != 0 {
		killProcess(plan.DaemonPID)
	}
	if plan.Orphan {
		LogLifecycle(plan.Session, "info", "upgrade", "daemon-orphan-killed",
			fmt.Sprintf("Killed orphan daemon for %s (tmux session gone)", plan.Session))
		return res
	}

	sessionDir := SessionRepoDir(plan.Session)
	logPath := filepath.Join(BusDir(plan.Session), "upgrade-relaunch.log")
	since := time.Now()
	var daemonPid, monitorPid int
	var err error
	if plan.DaemonPID != 0 {
		if daemonPid, err = startDetachedProcessLogged(sessionDir, logPath, "muxcode", "watch", plan.Session); err != nil {
			return failRollout(res, fmt.Sprintf("relaunch daemon: %v", err))
		}
	}
	if plan.MonitorPID != 0 {
		if monitorPid, err = startDetachedProcessLogged(sessionDir, logPath, "muxcode", "watch", "--monitor", plan.Session); err != nil {
			return failRollout(res, fmt.Sprintf("relaunch monitor: %v", err))
		}
	}
	if reason := awaitRestarted(plan, since, daemonPid, monitorPid); reason != "" {
		return failRollout(res, reason+logTail(logPath))
	}
	res.DaemonRestarted, res.MonitorRestarted = daemonPid != 0, monitorPid != 0
	LogLifecycle(plan.Session, "info", "upgrade", "daemon-upgraded",
		fmt.Sprintf("Restarted daemon for %s: %s", plan.Session, plan.VersionDelta()))
	return res
}

// restartByRequest is the road taken when ps cannot run — inside a Codex
// build sandbox, where a process started from here dies with the sandbox. It
// kills nothing and orphans nothing: it asks the daemon and monitor to re-exec
// the executable each was started from (RequestRestart), which an install has
// replaced, and waits for the proof that it now holds the installed build.
// Unproved — a process started from somewhere the install did not reach — the
// requests are withdrawn and the old processes keep running.
func restartByRequest(plan UpgradePlan) UpgradeResult {
	res := UpgradeResult{UpgradePlan: plan}
	var requests []string
	if plan.DaemonPID != 0 {
		requests = append(requests, DaemonRestartRequestPath(plan.Session))
	}
	if plan.MonitorPID != 0 {
		requests = append(requests, MonitorRestartRequestPath(plan.Session))
	}
	withdraw := func() {
		for _, p := range requests {
			_ = os.Remove(p)
		}
	}
	since := time.Now()
	for _, p := range requests {
		if err := RequestRestart(p); err != nil {
			withdraw()
			return failRollout(res, fmt.Sprintf("write restart request: %v", err))
		}
	}
	if reason := awaitRestarted(plan, since, plan.DaemonPID, plan.MonitorPID); reason != "" {
		withdraw()
		return failRollout(res, reason+" — restart request withdrawn, the old processes keep running (see daemon-reexec and monitor-reexec rows)")
	}
	res.DaemonRestarted, res.MonitorRestarted = plan.DaemonPID != 0, plan.MonitorPID != 0
	LogLifecycle(plan.Session, "info", "upgrade", "daemon-upgraded",
		fmt.Sprintf("Restarted daemon for %s by restart request: %s", plan.Session, plan.VersionDelta()))
	return res
}

// awaitRestarted waits rolloutVerifyTimeout for the restarted daemon and
// monitor to prove themselves: the kernel names wantDaemon and wantMonitor as
// the holders of their owner locks, and each has stamped the installed build
// since the rollout began. A zero pid is not waited for. It returns what is
// still unproved, or "".
func awaitRestarted(plan UpgradePlan, since time.Time, wantDaemon, wantMonitor int) string {
	daemonUp := func() bool {
		return wantDaemon == 0 || (LockHolderPid(DaemonOwnerPath(plan.Session)) == wantDaemon &&
			stampedBuildSince(DaemonVersionPath(plan.Session), plan.Installed, since))
	}
	monitorUp := func() bool {
		return wantMonitor == 0 || (LockHolderPid(MonitorOwnerPath(plan.Session)) == wantMonitor &&
			stampedBuildSince(MonitorVersionPath(plan.Session), plan.Installed, since))
	}
	for deadline := time.Now().Add(rolloutVerifyTimeout); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if daemonUp() && monitorUp() {
			return ""
		}
	}
	var missing []string
	if !daemonUp() {
		missing = append(missing, fmt.Sprintf("daemon (pid %d)", wantDaemon))
	}
	if !monitorUp() {
		missing = append(missing, fmt.Sprintf("monitor (pid %d)", wantMonitor))
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("restarted %s did not hold its lock on the installed build %s within %s",
		strings.Join(missing, " and "), plan.Installed.Version, rolloutVerifyTimeout)
}

// logTail is "; <path>: <last lines>" for a failure reason, or "".
func logTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	if len(lines) == 0 || lines[0] == "" {
		return ""
	}
	return fmt.Sprintf("; %s: %s", path, strings.Join(lines, " | "))
}

// upgradeDaemonsFn is the seam tests replace; production runs UpgradeDaemons.
var upgradeDaemonsFn = UpgradeDaemons

// AutoUpgradeDaemonsDisabled reports whether attaching to a running session
// should skip the daemon freshness check.
// MUXCODE_AUTO_UPGRADE_DAEMONS_DISABLE=1 opts out.
func AutoUpgradeDaemonsDisabled() bool {
	return os.Getenv("MUXCODE_AUTO_UPGRADE_DAEMONS_DISABLE") == "1"
}

// EnsureSessionDaemonCurrent rolls one session's daemon onto this binary's
// build when it is running an older one, and reports whether it restarted
// anything. LaunchSession already starts a daemon from the binary on PATH, so
// attaching is the only way a session's daemon drifts behind an install — and
// a session is launched once but attached many times, which is why staleness
// accumulates on that path and nowhere else.
//
// The upgrade is always scoped to session. An unscoped rollout cycles daemons
// for every session on the machine, including ones nobody asked to touch;
// that has happened, and this scope is what prevents it.
//
// Errors are returned for the caller to report, never to act on: ps is denied
// under some sandboxes, and a daemon check must never stand between the user
// and their session.
func EnsureSessionDaemonCurrent(session string) (UpgradeResult, bool, error) {
	if session == "" || AutoUpgradeDaemonsDisabled() {
		return UpgradeResult{}, false, nil
	}
	results, err := upgradeDaemonsFn(UpgradeOptions{Session: session})
	if err != nil {
		return UpgradeResult{}, false, err
	}
	for _, r := range results {
		if r.Err != nil {
			return r, false, r.Err
		}
		if r.DaemonRestarted || r.MonitorRestarted {
			return r, true, nil
		}
	}
	return UpgradeResult{}, false, nil
}

// DaemonBuildStatus is `session status`'s daemon line: whether the session's
// daemon runs the installed build, said every time without being asked, since
// a rollout that could not run leaves no other trace an agent reads (MUX-161).
func DaemonBuildStatus(session string) string {
	build, built := ReadDaemonVersion(session)
	return formatDaemonBuildStatus(session, IsDaemonAlive(session, 30), build, built, BuildInfo())
}

func formatDaemonBuildStatus(session string, alive bool, build Info, built bool, installed Info) string {
	switch {
	case !alive:
		return "Daemon: not running"
	case built && build.SameBuild(installed):
		return fmt.Sprintf("Daemon: %s (current)", build.Version)
	}
	plan := UpgradePlan{Installed: installed}
	if built {
		plan.DaemonBuild = build
	}
	return fmt.Sprintf("Daemon: STALE — %s; run `muxcode upgrade-daemons --session %s`", plan.VersionDelta(), session)
}

// killProcess sends SIGTERM and waits up to 2s for exit, escalating to
// SIGKILL if the process is still alive.
func killProcess(pid int) {
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if syscall.Kill(pid, 0) != nil {
			return // process gone
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
}
