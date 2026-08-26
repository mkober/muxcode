package bus

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

const controlPaneDefaultHeight = 14

// controlPaneCommand returns what the pane runs. The surface is
// selectable via MUXCODE_CONTROL_PANE_SURFACE so a second control
// surface never needs a second pane; an unknown name degrades to the
// graph UI rather than an empty pane.
func controlPaneCommand() string {
	switch strings.TrimSpace(os.Getenv("MUXCODE_CONTROL_PANE_SURFACE")) {
	case "gates":
		return "muxcode graph ui --gates"
	case "launcher", "templates":
		return "muxcode graph ui --templates"
	default:
		return "muxcode graph ui"
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
func CreateControlPane(session, win string) error {
	target := session + ":" + win
	// -e pins the session: the pane inherits the tmux SERVER environment,
	// where another session's BUS_SESSION can leak in — a scratch-session
	// pane then watches the wrong bus dir and never sees its own gates
	// (found by the integration script's first live run).
	if err := TmuxRun("split-window", "-vf", "-d", "-l", strconv.Itoa(ControlPaneHeight()),
		"-e", "BUS_SESSION="+session,
		"-t", target, controlPaneCommand()); err != nil {
		return err
	}
	return TmuxRun("select-pane", "-t", target+".2", "-T", " GRAPH ")
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

// controlPaneStatus reports whether a window's pane 2 exists and
// whether it runs the control pane binary.
func controlPaneStatus(session, win string) (present, isPane bool) {
	out, err := tmuxOutputRunner("list-panes", "-t", session+":"+win, "-F", "#{pane_index}:#{pane_current_command}")
	if err != nil {
		return false, false
	}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(ln, "2:") {
			return true, strings.TrimPrefix(ln, "2:") == "muxcode"
		}
	}
	return false, false
}

// ClampControlPane re-applies the fixed height to a window's control
// pane. Window refits (resize-window on attach or a terminal resize)
// rescale panes PROPORTIONALLY, so the fixed-height strip drifts with
// every geometry change — a pane split at 14 rows of an 80x24 detached
// window opens at over half the attached screen. Callers run this after
// any resize. A pane 2 that is not ours is never touched, and win may
// be a window index as well as a name.
func ClampControlPane(session, win string) {
	if present, isPane := controlPaneStatus(session, win); present && isPane {
		_ = TmuxRun("resize-pane", "-t", session+":"+win+".2",
			"-y", strconv.Itoa(ControlPaneHeight()))
	}
}

// EnsureControlPane respawns a missing control pane and, with recycle,
// kills a live one to relaunch on the freshly-installed binary (the
// daemon recycles once per start, and every install restarts the
// daemon). A pane 2 running something other than the control pane is
// NEVER touched — it is a user's own split, not ours to kill.
func EnsureControlPane(session, win string, recycle bool) error {
	present, isPane := controlPaneStatus(session, win)
	switch {
	case present && !isPane:
		return nil
	case present && isPane && !recycle:
		return nil
	case present && isPane && recycle:
		if err := TmuxRun("kill-pane", "-t", session+":"+win+".2"); err != nil {
			return err
		}
	}
	return CreateControlPane(session, win)
}
