package bus

import (
	"fmt"
	"os"
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

// unsetEnvForTest removes key for the duration of the test, restoring whatever
// value — or absence — it had beforehand. t.Setenv registers the restore of the
// original state; the Unsetenv then clears it for the test body, which lets a
// test exercise the "variable not set" default path even when the ambient
// session has it set.
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, os.Getenv(key))
	os.Unsetenv(key)
}

func TestBranchTimeIgnoredDefaults(t *testing.T) {
	// With no override, the shared integration branches are ignored, feature
	// branches are tracked, and an empty branch (detached HEAD) is always ignored.
	unsetEnvForTest(t, "MUXCODE_BRANCH_TIME_IGNORE")
	if !BranchTimeIgnored("main") {
		t.Error("main should be ignored by default")
	}
	if !BranchTimeIgnored("master") {
		t.Error("master should be ignored by default")
	}
	if BranchTimeIgnored("feature/x") {
		t.Error("feature/x should be tracked")
	}
	if !BranchTimeIgnored("") {
		t.Error("empty branch should always be ignored")
	}
}

func TestBranchTimeIgnoredOverride(t *testing.T) {
	// An explicit override replaces the default set entirely (main becomes
	// tracked) and trims whitespace around names.
	t.Setenv("MUXCODE_BRANCH_TIME_IGNORE", "develop, release")
	if BranchTimeIgnored("main") {
		t.Error("main should be tracked when the override omits it")
	}
	if !BranchTimeIgnored("develop") {
		t.Error("develop should be ignored per override")
	}
	if !BranchTimeIgnored("release") {
		t.Error("release should be ignored per override (whitespace trimmed)")
	}
}

func TestBranchTimeIgnoredEmptyOverrideTracksAll(t *testing.T) {
	// An empty override opts out of the default ignore set — every named branch
	// is tracked — but an empty branch is still ignored.
	t.Setenv("MUXCODE_BRANCH_TIME_IGNORE", "")
	if BranchTimeIgnored("main") {
		t.Error("empty override should track main")
	}
	if BranchTimeIgnored("master") {
		t.Error("empty override should track master")
	}
	if !BranchTimeIgnored("") {
		t.Error("empty branch should still be ignored")
	}
}

func TestBranchTimeActivityRolesExcludesAmbient(t *testing.T) {
	unsetEnvForTest(t, "MUXCODE_BRANCH_TIME_ACTIVITY_ROLES")
	roles := map[string]bool{}
	for _, r := range BranchTimeActivityRoles() {
		roles[r] = true
	}
	for _, want := range []string{"edit", "build", "test", "review", "run"} {
		if !roles[want] {
			t.Errorf("default activity roles should include %q", want)
		}
	}
	// Ambient-output roles must be excluded so they don't keep the clock alive.
	for _, excluded := range []string{"watch", "serve"} {
		if roles[excluded] {
			t.Errorf("activity roles should exclude ambient-output role %q", excluded)
		}
	}
}

func TestBranchTimeActivityRolesOverride(t *testing.T) {
	t.Setenv("MUXCODE_BRANCH_TIME_ACTIVITY_ROLES", "edit, build")
	got := BranchTimeActivityRoles()
	if len(got) != 2 || got[0] != "edit" || got[1] != "build" {
		t.Fatalf("override roles = %v, want [edit build]", got)
	}
}

