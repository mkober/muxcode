package bus

import (
	"os"
	"strings"
	"testing"
	"time"
)

// deliverTestSetup wires a temp bus dir and mocked tmux runners. captureContent
// is returned for every capture-pane/display-message query (control idle state).
// Returns a pointer to the captured send-keys/run calls.
func deliverTestSetup(t *testing.T, session, captureContent string) *[][]string {
	t.Helper()
	// Isolate provider resolution from the ambient environment. ResolveProvider
	// reads runtime overrides keyed off BUS_SESSION, then per-role CLI env vars
	// (MUXCODE_RUN_CLI), then the global one (MUXCODE_AGENT_CLI), then the role
	// default (run → opencode). Without pinning, a live session's override or a
	// leaked MUXCODE_RUN_CLI=opencode would route SendWakeUpWithText down a
	// non-hook provider path that never performs the `send-keys -l` injection
	// these tests assert. Point the override lookup at this test's (override-free)
	// session and force the Claude hook path. All ForceDeliver tests target the
	// "run" role, so pin its per-role CLI var (highest-precedence env source).
	t.Setenv("BUS_SESSION", session)
	t.Setenv("MUXCODE_AGENT_CLI", "claude")
	t.Setenv(RoleCLIEnvVar("run"), "claude")
	if err := Init(session, t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(BusDir(session)) })

	origRun := tmuxRunner
	origQuiet := tmuxQuietRunner
	origOutput := tmuxOutputRunner
	var calls [][]string
	tmuxRunner = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	tmuxQuietRunner = func(args ...string) error { return nil } // has-session → exists
	tmuxOutputRunner = func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "window_active") {
			return "0", nil // not focused
		}
		return captureContent, nil // capture-pane content
	}
	t.Cleanup(func() {
		tmuxRunner = origRun
		tmuxQuietRunner = origQuiet
		tmuxOutputRunner = origOutput
	})
	return &calls
}

func sendTestRequest(t *testing.T, session, to, id string) {
	t.Helper()
	m := Message{ID: id, From: "edit", To: to, Type: "request", Action: "run", Payload: "do the thing"}
	if err := SendNoCC(session, m); err != nil {
		t.Fatalf("SendNoCC: %v", err)
	}
}

func TestForceDeliver_NoMessages(t *testing.T) {
	session := "deliver-test-none"
	deliverTestSetup(t, session, "❯ \n")

	res, err := ForceDeliver(session, "run", true)
	if err != nil {
		t.Fatalf("ForceDeliver: %v", err)
	}
	if res.Delivered != 0 || res.Skipped == "" {
		t.Errorf("expected nothing delivered, got %+v", res)
	}
}

func TestForceDeliver_ForceInjectsAndMarksNotified(t *testing.T) {
	session := "deliver-test-force"
	calls := deliverTestSetup(t, session, "❯ \n") // idle, empty composer
	sendTestRequest(t, session, "run", "MSG-1")

	res, err := ForceDeliver(session, "run", true)
	if err != nil {
		t.Fatalf("ForceDeliver: %v", err)
	}
	if res.Delivered != 1 {
		t.Fatalf("expected 1 delivered, got %d (%+v)", res.Delivered, res)
	}

	// A send-keys text injection should have happened, in the dash-safe
	// form: `-l -- <payload>`. The -- separator is asserted at argv level
	// because MUX-104's failure was tmux flag parsing — a dash-leading
	// payload without -- is rejected as `invalid flag -`.
	sawText := false
	for _, c := range *calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "send-keys") && strings.Contains(j, "-l") {
			sawText = true
			if len(c) < 2 || c[len(c)-2] != "--" {
				t.Errorf("text injection lacks the -- separator before the payload: %v", c)
			}
		}
	}
	if !sawText {
		t.Errorf("expected a literal send-keys text injection, got %v", *calls)
	}

	// The message must now be marked notified (won't re-deliver).
	if got := UnnotifiedMessages(session, "run"); len(got) != 0 {
		t.Errorf("expected message marked notified, still unnotified: %d", len(got))
	}
}

// TestForceDeliver_NoForceRequiresIdlePrompt pins the idle gate, which --force
// is allowed to override. Its pane must therefore be at rest but composer-less
// (the launch window before the TUI draws its input box) — NOT mid-turn. A busy
// fixture here would exercise the MUX-171 gate above it instead, which returns a
// skip rather than this error, and the assertion would pass for the wrong reason.
func TestForceDeliver_NoForceRequiresIdlePrompt(t *testing.T) {
	session := "deliver-test-no-composer"
	deliverTestSetup(t, session, claudeNoComposerFrame)
	sendTestRequest(t, session, "run", "MSG-2")

	_, err := ForceDeliver(session, "run", false)
	if err == nil {
		t.Fatal("expected error when agent is not at an idle prompt without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should suggest --force, got: %v", err)
	}
}

