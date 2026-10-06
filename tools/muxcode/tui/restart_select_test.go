package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

var restartCounts = []bus.RestartProviderCount{
	{CLI: "claude", Total: 5, Down: 2},
	{CLI: "opencode", Total: 3},
}

// The provider list shows each provider's live count and down count, plus an
// "all" row summing them.
func TestRestartSelect_ListsProviderCounts(t *testing.T) {
	ui := newRestartSelectUI("s", restartCounts, nil)
	frame := StripAnsi(ui.render(80))

	for _, want := range []string{"Restart Agents", "claude", "5 agents (2 down)", "opencode", "3 agents", "all", "8 agents (2 down)", "⏎ Restart"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "3 agents (") {
		t.Errorf("opencode with nothing down shows a down count:\n%s", frame)
	}
}

// The confirm names only the roads the chosen filter takes: Claude resume
// and edit's resume-only rule for claude, the fresh same-provider reload for
// opencode — never each other's.
func TestRestartSelect_ConfirmStatesConsequences(t *testing.T) {
	cases := []struct {
		cursor        int
		want, notWant []string
	}{
		{0, []string{"Restart 5 claude agents", "resume their last session", "resume-only", "Dead agents are included"}, []string{"Other providers"}},
		{1, []string{"Restart 3 opencode agents", "Other providers relaunch fresh on the same provider"}, []string{"resume their last session", "resume-only"}},
		{2, []string{"resume their last session", "Other providers"}, nil},
	}
	for _, c := range cases {
		ui := newRestartSelectUI("s", restartCounts, nil)
		ui.cursor = c.cursor
		ui.handleKey(13)
		frame := StripAnsi(ui.render(80))
		for _, w := range c.want {
			if !strings.Contains(frame, w) {
				t.Errorf("cursor %d: confirm missing %q:\n%s", c.cursor, w, frame)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(frame, w) {
				t.Errorf("cursor %d: confirm wrongly states %q:\n%s", c.cursor, w, frame)
			}
		}
	}
}

// Any key but y backs out of the confirm without starting anything.
func TestRestartSelect_ConfirmBacksOut(t *testing.T) {
	ui := newRestartSelectUI("s", restartCounts, nil)
	ui.handleKey(13)
	ui.handleKey('n')
	if ui.confirming || ui.inProgress {
		t.Fatalf("after n: confirming=%v inProgress=%v", ui.confirming, ui.inProgress)
	}
}

// Empty and failed reads keep the header and a way out, never a blank body.
func TestRestartSelect_EmptyAndErrorStates(t *testing.T) {
	empty := StripAnsi(newRestartSelectUI("s", nil, nil).render(80))
	if !strings.Contains(empty, "No agents with a window") || !strings.Contains(empty, "q Quit") {
		t.Errorf("empty state:\n%s", empty)
	}
	failed := StripAnsi(newRestartSelectUI("s", nil, errors.New("no server running")).render(80))
	if !strings.Contains(failed, "no server running") || !strings.Contains(failed, "Restart Agents") {
		t.Errorf("error state:\n%s", failed)
	}
}

// A single provider gets no redundant "all" row.
func TestRestartSelect_SingleProviderHasNoAllRow(t *testing.T) {
	ui := newRestartSelectUI("s", restartCounts[:1], nil)
	if len(ui.options) != 1 {
		t.Fatalf("options = %+v, want claude only", ui.options)
	}
}

// The shared progress body reports how each restart came back, and a config
// reload row still reads as before.
func TestRenderBatchProgress_RestartAndReloadRows(t *testing.T) {
	results := []bus.ReloadResult{
		{Role: "plan", Success: true, Restarted: true, ResumedID: "8a744341-11bf-440f-b5d2-49248447a9c0", NewCLI: "claude", Duration: time.Second},
		{Role: "build", Success: true, Restarted: true, NewCLI: "opencode"},
		{Role: "edit", Error: bus.ErrResumeUnavailable},
	}
	frame := StripAnsi(renderBatchProgress([]string{"plan", "build", "edit", "test", "review"}, results, 5, false, "restart", "pending footer", 80))
	for _, want := range []string{"resumed 8a744341", "relaunched fresh (opencode)", "no resumable session", "⟳ test", "○ review", "3/5", "pending footer"} {
		if !strings.Contains(frame, want) {
			t.Errorf("restart frame missing %q:\n%s", want, frame)
		}
	}

	reload := StripAnsi(renderBatchProgress([]string{"test"},
		[]bus.ReloadResult{{Role: "test", Success: true, OldCLI: "claude", NewCLI: "claude"}}, 1, true, "reload", "pending footer", 80))
	if !strings.Contains(reload, "(no change)") || !strings.Contains(reload, "All agents reloaded successfully") {
		t.Errorf("reload frame:\n%s", reload)
	}
	if strings.Contains(reload, "resumed") || strings.Contains(reload, "relaunched") {
		t.Errorf("a config reload row reads as a restart:\n%s", reload)
	}
}

// Closing while the batch runs is deferred, never an abort: q and a signal
// (requestClose — what Run does on SIGHUP when the popup is torn down) leave
// the modal open, the batch runs every agent to the end, and the modal closes
// once it is done. The footer says so rather than promising a background run.
func TestRestartSelect_CloseDuringProgressWaitsForBatch(t *testing.T) {
	targets, roles := restartSelectTargets, restartSelectRoles
	t.Cleanup(func() { restartSelectTargets, restartSelectRoles = targets, roles })
	restartSelectTargets = func(string, string) ([]string, error) { return []string{"plan", "test"}, nil }
	release := make(chan struct{})
	finished := make(chan []string, 1)
	restartSelectRoles = func(_ string, rs []string, progress bus.ReloadProgress) []bus.ReloadResult {
		var done []string
		for i, r := range rs {
			if i == 1 {
				<-release // hold the batch mid-flight, one agent done
			}
			done = append(done, r)
			progress(i, bus.ReloadResult{Role: r, Success: true, Restarted: true})
		}
		finished <- done
		return nil
	}

	ui := newRestartSelectUI("s", restartCounts, nil)
	ui.handleKey(13)
	ui.handleKey('y')
	if !ui.inProgress {
		t.Fatal("confirm did not start the batch")
	}

	if ui.handleKey('q') == "close" || ui.requestClose() {
		t.Fatal("the modal closed mid-batch — the batch would be killed with the process")
	}
	if ui.closeDue() {
		t.Fatal("close due before the batch finished")
	}
	if frame := StripAnsi(ui.render(80)); !strings.Contains(frame, "Closing as soon as the restart finishes") || strings.Contains(frame, "continues in background") {
		t.Errorf("footer after a deferred close:\n%s", frame)
	}

	close(release)
	if got := <-finished; strings.Join(got, ",") != "plan,test" {
		t.Fatalf("batch processed %v, want every agent", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !ui.closeDue() {
		if time.Now().After(deadline) {
			t.Fatal("the deferred close never came due after the batch finished")
		}
		time.Sleep(time.Millisecond)
	}
}

// Negative control: with nothing running, or once the batch is done, a close
// is immediate.
func TestRestartSelect_CloseImmediateWhenIdleOrDone(t *testing.T) {
	ui := newRestartSelectUI("s", restartCounts, nil)
	if ui.handleKey('q') != "close" {
		t.Error("q on the provider list did not close")
	}
	ui.inProgress, ui.done = true, true
	if ui.handleKey('q') != "close" || ui.handleKey(13) != "close" {
		t.Error("q or Enter after the batch finished did not close")
	}
}
