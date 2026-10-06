package tui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// restartOption is one row of the restart modal's provider list.
type restartOption struct {
	filter string // provider CLI, or bus.RestartProviderAll
	total  int
	down   int
}

// RestartSelectUI is the `Restart Agents` modal (MUX-139 Phase 5): pick a
// provider by its live agent counts, confirm, then watch per-agent progress
// in the view the provider selector's reload uses. The provider list is a
// filter over the current assignment — nothing here can switch a provider.
type RestartSelectUI struct {
	session    string
	options    []restartOption
	loadErr    error
	cursor     int
	confirming bool
	notice     string // why a confirm did not start, shown on the list

	inProgress     bool
	progressMu     sync.Mutex
	roles          []string
	results        []bus.ReloadResult
	done           bool
	closeRequested bool
	progressCh     chan struct{}

	keyCh chan byte
}

// Seams for the batch the modal runs, so a test can hold it mid-flight.
var (
	restartSelectTargets = bus.RestartTargets
	restartSelectRoles   = bus.RestartRoles
)

// NewRestartSelectUI reads the session's restartable agents grouped by
// provider. A failed read is kept and rendered, never an empty list.
func NewRestartSelectUI(session string) *RestartSelectUI {
	counts, err := bus.RestartProviderCounts(session)
	return newRestartSelectUI(session, counts, err)
}

func newRestartSelectUI(session string, counts []bus.RestartProviderCount, err error) *RestartSelectUI {
	ui := &RestartSelectUI{session: session, loadErr: err, progressCh: make(chan struct{}, 16)}
	all := restartOption{filter: bus.RestartProviderAll}
	for _, c := range counts {
		ui.options = append(ui.options, restartOption{filter: c.CLI, total: c.Total, down: c.Down})
		all.total += c.Total
		all.down += c.Down
	}
	if len(ui.options) > 1 {
		ui.options = append(ui.options, all)
	}
	return ui
}

// Run drives the modal until it is closed.
//
// The batch runs in this process, so the process outlives every close request
// made while it runs: q, Escape and a signal (SIGHUP when the popup is torn
// down) only mark the close, which happens once the batch is done — exiting
// earlier would kill the batch mid-agent, skipping the rest and stranding
// reload markers.
func (ui *RestartSelectUI) Run() {
	rawCmd := exec.Command("stty", "-icanon", "-echo", "min", "1")
	rawCmd.Stdin = os.Stdin
	rawErr := rawCmd.Run()
	fmt.Print("\033[2J\033[H\033[?25l")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	ui.keyCh = make(chan byte, 16)
	go readKeysInto(ui.keyCh)
	defer restoreTerminal(rawErr == nil)

	for {
		fmt.Print("\033[H")
		fmt.Print(ClearFrame(ui.render(termWidth())))
		fmt.Print("\033[J")

		select {
		case <-sigCh:
			if ui.requestClose() {
				return
			}
		case <-ui.progressCh:
			if ui.closeDue() {
				return
			}
		case key := <-ui.keyCh:
			if ui.handleKey(key) == "close" {
				return
			}
		}
	}
}

func (ui *RestartSelectUI) batchDone() bool {
	ui.progressMu.Lock()
	defer ui.progressMu.Unlock()
	return ui.done
}

// requestClose reports whether the modal may close now; with a batch still
// running it records the request instead, honoured by closeDue.
func (ui *RestartSelectUI) requestClose() bool {
	if !ui.inProgress || ui.batchDone() {
		return true
	}
	ui.progressMu.Lock()
	ui.closeRequested = true
	ui.progressMu.Unlock()
	return false
}

// closeDue reports whether a deferred close request can now be honoured.
func (ui *RestartSelectUI) closeDue() bool {
	ui.progressMu.Lock()
	defer ui.progressMu.Unlock()
	return ui.closeRequested && ui.done
}

// handleKey applies one keypress and returns "close" when the modal ends.
func (ui *RestartSelectUI) handleKey(key byte) string {
	if ui.inProgress {
		if key == 'q' || key == 27 || (ui.batchDone() && (key == 10 || key == 13)) {
			if ui.requestClose() {
				return "close"
			}
		}
		return ""
	}
	if ui.confirming {
		switch key {
		case 'y', 'Y':
			ui.confirming = false
			ui.start()
		default:
			ui.confirming = false
		}
		return ""
	}
	switch key {
	case 'q':
		return "close"
	case 'k':
		ui.move(-1)
	case 'j':
		ui.move(1)
	case 10, 13:
		if len(ui.options) > 0 {
			ui.notice = ""
			ui.confirming = true
		}
	case 27:
		return ui.handleEscape()
	}
	return ""
}

// handleEscape tells a bare Escape (close) from an arrow sequence, which
// starts with the same byte.
func (ui *RestartSelectUI) handleEscape() string {
	select {
	case b1 := <-ui.keyCh:
		if b1 != '[' {
			return ""
		}
		select {
		case b2 := <-ui.keyCh:
			switch b2 {
			case 'A':
				ui.move(-1)
			case 'B':
				ui.move(1)
			}
		case <-time.After(50 * time.Millisecond):
		}
		return ""
	case <-time.After(50 * time.Millisecond):
		return "close"
	}
}

func (ui *RestartSelectUI) move(delta int) {
	if n := len(ui.options); n > 0 {
		ui.cursor = (ui.cursor + delta + n) % n
	}
}

