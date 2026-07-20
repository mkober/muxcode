package bus

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Verified-injection delivery for non-hook TUIs (OpenCode/Codex).
//
// A wake-up injected via tmux send-keys can silently fail to submit: send-keys
// returns no error, but the TUI drops the Enter and the prompt parks unsent in
// the composer. The old path drained the inbox unconditionally right after
// send-keys "succeeded", so a dropped Enter LOST the message. This replaces that
// fire-and-hope drain with: inject -> confirm the text actually left the composer
// (re-sending Enter if it is still parked) -> only then consume the inbox and
// write a `delivered` receipt. If it cannot be confirmed, the inbox is left
// intact for the daemon's next wake cycle. No message is dropped on a dropped
// Enter, and the receipt honestly reflects a verified inject, not a true ack.
//
// This is the one place the delivery-acknowledgement redesign still pane-scrapes:
// unlike Claude/harness, these TUIs cannot consume their inbox in-process, so a
// pane check is the only confirmation available (the spec's provider matrix).

const (
	// injectVerifyRetries bounds the confirm loop so a genuinely stuck pane
	// (agent mid-render, composer never clears) can never spin forever — after
	// the last attempt we leave the inbox for the daemon's next cycle.
	injectVerifyRetries = 3
	// injectComposerProbeLines is how many bottom lines to capture. The composer
	// input box sits just above the status footer, so a shallow tail captures it
	// while excluding the injected text's echo higher up in the conversation.
	injectComposerProbeLines = 10
	// injectNeedleLen is the rune length of the distinctive tail slice of the
	// injected prompt used to detect it still parked. The tail (not the head) is
	// used because a long prompt wraps and only its final characters sit on the
	// composer's last visible input line.
	injectNeedleLen = 24
)

// injectVerifyDelay is the settle time before each pane re-capture, giving the
// TUI time to process the Enter and clear the composer. A var (not a const) so
// tests can zero it to avoid real sleeps.
var injectVerifyDelay = 250 * time.Millisecond

// injectionNeedle returns a distinctive slice of an injected prompt used to
// detect whether it is still parked in the composer. It uses the TAIL of the
// prompt: a long prompt wraps in the composer and only its final characters sit
// on the last visible input line. Returns "" when the prompt is too short to
// yield a reliable needle — the caller then treats the inject as unverifiable
// and preserves the old drain, so short prompts are never worse than before.
func injectionNeedle(prompt string) string {
	p := strings.TrimSpace(prompt)
	r := []rune(p)
	if len(r) < injectNeedleLen {
		return "" // too short to match reliably
	}
	return strings.TrimSpace(string(r[len(r)-injectNeedleLen:]))
}

// composerHoldsText reports whether needle still appears in captured composer
// content — i.e. the injected prompt is still parked (its Enter was dropped).
// content is a shallow bottom-of-pane capture (the composer box + footer), so a
// present needle means unsubmitted text. Pure, for testability.
func composerHoldsText(content, needle string) bool {
	if needle == "" {
		return false
	}
	return strings.Contains(content, needle)
}

// injectOutcome is the result of verifying whether an injected wake-up submitted.
type injectOutcome int

const (
	injectSubmitted injectOutcome = iota // confirmed: composer no longer holds the text
	injectParked                         // confirmed dropped: text still parked after retries
	injectUnknown                        // could not verify (no needle / pane capture failed)
)

// verifyInjectionLanded confirms an injected wake-up actually submitted in a
// non-hook TUI. It re-captures the composer; if the injected text is still
// parked it re-sends Enter (recovering the dropped keystroke) and retries, up to
// injectVerifyRetries. Returns injectSubmitted once the composer clears,
// injectParked if it never clears, or injectUnknown when it cannot tell (empty
// needle, or every pane capture failed) — the caller treats unknown like the
// old behavior so a capture failure is never worse than before.
func verifyInjectionLanded(target, needle string) injectOutcome {
	if needle == "" {
		return injectUnknown
	}
	captured := false
	for attempt := 0; attempt < injectVerifyRetries; attempt++ {
		time.Sleep(injectVerifyDelay)
		content, err := TmuxCapturePaneLines(target, injectComposerProbeLines)
		if err != nil {
			continue // transient capture failure — try again
		}
		captured = true
		if !composerHoldsText(content, needle) {
			return injectSubmitted
		}
		// Still parked — the Enter was dropped (or an overlay ate it). Re-send.
		_ = TmuxRun("send-keys", "-t", target, "Enter")
	}
	if !captured {
		return injectUnknown
	}
	return injectParked
}

// confirmInjectionAndConsume finalizes a non-hook wake-up: it verifies the
// injected prompt submitted and, only then, consumes the inbox with a
// verified-inject `delivered` receipt. If the text is still parked after bounded
// Enter retries, it leaves the inbox UNCONSUMED so the daemon's next wake cycle
// retries — no message is dropped on a dropped Enter. When verification is
// impossible (capture failure or too-short prompt), it falls back to consuming so
// behavior is never worse than the old fire-and-hope drain.
func confirmInjectionAndConsume(session, role, target, needle string) {
	switch verifyInjectionLanded(target, needle) {
	case injectParked:
		fmt.Fprintf(os.Stderr,
			"  [wakeup] %s injection not confirmed — text still parked after %d retries; leaving inbox for next cycle\n",
			role, injectVerifyRetries)
		return // do NOT consume — the message stays for the daemon to retry
	default:
		// injectSubmitted (verified) or injectUnknown (unverifiable — preserve the
		// old drain). Consume with a verified-inject `delivered` receipt.
		_, _ = ReceiveDelivered(session, role)
	}
}