func TestPaneShowsAgentWorking(t *testing.T) {
	cases := []struct {
		name         string
		content      string
		hookProvider bool
		want         bool
	}{
		{
			"claude working (esc to interrupt)",
			"✻ Nebulizing… (1m 29s · ↓ 5.1k tokens · thinking with xhigh effort)\n──── code-editor ──\n❯ \n⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ← for agents",
			true, true,
		},
		{
			"claude idle (completed recap)",
			"✻ Cooked for 1m 47s\n──── code-editor ──\n❯ \n⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents",
			true, false,
		},
		{
			"claude idle with daemon wake-up injection",
			"❯ You have new messages\n⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents",
			true, false,
		},
		{
			"opencode working (running marker)",
			"▸  Build · MiniMax M2.5 · 3.2s\n  building...\n  ctrl+p commands",
			false, true,
		},
		{
			"opencode idle (cost/context status bar — phantom-churn source)",
			"                80.1K (8%) · $0.44  ctrl+p commands    • OpenCode 1.17.13",
			false, false,
		},
		{
			"opencode completed (stop marker)",
			"▣  Build · MiniMax M2.5 · 12.9s\n  ctrl+p commands • OpenCode 1.17.13",
			false, false,
		},
		{
			"opencode idle with truncated path (no false positive from … + ·)",
			"  ~/Repos/…/is-advising-gateway · main\n  80.1K (8%) · $0.44  ctrl+p commands",
			false, false,
		},
	}
	for _, c := range cases {
		if got := paneShowsAgentWorking(c.content, c.hookProvider); got != c.want {
			t.Errorf("%s: paneShowsAgentWorking=%v, want %v", c.name, got, c.want)
		}
	}
}

func TestBranchTimeUserActive(t *testing.T) {
	cases := []struct {
		name          string
		idle, idleMax int64
		want          bool
	}{
		{"detached always inactive", -1, 300, false},
		{"detached with idle detection off", -1, 0, false},
		{"attached and typing recently", 10, 300, true},
		{"attached but idle past threshold", 400, 300, false},
		{"attached exactly at threshold", 300, 300, true},
		{"attached with idle detection off", 9999, 0, true},
		{"attached just active", 0, 300, true},
	}
	for _, c := range cases {
		if got := BranchTimeUserActive(c.idle, c.idleMax); got != c.want {
			t.Errorf("%s: BranchTimeUserActive(%d,%d)=%v, want %v", c.name, c.idle, c.idleMax, got, c.want)
		}
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

// SessionRepoDir exists so long-lived processes never resolve git from their
// own working directory. The daemon is relaunched by upgrade-daemons with an
// inherited cwd, so a build in another repo would otherwise silently retarget
// or stop every other session's time tracking.

func TestSessionRepoDirPrefersEnvOverride(t *testing.T) {
	t.Setenv("MUXCODE_SESSION_REPO_DIR", "/tmp/some/repo")
	if got := SessionRepoDir("any-session"); got != "/tmp/some/repo" {
		t.Errorf("SessionRepoDir = %q, want the override", got)
	}
}

func TestSessionRepoDirMajorityPanePathWins(t *testing.T) {
	t.Setenv("MUXCODE_SESSION_REPO_DIR", "")
	orig := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		// One agent has cd'd elsewhere; it must not outvote the session.
		return "/repo/a\n/repo/a\n/repo/b\n/repo/a\n", nil
	}
	t.Cleanup(func() { tmuxOutputRunner = orig })

	if got := SessionRepoDir("s"); got != "/repo/a" {
		t.Errorf("SessionRepoDir = %q, want /repo/a", got)
	}
}

// An unresolvable session yields "" so callers fall back to their own working
// directory — the previous behaviour — rather than failing closed.
func TestSessionRepoDirEmptyWhenTmuxFails(t *testing.T) {
	t.Setenv("MUXCODE_SESSION_REPO_DIR", "")
	orig := tmuxOutputRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		return "", fmt.Errorf("no server")
	}
	t.Cleanup(func() { tmuxOutputRunner = orig })

	if got := SessionRepoDir("s"); got != "" {
		t.Errorf("SessionRepoDir = %q, want empty", got)
	}
}

// The regression this whole change exists for: resolving against an explicit
// directory must not depend on the process working directory.
func TestCurrentBranchInIgnoresProcessCwd(t *testing.T) {
	t.Setenv("MUXCODE_SESSION_REPO_DIR", "")
	dir := t.TempDir()
	// A directory that is not a git repo resolves to "" regardless of the fact
	// that the test process itself is running inside the muxcode repo.
	if got := CurrentBranchIn(dir); got != "" {
		t.Errorf("CurrentBranchIn(non-repo) = %q, want empty — it leaked the process cwd", got)
	}
	if got := RepoKeyIn(dir); got != "" {
		t.Errorf("RepoKeyIn(non-repo) = %q, want empty — it leaked the process cwd", got)
	}
}

