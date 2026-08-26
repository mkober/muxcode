package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// frTestSetup wires a daemon with recorder rung executors and one stale
// un-receipted request for build — an open escalation episode.
func frTestSetup(t *testing.T, idle bool) (*Daemon, *[]string, string) {
	t.Helper()
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	t.Setenv("MUXCODE_FORCE_RESPOND_DISABLE", "")
	d.agentAlive = allAlive
	// Hook-provider gating by default; the non-hook test overrides. A seam,
	// not an env pin: ResolveProvider consults session runtime overrides
	// first, so env alone is not deterministic under a live session.
	d.frPaneGated = func(string) bool { return true }

	var fired []string
	d.frNotify = func(_, role string) error {
		fired = append(fired, "notify:"+role)
		return nil
	}
	d.frDeliver = func(_, role string, force bool) error {
		fired = append(fired, fmt.Sprintf("deliver:%s:force=%v", role, force))
		return nil
	}
	d.frIsIdle = func(_, _ string) bool { return idle }

	msg := bus.NewMessage("edit", "build", "request", "build", "build it", "")
	msg.TS = time.Now().Unix() - (forceRespondDefaultSecs + 60)
	if err := bus.Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return d, &fired, msg.ID
}

// runLadderStep runs one sweep with the interval and rung cooldown gates
// cleared, so each call fires at most the next rung.
func runLadderStep(d *Daemon) {
	d.lastForceRespondCheck = 0
	for role := range d.frLastFire {
		if d.frLastFire[role] != 0 {
			d.frLastFire[role] = time.Now().Unix() - forceRespondRungCooldown() - 1
		}
	}
	d.checkForceRespond()
}

func TestForceRespond_LadderAdvancesThroughAllRungs(t *testing.T) {
	d, fired, _ := frTestSetup(t, true)

	runLadderStep(d) // rung 0
	if len(*fired) != 1 || (*fired)[0] != "notify:build" {
		t.Fatalf("rung 0 must be a notify, got %v", *fired)
	}

	runLadderStep(d) // rung 1
	if len(*fired) != 2 || (*fired)[1] != "deliver:build:force=false" {
		t.Fatalf("rung 1 must be a non-force deliver, got %v", *fired)
	}

	runLadderStep(d) // rung 2
	if len(*fired) != 3 || (*fired)[2] != "deliver:build:force=true" {
		t.Fatalf("rung 2 must be the force override, got %v", *fired)
	}

	runLadderStep(d) // rung 3 — alert
	msgs, _ := bus.Peek(d.session, "edit")
	var alert string
	for _, m := range msgs {
		if m.Action == "force-respond" && m.From == "daemon" {
			alert = m.Payload
		}
	}
	if alert == "" {
		t.Fatal("expected a force-respond alert in edit's inbox")
	}
	for _, want := range []string{"force-respond-notify", "force-respond-deliver", "force-respond-override", "muxcode diagnose build"} {
		if !strings.Contains(alert, want) {
			t.Errorf("alert must carry the escalation history, missing %q:\n%s", want, alert)
		}
	}

	// Exhausted: further sweeps fire nothing while the gap persists.
	before := len(*fired)
	runLadderStep(d)
	if len(*fired) != before {
		t.Errorf("an exhausted ladder must hold, fired %v", (*fired)[before:])
	}
}

// A receipt ends the episode: the ladder resets instead of advancing —
// success is judged by the receipt, never by a rung's return value.
func TestForceRespond_ReceiptResetsLadder(t *testing.T) {
	d, fired, msgID := frTestSetup(t, true)

	runLadderStep(d)
	if len(*fired) != 1 {
		t.Fatalf("expected the first rung fired, got %v", *fired)
	}

	// The episode is persisted for the TUI while open…
	if st, ok := bus.ReadForceRespondState(d.session, "build"); !ok || len(st.History) == 0 {
		t.Errorf("expected a persisted episode after the first rung, got %+v ok=%v", st, ok)
	}

	bus.WriteReceipt(d.session, msgID, "build", bus.ReceiptKindDelivered)
	runLadderStep(d)
	if len(*fired) != 1 {
		t.Errorf("a receipt must stop the ladder, got %v", *fired)
	}
	if d.frRung["build"] != frRungNotify || len(d.frHistory["build"]) != 0 {
		t.Errorf("episode state must reset on receipt: rung=%d history=%v", d.frRung["build"], d.frHistory["build"])
	}
	// …and cleared once the episode ends.
	if _, ok := bus.ReadForceRespondState(d.session, "build"); ok {
		t.Error("expected the persisted episode cleared on receipt")
	}
}

