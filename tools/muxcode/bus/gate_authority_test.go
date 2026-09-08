package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MUX-144 Phase 2 — the authority half of the gate.
//
// These invert the Defect A pins that lived in graph_bypass_test.go: what used
// to be characterized as "any role may open the gate" is now characterized as
// "only an authorized actor may, and never on its own run".

// gateApprover reads the approver identity a release recorded.
func gateApprover(t *testing.T, runID, nodeID string) string {
	t.Helper()
	data, err := os.ReadFile(graphApprovalPath(runTestSession, runID, nodeID, "approved"))
	if err != nil {
		t.Fatalf("read approval marker: %v", err)
	}
	return approvalGrantedBy(data)
}

// assertNoApproval fails unless the gate is still shut.
//
// A refusal that returned an error and wrote the marker anyway would be no
// refusal at all: the daemon reads the marker, not the exit code.
func assertNoApproval(t *testing.T, runID, nodeID string) {
	t.Helper()
	if _, err := os.Stat(graphApprovalPath(runTestSession, runID, nodeID, "approved")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("approval marker exists after a refused approval (stat err %v) — the gate opened anyway", err)
	}
}

// TestGateApprovalAuthorityDefault pins what ships. The acceptance criterion is
// "narrower than every agent"; the shipped answer is narrower still — no agent
// at all — and that is a decision worth failing a test over if it drifts.
func TestGateApprovalAuthorityDefault(t *testing.T) {
	pinCompiledAuthorities(t)

	got := GateApprovalAuthority()
	if len(got) != 1 || got[0] != ActorUser {
		t.Fatalf("GateApprovalAuthority() = %v, want [%s]", got, ActorUser)
	}
	for _, role := range KnownRoles {
		if deny := CheckGateApprovalAuthority(role, nil); deny == "" {
			t.Errorf("role %q may approve a gate by default — the default must admit no agent", role)
		}
	}
	// Without this a check that refused everyone would pass the loop above,
	// pinning an unusable gate rather than a narrow one.
	if deny := CheckGateApprovalAuthority(ActorUser, nil); deny != "" {
		t.Errorf("a person may not approve by default (%q) — the default must admit someone", deny)
	}
}

// TestApproveGraphGateRefusesAgentRoles is the inversion of the Defect A pin:
// the roles that could each open any gate are now each refused, and the gate is
// still shut afterwards.
func TestApproveGraphGateRefusesAgentRoles(t *testing.T) {
	pinCompiledAuthorities(t)
	for _, role := range []string{"build", "test", "review", "plan", "auto"} {
		pinActor(t, role)
		run := createTestRun(t, actorGateGraph())

		err := ApproveGraphGate(runTestSession, run.ID, "gate")
		if err == nil {
			t.Fatalf("ApproveGraphGate as %s succeeded — the gate is unguarded again", role)
		}
		if !strings.Contains(err.Error(), role) {
			t.Errorf("refusal %q does not name the refused actor %q", err, role)
		}
		assertNoApproval(t, run.ID, "gate")
	}
}

// TestApproveGraphGateRefusesSelfApproval covers the criterion "an autonomous
// agent cannot approve a gate on a run it created" at its sharpest: the agent
// holds the authority and is still refused, because the run is its own.
//
// The second half is the discriminator. Without it a rule that simply refused
// auto everywhere would pass, and the criterion is about the pairing rather
// than about the role.
func TestApproveGraphGateRefusesSelfApproval(t *testing.T) {
	pinCompiledAuthorities(t)
	pinGateAuthorityConfig(t, "user,auto")

	pinActor(t, "auto")
	own := createTestRun(t, actorGateGraph())
	if own.CreatedBy != "auto" {
		t.Fatalf("run.CreatedBy = %q, want auto", own.CreatedBy)
	}
	err := ApproveGraphGate(runTestSession, own.ID, "gate")
	if err == nil {
		t.Fatal("auto approved a gate on the run it created")
	}
	if !strings.Contains(err.Error(), own.ID) {
		t.Errorf("refusal %q does not name the run it refuses", err)
	}
	assertNoApproval(t, own.ID, "gate")

	pinActor(t, "")
	theirs := createTestRun(t, actorGateGraph())
	pinActor(t, "auto")
	if err := ApproveGraphGate(runTestSession, theirs.ID, "gate"); err != nil {
		t.Errorf("auto refused a gate on a run it did not create: %v — the rule must turn on the pairing", err)
	}
}

