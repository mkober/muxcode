package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// useEmptyShellConfig points MUXCODE_CONFIG at an empty file so config-file
// fallbacks resolve hermetically — never from the developer's real config.
func useEmptyShellConfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	t.Setenv("MUXCODE_CONFIG", path)
}

// forceIdle overrides the injectable idle probe for the test's duration.
func forceIdle(t *testing.T, idle bool) {
	t.Helper()
	orig := autoClearIsIdle
	autoClearIsIdle = func(_, _ string) bool { return idle }
	t.Cleanup(func() { autoClearIsIdle = orig })
}

// eligibleBaseline builds a session in which "review" passes every guard.
func eligibleBaseline(t *testing.T) string {
	t.Helper()
	useTempBusDir(t)
	useEmptyShellConfig(t)
	session := testSession(t)
	t.Setenv("MUXCODE_REVIEW_CLI", "claude")
	forceIdle(t, true)
	if ok, reason := AutoClearEligible(session, "review"); !ok {
		t.Fatalf("baseline not eligible: %s", reason)
	}
	return session
}

func TestAutoClearRoles_DefaultOff(t *testing.T) {
	useEmptyShellConfig(t)
	t.Setenv("MUXCODE_AUTO_CLEAR_ROLES", "")
	if roles := AutoClearRoles(); roles != nil {
		t.Errorf("default should be off, got %v", roles)
	}
}

