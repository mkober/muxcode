package bus

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const restartResumeID = "8a744341-11bf-440f-b5d2-49248447a9c0"

const restartExitPane = "❯ \n\nResume this session with:\nclaude --resume " + restartResumeID + "\nuser@host muxcode $ "

// relaunchStub is a pane for scrapeAndRelaunch: the resume scrape reads pane
// until C-c, then a pane whose banner the relaunch has typed over — so a
// scrape moved after the interrupt reads no id and the resume assertions fail
// rather than passing through the fresh fallback. The foreground process names
// the agent for liveReads reads after C-c (negative: it never exits), then the
// shell; once the launch line is typed the pane is afterLaunch and the
// foreground process is launchedFG (the agent, unless a test makes the launch
// fail back to its shell). log holds the
// ordered calls: "capture" (the scrape), "foreground", "ready" (a readiness
// capture) or the send-keys argv.
type relaunchStub struct {
	log         [][]string
	interrupted bool
	launched    bool
	liveReads   int
	afterLaunch string
	launchedFG  string
}

// stubRelaunch installs a relaunchStub whose agent has already exited and whose
// relaunch comes up, with waits short enough for a test.
func stubRelaunch(t *testing.T, pane string, captureErr error) *relaunchStub {
	t.Helper()
	s := &relaunchStub{afterLaunch: "user@host muxcode $ muxcode agent launch edit --reason restart\n❯ ", launchedFG: "claude"}
	origOut, origRun := tmuxOutputRunner, tmuxRunner
	origExit, origReady, origPoll := restartExitWait, restartReadyWait, restartPollInterval
	t.Cleanup(func() {
		tmuxOutputRunner, tmuxRunner = origOut, origRun
		restartExitWait, restartReadyWait, restartPollInterval = origExit, origReady, origPoll
	})
	restartExitWait, restartReadyWait, restartPollInterval = 200*time.Millisecond, 200*time.Millisecond, time.Millisecond

	tmuxOutputRunner = func(args ...string) (string, error) {
		switch {
		case len(args) > 0 && args[0] == "display-message":
			s.log = append(s.log, []string{"foreground"})
			if s.launched {
				return s.launchedFG, nil
			}
			if s.liveReads != 0 {
				if s.liveReads > 0 {
					s.liveReads--
				}
				return "claude", nil
			}
			return "-bash", nil
		case len(args) == 0 || args[0] != "capture-pane":
			return "", nil
		case s.launched:
			s.log = append(s.log, []string{"ready"})
			return s.afterLaunch, nil
		}
		s.log = append(s.log, []string{"capture"})
		if captureErr != nil {
			return "", captureErr
		}
		if s.interrupted {
			return "user@host muxcode $ muxcode agent launch edit", nil
		}
		return pane, nil
	}
	tmuxRunner = func(args ...string) error {
		s.log = append(s.log, args)
		if slices.Contains(args, "C-c") {
			s.interrupted = true
		}
		if n := len(args); n > 0 && args[n-1] == "Enter" {
			s.launched = true
		}
		return nil
	}
	return s
}

// stubRestartPane is stubRelaunch's call log alone.
func stubRestartPane(t *testing.T, pane string, captureErr error) *[][]string {
	t.Helper()
	return &stubRelaunch(t, pane, captureErr).log
}

