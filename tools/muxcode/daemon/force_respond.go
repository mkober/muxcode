package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The force-respond escalation ladder (MUX-105) recovers a role holding
// an un-receipted actionable request past a threshold — the state the
// 2026-08-26 build stall sat in for ~20 minutes while the single-shot
// backstop believed it had already re-driven delivery.
//
// The trigger is receipt absence, never elapsed activity: an agent that
// consumed its request (ack receipt) is legitimately working and is
// invisible to the ladder, however long it takes. Rungs, one per
// cooldown while the gap persists:
//
//	0 notify   — the routine wake path (bus.Notify)
//	1 deliver  — bus.ForceDeliver without force: robust path, idle-gated
//	2 override — bus.ForceDeliver with force: bypasses the idle gate and
//	             the in-flight-task skip. For hook providers only, it is
//	             postponed while the pane is actively generating —
//	             injecting mid-run reads as a user interrupt (the MUX-103
//	             incident) — and escalates past after
//	             frOverridePostponeMax postponements. Non-hook providers
//	             skip that gate: their IsIdle is unconditionally false
//	             (gating would make the override permanently inert for
//	             the exact class the incident hit), and their
//	             verified-inject path already declines to consume when
//	             text parks in a busy composer.
//	3 alert    — edit receives the full escalation history, then the
//	             ladder holds until the gap clears.
//
// A rung succeeds only when a receipt appears on a later sweep — command
// return values never advance the ladder to "recovered". Every rung
// emits its own lifecycle event. checkPollHealth's single re-drive
// deliberately overlaps rungs 0–1: it is the fast first responder at a
// lower threshold, the ladder the patient escalation behind it. Opt out
// with MUXCODE_FORCE_RESPOND_DISABLE=1; threshold via
// MUXCODE_FORCE_RESPOND_SECS.
const (
	frRungNotify = iota
	frRungDeliver
	frRungOverride
	frRungAlert
	frRungDone // alert sent — hold until the gap clears

	forceRespondDefaultSecs      = 180 // un-receipted request age that opens an episode
	forceRespondRungCooldownSecs = 60  // default seconds between rungs
	frOverridePostponeMax        = 3   // active-pane postponements before escalating past override
)

func forceRespondDisabled() bool {
	return os.Getenv("MUXCODE_FORCE_RESPOND_DISABLE") == "1"
}

func forceRespondThresholdSecs() int64 {
	if v := os.Getenv("MUXCODE_FORCE_RESPOND_SECS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return forceRespondDefaultSecs
}

func forceRespondRungCooldown() int64 {
	if v := os.Getenv("MUXCODE_FORCE_RESPOND_RUNG_SECS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return forceRespondRungCooldownSecs
}

// frReset clears a role's ladder state — the episode ended (receipt
// landed, agent died, or the inbox drained).
func (d *Daemon) frReset(role string) {
	if d.frRung[role] != frRungNotify || len(d.frHistory[role]) > 0 {
		bus.ClearForceRespondState(d.session, role)
	}
	d.frRung[role] = frRungNotify
	d.frLastFire[role] = 0
	d.frPostponed[role] = 0
	d.frHistory[role] = nil
}

func (d *Daemon) checkForceRespond() {
	if !d.ackDeliveryActive() || forceRespondDisabled() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastForceRespondCheck < pollHealthIntervalSecs {
		return
	}
	d.lastForceRespondCheck = now

	windows := d.sessionWindows()
	threshold := time.Duration(forceRespondThresholdSecs()) * time.Second

	for _, role := range bus.KnownRoles {
		if bus.WindowForRole(role) != role {
			continue // hosted roles are covered by their host
		}
		if bus.IsReloading(d.session, role) || d.permBlocked[role] {
			continue
		}
		// Same liveness gates as checkPollHealth.
		if !roleHasWindow(windows, role) || !d.agentAlive(d.session, role) || !bus.HasActionableMessages(d.session, role) {
			d.frReset(role)
			continue
		}

		gap := bus.ReceiptGap(d.session, role, threshold)
		if len(gap) == 0 {
			d.frReset(role) // receipt landed (or nothing old enough) — episode over
			continue
		}
		if d.frRung[role] >= frRungDone {
			continue // alerted; hold until the gap clears
		}
		if d.frLastFire[role] != 0 && now-d.frLastFire[role] < forceRespondRungCooldown() {
			continue
		}
		d.fireForceRespondRung(role, now)
	}
}

// fireForceRespondRung executes the role's next rung and records it in
// the episode history for the final alert.
func (d *Daemon) fireForceRespondRung(role string, now int64) {
	rung := d.frRung[role]
	var event string
	var err error

	switch rung {
	case frRungNotify:
		event = "force-respond-notify"
		err = d.frNotify(d.session, role)
	case frRungDeliver:
		event = "force-respond-deliver"
		err = d.frDeliver(d.session, role, false)
	case frRungOverride:
		// Pane-idle gate is hook-only — see the override rung's doc above.
		paneGated := d.frPaneGated(role)
		if paneGated && !d.frIsIdle(d.session, role) && d.frPostponed[role] < frOverridePostponeMax {
			d.frPostponed[role]++
			d.frLastFire[role] = now
			d.frHistory[role] = append(d.frHistory[role],
				fmt.Sprintf("override postponed %d/%d (pane active)", d.frPostponed[role], frOverridePostponeMax))
			bus.LogLifecycle(d.session, "info", "daemon", "force-respond-postpone",
				fmt.Sprintf("%s: pane active, postponement %d/%d", role, d.frPostponed[role], frOverridePostponeMax))
			_ = bus.WriteForceRespondState(d.session, bus.ForceRespondState{
				Role: role, Rung: rung, History: d.frHistory[role],
			})
			return
		}
		event = "force-respond-override"
		if !paneGated || d.frIsIdle(d.session, role) {
			err = d.frDeliver(d.session, role, true)
		} else {
			err = fmt.Errorf("skipped: pane stayed active through %d postponements", d.frPostponed[role])
		}
	case frRungAlert:
		event = "force-respond-alert"
		history := strings.Join(d.frHistory[role], "; ")
		alert := fmt.Sprintf(
			"force-respond escalation exhausted for %s — request un-receipted, ladder history: [%s]. Manual: muxcode diagnose %s / muxcode deliver %s --force",
			role, history, role, role)
		msg := bus.NewMessage("daemon", "edit", "event", "force-respond", alert, "")
		err = bus.SendNoCC(d.session, msg)
	}

	stamp := time.Unix(now, 0).Format("15:04:05")
	if err != nil {
		d.frHistory[role] = append(d.frHistory[role], fmt.Sprintf("%s@%s (err: %v)", event, stamp, err))
	} else {
		d.frHistory[role] = append(d.frHistory[role], fmt.Sprintf("%s@%s", event, stamp))
	}
	bus.LogLifecycle(d.session, "info", "daemon", event,
		fmt.Sprintf("%s: rung %d fired (err: %v)", role, rung, err))

	d.frRung[role] = rung + 1
	d.frLastFire[role] = now

	// Persist for the TUI and daemon restarts — see ForceRespondState.
	_ = bus.WriteForceRespondState(d.session, bus.ForceRespondState{
		Role: role, Rung: d.frRung[role], History: d.frHistory[role],
	})
}
