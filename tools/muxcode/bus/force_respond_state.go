package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ForceRespondState is one role's escalation episode (MUX-105), persisted
// so the TUI can show what the daemon's ladder already tried — the two
// run in different processes, so in-memory state cannot be shared — and
// so an episode survives a daemon restart.
type ForceRespondState struct {
	Role      string   `json:"role"`
	Rung      int      `json:"rung"` // next rung the daemon will fire
	History   []string `json:"history"`
	UpdatedAt int64    `json:"updated_at"`
}

func forceRespondDir(session string) string {
	return filepath.Join(BusDir(session), "force-respond")
}

// ForceRespondStatePath returns the state file for one role's episode.
func ForceRespondStatePath(session, role string) string {
	return filepath.Join(forceRespondDir(session), role+".json")
}

// WriteForceRespondState persists a role's episode state.
func WriteForceRespondState(session string, st ForceRespondState) error {
	if err := os.MkdirAll(forceRespondDir(session), 0755); err != nil {
		return err
	}
	st.UpdatedAt = time.Now().Unix()
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(ForceRespondStatePath(session, st.Role), data, 0644)
}

// ReadForceRespondState returns a role's episode state, or ok=false when
// no episode is open.
func ReadForceRespondState(session, role string) (ForceRespondState, bool) {
	data, err := os.ReadFile(ForceRespondStatePath(session, role))
	if err != nil {
		return ForceRespondState{}, false
	}
	var st ForceRespondState
	if err := json.Unmarshal(data, &st); err != nil {
		return ForceRespondState{}, false
	}
	return st, true
}

// ClearForceRespondState removes a role's episode state — the episode
// ended (a receipt landed or the inbox drained).
func ClearForceRespondState(session, role string) {
	_ = os.Remove(ForceRespondStatePath(session, role))
}
