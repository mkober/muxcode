package bus

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ownerHolderEnv makes the test binary a stand-in session process holding
// the owner lock "<session> <family> <role>" names — daemon.owner,
// monitor.owner, an OwnerLockDir record, or the proc lock
// ExecHoldingOwnerLock takes before it execs — `sleep 60` for proc-exec, and
// for proc-tree a shell whose child ignores TERM — until killed.
const ownerHolderEnv = "MUXCODE_TEST_OWNER_HOLDER"

func runOwnerHolder(spec string) {
	var session, family, role string
	if n, _ := fmt.Sscan(spec, &session, &family, &role); n != 3 {
		os.Exit(2)
	}
	switch family {
	case "daemon":
		if _, err := AcquirePidLock(DaemonOwnerPath(session)); err != nil {
			os.Exit(3)
		}
	case "monitor":
		if _, err := AcquirePidLock(MonitorOwnerPath(session)); err != nil {
			os.Exit(3)
		}
	case "proc-exec":
		_ = ExecHoldingOwnerLock(OwnerLockDir(session), role, []string{"sleep", "60"})
		os.Exit(4)
	case "proc-tree":
		_ = ExecHoldingOwnerLock(OwnerLockDir(session), role, []string{"sh", "-c", "(trap '' TERM; sleep 60) & wait"})
		os.Exit(4)
	default:
		HoldOwnerLock(session, family, role)
	}
	time.Sleep(60 * time.Second)
}

// reaperBus gives a test its own bus base, visible to holder children through
// MUXCODE_BUS_BASE, a tmux server on socket listing live, and a ps that lists
// census (nil = ps refused). Signals are real unless a test replaces
// reaperSignal; the grace is short.
type reaperBus struct {
	base   string
	socket string
	live   []string
	ids    []string // session ids the server lists, for renamed sessions
	census []censusProc
}

func newReaperBus(t *testing.T) *reaperBus {
	t.Helper()
	b := &reaperBus{base: useTempBusDir(t), socket: "/private/scratch/tmux-501/default"}
	t.Setenv(BusDirBaseEnv, b.base)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	view, ps, info, sig, alive, group, grace := reaperTmuxView, reaperPS, reaperProcInfo, reaperSignal, reaperAlive, reaperGroupAlive, reaperGrace
	t.Cleanup(func() {
		reaperTmuxView, reaperPS, reaperProcInfo, reaperSignal, reaperAlive, reaperGroupAlive, reaperGrace = view, ps, info, sig, alive, group, grace
	})
	reaperGrace = 300 * time.Millisecond
	reaperTmuxView = func() (tmuxView, error) {
		v := tmuxView{socket: b.socket, names: map[string]bool{}, ids: map[string]bool{}}
		for _, n := range b.live {
			v.names[n] = true
		}
		for _, id := range b.ids {
			v.ids[id] = true
		}
		return v, nil
	}
	reaperPS = func() ([]censusProc, error) {
		if b.census == nil {
			return nil, errors.New("ps: operation not permitted")
		}
		return b.census, nil
	}
	reaperProcInfo = func(pid int) (censusProc, error) {
		for _, p := range b.census {
			if p.pid == pid {
				return p, nil
			}
		}
		return censusProc{}, fmt.Errorf("pid %d not listed", pid)
	}
	return b
}

