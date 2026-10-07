package tui

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

func upgradeCheckOf(installed, latest string, verdict bus.UpgradeVerdict) bus.UpgradeCheck {
	return bus.UpgradeCheck{Installed: bus.Info{Version: installed}, Latest: bus.Release{Tag: latest}, Verdict: verdict}
}

var upgradeTestTarget = upgradeTarget{
	BinDir:    "/home/u/.local/bin",
	ConfigDir: "/home/u/.config/muxcode",
	Sessions:  []string{"alpha", "beta"},
}

// assertUpgradeFrameFits fails when a line is wider than width or the frame
// is not shorter than height.
func assertUpgradeFrameFits(t *testing.T, frame string, width, height int) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	if len(lines) >= height {
		t.Errorf("frame is %d lines, want under the pane's %d:\n%s", len(lines), height, StripAnsi(frame))
	}
	for _, l := range lines {
		if w := VisibleWidth(l); w > width {
			t.Errorf("line is %d wide, pane is %d: %q", w, width, StripAnsi(l))
		}
	}
}

// upgradeStarts records the runs the modal started and what each was bound to.
type upgradeStarts struct {
	n         int
	confirmed bus.UpgradeConfirmation
}

// stubUpgradeSeams replaces what the modal reads and starts; checks are
// handed out in order, the last repeating.
func stubUpgradeSeams(t *testing.T, checks ...bus.UpgradeCheck) *upgradeStarts {
	t.Helper()
	check, target, start, alive := upgradeCheckFn, upgradeTargetFn, upgradeStartFn, upgradeAliveFn
	t.Cleanup(func() { upgradeCheckFn, upgradeTargetFn, upgradeStartFn, upgradeAliveFn = check, target, start, alive })
	next := 0
	upgradeCheckFn = func() (bus.UpgradeCheck, error) {
		c := checks[min(next, len(checks)-1)]
		next++
		return c, nil
	}
	upgradeTargetFn = func() (upgradeTarget, error) { return upgradeTestTarget, nil }
	starts := &upgradeStarts{}
	upgradeStartFn = func(_ bool, c bus.UpgradeConfirmation) (string, int, error) {
		starts.n++
		starts.confirmed = c
		return filepath.Join(t.TempDir(), "run.jsonl"), 4242, nil
	}
	return starts
}

// The confirm screen warns before ⏎ replaces an unreleased dev build — the
// 2026-10-07 upgrade that silently dropped the feature being tested. A
// release build is the negative control: no warning.
func TestRenderUpgradeConfirm_WarnsBeforeReplacingDevBuild(t *testing.T) {
	for _, c := range []struct {
		installed string
		warn      bool
	}{
		{"v0.1.20-16-gfeb4a13-dirty", true},
		{"v0.1.20", false},
	} {
		v := upgradeConfirmView{Reading: upgradeReading{Check: upgradeCheckOf(c.installed, "v0.1.21", bus.UpgradeNewer), Target: upgradeTestTarget}}
		frame := renderUpgradeConfirm(v, 200, 30)
		plain := StripAnsi(frame)
		if got := strings.Contains(plain, "⚠ installed "+c.installed+" is an unreleased dev build"); got != c.warn {
			t.Errorf("%s: dev-build warning shown = %v, want %v:\n%s", c.installed, got, c.warn, plain)
		}
		if !strings.Contains(plain, "⏎ Upgrade") {
			t.Errorf("%s: the warning must not remove ⏎ Upgrade:\n%s", c.installed, plain)
		}
		assertUpgradeFrameFits(t, frame, 200, 30)
	}
}

// A short pane must not trade the consequence for the warning: ⏎ stays live,
// so what it installs and restarts has to stay on screen beside the warning
// (review should-fix). The full-size frame above is the negative control —
// there the long warning is shown.
func TestRenderUpgradeConfirm_ShortPaneKeepsWarningAndConsequence(t *testing.T) {
	v := upgradeConfirmView{Reading: upgradeReading{Check: upgradeCheckOf("v0.1.20-16-gfeb4a13-dirty", "v0.1.21", bus.UpgradeNewer), Target: upgradeTestTarget}}
	frame := renderUpgradeConfirm(v, 80, 12)
	flat := strings.Join(strings.Fields(StripAnsi(frame)), " ")
	for _, want := range []string{"⚠ unreleased dev build", "/home/u/.local/bin", "/home/u/.config/muxcode", "restarts 2 daemons", "⏎ Upgrade"} {
		if !strings.Contains(flat, want) {
			t.Errorf("80x12 frame missing %q:\n%s", want, StripAnsi(frame))
		}
	}
	assertUpgradeFrameFits(t, frame, 80, 12)
}

