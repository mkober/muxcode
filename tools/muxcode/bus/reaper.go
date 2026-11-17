package bus

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Orphan is one process a sweep proved, or failed to prove, a dead session
// owns. Session is "-" for a process that names none.
type Orphan struct {
	Session string
	Family  string // monitor, daemon, harness, listener, webhook, proc, launch-helper, legacy-listener, escabs-server
	PID     int
	Proof   string // owner-lock, argv, socket-pid-dead; empty when unproven
	Reason  string // why it is unproven, failed or held back
	group   bool   // signal the process group (a proc, started with Setpgid)
	prove   func() (bool, string)
	absent  func() (bool, string) // the session is still absent where it ran
}

// ReapOptions scope one sweep. LogSession receives every row; empty logs each
// row under the dead session it names (session-less rows are then dropped).
// DryRun proves and plans but sends no signal and logs no row. Sessions, when
// non-nil, limits the sweep to those sessions' processes — no escabs servers
// or session-less orphans — which is how cleanup reaps only what it removes.
type ReapOptions struct {
	LogSession string
	DryRun     bool
	Sessions   []string
}

// inScope reports whether o falls within the sweep's Sessions.
func (opts ReapOptions) inScope(o Orphan) bool {
	if opts.Sessions == nil {
		return true
	}
	for _, s := range opts.Sessions {
		if o.Session == s {
			return true
		}
	}
	return false
}

// ReapReport is what a sweep found and did. Reaped holds only processes
// confirmed gone after a signal; Failed those that survived, could not be
// signalled, or were held back because a process they depend on survived.
type ReapReport struct {
	Reaped   []Orphan
	Failed   []Orphan
	Unproven []Orphan
	Planned  []Orphan // DryRun: the proven candidates, in kill order
	Skipped  string   // non-empty when nothing could be judged, and why
}

// ErrOrphanLivenessUnknown is returned when the tmux server could not be
// asked which sessions exist, so no session could be judged dead.
var ErrOrphanLivenessUnknown = errors.New("tmux session liveness unknown")

// OrphanReapDisabled reports MUXCODE_ORPHAN_REAP=0, the launch sweep's opt-out
// (MUX-184 Decision 2).
func OrphanReapDisabled() bool {
	return os.Getenv("MUXCODE_ORPHAN_REAP") == "0"
}

// SweepOrphansAtLaunch runs the launch-time sweep for a session about to be
// launched (MUX-184): every dead session's processes on this machine, the
// name being launched included, since its tmux session does not exist yet. A
// no-op under MUXCODE_ORPHAN_REAP=0.
func SweepOrphansAtLaunch(session string) ReapReport {
	if OrphanReapDisabled() {
		return ReapReport{Skipped: "MUXCODE_ORPHAN_REAP=0"}
	}
	return ReapOrphans(ReapOptions{LogSession: session})
}

// ErrLaunchBlocked is returned by PrepareSessionLaunch when the name being
// launched still has a daemon or monitor the sweep could not prove dead and
// reap.
var ErrLaunchBlocked = errors.New("launch stopped: the session's previous daemon or monitor is still running")

// PrepareSessionLaunch clears the way for launching session: it runs the
// launch sweep, then refuses the launch while any daemon or monitor of that
// name survives it — an owner-lock holder, or a `muxcode watch` naming it.
// There is no other road to killing them: the old name-matched pkill killed a
// same-name daemon on another tmux server, one of unknown provenance, and a
// daemon whose monitor had survived, all of which the sweep refuses. The
// returned error names each survivor and why the sweep left it, and a
// `launch-blocked` row records it. Under MUXCODE_ORPHAN_REAP=0 nothing is
// reaped, so any survivor blocks.
func PrepareSessionLaunch(session string) error {
	rep := SweepOrphansAtLaunch(session)
	blockers := sessionLaunchBlockers(session)
	if len(blockers) == 0 {
		return nil
	}
	detail := fmt.Sprintf("%s: %s (%s)", session, strings.Join(blockers, ", "), launchBlockReason(session, rep))
	LogLifecycle(session, "error", "launcher", "launch-blocked", detail)
	return fmt.Errorf("%w — %s; stop them, or launch under another name", ErrLaunchBlocked, detail)
}