// session makes a bus dir that recorded running on socket ("" = no record).
func (b *reaperBus) session(t *testing.T, name, socket string) string {
	t.Helper()
	if err := os.MkdirAll(BusDir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if socket != "" {
		if err := os.WriteFile(filepath.Join(BusDir(name), tmuxSocketFile), []byte(socket+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return name
}

// hold starts a holder child for session's family and waits until the kernel
// names it as the lock's holder. A proc holder leads its own process group,
// as StartProc's Setpgid makes it.
func (b *reaperBus) hold(t *testing.T, session, family, role string) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), ownerHolderEnv+"="+session+" "+family+" "+role)
	lockFamily := family
	if strings.HasPrefix(family, "proc") {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		lockFamily = "proc"
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	pid := cmd.Process.Pid
	if lockFamily == "proc" {
		t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	}
	path := filepath.Join(OwnerLockDir(session), ownerLockName(lockFamily, role, pid))
	switch family {
	case "daemon":
		path = DaemonOwnerPath(session)
	case "monitor":
		path = MonitorOwnerPath(session)
	}
	for deadline := time.Now().Add(10 * time.Second); LockHolderPid(path) != pid; {
		if time.Now().After(deadline) {
			t.Fatalf("holder %d never took %s", pid, path)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return pid
}

// fakeSignals replaces signals: each is recorded, answered with err, and
// kills nothing, so alive pids stay alive.
type fakeSignals struct {
	sent   []string
	err    error
	alive  map[int]bool
	onTerm func(pid int)
}

func (b *reaperBus) fakeSignals(t *testing.T, alive ...int) *fakeSignals {
	t.Helper()
	f := &fakeSignals{alive: map[int]bool{}}
	for _, p := range alive {
		f.alive[p] = true
	}
	reaperAlive = func(pid int) bool { return f.alive[pid] }
	reaperSignal = func(pid int, sig syscall.Signal) error {
		f.sent = append(f.sent, fmt.Sprintf("%d:%s", pid, sigName(sig)))
		if sig == syscall.SIGTERM && f.onTerm != nil {
			f.onTerm(pid)
		}
		return f.err
	}
	return f
}

func sigName(s syscall.Signal) string {
	if s == syscall.SIGKILL {
		return "KILL"
	}
	return "TERM"
}

// standardTmuxSocket is tmux's default server socket with no TMUX_TMPDIR and
// no -L.
func standardTmuxSocket() string {
	return filepath.Join("/tmp", fmt.Sprintf("tmux-%d", os.Getuid()), "default")
}

func names(orphans []Orphan) []string {
	var out []string
	for _, o := range orphans {
		out = append(out, fmt.Sprintf("%s/%s/%s", o.Session, o.Family, o.Proof))
	}
	return out
}

// TestReapOrphansTerminatesADeadSessionsRecordedProcesses is the registry
// road with real processes and real signals, ps refused (MUX-161's sandbox): a
// dead session's monitor, daemon, harness, listener and proc, each proved by
// the kernel holder of its owner lock, are killed monitor first, then daemon,
// then the rest — the proc by its process group — and each is reported reaped
// only once it is confirmed gone, with an orphan-reaped row naming session,
// family, pid and proof.
func TestReapOrphansTerminatesADeadSessionsRecordedProcesses(t *testing.T) {
	b := newReaperBus(t)
	dead := b.session(t, "reap-dead", b.socket)
	pids := map[string]int{}
	for _, f := range []string{"daemon", "harness", "monitor", "listener", "proc"} {
		pids[f] = b.hold(t, dead, f, "r1")
	}

	rep := ReapOrphans(ReapOptions{LogSession: "launching"})

	if got := names(rep.Reaped); len(got) != 5 || got[0] != "reap-dead/monitor/owner-lock" || got[1] != "reap-dead/daemon/owner-lock" {
		t.Fatalf("want monitor, daemon, then the rest reaped; reaped %v failed %v", got, names(rep.Failed))
	}
	for f, pid := range pids {
		if CheckProcAlive(pid) {
			t.Errorf("%s pid %d reported reaped but alive", f, pid)
		}
	}
	rows := eventRows(t, "launching", "orphan-reaped")
	if len(rows) != 5 || !strings.Contains(rows[0], fmt.Sprintf("session=reap-dead family=monitor pid=%d proof=owner-lock", pids["monitor"])) {
		t.Errorf("orphan-reaped rows: %q", rows)
	}
	if rows := eventRows(t, "launching", "orphan-census-unavailable"); len(rows) != 1 {
		t.Errorf("a refused ps must log orphan-census-unavailable once: %q", rows)
	}
}

// TestExecHoldingOwnerLockSurvivesExec is the proc's identity token: the pid
// StartProc records execs into the command still holding its owner lock, so
// the reaper proves it and kills it by group; a proc.jsonl entry with no lock
// is reported unproven, never matched by time or command.
func TestExecHoldingOwnerLockSurvivesExec(t *testing.T) {
	b := newReaperBus(t)
	dead := b.session(t, "reap-proc", b.socket)
	owned := b.hold(t, dead, "proc-exec", "1794-proc-ab")
	if out, _ := exec.Command("ps", "-o", "command=", "-p", fmt.Sprint(owned)).Output(); !strings.HasPrefix(strings.TrimSpace(string(out)), "sleep 60") {
		t.Fatalf("holder did not exec into the command: %q", out)
	}
	stranger := exec.Command("sleep", "30")
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stranger.Process.Kill(); _ = stranger.Wait() })
	line := fmt.Sprintf(`{"id":"legacy","pid":%d,"command":"sleep 30","status":"running","started_at":%d}`, stranger.Process.Pid, time.Now().Unix())
	if err := os.WriteFile(filepath.Join(BusDir(dead), "proc.jsonl"), []byte(line+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rep := ReapOrphans(ReapOptions{LogSession: "launching"})

	if got := names(rep.Reaped); len(got) != 1 || got[0] != "reap-proc/proc/owner-lock" || CheckProcAlive(owned) {
		t.Errorf("want the exec-held proc reaped and gone, got %v", got)
	}
	if len(rep.Unproven) != 1 || rep.Unproven[0].PID != stranger.Process.Pid || !strings.Contains(rep.Unproven[0].Reason, "no owner lock") {
		t.Errorf("a lockless proc record must be unproven, never matched: %+v", rep.Unproven)
	}
	if !CheckProcAlive(stranger.Process.Pid) {
		t.Error("the unproven proc was killed")
	}
}

// TestReapOrphansTerminatesTheWholeProcGroup: a proc is reaped only once its
// whole process group is gone. The leader — the owner-lock holder — exits on
// TERM while its child ignores TERM; the sweep must see the group still
// alive, KILL it on the group anchor, and only then report it reaped. A sweep
// that watched the leader alone would report this proc reaped with its child
// still running.
func TestReapOrphansTerminatesTheWholeProcGroup(t *testing.T) {
	b := newReaperBus(t)
	dead := b.session(t, "reap-tree", b.socket)
	pgid := b.hold(t, dead, "proc-tree", "1794-proc-tree")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		if out, _ := exec.Command("pgrep", "-g", fmt.Sprint(pgid)).Output(); len(strings.Fields(string(out))) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the proc tree never started its TERM-ignoring child")
		}
	}
	var sent []string
	reaperSignal = func(pid int, sig syscall.Signal) error {
		sent = append(sent, fmt.Sprintf("%d:%s", pid, sigName(sig)))
		return syscall.Kill(pid, sig)
	}

	rep := ReapOrphans(ReapOptions{})

	if got := names(rep.Reaped); len(got) != 1 || got[0] != "reap-tree/proc/owner-lock" {
		t.Fatalf("want the proc reaped, got reaped %v failed %+v", got, rep.Failed)
	}
	if reaperGroupAlive(pgid) {
		t.Error("the proc was reported reaped while a member of its group still runs")
	}
	if want := fmt.Sprintf("-%d:TERM -%d:KILL", pgid, pgid); strings.Join(sent, " ") != want {
		t.Errorf("signals %v, want %s — the surviving child needs the group KILL", sent, want)
	}
}

// TestReapOrphansReportsASurvivingGroup: a group that outlives KILL is a
// failure naming the group, never a reaped proc.
func TestReapOrphansReportsASurvivingGroup(t *testing.T) {
	b := newReaperBus(t)
	dead := b.session(t, "reap-stuck", b.socket)
	pid := b.hold(t, dead, "proc", "1794-proc-stuck")
	reaperSignal = func(int, syscall.Signal) error { return nil }
	reaperGroupAlive = func(int) bool { return true }

	rep := ReapOrphans(ReapOptions{LogSession: "launching"})

	if len(rep.Reaped) != 0 || len(rep.Failed) != 1 || !strings.Contains(rep.Failed[0].Reason, fmt.Sprintf("process group %d survived KILL", pid)) {
		t.Errorf("reaped %v, failed %+v", names(rep.Reaped), rep.Failed)
	}
	if rows := eventRows(t, "launching", "orphan-kill-failed"); len(rows) != 1 {
		t.Errorf("orphan-kill-failed rows: %q", rows)
	}
}

// TestPrepareSessionLaunch is the launch road: there is no name-matched kill
// beside the sweep, so a same-name daemon is replaced only when the sweep
// proves and reaps it. One recorded on another tmux server, one of unknown
// provenance, and a daemon whose monitor survived all stop the launch with
// ErrLaunchBlocked and a launch-blocked row, the processes left running; a
// proven same-server pair is reaped and the launch proceeds; under the opt-out
// any survivor blocks, and none lets the launch through.
func TestPrepareSessionLaunch(t *testing.T) {
	blocked := func(t *testing.T, err error, want string) {
		t.Helper()
		if !errors.Is(err, ErrLaunchBlocked) || !strings.Contains(err.Error(), want) {
			t.Errorf("want ErrLaunchBlocked naming %q, got %v", want, err)
		}
		if rows := eventRows(t, "relaunch", "launch-blocked"); len(rows) != 1 {
			t.Errorf("launch-blocked rows: %q", rows)
		}
	}
	t.Run("same name on another tmux server", func(t *testing.T) {
		b := newReaperBus(t)
		pid := b.hold(t, b.session(t, "relaunch", "/tmp/tmux-501/private"), "daemon", "-")
		blocked(t, PrepareSessionLaunch("relaunch"), "another tmux server")
		if !CheckProcAlive(pid) {
			t.Error("the other server's daemon was killed")
		}
	})
	t.Run("unknown provenance", func(t *testing.T) {
		b := newReaperBus(t)
		pid := b.hold(t, b.session(t, "relaunch", ""), "daemon", "-")
		blocked(t, PrepareSessionLaunch("relaunch"), "unrecorded")
		if !CheckProcAlive(pid) {
			t.Error("a daemon of unknown provenance was killed")
		}
	})
	t.Run("a monitor that survives", func(t *testing.T) {
		b := newReaperBus(t)
		b.session(t, "relaunch", b.socket)
		b.census = parseCensus("501 1 /usr/local/bin/muxcode watch relaunch\n502 1 /usr/local/bin/muxcode watch --monitor relaunch")
		sig := b.fakeSignals(t, 501, 502)
		blocked(t, PrepareSessionLaunch("relaunch"), "monitor pid 502")
		if strings.Join(sig.sent, " ") != "502:TERM 502:KILL" {
			t.Errorf("only the monitor may be signalled: %v", sig.sent)
		}
	})
	t.Run("a proven same-server pair is replaced", func(t *testing.T) {
		b := newReaperBus(t)
		s := b.session(t, "relaunch", b.socket)
		daemon, monitor := b.hold(t, s, "daemon", "-"), b.hold(t, s, "monitor", "-")
		if err := PrepareSessionLaunch("relaunch"); err != nil {
			t.Fatalf("a proven replacement was refused: %v", err)
		}
		if CheckProcAlive(daemon) || CheckProcAlive(monitor) {
			t.Error("the old daemon and monitor must be gone before the launch proceeds")
		}
	})
	t.Run("the opt-out", func(t *testing.T) {
		b := newReaperBus(t)
		t.Setenv("MUXCODE_ORPHAN_REAP", "0")
		s := b.session(t, "relaunch", b.socket)
		if err := PrepareSessionLaunch("relaunch"); err != nil {
			t.Fatalf("nothing running must not block: %v", err)
		}
		pid := b.hold(t, s, "daemon", "-")
		blocked(t, PrepareSessionLaunch("relaunch"), "MUXCODE_ORPHAN_REAP=0")
		if !CheckProcAlive(pid) {
			t.Error("the opt-out reaped the daemon")
		}
	})
}

// TestReapOrphansNeedsTheSessionsOwnServer is the provenance bar: a session
// is judged only on the server it recorded. The server listing it, a record
// naming another server, no record at all — a live private-server session
// seen from the default server is exactly this — an unreadable record, and a
// failed probe all leave its processes running; a record naming this server
// is judged.
func TestReapOrphansNeedsTheSessionsOwnServer(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, b *reaperBus) string
		killed bool
	}{
		{"listed by the server", func(t *testing.T, b *reaperBus) string {
			s := b.session(t, "reap-live", b.socket)
			b.live = []string{s}
			return s
		}, false},
		{"recorded on another server", func(t *testing.T, b *reaperBus) string {
			return b.session(t, "reap-elsewhere", "/tmp/tmux-501/private")
		}, false},
		{"no record (a live private-server session seen from the default)", func(t *testing.T, b *reaperBus) string {
			b.socket = standardTmuxSocket()
			return b.session(t, "reap-unrecorded", "")
		}, false},
		{"an unreadable record", func(t *testing.T, b *reaperBus) string {
			s := b.session(t, "reap-unreadable", "")
			if err := os.Mkdir(filepath.Join(BusDir(s), tmuxSocketFile), 0755); err != nil {
				t.Fatal(err)
			}
			return s
		}, false},
		{"renamed — its session id still listed under a new name", func(t *testing.T, b *reaperBus) string {
			s := b.session(t, "reap-renamed", b.socket)
			b.recordID(t, s, "$11")
			b.live, b.ids = []string{"renamed-to"}, []string{"$11"}
			return s
		}, false},
		{"recorded on the default server, seen from it", func(t *testing.T, b *reaperBus) string {
			b.socket = standardTmuxSocket()
			return b.session(t, "reap-default", standardTmuxSocket())
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := newReaperBus(t)
			s := c.setup(t, b)
			pid := b.hold(t, s, "daemon", "-")
			rep := ReapOrphans(ReapOptions{LogSession: "launching"})
			if gone := !CheckProcAlive(pid); gone != c.killed {
				t.Errorf("daemon killed=%v, want %v (reaped %v, unproven %v)", gone, c.killed, names(rep.Reaped), rep.Unproven)
			}
			if !c.killed && s != "reap-live" && s != "reap-renamed" && (len(rep.Unproven) != 1 || rep.Unproven[0].PID != pid) {
				t.Errorf("an unjudgeable session's process must be logged unproven: %+v", rep.Unproven)
			}
		})
	}
	t.Run("a failed probe", func(t *testing.T) {
		b := newReaperBus(t)
		pid := b.hold(t, b.session(t, "reap-unknown", b.socket), "daemon", "-")
		reaperTmuxView = func() (tmuxView, error) { return tmuxView{}, errors.New("error connecting (Operation not permitted)") }
		rep := ReapOrphans(ReapOptions{LogSession: "launching"})
		if !CheckProcAlive(pid) || rep.Skipped == "" {
			t.Errorf("an unanswered probe must reap nothing and say why: skipped %q", rep.Skipped)
		}
		if rows := eventRows(t, "launching", "orphan-sweep-skipped"); len(rows) != 1 {
			t.Errorf("want one orphan-sweep-skipped row, got %q", rows)
		}
	})
}