// Nothing newer is an explicit state with the header and a footer naming only
// the keys it takes; a newer release is the half that states the full
// consequence and offers ⏎.
func TestRenderUpgradeConfirm_UpToDateAndAheadAreExplicit(t *testing.T) {
	for _, c := range []struct {
		name, installed, summary string
		verdict                  bus.UpgradeVerdict
	}{
		{"current", "v0.1.21", "installed v0.1.21 is current (latest v0.1.21)", bus.UpgradeCurrent},
		{"ahead", "v0.1.21-9-g7d339be", "installed v0.1.21-9-g7d339be is ahead of the latest release v0.1.21", bus.UpgradeAhead},
	} {
		v := upgradeConfirmView{Reading: upgradeReading{Check: upgradeCheckOf(c.installed, "v0.1.21", c.verdict), Target: upgradeTestTarget}}
		frame := StripAnsi(renderUpgradeConfirm(v, 200, 30))
		for _, want := range []string{"Check for Updates", "✓ " + c.summary, "f rebuilds and reinstalls v0.1.21", "f Force rebuild  q Quit"} {
			if !strings.Contains(frame, want) {
				t.Errorf("%s: frame missing %q:\n%s", c.name, want, frame)
			}
		}
		if strings.Contains(frame, "⏎ Upgrade") {
			t.Errorf("%s: ⏎ Upgrade offered with nothing newer:\n%s", c.name, frame)
		}
	}

	v := upgradeConfirmView{Reading: upgradeReading{Check: upgradeCheckOf("v0.1.20", "v0.1.21", bus.UpgradeNewer), Target: upgradeTestTarget}}
	frame := StripAnsi(renderUpgradeConfirm(v, 200, 30))
	for _, want := range []string{
		"↑ installed v0.1.20 → latest v0.1.21 available",
		"installed v0.1.20 → latest v0.1.21; rebuilds and installs to /home/u/.local/bin and /home/u/.config/muxcode, then restarts 2 daemons: alpha, beta",
		"⏎ Upgrade  f Force rebuild  q Quit",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("newer: frame missing %q:\n%s", want, frame)
		}
	}
}

// The failed step's cause — a sentence ending in a log path — wraps inside
// the pane at every width, losing none of its text, and still shows when a
// short pane folds the step list away.
func TestRenderUpgradeDone_FailureWrapsToNarrowWidth(t *testing.T) {
	cause := "make build failed (exit status 2): compile error: boom — log: /Users/someone/.cache/muxcode/upgrade/v0.1.21/build.log"
	v := upgradeProgressView{
		Steps: bus.SelfUpgradeStepNames(), Installed: "v0.1.20", Latest: "v0.1.21",
		Results: []bus.StepResult{
			{Name: "Check", Success: true, Note: "installed v0.1.20 → latest v0.1.21 available"},
			{Name: "Download", Success: true, Note: "1.2 MB, sha256 " + strings.Repeat("ab", 32)},
			{Name: "Build", Error: cause},
		},
		Err: "Build: " + cause,
	}
	for _, width := range []int{36, 50, 80} {
		for _, height := range []int{40, 12} {
			frame := renderUpgradeDone(v, width, height)
			assertUpgradeFrameFits(t, frame, width, height)
			plain := StripAnsi(frame)
			if !strings.Contains(plain, "✗ Build") {
				t.Errorf("%dx%d: failed step not shown:\n%s", width, height, plain)
			}
			if squeezed := strings.Join(strings.Fields(plain), ""); !strings.Contains(squeezed, strings.Join(strings.Fields(cause), "")) {
				t.Errorf("%dx%d: lost part of the cause or its log path:\n%s", width, height, plain)
			}
		}
		if full := StripAnsi(renderUpgradeDone(v, width, 40)); !strings.Contains(full, "✗ Failed at Build") || !strings.Contains(full, "○ Install") {
			t.Errorf("width %d at full height: want the step list and the outcome:\n%s", width, full)
		}
	}
}

