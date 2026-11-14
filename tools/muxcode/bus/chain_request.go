package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// openRequestMaxAge bounds how old a received-but-unanswered request may be
// and still stand behind a run. A request answered without --reply-to is
// never marked responded, so without a bound it would vouch for every later
// run forever.
const openRequestMaxAge = 2 * time.Hour

// NewChainRequest builds the message a hook (or `muxcode chain`) sends for a
// resolved chain action.
//
// A test-chain request names edit as its reply target (MUX-213). The hook
// sends it on the test agent's behalf, and the test agent has already
// answered its own requester, so delivering the review to it is information
// nobody asked for — and a Codex test agent woken by it re-ran the suite,
// whose success requested the next review: eight such turns in 69 minutes on
// 2026-11-13. Replying to edit also tells edit once: a reply to test reached
// edit twice, as review's own send plus the auto-CC copy. --reply-to still
// correlates the reply to this request.
func NewChainRequest(from, eventType string, action *ChainAction, message string) Message {
	msg := NewMessage(from, action.SendTo, action.Type, action.Action, message, "")
	if eventType == "test" && action.Type == "request" {
		msg.ReplyRole = "edit"
	}
	return msg
}

// OpenRequestFor returns the id of a request role has received and not yet
// answered — the request a run it is starting answers. ok is false
// when there is none: every request the role received is answered (or older
// than openRequestMaxAge), so the run was started by something other than a
// request — a response, a timeout notice, an idle wake, a rerun after the
// reply.
//
// Received means a receipt (acked on consume, or delivered by verified
// injection); a request still sitting unread in the inbox stands behind no
// run. Answered means marked responded by a --reply-to reply. With delivery
// acknowledgement off there are no receipts to read, so the answer fails
// open (ok true) and the caller behaves as before MUX-213.
//
// A request whose review was already dispatched (ClaimRequestReview) is
// returned only when no unspent one is open, so a second, distinct request
// still gets its own review while a rerun under the first is named and
// withheld.
func OpenRequestFor(session, role string) (string, bool) {
	if !AckDeliveryActive(session) {
		return "", true
	}
	statuses, err := ListDeliveryStatuses(session)
	if err != nil {
		return "", true
	}
	window := WindowForRole(role)
	cutoff := time.Now().Add(-openRequestMaxAge).Unix()
	spent := ""
	for _, ds := range statuses {
		if ds.Status != StatusAcked && ds.Status != StatusDelivered {
			continue
		}
		if ds.AckedBy == "" || WindowForRole(ds.AckedBy) != window || ds.AckedAt < cutoff {
			continue
		}
		m, ok := FindMessageByID(session, ds.ID)
		if !ok || m.Type != "request" || WindowForRole(m.To) != window {
			continue
		}
		if isStartupBootstrap(m) || isLoopingSelfSend(m) {
			continue
		}
		if requestReviewSpent(session, ds.ID) {
			if spent == "" {
				spent = ds.ID
			}
			continue
		}
		return ds.ID, true
	}
	if spent != "" {
		return spent, true
	}
	return "", false
}

func spentRequestPath(session, id string) string {
	stem := safeIDStem(id)
	if stem == "" {
		return ""
	}
	return filepath.Join(BusDir(session), "spent-requests", stem)
}

func requestReviewSpent(session, id string) bool {
	path := spentRequestPath(session, id)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// ClaimRequestReview spends request id on the test-chain dispatch about to be
// sent for it, returning false when an earlier run under the same request has
// already spent it. One request stands behind at most one review: without the
// claim, an agent rerunning the suite before replying fired a second review for
// one request (MUX-216). The marker is created O_EXCL, so two async hooks
// racing for one request cannot both win; an id that keys no file, or a marker
// that cannot be written, fails open. ReleaseRequestReview returns a claim
// whose dispatch was not sent. Markers older than openRequestMaxAge are swept:
// by then the request vouches for no run anyway.
func ClaimRequestReview(session, id string) bool {
	path := spentRequestPath(session, id)
	if path == "" {
		return true
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return true
	}
	sweepRunProvenance(dir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return !os.IsExist(err)
	}
	_ = f.Close()
	return true
}

// ReleaseRequestReview undoes ClaimRequestReview for a dispatch that was not
// sent, so the request still stands behind a later run.
func ReleaseRequestReview(session, id string) {
	if path := spentRequestPath(session, id); path != "" {
		_ = os.Remove(path)
	}
}

// testRunRecord is what RecordRunProvenance captured for one test invocation.
type testRunRecord struct {
	Request   string `json:"request,omitempty"`
	Open      bool   `json:"open"`
	GraphRun  string `json:"graph_run,omitempty"`
	GraphNode string `json:"graph_node,omitempty"`
	TS        int64  `json:"ts"`
}

// TestRunOrigin is what stood behind a test run as it started: whether a
// request did (Requested) and which one (Request — empty when delivery-ack is
// off and the answer failed open), and the graph run and node that owned the
// role then, if any.
type TestRunOrigin struct {
	Requested bool
	Request   string
	GraphRun  string
	GraphNode string
}

func runProvenanceDir(session string) string {
	return filepath.Join(BusDir(session), "run-provenance")
}

// safeIDStem maps an id to a file name stem, or "" when the id cannot key one
// (empty, or no safe character in it).
func safeIDStem(id string) string {
	safe := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '_'
	}, id)
	if len(safe) > 128 {
		safe = safe[:128]
	}
	if strings.Trim(safe, "_.") == "" {
		return ""
	}
	return safe
}

