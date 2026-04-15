package harness

import (
	"strings"
	"testing"
	"time"
)

func TestTUISink_AppendEvent(t *testing.T) {
	tui := NewTUISink("build", "gemma4:latest")

	// Add events within capacity
	for i := 0; i < 10; i++ {
		tui.appendEvent(Event{
			Kind:    EventStartup,
			Time:    time.Now(),
			Message: "test event",
		})
	}
	if len(tui.ring) != 10 {
		t.Errorf("expected 10 events, got %d", len(tui.ring))
	}
}

func TestTUISink_RingEviction(t *testing.T) {
	tui := NewTUISink("build", "gemma4:latest")
	tui.ringCap = 20

	// Fill beyond capacity
	for i := 0; i < 25; i++ {
		tui.appendEvent(Event{
			Kind:    EventStartup,
			Time:    time.Now(),
			Message: "event",
		})
	}

	if len(tui.ring) > 20 {
		t.Errorf("ring buffer exceeded capacity: %d > 20", len(tui.ring))
	}
}

func TestTUISink_UpdateStats(t *testing.T) {
	tui := NewTUISink("build", "gemma4:latest")

	tui.updateStats(Event{Kind: EventStartup})
	if tui.stats.Status != "Idle" {
		t.Errorf("expected Idle after startup, got %s", tui.stats.Status)
	}

	tui.updateStats(Event{Kind: EventBatchStart})
	if tui.stats.Status != "Processing" {
		t.Errorf("expected Processing after batch start, got %s", tui.stats.Status)
	}
	if tui.stats.BatchCount != 1 {
		t.Errorf("expected batch count 1, got %d", tui.stats.BatchCount)
	}

	tui.updateStats(Event{Kind: EventOllamaCall})
	if tui.stats.TurnCount != 1 {
		t.Errorf("expected turn count 1, got %d", tui.stats.TurnCount)
	}

	tui.updateStats(Event{Kind: EventBatchComplete})
	if tui.stats.Status != "Idle" {
		t.Errorf("expected Idle after batch complete, got %s", tui.stats.Status)
	}

	tui.updateStats(Event{Kind: EventCooldown})
	if tui.stats.Status != "Cooldown" {
		t.Errorf("expected Cooldown, got %s", tui.stats.Status)
	}
	if tui.stats.ConsecutiveFailures != 1 {
		t.Errorf("expected 1 consecutive failure, got %d", tui.stats.ConsecutiveFailures)
	}

	// Repeated cooldown events should NOT inflate the counter
	tui.updateStats(Event{Kind: EventCooldown})
	tui.updateStats(Event{Kind: EventCooldown})
	if tui.stats.ConsecutiveFailures != 1 {
		t.Errorf("repeated cooldowns should not inflate counter, got %d", tui.stats.ConsecutiveFailures)
	}

	// Transition out then back into cooldown should increment again
	tui.updateStats(Event{Kind: EventBatchStart})
	tui.updateStats(Event{Kind: EventCooldown})
	if tui.stats.ConsecutiveFailures != 2 {
		t.Errorf("expected 2 after re-entering cooldown, got %d", tui.stats.ConsecutiveFailures)
	}
}

func TestTUISink_CloseIdempotent(t *testing.T) {
	tui := NewTUISink("build", "gemma4:latest")

	// Should not panic on double close
	tui.Close()
	tui.Close()
}

func TestTUISink_EmitNonBlocking(t *testing.T) {
	tui := NewTUISink("build", "gemma4:latest")

	// Fill the channel
	for i := 0; i < 300; i++ {
		tui.Emit(Event{Kind: EventStartup, Message: "flood"})
	}
	// Should not block or panic — extras are dropped
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m30s"},
		{5*time.Minute + 12*time.Second, "5m12s"},
		{time.Hour + 23*time.Minute, "1h23m"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFormatEvent(t *testing.T) {
	tui := NewTUISink("build", "gemma4:latest")
	e := Event{
		Kind:    EventToolComplete,
		Time:    time.Date(2026, 1, 15, 14, 23, 6, 0, time.UTC),
		Message: "bash (1.8s)",
	}
	result := tui.formatEvent(e, 80)
	if result == "" {
		t.Error("formatEvent returned empty string")
	}
	// Should contain the timestamp
	if !containsVisible(result, "14:23:06") {
		t.Errorf("expected timestamp in output, got: %s", result)
	}
}

func TestTuiVisibleWidth(t *testing.T) {
	plain := "hello world"
	if w := tuiVisibleWidth(plain); w != 11 {
		t.Errorf("expected 11, got %d", w)
	}

	colored := cGreen + "hello" + cRST + " world"
	if w := tuiVisibleWidth(colored); w != 11 {
		t.Errorf("expected 11 for colored string, got %d", w)
	}

	// Double-width characters: ⚡ (U+26A1) = 2 columns
	// " ⚡ MuxCode" = space(1) + ⚡(2) + space(1) + MuxCode(7) = 11
	wide := " ⚡ MuxCode"
	if w := tuiVisibleWidth(wide); w != 11 {
		t.Errorf("expected 11 for string with ⚡ (2-wide), got %d", w)
	}

	// Double-width with ANSI
	wideColored := cPurple + "⚡" + cRST + " test"
	if w := tuiVisibleWidth(wideColored); w != 7 {
		t.Errorf("expected 7 for colored string with ⚡, got %d", w)
	}
}

func TestRuneWidth(t *testing.T) {
	tests := []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{' ', 1},
		{'─', 1},   // box drawing
		{'●', 1},   // geometric shapes (U+25CF)
		{'✓', 1},   // check mark (U+2713)
		{'❯', 1},   // dingbats (U+276F)
		{'◀', 1},   // geometric shapes (U+25C0)
		{'⚡', 2},   // miscellaneous symbols (U+26A1)
		{'⚙', 2},   // miscellaneous symbols (U+2699)
		{'⚠', 2},   // miscellaneous symbols (U+26A0)
		{'⏸', 2},   // transport symbols (U+23F8)
	}
	for _, tt := range tests {
		if got := runeWidth(tt.r); got != tt.want {
			t.Errorf("runeWidth(%q U+%04X) = %d, want %d", tt.r, tt.r, got, tt.want)
		}
	}
}

func TestTuiTruncateAnsi(t *testing.T) {
	s := cGreen + "hello world" + cRST
	truncated := tuiTruncateAnsi(s, 5)
	if w := tuiVisibleWidth(truncated); w != 5 {
		t.Errorf("expected visible width 5, got %d", w)
	}

	// Truncate should not split a double-width char
	wide := "ab⚡cd"
	truncated = tuiTruncateAnsi(wide, 4)
	// "ab" = 2 cols, "⚡" = 2 cols → total 4, fits exactly
	if w := tuiVisibleWidth(truncated); w != 4 {
		t.Errorf("expected 4 for truncated wide string, got %d", w)
	}

	// Truncate at 3 should not include ⚡ (would need 4 cols)
	truncated = tuiTruncateAnsi(wide, 3)
	if w := tuiVisibleWidth(truncated); w != 2 {
		t.Errorf("expected 2 (only 'ab' fits), got %d", w)
	}
}

// containsVisible checks if the visible (non-ANSI) text contains substr.
func containsVisible(s, substr string) bool {
	// Strip ANSI codes and check
	visible := ""
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if j < len(runes) {
				i = j + 1
				continue
			}
		}
		visible += string(runes[i])
		i++
	}
	return len(visible) > 0 && len(substr) > 0 && strings.Contains(visible, substr)
}
