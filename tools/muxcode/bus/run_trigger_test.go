package bus

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const triggerParentRun = "1790000000-parent-abcd1234"

// triggerTestBus gives a test an isolated bus and lifecycle log for
// runTestSession.
func triggerTestBus(t *testing.T) {
	t.Helper()
	useTempBusDir(t)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	if err := Init(runTestSession, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
}

func seedRequest(t *testing.T, from, to, msgType, action string) string {
	t.Helper()
	m := NewMessage(from, to, msgType, action, "payload", "")
	if err := SendNoCC(runTestSession, m); err != nil {
		t.Fatalf("send %s:%s to %s: %v", msgType, action, to, err)
	}
	return m.ID
}

// seedSpawnWorker registers a running worker for runID and sends it its seed.
// Returns the seed's message id.
func seedSpawnWorker(t *testing.T, spawnRole, runID string) string {
	t.Helper()
	seed := seedRequest(t, "edit", spawnRole, "request", "spawn-task")
	if err := WriteSpawnEntries(runTestSession, []SpawnEntry{
		{ID: "1-" + spawnRole, SpawnRole: spawnRole, RunID: runID, Status: "running", SeedMsgID: seed},
	}); err != nil {
		t.Fatal(err)
	}
	return seed
}

// dispatchParentNode starts a parent run as the user and ticks it once, so its
// first node (a send to build) is running and its dispatch is on the log.
// Returns the parent run id.
func dispatchParentNode(t *testing.T) string {
	t.Helper()
	pinActor(t, "")
	g := linearGraph()
	run, err := CreateGraphRun(runTestSession, g, g.Name, "parent")
	if err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeRunning {
		t.Fatalf("parent node a is %q, want running", s)
	}
	return run.ID
}

// MUX-141 Phase 3: the trigger is read from the verified actor, the spawn
// registry and the message log. Only a tie the bus holds is established; an
// agent's last request is recorded as inferred context. The startup cases are
// paired with their controls — a later request supersedes the startup, a
// response does not — so a derivation that always answers "startup" for an
// agent fails here. A regular agent's graph dispatch stays inferred even while
// its node reads running, since the node is marked running at enqueue, and a
// later request replaces it as the context.
func TestDeriveRunTrigger(t *testing.T) {
	const parent = "<parent>"
	cases := []struct {
		name     string
		actor    string
		setup    func(t *testing.T) (parentRun string)
		trigger  string
		detail   string
		inferred bool
	}{
		{"the user by hand", ActorUser, nil, RunTriggerUser, "", false},
		{"actor not established", ActorUnknown, nil, RunTriggerUnknown, "", false},
		{"agent with no request on the log", "auto", nil, RunTriggerUnknown, "", false},
		{"startup after a restart", "auto", func(t *testing.T) string {
			PreLaunchSetup("auto", runTestSession, "claude", LaunchReasonRestart)
			return ""
		}, RunTriggerStartup, "restart", true},
		{"startup of a user-started session", "auto", func(t *testing.T) string {
			PreLaunchSetup("auto", runTestSession, "claude", LaunchReasonUser)
			return ""
		}, RunTriggerStartup, "user", true},
		{"startup with the reason omitted", "auto", func(t *testing.T) string {
			PreLaunchSetup("auto", runTestSession, "claude", "")
			return ""
		}, RunTriggerStartup, "unset", true},
		{"a later request supersedes the startup", "auto", func(t *testing.T) string {
			PreLaunchSetup("auto", runTestSession, "claude", LaunchReasonRestart)
			seedRequest(t, "edit", "auto", "request", "implement")
			return ""
		}, RunTriggerBusRequest, "edit: implement", true},
		{"a later response does not", "auto", func(t *testing.T) string {
			PreLaunchSetup("auto", runTestSession, "claude", LaunchReasonRestart)
			seedRequest(t, "build", "auto", "response", "build")
			return ""
		}, RunTriggerStartup, "restart", true},
		{"another role's request does not", "auto", func(t *testing.T) string {
			PreLaunchSetup("auto", runTestSession, "claude", LaunchReasonRestart)
			seedRequest(t, "edit", "build", "request", "build")
			return ""
		}, RunTriggerStartup, "restart", true},
		{"worker of another run", "spawn-abcd1234", func(t *testing.T) string {
			seedSpawnWorker(t, "spawn-abcd1234", triggerParentRun)
			return ""
		}, RunTriggerGraphEdge, triggerParentRun, false},
		{"parked worker, its seed answered", "spawn-abcd1234", func(t *testing.T) string {
			seed := seedSpawnWorker(t, "spawn-abcd1234", triggerParentRun)
			MarkResponded(runTestSession, seed, "reply-1")
			return ""
		}, RunTriggerBusRequest, "edit: spawn-task", true},
		{"worker whose seed a later request superseded", "spawn-abcd1234", func(t *testing.T) string {
			seedSpawnWorker(t, "spawn-abcd1234", triggerParentRun)
			seedRequest(t, "review", "spawn-abcd1234", "request", "unrelated")
			return ""
		}, RunTriggerBusRequest, "review: unrelated", true},
		{"worker tied to no run", "spawn-abcd1234", func(t *testing.T) string {
			seedSpawnWorker(t, "spawn-abcd1234", "")
			return ""
		}, RunTriggerBusRequest, "edit: spawn-task", true},
		{"typed at the Prompt surface", "prompt", func(t *testing.T) string {
			if err := SendHumanPrompt(runTestSession, "build", "run the pipeline"); err != nil {
				t.Fatalf("SendHumanPrompt: %v", err)
			}
			return ""
		}, RunTriggerUser, "typed at the Prompt surface", true},
		{"regular agent whose last request is a running graph dispatch", "build", dispatchParentNode,
			RunTriggerGraphEdge, parent, true},
		{"a later request replaces the graph dispatch as context", "build", func(t *testing.T) string {
			run := dispatchParentNode(t)
			seedRequest(t, "edit", "build", "request", "unrelated")
			return run
		}, RunTriggerBusRequest, "edit: unrelated", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			triggerTestBus(t)
			wantDetail := tc.detail
			if tc.setup != nil {
				if run := tc.setup(t); wantDetail == parent {
					wantDetail = run
				}
			}
			trigger, detail, inferred := deriveRunTrigger(runTestSession, tc.actor)
			if trigger != tc.trigger || detail != wantDetail || inferred != tc.inferred {
				t.Errorf("deriveRunTrigger(%q) = (%q, %q, inferred %v), want (%q, %q, inferred %v)",
					tc.actor, trigger, detail, inferred, tc.trigger, wantDetail, tc.inferred)
			}
		})
	}
}

