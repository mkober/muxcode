package bus

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// windowlessSession sets up a bus session whose analyze role has no consumer:
// tmux points at an empty socket directory, so no window, pane or session
// exists for any role, and nothing polls or runs headless for analyze. The
// daemon reads alive on the current build, so its checks stay quiet and only
// the role's own evidence decides the verdict.
func windowlessSession(t *testing.T, cli string) string {
	t.Helper()
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv(RoleCLIEnvVar("analyze"), cli)
	useTempBusDir(t)
	session := testSession(t)
	if err := os.WriteFile(DaemonKeepalivePath(session), []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteDaemonVersion(session, BuildInfo()); err != nil {
		t.Fatal(err)
	}
	return session
}

// stubWindows makes TmuxListWindowNames report names for any session, or fail
// with err; every other tmux call fails, as with no server.
func stubWindows(t *testing.T, names []string, err error) {
	t.Helper()
	orig := tmuxOutputRunner
	t.Cleanup(func() { tmuxOutputRunner = orig })
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-windows" {
			return strings.Join(names, "\n"), err
		}
		return "", errors.New("no tmux in this test")
	}
}

func noConsumerRows(t *testing.T, session string) []string {
	t.Helper()
	rows, err := FilterLifecycleLog(session, LifecycleFilterOpts{Event: "send-no-consumer"})
	if err != nil {
		t.Fatalf("FilterLifecycleLog: %v", err)
	}
	var details []string
	for _, r := range rows {
		details = append(details, r.Detail)
	}
	return details
}

// TestSendRefusesRoleWithNoConsumer is MUX-145 Defect A fixed, inverting the
// Phase 1 pin: a session whose windows hold no analyze, with no listener or
// headless agent for it, refuses the daemon's analyze event with ErrNoConsumer
// and writes nothing to the inbox. The refusal is logged once per episode —
// repeated refusals add no row, a consumer appearing ends the episode, and a
// second episode logs again.
func TestSendRefusesRoleWithNoConsumer(t *testing.T) {
	session := windowlessSession(t, "opencode")
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	stubWindows(t, []string{"edit", "build"}, nil)
	event := func() Message {
		return NewMessage("daemon", "analyze", "event", "analyze", "Claude edited files: a.go", "")
	}

	for i := 0; i < 3; i++ {
		if err := Send(session, event()); !errors.Is(err, ErrNoConsumer) {
			t.Fatalf("send %d to a role with no consumer: err %v, want ErrNoConsumer", i, err)
		}
	}
	if msgs, _ := Peek(session, "analyze"); len(msgs) != 0 {
		t.Errorf("a refused send wrote %d messages to the inbox", len(msgs))
	}
	if rows := noConsumerRows(t, session); len(rows) != 1 || !strings.HasPrefix(rows[0], "analyze:") {
		t.Errorf("want one send-no-consumer row naming analyze across three refusals, got %q", rows)
	}

	stubWindows(t, []string{"edit", "analyze"}, nil)
	if err := Send(session, event()); err != nil {
		t.Fatalf("with an analyze window the send must land: %v", err)
	}
	stubWindows(t, []string{"edit"}, nil)
	if err := Send(session, event()); !errors.Is(err, ErrNoConsumer) {
		t.Fatalf("the window gone again: err %v, want ErrNoConsumer", err)
	}
	if rows := noConsumerRows(t, session); len(rows) != 2 {
		t.Errorf("a second episode must log again: %d rows, want 2", len(rows))
	}
}

