package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// cleanupWithReaper gives a cleanup test a reaperBus whose tmux server and
// cleanup's own liveness check agree: live is listed, every other session
// is absent from b.socket.
func cleanupWithReaper(t *testing.T, live ...string) *reaperBus {
	t.Helper()
	b := newReaperBus(t)
	b.live = live
	orig := tmuxSessionNames
	tmuxSessionNames = func() ([]string, error) { return b.live, nil }
	t.Cleanup(func() { tmuxSessionNames = orig })
	return b
}

// TestCleanupSessionReapsThenRemovesItsOwnSession is sub-defect A and B
// together: the close hook's form cleans the session that closed — its own
// name — reaping its daemon first and only then removing its bus dir, with a
// session-cleanup row. Before MUX-184 the hook skipped exactly this session,
// and file removal without the kill was undone by the daemon recreating it.
func TestCleanupSessionReapsThenRemovesItsOwnSession(t *testing.T) {
	b := cleanupWithReaper(t)
	s := b.session(t, "closed-session", b.socket)
	daemon := b.hold(t, s, "daemon", "-")

	result, err := CleanupSession(s, false)

	if err != nil {
		t.Fatal(err)
	}
	if CheckProcAlive(daemon) {
		t.Error("the closed session's daemon survived its cleanup")
	}
	if _, err := os.Stat(BusDir(s)); !os.IsNotExist(err) {
		t.Errorf("the closed session's own bus dir is still present: %v", err)
	}
	if len(result.Reaped) != 1 || len(result.BusDirs) != 1 || len(result.Held) != 0 {
		t.Errorf("result %+v", result)
	}
	if rows := eventRows(t, s, "session-cleanup"); len(rows) != 1 {
		t.Errorf("session-cleanup rows: %q", rows)
	}
}

// TestCleanupSessionHoldsADirItCannotClear: a session whose processes cannot
// be proved — no tmux socket on record — keeps its bus dir, reported and
// logged session-cleanup-held, its daemon left running; a session tmux still
// lists is not touched at all.
func TestCleanupSessionHoldsADirItCannotClear(t *testing.T) {
	t.Run("an unproven daemon", func(t *testing.T) {
		b := cleanupWithReaper(t)
		s := b.session(t, "unproven-session", "")
		daemon := b.hold(t, s, "daemon", "-")
		result, err := CleanupSession(s, false)
		if err != nil {
			t.Fatal(err)
		}
		if !CheckProcAlive(daemon) {
			t.Error("an unproven daemon was killed")
		}
		if _, err := os.Stat(BusDir(s)); err != nil {
			t.Errorf("a held bus dir was removed: %v", err)
		}
		if len(result.Held) != 1 || !strings.Contains(result.Held[0], "daemon pid") || len(result.BusDirs) != 0 {
			t.Errorf("result %+v", result)
		}
		if rows := eventRows(t, s, "session-cleanup-held"); len(rows) != 1 {
			t.Errorf("session-cleanup-held rows: %q", rows)
		}
		if out := FormatCleanupResult(result, false); !strings.Contains(out, "Left 1 bus directory(s) in place") {
			t.Errorf("report: %s", out)
		}
	})
	t.Run("a live session", func(t *testing.T) {
		b := cleanupWithReaper(t, "live-session")
		s := b.session(t, "live-session", b.socket)
		daemon := b.hold(t, s, "daemon", "-")
		result, err := CleanupSession(s, false)
		if err != nil {
			t.Fatal(err)
		}
		if !CheckProcAlive(daemon) || len(result.BusDirs) != 0 {
			t.Errorf("a live session was cleaned: %+v", result)
		}
		if _, err := os.Stat(BusDir(s)); err != nil {
			t.Errorf("a live session's bus dir was removed: %v", err)
		}
	})
}

