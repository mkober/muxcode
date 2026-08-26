package bus

import "testing"

func TestMeasureText(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantC, wantR int
	}{
		{"single line", "hello", 5, 1},
		{"longest line wins", "ab\nabcdef\nabc", 6, 3},
		{"trailing newline is not a line", "ab\ncd\n", 2, 2},
		{"empty is unavailable", "", 0, 0},
		{"blank lines are unavailable", "\n\n\n", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, r := MeasureText(tt.in)
			if c != tt.wantC || r != tt.wantR {
				t.Errorf("MeasureText(%q) = %d,%d want %d,%d", tt.in, c, r, tt.wantC, tt.wantR)
			}
		})
	}
}

// Colour bytes must not count as columns — the fit-tier renderers all emit
// Dracula escapes, so counting them would inflate every width several-fold and
// defeat the feature.
func TestMeasureText_IgnoresANSI(t *testing.T) {
	plain := "hello world"
	colored := ColorPurple + "hello " + ColorGreen + "world" + ColorReset

	wantC, _ := MeasureText(plain)
	gotC, gotR := MeasureText(colored)
	if gotC != wantC {
		t.Errorf("coloured text measured %d cols, want %d (same visible text)", gotC, wantC)
	}
	if gotR != 1 {
		t.Errorf("expected 1 row, got %d", gotR)
	}
}

// Width is visible columns, so a multi-byte glyph counts once, not once per byte.
func TestMeasureText_CountsRunesNotBytes(t *testing.T) {
	// Eight glyphs, but more than eight bytes in UTF-8.
	if c, _ := MeasureText("✓─┐│└✗●·"); c != 8 {
		t.Errorf("expected 8 columns for 8 glyphs, got %d", c)
	}
}

func TestMeasureLines_Empty(t *testing.T) {
	if c, r := MeasureLines(nil); c != 0 || r != 0 {
		t.Errorf("nil lines = %d,%d want 0,0", c, r)
	}
	if c, r := MeasureLines([]string{"", ""}); c != 0 || r != 0 {
		t.Errorf("blank lines = %d,%d want 0,0", c, r)
	}
}

// A measurer that cannot determine a size must return the (0, 0) sentinel so
// ResolveSize falls back to percentage defaults rather than sizing to nothing.
func TestMeasurers_UnknownSessionYieldsSentinel(t *testing.T) {
	measurers := map[string]ContentMeasurer{
		"remote sessions": MeasureRemoteSessions,
	}
	for name, m := range measurers {
		c, r := m("no-such-session-" + t.Name())
		if c < 0 || r < 0 {
			t.Errorf("%s returned negative size %d,%d", name, c, r)
		}
		if (c == 0) != (r == 0) {
			t.Errorf("%s returned half-empty size %d,%d — sentinel must be both or neither", name, c, r)
		}
	}
}
