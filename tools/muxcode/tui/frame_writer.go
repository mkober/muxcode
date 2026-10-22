package tui

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode/utf8"
)

// frameWriter prints full-screen frames, writing only what changed since the
// last one: an identical frame writes nothing, and a changed row is rewritten
// from its first differing column. tmux 3.8 paints every pane write over an
// open display-menu (prefix + b) — no write style is clipped, and no format,
// hook or option tells a pane a menu is open — so writing less is the only
// defence. The menu survives every cell a frame leaves alone, and an age
// ticking right of it never reaches it; before this, each tick of a ticking
// age rewrote every row and painted over the whole menu.
//
// The first frame, a change in row count and a resize (SIGWINCH) repaint the
// whole frame instead: tmux reflows a resized pane's rows, so the last frame
// no longer describes the screen.
type frameWriter struct {
	out   io.Writer
	last  []string
	drawn bool
	winch chan os.Signal
}

func newFrameWriter() *frameWriter {
	w := &frameWriter{out: os.Stdout, winch: make(chan os.Signal, 1)}
	signal.Notify(w.winch, syscall.SIGWINCH)
	return w
}

// draw prints frame from the top-left corner, rewriting only changed rows
// once a frame has been drawn.
func (w *frameWriter) draw(frame string) {
	rows := strings.Split(strings.TrimSuffix(frame, "\n"), "\n")
	if !w.drawn || w.resized() || len(rows) != len(w.last) {
		fmt.Fprint(w.out, CursorHome+ClearFrame(frame)+ClearBelow)
		w.last, w.drawn = rows, true
		return
	}
	var b strings.Builder
	for i, row := range rows {
		if row == w.last[i] {
			continue
		}
		col, style, rest := rowChange(w.last[i], row)
		b.WriteString(CursorTo(i+1, col+1) + RST + style + rest + ClearEOL)
	}
	if b.Len() > 0 {
		fmt.Fprint(w.out, b.String())
	}
	w.last = rows
}

// resized reports whether a SIGWINCH arrived since the last draw.
func (w *frameWriter) resized() bool {
	select {
	case <-w.winch:
		return true
	default:
		return false
	}
}

// rowChange locates where row next first differs from prev: the visible
// column, the SGR attributes in force there, and next from that point on.
// It returns the whole row from column 0 unless every token before the
// change is SGR or a rune surely one cell wide, and the change starts a
// fresh cell in both rows — a combining mark there belongs to the cell
// before it, which a tail write would leave stale (e + U+0301 → e keeps the
// accent). A wrong column or boundary writes the tail over the wrong cells.
func rowChange(prev, next string) (col int, style, rest string) {
	shared := 0
	for shared < len(prev) && shared < len(next) && prev[shared] == next[shared] {
		shared++
	}
	i := 0
	for i < shared {
		n, sgr := tokenLen(next[i:])
		if n == 0 {
			return 0, "", next
		}
		if i+n > shared {
			break
		}
		switch {
		case sgr && next[i:i+n] == RST:
			style = ""
		case sgr:
			style += next[i : i+n]
		default:
			col++
		}
		i += n
	}
	if !startsCell(prev[i:]) || !startsCell(next[i:]) {
		return 0, "", next
	}
	return col, style, next[i:]
}

// startsCell reports whether s begins where a new terminal cell surely
// starts: past any SGR escapes, which occupy no cell, at its end or at a rune
// one cell wide.
func startsCell(s string) bool {
	for s != "" {
		n, sgr := tokenLen(s)
		if !sgr {
			return n > 0
		}
		s = s[n:]
	}
	return true
}

// tokenLen returns the byte length of the token at the start of s — an SGR
// escape (sgr true) or one rune surely a single cell wide — or 0 for anything
// else: another escape, a malformed one, or a rune of uncertain width.
func tokenLen(s string) (n int, sgr bool) {
	if s[0] != '\033' {
		r, size := utf8.DecodeRuneInString(s)
		if !narrowRune(r) {
			return 0, false
		}
		return size, false
	}
	if len(s) < 2 || s[1] != '[' {
		return 0, false
	}
	for i := 2; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 0x40 && c <= 0x7e:
			if c != 'm' {
				return 0, false
			}
			return i + 1, true
		case c < 0x20 || c > 0x3f:
			return 0, false
		}
	}
	return 0, false
}

// narrowRanges lists code points that take exactly one terminal cell: no
// wide, fullwidth, combining or format characters (Unicode 15 East Asian
// Width and category data). It covers the blocks the TUIs draw from; a rune
// outside it costs a whole-row rewrite, never a misplaced one.
var narrowRanges = [][2]rune{
	{0x0020, 0x007e}, {0x00a0, 0x00ac}, {0x00ae, 0x02ff},
	{0x2010, 0x2027}, {0x2190, 0x2319}, {0x232b, 0x23e8},
	{0x23f4, 0x2426}, {0x2460, 0x25fc}, {0x2680, 0x2692},
	{0x2694, 0x26a0}, {0x2706, 0x2709}, {0x270c, 0x2727},
	{0x2758, 0x2794}, {0x27c0, 0x2b1a},
}

func narrowRune(r rune) bool {
	for _, rg := range narrowRanges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}