// TestCleanupStaleReapsBeforeRemoving: the stale sweep reaps a dead
// session's processes before removing its dir, and a dry run plans the reap,
// signals nothing and still lists the dir as removable.
func TestCleanupStaleReapsBeforeRemoving(t *testing.T) {
	t.Run("for real", func(t *testing.T) {
		b := cleanupWithReaper(t, "current")
		s := b.session(t, "stale-one", b.socket)
		harness := b.hold(t, s, "harness", "prompt")
		result, err := CleanupStale("current", false, false)
		if err != nil {
			t.Fatal(err)
		}
		if CheckProcAlive(harness) || len(result.Reaped) != 1 {
			t.Errorf("the stale session's harness was not reaped first: %+v", result)
		}
		if _, err := os.Stat(BusDir(s)); !os.IsNotExist(err) {
			t.Errorf("stale bus dir still present: %v", err)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		b := cleanupWithReaper(t, "current")
		s := b.session(t, "stale-two", b.socket)
		harness := b.hold(t, s, "harness", "prompt")
		result, err := CleanupStale("current", true, false)
		if err != nil {
			t.Fatal(err)
		}
		if !CheckProcAlive(harness) {
			t.Error("a dry run killed a process")
		}
		if len(result.Reaped) != 1 || len(result.BusDirs) != 1 || len(result.Held) != 0 {
			t.Errorf("a dry run must plan the reap and list the dir: %+v", result)
		}
		if out := FormatCleanupResult(result, true); !strings.Contains(out, "Would reap 1") {
			t.Errorf("report: %s", out)
		}
	})
}

// sessionArtifacts gives a session a spawn worktree dir and an analyze
// trigger beside its bus dir, under the overridden base cleanup scans, and
// returns all three paths.
func sessionArtifacts(t *testing.T, base, session string) []string {
	t.Helper()
	spawn := filepath.Join(base, "muxcode-spawn-"+session)
	trigger := filepath.Join(base, "muxcode-analyze-"+session+".trigger")
	if err := os.MkdirAll(spawn, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trigger, []byte("1 x.go\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return []string{BusDir(session), spawn, trigger}
}

func assertPresent(t *testing.T, want bool, paths []string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s present=%v, want %v", p, err == nil, want)
		}
	}
}

// TestCleanupHoldsAnUnresolvedSessionWhole: a session the reaper could not
// clear keeps every artifact — bus dir, spawn worktree, trigger — not only
// its bus dir. Unresolved covers what no lock or argv shows any more: a live
// proc whose record holds no owner lock (reported unproven), and a proc group
// that outlived its leader and KILL (reported failed). A dead session with
// nothing running, cleaned in the same pass, loses all three (negative
// control).
func TestCleanupHoldsAnUnresolvedSessionWhole(t *testing.T) {
	t.Run("a live proc record with no owner lock", func(t *testing.T) {
		b := cleanupWithReaper(t, "current")
		held := b.session(t, "unresolved-proc", b.socket)
		gone := b.session(t, "clear-session", b.socket)
		heldPaths, gonePaths := sessionArtifacts(t, b.base, held), sessionArtifacts(t, b.base, gone)
		sleeper := exec.Command("sleep", "30")
		if err := sleeper.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })
		line := `{"id":"legacy","pid":` + strconv.Itoa(sleeper.Process.Pid) + `,"command":"sleep 30","status":"running","started_at":1}`
		if err := os.WriteFile(filepath.Join(BusDir(held), "proc.jsonl"), []byte(line+"\n"), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := CleanupStale("current", false, false)

		if err != nil {
			t.Fatal(err)
		}
		assertPresent(t, true, heldPaths)
		assertPresent(t, false, gonePaths)
		if len(result.Held) != 1 || !strings.Contains(result.Held[0], "proc pid "+strconv.Itoa(sleeper.Process.Pid)) {
			t.Errorf("held %q", result.Held)
		}
		if !CheckProcAlive(sleeper.Process.Pid) {
			t.Error("an unproven proc was killed")
		}
	})
	t.Run("a proc group that outlived its leader", func(t *testing.T) {
		b := cleanupWithReaper(t, "current")
		held := b.session(t, "unresolved-group", b.socket)
		gone := b.session(t, "clear-session", b.socket)
		heldPaths, gonePaths := sessionArtifacts(t, b.base, held), sessionArtifacts(t, b.base, gone)
		pgid := b.hold(t, held, "proc-tree", "1794-proc-tree")
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(25 * time.Millisecond) {
			if out, _ := exec.Command("pgrep", "-g", strconv.Itoa(pgid)).Output(); len(strings.Fields(string(out))) >= 2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the proc tree never started its TERM-ignoring child")
			}
		}
		reaperSignal = func(pid int, sig syscall.Signal) error {
			if sig == syscall.SIGKILL {
				return nil
			}
			return syscall.Kill(pid, sig)
		}

		result, err := CleanupStale("current", false, false)

		if err != nil {
			t.Fatal(err)
		}
		if !reaperGroupAlive(pgid) || CheckProcAlive(pgid) {
			t.Fatal("fixture: want the leader gone and its group alive")
		}
		assertPresent(t, true, heldPaths)
		assertPresent(t, false, gonePaths)
		if len(result.Held) != 1 || !strings.Contains(result.Held[0], "survived KILL") {
			t.Errorf("held %q", result.Held)
		}
	})
}

// TestStopSessionHarness is the daemon's TERM handler's reach: it stops a
// harness its owner lock proves, and never signals a pid only the marker
// file names.
func TestStopSessionHarness(t *testing.T) {
	b := newReaperBus(t)
	s := b.session(t, "stopping", b.socket)
	harness := b.hold(t, s, "harness", "prompt")
	stranger := exec.Command("sleep", "30")
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stranger.Process.Kill(); _ = stranger.Wait() })
	if err := os.WriteFile(filepath.Join(BusDir(s), "harness-research.pid"), []byte(strconv.Itoa(stranger.Process.Pid)), 0644); err != nil {
		t.Fatal(err)
	}

	stopped := StopSessionHarness(s, 500*time.Millisecond)

	if len(stopped) != 1 || CheckProcAlive(harness) {
		t.Errorf("the locked harness was not stopped: %v", stopped)
	}
	if !CheckProcAlive(stranger.Process.Pid) {
		t.Error("a pid named only by a marker file was signalled")
	}
}

// TestStopOwnChild: a running child is stopped through its handle; a
// finished one is reported gone without a signal reaching its old pid.
func TestStopOwnChild(t *testing.T) {
	running := exec.Command("sleep", "30")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = running.Wait() }()
	if !StopOwnChild(running.Process, 500*time.Millisecond) {
		t.Error("a running child was not stopped")
	}
	finished := exec.Command("true")
	if err := finished.Run(); err != nil {
		t.Fatal(err)
	}
	if !StopOwnChild(finished.Process, 500*time.Millisecond) || !StopOwnChild(nil, 0) {
		t.Error("a finished or absent child must read as gone")
	}
}