// TestReapOrphansRechecksBeforeEverySignal: the proof is asked again before
// TERM and before KILL. An argv that changed after the census, a session
// relaunched before the kill, and an argv that changed during the TERM grace
// each stop the kill — nothing is sent, or KILL is withheld.
func TestReapOrphansRechecksBeforeEverySignal(t *testing.T) {
	daemonLine := "301 1 /usr/local/bin/muxcode watch reap-recheck"
	setup := func(t *testing.T) (*reaperBus, *fakeSignals) {
		b := newReaperBus(t)
		b.session(t, "reap-recheck", b.socket)
		b.census = parseCensus(daemonLine)
		return b, b.fakeSignals(t, 301)
	}
	t.Run("argv changed after the census", func(t *testing.T) {
		_, sig := setup(t)
		reaperProcInfo = func(int) (censusProc, error) { return parseCensus("301 1 /usr/bin/vim notes.txt")[0], nil }
		rep := ReapOrphans(ReapOptions{})
		if len(sig.sent) != 0 || len(rep.Unproven) != 1 || !strings.Contains(rep.Unproven[0].Reason, "no longer names") {
			t.Errorf("sent %v, unproven %+v", sig.sent, rep.Unproven)
		}
	})
	t.Run("session relaunched before the kill", func(t *testing.T) {
		b, sig := setup(t)
		calls := 0
		reaperTmuxView = func() (tmuxView, error) {
			calls++
			v := tmuxView{socket: b.socket, names: map[string]bool{}}
			if calls > 1 {
				v.names["reap-recheck"] = true
			}
			return v, nil
		}
		rep := ReapOrphans(ReapOptions{})
		if len(sig.sent) != 0 || len(rep.Unproven) != 1 || !strings.Contains(rep.Unproven[0].Reason, "live") {
			t.Errorf("sent %v, unproven %+v", sig.sent, rep.Unproven)
		}
	})
	t.Run("argv changed during the TERM grace", func(t *testing.T) {
		_, sig := setup(t)
		sig.onTerm = func(int) {
			reaperProcInfo = func(int) (censusProc, error) { return parseCensus("301 1 /usr/bin/vim notes.txt")[0], nil }
		}
		rep := ReapOrphans(ReapOptions{})
		if strings.Join(sig.sent, " ") != "301:TERM" || len(rep.Failed) != 1 || !strings.Contains(rep.Failed[0].Reason, "KILL withheld") {
			t.Errorf("sent %v, failed %+v", sig.sent, rep.Failed)
		}
	})
}

