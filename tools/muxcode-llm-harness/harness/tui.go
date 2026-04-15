package harness

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Dracula palette — duplicated from tools/muxcode/tui/styles.go
// (harness is a separate Go module with no shared deps).
const (
	cRST     = "\033[0m"
	cBold    = "\033[1m"
	cDim     = "\033[2m"
	cFG      = "\033[38;5;253m"
	cPurple  = "\033[38;5;141m"
	cGreen   = "\033[38;5;84m"
	cCyan    = "\033[38;5;117m"
	cPink    = "\033[38;5;212m"
	cYellow  = "\033[38;5;228m"
	cOrange  = "\033[38;5;215m"
	cRed     = "\033[38;5;203m"
	cComment = "\033[38;5;103m"
)

// Icon constants for activity log entries.
const (
	iconIn       = "←"
	iconOut      = "➜"
	iconTool     = "→"
	iconOK       = "✓"
	iconFail     = "✗"
	iconGear     = "⚙"
	iconDot      = "●"
	iconWarn     = "⚠"
	iconCooldown = "⏸"
	iconUser     = "❯"
	iconReply    = "◀"
)

// TUISink renders a live TUI in the terminal, consuming events from the
// harness loop via a channel. The render loop runs on the calling goroutine;
// events arrive from the harness goroutine.
type TUISink struct {
	role       string
	model      string
	eventCh    chan Event
	ring       []Event
	ringCap    int
	stats      tuiStats
	startAt    time.Time
	done       chan struct{}
	input      *tuiInput    // interactive text input state
	restoreTTY func()       // restores terminal on cleanup
}

type tuiStats struct {
	Status              string
	TurnCount           int
	BatchCount          int
	ConsecutiveFailures int
	LastFrom            string
	LastAction          string
}

// NewTUISink creates a TUI sink for the given role and model.
func NewTUISink(role, model string) *TUISink {
	return &TUISink{
		role:    role,
		model:   model,
		eventCh: make(chan Event, 256),
		ring:    make([]Event, 0, 500),
		ringCap: 500,
		startAt: time.Now(),
		done:    make(chan struct{}),
		input:   newTuiInput(),
		stats:   tuiStats{Status: "Starting"},
	}
}

// SubmitCh returns the channel that receives user-submitted chat messages.
// Wire this to Config.UserInput in main.go.
func (t *TUISink) SubmitCh() <-chan string {
	return t.input.submitCh
}

// Emit sends an event to the TUI render loop. Non-blocking; drops if full.
func (t *TUISink) Emit(e Event) {
	select {
	case t.eventCh <- e:
	default:
		// Drop event if channel is full (render will catch up)
	}
}

// Close signals the render loop to stop. Safe to call multiple times.
func (t *TUISink) Close() {
	select {
	case <-t.done:
		// Already closed
	default:
		close(t.done)
	}
}

// RunLoop enters the TUI render loop. Blocks until ctx is cancelled or
// Close() is called. Must be called on the main goroutine.
func (t *TUISink) RunLoop(ctx context.Context) {
	// Set up terminal (raw mode + clear screen)
	t.initTerminal()
	defer t.cleanupTerminal()

	// Start key reader goroutine (reads stdin in raw mode)
	go t.input.readKeys()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Drain all pending events before rendering
		t.drainEvents()

		// Process all pending key presses
		t.drainKeys()

		// Render frame
		t.render()

		select {
		case <-ctx.Done():
			return
		case <-t.done:
			return
		case <-t.input.quitCh:
			return
		case kp := <-t.input.keyCh:
			// Key press arrived — process it and re-render immediately.
			t.input.processKey(kp)
			continue
		case <-ticker.C:
			// Continue to next render cycle
		}
	}
}

// drainEvents reads all pending events from the channel and updates state.
func (t *TUISink) drainEvents() {
	for {
		select {
		case e := <-t.eventCh:
			t.appendEvent(e)
			t.updateStats(e)
		default:
			return
		}
	}
}

// drainKeys reads all pending key presses and processes them.
func (t *TUISink) drainKeys() {
	for {
		select {
		case kp := <-t.input.keyCh:
			t.input.processKey(kp)
		default:
			return
		}
	}
}

