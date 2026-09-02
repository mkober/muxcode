package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// WorkflowState represents the current state of the editing workflow.
// States are ordered integers for regression comparison.
type WorkflowState int

const (
	StateIdle       WorkflowState = 0
	StateEditing    WorkflowState = 1
	StateAnalyzing  WorkflowState = 2
	StateBuilding   WorkflowState = 3
	StateBuildFail  WorkflowState = 4
	StateTesting    WorkflowState = 5
	StateTestFail   WorkflowState = 6
	StateReviewing  WorkflowState = 7
	StateReviewed   WorkflowState = 8
	StateCommitting WorkflowState = 9
	StateDeploying  WorkflowState = 10
	StateDeployFail WorkflowState = 11
	StateRunning    WorkflowState = 12
	StateRunFail    WorkflowState = 13
	StateWatching   WorkflowState = 14
	StateWatchFail  WorkflowState = 15
)

// stateNames maps workflow states to their string names.
var stateNames = map[WorkflowState]string{
	StateIdle:       "idle",
	StateEditing:    "editing",
	StateAnalyzing:  "analyzing",
	StateBuilding:   "building",
	StateBuildFail:  "build-failed",
	StateTesting:    "testing",
	StateTestFail:   "test-failed",
	StateReviewing:  "reviewing",
	StateReviewed:   "reviewed",
	StateCommitting: "committing",
	StateDeploying:  "deploying",
	StateDeployFail: "deploy-failed",
	StateRunning:    "running",
	StateRunFail:    "run-failed",
	StateWatching:   "watching",
	StateWatchFail:  "watch-failed",
}

// stateFromName maps string names back to workflow states.
var stateFromName map[string]WorkflowState

func init() {
	stateFromName = make(map[string]WorkflowState, len(stateNames))
	for k, v := range stateNames {
		stateFromName[v] = k
	}
}

// String returns the state name.
func (s WorkflowState) String() string {
	if name, ok := stateNames[s]; ok {
		return name
	}
	return "unknown"
}

// MarshalJSON encodes the state as a JSON string.
func (s WorkflowState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON decodes a JSON string into a WorkflowState.
func (s *WorkflowState) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	if state, ok := stateFromName[name]; ok {
		*s = state
		return nil
	}
	return fmt.Errorf("unknown workflow state: %q", name)
}

// WorkflowStateEntry is the persisted state.
type WorkflowStateEntry struct {
	State         WorkflowState `json:"state"`
	PrevState     WorkflowState `json:"prev_state"`
	Since         int64         `json:"since"`
	Updated       int64         `json:"updated"`
	Trigger       string        `json:"trigger"`
	FilesChanged  int           `json:"files_changed"`
	LastFiles     []string      `json:"last_files"`
	BuildOutcome  string        `json:"build_outcome"`
	TestOutcome   string        `json:"test_outcome"`
	ReviewOutcome string        `json:"review_outcome"`
	DeployOutcome string        `json:"deploy_outcome"`
	RunOutcome    string        `json:"run_outcome"`
	WatchOutcome  string        `json:"watch_outcome"`
}

// WorkflowStatePath returns the state file path.
func WorkflowStatePath(session string) string {
	return filepath.Join(BusDir(session), "workflow-state.json")
}

// workflowLockPath returns the lock file path for workflow state.
func workflowLockPath(session string) string {
	return filepath.Join(BusDir(session), "lock", "workflow.lock")
}

// ReadWorkflowState reads current state from disk. Returns StateIdle if missing.
func ReadWorkflowState(session string) WorkflowStateEntry {
	data, err := os.ReadFile(WorkflowStatePath(session))
	if err != nil {
		return WorkflowStateEntry{State: StateIdle}
	}
	var entry WorkflowStateEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return WorkflowStateEntry{State: StateIdle}
	}
	return entry
}

// TransitionOpt is a functional option for TransitionWorkflow.
type TransitionOpt func(*WorkflowStateEntry)

