package bus

import "testing"

// stty prints "rows cols"; the parser returns (width, height) — the
// swap is the bug a naive reading would ship.
func TestParseSttySize(t *testing.T) {
	w, h, err := parseSttySize("48 200\n")
	if err != nil {
		t.Fatalf("parseSttySize: %v", err)
	}
	if w != 200 || h != 48 {
		t.Errorf("parseSttySize = %dx%d, want 200x48 (rows-first input must swap)", w, h)
	}

	for _, bad := range []string{"", "junk", "48", "0 200", "48 -1", "48 200 7"} {
		if _, _, err := parseSttySize(bad); err == nil {
			t.Errorf("parseSttySize(%q) must error", bad)
		}
	}
}
