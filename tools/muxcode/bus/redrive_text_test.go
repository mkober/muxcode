package bus

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// redrivePayload is 300 characters, its 80th rune the end of
// `--show-current` as in the 2026-11-17 tibo preflight dispatch.
var redrivePayload = func() string {
	head := "Preflight check, read-only: git status --porcelain and git branch --show-current"
	return head + strings.Repeat(" and report the tree, branch, upstream and any untracked files", 4)[:300-len(head)]
}()

// redriveSession is a scratch session with the consumer guard off, so a
// request can be logged to any role.
func redriveSession(t *testing.T) string {
	t.Helper()
	useTempBusDir(t)
	t.Setenv("MUXCODE_CONSUMER_GUARD", "0")
	return testSession(t)
}

// loggedTask sends m (so its log line, with any ReplyRole, exists) and returns
// the in-flight task a re-drive is rebuilt from.
func loggedTask(t *testing.T, session string, m Message) Task {
	t.Helper()
	if err := SendForce(session, m); err != nil {
		t.Fatal(err)
	}
	return Task{ID: m.ID, From: m.From, To: m.To, Action: m.Action, Payload: m.Payload,
		Status: TaskInFlight, SentAt: time.Now().Unix(), Timeout: 600}
}

// MUX-232, the Phase 1 pin inverted. A graph dispatch commit consumed with a
// receipt and never answered is re-driven with its whole payload, its id and a
// reply command to edit — a `daemon` dispatch's reply target — so the agent's
// reply correlates and closes the node. Before, the re-drive was the wake
// notification: the payload cut at 80 runes (after `--show-current`), the
// sender named as `daemon`, no id and no reply command; on 2026-11-17 that
// failed two defect-loop runs in the tibo session at 600 s each.
func TestRedriveNotification_CarriesPayloadIDAndReplyRoute(t *testing.T) {
	if utf8.RuneCountInString(redrivePayload) != 300 ||
		!strings.HasSuffix(string([]rune(redrivePayload)[:80]), "--show-current") {
		t.Fatal("fixture: want 300 runes with the 80th ending --show-current")
	}
	session := redriveSession(t)
	task := loggedTask(t, session, NewMessage("daemon", "commit", "request", "pr-read", redrivePayload, ""))
	msgs := redriveMessages([]Task{task}, "commit", time.Now().Unix())
	text, _ := redriveNotification(session, msgs)

	if !strings.HasPrefix(text, "Re-drive (consumed but never completed)") {
		t.Errorf("lost the re-drive lead: %q", text)
	}
	if !strings.Contains(text, redrivePayload) {
		t.Errorf("the whole 300-character payload is not in the re-drive: %q", text)
	}
	if !strings.Contains(text, "["+task.ID+"]") {
		t.Errorf("the re-drive does not name the message id %s: %q", task.ID, text)
	}
	if want := buildReplyCommand("edit", task.ID); !strings.Contains(text, "To reply: "+want) {
		t.Errorf("want the reply command %q, to edit, in %q", want, text)
	}
	if strings.Contains(text, "muxcode send daemon") {
		t.Errorf("the reply command addresses daemon, which is no bus role: %q", text)
	}
}

// A request carrying reply_role is answered there, not to its sender: test's
// chain review request names edit (NewChainRequest, MUX-213), so the re-drive's
// command to review's agent targets edit, not test.
func TestRedriveNotification_HonoursReplyRole(t *testing.T) {
	session := redriveSession(t)
	req := NewChainRequest("test", "test", &ChainAction{SendTo: "review", Type: "request", Action: "review"}, "review the tree")
	task := loggedTask(t, session, req)
	text, _ := redriveNotification(session, redriveMessages([]Task{task}, "review", time.Now().Unix()))
	if !strings.Contains(text, buildReplyCommand("edit", task.ID)) || strings.Contains(text, "muxcode send test ") {
		t.Errorf("the chain request's re-drive must reply to edit, its reply_role: %q", text)
	}
}

// The re-drive is bounded like a wake-up batch, cut between whole requests:
// past wakeUpMaxMessages the rest are counted, never half-shown — each shown
// request keeps its reply command. An oversized first request is still shown
// whole, or recovery could never make progress. A payload's newlines are
// flattened, since a typed newline would submit before the reply command.
func TestRedriveNotification_BoundedByWholeRequests(t *testing.T) {
	session := redriveSession(t)
	var tasks []Task
	for i := 0; i < wakeUpMaxMessages+3; i++ {
		tasks = append(tasks, loggedTask(t, session,
			NewMessage("edit", "build", "request", fmt.Sprintf("build-%d", i), fmt.Sprintf("job %d\nsecond line", i), "")))
	}
	text, shown := redriveNotification(session, redriveMessages(tasks, "build", time.Now().Unix()))
	if got := strings.Count(text, "To reply: "); got != wakeUpMaxMessages || shown != wakeUpMaxMessages {
		t.Errorf("%d requests shown, reported %d, want %d", got, shown, wakeUpMaxMessages)
	}
	if !strings.Contains(text, "[3 more re-driven request(s) not shown") {
		t.Errorf("the left-out count is missing: %q", text)
	}
	for _, tk := range tasks[:wakeUpMaxMessages] {
		if !strings.Contains(text, buildReplyCommand("edit", tk.ID)) {
			t.Errorf("shown request %s lost its reply command", tk.ID)
		}
	}
	if strings.Contains(text, tasks[wakeUpMaxMessages].ID) {
		t.Error("a left-out request was partly shown")
	}
	if strings.Contains(text, "\n") || !strings.Contains(text, "job 0 ⏎ second line") {
		t.Errorf("payload newlines must be flattened to ⏎: %q", text)
	}

	big := loggedTask(t, session, NewMessage("edit", "build", "request", "huge", strings.Repeat("x", wakeUpMaxBytes+500), ""))
	text, _ = redriveNotification(session, redriveMessages([]Task{big}, "build", time.Now().Unix()))
	if !strings.Contains(text, buildReplyCommand("edit", big.ID)) || strings.Contains(text, "not shown") {
		t.Errorf("an oversized lone request must still be shown whole with its reply command")
	}
}

