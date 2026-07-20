package bus

import (
	"fmt"
	"strings"
	"testing"
)

// injectMock programs a sequence of pane-capture results and counts the Enter
// re-sends (tmuxRunner calls) the verify loop issues. captures are returned in
// order, one per capture-pane call; captErr (parallel) forces a capture error;
// sendKeys records every TmuxRun call (the Enter re-sends).
type injectMock struct {
	captures []string
	captErr  []bool
	capIdx   int
	sendKeys [][]string
}

func setupInjectMock(t *testing.T, m *injectMock) {
	t.Helper()
	origOut := tmuxOutputRunner
	origRun := tmuxRunner
	origDelay := injectVerifyDelay
	injectVerifyDelay = 0 // no real sleeps in tests
	tmuxOutputRunner = func(args ...string) (string, error) {
		i := m.capIdx
		m.capIdx++
		if i < len(m.captErr) && m.captErr[i] {
			return "", fmt.Errorf("capture failed")
		}
		if i < len(m.captures) {
			return m.captures[i], nil
		}
		return "", nil
	}
	tmuxRunner = func(args ...string) error {
		m.sendKeys = append(m.sendKeys, args)
		return nil
	}
	t.Cleanup(func() {
		tmuxOutputRunner = origOut
		tmuxRunner = origRun
		injectVerifyDelay = origDelay
	})
}

func TestInjectionNeedle(t *testing.T) {
	if n := injectionNeedle("hi"); n != "" {
		t.Errorf("short prompt needle = %q, want empty", n)
	}
	prompt := "IMPORTANT: run the reply then do the actual review work right now"
	n := injectionNeedle(prompt)
	if n == "" {
		t.Fatal("long prompt yielded empty needle")
	}
	if len([]rune(n)) > injectNeedleLen {
		t.Errorf("needle rune len = %d, want <= %d", len([]rune(n)), injectNeedleLen)
	}
	if !strings.HasSuffix(strings.TrimSpace(prompt), n) {
		t.Errorf("needle %q must be the tail of the prompt", n)
	}
}

func TestComposerHoldsText(t *testing.T) {
	if !composerHoldsText("... the review work right now\n│ footer", "the review work right now") {
		t.Error("should detect parked needle in composer capture")
	}
	if composerHoldsText("│    │\n footer only", "the review work right now") {
		t.Error("should not detect an absent needle")
	}
	if composerHoldsText("anything at all", "") {
		t.Error("empty needle must never match")
	}
}

func TestVerifyInjectionLanded_Submitted(t *testing.T) {
	m := &injectMock{captures: []string{"│   │\n footer"}} // composer empty -> submitted
	setupInjectMock(t, m)
	if got := verifyInjectionLanded("s:review", "needle-xyz"); got != injectSubmitted {
		t.Errorf("outcome = %v, want injectSubmitted", got)
	}
	if len(m.sendKeys) != 0 {
		t.Errorf("first-try submit must not re-send Enter, got %d", len(m.sendKeys))
	}
}

func TestVerifyInjectionLanded_ParkedThenSubmitted(t *testing.T) {
	m := &injectMock{captures: []string{
		"parked: needle-xyz still in composer", // attempt 0: parked -> re-send Enter
		"│   │\n footer",                       // attempt 1: cleared -> submitted
	}}
	setupInjectMock(t, m)
	if got := verifyInjectionLanded("s:review", "needle-xyz"); got != injectSubmitted {
		t.Errorf("outcome = %v, want injectSubmitted", got)
	}
	if len(m.sendKeys) != 1 {
		t.Errorf("expected exactly 1 Enter re-send, got %d", len(m.sendKeys))
	}
}

func TestVerifyInjectionLanded_Parked(t *testing.T) {
	m := &injectMock{captures: []string{"needle-xyz", "needle-xyz", "needle-xyz"}}
	setupInjectMock(t, m)
	if got := verifyInjectionLanded("s:review", "needle-xyz"); got != injectParked {
		t.Errorf("outcome = %v, want injectParked", got)
	}
	if len(m.sendKeys) != injectVerifyRetries {
		t.Errorf("expected %d Enter re-sends, got %d", injectVerifyRetries, len(m.sendKeys))
	}
}

func TestVerifyInjectionLanded_CaptureFails(t *testing.T) {
	m := &injectMock{captErr: []bool{true, true, true}}
	setupInjectMock(t, m)
	if got := verifyInjectionLanded("s:review", "needle-xyz"); got != injectUnknown {
		t.Errorf("outcome = %v, want injectUnknown (unverifiable)", got)
	}
}

func TestVerifyInjectionLanded_EmptyNeedle(t *testing.T) {
	m := &injectMock{}
	setupInjectMock(t, m)
	if got := verifyInjectionLanded("s:review", ""); got != injectUnknown {
		t.Errorf("outcome = %v, want injectUnknown", got)
	}
	if m.capIdx != 0 {
		t.Errorf("empty needle must not capture the pane, captured %d times", m.capIdx)
	}
}

func TestConfirmInjectionAndConsume_SubmittedWritesDeliveredReceipt(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	m := &injectMock{captures: []string{"│  │\n footer"}} // submitted
	setupInjectMock(t, m)

	msg := NewMessage("edit", "review", "request", "review", "review it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	confirmInjectionAndConsume(session, "review", "s:review", "needle-xyz")

	if HasMessages(session, "review") {
		t.Error("inbox should be drained after a verified inject")
	}
	ds, acked := ReadReceipt(session, msg.ID)
	if !acked {
		t.Fatal("expected a receipt after verified inject")
	}
	if ds.ReceiptKind != ReceiptKindDelivered {
		t.Errorf("ReceiptKind = %q, want %q (verified inject is delivered, not a true ack)",
			ds.ReceiptKind, ReceiptKindDelivered)
	}
	if ds.Status != StatusDelivered {
		t.Errorf("Status = %q, want %q", ds.Status, StatusDelivered)
	}
}

func TestConfirmInjectionAndConsume_ParkedLeavesInbox(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	// Composer never clears -> parked -> must NOT drain (no message loss).
	m := &injectMock{captures: []string{"needle-xyz", "needle-xyz", "needle-xyz"}}
	setupInjectMock(t, m)

	msg := NewMessage("edit", "review", "request", "review", "review it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	confirmInjectionAndConsume(session, "review", "s:review", "needle-xyz")

	if !HasMessages(session, "review") {
		t.Error("inbox must NOT be drained when injection is unconfirmed — message would be lost")
	}
	if _, acked := ReadReceipt(session, msg.ID); acked {
		t.Error("no receipt should be written when the injection is not confirmed")
	}
}
