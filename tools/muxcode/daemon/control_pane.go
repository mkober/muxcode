package daemon

import (
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The control-pane supervisor (MUX-108): a dead pane is a respawn, not
// an alert. The first sweep after daemon start recycles live panes onto
// the freshly-installed binary — every install restarts the daemon, so
// hot-reload restart comes for free.
const controlPaneCheckSecs = 60

func (d *Daemon) checkControlPanes() {
	if !bus.ControlPanesEnabled() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastPaneSupervise < controlPaneCheckSecs {
		return
	}
	first := d.lastPaneSupervise == 0
	d.lastPaneSupervise = now

	windows, err := d.windowNames(d.session)
	if err != nil {
		return
	}
	for _, win := range windows {
		if !bus.ControlPaneEnabledFor(win) {
			continue
		}
		if err := bus.EnsureControlPane(d.session, win, first); err != nil {
			bus.LogLifecycle(d.session, "warn", "daemon", "control-pane-respawn-failed",
				win+": "+err.Error())
		}
	}
}
