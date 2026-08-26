package cmd

import (
	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// init attaches the measurers whose content is rendered by the tui package.
// They cannot be declared in the bus registry itself: tui imports bus, so bus
// cannot import tui, and duplicating the layout into bus would let the copy
// drift from the renderer. cmd imports both, which makes it the one place the
// two can meet.
//
// bus populates its registry from its own init, and cmd imports bus, so bus is
// fully initialised before this runs.
func init() {
	if cfg, ok := bus.GetModal("provider"); ok {
		cfg.Measurer = tui.MeasureProviderSelect
		bus.RegisterModal(cfg)
	}
	// One measurer for all three graph popups: Tab cycles the surfaces
	// inside a single popup, so each is sized for the widest of them.
	for _, name := range []string{"graph-runs", "graph-launch", "graph-gates"} {
		if cfg, ok := bus.GetPopup(name); ok {
			cfg.Measurer = tui.MeasureGraphUI
			bus.RegisterPopup(cfg)
		}
	}
}
