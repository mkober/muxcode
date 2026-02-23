package tui

import (
	"regexp"
	"strings"
)

// Dracula palette — ANSI 256-color escape codes.
const (
	RST     = "\033[0m"
	Bold    = "\033[1m"
	Dim     = "\033[2m"
	FG      = "\033[38;5;253m"
	Purple  = "\033[38;5;141m"
	Green   = "\033[38;5;84m"
	Cyan    = "\033[38;5;117m"
	Pink    = "\033[38;5;212m"
	Yellow  = "\033[38;5;228m"
	Orange  = "\033[38;5;215m"
	Red     = "\033[38;5;203m"
	Comment = "\033[38;5;103m"
	BG      = "\033[48;5;236m"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Pad pads or truncates s to exactly width visible characters.
// ANSI codes are preserved when truncating.
func Pad(s string, width int) string {
	vlen := VisibleWidth(s)
	if vlen >= width {
		return TruncateAnsi(s, width)
	}
	return s + strings.Repeat(" ", width-vlen)
}

// HLine repeats ch width times.
func HLine(ch rune, width int) string {
	return strings.Repeat(string(ch), width)
}

// StripAnsi removes ANSI escape sequences for length calculation.
func StripAnsi(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// TruncateAnsi truncates s to maxWidth visible characters, preserving ANSI
// escape codes encountered before the cut point. RST is appended when
// truncation occurs to ensure no open color codes leak.
func TruncateAnsi(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var b strings.Builder
	visible := 0
	hasAnsi := false
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		// Pass through ANSI escape sequences without counting them.
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if j < len(runes) {
				b.WriteString(string(runes[i : j+1]))
				i = j + 1
				hasAnsi = true
				continue
			}
		}
		if visible >= maxWidth {
			break
		}
		b.WriteRune(runes[i])
		visible++
		i++
	}
	// Reset open ANSI codes only if the string contained them and we truncated.
	if hasAnsi && i < len(runes) {
		b.WriteString(RST)
	}
	return b.String()
}

// VisibleWidth returns the number of visible (non-ANSI) runes in s.
func VisibleWidth(s string) int {
	return len([]rune(StripAnsi(s)))
}
