package bus

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestControlPaneEnabledFor(t *testing.T) {
	// Default: every window gets the pane (opt-out, not opt-in).
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_DISABLE")
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_EXCLUDE")
	for _, win := range []string{"edit", "watch", "commit"} {
		if !ControlPaneEnabledFor(win) {
			t.Errorf("default must enable the pane on %s", win)
		}
	}

	t.Setenv("MUXCODE_CONTROL_PANE_EXCLUDE", "watch, commit")
	if ControlPaneEnabledFor("watch") || ControlPaneEnabledFor("commit") {
		t.Error("excluded windows must not get the pane")
	}
	if !ControlPaneEnabledFor("edit") {
		t.Error("non-excluded windows keep the pane")
	}

	t.Setenv("MUXCODE_CONTROL_PANE_DISABLE", "1")
	if ControlPaneEnabledFor("edit") || ControlPanesEnabled() {
		t.Error("DISABLE=1 must turn the feature off wholesale")
	}
}

func TestControlPaneHeight(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_HEIGHT")
	if h := ControlPaneHeight(); h != controlPaneDefaultHeight {
		t.Errorf("default height = %d, want %d", h, controlPaneDefaultHeight)
	}
	t.Setenv("MUXCODE_CONTROL_PANE_HEIGHT", "20")
	if h := ControlPaneHeight(); h != 20 {
		t.Errorf("env height = %d, want 20", h)
	}
	t.Setenv("MUXCODE_CONTROL_PANE_HEIGHT", "junk")
	if h := ControlPaneHeight(); h != controlPaneDefaultHeight {
		t.Errorf("junk height must fall back, got %d", h)
	}
}

// stubPaneList stubs both tmux runners: list-panes reports the given
// pane lines (#{pane_id}:#{pane_index}:#{pane_start_command}),
// split-window returns a fresh pane id, and every call — run or output
// — lands in the returned log.
func stubPaneList(t *testing.T, lines string) *[][]string {
	t.Helper()
	calls := stubTmuxRunner(t)
	origOut := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		*calls = append(*calls, args)
		if len(args) > 0 && args[0] == "split-window" {
			return "%9", nil
		}
		return lines, nil
	}
	t.Cleanup(func() { tmuxOutputRunner = origOut })
	return calls
}

func joinCalls(calls *[][]string) string {
	out := ""
	for _, c := range *calls {
		out += strings.Join(c, " ") + "\n"
	}
	return out
}

// One detached full-width split created after panes 0/1, then titled —
// asserted at argv level because the pane-index contract lives in these
// flags (-vf full-width, -d detached, window target appends last). The
// title must land on the pane id split-window printed, never on an
// assumed ".2" — the duplicate-pane incident left an untitled pane
// because two creators both titled index 2.
func TestCreateControlPane_Argv(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_HEIGHT")
	calls := stubPaneList(t, "")

	if err := CreateControlPane("s", "edit"); err != nil {
		t.Fatalf("CreateControlPane: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected split + title calls, got %v", *calls)
	}
	split := strings.Join((*calls)[0], " ")
	for _, want := range []string{"split-window", "-vf", "-d", "-l 14", "-t s:edit", "muxcode graph ui", "-e BUS_SESSION=s", "-P -F #{pane_id}"} {
		if !strings.Contains(split, want) {
			t.Errorf("split call missing %q: %s", want, split)
		}
	}
	title := strings.Join((*calls)[1], " ")
	if !strings.Contains(title, "select-pane") || !strings.Contains(title, " GRAPH ") || !strings.Contains(title, "%9") {
		t.Errorf("title must target the printed pane id: %s", title)
	}
	if strings.Contains(title, "s:edit.2") {
		t.Errorf("title must not assume index 2: %s", title)
	}
}

// The surface is selectable; an unknown name degrades to the graph UI
// rather than an empty pane.
func TestControlPaneSurfaceSelectable(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_SURFACE")
	if c := controlPaneCommand(); c != "muxcode graph ui" {
		t.Errorf("default surface = %q", c)
	}
	t.Setenv("MUXCODE_CONTROL_PANE_SURFACE", "gates")
	if c := controlPaneCommand(); c != "muxcode graph ui --gates" {
		t.Errorf("gates surface = %q", c)
	}
	t.Setenv("MUXCODE_CONTROL_PANE_SURFACE", "launcher")
	if c := controlPaneCommand(); c != "muxcode graph ui --templates" {
		t.Errorf("launcher surface = %q", c)
	}
	t.Setenv("MUXCODE_CONTROL_PANE_SURFACE", "no-such-surface")
	if c := controlPaneCommand(); c != "muxcode graph ui" {
		t.Errorf("unknown surface must degrade to the graph UI, got %q", c)
	}
}

