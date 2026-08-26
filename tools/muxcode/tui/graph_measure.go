package tui

import (
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// MeasureGraphUI sizes the graph popup for the widest of its three
// surfaces — Tab cycles between them inside one popup, and a popup
// cannot resize once open. Returns the (0, 0) sentinel when every
// surface measures empty, leaving the popup on its percentage fallback.
func MeasureGraphUI(session string) (cols, rows int) {
	now := time.Now()
	frames := []string{
		RenderRunListFrame(LoadRunListRows(session, now), 200, -1),
		RenderTemplateListFrame(bus.ListGraphTemplates(), 200, 0, ""),
		RenderGateQueueFrame(LoadPendingGates(session, now),
			LoadResolvedGates(session, now, resolvedGateHistoryLimit), 200, 0),
	}
	for _, f := range frames {
		c, r := measureContent(f)
		if c > cols {
			cols = c
		}
		if r > rows {
			rows = r
		}
	}
	if cols == 0 {
		return 0, 0
	}
	// The interactive loop draws a spacer, divider, and footer under the
	// frame; one more row keeps the final newline off the last pane row,
	// which would otherwise scroll the popup and shift the tab bar.
	return cols, rows + 4
}

// measureContent measures a frame's widest line, skipping horizontal
// rules: they are drawn at the full render width, so counting them makes
// every frame measure as wide as the width it was rendered at.
func measureContent(frame string) (cols, rows int) {
	for _, ln := range strings.Split(strings.TrimRight(frame, "\n"), "\n") {
		rows++
		plain := strings.TrimSpace(StripAnsi(ln))
		if plain != "" && strings.Trim(plain, "─") == "" {
			continue
		}
		if c, _ := bus.MeasureText(ln); c > cols {
			cols = c
		}
	}
	return cols, rows
}