// MUX-008 Phase 2 (the Phase 1 characterization, inverted): the launch line is
// typed only once the pane's foreground process is its shell again. A slow
// exit is waited out; an agent that never exits gets no launch line and an
// unverified error. Negative control: a pane already at its shell relaunches
// at once, with no failure row.
func TestRestartLocalAgent_WaitsForTheOldProcessToExit(t *testing.T) {
	for _, c := range []struct {
		name      string
		liveReads int
		wantErr   bool
	}{
		{"control: already exited", 0, false},
		{"slow exit", 3, false},
		{"never exits", -1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			session := "restart-exit-wait"
			injectionTestSession(t, session)
			t.Setenv(RoleCLIEnvVar("build"), "claude")
			s := stubRelaunch(t, "user@host muxcode $ ", nil)
			s.liveReads = c.liveReads

			err := RestartLocalAgent(session, "build", nil)
			rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "agent-relaunch-unverified"})
			if !c.wantErr {
				if err != nil || launchTyped(s.log) == "" || len(rows) != 0 {
					t.Fatalf("err = %v, launch %q, unverified rows %+v — want nil, typed, none", err, launchTyped(s.log), rows)
				}
				launch := slices.IndexFunc(s.log, func(call []string) bool { return slices.Contains(call, "Enter") })
				if n := strings.Count(fmt.Sprint(s.log[:launch]), "foreground"); c.liveReads > 0 && n != c.liveReads+1 {
					t.Errorf("foreground reads before the launch line = %d, want %d", n, c.liveReads+1)
				}
				return
			}
			if !errors.Is(err, ErrRestartUnverified) {
				t.Fatalf("err = %v, want ErrRestartUnverified", err)
			}
			if launchTyped(s.log) != "" {
				t.Errorf("a launch line was typed into a process that never exited: %q", launchTyped(s.log))
			}
			if len(rows) != 1 || !strings.Contains(rows[0].Detail, "old process still in the pane") || !strings.Contains(rows[0].Detail, "foreground claude") {
				t.Errorf("unverified rows = %+v, want one naming the process left running", rows)
			}
		})
	}
}

// MUX-008 Phase 2: after the launch line the relaunch waits for a started
// agent below it. A pane holding only the typed line — swallowed, never run —
// is an unverified error naming the missing start; the started composer
// (stubRelaunch's default) is the control.
func TestRestartLocalAgent_SwallowedLaunchLineIsUnverified(t *testing.T) {
	session := "restart-swallowed-line"
	injectionTestSession(t, session)
	t.Setenv(RoleCLIEnvVar("build"), "claude")
	s := stubRelaunch(t, "user@host muxcode $ ", nil)
	s.afterLaunch = "user@host muxcode $ muxcode agent launch build --reason restart"

	err := RestartLocalAgent(session, "build", nil)
	if !errors.Is(err, ErrRestartUnverified) || launchTyped(s.log) == "" {
		t.Fatalf("err = %v, launch %q — want ErrRestartUnverified after typing the line", err, launchTyped(s.log))
	}
	rows, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "agent-relaunch-unverified"})
	if len(rows) != 1 || !strings.Contains(rows[0].Detail, "no started agent") {
		t.Errorf("unverified rows = %+v, want one naming the missing start", rows)
	}
	if IsReloading(session, "build") {
		t.Error("an unverified restart left its reload marker behind")
	}
}

// PR review (MUX-008 Phase 2): a launch that fails back to a shell whose prompt
// is drawn with ❯ leaves the same pane text as a started Claude. The
// foreground process tells them apart: the shell's is unverified, the agent's
// (the control) is started.
func TestRestartLocalAgent_ShellPromptWithComposerGlyphIsUnverified(t *testing.T) {
	for _, c := range []struct {
		fg      string
		wantErr bool
	}{
		{"zsh", true},
		{"claude", false},
	} {
		t.Run(c.fg, func(t *testing.T) {
			session := "restart-glyph-prompt"
			injectionTestSession(t, session)
			t.Setenv(RoleCLIEnvVar("build"), "claude")
			s := stubRelaunch(t, "user@host muxcode $ ", nil)
			s.afterLaunch = "❯ muxcode agent launch build --reason restart\nzsh: command not found: muxcode\n❯ "
			s.launchedFG = c.fg

			err := RestartLocalAgent(session, "build", nil)
			if got := errors.Is(err, ErrRestartUnverified); got != c.wantErr {
				t.Fatalf("foreground %s: err = %v, want unverified=%v", c.fg, err, c.wantErr)
			}
		})
	}
}

