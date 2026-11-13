package bus

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// A delegated cancel lets a role the user granted the gate authority stop a
// run the user launched — the remote-control case, where the user has no shell
// to cancel from (2026-11-13). The CLI cannot decide it: there the authority
// list is read unsealed from caller-controlled paths (the cwd's .muxcode/config,
// $HOME), so honouring it in-process let any agent self-grant. The CLI only
// files a request; the daemon, whose list was sealed at startup, decides.
//
// A request naming the user, an unidentified actor or the daemon is never
// honoured: the user cancels directly, and the daemon normalizes to edit, the
// shape MUX-144 Phase 4 closed on the commit backstop.
//
// The request's actor is a claim in a file any role can write, so the daemon
// also requires the graph-cancel-delegated row the CLI logs beside it — same
// actor, same detail, same second — as approvalHasAudit does for gates. That
// is corroboration, not authentication: forging takes two consistent
// artifacts, one a dated, attributed row in the lifecycle log. A request older
// than cancelRequestMaxAge is refused, so an old row cannot be replayed.

// ErrCancelDelegated reports that the cancel was handed to the daemon rather
// than performed: it has not happened yet and may still be refused.
var ErrCancelDelegated = errors.New("cancel handed to the daemon")

const (
	cancelRequestFile   = "cancel-request.json"
	cancelRequestMaxAge = 10 * 60
)

func cancelDelegatedDetail(runID, actor string) string {
	return fmt.Sprintf("Graph run %s: %s asked the daemon to cancel it", runID, actor)
}

type cancelRequest struct {
	Actor       string `json:"actor"`
	RequestedAt int64  `json:"requested_at"`
}

// delegableCancelActor reports whether actor may ask for a delegated cancel:
// a named agent role this process's gate authority lists. In the CLI that list
// is a claim; in the daemon, sealed, it is the decision.
func delegableCancelActor(actor string) bool {
	switch actor {
	case "", ActorUser, ActorUnknown, graphSender:
		return false
	}
	return slices.Contains(GateApprovalAuthority(), actor)
}

// sealedCancelGrant is delegableCancelActor where the answer is binding: only
// in a process whose gate authority is sealed — the daemon.
func sealedCancelGrant(actor string) bool {
	gateSealMu.RLock()
	sealed := gateAuthoritySealed
	gateSealMu.RUnlock()
	return sealed && delegableCancelActor(actor)
}

// fileCancelRequest logs the audit row, then publishes the request, both with
// one timestamp. The order is load-bearing: the daemon consumes a request the
// moment it appears and refuses one with no row, so a request published first
// could be read in the gap and lost.
func fileCancelRequest(session, runID, actor string) error {
	ts := time.Now().Unix()
	LogLifecycleAt(session, "info", actor, "graph-cancel-delegated", cancelDelegatedDetail(runID, actor), ts)
	return atomicWriteJSON(filepath.Join(GraphRunDir(session, runID), cancelRequestFile),
		cancelRequest{Actor: actor, RequestedAt: ts})
}

// cancelRequestHasAudit is the corroboration the package doc describes.
func cancelRequestHasAudit(session, runID string, req cancelRequest) bool {
	if req.RequestedAt <= 0 || time.Now().Unix()-req.RequestedAt > cancelRequestMaxAge {
		return false
	}
	entries, err := ReadLifecycleLog(session)
	if err != nil {
		return false
	}
	want := cancelDelegatedDetail(runID, req.Actor)
	for _, e := range entries {
		if e.Event == "graph-cancel-delegated" && e.Source == req.Actor && e.Detail == want && e.TS == req.RequestedAt {
			return true
		}
	}
	return false
}

// cancelRequestClaimedFile is the name a request is renamed to while the daemon
// works on it.
const cancelRequestClaimedFile = cancelRequestFile + ".claimed"

// ProcessCancelRequests performs or refuses every filed delegated cancel. A
// request is claimed by an atomic rename before it is read, so a request
// replaced meanwhile is a new file the next tick claims, never one deleted
// unread. A refusal consumes the claim, so it is never retried; a cancel that
// errors keeps it, and a claim left by a crashed daemon is processed first on
// the next tick — both bounded by cancelRequestMaxAge, past which the audit
// check refuses and consumes it. Called from the daemon's graph tick; a no-op
// outside a sealed process.
func ProcessCancelRequests(session string) {
	entries, err := os.ReadDir(GraphRunsDir(session))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runID := e.Name()
		dir := filepath.Join(GraphRunsDir(session), runID)
		claimed := filepath.Join(dir, cancelRequestClaimedFile)
		if _, err := os.Lstat(claimed); err != nil {
			if os.Rename(filepath.Join(dir, cancelRequestFile), claimed) != nil {
				continue
			}
		}
		processClaimedCancel(session, runID, claimed)
	}
}

// processClaimedCancel judges one claimed request and removes the claim unless
// the cancel itself failed.
func processClaimedCancel(session, runID, claimed string) {
	data, err := os.ReadFile(claimed)
	if err != nil {
		return
	}
	var req cancelRequest
	if err := json.Unmarshal(data, &req); err != nil {
		LogLifecycle(session, "warn", graphSender, "graph-cancel-refused", fmt.Sprintf("Graph run %s: unreadable delegated cancel request: %v", runID, err))
		os.Remove(claimed)
		return
	}
	if !sealedCancelGrant(req.Actor) {
		LogLifecycle(session, "warn", graphSender, "graph-cancel-refused",
			fmt.Sprintf("Graph run %s: delegated cancel by %q refused — not in the daemon's sealed gate authority", runID, req.Actor))
		os.Remove(claimed)
		return
	}
	if !cancelRequestHasAudit(session, runID, req) {
		LogLifecycle(session, "warn", graphSender, "graph-cancel-refused",
			fmt.Sprintf("Graph run %s: delegated cancel by %q refused — no matching graph-cancel-delegated audit row, or the request is stale", runID, req.Actor))
		os.Remove(claimed)
		return
	}
	if err := cancelGraphRun(session, runID, req.Actor, true); err != nil {
		LogLifecycle(session, "error", graphSender, "graph-cancel-delegated-failed", fmt.Sprintf("Graph run %s: %v (kept for retry)", runID, err))
		return
	}
	os.Remove(claimed)
}
