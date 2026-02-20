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
func Pad(s string, width int) string {
	visible := StripAnsi(s)
	vlen := len([]rune(visible))
	if vlen >= width {
		// Truncate by runes
		runes := []rune(visible)
		return string(runes[:width])
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
