package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Guard denials on an in-flight request (MUX-190).
//
// `hook guard` refusing a command leaves the agent's request consumed and
// receipted; a Codex agent then ends its turn, and before MUX-190 the node
// that dispatched the request waited out its clock with the denial named
// nowhere. The hook now files each denial bound to the requests it
// interrupted (RecordGuardDenial) and the daemon acts on each task once
// (ProcessGuardDenials): one re-drive whose text carries the guard's reason
// (redriveBlock reads the task's denial), or — when the pane is working, the
// re-drive cannot be delivered, or a second denial lands on the same task —
// one alert to edit naming the task, run and node. A graph node's output names
// the denial whatever happens next (GuardDenialNote).
//
// Once a task holds a denial the guard road owns every re-drive of it: the
// stall watchdog, the force-redrive road and the graph executor are refused
// (GuardDenialOwns, checked in RedriveTask and redriveInFlightTasks). So a
// denied task is re-driven at most once after its first denial, by either road
// and in either order, and the state is on disk, so a daemon restart keeps it.
// A re-drive from before the denial is not counted: it predates the refusal
// the agent now needs to be told about.
//
// The denial is filed as a structured record rather than read back from the
// `guard-denied` lifecycle row: that row is cut at 160 runes, which drops the
// reason the agent needs, and the log rotates. Its tasks are bound when it is
// filed, so a task answered or expired before the daemon's next poll is still
// annotated. Per-task state lives beside the task, never in it, so a denial
// cannot race CompleteTask's write.

// guardDenialIdleGraceSecs is how long a deferred denial's pane must read idle,
// continuously from its first idle sighting, before edit is alerted. A pane
// working at the denial may be the agent retrying with the guard's suggested
// form; its answer lands as its turn ends, so the grace separates "retried and
// answered" from "gave up".
const guardDenialIdleGraceSecs = 10

// guardDenialReasonRunes bounds the reason carried into a re-drive, an alert
// or a node output — enough for the guard's instruction, never a wall of text.
const guardDenialReasonRunes = 400

// GuardDenialRecord is one denial as the hook filed it, with the ids of the
// in-flight requests it interrupted.
type GuardDenialRecord struct {
	TS      int64    `json:"ts"`
	Role    string   `json:"role"`
	Tool    string   `json:"tool"`
	Reason  string   `json:"reason"`
	TaskIDs []string `json:"task_ids"`
}

// TaskGuardDenial is a task's denial state: the latest denial, how many there
// were, and which single action the daemon took on them.
type TaskGuardDenial struct {
	TaskID     string `json:"task_id"`
	Role       string `json:"role"`
	Tool       string `json:"tool"`
	Reason     string `json:"reason"`
	DeniedAt   int64  `json:"denied_at"`
	Denials    int    `json:"denials"`
	RedrivenAt int64  `json:"redriven_at,omitempty"`
	DeferredAt int64  `json:"deferred_at,omitempty"`
	IdleSince  int64  `json:"idle_since,omitempty"`
	AlertedAt  int64  `json:"alerted_at,omitempty"`
}

func guardDenialDir(session string) string {
	return filepath.Join(BusDir(session), "guard-denials")
}

func guardDenialPending(session string) string {
	return filepath.Join(guardDenialDir(session), "pending")
}

func guardDenialTasks(session string) string {
	return filepath.Join(guardDenialDir(session), "tasks")
}

func guardDenialTaskPath(session, id string) string {
	return filepath.Join(guardDenialTasks(session), id+".json")
}

// RecordGuardDenial files a denial for the daemon, bound to the requests it
// interrupted: in flight on the denied role's window, sent by now, not expired,
// and — with delivery acknowledgement on — received. A request still waiting
// in the inbox was not being worked on, and ordinary delivery owns it. A denial
// that interrupted nothing files nothing. Errors are returned for tests; the
// hook ignores them, since a denial must never fail on bookkeeping.
func RecordGuardDenial(session, role, tool, reason string) error {
	now := time.Now()
	ids := interruptedTaskIDs(session, role, now.Unix())
	if len(ids) == 0 {
		return nil
	}
	rec := GuardDenialRecord{TS: now.Unix(), Role: role, Tool: tool, Reason: reason, TaskIDs: ids}
	name := fmt.Sprintf("%d-%s.json", now.UnixNano(), role)
	return writeGuardDenialFile(session, filepath.Join(guardDenialPending(session), name), rec)
}

func interruptedTaskIDs(session, role string, now int64) []string {
	tasks, err := ListTasks(session, TaskInFlight)
	if err != nil {
		return nil
	}
	window := WindowForRole(role)
	acks := AckDeliveryActive(session)
	var ids []string
	for _, t := range tasks {
		if WindowForRole(t.To) != window || t.SentAt > now || TaskExpired(t, now) {
			continue
		}
		if _, received := ReadReceipt(session, t.ID); acks && !received {
			continue
		}
		ids = append(ids, t.ID)
	}
	return ids
}

