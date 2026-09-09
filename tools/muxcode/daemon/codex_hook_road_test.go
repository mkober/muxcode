package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The daemon's scrape checks gate on PaneIsEvidence(), which for codex is the
// hook-road activation marker a launch writes (MUX-159 Phase 3). These tests
// put two codex roles in one session — one per road — and run the real checks
// against a stubbed pane, so the gate is pinned where it executes rather than
// at the provider alone. Each skip carries its negative control in the same
// session: the scrape-road role, fed the same pane, must still be scraped.

// codexSession is a bus session the provider resolver reads (BUS_SESSION set)
// with the named roles on codex. A role starts on the scrape road; hookRoad
// moves it.
func codexSession(t *testing.T, roles ...string) string {
	t.Helper()
	session := testSession(t)
	t.Setenv("BUS_SESSION", session)
	for _, r := range roles {
		t.Setenv(bus.RoleCLIEnvVar(r), "codex")
		if !bus.ResolveProvider(r).PaneIsEvidence() {
			t.Fatalf("fixture: %s should start on the scrape road", r)
		}
	}
	return session
}

// hookRoad puts a codex role on the hook road the way a launch does — by its
// activation marker — so ResolveProvider, not a stub, answers PaneIsEvidence.
func hookRoad(t *testing.T, session, role string) {
	t.Helper()
	marker := bus.CodexHooksMarkerPath(session, role)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if bus.ResolveProvider(role).PaneIsEvidence() {
		t.Fatalf("fixture: %s should be on the hook road", role)
	}
}

// recordingPane stubs pane capture with a fixed pane and records every target
// asked for, so a test can assert a role was never looked at.
func recordingPane(d *Daemon, pane string) *[]string {
	var targets []string
	d.capturePane = func(target string, _ int) (string, error) {
		targets = append(targets, target)
		return pane, nil
	}
	return &targets
}

func capturedRole(targets []string, session, role string) bool {
	for _, target := range targets {
		if strings.HasPrefix(target, session+":"+role+".") {
			return true
		}
	}
	return false
}

