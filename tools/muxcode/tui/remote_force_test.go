package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

func forceRespondTestUI(t *testing.T) *RemoteUI {
	t.Helper()
	ui := NewRemoteUI("local")
	ui.view = viewSessionDetail
	ui.detailSession = "remote-force-test"
	ui.agents = []bus.AgentStatus{{Role: "build"}, {Role: "test"}}
	ui.agentIdx = 0
	t.Cleanup(func() { os.RemoveAll(bus.BusDir("remote-force-test")) })
	return ui
}

// f parks the action behind the confirm; nothing is selected until y.
func TestRemoteForceRespond_GatedOnConfirm(t *testing.T) {
	ui := forceRespondTestUI(t)

	if got := ui.handleKey('f'); got != "" {
		t.Fatalf("f must only park the confirm, got %q", got)
	}
	if ui.confirmPending == nil || ui.confirmPending.Action != ActionForceRespond {
		t.Fatalf("expected a parked force-respond, got %+v", ui.confirmPending)
	}
	if ui.confirmPending.Role != "build" || ui.confirmPending.Session != "remote-force-test" {
		t.Errorf("confirm must target the selected agent, got %+v", ui.confirmPending)
	}

	if got := ui.handleKey('n'); got != "" || ui.confirmPending != nil {
		t.Fatalf("n must abandon the confirm, got %q pending %+v", got, ui.confirmPending)
	}
	if ui.result != nil {
		t.Fatal("an abandoned confirm must not produce a selection")
	}

	ui.handleKey('f')
	if got := ui.handleKey('y'); got != "selected" {
		t.Fatalf("y must select the parked action, got %q", got)
	}
	if ui.result == nil || ui.result.Action != ActionForceRespond || ui.result.Role != "build" {
		t.Errorf("expected a force-respond selection for build, got %+v", ui.result)
	}
}

// An escape SEQUENCE (arrow key, Shift-Tab) must not cancel the
// confirm — only a bare Escape does (PR #38 Copilot finding).
func TestRemoteForceRespond_ArrowKeysDoNotCancelConfirm(t *testing.T) {
	ui := forceRespondTestUI(t)
	ui.keyCh = make(chan byte, 4)
	ui.handleKey('f')

	ui.keyCh <- '['
	ui.keyCh <- 'A' // arrow-up sequence tail
	if got := ui.handleKey(27); got != "" || ui.confirmPending == nil {
		t.Fatalf("an arrow sequence must not cancel the confirm, got %q pending %v", got, ui.confirmPending)
	}
	if len(ui.keyCh) != 0 {
		t.Errorf("sequence bytes must be swallowed, %d left queued", len(ui.keyCh))
	}

	// Bare Escape (no queued sequence bytes) cancels after the timeout.
	if got := ui.handleKey(27); got != "" || ui.confirmPending != nil {
		t.Errorf("bare Escape must cancel, got %q pending %v", got, ui.confirmPending)
	}
}

// While the confirm is up, other action keys must not fire.
func TestRemoteForceRespond_ConfirmSwallowsOtherKeys(t *testing.T) {
	ui := forceRespondTestUI(t)
	ui.handleKey('f')

	for _, k := range []byte{'c', 'i', 'd', 'j', 'k'} {
		if got := ui.handleKey(k); got != "" {
			t.Errorf("key %q must be inert during confirm, got %q", k, got)
		}
	}
	if ui.confirmPending == nil {
		t.Error("confirm must still be pending after inert keys")
	}
}

// The confirm frame shows the daemon ladder's history when an episode is
// open, and says so explicitly when none is.
func TestRemoteForceRespond_ConfirmShowsEscalationHistory(t *testing.T) {
	ui := forceRespondTestUI(t)
	ui.handleKey('f')

	frame := StripAnsi(ui.render())
	if !strings.Contains(frame, "Force-respond build") {
		t.Fatalf("confirm frame missing target:\n%s", frame)
	}
	if !strings.Contains(frame, "manual recovery") {
		t.Errorf("with no episode the frame must say it is manual:\n%s", frame)
	}

	err := bus.WriteForceRespondState("remote-force-test", bus.ForceRespondState{
		Role: "build", Rung: 2,
		History: []string{"force-respond-notify@11:00:00", "force-respond-deliver@11:01:00"},
	})
	if err != nil {
		t.Fatalf("WriteForceRespondState: %v", err)
	}

	frame = StripAnsi(ui.render())
	for _, want := range []string{"already tried", "force-respond-notify@11:00:00", "force-respond-deliver@11:01:00"} {
		if !strings.Contains(frame, want) {
			t.Errorf("confirm frame missing %q:\n%s", want, frame)
		}
	}
}
