package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// orphanListenerEnv makes this test binary's TestMain run the real
// `muxcode inbox --poll --loop` instead of the tests, so a test can start a
// listener as a separate process under a parent of its choosing.
const orphanListenerEnv = "MUXCODE_TEST_ORPHAN_LISTENER"

// listenerEnv is the environment a listener child needs: helper mode, the
// test's bus through MUXCODE_BUS_BASE, and no tmux server (TMUX_TMPDIR in the
// test's dir), so it can never touch a real session.
func listenerEnv(t *testing.T, dir, session, role string) []string {
	t.Helper()
	tmuxDir := filepath.Join(dir, "tmux")
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return append(os.Environ(), orphanListenerEnv+"=1", "BUS_SESSION="+session, "AGENT_ROLE="+role,
		bus.BusDirBaseEnv+"="+dir, "TMUX=", "TMUX_PANE=", "TMUX_TMPDIR="+tmuxDir)
}

// orphanListenerSession sets up a bus session in a temp dir for these tests.
func orphanListenerSession(t *testing.T) (dir, session string) {
	t.Helper()
	dir = t.TempDir()
	bus.SetBusDirBase(dir)
	t.Cleanup(bus.ResetBusDirBase)
	session = "test-orphan-listener"
	if err := bus.Init(session, filepath.Join(dir, "memory")); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return dir, session
}

// startOrphanedListener starts a listener under a /bin/sh that backgrounds it,
// lingers long enough for the listener to record sh as its parent, then exits.
// It returns the listener's pid once sh is gone. Stdout goes to /dev/null —
// the pipe nobody reads.
func startOrphanedListener(t *testing.T, dir, session, role string) int {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sh := exec.Command("/bin/sh", "-c", `"$0" >/dev/null 2>&1 & echo $!; sleep 2`, self)
	sh.Env = listenerEnv(t, dir, session, role)
	out, err := sh.Output()
	if err != nil {
		t.Fatalf("starting the listener: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("listener pid %q: %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

// startLiveListener starts a listener whose parent is this test process, which
// outlives it. The returned channel closes once the listener has exited and
// been reaped.
func startLiveListener(t *testing.T, dir, session, role string) (int, <-chan struct{}) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(self)
	c.Env = listenerEnv(t, dir, session, role)
	if err := c.Start(); err != nil {
		t.Fatalf("starting the listener: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = c.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = c.Process.Kill()
		<-exited
	})
	return c.Process.Pid, exited
}

func waitFor(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out after %s waiting for %s", limit, what)
}

// TestOrphanedListenerExitsWithoutConsuming is MUX-156's 2026-09-08 and
// 2026-09-10 orphans fixed: once its parent is gone the listener exits on its
// next tick — before reading the inbox — and releases the polling marker, so a
// message sent to the role after the parent died stays in the inbox with no
// receipt for a live listener to take. Before Phase 2 the orphan stayed alive,
// held IsPolling and acked that message into /dev/null.
func TestOrphanedListenerExitsWithoutConsuming(t *testing.T) {
	dir, session := orphanListenerSession(t)
	pid := startOrphanedListener(t, dir, session, "build")

	m := bus.NewMessage("edit", "build", "request", "build", "run the build", "")
	if err := bus.Send(session, m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, 10*time.Second, "the orphaned listener to exit", func() bool { return !bus.CheckProcAlive(pid) })

	if bus.IsPolling(session, "build") {
		t.Error("the exited orphan still holds the polling marker")
	}
	if msgs, _ := bus.Peek(session, "build"); len(msgs) != 1 || msgs[0].ID != m.ID {
		t.Errorf("the message must stay in build's inbox for a live listener, got %+v", msgs)
	}
	if ds, _ := bus.ReadDeliveryStatus(session, m.ID); ds.AckedAt != 0 {
		t.Errorf("the orphan acked a message it never handed to a reader: %+v", ds)
	}
}

// TestLiveListenerKeepsRunningAndConsumes is the negative control: a listener
// whose parent is alive neither exits by itself across several ticks nor stops
// counting for IsPolling, and consumes the role's next message with an ack
// receipt by its role, exactly as before Phase 2. Since Phase 3 that receipt
// also names the listener's pid and its live parent.
func TestLiveListenerKeepsRunningAndConsumes(t *testing.T) {
	dir, session := orphanListenerSession(t)
	pid, exited := startLiveListener(t, dir, session, "build")
	waitFor(t, 10*time.Second, "the listener to claim the polling marker", func() bool {
		return bus.IsPolling(session, "build")
	})

	time.Sleep(5 * time.Second)
	select {
	case <-exited:
		t.Fatalf("a listener with a live parent exited by itself (pid %d)", pid)
	default:
	}
	if !bus.IsPolling(session, "build") {
		t.Error("a live listener no longer counts for IsPolling")
	}

	m := bus.NewMessage("edit", "build", "request", "build", "run the build", "")
	if err := bus.Send(session, m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, 10*time.Second, "the live listener to consume the message", func() bool {
		ds, err := bus.ReadDeliveryStatus(session, m.ID)
		return err == nil && ds.AckedAt > 0
	})
	ds, _ := bus.ReadDeliveryStatus(session, m.ID)
	if ds.AckedBy != "build" || ds.ReceiptKind != bus.ReceiptKindAck {
		t.Errorf("want an ack receipt by build, got %+v", ds)
	}
	if ds.AckedPID != pid || ds.AckedParentAlive == nil || !*ds.AckedParentAlive {
		t.Errorf("the receipt must name listener %d with its parent alive, got pid=%d parentAlive=%v", pid, ds.AckedPID, ds.AckedParentAlive)
	}
}
