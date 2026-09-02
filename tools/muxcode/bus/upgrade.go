package bus

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DaemonProc describes a running daemon or monitor process discovered via ps.
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

// ListDaemonProcs discovers all running "muxcode watch" daemon and monitor
// processes across every session on this machine.
func ListDaemonProcs() ([]DaemonProc, error) {
	out, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseDaemonProcs(string(out)), nil
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
// Per session: the monitor is killed first (so it cannot resurrect the old
// daemon mid-cycle), then the daemon, then both are relaunched from the
// binary currently on PATH. A daemon whose recorded build matches this
// binary (see Info.SameBuild) is skipped unless Force is set; orphan daemons
// (tmux session gone) are killed without relaunch regardless. Session, when
// set, restricts all of this to that one session.
func UpgradeDaemons(opts UpgradeOptions) ([]UpgradeResult, error) {
	procs, err := ListDaemonProcs()
	if err != nil {
		return nil, err
	}
	plans := PlanUpgrades(FilterDaemonProcs(procs, opts.Session), TmuxHasSession, ReadDaemonVersion, BuildInfo())

	var results []UpgradeResult
	for _, plan := range plans {
		res := UpgradeResult{UpgradePlan: plan}
		if plan.Current && !opts.Force {
			res.Skipped = true
			if !opts.DryRun {
				LogLifecycle(plan.Session, "info", "upgrade", "daemon-current",
					fmt.Sprintf("Daemon for %s already on %s — not restarted", plan.Session, plan.Installed.Version))
			}
			results = append(results, res)
			continue
		}
		if opts.DryRun {
			results = append(results, res)
			continue
		}

		// Monitor first — a live monitor would relaunch the daemon we just
		// killed before we get to start the new one.
		if plan.MonitorPID != 0 {
			killProcess(plan.MonitorPID)
		}
		if plan.DaemonPID != 0 {
			killProcess(plan.DaemonPID)
		}

		if plan.Orphan {
			LogLifecycle(plan.Session, "info", "upgrade", "daemon-orphan-killed",
				fmt.Sprintf("Killed orphan daemon for %s (tmux session gone)", plan.Session))
			results = append(results, res)
			continue
		}

		// Relaunch in the session's own directory, not the repo being built.
		sessionDir := SessionRepoDir(plan.Session)

		if plan.DaemonPID != 0 {
			if _, err := startDetachedProcessIn(sessionDir, "muxcode", "watch", plan.Session); err != nil {
				res.Err = fmt.Errorf("relaunch daemon: %w", err)
				results = append(results, res)
				continue
			}
			res.DaemonRestarted = true
		}
		if plan.MonitorPID != 0 {
			if _, err := startDetachedProcessIn(sessionDir, "muxcode", "watch", "--monitor", plan.Session); err != nil {
				res.Err = fmt.Errorf("relaunch monitor: %w", err)
				results = append(results, res)
				continue
			}
			res.MonitorRestarted = true
		}
		LogLifecycle(plan.Session, "info", "upgrade", "daemon-upgraded",
			fmt.Sprintf("Restarted daemon for %s: %s", plan.Session, plan.VersionDelta()))
		results = append(results, res)
	}
	return results, nil
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