// Each trigger is recorded on the run and rendered in the same words on
// run.json, graph status and the graph-run-created lifecycle row. An inferred
// trigger is recorded as such and rendered as not established.
func TestRunTriggerRecordedAndRendered(t *testing.T) {
	const notEstablished = "not established — the launching agent's last bus request was "
	cases := []struct {
		name     string
		actor    string
		setup    func(t *testing.T)
		trigger  string
		inferred bool
		want     string
	}{
		{"user request", "", nil, RunTriggerUser, false, "a user request"},
		{"startup", "auto", func(t *testing.T) {
			PreLaunchSetup("auto", runTestSession, "claude", LaunchReasonRestart)
		}, RunTriggerStartup, true, notEstablished + "its startup message (launch reason: restart)"},
		{"bus request", "auto", func(t *testing.T) {
			seedRequest(t, "edit", "auto", "request", "implement")
		}, RunTriggerBusRequest, true, notEstablished + "a bus request (edit: implement)"},
		{"graph edge", "spawn-abcd1234", func(t *testing.T) {
			seedSpawnWorker(t, "spawn-abcd1234", triggerParentRun)
		}, RunTriggerGraphEdge, false, "graph run " + triggerParentRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			triggerTestBus(t)
			pinActor(t, tc.actor)
			if tc.setup != nil {
				tc.setup(t)
			}
			g := linearGraph()
			run, err := CreateGraphRun(runTestSession, g, g.Name, "test intent")
			if err != nil {
				t.Fatalf("CreateGraphRun: %v", err)
			}

			raw, err := os.ReadFile(graphRunPath(runTestSession, run.ID))
			if err != nil {
				t.Fatal(err)
			}
			var onDisk map[string]any
			if err := json.Unmarshal(raw, &onDisk); err != nil {
				t.Fatal(err)
			}
			if onDisk["trigger"] != tc.trigger || onDisk["triggered_by"] != tc.want {
				t.Errorf("run.json trigger = %v, triggered_by = %v; want %q, %q", onDisk["trigger"], onDisk["triggered_by"], tc.trigger, tc.want)
			}
			if inferred, _ := onDisk["trigger_inferred"].(bool); inferred != tc.inferred {
				t.Errorf("run.json trigger_inferred = %v, want %v", onDisk["trigger_inferred"], tc.inferred)
			}

			reread, err := ReadGraphRun(runTestSession, run.ID)
			if err != nil {
				t.Fatalf("ReadGraphRun: %v", err)
			}
			statuses, _ := ReadAllNodeStatuses(runTestSession, run.ID)
			if status := FormatGraphRun(reread, g, statuses); !strings.Contains(status, "Triggered by: "+tc.want+"\n") {
				t.Errorf("graph status does not render %q:\n%s", tc.want, status)
			}

			rows, err := FilterLifecycleLog(runTestSession, LifecycleFilterOpts{Event: "graph-run-created"})
			if err != nil || len(rows) != 1 {
				t.Fatalf("graph-run-created rows = %d (err %v), want 1", len(rows), err)
			}
			if !strings.Contains(rows[0].Detail, run.ID) || !strings.HasSuffix(rows[0].Detail, "triggered by: "+tc.want) {
				t.Errorf("graph-run-created %q does not name the trigger %q", rows[0].Detail, tc.want)
			}
		})
	}
}

