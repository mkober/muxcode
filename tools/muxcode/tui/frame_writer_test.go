package tui

import (
	"bytes"
	"os"
	"strings"
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

	if want := CursorHome + ClearFrame("tabs\nage 3s") + ClearBelow; buf.String() != want {
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

	if want := CursorHome + ClearFrame("tabs") + ClearBelow; buf.String() != want {
		t.Errorf("shorter frame = %q, want a full repaint %q", buf.String(), want)
	}
}

// The 2026-11-13 provider-selector duplicate rows: a repaint whose last row
// ended in a newline scrolled a pane it exactly filled, so every later row
// write landed a line low. A repaint must leave the cursor on its last row.
func TestFrameWriter_RepaintWritesNoNewlineAfterLastRow(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf, height: func() int { return 3 }}

	w.draw("agents\n> plan\n  edit\n")

	if got := strings.Count(buf.String(), "\n"); got != 2 {
		t.Errorf("3-row repaint wrote %d newlines, want 2: %q", got, buf.String())
	}
}

// A frame taller than the pane is cut to the pane, and a change in a row past
// it writes nothing, since that row is not on screen.
func TestFrameWriter_ClipsFrameTallerThanPane(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf, height: func() int { return 2 }}

	w.draw("a\nb\nc 1\n")
	if want := CursorHome + ClearFrame("a\nb") + ClearBelow; buf.String() != want {
		t.Errorf("tall frame = %q, want %q", buf.String(), want)
	}
	buf.Reset()
	w.draw("a\nb\nc 2\n")
	if buf.Len() != 0 {
		t.Errorf("change below the pane wrote %q", buf.String())
	}
}

// Negative control: a frame exactly the pane's height keeps every row.
func TestFrameWriter_KeepsFrameThatFitsPane(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf, height: func() int { return 3 }}

	w.draw("a\nb\nc\n")

	if want := CursorHome + ClearFrame("a\nb\nc") + ClearBelow; buf.String() != want {
		t.Errorf("fitting frame = %q, want %q", buf.String(), want)
	}
}

// A resize re-reads the pane height: a pane grown past the frame shows the
// rows the smaller pane clipped.
func TestFrameWriter_ResizeRereadsPaneHeight(t *testing.T) {
	var buf bytes.Buffer
	height := 1
	w := &frameWriter{out: &buf, winch: make(chan os.Signal, 1), height: func() int { return height }}

	w.draw("a\nb\n")
	buf.Reset()
	height = 2
	w.winch <- syscall.SIGWINCH
	w.draw("a\nb\n")

	if want := CursorHome + ClearFrame("a\nb") + ClearBelow; buf.String() != want {
		t.Errorf("frame after growing the pane = %q, want %q", buf.String(), want)
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
