package daemon

import (
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
