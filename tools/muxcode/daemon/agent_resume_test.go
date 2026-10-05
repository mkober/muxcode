package daemon

import (
	"errors"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

const resumeTestID = "8a744341-11bf-440f-b5d2-49248447a9c0"

// resumeHarness is a daemon whose one dead role is dead, every seam that would
// reach tmux or the process table stubbed, and every restart, stop and
// resume-hint read recorded.
type resumeHarness struct {
	d        *Daemon
	restarts []string
	stops    []string
	hint     map[string]bool
}

func newResumeHarness(t *testing.T, dead string) *resumeHarness {
	t.Helper()
	for _, env := range []string{"MUXCODE_EDIT_CLI", "MUXCODE_PLAN_CLI"} {
		t.Setenv(env, "claude")
	}
	t.Setenv("MUXCODE_AUTO_RESUME_DISABLE", "")
	t.Setenv("MUXCODE_RESUME_FIRST_SIGHTING", "")
	t.Setenv("MUXCODE_EDIT_AUTO_RESTART_DISABLE", "")

	h := &resumeHarness{hint: map[string]bool{}}
	d := New(testSession(t), 5, 8)
	d.agentAlive = func(_, role string) bool { return role != dead }
	d.windowNames = ownWindows
	d.snapshotAgentDown = func(_, _ string) (string, error) { return "/tmp/snap", nil }
	d.probeDefinition = func(_, _ string) bus.DefinitionProbe { return bus.DefinitionPresent }
	d.capturePane = func(string, int) (string, error) { return "", nil }
	d.paneResumeID = func(_, role string) (string, bool) {
		if h.hint[role] {
			return resumeTestID, true
		}
		return "", false
	}
	d.restartAgent = func(_, role string) error {
		h.restarts = append(h.restarts, role)
		return nil
	}
	d.stopAgent = func(_, role string) error {
		h.stops = append(h.stops, role)
		return nil
	}
	h.d = d
	return h
}

func (h *resumeHarness) sweep(n int) {
	for i := 0; i < n; i++ {
		h.d.lastAgentHealthCheck = 0
		h.d.checkAgentHealth()
	}
}

// A pane showing a resumable exit banner is restarted on the sweep that first
// sees it — down-alerted and snapshotted first, as strike 2 would have — rather
// than after the 3-strike, 90s wait.
func TestCheckAgentHealth_FirstSightingResumes(t *testing.T) {
	h := newResumeHarness(t, "plan")
	h.hint["plan"] = true

	h.sweep(1)

	if len(h.restarts) != 1 || h.restarts[0] != "plan" {
		t.Fatalf("restarts after one sweep = %v, want [plan]", h.restarts)
	}
	if !h.d.agentWasDown["plan"] || h.d.agentRestarts["plan"] != 1 {
		t.Errorf("first sighting skipped strike-2 bookkeeping: wasDown=%v restarts=%d",
			h.d.agentWasDown["plan"], h.d.agentRestarts["plan"])
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-down-snapshot")); n != 1 {
		t.Errorf("agent-down-snapshot rows = %d, want 1 — evidence must precede the relaunch", n)
	}
}

// Negative controls: with no banner, the switch off, or the opt-out set, the
// role waits out the three strikes exactly as before.
func TestCheckAgentHealth_NoFirstSightingWithoutBannerOrWhenOff(t *testing.T) {
	cases := []struct {
		name, env, value string
		hint             bool
	}{
		{"no banner", "", "", false},
		{"first sighting off", "MUXCODE_RESUME_FIRST_SIGHTING", "0", true},
		{"auto-resume opt-out", "MUXCODE_AUTO_RESUME_DISABLE", "1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newResumeHarness(t, "plan")
			if c.env != "" {
				t.Setenv(c.env, c.value)
			}
			h.hint["plan"] = c.hint

			h.sweep(2)
			if len(h.restarts) != 0 {
				t.Fatalf("restarted before strike 3: %v", h.restarts)
			}
			h.sweep(1)
			if len(h.restarts) != 1 {
				t.Errorf("strike 3 restarts = %v, want [plan]", h.restarts)
			}
		})
	}
}

// MUX-139 Phase 2: a dead edit with no resumable session is left down and
// alerted — never fresh-launched, and without spending a restart attempt.
func TestCheckAgentHealth_EditWithoutSessionIsLeftDown(t *testing.T) {
	h := newResumeHarness(t, "edit")

	h.sweep(6)

	if len(h.restarts) != 0 {
		t.Fatalf("resume-only edit was relaunched: %v", h.restarts)
	}
	if n := h.d.agentRestarts["edit"]; n != 0 {
		t.Errorf("leaving edit down spent %d restart attempts", n)
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-resume-unavailable")); n != 2 {
		t.Errorf("agent-resume-unavailable rows over two strike-3 cycles = %d, want 2", n)
	}
	msgs, _ := bus.Peek(h.d.session, "edit")
	alerts := 0
	for _, m := range msgs {
		if m.Action == "agent-down" && strings.Contains(m.Payload, "resume-only") {
			alerts++
		}
	}
	if alerts != 1 {
		t.Errorf("resume-only alerts to edit = %d, want 1 (deduped over 600s)", alerts)
	}
}