// TestTaskStalled pins the stall watchdog's age gate: graph-dispatched
// tasks (From daemon) stall at HALF the threshold — graph runs take
// delivery priority (user rule 2026-08-27) — expired tasks belong to
// the timeout path, and young tasks are left alone.
func TestTaskStalled(t *testing.T) {
	now := int64(10_000)
	mk := func(from string, age int64) Task {
		return Task{From: from, To: "plan", Status: TaskInFlight, SentAt: now - age, Timeout: 600}
	}
	if TaskStalled(mk("edit", 60), now, 90) {
		t.Error("a 60s-old task must not stall at a 90s threshold")
	}
	if !TaskStalled(mk("edit", 90), now, 90) {
		t.Error("a 90s-old task must stall at a 90s threshold")
	}
	if !TaskStalled(mk("daemon", 45), now, 90) {
		t.Error("a graph-dispatched task must stall at HALF the threshold — graph runs take priority")
	}
	if TaskStalled(mk("daemon", 30), now, 90) {
		t.Error("a graph task younger than half the threshold must not stall")
	}
	expired := mk("edit", 700)
	if TaskStalled(expired, now, 90) {
		t.Error("an expired task is the timeout path's business, never a stall")
	}
	// Copilot catch (PR #40): halving a threshold of 1 must clamp to 1,
	// not truncate to 0 (which would stall every graph task instantly).
	if TaskStalled(mk("daemon", 0), now, 1) {
		t.Error("a zero-age graph task must not stall at a clamped 1s threshold")
	}
	if !TaskStalled(mk("daemon", 1), now, 1) {
		t.Error("the clamped 1s threshold must still fire at age 1")
	}
	done := mk("edit", 200)
	done.Status = TaskCompleted
	if TaskStalled(done, now, 90) {
		t.Error("only in-flight tasks can stall")
	}
}

