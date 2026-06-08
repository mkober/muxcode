package bus

import (
	"strconv"
	"strings"
)

// windowEntry describes one tmux window across all sessions on the server.
type windowEntry struct {
	session  string
	index    string
	attached bool // true if the window's session has a connected client
}

// target returns the "session:index" form used by tmux -t flags.
func (w windowEntry) target() string {
	return w.session + ":" + w.index
}

// listAllWindows returns every window across every session on the tmux server,
// tagged with whether its session currently has an attached client.
//
// Fields are tab-delimited so a session name containing ':' (a legal tmux
// session character) does not corrupt the split — the inline-hook form had to
// use `cut -d:` and could only ever see the current session.
func listAllWindows() ([]windowEntry, error) {
	out, err := TmuxOutput("list-windows", "-a", "-F",
		"#{session_name}\t#{window_index}\t#{session_attached}")
	if err != nil {
		return nil, err
	}
	var entries []windowEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		entries = append(entries, windowEntry{
			session:  fields[0],
			index:    fields[1],
			attached: fields[2] == "1",
		})
	}
	return entries, nil
}

// windowSize returns the current dimensions of a single window. Used after
// pass 1 to read an attached window's *post-resize* fit size — a targeted
// display-message rather than a second full `list-windows -a` scan.
func windowSize(target string) (width, height int, ok bool) {
	out, err := TmuxOutput("display-message", "-t", target, "-p",
		"#{window_width}\t#{window_height}")
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Split(strings.TrimSpace(out), "\t")
	if len(fields) < 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(fields[0])
	h, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// ResizeAllWindows resizes every window in every tmux session to fit the
// connected client. The client-resized hook only auto-fits the session the
// client is viewing, so detached subsessions keep their old (often clipped)
// geometry after a monitor/terminal resize until the user switches to them and
// resizes by hand. This pushes the new size to all of them at once.
//
// Two passes, because `resize-window -A` fits a window to its *largest connected
// client* and is a no-op for a session with no client attached:
//
//  1. Attached sessions are resized with -A — the ideal path, since -A accounts
//     for the status bar and honours each client's true size.
//  2. The fit size is read back from an attached window (every session shares
//     the same status-bar geometry, so this is the correct window size for the
//     current client) and pushed explicitly to every detached window with
//     resize-window -x/-y. That explicit size is overridden automatically the
//     next time a client attaches, so it does no harm.
func ResizeAllWindows() error {
	entries, err := listAllWindows()
	if err != nil {
		return err
	}

	// Pass 1: refit windows in attached sessions to their client (-A) and
	// remember the first attached window so we can read its fit size back.
	var fitTarget string
	for _, e := range entries {
		if e.attached {
			TmuxResizeWindow(e.target())
			if fitTarget == "" {
				fitTarget = e.target()
			}
		}
	}
	if fitTarget == "" {
		// No attached client to copy from — nothing more we can do.
		return nil
	}

	// Read the fit size back from that window. It must be read *after* pass 1
	// so it reflects the post-refit (un-clipped) geometry for the current
	// client; reading the pre-pass listing could capture a stale clipped size.
	fw, fh, ok := windowSize(fitTarget)
	if !ok {
		return nil
	}

	// Pass 2: push that fit size to every window in detached sessions.
	for _, e := range entries {
		if !e.attached {
			TmuxResizeWindowToSize(e.target(), fw, fh)
		}
	}
	return nil
}
