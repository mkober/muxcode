package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// upgradeStepNameWidth fits the longest step name, "Reload tmux config".
const upgradeStepNameWidth = 18

type upgradePhase int

const (
	upgradeChecking upgradePhase = iota
	upgradeConfirm
	upgradeRechecking
	upgradeRunning
	upgradeDone
)

// upgradeTarget is what the confirm says the upgrade will touch.
type upgradeTarget struct {
	BinDir, ConfigDir string
	Sessions          []string
}

// upgradeReading is one read of everything the confirm states: the release
// check and the install target, each with its own failure.
type upgradeReading struct {
	Check     bus.UpgradeCheck
	CheckErr  error
	Target    upgradeTarget
	TargetErr error
}

func (r upgradeReading) canForce() bool {
	return r.CheckErr == nil && r.TargetErr == nil && r.Check.ToolsErr() == nil
}

func (r upgradeReading) canUpgrade() bool {
	return r.canForce() && r.Check.Verdict == bus.UpgradeNewer
}

// Seams for what the modal reads and starts, so a test drives it with no
// network, no ps and no child process.
var (
	upgradeCheckFn = func() (bus.UpgradeCheck, error) {
		return bus.CheckUpgrade(context.Background(), bus.DefaultReleaseClient())
	}
	upgradeTargetFn = loadUpgradeTarget
	upgradeStartFn  = bus.StartSelfUpgradeDetached
	upgradeAliveFn  = processAlive
)

func loadUpgradeTarget() (upgradeTarget, error) {
	_, bin, config, err := bus.ResolveUpgradePaths(bus.SelfUpgradeOptions{})
	if err != nil {
		return upgradeTarget{}, err
	}
	sessions, err := bus.UpgradeDaemonSessions()
	if err != nil {
		return upgradeTarget{BinDir: bin, ConfigDir: config}, fmt.Errorf("listing daemons: %w", err)
	}
	return upgradeTarget{BinDir: bin, ConfigDir: config, Sessions: sessions}, nil
}

