package bus

import (
	"fmt"
	"testing"
)

// MUX-163 Phase 2. Every muxcode site that sends Escape ahead of a payload
// or an Enter routes through TmuxDismissOverlay, so a pending ESC never fuses
// with the payload's first byte into a Meta chord. These tests pin the typed
// send-keys sequence each site produces, not just the payload string: the
// defect lived in the ADJACENCY of Escape to the next key, which a payload-only
// assertion cannot see. The shape rule is escapeAbsorbViolation, and its
// negative control is the pre-fix inject sequence, which it must reject.

// escapeAbsorbViolation returns a non-empty description when a `send-keys …
// Escape` call is directly followed by a literal (-l) or Enter send-keys call
// — the unabsorbed adjacency the fusion needs. Non-key tmux calls (capture-pane
// etc.) are filtered out: the question is what KEY is typed after the Escape.
func escapeAbsorbViolation(calls [][]string) string {
	var sends [][]string
	for _, c := range calls {
		if len(c) > 0 && c[0] == "send-keys" {
			sends = append(sends, c)
		}
	}
	for i, c := range sends {
		if c[len(c)-1] != "Escape" {
			continue
		}
		if i+1 >= len(sends) {
			continue
		}
		next := sends[i+1]
		switch {
		case argvContains(next, "-l"):
			return fmt.Sprintf("Escape directly followed by a literal payload: %v -> %v", c, next)
		case next[len(next)-1] == "Enter":
			return fmt.Sprintf("Escape directly followed by Enter: %v -> %v", c, next)
		}
	}
	return ""
}

// assertEscapeAbsorbed fails the test when the captured send-keys sequence has
// an unabsorbed Escape (see escapeAbsorbViolation).
func assertEscapeAbsorbed(t *testing.T, calls [][]string) {
	t.Helper()
	if v := escapeAbsorbViolation(calls); v != "" {
		t.Errorf("escape not absorbed: %s", v)
	}
}

// TestTmuxDismissOverlay_ArgvShape pins the preamble to two separate writes,
// Escape then C-e — never one `send-keys Escape C-e` (which would put ESC^E in
// one chunk) and never Escape adjacent to a payload.
func TestTmuxDismissOverlay_ArgvShape(t *testing.T) {
	calls := stubTmuxRunner(t)

	if err := TmuxDismissOverlay("s:edit.1"); err != nil {
		t.Fatalf("TmuxDismissOverlay: %v", err)
	}

	sends := *calls
	if len(sends) != 2 {
		t.Fatalf("expected exactly 2 send-keys calls (Escape, C-e), got %d: %v", len(sends), sends)
	}
	esc := []string{"send-keys", "-t", "s:edit.1", "Escape"}
	ce := []string{"send-keys", "-t", "s:edit.1", "C-e"}
	if fmt.Sprint(sends[0]) != fmt.Sprint(esc) {
		t.Errorf("first call = %v, want %v", sends[0], esc)
	}
	if fmt.Sprint(sends[1]) != fmt.Sprint(ce) {
		t.Errorf("second call = %v, want %v", sends[1], ce)
	}
	assertEscapeAbsorbed(t, sends)
}

// TestEscapeAbsorbViolation_NegativeControl is the mandated negative control:
// the pre-fix InjectPromptText sequence — Escape straight into the literal,
// then Enter — must be REJECTED by the shape rule. A check that cannot fail on
// the defect proves nothing.
func TestEscapeAbsorbViolation_NegativeControl(t *testing.T) {
	preFix := [][]string{
		{"send-keys", "-t", "s:edit.1", "Escape"},
		{"send-keys", "-t", "s:edit.1", "-l", "--", "- dash payload"},
		{"send-keys", "-t", "s:edit.1", "Enter"},
	}
	if escapeAbsorbViolation(preFix) == "" {
		t.Fatal("the pre-fix Escape→literal sequence must be flagged, but was not")
	}

	// And the fixed shape must pass, so the rule is not simply always-true.
	fixed := [][]string{
		{"send-keys", "-t", "s:edit.1", "Escape"},
		{"send-keys", "-t", "s:edit.1", "C-e"},
		{"send-keys", "-t", "s:edit.1", "-l", "--", "- dash payload"},
		{"send-keys", "-t", "s:edit.1", "Enter"},
	}
	if v := escapeAbsorbViolation(fixed); v != "" {
		t.Errorf("the absorbed sequence must pass, got violation: %s", v)
	}

	// Escape directly before Enter (the pre-fix re-submit shape) is caught too.
	preFixResubmit := [][]string{
		{"send-keys", "-t", "s:edit.1", "Escape"},
		{"send-keys", "-t", "s:edit.1", "Enter"},
	}
	if escapeAbsorbViolation(preFixResubmit) == "" {
		t.Fatal("the pre-fix Escape→Enter re-submit must be flagged, but was not")
	}
}

