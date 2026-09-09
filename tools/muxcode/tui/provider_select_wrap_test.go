package tui

import (
	"fmt"
	"strings"
	"testing"
)

// The live failure: "stop agent: agent research did not exit after 12 seconds"
// was emitted as one unwrapped line, so the terminal broke it at its own width
// and the text ran through the modal's border.
func TestRenderFailureRowWrapsToWidth(t *testing.T) {
	const width = 62
	err := fmt.Errorf("stop agent: agent research did not exit after 12 seconds")

	out := renderFailureRow("research", err, width)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if got := VisibleWidth(line); got > width {
			t.Errorf("line exceeds frame width %d (got %d): %q", width, got, StripAnsi(line))
		}
	}
	// Reassemble before comparing: wrapping legitimately splits the message
	// across lines, so a contiguous-substring check would fail on correct output.
	if got := strings.Join(strings.Fields(StripAnsi(out)), " "); got != "✗ research "+err.Error() {
		t.Errorf("wrapping altered the message:\n got %q\nwant %q", got, "✗ research "+err.Error())
	}
}

// Negative control: a short error must stay on one line, or a renderer that
// wrapped everything to one word per line would pass the width test above.
func TestRenderFailureRowKeepsShortErrorOnOneLine(t *testing.T) {
	out := renderFailureRow("build", fmt.Errorf("boom"), 62)
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n"); n != 0 {
		t.Errorf("short error must occupy one line, got %d extra:\n%s", n, StripAnsi(out))
	}
}

// Continuation lines align under the message, not the glyph, so the role column
// stays readable when several agents fail at once.
func TestRenderFailureRowIndentsContinuations(t *testing.T) {
	err := fmt.Errorf("stop agent: agent research did not exit after 12 seconds and then some more text")
	lines := strings.Split(strings.TrimRight(renderFailureRow("research", err, 50), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the message to wrap, got:\n%s", StripAnsi(strings.Join(lines, "\n")))
	}
	for _, line := range lines[1:] {
		if !strings.HasPrefix(StripAnsi(line), strings.Repeat(" ", failureRowIndent)) {
			t.Errorf("continuation not aligned: %q", StripAnsi(line))
		}
	}
}

// A word wider than the line is kept whole: cutting a path or a run id mid-token
// makes it unusable to the reader who needs to act on it.
func TestWrapWordsKeepsOverlongWordIntact(t *testing.T) {
	long := "graphs/run-0123456789abcdef/nodes/commit-gate.json"
	lines := wrapWords("see "+long, 20)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, long) {
		t.Errorf("overlong word was split:\n%s", joined)
	}
}

// Wrapping counts visible runes, not bytes. The words below are 4 runes each but
// 10 bytes, so a byte-length wrap breaks after one word where three fit — and it
// misjudges exactly the glyph-heavy rows (box drawing, status ticks) this TUI is
// built from. Asserted as a width bound rather than an exact layout so the test
// pins the property, not one arrangement of it.
func TestWrapWordsMeasuresVisibleRunesNotBytes(t *testing.T) {
	lines := wrapWords("✅✅✅✅ ✅✅✅✅ ✅✅✅✅", 14)
	if len(lines) != 1 {
		t.Errorf("byte-length wrap: got %d lines, want 1\n%q", len(lines), lines)
	}
	for _, line := range lines {
		if w := VisibleWidth(line); w > 14 {
			t.Errorf("line exceeds width: %d > 14 (%q)", w, line)
		}
	}
	// Negative control: genuinely overlong content must still wrap, or a wrapper
	// that returned one line unconditionally would pass the assertion above.
	if got := wrapWords("✅✅✅✅ ✅✅✅✅ ✅✅✅✅ ✅✅✅✅", 9); len(got) < 2 {
		t.Errorf("content wider than the line did not wrap: %q", got)
	}
}