func readUpgrade() upgradeReading {
	var r upgradeReading
	r.Check, r.CheckErr = upgradeCheckFn()
	r.Target, r.TargetErr = upgradeTargetFn()
	return r
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// UpgradeUI is the `Check for Updates` modal (MUX-202): check the latest
// release, confirm what the upgrade will do, then follow it. The upgrade runs
// as its own detached `muxcode upgrade --events` process, so q closes the
// modal at any point and the upgrade carries on.
//
// All state is changed on the Run goroutine: a read runs in a goroutine and
// hands its result back as a closure on updates.
type UpgradeUI struct {
	phase   upgradePhase
	reading upgradeReading
	notice  string

	force      bool
	eventsPath string
	pid        int
	results    []bus.StepResult
	finalErr   string
	summary    string

	updates chan func()
	keyCh   chan byte
}

// NewUpgradeUI returns the modal; Run starts its release check.
func NewUpgradeUI() *UpgradeUI {
	return &UpgradeUI{updates: make(chan func(), 4)}
}

// Run drives the modal until it is closed. Closing — q, Escape, or a signal
// when the popup is torn down — never touches a running upgrade: it lives in
// its own process. A finished run's events file is removed on the way out.
func (ui *UpgradeUI) Run() {
	rawCmd := exec.Command("stty", "-icanon", "-echo", "min", "1")
	rawCmd.Stdin = os.Stdin
	rawErr := rawCmd.Run()
	fmt.Print(ClearScreen + CursorHome + HideCursor)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	ui.keyCh = make(chan byte, 16)
	go readKeysInto(ui.keyCh)
	defer restoreTerminal(rawErr == nil)
	defer ui.removeFinishedEvents()

	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	ui.startCheck()
	redraw := true
	for {
		if redraw {
			fmt.Print(CursorHome + ClearFrame(ui.render(termWidth(), termHeight())) + ClearBelow)
		}
		redraw = true
		select {
		case <-sigCh:
			return
		case fn := <-ui.updates:
			fn()
		case <-tick.C:
			redraw = ui.phase == upgradeRunning && ui.poll()
		case key := <-ui.keyCh:
			if ui.handleKey(key) == "close" {
				return
			}
		}
	}
}

func (ui *UpgradeUI) removeFinishedEvents() {
	if ui.phase == upgradeDone && ui.eventsPath != "" {
		_ = os.Remove(ui.eventsPath)
	}
}

// handleKey applies one keypress and returns "close" when the modal ends.
func (ui *UpgradeUI) handleKey(key byte) string {
	if key == 27 {
		return ui.handleEscape()
	}
	switch ui.phase {
	case upgradeConfirm:
		switch key {
		case 'q':
			return "close"
		case 10, 13:
			if ui.reading.CheckErr != nil || ui.reading.TargetErr != nil {
				ui.startCheck()
			} else if ui.reading.canUpgrade() {
				ui.startRecheck(false)
			}
		case 'f':
			if ui.reading.canForce() {
				ui.startRecheck(true)
			}
		}
	case upgradeDone:
		if key == 'q' || key == 10 || key == 13 {
			return "close"
		}
	default:
		if key == 'q' {
			return "close"
		}
	}
	return ""
}

// handleEscape tells a bare Escape (close) from an escape sequence such as an
// arrow, which starts with the same byte and does nothing here.
func (ui *UpgradeUI) handleEscape() string {
	select {
	case <-ui.keyCh:
		select {
		case <-ui.keyCh:
		case <-time.After(50 * time.Millisecond):
		}
		return ""
	case <-time.After(50 * time.Millisecond):
		return "close"
	}
}

func (ui *UpgradeUI) startCheck() {
	ui.phase, ui.notice = upgradeChecking, ""
	go func() {
		r := readUpgrade()
		ui.updates <- func() { ui.reading, ui.phase = r, upgradeConfirm }
	}()
}

// startRecheck reads the release and the daemons again at execution: the
// confirm described a moment that has passed, and the upgrade must never act
// on a release or a set of sessions the user was not shown.
func (ui *UpgradeUI) startRecheck(force bool) {
	shown := ui.reading
	ui.phase, ui.notice = upgradeRechecking, ""
	go func() {
		r := readUpgrade()
		ui.updates <- func() { ui.finishRecheck(force, shown, r) }
	}()
}

// finishRecheck starts the run only when the fresh reading still matches the
// confirm; otherwise it shows the new reading and says what changed. The run
// is bound to the confirmed release and sessions, so a change after this
// point is refused by the run itself rather than acted on.
func (ui *UpgradeUI) finishRecheck(force bool, shown, now upgradeReading) {
	ui.reading, ui.phase = now, upgradeConfirm
	if now.CheckErr != nil || now.TargetErr != nil {
		ui.notice = "The re-check failed — nothing was started"
		return
	}
	if reason := confirmChanged(shown, now); reason != "" {
		ui.notice = reason + " since this was shown — review and confirm again"
		return
	}
	path, pid, err := upgradeStartFn(force, bus.UpgradeConfirmation{Tag: now.Check.Latest.Tag, Sessions: now.Target.Sessions})
	if err != nil {
		ui.notice = err.Error()
		return
	}
	ui.phase, ui.force, ui.eventsPath, ui.pid = upgradeRunning, force, path, pid
}

// confirmChanged names what a re-read found different from what the confirm
// showed — the release, the verdict, the install paths or the daemons the
// restart touches — or "" when the confirm still describes the run.
func confirmChanged(shown, now upgradeReading) string {
	switch {
	case shown.Check.Latest.Tag != now.Check.Latest.Tag:
		return fmt.Sprintf("The latest release changed (%s → %s)", shown.Check.Latest.Tag, now.Check.Latest.Tag)
	case shown.Check.Verdict != now.Check.Verdict:
		return "The installed version changed"
	case shown.Target.BinDir != now.Target.BinDir || shown.Target.ConfigDir != now.Target.ConfigDir:
		return "The install paths changed"
	case !slices.Equal(shown.Target.Sessions, now.Target.Sessions):
		return "The running daemons changed"
	}
	return ""
}

// poll reads the run's events file and reports whether anything changed. A
// run whose process is gone without writing its end is finished as failed,
// so the modal never waits on a dead upgrade.
func (ui *UpgradeUI) poll() bool {
	events, _ := bus.ReadUpgradeEvents(ui.eventsPath)
	changed := ui.applyEvents(events)
	if ui.phase != upgradeRunning || upgradeAliveFn(ui.pid) {
		return changed
	}
	events, _ = bus.ReadUpgradeEvents(ui.eventsPath)
	ui.applyEvents(events)
	if ui.phase == upgradeRunning {
		ui.phase = upgradeDone
		ui.finalErr = "the upgrade process exited without reporting its end — see `muxcode lifecycle show`"
	}
	return true
}

func (ui *UpgradeUI) applyEvents(events []bus.UpgradeEvent) bool {
	var results []bus.StepResult
	changed := false
	for _, ev := range events {
		switch {
		case ev.Done && ui.phase != upgradeDone:
			ui.phase, ui.finalErr, ui.summary = upgradeDone, ev.Error, ev.Summary
			changed = true
		case ev.Step != nil:
			results = append(results, *ev.Step)
		}
	}
	if len(results) != len(ui.results) {
		changed = true
	}
	ui.results = results
	return changed
}

func (ui *UpgradeUI) render(width, height int) string {
	switch ui.phase {
	case upgradeRunning:
		return renderUpgradeProgress(ui.progressView(), width, height)
	case upgradeDone:
		return renderUpgradeDone(ui.progressView(), width, height)
	}
	return renderUpgradeConfirm(upgradeConfirmView{
		Reading:    ui.reading,
		Checking:   ui.phase == upgradeChecking,
		Rechecking: ui.phase == upgradeRechecking,
		Notice:     ui.notice,
	}, width, height)
}

func (ui *UpgradeUI) progressView() upgradeProgressView {
	return upgradeProgressView{
		Steps:     bus.SelfUpgradeStepNames(),
		Results:   ui.results,
		Installed: ui.reading.Check.Installed.Version,
		Latest:    ui.reading.Check.Latest.Tag,
		Force:     ui.force,
		Err:       ui.finalErr,
		Summary:   ui.summary,
	}
}

// upgradeConfirmView is the confirm screen's snapshot.
type upgradeConfirmView struct {
	Reading    upgradeReading
	Checking   bool
	Rechecking bool
	Notice     string
}

// upgradeProgressView is the progress and done screens' snapshot.
type upgradeProgressView struct {
	Steps             []string
	Results           []bus.StepResult
	Installed, Latest string
	Force             bool
	Err, Summary      string
}

func (v upgradeProgressView) title() string {
	if v.Force {
		return fmt.Sprintf("Reinstalling %s (forced)", v.Latest)
	}
	return fmt.Sprintf("Upgrading %s → %s", v.Installed, v.Latest)
}

// renderUpgradeConfirm draws the release check and, before anything mutates,
// what the upgrade will do. Every state has an explicit body; the footer
// names only the keys that state accepts. A popup too short for the session
// list gets their count, then the frame is cut above the footer. Pure.
func renderUpgradeConfirm(v upgradeConfirmView, width, height int) string {
	form := func(compact bool) []string {
		body, footer := confirmBody(v, compact, width)
		return upgradeFrame(upgradeHeader("Check · download · rebuild · restart daemons", width), body, footer, width)
	}
	return fitUpgrade(height, form(false), form(true))
}

func confirmBody(v upgradeConfirmView, compact bool, width int) ([]string, string) {
	r := v.Reading
	var body []string
	footer := "q Quit"
	switch {
	case v.Checking:
		body = append(body, TruncateAnsi(fmt.Sprintf("  %s⟳%s Checking the latest release…", Yellow, RST), width))
	case r.CheckErr != nil:
		body = append(body, fmt.Sprintf("  %s✗%s Cannot check the latest release:", Red, RST))
		body = append(body, wrapStyled("    ", Red, r.CheckErr.Error(), width)...)
		footer = "⏎ Re-check  q Quit"
	default:
		body = append(body, verdictLines(r.Check, width)...)
		if w := r.Check.DevBuildWarning(); w != "" {
			body = append(body, wrapStyled("  ", Yellow, "⚠ "+w, width)...)
		}
		body = append(body, "")
		switch {
		case r.TargetErr != nil:
			body = append(body, wrapStyled("  ", Red, "✗ Cannot read the install target: "+r.TargetErr.Error(), width)...)
			footer = "⏎ Re-check  q Quit"
		case r.Check.ToolsErr() != nil:
			body = append(body, wrapStyled("  ", Red, "✗ "+r.Check.ToolsErr().Error(), width)...)
		case r.Check.Verdict == bus.UpgradeNewer:
			body = append(body, wrapStyled("  ", "", upgradeConsequence(r, false, compact), width)...)
			footer = "⏎ Upgrade  f Force rebuild  q Quit"
		default:
			body = append(body, wrapStyled("  ", Comment, upgradeConsequence(r, true, compact), width)...)
			footer = "f Force rebuild  q Quit"
		}
	}
	if v.Rechecking {
		body = append(body, "", TruncateAnsi(fmt.Sprintf("  %s⟳%s Re-checking the release and the daemons…", Yellow, RST), width))
		footer = "q Quit"
	}
	if v.Notice != "" {
		body = append(body, "")
		body = append(body, wrapStyled("  ", Yellow, v.Notice, width)...)
	}
	return body, footer
}

// verdictLines is the check's summary behind a glyph that carries the
// verdict without colour: ↑ newer, ✓ current or ahead, ? uncomparable.
func verdictLines(c bus.UpgradeCheck, width int) []string {
	glyph, style := "?", Yellow
	switch c.Verdict {
	case bus.UpgradeNewer:
		glyph, style = "↑", Cyan
	case bus.UpgradeCurrent, bus.UpgradeAhead:
		glyph, style = "✓", Green
	}
	return wrapStyled("  ", style, glyph+" "+c.Summary(), width)
}

// upgradeConsequence is the confirm's statement of what the run does: the
// versions, where it installs and which daemons it restarts. forced phrases
// it as what f does; compact counts the daemons instead of naming them.
func upgradeConsequence(r upgradeReading, forced, compact bool) string {
	installed, latest := r.Check.Installed.Version, r.Check.Latest.Tag
	lead := fmt.Sprintf("installed %s → latest %s; rebuilds and installs", installed, latest)
	if forced {
		lead = fmt.Sprintf("f rebuilds and reinstalls %s over %s anyway — installs", latest, installed)
	}
	return fmt.Sprintf("%s to %s and %s, then %s", lead, r.Target.BinDir, r.Target.ConfigDir, daemonClause(r.Target.Sessions, compact))
}

func daemonClause(sessions []string, compact bool) string {
	n := len(sessions)
	noun := "daemons"
	if n == 1 {
		noun = "daemon"
	}
	switch {
	case n == 0:
		return "restarts no daemons (none running)"
	case compact:
		return fmt.Sprintf("restarts %d %s", n, noun)
	}
	return fmt.Sprintf("restarts %d %s: %s", n, noun, strings.Join(sessions, ", "))
}

// renderUpgradeProgress draws the running upgrade: one row per step, a
// session's daemon under Restart daemons, the bar, and a footer saying q
// leaves the upgrade running. A popup too short drops the sub-rows, then folds
// the steps to one status line. Pure.
func renderUpgradeProgress(v upgradeProgressView, width, height int) string {
	header := upgradeHeader(v.title(), width)
	footer := "q Close — the upgrade keeps running in the background"
	return fitUpgrade(height,
		upgradeFrame(header, progressRows(v, true, true, width), footer, width),
		upgradeFrame(header, progressRows(v, true, false, width), footer, width),
		upgradeFrame(header[1:2], progressSummary(v, true, width), footer, width))
}

// renderUpgradeDone draws the finished run: the final rows and the outcome —
// the version delta and the agents left to restart, or where it stopped.
// It degrades as renderUpgradeProgress does; folded, a failed run keeps the
// failed step's cause, log path included, since that is what the reader has
// to act on. Pure.
func renderUpgradeDone(v upgradeProgressView, width, height int) string {
	header := upgradeHeader(v.title(), width)
	outcome := doneOutcome(v, width)
	folded := outcome
	if n := len(v.Results); n > 0 && !v.Results[n-1].Success {
		folded = progressSummary(v, false, width)
	}
	footer := "⏎ Close  q Quit"
	withOutcome := func(rows []string) []string { return append(append(rows, ""), outcome...) }
	return fitUpgrade(height,
		upgradeFrame(header, withOutcome(progressRows(v, false, true, width)), footer, width),
		upgradeFrame(header, withOutcome(progressRows(v, false, false, width)), footer, width),
		upgradeFrame(header[1:2], folded, footer, width))
}

func doneOutcome(v upgradeProgressView, width int) []string {
	if n := len(v.Results); n > 0 && !v.Results[n-1].Success {
		return wrapStyled("  ", Red, fmt.Sprintf("✗ Failed at %s — nothing after it ran", v.Results[n-1].Name), width)
	}
	if v.Err != "" {
		return wrapStyled("  ", Red, "✗ "+v.Err, width)
	}
	return wrapStyled("  ", Green, "✓ "+v.Summary, width)
}

func progressRows(v upgradeProgressView, running, withSub bool, width int) []string {
	return splitFrame(renderBatchRows(v.Steps, stepRows(v.Results, withSub), len(v.Steps), running, upgradeStepNameWidth, width))
}

// progressSummary folds the step list to one status line — or the failed
// step and its cause — for a popup too short to list the steps.
func progressSummary(v upgradeProgressView, running bool, width int) []string {
	for _, r := range v.Results {
		if !r.Success {
			return splitFrame(renderFailureRowIn("  ", r.Name, errors.New(r.Error), 0, width))
		}
	}
	status := fmt.Sprintf("  %s✓%s %d/%d done", Green, RST, len(v.Results), len(v.Steps))
	if running && len(v.Results) < len(v.Steps) {
		status += fmt.Sprintf(" · %s⟳%s %s", Yellow, RST, v.Steps[len(v.Results)])
	}
	return []string{TruncateAnsi(status, width)}
}

func stepRows(results []bus.StepResult, withSub bool) []batchRow {
	rows := make([]batchRow, len(results))
	for i, r := range results {
		rows[i] = batchRow{Name: r.Name, Success: r.Success, Note: Comment + r.Note + RST, Duration: r.Duration}
		if !r.Success {
			rows[i].Err = errors.New(r.Error)
		}
		if withSub {
			rows[i].Sub = stepRows(r.Sub, false)
		}
	}
	return rows
}

func upgradeHeader(subtitle string, width int) []string {
	return []string{
		"",
		TruncateAnsi(fmt.Sprintf("  %s%sCheck for Updates%s", Bold, Purple, RST), width),
		TruncateAnsi(fmt.Sprintf("  %s%s%s", Comment, subtitle, RST), width),
		"",
	}
}

func upgradeFrame(header, body []string, footer string, width int) []string {
	lines := append(append([]string{}, header...), body...)
	return append(lines, "", TruncateAnsi(fmt.Sprintf("  %s%s%s", Comment, footer, RST), width))
}

// fitUpgrade returns the first form that fits height, else the last cut to
// fit with its footer kept, so the keys stay visible however small the popup.
// One row is left free: the frame's last newline would otherwise scroll a
// full-height pane.
func fitUpgrade(height int, forms ...[]string) string {
	limit := height - 1
	if limit < 1 {
		return ""
	}
	for _, f := range forms {
		if len(f) <= limit {
			return strings.Join(f, "\n") + "\n"
		}
	}
	last := forms[len(forms)-1]
	cut := append(append([]string{}, last[:limit-1]...), last[len(last)-1])
	return strings.Join(cut, "\n") + "\n"
}

// wrapStyled wraps text to the frame under indent, every line in style.
func wrapStyled(indent, style, text string, width int) []string {
	var lines []string
	for _, l := range wrapCause(text, max(width-VisibleWidth(indent)-1, 1)) {
		lines = append(lines, indent+style+l+RST)
	}
	return lines
}

func splitFrame(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