func runProvenancePath(session, toolUseID string) string {
	stem := safeIDStem(toolUseID)
	if stem == "" {
		return ""
	}
	return filepath.Join(runProvenanceDir(session), stem+".json")
}

// RecordRunProvenance captures, at PreToolUse, what stands behind a test
// invocation — an unanswered request (OpenRequestFor) and the graph run that
// owns the role (GraphOwnsRunningSendNode) — keyed by its tool_use_id, for
// TakeTestRunOrigin to read when the call completes.
//
// Both answers have to be taken before the command runs. Claude's `hook bash`
// is async: a requested suite finishes, the agent replies — which marks the
// request responded and completes the graph node's dispatch task — and only
// then can the delayed hook ask. Asked at completion, a requested run loses its
// review (MUX-213 review must-fix), and a graph test node's run no longer
// reads as graph-owned, so its success escapes into an ungated test→review
// chain beside the graph's own review node. The guard hook that calls this is
// synchronous on Claude and on the Codex hook road, and runs before the agent
// can reply. Only test commands are recorded; records older than
// openRequestMaxAge, whose completion never arrived, are swept here.
func RecordRunProvenance(session, role string, ev *ToolEvent) {
	if ev == nil || ev.ToolInput.Command == "" || ClassifyCommand(ev.ToolInput.Command) != CmdTest {
		return
	}
	path := runProvenancePath(session, ev.ToolUseID)
	if path == "" {
		return
	}
	dir := runProvenanceDir(session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	sweepRunProvenance(dir)
	id, open := OpenRequestFor(session, role)
	runID, nodeID, _ := GraphOwnsRunningSendNode(session, role)
	data, err := json.Marshal(testRunRecord{Request: id, Open: open, GraphRun: runID, GraphNode: nodeID, TS: time.Now().Unix()})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func sweepRunProvenance(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-openRequestMaxAge)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// TakeTestRunOrigin returns what stood behind the test invocation toolUseID
// when it started, consuming the record RecordRunProvenance wrote. With no
// record — no guard hook registered, or no tool_use_id — it falls back to
// asking OpenRequestFor now and names no graph run (the caller's live
// ownership check still applies), which refuses a run nobody requested but can
// lose the race with a fast reply.
func TakeTestRunOrigin(session, role, toolUseID string) TestRunOrigin {
	if path := runProvenancePath(session, toolUseID); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_ = os.Remove(path)
			var rec testRunRecord
			if json.Unmarshal(data, &rec) == nil {
				return TestRunOrigin{Requested: rec.Open, Request: rec.Request, GraphRun: rec.GraphRun, GraphNode: rec.GraphNode}
			}
		}
	}
	id, ok := OpenRequestFor(session, role)
	return TestRunOrigin{Requested: ok, Request: id}
}

// LogChainUnrequested records that a chain did not fire because no request
// stood behind the run (TakeTestRunOrigin), in the same chain-withheld event as
// an unfinished call, the command scrubbed for role and clipped.
func LogChainUnrequested(session, role, command, chain string) {
	cmd, _ := ScrubForRole(role, command)
	LogLifecycle(session, "info", "hook", "chain-withheld",
		fmt.Sprintf("%s chain not fired for %s — no unanswered request stands behind this run: %s",
			chain, role, truncateAtRune(cmd, 160)))
}

// LogChainRequestSpent records that a chain did not fire because the request
// behind the run already dispatched its review (ClaimRequestReview), in the
// same chain-withheld event, naming the request.
func LogChainRequestSpent(session, role, command, chain, request string) {
	cmd, _ := ScrubForRole(role, command)
	LogLifecycle(session, "info", "hook", "chain-withheld",
		fmt.Sprintf("%s chain not fired for %s — request %s already dispatched its review: %s",
			chain, role, request, truncateAtRune(cmd, 160)))
}