// ReadTaskGuardDenial returns the denial state recorded against a task.
func ReadTaskGuardDenial(session, taskID string) (TaskGuardDenial, bool) {
	if taskID == "" {
		return TaskGuardDenial{}, false
	}
	data, err := os.ReadFile(guardDenialTaskPath(session, taskID))
	if err != nil {
		return TaskGuardDenial{}, false
	}
	var st TaskGuardDenial
	if json.Unmarshal(data, &st) != nil || st.Denials == 0 {
		return TaskGuardDenial{}, false
	}
	return st, true
}

// GuardDenialOwns reports whether a task holds a denial, filed or processed —
// the guard road then owns every re-drive of it.
func GuardDenialOwns(session, taskID string) bool {
	if _, ok := ReadTaskGuardDenial(session, taskID); ok {
		return true
	}
	_, ok := pendingDenialFor(session, taskID)
	return ok
}

// pendingDenialFor returns the latest filed, not yet processed denial bound
// to taskID.
func pendingDenialFor(session, taskID string) (GuardDenialRecord, bool) {
	var found GuardDenialRecord
	ok := false
	for _, path := range pendingGuardDenials(session) {
		rec, err := readGuardDenialRecord(path)
		if err != nil {
			continue
		}
		for _, id := range rec.TaskIDs {
			if id == taskID {
				found, ok = rec, true
			}
		}
	}
	return found, ok
}

// withoutGuardOwned drops the tasks the guard road owns from a re-drive batch.
func withoutGuardOwned(session string, tasks []Task) []Task {
	out := tasks[:0:0]
	for _, t := range tasks {
		if !GuardDenialOwns(session, t.ID) {
			out = append(out, t)
		}
	}
	return out
}

func readGuardDenialRecord(path string) (GuardDenialRecord, error) {
	var rec GuardDenialRecord
	data, err := os.ReadFile(path)
	if err != nil {
		return rec, err
	}
	return rec, json.Unmarshal(data, &rec)
}

// writeGuardDenialFile publishes v at path atomically, staging in the
// guard-denials root so a reader of pending/ or tasks/ never parses a partial
// file.
func writeGuardDenialFile(session, path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(guardDenialDir(session), ".denial-*.tmp")
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
	return os.Rename(tmp.Name(), path)
}

// guardDenialRoad is what ProcessGuardDenials acts through — the pane and bus
// effects, so the decisions can be tested without tmux.
type guardDenialRoad struct {
	working func(session, role string) bool
	redrive func(session string, t Task) bool
	send    func(session string, m Message) error
}

var liveGuardDenialRoad = guardDenialRoad{
	working: AgentIsWorking,
	redrive: redriveTask,
	send:    Send,
}

// ProcessGuardDenials applies the denials the hook filed to the tasks they
// were bound to, then alerts on deferred denials whose pane has read idle for
// the grace with the task unanswered.
func ProcessGuardDenials(session string) {
	processGuardDenials(session, time.Now().Unix(), liveGuardDenialRoad)
}

func processGuardDenials(session string, now int64, road guardDenialRoad) {
	for _, path := range pendingGuardDenials(session) {
		rec, err := readGuardDenialRecord(path)
		if err != nil {
			os.Remove(path)
			continue
		}
		for _, id := range rec.TaskIDs {
			if t, err := ReadTask(session, id); err == nil {
				applyGuardDenial(session, t, rec, now, road)
			}
		}
		os.Remove(path)
	}
	alertDeferredGuardDenials(session, now, road)
}

func pendingGuardDenials(session string) []string {
	entries, err := os.ReadDir(guardDenialPending(session))
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			paths = append(paths, filepath.Join(guardDenialPending(session), e.Name()))
		}
	}
	sort.Strings(paths)
	return paths
}

// applyGuardDenial records rec against t and takes the task's one action. A
// task already answered or expired is annotated only. The first denial on a
// live task re-drives, unless the pane is working (deferred to the idle check)
// or the re-drive cannot be delivered (alert). A later denial never re-drives:
// it alerts, once.
func applyGuardDenial(session string, t Task, rec GuardDenialRecord, now int64, road guardDenialRoad) {
	st, _ := ReadTaskGuardDenial(session, t.ID)
	st.TaskID, st.Role, st.Tool, st.Reason, st.DeniedAt = t.ID, rec.Role, rec.Tool, rec.Reason, rec.TS
	st.Denials++
	defer func() { _ = writeGuardDenialFile(session, guardDenialTaskPath(session, t.ID), st) }()

	role := WindowForRole(t.To)
	switch {
	case t.Status != TaskInFlight || TaskExpired(t, now):
		LogLifecycle(session, "info", "daemon", "guard-denied-annotated",
			fmt.Sprintf("%s: task %s%s %s before the denial was processed — recorded, nothing re-driven", role, t.ID, graphLabel(t), t.Status))
	case st.Denials > 1:
		if st.AlertedAt == 0 {
			alertGuardDenial(session, t, &st, now, road, fmt.Sprintf("denied again (%d denials on this task) — not re-driven a second time", st.Denials))
		}
	case road.working(session, role):
		st.DeferredAt = now
		LogLifecycle(session, "info", "daemon", "guard-denied-redrive-deferred",
			fmt.Sprintf("%s: task %s — pane working, re-drive withheld", role, t.ID))
	default:
		// Written before the re-drive so redriveBlock finds the reason it carries.
		_ = writeGuardDenialFile(session, guardDenialTaskPath(session, t.ID), st)
		if !road.redrive(session, t) {
			alertGuardDenial(session, t, &st, now, road, "the re-drive could not be delivered")
			return
		}
		st.RedrivenAt = now
		LogLifecycle(session, "info", "daemon", "guard-denied-redrive",
			fmt.Sprintf("%s: task %s%s re-driven with the denial — %s", role, t.ID, graphLabel(t), guardClip(st.Reason, 120)))
	}
}