// WithFiles sets the files changed in the transition.
func WithFiles(files []string) TransitionOpt {
	return func(e *WorkflowStateEntry) {
		e.FilesChanged = len(files)
		// Keep at most 5 files for display
		if len(files) > 5 {
			e.LastFiles = files[:5]
		} else {
			e.LastFiles = files
		}
	}
}

// WithOutcome sets an outcome field for the given phase.
func WithOutcome(phase string, outcome string) TransitionOpt {
	return func(e *WorkflowStateEntry) {
		switch phase {
		case "build":
			e.BuildOutcome = outcome
		case "test":
			e.TestOutcome = outcome
		case "review":
			e.ReviewOutcome = outcome
		case "deploy":
			e.DeployOutcome = outcome
		case "run":
			e.RunOutcome = outcome
		case "watch":
			e.WatchOutcome = outcome
		}
	}
}

// TransitionWorkflow atomically transitions state under flock.
// Returns true if the state actually changed.
func TransitionWorkflow(session string, newState WorkflowState, trigger string, opts ...TransitionOpt) bool {
	lockPath := workflowLockPath(session)
	_ = os.MkdirAll(filepath.Dir(lockPath), 0755)

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return false
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return false
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	entry := ReadWorkflowState(session)
	now := time.Now().Unix()

	if entry.State == newState {
		// Same state — update timestamp and apply opts but don't change since
		entry.Updated = now
		entry.Trigger = trigger
		for _, opt := range opts {
			opt(&entry)
		}
		writeWorkflowState(session, entry)
		return false
	}

	// Regression rule: transitioning to editing from >= analyzing clears outcomes
	if newState == StateEditing && entry.State >= StateAnalyzing {
		entry.BuildOutcome = ""
		entry.TestOutcome = ""
		entry.ReviewOutcome = ""
		entry.DeployOutcome = ""
		entry.RunOutcome = ""
		entry.WatchOutcome = ""
	}

	entry.PrevState = entry.State
	entry.State = newState
	entry.Since = now
	entry.Updated = now
	entry.Trigger = trigger

	for _, opt := range opts {
		opt(&entry)
	}

	writeWorkflowState(session, entry)

	LogLifecycle(session, "info", "workflow", newState.String(),
		fmt.Sprintf("from=%s trigger=%s", entry.PrevState, trigger))

	return true
}

// writeWorkflowState writes the state entry to disk.
func writeWorkflowState(session string, entry WorkflowStateEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = os.WriteFile(WorkflowStatePath(session), data, 0644)
}

// FormatWorkflowState returns a human-readable state description.
func FormatWorkflowState(entry WorkflowStateEntry) string {
	if entry.State == StateIdle && entry.Since == 0 {
		return "idle (no activity)"
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("state: %s", entry.State))

	if entry.Since > 0 {
		dur := time.Since(time.Unix(entry.Since, 0))
		parts = append(parts, fmt.Sprintf("since: %s ago", workflowDuration(dur)))
	}
	if entry.Trigger != "" {
		parts = append(parts, fmt.Sprintf("trigger: %s", entry.Trigger))
	}
	if entry.FilesChanged > 0 {
		parts = append(parts, fmt.Sprintf("files: %d", entry.FilesChanged))
	}
	if len(entry.LastFiles) > 0 {
		// Show just basenames
		var names []string
		for _, f := range entry.LastFiles {
			names = append(names, filepath.Base(f))
		}
		parts = append(parts, fmt.Sprintf("last: %s", strings.Join(names, ", ")))
	}

	// Show accumulated outcomes
	var outcomes []string
	if entry.BuildOutcome != "" {
		outcomes = append(outcomes, "build:"+entry.BuildOutcome)
	}
	if entry.TestOutcome != "" {
		outcomes = append(outcomes, "test:"+entry.TestOutcome)
	}
	if entry.ReviewOutcome != "" {
		outcomes = append(outcomes, "review:"+entry.ReviewOutcome)
	}
	if entry.DeployOutcome != "" {
		outcomes = append(outcomes, "deploy:"+entry.DeployOutcome)
	}
	if entry.RunOutcome != "" {
		outcomes = append(outcomes, "run:"+entry.RunOutcome)
	}
	if entry.WatchOutcome != "" {
		outcomes = append(outcomes, "watch:"+entry.WatchOutcome)
	}
	if len(outcomes) > 0 {
		parts = append(parts, fmt.Sprintf("outcomes: %s", strings.Join(outcomes, " ")))
	}

	return strings.Join(parts, "  ")
}