// sessionLaunchBlockers names the live daemon and monitor processes of
// session, which would hold the instance locks a new launch needs.
func sessionLaunchBlockers(session string) []string {
	return sessionProcesses(BusDir(session), session, false, nil)
}

// sessionProcesses names the live processes a session's bus dir and the
// process table attribute to it: owner-lock holders and, with ps, any argv
// naming it. all widens it from the daemon and monitor to every family. A pid
// in skip is left out — a dry run's planned kills.
func sessionProcesses(dir, session string, all bool, skip map[int]bool) []string {
	seen := map[int]bool{}
	var out []string
	note := func(family string, pid int) {
		if pid > 0 && !seen[pid] && !skip[pid] {
			seen[pid] = true
			out = append(out, fmt.Sprintf("%s pid %d", family, pid))
		}
	}
	lockDir := filepath.Join(dir, "lock")
	note("monitor", LockHolderPid(filepath.Join(lockDir, "monitor.owner")))
	note("daemon", LockHolderPid(filepath.Join(lockDir, "daemon.owner")))
	if all {
		entries, _ := os.ReadDir(filepath.Join(lockDir, "owners"))
		for _, e := range entries {
			if family, _, _, ok := parseOwnerLockName(e.Name()); ok {
				note(family, LockHolderPid(filepath.Join(lockDir, "owners", e.Name())))
			}
		}
	}
	census, err := reaperPS()
	if err != nil {
		return out
	}
	for _, p := range census {
		s, family, ok := argvClaim(p)
		if ok && s == session && (all || family == "daemon" || family == "monitor") && reaperAlive(p.pid) {
			note(family, p.pid)
		}
	}
	return out
}

// StopSessionHarness stops the headless harness processes session's owner
// locks prove it owns — TERM, up to grace, then KILL — and names each it
// stopped. A harness holding no lock is never signalled: the pid in
// harness-<role>.pid could name anything by now. The daemon calls it on
// TERM/INT (MUX-184 Decision 4).
func StopSessionHarness(session string, grace time.Duration) []string {
	var stopped []string
	dir := OwnerLockDir(session)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		family, role, pid, ok := parseOwnerLockName(e.Name())
		path := filepath.Join(dir, e.Name())
		if !ok || family != OwnerFamilyHarness || LockHolderPid(path) != pid {
			continue
		}
		o := Orphan{Session: session, Family: family, PID: pid, Proof: "owner-lock"}
		o.prove = func() (bool, string) {
			if LockHolderPid(path) != pid {
				return false, e.Name() + " is no longer held by it"
			}
			return true, ""
		}
		if outcome, why := terminate(o, grace); outcome == killReaped {
			stopped = append(stopped, fmt.Sprintf("%s pid %d", role, pid))
		} else {
			LogLifecycle(session, "warn", "daemon", "harness-stop-failed", fmt.Sprintf("%s pid %d: %s", role, pid, why))
		}
	}
	return stopped
}

// launchBlockReason says why the sweep left session's processes running.
func launchBlockReason(session string, rep ReapReport) string {
	if rep.Skipped != "" {
		return "sweep skipped: " + rep.Skipped
	}
	for _, o := range append(append([]Orphan{}, rep.Failed...), rep.Unproven...) {
		if o.Session == session && o.Reason != "" {
			return o.Reason
		}
	}
	return "not proven to belong to a dead session"
}

