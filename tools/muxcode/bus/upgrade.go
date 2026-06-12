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
	Session    string
	DaemonPID  int  // 0 if no daemon process found
	MonitorPID int  // 0 if no monitor process found
	Orphan     bool // tmux session gone — kill without relaunch
}

// UpgradeResult records the outcome of cycling one session's daemon.
type UpgradeResult struct {
	Session          string
	DaemonRestarted  bool
	MonitorRestarted bool
	Orphan           bool
	Err              error
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

// PlanUpgrades groups discovered processes by session and marks sessions whose
// tmux session no longer exists as orphans (their daemons are killed without
// relaunch). sessionExists is injectable for tests; pass TmuxHasSession.
func PlanUpgrades(procs []DaemonProc, sessionExists func(string) bool) []UpgradePlan {
	bySession := map[string]*UpgradePlan{}
	for _, p := range procs {
		plan, ok := bySession[p.Session]
		if !ok {
			plan = &UpgradePlan{Session: p.Session}
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
		plans = append(plans, *plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Session < plans[j].Session })
	return plans
}

// UpgradeDaemons cycles every running daemon/monitor so they re-exec the
// freshly installed binary. Run after `make install` (build.sh calls this) —
// long-lived daemons otherwise keep executing the code loaded at their launch
// and never pick up fixes. Per session: the monitor is killed first (so it
// cannot resurrect the old daemon mid-cycle), then the daemon, then both are
// relaunched from the binary currently on PATH. Orphan daemons (tmux session
// gone) are killed without relaunch.
func UpgradeDaemons(dryRun bool) ([]UpgradeResult, error) {
	procs, err := ListDaemonProcs()
	if err != nil {
		return nil, err
	}
	plans := PlanUpgrades(procs, TmuxHasSession)

	var results []UpgradeResult
	for _, plan := range plans {
		res := UpgradeResult{Session: plan.Session, Orphan: plan.Orphan}
		if dryRun {
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

		if plan.DaemonPID != 0 {
			if _, err := startDetachedProcess("muxcode", "watch", plan.Session); err != nil {
				res.Err = fmt.Errorf("relaunch daemon: %w", err)
				results = append(results, res)
				continue
			}
			res.DaemonRestarted = true
		}
		if plan.MonitorPID != 0 {
			if _, err := startDetachedProcess("muxcode", "watch", "--monitor", plan.Session); err != nil {
				res.Err = fmt.Errorf("relaunch monitor: %w", err)
				results = append(results, res)
				continue
			}
			res.MonitorRestarted = true
		}
		LogLifecycle(plan.Session, "info", "upgrade", "daemon-upgraded",
			fmt.Sprintf("Restarted daemon for %s on new binary", plan.Session))
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