// paneShowsStartedAgent takes both halves of the evidence: a non-shell,
// readable foreground process and the provider's pane evidence below the last
// launch line. The discriminating cases are the scoping — Codex text and a TUI
// frame in scrollback above a launch line that never ran are not a start — and
// the same started-looking pane under a shell, or an unreadable, foreground.
func TestPaneShowsStartedAgent(t *testing.T) {
	const launch = "user@host muxcode $ muxcode agent launch build --reason restart"
	for _, c := range []struct {
		name, cli, fg, pane string
		want                bool
	}{
		{"claude composer below the line", "claude", "claude", launch + "\n❯ ", true},
		{"claude: same pane, shell foreground", "claude", "zsh", launch + "\n❯ ", false},
		{"claude: same pane, foreground unreadable", "claude", "", launch + "\n❯ ", false},
		{"claude: only the line", "claude", "claude", launch, false},
		{"claude: old composer above the line", "claude", "claude", "❯ \n" + launch, false},
		{"opencode alt-screen TUI", "opencode", "opencode", "╭──────╮\n│ opencode │", true},
		{"opencode: same TUI, shell foreground", "opencode", "bash", "╭──────╮\n│ opencode │", false},
		{"opencode: only the line", "opencode", "opencode", launch, false},
		{"codex TUI below the line", "codex", "codex", launch + "\n╭─ OpenAI Codex ─╮", true},
		{"codex: scrollback above the line", "codex", "codex", "╭─ OpenAI Codex ─╮\ncodex exited\n" + launch, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(RoleCLIEnvVar("build"), c.cli)
			origOut := tmuxOutputRunner
			t.Cleanup(func() { tmuxOutputRunner = origOut })
			tmuxOutputRunner = func(args ...string) (string, error) {
				if len(args) > 0 && args[0] == "display-message" {
					return c.fg, nil
				}
				return c.pane, nil
			}
			if got := paneShowsStartedAgent("restart-ready", "build", "s:build.1"); got != c.want {
				t.Errorf("paneShowsStartedAgent = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsShellCommand(t *testing.T) {
	t.Setenv("SHELL", "/opt/homebrew/bin/nu")
	for name, want := range map[string]bool{
		"bash": true, "-zsh": true, "/bin/sh": true, "nu": true,
		"claude": false, "node": false, "codex": false, "muxcode": false, "": false,
	} {
		if got := isShellCommand(name); got != want {
			t.Errorf("isShellCommand(%q) = %v, want %v", name, got, want)
		}
	}
}

// MUX-008 Phase 1: a launch line that reached the shell but never ran — its
// Enter eaten by the dying process — leaves the pane ending in the command,
// not a prompt, and the liveness heuristic reads that as a live agent. The
// bare prompt left when the whole line was swallowed is the control: it reads
// dead. OpenCode's IsAlive is the provider pinned because it captures through
// TmuxOutput; Claude's and Codex's run tmux directly, and the same fall-through
// to "assume alive" is read from their code.
func TestIsAlive_UnsubmittedLaunchLineReadsAlive_Characterization(t *testing.T) {
	origOut := tmuxOutputRunner
	t.Cleanup(func() { tmuxOutputRunner = origOut })
	for _, c := range []struct {
		name, pane string
		alive      bool
	}{
		{"launch line unsubmitted", "user@host muxcode $ muxcode agent launch build --reason restart", true},
		{"control: bare prompt", "user@host muxcode $ ", false},
	} {
		tmuxOutputRunner = func(args ...string) (string, error) { return c.pane, nil }
		if got := (&OpenCodeProvider{}).IsAlive("restart-swallowed", "build"); got != c.alive {
			t.Errorf("%s: IsAlive = %v, want %v", c.name, got, c.alive)
		}
	}
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

	if err := RestartLocalAgent(session, "edit", nil); err != nil {
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

	if err := RestartLocalAgent(session, "edit", nil); !errors.Is(err, ErrReloadMarkerHeld) {
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

			if err := RestartLocalAgent(session, "plan", nil); err != nil {
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

			if err := RestartLocalAgent(session, "edit", nil); !errors.Is(err, ErrResumeUnavailable) {
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

	if err := RestartLocalAgent(session, "edit", nil); err != nil {
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
