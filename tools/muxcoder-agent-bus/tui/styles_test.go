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
