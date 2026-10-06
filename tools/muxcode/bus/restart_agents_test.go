package bus

import (
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// restartFixture stubs every tmux, process and relaunch seam RestartAgent and
// RestartTargets reach, and records what each road was asked to do.
type restartFixture struct {
	session    string
	alive      map[string]bool
	stops      []string
	modes      map[string]relaunchMode
	reloads    []string
	reloadArgs []string // "cli|model" per reload
	relaunchID func(role string) (string, error)
	// verdict is the terminal or active status the stand-in daemon posts on
	// the handed-off record (verified unless a test sets it).
	verdict string
	// handedOff records, per relaunch, whether a pending record was already
	// on disk when the launch was typed.
	handedOff map[string]bool
	// statusAtStop records, per stop, the record status on disk as it began.
	statusAtStop map[string]string
	// stopErr fails every stop; onStop runs after a successful one.
	stopErr error
	onStop  func()
}

// newRestartFixture puts each role in windows on its provider from clis
// (claude when unlisted) and starts every agent dead.
func newRestartFixture(t *testing.T, windows []string, clis map[string]string) *restartFixture {
	t.Helper()
	SetBusDirBase(t.TempDir())
	t.Cleanup(ResetBusDirBase)
	t.Setenv("BUS_SESSION", "")
	f := &restartFixture{
		session:      "restart-test",
		alive:        map[string]bool{},
		modes:        map[string]relaunchMode{},
		verdict:      RestartVerifyVerified,
		handedOff:    map[string]bool{},
		statusAtStop: map[string]string{},
		relaunchID: func(role string) (string, error) {
			return "8a744341-11bf-440f-b5d2-49248447a9c0", nil
		},
	}
	if err := os.MkdirAll(BusDir(f.session), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, role := range windows {
		cli := clis[role]
		if cli == "" {
			cli = "claude"
		}
		t.Setenv(RoleCLIEnvVar(role), cli)
	}

	w, a, s, r, rl, av := restartWindowNames, restartAgentAlive, restartStopAgent, restartRelaunch, restartReload, restartAwaitVerify
	g, vp, vw := restartGap, restartVerifyPoll, restartVerifyWait
	t.Cleanup(func() {
		restartWindowNames, restartAgentAlive, restartStopAgent, restartRelaunch, restartReload, restartAwaitVerify = w, a, s, r, rl, av
		restartGap, restartVerifyPoll, restartVerifyWait = g, vp, vw
	})

	restartWindowNames = func(string) ([]string, error) { return windows, nil }
	restartAgentAlive = func(_, role string) bool { return f.alive[role] }
	restartStopAgent = func(session, role string) error {
		f.stops = append(f.stops, role)
		v, _, _ := ReadRestartVerification(session, role)
		f.statusAtStop[role] = v.Status
		if f.stopErr != nil {
			return f.stopErr
		}
		f.alive[role] = false
		if f.onStop != nil {
			f.onStop()
		}
		return nil
	}
	restartRelaunch = func(session, role string, mode relaunchMode) (string, error) {
		f.modes[role] = mode
		f.handedOff[role] = RestartVerificationActive(session, role)
		id, err := f.relaunchID(role)
		if err == nil {
			f.alive[role] = true
		}
		return id, err
	}
	restartReload = func(_, role, cli, model string, _ bool) error {
		f.reloads = append(f.reloads, role)
		f.reloadArgs = append(f.reloadArgs, cli+"|"+model)
		f.alive[role] = true
		return nil
	}
	// The stand-in daemon posts f.verdict on the handed-off record; the real
	// awaitRestartVerification then reads it back.
	restartAwaitVerify = func(session, role string) error {
		v, ok, err := ReadRestartVerification(session, role)
		if err != nil || !ok || v.Status != RestartVerifyPending {
			t.Errorf("%s: no pending record handed off (ok=%v err=%v status=%q)", role, ok, err, v.Status)
		}
		v.Status = f.verdict
		v.Detail = "fixture verdict"
		if err := WriteRestartVerification(session, v); err != nil {
			t.Fatalf("WriteRestartVerification: %v", err)
		}
		return awaitRestartVerification(session, role)
	}
	restartGap, restartVerifyPoll, restartVerifyWait = 0, time.Millisecond, 20*time.Millisecond
	return f
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

var restartWindows = []string{"edit", "plan", "build", "test", "commit"}
var restartCLIs = map[string]string{"build": "opencode", "commit": "opencode"}

// After a mass exit every agent is dead: the restart selection keeps them and
// edit, while ReloadAll's selection — left untouched — still drops the dead
// and never takes edit (MUX-139 Decision 2).
func TestRestartTargets_SelectsDeadAgentsAndEdit(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)

	cases := map[string][]string{
		"claude":   {"edit", "plan", "test"},
		"opencode": {"build", "commit"},
		"all":      {"build", "commit", "edit", "plan", "test"},
		"":         {"build", "commit", "edit", "plan", "test"},
	}
	for filter, want := range cases {
		got, err := RestartTargets(f.session, filter)
		if err != nil {
			t.Fatalf("RestartTargets(%q): %v", filter, err)
		}
		if strings.Join(sorted(got), ",") != strings.Join(want, ",") {
			t.Errorf("RestartTargets(%q) = %v, want %v", filter, sorted(got), want)
		}
	}

	if got := reloadAllTargets(f.session, "claude", func(_, _ string) bool { return false }); len(got) != 0 {
		t.Errorf("reload --all selected dead agents: %v", got)
	}
	live := reloadAllTargets(f.session, "", func(_, _ string) bool { return true })
	for _, role := range live {
		if role == "edit" || role == "auto" {
			t.Errorf("reload --all selected orchestrator %s — the restart's edit override leaked", role)
		}
	}
	if len(live) == 0 {
		t.Fatal("reload --all selected nothing with every agent live — the check above is vacuous")
	}
}

// A role with no window is not a restart target, and an unreadable window
// list is an error rather than an empty selection.
func TestRestartTargets_WindowsRequired(t *testing.T) {
	f := newRestartFixture(t, []string{"plan"}, nil)
	got, err := RestartTargets(f.session, "")
	if err != nil || len(got) != 1 || got[0] != "plan" {
		t.Fatalf("RestartTargets = %v, %v; want [plan]", got, err)
	}

	restartWindowNames = func(string) ([]string, error) { return nil, errors.New("no server") }
	if _, err := RestartTargets(f.session, ""); err == nil {
		t.Error("an unreadable window list read as a selection")
	}
}

// Claude agents go through the resume road: a dead one is relaunched without
// a stop, a live one is exited first, and edit alone is resume-only.
func TestRestartAgent_ClaudeRoutesThroughResume(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)
	f.alive["test"] = true

	for _, role := range []string{"plan", "test", "edit"} {
		id, err := RestartAgent(f.session, role)
		if err != nil || id == "" {
			t.Fatalf("RestartAgent(%s) = %q, %v", role, id, err)
		}
	}
	if strings.Join(f.stops, ",") != "test" {
		t.Errorf("stops = %v, want only the live test agent", f.stops)
	}
	want := map[string]relaunchMode{"plan": relaunchResume, "test": relaunchResume, "edit": relaunchResumeOnly}
	for role, mode := range want {
		if f.modes[role] != mode {
			t.Errorf("%s relaunch mode = %v, want %v", role, f.modes[role], mode)
		}
	}
	if len(f.reloads) != 0 {
		t.Errorf("Claude agents took the fresh reload road: %v", f.reloads)
	}
	if IsReloading(f.session, "plan") {
		t.Error("reload marker left behind after the restart")
	}
}

// edit with no session to resume stays down: no fresh launch on any road.
func TestRestartAgent_EditWithoutSessionStaysDown(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)
	f.relaunchID = func(string) (string, error) { return "", ErrResumeUnavailable }

	_, err := RestartAgent(f.session, "edit")
	if !errors.Is(err, ErrResumeUnavailable) {
		t.Fatalf("err = %v, want ErrResumeUnavailable", err)
	}
	if len(f.reloads) != 0 || f.alive["edit"] {
		t.Errorf("edit was fresh-launched: reloads=%v alive=%v", f.reloads, f.alive["edit"])
	}
}

