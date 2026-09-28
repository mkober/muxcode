package bus

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// resumeFixture stubs tmux for ResumeAgent on top of stubRestartPane: the
// session lists windows, liveness is alive, and each keystroke records whether
// the reload marker was held when it was sent.
type resumeFixture struct {
	session      string
	log          *[][]string
	markerAtKeys []bool
	stopped      bool
}

func newResumeFixture(t *testing.T, pane string, windows string, alive bool) *resumeFixture {
	t.Helper()
	f := &resumeFixture{session: "resume-manual"}
	injectionTestSession(t, f.session)
	t.Setenv(RoleCLIEnvVar("edit"), "claude")
	f.log = stubRestartPane(t, pane, nil)

	capture, run := tmuxOutputRunner, tmuxRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-windows" {
			return windows, nil
		}
		return capture(args...)
	}
	tmuxRunner = func(args ...string) error {
		f.markerAtKeys = append(f.markerAtKeys, IsReloading(f.session, "edit"))
		return run(args...)
	}

	origAlive, origStop := resumeAgentAlive, resumeStopAgent
	t.Cleanup(func() { resumeAgentAlive, resumeStopAgent = origAlive, origStop })
	resumeAgentAlive = func(string, string) bool { return alive && !f.stopped }
	resumeStopAgent = func(string, string) error {
		*f.log = append(*f.log, []string{"stop"})
		f.stopped = true
		return nil
	}
	return f
}

func (f *resumeFixture) typedNothing(t *testing.T) {
	t.Helper()
	for _, call := range *f.log {
		if call[0] != "capture" {
			t.Fatalf("a refusal must type nothing, got %v", *f.log)
		}
	}
}

func manualRows(t *testing.T, session, event string) []LifecycleEntry {
	t.Helper()
	entries, _ := FilterLifecycleLog(session, LifecycleFilterOpts{Source: "manual", Event: event})
	return entries
}

// MUX-126 Phase 6: a dead pane with a banner resumes with the id, recorded
// as a manual resume by its actor, with the daemon held off throughout.
func TestResumeAgent_DeadPaneResumes(t *testing.T) {
	f := newResumeFixture(t, restartExitPane, "edit\nbuild", false)

	if err := ResumeAgent(f.session, "edit", false, "user"); err != nil {
		t.Fatalf("ResumeAgent: %v", err)
	}
	if want := "muxcode agent launch edit --resume " + restartResumeID; launchTyped(*f.log) != want {
		t.Errorf("relaunch = %q, want %q", launchTyped(*f.log), want)
	}
	for _, event := range []string{"resume-scrape-hit", "agent-relaunch"} {
		rows := manualRows(t, f.session, event)
		if len(rows) != 1 || !strings.Contains(rows[0].Detail, "(by user)") {
			t.Errorf("%s manual rows = %+v, want one naming the actor", event, rows)
		}
	}
	if len(f.markerAtKeys) == 0 || slices.Contains(f.markerAtKeys, false) {
		t.Errorf("reload marker must be held on every keystroke, got %v", f.markerAtKeys)
	}
	if IsReloading(f.session, "edit") {
		t.Error("reload marker outlived the command")
	}
}

// Negative control: no banner relaunches fresh — the flagged plain launch.
func TestResumeAgent_NoBannerLaunchesFresh(t *testing.T) {
	f := newResumeFixture(t, "❯ \nuser@host muxcode $ ", "edit", false)

	if err := ResumeAgent(f.session, "edit", false, "user"); err != nil {
		t.Fatalf("ResumeAgent: %v", err)
	}
	if got := launchTyped(*f.log); got != "muxcode agent launch edit" {
		t.Errorf("relaunch = %q, want a fresh launch", got)
	}
	if n := len(manualRows(t, f.session, "resume-scrape-miss")); n != 1 {
		t.Errorf("resume-scrape-miss manual rows = %d, want 1", n)
	}
}

// --force exits a live agent BEFORE the scrape, so the banner it draws is read.
func TestResumeAgent_ForceExitsLiveAgentBeforeScrape(t *testing.T) {
	f := newResumeFixture(t, restartExitPane, "edit", true)

	if err := ResumeAgent(f.session, "edit", true, "edit"); err != nil {
		t.Fatalf("ResumeAgent: %v", err)
	}
	stop := slices.IndexFunc(*f.log, func(c []string) bool { return c[0] == "stop" })
	capture := slices.IndexFunc(*f.log, func(c []string) bool { return c[0] == "capture" })
	if stop < 0 || capture < 0 || stop > capture {
		t.Fatalf("stop must precede the scrape: stop at %d, capture at %d in %v", stop, capture, *f.log)
	}
	if want := "muxcode agent launch edit --resume " + restartResumeID; launchTyped(*f.log) != want {
		t.Errorf("relaunch = %q, want %q", launchTyped(*f.log), want)
	}
}

