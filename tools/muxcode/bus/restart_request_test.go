package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTakeRestartTarget_IsAlwaysTheConsumersOwnExecutable: the bus directory
// is writable by sandboxed roles, so nothing a request holds may choose what
// the unsandboxed daemon execs (MUX-161 review). A request planting an
// executable /tmp/.../muxcode is consumed and answered with this process's own
// executable — the installation it was started from — never the planted path.
func TestTakeRestartTarget_IsAlwaysTheConsumersOwnExecutable(t *testing.T) {
	dir := t.TempDir()
	planted := filepath.Join(dir, "evil", "muxcode")
	writeFile(t, planted, "#!/bin/sh\necho pwned\n")
	if err := os.Chmod(planted, 0755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	req := filepath.Join(dir, "lock", "daemon.restart")

	writeFile(t, req, planted)
	got, ok := TakeRestartTarget(req)
	if !ok || got != self || strings.Contains(got, "evil") {
		t.Errorf("TakeRestartTarget = %q, %v; want this process's own executable %q, never the planted %q", got, ok, self, planted)
	}
	if _, err := os.Stat(req); !os.IsNotExist(err) {
		t.Error("the request was not consumed")
	}

	if err := RequestRestart(req); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(req); strings.Contains(string(data), "/") {
		t.Errorf("a request carries no path: %q", data)
	}
	if got, ok := TakeRestartTarget(req); !ok || got != self {
		t.Errorf("a written request must be honoured with this executable, got %q, %v", got, ok)
	}
	if _, ok := TakeRestartTarget(req); ok {
		t.Error("no request pending, yet one was honoured")
	}
}

// TestStampedBuildSince: a restart is proved only by the installed build
// stamped after the rollout began — not by a stamp already on disk, and not by
// another build stamped since.
func TestStampedBuildSince(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.version")
	want := Info{Version: "v0.3.0", Commit: "bbb2222", Date: "2026-11-14T10:00:00Z"}
	writeStamp := func(info Info, age time.Duration) {
		t.Helper()
		writeFile(t, path, `{"version":"`+info.Version+`","commit":"`+info.Commit+`","date":"`+info.Date+`"}`)
		mtime := time.Now().Add(-age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	since := time.Now().Add(-time.Minute)

	writeStamp(want, 0)
	if !stampedBuildSince(path, want, since) {
		t.Error("the installed build stamped after the rollout began must prove the restart")
	}
	writeStamp(want, 10*time.Minute)
	if stampedBuildSince(path, want, since) {
		t.Error("a stamp older than the rollout proves nothing")
	}
	writeStamp(Info{Version: "v0.2.0", Commit: "aaa1111", Date: "2026-11-01T10:00:00Z"}, 0)
	if stampedBuildSince(path, want, since) {
		t.Error("another build's fresh stamp proves nothing")
	}
}

// TestRestartByRequest_UnprovedRestartFailsAndKillsNothing is MUX-161's
// failure mode on the no-ps road: when no process honours the request, the
// rollout fails with a daemon-upgrade-failed row, withdraws the request so it
// cannot fire later, and leaves the named process alive.
func TestRestartByRequest_UnprovedRestartFailsAndKillsNothing(t *testing.T) {
	useTempBusDir(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	orig := rolloutVerifyTimeout
	rolloutVerifyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { rolloutVerifyTimeout = orig })
	bystander := livePidForTest(t)
	plan := UpgradePlan{Session: "no-reexec", DaemonPID: bystander, Installed: Info{Version: "v0.3.0"}}

	res := restartByRequest(plan)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "did not hold its lock") || res.DaemonRestarted {
		t.Fatalf("an unproved restart must fail: %+v", res)
	}
	if _, err := os.Stat(DaemonRestartRequestPath("no-reexec")); !os.IsNotExist(err) {
		t.Error("the unhonoured request was not withdrawn")
	}
	if !CheckProcAlive(bystander) {
		t.Error("the request road killed the process it named")
	}
	rows, _ := FilterLifecycleLog("no-reexec", LifecycleFilterOpts{Event: "daemon-upgrade-failed"})
	if len(rows) != 1 {
		t.Errorf("want one daemon-upgrade-failed row, got %+v", rows)
	}
}
