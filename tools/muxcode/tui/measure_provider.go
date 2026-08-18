package tui

import "github.com/mkober/muxcode/tools/muxcode/bus"

// MeasureProviderSelect sizes the provider modal from the view the selector
// actually renders, rather than a copy of its layout kept in the bus package.
// The layout lives here and bus cannot import tui, so the measurer is defined
// alongside the renderer and attached to the modal registry by the caller.
//
// The selector filters and scrolls as the user navigates, but a popup cannot
// resize once open, so this measures the initial view — which already includes
// the widest furniture (the key-hint footer) and the full agent list up to the
// scroll limit the renderer applies.
//
// Returns (0, 0) when the active window cannot be resolved, which leaves the
// modal on its configured percentage rather than opening it at the minimum.
func MeasureProviderSelect(session string) (int, int) {
	if session == "" {
		return 0, 0
	}
	window, role, err := bus.ResolveActiveAgentWindow(session)
	if err != nil || !bus.IsKnownRole(role) {
		return 0, 0
	}
	ui := NewProviderSelectUI(session, role, window)
	if ui == nil {
		return 0, 0
	}
	return bus.MeasureText(ui.render())
}