// appendEvent adds an event to the ring buffer, evicting oldest if full.
func (t *TUISink) appendEvent(e Event) {
	if len(t.ring) >= t.ringCap {
		// Evict oldest quarter to avoid constant shifting
		copy(t.ring, t.ring[t.ringCap/4:])
		t.ring = t.ring[:len(t.ring)-t.ringCap/4]
	}
	t.ring = append(t.ring, e)
}

// updateStats updates aggregate stats based on event kind.
func (t *TUISink) updateStats(e Event) {
	switch e.Kind {
	case EventStartup:
		t.stats.Status = "Idle"
	case EventMessageReceived:
		// Parsed from message field if needed
	case EventBatchStart:
		t.stats.Status = "Processing"
		t.stats.BatchCount++
	case EventOllamaCall:
		t.stats.Status = "Processing"
		t.stats.TurnCount++
	case EventBatchComplete:
		t.stats.Status = "Idle"
	case EventCooldown:
		// Only count the transition into cooldown, not repeated cooldown
		// poll events (the loop emits EventCooldown every PollInterval).
		if t.stats.Status != "Cooldown" {
			t.stats.ConsecutiveFailures++
		}
		t.stats.Status = "Cooldown"
	case EventError:
		t.stats.Status = "Error"
	case EventAllBlocked:
		// Keep processing status
	case EventUserInput:
		t.stats.Status = "Processing"
	case EventChatResponse:
		t.stats.Status = "Idle"
	}
}

// render builds and writes a full terminal frame.
func (t *TUISink) render() {
	w, h := tuiTermSize()
	if w < 20 || h < 10 {
		return
	}

	var b strings.Builder

	// Home cursor
	b.WriteString("\033[H")

	// --- Header ---
	t.renderHeader(&b, w)

	// --- Activity log (fills remaining space) ---
	headerLines := 2    // branding + separator
	inputLines := 2     // separator + input line
	statusBarLines := 2 // separator + status bar
	logLines := h - headerLines - inputLines - statusBarLines
	if logLines < 1 {
		logLines = 1
	}
	t.renderActivityLog(&b, w, logLines)

	// --- Input area ---
	t.renderInput(&b, w)

	// --- Status bar ---
	t.renderStatusBar(&b, w)

	// Clear remaining screen below
	b.WriteString("\033[J")

	// Position cursor at the input line
	inputRow := h - statusBarLines // input line row (1-indexed)
	text, cursor := t.input.getInputState()
	promptWidth := 3 // " ❯ " visible chars
	textWidth := w - promptWidth
	if textWidth < 1 {
		textWidth = 1
	}
	// Calculate horizontal scroll offset
	visibleStart := 0
	if cursor > textWidth-1 {
		visibleStart = cursor - textWidth + 1
	}
	_ = text // used for scroll calculation
	cursorCol := promptWidth + (cursor - visibleStart) + 1 // 1-indexed

	// Show cursor at input position
	b.WriteString(fmt.Sprintf("\033[%d;%dH\033[?25h", inputRow, cursorCol))

	os.Stdout.WriteString(b.String())
}

// renderHeader draws the branding line and separator.
func (t *TUISink) renderHeader(b *strings.Builder, w int) {
	// Branding line
	brand := fmt.Sprintf(" %s%s⚡ MuxCode LLM Harness%s", cBold, cPurple, cRST)
	b.WriteString(tuiPad(brand, w))
	b.WriteByte('\n')

	// Separator
	b.WriteString(cComment)
	b.WriteString(strings.Repeat("─", w))
	b.WriteString(cRST)
	b.WriteByte('\n')
}

// renderActivityLog draws the scrolling event log.
func (t *TUISink) renderActivityLog(b *strings.Builder, w, lines int) {
	// Show the last N events that fit
	start := 0
	if len(t.ring) > lines {
		start = len(t.ring) - lines
	}

	rendered := 0
	for i := start; i < len(t.ring); i++ {
		line := t.formatEvent(t.ring[i], w)
		b.WriteString(tuiPad(line, w))
		b.WriteByte('\n')
		rendered++
	}

	// Fill remaining lines with blanks
	for i := rendered; i < lines; i++ {
		b.WriteString(strings.Repeat(" ", w))
		b.WriteByte('\n')
	}
}