// inFlightTask records an edit→to request old enough to clear the send grace.
func inFlightTask(t *testing.T, session, to string) bus.Task {
	t.Helper()
	m := bus.NewMessage("edit", to, "request", to, "run it", "")
	m.TS -= 30
	if err := bus.CreateTask(session, m, 600); err != nil {
		t.Fatal(err)
	}
	task, err := bus.ReadTask(session, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func taskStatus(t *testing.T, session, id string) string {
	t.Helper()
	task, err := bus.ReadTask(session, id)
	if err != nil {
		t.Fatalf("ReadTask %s: %v", id, err)
	}
	return task.Status
}

func lifecycleDetails(t *testing.T, session, event string) []string {
	t.Helper()
	entries, err := bus.ReadLifecycleLog(session)
	if err != nil {
		t.Fatalf("ReadLifecycleLog: %v", err)
	}
	var details []string
	for _, e := range entries {
		if e.Event == event {
			details = append(details, e.Detail)
		}
	}
	return details
}

// codexDonePane is a codex turn that ended with a composed result above the
// composer — the one shape DetectTaskCompletion reports as complete.
const codexDonePane = "Build finished: EXIT=0 for go build ./...\n\n› \n"

func TestCheckNonHookTasks_HookCodexNeverScraped(t *testing.T) {
	session := codexSession(t, "build", "test")
	hookRoad(t, session, "build")
	d := New(session, 5, 8)
	d.agentAlive = func(_, role string) bool { return role == "build" || role == "test" }
	targets := recordingPane(d, codexDonePane)

	hook := inFlightTask(t, session, "build")
	scrape := inFlightTask(t, session, "test")
	tracked := time.Now().Unix() - 10
	d.taskDeliveredAt[hook.ID] = tracked
	d.taskDeliveredAt[scrape.ID] = tracked

	d.checkNonHookTasks()

	if capturedRole(*targets, session, "build") {
		t.Error("hook-road build pane was captured — its hooks are the evidence")
	}
	if got := taskStatus(t, session, hook.ID); got != bus.TaskInFlight {
		t.Errorf("hook-road task = %q, want still in-flight", got)
	}
	if _, ok := bus.FindResponseSince(session, "build", "edit", hook.SentAt); ok {
		t.Error("a response was synthesized for the hook-road agent")
	}

	// Negative control: the scrape-road role, same session, same pane.
	if !capturedRole(*targets, session, "test") {
		t.Fatal("scrape-road test pane was never captured")
	}
	if got := taskStatus(t, session, scrape.ID); got != bus.TaskCompleted {
		t.Errorf("scrape-road task = %q, want completed", got)
	}
	if _, ok := bus.FindResponseSince(session, "test", "edit", scrape.SentAt); !ok {
		t.Error("no response synthesized for the scrape-road agent")
	}
	rows := lifecycleDetails(t, session, "task-detected")
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "test task test from edit") {
		t.Errorf("task-detected rows = %q, want exactly one, naming test", rows)
	}
}

func TestCheckStuckProviders_HookCodexNotJudgedByPane(t *testing.T) {
	session := codexSession(t, "build", "test")
	hookRoad(t, session, "build")
	t.Setenv("MUXCODE_STUCK_RELOAD_DISABLE", "")
	d := New(session, 5, 8)
	d.agentAlive = func(_, role string) bool { return role == "build" || role == "test" }
	targets := recordingPane(d, "type validation failed: repeated across multiple consecutive rounds\n")

	d.checkStuckProviders()

	if capturedRole(*targets, session, "build") {
		t.Error("hook-road build pane was captured")
	}
	if n, ok := d.stuckSeen["build"]; ok {
		t.Errorf("hook-road build counted %d provider-loop sighting(s)", n)
	}
	if !capturedRole(*targets, session, "test") {
		t.Fatal("scrape-road test pane was never captured")
	}
	if d.stuckSeen["test"] != 1 {
		t.Errorf("scrape-road test sightings = %d, want 1", d.stuckSeen["test"])
	}
}

// dirtyRepo is a git repository with one committed file carrying an
// uncommitted change, so `git diff --stat` in it is non-empty. The test runs
// from inside it: checkNonHookEdits diffs the working directory.
func dirtyRepo(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	git := func(args ...string) {
		t.Helper()
		base := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}
		if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	file := filepath.Join(dir, "work.go")
	if err := os.WriteFile(file, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "work.go")
	git("commit", "-q", "-m", "init")
	if err := os.WriteFile(file, []byte("package x\n\nvar changed = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}

func TestCheckNonHookEdits_HookCodexSkipped(t *testing.T) {
	session := codexSession(t, "edit")
	dirtyRepo(t)
	t.Cleanup(func() { _ = os.Remove(bus.TriggerFile(session)) })
	d := New(session, 5, 8)

	hookRoad(t, session, "edit")
	d.lastEditDiffCheck = 0
	d.checkNonHookEdits()
	if d.lastEditDiffHash != "" {
		t.Errorf("hook-road edit was diffed: hash %q", d.lastEditDiffHash)
	}
	if _, err := os.Stat(bus.TriggerFile(session)); !os.IsNotExist(err) {
		t.Error("analyze trigger written for a hook-road edit agent")
	}

	// Negative control: the same dirty tree on the scrape road is detected.
	if err := os.Remove(bus.CodexHooksMarkerPath(session, "edit")); err != nil {
		t.Fatal(err)
	}
	d.lastEditDiffCheck = 0
	d.checkNonHookEdits()
	if d.lastEditDiffHash == "" {
		t.Fatal("scrape-road edit: the dirty tree was not detected")
	}
	trigger, err := os.ReadFile(bus.TriggerFile(session))
	if err != nil || !strings.Contains(string(trigger), "work.go") {
		t.Errorf("analyze trigger = %q (%v), want the changed file", trigger, err)
	}
}
