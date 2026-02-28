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

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
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
	// Clear screen and hide cursor
	fmt.Print("\033[2J\033[H")
	fmt.Print("\033[?25l")

	// Set up signal handler for clean exit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start non-blocking key reader
	d.keyCh = make(chan byte, 16)
	go d.readKeys()

	defer d.cleanup()

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
			case key := <-d.keyCh:
				switch key {
				case 'q', 'Q':
					return nil
				case 'r', 'R':
					break waitLoop
				}
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
func (d *Dashboard) cleanup() {
	fmt.Print("\033[?25h") // show cursor
	fmt.Print(RST)        // reset colors
	fmt.Print("\033[2J")   // clear screen
	fmt.Print("\033[H")    // move to top
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
	inner := W - 2 // inside box (minus left + right border)
	if inner < 10 {
		inner = 10
	}

	var b strings.Builder

	border := Purple + Bold
	borderRst := RST

	// ── Top border ──
	b.WriteString(border)
	b.WriteRune('\u2554') // ╔
	b.WriteString(HLine('\u2550', inner))
	b.WriteRune('\u2557') // ╗
	b.WriteString(borderRst)
	b.WriteRune('\n')

	// ── Header ──
	title := "AGENT DASHBOARD"
	right := fmt.Sprintf("Session: %s   %ds", d.session, d.refresh)
	gap := inner - len(title) - len(right) - 4
	if gap < 1 {
		gap = 1
	}
	b.WriteString(border)
	b.WriteRune('\u2551') // ║
	b.WriteString(borderRst)
	b.WriteString("  ")
	b.WriteString(Pink + Bold + title + RST)
	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(Comment + right + RST)
	b.WriteString("  ")
	b.WriteString(border)
	b.WriteRune('\u2551') // ║
	b.WriteString(borderRst)
	b.WriteRune('\n')

	// ── Separator ──
	b.WriteString(d.separator(inner))

	// ── AGENTS section ──
	b.WriteString(d.sectionHeader("AGENTS", inner))

	sessionCost := 0.0
	sessionTokens := 0

	for _, win := range d.windows {
		pane := PaneTarget(d.session, win)

		// Check if window exists
		windowExists := d.windowExists(win)
		if !windowExists {
			line := fmt.Sprintf("  %so %s  --          -       -     window not found%s",
				Dim, Pad(win, 8), RST)
			b.WriteString(d.boxLine(line, inner))
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
		snippetMax := inner - prefixLen - 2
		if snippetMax < 0 {
			snippetMax = 0
		}
		snip := status.Snippet
		if len([]rune(snip)) > snippetMax {
			snip = string([]rune(snip)[:snippetMax])
		}
		snipLen := len([]rune(snip))
		trailing := inner - prefixLen - snipLen
		if trailing < 0 {
			trailing = 0
		}

		line := fmt.Sprintf("  %s%s %s%s  %s%s%s%s  %s%s%s %s%s%s  %s%s%s%s",
			status.StatusColor, bullet, winPad, RST,
			status.StatusColor, Bold, statusPad, RST,
			Yellow, costPad, RST,
			Cyan, tokensPad, RST,
			Comment, snip, RST,
			strings.Repeat(" ", trailing))
		b.WriteString(border)
		b.WriteRune('\u2551')
		b.WriteString(borderRst)
		b.WriteString(line)
		b.WriteString(border)
		b.WriteRune('\u2551')
		b.WriteString(borderRst)
		b.WriteRune('\n')
	}

	// Session total line
	totalFmt := fmt.Sprintf("$%.2f", sessionCost)
	totalTokensFmt := RawToCompact(sessionTokens)
	totalText := fmt.Sprintf("Session total: %s / %s tokens", totalFmt, totalTokensFmt)
	tpad := inner - len(totalText) - 2
	if tpad < 0 {
		tpad = 0
	}
	totalLine := fmt.Sprintf("%s%s%s%s / %s tokens%s  ",
		strings.Repeat(" ", tpad),
		Yellow+Bold, "Session total: "+totalFmt, RST,
		Cyan+Bold+totalTokensFmt, RST)
	b.WriteString(border)
	b.WriteRune('\u2551')
	b.WriteString(borderRst)
	b.WriteString(totalLine)
	b.WriteString(border)
	b.WriteRune('\u2551')
	b.WriteString(borderRst)
	b.WriteRune('\n')

	// ── Separator ──
	b.WriteString(d.separator(inner))

	// ── MESSAGE BUS section ──
	b.WriteString(d.sectionHeader("MESSAGE BUS", inner))
	busLines := RenderBus(d.session, inner)
	for _, line := range busLines {
		b.WriteString(d.boxLine(line, inner))
	}

	// ── Separator ──
	b.WriteString(d.separator(inner))

	// ── TEAMS section ──
	b.WriteString(d.sectionHeader("TEAMS", inner))
	teamLines := RenderTeams()
	for _, line := range teamLines {
		b.WriteString(d.boxLine(line, inner))
	}

	// ── Separator ──
	b.WriteString(d.separator(inner))

	// ── MESSAGES section ──
	b.WriteString(d.sectionHeader("MESSAGES", inner))
	msgs := d.msgBuffer.Messages()
	if len(msgs) == 0 {
		noMsg := fmt.Sprintf("  %s(no recent messages)%s", Comment, RST)
		b.WriteString(d.boxLine(noMsg, inner))
	} else {
		for _, msg := range msgs {
			maxLen := inner - 4
			truncated := msg
			if len([]rune(truncated)) > maxLen {
				truncated = string([]rune(truncated)[:maxLen])
			}
			line := fmt.Sprintf("  %s%s%s", Comment, truncated, RST)
			b.WriteString(d.boxLine(line, inner))
		}
	}

	// ── Separator ──
	b.WriteString(d.separator(inner))

	// ── Footer ──
	footer := "q: quit  r: refresh  F1-F8: jump to window"
	fpad := inner - len(footer) - 4
	if fpad < 0 {
		fpad = 0
	}
	footerLine := fmt.Sprintf("  %s%s%s%s  ", Comment, footer, RST, strings.Repeat(" ", fpad))
	b.WriteString(border)
	b.WriteRune('\u2551')
	b.WriteString(borderRst)
	b.WriteString(footerLine)
	b.WriteString(border)
	b.WriteRune('\u2551')
	b.WriteString(borderRst)
	b.WriteRune('\n')

	// ── Bottom border ──
	b.WriteString(border)
	b.WriteRune('\u255a') // ╚
	b.WriteString(HLine('\u2550', inner))
	b.WriteRune('\u255d') // ╝
	b.WriteString(borderRst)
	b.WriteRune('\n')

	return b.String()
}

// separator writes a ╠═══╣ divider line.
func (d *Dashboard) separator(inner int) string {
	border := Purple + Bold
	return fmt.Sprintf("%s\u2560%s\u2563%s\n",
		border, HLine('\u2550', inner), RST)
}

// sectionHeader writes a section title inside the box (e.g. "║  AGENTS  ║").
func (d *Dashboard) sectionHeader(title string, inner int) string {
	border := Purple + Bold
	pad := inner - len(title) - 4
	if pad < 0 {
		pad = 0
	}
	return fmt.Sprintf("%s\u2551%s  %s%s%s%s  %s\u2551%s\n",
		border, RST,
		Orange+Bold, title, RST,
		strings.Repeat(" ", pad),
		border, RST)
}

// boxLine wraps a content line inside ║...║, padding or truncating to inner width.
func (d *Dashboard) boxLine(content string, inner int) string {
	border := Purple + Bold
	plen := VisibleWidth(content)
	if plen > inner {
		content = TruncateAnsi(content, inner)
		plen = inner
	}
	padN := inner - plen
	return fmt.Sprintf("%s\u2551%s%s%s%s\u2551%s\n",
		border, RST,
		content,
		strings.Repeat(" ", padN),
		border, RST)
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
