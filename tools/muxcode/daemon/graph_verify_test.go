package daemon

import (
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

const graphVerifyRun = "1794666397-55-defect-backlog-loop-x"

// graphVerifySession is a scratch session with a spec to verify, its own
// lifecycle log, and the dedup window and consumer guard off.
func graphVerifySession(t *testing.T) (string, *Daemon) {
	t.Helper()
	t.Setenv("MUXCODE_DEDUP_WINDOW", "0")
	t.Setenv("MUXCODE_CONSUMER_GUARD", "0")
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	session := testSession(t)
	seedRepoSpec(t, session)
	return session, New(session, 5, 2)
}

// reviewRequest logs a review request, stamped with a graph run and node when
// run is set, as the executor stamps its dispatch.
func reviewRequest(t *testing.T, session, run string) bus.Message {
	t.Helper()
	req := bus.NewMessage("daemon", "review", "request", "review", "Review the latest changes", "")
	if run != "" {
		req.GraphRun, req.GraphNode = run, "review"
	}
	if err := bus.SendForce(session, req); err != nil {
		t.Fatal(err)
	}
	return req
}

func lifecycleEvents(t *testing.T, session, event string) []string {
	t.Helper()
	rows, err := bus.ReadLifecycleLog(session)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if r.Event == event {
			out = append(out, r.Detail)
		}
	}
	return out
}

// MUX-219, the Phase 1 pin inverted: a review the graph executor dispatched —
// its request stamped with GraphRun and GraphNode — sends plan no verify-spec
// from the daemon, whatever its verdict: the run's update-spec node verifies a
// clean review, its fix node takes one with findings. One plan-verify-graph-owned
// row names the run and node, distinct from the "nothing moved"
// plan-verify-suppressed row; the reviewed marker and transition still happen,
// and the movement marker stays unwritten. On run 1794626267 plan verified every
// phase twice in one inbox read.
func TestCheckInboxes_GraphOwnedReviewSendsNoVerifySpec(t *testing.T) {
	for _, tc := range []struct{ name, verdict string }{
		{"clean, routed to update-spec", "0 must-fix, 0 should-fix, 0 nits\nEXIT=0"},
		{"a should-fix, routed to fix", "0 must-fix, 1 should-fix, 0 nits\nEXIT=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, d := graphVerifySession(t)
			req := reviewRequest(t, session, graphVerifyRun)
			reply := bus.NewMessage("review", "edit", "response", "review", tc.verdict, req.ID)
			seedEditInbox(t, session, reply)

			d.checkInboxes()
			if got := countVerifySpec(t, session); got != 0 {
				t.Errorf("verify-spec sent %d time(s) for a graph-owned review, want 0 — the run verifies it", got)
			}
			rows := lifecycleEvents(t, session, "plan-verify-graph-owned")
			if len(rows) != 1 || !strings.Contains(rows[0], graphVerifyRun) || !strings.Contains(rows[0], "node review") || !strings.Contains(rows[0], reply.ID) {
				t.Errorf("want one plan-verify-graph-owned row naming the run, node and review: %q", rows)
			}
			if n := lifecycleEvents(t, session, "plan-verify-suppressed"); len(n) != 0 {
				t.Errorf("the graph suppression wrote the 'nothing moved' row: %q", n)
			}
			if st := bus.ReadWorkflowState(session).State; st != bus.StateReviewed {
				t.Errorf("workflow %s, want reviewed — the transition is unchanged", st)
			}
			if got := bus.ReadReviewedMarker(session); got != reply.ID {
				t.Errorf("reviewed marker %q, want %q", got, reply.ID)
			}
			if m := bus.ReadVerifyMovementMarker(session); m != "" {
				t.Errorf("movement marker %q written for a withheld fire", m)
			}
		})
	}
}

// The task record is a second source: a dispatch whose log line is gone but
// whose task carries the run and node is still graph-owned.
func TestCheckInboxes_GraphOwnedByTaskRecord(t *testing.T) {
	session, d := graphVerifySession(t)
	req := bus.NewMessage("daemon", "review", "request", "review", "Review", "")
	req.GraphRun, req.GraphNode = graphVerifyRun, "review"
	if err := bus.CreateTask(session, req, 600); err != nil {
		t.Fatal(err)
	}
	seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "0 must-fix, 0 should-fix, 0 nits\nEXIT=0", req.ID))
	d.checkInboxes()
	if got := countVerifySpec(t, session); got != 0 {
		t.Errorf("verify-spec sent %d time(s); the task record names a graph run", got)
	}
}

// Negative controls: a hand-delegated review — its request unstamped, or a
// reply naming no request at all — fires exactly one verify-spec, as before;
// and one following a graph-owned review still fires, since the withheld fire
// left the movement marker unwritten. Ownership is the request's stamp, not
// whether some run exists: a run parked on a gate does not silence the review
// edit requested by hand.
func TestCheckInboxes_HandDelegatedReviewStillFires(t *testing.T) {
	t.Run("unstamped request", func(t *testing.T) {
		session, d := graphVerifySession(t)
		req := reviewRequest(t, session, "")
		seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "0 must-fix, 0 should-fix, 0 nits\nEXIT=0", req.ID))
		d.checkInboxes()
		if got := countVerifySpec(t, session); got != 1 {
			t.Errorf("verify-spec sent %d time(s), want 1", got)
		}
	})
	t.Run("no reply-to", func(t *testing.T) {
		session, d := graphVerifySession(t)
		seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "0 must-fix, 0 should-fix, 0 nits\nEXIT=0", ""))
		d.checkInboxes()
		if got := countVerifySpec(t, session); got != 1 {
			t.Errorf("verify-spec sent %d time(s), want 1", got)
		}
	})
	t.Run("after a graph-owned review", func(t *testing.T) {
		session, d := graphVerifySession(t)
		owned := reviewRequest(t, session, graphVerifyRun)
		seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "0 must-fix, 0 should-fix, 0 nits\nEXIT=0", owned.ID))
		d.checkInboxes()
		if got := countVerifySpec(t, session); got != 0 {
			t.Fatalf("fixture: the graph-owned review fired %d", got)
		}
		hand := reviewRequest(t, session, "")
		seedEditInbox(t, session, bus.NewMessage("review", "edit", "response", "review", "0 must-fix, 0 should-fix, 0 nits\nEXIT=0 (by hand)", hand.ID))
		d.checkInboxes()
		if got := countVerifySpec(t, session); got != 1 {
			t.Errorf("a hand review after a graph-owned one fired %d verify-spec, want 1", got)
		}
	})
}
