package tui

import (
	"bytes"
	"os"
	"syscall"
	"testing"
)

// An unchanged frame must write nothing: any write lands on top of an open
// tmux menu.
func TestFrameWriter_SkipsUnchangedFrame(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf}

	w.draw("runs\n")
	buf.Reset()
	w.draw("runs\n")

	if buf.Len() != 0 {
		t.Errorf("identical frame rewritten: %q", buf.String())
	}
}

// Negative control: a changed row must still be written, from its first
// differing column.
func TestFrameWriter_RewritesChangedRowFromFirstDifference(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf}

	w.draw("runs\n")
	buf.Reset()
	w.draw("runs: 1\n")

	if want := CursorTo(1, 5) + RST + ": 1" + ClearEOL; buf.String() != want {
		t.Errorf("changed frame = %q, want %q", buf.String(), want)
	}
}

// The 2026-10-22 Pending Gates overdraw: a resolved gate's age ticks every
// refresh, and rewriting the frame painted every row over the quick menu.
// Only the age's row may be written, starting at the age's column — past the
// multibyte ✓, counted as one cell — in the row's own colour.
func TestFrameWriter_TickingAgeLeavesOtherRowsAlone(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf}
	frame := func(age string) string {
		return "Prompt / Launch Graph / Graph Runs / Pending Gates\n" +
			"\n" +
			"  " + Comment + "✓ approved  merge-gate  " + age + " run-1" + RST + "\n"
	}

	w.draw(frame("11m27s"))
	buf.Reset()
	w.draw(frame("11m29s"))

	if want := CursorTo(3, 31) + RST + Comment + "9s run-1" + RST + ClearEOL; buf.String() != want {
		t.Errorf("ticking age wrote %q, want %q", buf.String(), want)
	}
}

func TestRowChange(t *testing.T) {
	tests := []struct {
		name, prev, next string
		col              int
		style, rest      string
	}{
		{"tail after a styled prefix", Purple + "ab" + "cd", Purple + "ab" + "xy", 2, Purple, "xy"},
		{"a reset in the prefix drops the style", Purple + "a" + RST + "bc", Purple + "a" + RST + "bX", 2, "", "X"},
		{"a shortened row keeps only the clear", "abc def", "abc", 3, "", ""},
		{"a change at the first byte is the whole row", "abc", "xbc", 0, "", "xbc"},
		{"a wide rune in the prefix is the whole row", "⚡ run 1s", "⚡ run 2s", 0, "", "⚡ run 2s"},
		{"a non-SGR escape in the prefix is the whole row", "a" + ClearEOL + "b1", "a" + ClearEOL + "b2", 0, "", "a" + ClearEOL + "b2"},
		{"a wide rune at the change is the whole row", "ab", "a⚡", 0, "", "a⚡"},
		{"an accent removed mid-row is the whole row", "éx", "ex", 0, "", "ex"},
		{"an accent removed at the end is the whole row", "é", "e", 0, "", "e"},
		{"an accent added is the whole row", "ex", "éx", 0, "", "éx"},
		{"an accent behind a style change is the whole row", "e" + Purple + "́x", "e" + RST + "x", 0, "", "e" + RST + "x"},
		{"a style change before a narrow rune is still a tail", "a" + Purple + "b", "a" + Green + "b", 1, "", Green + "b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			col, style, rest := rowChange(tt.prev, tt.next)
			if col != tt.col || style != tt.style || rest != tt.rest {
				t.Errorf("rowChange(%q, %q) = (%d, %q, %q), want (%d, %q, %q)",
					tt.prev, tt.next, col, style, rest, tt.col, tt.style, tt.rest)
			}
		})
	}
}

// A resize reflows the pane under the last frame, so the next frame must be
// repainted whole even though only one row changed.
func TestFrameWriter_ResizeRepaintsWholeFrame(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf, winch: make(chan os.Signal, 1)}

	w.draw("tabs\nage 1s\n")
	buf.Reset()
	w.winch <- syscall.SIGWINCH
	w.draw("tabs\nage 3s\n")

	if want := CursorHome + ClearFrame("tabs\nage 3s\n") + ClearBelow; buf.String() != want {
		t.Errorf("frame after resize = %q, want a full repaint %q", buf.String(), want)
	}
}

// A frame with a different row count has no row-for-row predecessor and is
// repainted whole.
func TestFrameWriter_RowCountChangeRepaintsWholeFrame(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf}

	w.draw("tabs\nrun 1\n")
	buf.Reset()
	w.draw("tabs\n")

	if want := CursorHome + ClearFrame("tabs\n") + ClearBelow; buf.String() != want {
		t.Errorf("shorter frame = %q, want a full repaint %q", buf.String(), want)
	}
}

// The first frame is always drawn, even an empty one, so the screen cleared at
// startup is never left without a frame.
func TestFrameWriter_DrawsFirstFrameEvenIfEmpty(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf}

	w.draw("")

	if buf.String() != CursorHome+ClearBelow {
		t.Errorf("first empty frame = %q", buf.String())
	}
}
