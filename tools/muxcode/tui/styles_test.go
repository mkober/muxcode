package tui

import "testing"

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"with codes", Green + "hello" + RST, "hello"},
		{"plain", "hello", "hello"},
		{"empty", "", ""},
		{"multiple codes", Bold + Purple + "hi" + RST, "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripAnsi(tt.input)
			if got != tt.want {
				t.Errorf("StripAnsi(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPad_Shorter(t *testing.T) {
	got := Pad("abc", 10)
	if len(got) != 10 {
		t.Errorf("Pad(\"abc\", 10) len = %d, want 10", len(got))
	}
	if got != "abc       " {
		t.Errorf("Pad(\"abc\", 10) = %q", got)
	}
}

func TestPad_Longer(t *testing.T) {
	got := Pad("abcdefghij", 5)
	if got != "abcde" {
		t.Errorf("Pad(\"abcdefghij\", 5) = %q, want \"abcde\"", got)
	}
}

func TestPad_WithAnsi(t *testing.T) {
	input := Green + "hi" + RST
	got := Pad(input, 5)
	// Should pad based on visible "hi" (2 chars) → 3 spaces appended
	visible := StripAnsi(got)
	visRunes := []rune(visible)
	if len(visRunes) != 5 {
		t.Errorf("visible length = %d, want 5 (got %q)", len(visRunes), visible)
	}
}

func TestPad_Exact(t *testing.T) {
	got := Pad("abcde", 5)
	if got != "abcde" {
		t.Errorf("Pad(\"abcde\", 5) = %q, want \"abcde\"", got)
	}
}

func TestTruncateAnsi_PlainText(t *testing.T) {
	got := TruncateAnsi("abcdefghij", 5)
	if got != "abcde" {
		t.Errorf("TruncateAnsi plain = %q, want %q", got, "abcde")
	}
}

func TestTruncateAnsi_WithAnsi(t *testing.T) {
	input := Green + "abcdefghij" + RST
	got := TruncateAnsi(input, 5)
	visible := StripAnsi(got)
	if visible != "abcde" {
		t.Errorf("TruncateAnsi visible = %q, want %q", visible, "abcde")
	}
	// Should preserve the leading ANSI code and append RST
	if got == "abcde" {
		t.Error("TruncateAnsi should preserve ANSI codes, got plain text")
	}
}

func TestTruncateAnsi_MixedAnsi(t *testing.T) {
	// "ab" in green, then "cd" in red, then "ef" plain — truncate to 4
	input := Green + "ab" + RST + Red + "cd" + RST + "ef"
	got := TruncateAnsi(input, 4)
	visible := StripAnsi(got)
	if visible != "abcd" {
		t.Errorf("TruncateAnsi mixed visible = %q, want %q", visible, "abcd")
	}
}

func TestTruncateAnsi_ZeroWidth(t *testing.T) {
	got := TruncateAnsi("hello", 0)
	if got != "" {
		t.Errorf("TruncateAnsi zero = %q, want empty", got)
	}
}

func TestTruncateAnsi_AnsiOnly(t *testing.T) {
	input := Green + RST
	got := TruncateAnsi(input, 5)
	// No visible chars to truncate — returns the ANSI codes as-is
	visible := StripAnsi(got)
	if visible != "" {
		t.Errorf("TruncateAnsi ansi-only visible = %q, want empty", visible)
	}
}

func TestTruncateAnsi_NoTruncation(t *testing.T) {
	input := Green + "hi" + RST
	got := TruncateAnsi(input, 10)
	// No truncation needed — should return original unchanged
	if got != input {
		t.Errorf("TruncateAnsi no-trunc = %q, want %q", got, input)
	}
}

func TestTruncateAnsi_PlainNoRST(t *testing.T) {
	// Plain text truncation should NOT append RST
	got := TruncateAnsi("hello world", 5)
	if got != "hello" {
		t.Errorf("TruncateAnsi plain no-RST = %q, want %q", got, "hello")
	}
}

func TestVisibleWidth_Plain(t *testing.T) {
	if got := VisibleWidth("hello"); got != 5 {
		t.Errorf("VisibleWidth plain = %d, want 5", got)
	}
}

func TestVisibleWidth_WithAnsi(t *testing.T) {
	input := Green + Bold + "hi" + RST
	if got := VisibleWidth(input); got != 2 {
		t.Errorf("VisibleWidth ansi = %d, want 2", got)
	}
}

func TestVisibleWidth_Empty(t *testing.T) {
	if got := VisibleWidth(""); got != 0 {
		t.Errorf("VisibleWidth empty = %d, want 0", got)
	}
}

func TestVisibleWidth_AnsiOnly(t *testing.T) {
	if got := VisibleWidth(Green + RST); got != 0 {
		t.Errorf("VisibleWidth ansi-only = %d, want 0", got)
	}
}

func TestHLine(t *testing.T) {
	got := HLine('─', 5)
	want := "─────"
	if got != want {
		t.Errorf("HLine('─', 5) = %q, want %q", got, want)
	}
}

func TestHLine_Zero(t *testing.T) {
	got := HLine('═', 0)
	if got != "" {
		t.Errorf("HLine('═', 0) = %q, want empty", got)
	}
}
