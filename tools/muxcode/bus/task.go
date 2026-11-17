package bus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Task represents a delegated task tracked by the orchestrator.
// Created automatically when --wait is used on muxcode send.
type Task struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Action     string `json:"action"`
	Payload    string `json:"payload"`
	Status     string `json:"status"` // in-flight, completed, timed-out, failed
	SentAt     int64  `json:"sent_at"`
	Timeout    int    `json:"timeout"`
	ResponseID string `json:"response_id"`
	ResponseAt int64  `json:"response_at"`
	ClearedBy  string `json:"cleared_by,omitempty"` // the relaunch that timed it out (ClearInFlightTasksForRole)
	GraphRun   string `json:"graph_run,omitempty"`  // the graph run and node whose dispatch this is (MUX-170)
	GraphNode  string `json:"graph_node,omitempty"`
}

// ownedByNode reports whether t is the dispatch of run's node — the only
// in-flight task that node may adopt as its own (MUX-170). A task carrying
// neither field, any non-graph send or one written before they existed,
// belongs to no node.
func (t Task) ownedByNode(run, node string) bool {
	return t.GraphRun != "" && t.GraphRun == run && t.GraphNode == node
}

// Task status constants.
const (
	TaskInFlight  = "in-flight"
	TaskCompleted = "completed"
	TaskTimedOut  = "timed-out"
	TaskFailed    = "failed"
)

// TaskDir returns the tasks directory path for a session.
func TaskDir(session string) string {
	return filepath.Join(BusDir(session), "tasks")
}

// TaskPath returns the task file path for a message ID.
func TaskPath(session, msgID string) string {
	return filepath.Join(TaskDir(session), msgID+".json")
}

// CreateTask writes a new in-flight task entry, carrying m's graph run and
// node so a graph dispatch's task records which node owns it.
// Called by --wait in cmd/send.go after the message is sent.
func CreateTask(session string, m Message, timeout int) error {
	t := Task{
		ID:        m.ID,
		From:      m.From,
		To:        m.To,
		Action:    m.Action,
		Payload:   m.Payload,
		Status:    TaskInFlight,
		SentAt:    m.TS,
		Timeout:   timeout,
		GraphRun:  m.GraphRun,
		GraphNode: m.GraphNode,
	}
	return writeTask(session, t)
}

// CompleteTask marks a task as completed with the response message ID.
func CompleteTask(session, taskID, responseID string) {
	t, err := ReadTask(session, taskID)
	if err != nil {
		return
	}
	t.Status = TaskCompleted
	t.ResponseID = responseID
	t.ResponseAt = time.Now().Unix()
	_ = writeTask(session, t)
}

// TimeoutTask marks a task as timed-out, best effort. A caller that must
// know the task left in-flight uses expireTask.
func TimeoutTask(session, taskID string) {
	_ = expireTask(session, taskID)
}

// expireTask marks an in-flight task timed-out and returns why it could
// not. A task that does not exist, or has already left in-flight, is not
// an error.
func expireTask(session, taskID string) error {
	return expireTaskBecause(session, taskID, "")
}