// ReapOrphans kills every process it can prove belongs to a dead muxcode
// session.
//
// Two proofs exist and nothing else counts (MUX-184 Decision 1):
//   - the kernel holder of an owner lock under the session's bus dir —
//     daemon.owner, monitor.owner, or an OwnerLockDir record held by a
//     harness, listener, webhook or proc (ExecHoldingOwnerLock) — which can
//     never name a reused pid and needs no `ps`;
//   - the session in argv: the daemon, monitor and launch helpers of a
//     session with a bus dir here, and the legacy `inbox-listener.sh` whose
//     path names its bus dir.
//
// Never an environment probe, and never a proc.jsonl entry alone: its
// started_at is wall time, not identity. Anything without a proof is logged
// `orphan-unproven` and left running.
//
// A session is dead only on positive absence from the tmux server it
// recorded running on (RecordTmuxSocket). A session that recorded none, or
// whose record cannot be read, cannot be judged from any server: its
// processes are unproven, never killed.
//
// The proof — identity and the session's absence, freshly asked — is run
// again before every signal, TERM and KILL alike, because killing earlier
// targets takes seconds in which a pid can be reused or a session relaunched.
// A proof that no longer holds stops the kill. Within a session the monitor
// goes first, then the daemon, then the rest; a monitor that survives holds
// back its daemon, which it would relaunch, and a surviving daemon holds back
// the rest, which it would restart.
//
// Without `ps` the argv census is refused, logged `orphan-census-unavailable`;
// the owner-lock road still runs (MUX-161).
func ReapOrphans(opts ReapOptions) ReapReport {
	var rep ReapReport
	view, err := reaperTmuxView()
	if err != nil {
		rep.Skipped = fmt.Sprintf("%v: %v", ErrOrphanLivenessUnknown, err)
		opts.log("-", "info", "orphan-sweep-skipped", rep.Skipped)
		return rep
	}
	census, psErr := reaperPS()
	if psErr != nil {
		opts.log("-", "warn", "orphan-census-unavailable",
			fmt.Sprintf("ps: %v — argv census refused; owner-lock proofs only", psErr))
	}

	dirs := scanBusDirs()
	dead := map[string]bool{}
	unknown := map[string]string{}
	found := map[int]bool{os.Getpid(): true, os.Getppid(): true}
	var proven []Orphan
	add := func(o Orphan) {
		if found[o.PID] {
			return
		}
		found[o.PID] = true
		if !opts.inScope(o) {
			return
		}
		if o.Proof == "" {
			rep.Unproven = append(rep.Unproven, o)
			return
		}
		proven = append(proven, o)
	}
	for session, dir := range dirs {
		switch verdict, why := sessionVerdict(session, dir, view); verdict {
		case sessionDead:
			dead[session] = true
			for _, o := range sessionRecords(session, dir, dirs) {
				add(o)
			}
		case sessionUnknown:
			unknown[session] = why
			for _, o := range sessionRecords(session, dir, dirs) {
				o.Proof, o.Reason, o.prove = "", why, nil
				add(o)
			}
		}
	}
	for _, pid := range ownerLockHolders(dirs) {
		found[pid] = true
	}
	if psErr == nil {
		for _, o := range censusOrphans(census, dirs, dead, unknown, found) {
			add(o)
		}
	}

	if opts.DryRun {
		rep.Planned = killOrder(proven)
		return rep
	}
	held := map[string]string{}
	for _, o := range killOrder(proven) {
		if why, ok := held[o.Session]; ok {
			o.Reason = why
			rep.Failed = append(rep.Failed, o)
			continue
		}
		outcome, why := terminate(o, reaperGrace)
		o.Reason = why
		switch outcome {
		case killReaped:
			rep.Reaped = append(rep.Reaped, o)
			continue
		case killRefused:
			o.Proof = ""
			rep.Unproven = append(rep.Unproven, o)
		default:
			rep.Failed = append(rep.Failed, o)
		}
		if o.Session != "-" && (o.Family == "monitor" || o.Family == "daemon") {
			held[o.Session] = fmt.Sprintf("held back: the session's %s (pid %d) was not reaped", o.Family, o.PID)
		}
	}
	for _, o := range rep.Reaped {
		event := "orphan-reaped"
		if o.Family == "escabs-server" {
			event = "escabs-reaped"
		}
		detail := fmt.Sprintf("session=%s family=%s pid=%d proof=%s", o.Session, o.Family, o.PID, o.Proof)
		if o.Reason != "" {
			detail += " note=" + o.Reason
		}
		opts.log(o.Session, "info", event, detail)
	}
	for _, o := range rep.Failed {
		opts.log(o.Session, "warn", "orphan-kill-failed", fmt.Sprintf("session=%s family=%s pid=%d proof=%s reason=%s", o.Session, o.Family, o.PID, o.Proof, o.Reason))
	}
	for _, o := range rep.Unproven {
		opts.log(o.Session, "warn", "orphan-unproven", fmt.Sprintf("session=%s family=%s pid=%d reason=%s", o.Session, o.Family, o.PID, o.Reason))
	}
	return rep
}

