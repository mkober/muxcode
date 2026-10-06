package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func massExitBase(t *testing.T, sessions ...string) {
	t.Helper()
	SetBusDirBase(t.TempDir())
	t.Cleanup(ResetBusDirBase)
	for _, s := range sessions {
		if err := os.MkdirAll(BusDir(s), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
}

func recordExit(t *testing.T, session, role string, at int64) {
	t.Helper()
	if err := RecordAgentExit(session, role, at); err != nil {
		t.Fatalf("RecordAgentExit(%s, %s): %v", session, role, err)
	}
}

// Sightings in every visible session count toward one burst; one outside the
// window does not.
func TestRecentAgentExits_CrossSession(t *testing.T) {
	massExitBase(t, "alpha", "beta", "gamma")
	const now = 1_800_000_000
	recordExit(t, "alpha", "plan", now-10)
	recordExit(t, "beta", "commit", now-5)
	recordExit(t, "gamma", "run", now-200)

	got, err := RecentAgentExits("alpha", now, 60)
	if err != nil {
		t.Fatalf("RecentAgentExits: %v", err)
	}
	if len(got) != 2 || got[0].Session != "alpha" || got[1].Session != "beta" {
		t.Fatalf("sightings = %+v, want alpha/plan then beta/commit", got)
	}

	m, ok := DetectMassExit(got, 60)
	if !ok {
		t.Fatal("two sessions' deaths within the window were not a mass exit")
	}
	detail := m.Detail()
	for _, want := range []string{"2 Claude agents", "within 60s", "2 session(s)", "alpha: plan", "beta: commit"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q missing %q", detail, want)
		}
	}
	if strings.Contains(detail, "gamma") {
		t.Errorf("detail names a death outside the window: %q", detail)
	}
}

// Negative control: two unrelated single deaths five minutes apart are never
// one mass exit, from either side of the gap.
func TestDetectMassExit_SpacedDeathsAreNotCorrelated(t *testing.T) {
	massExitBase(t, "alpha")
	const first = 1_800_000_000
	recordExit(t, "alpha", "plan", first)
	recordExit(t, "alpha", "commit", first+300)

	for _, now := range []int64{first, first + 300} {
		got, err := RecentAgentExits("alpha", now, 60)
		if err != nil {
			t.Fatalf("RecentAgentExits: %v", err)
		}
		if m, ok := DetectMassExit(got, 60); ok {
			t.Errorf("at %d: spaced deaths raised a mass exit: %s", now, m.Detail())
		}
	}
}

// One agent failing its restarts is sighted repeatedly; it must never
// correlate with itself.
func TestDetectMassExit_DistinctAgents(t *testing.T) {
	cases := []struct {
		name      string
		sightings []ExitSighting
		want      bool
	}{
		{"none", nil, false},
		{"one", []ExitSighting{{"a", "plan", 1}}, false},
		{"same agent twice", []ExitSighting{{"a", "plan", 1}, {"a", "plan", 2}}, false},
		{"same role, two sessions", []ExitSighting{{"a", "plan", 1}, {"b", "plan", 2}}, true},
		{"two roles, one session", []ExitSighting{{"a", "plan", 1}, {"a", "run", 2}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got := DetectMassExit(c.sightings, 60); got != c.want {
				t.Errorf("DetectMassExit = %v, want %v", got, c.want)
			}
		})
	}
}

// The exit log is bounded: rows older than the retention horizon are dropped
// on the next write, recent ones kept.
func TestRecordAgentExit_PrunesOldRows(t *testing.T) {
	massExitBase(t, "alpha")
	const now = 1_800_000_000
	recordExit(t, "alpha", "plan", now-agentExitsRetainSecs-1)
	recordExit(t, "alpha", "run", now-100)
	recordExit(t, "alpha", "commit", now)

	rows := readAgentExits(filepath.Join(BusDir("alpha"), agentExitsFile))
	if len(rows) != 2 || rows[0].Role != "run" || rows[1].Role != "commit" {
		t.Fatalf("rows = %+v, want run then commit", rows)
	}
}

func TestMassExitWindowSecs(t *testing.T) {
	for value, want := range map[string]int64{"": 60, "120": 120, "0": 60, "-5": 60, "abc": 60} {
		t.Setenv(massExitWindowEnv, value)
		if got := MassExitWindowSecs(); got != want {
			t.Errorf("%s=%q: got %d, want %d", massExitWindowEnv, value, got, want)
		}
	}
}

// A dead agent whose pane still offers its session gets the resumable-session
// finding; alive, mid-reload or banner-less panes do not.
func TestCheckResumableSession(t *testing.T) {
	const id = "8a744341-11bf-440f-b5d2-49248447a9c0"
	cases := []struct {
		name  string
		state AgentStateEvidence
		want  bool
	}{
		{"dead with banner", AgentStateEvidence{ResumeSessionID: id}, true},
		{"stopped with banner", AgentStateEvidence{IsStopped: true, ResumeSessionID: id}, true},
		{"dead without banner", AgentStateEvidence{}, false},
		{"alive", AgentStateEvidence{IsAlive: true, ResumeSessionID: id}, false},
		{"reloading", AgentStateEvidence{IsReloading: true, ResumeSessionID: id}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := checkResumableSession(&DiagnosticReport{Role: "plan", AgentState: c.state})
			if (f != nil) != c.want {
				t.Fatalf("finding = %+v, want present=%v", f, c.want)
			}
			if f == nil {
				return
			}
			if f.Severity != "info" || f.FailureMode != "resumable-session" {
				t.Errorf("finding = %s/%s, want info/resumable-session", f.Severity, f.FailureMode)
			}
			if !strings.Contains(strings.Join(f.Remediation, "\n"), "muxcode agent launch plan --resume "+id) {
				t.Errorf("remediation does not name the session: %v", f.Remediation)
			}
		})
	}
}
