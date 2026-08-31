package bus

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The control pane (MUX-108) is a fixed full-width pane at the bottom of
// agent windows hosting global muxcode TUIs — currently the graph UI.
// It is ALWAYS created after panes 0 and 1: AgentPane() is a hardcoded
// "1" and every path that reaches an agent resolves through PaneTarget,
// so creation order is the delivery contract — and a slip here breaks
// every agent's delivery at once, with messages typing into an nvim
// buffer rather than crashing. (Related: select-layout preserves pane
// indices; rotate-window does not, which is why no rotate binding
// exists.) Border styling is global in config/tmux.conf, not applied
// here — 12 windows must not each re-apply it.

const controlPaneDefaultHeight = 18

// controlPaneBaseCmd is the identity of a control pane: every surface
// variant starts with it, and scanControlPanes matches panes by their
// tmux start command against this prefix. Old-binary panes were split
// with the same command, so identification is retroactive.
const controlPaneBaseCmd = "muxcode graph ui"

// controlPaneCommand returns what the pane runs. The surface is
// selectable via MUXCODE_CONTROL_PANE_SURFACE so a second control
// surface never needs a second pane; an unknown name degrades to the
// graph UI rather than an empty pane.
func controlPaneCommand() string {
	switch strings.TrimSpace(os.Getenv("MUXCODE_CONTROL_PANE_SURFACE")) {
	case "prompt":
		return controlPaneBaseCmd + " --prompt"
	case "gates":
		return controlPaneBaseCmd + " --gates"
	case "launcher", "templates":
		return controlPaneBaseCmd + " --templates"
	default:
		return controlPaneBaseCmd
	}
}

// ControlPaneHeight returns the pane height in rows.
func ControlPaneHeight() int {
	if v := os.Getenv("MUXCODE_CONTROL_PANE_HEIGHT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return controlPaneDefaultHeight
}

// ControlPanesEnabled reports whether the control pane is enabled for
// this session at all. The gate auto-show popup is suppressed when it
// is — the pane makes gates ambient on every window.
func ControlPanesEnabled() bool {
	return os.Getenv("MUXCODE_CONTROL_PANE_DISABLE") != "1"
}

// ControlPaneEnabledFor reports whether a window gets the control pane:
// on by default for every window; MUXCODE_CONTROL_PANE_EXCLUDE names
// windows that opt out; MUXCODE_CONTROL_PANE_DISABLE=1 turns the
// feature off wholesale.
func ControlPaneEnabledFor(win string) bool {
	if !ControlPanesEnabled() {
		return false
	}
	for _, w := range strings.Split(os.Getenv("MUXCODE_CONTROL_PANE_EXCLUDE"), ",") {
		if strings.TrimSpace(w) == win && win != "" {
			return false
		}
	}
	return true
}

// CreateControlPane creates the control pane on a window: a detached
// full-width bottom split running the graph UI, titled. Call only after
// panes 0 and 1 exist.
//
// -e pins the session: the pane inherits the tmux SERVER environment,
// where another session's BUS_SESSION can leak in — a scratch-session
// pane then watches the wrong bus dir and never sees its own gates
// (found by the integration script's first live run). -P -F prints the
// new pane's id so the title lands on the pane just created — titling
// ".2" by assumption is how a racing second creator once titled
// somebody else's pane and left its own on the hostname default
// (2026-08-26 duplicate-pane incident).
func CreateControlPane(session, win string) error {
	target := session + ":" + win
	out, err := tmuxOutputRunner("split-window", "-vf", "-d", "-l", strconv.Itoa(ControlPaneHeight()),
		"-e", "BUS_SESSION="+session, "-P", "-F", "#{pane_id}",
		"-t", target, controlPaneCommand())
	if err != nil {
		return err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		id = target + ".2"
	}
	// Identity tag (MUX-117) — resolution failure is logged, not fatal:
	// start-command matching still identifies the pane retroactively.
	if err := TagPane(id, PaneTagControl); err != nil {
		LogLifecycle(session, "error", "pane", "pane-tag-failed",
			"control pane "+id+" on "+win+": "+err.Error())
	}
	return TmuxRun("select-pane", "-t", id, "-T", " GRAPH ")
}

// The selected surface is shared across every control pane: each pane is
// its own process, so without shared state the plan window sits on
// Pending Gates while edit shows Graph Runs — switching windows loses
// your place (user-reported 2026-08-26). Panes write on a user-driven
// surface change and adopt on tick; sync reads never write back, so
// convergence is one-way.
func controlPaneSurfaceFile(session string) string {
	return filepath.Join(BusDir(session), "control-pane-surface")
}

// WriteControlPaneSurface records the shared surface selection.
func WriteControlPaneSurface(session, surface string) {
	_ = os.WriteFile(controlPaneSurfaceFile(session), []byte(surface), 0644)
}

// ReadControlPaneSurface returns the shared surface selection, if any.
func ReadControlPaneSurface(session string) (string, bool) {
	data, err := os.ReadFile(controlPaneSurfaceFile(session))
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(data))
	return s, s != ""
}