// FormatWorkflowStateCompact returns a short colored indicator for console/TUI.
func FormatWorkflowStateCompact(entry WorkflowStateEntry, width int) string {
	color := WorkflowStateColor(entry.State)
	name := entry.State.String()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s● %s%s", color, name, ColorReset))

	if entry.Trigger != "" {
		b.WriteString(fmt.Sprintf("  %s⟵ %s%s", ColorDim, entry.Trigger, ColorReset))
	}
	if entry.FilesChanged > 0 {
		b.WriteString(fmt.Sprintf("  %d files", entry.FilesChanged))
	}
	if entry.Since > 0 {
		dur := time.Since(time.Unix(entry.Since, 0))
		b.WriteString(fmt.Sprintf("  %s", workflowDuration(dur)))
	}

	return b.String()
}

// WorkflowStateColor returns the ANSI color for a state.
func WorkflowStateColor(state WorkflowState) string {
	switch state {
	case StateIdle:
		return ColorDim
	case StateEditing, StateAnalyzing:
		return ColorCyan
	case StateBuilding, StateTesting, StateReviewing, StateCommitting, StateDeploying, StateRunning, StateWatching:
		return ColorYellow
	case StateBuildFail, StateTestFail, StateDeployFail, StateRunFail, StateWatchFail:
		return ColorRed
	case StateReviewed:
		return ColorGreen
	default:
		return ColorDim
	}
}

// workflowDuration returns a human-friendly short duration string.
func workflowDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

// NewestMessageIDFrom returns the ID of the newest unconsumed message in
// role's inbox that was sent by from AND addressed to role, or "" if none.
//
// The addressee filter is load-bearing (MUX-007): auto-CC copies keep their
// original To, so a review→test response CC'd into edit's inbox must never
// read as "review reported completion to edit". Matching on From alone is
// what let any stale review CC satisfy the reviewed-transition check.
// Inboxes are append-only, so the last matching entry is the newest.
func NewestMessageIDFrom(session, role, from string) string {
	msgs, err := Peek(session, role)
	if err != nil {
		return ""
	}
	newest := ""
	for _, m := range msgs {
		if m.From == from && m.To == role {
			newest = m.ID
		}
	}
	return newest
}

// ReviewedMarkerPath returns the file recording the ID of the review
// completion message that last fired the reviewed transition.
func ReviewedMarkerPath(session string) string {
	return filepath.Join(BusDir(session), "reviewed-transition.last")
}

// ReadReviewedMarker returns the message ID recorded by WriteReviewedMarker,
// or "" if no reviewed transition has fired yet.
func ReadReviewedMarker(session string) string {
	data, err := os.ReadFile(ReviewedMarkerPath(session))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteReviewedMarker records id as the review completion that fired the
// reviewed transition, making the daemon's gate once-per-completion (MUX-007):
// unrelated inbox growth re-observes the same ID and does not re-fire, while a
// genuine new completion carries a new ID. On disk rather than in daemon
// memory so a daemon restart mid-storm cannot replay a stale completion.
//
// Written atomically (tmp+rename) so a crash mid-write can never leave a
// truncated ID that matches no real message and re-fires forever. On error
// the caller must withhold the transition: firing with the marker unrecorded
// replays the same completion on the next inbox growth — the storm itself.
// Withholding is safe because the unconsumed completion is re-observed and
// retried on a later growth once the write succeeds.
func WriteReviewedMarker(session, id string) error {
	return atomicWriteFile(ReviewedMarkerPath(session), []byte(id+"\n"))
}
