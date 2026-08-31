package bus

import (
	"fmt"
	"time"
)

// injectEnterDelay separates the literal text write from the Enter
// keystroke: both in one pty write is the documented dropped-Enter
// pitfall — the text lands in the composer and never submits.
const injectEnterDelay = 150 * time.Millisecond

// InjectPromptText delivers text typed in the Prompt surface to the
// window's ACTIVE main agent (MUX-109 Phase 6). Mode cycling swaps
// panes, so the active agent always sits in the host window's agent
// pane — resolved by identity (MUX-117) — and ActiveModeRole names who
// is actually there (a window cycled to another mode must not have the
// text land as if the default role were on screen). Delivery is
// Escape (dismiss overlays) → TmuxSendLiteral (-l --, the MUX-104-safe
// form, so a dash-leading prompt arrives intact) → delay → separate
// Enter. Returns the role that received the text.
func InjectPromptText(session, window, text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("nothing to inject")
	}
	role := window
	if active, err := ActiveModeRole(session, window); err == nil && active != "" {
		role = active
	}
	target, err := ResolvePane(session, window, PaneTagAgent)
	if err != nil {
		return role, fmt.Errorf("resolving agent pane for %s: %w", window, err)
	}
	if err := TmuxSendEscape(target); err != nil {
		return role, fmt.Errorf("injecting into %s: %w", target, err)
	}
	if err := TmuxSendLiteral(target, text); err != nil {
		return role, fmt.Errorf("injecting into %s: %w", target, err)
	}
	time.Sleep(injectEnterDelay)
	if err := TmuxSendEnter(target); err != nil {
		return role, fmt.Errorf("submitting into %s: %w", target, err)
	}
	return role, nil
}