func TestRepoKeyInGitRepoNonEmpty(t *testing.T) {
	// The test runs inside the muxcode git repo, so RepoKey resolves to the
	// remote URL or toplevel path — either way, non-empty.
	if RepoKey() == "" {
		t.Skip("not in a git repo; skipping RepoKey smoke test")
	}
}

// --- doc-recording watermark + never-regress seeding ---

func TestSetRecordedWatermarkDoesNotChangeAccrual(t *testing.T) {
	useTempLedger(t)
	if _, err := AccumulateBranchTime("repoA", "feat/x", 500, 0); err != nil {
		t.Fatalf("AccumulateBranchTime: %v", err)
	}
	if err := SetRecordedWatermark("repoA", "feat/x", 500); err != nil {
		t.Fatalf("SetRecordedWatermark: %v", err)
	}
	e, ok := BranchTimeGet("repoA", "feat/x")
	if !ok {
		t.Fatal("entry missing after recording")
	}
	// The watermark is bookkeeping: accrued time must be untouched.
	if e.Seconds != 500 {
		t.Errorf("Seconds = %d, want 500 (watermark must not alter accrual)", e.Seconds)
	}
	if e.LastRecordedSeconds != 500 {
		t.Errorf("LastRecordedSeconds = %d, want 500", e.LastRecordedSeconds)
	}
	if e.LastRecordedAt == 0 {
		t.Error("LastRecordedAt not stamped")
	}
}

// Recording is idempotent by construction because doc rows carry absolute
// totals: re-recording an unchanged branch must leave the same watermark and
// report nothing new outstanding.
func TestSetRecordedWatermarkIsIdempotent(t *testing.T) {
	useTempLedger(t)
	if _, err := AccumulateBranchTime("repoA", "feat/x", 900, 0); err != nil {
		t.Fatalf("AccumulateBranchTime: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := SetRecordedWatermark("repoA", "feat/x", 900); err != nil {
			t.Fatalf("SetRecordedWatermark #%d: %v", i, err)
		}
	}
	e, _ := BranchTimeGet("repoA", "feat/x")
	if e.Seconds != 900 || e.LastRecordedSeconds != 900 {
		t.Errorf("after 3 records: Seconds=%d LastRecorded=%d, want 900/900", e.Seconds, e.LastRecordedSeconds)
	}
}

func TestSeedBranchTimeRaisesWhenBelow(t *testing.T) {
	useTempLedger(t)
	if _, err := AccumulateBranchTime("repoA", "feat/x", 100, 0); err != nil {
		t.Fatalf("AccumulateBranchTime: %v", err)
	}
	total, err := SeedBranchTime("repoA", "feat/x", 5000)
	if err != nil {
		t.Fatalf("SeedBranchTime: %v", err)
	}
	if total != 5000 {
		t.Errorf("total = %d, want 5000", total)
	}
	if got := BranchTimeSeconds("repoA", "feat/x"); got != 5000 {
		t.Errorf("persisted = %d, want 5000", got)
	}
}

// The never-regress guarantee: a seed can only ever raise. A stale or replayed
// seed must not deflate a ledger that has since accrued past the seeded value.
func TestSeedBranchTimeNeverLowers(t *testing.T) {
	useTempLedger(t)
	if _, err := AccumulateBranchTime("repoA", "feat/x", 9000, 0); err != nil {
		t.Fatalf("AccumulateBranchTime: %v", err)
	}
	total, err := SeedBranchTime("repoA", "feat/x", 60)
	if err != nil {
		t.Fatalf("SeedBranchTime: %v", err)
	}
	if total != 9000 {
		t.Errorf("total = %d, want 9000 (seed must not lower)", total)
	}
	if got := BranchTimeSeconds("repoA", "feat/x"); got != 9000 {
		t.Errorf("persisted = %d, want 9000", got)
	}
}