// renderInput draws the text input area (separator + input line).
func (t *TUISink) renderInput(b *strings.Builder, w int) {
	// Separator
	b.WriteString(cComment)
	b.WriteString(strings.Repeat("─", w))
	b.WriteString(cRST)
	b.WriteByte('\n')

	// Build input line: " ❯ <text>"
	text, cursor := t.input.getInputState()
	promptWidth := 3 // " ❯ " = 3 visible chars
	textWidth := w - promptWidth
	if textWidth < 1 {
		textWidth = 1
	}

	// Horizontal scroll: keep cursor visible within the text area
	visibleStart := 0
	if cursor > textWidth-1 {
		visibleStart = cursor - textWidth + 1
	}
	visibleEnd := visibleStart + textWidth
	if visibleEnd > len(text) {
		visibleEnd = len(text)
	}

	visibleText := ""
	if visibleEnd > visibleStart {
		visibleText = string(text[visibleStart:visibleEnd])
	}

	prompt := fmt.Sprintf(" %s❯%s ", cPurple, cRST)
	line := fmt.Sprintf("%s%s%s%s", prompt, cFG, visibleText, cRST)
	b.WriteString(tuiPad(line, w))
	b.WriteByte('\n')
}

// renderStatusBar draws the bottom stats line.
func (t *TUISink) renderStatusBar(b *strings.Builder, w int) {
	// Separator
	b.WriteString(cComment)
	b.WriteString(strings.Repeat("─", w))
	b.WriteString(cRST)
	b.WriteByte('\n')

	// Role/model + status/uptime, right-aligned
	statusColor := cGreen
	statusIcon := iconDot
	switch t.stats.Status {
	case "Processing":
		statusColor = cCyan
		statusIcon = iconGear
	case "Error":
		statusColor = cRed
		statusIcon = iconFail
	case "Cooldown":
		statusColor = cOrange
		statusIcon = iconCooldown
	case "Starting":
		statusColor = cYellow
		statusIcon = iconGear
	}
	uptime := time.Since(t.startAt).Round(time.Second)

	left := fmt.Sprintf(" %s%s %s%s  %sUp: %s%s",
		statusColor, statusIcon, t.stats.Status, cRST,
		cComment, formatDuration(uptime), cRST)
	right := fmt.Sprintf("%s%s%s %s%s%s %sOllama%s ",
		cCyan, t.role, cRST,
		cYellow, t.model, cRST,
		cComment, cRST)
	leftW := tuiVisibleWidth(left)
	rightW := tuiVisibleWidth(right)
	gap := w - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	b.WriteString(left)
	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(right)
}

// formatEvent returns a colored, single-line representation of an event.
func (t *TUISink) formatEvent(e Event, maxWidth int) string {
	ts := e.Time.Format("15:04:05")
	// Budget: timestamp (8) + 2 spaces + icon (1-2) + space + message
	msgBudget := maxWidth - 14 // conservative
	if msgBudget < 10 {
		msgBudget = 10
	}

	msg := e.Message
	if msgRunes := []rune(msg); len(msgRunes) > msgBudget {
		msg = string(msgRunes[:msgBudget-1]) + "…"
	}

	switch e.Kind {
	case EventStartup:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cGreen, iconOK, msg, cRST)
	case EventMessageReceived:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cPurple, iconIn, msg, cRST)
	case EventBatchStart:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cCyan, iconGear, msg, cRST)
	case EventOllamaCall:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cYellow, iconGear, msg, cRST)
	case EventOllamaResponse:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cGreen, iconOK, msg, cRST)
	case EventToolStart:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cCyan, iconTool, msg, cRST)
	case EventToolComplete:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cGreen, iconOK, msg, cRST)
	case EventToolOutput:
		return fmt.Sprintf(" %s╰ %s%s", cComment, msg, cRST)
	case EventToolBlocked:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cRed, iconFail, msg, cRST)
	case EventTextResponse:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cFG, iconOut, msg, cRST)
	case EventBatchComplete:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cPink, iconOut, msg, cRST)
	case EventCooldown:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cOrange, iconCooldown, msg, cRST)
	case EventError:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cRed, iconFail, msg, cRST)
	case EventForceToolUse:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cYellow, iconWarn, msg, cRST)
	case EventNarrationRetry:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cYellow, iconWarn, msg, cRST)
	case EventAllBlocked:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cOrange, iconWarn, msg, cRST)
	case EventUserInput:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cPurple, iconUser, msg, cRST)
	case EventChatResponse:
		return fmt.Sprintf(" %s%s%s  %s%s %s%s", cComment, ts, cRST, cGreen, iconReply, msg, cRST)
	default:
		return fmt.Sprintf(" %s%s%s  %s", cComment, ts, cRST, msg)
	}
}

