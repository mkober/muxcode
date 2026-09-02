package daemon

import (
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// definitionDaemon builds a daemon whose probe answers from verdicts (Present
// for every role not listed), with a clean pane and reloads captured on a
// channel instead of run.
func definitionDaemon(t *testing.T, verdicts map[string]bus.DefinitionProbe) (*Daemon, chan string) {
	t.Helper()
	t.Setenv("MUXCODE_DEFINITION_WATCHDOG_DISABLE", "")
	t.Setenv("MUXCODE_AGENT_CLI", "")
	t.Setenv("MUXCODE_PLAN_CLI", "claude")
	t.Setenv("MUXCODE_EDIT_CLI", "claude")
	d := New(testSession(t), 5, 8)
	d.agentAlive = allAlive
	d.windowNames = ownWindows
	d.probeDefinition = func(_, role string) bus.DefinitionProbe {
		if v, ok := verdicts[role]; ok {
			return v
		}
		return bus.DefinitionPresent
	}
	d.capturePane = func(_ string, _ int) (string, error) { return "❯ ", nil }
	reloads := make(chan string, 8)
	d.reloadAgent = func(_, role string) error {
		reloads <- role
		return nil
	}
	return d, reloads
}

func sweepDefinitions(d *Daemon) {
	d.lastDefinitionCheck = 0
	d.checkDefinitionless()
}

func expectReload(t *testing.T, reloads chan string, role string) {
	t.Helper()
	select {
	case got := <-reloads:
		if got != role {
			t.Fatalf("reloaded %q, want %q", got, role)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no reload of %s", role)
	}
}

func expectNoReload(t *testing.T, reloads chan string) {
	t.Helper()
	select {
	case got := <-reloads:
		t.Fatalf("unexpected reload of %s", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func editEvents(t *testing.T, session, action string) int {
	t.Helper()
	msgs, err := bus.Peek(session, "edit")
	if err != nil {
		t.Fatalf("peek edit inbox: %v", err)
	}
	n := 0
	for _, m := range msgs {
		if m.Action == action {
			n++
		}
	}
	return n
}

// A definition-less plan is refused: flagged and reloaded after the debounce,
// edit alerted once, and the cooldown holds off a second reload.
func TestCheckDefinitionlessRefusesAfterDebounce(t *testing.T) {
	d, reloads := definitionDaemon(t, map[string]bus.DefinitionProbe{"plan": bus.DefinitionMissing})

	sweepDefinitions(d)
	if d.definitionless["plan"] {
		t.Fatal("flagged on a single sighting — debounce missing")
	}
	expectNoReload(t, reloads)

	sweepDefinitions(d)
	if !d.definitionless["plan"] {
		t.Fatal("plan not flagged after two sightings")
	}
	expectReload(t, reloads, "plan")
	if got := editEvents(t, d.session, "agent-definitionless"); got != 1 {
		t.Fatalf("edit alerts = %d, want 1", got)
	}

	sweepDefinitions(d)
	sweepDefinitions(d)
	expectNoReload(t, reloads)
	if got := d.definitionReloads["plan"]; got != 1 {
		t.Fatalf("reloads within cooldown = %d, want 1", got)
	}
}

// Negative controls: every role carrying its definition leaves no trace, and
// the opt-out never probes at all.
func TestCheckDefinitionlessHealthyAndDisabled(t *testing.T) {
	d, reloads := definitionDaemon(t, nil)
	for i := 0; i < 3; i++ {
		sweepDefinitions(d)
	}
	expectNoReload(t, reloads)
	for role, flagged := range d.definitionless {
		if flagged {
			t.Fatalf("%s flagged on a healthy sweep", role)
		}
	}
	if got := editEvents(t, d.session, "agent-definitionless"); got != 0 {
		t.Fatalf("healthy sweeps alerted edit %d times", got)
	}

	d, reloads = definitionDaemon(t, map[string]bus.DefinitionProbe{"plan": bus.DefinitionMissing})
	t.Setenv("MUXCODE_DEFINITION_WATCHDOG_DISABLE", "1")
	probed := false
	d.probeDefinition = func(_, _ string) bus.DefinitionProbe { probed = true; return bus.DefinitionMissing }
	sweepDefinitions(d)
	sweepDefinitions(d)
	if probed || d.definitionless["plan"] {
		t.Fatal("disabled watchdog still probed or flagged")
	}
	expectNoReload(t, reloads)
}

// The banner is only a fallback: it flags when no process can be attributed,
// and never overrides a positive probe (an agent discussing this bug prints
// the banner text without being downgraded).
func TestCheckDefinitionlessBannerFallback(t *testing.T) {
	banner := "This session was running agent 'planner', which is no longer available (no agent by that name in /repo)."

	d, reloads := definitionDaemon(t, map[string]bus.DefinitionProbe{"plan": bus.DefinitionUnknown})
	d.capturePane = func(target string, _ int) (string, error) {
		if target == bus.PaneTarget(d.session, "plan") {
			return banner, nil
		}
		return "❯ ", nil
	}
	sweepDefinitions(d)
	sweepDefinitions(d)
	if !d.definitionless["plan"] {
		t.Fatal("unattributable process with the banner in its pane was not flagged")
	}
	expectReload(t, reloads, "plan")

	d, reloads = definitionDaemon(t, map[string]bus.DefinitionProbe{"plan": bus.DefinitionUnknown})
	sweepDefinitions(d)
	sweepDefinitions(d)
	if d.definitionless["plan"] {
		t.Fatal("unattributable process with a clean pane was flagged")
	}
	expectNoReload(t, reloads)

	d, reloads = definitionDaemon(t, nil)
	d.capturePane = func(_ string, _ int) (string, error) { return banner, nil }
	sweepDefinitions(d)
	sweepDefinitions(d)
	if d.definitionless["plan"] {
		t.Fatal("banner text outranked a positive probe")
	}
	expectNoReload(t, reloads)
}

// At the reload cap the watchdog stops reloading and alerts once.
func TestCheckDefinitionlessCapAlertsOnce(t *testing.T) {
	d, reloads := definitionDaemon(t, map[string]bus.DefinitionProbe{"plan": bus.DefinitionMissing})
	d.definitionReloads["plan"] = definitionReloadCap

	for i := 0; i < 4; i++ {
		sweepDefinitions(d)
	}
	expectNoReload(t, reloads)
	if !d.definitionGaveUp["plan"] {
		t.Fatal("cap reached but watchdog did not give up")
	}
	if got := d.definitionReloads["plan"]; got != definitionReloadCap {
		t.Fatalf("reloads past the cap: %d", got)
	}
	if got := editEvents(t, d.session, "agent-definitionless"); got != 2 {
		t.Fatalf("edit alerts = %d, want 2 (flagged + gave up)", got)
	}
}

// A definition found on the process again clears the flag and re-arms.
func TestCheckDefinitionlessRestoreClearsFlag(t *testing.T) {
	verdicts := map[string]bus.DefinitionProbe{"plan": bus.DefinitionMissing}
	d, reloads := definitionDaemon(t, verdicts)
	sweepDefinitions(d)
	sweepDefinitions(d)
	expectReload(t, reloads, "plan")

	verdicts["plan"] = bus.DefinitionPresent
	sweepDefinitions(d)
	if d.definitionless["plan"] || d.definitionSeen["plan"] != 0 {
		t.Fatalf("restored plan still flagged (seen=%d)", d.definitionSeen["plan"])
	}
}

// edit is the user's own pane: alerted, never reloaded by the daemon.
func TestCheckDefinitionlessEditAlertedNotReloaded(t *testing.T) {
	d, reloads := definitionDaemon(t, map[string]bus.DefinitionProbe{"edit": bus.DefinitionMissing})
	sweepDefinitions(d)
	sweepDefinitions(d)
	if !d.definitionless["edit"] {
		t.Fatal("definition-less edit was not flagged")
	}
	if got := editEvents(t, d.session, "agent-definitionless"); got != 1 {
		t.Fatalf("edit alerts = %d, want 1", got)
	}
	expectNoReload(t, reloads)
	if d.definitionReloads["edit"] != 0 {
		t.Fatal("daemon reloaded edit")
	}
}

// agent-recovered is withheld until the agent is back WITH its definition —
// the 2026-09-01 recovery event described an unconstrained agent.
func TestCheckAgentHealthWithholdsRecoveryWithoutDefinition(t *testing.T) {
	verdicts := map[string]bus.DefinitionProbe{"plan": bus.DefinitionMissing}
	d, _ := definitionDaemon(t, verdicts)
	planAlive := false
	d.agentAlive = func(_, role string) bool {
		if role == "plan" {
			return planAlive
		}
		return true
	}
	healthSweep := func() {
		d.lastAgentHealthCheck = 0
		d.checkAgentHealth()
	}

	healthSweep()
	healthSweep()
	if !d.agentWasDown["plan"] {
		t.Fatal("plan not marked down after two failed probes")
	}

	planAlive = true
	healthSweep()
	if !d.agentWasDown["plan"] {
		t.Fatal("recovery announced for an agent back without its definition")
	}
	if got := editEvents(t, d.session, "agent-recovered"); got != 0 {
		t.Fatalf("agent-recovered sent %d times for a definition-less agent", got)
	}

	verdicts["plan"] = bus.DefinitionPresent
	healthSweep()
	if d.agentWasDown["plan"] {
		t.Fatal("recovery still withheld with the definition present")
	}
	if got := editEvents(t, d.session, "agent-recovered"); got != 1 {
		t.Fatalf("agent-recovered sent %d times, want 1", got)
	}
}
