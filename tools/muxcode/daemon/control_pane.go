package daemon

import (
	"os"
	"strconv"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The control-pane supervisor (MUX-108): a dead pane is a respawn, not
// an alert, and a duplicate converges to one (EnsureControlPane). The
// first sweep runs a full interval after daemon start — the launcher
// owns launch-time creation, and a sweep mid-launch sees half-built
// windows as pane-less and double-creates (the 2026-08-26 duplicate).
// That sweep recycles live panes onto the freshly-installed binary only
// when they predate this daemon (an install restarted us under a live
// session); panes the launcher just built are already fresh.
const controlPaneCheckSecsDefault = 60

// controlPaneCheckSecs returns the sweep interval. The env override
// exists for hermetic tests that compress supervisor time.
func controlPaneCheckSecs() int64 {
	if v := os.Getenv("MUXCODE_CONTROL_PANE_CHECK_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return int64(n)
		}
	}
	return controlPaneCheckSecsDefault
}

func (d *Daemon) checkControlPanes() {
	if !bus.ControlPanesEnabled() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastPaneSupervise < controlPaneCheckSecs() {
		return
	}
	d.lastPaneSupervise = now

	recycle := false
	if !d.paneRecycleDone {
		d.paneRecycleDone = true
		recycle = bus.ControlPanesPredate(d.session, d.startedAt)
	}

	windows, err := d.windowNames(d.session)
	if err != nil {
		return
	}
	for _, win := range windows {
		if !bus.ControlPaneEnabledFor(win) {
			continue
		}
		if err := bus.EnsureControlPane(d.session, win, recycle); err != nil {
			bus.LogLifecycle(d.session, "warn", "daemon", "control-pane-respawn-failed",
				win+": "+err.Error())
		}
	}
}
