package bus

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// stubPs replaces the ps listing for the test.
func stubPs(t *testing.T, out string, err error) {
	t.Helper()
	orig := psOutputFn
	psOutputFn = func() ([]byte, error) { return []byte(out), err }
	t.Cleanup(func() { psOutputFn = orig })
}

// livePidForTest is a child process that stays up for the test — the
// unrelated live process a reused pid would name.
func livePidForTest(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd.Process.Pid
}

func lockPath(session, name string) string {
	return filepath.Join(BusDirBase(), "muxcode-bus-"+session, "lock", name)
}

// TestPidLockHelperProcess is not a test: run as a child by holdLockInChild,
// it holds a lock until its stdin closes. Record locks never conflict within
// one process, so a holder must be another process. Mode "acquire" uses
// AcquirePidLock; mode "raw" takes the record lock and writes nothing — the
// interval before a new holder publishes its pid, or a publication that failed;
// mode "instance" takes AcquireInstanceLock on path and its .owner sibling, as
// a daemon of this build does; mode "flock" takes only the flock on path, as a
// daemon of an earlier build does.
func TestPidLockHelperProcess(t *testing.T) {
	path, mode := os.Getenv("MUXCODE_PIDLOCK_HELPER_PATH"), os.Getenv("MUXCODE_PIDLOCK_HELPER_MODE")
	if path == "" {
		t.Skip("helper process only")
	}
	switch mode {
	case "acquire":
		if _, err := AcquirePidLock(path); err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
	case "instance":
		if _, err := AcquireInstanceLock(path, ownerSibling(path)); err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
	case "flock":
		if _, err := acquireFlock(path); err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
	case "raw":
		f, err := os.OpenFile(path, os.O_RDWR, 0644)
		if err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
		lk := wholeFileLock(syscall.F_WRLCK)
		if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lk); err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
	}
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// holdLockInChild starts a helper holding path's lock and returns its pid
// once it holds it; the helper exits when the test ends or stop is called.
func holdLockInChild(t *testing.T, path, mode string) (pid int, stop func()) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		writeFile(t, path, "")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPidLockHelperProcess$")
	cmd.Env = append(os.Environ(), "MUXCODE_PIDLOCK_HELPER_PATH="+path, "MUXCODE_PIDLOCK_HELPER_MODE="+mode)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stop = func() { stdin.Close(); _ = cmd.Wait() }
	t.Cleanup(func() { _ = cmd.Process.Kill(); stop() })
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("lock helper for %s: %q", path, line)
	}
	return cmd.Process.Pid, stop
}

func ownerSibling(lock string) string {
	return strings.TrimSuffix(lock, ".lock") + ".owner"
}

// TestAcquireInstanceLock_ExcludesAcrossVersions: flock and record locks are
// independent on Linux, so the instance flock is what keeps an earlier build's
// daemon and this build's from sharing one bus. Each way round, the second
// instance is refused while the first holds; this build's holder is still
// discoverable through its owner file.
func TestAcquireInstanceLock_ExcludesAcrossVersions(t *testing.T) {
	t.Run("legacy holder refuses this build", func(t *testing.T) {
		lock := filepath.Join(t.TempDir(), "lock", "daemon.lock")
		_, stop := holdLockInChild(t, lock, "flock")
		if _, err := AcquireInstanceLock(lock, ownerSibling(lock)); !errors.Is(err, ErrLockHeld) {
			t.Fatalf("this build started beside an earlier build's daemon: %v", err)
		}
		if got := LockHolderPid(ownerSibling(lock)); got != 0 {
			t.Errorf("a refused acquisition left an owner lock held by %d", got)
		}
		stop()
		release, err := AcquireInstanceLock(lock, ownerSibling(lock))
		if err != nil {
			t.Fatalf("once the earlier daemon exits this build must start: %v", err)
		}
		release()
	})
	t.Run("this build refuses a legacy holder", func(t *testing.T) {
		lock := filepath.Join(t.TempDir(), "lock", "daemon.lock")
		holder, _ := holdLockInChild(t, lock, "instance")
		if _, err := acquireFlock(lock); !errors.Is(err, ErrLockHeld) {
			t.Fatalf("an earlier build's daemon started beside this build's: %v", err)
		}
		if got := LockHolderPid(ownerSibling(lock)); got != holder {
			t.Errorf("owner = %d, want the instance holder %d", got, holder)
		}
	})
}