// TestReapOrphansReportsOutcomes: reaped means confirmed gone. A monitor that
// survives TERM and KILL is a failure that holds back its daemon and the rest,
// none of them signalled; a refused signal is a failure; a dry run signals
// nothing, logs nothing and returns the plan in kill order.
func TestReapOrphansReportsOutcomes(t *testing.T) {
	lines := strings.Join([]string{
		"401 1 /usr/local/bin/muxcode watch reap-outcome",
		"402 1 /usr/local/bin/muxcode watch --monitor reap-outcome",
		"403 1 muxcode launch --resize reap-outcome",
	}, "\n")
	setup := func(t *testing.T) (*reaperBus, *fakeSignals) {
		b := newReaperBus(t)
		b.session(t, "reap-outcome", b.socket)
		b.census = parseCensus(lines)
		return b, b.fakeSignals(t, 401, 402, 403)
	}
	t.Run("a surviving monitor holds back the rest", func(t *testing.T) {
		_, sig := setup(t)
		rep := ReapOrphans(ReapOptions{LogSession: "launching"})
		if strings.Join(sig.sent, " ") != "402:TERM 402:KILL" {
			t.Errorf("only the monitor may be signalled: %v", sig.sent)
		}
		if len(rep.Reaped) != 0 || len(rep.Failed) != 3 || !strings.Contains(rep.Failed[0].Reason, "survived KILL") || !strings.Contains(rep.Failed[1].Reason, "held back") {
			t.Errorf("reaped %v, failed %+v", names(rep.Reaped), rep.Failed)
		}
		if rows := eventRows(t, "launching", "orphan-kill-failed"); len(rows) != 3 {
			t.Errorf("orphan-kill-failed rows: %q", rows)
		}
		if rows := eventRows(t, "launching", "orphan-reaped"); len(rows) != 0 {
			t.Errorf("a survivor was logged reaped: %q", rows)
		}
	})
	t.Run("a refused signal", func(t *testing.T) {
		_, sig := setup(t)
		sig.err = syscall.EPERM
		rep := ReapOrphans(ReapOptions{})
		if len(rep.Reaped) != 0 || len(rep.Failed) == 0 || !strings.Contains(rep.Failed[0].Reason, "TERM: operation not permitted") {
			t.Errorf("reaped %v, failed %+v", names(rep.Reaped), rep.Failed)
		}
	})
	t.Run("signals that work", func(t *testing.T) {
		_, sig := setup(t)
		sig.onTerm = func(pid int) { delete(sig.alive, pid) }
		rep := ReapOrphans(ReapOptions{})
		if got := names(rep.Reaped); strings.Join(got, " ") != "reap-outcome/monitor/argv reap-outcome/daemon/argv reap-outcome/launch-helper/argv" {
			t.Errorf("reaped %v", got)
		}
	})
	t.Run("gone before its turn", func(t *testing.T) {
		_, sig := setup(t)
		sig.onTerm = func(pid int) { delete(sig.alive, pid) }
		delete(sig.alive, 403)
		rep := ReapOrphans(ReapOptions{LogSession: "launching"})
		var helper *Orphan
		for i := range rep.Reaped {
			if rep.Reaped[i].PID == 403 {
				helper = &rep.Reaped[i]
			}
		}
		if helper == nil || helper.Reason != "exited before its own signal" || len(rep.Unproven) != 0 {
			t.Errorf("an exited candidate must read as gone, not unresolved: reaped %+v unproven %+v", rep.Reaped, rep.Unproven)
		}
		for _, s := range sig.sent {
			if strings.HasPrefix(s, "403:") {
				t.Errorf("an exited candidate was signalled: %v", sig.sent)
			}
		}
	})
	t.Run("a dry run", func(t *testing.T) {
		_, sig := setup(t)
		rep := ReapOrphans(ReapOptions{LogSession: "launching", DryRun: true})
		if len(sig.sent) != 0 || len(rep.Reaped) != 0 || len(names(rep.Planned)) != 3 || rep.Planned[0].Family != "monitor" {
			t.Errorf("sent %v, reaped %v, planned %v", sig.sent, names(rep.Reaped), names(rep.Planned))
		}
		if rows := eventRows(t, "launching", "orphan-reaped"); len(rows) != 0 {
			t.Errorf("a dry run logged reaped rows: %q", rows)
		}
	})
}

