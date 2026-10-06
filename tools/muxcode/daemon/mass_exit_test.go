package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// massExitHarness is a resumeHarness whose dead set is chosen per test, with
// every role on the Claude provider unless a test overrides one.
func massExitHarness(t *testing.T, dead ...string) *resumeHarness {
	t.Helper()
	for _, env := range []string{"MUXCODE_COMMIT_CLI", "MUXCODE_RUN_CLI"} {
		t.Setenv(env, "claude")
	}
	t.Setenv("MUXCODE_MASS_EXIT_WINDOW_SECS", "")
	h := newResumeHarness(t, "")
	isDead := map[string]bool{}
	for _, r := range dead {
		isDead[r] = true
		h.hint[r] = true
	}
	h.d.agentAlive = func(_, role string) bool { return !isDead[role] }
	return h
}

func massExitEvents(t *testing.T, session string) []bus.Message {
	t.Helper()
	msgs, _ := bus.Peek(session, "edit")
	var out []bus.Message
	for _, m := range msgs {
		if m.Action == "mass-agent-exit" {
			out = append(out, m)
		}
	}
	return out
}

// Three Claude agents down in one sweep raise exactly one mass-agent-exit to
// edit and one lifecycle row naming all three — and the sweeps that follow
// inside the window raise no second one. The event stands in for their
// agent-down alerts, so edit is notified once, not once per agent. Per-role
// restarts still run.
func TestCheckAgentHealth_MassExitRaisesOneEvent(t *testing.T) {
	h := massExitHarness(t, "plan", "commit", "run")

	h.sweep(3)

	events := massExitEvents(t, h.d.session)
	if len(events) != 1 {
		t.Fatalf("mass-agent-exit events = %d, want exactly 1", len(events))
	}
	if n := editEvents(t, h.d.session, "agent-down"); n != 0 {
		t.Errorf("agent-down alerts = %d alongside the mass-agent-exit, want 0 — the burst must reach edit as one event", n)
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-down-folded")); n != 3 {
		t.Errorf("agent-down-folded rows = %d, want 3", n)
	}
	rows := lifecycleDetails(t, h.d.session, "mass-agent-exit")
	if len(rows) != 1 {
		t.Fatalf("mass-agent-exit lifecycle rows = %d, want 1", len(rows))
	}
	for _, want := range []string{"3 Claude agents", h.d.session + ": commit, plan, run"} {
		if !strings.Contains(rows[0], want) || !strings.Contains(events[0].Payload, want) {
			t.Errorf("row %q / event %q missing %q", rows[0], events[0].Payload, want)
		}
	}
	for _, role := range []string{"plan", "commit", "run"} {
		if h.d.agentRestarts[role] == 0 {
			t.Errorf("%s was not restarted — correlation must not hold up per-role restart", role)
		}
	}
}

// Negative control: a single death is an agent-down, never a mass exit — and
// holding the alert to the end of the probe pass still sends it ahead of the
// restart notice.
func TestCheckAgentHealth_SingleDeathRaisesNoMassExit(t *testing.T) {
	h := massExitHarness(t, "plan")

	h.sweep(3)

	if n := len(massExitEvents(t, h.d.session)); n != 0 {
		t.Fatalf("a single death raised %d mass-agent-exit events", n)
	}
	if len(h.restarts) == 0 {
		t.Fatal("plan never restarted — the fixture did not exercise a death")
	}
	msgs, _ := bus.Peek(h.d.session, "edit")
	down, restarting := -1, -1
	for i, m := range msgs {
		switch {
		case m.Action == "agent-down" && down < 0:
			down = i
		case m.Action == "agent-restarting" && restarting < 0:
			restarting = i
		}
	}
	if down < 0 {
		t.Fatal("an uncorrelated death sent no agent-down alert")
	}
	if restarting >= 0 && restarting < down {
		t.Errorf("agent-restarting (#%d) reached edit before agent-down (#%d)", restarting, down)
	}
}

// An earlier sighting correlates only while it is inside the window: ten
// seconds before, it joins the burst; five minutes before, it is unrelated.
func TestCheckAgentHealth_MassExitWindowBoundary(t *testing.T) {
	cases := []struct {
		name string
		ago  int64
		want int
	}{
		{"inside window", 10, 1},
		{"five minutes apart", 300, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := massExitHarness(t, "plan")
			if err := bus.RecordAgentExit(h.d.session, "commit", time.Now().Unix()-c.ago); err != nil {
				t.Fatalf("RecordAgentExit: %v", err)
			}

			h.sweep(1)

			if n := len(massExitEvents(t, h.d.session)); n != c.want {
				t.Errorf("mass-agent-exit events = %d, want %d", n, c.want)
			}
		})
	}
}

