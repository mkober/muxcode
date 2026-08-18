package bus

import (
	"os"
	"strings"
	"testing"
)

// TestSwitchSessionFormatMatchesScript pins the measurer's row format to the
// script that actually renders the popup. Sizing is computed from the Go
// format while the rows are drawn by bash, so a change to either alone would
// mis-size the popup with nothing failing.
func TestSwitchSessionFormatMatchesScript(t *testing.T) {
	b, err := os.ReadFile("../../../scripts/muxcode-switch-session.sh")
	if err != nil {
		t.Fatalf("reading switch-session script: %v", err)
	}
	if !strings.Contains(string(b), switchSessionRowFormat) {
		t.Errorf("script does not contain the measured row format %q —\n"+
			"the popup is sized from this format, so update whichever of the two drifted",
			switchSessionRowFormat)
	}
	if !strings.Contains(string(b), switchSessionHeader) {
		t.Errorf("script does not contain the measured header %q", switchSessionHeader)
	}
	if !strings.Contains(string(b), switchSessionPrompt) {
		t.Errorf("script does not contain the measured prompt %q", switchSessionPrompt)
	}
	if !strings.Contains(string(b), switchSessionListFormat) {
		t.Errorf("script queries tmux with a different -F string than the measurer:\nmeasured: %s",
			switchSessionListFormat)
	}
}

// TestParseSwitchSessionRows pins the row layout the popup is sized from,
// including the markers, without needing a tmux server.
func TestParseSwitchSessionRows(t *testing.T) {
	// name|windows|created|attached — 1755517440 is 2025-08-18 in UTC terms;
	// the row width is what matters here, not the wall-clock rendering.
	out := "muxcode|10|1755517440|attached\nother|3|1755517440|\nelsewhere|7|1755517440|attached\n"
	rows := parseSwitchSessionRows(out, "muxcode")

	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if !strings.HasSuffix(rows[0], " ●") {
		t.Errorf("current session row should end with the current marker, got %q", rows[0])
	}
	if strings.HasSuffix(rows[1], "●") || strings.HasSuffix(rows[1], "◆") {
		t.Errorf("detached session row should carry no marker, got %q", rows[1])
	}
	if !strings.HasSuffix(rows[2], " ◆") {
		t.Errorf("session attached elsewhere should carry the elsewhere marker, got %q", rows[2])
	}
	// Every row is padded to the same width by the %-20s field, so a short name
	// must not produce a narrower popup than a long one.
	w0, _ := MeasureLines(rows[:1])
	w1, _ := MeasureLines(rows[1:2])
	if w1 > w0 {
		t.Errorf("unmarked row (%d) wider than marked row (%d) — padding drifted", w1, w0)
	}
}

// TestMeasureSwitchSessionFitsOneRow is the negative control for the bug this
// fixes: a single short session must not resolve to the max-width cap.
func TestMeasureSwitchSessionFitsOneRow(t *testing.T) {
	rows := []string{"  muxcode              10 windows  Aug 18 11:44 ●"}
	lines := append([]string{switchSessionHeader, switchSessionPrompt},
		strings.Repeat(" ", switchSessionPointerCols)+rows[0])

	cols, n := MeasureLines(lines)
	if n != 3 {
		t.Errorf("rows = %d, want 3 (header + prompt + one session)", n)
	}
	// The row is ~50 columns; anything near the 160 cap means the measurer is
	// being bypassed, which is exactly the defect under test.
	if cols < 40 || cols > 60 {
		t.Errorf("cols = %d, want roughly the row width (40..60)", cols)
	}

	w, h := FitSize(cols, n, 317, 80)
	if w > 80 {
		t.Errorf("fitted width = %d, want a content-sized popup, not the cap", w)
	}
	if h > 12 {
		t.Errorf("fitted height = %d, want a few rows, not half the terminal", h)
	}
}

// TestFitSizeAppliesInnerPadding pins the universal gutter between content and
// border. Without it a popup is sized to exactly its longest line, which puts
// that line flush against the frame and reads as truncated.
func TestFitSizeAppliesInnerPadding(t *testing.T) {
	const contentW, contentH = 55, 20

	padded, paddedH := FitSize(contentW, contentH, 317, 80)

	t.Setenv("MUXCODE_MODAL_PAD_COLS", "0")
	t.Setenv("MUXCODE_MODAL_PAD_ROWS", "0")
	bare, bareH := FitSize(contentW, contentH, 317, 80)

	if padded-bare != defaultModalPadCols {
		t.Errorf("padded width %d vs unpadded %d — want a %d column gutter",
			padded, bare, defaultModalPadCols)
	}
	if paddedH-bareH != defaultModalPadRows {
		t.Errorf("padded height %d vs unpadded %d — want a %d row gutter",
			paddedH, bareH, defaultModalPadRows)
	}
	// Zero is a meaningful setting: content flush against the border.
	if bare != contentW+PopupChromeCols {
		t.Errorf("with padding off, width %d should be content plus chrome %d",
			bare, contentW+PopupChromeCols)
	}
}