// start re-reads the targets at execution — agents may have died, come back
// or been reassigned since the list was drawn — and restarts exactly those,
// so the progress rows are the agents acted on.
func (ui *RestartSelectUI) start() {
	filter := ui.options[ui.cursor].filter
	roles, err := restartSelectTargets(ui.session, filter)
	if err != nil {
		ui.notice = err.Error()
		return
	}
	if len(roles) == 0 {
		ui.notice = fmt.Sprintf("No %s agents to restart any more", filter)
		return
	}
	ui.roles = roles
	ui.inProgress = true
	go func() {
		restartSelectRoles(ui.session, roles, func(_ int, r bus.ReloadResult) {
			ui.progressMu.Lock()
			ui.results = append(ui.results, r)
			ui.progressMu.Unlock()
			ui.signal()
		})
		ui.progressMu.Lock()
		ui.done = true
		ui.progressMu.Unlock()
		ui.signal()
	}()
}

func (ui *RestartSelectUI) signal() {
	select {
	case ui.progressCh <- struct{}{}:
	default:
	}
}

// render draws the current frame. Pure: it reads only the modal's state.
func (ui *RestartSelectUI) render(width int) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s%sRestart Agents%s\n", Bold, Purple, RST))
	b.WriteString(fmt.Sprintf("  %sProvider and model are never changed%s\n\n", Comment, RST))

	switch {
	case ui.inProgress:
		ui.progressMu.Lock()
		results := append([]bus.ReloadResult(nil), ui.results...)
		done, closing := ui.done, ui.closeRequested
		ui.progressMu.Unlock()
		footer := "q closes once the restart finishes — closing earlier would abort it"
		if closing {
			footer = "Closing as soon as the restart finishes…"
		}
		b.WriteString(renderBatchProgress(ui.roles, results, len(ui.roles), done, "restart", footer, width))
		return b.String()
	case ui.loadErr != nil:
		b.WriteString(fmt.Sprintf("  %s✗ Cannot read the session's agents:%s\n", Red, RST))
		for _, line := range wrapWords(ui.loadErr.Error(), width-6) {
			b.WriteString(fmt.Sprintf("    %s\n", line))
		}
		b.WriteString(fmt.Sprintf("\n  %sq Quit%s\n", Comment, RST))
		return b.String()
	case len(ui.options) == 0:
		b.WriteString(fmt.Sprintf("  %sNo agents with a window in this session%s\n", Comment, RST))
		b.WriteString(fmt.Sprintf("\n  %sq Quit%s\n", Comment, RST))
		return b.String()
	case ui.confirming:
		b.WriteString(ui.renderConfirm(width))
		return b.String()
	}

	b.WriteString(fmt.Sprintf("  %s%s── Provider ─────────────────────%s\n\n", Bold, Purple, RST))
	for i, o := range ui.options {
		marker, color := "  ", ""
		if i == ui.cursor {
			marker, color = Cyan+"▸ "+RST, Bold
		}
		b.WriteString(fmt.Sprintf("  %s%s%-9s%s %2d %s%s\n", marker, color, o.filter, RST, o.total, agentNoun(o.total), downNote(o.down)))
	}
	if ui.notice != "" {
		b.WriteString("\n")
		for _, line := range wrapWords(ui.notice, width-6) {
			b.WriteString(fmt.Sprintf("  %s%s%s\n", Yellow, line, RST))
		}
	}
	b.WriteString(fmt.Sprintf("\n  %s↑↓ Navigate  ⏎ Restart…  q Quit%s\n", Comment, RST))
	return b.String()
}

// renderConfirm states what the restart will do before anything is touched,
// naming only the roads the chosen filter can take.
func (ui *RestartSelectUI) renderConfirm(width int) string {
	o := ui.options[ui.cursor]
	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %sRestart %d %s %s%s%s?%s\n\n", Bold, o.total, o.filter, agentNoun(o.total), RST, downNote(o.down), RST))
	var lines []string
	if o.filter == "claude" || o.filter == bus.RestartProviderAll {
		lines = append(lines,
			"Claude agents resume their last session; a live one is exited first",
			"A Claude edit is resume-only — left down if it has no session to resume")
	}
	if o.filter != "claude" {
		lines = append(lines, "Other providers relaunch fresh on the same provider and model")
	}
	lines = append(lines, "Dead agents are included")
	for _, line := range lines {
		for i, w := range wrapWords(line, width-8) {
			lead := "•"
			if i > 0 {
				lead = " "
			}
			b.WriteString(fmt.Sprintf("    %s%s %s%s\n", Comment, lead, w, RST))
		}
	}
	b.WriteString(fmt.Sprintf("\n  %sy Confirm  any other key Back%s\n", Comment, RST))
	return b.String()
}

func agentNoun(n int) string {
	if n == 1 {
		return "agent"
	}
	return "agents"
}

func downNote(down int) string {
	if down == 0 {
		return ""
	}
	return fmt.Sprintf(" %s(%d down)%s", Red, down, RST)
}

// readKeysInto reads single bytes from stdin into ch forever.
func readKeysInto(ch chan<- byte) {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		ch <- buf[0]
	}
}

// restoreTerminal undoes Run's raw mode and hidden cursor.
func restoreTerminal(restoreStty bool) {
	if restoreStty {
		saneCmd := exec.Command("stty", "sane")
		saneCmd.Stdin = os.Stdin
		_ = saneCmd.Run()
	}
	fmt.Print("\033[?25h" + RST + "\033[2J\033[H")
}