// TestSendDeliversWhenAConsumerExists is the negative control: a window is not
// the test. Each case has no analyze window — or reaches a role the refusal
// must let through — and its send lands in the inbox it addresses: a live
// --poll or --wait listener, a running harness, an unreadable window list,
// a hosted role whose host has a window, a spawn worker seeded before its
// window exists, a mode role once its hold window exists, the human prompt
// surface with the prompt agent running, the startup bootstrap self-send, and
// the guard turned off.
func TestSendDeliversWhenAConsumerExists(t *testing.T) {
	cases := []struct {
		name    string
		windows []string
		listErr error
		setup   func(t *testing.T, session string)
		msg     Message
		inbox   string
	}{
		{name: "listener", windows: []string{"edit"},
			setup: func(t *testing.T, s string) { SetPolling(s, "analyze") }},
		{name: "waiting", windows: []string{"edit"},
			setup: func(t *testing.T, s string) { SetWaiting(s, "analyze") }},
		{name: "harness", windows: []string{"edit"}, setup: func(t *testing.T, s string) {
			if err := os.WriteFile(HarnessMarkerPath(s, "analyze"), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unreadable window list", listErr: errors.New("can't find session")},
		{name: "hosted docs to plan", windows: []string{"edit", "plan"},
			msg: NewMessage("edit", "docs", "request", "update-docs", "a doc change", ""), inbox: "plan"},
		{name: "spawn worker before its window", windows: []string{"edit"},
			msg: NewMessage("daemon", "spawn-abc12345", "request", "spawn-task", "seed", ""), inbox: "spawn-abc12345"},
		{name: "mode role once cycled to", windows: []string{"edit", "plan", "research"},
			msg: NewMessage("edit", "research", "request", "research", "look this up", ""), inbox: "research"},
		{name: "startup bootstrap", windows: []string{"edit"},
			msg: NewMessage("analyze", "analyze", "request", "startup", "restore context", "")},
		{name: "guard off", windows: []string{"edit"},
			setup: func(t *testing.T, s string) { t.Setenv(consumerGuardEnv, "0") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			session := windowlessSession(t, "opencode")
			stubWindows(t, c.windows, c.listErr)
			if c.setup != nil {
				c.setup(t, session)
			}
			m, inbox := c.msg, c.inbox
			if m.ID == "" {
				m = NewMessage("daemon", "analyze", "event", "analyze", "Claude edited files: a.go", "")
			}
			if inbox == "" {
				inbox = "analyze"
			}
			if err := SendNoCC(session, m); err != nil {
				t.Fatalf("send must land: %v", err)
			}
			if msgs, _ := Peek(session, inbox); len(msgs) != 1 || msgs[0].ID != m.ID {
				t.Errorf("%s's inbox must hold the message, got %+v", inbox, msgs)
			}
		})
	}

	t.Run("prompt surface with the prompt agent running", func(t *testing.T) {
		session := windowlessSession(t, "opencode")
		stubWindows(t, []string{"edit"}, nil)
		if err := os.WriteFile(HarnessMarkerPath(session, promptAgentRole), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
			t.Fatal(err)
		}
		if !PromptAgentAlive(session) {
			t.Fatal("fixture: the prompt agent must read as running")
		}
		if err := SendHumanPrompt(session, "edit", "run the tests"); err != nil {
			t.Fatalf("a typed prompt must land: %v", err)
		}
		if msgs, _ := Peek(session, "prompt"); len(msgs) != 1 {
			t.Errorf("prompt's inbox holds %d messages, want 1", len(msgs))
		}
	})
}

// TestSendRefusesNeverCycledModeRole: a mode role gets no exception (the
// user's decision of 2026-11-16). research and auto, never cycled to — no hold
// window, no listener — refuse a send with nothing written; once cycled to,
// TestSendDeliversWhenAConsumerExists shows the send landing.
func TestSendRefusesNeverCycledModeRole(t *testing.T) {
	for _, role := range []string{"research", "auto"} {
		t.Run(role, func(t *testing.T) {
			session := windowlessSession(t, "opencode")
			stubWindows(t, []string{"edit", "plan"}, nil)
			m := NewMessage("edit", role, "request", role, "take this on", "")
			if err := SendNoCC(session, m); !errors.Is(err, ErrNoConsumer) {
				t.Fatalf("send to never-cycled %s: err %v, want ErrNoConsumer", role, err)
			}
			if msgs, _ := Peek(session, role); len(msgs) != 0 {
				t.Errorf("a refused send wrote %d messages to %s", len(msgs), role)
			}
		})
	}
}

// TestSendRefusesPromptWithNoRunningAgent: the prompt agent being enabled is
// configuration, not a reader. With it enabled but not running — no harness
// marker — a typed prompt is refused like any other send to a role with no
// consumer, and nothing reaches the inbox.
func TestSendRefusesPromptWithNoRunningAgent(t *testing.T) {
	session := windowlessSession(t, "opencode")
	stubWindows(t, []string{"edit"}, nil)
	if !PromptAgentEnabled() || PromptAgentAlive(session) {
		t.Fatal("fixture: the prompt agent must be enabled and not running")
	}
	if err := SendHumanPrompt(session, "edit", "run the tests"); !errors.Is(err, ErrNoConsumer) {
		t.Fatalf("a prompt with no running agent: err %v, want ErrNoConsumer", err)
	}
	if msgs, _ := Peek(session, "prompt"); len(msgs) != 0 {
		t.Errorf("a refused prompt wrote %d messages", len(msgs))
	}
}

// TestStatusShowsNoConsumer is MUX-145 Phase 4: `muxcode status` marks a role
// nothing can read for as no-consumer, distinct from an idle role that has a
// reader — in the data and in the table. A hosted role is judged by its
// host's window, so docs with plan's window is not misreported while pr-read
// with no commit window is, and a never-cycled mode role is reported like any
// other. Controls: a windowed role and the health-excluded webhook keep their
// own health.
func TestStatusShowsNoConsumer(t *testing.T) {
	session := windowlessSession(t, "opencode")
	stubWindows(t, []string{"edit", "plan", "build"}, nil)
	cases := []struct {
		role       string
		noConsumer bool
	}{
		{"analyze", true},
		{"pr-read", true},
		{"build", false},
		{"docs", false},
		{"research", true},
		{"webhook", false},
	}
	var statuses []AgentStatus
	for _, c := range cases {
		s := GetAgentStatus(session, c.role)
		statuses = append(statuses, s)
		if got := s.Health == "no-consumer"; got != c.noConsumer {
			t.Errorf("%s health = %q, want no-consumer=%v", c.role, s.Health, c.noConsumer)
		}
	}
	if got := GetAgentStatus(session, "webhook").Health; got != "excluded" {
		t.Errorf("webhook health = %q, want excluded", got)
	}
	for _, line := range strings.Split(FormatStatusTable(statuses), "\n") {
		if strings.HasPrefix(line, "analyze ") && !strings.Contains(line, "no-consumer") {
			t.Errorf("the table does not mark analyze: %q", line)
		}
		if strings.HasPrefix(line, "build ") && strings.Contains(line, "no-consumer") {
			t.Errorf("the table marks a windowed role: %q", line)
		}
	}
}

// strandEvents writes n analyze events aged past the stuck threshold, the
// pile a role with no consumer held before the refusal (MUX-145).
func strandEvents(t *testing.T, session string, n int) {
	t.Helper()
	t.Setenv(consumerGuardEnv, "0")
	for i := 0; i < n; i++ {
		m := NewMessage("daemon", "analyze", "event", "analyze", "Claude edited files: f"+strconv.Itoa(i)+".go", "")
		m.TS = time.Now().Unix() - diagnoseStuckInboxSecs - 60
		if err := Send(session, m); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(consumerGuardEnv, "")
}

func diagnose(session, role string) DiagnosticReport {
	report := CollectEvidence(session, role)
	RunDiagnostics(&report)
	return report
}

func findingFor(report DiagnosticReport, mode string) *DiagnosticFinding {
	for i, f := range report.Findings {
		if f.FailureMode == mode {
			return &report.Findings[i]
		}
	}
	return nil
}

// TestDiagnoseNoConsumer is MUX-145 Defect B and the 2026-09-08 clean verdict
// fixed, inverting the Phase 1 pin. With analyze absent from a known window
// list and nothing else reading it, stranded messages give a critical
// no-consumer finding naming the launch and stop-routing remediations, and no
// finding prescribes `muxcode deliver` — receipt-gap included — nor calls the
// state unexplained. With the inbox empty the role is still reported, as a
// warning, never "No issues detected".
func TestDiagnoseNoConsumer(t *testing.T) {
	for _, cli := range []string{"claude", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			session := windowlessSession(t, cli)
			stubWindows(t, []string{"edit", "build"}, nil)
			strandEvents(t, session, 3)

			report := diagnose(session, "analyze")
			nc := findingFor(report, "no-consumer")
			if nc == nil || nc.Severity != "critical" {
				t.Fatalf("want a critical no-consumer finding, got %+v", report.Findings)
			}
			remedy := strings.Join(nc.Remediation, "\n")
			if !strings.Contains(remedy, "MUXCODE_WINDOWS") || !strings.Contains(remedy, "stop routing") {
				t.Errorf("remediation must name launching the role and stopping the routing: %q", remedy)
			}
			for _, f := range report.Findings {
				if prescribesDeliver(&f) {
					t.Errorf("%s prescribes deliver for a role with no pane: %q", f.FailureMode, f.Remediation)
				}
				if f.FailureMode == "receipt-gap" || f.FailureMode == "unexplained-stuck-inbox" {
					t.Errorf("%s fired beside no-consumer", f.FailureMode)
				}
			}

			if _, err := Receive(session, "analyze"); err != nil {
				t.Fatal(err)
			}
			empty := diagnose(session, "analyze")
			if nc := findingFor(empty, "no-consumer"); nc == nil || nc.Severity != "warning" {
				t.Errorf("an empty inbox must still report no-consumer as a warning, got %+v", empty.Findings)
			}
			if out := FormatDiagnosticReport(&empty); strings.Contains(out, "No issues detected") {
				t.Errorf("a role with no consumer read clean:\n%s", out)
			}
		})
	}
}

// TestDiagnoseConsumerControls are the negative controls: a role with a
// window and a genuine receipt gap still reports receipt-gap with its
// force-deliver remediation; a windowless role with its own listener is not
// reported; and an unreadable window list — indeterminate, so no claim is
// made — leaves receipt-gap standing, as before MUX-145.
func TestDiagnoseConsumerControls(t *testing.T) {
	cases := []struct {
		name     string
		windows  []string
		listErr  error
		listener bool
	}{
		{name: "windowed role with a real receipt gap", windows: []string{"edit", "analyze"}},
		{name: "windowless role with its own listener", windows: []string{"edit"}, listener: true},
		{name: "unreadable window list", listErr: errors.New("no server")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			session := windowlessSession(t, "opencode")
			stubWindows(t, c.windows, c.listErr)
			if c.listener {
				SetPolling(session, "analyze")
			}
			strandEvents(t, session, 2)

			report := diagnose(session, "analyze")
			if f := findingFor(report, "no-consumer"); f != nil {
				t.Errorf("no-consumer fired with a consumer present: %s", f.Summary)
			}
			if c.listener {
				return
			}
			gap := findingFor(report, "receipt-gap")
			if gap == nil || !prescribesDeliver(gap) {
				t.Errorf("a genuine receipt gap must still report receipt-gap with deliver, got %+v", report.Findings)
			}
		})
	}
}
