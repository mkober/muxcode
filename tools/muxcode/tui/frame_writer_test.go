package tui

import (
	"bytes"
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

// Negative control: a changed frame — including a resize, which changes the
// rendered width or height — must repaint.
func TestFrameWriter_RedrawsChangedFrame(t *testing.T) {
	var buf bytes.Buffer
	w := &frameWriter{out: &buf}

	w.draw("runs\n")
	buf.Reset()
	w.draw("runs: 1\n")

	if want := CursorHome + "runs: 1" + ClearEOL + "\n" + ClearBelow; buf.String() != want {
		t.Errorf("changed frame = %q, want %q", buf.String(), want)
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