// An agent that consumed its request (ack receipt) is legitimately busy —
// it must be invisible to the ladder no matter how old the request is.
func TestForceRespond_BusyButRespondingNeverEscalates(t *testing.T) {
	d, fired, msgID := frTestSetup(t, true)
	bus.WriteReceipt(d.session, msgID, "build", bus.ReceiptKindAck)

	runLadderStep(d)
	if len(*fired) != 0 {
		t.Errorf("an acked (busy-but-responding) agent must never escalate, got %v", *fired)
	}
}

// A request younger than the threshold never opens an episode.
func TestForceRespond_YoungRequestNoTrigger(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	t.Setenv("MUXCODE_DELIVERY_ACK", "1")
	t.Setenv("MUXCODE_DELIVERY_ACK_DISABLE", "")
	d.agentAlive = allAlive
	var fired []string
	d.frNotify = func(_, role string) error { fired = append(fired, role); return nil }

	msg := bus.NewMessage("edit", "build", "request", "build", "build it", "")
	if err := bus.Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	d.lastForceRespondCheck = 0
	d.checkForceRespond()
	if len(fired) != 0 {
		t.Errorf("a fresh request must not trigger the ladder, got %v", fired)
	}
}

func TestForceRespond_DisabledByEnv(t *testing.T) {
	d, fired, _ := frTestSetup(t, true)
	t.Setenv("MUXCODE_FORCE_RESPOND_DISABLE", "1")

	runLadderStep(d)
	if len(*fired) != 0 {
		t.Errorf("the opt-out env var must disable the ladder, got %v", *fired)
	}
}

// A non-hook role must fire the override despite IsIdle being false —
// provider.IsIdle is unconditionally false for OpenCode/Codex, and
// gating on it made the ladder alert-only for the exact agent class the
// 2026-08-26 incident hit (found by review; tests had stubbed the seam).
func TestForceRespond_NonHookOverrideFiresWithoutIdleGate(t *testing.T) {
	d, fired, _ := frTestSetup(t, false) // pane never reads idle
	d.frPaneGated = func(string) bool { return false }

	runLadderStep(d) // rung 0
	runLadderStep(d) // rung 1
	runLadderStep(d) // rung 2 — must fire, not postpone
	found := false
	for _, f := range *fired {
		if f == "deliver:build:force=true" {
			found = true
		}
	}
	if !found {
		t.Errorf("non-hook override must fire without the idle gate, got %v", *fired)
	}
}

// The override rung never injects into an active hook-provider pane: it
// postpones up to the cap, then escalates to the alert without firing.
func TestForceRespond_OverridePostponedWhileActive(t *testing.T) {
	d, fired, _ := frTestSetup(t, false) // pane never idle

	runLadderStep(d) // rung 0
	runLadderStep(d) // rung 1
	for i := 0; i < frOverridePostponeMax; i++ {
		runLadderStep(d) // postponements — no override fired
	}
	for _, f := range *fired {
		if f == "deliver:build:force=true" {
			t.Fatalf("override must not fire into an active pane, got %v", *fired)
		}
	}

	runLadderStep(d) // exhausted postponements — rung advances past override
	runLadderStep(d) // alert
	msgs, _ := bus.Peek(d.session, "edit")
	found := false
	for _, m := range msgs {
		if m.Action == "force-respond" && strings.Contains(m.Payload, "postponed") {
			found = true
		}
	}
	if !found {
		t.Error("expected the alert to record the postponed override")
	}
	for _, f := range *fired {
		if f == "deliver:build:force=true" {
			t.Errorf("override must never have fired, got %v", *fired)
		}
	}
}
