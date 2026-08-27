package bus

import (
	"os"
	"strings"
	"testing"
)

// captureTmux stubs the tmux runner and records every call's argv.
func captureTmux(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	orig := tmuxRunner
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	t.Cleanup(func() { tmuxRunner = orig })
	return &calls
}

// TestInjectPromptText_DashLeadingIntact pins the MUX-104 regression
// shape on the inject path: a dash-leading prompt must go through the
// `-l --` literal form (a bare or -l-only send-keys rejects it as an
// invalid flag), and Enter must arrive as its own separate call — text
// and Enter in one write is the dropped-Enter pitfall.
func TestInjectPromptText_DashLeadingIntact(t *testing.T) {
	SetBusDirBase(t.TempDir()) // no mode state → role falls back to the window
	defer ResetBusDirBase()
	calls := captureTmux(t)
	text := "- fix the flaky test, then rerun"

	role, err := InjectPromptText("s", "edit", text)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if role != "edit" {
		t.Errorf("role = %q, want edit (no mode state)", role)
	}

	var literal, enter []string
	for _, c := range *calls {
		if len(c) > 0 && c[0] == "send-keys" {
			switch {
			case argvContains(c, "-l"):
				literal = c
			case c[len(c)-1] == "Enter":
				enter = c
			}
		}
	}
	if literal == nil {
		t.Fatal("no literal send-keys call captured")
	}
	if !argvContains(literal, "--") {
		t.Errorf("literal call must use -- (MUX-104): %v", literal)
	}
	if literal[len(literal)-1] != text {
		t.Errorf("payload must arrive intact, got %q", literal[len(literal)-1])
	}
	if !argvContains(literal, "s:edit.1") {
		t.Errorf("literal call must target the window's agent pane: %v", literal)
	}
	if enter == nil {
		t.Error("Enter must be sent as its own call, never with the text")
	}
}

// TestInjectPromptText_ModeCycledWindowReportsActiveAgent pins the
// active-agent rule: with the edit window cycled to its second mode, the
// text is reported as delivered to that agent — and the pane target
// stays the HOST window's pane 1, because mode cycling swaps panes (the
// active agent is on screen there; the hold window holds the parked one).
func TestInjectPromptText_ModeCycledWindowReportsActiveAgent(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	session := "test-inject-mode"
	if err := os.MkdirAll(BusDir(session), 0755); err != nil {
		t.Fatal(err)
	}
	state := DefaultModeCycleState()
	state.Current = 1 // cycled: the auto agent is on screen in the edit window
	if err := WriteModeCycleState(session, state); err != nil {
		t.Fatalf("WriteModeCycleState: %v", err)
	}

	calls := captureTmux(t)
	role, err := InjectPromptText(session, "edit", "summarize the last build")
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if role != "auto" {
		t.Errorf("role = %q, want the ACTIVE agent auto, not the default edit", role)
	}
	for _, c := range *calls {
		for _, a := range c {
			if strings.Contains(a, ":auto.") {
				t.Errorf("target must stay the host window's pane (swap semantics), got %v", c)
			}
		}
	}
}

func TestInjectPromptText_EmptyRefused(t *testing.T) {
	calls := captureTmux(t)
	if _, err := InjectPromptText("s", "edit", ""); err == nil {
		t.Error("empty text must be refused")
	}
	if len(*calls) != 0 {
		t.Errorf("nothing may be sent for empty text: %v", *calls)
	}
}

// argvContains reports whether an argv slice carries an exact argument.
func argvContains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
