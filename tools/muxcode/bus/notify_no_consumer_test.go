package bus

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

func eventRows(t *testing.T, session, event string) []string {
	t.Helper()
	rows, err := FilterLifecycleLog(session, LifecycleFilterOpts{Event: event})
	if err != nil {
		t.Fatalf("FilterLifecycleLog: %v", err)
	}
	var details []string
	for _, r := range rows {
		details = append(details, r.Detail)
	}
	return details
}

func triggerWritten(session, role string) bool {
	_, err := os.Stat(TriggerNotifyPath(session, role))
	return err == nil
}

// TestNotifySkipsRoleWithNoConsumer is MUX-180's notify half: a role nothing
// can read for is not notified — no trigger file, so no injection follows —
// and the skip is reported once per episode, not per attempt. When a consumer
// appears the role is notified again, and a later absence is a new episode
// with its own row.
func TestNotifySkipsRoleWithNoConsumer(t *testing.T) {
	session := windowlessSession(t, "opencode")
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	stubWindows(t, []string{"edit", "build"}, nil)

	for i := 0; i < 3; i++ {
		if err := Notify(session, "analyze"); err != nil {
			t.Fatalf("Notify %d: %v", i, err)
		}
	}
	if triggerWritten(session, "analyze") {
		t.Error("a role with no consumer got a trigger file")
	}
	if rows := eventRows(t, session, "notify-no-consumer"); len(rows) != 1 || !strings.HasPrefix(rows[0], "analyze:") {
		t.Errorf("want one notify-no-consumer row for three skips, got %q", rows)
	}

	stubWindows(t, []string{"edit", "analyze"}, nil)
	_ = Notify(session, "analyze")
	if !triggerWritten(session, "analyze") {
		t.Error("once its window exists the role must be notified again")
	}
	stubWindows(t, []string{"edit"}, nil)
	_ = Notify(session, "analyze")
	if rows := eventRows(t, session, "notify-no-consumer"); len(rows) != 2 {
		t.Errorf("a second absence must be reported again: %d rows, want 2", len(rows))
	}
}

// TestNotifyStillNotifiesARoleWithAConsumer is the negative control: a window
// is not the test (Decision 3). A role with its window, a windowless role with
// its own listener or a running headless agent, and any role when the window
// list cannot be read are all notified — the trigger file is written — with
// no notify-no-consumer row.
func TestNotifyStillNotifiesARoleWithAConsumer(t *testing.T) {
	cases := []struct {
		name    string
		windows []string
		listErr error
		setup   func(t *testing.T, session string)
	}{
		{name: "its window", windows: []string{"edit", "analyze"}},
		{name: "its own listener", windows: []string{"edit"},
			setup: func(t *testing.T, s string) { SetPolling(s, "analyze") }},
		{name: "a running headless agent", windows: []string{"edit"}, setup: func(t *testing.T, s string) {
			if err := os.WriteFile(HarnessMarkerPath(s, "analyze"), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "an unreadable window list", listErr: errors.New("no server")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			session := windowlessSession(t, "opencode")
			t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
			stubWindows(t, c.windows, c.listErr)
			if c.setup != nil {
				c.setup(t, session)
			}
			if err := Notify(session, "analyze"); err != nil {
				t.Fatalf("Notify: %v", err)
			}
			if !triggerWritten(session, "analyze") {
				t.Error("a role with a consumer was not notified")
			}
			if rows := eventRows(t, session, "notify-no-consumer"); len(rows) != 0 {
				t.Errorf("notify-no-consumer fired with a consumer present: %q", rows)
			}
		})
	}
}

// TestInjectionRefusedSurvivesForAPresentWindow is MUX-180 Phase 3, the
// load-bearing control: skipping a role with no consumer must not silence the
// injection guard for a role whose window is present but whose pane capture
// fails — a dead agent's window, whose shell would run an injected payload.
// An absent window is skipped at Notify with no injection-refused row; a
// present window passes Notify's consumer check and its wake-up still reaches
// the guard, which refuses on the failed capture and logs injection-refused.
// The wake-up is driven through notifySendKeys, the step Notify reaches in a
// live session, because Notify's tmux has-session guard cannot be stubbed;
// scripts cover Notify end to end.
func TestInjectionRefusedSurvivesForAPresentWindow(t *testing.T) {
	t.Run("absent window: skipped, no injection-refused", func(t *testing.T) {
		session := windowlessSession(t, "opencode")
		t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
		stubWindows(t, []string{"edit"}, nil)
		_ = Notify(session, "analyze")
		if rows := eventRows(t, session, "injection-refused"); len(rows) != 0 {
			t.Errorf("an absent window raised injection-refused: %q", rows)
		}
		if rows := eventRows(t, session, "notify-no-consumer"); len(rows) != 1 {
			t.Errorf("an absent window must be reported once as notify-no-consumer, got %q", rows)
		}
	})
	t.Run("present window, capture fails: injection-refused fires", func(t *testing.T) {
		session := windowlessSession(t, "opencode")
		t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
		stubWindows(t, []string{"edit", "analyze"}, nil)
		t.Setenv(consumerGuardEnv, "0")
		fillInbox(t, session, "daemon", "analyze", 1)
		t.Setenv(consumerGuardEnv, "")
		_ = Notify(session, "analyze")
		if !triggerWritten(session, "analyze") {
			t.Fatal("a present window must pass Notify's consumer check")
		}
		_ = notifySendKeys(session, "analyze")
		rows := eventRows(t, session, "injection-refused")
		if len(rows) != 1 || !strings.Contains(rows[0], "analyze: pane capture failed") {
			t.Errorf("a present window whose capture fails must log injection-refused, got %q", rows)
		}
		if rows := eventRows(t, session, "notify-no-consumer"); len(rows) != 0 {
			t.Errorf("a present window was reported as having no consumer: %q", rows)
		}
	})
}

// TestRouteToRole is MUX-180 Decision 2: the daemon's analyze route does not
// hand work — nor move the workflow into analyzing — to a role nothing can
// read for, and logs the skip once per episode; a role with a consumer, or an
// unreadable window list, is routed as before.
func TestRouteToRole(t *testing.T) {
	session := windowlessSession(t, "opencode")
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	stubWindows(t, []string{"edit"}, nil)
	for i := 0; i < 2; i++ {
		if RouteToRole(session, "analyze", "analyze-route") {
			t.Fatalf("cycle %d routed to a role with no consumer", i)
		}
	}
	if rows := eventRows(t, session, "route-no-consumer"); len(rows) != 1 || !strings.Contains(rows[0], "analyze-route skipped") {
		t.Errorf("want one route-no-consumer row naming the route, got %q", rows)
	}

	for _, c := range []struct {
		name    string
		windows []string
		listErr error
	}{
		{name: "its window", windows: []string{"edit", "analyze"}},
		{name: "an unreadable window list", listErr: errors.New("no server")},
	} {
		stubWindows(t, c.windows, c.listErr)
		if !RouteToRole(session, "analyze", "analyze-route") {
			t.Errorf("%s: a role with a consumer was not routed", c.name)
		}
	}
	stubWindows(t, []string{"edit"}, nil)
	RouteToRole(session, "analyze", "analyze-route")
	if rows := eventRows(t, session, "route-no-consumer"); len(rows) != 2 {
		t.Errorf("a consumer appearing must end the episode, so the next absence logs again: %d rows", len(rows))
	}
}
