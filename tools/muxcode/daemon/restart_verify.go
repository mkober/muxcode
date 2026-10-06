package daemon

import (
	"errors"
	"fmt"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// checkRestartVerifications runs every tick and owns each operator restart's
// definition check (bus.RestartVerification, MUX-139 Phase 5) from the moment
// the restarting process hands it off until a terminal status — regardless of
// whether that process (the restart modal, `reload --all --resume`) is still
// alive to read it. A pending record advances by bus.AdvanceRestartVerification;
// a relaunch that must be stopped is contained through stopUnrestricted and
// stays stop-pending, retried every tick, until the stop is confirmed. Edit is
// alerted once when containment starts failing and once when it succeeds.
// Terminal records the restarter never collected are pruned.
func (d *Daemon) checkRestartVerifications() {
	now := time.Now().Unix()
	for _, v := range bus.ListRestartVerifications(d.session) {
		if !v.Active() {
			if bus.RestartVerificationPrunable(v, now) {
				bus.ClearRestartVerification(d.session, v.Role)
			}
			continue
		}
		d.advanceRestartVerification(v, now)
	}
}

// restartStopRetrySecs spaces stop retries: GracefulStop blocks the poll loop
// for up to 10s, and the health sweep retries its own refused stops at the
// same cadence.
var restartStopRetrySecs int64 = 30

// stopMarked is the daemon's stop: marker, then termination. A marker failure
// is reported but never skips the termination — an agent being contained must
// be ended even when its marker cannot be written.
func stopMarked(session, role string, mark, stop func(session, role string) error) error {
	markErr := mark(session, role)
	return errors.Join(stop(session, role), markErr)
}

func (d *Daemon) advanceRestartVerification(v bus.RestartVerification, now int64) {
	role := v.Role
	if v.Status == bus.RestartVerifyStopPending && now-v.UpdatedAt < restartStopRetrySecs {
		return
	}
	alive := d.agentAlive(d.session, role)
	var content string
	var captureErr error
	if v.Status == bus.RestartVerifyPending {
		content, captureErr = d.capturePane(bus.PaneTarget(d.session, role), definitionBannerLines)
	}
	next, stop := bus.AdvanceRestartVerification(v, now, alive, content, captureErr)

	if stop {
		firstAttempt := v.Status == bus.RestartVerifyPending
		if firstAttempt {
			fmt.Printf("  %s  Agent %s restarted by the operator failed its definition check — stopping it\n", time.Now().Format("15:04:05"), role)
			bus.LogLifecycle(d.session, "error", "daemon", "agent-resume-unrestricted", role+": operator restart: "+next.Detail)
		}
		if err := d.stopUnrestricted(role); err != nil {
			next.Status = bus.RestartVerifyStopPending
			bus.LogLifecycle(d.session, "error", "daemon", "agent-resume-stop-failed", role+": operator restart: "+err.Error())
			if firstAttempt {
				d.alertUnrestricted(role, fmt.Sprintf(
					"%s was restarted from the Restart Agents control but its agent definition could not be verified (%s). Stopping it FAILED (%v): it may still be running unrestricted. The daemon retries the stop every tick and will not restart it; if this persists, end it by hand (/exit in its pane, or kill its process).", role, next.Detail, err))
			}
		} else {
			next.Status = bus.RestartVerifyStopped
			bus.LogLifecycle(d.session, "info", "daemon", "agent-resume-unrestricted-stopped", role+": operator restart")
			d.alertUnrestricted(role, fmt.Sprintf(
				"%s was restarted from the Restart Agents control but its agent definition could not be verified (%s). It has been stopped and will not be auto-restarted. Check the definition (make install), then: muxcode agent-health --start %s.", role, next.Detail, role))
		}
	} else if next.Status == bus.RestartVerifyVerified {
		bus.LogLifecycle(d.session, "info", "daemon", "agent-restart-verified", role)
	}

	if stop || next != v { // a stop attempt always persists, restamping the retry clock
		if err := bus.WriteRestartVerification(d.session, next); err != nil {
			bus.LogLifecycle(d.session, "warn", "daemon", "agent-restart-verify-write-failed", role+": "+err.Error())
		}
	}
}