// A lost ledger reconciles from the doc's larger total without the doc ever
// showing less time than it already did.
func TestSeedBranchTimeRecoversLostLedger(t *testing.T) {
	useTempLedger(t)
	const docTotal int64 = 45296 // what a requirements doc still shows
	if got := BranchTimeSeconds("repoA", "feat/x"); got != 0 {
		t.Fatalf("expected empty ledger, got %d", got)
	}
	if _, err := SeedBranchTime("repoA", "feat/x", docTotal); err != nil {
		t.Fatalf("SeedBranchTime: %v", err)
	}
	if got := BranchTimeSeconds("repoA", "feat/x"); got != docTotal {
		t.Errorf("reseeded = %d, want %d", got, docTotal)
	}
	// Further accrual continues from the reseeded floor, not from zero.
	if _, err := AccumulateBranchTime("repoA", "feat/x", 100, 0); err != nil {
		t.Fatalf("AccumulateBranchTime: %v", err)
	}
	if got := BranchTimeSeconds("repoA", "feat/x"); got != docTotal+100 {
		t.Errorf("after accrual = %d, want %d", got, docTotal+100)
	}
}

func TestSeedBranchTimeRejectsNonPositiveAndEmptyKeys(t *testing.T) {
	useTempLedger(t)
	if _, err := AccumulateBranchTime("repoA", "feat/x", 300, 0); err != nil {
		t.Fatalf("AccumulateBranchTime: %v", err)
	}
	cases := []struct {
		repo   string
		branch string
		secs   int64
	}{
		{"repoA", "feat/x", 0},
		{"repoA", "feat/x", -5},
		{"", "feat/x", 100},
		{"repoA", "", 100},
	}
	for _, c := range cases {
		if _, err := SeedBranchTime(c.repo, c.branch, c.secs); err != nil {
			t.Errorf("SeedBranchTime(%q,%q,%d) errored: %v", c.repo, c.branch, c.secs, err)
		}
	}
	if got := BranchTimeSeconds("repoA", "feat/x"); got != 300 {
		t.Errorf("total = %d, want 300 (no-op seeds must not mutate)", got)
	}
}

func TestResetClearsRecordedWatermark(t *testing.T) {
	useTempLedger(t)
	if _, err := AccumulateBranchTime("repoA", "feat/x", 700, 0); err != nil {
		t.Fatalf("AccumulateBranchTime: %v", err)
	}
	if err := SetRecordedWatermark("repoA", "feat/x", 700); err != nil {
		t.Fatalf("SetRecordedWatermark: %v", err)
	}
	if err := ResetBranchTime("repoA", "feat/x"); err != nil {
		t.Fatalf("ResetBranchTime: %v", err)
	}
	e, _ := BranchTimeGet("repoA", "feat/x")
	if e.Seconds != 0 || e.LastRecordedSeconds != 0 || e.LastRecordedAt != 0 {
		t.Errorf("after reset: %+v, want all counters zeroed", e)
	}
}

// Ledgers written before doc-recording existed must load cleanly, with the new
// fields defaulting to zero rather than failing the unmarshal.
func TestLegacyLedgerWithoutRecordFieldsLoads(t *testing.T) {
	useTempLedger(t)
	legacy := `{"repoA":{"feat/x":{"seconds":1200,"lastJiraLoggedSeconds":600,"updated":99}}}`
	if err := os.WriteFile(BranchTimePath(), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	e, ok := BranchTimeGet("repoA", "feat/x")
	if !ok {
		t.Fatal("legacy entry not found")
	}
	if e.Seconds != 1200 || e.LastJiraLoggedSeconds != 600 {
		t.Errorf("legacy fields lost: %+v", e)
	}
	if e.LastRecordedSeconds != 0 || e.LastRecordedAt != 0 {
		t.Errorf("new fields should default to zero, got %+v", e)
	}
}