// --- Terminal helpers ---

func (t *TUISink) initTerminal() {
	// Enter raw mode for character-at-a-time input
	restore, err := sttyRawMode()
	if err == nil {
		t.restoreTTY = restore
	}
	// Enter alternate screen buffer (clean canvas, no scrollback pollution)
	// then clear and home cursor
	os.Stdout.WriteString("\033[?1049h\033[2J\033[H")
}

func (t *TUISink) cleanupTerminal() {
	// Show cursor, reset colors, leave alternate screen buffer
	os.Stdout.WriteString("\033[?25h" + cRST + "\033[?1049l")
	// Restore terminal mode
	if t.restoreTTY != nil {
		t.restoreTTY()
	}
}

// winsize is the kernel struct returned by the TIOCGWINSZ ioctl.
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// tuiTermSize returns the terminal (or tmux pane) dimensions by querying the
// stdout file descriptor directly via ioctl. This is more reliable than
// spawning tput/stty subprocesses, which can fail or return stale values in
// tmux panes. Falls back to 80×24 if the ioctl fails.
func tuiTermSize() (width, height int) {
	ws := &winsize{}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)
	if errno == 0 && ws.Col > 0 && ws.Row > 0 {
		return int(ws.Col), int(ws.Row)
	}
	return 80, 24
}

func tuiTermWidth() int {
	w, _ := tuiTermSize()
	return w
}

func tuiTermHeight() int {
	_, h := tuiTermSize()
	return h
}

// runeWidth returns the number of terminal columns a rune occupies.
// Most characters are 1 column; certain emoji and CJK ideographs are 2.
func runeWidth(r rune) int {
	// Common double-width symbols used in this TUI and general emoji
	switch {
	case r >= 0x1F000: // Supplemental Symbols, Emoticons, etc.
		return 2
	case r >= 0x2600 && r <= 0x26FF: // Miscellaneous Symbols (⚡ ⚙ ⚠ etc.)
		return 2
	case r >= 0x2700 && r <= 0x27BF: // Dingbats (❯ ❮ etc.)
		// Most dingbats are single-width; only a few are double.
		// ❯ (U+276F) is consistently 1-wide in monospace fonts.
		return 1
	case r >= 0x23E9 && r <= 0x23FA: // Transport/map symbols (⏩ ⏪ ⏸ etc.)
		return 2
	case r >= 0x3000 && r <= 0x9FFF: // CJK Unified Ideographs
		return 2
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return 2
	case r >= 0xFE30 && r <= 0xFE6F: // CJK Compatibility Forms
		return 2
	case r >= 0xFF01 && r <= 0xFF60: // Fullwidth Forms
		return 2
	case r >= 0xFFE0 && r <= 0xFFE6: // Fullwidth Signs
		return 2
	}
	return 1
}

// tuiPad pads or truncates s to exactly width terminal columns.
// ANSI escape codes are not counted toward width.
func tuiPad(s string, width int) string {
	vlen := tuiVisibleWidth(s)
	if vlen >= width {
		return tuiTruncateAnsi(s, width)
	}
	return s + strings.Repeat(" ", width-vlen)
}

// tuiVisibleWidth returns the number of terminal columns occupied by s,
// skipping ANSI escape codes and counting double-width characters as 2.
func tuiVisibleWidth(s string) int {
	visible := 0
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
		visible += runeWidth(runes[i])
		i++
	}
	return visible
}

// tuiTruncateAnsi truncates s to maxWidth terminal columns, preserving
// ANSI codes and accounting for double-width characters. Appends RST
// when truncation occurs.
func tuiTruncateAnsi(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var b strings.Builder
	visible := 0
	hasAnsi := false
	runes := []rune(s)
	i := 0
	for i < len(runes) {
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
		w := runeWidth(runes[i])
		if visible+w > maxWidth {
			break
		}
		b.WriteRune(runes[i])
		visible += w
		i++
	}
	if hasAnsi && i < len(runes) {
		b.WriteString(cRST)
	}
	return b.String()
}

// formatDuration formats a duration as compact human-readable (e.g. "1h23m", "5m12s").
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%02dm", h, m)
}
