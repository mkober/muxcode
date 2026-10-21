package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// sandboxWatch builds a daemon whose agent-defs check sees every agent alive,
// reads want as the installed binary's sandbox fingerprints, and judges reloads
// safe per *safe. build launched as a Codex agent and stamped its sandbox.
// Reloads land on the returned channel instead of tmux.
func sandboxWatch(t *testing.T, want map[string]string, safe *bool) (*Daemon, chan string) {
	t.Helper()
	bus.SetBusDirBase(t.TempDir())
	t.Cleanup(bus.ResetBusDirBase)
	useEmptyShellConfig(t)
	t.Setenv("MUXCODE_AGENTDEFS_WATCH_DISABLE", "")
	session := testSession(t)
	t.Setenv("BUS_SESSION", session)
	t.Setenv("MUXCODE_BUILD_CLI", "codex")
	bus.StampLaunchSandbox(session, "build")

	reloads := make(chan string, 16)
	d := New(session, 5, 8)
	d.agentAlive = func(_, _ string) bool { return true }
	d.reloadSafe = func(_, _ string) (bool, string) { return *safe, "test" }
	d.sandboxPrints = func(string, []string) (map[string]string, error) { return want, nil }
	d.reloadAgent = func(_, role string) error { reloads <- role; return nil }
	return d, reloads
}

func tickAgentDefs(d *Daemon) {
	d.lastAgentDefsCheck = 0
	d.checkAgentDefs()
}

// expectReloads waits for the reloads issued by the last tick: exactly roles,
// in any order, and nothing more.
func expectReloads(t *testing.T, reloads chan string, roles ...string) {
	t.Helper()
	want := map[string]bool{}
	for _, r := range roles {
		want[r] = true
	}
	deadline := time.After(2 * time.Second)
	for len(want) > 0 {
		select {
		case r := <-reloads:
			if !want[r] {
				t.Fatalf("unexpected reload of %s", r)
			}
			delete(want, r)
		case <-deadline:
			t.Fatalf("reloads never issued: %v", want)
		}
	}
	select {
	case r := <-reloads:
		t.Fatalf("unexpected reload of %s", r)
	case <-time.After(100 * time.Millisecond):
	}
}

// 2026-10-16: is-admissions-gateway's build agent ran three days on a sandbox
// without network after the install that granted it. The stub reload lands no
// new stamp, so the agent stays stale; the next check must wait the cooldown.
func TestCheckAgentDefs_StaleSandboxReloadWaitsCooldown(t *testing.T) {
	safe := true
	d, reloads := sandboxWatch(t, map[string]string{"build": "installed-now"}, &safe)
	tickAgentDefs(d)
	expectReloads(t, reloads, "build")
	tickAgentDefs(d)
	expectReloads(t, reloads)
}

// A reload that never lands the new sandbox is retried, then given up on at the
// cap rather than looping.
func TestCheckAgentDefs_StaleSandboxRetriesToCap(t *testing.T) {
	safe := true
	d, reloads := sandboxWatch(t, map[string]string{"build": "installed-now"}, &safe)
	d.reloadCooldownSecs = 0
	for i := 0; i < autoReloadCap; i++ {
		tickAgentDefs(d)
		expectReloads(t, reloads, "build")
	}
	tickAgentDefs(d)
	expectReloads(t, reloads)
}