// The restart never switches what runs an agent: other providers reload on
// their own provider with no --cli/--model, no override file is written, and
// every result's provider and model are unchanged.
func TestRestartAgents_NeverChangesProviderOrModel(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)

	results, err := RestartAgents(f.session, "", nil)
	if err != nil {
		t.Fatalf("RestartAgents: %v", err)
	}
	if len(results) != len(restartWindows) {
		t.Fatalf("results = %d, want %d — every dead agent must come back", len(results), len(restartWindows))
	}
	for _, args := range f.reloadArgs {
		if args != "|" {
			t.Errorf("reload passed an override %q", args)
		}
	}
	if strings.Join(sorted(f.reloads), ",") != "build,commit" {
		t.Errorf("fresh reloads = %v, want the opencode agents", f.reloads)
	}
	for _, r := range results {
		if !r.Success || !r.Restarted || r.OldCLI != r.NewCLI || r.OldModel != r.NewModel {
			t.Errorf("%s: success=%v restarted=%v cli %s→%s model %s→%s", r.Role, r.Success, r.Restarted, r.OldCLI, r.NewCLI, r.OldModel, r.NewModel)
		}
		if _, err := os.Stat(RuntimeOverridePath(f.session, r.Role)); err == nil {
			t.Errorf("%s: a runtime override file was written", r.Role)
		}
	}
}

