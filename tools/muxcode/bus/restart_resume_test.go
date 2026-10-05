package bus

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const restartResumeID = "8a744341-11bf-440f-b5d2-49248447a9c0"

const restartExitPane = "❯ \n\nResume this session with:\nclaude --resume " + restartResumeID + "\nuser@host muxcode $ "

// stubRestartPane serves pane to capture-pane until C-c is sent, then a pane
// whose banner the relaunch has typed over — so a scrape moved after the
// interrupt reads no id and the resume assertions fail rather than passing
// through the fresh fallback. Returns the ordered call log ("capture" or the
// send-keys argv).
func stubRestartPane(t *testing.T, pane string, captureErr error) *[][]string {
	t.Helper()
	var log [][]string
	interrupted := false

	origOut, origRun, origDelay := tmuxOutputRunner, tmuxRunner, restartInterruptDelay
	t.Cleanup(func() { tmuxOutputRunner, tmuxRunner, restartInterruptDelay = origOut, origRun, origDelay })
	restartInterruptDelay = 0

	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) == 0 || args[0] != "capture-pane" {
			return "", nil
		}
		log = append(log, []string{"capture"})
		if captureErr != nil {
			return "", captureErr
		}
		if interrupted {
			return "user@host muxcode $ muxcode agent launch edit", nil
		}
		return pane, nil
	}
	tmuxRunner = func(args ...string) error {
		log = append(log, args)
		if slices.Contains(args, "C-c") {
			interrupted = true
		}
		return nil
	}
	return &log
}

// launchTyped returns the relaunch command RestartLocalAgent typed, or "".
func launchTyped(log [][]string) string {
	for _, call := range log {
		if n := len(call); n >= 2 && call[0] == "send-keys" && call[n-1] == "Enter" {
			return call[n-2]
		}
	}
	return ""
}

// MUX-126 Phase 3: the scrape precedes the interrupt, and a hit relaunches
// with --resume <id>.
func TestRestartLocalAgent_ScrapesBeforeInterruptAndResumes(t *testing.T) {
	session := "restart-resume-hit"
	injectionTestSession(t, session)
	log := stubRestartPane(t, restartExitPane, nil)

	if err := RestartLocalAgent(session, "edit"); err != nil {
		t.Fatalf("RestartLocalAgent: %v", err)
	}

	capture := slices.IndexFunc(*log, func(c []string) bool { return c[0] == "capture" })
	interrupt := slices.IndexFunc(*log, func(c []string) bool { return slices.Contains(c, "C-c") })
	if capture < 0 || interrupt < 0 || capture > interrupt {
		t.Fatalf("scrape must precede C-c: capture at %d, interrupt at %d in %v", capture, interrupt, *log)
	}
	if want := "muxcode agent launch edit --reason restart --resume " + restartResumeID; launchTyped(*log) != want {
		t.Errorf("relaunch = %q, want %q", launchTyped(*log), want)
	}
	for _, event := range []string{"resume-scrape-hit", "agent-resume", "agent-relaunch"} {
		if n := countLifecycleEvents(t, session, event); n != 1 {
			t.Errorf("%s rows = %d, want 1", event, n)
		}
	}
	rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "agent-resume"})
	if want := "id=" + restartResumeID + " source=pane"; len(rows) != 1 || !strings.Contains(rows[0].Detail, want) {
		t.Errorf("agent-resume rows = %+v, want detail naming %q", rows, want)
	}
	if IsReloading(session, "edit") {
		t.Error("restart left its reload marker behind")
	}
}

// PR #95 review: a relaunch already holding the role's marker (a manual
// resume, or a second restart) makes the daemon restart refuse before any
// scrape or keystroke, and leaves the holder's marker in place.
func TestRestartLocalAgent_RefusesWhileMarkerHeld(t *testing.T) {
	session := "restart-resume-held"
	injectionTestSession(t, session)
	log := stubRestartPane(t, restartExitPane, nil)
	if err := writeReloadMarker(session, "edit"); err != nil {
		t.Fatal(err)
	}

	if err := RestartLocalAgent(session, "edit"); !errors.Is(err, ErrReloadMarkerHeld) {
		t.Fatalf("err = %v, want ErrReloadMarkerHeld", err)
	}
	if len(*log) != 0 {
		t.Errorf("a held marker must stop the restart before the pane is touched, got %v", *log)
	}
	if !IsReloading(session, "edit") {
		t.Error("restart removed a marker it does not own")
	}
}

