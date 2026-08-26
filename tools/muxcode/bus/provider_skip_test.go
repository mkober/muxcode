package bus

import (
	"errors"
	"os"
	"testing"
	"time"
)

// skipTestSetup isolates a bus session and plants an aged in-flight task
// for the role, so the SendWakeUp in-flight guard fires. The guard runs
// before any tmux access, so no pane stubbing is needed.
func skipTestSetup(t *testing.T, session, role string) {
	t.Helper()
	t.Setenv("BUS_SESSION", session)
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(BusDir(session)) })

	m := Message{
		ID: "aged-task-1", From: "edit", To: role, Type: "request",
		Action: "run", Payload: "prior work", TS: time.Now().Unix() - 60,
	}
	if err := CreateTask(session, m, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

// A skipped injection must be distinguishable from a delivered one — the
// nil-on-skip return made the receipt-gap backstop record re-drives that
// never happened (the 2026-08-26 incident behind MUX-105).
func TestOpenCodeSendWakeUp_SkipReturnsSentinel(t *testing.T) {
	session := "skip-test-opencode"
	skipTestSetup(t, session, "build")

	err := (&OpenCodeProvider{}).SendWakeUp(session, "build", false)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Errorf("expected ErrInjectionSkipped, got %v", err)
	}
}

// The codex guard is the same skip — it must carry the same sentinel.
func TestCodexSendWakeUp_SkipReturnsSentinel(t *testing.T) {
	session := "skip-test-codex"
	skipTestSetup(t, session, "review")

	err := (&CodexProvider{}).SendWakeUp(session, "review", false)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Errorf("expected ErrInjectionSkipped, got %v", err)
	}
}

// force bypasses the guard entirely: with an aged in-flight task and an
// empty inbox, a forced wake-up reaches the nothing-to-inject nil path
// instead of the skip sentinel — proving the guard, not the call, is
// what force disables. No tmux is touched on either path.
func TestSendWakeUp_ForceBypassesGuard(t *testing.T) {
	session := "skip-test-force"
	skipTestSetup(t, session, "build")

	if err := (&OpenCodeProvider{}).SendWakeUp(session, "build", true); errors.Is(err, ErrInjectionSkipped) {
		t.Errorf("force must bypass the in-flight skip, got %v", err)
	}
	if err := (&CodexProvider{}).SendWakeUp(session, "build", true); errors.Is(err, ErrInjectionSkipped) {
		t.Errorf("codex force must bypass the in-flight skip, got %v", err)
	}
}

// A young in-flight task (<5s) does not trigger the guard — the negative
// control proving the sentinel comes from the skip, not from every call.
func TestSendWakeUp_YoungTaskDoesNotSkip(t *testing.T) {
	session := "skip-test-young"
	t.Setenv("BUS_SESSION", session)
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(BusDir(session)) })

	m := Message{
		ID: "young-task-1", From: "edit", To: "build", Type: "request",
		Action: "run", Payload: "just sent", TS: time.Now().Unix(),
	}
	if err := CreateTask(session, m, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// With no pending inbox the wake-up is a no-op nil — but it must not
	// be the skip sentinel.
	err := (&OpenCodeProvider{}).SendWakeUp(session, "build", false)
	if errors.Is(err, ErrInjectionSkipped) {
		t.Errorf("a young task must not trigger the skip guard, got %v", err)
	}
}

// A non-forced deliver must surface the skip and roll back the notified
// markers — claiming "woke N" while the guard swallowed the injection is
// exactly the false success the sentinel exists to kill.
func TestForceDeliver_NonForceSkipRollsBackAndSurfaces(t *testing.T) {
	session := "deliver-test-skip"
	deliverTestSetup(t, session, "❯ \n")       // idle prompt satisfies the non-force gate
	t.Setenv(RoleCLIEnvVar("run"), "opencode") // non-hook path → SendWakeUp guard

	sendTestRequest(t, session, "run", "MSG-SKIP")

	aged := Message{
		ID: "aged-task-2", From: "edit", To: "run", Type: "request",
		Action: "run", Payload: "prior work", TS: time.Now().Unix() - 60,
	}
	if err := CreateTask(session, aged, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err := ForceDeliver(session, "run", false)
	if !errors.Is(err, ErrInjectionSkipped) {
		t.Fatalf("expected the skip surfaced as ErrInjectionSkipped, got %v", err)
	}
	if got := UnnotifiedMessages(session, "run"); len(got) != 1 {
		t.Errorf("expected notified markers rolled back after a skip, unnotified=%d", len(got))
	}
}

// ForceDeliver with force must bypass the in-flight skip and inject —
// the 2026-08-26 catch-22 where the stuck request blocked its own
// recovery until the task timeout.
func TestForceDeliver_ForceBypassesInFlightSkip(t *testing.T) {
	session := "deliver-test-force-bypass"
	calls := deliverTestSetup(t, session, "❯ \n")
	t.Setenv(RoleCLIEnvVar("run"), "opencode")

	sendTestRequest(t, session, "run", "MSG-FORCE")

	aged := Message{
		ID: "aged-task-3", From: "edit", To: "run", Type: "request",
		Action: "run", Payload: "prior work", TS: time.Now().Unix() - 60,
	}
	if err := CreateTask(session, aged, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	res, err := ForceDeliver(session, "run", true)
	if err != nil {
		t.Fatalf("forced deliver must bypass the skip, got %v", err)
	}
	if res.Delivered != 1 {
		t.Fatalf("expected 1 delivered, got %+v", res)
	}
	sawInjection := false
	for _, c := range *calls {
		if len(c) > 0 && c[0] == "send-keys" {
			sawInjection = true
		}
	}
	if !sawInjection {
		t.Error("expected a real send-keys injection under force, saw none")
	}
}
