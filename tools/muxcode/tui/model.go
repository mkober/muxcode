package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Dashboard is the main TUI model for the agent dashboard.
type Dashboard struct {
	session    string
	refresh    int
	windows    []string
	prevHashes map[string]string
	msgBuffer  *MessageBuffer
	keyCh      chan byte
}

// NewDashboard creates a new Dashboard instance.
// Windows are read from the tmux session; falls back to KnownRoles.
func NewDashboard(session string, refresh int) *Dashboard {
	windows := sessionWindows(session)
	if len(windows) == 0 {
		// Fallback: use all known roles
		windows = make([]string, len(bus.KnownRoles))
		copy(windows, bus.KnownRoles)
	}
	return &Dashboard{
		session:    session,
		refresh:    refresh,
		windows:    windows,
		prevHashes: make(map[string]string),
		msgBuffer:  NewMessageBuffer(5),
	}
}

// sessionWindows queries tmux for the list of windows in the session.
// All windows are included — the dashboard excludes itself by not being
// in the window list (it runs in a standalone terminal or tmux popup).
func sessionWindows(session string) []string {
	out, err := exec.Command("tmux", "list-windows", "-t", session, "-F", "#W").Output()
	if err != nil {
		return nil
	}
	var windows []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		w := strings.TrimSpace(line)
		if w != "" {
			windows = append(windows, w)
		}
	}
	return windows
}

// Run starts the main render loop.
func (d *Dashboard) Run() error {
	// Switch terminal to raw mode so any keypress is immediate.
	rawErr := exec.Command("stty", "raw", "-echo").Run()

	// Clear screen and hide cursor
	fmt.Print("\033[2J\033[H")
	fmt.Print("\033[?25l")

	// Set up signal handler for clean exit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start non-blocking key reader
	d.keyCh = make(chan byte, 16)
	go d.readKeys()

	defer d.cleanup(rawErr == nil)

	for {
		frame := d.render()

		// Move cursor to 0,0 and print frame
		fmt.Print("\033[H")
		fmt.Print(frame)
		// Clear to end of screen
		fmt.Print("\033[J")

		// Wait for refresh interval, checking for keys and signals
		deadline := time.After(time.Duration(d.refresh) * time.Second)

	waitLoop:
		for {
			select {
			case <-sigCh:
				return nil
			case <-d.keyCh:
				// Any keypress exits
				return nil
			case <-deadline:
				break waitLoop
			}
		}
	}
}

// readKeys reads single bytes from stdin in a loop, sending to keyCh.
func (d *Dashboard) readKeys() {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		d.keyCh <- buf[0]
	}
}

// cleanup restores the terminal to a usable state.
func (d *Dashboard) cleanup(restoreStty bool) {
	if restoreStty {
		_ = exec.Command("stty", "sane").Run()
	}
	fmt.Print("\033[?25h") // show cursor
	fmt.Print(RST)         // reset colors
	fmt.Print("\033[2J")   // clear screen
	fmt.Print("\033[H")    // move to top
}

// termHeight returns the terminal height, defaulting to 24.
func termHeight() int {
	out, err := exec.Command("tput", "lines").Output()
	if err == nil {
		s := strings.TrimSpace(string(out))
		if h, err := strconv.Atoi(s); err == nil && h > 0 {
			return h
		}
	}

	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err = cmd.Output()
	if err == nil {
		parts := strings.Fields(strings.TrimSpace(string(out)))
		if len(parts) == 2 {
			if h, err := strconv.Atoi(parts[0]); err == nil && h > 0 {
				return h
			}
		}
	}

	return 24
}

// termWidth returns the terminal width, defaulting to 62.
func termWidth() int {
	// Try tput cols first
	out, err := exec.Command("tput", "cols").Output()
	if err == nil {
		s := strings.TrimSpace(string(out))
		if w, err := strconv.Atoi(s); err == nil && w > 0 {
			return w
		}
	}

	// Fallback: stty size (returns "rows cols")
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err = cmd.Output()
	if err == nil {
		parts := strings.Fields(strings.TrimSpace(string(out)))
		if len(parts) == 2 {
			if w, err := strconv.Atoi(parts[1]); err == nil && w > 0 {
				return w
			}
		}
	}

	return 62
}