// TestRedriveMessages pins the consumed-but-never-started recovery: a
// role's live in-flight tasks map back to injectable requests, expired
// tasks and other roles' tasks are excluded, and hosted roles resolve
// to their host window.
func TestRedriveMessages(t *testing.T) {
	now := int64(10_000)
	tasks := []Task{
		{ID: "t1", From: "daemon", To: "plan", Action: "verify-spec", Payload: "check the spec", Status: TaskInFlight, SentAt: now - 60, Timeout: 600},
		{ID: "t2", From: "edit", To: "build", Action: "build", Payload: "other role", Status: TaskInFlight, SentAt: now - 60, Timeout: 600},
		{ID: "t3", From: "daemon", To: "plan", Action: "update-docs", Payload: "expired", Status: TaskInFlight, SentAt: now - 5000, Timeout: 600},
		{ID: "t4", From: "edit", To: "docs", Action: "update-docs", Payload: "hosted → plan", Status: TaskInFlight, SentAt: now - 60, Timeout: 600},
	}
	msgs := redriveMessages(tasks, "plan", now)
	if len(msgs) != 2 {
		t.Fatalf("expected the live plan task + hosted docs task, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].ID != "t1" || msgs[0].Action != "verify-spec" || msgs[0].Payload != "check the spec" || msgs[0].Type != "request" {
		t.Errorf("t1 mapped wrong: %+v", msgs[0])
	}
	if msgs[1].ID != "t4" {
		t.Errorf("hosted role's task must resolve to the host window, got %+v", msgs[1])
	}
	if got := redriveMessages(tasks, "review", now); len(got) != 0 {
		t.Errorf("a role with no in-flight tasks must re-drive nothing, got %+v", got)
	}
}

// claudeBusyFrame is the shape that fooled the stall watchdog on
// 2026-09-09: the run agent mid-Bash-call, its ❯ composer on screen the
// whole time, so PaneHasIdlePrompt reads it as at-rest while the spinner
// says otherwise.
const claudeBusyFrame = "⏺ Now running the requested command exactly as specified.\n" +
	"  Ran 1 shell command\n" +
	"\n" +
	"✻ Cogitating… (2m 14s · esc to interrupt)\n" +
	"\n" +
	"❯ \n" +
	"  ⏵⏵ bypass permissions on · 1 shell\n"

// claudeRestFrame is the same pane one moment later — the completed recap
// carries no interrupt hint, so the agent is genuinely deliverable.
const claudeRestFrame = "✻ Cooked for 2m 14s\n" +
	"\n" +
	"❯ \n" +
	"  ⏵⏵ bypass permissions on · 1 shell\n"

// claudeNoComposerFrame is a pane that is neither working nor deliverable: the
// TUI has printed its banner but not yet drawn the ❯ composer. It carries no
// spinner, no interrupt hint and no prompt, which is what separates the idle
// gate (overridable with --force) from the busy gate (never overridable).
const claudeNoComposerFrame = "╭──────────────────────────────────────╮\n" +
	"│ Welcome to Claude Code               │\n" +
	"╰──────────────────────────────────────╯\n" +
	"\n" +
	"  cwd: /Users/dev/repo\n"

// The next two frames differ in ONE variable — where the spinner sits. Both are
// long enough that the top line falls outside the live tail, so they separate a
// pane that is working NOW from one whose scrollback merely mentions working.
// Judging the full capture reads both as busy, which is the capture-scope
// must-fix: this gate withholds delivery, so a stale match refuses force
// recovery indefinitely rather than costing a single tick.
const claudeStaleSpinnerScrollbackFrame = "✻ Cogitating… (2m 14s · esc to interrupt)\n" +
	"⏺ Read(bus/deliver.go)\n" +
	"  ⎿  Read 240 lines\n" +
	"⏺ The guard needs to sit above the branch.\n" +
	"  Ran 1 shell command\n" +
	"⏺ Edit(bus/timetrack.go)\n" +
	"  ⎿  Updated 1 addition\n" +
	"⏺ Done — the gate now scopes to the tail.\n" +
	"  Ran 2 shell commands\n" +
	"✻ Cooked for 2m 14s\n" +
	"\n" +
	"❯ \n" +
	"  ⏵⏵ bypass permissions on · 1 shell\n"

const claudeLiveSpinnerDeepFrame = "✻ Cogitating… (2m 14s · esc to interrupt)\n" +
	"⏺ Read(bus/deliver.go)\n" +
	"  ⎿  Read 240 lines\n" +
	"⏺ The guard needs to sit above the branch.\n" +
	"  Ran 1 shell command\n" +
	"⏺ Edit(bus/timetrack.go)\n" +
	"  ⎿  Updated 1 addition\n" +
	"⏺ Done — the gate now scopes to the tail.\n" +
	"  Ran 2 shell commands\n" +
	"✻ Ruminating… (9s · esc to interrupt)\n" +
	"\n" +
	"❯ \n" +
	"  ⏵⏵ bypass permissions on · 1 shell\n"

func seedInFlightTask(t *testing.T, session, id string) {
	t.Helper()
	m := Message{
		ID: id, TS: time.Now().Unix(), From: "edit", To: "run",
		Type: "request", Action: "run", Payload: "bash scripts/test-multi-phase-graph.sh",
	}
	if err := CreateTask(session, m, 600); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

func sawTextInjection(calls [][]string) bool {
	for _, c := range calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "send-keys") && strings.Contains(j, "-l") {
			return true
		}
	}
	return false
}

// TestRedriveInFlightTasks_RefusesBusyPane pins MUX-171 at the helper every
// re-drive road passes through. The injection is preceded by an Escape,
// which on a working Claude agent is the tool-interrupt key: on 2026-09-09
// this path killed the run agent's integration script twice, 61 s apart.
// The negative control is the whole point — a pane at rest must still be
// re-driven, or the watchdog stops catching the stalls it exists for.
func TestRedriveInFlightTasks_RefusesBusyPane(t *testing.T) {
	session := "deliver-test-redrive-busy"
	calls := deliverTestSetup(t, session, claudeBusyFrame)
	seedInFlightTask(t, session, "TASK-BUSY")

	res, err := ForceDeliver(session, "run", true)
	if err != nil {
		t.Fatalf("ForceDeliver: %v", err)
	}
	if res.Delivered != 0 {
		t.Errorf("a working pane must not be re-driven, got %+v", res)
	}
	if sawTextInjection(*calls) {
		t.Error("text was injected into a running tool call — the MUX-171 interrupt")
	}
}

func TestRedriveInFlightTasks_RestingPaneStillRedriven(t *testing.T) {
	session := "deliver-test-redrive-rest"
	calls := deliverTestSetup(t, session, claudeRestFrame)
	seedInFlightTask(t, session, "TASK-REST")

	res, err := ForceDeliver(session, "run", true)
	if err != nil {
		t.Fatalf("ForceDeliver: %v", err)
	}
	if res.Delivered != 1 {
		t.Fatalf("a consumed-but-unstarted task at an idle prompt must re-drive, got %+v", res)
	}
	if !sawTextInjection(*calls) {
		t.Errorf("expected the re-drive injection, got %v", *calls)
	}
}

