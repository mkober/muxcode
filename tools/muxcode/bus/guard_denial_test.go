package bus

import (
	"strings"
	"testing"
	"time"
)

const testDenialReason = "BLOCKED: hook-road evidence — the build command must be the only statement in its call"

// fakeGuardRoad records what processGuardDenials did to the pane and the bus.
type fakeGuardRoad struct {
	busy      bool
	deliver   bool
	redrives  []string
	alerts    []Message
	typedText []string
}

func (f *fakeGuardRoad) road() guardDenialRoad {
	return guardDenialRoad{
		working: func(string, string) bool { return f.busy },
		redrive: func(session string, t Task) bool {
			f.redrives = append(f.redrives, t.ID)
			f.typedText = append(f.typedText, guardDenialRedriveText(session, t.ID))
			return f.deliver
		},
		send: func(_ string, m Message) error { f.alerts = append(f.alerts, m); return nil },
	}
}

// receivedTask creates an in-flight graph dispatch to build that build's
// listener has read — the state a denial interrupts.
func receivedTask(t *testing.T, session string) Task {
	t.Helper()
	m := NewMessage("daemon", "build", "request", "build", "Run ./build.sh", "")
	m.GraphRun, m.GraphNode = "run-1", "build-node"
	if err := CreateTask(session, m, 600); err != nil {
		t.Fatal(err)
	}
	WriteReceipt(session, m.ID, "build", ReceiptKindAck)
	task, err := ReadTask(session, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func deny(t *testing.T, session string) {
	t.Helper()
	if err := RecordGuardDenial(session, "build", "Bash", testDenialReason); err != nil {
		t.Fatal(err)
	}
}

func TestGuardDenialRedrivesTheInterruptedTaskOnceWithTheReason(t *testing.T) {
	session := testSession(t)
	task := receivedTask(t, session)
	f := &fakeGuardRoad{deliver: true}
	deny(t, session)

	processGuardDenials(session, time.Now().Unix(), f.road())
	if len(f.redrives) != 1 || f.redrives[0] != task.ID {
		t.Fatalf("redrive calls = %v; want one re-drive of %s", f.redrives, task.ID)
	}
	if !strings.Contains(f.typedText[0], "hook-road evidence") {
		t.Errorf("re-drive text %q does not carry the guard's reason", f.typedText[0])
	}
	if len(f.alerts) != 0 {
		t.Errorf("a delivered re-drive alerted edit: %v", f.alerts)
	}
	if st, ok := ReadTaskGuardDenial(session, task.ID); !ok || st.RedrivenAt == 0 {
		t.Errorf("denial state = %+v, %v; want a recorded re-drive", st, ok)
	}
	if pending := pendingGuardDenials(session); len(pending) != 0 {
		t.Errorf("the denial record was not consumed: %v", pending)
	}
}

func TestGuardDenialWithNoInFlightTaskDoesNothing(t *testing.T) {
	session := testSession(t)
	f := &fakeGuardRoad{deliver: true}
	deny(t, session)

	if pending := pendingGuardDenials(session); len(pending) != 0 {
		t.Errorf("a denial that interrupted nothing was filed: %v", pending)
	}
	if processGuardDenials(session, time.Now().Unix(), f.road()); len(f.redrives) != 0 || len(f.alerts) != 0 {
		t.Fatalf("no task, yet redrives %v, alerts %v", f.redrives, f.alerts)
	}
}

func TestGuardDenialIgnoresARequestStillInTheInbox(t *testing.T) {
	session := testSession(t)
	m := NewMessage("daemon", "build", "request", "build", "Run ./build.sh", "")
	if err := CreateTask(session, m, 600); err != nil {
		t.Fatal(err)
	}
	f := &fakeGuardRoad{deliver: true}
	deny(t, session)

	if processGuardDenials(session, time.Now().Unix(), f.road()); len(f.redrives) != 0 || len(f.alerts) != 0 {
		t.Fatalf("an unreceived request was acted on: redrives %v, alerts %v", f.redrives, f.alerts)
	}
	if GuardDenialOwns(session, m.ID) {
		t.Error("an unreceived request was claimed by the guard road")
	}
}

// The state is on disk: a fresh road per denial is a daemon restart between them.
func TestGuardDenialSecondOnTheSameTaskAlertsAcrossARestart(t *testing.T) {
	session := testSession(t)
	task := receivedTask(t, session)
	now := time.Now().Unix()
	first := &fakeGuardRoad{deliver: true}
	deny(t, session)
	processGuardDenials(session, now, first.road())

	restarted := &fakeGuardRoad{deliver: true}
	deny(t, session)
	processGuardDenials(session, now, restarted.road())
	deny(t, session)
	processGuardDenials(session, now, restarted.road())

	if len(first.redrives) != 1 || len(restarted.redrives) != 0 {
		t.Errorf("redrives before %v, after restart %v; want exactly one in all", first.redrives, restarted.redrives)
	}
	if len(restarted.alerts) != 1 {
		t.Fatalf("alerts = %d, want one for the repeat denials", len(restarted.alerts))
	}
	for _, want := range []string{task.ID, "run-1", "build-node", "hook-road evidence"} {
		if !strings.Contains(restarted.alerts[0].Payload, want) {
			t.Errorf("alert %q does not name %q", restarted.alerts[0].Payload, want)
		}
	}
}

// Guard first, then any other road: once the guard road holds the task, the
// stall and force-redrive roads and RedriveTask are refused.
func TestGuardDenialOwnsTheTaskOnceFiledAgainstOtherRedriveRoads(t *testing.T) {
	session := testSession(t)
	task := receivedTask(t, session)
	if GuardDenialOwns(session, task.ID) || len(withoutGuardOwned(session, []Task{task})) != 1 {
		t.Fatal("an undenied task is not eligible for the other roads")
	}
	deny(t, session)
	if !GuardDenialOwns(session, task.ID) {
		t.Error("a filed, unprocessed denial leaves the task to the other roads")
	}
	processGuardDenials(session, time.Now().Unix(), (&fakeGuardRoad{deliver: true}).road())

	if len(withoutGuardOwned(session, []Task{task})) != 0 {
		t.Error("the force-redrive batch still holds the re-driven denied task")
	}
	if RedriveTask(session, task) {
		t.Error("RedriveTask re-drove a task the guard road already re-drove")
	}
}

// Stall first, then guard: a re-drive before the denial is not counted, so the
// guard road still makes its one re-drive — and nothing after it.
func TestGuardDenialAfterAnEarlierRedriveStillRedrivesOnce(t *testing.T) {
	session := testSession(t)
	task := receivedTask(t, session)
	if len(withoutGuardOwned(session, []Task{task})) != 1 {
		t.Fatal("before any denial the stall road may re-drive the task")
	}
	f := &fakeGuardRoad{deliver: true}
	deny(t, session)
	processGuardDenials(session, time.Now().Unix(), f.road())

	if len(f.redrives) != 1 {
		t.Fatalf("redrives = %v, want the guard road's one", f.redrives)
	}
	if RedriveTask(session, task) {
		t.Error("a second re-drive followed the guard road's")
	}
}

func TestGuardDenialDeferredLeavesNoOtherRoadARedrive(t *testing.T) {
	session := testSession(t)
	task := receivedTask(t, session)
	deny(t, session)
	processGuardDenials(session, time.Now().Unix(), (&fakeGuardRoad{busy: true, deliver: true}).road())

	if len(withoutGuardOwned(session, []Task{task})) != 0 || RedriveTask(session, task) {
		t.Error("a deferred denial left the task to another re-drive road")
	}
}

func TestGuardDenialOnAWorkingPaneAlertsOnlyAfterAFullIdleGrace(t *testing.T) {
	session := testSession(t)
	receivedTask(t, session)
	f := &fakeGuardRoad{busy: true, deliver: true}
	now := time.Now().Unix()
	deny(t, session)
	for s := int64(0); s <= 60; s += 5 {
		processGuardDenials(session, now+s, f.road())
	}
	if len(f.redrives) != 0 || len(f.alerts) != 0 {
		t.Fatalf("working pane for 60 s: redrives %v, alerts %v; want neither", f.redrives, f.alerts)
	}

	f.busy = false
	processGuardDenials(session, now+61, f.road())
	f.busy = true
	processGuardDenials(session, now+65, f.road())
	f.busy = false
	processGuardDenials(session, now+66, f.road())
	processGuardDenials(session, now+61+guardDenialIdleGraceSecs, f.road())
	if len(f.alerts) != 0 {
		t.Fatalf("alerted before a continuous idle grace: %v", f.alerts)
	}
	processGuardDenials(session, now+66+guardDenialIdleGraceSecs, f.road())
	processGuardDenials(session, now+70+guardDenialIdleGraceSecs, f.road())
	if len(f.redrives) != 0 || len(f.alerts) != 1 {
		t.Fatalf("idle and unanswered: redrives %v, alerts %d; want no re-drive and one alert", f.redrives, len(f.alerts))
	}
}

// The Claude road: the agent retries with the guard's form and answers inside
// the grace, so a deferred denial must never alert.
func TestGuardDenialDeferredThenAnsweredWithinTheGraceNeverAlerts(t *testing.T) {
	session := testSession(t)
	task := receivedTask(t, session)
	f := &fakeGuardRoad{busy: true, deliver: true}
	now := time.Now().Unix()
	deny(t, session)
	processGuardDenials(session, now, f.road())
	f.busy = false
	processGuardDenials(session, now+60, f.road())
	CompleteTask(session, task.ID, "resp-1")
	processGuardDenials(session, now+60+guardDenialIdleGraceSecs, f.road())

	if len(f.alerts) != 0 || len(f.redrives) != 0 {
		t.Fatalf("answered task: redrives %v, alerts %v; want neither", f.redrives, f.alerts)
	}
}

func TestGuardDenialUndeliverableRedriveAlerts(t *testing.T) {
	session := testSession(t)
	receivedTask(t, session)
	f := &fakeGuardRoad{deliver: false}
	deny(t, session)
	processGuardDenials(session, time.Now().Unix(), f.road())

	if len(f.alerts) != 1 || !strings.Contains(f.alerts[0].Payload, "could not be delivered") {
		t.Fatalf("alerts = %v, want one naming the undelivered re-drive", f.alerts)
	}
}

// A task answered, or expired, between the denial and the daemon's first look
// is annotated — nothing re-driven, nothing alerted — and its note survives.
func TestGuardDenialOnATaskEndedBeforeProcessingIsAnnotatedOnly(t *testing.T) {
	for _, end := range []string{"answered", "expired"} {
		t.Run(end, func(t *testing.T) {
			session := testSession(t)
			task := receivedTask(t, session)
			deny(t, session)
			if end == "answered" {
				CompleteTask(session, task.ID, "resp-1")
			} else {
				task.SentAt -= 601
				if err := writeTask(session, task); err != nil {
					t.Fatal(err)
				}
			}
			f := &fakeGuardRoad{deliver: true}
			processGuardDenials(session, time.Now().Unix(), f.road())

			if len(f.redrives) != 0 || len(f.alerts) != 0 {
				t.Errorf("redrives %v, alerts %v; want neither", f.redrives, f.alerts)
			}
			if got := GuardDenialNote(session, task.ID, "done"); !strings.Contains(got, "hook-road evidence") {
				t.Errorf("note %q lost the denial", got)
			}
		})
	}
}

func TestGuardDenialNoteNamesTheDenialAndKeepsTheSentinelLast(t *testing.T) {
	session := testSession(t)
	task := receivedTask(t, session)
	if got := GuardDenialNote(session, task.ID, "done EXIT=0"); got != "done EXIT=0" {
		t.Errorf("no denial: output %q changed", got)
	}
	deny(t, session)
	if got := GuardDenialNote(session, task.ID, "node timeout"); !strings.Contains(got, "hook-road evidence") {
		t.Errorf("a filed, unprocessed denial is missing from %q", got)
	}
	processGuardDenials(session, time.Now().Unix(), (&fakeGuardRoad{deliver: true}).road())

	got := GuardDenialNote(session, task.ID, "done EXIT=0")
	if !strings.Contains(got, "guard-denied") || !strings.Contains(got, "hook-road evidence") || !strings.HasSuffix(got, "EXIT=0") {
		t.Errorf("node output %q: want the denial named and the agent's sentinel last", got)
	}
}

func TestRedriveBlockCarriesTheDenialOnlyForADeniedTask(t *testing.T) {
	session := testSession(t)
	task := receivedTask(t, session)
	m := Message{ID: task.ID, From: "daemon", Type: "request", Action: "build", Payload: task.Payload}
	if strings.Contains(redriveBlock(session, m), "refused by the guard") {
		t.Fatal("an undenied task's re-drive names a denial")
	}
	deny(t, session)
	processGuardDenials(session, time.Now().Unix(), (&fakeGuardRoad{deliver: true}).road())
	if block := redriveBlock(session, m); !strings.Contains(block, "hook-road evidence") || !strings.Contains(block, "To reply:") {
		t.Errorf("re-drive block %q: want the reason before the reply command", block)
	}
}

// Through the graph harvest, with the daemon never having processed the denial:
// an answer and an expiry each record it; an undenied node records none.
func TestExecSendNodeOutputNamesAGuardDenialProcessedOrNot(t *testing.T) {
	for _, tc := range []struct{ name, end string }{{"answered", "answer"}, {"expired", "timeout"}, {"control", "answer"}} {
		t.Run(tc.name, func(t *testing.T) {
			isolateLifecycle(t)
			run := createTestRun(t, &Graph{
				Name:  "t",
				Start: "b",
				Nodes: []Node{{ID: "b", Type: NodeSend, Role: "build", Action: "build", Message: "build it", TimeoutSec: 600}},
			})
			step(t, runTestSession, run.ID)
			st, err := ReadNodeStatus(runTestSession, run.ID, "b")
			if err != nil || st.State != GraphNodeRunning {
				t.Fatalf("fixture: b %+v %v, want running", st, err)
			}
			if tc.name != "control" {
				WriteReceipt(runTestSession, st.TaskID, "build", ReceiptKindAck)
				deny(t, runTestSession)
			}
			if tc.end == "answer" {
				completeSendNode(t, runTestSession, run.ID, "b", OutcomeSuccess)
			} else if err := MutateNodeStatus(runTestSession, run.ID, "b", func(s *GraphNodeStatus) { s.StartedAt -= 601 }); err != nil {
				t.Fatal(err)
			}
			step(t, runTestSession, run.ID)
			st, _ = ReadNodeStatus(runTestSession, run.ID, "b")
			named := strings.Contains(st.Output, "guard-denied") && strings.Contains(st.Output, "hook-road evidence")
			if named != (tc.name != "control") {
				t.Errorf("node output %q: denial named = %v, want %v", st.Output, named, tc.name != "control")
			}
		})
	}
}
