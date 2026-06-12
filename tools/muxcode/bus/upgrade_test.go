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

func TestPlanUpgrades_GroupsBySessionAndSorts(t *testing.T) {
	procs := []DaemonProc{
		{PID: 20, Session: "beta", Monitor: false},
		{PID: 21, Session: "beta", Monitor: true},
		{PID: 10, Session: "alpha", Monitor: false},
	}
	plans := PlanUpgrades(procs, func(string) bool { return true })
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
	plans := PlanUpgrades(procs, func(s string) bool { return s == "alive" })
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
