package bus

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Pane identity (MUX-117). Panes are resolved by the @muxcode_pane
// per-pane user option stamped at creation, never by position: tmux
// assigns indices by position, so a split landing before the agent
// renumbers it, and index-addressed delivery then types keystrokes into
// whatever pane took the index — an nvim buffer or a git TUI — while
// reporting success.
//
// The window-level @muxcode_tagged option is a positive record that
// tagging RAN on a per-pane-capable tmux — not that it succeeded. The
// resolver branches on that marker, never on tag absence, because
// absence is ambiguous between "window predates tagging" (index
// fallback is safe — old binaries honored the creation-order
// convention) and "tagging attempted and failed" (an index fallback
// would reintroduce exactly the misdelivery this mechanism removes).
// A window whose tagging failed on capable tmux is therefore marked
// anyway — broken resolves as a loud error, never as legacy. The one
// legitimate legacy world is a tmux that rejects per-pane options
// outright (`set-option -p`, tmux >= 3.0), detected by an explicit
// probe: there no window is ever marked, and every window resolves
// through the legacy path with the degradation logged at launch.

// Canonical @muxcode_pane tag values.
const (
	PaneTagLeft    = "left"
	PaneTagAgent   = "agent"
	PaneTagControl = "control"
)

const (
	paneTagOption      = "@muxcode_pane"
	windowTaggedOption = "@muxcode_tagged"
)

// unresolvedPaneSentinel is the pane part PaneTarget substitutes when
// identity resolution fails: not an index, so tmux rejects the target
// ("can't find pane") instead of delivering keystrokes to whatever pane
// occupies a number.
const unresolvedPaneSentinel = "{unresolved}"

// paneIDPattern matches a tmux pane id (%N). listWindowPanes drops any
// census row without one: tmux always prints an id first, so a row
// missing it is not pane output (a test stub answering for a different
// tmux call, a transient error string) and must not influence resolution.
var paneIDPattern = regexp.MustCompile(`^%[0-9]+$`)

// legacyPaneIndex maps a tag to the pre-MUX-117 creation-order index
// convention: left pane 0, agent pane 1, control pane 2. Used only for
// windows without the @muxcode_tagged marker.
func legacyPaneIndex(tag string) string {
	switch tag {
	case PaneTagLeft:
		return "0"
	case PaneTagControl:
		return "2"
	default:
		return "1"
	}
}

// TagPane stamps the @muxcode_pane identity tag on one pane.
func TagPane(target, tag string) error {
	return TmuxRun("set-option", "-p", "-t", target, paneTagOption, tag)
}

// ErrPaneTagUnsupported marks the documented degradation: this tmux
// rejects per-pane options, so no window is marked and everything
// resolves by the legacy index convention. Callers treat it as
// expected, unlike any other TagWindowPanes error.
var ErrPaneTagUnsupported = errors.New("per-pane options unsupported by this tmux — legacy index resolution in effect")

// tmuxRejectsFlag reports a parse-time rejection — the capability is
// absent, not transiently failing. Checks the error text and, for real
// tmux runs, the stderr an exec.ExitError captured.
func tmuxRejectsFlag(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		s += " " + strings.ToLower(string(ee.Stderr))
	}
	return strings.Contains(s, "usage:") || strings.Contains(s, "unknown") || strings.Contains(s, "invalid")
}

// paneOptionsSupported probes per-pane option support via show-options
// -p, whose TmuxOutput error carries stderr. Inconclusive errors read
// as supported so real failures stay loud instead of hiding as legacy.
func paneOptionsSupported(target string) bool {
	_, err := TmuxOutput("show-options", "-p", "-t", target)
	return !tmuxRejectsFlag(err)
}

// paneEntry is one row of a window's pane census.
type paneEntry struct {
	id     string
	tag    string
	tagged bool // window-level @muxcode_tagged marker (same on every row)
}

// listWindowPanes reads a window's pane ids, identity tags, and the
// window marker in a single tmux call. Unset user options render as
// empty format fields, so the colon-separated row parses cleanly for
// tagged and untagged panes alike.
func listWindowPanes(session, window string) ([]paneEntry, error) {
	out, err := TmuxOutput("list-panes", "-t", session+":"+window,
		"-F", "#{pane_id}:#{"+paneTagOption+"}:#{"+windowTaggedOption+"}")
	if err != nil {
		return nil, err
	}
	var entries []paneEntry
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.SplitN(ln, ":", 3)
		if len(parts) != 3 || !paneIDPattern.MatchString(strings.TrimSpace(parts[0])) {
			continue
		}
		entries = append(entries, paneEntry{
			id:     strings.TrimSpace(parts[0]),
			tag:    strings.TrimSpace(parts[1]),
			tagged: strings.TrimSpace(parts[2]) != "",
		})
	}
	return entries, nil
}