// Negative controls for resume-only: a banner resumes edit on first sighting,
// and the opt-out restores today's fresh relaunch at strike 3.
func TestCheckAgentHealth_EditResumesOrOptsOut(t *testing.T) {
	t.Run("banner resumes", func(t *testing.T) {
		h := newResumeHarness(t, "edit")
		h.hint["edit"] = true
		h.sweep(1)
		if len(h.restarts) != 1 || h.restarts[0] != "edit" {
			t.Errorf("restarts = %v, want [edit]", h.restarts)
		}
	})
	t.Run("opt-out relaunches without a session", func(t *testing.T) {
		h := newResumeHarness(t, "edit")
		t.Setenv("MUXCODE_AUTO_RESUME_DISABLE", "1")
		h.sweep(3)
		if len(h.restarts) != 1 || h.restarts[0] != "edit" {
			t.Errorf("restarts = %v, want [edit]", h.restarts)
		}
	})
}

// definitionlessPane is a pane in which the session that came back after the
// exit banner announced its agent unavailable — the warning Claude prints even
// when --agents was on the argv, so the argv probe reads Present.
const definitionlessPane = "Resume this session with:\nclaude --resume " + resumeTestID +
	"\n$ muxcode agent launch plan --reason restart --resume " + resumeTestID +
	"\nAgent planner, which is no longer available (no agent by that name in this session); the agent's tool restrictions no longer apply.\n❯ "

// A relaunched agent that comes back without its definition is stopped and
// alerted, never announced recovered or left running unrestricted.
func TestCheckAgentHealth_RestartedWithoutDefinitionIsStopped(t *testing.T) {
	h := newResumeHarness(t, "plan")
	h.hint["plan"] = true
	h.sweep(1)

	h.d.agentAlive = func(_, _ string) bool { return true }
	h.d.capturePane = func(string, int) (string, error) { return definitionlessPane, nil }
	h.sweep(1)

	if len(h.stops) != 1 || h.stops[0] != "plan" {
		t.Fatalf("stops = %v, want [plan]", h.stops)
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-recovered")); n != 0 {
		t.Errorf("an unrestricted agent was announced recovered (%d rows)", n)
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-resume-unrestricted")); n != 1 {
		t.Errorf("agent-resume-unrestricted rows = %d, want 1", n)
	}
}

// unrestrictedAlerts counts edit's agent-resume-unrestricted events whose
// payload contains text.
func unrestrictedAlerts(t *testing.T, session, text string) int {
	t.Helper()
	msgs, _ := bus.Peek(session, "edit")
	n := 0
	for _, m := range msgs {
		if m.Action == "agent-resume-unrestricted" && strings.Contains(m.Payload, text) {
			n++
		}
	}
	return n
}

var errCapture = errors.New("capture-pane: no such pane")

// A failed capture is not a clean pane: recovery waits, the verification stays
// pending, and the warning the next capture reads is still refused.
func TestCheckAgentHealth_FailedCaptureDefersVerification(t *testing.T) {
	h := newResumeHarness(t, "plan")
	h.hint["plan"] = true
	h.sweep(1)

	h.d.agentAlive = func(_, _ string) bool { return true }
	h.d.capturePane = func(string, int) (string, error) { return "", errCapture }
	h.sweep(1)

	if n := len(lifecycleDetails(t, h.d.session, "agent-recovered")); n != 0 {
		t.Fatalf("recovered on a failed capture (%d rows)", n)
	}
	if !h.d.daemonRestarted["plan"] {
		t.Fatal("a failed capture dropped the pending verification")
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-resume-verify-deferred")); n != 1 {
		t.Errorf("agent-resume-verify-deferred rows = %d, want 1", n)
	}

	h.d.capturePane = func(string, int) (string, error) { return definitionlessPane, nil }
	h.sweep(1)

	if len(h.stops) != 1 || h.stops[0] != "plan" {
		t.Errorf("stops = %v, want [plan]", h.stops)
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-recovered")); n != 0 {
		t.Errorf("an unrestricted agent was announced recovered (%d rows)", n)
	}
}

// Negative control: a failed capture followed by a clean one recovers, once,
// on the clean one.
func TestCheckAgentHealth_FailedThenCleanCaptureRecovers(t *testing.T) {
	h := newResumeHarness(t, "plan")
	h.hint["plan"] = true
	h.sweep(1)

	h.d.agentAlive = func(_, _ string) bool { return true }
	h.d.capturePane = func(string, int) (string, error) { return "", errCapture }
	h.sweep(1)
	h.d.capturePane = func(string, int) (string, error) { return "❯ ", nil }
	h.sweep(2)

	if n := len(lifecycleDetails(t, h.d.session, "agent-recovered")); n != 1 {
		t.Errorf("agent-recovered rows = %d, want 1", n)
	}
	if len(h.stops) != 0 || h.d.daemonRestarted["plan"] {
		t.Errorf("clean capture left stops=%v pending=%v", h.stops, h.d.daemonRestarted["plan"])
	}
}

