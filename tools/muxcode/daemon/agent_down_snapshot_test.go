package daemon

import (
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The forensic snapshot is taken exactly once per down episode, at strike 2 —
// after the first failure would be noise, after strike 3 the relaunch has
// typed over the pane.
func TestCheckAgentHealthSnapshotsAtStrikeTwo(t *testing.T) {
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	d := New(testSession(t), 5, 8)
	d.agentAlive = allDead
	d.windowNames = ownWindows
	d.probeDefinition = func(_, _ string) bus.DefinitionProbe { return bus.DefinitionPresent }
	snapshots := map[string]int{}
	d.snapshotAgentDown = func(_, role string) (string, error) {
		snapshots[role]++
		return "/snap/" + role, nil
	}

	d.lastAgentHealthCheck = 0
	d.checkAgentHealth()
	if len(snapshots) != 0 {
		t.Fatalf("snapshot taken at strike 1: %v", snapshots)
	}

	d.lastAgentHealthCheck = 0
	d.checkAgentHealth()
	if got := snapshots["plan"]; got != 1 {
		t.Fatalf("plan snapshots at strike 2 = %d, want 1", got)
	}

	d.lastAgentHealthCheck = 0
	d.checkAgentHealth()
	if got := snapshots["plan"]; got != 1 {
		t.Fatalf("plan snapshots after strike 3 = %d, want still 1", got)
	}
}