func TestAutoClearRoles_ParseAndFilter(t *testing.T) {
	useEmptyShellConfig(t)
	t.Setenv("MUXCODE_AUTO_CLEAR_ROLES", "review, Plan,commit,review,edit,auto,bogus,pr-read")
	got := AutoClearRoles()
	want := []string{"review", "plan", "commit"}
	if len(got) != len(want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("roles[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// pr-read normalizes to its host window commit, which is already listed —
	// deduped, not duplicated.
	count := 0
	for _, r := range got {
		if r == "commit" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("commit appears %d times, want 1", count)
	}
}

func TestAutoClearRoles_EditAutoNeverEnrolled(t *testing.T) {
	useEmptyShellConfig(t)
	t.Setenv("MUXCODE_AUTO_CLEAR_ROLES", "edit,auto")
	if roles := AutoClearRoles(); len(roles) != 0 {
		t.Errorf("edit/auto must be filtered at parse, got %v", roles)
	}
}

func TestAutoClearRoles_ConfigFileFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("MUXCODE_AUTO_CLEAR_ROLES=review\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("MUXCODE_CONFIG", path)
	t.Setenv("MUXCODE_AUTO_CLEAR_ROLES", "")
	got := AutoClearRoles()
	if len(got) != 1 || got[0] != "review" {
		t.Errorf("config-file fallback roles = %v, want [review]", got)
	}
}

func TestAutoClearQuietSecs(t *testing.T) {
	useEmptyShellConfig(t)
	t.Setenv("MUXCODE_AUTO_CLEAR_QUIET_SECS", "")
	if got := AutoClearQuietSecs(); got != 60 {
		t.Errorf("default quiet = %d, want 60", got)
	}
	t.Setenv("MUXCODE_AUTO_CLEAR_QUIET_SECS", "5")
	if got := AutoClearQuietSecs(); got != 5 {
		t.Errorf("quiet = %d, want 5", got)
	}
	t.Setenv("MUXCODE_AUTO_CLEAR_QUIET_SECS", "-1")
	if got := AutoClearQuietSecs(); got != 60 {
		t.Errorf("invalid quiet = %d, want default 60", got)
	}
}

// TestAutoClearEligible_EditAutoHardExcluded pins the acceptance criterion
// that edit and auto can never be cleared, independent of config filtering.
func TestAutoClearEligible_EditAutoHardExcluded(t *testing.T) {
	useTempBusDir(t)
	useEmptyShellConfig(t)
	session := testSession(t)
	forceIdle(t, true)
	for _, role := range []string{"edit", "auto"} {
		if ok, reason := AutoClearEligible(session, role); ok {
			t.Errorf("%s must be hard-excluded", role)
		} else if reason == "" {
			t.Errorf("%s exclusion must carry a reason", role)
		}
	}
}

func TestAutoClearEligible_Baseline(t *testing.T) {
	eligibleBaseline(t)
}

func TestAutoClearEligible_HarnessPaneBlocks(t *testing.T) {
	session := eligibleBaseline(t)
	if err := os.WriteFile(HarnessMarkerPath(session, "review"), []byte("123"), 0644); err != nil {
		t.Fatalf("write harness marker: %v", err)
	}
	if ok, _ := AutoClearEligible(session, "review"); ok {
		t.Error("harness pane must block clearing")
	}
}

func TestAutoClearEligible_NonClaudeProviderBlocks(t *testing.T) {
	session := eligibleBaseline(t)
	t.Setenv("MUXCODE_REVIEW_CLI", "opencode")
	if ok, _ := AutoClearEligible(session, "review"); ok {
		t.Error("non-Claude provider must block clearing")
	}
}

func TestAutoClearEligible_ReloadMarkerBlocks(t *testing.T) {
	session := eligibleBaseline(t)
	marker := ReloadMarkerPath(session, "review")
	if err := os.MkdirAll(filepath.Dir(marker), 0755); err != nil {
		t.Fatalf("mkdir lock: %v", err)
	}
	if err := os.WriteFile(marker, []byte("reloading"), 0644); err != nil {
		t.Fatalf("write reload marker: %v", err)
	}
	if ok, _ := AutoClearEligible(session, "review"); ok {
		t.Error("reload marker must block clearing")
	}
}

func TestAutoClearEligible_ActionableInboxBlocks(t *testing.T) {
	session := eligibleBaseline(t)
	if err := Send(session, NewMessage("edit", "review", "request", "review", "check it", "")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ok, _ := AutoClearEligible(session, "review"); ok {
		t.Error("pending actionable inbox must block clearing")
	}
}

func TestAutoClearEligible_InFlightTaskBlocks(t *testing.T) {
	session := eligibleBaseline(t)
	msg := NewMessage("edit", "review", "request", "review", "check it", "")
	if err := CreateTask(session, msg, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if ok, _ := AutoClearEligible(session, "review"); ok {
		t.Error("in-flight task must block clearing")
	}
}

// TestAutoClearEligible_AnsweredInFlightTaskDoesNotBlock pins the "a reply
// implies completion" invariant: bare sends default to --track and only the
// daemon completes tracked tasks, so an answered request's task can sit
// in-flight for a poll cycle (or forever with no daemon). It must not block.
func TestAutoClearEligible_AnsweredInFlightTaskDoesNotBlock(t *testing.T) {
	session := eligibleBaseline(t)
	msg := NewMessage("edit", "review", "request", "review", "check it", "")
	if err := Send(session, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := CreateTask(session, msg, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	resp := NewMessage("review", "edit", "response", "review", "LGTM", msg.ID)
	MarkResponded(session, msg.ID, resp.ID)

	if ok, reason := AutoClearEligible(session, "review"); !ok {
		t.Errorf("answered in-flight task must not block clearing: %s", reason)
	}
}

func TestAutoClearEligible_BusyAgentBlocks(t *testing.T) {
	session := eligibleBaseline(t)
	forceIdle(t, false)
	if ok, _ := AutoClearEligible(session, "review"); ok {
		t.Error("busy agent must block clearing")
	}
}

func TestAutoClearEligible_ModeCycledWindowBlocks(t *testing.T) {
	session := eligibleBaseline(t)
	t.Setenv("MUXCODE_PLAN_CLI", "claude")
	// Default plan mode state (index 0 = plan) is eligible.
	if ok, reason := AutoClearEligible(session, "plan"); !ok {
		t.Fatalf("plan baseline not eligible: %s", reason)
	}
	state := DefaultPlanModeCycleState()
	state.Current = 1 // research mode active
	if err := WriteModeCycleState(session, state); err != nil {
		t.Fatalf("WriteModeCycleState: %v", err)
	}
	if ok, _ := AutoClearEligible(session, "plan"); ok {
		t.Error("window cycled to research mode must block clearing plan")
	}
}

func TestAutoClearDue_MarkerGatesRefire(t *testing.T) {
	useTempBusDir(t)
	useEmptyShellConfig(t)
	session := testSession(t)
	now := time.Now().Unix()

	// No completed work → not due.
	if due, _ := AutoClearDue(session, "review", now, 0); due {
		t.Error("due with no completed work")
	}

	// Completed task → due once the quiet window elapses.
	msg := NewMessage("edit", "review", "request", "review", "check it", "")
	if err := CreateTask(session, msg, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	CompleteTask(session, msg.ID, "resp-1")
	if due, _ := AutoClearDue(session, "review", now, 0); !due {
		t.Error("not due after completed task with zero quiet window")
	}
	// Quiet window not yet elapsed → not due.
	if due, _ := AutoClearDue(session, "review", now, 3600); due {
		t.Error("due before quiet window elapsed")
	}

	// Marker written after completion → the same task never re-fires.
	if err := WriteAutoClearMarker(session, "review"); err != nil {
		t.Fatalf("WriteAutoClearMarker: %v", err)
	}
	if due, _ := AutoClearDue(session, "review", now+3600, 0); due {
		t.Error("marker must gate re-fire for already-cleared work")
	}
}

func TestLastTaskCompletion_RespondedDeliveryStatus(t *testing.T) {
	useTempBusDir(t)
	useEmptyShellConfig(t)
	session := testSession(t)

	// A chain-style request (no task entry) that the role answered: the
	// responded delivery status must count as completed work.
	req := NewMessage("edit", "review", "request", "review", "check it", "")
	if err := Send(session, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp := NewMessage("review", "edit", "response", "review", "LGTM", req.ID)
	MarkResponded(session, req.ID, resp.ID)

	got := LastTaskCompletion(session, "review")
	want := msgIDTimestamp(resp.ID)
	if want == 0 {
		t.Fatalf("unparseable response ID %q", resp.ID)
	}
	if got != want {
		t.Errorf("LastTaskCompletion = %d, want %d", got, want)
	}
	// The response was work completed BY review — it must not register as
	// completed work for the responder's peer (edit is excluded anyway, but
	// the recipient attribution matters for any role pair).
	if got := LastTaskCompletion(session, "build"); got != 0 {
		t.Errorf("unrelated role completion = %d, want 0", got)
	}
}

func TestClearAgent_InjectsAndWritesMarker(t *testing.T) {
	useTempBusDir(t)
	useEmptyShellConfig(t)
	session := testSession(t)

	var injected []string
	orig := autoClearInject
	autoClearInject = func(target string) error {
		injected = append(injected, target)
		return nil
	}
	t.Cleanup(func() { autoClearInject = orig })

	if err := ClearAgent(session, "review", "daemon", "test-trigger"); err != nil {
		t.Fatalf("ClearAgent: %v", err)
	}
	if len(injected) != 1 || injected[0] != PaneTarget(session, "review") {
		t.Errorf("injected = %v, want [%s]", injected, PaneTarget(session, "review"))
	}
	if ReadAutoClearMarker(session, "review") == 0 {
		t.Error("marker not written after clear")
	}
}

func TestClearAgent_InjectFailureSkipsMarker(t *testing.T) {
	useTempBusDir(t)
	useEmptyShellConfig(t)
	session := testSession(t)

	orig := autoClearInject
	autoClearInject = func(_ string) error { return fmt.Errorf("boom") }
	t.Cleanup(func() { autoClearInject = orig })

	if err := ClearAgent(session, "review", "daemon", "test-trigger"); err == nil {
		t.Fatal("ClearAgent should propagate inject failure")
	}
	if ReadAutoClearMarker(session, "review") != 0 {
		t.Error("marker must not be written when injection failed")
	}
}

func TestMsgIDTimestamp(t *testing.T) {
	if got := msgIDTimestamp("1787602592-commit-e5bdc901"); got != 1787602592 {
		t.Errorf("got %d, want 1787602592", got)
	}
	for _, bad := range []string{"", "-x-y", "abc-def", "nodash"} {
		if got := msgIDTimestamp(bad); got != 0 {
			t.Errorf("msgIDTimestamp(%q) = %d, want 0", bad, got)
		}
	}
}