func (opts ReapOptions) log(session, level, event, detail string) {
	target := opts.LogSession
	if target == "" {
		target = session
	}
	if target == "" || target == "-" {
		return
	}
	LogLifecycle(target, level, "reaper", event, detail)
}

// killOrder groups candidates by session, monitor then daemon then the rest,
// keeping discovery order within each rank.
func killOrder(proven []Orphan) []Orphan {
	rank := func(o Orphan) int {
		switch o.Family {
		case "monitor":
			return 0
		case "daemon":
			return 1
		}
		return 2
	}
	var sessions []string
	bySession := map[string][]Orphan{}
	for _, o := range proven {
		if _, ok := bySession[o.Session]; !ok {
			sessions = append(sessions, o.Session)
		}
		bySession[o.Session] = append(bySession[o.Session], o)
	}
	var out []Orphan
	for _, s := range sessions {
		for r := 0; r < 3; r++ {
			for _, o := range bySession[s] {
				if rank(o) == r {
					out = append(out, o)
				}
			}
		}
	}
	return out
}

const (
	killReaped  = "reaped"
	killFailed  = "failed"
	killRefused = "refused"
)

// reaperGrace is how long a TERMed orphan has to exit before KILL; a var so
// tests can shorten it.
var reaperGrace = 2 * time.Second

// terminate stops one proven orphan and reports what happened: reaped only
// once it is confirmed gone — for a proc, its whole process group. One already
// gone when its turn comes is reaped with that noted, unsignalled: a daemon
// TERMed earlier in the sweep stops its own harness (MUX-184 Decision 4), and
// that harness must not then read as unresolved. A reused pid reads as alive
// and meets the proof. The proof runs before TERM and again before KILL; one
// that no longer holds sends nothing further.
//
// A proc's owner lock dies with its leader, so a leader that exits on TERM
// while a child ignores it would leave nothing to prove the survivor by. The
// group itself is the anchor then: a process-group id is never reused while
// any member lives, and the group is watched from TERM on, so a group still
// alive before KILL is the one the lock proved — KILL needs only the
// session's absence asked again.
func terminate(o Orphan, grace time.Duration) (outcome, reason string) {
	if !o.alive() {
		return killReaped, "exited before its own signal"
	}
	if ok, why := o.prove(); !ok {
		return killRefused, "proof no longer holds before TERM: " + why
	}
	if err := o.signal(syscall.SIGTERM); err != nil && o.alive() {
		return killFailed, fmt.Sprintf("TERM: %v", err)
	}
	if o.waitGone(grace) {
		return killReaped, ""
	}
	recheck := o.prove
	if o.group && !reaperAlive(o.PID) {
		recheck = o.absent
	}
	if ok, why := recheck(); !ok {
		return killFailed, "alive after TERM but its proof no longer holds (" + why + "), KILL withheld"
	}
	if err := o.signal(syscall.SIGKILL); err != nil && o.alive() {
		return killFailed, fmt.Sprintf("KILL: %v", err)
	}
	if o.waitGone(time.Second) {
		return killReaped, ""
	}
	if o.group {
		return killFailed, fmt.Sprintf("process group %d survived KILL", o.PID)
	}
	return killFailed, "survived KILL"
}