// seedRedriveTask records an in-flight request to run with the given payload,
// consumed and unanswered — what the stall watchdog re-drives.
func seedRedriveTask(t *testing.T, session, id, payload string) {
	t.Helper()
	m := Message{ID: id, TS: time.Now().Unix(), From: "edit", To: "run", Type: "request", Action: "run", Payload: payload}
	if err := CreateTask(session, m, 600); err != nil {
		t.Fatal(err)
	}
}

// typedText joins every literal send-keys payload the stubbed tmux saw.
func typedText(calls [][]string) string {
	var b strings.Builder
	for _, c := range calls {
		if j := strings.Join(c, " "); strings.Contains(j, "send-keys") && strings.Contains(j, "-l") {
			b.WriteString(j)
		}
	}
	return b.String()
}

// MUX-232 review must-fix, through the delivery bookkeeping the stall watchdog
// reads. Past the count cap, ForceDeliverFor reports only the requests it
// typed, and the stalled task it names is among them however late it sorts —
// the watchdog spends that task's retry before calling, so a role-wide batch
// that cut it from the tail would burn its retries on the leading requests.
func TestForceDeliverFor_CountCapReportsShownAndLeadsWithTheTask(t *testing.T) {
	session := "deliver-test-redrive-countcap"
	isolateLifecycle(t)
	calls := deliverTestSetup(t, session, claudeRestFrame)
	n := wakeUpMaxMessages + 2
	for i := 0; i < n; i++ {
		seedRedriveTask(t, session, fmt.Sprintf("TASK-%02d", i), fmt.Sprintf("job %d", i))
	}
	last := fmt.Sprintf("TASK-%02d", n-1)

	res, err := ForceDeliverFor(session, "run", true, last)
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered != wakeUpMaxMessages {
		t.Errorf("Delivered = %d, want the %d typed, not all %d in flight", res.Delivered, wakeUpMaxMessages, n)
	}
	typed := typedText(*calls)
	if !strings.Contains(typed, "["+last+"]") {
		t.Errorf("the stalled task %s sorts last but must lead the re-drive: %q", last, typed)
	}
	if got := strings.Count(typed, "To reply: "); got != wakeUpMaxMessages {
		t.Errorf("%d requests typed, want %d", got, wakeUpMaxMessages)
	}
	if !strings.Contains(lifecycleText(t), fmt.Sprintf("%d of %d in-flight task(s)", wakeUpMaxMessages, n)) {
		t.Errorf("force-redrive row must count shown of in flight:\n%s", lifecycleText(t))
	}
}

// Two requests whose blocks cross the byte cap: each watchdog attempt types
// the task it names — the second request is re-driven on its own attempt even
// though the first stays in flight — and each reports one delivery.
func TestForceDeliverFor_ByteCapGivesEachTaskItsOwnAttempt(t *testing.T) {
	session := "deliver-test-redrive-bytecap"
	calls := deliverTestSetup(t, session, claudeRestFrame)
	seedRedriveTask(t, session, "BIG-1", strings.Repeat("a", 3000))
	seedRedriveTask(t, session, "BIG-2", strings.Repeat("b", 3000))

	for _, id := range []string{"BIG-2", "BIG-1"} {
		*calls = nil
		res, err := ForceDeliverFor(session, "run", true, id)
		if err != nil {
			t.Fatal(err)
		}
		typed := typedText(*calls)
		other := map[string]string{"BIG-1": "BIG-2", "BIG-2": "BIG-1"}[id]
		if res.Delivered != 1 || !strings.Contains(typed, "["+id+"]") || strings.Contains(typed, "["+other+"]") {
			t.Errorf("attempt for %s: Delivered %d, typed it %v, typed %s %v — want only %s, counted once",
				id, res.Delivered, strings.Contains(typed, "["+id+"]"), other, strings.Contains(typed, "["+other+"]"), id)
		}
	}
}

// MUX-232 negative control, green before and after: a wake notification for
// an unconsumed message is still the short preview — the agent reads the rest
// from its inbox — bounded in length whatever the payload.
func TestWakeNotification_StaysAShortPreview(t *testing.T) {
	m := NewMessage("edit", "build", "request", "build", redrivePayload, "")
	text := BuildCombinedNotification([]Message{m})
	want := "New message from edit [request:build]: " + string([]rune(redrivePayload)[:80]) + "..."
	if text != want {
		t.Errorf("wake notification\n got: %q\nwant: %q", text, want)
	}
	if strings.Contains(text, m.ID) || strings.Contains(text, "--reply-to") {
		t.Errorf("the wake notification grew a reply route: %q", text)
	}
	if n := utf8.RuneCountInString(text); n > 130 {
		t.Errorf("wake notification is %d runes, want a bounded preview", n)
	}
}
