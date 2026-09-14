package daemon

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

func countApprovalDeniedRows(t *testing.T, session string) int {
	t.Helper()
	entries, err := bus.ReadLifecycleLog(session)
	if err != nil {
		t.Fatalf("ReadLifecycleLog: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.Event == "approval-denied" {
			n++
		}
	}
	return n
}

// The eligibility gate is the whole safety argument for a watchdog that acts
// rather than alerts: an Escape is Codex's interrupt, so firing it at a role
// that executes without asking would discard that turn's work.
func TestCodexApprovalRoleEligible_OnlyOnRequestCodexRoles(t *testing.T) {
	t.Setenv("MUXCODE_REVIEW_CLI", "codex")
	t.Setenv("MUXCODE_ANALYZE_CLI", "codex")
	for _, role := range []string{"review", "analyze"} {
		if !codexApprovalRoleEligible(role) {
			t.Errorf("%s runs codex -a on-request and must be watched", role)
		}
	}
}

// Roles launched with `-a never` never raise the prompt, so they must be
// excluded even while running codex.
func TestCodexApprovalRoleEligible_ExcludesExecutingRoles(t *testing.T) {
	t.Setenv("MUXCODE_BUILD_CLI", "codex")
	t.Setenv("MUXCODE_TEST_CLI", "codex")
	t.Setenv("MUXCODE_COMMIT_CLI", "codex")
	for _, role := range []string{"build", "test", "commit", "edit"} {
		if codexApprovalRoleEligible(role) {
			t.Errorf("%s executes without asking — an Escape would kill its turn", role)
		}
	}
}

// A read-only role on another provider has no such prompt to answer.
func TestCodexApprovalRoleEligible_ExcludesNonCodexProviders(t *testing.T) {
	t.Setenv("MUXCODE_REVIEW_CLI", "claude")
	if codexApprovalRoleEligible("review") {
		t.Error("review on claude raises no codex approval prompt")
	}
}

// The valve has to work without a daemon restart to be a rollback.
func TestCodexApprovalWatchdog_DisableFlagStopsTheSweep(t *testing.T) {
	session := testSession(t)
	d := &Daemon{session: session}
	t.Setenv("MUXCODE_CODEX_APPROVAL_WATCHDOG_DISABLE", "1")

	d.checkCodexApprovals()

	if d.lastCodexApprovalCheck != 0 {
		t.Error("disabled watchdog must not even stamp its clock")
	}
	if os.Getenv("MUXCODE_CODEX_APPROVAL_WATCHDOG_DISABLE") != "1" {
		t.Fatal("test setup lost the disable flag")
	}
}

const approvalPromptPane = "  Would you like to run the following command?\n" +
	"  Environment: local\n" +
	"  Reason: May I rerun the browser tests outside the sandbox?\n" +
	"  $ pnpm exec playwright test\n" +
	"› 1. Yes, proceed (y)\n" +
	"  3. No, and tell Codex what to do differently (esc)\n" +
	"  Press enter to confirm or esc to cancel\n"

const approvalAnsweredPane = approvalPromptPane +
	"› Ask Codex to do anything\n" +
	"  gpt-6 medium · ~/repo\n"

// watchdogWithPane wires the capture/alive/deny seams and returns the targets
// an Escape was sent to.
func watchdogWithPane(t *testing.T, session, content string, denyErr error) (*Daemon, *[]string) {
	t.Helper()
	t.Setenv("MUXCODE_REVIEW_CLI", "codex")
	t.Setenv("MUXCODE_ANALYZE_CLI", "codex")
	var denied []string
	d := &Daemon{
		session:     session,
		agentAlive:  func(string, string) bool { return true },
		capturePane: func(string, int) (string, error) { return content, nil },
		denyApproval: func(target string) error {
			denied = append(denied, target)
			return denyErr
		},
		lastAlertKey:  make(map[string]int64),
		lastEventSent: make(map[string]int64),
	}
	return d, &denied
}

// The recovery path end to end: a live prompt with an empty inbox is answered.
func TestCodexApprovalWatchdog_DeniesLivePromptWithEmptyInbox(t *testing.T) {
	session := testSession(t)
	d, denied := watchdogWithPane(t, session, approvalPromptPane, nil)

	d.checkCodexApprovals()

	if len(*denied) == 0 {
		t.Fatal("a live approval prompt must receive an Escape")
	}
	if n := countApprovalDeniedRows(t, session); n == 0 {
		t.Error("denying must log an approval-denied lifecycle row")
	}
}

// The next tick: the prompt is in scrollback with the composer beneath, so a
// second Escape would interrupt the turn the first one freed.
func TestCodexApprovalWatchdog_AnsweredPromptGetsNoSecondEscape(t *testing.T) {
	session := testSession(t)
	d, denied := watchdogWithPane(t, session, approvalAnsweredPane, nil)

	d.checkCodexApprovals()

	if len(*denied) != 0 {
		t.Errorf("an answered prompt must not be re-denied, sent to %v", *denied)
	}
}

// A failed Escape is not a denial, and must not be logged as one.
func TestCodexApprovalWatchdog_DenyFailureLogsNoSuccess(t *testing.T) {
	session := testSession(t)
	d, _ := watchdogWithPane(t, session, approvalPromptPane, errors.New("pane gone"))

	d.checkCodexApprovals()

	if n := countApprovalDeniedRows(t, session); n != 0 {
		t.Errorf("a failed deny logged %d approval-denied rows, want 0", n)
	}
}

// Rate limiting: the sweep captures panes, so it must not run on every 5s poll
// — but it is worthless unless it fires well inside the 600s task timeout it
// exists to pre-empt.
func TestCodexApprovalWatchdog_RespectsInterval(t *testing.T) {
	session := testSession(t)
	d := &Daemon{session: session, lastCodexApprovalCheck: time.Now().Unix()}

	before := d.lastCodexApprovalCheck
	d.checkCodexApprovals()

	if d.lastCodexApprovalCheck != before {
		t.Error("a sweep inside the interval must not re-stamp the clock")
	}
	if codexApprovalCheckSecs < 5 || codexApprovalCheckSecs > 60 {
		t.Errorf("interval %ds is outside the useful band between the poll and the 600s timeout",
			codexApprovalCheckSecs)
	}
}