// The ready marker records when the launcher finished building this
// session's windows and control panes. The daemon's recycle-on-install
// consults it: panes are recycled onto a fresh binary only when they
// PREDATE the daemon's own start. Without that distinction a daemon
// started mid-launch treated half-built windows as pane-less and
// created its own — the duplicate-control-pane incident (2026-08-26).
func controlPaneReadyMarker(session string) string {
	return filepath.Join(BusDir(session), "control-panes-ready")
}

// WriteControlPaneReadyMarker stamps the marker with the current time.
func WriteControlPaneReadyMarker(session string) {
	_ = os.WriteFile(controlPaneReadyMarker(session),
		[]byte(strconv.FormatInt(time.Now().Unix(), 10)), 0644)
}

// ControlPanesPredate reports whether the session's control panes were
// built before the given unix time. A missing or unreadable marker
// reads as predating — recycling a fresh pane is a flicker, skipping a
// stale one leaves an old binary running.
func ControlPanesPredate(session string, t int64) bool {
	data, err := os.ReadFile(controlPaneReadyMarker(session))
	if err != nil {
		return true
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return true
	}
	return ts < t
}

// controlPaneScan is one window's control-pane census. Panes are
// identified by tmux start command (prefix controlPaneBaseCmd) at index
// 2 or higher — never by assuming index 2 is ours, which cannot see a
// duplicate that landed at index 3.
type controlPaneScan struct {
	ours     []string // pane ids of control panes, ascending index
	foreign2 bool     // a pane 2 exists that is not ours (a user's split)
}

func scanControlPanes(session, win string) (controlPaneScan, error) {
	var scan controlPaneScan
	out, err := tmuxOutputRunner("list-panes", "-t", session+":"+win,
		"-F", "#{pane_id}:#{pane_index}:#{pane_start_command}")
	if err != nil {
		return scan, err
	}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(ln, ":", 3)
		if len(parts) != 3 {
			continue
		}
		idx, err := strconv.Atoi(parts[1])
		if err != nil || idx < 2 {
			continue
		}
		// tmux re-quotes the stored command when it contains spaces:
		// #{pane_start_command} reports `"muxcode graph ui"`, literal
		// quotes included (verified live — the unquoted assumption made
		// every control pane scan as foreign and no-op'd the dedupe).
		start := strings.Trim(parts[2], `"'`)
		if strings.HasPrefix(start, controlPaneBaseCmd) {
			scan.ours = append(scan.ours, parts[0])
		} else if idx == 2 {
			scan.foreign2 = true
		}
	}
	return scan, nil
}

// ClampControlPane re-applies the fixed height to a window's control
// pane. Window refits (resize-window on attach or a terminal resize)
// rescale panes PROPORTIONALLY, so the fixed-height strip drifts with
// every geometry change — a pane split at 14 rows of an 80x24 detached
// window opens at over half the attached screen. Callers run this after
// any resize. A pane that is not ours is never touched, and win may be
// a window index as well as a name.
func ClampControlPane(session, win string) {
	scan, err := scanControlPanes(session, win)
	if err != nil || len(scan.ours) == 0 {
		return
	}
	_ = TmuxRun("resize-pane", "-t", scan.ours[0],
		"-y", strconv.Itoa(ControlPaneHeight()))
}

// EnsureControlPane converges a window to exactly one control pane:
// duplicates beyond the lowest-index pane are killed (two creators
// racing at session launch once left a second, untitled graph pane on
// the window — 2026-08-26), a missing pane respawns, and with recycle
// the survivor is killed and relaunched on the freshly-installed
// binary. A foreign pane 2 (the user's own split) is NEVER touched and
// suppresses creation.
func EnsureControlPane(session, win string, recycle bool) error {
	scan, err := scanControlPanes(session, win)
	if err != nil {
		return err
	}
	if len(scan.ours) > 1 {
		for _, id := range scan.ours[1:] {
			if err := TmuxRun("kill-pane", "-t", id); err != nil {
				return err
			}
		}
	}
	switch {
	case len(scan.ours) == 0 && scan.foreign2:
		return nil
	case len(scan.ours) > 0 && !recycle:
		return nil
	case len(scan.ours) > 0:
		if err := TmuxRun("kill-pane", "-t", scan.ours[0]); err != nil {
			return err
		}
	}
	return CreateControlPane(session, win)
}
