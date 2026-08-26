package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// remoteView tracks which screen is active in the Remote TUI.
type remoteView int

const (
	viewSessionList remoteView = iota
	viewSessionDetail
)

// RemoteAction identifies what the user selected in the TUI.
type RemoteAction string

const (
	ActionCapture      RemoteAction = "capture"
	ActionInbox        RemoteAction = "inbox"
	ActionDiagnose     RemoteAction = "diagnose"
	ActionAllInboxes   RemoteAction = "all-inboxes"
	ActionDiagnoseAll  RemoteAction = "diagnose-all"
	ActionForceRespond RemoteAction = "force-respond"
)

// RemoteSelection is the result returned when the user picks an action.
// Nil means the user cancelled (q/Esc).
type RemoteSelection struct {
	Session string
	Role    string // empty for session-wide actions (all-inboxes, diagnose-all)
	Action  RemoteAction
}

// RemoteUI is the interactive TUI for investigating remote muxcode sessions.
type RemoteUI struct {
	currentSession string // the session the user is running in
	view           remoteView

	// Session list
	sessions   []bus.RemoteSession
	sessionIdx int // selected session index

	// Session detail
	detailSession string
	agents        []bus.AgentStatus
	agentIdx      int // selected agent in detail view

	// Result — set when user picks an action
	result *RemoteSelection

	// Force-respond confirm: a destructive action parked behind a y/n
	// prompt. Nothing dispatches without the confirm keypress (MUX-105).
	confirmPending *RemoteSelection

	keyCh chan byte
}

// NewRemoteUI creates a new Remote TUI.
func NewRemoteUI(currentSession string) *RemoteUI {
	return &RemoteUI{
		currentSession: currentSession,
	}
}

// Run starts the interactive Remote TUI loop.
// Returns the user's selection, or nil if cancelled.
func (ui *RemoteUI) Run() *RemoteSelection {
	rawCmd := exec.Command("stty", "-icanon", "-echo", "min", "1")
	rawCmd.Stdin = os.Stdin
	rawErr := rawCmd.Run()

	fmt.Print("\033[2J\033[H")
	fmt.Print("\033[?25l")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ui.keyCh = make(chan byte, 16)
	go ui.readKeys()

	defer ui.cleanup(rawErr == nil)

	// Initial data load
	ui.refreshSessions()

	for {
		frame := ui.render()
		fmt.Print("\033[H")
		fmt.Print(ClearFrame(frame))
		fmt.Print("\033[J")

		// Wait for key or auto-refresh (10s)
		deadline := time.After(10 * time.Second)

	waitLoop:
		for {
			select {
			case <-sigCh:
				return nil
			case key := <-ui.keyCh:
				action := ui.handleKey(key)
				if action == "quit" {
					return nil
				}
				if action == "selected" {
					return ui.result
				}
				break waitLoop
			case <-deadline:
				// Auto-refresh data
				if ui.view == viewSessionList {
					ui.refreshSessions()
				} else if ui.view == viewSessionDetail {
					ui.refreshDetail()
				}
				break waitLoop
			}
		}
	}
}

// refreshSessions reloads the session list from /tmp.
func (ui *RemoteUI) refreshSessions() {
	sessions, _ := bus.DiscoverSessions("", false)
	ui.sessions = sessions
	if ui.sessionIdx >= len(sessions) {
		ui.sessionIdx = len(sessions) - 1
	}
	if ui.sessionIdx < 0 {
		ui.sessionIdx = 0
	}
}

// refreshDetail reloads agent statuses for the selected session.
func (ui *RemoteUI) refreshDetail() {
	ui.agents = bus.GetAllAgentStatus(ui.detailSession)
	if ui.agentIdx >= len(ui.agents) {
		ui.agentIdx = len(ui.agents) - 1
	}
	if ui.agentIdx < 0 {
		ui.agentIdx = 0
	}
}

