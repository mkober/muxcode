package bus

import (
	"strings"
	"testing"
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

// stubPaneList makes list-panes report the given pane lines.
func stubPaneList(t *testing.T, lines string) *[][]string {
	t.Helper()
	calls := stubTmuxRunner(t)
	origOut := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) { return lines, nil }
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
// flags (-vf full-width, -d detached, window target appends last).
func TestCreateControlPane_Argv(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_HEIGHT")
	calls := stubTmuxRunner(t)

	if err := CreateControlPane("s", "edit"); err != nil {
		t.Fatalf("CreateControlPane: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected split + title calls, got %v", *calls)
	}
	split := strings.Join((*calls)[0], " ")
	for _, want := range []string{"split-window", "-vf", "-d", "-l 14", "-t s:edit", "muxcode graph ui", "-e BUS_SESSION=s"} {
		if !strings.Contains(split, want) {
			t.Errorf("split call missing %q: %s", want, split)
		}
	}
	title := strings.Join((*calls)[1], " ")
	if !strings.Contains(title, "select-pane") || !strings.Contains(title, " GRAPH ") || !strings.Contains(title, "s:edit.2") {
		t.Errorf("title call wrong: %s", title)
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
// foreign pane 2 (the user's own split) is never touched.
func TestEnsureControlPane(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_CONTROL_PANE_HEIGHT")

	calls := stubPaneList(t, "0:nvim\n1:claude")
	if err := EnsureControlPane("s", "edit", false); err != nil {
		t.Fatalf("EnsureControlPane: %v", err)
	}
	if j := joinCalls(calls); !strings.Contains(j, "split-window") {
		t.Errorf("missing pane must respawn:\n%s", j)
	}

	calls = stubPaneList(t, "0:nvim\n1:claude\n2:muxcode")
	if err := EnsureControlPane("s", "edit", false); err != nil {
		t.Fatalf("EnsureControlPane: %v", err)
	}
	if j := joinCalls(calls); strings.Contains(j, "split-window") || strings.Contains(j, "kill-pane") {
		t.Errorf("healthy pane without recycle must be untouched:\n%s", j)
	}

	calls = stubPaneList(t, "0:nvim\n1:claude\n2:muxcode")
	if err := EnsureControlPane("s", "edit", true); err != nil {
		t.Fatalf("EnsureControlPane recycle: %v", err)
	}
	if j := joinCalls(calls); !strings.Contains(j, "kill-pane") || !strings.Contains(j, "split-window") {
		t.Errorf("recycle must kill then respawn:\n%s", j)
	}

	calls = stubPaneList(t, "0:nvim\n1:claude\n2:htop")
	if err := EnsureControlPane("s", "edit", true); err != nil {
		t.Fatalf("EnsureControlPane foreign: %v", err)
	}
	if j := joinCalls(calls); strings.Contains(j, "kill-pane") || strings.Contains(j, "split-window") {
		t.Errorf("a foreign pane 2 must never be touched:\n%s", j)
	}
}