// TestEscapeAbsorbed_InjectPromptText: the Prompt-surface inject path routes
// through the preamble, and the existing MUX-104 dash-leading pin still holds.
func TestEscapeAbsorbed_InjectPromptText(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	calls := captureTmux(t)

	if _, err := InjectPromptText("s", "edit", "- dash payload"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	assertEscapeAbsorbed(t, *calls)

	// The literal payload must still be present and preceded by -- (MUX-104).
	var sawLiteral bool
	for _, c := range *calls {
		if len(c) >= 2 && c[0] == "send-keys" && argvContains(c, "-l") {
			sawLiteral = true
			if c[len(c)-1] != "- dash payload" {
				t.Errorf("payload mangled: %q", c[len(c)-1])
			}
		}
	}
	if !sawLiteral {
		t.Error("no literal send-keys call captured")
	}
}

// TestInjectPromptText_PreambleErrorPropagates: a runner error on the Escape
// preamble must surface from InjectPromptText — the void helper used to swallow
// it (Phase 2 review should-fix). The literal payload is never reached.
func TestInjectPromptText_PreambleErrorPropagates(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()

	var literalSent bool
	orig := tmuxRunner
	tmuxRunner = func(args ...string) error {
		if len(args) > 0 && args[0] == "send-keys" {
			if argvContains(args, "Escape") {
				return fmt.Errorf("no server running")
			}
			if argvContains(args, "-l") {
				literalSent = true
			}
		}
		return nil
	}
	t.Cleanup(func() { tmuxRunner = orig })

	if _, err := InjectPromptText("s", "edit", "payload"); err == nil {
		t.Fatal("a preamble send-keys error must propagate from InjectPromptText")
	}
	if literalSent {
		t.Error("the literal payload must not be sent after a preamble failure")
	}
}

// TestEscapeAbsorbed_ResubmitEnter covers the shared re-submit helper that both
// verifyEnterDelivery and the daemon parked-input watchdog (daemon.go:2522)
// call, so a regression to an adjacent Escape→Enter at either site is caught
// here — the daemon branch has no send sequence of its own to test.
func TestEscapeAbsorbed_ResubmitEnter(t *testing.T) {
	calls := stubTmuxRunner(t)

	TmuxResubmitEnter("s:edit.1")

	assertEscapeAbsorbed(t, *calls)
	sends := *calls
	if len(sends) == 0 || sends[len(sends)-1][len(sends[len(sends)-1])-1] != "Enter" {
		t.Errorf("re-submit must end with Enter, got %v", sends)
	}
	var sawAbsorber bool
	for _, c := range sends {
		if argvContains(c, "C-e") {
			sawAbsorber = true
		}
	}
	if !sawAbsorber {
		t.Error("re-submit must send the C-e absorber between Escape and Enter")
	}
}

// TestEscapeAbsorbed_SendWakeUpWithText: the Claude wake-up path (the safe
// shape the defect was measured against) stays absorbed after the refactor.
func TestEscapeAbsorbed_SendWakeUpWithText(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	// An empty live composer under a non-shell status line: captureInjectionTarget
	// proceeds, and verifyEnterDelivery sees no parked text and returns at once.
	calls := stubInjectionPane(t, "❯ \n▶▶ bypass permissions on (shift+tab to cycle)", nil)

	if err := SendWakeUpWithText("s", "edit", &ClaudeCodeProvider{}, "wake up", false); err != nil {
		t.Fatalf("SendWakeUpWithText: %v", err)
	}
	assertEscapeAbsorbed(t, *calls)
}

// TestEscapeAbsorbed_VerifyEnterDelivery: the dropped-Enter re-submit dismisses
// with the absorber before Enter, so the Enter is never fused into Meta-Enter.
func TestEscapeAbsorbed_VerifyEnterDelivery(t *testing.T) {
	// Parked text keeps composerHasText true, so the re-submit branch fires.
	calls := stubInjectionPane(t, "❯ still parked here", nil)

	verifyEnterDelivery("s:edit.1")
	assertEscapeAbsorbed(t, *calls)

	var sawAbsorber, sawEnter bool
	for _, c := range *calls {
		if len(c) == 0 || c[0] != "send-keys" {
			continue
		}
		if argvContains(c, "C-e") {
			sawAbsorber = true
		}
		if c[len(c)-1] == "Enter" {
			sawEnter = true
		}
	}
	if !sawAbsorber {
		t.Error("re-submit must send the C-e absorber after Escape")
	}
	if !sawEnter {
		t.Error("re-submit must send Enter")
	}
}