// handleKey processes a keypress and returns an action string.
// Returns "quit" to exit, "selected" when an action is chosen, "" otherwise.
func (ui *RemoteUI) handleKey(key byte) string {
	if ui.confirmPending != nil {
		switch key {
		case 'y':
			ui.result = ui.confirmPending
			ui.confirmPending = nil
			return "selected"
		case 'n', 'q', 27:
			ui.confirmPending = nil
		}
		return ""
	}
	switch key {
	case 'q':
		if ui.view == viewSessionList {
			return "quit"
		}
		// Back navigation
		if ui.view == viewSessionDetail {
			ui.view = viewSessionList
			ui.agentIdx = 0
		}
		return ""

	case 27: // Escape or arrow key
		return ui.handleEscapeSequence()

	case 'j', 14: // j or Ctrl-N — down
		ui.moveDown()
	case 'k', 16: // k or Ctrl-P — up
		ui.moveUp()

	case 10, 13: // Enter
		return ui.handleEnter()

	case 'c': // capture
		if ui.view == viewSessionDetail && len(ui.agents) > 0 {
			return ui.selectAction(ActionCapture)
		}
	case 'i': // inbox
		if ui.view == viewSessionDetail && len(ui.agents) > 0 {
			return ui.selectAction(ActionInbox)
		}
	case 'd': // diagnose
		if ui.view == viewSessionDetail && len(ui.agents) > 0 {
			return ui.selectAction(ActionDiagnose)
		}
	case 'f': // force-respond — confirm-gated (MUX-105)
		if ui.view == viewSessionDetail && len(ui.agents) > 0 {
			ui.confirmPending = &RemoteSelection{
				Session: ui.detailSession,
				Role:    ui.agents[ui.agentIdx].Role,
				Action:  ActionForceRespond,
			}
		}
	case 'I': // all inboxes
		if ui.view == viewSessionDetail {
			return ui.selectSessionAction(ActionAllInboxes)
		}
	case 'D': // diagnose all
		if ui.view == viewSessionDetail {
			return ui.selectSessionAction(ActionDiagnoseAll)
		}
	case 'r', 'R': // refresh
		if ui.view == viewSessionList {
			ui.refreshSessions()
		} else if ui.view == viewSessionDetail {
			ui.refreshDetail()
		}
	}

	return ""
}

// selectAction sets the result for a per-agent action and returns "selected".
func (ui *RemoteUI) selectAction(action RemoteAction) string {
	ui.result = &RemoteSelection{
		Session: ui.detailSession,
		Role:    ui.agents[ui.agentIdx].Role,
		Action:  action,
	}
	return "selected"
}

// selectSessionAction sets the result for a session-wide action and returns "selected".
func (ui *RemoteUI) selectSessionAction(action RemoteAction) string {
	ui.result = &RemoteSelection{
		Session: ui.detailSession,
		Action:  action,
	}
	return "selected"
}

// handleEscapeSequence processes escape/arrow key sequences.
func (ui *RemoteUI) handleEscapeSequence() string {
	select {
	case b1 := <-ui.keyCh:
		if b1 == '[' {
			select {
			case b2 := <-ui.keyCh:
				switch b2 {
				case 'A': // Up
					ui.moveUp()
				case 'B': // Down
					ui.moveDown()
				}
			case <-time.After(50 * time.Millisecond):
			}
		}
	case <-time.After(50 * time.Millisecond):
		// Bare Escape — go back
		switch ui.view {
		case viewSessionList:
			return "quit"
		case viewSessionDetail:
			ui.view = viewSessionList
			ui.agentIdx = 0
		}
	}
	return ""
}

// moveDown moves the cursor down in the current view.
func (ui *RemoteUI) moveDown() {
	switch ui.view {
	case viewSessionList:
		if ui.sessionIdx < len(ui.sessions)-1 {
			ui.sessionIdx++
		}
	case viewSessionDetail:
		if ui.agentIdx < len(ui.agents)-1 {
			ui.agentIdx++
		}
	}
}

// moveUp moves the cursor up in the current view.
func (ui *RemoteUI) moveUp() {
	switch ui.view {
	case viewSessionList:
		if ui.sessionIdx > 0 {
			ui.sessionIdx--
		}
	case viewSessionDetail:
		if ui.agentIdx > 0 {
			ui.agentIdx--
		}
	}
}

// handleEnter processes Enter in the current view.
func (ui *RemoteUI) handleEnter() string {
	switch ui.view {
	case viewSessionList:
		if len(ui.sessions) > 0 {
			s := ui.sessions[ui.sessionIdx]
			ui.detailSession = s.Name
			ui.refreshDetail()
			ui.view = viewSessionDetail
			ui.agentIdx = 0
		}
	case viewSessionDetail:
		if len(ui.agents) > 0 {
			return ui.selectAction(ActionDiagnose)
		}
	}
	return ""
}

// ── Rendering ──────────────────────────────────────────────

