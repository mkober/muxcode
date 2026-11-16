package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// orphanReceiptEnv ("<session> <msgID> <adopterFile>") makes TestMain run
// runOrphanReceiptWriter instead of the tests; orphanReceiptShellEnv names the
// shell that started it.
const (
	orphanReceiptEnv      = "MUXCODE_TEST_ORPHAN_RECEIPT"
	orphanReceiptShellEnv = "MUXCODE_TEST_ORPHAN_RECEIPT_SHELL"
)

// runOrphanReceiptWriter waits until the shell that started it is no longer its
// parent, writes one ack receipt for msgID through WriteReceipt, then records
// the pid that adopted it in adopterFile — renamed into place, so a reader that
// sees the file also sees the receipt.
func runOrphanReceiptWriter(spec string) {
	var session, msgID, adopterFile string
	if n, _ := fmt.Sscan(spec, &session, &msgID, &adopterFile); n != 3 {
		os.Exit(2)
	}
	shell, _ := strconv.Atoi(os.Getenv(orphanReceiptShellEnv))
	for deadline := time.Now().Add(10 * time.Second); os.Getppid() == shell && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	WriteReceipt(session, msgID, "build", ReceiptKindAck)
	tmp := adopterFile + ".tmp"
	if os.WriteFile(tmp, []byte(strconv.Itoa(os.Getppid())), 0644) == nil {
		_ = os.Rename(tmp, adopterFile)
	}
}

// deadPID returns the pid of a process that has exited and been reaped.
func deadPID(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.Process.Pid
}

// liveHolder starts a process that stands in for a listener holding the
// --poll marker, alive for the test.
func liveHolder(t *testing.T) int {
	t.Helper()
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	return c.Process.Pid
}

// TestIsPollingExcludesAnOrphanedHolder is MUX-156 Phase 3: a live marker
// holder whose recorded parent is dead or pid 1 no longer counts, so the Stop
// hook relaunches the agent's listener instead of trusting the orphan — whose
// marker is left for it or its successor to clear. A holder with a live
// parent, a marker with no sidecar (an older binary's) and a sidecar naming
// another pid all count exactly as before.
func TestIsPollingExcludesAnOrphanedHolder(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	holder, dead := liveHolder(t), deadPID(t)
	cases := []struct {
		name    string
		sidecar string
		want    bool
	}{
		{"recorded parent dead", fmt.Sprintf("%d %d", holder, dead), false},
		{"recorded parent is pid 1", fmt.Sprintf("%d 1", holder), false},
		{"recorded parent alive", fmt.Sprintf("%d %d", holder, os.Getpid()), true},
		{"no sidecar", "", true},
		{"sidecar names another pid", fmt.Sprintf("%d %d", dead, dead), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			marker, sidecar := PollingMarkerPath(session, "build"), PollingParentPath(session, "build")
			if err := os.WriteFile(marker, []byte(fmt.Sprint(holder)), 0644); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(sidecar)
			if c.sidecar != "" {
				if err := os.WriteFile(sidecar, []byte(c.sidecar), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if got := IsPolling(session, "build"); got != c.want {
				t.Errorf("IsPolling = %v, want %v", got, c.want)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Errorf("the live holder's marker must be left in place: %v", err)
			}
		})
	}
}

// TestSetPollingRecordsParentSidecar: a claim records this process and its
// parent beside the marker, which stays a bare pid for scripts' `kill -0`;
// ClearPolling removes both.
func TestSetPollingRecordsParentSidecar(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	if !SetPolling(session, "build") {
		t.Fatal("SetPolling failed")
	}
	if data, _ := os.ReadFile(PollingMarkerPath(session, "build")); string(data) != fmt.Sprint(os.Getpid()) {
		t.Errorf("marker = %q, want the bare pid %d", data, os.Getpid())
	}
	pid, parent, ok := readPollingParent(session, "build")
	if !ok || pid != os.Getpid() || parent != os.Getppid() {
		t.Errorf("sidecar = %d %d (ok=%v), want %d %d", pid, parent, ok, os.Getpid(), os.Getppid())
	}
	if !IsPolling(session, "build") {
		t.Error("this process, parent alive, must count as polling")
	}
	ClearPolling(session, "build")
	if _, err := os.Stat(PollingParentPath(session, "build")); !os.IsNotExist(err) {
		t.Errorf("ClearPolling left the sidecar: %v", err)
	}
}

// TestWriteReceiptNamesTheConsumer: a receipt records the writing process and
// that its parent was alive, so an orphan-consumed message is told apart after
// the fact (MUX-156); the role and kind are unchanged.
func TestWriteReceiptNamesTheConsumer(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	WriteReceipt(session, "1-edit-receipt", "build", ReceiptKindAck)
	ds, err := ReadDeliveryStatus(session, "1-edit-receipt")
	if err != nil {
		t.Fatal(err)
	}
	if ds.AckedPID != os.Getpid() || ds.AckedParentAlive == nil || !*ds.AckedParentAlive {
		t.Errorf("receipt pid=%d parentAlive=%v, want %d and true", ds.AckedPID, ds.AckedParentAlive, os.Getpid())
	}
	if ds.AckedBy != "build" || ds.ReceiptKind != ReceiptKindAck {
		t.Errorf("role/kind changed: %+v", ds)
	}
}

// TestWriteReceiptByAnOrphanRecordsItsParentGone is the opposite case to
// TestWriteReceiptNamesTheConsumer: a process whose shell exited before it
// wrote — reparented as the 2026-09-08 orphan was — records its own pid and a
// present, false acked_parent_alive, so a receipt that always claims a live
// parent fails here. Adoption by a subreaper instead of pid 1 is the
// limitation WriteReceipt documents, so the test skips naming the adopter
// rather than count it as the pid-1 case.
func TestWriteReceiptByAnOrphanRecordsItsParentGone(t *testing.T) {
	dir := useTempBusDir(t)
	session := testSession(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const msgID = "1-edit-orphan-receipt"
	adopterFile := filepath.Join(t.TempDir(), "adopter")
	sh := exec.Command("/bin/sh", "-c", orphanReceiptShellEnv+`=$$ "$0" >/dev/null 2>&1 & echo $!`, self)
	sh.Env = append(os.Environ(), orphanReceiptEnv+"="+session+" "+msgID+" "+adopterFile,
		BusDirBaseEnv+"="+dir, "TMUX=", "TMUX_PANE=")
	out, err := sh.Output()
	if err != nil {
		t.Fatalf("starting the receipt writer: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("writer pid %q: %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	var adopted []byte
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if adopted, err = os.ReadFile(adopterFile); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("the orphaned writer never finished: %v", err)
	}
	if adopter := strings.TrimSpace(string(adopted)); adopter != "1" {
		t.Skipf("writer %d was adopted by subreaper %s, not pid 1: WriteReceipt reads that as a live parent (documented limitation)", pid, adopter)
	}
	ds, err := ReadDeliveryStatus(session, msgID)
	if err != nil {
		t.Fatal(err)
	}
	if ds.AckedPID != pid {
		t.Errorf("receipt pid = %d, want the orphaned writer %d", ds.AckedPID, pid)
	}
	if ds.AckedParentAlive == nil || *ds.AckedParentAlive {
		t.Errorf("an orphan's receipt must persist acked_parent_alive=false, got %v", ds.AckedParentAlive)
	}
	if ds.AckedBy != "build" || ds.ReceiptKind != ReceiptKindAck {
		t.Errorf("role/kind changed: %+v", ds)
	}
}