func daemonsDoneView() upgradeProgressView {
	return upgradeProgressView{
		Steps: bus.SelfUpgradeStepNames(), Installed: "v0.1.20", Latest: "v0.1.21",
		Results: []bus.StepResult{
			{Name: "Check", Success: true}, {Name: "Download", Success: true}, {Name: "Build", Success: true},
			{Name: "Install", Success: true}, {Name: "Verify", Success: true},
			{Name: "Restart daemons", Success: true, Sub: []bus.StepResult{
				{Name: "alpha", Success: true, Note: "daemon v0.1.20 → installed v0.1.21 — daemon restarted"},
				{Name: "beta", Success: true, Note: "daemon v0.1.20 → installed v0.1.21 — daemon restarted"},
			}},
		},
	}
}

// At a comfortable size nothing degrades — the negative control. Shorter, the
// session sub-rows go first, then the steps fold to one status line, and the
// footer survives at every height.
func TestRenderUpgradeProgress_DegradesToHeight(t *testing.T) {
	v := daemonsDoneView()
	full := StripAnsi(renderUpgradeProgress(v, 80, 40))
	for _, want := range append(bus.SelfUpgradeStepNames(), "alpha", "beta", "q Close") {
		if !strings.Contains(full, want) {
			t.Errorf("comfortable frame missing %q:\n%s", want, full)
		}
	}

	compact := StripAnsi(renderUpgradeProgress(v, 80, 16))
	if strings.Contains(compact, "alpha") || !strings.Contains(compact, "Reload tmux config") {
		t.Errorf("at height 16 want the steps without session sub-rows:\n%s", compact)
	}

	folded := StripAnsi(renderUpgradeProgress(v, 80, 8))
	if !strings.Contains(folded, "6/7 done · ⟳ Reload tmux config") || strings.Contains(folded, "○") {
		t.Errorf("at height 8 want one status line naming the running step:\n%s", folded)
	}

	for _, height := range []int{40, 16, 8, 4} {
		frame := renderUpgradeProgress(v, 80, height)
		assertUpgradeFrameFits(t, frame, 80, height)
		if !strings.Contains(StripAnsi(frame), "q Close") {
			t.Errorf("height %d lost the footer:\n%s", height, StripAnsi(frame))
		}
	}
}