// A missing pane respawns; a live one recycles only when asked; a
// foreign pane 2 (the user's own split) is never touched. Surface
// variants (--gates) still identify as ours.
func TestEnsureControlPane(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_HEIGHT")

	calls := stubPaneList(t, "%0:0:\n%1:1:")
	if err := EnsureControlPane("s", "edit", false); err != nil {
		t.Fatalf("EnsureControlPane: %v", err)
	}
	if j := joinCalls(calls); !strings.Contains(j, "split-window") {
		t.Errorf("missing pane must respawn:\n%s", j)
	}

	calls = stubPaneList(t, "%0:0:\n%1:1:\n%2:2:muxcode graph ui --gates")
	if err := EnsureControlPane("s", "edit", false); err != nil {
		t.Fatalf("EnsureControlPane: %v", err)
	}
	if j := joinCalls(calls); strings.Contains(j, "split-window") || strings.Contains(j, "kill-pane") {
		t.Errorf("healthy pane without recycle must be untouched:\n%s", j)
	}

	calls = stubPaneList(t, "%0:0:\n%1:1:\n%2:2:muxcode graph ui")
	if err := EnsureControlPane("s", "edit", true); err != nil {
		t.Fatalf("EnsureControlPane recycle: %v", err)
	}
	if j := joinCalls(calls); !strings.Contains(j, "kill-pane -t %2") || !strings.Contains(j, "split-window") {
		t.Errorf("recycle must kill then respawn:\n%s", j)
	}

	calls = stubPaneList(t, "%0:0:\n%1:1:\n%2:2:htop")
	if err := EnsureControlPane("s", "edit", true); err != nil {
		t.Fatalf("EnsureControlPane foreign: %v", err)
	}
	if j := joinCalls(calls); strings.Contains(j, "kill-pane") || strings.Contains(j, "split-window") {
		t.Errorf("a foreign pane 2 must never be touched:\n%s", j)
	}
}

// Duplicate control panes converge to one: two creators racing at
// session launch once left a second, untitled graph pane on the window
// (2026-08-26). The lowest-index pane survives; extras die even
// without recycle; with recycle everything dies and one respawns.
func TestEnsureControlPane_DedupesDuplicates(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_HEIGHT")

	calls := stubPaneList(t, "%0:0:\n%1:1:\n%2:2:muxcode graph ui\n%3:3:muxcode graph ui")
	if err := EnsureControlPane("s", "edit", false); err != nil {
		t.Fatalf("EnsureControlPane dup: %v", err)
	}
	j := joinCalls(calls)
	if !strings.Contains(j, "kill-pane -t %3") {
		t.Errorf("duplicate pane must be killed:\n%s", j)
	}
	if strings.Contains(j, "kill-pane -t %2") || strings.Contains(j, "split-window") {
		t.Errorf("survivor must be untouched and nothing respawned:\n%s", j)
	}

	calls = stubPaneList(t, "%0:0:\n%1:1:\n%2:2:muxcode graph ui\n%3:3:muxcode graph ui")
	if err := EnsureControlPane("s", "edit", true); err != nil {
		t.Fatalf("EnsureControlPane dup recycle: %v", err)
	}
	j = joinCalls(calls)
	for _, want := range []string{"kill-pane -t %3", "kill-pane -t %2", "split-window"} {
		if !strings.Contains(j, want) {
			t.Errorf("dup+recycle missing %q:\n%s", want, j)
		}
	}
}

// The clamp re-applies the fixed height, and only to a pane that is
// ours — a foreign pane 2 (or no pane 2) is never resized.
func TestClampControlPane(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_HEIGHT")

	calls := stubPaneList(t, "%0:0:\n%1:1:\n%2:2:muxcode graph ui")
	ClampControlPane("s", "edit")
	j := joinCalls(calls)
	for _, want := range []string{"resize-pane", "-t %2", "-y 14"} {
		if !strings.Contains(j, want) {
			t.Errorf("clamp missing %q:\n%s", want, j)
		}
	}

	calls = stubPaneList(t, "%0:0:\n%1:1:\n%2:2:htop")
	ClampControlPane("s", "edit")
	if j := joinCalls(calls); strings.Contains(j, "resize-pane") {
		t.Errorf("a foreign pane 2 must never be resized:\n%s", j)
	}

	calls = stubPaneList(t, "%0:0:\n%1:1:")
	ClampControlPane("s", "edit")
	if j := joinCalls(calls); strings.Contains(j, "resize-pane") {
		t.Errorf("a missing pane 2 must not be resized:\n%s", j)
	}
}

// Recycle-on-install fires only for panes that predate the daemon: a
// missing or stale marker reads as predating (recycling a fresh pane
// is a flicker; skipping a stale one leaves an old binary running),
// while a marker stamped after the given time — the launcher finishing
// while this daemon was already up — suppresses it.
func TestControlPanesPredate(t *testing.T) {
	session := "cp-marker-test"
	dir := BusDir(session)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Remove(controlPaneReadyMarker(session))

	now := time.Now().Unix()
	if !ControlPanesPredate(session, now) {
		t.Error("missing marker must read as predating")
	}
	WriteControlPaneReadyMarker(session)
	if ControlPanesPredate(session, now-5) {
		t.Error("a marker stamped after t must not predate t")
	}
	if !ControlPanesPredate(session, now+60) {
		t.Error("a marker stamped before t must predate t")
	}
}