// A stop that errors with the agent still alive is reported as a failure, not
// a stop; the stop marker it wrote does not hide the agent, and the next sweep
// retries. Edit hears "stopped" only after the stop succeeds, and the role is
// never recovered or restarted in between.
func TestCheckAgentHealth_FailedStopIsRetriedNotClaimed(t *testing.T) {
	h := newResumeHarness(t, "plan")
	h.hint["plan"] = true
	h.sweep(1)

	h.d.agentAlive = func(_, _ string) bool { return true }
	h.d.capturePane = func(string, int) (string, error) { return definitionlessPane, nil }
	fail := true
	h.d.stopAgent = func(session, role string) error {
		h.stops = append(h.stops, role)
		if err := bus.MarkAgentStopped(session, role); err != nil {
			return err
		}
		if fail {
			return errors.New("agent plan did not exit after 12 seconds")
		}
		return nil
	}
	h.sweep(1)

	if n := unrestrictedAlerts(t, h.d.session, "has been stopped"); n != 0 {
		t.Fatalf("claimed stopped after a failed stop (%d alerts)", n)
	}
	if n := unrestrictedAlerts(t, h.d.session, "FAILED"); n != 1 {
		t.Errorf("stop-failed alerts = %d, want 1", n)
	}

	h.sweep(1)
	if len(h.stops) != 2 {
		t.Fatalf("stops = %v — the marker hid the still-running agent from the retry", h.stops)
	}
	if n := unrestrictedAlerts(t, h.d.session, "has been stopped"); n != 0 {
		t.Fatalf("claimed stopped while the retry still fails (%d alerts)", n)
	}

	fail = false
	h.sweep(2)

	if len(h.stops) != 3 {
		t.Errorf("stops = %v, want three attempts and none after confirmation", h.stops)
	}
	if n := unrestrictedAlerts(t, h.d.session, "has been stopped"); n != 1 {
		t.Errorf("stopped alerts after a successful retry = %d, want 1", n)
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-recovered")); n != 0 {
		t.Errorf("recovered while its stop was unconfirmed (%d rows)", n)
	}
	if len(h.restarts) != 1 {
		t.Errorf("restarts = %v, want only the original relaunch", h.restarts)
	}
}

// A stop that fails before writing its marker must not let the agent, once it
// dies, be relaunched fresh: the pending stop writes the marker and confirms.
func TestCheckAgentHealth_FailedMarkerThenDeathIsNotRestarted(t *testing.T) {
	h := newResumeHarness(t, "plan")
	h.hint["plan"] = true
	h.sweep(1)

	h.d.agentAlive = func(_, _ string) bool { return true }
	h.d.capturePane = func(string, int) (string, error) { return definitionlessPane, nil }
	h.d.stopAgent = func(_, role string) error {
		h.stops = append(h.stops, role)
		return errors.New("write stop marker: read-only file system")
	}
	h.sweep(1)

	h.d.agentAlive = func(_, role string) bool { return role != "plan" }
	h.sweep(4)

	if len(h.restarts) != 1 {
		t.Errorf("restarts = %v — a refused agent was relaunched after dying", h.restarts)
	}
	if !bus.IsAgentStopped(h.d.session, "plan") {
		t.Error("confirmed stop left no stop marker")
	}
	if n := unrestrictedAlerts(t, h.d.session, "has been stopped"); n != 1 {
		t.Errorf("stopped alerts = %d, want 1", n)
	}
}

// Negative control: the same warning printed by the session BEFORE its exit
// banner says nothing about the one running now, which recovers normally.
func TestCheckAgentHealth_OldDefinitionWarningIsIgnored(t *testing.T) {
	h := newResumeHarness(t, "plan")
	h.hint["plan"] = true
	h.sweep(1)

	stale := "the agent's tool restrictions no longer apply\n❯ \nResume this session with:\nclaude --resume " + resumeTestID +
		"\n$ muxcode agent launch plan --reason restart --resume " + resumeTestID + "\n❯ "
	h.d.agentAlive = func(_, _ string) bool { return true }
	h.d.capturePane = func(string, int) (string, error) { return stale, nil }
	h.sweep(1)

	if len(h.stops) != 0 {
		t.Fatalf("stopped on a warning from the previous session: %v", h.stops)
	}
	if n := len(lifecycleDetails(t, h.d.session, "agent-recovered")); n != 1 {
		t.Errorf("agent-recovered rows = %d, want 1", n)
	}
}