func (o Orphan) signal(sig syscall.Signal) error {
	if o.group {
		return reaperSignal(-o.PID, sig)
	}
	return reaperSignal(o.PID, sig)
}

// alive reports the orphan still running: any member of a proc's group.
func (o Orphan) alive() bool {
	if o.group {
		return reaperGroupAlive(o.PID)
	}
	return reaperAlive(o.PID)
}

func (o Orphan) waitGone(within time.Duration) bool {
	for deadline := time.Now().Add(within); ; time.Sleep(25 * time.Millisecond) {
		if !o.alive() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

// SessionPositivelyGone reports session positively absent from the tmux
// server it recorded running on — the reaper's bar for "dead". An unanswered
// probe, no record, or another server's record all read as not gone.
func SessionPositivelyGone(session string) bool {
	view, err := reaperTmuxView()
	if err != nil {
		return false
	}
	verdict, _ := sessionVerdict(session, BusDir(session), view)
	return verdict == sessionDead
}

// stillDead asks the server again whether session is absent where it ran.
func stillDead(session string, dirs map[string]string) (bool, string) {
	view, err := reaperTmuxView()
	if err != nil {
		return false, fmt.Sprintf("liveness unknown: %v", err)
	}
	if verdict, why := sessionVerdict(session, dirs[session], view); verdict != sessionDead {
		if why == "" {
			why = "session is live"
		}
		return false, why
	}
	return true, ""
}

// sessionRecords lists the processes a session's owner locks prove it owns,
// and reports its running proc.jsonl entries that hold none as unproven.
func sessionRecords(session, dir string, dirs map[string]string) []Orphan {
	var out []Orphan
	lockDir := filepath.Join(dir, "lock")
	held := map[int]bool{}
	ownerLock := func(path, family string, pid int) Orphan {
		held[pid] = true
		absent := func() (bool, string) { return stillDead(session, dirs) }
		return Orphan{Session: session, Family: family, PID: pid, Proof: "owner-lock", group: family == "proc", absent: absent,
			prove: func() (bool, string) {
				if LockHolderPid(path) != pid {
					return false, filepath.Base(path) + " is no longer held by it"
				}
				return absent()
			}}
	}
	for _, f := range []struct{ file, family string }{{"monitor.owner", "monitor"}, {"daemon.owner", "daemon"}} {
		path := filepath.Join(lockDir, f.file)
		if pid := LockHolderPid(path); pid > 0 {
			out = append(out, ownerLock(path, f.family, pid))
		}
	}
	entries, _ := os.ReadDir(filepath.Join(lockDir, "owners"))
	for _, e := range entries {
		family, _, pid, ok := parseOwnerLockName(e.Name())
		if !ok {
			continue
		}
		path := filepath.Join(lockDir, "owners", e.Name())
		switch holder := LockHolderPid(path); {
		case holder == 0:
			_ = os.Remove(path)
		case holder != pid:
			out = append(out, Orphan{Session: session, Family: family, PID: holder, Reason: fmt.Sprintf("owner lock %s held by another pid", e.Name())})
		default:
			out = append(out, ownerLock(path, family, pid))
		}
	}
	procs, _ := readProcEntriesAt(filepath.Join(dir, "proc.jsonl"))
	for _, p := range procs {
		if p.Status == "running" && !held[p.PID] && reaperAlive(p.PID) {
			out = append(out, Orphan{Session: session, Family: "proc", PID: p.PID, Reason: "proc record holds no owner lock — its started_at is wall time, not identity"})
		}
	}
	return out
}

// ownerLockHolders is every pid an owner lock of any session holds, live
// sessions included: a process a record accounts for is never reported as an
// unrecorded orphan.
func ownerLockHolders(dirs map[string]string) []int {
	var pids []int
	for _, dir := range dirs {
		lockDir := filepath.Join(dir, "lock")
		paths := []string{filepath.Join(lockDir, "daemon.owner"), filepath.Join(lockDir, "monitor.owner")}
		entries, _ := os.ReadDir(filepath.Join(lockDir, "owners"))
		for _, e := range entries {
			paths = append(paths, filepath.Join(lockDir, "owners", e.Name()))
		}
		for _, p := range paths {
			if pid := LockHolderPid(p); pid > 0 {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// readProcEntriesAt reads a proc registry by path; a dead session's bus dir
// may sit under the legacy base, which BusDir does not resolve.
func readProcEntriesAt(path string) ([]ProcEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseProcEntries(data)
}

// censusProc is one `ps -axww -o pid=,ppid=,command=` line.
type censusProc struct {
	pid, ppid int
	argv      []string
	command   string
}

func parseCensus(out string) []censusProc {
	var procs []censusProc
	for _, line := range strings.Split(out, "\n") {
		if p, ok := parseCensusLine(line); ok {
			procs = append(procs, p)
		}
	}
	return procs
}

func parseCensusLine(line string) (censusProc, bool) {
	f := strings.Fields(line)
	if len(f) < 3 {
		return censusProc{}, false
	}
	pid, err1 := strconv.Atoi(f[0])
	ppid, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return censusProc{}, false
	}
	return censusProc{pid: pid, ppid: ppid, argv: f[2:], command: strings.Join(f[2:], " ")}, true
}

var escabsSocket = regexp.MustCompile(`^escabs-(\d+)$`)

// argvClaim reads the session and family a process names in argv, for the
// families whose argv is their proof.
func argvClaim(p censusProc) (session, family string, ok bool) {
	if len(p.argv) == 0 {
		return "", "", false
	}
	bin := filepath.Base(p.argv[0])
	switch {
	case bin == "muxcode" && len(p.argv) > 1 && p.argv[1] == "watch":
		if procs := parseDaemonProcs(fmt.Sprintf("%d %s", p.pid, p.command)); len(procs) == 1 {
			if procs[0].Monitor {
				return procs[0].Session, "monitor", true
			}
			return procs[0].Session, "daemon", true
		}
	case bin == "muxcode" && len(p.argv) > 3 && p.argv[1] == "launch" && (p.argv[2] == "--resize" || p.argv[2] == "--auto-accept"):
		return p.argv[3], "launch-helper", true
	case isShell(bin) && len(p.argv) > 1 && strings.HasSuffix(p.argv[len(p.argv)-1], "/inbox-listener.sh"):
		if s, ok := legacyListenerSession(p.argv[len(p.argv)-1]); ok {
			return s, "legacy-listener", true
		}
	}
	return "", "", false
}

// censusOrphans reads the process table for the argv-carrying families of
// dead sessions, escabs test servers, and the orphaned processes no record
// accounts for. unknown names the absent sessions that cannot be judged, with
// why; found holds every pid already decided.
func censusOrphans(census []censusProc, dirs map[string]string, dead map[string]bool, unknown map[string]string, found map[int]bool) []Orphan {
	var out []Orphan
	for _, p := range census {
		if found[p.pid] {
			continue
		}
		bin := filepath.Base(p.argv[0])
		if session, family, ok := argvClaim(p); ok {
			switch {
			case dead[session]:
				out = append(out, argvOrphan(p.pid, session, family, dirs))
			case unknown[session] != "":
				out = append(out, Orphan{Session: session, Family: family, PID: p.pid, Reason: unknown[session]})
			case family == "legacy-listener" && dirs[session] == "":
				out = append(out, Orphan{Session: session, Family: family, PID: p.pid, Reason: "its bus dir is gone, so its tmux server is unrecorded"})
			}
			continue
		}
		switch {
		case bin == "tmux":
			if n := escabsServerPid(p.argv); n > 0 && !reaperAlive(n) {
				out = append(out, escabsOrphan(p.pid, n))
			}
		case isShell(bin) && strings.HasSuffix(p.argv[len(p.argv)-1], "/inbox-listener.sh"):
			out = append(out, Orphan{Session: "-", Family: "legacy-listener", PID: p.pid, Reason: "script path names no bus dir under this installation"})
		case p.ppid != 1:
		case isShell(bin) && strings.Contains(p.command, "while") && strings.Contains(p.command, "muxcode inbox --poll"):
			out = append(out, Orphan{Session: "-", Family: "legacy-listener", PID: p.pid, Reason: "loop form names no session"})
		case bin == "muxcode-llm-harness" || (isShell(bin) && len(p.argv) > 1 && filepath.Base(p.argv[1]) == "muxcode-llm-harness"):
			out = append(out, Orphan{Session: "-", Family: "harness", PID: p.pid, Reason: "orphaned with no owner lock naming it"})
		case bin == "muxcode" && len(p.argv) > 2 && p.argv[1] == "inbox" && strings.HasPrefix(p.argv[2], "--poll"):
			out = append(out, Orphan{Session: "-", Family: "listener", PID: p.pid, Reason: "orphaned with no owner lock naming it"})
		}
	}
	return out
}

// argvOrphan is an argv-proven candidate whose proof re-reads the pid's argv
// and asks the server again.
func argvOrphan(pid int, session, family string, dirs map[string]string) Orphan {
	return Orphan{Session: session, Family: family, PID: pid, Proof: "argv", prove: func() (bool, string) {
		p, err := reaperProcInfo(pid)
		if err != nil {
			return false, fmt.Sprintf("argv unreadable: %v", err)
		}
		if s, f, ok := argvClaim(p); !ok || s != session || f != family {
			return false, "its argv no longer names " + family + " of " + session
		}
		return stillDead(session, dirs)
	}}
}

// escabsOrphan is a test tmux server whose socket-name pid is dead; its proof
// re-reads the argv and the pid's liveness.
func escabsOrphan(pid, socketPid int) Orphan {
	return Orphan{Session: "-", Family: "escabs-server", PID: pid, Proof: "socket-pid-dead", prove: func() (bool, string) {
		p, err := reaperProcInfo(pid)
		if err != nil {
			return false, fmt.Sprintf("argv unreadable: %v", err)
		}
		if escabsServerPid(p.argv) != socketPid {
			return false, "its argv no longer names escabs-" + strconv.Itoa(socketPid)
		}
		if reaperAlive(socketPid) {
			return false, "its socket-name pid is alive"
		}
		return true, ""
	}}
}

func isShell(bin string) bool { return bin == "bash" || bin == "sh" || bin == "zsh" }

// escabsServerPid returns the pid named by a `tmux -L escabs-<pid>` socket,
// or 0 for any other tmux.
func escabsServerPid(argv []string) int {
	if len(argv) == 0 || filepath.Base(argv[0]) != "tmux" {
		return 0
	}
	for i := 1; i+1 < len(argv); i++ {
		if argv[i] == "-L" {
			if m := escabsSocket.FindStringSubmatch(argv[i+1]); m != nil {
				n, _ := strconv.Atoi(m[1])
				return n
			}
		}
	}
	return 0
}

// legacyListenerSession parses the session from a legacy listener script path
// `<base>/muxcode-bus-<s>/inbox-listener.sh`, accepting only a base this
// installation scans.
func legacyListenerSession(script string) (string, bool) {
	busDir := filepath.Dir(script)
	session, ok := strings.CutPrefix(filepath.Base(busDir), "muxcode-bus-")
	if !ok || session == "" {
		return "", false
	}
	for _, root := range cleanupScanRoots() {
		if samePath(filepath.Dir(busDir), root) {
			return session, true
		}
	}
	return "", false
}

// scanBusDirs maps each session with a bus dir under this installation's
// bases to that dir. The current base wins over a legacy /tmp copy.
func scanBusDirs() map[string]string {
	dirs := map[string]string{}
	roots := cleanupScanRoots()
	for i := len(roots) - 1; i >= 0; i-- {
		entries, err := os.ReadDir(roots[i])
		if err != nil {
			continue
		}
		for _, e := range entries {
			if session, ok := strings.CutPrefix(e.Name(), "muxcode-bus-"); ok && session != "" && e.IsDir() {
				dirs[session] = filepath.Join(roots[i], e.Name())
			}
		}
	}
	return dirs
}

// tmuxView is what the caller's tmux server says: its socket, and its
// sessions by name and by session id.
type tmuxView struct {
	socket string
	names  map[string]bool
	ids    map[string]bool
}

const (
	sessionLive = iota
	sessionDead
	sessionUnknown
)

// sessionVerdict is positive absence on the server the session recorded
// running on. A session the server lists — by name, or by its recorded
// session id under a new name after `tmux rename-session` — is live; an
// absent one is dead only when its recorded socket is this server's; with no
// readable record, or a record naming another server, it cannot be judged
// from here. A recycled id after a server restart reads as live: the safe
// side.
func sessionVerdict(session, dir string, view tmuxView) (int, string) {
	if view.names[session] {
		return sessionLive, ""
	}
	if dir == "" {
		return sessionUnknown, "no bus dir, so its tmux server is unrecorded"
	}
	recorded, id, err := readTmuxRecord(dir)
	if err != nil {
		return sessionUnknown, fmt.Sprintf("its tmux server is unrecorded (%s: %v)", tmuxSocketFile, errors.Unwrap(err))
	}
	if !samePath(recorded, view.socket) {
		return sessionUnknown, "it ran on another tmux server (" + recorded + ")"
	}
	if id != "" && view.ids[id] {
		return sessionLive, "renamed: its session id " + id + " is still listed"
	}
	return sessionDead, ""
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return resolvePath(a) == resolvePath(b)
}

// resolvePath follows symlinks as far as the path exists (/tmp is
// /private/tmp on macOS), keeping a missing tail as written.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

// noServerOn reads the tmux client's reply when no server listens on the
// socket — positive absence of every session on it. A socket the caller may
// not reach ("Operation not permitted") is not absence.
var noServerOn = regexp.MustCompile(`(?:no server running on (\S+)|error connecting to (\S+) \((?:No such file or directory|Connection refused)\))`)

// reaperTmuxView asks the caller's tmux server for its socket and sessions.
// A var so tests can answer for it.
var reaperTmuxView = func() (tmuxView, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{socket_path}\t#{session_name}\t#{session_id}").CombinedOutput()
	if err != nil {
		if m := noServerOn.FindStringSubmatch(string(out)); m != nil {
			return tmuxView{socket: m[1] + m[2], names: map[string]bool{}, ids: map[string]bool{}}, nil
		}
		return tmuxView{}, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	view := tmuxView{names: map[string]bool{}, ids: map[string]bool{}}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		view.socket = f[0]
		view.names[f[1]] = true
		view.ids[f[2]] = true
	}
	if view.socket == "" {
		return tmuxView{}, errors.New("tmux listed no socket path")
	}
	return view, nil
}

// reaperPS lists every process; a var so tests can answer for it.
var reaperPS = func() ([]censusProc, error) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, err
	}
	return parseCensus(string(out)), nil
}

// reaperProcInfo re-reads one process's ppid and argv for a proof recheck.
var reaperProcInfo = func(pid int) (censusProc, error) {
	out, err := exec.Command("ps", "-ww", "-o", "pid=,ppid=,command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return censusProc{}, err
	}
	p, ok := parseCensusLine(strings.TrimSpace(string(out)))
	if !ok {
		return censusProc{}, fmt.Errorf("pid %d not listed", pid)
	}
	return p, nil
}

var (
	reaperAlive  = CheckProcAlive
	reaperSignal = syscall.Kill
	// reaperGroupAlive reports any member of process group pgid alive; EPERM
	// means a member exists that this user may not signal.
	reaperGroupAlive = func(pgid int) bool {
		err := syscall.Kill(-pgid, 0)
		return err == nil || errors.Is(err, syscall.EPERM)
	}
)
