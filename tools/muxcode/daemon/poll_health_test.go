package daemon

import (
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// allAlive is an agentAlive override that treats every role as a live agent —
// the unit-test analogue of launched panes, which checkPollHealth's liveness
// gate requires (provider.IsAlive fail-safes to "alive" and can't be forced
// false without a real tmux session, so tests inject liveness directly).
func allAlive(_, _ string) bool { return true }

func TestAckDeliveryActive(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)

	// Default: cutover ON — receipt-based delivery is the default path.
	t.Setenv("MUXCODE_DELIVERY_ACK", "")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	if !d.ackDeliveryActive() {
		t.Error("default must be active (receipt-based delivery is the default)")
	}

	// Explicit env opt-out forces the old path.
	t.Setenv("MUXCODE_DELIVERY_ACK", "off")
	if d.ackDeliveryActive() {
		t.Error("MUXCODE_DELIVERY_ACK=off must deactivate the cutover")
	}

	// Explicit env pin keeps it on.
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	if !d.ackDeliveryActive() {
		t.Error("MUXCODE_DELIVERY_ACK=1 must keep the cutover active")
	}

	// Kill switch overrides an explicit env enable.
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "1")
	if d.ackDeliveryActive() {
		t.Error("MUXCODE_DELIVERY_ACK_DISABLE must force the old path even when enabled")
	}
	t.Setenv("MUXCODE_DELIVERY_ACK", "")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")

	// Runtime OFF marker rolls back to the old path with no env set (instant, no
	// daemon restart).
	if err := bus.SetAckDeliveryOff(session, true); err != nil {
		t.Fatalf("SetAckDeliveryOff on: %v", err)
	}
	if d.ackDeliveryActive() {
		t.Error("delivery-ack OFF marker must roll back to the old path")
	}

	// An explicit env pin outranks the runtime marker (precedence: env > marker).
	t.Setenv("MUXCODE_DELIVERY_ACK", "on")
	if !d.ackDeliveryActive() {
		t.Error("explicit MUXCODE_DELIVERY_ACK=on must outrank the OFF marker")
	}
	t.Setenv("MUXCODE_DELIVERY_ACK", "")

	// Removing the marker restores the default (ON).
	if err := bus.SetAckDeliveryOff(session, false); err != nil {
		t.Fatalf("SetAckDeliveryOff off: %v", err)
	}
	if !d.ackDeliveryActive() {
		t.Error("removing the OFF marker must restore the default (cutover ON)")
	}
}

func TestCheckPollHealth_InertWhenCutoverOff(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "off") // cutover explicitly off (default is now on)
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")

	// A stale un-receipted message would be a gap IF the backstop ran.
	msg := bus.NewMessage("edit", "build", "request", "build", "build it", "")
	msg.TS = time.Now().Unix() - (pollHealthGapSecs + 30)
	if err := bus.Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	d.lastPollHealthCheck = 0 // clear interval guard
	d.checkPollHealth()

	if d.pollGapSince["build"] != 0 {
		t.Error("checkPollHealth must be inert (no gap tracking) when the cutover is off")
	}
}

func TestCheckPollHealth_RecordsGapAndAlertsWhenActive(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1") // cutover on
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	d.agentAlive = allAlive // liveness gate: role must be a live agent

	// Stale un-receipted message for build -> a receipt gap.
	msg := bus.NewMessage("edit", "build", "request", "build", "build it", "")
	msg.TS = time.Now().Unix() - (pollHealthGapSecs + 30)
	if err := bus.Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// First run records the gap onset (no alert yet — too fresh).
	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if d.pollGapSince["build"] == 0 {
		t.Fatal("expected a recorded gap onset for build")
	}
	if d.pollGapAlerted["build"] {
		t.Error("must not alert on a just-appeared gap")
	}

	// Backdate the gap onset past the alert threshold and run again -> alert edit.
	d.pollGapSince["build"] = time.Now().Unix() - (pollHealthAlertSecs + 5)
	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if !d.pollGapAlerted["build"] {
		t.Fatal("expected an alert after the gap persisted past the threshold")
	}
	msgs, _ := bus.Peek(session, "edit")
	found := false
	for _, m := range msgs {
		if m.Action == "delivery-gap" && m.From == "daemon" {
			found = true
		}
	}
	if !found {
		t.Error("expected a delivery-gap event in edit's inbox")
	}
}

func TestCheckPollHealth_ClearsGapOnReceipt(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	d.agentAlive = allAlive // liveness gate: role must be a live agent

	msg := bus.NewMessage("edit", "build", "request", "build", "build it", "")
	msg.TS = time.Now().Unix() - (pollHealthGapSecs + 30)
	if err := bus.Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if d.pollGapSince["build"] == 0 {
		t.Fatal("expected a recorded gap")
	}

	// A receipt (agent consumed, or verified-inject landed) clears the gap.
	bus.WriteReceipt(session, msg.ID, "build", bus.ReceiptKindDelivered)
	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if d.pollGapSince["build"] != 0 {
		t.Error("gap must clear once the message carries a receipt")
	}
}

