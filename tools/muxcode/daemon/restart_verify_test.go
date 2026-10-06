package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

const restartReadyPane = "Resume this session with:\nclaude --resume x\n$ muxcode agent launch plan\n❯ "

func handOff(t *testing.T, session string, v bus.RestartVerification) {
	t.Helper()
	if err := bus.WriteRestartVerification(session, v); err != nil {
		t.Fatalf("WriteRestartVerification: %v", err)
	}
}

func restartRecord(t *testing.T, session, role string) bus.RestartVerification {
	t.Helper()
	v, ok, err := bus.ReadRestartVerification(session, role)
	if err != nil || !ok {
		t.Fatalf("record for %s: ok=%v err=%v", role, ok, err)
	}
	return v
}

// An operator-restarted pane that held a ready composer through the settle
// period is verified by the daemon with no stop.
func TestCheckRestartVerifications_ReadyPaneVerifies(t *testing.T) {
	h := newResumeHarness(t, "")
	now := time.Now().Unix()
	handOff(t, h.d.session, bus.RestartVerification{Role: "plan", Status: bus.RestartVerifyPending, RelaunchedAt: now - 10, ReadyAt: now - bus.RestartVerifySettleSecs})
	h.d.capturePane = func(string, int) (string, error) { return restartReadyPane, nil }

	h.d.checkRestartVerifications()

	if v := restartRecord(t, h.d.session, "plan"); v.Status != bus.RestartVerifyVerified {
		t.Fatalf("status = %s, want verified", v.Status)
	}
	if len(h.stops) != 0 {
		t.Errorf("a verified agent was stopped: %v", h.stops)
	}
}

// A late definition banner is contained; a failed stop stays stop-pending —
// told truthfully as FAILED, retried — and the retry that succeeds records
// stopped and says so. The health sweep leaves the role alone throughout.
func TestCheckRestartVerifications_StopFailureRetriedUntilConfirmed(t *testing.T) {
	h := newResumeHarness(t, "plan")
	restore := restartStopRetrySecs
	restartStopRetrySecs = 0
	t.Cleanup(func() { restartStopRetrySecs = restore })
	h.d.agentAlive = func(string, string) bool { return true }
	h.d.capturePane = func(string, int) (string, error) { return definitionlessPane, nil }
	stopErr := errors.New("pane busy")
	h.d.stopAgent = func(_, role string) error {
		h.stops = append(h.stops, role)
		return stopErr
	}
	handOff(t, h.d.session, bus.RestartVerification{Role: "plan", Status: bus.RestartVerifyPending, RelaunchedAt: time.Now().Unix()})

	h.d.checkRestartVerifications()
	if v := restartRecord(t, h.d.session, "plan"); v.Status != bus.RestartVerifyStopPending {
		t.Fatalf("after a failed stop: status = %s, want stop-pending", v.Status)
	}
	if n := unrestrictedAlerts(t, h.d.session, "FAILED"); n != 1 {
		t.Fatalf("FAILED alerts after a failed stop = %d, want 1", n)
	}
	if n := unrestrictedAlerts(t, h.d.session, "has been stopped"); n != 0 {
		t.Fatalf("a failed stop was reported as stopped (%d alerts)", n)
	}

	h.d.agentAlive = func(_, role string) bool { return role != "plan" }
	h.sweep(3)
	if len(h.restarts) != 0 {
		t.Fatalf("the health sweep restarted a role whose containment is pending: %v", h.restarts)
	}
	h.d.agentAlive = func(string, string) bool { return true }

	stopErr = nil
	h.d.checkRestartVerifications()
	if v := restartRecord(t, h.d.session, "plan"); v.Status != bus.RestartVerifyStopped {
		t.Fatalf("after the retry succeeded: status = %s, want stopped", v.Status)
	}
	if len(h.stops) != 2 {
		t.Errorf("stop attempts = %d, want 2 (failure, then the retry)", len(h.stops))
	}
	if n := unrestrictedAlerts(t, h.d.session, "has been stopped"); n != 1 {
		t.Errorf("stopped alerts after the retry = %d, want 1", n)
	}
	if n := unrestrictedAlerts(t, h.d.session, "FAILED"); n != 1 {
		t.Errorf("FAILED alerts = %d, want still 1 — a retry must not re-alert", n)
	}
}

// Negative control for the sweep skip: once the check has closed, a dead role
// is the health sweep's again and is restarted.
func TestCheckAgentHealth_ClosedRestartCheckReturnsRoleToSweep(t *testing.T) {
	h := newResumeHarness(t, "plan")
	handOff(t, h.d.session, bus.RestartVerification{Role: "plan", Status: bus.RestartVerifyStopped})

	h.sweep(3)

	if len(h.restarts) == 0 {
		t.Fatal("a role with only a terminal record was never restarted — the skip is not scoped to active checks")
	}
}

// The daemon's stop never lets a marker failure skip the termination.
func TestStopMarked_MarkerFailureStillTerminates(t *testing.T) {
	stopped := false
	err := stopMarked("s", "plan",
		func(string, string) error { return errors.New("read-only fs") },
		func(string, string) error { stopped = true; return nil })
	if !stopped {
		t.Fatal("a marker failure skipped the termination")
	}
	if err == nil || !strings.Contains(err.Error(), "read-only fs") {
		t.Errorf("err = %v, want the marker failure reported", err)
	}
	if err := stopMarked("s", "plan", func(string, string) error { return nil }, func(string, string) error { return nil }); err != nil {
		t.Errorf("clean mark and stop returned %v", err)
	}
}
