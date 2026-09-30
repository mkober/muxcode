package bus

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// autoStartupFor launches auto for reason on a fresh bus and returns the one
// startup message PreLaunchSetup seeded.
func autoStartupFor(t *testing.T, reason LaunchReason) Message {
	t.Helper()
	dir := t.TempDir()
	session := "test-launch-reason"
	t.Setenv("BUS_DIR_BASE", dir)
	Init(session, dir)

	PreLaunchSetup("auto", session, "claude", reason)

	msgs, err := Peek(session, "auto")
	if err != nil {
		t.Fatalf("Peek auto inbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("reason %q: want 1 startup message, got %d: %+v", reason, len(msgs), msgs)
	}
	return msgs[0]
}

func seedsAutoTask(m Message) bool {
	return strings.Contains(m.Payload, "Jira")
}

// MUX-141 Phase 2: MUXCODE_AUTO_STARTUP_TASK=0 withholds the task even from a
// user-initiated launch, which then gets the same context-restoration startup
// a restart does. Only the literal 0 opts out — unset and any other value
// still seed, so the switch cannot disable the agent by accident.
func TestPreLaunchSetup_AutoStartupTaskOptOut(t *testing.T) {
	cases := []struct {
		name, value string
		task        bool
	}{
		{"opted out", "0", false},
		{"unset", "", true},
		{"explicitly on", "1", true},
		{"not the opt-out value", "false", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MUXCODE_AUTO_STARTUP_TASK", tc.value)
			m := autoStartupFor(t, LaunchReasonUser)
			if got := seedsAutoTask(m); got != tc.task {
				t.Fatalf("MUXCODE_AUTO_STARTUP_TASK=%q: task seeded = %v, want %v (payload %q)", tc.value, got, tc.task, m.Payload)
			}
			if !tc.task && (m.Payload != startupRestorePayload || m.From != "auto" || !HasActionableMessages("test-launch-reason", "auto")) {
				t.Errorf("opted out: want the actionable self-addressed restore startup, got from %q payload %q", m.From, m.Payload)
			}
		})
	}
}

// MUX-141 Phase 1: only a user-initiated launch seeds the auto agent's task.
// Every other reason — the omitted one and an unrecognized one included —
// seeds the ordinary, still-actionable context-restoration startup.
func TestPreLaunchSetup_AutoTaskOnlyOnUserReason(t *testing.T) {
	cases := []struct {
		name   string
		reason LaunchReason
		task   bool
	}{
		{"user", LaunchReasonUser, true},
		{"restart", LaunchReasonRestart, false},
		{"reload", LaunchReasonReload, false},
		{"mode-cycle", LaunchReasonModeCycle, false},
		{"resume", LaunchReasonResume, false},
		{"spawn", LaunchReasonSpawn, false},
		{"omitted", "", false},
		{"unrecognized", "User", false},
	}
	t.Setenv("MUXCODE_AUTO_STARTUP_TASK", "")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := autoStartupFor(t, tc.reason)
			if got := seedsAutoTask(m); got != tc.task {
				t.Fatalf("reason %q: task seeded = %v, want %v (payload %q)", tc.reason, got, tc.task, m.Payload)
			}
			if m.Type != "request" || m.Action != "startup" {
				t.Errorf("reason %q: startup is %s:%s, want request:startup so the daemon can re-wake", tc.reason, m.Type, m.Action)
			}
			if !tc.task && !strings.Contains(m.Payload, "Session started") {
				t.Errorf("reason %q: want the context-restoration startup, got %q", tc.reason, m.Payload)
			}
		})
	}
}

// typedLaunchArgs returns the arguments a typed launch command hands to
// `muxcode agent launch`, whatever shell prefix the road puts before it.
func typedLaunchArgs(t *testing.T, command string) []string {
	t.Helper()
	_, args, ok := strings.Cut(command, " agent launch ")
	if !ok {
		t.Fatalf("command %q is not an agent launch", command)
	}
	return strings.Fields(args)
}

// stubLaunchTmux records every tmux command a launch road issues and answers
// list-panes with a tagged two-pane census, so the road runs through to its
// launch without a live session. onRun, when set, sees each command as it runs.
func stubLaunchTmux(t *testing.T, onRun func(args []string)) *[][]string {
	t.Helper()
	var calls [][]string
	origRun, origQuiet, origOut := tmuxRunner, tmuxQuietRunner, tmuxOutputRunner
	t.Cleanup(func() { tmuxRunner, tmuxQuietRunner, tmuxOutputRunner = origRun, origQuiet, origOut })

	record := func(args ...string) error {
		calls = append(calls, args)
		if onRun != nil {
			onRun(args)
		}
		return nil
	}
	tmuxRunner, tmuxQuietRunner = record, record
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-panes" {
			return "%0:left:1\n%1:agent:1", nil
		}
		return "", nil
	}
	return &calls
}

// typedLaunches returns every agent launch the recorded tmux calls typed into
// a pane.
func typedLaunches(calls [][]string) []string {
	var typed []string
	for _, call := range calls {
		if len(call) == 0 || call[0] != "send-keys" {
			continue
		}
		for _, arg := range call[1:] {
			if strings.Contains(arg, " agent launch ") {
				typed = append(typed, arg)
			}
		}
	}
	return typed
}