// Every refusal types nothing and leaves no marker behind.
func TestResumeAgent_Refusals(t *testing.T) {
	cases := []struct {
		name    string
		role    string
		windows string
		alive   bool
		cli     string
		want    string
	}{
		{"live agent without --force", "edit", "edit", true, "claude", "is running"},
		{"non-Claude provider", "edit", "edit", false, "opencode", "muxcode reload edit"},
		{"unknown role", "nosuchrole", "edit", false, "claude", "unknown role"},
		{"no window", "edit", "build", false, "claude", "has no window"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newResumeFixture(t, restartExitPane, c.windows, c.alive)
			t.Setenv(RoleCLIEnvVar("edit"), c.cli)

			err := ResumeAgent(f.session, c.role, false, "user")
			if !errors.Is(err, ErrResumeRefused) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a refusal containing %q", err, c.want)
			}
			f.typedNothing(t)
			if f.stopped {
				t.Error("a refusal must not stop the agent")
			}
			if IsReloading(f.session, "edit") {
				t.Error("a refusal left a reload marker")
			}
		})
	}
}

// A hosted role resolves to its host's pane, so resuming it would type the
// hosted role's launch into the host. Refuse, naming the host's resume, even
// when the hosted role's own provider would pass the Claude check, and leave
// the host's marker untouched.
func TestResumeAgent_RefusesHostedRoles(t *testing.T) {
	cases := []struct{ role, host, hostCLI string }{
		{"docs", "plan", "claude"},
		{"pr-read", "commit", "codex"},
	}
	for _, c := range cases {
		t.Run(c.role, func(t *testing.T) {
			f := newResumeFixture(t, restartExitPane, c.host+"\nedit", true)
			t.Setenv(RoleCLIEnvVar(c.role), "claude")
			t.Setenv(RoleCLIEnvVar(c.host), c.hostCLI)
			if err := writeReloadMarker(f.session, c.host); err != nil {
				t.Fatal(err)
			}

			err := ResumeAgent(f.session, c.role, true, "user")
			if !errors.Is(err, ErrResumeRefused) || !strings.Contains(err.Error(), "muxcode resume "+c.host) {
				t.Fatalf("err = %v, want a refusal naming `muxcode resume %s`", err, c.host)
			}
			f.typedNothing(t)
			if f.stopped {
				t.Errorf("refusing %s must not stop %s", c.role, c.host)
			}
			if IsReloading(f.session, c.role) {
				t.Errorf("refusal left a %s marker", c.role)
			}
			if !IsReloading(f.session, c.host) {
				t.Errorf("refusal removed %s's own marker", c.host)
			}
		})
	}
}

// Negative control: the host itself is not refused as hosted.
func TestResumeAgent_HostRoleIsNotRefusedAsHosted(t *testing.T) {
	f := newResumeFixture(t, restartExitPane, "plan", false)
	t.Setenv(RoleCLIEnvVar("plan"), "claude")

	if err := ResumeAgent(f.session, "plan", false, "user"); err != nil {
		t.Fatalf("ResumeAgent(plan): %v", err)
	}
	if want := "muxcode agent launch plan --resume " + restartResumeID; launchTyped(*f.log) != want {
		t.Errorf("relaunch = %q, want %q", launchTyped(*f.log), want)
	}
}

// A reload already in progress owns the marker: refuse, and do not remove it.
func TestResumeAgent_RefusesDuringReloadAndKeepsItsMarker(t *testing.T) {
	f := newResumeFixture(t, restartExitPane, "edit", false)
	if err := writeReloadMarker(f.session, "edit"); err != nil {
		t.Fatal(err)
	}

	if err := ResumeAgent(f.session, "edit", false, "user"); !errors.Is(err, ErrResumeRefused) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	f.typedNothing(t)
	if !IsReloading(f.session, "edit") {
		t.Error("resume removed a marker it does not own")
	}
}

// A failed relaunch still releases the daemon.
func TestResumeAgent_FailedRelaunchClearsMarker(t *testing.T) {
	f := newResumeFixture(t, restartExitPane, "edit", false)
	run := tmuxRunner
	tmuxRunner = func(args ...string) error {
		if args[len(args)-1] == "Enter" {
			return errors.New("pane gone")
		}
		return run(args...)
	}

	if err := ResumeAgent(f.session, "edit", false, "user"); err == nil || errors.Is(err, ErrResumeRefused) {
		t.Fatalf("err = %v, want a relaunch failure", err)
	}
	if IsReloading(f.session, "edit") {
		t.Error("reload marker outlived a failed relaunch")
	}
}