// TestForceDeliver_BusyPaneWithPendingInboxRefuses is the MUX-171 review's
// second must-fix: the first cut guarded only the re-drive road, so a busy
// agent that still had an unnotified inbox row took the ordinary delivery
// branch and was typed into anyway. The message must survive unnotified —
// withholding delivery may not consume the row it declined to deliver.
// Both force modes are asserted because the two gates differ in what --force
// buys: it overrides the idle-prompt gate but must never override this one, and
// a busy pane is a SKIP (nil error) rather than the idle gate's error — a caller
// that treats "not delivered" as failure would otherwise retry into the turn.
func TestForceDeliver_BusyPaneWithPendingInboxRefuses(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "no-force"
		if force {
			name = "force"
		}
		t.Run(name, func(t *testing.T) {
			session := "deliver-test-busy-inbox-" + name
			calls := deliverTestSetup(t, session, claudeBusyFrame)
			sendTestRequest(t, session, "run", "MSG-BUSY")

			res, err := ForceDeliver(session, "run", force)
			if err != nil {
				t.Fatalf("a busy pane must be skipped, not errored: %v", err)
			}
			if res.Delivered != 0 {
				t.Errorf("a pending row must not be delivered into a running tool call, got %+v", res)
			}
			if res.Skipped == "" {
				t.Error("withholding delivery must report a reason")
			}
			if sawTextInjection(*calls) {
				t.Error("the pending inbox row was injected mid-turn")
			}
			if got := UnnotifiedMessages(session, "run"); len(got) != 1 {
				t.Errorf("the withheld message must stay unnotified for a later delivery, got %d", len(got))
			}
		})
	}
}

// TestAgentIsWorking_ProviderAware is the review's first must-fix: the busy
// test must not be Claude's ❯ predicate applied to everyone. An idle codex
// pane has no ❯ at all, so a PaneShowsRecoverableIdle-based check called it
// busy forever and cost every non-Claude agent its delivery recovery.
func TestAgentIsWorking_ProviderAware(t *testing.T) {
	codexIdle := "› Ask Codex to do anything\n  gpt-5.6-luna medium · ~/Repos\n"

	session := "deliver-test-provider-codex"
	deliverTestSetup(t, session, codexIdle)
	t.Setenv(RoleCLIEnvVar("run"), "codex")
	if AgentIsWorking(session, "run") {
		t.Error("an idle codex pane must not read as working — the ❯ test is Claude-shaped")
	}

	busy := "deliver-test-provider-claude-busy"
	deliverTestSetup(t, busy, claudeBusyFrame)
	if !AgentIsWorking(busy, "run") {
		t.Error("a Claude pane with a live spinner must read as working")
	}

	rest := "deliver-test-provider-claude-rest"
	deliverTestSetup(t, rest, claudeRestFrame)
	if AgentIsWorking(rest, "run") {
		t.Error("a Claude pane at rest must not read as working")
	}
}

// TestAgentIsWorking_ScopedToLiveTail pins the capture-scope must-fix. The two
// frames differ only in where the spinner sits, so the pair fails against a
// whole-capture check in exactly one direction — the stale case — and the live
// control proves the assertion is still reachable rather than always-false.
func TestAgentIsWorking_ScopedToLiveTail(t *testing.T) {
	stale := "deliver-test-tail-stale"
	deliverTestSetup(t, stale, claudeStaleSpinnerScrollbackFrame)
	if AgentIsWorking(stale, "run") {
		t.Error("a spinner in scrollback above an at-rest tail must not pin the pane busy — force recovery would be refused forever")
	}

	live := "deliver-test-tail-live"
	deliverTestSetup(t, live, claudeLiveSpinnerDeepFrame)
	if !AgentIsWorking(live, "run") {
		t.Error("a spinner in the live tail must still read as working (negative control)")
	}
}

// TestForceDeliver_StaleSpinnerStillDelivers carries the scope fix through to
// the behaviour that matters: the gate is what withholds delivery, so a pane
// whose history merely mentions a spinner must still be recoverable.
func TestForceDeliver_StaleSpinnerStillDelivers(t *testing.T) {
	session := "deliver-test-stale-spinner-delivers"
	calls := deliverTestSetup(t, session, claudeStaleSpinnerScrollbackFrame)
	sendTestRequest(t, session, "run", "MSG-STALE")

	res, err := ForceDeliver(session, "run", false)
	if err != nil {
		t.Fatalf("ForceDeliver: %v", err)
	}
	if res.Delivered != 1 {
		t.Fatalf("an idle pane with stale spinner history must still be delivered to, got %+v", res)
	}
	if !sawTextInjection(*calls) {
		t.Errorf("expected the delivery injection, got %v", *calls)
	}
}