// TestApproveGraphGateAllowsUser is Phase 2's negative control: the human path
// is unchanged. A person approves, the marker lands with their identity, and
// approving their own run is not self-approval.
func TestApproveGraphGateAllowsUser(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "")
	run := createTestRun(t, actorGateGraph())

	if run.CreatedBy != ActorUser {
		t.Fatalf("run.CreatedBy = %q, want %s", run.CreatedBy, ActorUser)
	}
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("a person's approval was refused: %v", err)
	}
	if by := gateApprover(t, run.ID, "gate"); by != ActorUser {
		t.Errorf("approved_by = %q, want %s", by, ActorUser)
	}
}

// TestApproveGraphGateRefusesUnidentifiedApprovers covers the two ways an agent
// reaches the CLI without admitting to being one. Neither may be read as a
// person: ancestry names the runtime when the environment does not, and a probe
// that cannot answer fails closed.
//
// The unknown case is asserted with "unknown" explicitly authorized, so it pins
// a rule the configuration cannot switch off rather than an absence from the
// default list.
func TestApproveGraphGateRefusesUnidentifiedApprovers(t *testing.T) {
	pinCompiledAuthorities(t)

	pinAgentAncestry(t, "/usr/local/bin/claude")
	stripped := createTestRun(t, actorGateGraph())
	if err := ApproveGraphGate(runTestSession, stripped.ID, "gate"); err == nil {
		t.Error("an agent that stripped AGENT_ROLE opened the gate — ancestry must still name it")
	}
	assertNoApproval(t, stripped.ID, "gate")

	pinGateAuthorityConfig(t, "user,"+ActorUnknown)
	pinActor(t, "")
	pinProcessTable(t, "", errors.New("ps unavailable"))
	unreadable := createTestRun(t, actorGateGraph())
	if err := ApproveGraphGate(runTestSession, unreadable.ID, "gate"); err == nil {
		t.Error("an unreadable process table opened the gate — an unidentified approver must be refused")
	}
	assertNoApproval(t, unreadable.ID, "gate")
}

// TestApproveGraphGateHonorsConfiguredAuthority pins both ends of the
// configuration: an opted-in agent may release a gate it did not raise, and the
// empty list shuts every gate including the user's.
func TestApproveGraphGateHonorsConfiguredAuthority(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "")
	run := createTestRun(t, actorGateGraph())

	pinActor(t, "build")
	pinGateAuthorityConfig(t, "user,build")
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("an authorized role was refused: %v", err)
	}

	shut := createTestRun(t, actorGateGraph())
	pinActor(t, "")
	pinGateAuthorityConfig(t, "")
	if err := ApproveGraphGate(runTestSession, shut.ID, "gate"); err == nil {
		t.Error("the empty authority list let the user approve — it must deny every actor")
	}
	assertNoApproval(t, shut.ID, "gate")
}

// pinGateAuthorityConfig writes the gate authority into a scratch config file,
// which is the only store GateApprovalAuthority reads.
func pinGateAuthorityConfig(t *testing.T, value string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	body := "MUXCODE_GATE_AUTHORITY_ROLES=" + value + "\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("MUXCODE_CONFIG", path)
}

// The P1 this closes: the authority was read from the caller's own environment,
// so an agent refused a gate could re-run the identical command with the
// variable prefixed and let itself through. The env must now carry no weight.
func TestGateAuthorityIgnoresCallerEnvironment(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "")
	run := createTestRun(t, actorGateGraph())

	pinActor(t, "build")
	t.Setenv("MUXCODE_GATE_AUTHORITY_ROLES", "user,build")
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err == nil {
		t.Error("an agent self-authorized by setting the authority in its own environment")
	}
	assertNoApproval(t, run.ID, "gate")

	// Negative control: the same value in the config file DOES authorize, so the
	// fix cannot degenerate into "the override never works".
	pinGateAuthorityConfig(t, "user,build")
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("the configured authority must still admit build: %v", err)
	}
}