// An opencode filter leaves every Claude agent untouched.
func TestRestartAgents_FilterLeavesOtherProvidersAlone(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)

	if _, err := RestartAgents(f.session, "opencode", nil); err != nil {
		t.Fatalf("RestartAgents: %v", err)
	}
	if len(f.modes) != 0 || len(f.stops) != 0 {
		t.Errorf("opencode filter touched Claude agents: relaunched=%v stopped=%v", f.modes, f.stops)
	}
	if len(f.reloads) != 2 {
		t.Errorf("reloads = %v, want build and commit", f.reloads)
	}
}

// The definition check is handed to the daemon before the launch is typed,
// and only a "verified" verdict passes. A record the daemon still owns at the
// wait deadline stays on disk for it, and the error says which state it is in
// — a failed stop never reads as "stopped".
func TestRestartAgent_HandsVerificationToDaemon(t *testing.T) {
	cases := []struct {
		verdict    string
		wantErr    error // nil: success
		recordKept bool
	}{
		{RestartVerifyVerified, nil, false},
		{RestartVerifyStopped, ErrRestartedUnrestricted, false},
		{RestartVerifyStopPending, ErrRestartStopPending, true},
		{RestartVerifyPending, ErrRestartVerifyPending, true},
		{RestartVerifyExited, errors.New("did not stay up"), false},
	}
	for _, c := range cases {
		t.Run(c.verdict, func(t *testing.T) {
			f := newRestartFixture(t, restartWindows, restartCLIs)
			f.verdict = c.verdict

			_, err := RestartAgent(f.session, "plan")
			switch {
			case c.wantErr == nil && err != nil:
				t.Fatalf("err = %v, want success", err)
			case c.wantErr != nil && err == nil:
				t.Fatalf("verdict %s passed as a verified restart", c.verdict)
			case c.wantErr != nil && !errors.Is(err, c.wantErr) && !strings.Contains(err.Error(), c.wantErr.Error()):
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if !f.handedOff["plan"] {
				t.Error("the launch was typed before the daemon owned its check")
			}
			if kept := RestartVerificationActive(f.session, "plan"); kept != c.recordKept {
				t.Errorf("record still owned by the daemon = %v, want %v", kept, c.recordKept)
			}
			if c.verdict == RestartVerifyStopPending && strings.Contains(err.Error(), "— stopped") {
				t.Errorf("a failed stop reads as stopped: %v", err)
			}
		})
	}
}

// A restart whose check cannot be handed off does not happen, and a role
// whose previous check is still active is not restarted over it.
func TestRestartAgent_RefusedWithoutHandoff(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)
	f.alive["plan"] = true
	if err := os.WriteFile(restartVerifyDir(f.session), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RestartAgent(f.session, "plan"); err == nil || !strings.Contains(err.Error(), "not restarting") {
		t.Fatalf("err = %v, want the handoff failure", err)
	}
	if len(f.stops) != 0 {
		t.Error("live plan was stopped with no record handed to the daemon")
	}
	if _, typed := f.modes["plan"]; typed {
		t.Error("plan was relaunched with no record handed to the daemon")
	}

	g := newRestartFixture(t, restartWindows, restartCLIs)
	if err := WriteRestartVerification(g.session, RestartVerification{Role: "plan", Status: RestartVerifyStopPending}); err != nil {
		t.Fatal(err)
	}
	if _, err := RestartAgent(g.session, "plan"); err == nil || !strings.Contains(err.Error(), "still being verified") {
		t.Fatalf("err = %v, want refusal over an active check", err)
	}
	if _, typed := g.modes["plan"]; typed {
		t.Error("plan was relaunched over its previous, still-active check")
	}
	if _, err := RestartAgent(g.session, "test"); err != nil {
		t.Fatalf("negative control: test has no active check but was refused: %v", err)
	}
}

// A live agent is stopped only once the daemon holds a preparing record for
// it, and the check is armed as pending only after the stop — a restarter that
// dies between the two leaves the daemon a record, not an unsupervised dead
// agent.
func TestRestartAgent_RecordPrecedesStop(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)
	f.alive["plan"] = true

	if _, err := RestartAgent(f.session, "plan"); err != nil {
		t.Fatalf("RestartAgent: %v", err)
	}
	if len(f.stops) != 1 {
		t.Fatalf("stops = %v, want plan once — the fixture never exercised a live stop", f.stops)
	}
	if got := f.statusAtStop["plan"]; got != RestartVerifyPreparing {
		t.Errorf("record at stop = %q, want %q", got, RestartVerifyPreparing)
	}
	if !f.handedOff["plan"] {
		t.Error("the launch was typed before the check was armed")
	}
}