func procKeys(procs []DaemonProc) []string {
	var keys []string
	for _, p := range procs {
		keys = append(keys, fmt.Sprintf("%s/%d/monitor=%v", p.Session, p.PID, p.Monitor))
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys(keys ...string) []string {
	sort.Strings(keys)
	return keys
}

// TestListDaemonProcs_LockHoldersWhenPsCannotRun is MUX-161: a Codex build
// sandbox refuses to exec ps, and discovery was ps alone, so every build there
// skipped the rollout. With ps failing, a session's daemon and monitor are the
// processes the kernel reports as holding its locks, and the failure is an
// info row, not an error. Negative controls, each with a live unrelated
// process named in the file: an unheld lock is no target, a lock held before
// its holder published its pid names the holder, and so does a held lock
// whose pid was never written.
func TestListDaemonProcs_LockHoldersWhenPsCannotRun(t *testing.T) {
	useTempBusDir(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	t.Setenv("BUS_SESSION", "alpha")
	unrelated := strconv.Itoa(livePidForTest(t))
	daemonPid, _ := holdLockInChild(t, lockPath("alpha", "daemon.owner"), "acquire")
	monitorPid, _ := holdLockInChild(t, lockPath("alpha", "monitor.owner"), "acquire")
	writeFile(t, lockPath("beta", "daemon.owner"), unrelated)
	writeFile(t, lockPath("gamma", "daemon.owner"), unrelated)
	prePublishPid, _ := holdLockInChild(t, lockPath("gamma", "daemon.owner"), "raw")
	writeFile(t, lockPath("delta", "monitor.owner"), "")
	unpublishedPid, _ := holdLockInChild(t, lockPath("delta", "monitor.owner"), "raw")
	stubPs(t, "", errors.New("fork/exec /bin/ps: operation not permitted"))

	procs, err := ListDaemonProcs()
	if err != nil {
		t.Fatalf("a ps failure must not fail discovery: %v", err)
	}
	want := sortedKeys(
		fmt.Sprintf("alpha/%d/monitor=false", daemonPid),
		fmt.Sprintf("alpha/%d/monitor=true", monitorPid),
		fmt.Sprintf("gamma/%d/monitor=false", prePublishPid),
		fmt.Sprintf("delta/%d/monitor=true", unpublishedPid),
	)
	if got := procKeys(procs); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("discovered %v, want %v — the unrelated pid %s in a lock file must never be a target", got, want, unrelated)
	}
	rows, _ := FilterLifecycleLog("alpha", LifecycleFilterOpts{Event: "daemon-discovery-ps-failed"})
	if len(rows) != 1 || rows[0].Level != "info" {
		t.Errorf("want one info daemon-discovery-ps-failed row, got %+v", rows)
	}
}

// TestListDaemonProcs_PsIsAuthoritative: when ps runs, a held lock it
// contradicts is no target — its holder listed as an unrelated command, or as
// muxcode watch for another session and role, is reported only as ps names it.
func TestListDaemonProcs_PsIsAuthoritative(t *testing.T) {
	useTempBusDir(t)
	unrelated, _ := holdLockInChild(t, lockPath("alpha", "daemon.owner"), "acquire")
	otherMonitor, _ := holdLockInChild(t, lockPath("alpha", "monitor.owner"), "acquire")
	stubPs(t, fmt.Sprintf("%d /bin/sleep 60\n%d /usr/local/bin/muxcode watch --monitor other\n", unrelated, otherMonitor), nil)

	procs, err := ListDaemonProcs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fmt.Sprintf("other/%d/monitor=true", otherMonitor)}
	if got := procKeys(procs); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("discovered %v, want only what ps names %v", got, want)
	}
}

// TestListDaemonProcs_RoadsAgree: for a live session the lock holders and the
// ps listing name the same processes; ps alone sees the orphan whose bus
// directory is gone.
func TestListDaemonProcs_RoadsAgree(t *testing.T) {
	useTempBusDir(t)
	daemonPid, _ := holdLockInChild(t, lockPath("alpha", "daemon.owner"), "acquire")
	monitorPid, _ := holdLockInChild(t, lockPath("alpha", "monitor.owner"), "acquire")
	stubPs(t, fmt.Sprintf("%d /usr/local/bin/muxcode watch alpha\n%d /usr/local/bin/muxcode watch --monitor alpha\n424242 muxcode watch omega\n",
		daemonPid, monitorPid), nil)

	held := procKeys(listLockHolderProcs())
	procs, err := ListDaemonProcs()
	if err != nil {
		t.Fatal(err)
	}
	if fromPs := procKeys(FilterDaemonProcs(procs, "alpha")); fmt.Sprint(held) != fmt.Sprint(fromPs) {
		t.Errorf("the roads disagree: lock holders %v, ps %v", held, fromPs)
	}
	if len(FilterDaemonProcs(procs, "omega")) != 1 {
		t.Errorf("ps must still report the orphan: %v", procKeys(procs))
	}
}

// TestAcquirePidLock_HolderIsTheKernelsOwner: a held lock refuses a second
// process and reports its holder; once the holder exits the lock is free and
// can be taken; and this process never probes a lock it holds, since closing
// that descriptor would release it.
func TestAcquirePidLock_HolderIsTheKernelsOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock", "daemon.owner")
	holder, stop := holdLockInChild(t, path, "acquire")
	if _, err := AcquirePidLock(path); !errors.Is(err, ErrLockHeld) {
		t.Errorf("a second process must be refused with ErrLockHeld, got %v", err)
	}
	if got := LockHolderPid(path); got != holder {
		t.Errorf("holder = %d, want %d", got, holder)
	}
	stop()
	if got := LockHolderPid(path); got != 0 {
		t.Errorf("an exited holder's lock still names pid %d", got)
	}
	release, err := AcquirePidLock(path)
	if err != nil {
		t.Fatalf("a freed lock must be acquirable: %v", err)
	}
	defer release()
	if got := LockHolderPid(path); got != 0 {
		t.Errorf("probing its own lock must report 0, got %d", got)
	}
	if data, _ := os.ReadFile(path); strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("the holder's pid is written for a human reader: %q", data)
	}
}
