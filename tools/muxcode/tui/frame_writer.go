package tui

import (
	"fmt"
	"io"
	"os"
)

// frameWriter prints full-screen frames, skipping one identical to the last
// frame printed. A refresh tick otherwise rewrites every row, and tmux paints
// those writes over an open display-menu (prefix + b) wherever the pane has
// text. A size change alters the rendered frame, so a resize still repaints.
type frameWriter struct {
	out   io.Writer
	last  string
	drawn bool
}

func newFrameWriter() *frameWriter {
	return &frameWriter{out: os.Stdout}
}

// draw prints frame from the top-left corner unless it equals the last one.
func (w *frameWriter) draw(frame string) {
	if w.drawn && frame == w.last {
		return
	}
	fmt.Fprint(w.out, CursorHome+ClearFrame(frame)+ClearBelow)
	w.last, w.drawn = frame, true
}
