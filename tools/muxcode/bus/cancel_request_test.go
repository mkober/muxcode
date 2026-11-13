package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cancelRequestPath(runID string) string {
	return filepath.Join(GraphRunDir(runTestSession, runID), cancelRequestFile)
}

func runState(t *testing.T, runID string) string {
	t.Helper()
	r, err := ReadGraphRun(runTestSession, runID)
	if err != nil {
		t.Fatal(err)
	}
	return r.State
}

// The remote-control road end to end: a granted agent's cancel of the user's
// run only files a request; a process whose authority is unsealed never acts
// on one; the daemon's sealed list does, and the canceled row names the agent.
func TestDelegatedCancelDecidedBySealedAuthority(t *testing.T) {
	unsealGateAuthority(t)
	pinGateAuthorityConfig(t, "user,edit")
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	pinActor(t, "edit")
	if err := CancelGraphRun(runTestSession, run.ID); !errors.Is(err, ErrCancelDelegated) {
		t.Fatalf("granted edit's cancel of the user's run: err %v, want ErrCancelDelegated", err)
	}
	if st := runState(t, run.ID); st != GraphRunRunning {
		t.Fatalf("the CLI acted on a delegated cancel: run %q", st)
	}

	ProcessCancelRequests(runTestSession)
	if st := runState(t, run.ID); st != GraphRunRunning {
		t.Errorf("an unsealed process honoured a delegated cancel: run %q", st)
	}
	if _, err := os.Stat(cancelRequestPath(run.ID)); !os.IsNotExist(err) {
		t.Error("a refused request was left to be retried")
	}

	if err := CancelGraphRun(runTestSession, run.ID); !errors.Is(err, ErrCancelDelegated) {
		t.Fatalf("second request: %v", err)
	}
	SealGateAuthority()
	ProcessCancelRequests(runTestSession)
	if st := runState(t, run.ID); st != GraphRunCanceled {
		t.Fatalf("the sealed daemon refused a granted request: run %q", st)
	}
	done := cancelEvents(t, "graph-run-canceled", run.ID)
	if len(done) != 1 || done[0].Source != "edit" || !strings.Contains(done[0].Detail, "canceled by edit") {
		t.Errorf("graph-run-canceled must name the delegating agent, got %+v", done)
	}
}

// A config widened after the seal grants nothing: the CLI's own list is the
// sealed one intersected with the file, so edit is refused outright.
func TestDelegatedCancelRefusedOutsideSealedAuthority(t *testing.T) {
	unsealGateAuthority(t)
	pinGateAuthorityConfig(t, "user")
	SealGateAuthority()
	pinGateAuthorityConfig(t, "user,edit")
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	pinActor(t, "edit")
	if err := CancelGraphRun(runTestSession, run.ID); err == nil || errors.Is(err, ErrCancelDelegated) {
		t.Errorf("a config widened after the seal let edit file a request: %v", err)
	}
	if _, err := os.Stat(cancelRequestPath(run.ID)); !os.IsNotExist(err) {
		t.Error("a refused cancel filed a request")
	}
}

// A request file written without the CLI's audit row is forged, whatever role
// it names — even one the seal grants — and is consumed and refused. This is
// the bypass the 2026-11-13 review found: the actor field alone is a claim.
func TestDelegatedCancelRefusesForgedRequests(t *testing.T) {
	unsealGateAuthority(t)
	pinGateAuthorityConfig(t, "user,edit")
	SealGateAuthority()
	pinActor(t, "")
	run := createTestRun(t, linearGraph())

	for _, actor := range []string{"edit", ActorUser, graphSender, ActorUnknown} {
		forged := cancelRequest{Actor: actor, RequestedAt: time.Now().Unix()}
		if err := atomicWriteJSON(cancelRequestPath(run.ID), forged); err != nil {
			t.Fatal(err)
		}
		ProcessCancelRequests(runTestSession)
		if st := runState(t, run.ID); st != GraphRunRunning {
			t.Fatalf("a forged request naming %q canceled the user's run: %q", actor, st)
		}
		if _, err := os.Stat(cancelRequestPath(run.ID)); !os.IsNotExist(err) {
			t.Fatalf("forged request naming %q was left to be retried", actor)
		}
	}
	if refused := cancelEvents(t, "graph-cancel-refused", run.ID); len(refused) != 4 {
		t.Errorf("each forged request must log one graph-cancel-refused, got %d", len(refused))
	}
}