// TagWindowPanes tags a freshly created two-pane window (pane 0 left,
// pane 1 agent), verifies the tags read back, and stamps the window's
// @muxcode_tagged marker. Index targets are safe here and only here:
// the caller just created both panes, so the creation-order convention
// holds by construction at this instant.
//
// Failure semantics (review must-fix, 2026-08-31): on a tmux that
// rejects per-pane options both tag writes fail at parse time — the
// probe confirms it, nothing is marked, and ErrPaneTagUnsupported
// reports the documented legacy degradation. Every OTHER failure —
// partial tag, total tag, dead read-back — still stamps the marker, so
// the broken window resolves as a loud error, never as legacy: a
// freshly created window whose tagging failed must not impersonate a
// pre-tagging one. Failures are logged as pane-tag-failed lifecycle
// events and returned; callers warn but continue — the marked-broken
// window fails deliveries loudly on its own.
func TagWindowPanes(session, window string) error {
	target := session + ":" + window
	err0 := TagPane(target+".0", PaneTagLeft)
	err1 := TagPane(target+".1", PaneTagAgent)
	if err0 != nil && err1 != nil && !paneOptionsSupported(target+".0") {
		logPaneEventOnce(session, window, "warn", "pane-tag-unsupported",
			fmt.Sprintf("tmux rejects per-pane options — window %s resolves by legacy index convention", window))
		return ErrPaneTagUnsupported
	}

	var failures []string
	if err0 != nil {
		failures = append(failures, PaneTagLeft+": "+err0.Error())
	}
	if err1 != nil {
		failures = append(failures, PaneTagAgent+": "+err1.Error())
	}

	entries, err := listWindowPanes(session, window)
	if err != nil {
		failures = append(failures, "read-back: "+err.Error())
	}
	verified := false
	for _, e := range entries {
		if e.tag != "" {
			verified = true
			break
		}
	}
	if merr := TmuxRun("set-option", "-w", "-t", target, windowTaggedOption, "1"); merr != nil {
		failures = append(failures, "marker: "+merr.Error())
	}
	if !verified && len(failures) == 0 {
		failures = append(failures, "no tag read back — window marked, unresolved tags will error loudly")
	}

	if len(failures) > 0 {
		err := fmt.Errorf("tag window %s: %s", window, strings.Join(failures, "; "))
		LogLifecycle(session, "error", "pane", "pane-tag-failed", err.Error())
		return err
	}
	return nil
}

// ResolvePane resolves (session, window, tag) to a tmux pane target by
// identity. Three-way outcome, branched on the window's @muxcode_tagged
// marker:
//
//  1. Marked window, tag present exactly once — the pane id (%N), which
//     tmux addresses directly and which survives renumbering.
//  2. Unmarked window — legacy fallback to the creation-order index,
//     logged as one pane-fallback lifecycle event per window per session.
//  3. Marked window, tag absent or claimed twice — an error, never an
//     index: the index may host an editor or a git TUI, and silent
//     misdelivery is the failure this resolver exists to remove.
//     (Duplicate-tag convergence is the control-pane sweep's job.)
//
// A window that yields no census at all (no server, no such window, or
// unparseable output) resolves to the legacy index silently: there is
// no evidence to branch on, and the follow-up tmux command fails loudly
// on its own when the window is genuinely absent.
func ResolvePane(session, window, tag string) (string, error) {
	entries, err := listWindowPanes(session, window)
	if err != nil || len(entries) == 0 {
		return session + ":" + window + "." + legacyPaneIndex(tag), nil
	}
	if !entries[0].tagged {
		logPaneEventOnce(session, window, "warn", "pane-fallback",
			fmt.Sprintf("window %s carries no %s marker — resolving by legacy index convention", window, windowTaggedOption))
		return session + ":" + window + "." + legacyPaneIndex(tag), nil
	}
	var matches []string
	for _, e := range entries {
		if e.tag == tag {
			matches = append(matches, e.id)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		err := fmt.Errorf("pane tag %q missing on tagged window %s:%s", tag, session, window)
		logPaneEventOnce(session, window, "error", "pane-resolve-failed", err.Error())
		return "", err
	default:
		err := fmt.Errorf("pane tag %q claimed by %d panes on window %s:%s", tag, len(matches), session, window)
		logPaneEventOnce(session, window, "error", "pane-resolve-failed", err.Error())
		return "", err
	}
}

// ResolvePaneTarget resolves a role's agent pane target by identity,
// returning the resolution error that PaneTarget swallows into its
// sentinel. Hosted roles resolve to their host window.
func ResolvePaneTarget(session, role string) (string, error) {
	return ResolvePane(session, WindowForRole(role), PaneTagAgent)
}

// logPaneEventOnce writes one lifecycle row per (event, window) per
// session. The resolver runs on every delivery, and a row per message
// would rotate the 1000-entry lifecycle log past its useful history;
// the marker file makes the throttle survive across processes (each CLI
// send is a fresh one). A session without a bus dir has nothing to
// record against, so the failed marker create doubles as the skip.
func logPaneEventOnce(session, window, level, event, detail string) {
	marker := filepath.Join(BusDir(session), event+"-"+window+".logged")
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	f.Close()
	LogLifecycle(session, level, "pane", event, detail)
}