// render builds the complete dashboard frame as a single string.
func (d *Dashboard) render() string {
	W := termWidth()
	H := termHeight()
	if W < 10 {
		W = 10
	}

	var b strings.Builder
	lineCount := 0

	// writeLine writes a full-width line, padding or truncating to W visible chars.
	writeLine := func(content string) {
		vw := VisibleWidth(content)
		if vw > W {
			content = TruncateAnsi(content, W)
			vw = W
		}
		pad := W - vw
		b.WriteString(content)
		if pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteRune('\n')
		lineCount++
	}

	// hrLine writes a dim horizontal rule.
	hrLine := func() {
		writeLine(Comment + HLine('\u2500', W) + RST)
	}

	// ── Session info line ──
	right := fmt.Sprintf("Session: %s   %ds", d.session, d.refresh)
	rpad := W - len(right) - 2
	if rpad < 0 {
		rpad = 0
	}
	writeLine(fmt.Sprintf("%s%s%s%s",
		strings.Repeat(" ", rpad),
		Comment, right, RST))

	hrLine()

	// ── WORKFLOW section ──
	wfEntry := bus.ReadWorkflowState(d.session)
	if wfEntry.State != bus.StateIdle || wfEntry.Since > 0 {
		writeLine(fmt.Sprintf("  %s%s%s  %s",
			Orange+Bold, "WORKFLOW", RST,
			bus.FormatWorkflowStateCompact(wfEntry, W)))
		hrLine()
	}

	// ── AGENTS section ──
	writeLine(fmt.Sprintf("  %s%s%s", Orange+Bold, "AGENTS", RST))

	sessionCost := 0.0
	sessionTokens := 0

	for _, win := range d.windows {
		pane := PaneTarget(d.session, win)

		// Check if window exists
		windowExists := d.windowExists(win)
		if !windowExists {
			writeLine(fmt.Sprintf("  %so %s  --          -       -     window not found%s",
				Dim, Pad(win, 8), RST))
			continue
		}

		// Capture pane output
		fullOutput := CapturePaneExtended(d.session, pane)
		trimmed := trimOutput(fullOutput, 8)

		prevHash := d.prevHashes[win]
		status, newHash := DetectStatus(win, trimmed, prevHash)
		d.prevHashes[win] = newHash

		// Scan for inter-agent messages
		d.msgBuffer.ScanMessages(win, trimmed)

		// Extract cost
		agentCost := ExtractCost(fullOutput)
		costDisplay := "-"
		if agentCost != "" {
			costVal, err := strconv.ParseFloat(agentCost, 64)
			if err == nil {
				costDisplay = fmt.Sprintf("$%.2f", costVal)
				sessionCost += costVal
			}
		}

		// Extract tokens
		agentTokens := ExtractTokens(fullOutput)
		tokensDisplay := "-"
		if agentTokens != "" {
			tokensDisplay = agentTokens
			sessionTokens += TokensToRaw(agentTokens)
		}

		bullet := "*"
		if status.Status == "IDLE" {
			bullet = "o"
		}

		winPad := Pad(win, 8)
		statusPad := Pad(status.Status, 8)
		costPad := Pad(costDisplay, 7)
		tokensPad := Pad(tokensDisplay, 7)

		// Calculate snippet space
		prefixLen := 2 + 2 + 8 + 2 + 8 + 2 + 7 + 1 + 7 + 2
		snippetMax := W - prefixLen
		if snippetMax < 0 {
			snippetMax = 0
		}
		snip := status.Snippet
		if len([]rune(snip)) > snippetMax {
			snip = string([]rune(snip)[:snippetMax])
		}

		writeLine(fmt.Sprintf("  %s%s %s%s  %s%s%s%s  %s%s%s %s%s%s  %s%s%s",
			status.StatusColor, bullet, winPad, RST,
			status.StatusColor, Bold, statusPad, RST,
			Yellow, costPad, RST,
			Cyan, tokensPad, RST,
			Comment, snip, RST))
	}

	// Session total line
	totalFmt := fmt.Sprintf("$%.2f", sessionCost)
	totalTokensFmt := RawToCompact(sessionTokens)
	totalText := fmt.Sprintf("Session total: %s / %s tokens", totalFmt, totalTokensFmt)
	tpad := W - len(totalText) - 2
	if tpad < 0 {
		tpad = 0
	}
	writeLine(fmt.Sprintf("%s%s%s%s / %s tokens%s",
		strings.Repeat(" ", tpad),
		Yellow+Bold, "Session total: "+totalFmt, RST,
		Cyan+Bold+totalTokensFmt, RST))

	hrLine()

	// ── MESSAGE BUS section ──
	writeLine(fmt.Sprintf("  %s%s%s", Orange+Bold, "MESSAGE BUS", RST))
	busLines := RenderBus(d.session, W)
	for _, line := range busLines {
		writeLine(line)
	}

	hrLine()

	// ── TEAMS section ──
	writeLine(fmt.Sprintf("  %s%s%s", Orange+Bold, "TEAMS", RST))
	teamLines := RenderTeams()
	for _, line := range teamLines {
		writeLine(line)
	}

	hrLine()

	// ── MESSAGES section ──
	writeLine(fmt.Sprintf("  %s%s%s", Orange+Bold, "MESSAGES", RST))
	msgs := d.msgBuffer.Messages()
	if len(msgs) == 0 {
		writeLine(fmt.Sprintf("  %s(no recent messages)%s", Comment, RST))
	} else {
		for _, msg := range msgs {
			maxLen := W - 4
			truncated := msg
			if len([]rune(truncated)) > maxLen {
				truncated = string([]rune(truncated)[:maxLen])
			}
			writeLine(fmt.Sprintf("  %s%s%s", Comment, truncated, RST))
		}
	}

	// ── Fill remaining space + footer ──
	footer := "Press any key to close"
	// Reserve 1 line for footer
	remaining := H - lineCount - 1
	for i := 0; i < remaining; i++ {
		b.WriteString(strings.Repeat(" ", W))
		b.WriteRune('\n')
	}

	// Footer centered at bottom
	fpad := (W - len(footer)) / 2
	if fpad < 0 {
		fpad = 0
	}
	b.WriteString(fmt.Sprintf("%s%s%s%s",
		strings.Repeat(" ", fpad),
		Comment, footer, RST))

	return b.String()
}

// windowExists checks if a tmux window exists in the session.
func (d *Dashboard) windowExists(window string) bool {
	out, err := exec.Command("tmux", "list-windows", "-t", d.session, "-F", "#W").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == window {
			return true
		}
	}
	return false
}

// trimOutput filters empty lines and returns the last n lines.
func trimOutput(output string, n int) string {
	lines := strings.Split(output, "\n")
	var nonEmpty []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if len(nonEmpty) > n {
		nonEmpty = nonEmpty[len(nonEmpty)-n:]
	}
	return strings.Join(nonEmpty, "\n")
}
