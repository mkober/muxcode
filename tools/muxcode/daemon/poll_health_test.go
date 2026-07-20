package daemon

import (
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

func TestAckDeliveryActive(t *testing.T) {
	d := New(testSession(t), 5, 8)

	// Default: cutover OFF (safe — the old delivery machinery stays in charge).
	t.Setenv("MUXCODE_DELIVERY_ACK", "")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	if d.ackDeliveryActive() {
		t.Error("default must be inactive (old delivery path)")
	}

	// Explicit enable.
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	if !d.ackDeliveryActive() {
		t.Error("MUXCODE_DELIVERY_ACK=1 must activate the cutover")
	}

	// Kill switch overrides enable.
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "1")
	if d.ackDeliveryActive() {
		t.Error("MUXCODE_DELIVERY_ACK_DISABLE must force the old path even when enabled")
	}
}

func TestCheckPollHealth_InertWhenCutoverOff(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "") // cutover off
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