// expireTaskBecause is expireTask recording cause as the task's ClearedBy.
func expireTaskBecause(session, taskID, cause string) error {
	t, err := ReadTask(session, taskID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.Status != TaskInFlight {
		return nil
	}
	t.Status = TaskTimedOut
	t.ClearedBy = cause
	return writeTask(session, t)
}

// ClearInFlightTasksForRole times out the in-flight tasks addressed to role (or
// a hosted role it fronts) whose request a previous instance consumed. Called
// when an agent (re)launches: a fresh instance cannot be mid-processing a task
// delivered to a previous one, so such a task is stale.
//
// A task whose request carries no receipt and still sits in the role's inbox
// is kept on its own clock: the relaunched agent will read and answer it. On
// 2026-09-28 a reload cleared such a dispatch 250 s into a 3600 s ci-watch
// node and failed the run seconds before the answer (MUX-196). The receipt is
// the addressee's alone (MUX-216), so an auto-CC copy edit read proves
// nothing. Every task considered writes one row — inflight-task-kept or
// inflight-task-cleared — naming it and whether its request was consumed.
//
// Absence from the inbox is proven, never assumed. The inbox is read under
// its capacity lock, which the partial consumes hold while they rename the
// whole inbox away and restore the rows they did not take; and a
// `.consuming` file — the unlocked full drain mid-read — counts as queued. A
// lock or read failure proves nothing, so it keeps every receipt-less task.
//
// Left stranded, a consumed task blocks EVERY new send of the same (to, action) via
// HasInFlightTaskForRole until the 600s TaskExpired grace elapses — the exact
// wedge that made a crashed-and-restarted review agent silently undeliverable:
// the crash left its chain task in-flight, and the dedup guard then dropped each
// re-sent review request before it reached the inbox. Clearing on launch ties
// task liveness to agent liveness, so a restart (crash recovery, reload, manual
// relaunch) unblocks delivery immediately instead of after a 10-minute stall.
//
// Matching is by WindowForRole so a relaunching host clears its hosted roles'
// tasks too (e.g. plan clears docs, commit clears pr-read). Each cleared task
// records cause as its ClearedBy, so a graph node failed on it names the
// relaunch rather than a bare "task timed-out" seconds into its allowance
// (MUX-217). Returns the count.
func ClearInFlightTasksForRole(session, role, cause string) int {
	tasks, err := ListTasks(session, TaskInFlight)
	if err != nil {
		return 0
	}
	host := WindowForRole(role)
	var mine []Task
	for _, t := range tasks {
		if WindowForRole(t.To) == host {
			mine = append(mine, t)
		}
	}
	if len(mine) == 0 {
		return 0
	}
	cleared := 0
	settle := func(queued map[string]bool, unknown error) {
		for _, t := range mine {
			if clearUnlessQueued(session, role, cause, t, queued, unknown) {
				cleared++
			}
		}
	}
	if err := withInboxCapLock(session, host, func() error {
		settle(queuedIDs(session, host))
		return nil
	}); err != nil {
		settle(nil, err)
	}
	return cleared
}

// clearUnlessQueued times out t unless its request is unconsumed and queued —
// or unconsumed while the inbox could not be read (unknown), which proves
// nothing — writing the row that names the decision, and reports whether it
// cleared t.
func clearUnlessQueued(session, role, cause string, t Task, queued map[string]bool, unknown error) bool {
	what := fmt.Sprintf("%s: task %s %s→%s:%s", role, t.ID, t.From, t.To, t.Action)
	_, consumed := ReadReceipt(session, t.ID)
	if !consumed && unknown != nil {
		LogLifecycle(session, "warn", "launch", "inflight-task-kept",
			fmt.Sprintf("%s kept — consumed=no, and the inbox could not be read (%v), which is no proof the request is gone", what, unknown))
		return false
	}
	if !consumed && queued[t.ID] {
		LogLifecycle(session, "info", "launch", "inflight-task-kept",
			what+" kept — consumed=no, its request waits in the inbox for the relaunched agent")
		return false
	}
	_ = expireTaskBecause(session, t.ID, cause)
	detail := "consumed=yes"
	if !consumed {
		detail = "consumed=no, its request is gone from the inbox"
	}
	LogLifecycle(session, "info", "launch", "inflight-task-cleared", what+" cleared — "+detail)
	return true
}

// queuedIDs returns the ids of the messages queued for role: its inbox, and a
// `.consuming` file a full drain (Receive) has renamed it to but not yet read.
func queuedIDs(session, role string) (map[string]bool, error) {
	ids := map[string]bool{}
	inbox := InboxPath(session, role)
	for _, path := range []string{inbox, inbox + ".consuming"} {
		msgs, err := readMessages(path)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			ids[m.ID] = true
		}
	}
	return ids, nil
}