// A role that stays down through repeated strike-count resets is one outage,
// recorded once: its re-marks must not refresh into a fresh timestamp that a
// second role first dying five minutes later correlates with. Recording goes
// through the real producer (markAgentDown), with plan's first-episode row
// back-dated five minutes to stand in for the elapsed time.
func TestCheckAgentHealth_PersistentOutageNotRefreshed(t *testing.T) {
	h := massExitHarness(t)
	dead := map[string]bool{"plan": true}
	h.d.agentAlive = func(_, role string) bool { return !dead[role] }
	var shift int64 = 300
	recorded := map[string]int{}
	h.d.recordAgentExit = func(session, role string, at int64) error {
		recorded[role]++
		return bus.RecordAgentExit(session, role, at-shift)
	}

	h.sweep(3)
	shift = 0
	dead["commit"] = true
	h.sweep(3)

	if recorded["commit"] != 1 {
		t.Fatalf("commit exits recorded = %d, want 1 — fixture never exercised the second death", recorded["commit"])
	}
	if !h.d.agentWasDown["plan"] {
		t.Fatal("plan did not stay down across the sweeps")
	}
	if recorded["plan"] != 1 {
		t.Errorf("plan exits recorded = %d, want 1 — a continuing outage was re-recorded", recorded["plan"])
	}
	if n := len(massExitEvents(t, h.d.session)); n != 0 {
		t.Errorf("an old outage and a death five minutes later raised %d mass-agent-exit events", n)
	}
}

// Positive control for the episode gate: a role that recovers and then dies
// again opens a new episode and is recorded again.
func TestCheckAgentHealth_RecoveredRoleRecordedAgain(t *testing.T) {
	h := massExitHarness(t)
	dead := map[string]bool{"plan": true}
	h.d.agentAlive = func(_, role string) bool { return !dead[role] }
	recorded := 0
	h.d.recordAgentExit = func(_, role string, _ int64) error {
		if role == "plan" {
			recorded++
		}
		return nil
	}

	h.sweep(2)
	dead["plan"] = false
	h.sweep(1)
	if h.d.agentWasDown["plan"] {
		t.Fatal("plan never recovered — fixture did not close the first episode")
	}
	dead["plan"] = true
	h.sweep(2)

	if recorded != 2 {
		t.Errorf("plan exits recorded = %d, want 2 (one per episode)", recorded)
	}
}

// A session whose own death was recorded before a peer session's still learns
// of the burst on its next sweep inside the window, and the event names both.
func TestCheckAgentHealth_MassExitSeesLaterPeerSession(t *testing.T) {
	h := massExitHarness(t, "plan")
	peer := testSession(t)
	h.d.recentAgentExits = func(session string, now, window int64) ([]bus.ExitSighting, error) {
		return bus.AgentExitsIn([]string{session, peer}, now, window), nil
	}

	h.sweep(1)
	if n := len(massExitEvents(t, h.d.session)); n != 0 {
		t.Fatalf("mass exit raised before the peer died: %d events", n)
	}
	if n := editEvents(t, h.d.session, "agent-down"); n != 1 {
		t.Fatalf("agent-down alerts before any burst = %d, want plan's 1 — a death not yet correlated is never held back", n)
	}

	if err := bus.RecordAgentExit(peer, "commit", time.Now().Unix()); err != nil {
		t.Fatalf("RecordAgentExit: %v", err)
	}
	h.sweep(1)

	events := massExitEvents(t, h.d.session)
	if len(events) != 1 {
		t.Fatalf("mass-agent-exit events = %d, want 1", len(events))
	}
	for _, want := range []string{h.d.session + ": plan", peer + ": commit"} {
		if !strings.Contains(events[0].Payload, want) {
			t.Errorf("event %q missing %q", events[0].Payload, want)
		}
	}
}

// Only Claude agents are correlated: the 2026-09-02 exit spared every
// OpenCode agent, so their deaths are a different event.
func TestCheckAgentHealth_NonClaudeDeathsNotCorrelated(t *testing.T) {
	t.Setenv("MUXCODE_COMMIT_CLI", "opencode")
	t.Setenv("MUXCODE_RUN_CLI", "opencode")
	h := newResumeHarness(t, "")
	h.d.agentAlive = func(_, role string) bool { return role != "commit" && role != "run" }

	h.sweep(3)

	if n := len(massExitEvents(t, h.d.session)); n != 0 {
		t.Fatalf("two OpenCode deaths raised %d mass-agent-exit events", n)
	}
	if !h.d.agentWasDown["commit"] || !h.d.agentWasDown["run"] {
		t.Fatal("fixture never marked the OpenCode roles down — assertion above is vacuous")
	}
	if n := editEvents(t, h.d.session, "agent-down"); n != 2 {
		t.Errorf("agent-down alerts = %d, want 2 — uncorrelated deaths keep their own alerts", n)
	}
}