// A stop that fails leaves the agent running: the preparing record is
// withdrawn and nothing is typed. A stop that succeeds but whose check cannot
// then be armed leaves the agent down for the health sweep — never relaunched
// unverified.
func TestRestartAgent_StopOrArmFailureNeverRelaunches(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)
	f.alive["plan"] = true
	f.stopErr = errors.New("did not exit")

	if _, err := RestartAgent(f.session, "plan"); err == nil || !strings.Contains(err.Error(), "exiting live plan") {
		t.Fatalf("err = %v, want the stop failure", err)
	}
	if _, typed := f.modes["plan"]; typed {
		t.Error("plan was relaunched over a failed stop")
	}
	if _, ok, _ := ReadRestartVerification(f.session, "plan"); ok {
		t.Error("a failed stop left a record for an agent still running")
	}

	g := newRestartFixture(t, restartWindows, restartCLIs)
	g.alive["plan"] = true
	g.onStop = func() {
		if err := os.RemoveAll(restartVerifyDir(g.session)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(restartVerifyDir(g.session), []byte("not a dir"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RestartAgent(g.session, "plan"); err == nil || !strings.Contains(err.Error(), "not relaunching") {
		t.Fatalf("err = %v, want the arm failure", err)
	}
	if g.statusAtStop["plan"] != RestartVerifyPreparing {
		t.Fatalf("record at stop = %q — the fixture did not reach the arm step", g.statusAtStop["plan"])
	}
	if _, typed := g.modes["plan"]; typed {
		t.Error("plan was relaunched with its check unarmed")
	}
}

// One daemon step over a pending record. Only a ready composer held clean for
// the settle period verifies; startup text, a failed capture and a not-yet-
// settled pane only wait; a banner at any point, or an unproven agent at the
// timeout, must be stopped; a dead agent at the timeout closes as exited.
func TestAdvanceRestartVerification(t *testing.T) {
	const t0 = int64(1000)
	ready := "Resume this session with:\nclaude --resume x\n$ muxcode agent launch plan\n❯ "
	startup := "Resume this session with:\nclaude --resume x\n$ muxcode agent launch plan\n✻ Welcome to Claude Code"
	late := ready + "\n" + definitionlessBanner
	capErr := errors.New("no pane")
	pending := RestartVerification{Role: "plan", Status: RestartVerifyPending, RelaunchedAt: t0}
	settling := pending
	settling.ReadyAt = t0 + 2
	preparing := RestartVerification{Role: "plan", Status: RestartVerifyPreparing, UpdatedAt: t0}

	cases := []struct {
		name       string
		v          RestartVerification
		now        int64
		alive      bool
		content    string
		err        error
		wantStatus string
		wantStop   bool
		wantReady  int64
	}{
		{"delayed startup waits", pending, t0 + 3, true, startup, nil, RestartVerifyPending, false, 0},
		{"first ready sighting starts the settle", pending, t0 + 2, true, ready, nil, RestartVerifyPending, false, t0 + 2},
		{"ready inside the settle still waits", settling, t0 + 2 + RestartVerifySettleSecs - 1, true, ready, nil, RestartVerifyPending, false, t0 + 2},
		{"ready through the settle verifies", settling, t0 + 2 + RestartVerifySettleSecs, true, ready, nil, RestartVerifyVerified, false, t0 + 2},
		{"late warning after ready stops", settling, t0 + 4, true, late, nil, RestartVerifyPending, true, t0 + 2},
		{"capture error waits and resets the settle", settling, t0 + 10, true, "", capErr, RestartVerifyPending, false, 0},
		{"capture error at the timeout stops", pending, t0 + RestartVerifyTimeoutSecs, true, "", capErr, RestartVerifyPending, true, 0},
		{"never ready by the timeout stops", pending, t0 + RestartVerifyTimeoutSecs, true, startup, nil, RestartVerifyPending, true, 0},
		{"dead before the timeout waits", pending, t0 + 5, false, "", nil, RestartVerifyPending, false, 0},
		{"dead at the timeout exits", pending, t0 + RestartVerifyTimeoutSecs, false, "", nil, RestartVerifyExited, false, 0},
		{"stop-pending keeps stopping", RestartVerification{Role: "plan", Status: RestartVerifyStopPending, RelaunchedAt: t0}, t0 + 1, true, ready, nil, RestartVerifyStopPending, true, 0},
		{"preparing never verifies the pre-stop pane", preparing, t0 + 30, true, ready, nil, RestartVerifyPreparing, false, 0},
		{"preparing waits for its restarter", preparing, t0 + restartPrepareTimeoutSecs - 1, false, "", nil, RestartVerifyPreparing, false, 0},
		{"preparing never armed is abandoned", preparing, t0 + restartPrepareTimeoutSecs, false, "", nil, RestartVerifyAbandoned, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, stop := AdvanceRestartVerification(c.v, c.now, c.alive, c.content, c.err)
			if got.Status != c.wantStatus || stop != c.wantStop || got.ReadyAt != c.wantReady {
				t.Errorf("got status=%s stop=%v ready=%d, want %s %v %d", got.Status, stop, got.ReadyAt, c.wantStatus, c.wantStop, c.wantReady)
			}
		})
	}

	// A capture error followed by the warning: the error alone never verified,
	// and the next readable pane is refused.
	v, stop := AdvanceRestartVerification(settling, t0+3, true, "", capErr)
	if stop || v.Status != RestartVerifyPending {
		t.Fatalf("capture error: status=%s stop=%v", v.Status, stop)
	}
	if _, stop := AdvanceRestartVerification(v, t0+6, true, late, nil); !stop {
		t.Error("the warning after a capture error was not refused")
	}
}

func TestRestartProviderCounts(t *testing.T) {
	f := newRestartFixture(t, restartWindows, restartCLIs)
	f.alive["plan"] = true

	counts, err := RestartProviderCounts(f.session)
	if err != nil {
		t.Fatalf("RestartProviderCounts: %v", err)
	}
	got := map[string]RestartProviderCount{}
	for _, c := range counts {
		got[c.CLI] = c
	}
	if c := got["claude"]; c.Total != 3 || c.Down != 2 {
		t.Errorf("claude = %+v, want 3 total, 2 down", c)
	}
	if c := got["opencode"]; c.Total != 2 || c.Down != 2 {
		t.Errorf("opencode = %+v, want 2 total, 2 down", c)
	}
}
