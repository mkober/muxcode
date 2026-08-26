package bus

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ContentMeasurer reports a modal's content size — the longest line and the
// number of lines — for the auto-fit tier. It returns (0, 0) when the size
// cannot be determined, which makes the modal fall back to percentage sizing.
//
// Measurers must be side-effect free: they read the same data the modal will
// render and must never run the modal's command. Sizing is decided before the
// popup opens and a popup cannot be resized afterwards, so a measurer reports
// the widest state the content can reach, not its current filtered view.
type ContentMeasurer func(session string) (cols, rows int)

// MeasureText returns the width of the longest line and the number of lines.
//
// Colour is stripped before measuring — the fit-tier renderers emit Dracula
// escapes, whose bytes would otherwise be counted as visible columns and
// inflate every measurement several-fold. Width is counted in runes rather
// than bytes so box-drawing and check-mark glyphs count as the one column they
// occupy; this under-measures double-width CJK, which none of these modals
// render.
func MeasureText(s string) (cols, rows int) {
	if s == "" {
		return 0, 0
	}
	return MeasureLines(strings.Split(strings.TrimRight(s, "\n"), "\n"))
}

// MeasureLines is MeasureText for content already split into lines.
func MeasureLines(lines []string) (cols, rows int) {
	for _, ln := range lines {
		if w := utf8.RuneCountInString(StripANSI(ln)); w > cols {
			cols = w
		}
	}
	if cols == 0 {
		return 0, 0
	}
	return cols, len(lines)
}

// MeasureProjectPicker sizes the session picker from the full project list.
// The picker filters as the user types, but the popup cannot resize once open,
// so it is sized for the unfiltered list — the widest state it can show.
func MeasureProjectPicker(string) (int, int) {
	// LoadLauncherConfig, not DefaultLauncherConfig: only the former applies
	// MUXCODE_PROJECTS_DIR and MUXCODE_SCAN_DEPTH, and measuring a different
	// tree than the picker scans would size the popup for the wrong list.
	cfg := LoadLauncherConfig()
	projects := ScanProjects(cfg.ProjectsDir, cfg.ScanDepth)
	cols, rows := MeasureLines(projects)
	if cols == 0 {
		return 0, 0
	}
	// The picker draws a prompt, a match counter and a hint above the list.
	return cols, rows + pickerHeaderRows
}

// pickerHeaderRows covers the picker's prompt, "n/m" counter and hint line.
const pickerHeaderRows = 3

// MeasureMemoryContext sizes the memory modal from exactly what
// `muxcode memory context` renders — ReadContextFull with the same role, day
// window and global flag. Measuring a single role's memory instead would
// under-size the popup by whatever the shared and global sections add.
func MeasureMemoryContext(string) (int, int) {
	text, err := ReadContextFull(BusRole(), DefaultRotationConfig().ContextDays, true)
	if err != nil {
		return 0, 0
	}
	return MeasureText(text)
}

// MeasureRemoteSessions sizes the sessions browser from its session table.
func MeasureRemoteSessions(session string) (int, int) {
	sessions, err := DiscoverSessions(session, false)
	if err != nil {
		return 0, 0
	}
	return MeasureText(FormatSessionList(sessions, session))
}

// switchSessionRowFormat mirrors build_list in
// scripts/muxcode-switch-session.sh, which renders this popup's rows. The
// popup is sized before it opens and cannot be resized afterwards, so the
// measurer has to reproduce the script's layout rather than read it back.
// TestSwitchSessionFormatMatchesScript pins the two together, so changing the
// script's format without changing this one fails the suite instead of
// silently mis-sizing the popup.
const switchSessionRowFormat = "  %-20s  %2d windows  %s%s"

// switchSessionTimeLayout is the Go spelling of the script's date format,
// '+%b %d %H:%M'.
const switchSessionTimeLayout = "Jan 02 15:04"

// switchSessionListFormat is the tmux -F string the script queries with. It is
// pinned alongside the row format: measuring different fields than the script
// lists would size the popup for rows it never draws.
const switchSessionListFormat = "#{session_name}|#{session_windows}|#{session_created}|#{?session_attached,attached,}"

const (
	switchSessionHeader = "● current  ◆ attached elsewhere"
	switchSessionPrompt = "Switch to: "
	// fzf draws a pointer column to the left of every row.
	switchSessionPointerCols = 2
	// fzf draws a scrollbar once the list is taller than the popup. The popup
	// cannot resize after it opens, so the column is reserved up front rather
	// than only when the current list happens to overflow — without it the
	// rows are sized to the exact interior width and the scrollbar truncates
	// the last column of every row.
	switchSessionScrollbarCols = 1
)

// MeasureSwitchSession sizes the session switcher from the tmux session list
// its script renders. The script filters as the user types, but the popup
// cannot resize once open, so this measures the unfiltered list.
//
// Returns (0, 0) when tmux reports nothing, which leaves the popup on
// percentage sizing rather than opening it at the minimum width.
func MeasureSwitchSession(session string) (int, int) {
	rows := switchSessionRows(session)
	if len(rows) == 0 {
		return 0, 0
	}
	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, switchSessionHeader, switchSessionPrompt)
	gutter := strings.Repeat(" ", switchSessionPointerCols+switchSessionScrollbarCols)
	for _, r := range rows {
		lines = append(lines, gutter+r)
	}
	return MeasureLines(lines)
}

// switchSessionRows renders the session rows exactly as the popup's script
// does. Returns nil when tmux is unavailable or lists no sessions.
func switchSessionRows(current string) []string {
	out, err := exec.Command("tmux", "list-sessions", "-F", switchSessionListFormat).Output()
	if err != nil {
		return nil
	}
	return parseSwitchSessionRows(string(out), current)
}

// parseSwitchSessionRows formats tmux list-sessions output into the rows the
// script draws. Split from the exec above so the formatting - which is what
// the popup is sized from - can be tested without a running tmux server.
func parseSwitchSessionRows(out, current string) []string {
	var rows []string
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if ln == "" {
			continue
		}
		f := strings.Split(ln, "|")
		if len(f) < 4 {
			continue
		}
		name, attached := f[0], f[3]
		windows, _ := strconv.Atoi(f[1])
		created := "unknown"
		if ts, err := strconv.ParseInt(f[2], 10, 64); err == nil {
			created = time.Unix(ts, 0).Format(switchSessionTimeLayout)
		}
		marker := ""
		switch {
		case name == current:
			marker = " ●"
		case attached != "":
			marker = " ◆"
		}
		rows = append(rows, fmt.Sprintf(switchSessionRowFormat, name, windows, created, marker))
	}
	return rows
}