// alertDeferredGuardDenials alerts on each denial withheld from a working pane
// once that pane has read idle continuously for the grace — a working sighting
// restarts it — with the task unanswered. It also drops state for tasks whose
// record is gone.
func alertDeferredGuardDenials(session string, now int64, road guardDenialRoad) {
	entries, err := os.ReadDir(guardDenialTasks(session))
	if err != nil {
		return
	}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if id == e.Name() {
			continue
		}
		t, err := ReadTask(session, id)
		if err != nil {
			os.Remove(guardDenialTaskPath(session, id))
			continue
		}
		st, ok := ReadTaskGuardDenial(session, id)
		if !ok || st.DeferredAt == 0 || st.AlertedAt > 0 || st.RedrivenAt > 0 || t.Status != TaskInFlight || TaskExpired(t, now) {
			continue
		}
		switch {
		case road.working(session, WindowForRole(t.To)):
			if st.IdleSince == 0 {
				continue
			}
			st.IdleSince = 0
		case st.IdleSince == 0:
			st.IdleSince = now
		case now-st.IdleSince >= guardDenialIdleGraceSecs:
			alertGuardDenial(session, t, &st, now, road, "the pane was working at the denial and went idle without an answer")
		default:
			continue
		}
		_ = writeGuardDenialFile(session, guardDenialTaskPath(session, id), st)
	}
}

func alertGuardDenial(session string, t Task, st *TaskGuardDenial, now int64, road guardDenialRoad, why string) {
	st.AlertedAt = now
	text := fmt.Sprintf("Guard denied %s's %s on task %s%s: %s — %s; nothing was typed into its pane, and the request stays unanswered until the agent is told what to change.",
		st.Role, st.Tool, t.ID, graphLabel(t), guardClip(st.Reason, 200), why)
	if err := road.send(session, NewMessage("daemon", "edit", "event", "guard-denied", text, "")); err != nil {
		LogLifecycle(session, "warn", "daemon", "guard-denied-alert",
			fmt.Sprintf("%s: task %s%s — alert not sent: %v", st.Role, t.ID, graphLabel(t), err))
		return
	}
	LogLifecycle(session, "warn", "daemon", "guard-denied-alert",
		fmt.Sprintf("%s: task %s%s — %s", st.Role, t.ID, graphLabel(t), why))
}

func graphLabel(t Task) string {
	if t.GraphRun == "" {
		return ""
	}
	return fmt.Sprintf(" (run %s · node %s)", t.GraphRun, t.GraphNode)
}

// guardDenialRedriveText is the sentence a re-drive adds for a denied request,
// or "" when the task holds no denial.
func guardDenialRedriveText(session, taskID string) string {
	st, ok := ReadTaskGuardDenial(session, taskID)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" — your last command on this request was refused by the guard: %s — change it as the guard says, then finish the request", guardClip(st.Reason, guardDenialReasonRunes))
}

// GuardDenialNote prefixes a send node's output with the denial its task
// received, processed or still filed, so the node's record names it whether
// or not the re-drive rescued the task. A prefix keeps the agent's own last
// EXIT sentinel last.
func GuardDenialNote(session, taskID, output string) string {
	role, tool, reason := "", "", ""
	if st, ok := ReadTaskGuardDenial(session, taskID); ok {
		role, tool, reason = st.Role, st.Tool, st.Reason
	} else if rec, ok := pendingDenialFor(session, taskID); ok {
		role, tool, reason = rec.Role, rec.Tool, rec.Reason
	} else {
		return output
	}
	note := fmt.Sprintf("[guard-denied %s %s: %s]", role, tool, guardClip(reason, 240))
	if output == "" {
		return note
	}
	return note + "\n" + output
}

// guardClip flattens s to one line and cuts it at n runes.
func guardClip(s string, n int) string {
	s = redriveNewlines.Replace(strings.TrimSpace(s))
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