// Every state reads from its glyph alone; a finished run shows nothing still
// running.
func TestRenderUpgradeProgress_StatesReadableWithoutColour(t *testing.T) {
	running := upgradeProgressView{
		Steps: bus.SelfUpgradeStepNames(), Installed: "v0.1.20", Latest: "v0.1.21",
		Results: []bus.StepResult{{Name: "Check", Success: true}, {Name: "Download", Success: true}},
	}
	frame := StripAnsi(renderUpgradeProgress(running, 80, 40))
	for _, want := range []string{"✓ Check", "✓ Download", "⟳ Build", "○ Install", "○ Reload tmux config", "2/7"} {
		if !strings.Contains(frame, want) {
			t.Errorf("running frame missing %q:\n%s", want, frame)
		}
	}

	failed := running
	failed.Results = []bus.StepResult{{Name: "Check", Success: true}, {Name: "Download", Error: "HTTP 404"}}
	failed.Err = "Download: HTTP 404"
	frame = StripAnsi(renderUpgradeDone(failed, 80, 40))
	for _, want := range []string{"✓ Check", "✗ Download", "○ Build", "✗ Failed at Download"} {
		if !strings.Contains(frame, want) {
			t.Errorf("failed frame missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "⟳") {
		t.Errorf("a finished run shows a step running:\n%s", frame)
	}
}

// Confirming re-reads the release: a newer tag than the confirm showed starts
// nothing and says what changed. Confirming the refreshed screen is the half
// that starts the run, bound to exactly the release and sessions it showed,
// so a later change is the run's to refuse.
func TestUpgradeUI_RecheckRefusesAStaleConfirm(t *testing.T) {
	starts := stubUpgradeSeams(t,
		upgradeCheckOf("v0.1.20", "v0.1.21", bus.UpgradeNewer),
		upgradeCheckOf("v0.1.20", "v0.1.22", bus.UpgradeNewer))
	ui := NewUpgradeUI()
	ui.startCheck()
	(<-ui.updates)()

	ui.handleKey('\r')
	(<-ui.updates)()
	if starts.n != 0 || ui.phase != upgradeConfirm || !strings.Contains(ui.notice, "v0.1.21 → v0.1.22") {
		t.Fatalf("stale confirm: started %d phase %d notice %q, want nothing started and the change named", starts.n, ui.phase, ui.notice)
	}

	ui.handleKey('\r')
	(<-ui.updates)()
	if starts.n != 1 || ui.phase != upgradeRunning || ui.force {
		t.Fatalf("confirming the refreshed screen: started %d phase %d force %v, want one unforced run", starts.n, ui.phase, ui.force)
	}
	if c := starts.confirmed; c.Tag != "v0.1.22" || !reflect.DeepEqual(c.Sessions, upgradeTestTarget.Sessions) {
		t.Errorf("run bound to %+v, want v0.1.22 and the confirmed sessions %v", c, upgradeTestTarget.Sessions)
	}
}

// Keys do what the footer says and nothing else: ⏎ is inert with nothing
// newer, f forces, and f is inert when a build tool is missing.
func TestUpgradeUI_KeysFollowTheFooter(t *testing.T) {
	starts := stubUpgradeSeams(t, upgradeCheckOf("v0.1.21", "v0.1.21", bus.UpgradeCurrent))
	ui := NewUpgradeUI()
	ui.startCheck()
	(<-ui.updates)()

	ui.handleKey('\r')
	if ui.phase != upgradeConfirm || starts.n != 0 {
		t.Fatalf("⏎ with nothing newer moved to phase %d, started %d", ui.phase, starts.n)
	}
	ui.handleKey('f')
	(<-ui.updates)()
	if starts.n != 1 || ui.phase != upgradeRunning || !ui.force {
		t.Errorf("f: started %d phase %d force %v, want one forced run", starts.n, ui.phase, ui.force)
	}

	missing := upgradeCheckOf("v0.1.20", "v0.1.21", bus.UpgradeNewer)
	missing.MissingTools = []string{"go"}
	blocked := &UpgradeUI{phase: upgradeConfirm, reading: upgradeReading{Check: missing, Target: upgradeTestTarget}, updates: make(chan func(), 1)}
	blocked.handleKey('f')
	blocked.handleKey('\r')
	if blocked.phase != upgradeConfirm {
		t.Errorf("a missing build tool still let a key start a run (phase %d)", blocked.phase)
	}
	if footer := StripAnsi(renderUpgradeConfirm(upgradeConfirmView{Reading: blocked.reading}, 200, 30)); strings.Contains(footer, "Force rebuild") {
		t.Errorf("footer offers f with a build tool missing:\n%s", footer)
	}
}

// The modal follows the run through its events file, and a run whose process
// dies without writing its end is finished as failed, never waited on.
func TestUpgradeUI_PollFollowsTheEventsFile(t *testing.T) {
	stubUpgradeSeams(t, upgradeCheckOf("v0.1.20", "v0.1.21", bus.UpgradeNewer))
	alive := true
	upgradeAliveFn = func(int) bool { return alive }
	path := filepath.Join(t.TempDir(), "run.jsonl")
	ui := &UpgradeUI{phase: upgradeRunning, eventsPath: path, pid: 4242}

	if err := bus.AppendUpgradeEvent(path, bus.UpgradeEvent{Index: 0, Step: &bus.StepResult{Name: "Check", Success: true}}); err != nil {
		t.Fatal(err)
	}
	if !ui.poll() || len(ui.results) != 1 || ui.phase != upgradeRunning {
		t.Fatalf("after one step: results %d phase %d", len(ui.results), ui.phase)
	}
	if ui.poll() {
		t.Error("a poll with nothing new reported a change")
	}
	if err := bus.AppendUpgradeEvent(path, bus.UpgradeEvent{Index: 1, Done: true, Summary: "upgraded v0.1.20 → v0.1.21"}); err != nil {
		t.Fatal(err)
	}
	if !ui.poll() || ui.phase != upgradeDone || ui.summary != "upgraded v0.1.20 → v0.1.21" {
		t.Errorf("after the end event: phase %d summary %q", ui.phase, ui.summary)
	}

	alive = false
	dead := &UpgradeUI{phase: upgradeRunning, eventsPath: filepath.Join(t.TempDir(), "run.jsonl"), pid: 4243}
	if !dead.poll() || dead.phase != upgradeDone || !strings.Contains(dead.finalErr, "exited without reporting") {
		t.Errorf("dead run: phase %d err %q, want it finished as failed", dead.phase, dead.finalErr)
	}
}
