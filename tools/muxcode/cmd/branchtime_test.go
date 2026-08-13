package cmd

import (
	"encoding/json"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// newBranchTimeJSON is the projection the plan agent's automated read path
// parses, so its shape is a contract: these tests pin the field values rather
// than just exercising the function.

func TestNewBranchTimeJSONProjectsEntry(t *testing.T) {
	e := bus.BranchTimeEntry{
		Seconds:               3600,
		LastRecordedSeconds:   1200,
		LastRecordedAt:        1700000000,
		LastJiraLoggedSeconds: 600,
		Updated:               1700000001,
	}
	got := newBranchTimeJSON("repoA", "feat/x", e, true)

	if got.RepoKey != "repoA" || got.Branch != "feat/x" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if got.Seconds != 3600 {
		t.Errorf("Seconds = %d, want 3600", got.Seconds)
	}
	if got.Formatted != bus.FormatDuration(3600) {
		t.Errorf("Formatted = %q, want %q", got.Formatted, bus.FormatDuration(3600))
	}
	// The staleness signal: accrued minus already-recorded.
	if got.UnrecordedSeconds != 2400 {
		t.Errorf("UnrecordedSeconds = %d, want 2400", got.UnrecordedSeconds)
	}
	if got.LastRecordedSeconds != 1200 || got.LastRecordedAt != 1700000000 {
		t.Errorf("record watermark not carried: %+v", got)
	}
	if got.LastJiraLoggedSeconds != 600 {
		t.Errorf("LastJiraLoggedSeconds = %d, want 600", got.LastJiraLoggedSeconds)
	}
	if !got.Current {
		t.Error("Current = false, want true")
	}
}

// A ledger behind the doc (lost or reset store) must report 0 outstanding, not
// a negative. Reconciliation is seed's job; a negative delta would invite a
// consumer to "subtract" time from a doc row.
func TestNewBranchTimeJSONFloorsNegativeUnrecorded(t *testing.T) {
	e := bus.BranchTimeEntry{Seconds: 100, LastRecordedSeconds: 45296}
	got := newBranchTimeJSON("repoA", "feat/x", e, false)
	if got.UnrecordedSeconds != 0 {
		t.Errorf("UnrecordedSeconds = %d, want 0 (must floor, never go negative)", got.UnrecordedSeconds)
	}
	if got.Seconds != 100 {
		t.Errorf("Seconds = %d, want 100 (floor must not alter the total)", got.Seconds)
	}
}

// An untracked branch yields a well-formed zero object so automated callers
// never have to special-case absence.
func TestNewBranchTimeJSONZeroEntryIsWellFormed(t *testing.T) {
	got := newBranchTimeJSON("repoA", "never/seen", bus.BranchTimeEntry{}, false)
	if got.Seconds != 0 || got.UnrecordedSeconds != 0 || got.LastRecordedAt != 0 {
		t.Errorf("expected zeroed counters, got %+v", got)
	}
	if got.Branch != "never/seen" {
		t.Errorf("Branch = %q, want never/seen", got.Branch)
	}
	if got.Formatted == "" {
		t.Error("Formatted must render even for a zero total")
	}
}

// main/master are on the shipped ignore list; the flag is how a consumer knows
// to accumulate but never write the branch into a doc.
//
// The ignore list is pinned explicitly rather than left to ambient config: the
// env var fully replaces the built-in default when set, so a value inherited
// from the surrounding session would otherwise decide this assertion.
func TestNewBranchTimeJSONFlagsIgnoredBranch(t *testing.T) {
	t.Setenv("MUXCODE_BRANCH_TIME_IGNORE", "main,master")
	if got := newBranchTimeJSON("repoA", "main", bus.BranchTimeEntry{}, true); !got.Ignored {
		t.Error("main should be flagged Ignored")
	}
	if got := newBranchTimeJSON("repoA", "feat/x", bus.BranchTimeEntry{}, false); got.Ignored {
		t.Error("feature branch should not be flagged Ignored")
	}
}

// Setting the ignore list to empty replaces the default with nothing rather
// than falling back to {main, master} — so main becomes trackable, and the
// Ignored flag must follow that override rather than hard-coding main.
func TestNewBranchTimeJSONEmptyIgnoreListTracksMain(t *testing.T) {
	t.Setenv("MUXCODE_BRANCH_TIME_IGNORE", "")
	if got := newBranchTimeJSON("repoA", "main", bus.BranchTimeEntry{}, true); got.Ignored {
		t.Error("with an empty ignore list, main must NOT be flagged Ignored")
	}
}

// The JSON keys are what the plan agent parses — renaming one silently breaks
// the read path, so the serialised form is pinned explicitly.
func TestBranchTimeJSONKeysAreStable(t *testing.T) {
	out, err := json.Marshal(newBranchTimeJSON("repoA", "feat/x", bus.BranchTimeEntry{}, false))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{
		"repoKey", "branch", "seconds", "formatted", "unrecordedSeconds",
		"lastRecordedSeconds", "lastRecordedAt", "lastJiraLoggedSeconds",
		"updated", "current", "ignored",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing JSON key %q — this is a consumer-visible contract break", key)
		}
	}
}

// Ordering is descending by time with a name tiebreak, so --all --json output
// is deterministic across runs.
func TestSortBranchNamesOrderingIsDeterministic(t *testing.T) {
	m := map[string]*bus.BranchTimeEntry{
		"low":     {Seconds: 10},
		"high":    {Seconds: 900},
		"tie-b":   {Seconds: 100},
		"tie-a":   {Seconds: 100},
		"zeroest": {Seconds: 0},
	}
	want := []string{"high", "tie-a", "tie-b", "low", "zeroest"}
	for i := 0; i < 5; i++ {
		got := bus.SortBranchNames(m)
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d: got %v, want %v", i, got, want)
			}
		}
	}
}