func sessionRoadLaunches(t *testing.T, session string) []string {
	t.Setenv("MUXCODE_CONTROL_PANE_DISABLE", "1")
	calls := stubLaunchTmux(t, nil)
	if err := createWindowContent(DefaultLauncherConfig(), session, "auto", t.TempDir()); err != nil {
		t.Fatalf("createWindowContent: %v", err)
	}
	return typedLaunches(*calls)
}

func modeRoadLaunches(t *testing.T, session string) []string {
	calls := stubLaunchTmux(t, nil)
	if err := modeCreateAgent(session, &DefaultModeCycleState().Agents[1]); err != nil {
		t.Fatalf("modeCreateAgent: %v", err)
	}
	return typedLaunches(*calls)
}

// reloadRoadLaunches runs a whole ReloadAgent on the local provider, whose
// liveness is the harness marker alone: absent while the stop polls, written
// when the relaunch is typed, so the reload completes with no pane to scrape.
func reloadRoadLaunches(t *testing.T, session string) []string {
	t.Setenv("MUXCODE_AUTO_CLI", "local")
	calls := stubLaunchTmux(t, func(args []string) {
		if len(typedLaunches([][]string{args})) == 0 {
			return
		}
		pid := []byte(strconv.Itoa(os.Getpid()))
		if err := os.WriteFile(HarnessMarkerPath(session, "auto"), pid, 0o644); err != nil {
			t.Errorf("write harness marker: %v", err)
		}
	})
	if err := ReloadAgent(session, "auto", "", "", false); err != nil {
		t.Fatalf("ReloadAgent: %v", err)
	}
	return typedLaunches(*calls)
}

func spawnRoadLaunches(worktree string) func(*testing.T, string) []string {
	return func(*testing.T, string) []string {
		return []string{spawnLaunchCommand(worktree, "spawn-1", "/opt/bin/muxcode", "auto")}
	}
}

// Each road that types a launch into a pane names its own reason. The session,
// mode-cycle and reload roads are driven through their real callers and read at
// the tmux boundary, so a caller that types another reason fails here; the
// command — read back by the launcher's own parser — seeds the auto task only
// on the session-launch road. The restart and resume roads are pinned on their
// typed command in restart_resume_test.go and resume_test.go.
func TestLaunchRoads_CarryExplicitReason(t *testing.T) {
	roads := []struct {
		name     string
		launches func(t *testing.T, session string) []string
		reason   LaunchReason
	}{
		{"session launch", sessionRoadLaunches, LaunchReasonUser},
		{"reload", reloadRoadLaunches, LaunchReasonReload},
		{"mode cycle", modeRoadLaunches, LaunchReasonModeCycle},
		{"spawn", spawnRoadLaunches(""), LaunchReasonSpawn},
		{"spawn in worktree", spawnRoadLaunches("/tmp/wt"), LaunchReasonSpawn},
	}
	t.Setenv("MUXCODE_AUTO_STARTUP_TASK", "")
	for _, road := range roads {
		t.Run(road.name, func(t *testing.T) {
			session := "launch-road"
			injectionTestSession(t, session)

			typed := road.launches(t, session)
			if len(typed) != 1 {
				t.Fatalf("road typed %d agent launches, want 1: %q", len(typed), typed)
			}
			command := typed[0]
			role, _, reason, err := ParseLaunchArgs(typedLaunchArgs(t, command))
			if err != nil {
				t.Fatalf("launcher rejects the road's own command %q: %v", command, err)
			}
			if role != "auto" || reason != road.reason {
				t.Fatalf("command %q parsed as role %q reason %q, want auto %q", command, role, reason, road.reason)
			}
			if got, want := seedsAutoTask(autoStartupFor(t, reason)), road.reason == LaunchReasonUser; got != want {
				t.Errorf("command %q: task seeded = %v, want %v", command, got, want)
			}
		})
	}
}

func TestParseLaunchArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		role     string
		resumeID string
		reason   LaunchReason
		wantErr  bool
	}{
		{"role only leaves the reason unset", []string{"auto"}, "auto", "", "", false},
		{"reason", []string{"auto", "--reason", "user"}, "auto", "", LaunchReasonUser, false},
		{"reason then resume", []string{"edit", "--reason", "restart", "--resume", restartResumeID}, "edit", restartResumeID, LaunchReasonRestart, false},
		{"resume then reason", []string{"edit", "--resume", restartResumeID, "--reason", "resume"}, "edit", restartResumeID, LaunchReasonResume, false},
		{"reason without a value", []string{"auto", "--reason"}, "", "", "", true},
		{"resume without a value", []string{"auto", "--resume"}, "", "", "", true},
		{"unknown flag", []string{"auto", "--fresh"}, "", "", "", true},
		{"second positional", []string{"auto", "user"}, "", "", "", true},
		{"no role", []string{"--reason", "user"}, "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role, resumeID, reason, err := ParseLaunchArgs(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseLaunchArgs(%v) err = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
			if role != tc.role || resumeID != tc.resumeID || reason != tc.reason {
				t.Errorf("ParseLaunchArgs(%v) = (%q, %q, %q), want (%q, %q, %q)",
					tc.args, role, resumeID, reason, tc.role, tc.resumeID, tc.reason)
			}
		})
	}
}
