package bus

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSpawnPath(t *testing.T) {
	p := SpawnPath("test-session")
	if !strings.HasSuffix(p, "spawn.jsonl") {
		t.Errorf("SpawnPath: expected path ending in spawn.jsonl, got %s", p)
	}
}

func TestIsSpawnRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{"spawn-a1b2c3d4", true},
		{"spawn-12345678", true},
		{"spawn-", true},
		{"edit", false},
		{"build", false},
		{"research", false},
		{"spawner", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsSpawnRole(tt.role)
		if got != tt.want {
			t.Errorf("IsSpawnRole(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestIsKnownRole_AcceptsSpawnRoles(t *testing.T) {
	if !IsKnownRole("spawn-a1b2c3d4") {
		t.Error("expected spawn-a1b2c3d4 to be a known role")
	}
	if !IsKnownRole("spawn-12345678") {
		t.Error("expected spawn-12345678 to be a known role")
	}
	if !IsKnownRole("edit") {
		t.Error("expected edit to still be a known role")
	}
	if IsKnownRole("unknown-role-xyz") {
		t.Error("expected unknown-role-xyz to not be a known role")
	}
}

func TestReadWriteSpawnEntries(t *testing.T) {
	session := testSession(t)

	// Initially empty
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		t.Fatalf("ReadSpawnEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}

	// Write entries
	testEntries := []SpawnEntry{
		{ID: "s1", Role: "research", SpawnRole: "spawn-11111111", Owner: "edit", Task: "research topic", Status: "running", StartedAt: time.Now().Unix()},
		{ID: "s2", Role: "test", SpawnRole: "spawn-22222222", Owner: "edit", Task: "run tests", Status: "completed", StartedAt: time.Now().Unix()},
	}
	if err := WriteSpawnEntries(session, testEntries); err != nil {
		t.Fatalf("WriteSpawnEntries: %v", err)
	}

	// Read back
	entries, err = ReadSpawnEntries(session)
	if err != nil {
		t.Fatalf("ReadSpawnEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ID != "s1" || entries[1].ID != "s2" {
		t.Errorf("unexpected entry IDs: %s, %s", entries[0].ID, entries[1].ID)
	}
}

func TestReadSpawnEntries_NotExist(t *testing.T) {
	entries, err := ReadSpawnEntries("nonexistent-session-spawn-12345")
	if err != nil {
		t.Fatalf("ReadSpawnEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestGetSpawnEntry(t *testing.T) {
	session := testSession(t)

	testEntries := []SpawnEntry{
		{ID: "s1", Role: "research", SpawnRole: "spawn-11111111", Owner: "edit", Status: "running"},
		{ID: "s2", Role: "test", SpawnRole: "spawn-22222222", Owner: "edit", Status: "completed"},
	}
	_ = WriteSpawnEntries(session, testEntries)

	e, err := GetSpawnEntry(session, "s2")
	if err != nil {
		t.Fatalf("GetSpawnEntry: %v", err)
	}
	if e.Role != "test" {
		t.Errorf("expected role 'test', got %s", e.Role)
	}

	_, err = GetSpawnEntry(session, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestUpdateSpawnEntry(t *testing.T) {
	session := testSession(t)

	testEntries := []SpawnEntry{
		{ID: "s1", Status: "running"},
	}
	_ = WriteSpawnEntries(session, testEntries)

	err := UpdateSpawnEntry(session, "s1", func(e *SpawnEntry) {
		e.Status = "completed"
		e.FinishedAt = time.Now().Unix()
	})
	if err != nil {
		t.Fatalf("UpdateSpawnEntry: %v", err)
	}

	e, _ := GetSpawnEntry(session, "s1")
	if e.Status != "completed" {
		t.Errorf("expected status 'completed', got %s", e.Status)
	}
}

func TestUpdateSpawnEntry_NotFound(t *testing.T) {
	session := testSession(t)

	err := UpdateSpawnEntry(session, "nonexistent", func(e *SpawnEntry) {
		e.Status = "completed"
	})
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestGetSpawnResult(t *testing.T) {
	session := testSession(t)

	spawnRole := "spawn-aabbccdd"

	// Send a message FROM the spawn role
	msg1 := NewMessage(spawnRole, "edit", "response", "spawn-task", "here is my result", "")
	if err := Send(session, msg1); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Send another message FROM the spawn role (this should be the result)
	msg2 := NewMessage(spawnRole, "edit", "response", "spawn-task", "final result", "")
	if err := Send(session, msg2); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Send a message TO the spawn role (should not be the result)
	msg3 := NewMessage("edit", spawnRole, "request", "spawn-task", "do something", "")
	if err := Send(session, msg3); err != nil {
		t.Fatalf("Send: %v", err)
	}

	result, ok := GetSpawnResult(session, spawnRole)
	if !ok {
		t.Fatal("expected to find a result")
	}
	if result.Payload != "final result" {
		t.Errorf("expected payload 'final result', got %q", result.Payload)
	}
	if result.From != spawnRole {
		t.Errorf("expected from %q, got %q", spawnRole, result.From)
	}
}

func TestGetSpawnResult_NoResult(t *testing.T) {
	session := testSession(t)

	_, ok := GetSpawnResult(session, "spawn-noexist1")
	if ok {
		t.Error("expected no result for spawn with no messages")
	}
}

func TestGetSpawnResult_OnlyIncoming(t *testing.T) {
	session := testSession(t)

	spawnRole := "spawn-onlyrecv"

	// Only messages TO the spawn role (no outgoing messages)
	msg := NewMessage("edit", spawnRole, "request", "spawn-task", "do something", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, ok := GetSpawnResult(session, spawnRole)
	if ok {
		t.Error("expected no result when only incoming messages exist")
	}
}

func TestCleanFinishedSpawns(t *testing.T) {
	session := testSession(t)

	// Create inbox files for spawn roles
	for _, role := range []string{"spawn-11111111", "spawn-22222222", "spawn-33333333"} {
		_ = touchFile(InboxPath(session, role))
	}

	testEntries := []SpawnEntry{
		{ID: "s1", SpawnRole: "spawn-11111111", Status: "running"},
		{ID: "s2", SpawnRole: "spawn-22222222", Status: "completed"},
		{ID: "s3", SpawnRole: "spawn-33333333", Status: "stopped"},
	}
	_ = WriteSpawnEntries(session, testEntries)

	removed, err := CleanFinishedSpawns(session)
	if err != nil {
		t.Fatalf("CleanFinishedSpawns: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}

	entries, _ := ReadSpawnEntries(session)
	if len(entries) != 1 {
		t.Fatalf("expected 1 remaining entry, got %d", len(entries))
	}
	if entries[0].ID != "s1" {
		t.Errorf("expected remaining entry s1, got %s", entries[0].ID)
	}

	// Inbox files for finished spawns should be removed
	if _, err := os.Stat(InboxPath(session, "spawn-22222222")); !os.IsNotExist(err) {
		t.Error("expected spawn-22222222 inbox to be removed")
	}
	if _, err := os.Stat(InboxPath(session, "spawn-33333333")); !os.IsNotExist(err) {
		t.Error("expected spawn-33333333 inbox to be removed")
	}
	// Running entry's inbox should still exist
	if _, err := os.Stat(InboxPath(session, "spawn-11111111")); err != nil {
		t.Error("expected spawn-11111111 inbox to still exist")
	}
}

func TestFormatSpawnList(t *testing.T) {
	entries := []SpawnEntry{
		{ID: "s1", Role: "research", SpawnRole: "spawn-11111111", Status: "running", Owner: "edit", Task: "research something"},
		{ID: "s2", Role: "test", SpawnRole: "spawn-22222222", Status: "completed", Owner: "edit", Task: "run tests"},
	}

	// showAll=false: only running
	out := FormatSpawnList(entries, false)
	if !strings.Contains(out, "s1") {
		t.Error("expected s1 in output")
	}
	if strings.Contains(out, "s2") {
		t.Error("expected s2 to be hidden when showAll=false")
	}

	// showAll=true: all entries
	out = FormatSpawnList(entries, true)
	if !strings.Contains(out, "s1") {
		t.Error("expected s1 in output")
	}
	if !strings.Contains(out, "s2") {
		t.Error("expected s2 in output when showAll=true")
	}
}

func TestFormatSpawnList_Empty(t *testing.T) {
	out := FormatSpawnList(nil, false)
	if !strings.Contains(out, "No running spawns") {
		t.Errorf("unexpected output for empty list: %s", out)
	}

	out = FormatSpawnList(nil, true)
	if !strings.Contains(out, "No spawns") {
		t.Errorf("unexpected output for empty --all list: %s", out)
	}
}

func TestFormatSpawnList_LongTask(t *testing.T) {
	entries := []SpawnEntry{
		{ID: "s1", Role: "research", SpawnRole: "spawn-11111111", Status: "running", Owner: "edit",
			Task: "This is a very long task description that should be truncated for display"},
	}
	out := FormatSpawnList(entries, true)
	if !strings.Contains(out, "...") {
		t.Error("expected truncated task with ellipsis")
	}
}

func TestFormatSpawnStatus(t *testing.T) {
	entry := SpawnEntry{
		ID:         "s1",
		Role:       "research",
		SpawnRole:  "spawn-a1b2c3d4",
		Status:     "completed",
		Owner:      "edit",
		Window:     "spawn-a1b2c3d4",
		Task:       "Research the topic",
		StartedAt:  1700000000,
		FinishedAt: 1700000120,
	}

	out := FormatSpawnStatus(entry)

	checks := []string{"s1", "research", "spawn-a1b2c3d4", "completed", "edit", "Research the topic", "Duration:"}
	for _, check := range checks {
		if !strings.Contains(out, check) {
			t.Errorf("expected %q in output, got:\n%s", check, out)
		}
	}
}

func TestFormatSpawnStatus_Running(t *testing.T) {
	entry := SpawnEntry{
		ID:        "s1",
		Role:      "research",
		SpawnRole: "spawn-a1b2c3d4",
		Status:    "running",
		Owner:     "edit",
		Window:    "spawn-a1b2c3d4",
		Task:      "Research the topic",
		StartedAt: time.Now().Unix(),
	}

	out := FormatSpawnStatus(entry)

	// Running spawn should not show Finished or Duration
	if strings.Contains(out, "Finished:") {
		t.Error("running spawn should not show finished time")
	}
	if strings.Contains(out, "Duration:") {
		t.Error("running spawn should not show duration")
	}
}

func TestSpawnWorktreeBase(t *testing.T) {
	base := SpawnWorktreeBase("test-session")
	if !strings.Contains(base, "muxcode-spawn-test-session") {
		t.Errorf("SpawnWorktreeBase: expected path containing muxcode-spawn-test-session, got %s", base)
	}
}

func TestSpawnEntryWorktreeFields_BackwardCompat(t *testing.T) {
	// Verify that SpawnEntry without worktree fields still unmarshals correctly
	// (backward compatibility with pre-worktree spawn entries)
	jsonStr := `{"id":"s1","role":"research","spawn_role":"spawn-11111111","owner":"edit","task":"test","status":"running","window":"spawn-11111111","started_at":1700000000}`
	var e SpawnEntry
	if err := json.Unmarshal([]byte(jsonStr), &e); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if e.Worktree != "" {
		t.Errorf("expected empty Worktree, got %q", e.Worktree)
	}
	if e.WorktreeRef != "" {
		t.Errorf("expected empty WorktreeRef, got %q", e.WorktreeRef)
	}
	if e.ID != "s1" || e.Role != "research" {
		t.Errorf("unexpected fields: ID=%q Role=%q", e.ID, e.Role)
	}
}

func TestSpawnEntryWorktreeFields_Marshal(t *testing.T) {
	// Verify that empty worktree fields are omitted from JSON (omitempty)
	e := SpawnEntry{ID: "s1", Role: "research", SpawnRole: "spawn-11111111", Status: "running"}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "worktree") {
		t.Errorf("expected worktree fields to be omitted, got: %s", string(data))
	}

	// With worktree set, it should appear
	e.Worktree = "/tmp/muxcode-spawn-test/spawn-11111111"
	e.WorktreeRef = "abc123"
	data, err = json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"worktree"`) {
		t.Errorf("expected worktree field in JSON, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"worktree_ref"`) {
		t.Errorf("expected worktree_ref field in JSON, got: %s", string(data))
	}
}

func TestRemoveSpawnWorktree_EmptyPath(t *testing.T) {
	// Should be a no-op for empty path
	if err := removeSpawnWorktree(""); err != nil {
		t.Errorf("removeSpawnWorktree empty path: %v", err)
	}
}

func TestRemoveSpawnWorktree_NonexistentPath(t *testing.T) {
	// Should succeed even if path doesn't exist (graceful)
	if err := removeSpawnWorktree("/tmp/nonexistent-worktree-path-12345"); err != nil {
		t.Errorf("removeSpawnWorktree nonexistent path: %v", err)
	}
}

func TestPruneOrphanedWorktrees_NoBaseDir(t *testing.T) {
	// Should not panic when base dir doesn't exist
	pruneOrphanedWorktrees("nonexistent-session-xyz", nil)
}

func TestPruneOrphanedWorktrees_RemovesOrphans(t *testing.T) {
	session := fmt.Sprintf("test-prune-%d", rand.Int())
	base := SpawnWorktreeBase(session)
	defer os.RemoveAll(base)

	// Create some directories under the base
	activeDir := filepath.Join(base, "spawn-active1")
	orphanDir := filepath.Join(base, "spawn-orphan1")
	if err := os.MkdirAll(activeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orphanDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Only active entry references activeDir
	running := []SpawnEntry{
		{Worktree: activeDir, Status: "running"},
	}

	pruneOrphanedWorktrees(session, running)

	// Active dir should still exist
	if _, err := os.Stat(activeDir); err != nil {
		t.Errorf("expected active dir to still exist: %v", err)
	}
	// Orphan dir should be removed
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Errorf("expected orphan dir to be removed")
	}
}

func TestCleanFinishedSpawns_CleansWorktrees(t *testing.T) {
	session := testSession(t)

	// Create a fake worktree directory for a completed spawn
	wtDir := filepath.Join(t.TempDir(), "fake-worktree")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatal(err)
	}

	testEntries := []SpawnEntry{
		{ID: "s1", SpawnRole: "spawn-11111111", Status: "running"},
		{ID: "s2", SpawnRole: "spawn-22222222", Status: "completed", Worktree: wtDir},
	}
	_ = WriteSpawnEntries(session, testEntries)
	_ = touchFile(InboxPath(session, "spawn-11111111"))
	_ = touchFile(InboxPath(session, "spawn-22222222"))

	removed, err := CleanFinishedSpawns(session)
	if err != nil {
		t.Fatalf("CleanFinishedSpawns: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	// Worktree dir should be removed
	if _, err := os.Stat(wtDir); !os.IsNotExist(err) {
		t.Errorf("expected worktree dir to be removed")
	}
}

func TestFormatSpawnList_ShowsWorktreeColumn(t *testing.T) {
	entries := []SpawnEntry{
		{ID: "s1", Role: "research", SpawnRole: "spawn-11111111", Status: "running", Owner: "edit",
			Task: "task1", Worktree: "/tmp/wt"},
		{ID: "s2", Role: "test", SpawnRole: "spawn-22222222", Status: "running", Owner: "edit",
			Task: "task2"},
	}

	out := FormatSpawnList(entries, true)
	if !strings.Contains(out, "WORKTREE") {
		t.Error("expected WORKTREE header column")
	}
	if !strings.Contains(out, "yes") {
		t.Error("expected 'yes' for spawn with worktree")
	}
	if !strings.Contains(out, "shared") {
		t.Error("expected 'shared' for spawn without worktree")
	}
}

func TestFormatSpawnStatus_ShowsWorktree(t *testing.T) {
	entry := SpawnEntry{
		ID:          "s1",
		Role:        "research",
		SpawnRole:   "spawn-a1b2c3d4",
		Status:      "running",
		Owner:       "edit",
		Window:      "spawn-a1b2c3d4",
		Task:        "Research the topic",
		StartedAt:   time.Now().Unix(),
		Worktree:    "/tmp/muxcode-spawn-test/spawn-a1b2c3d4",
		WorktreeRef: "abc123def456",
	}

	out := FormatSpawnStatus(entry)
	if !strings.Contains(out, "/tmp/muxcode-spawn-test/spawn-a1b2c3d4") {
		t.Error("expected worktree path in output")
	}
	if !strings.Contains(out, "abc123def456") {
		t.Error("expected worktree ref in output")
	}
}

func TestFormatSpawnStatus_SharedWorktree(t *testing.T) {
	entry := SpawnEntry{
		ID:        "s1",
		Role:      "research",
		SpawnRole: "spawn-a1b2c3d4",
		Status:    "running",
		Owner:     "edit",
		Window:    "spawn-a1b2c3d4",
		Task:      "Research the topic",
		StartedAt: time.Now().Unix(),
	}

	out := FormatSpawnStatus(entry)
	if !strings.Contains(out, "shared") {
		t.Error("expected 'shared' for spawn without worktree")
	}
}

func TestInit_CreatesSpawnFile(t *testing.T) {
	session := fmt.Sprintf("test-init-spawn-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := os.Stat(SpawnPath(session)); err != nil {
		t.Errorf("spawn.jsonl: %v", err)
	}
}

// NthSpawnWindowIndex resolves by the spawn- name prefix ordered by
// window_index — the non-contiguous case is the negative control a
// hardcoded -t:11/-t:12 implementation cannot pass (MUX-128).
func TestNthSpawnWindowIndex(t *testing.T) {
	stub := func(census string) {
		orig := tmuxOutputRunner
		t.Cleanup(func() { tmuxOutputRunner = orig })
		tmuxOutputRunner = func(args ...string) (string, error) {
			return census, nil
		}
	}

	// Zero spawns: every slot empty.
	stub("0:research\n1:plan\n2:edit")
	if _, ok := NthSpawnWindowIndex("s", 1); ok {
		t.Error("slot 1 resolved with zero spawn windows")
	}

	// One spawn: slot 1 filled, slot 2 empty.
	stub("0:research\n1:plan\n11:spawn-aaaa1111")
	if idx, ok := NthSpawnWindowIndex("s", 1); !ok || idx != 11 {
		t.Errorf("slot 1 = %d,%v, want 11,true", idx, ok)
	}
	if _, ok := NthSpawnWindowIndex("s", 2); ok {
		t.Error("slot 2 resolved with one spawn window")
	}

	// Three spawns at non-contiguous indices: order by index, not by
	// listing position, and never a hardcoded 11/12.
	stub("1:plan\n14:spawn-cccc3333\n11:spawn-aaaa1111\n17:spawn-dddd4444")
	if idx, ok := NthSpawnWindowIndex("s", 1); !ok || idx != 11 {
		t.Errorf("slot 1 = %d,%v, want 11,true", idx, ok)
	}
	if idx, ok := NthSpawnWindowIndex("s", 2); !ok || idx != 14 {
		t.Errorf("slot 2 = %d,%v, want 14,true", idx, ok)
	}
	if idx, ok := NthSpawnWindowIndex("s", 3); !ok || idx != 17 {
		t.Errorf("slot 3 = %d,%v, want 17,true", idx, ok)
	}

	// n < 1 is never valid.
	if _, ok := NthSpawnWindowIndex("s", 0); ok {
		t.Error("slot 0 must not resolve")
	}
}

// The census must be scoped to the session the caller named — an
// ambient-session query is exactly how F11 could select another
// session's spawn window (review must-fix behind the --session flag).
func TestNthSpawnWindowIndex_QueriesGivenSession(t *testing.T) {
	orig := tmuxOutputRunner
	t.Cleanup(func() { tmuxOutputRunner = orig })
	var gotArgs []string
	tmuxOutputRunner = func(args ...string) (string, error) {
		gotArgs = args
		return "11:spawn-aaaa1111", nil
	}

	if idx, ok := NthSpawnWindowIndex("other-session", 1); !ok || idx != 11 {
		t.Fatalf("resolve = %d,%v, want 11,true", idx, ok)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "-t other-session") {
		t.Errorf("census not scoped to the given session: %v", gotArgs)
	}
}

func TestFindMuxcodeBinary_NotFound(t *testing.T) {
	// Save and clear PATH to test not-found case
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", t.TempDir()) // empty dir
	defer os.Setenv("PATH", origPath)

	// Clear home to prevent finding in config dirs
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	// findMuxcodeBinary falls back to os.Executable(), so it should
	// succeed even with empty PATH/HOME. We just verify it returns
	// a non-empty path (the running test binary).
	p, err := findMuxcodeBinary()
	if err != nil {
		t.Fatalf("expected fallback to os.Executable(), got error: %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path from os.Executable() fallback")
	}
}

// stubSpawnWindow fakes a live window and records kills; returns the
// kill log and restores both seams on cleanup.
func stubSpawnWindow(t *testing.T) *[]string {
	t.Helper()
	origExists, origKill := spawnWindowExistsFn, spawnKillWindowFn
	t.Cleanup(func() { spawnWindowExistsFn, spawnKillWindowFn = origExists, origKill })
	spawnWindowExistsFn = func(string, string) bool { return true }
	var killed []string
	spawnKillWindowFn = func(_, window string) error {
		killed = append(killed, window)
		return nil
	}
	return &killed
}

func TestRefreshSpawnStatus_ReapsRespondedWorker(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	killed := stubSpawnWindow(t)

	seed := NewMessage("daemon", "spawn-aaaa1111", "request", "spawn-task", "implement phase", "")
	if err := CreateDeliveryStatus(session, seed); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}
	MarkResponded(session, seed.ID, "resp-1")

	entries := []SpawnEntry{{
		ID: "s1", Role: "edit", SpawnRole: "spawn-aaaa1111", Window: "spawn-aaaa1111",
		Owner: "daemon", Status: "running", StartedAt: time.Now().Unix(), SeedMsgID: seed.ID,
	}}
	if err := WriteSpawnEntries(session, entries); err != nil {
		t.Fatalf("WriteSpawnEntries: %v", err)
	}

	completed, err := RefreshSpawnStatus(session)
	if err != nil {
		t.Fatalf("RefreshSpawnStatus: %v", err)
	}
	if len(completed) != 1 || completed[0].Status != "completed" {
		t.Fatalf("expected 1 completed entry, got %+v", completed)
	}
	if len(*killed) != 1 || (*killed)[0] != "spawn-aaaa1111" {
		t.Errorf("expected window spawn-aaaa1111 killed, got %v", *killed)
	}
	got, err := ReadSpawnEntries(session)
	if err != nil || len(got) != 1 || got[0].Status != "completed" {
		t.Errorf("persisted status = %+v, err=%v, want completed", got, err)
	}
}

func TestRefreshSpawnStatus_LiveUnansweredWorkerNotReaped(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	killed := stubSpawnWindow(t)

	seed := NewMessage("daemon", "spawn-bbbb2222", "request", "spawn-task", "implement phase", "")
	if err := CreateDeliveryStatus(session, seed); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}

	entries := []SpawnEntry{{
		ID: "s1", Role: "edit", SpawnRole: "spawn-bbbb2222", Window: "spawn-bbbb2222",
		Owner: "daemon", Status: "running", StartedAt: time.Now().Unix(), SeedMsgID: seed.ID,
	}}
	if err := WriteSpawnEntries(session, entries); err != nil {
		t.Fatalf("WriteSpawnEntries: %v", err)
	}

	completed, err := RefreshSpawnStatus(session)
	if err != nil {
		t.Fatalf("RefreshSpawnStatus: %v", err)
	}
	if len(completed) != 0 {
		t.Errorf("unanswered worker reaped: %+v", completed)
	}
	if len(*killed) != 0 {
		t.Errorf("unanswered worker's window killed: %v", *killed)
	}
	got, _ := ReadSpawnEntries(session)
	if len(got) != 1 || got[0].Status != "running" {
		t.Errorf("persisted status = %+v, want running", got)
	}
}

func TestRefreshSpawnStatus_KillFailureLeavesRunning(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)

	origExists, origKill := spawnWindowExistsFn, spawnKillWindowFn
	t.Cleanup(func() { spawnWindowExistsFn, spawnKillWindowFn = origExists, origKill })
	spawnWindowExistsFn = func(string, string) bool { return true }
	spawnKillWindowFn = func(string, string) error { return fmt.Errorf("tmux exited 1") }

	seed := NewMessage("daemon", "spawn-dddd4444", "request", "spawn-task", "implement phase", "")
	if err := CreateDeliveryStatus(session, seed); err != nil {
		t.Fatalf("CreateDeliveryStatus: %v", err)
	}
	MarkResponded(session, seed.ID, "resp-1")

	wt := t.TempDir()
	entries := []SpawnEntry{{
		ID: "s1", Role: "edit", SpawnRole: "spawn-dddd4444", Window: "spawn-dddd4444",
		Owner: "daemon", Status: "running", StartedAt: time.Now().Unix(),
		SeedMsgID: seed.ID, Worktree: wt,
	}}
	if err := WriteSpawnEntries(session, entries); err != nil {
		t.Fatalf("WriteSpawnEntries: %v", err)
	}

	completed, err := RefreshSpawnStatus(session)
	if err != nil {
		t.Fatalf("RefreshSpawnStatus: %v", err)
	}
	if len(completed) != 0 {
		t.Errorf("kill-failed worker marked completed: %+v", completed)
	}
	got, _ := ReadSpawnEntries(session)
	if len(got) != 1 || got[0].Status != "running" {
		t.Errorf("persisted status = %+v, want running", got)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Errorf("worktree removed despite failed kill: %v", statErr)
	}
}

func TestRemoveSpawnWorktree_PreservesDirty(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work.go"), []byte("package x\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := removeSpawnWorktree(dir); err != nil {
		t.Fatalf("removeSpawnWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "work.go")); err != nil {
		t.Errorf("dirty worktree destroyed: %v", err)
	}
}

func TestRemoveSpawnWorktree_PreservesUnknownState(t *testing.T) {
	dir := t.TempDir()
	// A .git file with a dangling gitdir pointer makes `git status` fail:
	// cleanliness is unknowable, and unknown must refuse cleanup.
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nonexistent/nowhere\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := removeSpawnWorktree(dir); err != nil {
		t.Fatalf("removeSpawnWorktree: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("unknown-state worktree destroyed: %v", err)
	}
}

func TestRemoveSpawnWorktree_RemovesClean(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}

	if err := removeSpawnWorktree(dir); err != nil {
		t.Fatalf("removeSpawnWorktree: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("clean worktree not removed (stat err: %v)", err)
	}
}

func TestRefreshSpawnStatus_LegacyEntryKeepsWindowGoneLifecycle(t *testing.T) {
	useTempBusDir(t)
	session := testSession(t)
	killed := stubSpawnWindow(t)

	entries := []SpawnEntry{{
		ID: "s1", Role: "edit", SpawnRole: "spawn-cccc3333", Window: "spawn-cccc3333",
		Owner: "daemon", Status: "running", StartedAt: time.Now().Unix(),
	}}
	if err := WriteSpawnEntries(session, entries); err != nil {
		t.Fatalf("WriteSpawnEntries: %v", err)
	}

	completed, err := RefreshSpawnStatus(session)
	if err != nil {
		t.Fatalf("RefreshSpawnStatus: %v", err)
	}
	if len(completed) != 0 || len(*killed) != 0 {
		t.Errorf("legacy entry with live window was reaped: completed=%+v killed=%v", completed, *killed)
	}
}