// changeReviewDefinition launches review as a Codex agent from a project
// definition, stamping both, then changes the definition on disk.
func changeReviewDefinition(t *testing.T, d *Daemon) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	def := filepath.Join(".claude", "agents", bus.AgentFileName("review")+".md")
	if err := os.MkdirAll(filepath.Dir(def), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDef := func(body string) {
		if err := os.WriteFile(def, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeDef("launched with")
	t.Setenv("MUXCODE_REVIEW_CLI", "codex")
	bus.StampAgentDefHash(d.session, "review")
	bus.StampLaunchSandbox(d.session, "review")
	writeDef("installed since")
}

// An install that changes both review's definition and its Codex sandbox, with
// a reload that lands neither stamp: the changed definition must not slip past
// the budget the sandbox reload spent — no reload within the cooldown, a retry
// after it, none past the cap.
func TestCheckAgentDefs_DefinitionChangeSharesReloadBudget(t *testing.T) {
	safe := true
	d, reloads := sandboxWatch(t, map[string]string{"review": "installed-now"}, &safe)
	changeReviewDefinition(t, d)

	tickAgentDefs(d)
	expectReloads(t, reloads, "review")
	tickAgentDefs(d)
	expectReloads(t, reloads)

	d.reloadCooldownSecs = 0
	for i := 1; i < autoReloadCap; i++ {
		tickAgentDefs(d)
		expectReloads(t, reloads, "review")
	}
	tickAgentDefs(d)
	expectReloads(t, reloads)
}

// A probe that fails, or is backing off after a failure, says nothing about the
// install, so review's reload target and the budget spent on it survive both it
// and the recovery: no reload within the cooldown, none past the cap, however
// the probe alternates.
func TestCheckAgentDefs_UnavailableProbeKeepsReloadBudget(t *testing.T) {
	safe := true
	d, reloads := sandboxWatch(t, nil, &safe)
	changeReviewDefinition(t, d)
	answered := map[string]string{"review": "installed-now"}
	failed := errors.New("probe failed")
	probe := func(fps map[string]string, err error) {
		d.sandboxPrints = func(string, []string) (map[string]string, error) { return fps, err }
	}

	probe(answered, nil)
	tickAgentDefs(d)
	expectReloads(t, reloads, "review")
	probe(nil, failed)
	tickAgentDefs(d)
	expectReloads(t, reloads)
	probe(nil, nil)
	tickAgentDefs(d)
	expectReloads(t, reloads)
	probe(answered, nil)
	tickAgentDefs(d)
	expectReloads(t, reloads)

	d.reloadCooldownSecs = 0
	for i := 1; i < autoReloadCap; i++ {
		if i%2 == 1 {
			probe(nil, failed)
		} else {
			probe(answered, nil)
		}
		tickAgentDefs(d)
		expectReloads(t, reloads, "review")
	}
	probe(nil, failed)
	tickAgentDefs(d)
	expectReloads(t, reloads)
	probe(answered, nil)
	tickAgentDefs(d)
	expectReloads(t, reloads)
}

// Before any probe answers, review's changed definition is reloaded toward an
// unknown sandbox. The answer arriving names no new install, so the budget
// survives it: no reload within the cooldown, none past the cap. A definition
// changed again is a new install, reloaded at once.
func TestCheckAgentDefs_LearnedSandboxKeepsReloadBudget(t *testing.T) {
	safe := true
	d, reloads := sandboxWatch(t, nil, &safe)
	changeReviewDefinition(t, d)

	tickAgentDefs(d)
	expectReloads(t, reloads, "review")
	d.sandboxPrints = func(string, []string) (map[string]string, error) {
		return map[string]string{"review": "installed-now"}, nil
	}
	tickAgentDefs(d)
	expectReloads(t, reloads)

	d.reloadCooldownSecs = 0
	for i := 1; i < autoReloadCap; i++ {
		tickAgentDefs(d)
		expectReloads(t, reloads, "review")
	}
	tickAgentDefs(d)
	expectReloads(t, reloads)

	def := filepath.Join(".claude", "agents", bus.AgentFileName("review")+".md")
	if err := os.WriteFile(def, []byte("installed again"), 0o644); err != nil {
		t.Fatal(err)
	}
	tickAgentDefs(d)
	expectReloads(t, reloads, "review")
}

// hungInstalledMuxcode puts a muxcode on PATH that records each run in calls,
// then hangs until release exists (10 s at most, so no run outlives the test)
// and reports build's sandbox as "installed-now".
func hungInstalledMuxcode(t *testing.T) (calls, release string) {
	t.Helper()
	dir := t.TempDir()
	calls, release = filepath.Join(dir, "calls"), filepath.Join(dir, "release")
	script := "#!/bin/sh\necho >> '" + calls + "'\ni=0\n" +
		"while [ ! -f '" + release + "' ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done\n" +
		"printf 'build\\tinstalled-now\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "muxcode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	return calls, release
}

func probeRuns(calls string) int {
	data, _ := os.ReadFile(calls)
	return strings.Count(string(data), "\n")
}

// The daemon's own probe spawns the installed binary under a 30 s timeout —
// the keepalive age at which `muxcode watch --monitor` relaunches a daemon — so
// the check must never wait on it. While the binary hangs, checks return at
// once and start no second run; its answer reaches a later check, which
// reloads build.
func TestCheckAgentDefs_HungInstalledBinaryNeverBlocksTheCheck(t *testing.T) {
	safe := true
	d, reloads := sandboxWatch(t, nil, &safe)
	calls, release := hungInstalledMuxcode(t)
	d.sandboxPrints = New(d.session, 5, 8).sandboxPrints
	releaseBinary := func() {
		if err := os.WriteFile(release, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	checked := make(chan struct{})
	go func() {
		for i := 0; i < 3; i++ {
			tickAgentDefs(d)
		}
		close(checked)
	}()
	select {
	case <-checked:
	case <-time.After(2 * time.Second):
		releaseBinary()
		t.Fatal("the agent-defs check waited on the installed binary")
	}
	expectReloads(t, reloads)
	for deadline := time.Now().Add(2 * time.Second); probeRuns(calls) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			releaseBinary()
			t.Fatal("the installed binary was never probed")
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := probeRuns(calls); n != 1 {
		releaseBinary()
		t.Fatalf("installed binary probed %d times while one probe hung, want 1", n)
	}

	releaseBinary()
	deadline := time.After(5 * time.Second)
	for {
		tickAgentDefs(d)
		select {
		case r := <-reloads:
			if r != "build" {
				t.Fatalf("unexpected reload of %s", r)
			}
			return
		case <-deadline:
			t.Fatal("the installed binary's answer never reached the check")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestCheckAgentDefs_CurrentSandboxNotReloaded(t *testing.T) {
	safe := true
	d, reloads := sandboxWatch(t, nil, &safe)
	d.sandboxPrints = func(string, []string) (map[string]string, error) {
		return map[string]string{"build": bus.ReadLaunchSandbox(d.session, "build")}, nil
	}
	tickAgentDefs(d)
	expectReloads(t, reloads)
}

// A launch by a binary that predates stamping left no stamp: its sandbox is
// unknown, so it is reloaded. A role the installed binary gives no sandbox is
// never reloaded for one.
func TestCheckAgentDefs_UnstampedCodexReloadedNonCodexSkipped(t *testing.T) {
	safe := true
	d, reloads := sandboxWatch(t, map[string]string{"test": "installed-now", "review": ""}, &safe)
	tickAgentDefs(d)
	expectReloads(t, reloads, "test")
}

// A busy agent's reload waits for the next safe check rather than being dropped.
func TestCheckAgentDefs_UnsafeDefersSandboxReload(t *testing.T) {
	safe := false
	d, reloads := sandboxWatch(t, map[string]string{"build": "installed-now"}, &safe)
	tickAgentDefs(d)
	expectReloads(t, reloads)
	safe = true
	tickAgentDefs(d)
	expectReloads(t, reloads, "build")
}
