package bus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The events file is how the modal follows a detached run: every complete
// line comes back, sub-rows included, and a final line still being written is
// left for the next read rather than failing it.
func TestUpgradeEventsRoundTripAndPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	step := StepResult{Name: "Restart daemons", Success: true, Sub: []StepResult{{Name: "alpha", Success: true, Note: "daemon restarted"}}}
	if err := AppendUpgradeEvent(path, UpgradeEvent{Index: 5, Step: &step}); err != nil {
		t.Fatal(err)
	}
	if err := AppendUpgradeEvent(path, UpgradeEvent{Index: 6, Done: true, Error: "Reload tmux config: boom"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"index":7,"step":`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	events, err := ReadUpgradeEvents(path)
	if err != nil {
		t.Fatalf("ReadUpgradeEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want the two complete lines", events)
	}
	if got := events[0].Step; got == nil || got.Name != "Restart daemons" || len(got.Sub) != 1 || got.Sub[0].Name != "alpha" {
		t.Errorf("step event = %+v, want the step with its sub-row", got)
	}
	if !events[1].Done || events[1].Error != "Reload tmux config: boom" {
		t.Errorf("end event = %+v", events[1])
	}
}

// Abandoned events files are pruned once they are a day old; a fresh one —
// possibly a run still being followed — is kept.
func TestPruneUpgradeEventsKeepsFreshFiles(t *testing.T) {
	dir := t.TempDir()
	old, fresh := filepath.Join(dir, "run-old.jsonl"), filepath.Join(dir, "run-fresh.jsonl")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stale := time.Now().Add(-2 * upgradeEventsMaxAge)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}

	pruneUpgradeEvents(dir, time.Now().Add(-upgradeEventsMaxAge))
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("a two-day-old events file survived the prune")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a fresh events file was pruned: %v", err)
	}
}
