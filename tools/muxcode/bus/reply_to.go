package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReplyToKnown reports whether id names a message the bus knows: one in the
// session's bus log, or a tracked task.
func ReplyToKnown(session, id string) bool {
	if _, ok := FindMessageByID(session, id); ok {
		return true
	}
	_, err := ReadTask(session, id)
	return err == nil
}

// OpenRequest is a request a reply could be meant for, with Target — where a
// reply to it goes (ReplyTarget: its ReplyRole, else its sender), which a
// Task alone does not record.
type OpenRequest struct {
	Task
	Target string
}

// OpenRequestsFor returns the requests a reply from role could be meant for,
// oldest first: tracked requests to role's inbox still in flight and
// unanswered, and requests role received by receipt, has not answered and that
// have no task record — an untracked send has none, and only its receipt shows
// it open. A task record is authoritative: a timed-out or expired dispatch
// keeps its receipt, and must not come back as open through it. Each request
// appears once.
func OpenRequestsFor(session, role string) []OpenRequest {
	window := WindowForRole(NormalizeBusRole(role))
	var open []OpenRequest
	if tasks, err := ListTasks(session, TaskInFlight); err == nil {
		for _, t := range tasks {
			if WindowForRole(NormalizeBusRole(t.To)) == window && !IsResponded(session, t.ID) {
				open = append(open, OpenRequest{Task: t, Target: replyTargetFor(session, t)})
			}
		}
	}
	if received, err := receivedOpenRequests(session, NormalizeBusRole(role)); err == nil {
		for _, m := range received {
			if _, err := ReadTask(session, m.ID); err == nil {
				continue
			}
			t := Task{ID: m.ID, From: m.From, To: m.To, Action: m.Action, SentAt: m.TS}
			open = append(open, OpenRequest{Task: t, Target: ReplyTarget(m)})
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].SentAt < open[j].SentAt })
	return open
}

// replyTargetFor resolves where a reply to tracked request t goes, reading
// the request itself for its ReplyRole; with no log line, its sender.
func replyTargetFor(session string, t Task) string {
	if m, ok := FindMessageByID(session, t.ID); ok {
		return ReplyTarget(m)
	}
	return NormalizeBusRole(t.From)
}

// ErrReplyToUnknown refuses a response whose --reply-to names no message when
// the sender holds no single open request from the reply's requester.
var ErrReplyToUnknown = errors.New("--reply-to names no message")

// ResolveResponseReplyTo applies the user's Decision 1 (MUX-225) to a response
// whose --reply-to names no message, returning the id to send under. A
// request, a response with no --reply-to and one whose id the bus knows — a
// stale one included — pass through unchanged. For an unknown id: when from
// holds exactly one open request whose reply goes to `to` (its ReplyTarget:
// ReplyRole when set, so a chain request from test answers edit; `daemon`
// counts as edit), the reply is attached to it with a reply-to-repaired row;
// otherwise it is refused with ErrReplyToUnknown and the sender's open
// requests, for the caller to print, with a reply-to-unknown row. Either way
// edit is told once per sender and id. No nearest-id guessing (Decision 2).
// On 2026-11-14 a test agent's reply dropped six characters from its id, was
// accepted silently, and the graph node it answered waited out its 600 s cap.
func ResolveResponseReplyTo(session, from, to, msgType, replyTo string) (string, []OpenRequest, error) {
	if msgType != "response" || replyTo == "" || ReplyToKnown(session, replyTo) {
		return replyTo, nil, nil
	}
	open := OpenRequestsFor(session, from)
	var fromRequester []OpenRequest
	for _, o := range open {
		if inboxOwner(o.Target) == inboxOwner(to) {
			fromRequester = append(fromRequester, o)
		}
	}
	if len(fromRequester) == 1 {
		fixed := fromRequester[0].ID
		LogLifecycle(session, "warn", from, "reply-to-repaired", fmt.Sprintf(
			"%s replied to %s with --reply-to %s, which names no message; repaired to %s, its one open request from %s; its open requests: %s",
			from, to, replyTo, fixed, to, describeOpenRequests(open)))
		notifyEditOnce(session, from, replyTo, "reply-to-repaired", fmt.Sprintf(
			"%s's reply named no message (--reply-to %s); it was attached to %s, %s's one open request from %s", from, replyTo, fixed, from, to))
		return fixed, nil, nil
	}
	LogLifecycle(session, "warn", from, "reply-to-unknown", fmt.Sprintf(
		"%s replied to %s with --reply-to %s, which names no message; refused, %d open request(s) from %s; its open requests: %s",
		from, to, replyTo, len(fromRequester), to, describeOpenRequests(open)))
	notifyEditOnce(session, from, replyTo, "reply-to-unknown", fmt.Sprintf(
		"%s's reply named no message (--reply-to %s) and was refused — %d open request(s) from %s; %s's open requests: %s",
		from, replyTo, len(fromRequester), to, from, describeOpenRequests(open)))
	return "", open, fmt.Errorf("%w: %s holds %d open request(s) from %s", ErrReplyToUnknown, from, len(fromRequester), to)
}

// ReplyCommandFor is the ready-to-copy reply command for an open request,
// addressed to its reply target — edit for a chain request, never its sender.
func ReplyCommandFor(o OpenRequest) string {
	target := o.Target
	if target == "" || !IsKnownRole(target) {
		target = "edit"
	}
	return buildReplyCommand(target, o.ID)
}

// notifyEditOnce tells edit about from's unknown reply id once per sender and
// id; edit's own replies are not echoed back to it.
func notifyEditOnce(session, from, replyTo, action, text string) {
	if NormalizeBusRole(from) == "edit" || !firstUnknownReplyNotice(session, from, replyTo) {
		return
	}
	_ = SendNoCC(session, NewMessage("daemon", "edit", "event", action, text, ""))
}

func describeOpenRequests(open []OpenRequest) string {
	if len(open) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(open))
	for _, o := range open {
		parts = append(parts, fmt.Sprintf("%s (%s, from %s, reply to %s)", o.ID, o.Action, o.From, o.Target))
	}
	return strings.Join(parts, ", ")
}

// firstUnknownReplyNotice claims the one edit notice for from's unknown id,
// reporting whether this call claimed it, so a retried reply with the same
// mistyped id does not tell edit again.
func firstUnknownReplyNotice(session, from, replyTo string) bool {
	stem := safeIDStem(from + "-" + replyTo)
	if stem == "" {
		return true
	}
	dir := filepath.Join(BusDir(session), "reply-to-unknown")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return true
	}
	f, err := os.OpenFile(filepath.Join(dir, stem), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return !os.IsExist(err)
	}
	_ = f.Close()
	return true
}