// render builds the frame for the current view.
func (ui *RemoteUI) render() string {
	if ui.confirmPending != nil {
		return ui.renderForceRespondConfirm()
	}
	switch ui.view {
	case viewSessionList:
		return ui.renderSessionList()
	case viewSessionDetail:
		return ui.renderSessionDetail()
	default:
		return ""
	}
}

// renderForceRespondConfirm renders the confirm prompt for a pending
// force-respond, showing what the daemon's ladder already tried so the
// user knows where recovery stands before overriding.
func (ui *RemoteUI) renderForceRespondConfirm() string {
	sel := ui.confirmPending
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s%sForce-respond %s%s on %s?\n", Yellow, Bold, sel.Role, RST, sel.Session)
	fmt.Fprintf(&b, "  %sInjects the pending inbox with force — bypasses the idle gate and the in-flight-task skip.%s\n\n", Comment, RST)

	if st, ok := bus.ReadForceRespondState(sel.Session, sel.Role); ok {
		fmt.Fprintf(&b, "  %sthe daemon's escalation already tried:%s\n", Comment, RST)
		for _, h := range st.History {
			fmt.Fprintf(&b, "    %s%s%s\n", Comment, h, RST)
		}
	} else {
		fmt.Fprintf(&b, "  %sNo escalation episode recorded — this is a manual recovery.%s\n", Comment, RST)
	}

	fmt.Fprintf(&b, "\n  %sy%s Confirm  %sn/Esc%s Cancel\n", Yellow, RST, Yellow, RST)
	return b.String()
}

// renderSessionList renders the session list view.
func (ui *RemoteUI) renderSessionList() string {
	W := termWidth()
	H := termHeight()
	if W < 10 {
		W = 10
	}

	var b strings.Builder
	lineCount := 0

	writeLine := func(content string) {
		vw := VisibleWidth(content)
		if vw > W {
			content = TruncateAnsi(content, W)
		}
		pad := W - VisibleWidth(content)
		b.WriteString(content)
		if pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteRune('\n')
		lineCount++
	}

	hrLine := func() {
		writeLine(Comment + HLine('─', W) + RST)
	}

	// Title
	writeLine("")
	writeLine(fmt.Sprintf("  %s%sRemote Sessions%s", Purple, Bold, RST))
	hrLine()

	if len(ui.sessions) == 0 {
		writeLine(fmt.Sprintf("  %sNo muxcode sessions found%s", Comment, RST))
	} else {
		// Header
		writeLine(fmt.Sprintf("  %s   %-28s %-8s %-8s %-10s %s%s",
			Comment, "SESSION", "STATUS", "AGENTS", "LOG", "PROJECT", RST))
		hrLine()

		homeDir, _ := os.UserHomeDir()
		for i, s := range ui.sessions {
			status := fmt.Sprintf("%sdead%s", Red, RST)
			if s.TmuxAlive {
				status = fmt.Sprintf("%salive%s", Green, RST)
			}

			marker := " "
			if s.Name == ui.currentSession {
				marker = "*"
			}

			logSize := remoteFormatBytes(s.LogSize)
			project := s.ProjectDir
			if project == "" {
				project = "—"
			}
			if homeDir != "" {
				project = strings.Replace(project, homeDir, "~", 1)
			}
			// Truncate project to fit
			maxProj := W - 58
			if maxProj < 0 {
				maxProj = 0
			}
			if len(project) > maxProj {
				project = "…" + project[len(project)-maxProj+1:]
			}

			cursor := " "
			nameColor := FG
			if i == ui.sessionIdx {
				cursor = "▸"
				nameColor = Cyan + Bold
			}

			writeLine(fmt.Sprintf("  %s%s%s%-28s%s %-14s %-8d %-10s %s%s%s",
				Yellow, cursor, marker,
				nameColor+s.Name, RST,
				status, s.AgentCount, logSize,
				Comment, project, RST))
		}
	}

	// Fill remaining space
	footerLines := 2
	remaining := H - lineCount - footerLines
	for i := 0; i < remaining; i++ {
		b.WriteString(strings.Repeat(" ", W))
		b.WriteRune('\n')
	}

	// Footer
	hrLine()
	footer := fmt.Sprintf("  %s↑↓%s Navigate  %sEnter%s Select  %sr%s Refresh  %sq%s Close",
		Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST)
	writeLine(footer)

	return b.String()
}

