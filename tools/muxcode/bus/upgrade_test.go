package bus

import (
	"testing"
)

func TestParseDaemonProcs_DaemonAndMonitor(t *testing.T) {
	psOutput := ` 5637 /Users/mk/.local/bin/muxcode watch muxcode
 5638 /Users/mk/.local/bin/muxcode watch --monitor muxcode
 7736 muxcode watch core-config
99999 grep muxcode watch core-config
12345 /usr/bin/vim muxcode watch notes.md
`
	procs := parseDaemonProcs(psOutput)
	if len(procs) != 3 {
		t.Fatalf("expected 3 procs, got %d: %+v", len(procs), procs)
	}
	if procs[0].PID != 5637 || procs[0].Session != "muxcode" || procs[0].Monitor {
		t.Errorf("proc[0] mismatch: %+v", procs[0])
	}
	if procs[1].PID != 5638 || procs[1].Session != "muxcode" || !procs[1].Monitor {
		t.Errorf("proc[1] should be monitor for muxcode: %+v", procs[1])
	}
	if procs[2].PID != 7736 || procs[2].Session != "core-config" || procs[2].Monitor {
		t.Errorf("proc[2] mismatch: %+v", procs[2])
	}
}

func TestParseDaemonProcs_FlagsWithValues(t *testing.T) {
	psOutput := `100 muxcode watch --poll 10 --debounce 5 my-session
200 muxcode watch --monitor --poll 10 other-session
`
	procs := parseDaemonProcs(psOutput)
	if len(procs) != 2 {
		t.Fatalf("expected 2 procs, got %d: %+v", len(procs), procs)
	}
	if procs[0].Session != "my-session" || procs[0].Monitor {
		t.Errorf("proc[0] should be daemon for my-session: %+v", procs[0])
	}
	if procs[1].Session != "other-session" || !procs[1].Monitor {
		t.Errorf("proc[1] should be monitor for other-session: %+v", procs[1])
	}
}

func TestParseDaemonProcs_UnknownFlagWithValue(t *testing.T) {
	// A future unknown value-taking flag must not have its value mistaken
	// for the session name — last positional wins.
	psOutput := `500 muxcode watch --some-flag 123 my-session
`
	procs := parseDaemonProcs(psOutput)
	if len(procs) != 1 {
		t.Fatalf("expected 1 proc, got %d: %+v", len(procs), procs)
	}
	if procs[0].Session != "my-session" {
		t.Errorf("expected session my-session, got %q", procs[0].Session)
	}
}

func TestParseDaemonProcs_SkipsNoSession(t *testing.T) {
	// "muxcode watch" with no session arg (current-dir inference) — cannot
	// upgrade what we can't name; skipped.
	psOutput := `300 muxcode watch
400 not-a-pid muxcode watch foo
`
	procs := parseDaemonProcs(psOutput)
	if len(procs) != 0 {
		t.Fatalf("expected 0 procs, got %d: %+v", len(procs), procs)
	}
}

// noDaemonBuild stands in for ReadDaemonVersion when a test is not about
// version awareness: every daemon reads as unstamped.
func noDaemonBuild(string) (Info, bool) { return Info{}, false }

func TestPlanUpgrades_GroupsBySessionAndSorts(t *testing.T) {
	procs := []DaemonProc{
		{PID: 20, Session: "beta", Monitor: false},
		{PID: 21, Session: "beta", Monitor: true},
		{PID: 10, Session: "alpha", Monitor: false},
	}
	plans := PlanUpgrades(procs, func(string) bool { return true }, noDaemonBuild, Info{})
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d: %+v", len(plans), plans)
	}
	if plans[0].Session != "alpha" || plans[0].DaemonPID != 10 || plans[0].MonitorPID != 0 {
		t.Errorf("plans[0] mismatch: %+v", plans[0])
	}
	if plans[1].Session != "beta" || plans[1].DaemonPID != 20 || plans[1].MonitorPID != 21 {
		t.Errorf("plans[1] mismatch: %+v", plans[1])
	}
	if plans[0].Orphan || plans[1].Orphan {
		t.Error("no plan should be orphan when sessionExists is always true")
	}
}

func TestPlanUpgrades_MarksOrphans(t *testing.T) {
	procs := []DaemonProc{
		{PID: 10, Session: "alive", Monitor: false},
		{PID: 20, Session: "dead", Monitor: false},
	}
	plans := PlanUpgrades(procs, func(s string) bool { return s == "alive" }, noDaemonBuild, Info{})
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].Session != "alive" || plans[0].Orphan {
		t.Errorf("alive session should not be orphan: %+v", plans[0])
	}
	if plans[1].Session != "dead" || !plans[1].Orphan {
		t.Errorf("dead session should be orphan: %+v", plans[1])
	}
}

// TestPlanUpgrades_VersionAwareness pins the staleness decision per session:
// only a daemon on the very same build is current; a same-version rebuild,
// an older version, and an unstamped daemon all cycle; an orphan is never
// current even when its build matches, because it is killed regardless.
func TestPlanUpgrades_VersionAwareness(t *testing.T) {
	installed := Info{Version: "v0.2.0", Commit: "def5678", Date: "2026-09-02T13:00:00Z"}
	builds := map[string]Info{
		"current":       installed,
		"stale-version": {Version: "v0.1.0", Commit: "abc1234", Date: "2026-09-01T10:00:00Z"},
		"stale-rebuild": {Version: "v0.2.0", Commit: "def5678", Date: "2026-09-02T12:00:00Z"},
		"orphan":        installed,
	}
	procs := []DaemonProc{
		{PID: 1, Session: "current"},
		{PID: 2, Session: "stale-version"},
		{PID: 3, Session: "stale-rebuild"},
		{PID: 4, Session: "unstamped"},
		{PID: 5, Session: "orphan"},
	}
	plans := PlanUpgrades(procs,
		func(s string) bool { return s != "orphan" },
		func(s string) (Info, bool) { b, ok := builds[s]; return b, ok },
		installed)
	if len(plans) != len(procs) {
		t.Fatalf("expected %d plans, got %d", len(procs), len(plans))
	}
	byName := map[string]UpgradePlan{}
	for _, p := range plans {
		byName[p.Session] = p
	}

	cases := []struct {
		session string
		current bool
		delta   string
	}{
		{"current", true, "daemon v0.2.0 → installed v0.2.0 (current)"},
		{"stale-version", false, "daemon v0.1.0 → installed v0.2.0"},
		{"stale-rebuild", false, "daemon v0.2.0 (built 2026-09-02T12:00:00Z) → installed v0.2.0 (built 2026-09-02T13:00:00Z)"},
		{"unstamped", false, "daemon (unstamped) → installed v0.2.0"},
		{"orphan", false, ""},
	}
	for _, c := range cases {
		p, ok := byName[c.session]
		if !ok {
			t.Fatalf("no plan for %s", c.session)
		}
		if p.Current != c.current {
			t.Errorf("%s: Current=%v, want %v (%+v)", c.session, p.Current, c.current, p)
		}
		if p.Installed != installed {
			t.Errorf("%s: installed identity not carried on the plan", c.session)
		}
		if c.delta != "" && p.VersionDelta() != c.delta {
			t.Errorf("%s: VersionDelta()=%q, want %q", c.session, p.VersionDelta(), c.delta)
		}
	}
	if !byName["orphan"].Orphan {
		t.Error("orphan session should be marked orphan")
	}
}