// A run stored before the trigger fields existed renders as unrecorded, never
// as one of the real triggers.
func TestDescribeRunTrigger_UnrecordedAndUnknown(t *testing.T) {
	if got := DescribeRunTrigger("", "", false); got != "unrecorded" {
		t.Errorf("empty trigger renders %q, want unrecorded", got)
	}
	if got := DescribeRunTrigger(RunTriggerUnknown, "", false); got != "could not be established" {
		t.Errorf("unknown trigger renders %q, want could not be established", got)
	}
}

// The same graph edge reads as the parent run when established and as the last
// request's sender when inferred; neither borrows the creator's reserved words.
func TestDescribeRunTrigger_InferredGraphEdge(t *testing.T) {
	established := DescribeRunTrigger(RunTriggerGraphEdge, triggerParentRun, false)
	inferred := DescribeRunTrigger(RunTriggerGraphEdge, triggerParentRun, true)
	if established != "graph run "+triggerParentRun {
		t.Errorf("established graph edge renders %q", established)
	}
	if want := "not established — the launching agent's last bus request was a request from graph run " + triggerParentRun; inferred != want {
		t.Errorf("inferred graph edge renders %q, want %q", inferred, want)
	}
	for _, reserved := range []string{"the user", "autonomous"} {
		if strings.Contains(inferred, reserved) {
			t.Errorf("inferred trigger %q uses the creator's word %q", inferred, reserved)
		}
	}
}

// scanLinesBackward yields every line last-first across chunk boundaries — a
// line longer than a chunk included — skips blanks, reads a final line with no
// newline, and stops the moment visit declines.
func TestScanLinesBackward(t *testing.T) {
	long := strings.Repeat("x", 150<<10)
	path := t.TempDir() + "/log"
	if err := os.WriteFile(path, []byte("first\n"+long+"\n\nthird\nlast"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := scanLinesBackward(path, func(line []byte) bool {
		got = append(got, string(line))
		return true
	}); err != nil {
		t.Fatalf("scanLinesBackward: %v", err)
	}
	if want := []string{"last", "third", long, "first"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %d %.40q, want %d last-first", len(got), got, len(want))
	}

	got = nil
	_ = scanLinesBackward(path, func(line []byte) bool {
		got = append(got, string(line))
		return len(got) < 2
	})
	if len(got) != 2 || got[1] != "third" {
		t.Errorf("early stop visited %.40q, want [last third]", got)
	}
	if err := scanLinesBackward(path+".missing", func([]byte) bool { t.Error("visited a missing file"); return true }); err != nil {
		t.Errorf("missing file: %v", err)
	}
}
