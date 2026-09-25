package bus

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// chromeCompletedSendNode drives linearGraph's node a to a task completed by a
// provider-chrome payload — the shape that closed spec-to-pr nodes on
// 2026-09-08 (20:31:51 rule line) and the 14:12:55 working-line incident.
//
// The chrome response is appended to the session log directly, bypassing Send:
// Send drops chrome (dropsAsProviderChrome), and the executor guard under test
// exists for exactly the chrome that slips past that layer and the daemon's.
// The role's history is removed so no stray row decides the outcome.
func chromeCompletedSendNode(t *testing.T, chrome string) (*GraphRun, string) {
	t.Helper()
	run := createTestRun(t, linearGraph())
	step(t, runTestSession, run.ID)
	st, err := ReadNodeStatus(runTestSession, run.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(HistoryPath(runTestSession, "build")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	completeWithChrome(t, NewMessage("build", "edit", "response", "response", chrome, st.TaskID))
	return run, st.TaskID
}

// completeWithChrome logs a chrome response without Send and completes its task
// with it — see chromeCompletedSendNode for why Send cannot be used.
func completeWithChrome(t *testing.T, resp Message) {
	t.Helper()
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendToFile(LogPath(runTestSession), append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	CompleteTask(runTestSession, resp.ReplyTo, resp.ID)
}

func replyToTask(t *testing.T, replyTo, payload string) Message {
	t.Helper()
	m := NewMessage("build", "edit", "response", "response", payload, replyTo)
	if err := Send(runTestSession, m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestExecSendNodeNonResultWaitsForGenuineReply pins MUX-154 AC 8 where the
// executor runs: a chrome-completed node neither routes nor raises a gate, a
// reply to some other request cannot answer it, and a genuine reply to its own
// task routes success with no hold. Before acceptReply that reply was
// suppressed as a duplicate, so the node's only exit was its expiry.
func TestExecSendNodeNonResultWaitsForGenuineReply(t *testing.T) {
	for _, chrome := range []struct{ name, payload string }{
		{"rule line (20:31:51)", ruleLine158()},
		{"working line (14:12:55)", "• Working (13s • esc to interrupt)"},
	} {
		t.Run(chrome.name, func(t *testing.T) {
			run, taskID := chromeCompletedSendNode(t, chrome.payload)

			step(t, runTestSession, run.ID)
			st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
			if st.State != GraphNodeRunning || st.Outcome != "" {
				t.Fatalf("a = %s/%q after chrome, want running with no outcome", st.State, st.Outcome)
			}
			if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodePending {
				t.Fatalf("b = %s, want pending — chrome must not route", s)
			}
			if gates := gateRequestPayloads(t, run.ID); len(gates) != 0 {
				t.Fatalf("chrome raised %d approval gate(s), want none: %v", len(gates), gates)
			}

			replyToTask(t, "1-edit-unrelated", "some other build. EXIT=0")
			step(t, runTestSession, run.ID)
			if s := nodeState(t, runTestSession, run.ID, "a"); s != GraphNodeRunning {
				t.Fatalf("a = %s after an unrelated reply, want running", s)
			}

			replyToTask(t, taskID, "built both modules. EXIT=0")
			step(t, runTestSession, run.ID)
			st, _ = ReadNodeStatus(runTestSession, run.ID, "a")
			if st.State != GraphNodeDone || st.Outcome != OutcomeSuccess {
				t.Fatalf("a = %s/%q after genuine EXIT=0 reply, want done/success", st.State, st.Outcome)
			}
			if st.Output != "built both modules. EXIT=0" {
				t.Errorf("a output = %q, want the genuine reply, not the chrome", st.Output)
			}
			if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
				t.Errorf("b = %s, want running — success must route", s)
			}
			if gates := gateRequestPayloads(t, run.ID); len(gates) != 0 {
				t.Errorf("a sentinel-bearing reply raised %d gate(s), want none", len(gates))
			}
		})
	}
}

// TestExecSendNodeNonResultGenuineReplyWithoutSentinelHolds is the negative
// control for "no hold": the claimed reply goes through the ordinary outcome
// derivation, so one with no verdict still holds for a human.
func TestExecSendNodeNonResultGenuineReplyWithoutSentinelHolds(t *testing.T) {
	run, taskID := chromeCompletedSendNode(t, ruleLine158())
	step(t, runTestSession, run.ID)

	replyToTask(t, taskID, "had a look at the build")
	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.Outcome != OutcomeUnknown {
		t.Errorf("a outcome = %q, want unknown — a reply with no sentinel is no verdict", st.Outcome)
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodePending {
		t.Errorf("b = %s, want pending", s)
	}
	if len(gateRequestPayloads(t, run.ID)) == 0 {
		t.Error("a held node must raise its gate")
	}
}

// TestExecSendNodeNonResultFirstGenuineReplyWins pins the claim at acceptance:
// two contradicting replies landing before the next tick must not let the
// newer one overwrite the first. Claiming at the tick would record the EXIT=0
// here and route a failed build as success.
func TestExecSendNodeNonResultFirstGenuineReplyWins(t *testing.T) {
	run, taskID := chromeCompletedSendNode(t, ruleLine158())
	first := replyToTask(t, taskID, "build broke. EXIT=1")
	second := replyToTask(t, taskID, "fixed it. EXIT=0")

	if got := mustReadTask(t, taskID).ResponseID; got != first.ID {
		t.Fatalf("task response = %s, want the first genuine reply %s", got, first.ID)
	}
	if _, ok := FindMessageByID(runTestSession, second.ID); ok {
		t.Error("the second reply was recorded — it must be suppressed as a duplicate")
	}

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.State != GraphNodeFailed || st.Outcome != OutcomeFailure {
		t.Errorf("a = %s/%q, want failed/failure from the first reply", st.State, st.Outcome)
	}
}

// chromeCompletedTask creates a task no graph owns — requester to target — and
// completes it with chrome.
func chromeCompletedTask(t *testing.T, requester, target string) Task {
	t.Helper()
	useTempBusDir(t)
	if err := os.MkdirAll(BusDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	req := NewMessage(requester, target, "request", "build", "go", "")
	if err := CreateTask(runTestSession, req, 0); err != nil {
		t.Fatal(err)
	}
	completeWithChrome(t, NewMessage(target, requester, "response", "response", ruleLine158(), req.ID))
	return mustReadTask(t, req.ID)
}

func inboxHolds(t *testing.T, role, id string) bool {
	t.Helper()
	msgs, _ := Peek(runTestSession, role)
	for _, m := range msgs {
		if m.ID == id {
			return true
		}
	}
	return false
}

// TestClaimReplyOutsideTheGraph covers what the executor tests cannot: a task no
// graph owns, answered on both reply paths. The self-addressed path is edit
// answering a daemon dispatch — the CLI normalizes daemon to edit, so the reply
// arrives edit→edit and is recorded without delivery. Each case ends on the
// negative control: once answered, the guard holds again.
func TestClaimReplyOutsideTheGraph(t *testing.T) {
	cases := []struct {
		name, requester, target string
		delivered               bool
	}{
		{"delivered reply", "edit", "build", true},
		{"self-addressed reply", "edit", "edit", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := chromeCompletedTask(t, tc.requester, tc.target)

			genuine := NewMessage(tc.target, tc.requester, "response", "response", "built. EXIT=0", task.ID)
			if err := Send(runTestSession, genuine); err != nil {
				t.Fatal(err)
			}
			if got := mustReadTask(t, task.ID).ResponseID; got != genuine.ID {
				t.Fatalf("task response = %s, want the genuine reply %s", got, genuine.ID)
			}
			if got := inboxHolds(t, tc.requester, genuine.ID); got != tc.delivered {
				t.Errorf("reply in %s inbox = %v, want %v", tc.requester, got, tc.delivered)
			}

			late := NewMessage(tc.target, tc.requester, "response", "response", "built again. EXIT=0", task.ID)
			if err := Send(runTestSession, late); err != nil {
				t.Fatal(err)
			}
			if got := mustReadTask(t, task.ID).ResponseID; got != genuine.ID {
				t.Errorf("a late reply overwrote the answer: response = %s", got)
			}
			if _, ok := FindMessageByID(runTestSession, late.ID); ok {
				t.Error("a late reply to an answered task was recorded")
			}
		})
	}
}

// TestClaimReplyRefusesStrangers pins who may claim: only a response from the
// task's own target. The genuine reply that follows is the negative control —
// without it, a guard that suppressed everything would pass.
func TestClaimReplyRefusesStrangers(t *testing.T) {
	task := chromeCompletedTask(t, "edit", "build")

	strangers := []Message{
		NewMessage("test", "edit", "response", "response", "built. EXIT=0", task.ID),
		NewMessage("build", "edit", "event", "notify", "built. EXIT=0", task.ID),
	}
	for _, m := range strangers {
		if err := Send(runTestSession, m); err != nil {
			t.Fatal(err)
		}
		if got := mustReadTask(t, task.ID).ResponseID; got != task.ResponseID {
			t.Fatalf("%s %s from %s claimed the task", m.Type, m.Action, m.From)
		}
		if _, ok := FindMessageByID(runTestSession, m.ID); ok {
			t.Errorf("%s from %s was recorded against an answered-by-chrome task", m.Type, m.From)
		}
	}

	genuine := replyToTask(t, task.ID, "built. EXIT=0")
	if got := mustReadTask(t, task.ID).ResponseID; got != genuine.ID {
		t.Errorf("the task's own target was refused: response = %s", got)
	}
}

// TestClaimReplyFailsClosedOnUnreadableTask pins the other read road: only a
// task that does not exist is "not a tracked reply". An unreadable one could be
// answered, so the reply is refused rather than delivered unguarded. A reply
// naming no task at all is the negative control and must still deliver.
func TestClaimReplyFailsClosedOnUnreadableTask(t *testing.T) {
	task := chromeCompletedTask(t, "edit", "build")
	if err := os.WriteFile(TaskPath(runTestSession, task.ID), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewMessage("build", "edit", "response", "response", "built. EXIT=0", task.ID)
	if err := Send(runTestSession, m); err == nil {
		t.Error("a reply to an unreadable task was accepted")
	}
	if _, ok := FindMessageByID(runTestSession, m.ID); ok {
		t.Error("a reply to an unreadable task was recorded")
	}

	untracked := replyToTask(t, "1-edit-untracked", "built. EXIT=0")
	if !inboxHolds(t, "edit", untracked.ID) {
		t.Error("a reply naming no task was not delivered")
	}
}

// TestClaimReplyFailedWriteLeavesTaskUnclaimed pins the order: the reply is
// stored before the task names it. A failed inbox write must return an error
// and leave the task on its chrome, so the retry can still claim it.
func TestClaimReplyFailedWriteLeavesTaskUnclaimed(t *testing.T) {
	task := chromeCompletedTask(t, "edit", "build")
	blocker := InboxPath(runTestSession, "edit")
	if err := os.MkdirAll(blocker, 0755); err != nil {
		t.Fatal(err)
	}

	failed := NewMessage("build", "edit", "response", "response", "built. EXIT=0", task.ID)
	if err := Send(runTestSession, failed); err == nil {
		t.Fatal("Send reported success with an unwritable inbox")
	}
	if got := mustReadTask(t, task.ID).ResponseID; got != task.ResponseID {
		t.Fatalf("a failed write claimed the task: response = %s", got)
	}

	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	retry := replyToTask(t, task.ID, "built. EXIT=0")
	if got := mustReadTask(t, task.ID).ResponseID; got != retry.ID {
		t.Errorf("the retry could not claim the task: response = %s", got)
	}
}

func mustReadTask(t *testing.T, id string) Task {
	t.Helper()
	task, err := ReadTask(runTestSession, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// TestExecSendNodeNonResultExpires pins the other exit: a chrome-completed node
// with no TimeoutSec fails on its task's expiry instead of running forever.
// The un-aged run in the tests above is the negative control.
func TestExecSendNodeNonResultExpires(t *testing.T) {
	run, taskID := chromeCompletedSendNode(t, ruleLine158())
	task, err := ReadTask(runTestSession, taskID)
	if err != nil {
		t.Fatal(err)
	}
	task.SentAt = time.Now().Unix() - int64(taskTimeoutSecs(task)) - 1
	if err := writeTask(runTestSession, task); err != nil {
		t.Fatal(err)
	}

	step(t, runTestSession, run.ID)

	st, _ := ReadNodeStatus(runTestSession, run.ID, "a")
	if st.State != GraphNodeFailed || st.Output != "task expired" {
		t.Errorf("a = %s/%q, want failed/\"task expired\"", st.State, st.Output)
	}
}