// TestReapOrphansCensus covers the argv families and the processes no record
// accounts for: a dead session's argv-only daemon, monitor and launch helper
// and its script-form legacy listener are reaped on argv; the loop form, a
// script path naming no bus dir here, and an orphaned harness holding no lock
// are left running and logged orphan-unproven; a scratch daemon with no bus
// dir here, a live session's script listener and a harness with a live parent
// are not reported at all.
func TestReapOrphansCensus(t *testing.T) {
	b := newReaperBus(t)
	dead := b.session(t, "reap-argv", b.socket)
	live := b.session(t, "reap-alive", b.socket)
	b.live = []string{live}
	script := func(s string) string { return filepath.Join(b.base, "muxcode-bus-"+s, "inbox-listener.sh") }
	b.census = parseCensus(strings.Join([]string{
		"101 1 /usr/local/bin/muxcode watch reap-argv",
		"102 1 /usr/local/bin/muxcode watch --monitor reap-argv",
		"103 1 muxcode launch --resize reap-argv",
		"104 1 bash " + script(dead),
		"105 1 bash -c while true; do muxcode inbox --poll; done",
		"106 1 bash /elsewhere/muxcode-bus-x/inbox-listener.sh",
		"107 1 /opt/bin/muxcode-llm-harness run prompt",
		"108 1 /usr/local/bin/muxcode watch scratch-test-session",
		"109 1 bash " + script(live),
		"110 1 /usr/local/bin/muxcode watch reap-alive",
		"111 4242 /opt/bin/muxcode-llm-harness run prompt",
	}, "\n"))
	sig := b.fakeSignals(t, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111)
	sig.onTerm = func(pid int) { delete(sig.alive, pid) }

	rep := ReapOrphans(ReapOptions{LogSession: "launching"})

	want := "reap-argv/monitor/argv reap-argv/daemon/argv reap-argv/launch-helper/argv reap-argv/legacy-listener/argv"
	if got := strings.Join(names(rep.Reaped), " "); got != want {
		t.Errorf("reaped %s, want %s", got, want)
	}
	unproven := map[int]string{}
	for _, o := range rep.Unproven {
		unproven[o.PID] = o.Reason
	}
	if len(unproven) != 3 || unproven[105] == "" || unproven[106] == "" || unproven[107] == "" {
		t.Errorf("want exactly 105, 106 and 107 unproven: %v", unproven)
	}
	if rows := eventRows(t, "launching", "orphan-unproven"); len(rows) != 3 {
		t.Errorf("orphan-unproven rows: %q", rows)
	}
}