// TestRedriveTask_RefusesBusyPane covers the graph executor's per-dispatch
// road, which reaches SendWakeUpWithText without passing ForceDeliver.
func TestRedriveTask_RefusesBusyPane(t *testing.T) {
	session := "deliver-test-task-busy"
	calls := deliverTestSetup(t, session, claudeBusyFrame)
	task := Task{
		ID: "TASK-1", From: "daemon", To: "run", Action: "run",
		Payload: "verify the phase", Status: TaskInFlight,
		SentAt: time.Now().Unix(), Timeout: 600,
	}
	if RedriveTask(session, task) {
		t.Error("RedriveTask must refuse a working pane")
	}
	if sawTextInjection(*calls) {
		t.Error("RedriveTask injected into a running tool call")
	}

	restCalls := deliverTestSetup(t, "deliver-test-task-rest", claudeRestFrame)
	if !RedriveTask("deliver-test-task-rest", task) {
		t.Error("RedriveTask must still re-drive a pane at rest (negative control)")
	}
	if !sawTextInjection(*restCalls) {
		t.Error("expected the re-drive injection on the resting pane")
	}
}

func TestForceDeliver_UnknownRole(t *testing.T) {
	session := "deliver-test-unknown"
	deliverTestSetup(t, session, "❯ \n")
	if _, err := ForceDeliver(session, "nonsense", true); err == nil {
		t.Error("expected error for unknown role")
	}
}

// redriveText must refuse a provider that rebuilds payloads from the inbox: the
// redriven row is already consumed, so that road types nothing and used to
// return nil, which the callers logged as a delivered re-drive. The Claude case
// is the positive control — without it a function that always refused would
// satisfy the first assertion.
func TestRedriveText_RefusesInboxRebuildingProviders(t *testing.T) {
	SetBusDirBase(t.TempDir())
	defer ResetBusDirBase()
	const session, payload = "redrive-text-test", "re-drive: do the thing"

	var sent [][]string
	origRun, origOut := tmuxRunner, tmuxOutputRunner
	tmuxRunner = func(args ...string) error { sent = append(sent, args); return nil }
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return "  no messages\n\n❯\n", nil
		}
		return "", nil
	}
	t.Cleanup(func() { tmuxRunner, tmuxOutputRunner = origRun, origOut })

	for _, p := range []Provider{&CodexProvider{}, &OpenCodeProvider{}} {
		sent = nil
		if err := redriveText(session, "build", p, payload); err == nil {
			t.Errorf("%s rebuilds its payload from the inbox, so a consumed row must refuse", p.Name())
		}
		for _, c := range sent {
			if len(c) > 0 && c[0] == "send-keys" {
				t.Errorf("%s refusal still sent keys: %v", p.Name(), c)
			}
		}
	}

	// Positive control: the supported path must actually type the explicit text
	// and submit it. Asserting only "not the refusal error" would pass for a
	// Claude injection that silently delivered nothing.
	sent = nil
	if err := redriveText(session, "edit", &ClaudeCodeProvider{}, payload); err != nil {
		t.Fatalf("claude self-polls, so the redrive must deliver: %v", err)
	}
	var typed, submitted bool
	for _, c := range sent {
		if len(c) == 0 || c[0] != "send-keys" {
			continue
		}
		if strings.Contains(strings.Join(c, " "), payload) {
			typed = true
		}
		if argvContains(c, "Enter") {
			submitted = true
		}
	}
	if !typed {
		t.Errorf("the explicit redrive payload was never typed; calls=%v", sent)
	}
	if !submitted {
		t.Error("the redrive text was typed but never submitted")
	}
}

// A failed composer clear must stop the sequence: continuing appends /clear to
// parked text and submits the pair.
func TestAutoClearInject_PreambleFailureStopsBeforeSlashClear(t *testing.T) {
	var sent []string
	origRun := tmuxRunner
	tmuxRunner = func(args ...string) error {
		if len(args) > 0 && args[0] == "send-keys" {
			if argvContains(args, "Escape") {
				return os.ErrPermission
			}
			sent = append(sent, strings.Join(args, " "))
		}
		return nil
	}
	t.Cleanup(func() { tmuxRunner = origRun })

	if err := autoClearInject("s:edit.1"); err == nil {
		t.Fatal("a failed composer clear must surface, not be discarded")
	}
	for _, s := range sent {
		if strings.Contains(s, "/clear") {
			t.Errorf("/clear was sent after the preamble failed: %q", s)
		}
	}
}