// PR #95 review: ReloadAgent takes the same exclusive marker, so a reload
// started while a resume or restart holds it refuses before stopping the agent
// or typing, and leaves the holder's marker in place.
func TestReloadAgent_RefusesWhileMarkerHeld(t *testing.T) {
	session := "reload-held"
	injectionTestSession(t, session)
	log := stubRestartPane(t, restartExitPane, nil)
	release, err := acquireReloadMarker(session, "edit")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if err := ReloadAgent(session, "edit", "", "", false); !errors.Is(err, ErrReloadMarkerHeld) {
		t.Fatalf("err = %v, want ErrReloadMarkerHeld", err)
	}
	if len(*log) != 0 {
		t.Errorf("a held marker must stop the reload before the pane is touched, got %v", *log)
	}
	if !IsReloading(session, "edit") {
		t.Error("reload removed a marker it does not own")
	}
}

// The acquisition is exclusive: a second acquire fails until the first is
// released, then succeeds again.
func TestAcquireReloadMarker_Exclusive(t *testing.T) {
	session := "reload-marker-excl"
	injectionTestSession(t, session)

	release, err := acquireReloadMarker(session, "edit")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := acquireReloadMarker(session, "edit"); !errors.Is(err, ErrReloadMarkerHeld) {
		t.Fatalf("second acquire err = %v, want ErrReloadMarkerHeld", err)
	}
	release()
	if IsReloading(session, "edit") {
		t.Fatal("release left the marker")
	}
	again, err := acquireReloadMarker(session, "edit")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	again()
}

// The panes a restart finds no usable id in, with the row recording why.
var noResumeIDPanes = []struct {
	name       string
	pane       string
	captureErr error
	event      string
}{
	{"no banner", "❯ \nuser@host muxcode $ ", nil, "resume-scrape-miss"},
	{"capture failed", "", errors.New("no such pane"), "resume-scrape-miss"},
	{"resume already relaunched and died", restartExitPane +
		"muxcode agent launch edit --resume " + restartResumeID + "\nError: cannot find claude\nuser@host muxcode $ ",
		nil, "resume-scrape-stale"},
}

// Every road that finds no usable id relaunches an ordinary role fresh — the
// plain launch command, which carries the full flag set — and records why.
func TestRestartLocalAgent_FallsBackToFreshLaunch(t *testing.T) {
	t.Setenv("MUXCODE_PLAN_CLI", "claude")
	for _, c := range noResumeIDPanes {
		t.Run(c.name, func(t *testing.T) {
			session := "restart-resume-fresh"
			injectionTestSession(t, session)
			log := stubRestartPane(t, c.pane, c.captureErr)

			if err := RestartLocalAgent(session, "plan"); err != nil {
				t.Fatalf("RestartLocalAgent: %v", err)
			}
			if got := launchTyped(*log); got != "muxcode agent launch plan --reason restart" {
				t.Errorf("relaunch = %q, want a fresh launch", got)
			}
			if n := countLifecycleEvents(t, session, c.event); n != 1 {
				t.Errorf("%s rows = %d, want 1", c.event, n)
			}
		})
	}
}

// MUX-139 Phase 2: the same panes leave a Claude edit down — nothing typed,
// not even the interrupt — because a fresh edit discards the conversation the
// user was in. The marker is released so a later resume is not blocked.
func TestRestartLocalAgent_EditIsResumeOnly(t *testing.T) {
	t.Setenv("MUXCODE_EDIT_CLI", "claude")
	t.Setenv(autoResumeDisableEnv, "")
	for _, c := range noResumeIDPanes {
		t.Run(c.name, func(t *testing.T) {
			session := "restart-resume-only"
			injectionTestSession(t, session)
			log := stubRestartPane(t, c.pane, c.captureErr)

			if err := RestartLocalAgent(session, "edit"); !errors.Is(err, ErrResumeUnavailable) {
				t.Fatalf("err = %v, want ErrResumeUnavailable", err)
			}
			for _, call := range *log {
				if call[0] != "capture" {
					t.Errorf("resume-only edit typed into its pane: %v", call)
				}
			}
			for event, want := range map[string]int{c.event: 1, "agent-resume-unavailable": 1, "agent-relaunch": 0} {
				if n := countLifecycleEvents(t, session, event); n != want {
					t.Errorf("%s rows = %d, want %d", event, n, want)
				}
			}
			if IsReloading(session, "edit") {
				t.Error("restart left its reload marker behind")
			}
		})
	}
}

