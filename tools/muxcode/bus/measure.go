package bus

import (
	"strings"
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

// MeasureAgentStatus sizes the agent status modal from its rendered table.
func MeasureAgentStatus(session string) (int, int) {
	return MeasureText(FormatStatusTable(GetAllAgentStatus(session)))
}

// MeasureProcList sizes the process modal from the proc and spawn lists it
// concatenates.
func MeasureProcList(session string) (int, int) {
	procs, err := ReadProcEntries(session)
	if err != nil {
		return 0, 0
	}
	spawns, err := ReadSpawnEntries(session)
	if err != nil {
		return 0, 0
	}
	return MeasureText(FormatProcList(procs, false) + "\n" + FormatSpawnList(spawns, false))
}

// MeasureCronList sizes the cron modal from its rendered list.
func MeasureCronList(session string) (int, int) {
	entries, err := ReadCronEntries(session)
	if err != nil {
		return 0, 0
	}
	return MeasureText(FormatCronList(entries, false))
}

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