// TestReapOrphansEscabs is MUX-184 Decision 3: a `tmux -L escabs-<pid>`
// server whose socket-name pid is dead is reaped with its own row; one whose
// pid is alive survives.
func TestReapOrphansEscabs(t *testing.T) {
	b := newReaperBus(t)
	gone := deadPID(t)
	b.census = parseCensus(fmt.Sprintf("201 1 tmux -L escabs-%d new-session -d python3 receiver.py\n202 1 tmux -L escabs-%d new-session -d python3 receiver.py", gone, os.Getpid()))
	sig := b.fakeSignals(t, 201, 202, os.Getpid())
	sig.onTerm = func(pid int) { delete(sig.alive, pid) }

	rep := ReapOrphans(ReapOptions{LogSession: "launching"})

	if got := names(rep.Reaped); len(got) != 1 || got[0] != "-/escabs-server/socket-pid-dead" || rep.Reaped[0].PID != 201 {
		t.Errorf("want only the dead-pid server reaped, got %v", got)
	}
	if strings.Join(sig.sent, " ") != "201:TERM" {
		t.Errorf("signals %v", sig.sent)
	}
	if rows := eventRows(t, "launching", "escabs-reaped"); len(rows) != 1 {
		t.Errorf("want one escabs-reaped row, got %q", rows)
	}
}

