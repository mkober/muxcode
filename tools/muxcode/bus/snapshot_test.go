package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotAgentDown_WritesBundle(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	session := "snap-test"
	LogLifecycle(session, "warn", "daemon", "agent-health-fail", "plan failure #2")

	origCapture, origProcs := snapshotPaneCapture, snapshotProcList
	snapshotPaneCapture = func(target string, lines int) (string, error) {
		if lines != agentDownSnapshotPaneLines {
			t.Errorf("capture asked for %d lines, want %d", lines, agentDownSnapshotPaneLines)
		}
		return "Resume this session with: claude --resume 0f3a\n$ ", nil
	}
	snapshotProcList = func() (string, error) {
		return "  100 1 Wed Sep 2 15:56:44 2026 -bash\n" +
			"  200 100 Wed Sep 2 15:56:44 2026 claude --agent planner --agents " + strings.Repeat("x", 400) + "\n" +
			"  300 1 Wed Sep 2 15:56:45 2026 opencode --agent build\n" +
			"  400 1 Wed Sep 2 15:56:45 2026 nvim\n", nil
	}
	t.Cleanup(func() { snapshotPaneCapture, snapshotProcList = origCapture, origProcs })

	dir, err := SnapshotAgentDown(session, "plan")
	if err != nil {
		t.Fatalf("SnapshotAgentDown: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(dir), session+"-plan-") {
		t.Fatalf("snapshot dir %s not named by session and role", dir)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s missing from bundle: %v", name, err)
		}
		return string(data)
	}
	if !strings.Contains(read("lifecycle.log"), "plan failure #2") {
		t.Error("lifecycle.log does not carry the log as it stood")
	}
	if !strings.Contains(read("pane.txt"), "claude --resume 0f3a") {
		t.Error("pane.txt does not carry the pane scrollback")
	}
	procs := read("procs.txt")
	if !strings.Contains(procs, "claude --agent planner") || !strings.Contains(procs, "opencode --agent build") {
		t.Errorf("procs.txt missing agent processes:\n%s", procs)
	}
	if strings.Contains(procs, "nvim") || strings.Contains(procs, strings.Repeat("x", 300)) {
		t.Errorf("procs.txt not filtered/trimmed:\n%s", procs)
	}
}

// A failed capture is recorded, never fatal — partial evidence beats none.
func TestSnapshotAgentDown_RecordsCaptureFailure(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	origCapture, origProcs := snapshotPaneCapture, snapshotProcList
	snapshotPaneCapture = func(string, int) (string, error) { return "", errors.New("no server running") }
	snapshotProcList = func() (string, error) { return "", errors.New("ps failed") }
	t.Cleanup(func() { snapshotPaneCapture, snapshotProcList = origCapture, origProcs })

	dir, err := SnapshotAgentDown("snap-fail", "run")
	if err != nil {
		t.Fatalf("SnapshotAgentDown: %v", err)
	}
	pane, _ := os.ReadFile(filepath.Join(dir, "pane.txt"))
	procs, _ := os.ReadFile(filepath.Join(dir, "procs.txt"))
	if !strings.Contains(string(pane), "no server running") || !strings.Contains(string(procs), "ps failed") {
		t.Errorf("failures not recorded in the bundle: pane=%q procs=%q", pane, procs)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "lifecycle.log"))
	if !strings.Contains(string(log), "lifecycle log unreadable") {
		t.Errorf("missing lifecycle log not recorded in the bundle: %q", log)
	}
}

// A file the bundle could not write is reported by name, so the daemon never
// logs a path as evidence it does not hold; the files that did write stay.
func TestSnapshotAgentDown_ReportsWriteFailure(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	origCapture, origProcs, origWrite := snapshotPaneCapture, snapshotProcList, snapshotWriteFile
	snapshotPaneCapture = func(string, int) (string, error) { return "pane ok", nil }
	snapshotProcList = func() (string, error) { return "", nil }
	snapshotWriteFile = func(name string, data []byte, perm os.FileMode) error {
		if filepath.Base(name) == "pane.txt" {
			return errors.New("disk full")
		}
		return os.WriteFile(name, data, perm)
	}
	t.Cleanup(func() {
		snapshotPaneCapture, snapshotProcList, snapshotWriteFile = origCapture, origProcs, origWrite
	})

	dir, err := SnapshotAgentDown("snap-write", "commit")
	if err == nil {
		t.Fatal("write failure not reported")
	}
	if dir == "" {
		t.Fatal("dir withheld on a partial bundle — partial evidence beats none")
	}
	if !strings.Contains(err.Error(), "pane.txt") || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error does not name the missing file and cause: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "procs.txt")); statErr != nil {
		t.Errorf("files that could be written were not kept: %v", statErr)
	}
}

func TestSnapshotAgentDown_PrunesPerSession(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	origCapture, origProcs := snapshotPaneCapture, snapshotProcList
	snapshotPaneCapture = func(string, int) (string, error) { return "", nil }
	snapshotProcList = func() (string, error) { return "", nil }
	t.Cleanup(func() { snapshotPaneCapture, snapshotProcList = origCapture, origProcs })

	for i := 0; i < agentDownSnapshotKeep+5; i++ {
		os.MkdirAll(filepath.Join(AgentDownSnapshotDir(), "prune-me-plan-"+string(rune('a'+i))), 0755)
	}
	os.MkdirAll(filepath.Join(AgentDownSnapshotDir(), "other-session-plan-z"), 0755)

	if _, err := SnapshotAgentDown("prune-me", "plan"); err != nil {
		t.Fatalf("SnapshotAgentDown: %v", err)
	}
	entries, _ := os.ReadDir(AgentDownSnapshotDir())
	mine, other := 0, 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "prune-me-") {
			mine++
		} else {
			other++
		}
	}
	if mine != agentDownSnapshotKeep {
		t.Errorf("kept %d snapshots for the session, want %d", mine, agentDownSnapshotKeep)
	}
	if other != 1 {
		t.Errorf("another session's snapshot was pruned (%d left)", other)
	}
}
