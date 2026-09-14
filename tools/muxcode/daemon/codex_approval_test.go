package daemon

import (
	"os"
	"testing"
	"time"
)

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
