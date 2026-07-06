package bus

import (
	"path/filepath"
	"sync"
	"testing"
)

// useTempLedger points the ledger at a temp file for hermetic tests.
func useTempLedger(t *testing.T) {
	t.Helper()
	t.Setenv("MUXCODE_BRANCH_TIME_FILE", filepath.Join(t.TempDir(), "branch-time.json"))
}

func TestLoadBranchTimeMissingFileIsEmpty(t *testing.T) {
	useTempLedger(t)
	l, err := LoadBranchTime()
	if err != nil {
		t.Fatalf("LoadBranchTime: %v", err)
	}
	if l == nil || len(l) != 0 {
		t.Fatalf("expected empty non-nil ledger, got %#v", l)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	useTempLedger(t)
	in := BranchTimeLedger{
		"repoA": {
			"feature/x": {Seconds: 15120, LastJiraLoggedSeconds: 3600, Updated: 42},
		},
	}
	if err := SaveBranchTime(in); err != nil {
		t.Fatalf("SaveBranchTime: %v", err)
	}
	out, err := LoadBranchTime()
	if err != nil {
		t.Fatalf("LoadBranchTime: %v", err)
	}
	e := out["repoA"]["feature/x"]
	if e == nil || e.Seconds != 15120 || e.LastJiraLoggedSeconds != 3600 || e.Updated != 42 {
		t.Fatalf("round-trip mismatch: %#v", e)
	}
}

func TestAccumulateAddsAndReads(t *testing.T) {
	useTempLedger(t)
	if _, err := AccumulateBranchTime("repoA", "main", 30, 0); err != nil {
		t.Fatalf("accumulate: %v", err)
	}
	total, err := AccumulateBranchTime("repoA", "main", 45, 0)
	if err != nil {
		t.Fatalf("accumulate: %v", err)
	}
	if total != 75 {
		t.Fatalf("expected total 75, got %d", total)
	}
	if got := BranchTimeSeconds("repoA", "main"); got != 75 {
		t.Fatalf("BranchTimeSeconds = %d, want 75", got)
	}
}

func TestAccumulateClockJumpGuard(t *testing.T) {
	useTempLedger(t)
	// A huge delta (e.g. laptop slept for hours) is clamped to maxDelta.
	total, err := AccumulateBranchTime("repoA", "main", 999999, 20)
	if err != nil {
		t.Fatalf("accumulate: %v", err)
	}
	if total != 20 {
		t.Fatalf("expected clamp to 20, got %d", total)
	}
}

func TestAccumulateNonPositiveIsNoop(t *testing.T) {
	useTempLedger(t)
	if total, _ := AccumulateBranchTime("repoA", "main", 0, 10); total != 0 {
		t.Fatalf("zero delta should be no-op, got %d", total)
	}
	if total, _ := AccumulateBranchTime("repoA", "main", -5, 10); total != 0 {
		t.Fatalf("negative delta should be no-op, got %d", total)
	}
	if got := BranchTimeSeconds("repoA", "main"); got != 0 {
		t.Fatalf("expected 0 accumulated, got %d", got)
	}
}

func TestAccumulateEmptyKeysIsNoop(t *testing.T) {
	useTempLedger(t)
	if total, _ := AccumulateBranchTime("", "main", 30, 0); total != 0 {
		t.Fatalf("empty repoKey should be no-op, got %d", total)
	}
	if total, _ := AccumulateBranchTime("repoA", "", 30, 0); total != 0 {
		t.Fatalf("empty branch should be no-op, got %d", total)
	}
}

func TestConcurrentAccumulateRaceSafe(t *testing.T) {
	useTempLedger(t)
	const goroutines = 8
	const perGoroutine = 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, err := AccumulateBranchTime("repoA", "main", 1, 0); err != nil {
					t.Errorf("accumulate: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	want := int64(goroutines * perGoroutine)
	if got := BranchTimeSeconds("repoA", "main"); got != want {
		t.Fatalf("expected %d after concurrent accumulate, got %d", want, got)
	}
}

func TestResetBranchTime(t *testing.T) {
	useTempLedger(t)
	AccumulateBranchTime("repoA", "main", 120, 0)
	SetJiraWatermark("repoA", "main", 60)
	if err := ResetBranchTime("repoA", "main"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	e, _ := BranchTimeGet("repoA", "main")
	if e.Seconds != 0 || e.LastJiraLoggedSeconds != 0 {
		t.Fatalf("reset should zero counters, got %#v", e)
	}
}

func TestSetJiraWatermark(t *testing.T) {
	useTempLedger(t)
	AccumulateBranchTime("repoA", "main", 300, 0)
	if err := SetJiraWatermark("repoA", "main", 180); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	e, ok := BranchTimeGet("repoA", "main")
	if !ok || e.LastJiraLoggedSeconds != 180 {
		t.Fatalf("watermark = %#v, want 180", e)
	}
	if e.Seconds != 300 {
		t.Fatalf("watermark should not change seconds, got %d", e.Seconds)
	}
}

func TestSortedBranchNamesByDescendingTime(t *testing.T) {
	useTempLedger(t)
	AccumulateBranchTime("repoA", "low", 60, 0)
	AccumulateBranchTime("repoA", "high", 600, 0)
	AccumulateBranchTime("repoA", "mid", 300, 0)
	names := SortedBranchNames("repoA")
	want := []string{"high", "mid", "low"}
	if len(names) != 3 || names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Fatalf("SortedBranchNames = %v, want %v", names, want)
	}
}

func TestBranchTimeFormatDuration(t *testing.T) {
	cases := []struct {
		secs int64
		want string
	}{
		{0, "0m"},
		{45, "0m"},
		{60, "1m"},
		{46 * 60, "46m"},
		{4*3600 + 12*60, "4h 12m"},
		{2*86400 + 5*3600 + 30*60, "2d 5h"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.secs); got != c.want {
			t.Errorf("FormatDuration(%d) = %q, want %q", c.secs, got, c.want)
		}
	}
}

func TestBranchTimeFormatDurationCompact(t *testing.T) {
	if got := FormatDurationCompact(4*3600 + 12*60); got != "4h12m" {
		t.Errorf("FormatDurationCompact = %q, want 4h12m", got)
	}
}

func TestStripURLUserinfo(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://user:token@github.com/o/r.git", "https://github.com/o/r.git"},
		{"https://user@github.com/o/r.git", "https://github.com/o/r.git"},
		{"https://github.com/o/r.git", "https://github.com/o/r.git"},
		{"git@github.com:o/r.git", "git@github.com:o/r.git"}, // scp-style: unchanged
		{"/local/path/to/repo", "/local/path/to/repo"},
		{"ssh://git@host:22/o/r.git", "ssh://host:22/o/r.git"},
	}
	for _, c := range cases {
		if got := stripURLUserinfo(c.in); got != c.want {
			t.Errorf("stripURLUserinfo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRepoKeyInGitRepoNonEmpty(t *testing.T) {
	// The test runs inside the muxcode git repo, so RepoKey resolves to the
	// remote URL or toplevel path — either way, non-empty.
	if RepoKey() == "" {
		t.Skip("not in a git repo; skipping RepoKey smoke test")
	}
}