// renderSessionDetail renders the agent detail view for a selected session.
func (ui *RemoteUI) renderSessionDetail() string {
	W := termWidth()
	H := termHeight()
	if W < 10 {
		W = 10
	}

	var b strings.Builder
	lineCount := 0

	writeLine := func(content string) {
		vw := VisibleWidth(content)
		if vw > W {
			content = TruncateAnsi(content, W)
		}
		pad := W - VisibleWidth(content)
		b.WriteString(content)
		if pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteRune('\n')
		lineCount++
	}

	hrLine := func() {
		writeLine(Comment + HLine('─', W) + RST)
	}

	// Session header
	writeLine("")
	sessionStatus := fmt.Sprintf("%sdead%s", Red, RST)
	if bus.TmuxHasSession(ui.detailSession) {
		sessionStatus = fmt.Sprintf("%salive%s", Green, RST)
	}
	writeLine(fmt.Sprintf("  %s%s%s%s  %s",
		Purple, Bold, ui.detailSession, RST, sessionStatus))
	hrLine()

	// Agent table header
	writeLine(fmt.Sprintf("  %s   %-12s %-10s %-6s %-8s %-6s %s%s",
		Comment, "ROLE", "PROVIDER", "STATE", "HEALTH", "INBOX", "LAST ACTIVITY", RST))
	hrLine()

	for i, s := range ui.agents {
		state := "idle"
		if s.Locked {
			state = "busy"
		}

		health := s.Health
		healthColor := Comment
		switch health {
		case "alive":
			healthColor = Green
		case "dead":
			healthColor = Red
		case "stopped":
			healthColor = Yellow
		}

		activity := "—"
		if s.LastMsgTS > 0 {
			t := time.Unix(s.LastMsgTS, 0).Format("15:04")
			arrow := "←"
			if s.LastDir == "sent" {
				arrow = "→"
			}
			activity = fmt.Sprintf("%s %s %s:%s", t, arrow, s.LastPeer, s.LastAction)
		}
		// Surface an open escalation episode (MUX-105).
		if st, ok := bus.ReadForceRespondState(ui.detailSession, s.Role); ok {
			activity = fmt.Sprintf("⚡esc r%d  %s", st.Rung, activity)
		}

		provider := s.Provider
		if provider == "" {
			provider = "—"
		}

		inboxColor := Comment
		if s.InboxCount > 0 {
			inboxColor = Yellow
		}

		cursor := " "
		roleColor := FG
		if i == ui.agentIdx {
			cursor = "▸"
			roleColor = Cyan + Bold
		}

		// Truncate activity to fit
		maxAct := W - 52
		if maxAct < 0 {
			maxAct = 0
		}
		if len(activity) > maxAct {
			activity = activity[:maxAct]
		}

		writeLine(fmt.Sprintf("  %s%s%s%-12s%s %-10s %-6s %s%-8s%s %s%-6d%s %s%s%s",
			Yellow, cursor,
			roleColor, s.Role, RST,
			provider, state,
			healthColor, health, RST,
			inboxColor, s.InboxCount, RST,
			Comment, activity, RST))
	}

	// Fill remaining space
	footerLines := 3
	remaining := H - lineCount - footerLines
	for i := 0; i < remaining; i++ {
		b.WriteString(strings.Repeat(" ", W))
		b.WriteRune('\n')
	}

	// Footer
	hrLine()
	footer1 := fmt.Sprintf("  %s↑↓%s Navigate  %sEnter/c%s Capture  %si%s Inbox  %sd%s Diagnose  %sf%s Force-respond",
		Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST)
	writeLine(footer1)
	footer2 := fmt.Sprintf("  %sI%s All Inboxes  %sD%s Diagnose All  %sr%s Refresh  %sq/Esc%s Back",
		Yellow, RST, Yellow, RST, Yellow, RST, Yellow, RST)
	writeLine(footer2)

	return b.String()
}

// renderContent renders a scrollable content view (capture/inbox/diagnose).
// readKeys reads single bytes from stdin in a loop.
func (ui *RemoteUI) readKeys() {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		ui.keyCh <- buf[0]
	}
}

// cleanup restores the terminal.
func (ui *RemoteUI) cleanup(restoreStty bool) {
	if restoreStty {
		saneCmd := exec.Command("stty", "sane")
		saneCmd.Stdin = os.Stdin
		_ = saneCmd.Run()
	}
	fmt.Print("\033[?25h")
	fmt.Print(RST)
	fmt.Print("\033[2J")
	fmt.Print("\033[H")
}

// remoteFormatBytes formats a byte count as a human-readable string.
func remoteFormatBytes(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%dB", b)
	}
}
