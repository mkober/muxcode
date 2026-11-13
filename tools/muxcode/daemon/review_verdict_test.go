package daemon

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Tests for MUX-172: the spec-verification gate reads the review's own
// verdict — the reply's EXIT sentinel — instead of asking NotifyPlanOn about
// a literal "success".

// setNotifyPlanOn pins the review chain's NotifyPlanOn for one test, so a
// user or project config on the machine cannot decide the outcome.
func setNotifyPlanOn(t *testing.T, on []string) {
	t.Helper()
	cfg := bus.DefaultConfig()
	chain := cfg.EventChains["review"]
	chain.NotifyPlanOn = on
	cfg.EventChains["review"] = chain
	bus.SetConfig(cfg)
	t.Cleanup(func() { bus.SetConfig(nil) })
}

// seedReviewRow appends a self-reported review history row stamped at ts —
// the row `muxcode log review … --exit-code N` writes, which review records
// after it has already replied.
func seedReviewRow(t *testing.T, session, outcome string, exit int, ts int64) {
	t.Helper()
	line := fmt.Sprintf(`{"ts":%d,"outcome":%q,"exit_code":%d,"source":%q,"summary":"seeded review row"}`+"\n",
		ts, outcome, exit, bus.SourceSelfReported)
	f, err := os.OpenFile(bus.HistoryPath(session, "review"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open review history: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("write review history: %v", err)
	}
}

// withheldRows returns the details of the session's plan-verify-withheld rows.
func withheldRows(t *testing.T, session string) []string {
	t.Helper()
	entries, err := bus.ReadLifecycleLog(session)
	if err != nil {
		t.Fatalf("read lifecycle: %v", err)
	}
	var details []string
	for _, e := range entries {
		if e.Event == "plan-verify-withheld" {
			details = append(details, e.Detail)
		}
	}
	return details
}

// reviewSession is a session with an active in-repo spec, a pinned
// NotifyPlanOn and the dedup window off.
func reviewSession(t *testing.T, on []string) (string, *Daemon) {
	t.Helper()
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	setNotifyPlanOn(t, on)
	session := testSession(t)
	seedRepoSpec(t, session)
	return session, New(session, 5, 2)
}

// reviewReply builds a review→edit reply.
func reviewReply(payload, replyTo string) bus.Message {
	return bus.NewMessage("review", "edit", "response", "review", payload, replyTo)
}

// deliver seeds a reply into edit's inbox and runs one inbox pass.
func deliver(t *testing.T, session string, d *Daemon, reply bus.Message) bus.Message {
	t.Helper()
	seedEditInbox(t, session, reply)
	d.checkInboxes()
	return reply
}

const (
	passingReview = "0 must-fix, 0 should-fix, 0 nits\n\nEXIT=0"
	failingReview = "2 must-fix, 1 should-fix, 0 nits\n\nEXIT=1"
)

func TestReviewVerdict_FailureWithholds(t *testing.T) {
	session, d := reviewSession(t, []string{"success"})

	reply := deliver(t, session, d, reviewReply(failingReview, "req-1"))

	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("a failing review fired %d verify-spec, want 0", got)
	}
	rows := withheldRows(t, session)
	if len(rows) != 1 || rows[0] != "failure "+reply.ID+" (EXIT sentinel)" {
		t.Errorf("want one plan-verify-withheld row naming failure and %s, got %q", reply.ID, rows)
	}
	if st := bus.ReadWorkflowState(session).State; st != bus.StateReviewed {
		t.Errorf("a failing review must still transition to reviewed, got %s", st)
	}
	if got := bus.ReadReviewedMarker(session); got != reply.ID {
		t.Errorf("reviewed marker = %q, want %q", got, reply.ID)
	}
	if got := bus.ReadVerifyMovementMarker(session); got != "" {
		t.Errorf("a withheld fire wrote the movement marker %q", got)
	}
}

// Negative control: a passing review fires exactly as before — including
// one whose only findings are should-fixes, which review records as a pass.
func TestReviewVerdict_PassingFires(t *testing.T) {
	for _, payload := range []string{passingReview, "0 must-fix, 2 should-fix, 0 nits\n\nEXIT=0"} {
		t.Run(firstLine(payload), func(t *testing.T) {
			session, d := reviewSession(t, []string{"success"})

			deliver(t, session, d, reviewReply(payload, "req-1"))

			if got := countVerifySpec(t, session); got != 1 {
				t.Errorf("a passing review fired %d verify-spec, want 1", got)
			}
			if rows := withheldRows(t, session); len(rows) != 0 {
				t.Errorf("a passing review logged withheld rows %q", rows)
			}
		})
	}
}

