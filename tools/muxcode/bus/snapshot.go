package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Agent-down forensic snapshot (MUX-136 Phase 4). Both 2026-09-02 mass-death
// waves lost their evidence twice over: the lifecycle log rotated past them
// within ~2 hours, and the daemon's restart typed over the pane that held
// Claude's exit message. The snapshot is taken when the health sweep first
// declares a role down (strike 2, before strike 3 relaunches), so the next
// occurrence documents itself: the lifecycle log as it stands, the pane's
// scrollback, and the process table for the agent CLIs.

// agentDownSnapshotKeep bounds the snapshots kept per session; a flapping
// agent must not fill the disk with copies of the same log.
const agentDownSnapshotKeep = 20

// agentDownSnapshotPaneLines is how much scrollback to keep — enough to hold
// the exit banner and the turns before it, not the whole history.
const agentDownSnapshotPaneLines = 300

// snapshotPaneCapture and snapshotProcList are the two live reads and
// snapshotWriteFile the bundle write; tests replace them.
var snapshotPaneCapture = TmuxCapturePaneLines

var snapshotProcList = func() (string, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,lstart=,command=").Output()
	return string(out), err
}

var snapshotWriteFile = os.WriteFile

// AgentDownSnapshotDir is where a session's snapshots live.
func AgentDownSnapshotDir() string {
	return filepath.Join(LifecycleLogDir(), "snapshots")
}

// SnapshotAgentDown writes the forensic bundle for role and returns its
// directory. A failed capture (pane, process listing, lifecycle log) is
// recorded inside the bundle rather than failing it — partial evidence beats
// none. A file the bundle could not write is different: the directory is
// still returned, but with an error naming every missing file, so the caller
// never logs the path as evidence it does not hold.
func SnapshotAgentDown(session, role string) (string, error) {
	dir := filepath.Join(AgentDownSnapshotDir(),
		fmt.Sprintf("%s-%s-%d", session, role, time.Now().Unix()))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	var missing []string
	write := func(name string, data []byte) {
		if err := snapshotWriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			missing = append(missing, name+": "+err.Error())
		}
	}

	log, err := os.ReadFile(LifecycleLogPath(session))
	if err != nil {
		log = []byte("(lifecycle log unreadable: " + err.Error() + ")")
	}
	write("lifecycle.log", log)

	pane, err := snapshotPaneCapture(PaneTarget(session, role), agentDownSnapshotPaneLines)
	if err != nil {
		pane = "(pane capture failed: " + err.Error() + ")"
	}
	write("pane.txt", []byte(pane))

	procs, err := snapshotProcList()
	if err != nil {
		procs = "(process listing failed: " + err.Error() + ")"
	} else {
		procs = agentProcLines(procs)
	}
	write("procs.txt", []byte(procs))

	pruneAgentDownSnapshots(session, agentDownSnapshotKeep)
	if len(missing) > 0 {
		return dir, fmt.Errorf("snapshot %s incomplete: %s", dir, strings.Join(missing, "; "))
	}
	return dir, nil
}

// agentProcLines keeps the process-table lines that describe agents — the
// CLIs and muxcode's own processes — and trims each so a Claude argv
// (which carries the whole --agents JSON) does not bloat the bundle.
func agentProcLines(ps string) string {
	var b strings.Builder
	for _, line := range strings.Split(ps, "\n") {
		if !strings.Contains(line, "claude") && !strings.Contains(line, "opencode") &&
			!strings.Contains(line, "codex") && !strings.Contains(line, "muxcode") {
			continue
		}
		if len(line) > 200 {
			line = line[:200] + " …"
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// pruneAgentDownSnapshots removes the oldest snapshots of a session beyond
// keep. Names sort by their trailing unix timestamp because the prefix is
// fixed per session and role; ties across roles sort lexically, which is
// good enough for a bound.
func pruneAgentDownSnapshots(session string, keep int) {
	entries, err := os.ReadDir(AgentDownSnapshotDir())
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), session+"-") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return snapshotStamp(names[i]) < snapshotStamp(names[j])
	})
	for len(names) > keep {
		_ = os.RemoveAll(filepath.Join(AgentDownSnapshotDir(), names[0]))
		names = names[1:]
	}
}

// snapshotStamp returns the trailing unix timestamp of a snapshot name.
func snapshotStamp(name string) string {
	if i := strings.LastIndex(name, "-"); i >= 0 {
		return name[i+1:]
	}
	return name
}