// TestCheckPollHealth_RecoversOncePerGap guards the anti-churn fix: recovery is
// attempted at most once per gap episode (not every poll), then re-arms only
// after a receipt clears the gap. This stops the per-15s force-deliver + warning
// churn found in live testing for an agent that legitimately isn't consuming yet
// (busy, or a freshly-idle agent whose self-poll loop hasn't launched).
func TestCheckPollHealth_RecoversOncePerGap(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	d.agentAlive = allAlive

	msg := bus.NewMessage("edit", "build", "request", "build", "build it", "")
	msg.TS = time.Now().Unix() - (pollHealthGapSecs + 30)
	if err := bus.Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// First gap encounter attempts recovery exactly once (flag set).
	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if !d.pollGapRecovered["build"] {
		t.Fatal("expected a one-time recovery attempt on the first gap")
	}

	// A second poll with the gap still open must not re-arm recovery (no churn).
	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if !d.pollGapRecovered["build"] {
		t.Error("recovery must stay attempted-once while the gap persists")
	}

	// A receipt clears the gap and re-arms recovery for any future gap.
	bus.WriteReceipt(session, msg.ID, "build", bus.ReceiptKindDelivered)
	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if d.pollGapRecovered["build"] {
		t.Error("recovery flag must reset once the gap clears (re-arm for next gap)")
	}
	if d.pollGapSince["build"] != 0 {
		t.Error("gap must clear on receipt")
	}
}

// TestCheckPollHealth_SkipsNonLiveOrNonActionable guards the fix for the
// false-alarm churn found in live testing: the backstop must not flag (a) a role
// with no running agent (modal-only / unstarted roles like api/auto/webhook), nor
// (b) a live role whose inbox growth is response-only / informational (analyze,
// watch accumulating CC'd responses) — mirroring the checkInboxes actionable gate.
func TestCheckPollHealth_SkipsNonLiveOrNonActionable(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	// "test" is a live agent; "build" is not running (no pane / crashed).
	d.agentAlive = func(_, role string) bool { return role == "test" }

	// (a) Non-live role: a stale un-consumed REQUEST for a role with no running
	// agent must NOT register a gap — nothing to recover, so the old path would
	// have churned failed force-deliver attempts + false delivery-gap alerts.
	dead := bus.NewMessage("edit", "build", "request", "build", "build it", "")
	dead.TS = time.Now().Unix() - (pollHealthGapSecs + 30)
	if err := bus.Send(session, dead); err != nil {
		t.Fatalf("Send request: %v", err)
	}
	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if d.pollGapSince["build"] != 0 {
		t.Error("a stale request for a non-live role must not register a gap")
	}

	// (b) Live role but RESPONSE-only inbox: informational growth is not a
	// delivery failure, so no gap despite the stale un-receipted message.
	resp := bus.NewMessage("build", "test", "response", "response", "done", "")
	resp.TS = time.Now().Unix() - (pollHealthGapSecs + 30)
	if err := bus.Send(session, resp); err != nil {
		t.Fatalf("Send response: %v", err)
	}
	d.lastPollHealthCheck = 0
	d.checkPollHealth()
	if d.pollGapSince["test"] != 0 {
		t.Error("a response-only inbox for a live role must not register a gap")
	}
}

// A role with no tmux window in this session (never launched — e.g. "auto",
// which the default window set omits) can never consume its inbox, so every
// message to it ages into a permanent receipt gap that no recovery can clear:
// force-deliver has no pane to target and fails every attempt. agentAlive
// cannot filter these — provider.IsAlive fail-safes to "alive" when it cannot
// capture a pane, so a phantom role reads as live.
func TestCheckPollHealth_SkipsRoleWithNoWindow(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	d.agentAlive = allAlive // phantom role still reports "alive" (the fail-safe)
	// "auto" was never launched; every other role has a window.
	d.windowExists = func(_, role string) bool { return role != "auto" }

	stale := bus.NewMessage("daemon", "auto", "request", "heartbeat", "tick", "")
	stale.TS = time.Now().Unix() - (pollHealthGapSecs + 30)
	if err := bus.Send(session, stale); err != nil {
		t.Fatalf("Send: %v", err)
	}

	d.lastPollHealthCheck = 0
	d.checkPollHealth()

	if d.pollGapSince["auto"] != 0 {
		t.Error("a stale request for a role with no window must not register a gap")
	}
}

func TestDeliveryChecksGatedWhenCutoverActive(t *testing.T) {
	d := New(testSession(t), 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")

	// When the cutover is active, the pane-scrape delivery checks bypass their
	// work before touching their interval stamps (the gate is the first line).
	sentinel := int64(12345)
	d.lastIdleCheck = sentinel
	d.lastParkedCheck = sentinel
	d.lastPaneSweep = sentinel

	d.checkIdleAgents()
	d.checkParkedInput()
	d.checkPaneSweep()

	if d.lastIdleCheck != sentinel {
		t.Error("checkIdleAgents must bypass (not update its stamp) when cutover active")
	}
	if d.lastParkedCheck != sentinel {
		t.Error("checkParkedInput must bypass when cutover active")
	}
	if d.lastPaneSweep != sentinel {
		t.Error("checkPaneSweep must bypass when cutover active")
	}
}
