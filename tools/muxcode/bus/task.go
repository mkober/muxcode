package bus

import (
	"encoding/json"
	"fmt"
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

// CreateTask writes a new in-flight task entry.
// Called by --wait in cmd/send.go after the message is sent.
func CreateTask(session string, m Message, timeout int) error {
	t := Task{
		ID:      m.ID,
		From:    m.From,
		To:      m.To,
		Action:  m.Action,
		Payload: m.Payload,
		Status:  TaskInFlight,
		SentAt:  m.TS,
		Timeout: timeout,
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

// TimeoutTask marks a task as timed-out.
func TimeoutTask(session, taskID string) {
	t, err := ReadTask(session, taskID)
	if err != nil {
		return
	}
	if t.Status != TaskInFlight {
		return
	}
	t.Status = TaskTimedOut
	_ = writeTask(session, t)
}

// ClearInFlightTasksForRole times out every in-flight task addressed to role (or
// a hosted role it fronts). Called when an agent (re)launches: a fresh agent
// instance cannot be mid-processing a task that was delivered to a previous
// instance, so any task still marked in-flight is stale by definition.
//
// Left stranded, such a task blocks EVERY new send of the same (to, action) via
// HasInFlightTaskForRole until the 600s TaskExpired grace elapses — the exact
// wedge that made a crashed-and-restarted review agent silently undeliverable:
// the crash left its chain task in-flight, and the dedup guard then dropped each
// re-sent review request before it reached the inbox. Clearing on launch ties
// task liveness to agent liveness, so a restart (crash recovery, reload, manual
// relaunch) unblocks delivery immediately instead of after a 10-minute stall.
//
// Matching is by WindowForRole so a relaunching host clears its hosted roles'
// tasks too (e.g. plan clears docs, commit clears pr-read). Returns the count.
func ClearInFlightTasksForRole(session, role string) int {
	tasks, err := ListTasks(session, TaskInFlight)
	if err != nil {
		return 0
	}
	host := WindowForRole(role)
	cleared := 0
	for _, t := range tasks {
		if WindowForRole(t.To) == host {
			TimeoutTask(session, t.ID)
			cleared++
		}
	}
	return cleared
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
// Pass empty filterStatus to return all tasks.
func ListTasks(session, filterStatus string) ([]Task, error) {
	dir := TaskDir(session)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var tasks []Task
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			continue
		}
		if filterStatus != "" && t.Status != filterStatus {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
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
	return os.WriteFile(TaskPath(session, t.ID), data, 0644)
}
