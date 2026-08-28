package bus

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRenderSpawnConsoleShowsOwningRun(t *testing.T) {
	run := createTestRun(t, linearGraph())
	// ID and SpawnRole must DIFFER, and the node stores SpawnRole — the
	// real shape (graphSpawnFn returns entry.SpawnRole). Identical values
	// here made the original test pass over a live match-direction bug.
	entry := SpawnEntry{ID: "1787900000-spawn-cafe0001", Role: "edit", SpawnRole: "spawn-cafe0001",
		Owner: "daemon", Task: "Implement per the active requirements spec",
		Status: "running", StartedAt: time.Now().Unix()}
	if err := appendSpawnEntry(runTestSession, entry); err != nil {
		t.Fatal(err)
	}
	if err := MutateNodeStatus(runTestSession, run.ID, "a", func(st *GraphNodeStatus) {
		st.State = GraphNodeRunning
		st.TaskID = entry.SpawnRole
	}); err != nil {
		t.Fatal(err)
	}

	out := RenderSpawnConsole(runTestSession, "spawn-cafe0001", 120)
	if !strings.Contains(out, "Implement per the active requirements spec") {
		t.Errorf("console must show the spawn task:\n%s", out)
	}
	if !strings.Contains(out, run.ID) || !strings.Contains(out, "running") {
		t.Errorf("console must show the owning run's node state:\n%s", out)
	}
}

func TestRenderSpawnConsoleAdHocSpawn(t *testing.T) {
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	entry := SpawnEntry{ID: "spawn-solo0001", SpawnRole: "spawn-solo0001",
		Owner: "edit", Task: "one-off", Status: "running", StartedAt: time.Now().Unix()}
	if err := appendSpawnEntry(runTestSession, entry); err != nil {
		t.Fatal(err)
	}
	out := RenderSpawnConsole(runTestSession, "spawn-solo0001", 120)
	if !strings.Contains(out, "one-off") || !strings.Contains(out, "no owning graph run") {
		t.Errorf("ad-hoc spawn must show task and say it has no run:\n%s", out)
	}
}

func TestRenderSpawnConsoleUnknownRole(t *testing.T) {
	useTempBusDir(t)
	out := RenderSpawnConsole(runTestSession, "spawn-none", 120)
	if !strings.Contains(out, "no spawn entry") {
		t.Errorf("unknown spawn role must degrade explicitly:\n%s", out)
	}
}