// The opt-out restores fresh restarts for every role: no scrape, the banner
// ignored, and edit relaunched rather than left down.
func TestRestartLocalAgent_AutoResumeDisabled(t *testing.T) {
	t.Setenv("MUXCODE_EDIT_CLI", "claude")
	t.Setenv(autoResumeDisableEnv, "1")
	session := "restart-resume-disabled"
	injectionTestSession(t, session)
	log := stubRestartPane(t, restartExitPane, nil)

	if err := RestartLocalAgent(session, "edit"); err != nil {
		t.Fatalf("RestartLocalAgent: %v", err)
	}
	if got := launchTyped(*log); got != "muxcode agent launch edit --reason restart" {
		t.Errorf("relaunch = %q, want a fresh launch", got)
	}
	if slices.IndexFunc(*log, func(c []string) bool { return c[0] == "capture" }) >= 0 {
		t.Errorf("opt-out still scraped the pane: %v", *log)
	}
	for event, want := range map[string]int{"resume-disabled": 1, "agent-resume": 0, "resume-scrape-hit": 0} {
		if n := countLifecycleEvents(t, session, event); n != want {
			t.Errorf("%s rows = %d, want %d", event, n, want)
		}
	}
}

func TestResumeOnlyRole(t *testing.T) {
	t.Setenv("BUS_SESSION", "")
	cases := []struct {
		role, cli, disable string
		want               bool
	}{
		{"edit", "claude", "", true},
		{"edit", "claude", "1", false},
		{"edit", "opencode", "", false},
		{"plan", "claude", "", false},
	}
	for _, c := range cases {
		t.Setenv(RoleCLIEnvVar(c.role), c.cli)
		t.Setenv(autoResumeDisableEnv, c.disable)
		if got := ResumeOnlyRole(c.role); got != c.want {
			t.Errorf("ResumeOnlyRole(%s) cli=%s disable=%q = %v, want %v", c.role, c.cli, c.disable, got, c.want)
		}
	}
}

func TestResumeFirstSighting(t *testing.T) {
	cases := []struct {
		firstSighting, disable string
		want                   bool
	}{
		{"", "", true},
		{"1", "", true},
		{"0", "", false},
		{"", "1", false},
	}
	for _, c := range cases {
		t.Setenv("MUXCODE_RESUME_FIRST_SIGHTING", c.firstSighting)
		t.Setenv(autoResumeDisableEnv, c.disable)
		if got := ResumeFirstSighting(); got != c.want {
			t.Errorf("ResumeFirstSighting first=%q disable=%q = %v, want %v", c.firstSighting, c.disable, got, c.want)
		}
	}
}

// PaneResumeID answers the first-sighting question with restartResumeTarget's
// rules: a stale banner — a relaunch already typed under it — is no proof the
// current agent exited, so it yields nothing.
func TestPaneResumeID(t *testing.T) {
	stale := noResumeIDPanes[2].pane
	for _, c := range []struct {
		name, pane string
		wantOK     bool
	}{{"live banner", restartExitPane, true}, {"stale banner", stale, false}, {"no banner", "❯ \n$ ", false}} {
		stubRestartPane(t, c.pane, nil)
		id, ok := PaneResumeID("s", "edit")
		if ok != c.wantOK || (ok && id != restartResumeID) {
			t.Errorf("%s: PaneResumeID = (%q, %v), want ok=%v", c.name, id, ok, c.wantOK)
		}
	}
}

// Negative control for the stale rule: a resumed session that ran and died
// again draws a NEW banner below its relaunch line, and must resume again.
func TestRestartResumeTarget_ResumedSessionDiedAgain(t *testing.T) {
	pane := "user@host muxcode $ muxcode agent launch edit --resume " + restartResumeID + "\n" + restartExitPane
	if id, event := restartResumeTarget(pane); id != restartResumeID || event != "resume-scrape-hit" {
		t.Errorf("restartResumeTarget = (%q, %q), want (%q, resume-scrape-hit)", id, event, restartResumeID)
	}
}