// TestSweepOrphansAtLaunchOptOut: MUXCODE_ORPHAN_REAP=0 reaps nothing beside
// a dead session; any other value sweeps.
func TestSweepOrphansAtLaunchOptOut(t *testing.T) {
	b := newReaperBus(t)
	pid := b.hold(t, b.session(t, "reap-optout", b.socket), "daemon", "-")
	t.Setenv("MUXCODE_ORPHAN_REAP", "0")
	if rep := SweepOrphansAtLaunch("launching"); !CheckProcAlive(pid) || rep.Skipped == "" {
		t.Errorf("the opt-out reaped %v", names(rep.Reaped))
	}
	t.Setenv("MUXCODE_ORPHAN_REAP", "1")
	if rep := SweepOrphansAtLaunch("launching"); CheckProcAlive(pid) {
		t.Errorf("with the sweep on the dead daemon must be reaped: %v", names(rep.Reaped))
	}
}

// recordID appends a tmux session id to a session's tmux.socket record, as
// RecordTmuxSocket writes it.
func (b *reaperBus) recordID(t *testing.T, session, id string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(BusDir(session), tmuxSocketFile), []byte(b.socket+"\n"+id+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestSessionPositivelyGone is the daemon's own exit bar: gone only when
// absent from the server it recorded, by name and by session id; listed,
// renamed (its id still listed under another name), unrecorded, recorded on
// another server, or an unanswered probe all read as not gone.
func TestSessionPositivelyGone(t *testing.T) {
	b := newReaperBus(t)
	dead := b.session(t, "gone-dead", b.socket)
	live := b.session(t, "gone-live", b.socket)
	unrecorded := b.session(t, "gone-unrecorded", "")
	elsewhere := b.session(t, "gone-elsewhere", "/tmp/tmux-501/private")
	renamed := b.session(t, "gone-renamed", b.socket)
	closed := b.session(t, "gone-closed-with-id", b.socket)
	b.recordID(t, renamed, "$7")
	b.recordID(t, closed, "$8")
	b.live = []string{live, "a-new-name"}
	b.ids = []string{"$7"}
	for session, want := range map[string]bool{dead: true, live: false, unrecorded: false, elsewhere: false, renamed: false, closed: true} {
		if got := SessionPositivelyGone(session); got != want {
			t.Errorf("%s: gone=%v, want %v", session, got, want)
		}
	}
	reaperTmuxView = func() (tmuxView, error) { return tmuxView{}, errors.New("Operation not permitted") }
	if SessionPositivelyGone(dead) {
		t.Error("an unanswered probe read as gone")
	}
}

// TestOwnerLockName pins the cross-module file name the harness writes.
func TestOwnerLockName(t *testing.T) {
	name := ownerLockName(OwnerFamilyHarness, "prompt", 4242)
	if name != "harness.prompt.4242.owner" {
		t.Fatalf("name %q", name)
	}
	if f, r, p, ok := parseOwnerLockName(name); !ok || f != "harness" || r != "prompt" || p != 4242 {
		t.Errorf("parse %q: %s %s %d %v", name, f, r, p, ok)
	}
	for _, bad := range []string{"harness.prompt.owner", "harness.prompt.x.owner", "daemon.owner", "a.b.0.owner"} {
		if _, _, _, ok := parseOwnerLockName(bad); ok {
			t.Errorf("%q parsed as a record", bad)
		}
	}
}

// TestRecordTmuxSocket: the daemon records the server and session id holding
// its session, and a session tmux cannot find keeps what was recorded.
func TestRecordTmuxSocket(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	saved := tmuxSocketOf
	t.Cleanup(func() { tmuxSocketOf = saved })
	tmuxSocketOf = func(string) (string, string, error) { return "/tmp/tmux-501/default", "$3", nil }
	if !RecordTmuxSocket(session) {
		t.Fatal("record failed")
	}
	tmuxSocketOf = func(string) (string, string, error) { return "", "", errors.New("can't find session") }
	if RecordTmuxSocket(session) {
		t.Error("a missing session reported a record")
	}
	if socket, id, err := readTmuxRecord(BusDir(session)); err != nil || socket != "/tmp/tmux-501/default" || id != "$3" {
		t.Errorf("recorded %q %q %v", socket, id, err)
	}
}

// TestNoServerOnIsPositiveAbsence: tmux's reply when nothing listens on the
// socket is an empty server, but a socket the caller may not reach is not.
func TestNoServerOnIsPositiveAbsence(t *testing.T) {
	for msg, want := range map[string]bool{
		"no server running on /private/tmp/tmux-501/default":                    true,
		"error connecting to /tmp/tmux-501/default (No such file or directory)": true,
		"error connecting to /tmp/tmux-501/default (Operation not permitted)":   false,
		"error connecting to /tmp/tmux-501/default (Permission denied)":         false,
	} {
		if got := noServerOn.MatchString(msg); got != want {
			t.Errorf("%q: absence %v, want %v", msg, got, want)
		}
	}
}