// Fail closed: a reply that does not establish its own verdict fires nothing,
// even under ["*"] — and a history row cannot supply one.
func TestReviewVerdict_UnknownWithholds(t *testing.T) {
	cases := []struct {
		name, payload, reason string
		row                   string
	}{
		{"no sentinel", "looks fine to me", "unknown verdict", ""},
		{"no sentinel beside a passing history row", "looks fine to me", "unknown verdict", bus.OutcomeSuccess},
		{"EXIT=0 beside a must-fix count", "1 must-fix, 0 should-fix, 0 nits\n\nEXIT=0", "conflicting verdict", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session, d := reviewSession(t, []string{"*"})
			if tc.row != "" {
				seedReviewRow(t, session, tc.row, 0, time.Now().Unix())
			}

			reply := deliver(t, session, d, reviewReply(tc.payload, "req-1"))

			if got := countVerifySpec(t, session); got != 0 {
				t.Errorf("an unknown verdict fired %d verify-spec, even under [\"*\"]", got)
			}
			rows := withheldRows(t, session)
			if len(rows) != 1 || !strings.HasPrefix(rows[0], "unknown "+reply.ID) || !strings.Contains(rows[0], tc.reason) {
				t.Errorf("want one row \"unknown %s … %s\", got %q", reply.ID, tc.reason, rows)
			}
			if st := bus.ReadWorkflowState(session).State; st != bus.StateReviewed {
				t.Errorf("an unknown verdict must still transition to reviewed, got %s", st)
			}
		})
	}
}

// Review replies before it logs, so the newest history row can belong to the
// previous review. Neither a late success nor a late failure from review A
// may decide review B (MUX-172 review must-fix).
func TestReviewVerdict_PreviousReviewsLateRowIgnored(t *testing.T) {
	t.Run("late success does not pass a sentinel-less reply", func(t *testing.T) {
		session, d := reviewSession(t, []string{"success"})
		a := deliver(t, session, d, reviewReply(passingReview, "req-a"))
		if got := countVerifySpec(t, session); got != 1 {
			t.Fatalf("review A fired %d verify-spec, want 1", got)
		}
		drainPlanInbox(t, session)
		seedReviewRow(t, session, bus.OutcomeSuccess, 0, a.TS+1)

		b := deliver(t, session, d, reviewReply("looks fine to me", "req-b"))

		if got := countVerifySpec(t, session); got != 0 {
			t.Errorf("review B inherited A's late success row: %d verify-spec", got)
		}
		if rows := withheldRows(t, session); len(rows) != 1 || !strings.HasPrefix(rows[0], "unknown "+b.ID) {
			t.Errorf("want B withheld as unknown, got %q", rows)
		}
	})
	t.Run("late failure does not withhold a passing reply", func(t *testing.T) {
		session, d := reviewSession(t, []string{"success"})
		a := deliver(t, session, d, reviewReply(failingReview, "req-a"))
		seedReviewRow(t, session, bus.OutcomeFailure, 1, a.TS+1)

		deliver(t, session, d, reviewReply(passingReview, "req-b"))

		if got := countVerifySpec(t, session); got != 1 {
			t.Errorf("A's late failure row withheld passing review B: %d verify-spec, want 1", got)
		}
	})
}

// Two reviews in the same second are told apart by reply id, never by time.
func TestReviewVerdict_SameSecondConsecutiveReviews(t *testing.T) {
	session, d := reviewSession(t, []string{"success"})
	a := reviewReply(failingReview, "req-a")
	b := reviewReply(passingReview, "req-b")
	b.TS = a.TS
	seedReviewRow(t, session, bus.OutcomeFailure, 1, a.TS)

	deliver(t, session, d, a)
	if got := countVerifySpec(t, session); got != 0 {
		t.Fatalf("failing review A fired %d verify-spec", got)
	}
	deliver(t, session, d, b)

	if got := countVerifySpec(t, session); got != 1 {
		t.Errorf("passing review B in A's second fired %d verify-spec, want 1", got)
	}
	if got := bus.ReadReviewedMarker(session); got != b.ID {
		t.Errorf("reviewed marker = %q, want B's %q", got, b.ID)
	}
	rows := withheldRows(t, session)
	if len(rows) != 1 || !strings.HasPrefix(rows[0], "failure "+a.ID) {
		t.Errorf("want only A withheld, got %q", rows)
	}
}

// A withheld failure leaves the movement marker unwritten, so the next
// passing review still fires.
func TestReviewVerdict_LaterPassingReviewFires(t *testing.T) {
	session, d := reviewSession(t, []string{"success"})
	deliver(t, session, d, reviewReply(failingReview, "req-1"))
	if got := countVerifySpec(t, session); got != 0 {
		t.Fatalf("failing review fired %d verify-spec", got)
	}

	deliver(t, session, d, reviewReply(passingReview, "req-2"))

	if got := countVerifySpec(t, session); got != 1 {
		t.Errorf("passing review after a withheld one fired %d verify-spec, want 1", got)
	}
}

// NotifyPlanOn is load-bearing: ["success"] and ["*"] must disagree on a
// failing review — the assertion the hardcoded "success" could never pass —
// and [] fires on neither.
func TestReviewVerdict_NotifyPlanOnMatrix(t *testing.T) {
	cases := []struct {
		on        []string
		payload   string
		wantFires int
	}{
		{[]string{"success"}, failingReview, 0},
		{[]string{"success"}, passingReview, 1},
		{[]string{"*"}, failingReview, 1},
		{[]string{"*"}, passingReview, 1},
		{[]string{}, failingReview, 0},
		{[]string{}, passingReview, 0},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%v/%s", tc.on, firstLine(tc.payload)), func(t *testing.T) {
			session, d := reviewSession(t, tc.on)

			deliver(t, session, d, reviewReply(tc.payload, "req-1"))

			if got := countVerifySpec(t, session); got != tc.wantFires {
				t.Errorf("NotifyPlanOn %v fired %d verify-spec, want %d", tc.on, got, tc.wantFires)
			}
		})
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
