package bus

import (
	"strings"
	"testing"
)

// The dialog exactly as Claude Code renders it when a background shell is
// running — the shape that broke every reload once receipt-based delivery made
// `muxcode inbox --poll --loop` universal.
const exitConfirmPane = `   ⏺ Done.

   The following will stop when you exit:
   shell · muxcode inbox --poll --loop
   ❯ 1. Exit anyway
     2. Move to background and exit
     3. Stay
   Enter to confirm · Esc to cancel`

func TestPaneShowsExitConfirmation_RealDialog(t *testing.T) {
	if !PaneShowsExitConfirmation(exitConfirmPane) {
		t.Error("must detect the exit-confirmation dialog")
	}
}

func TestPaneShowsExitConfirmation_IdlePrompt(t *testing.T) {
	idle := "   ⏺ Build completed successfully\n\n❯ \n  ⏵⏵ bypass permissions on · 1 shell"
	if PaneShowsExitConfirmation(idle) {
		t.Error("an idle prompt must not read as a pending exit dialog")
	}
}

func TestPaneShowsExitConfirmation_Empty(t *testing.T) {
	if PaneShowsExitConfirmation("") {
		t.Error("empty capture must not read as a dialog")
	}
}

// Wording-drift fallback: header gone, option list still present.
func TestPaneShowsExitConfirmation_OptionPairFallback(t *testing.T) {
	pane := "❯ 1. Exit anyway\n  2. Move to background and exit\n  3. Stay"
	if !PaneShowsExitConfirmation(pane) {
		t.Error("option-list pair must still be detected")
	}
}

// "exit anyway" in ordinary prose must not trigger a stray Enter.
func TestPaneShowsExitConfirmation_ProseIsNotADialog(t *testing.T) {
	prose := "I could not stop it cleanly so I will exit anyway and retry the build."
	if PaneShowsExitConfirmation(prose) {
		t.Error("prose containing 'exit anyway' must not read as a dialog")
	}
}

// The regression that forced tail-scoping on PaneShowsRecoverableIdle: an agent
// that discussed this dialog earlier, then returned to an idle prompt, must not
// read as prompting. Otherwise GracefulStop fires Enter into a live session.
func TestPaneShowsExitConfirmation_StaleScrollbackIgnored(t *testing.T) {
	pane := exitConfirmPane + strings.Repeat("\n   ⏺ Fixed the reload bug and moved on.", 12) + "\n❯ "
	if PaneShowsExitConfirmation(pane) {
		t.Error("a dialog scrolled out of the live tail must not read as current state")
	}
}
