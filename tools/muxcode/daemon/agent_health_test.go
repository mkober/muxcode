package daemon

import (
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// allDead is an agentAlive override that reports every role as crashed.
// provider.IsAlive fail-safes to "alive" and cannot be forced false without a
// real tmux session, so tests inject the dead verdict directly.
func allDead(_, _ string) bool { return false }

// ownWindows is a windowNames override listing a tmux window for every role
// that owns one, so roleHasWindow passes for real roles and the hosted-role
// guard is what does the skipping.
func ownWindows(_ string) ([]string, error) {
	var names []string
	for _, role := range bus.KnownRoles {
		if bus.WindowForRole(role) == role {
			names = append(names, role)
		}
	}
	return names, nil
}

// TestCheckAgentHealthSkipsHostedRoles guards the regression that let the
// daemon "restart" a hosted role. Hosted roles (docs→plan, pr-read→commit)
// have no pane of their own, but RestartLocalAgent resolves PaneTarget by
// role — so restarting pr-read sends C-c into the *commit* window and kills a
// healthy agent. It also hands that pane a second, independent restart budget,
// letting one window be killed up to six times by two counters that don't know
// about each other.
func TestCheckAgentHealthSkipsHostedRoles(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	d.agentAlive = allDead
	d.windowNames = ownWindows

	// Two sweeps: real roles reach strike 2 (alert only). Nothing reaches
	// strike 3, so no restart runs and no tmux call is made.
	for i := 0; i < 2; i++ {
		d.lastAgentHealthCheck = 0
		d.checkAgentHealth()
	}

	hostedSeen := false
	for _, role := range bus.KnownRoles {
		host := bus.WindowForRole(role)
		if host == role {
			continue
		}
		hostedSeen = true
		if got := d.agentFailCounts[role]; got != 0 {
			t.Errorf("hosted role %q must never be probed (host=%q), got fail count %d",
				role, host, got)
		}
		if got := d.agentRestarts[role]; got != 0 {
			t.Errorf("hosted role %q must never be restarted (host=%q), got %d restarts",
				role, host, got)
		}
	}
	if !hostedSeen {
		t.Fatal("no hosted roles in KnownRoles — test would pass vacuously")
	}

	// Sanity: a role that owns its window is still probed, otherwise the
	// assertions above would pass because nothing ran at all.
	if d.agentFailCounts["plan"] == 0 {
		t.Error("real role plan owns its window and must still be probed")
	}
}

// TestCheckAgentHealthResetsFailCountAtRestartCap guards the regression where
// the restart-cap branch returned without resetting the counter. That branch is
// nested under `count == 3`, so a counter left above 3 never matches again:
// alert-only mode fired exactly one alert and then stayed silent forever, no
// matter how long the agent stayed down.
func TestCheckAgentHealthResetsFailCountAtRestartCap(t *testing.T) {
	session := testSession(t)
	d := New(session, 5, 8)
	d.agentAlive = allDead
	d.windowNames = ownWindows

	// plan has exhausted its restart budget and is one probe from strike 3.
	d.agentRestarts["plan"] = 3
	d.agentFailCounts["plan"] = 2

	d.lastAgentHealthCheck = 0
	d.checkAgentHealth()

	if got := d.agentFailCounts["plan"]; got != 0 {
		t.Errorf("a capped agent must reset its fail count so the count can cycle "+
			"back to 3 and keep re-alerting, got %d", got)
	}
	// The cap must still hold — reaching strike 3 at the cap must not restart.
	if got := d.agentRestarts["plan"]; got != 3 {
		t.Errorf("restart cap must not be exceeded, got %d restarts", got)
	}
}

// MUX-126 Phase 4: edit is swept like any role — strike 2 alerts agent-down,
// and at the restart cap it goes alert-only without restarting. Only edit
// reads dead and its cap is pre-set, so strike 3 reaches no real tmux restart.
func TestCheckAgentHealthMonitorsEdit(t *testing.T) {
	t.Setenv("MUXCODE_EDIT_AUTO_RESTART_DISABLE", "")
	session := testSession(t)
	d := New(session, 5, 8)
	d.agentAlive = func(_, role string) bool { return role != "edit" }
	d.windowNames = ownWindows
	d.agentRestarts["edit"] = 3

	for i := 0; i < 3; i++ {
		d.lastAgentHealthCheck = 0
		d.checkAgentHealth()
	}

	for role, n := range d.agentFailCounts {
		if role != "edit" && n != 0 {
			t.Errorf("only edit may read dead in this fixture, %q has fail count %d", role, n)
		}
	}
	if got := d.agentRestarts["edit"]; got != 3 {
		t.Errorf("edit restart cap must hold, got %d restarts", got)
	}
	if got := d.agentFailCounts["edit"]; got != 0 {
		t.Errorf("edit at the cap must reset its fail count like any role, got %d", got)
	}
	msgs, _ := bus.Peek(session, "edit")
	down := false
	for _, m := range msgs {
		down = down || (m.Action == "agent-down" && strings.Contains(m.Payload, "AGENT DOWN: edit"))
	}
	if !down {
		t.Errorf("edit reached strike 2 without an agent-down alert: %+v", msgs)
	}
}

// Negative controls: the env opt-out and an agent-health --stop marker each
// keep edit out of the sweep entirely — never probed, so never restarted.
func TestCheckAgentHealthEditOptOuts(t *testing.T) {
	optOuts := map[string]func(t *testing.T, session string){
		"env opt-out": func(t *testing.T, _ string) {
			t.Setenv("MUXCODE_EDIT_AUTO_RESTART_DISABLE", "1")
		},
		"stop marker": func(t *testing.T, session string) {
			t.Setenv("MUXCODE_EDIT_AUTO_RESTART_DISABLE", "")
			if err := bus.MarkAgentStopped(session, "edit"); err != nil {
				t.Fatalf("MarkAgentStopped: %v", err)
			}
		},
	}
	for name, optOut := range optOuts {
		t.Run(name, func(t *testing.T) {
			session := testSession(t)
			optOut(t, session)
			d := New(session, 5, 8)
			d.agentAlive = allDead
			d.windowNames = ownWindows

			for i := 0; i < 2; i++ {
				d.lastAgentHealthCheck = 0
				d.checkAgentHealth()
			}
			if got := d.agentFailCounts["edit"]; got != 0 {
				t.Errorf("opted-out edit was probed: fail count %d", got)
			}
			if d.agentFailCounts["plan"] == 0 {
				t.Error("plan must still be probed, or the assertion above is vacuous")
			}
		})
	}
}