// ReadTask reads a task by its message ID.
func ReadTask(session, msgID string) (Task, error) {
	data, err := os.ReadFile(TaskPath(session, msgID))
	if err != nil {
		return Task{}, err
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return Task{}, err
	}
	return t, nil
}

// ListTasks returns all tasks for a session, optionally filtered by status.
// Pass empty filterStatus to return all tasks. Unreadable or invalid task
// files are skipped; scanTasks reports them.
func ListTasks(session, filterStatus string) ([]Task, error) {
	tasks, _, err := scanTasks(session, filterStatus)
	return tasks, err
}

// scanTasks lists tasks like ListTasks and also returns one error per task
// file it could not read or parse, so a caller that must not miss a task
// can refuse a partial listing. err is set only when the directory itself
// cannot be read; a missing directory is no tasks.
func scanTasks(session, filterStatus string) (tasks []Task, unreadable []error, err error) {
	dir := TaskDir(session)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			unreadable = append(unreadable, fmt.Errorf("task file %s: %w", e.Name(), err))
			continue
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			unreadable = append(unreadable, fmt.Errorf("task file %s: %w", e.Name(), err))
			continue
		}
		if filterStatus != "" && t.Status != filterStatus {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, unreadable, nil
}

// CleanExpiredTasks removes task files older than maxAge.
// Uses SentAt from the JSON payload for age checks.
func CleanExpiredTasks(session string, maxAge time.Duration) int {
	dir := TaskDir(session)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	cutoff := time.Now().Add(-maxAge).Unix()
	cleaned := 0
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			if os.Remove(path) == nil {
				cleaned++
			}
			continue
		}
		if t.SentAt < cutoff {
			if os.Remove(path) == nil {
				cleaned++
			}
		}
	}
	return cleaned
}

// taskTimeoutSecs returns a task's effective timeout in seconds, defaulting to
// 600 when unset/invalid.
func taskTimeoutSecs(t Task) int {
	if t.Timeout > 0 {
		return t.Timeout
	}
	return 600
}

// TaskExpired reports whether an in-flight task has passed its timeout window
// (SentAt + Timeout). A stuck task — one that was delivered but whose target
// never responded (e.g. the agent was busy at delivery and went idle without
// acting) — would otherwise remain "in-flight" forever and permanently block
// new requests via the dedup suppression in Send(). Treating expired tasks as
// no-longer-in-flight lets fresh requests through and lets the daemon time
// them out.
func TaskExpired(t Task, now int64) bool {
	return now-t.SentAt > int64(taskTimeoutSecs(t))
}

// FormatTask returns a human-readable string for a task.
func FormatTask(t Task) string {
	s := fmt.Sprintf("%s  %s\u2192%s  %s  [%s]", t.ID, t.From, t.To, t.Action, t.Status)
	if t.Payload != "" {
		payload := t.Payload
		if len(payload) > 60 {
			payload = payload[:57] + "..."
		}
		s += "  " + payload
	}
	if t.ResponseID != "" {
		s += fmt.Sprintf("  response=%s", t.ResponseID)
	}
	return s
}

func writeTask(session string, t Task) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	// Session init normally creates the tasks dir, but callers that write
	// tasks outside a fully initialized session (graph executor dispatch,
	// tests) must not lose the record silently — a missing task file later
	// reads as a lost/failed task.
	if err := os.MkdirAll(TaskDir(session), 0755); err != nil {
		return err
	}
	return publishTaskFile(session, TaskPath(session, t.ID), data)
}

// publishTaskFile replaces a task file atomically: readers see the old record
// or the new one, never a truncated one. An in-place write let a concurrent
// reader take an empty file as "no answer yet" and admit a second reply, or
// fail a graph harvest as "task record lost". The temporary file is staged in
// the bus directory, not the tasks directory, where scanTasks would parse it as
// a task; both sit in the same bus tree, so the rename stays atomic.
func publishTaskFile(session, path string, data []byte) error {
	tmp, err := os.CreateTemp(BusDir(session), ".task-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
