package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// pinGateAuthorityConfig points the authority read at a scratch config file.
//
// It overrides the path list directly rather than setting $MUXCODE_CONFIG: the
// production reader deliberately ignores that variable, because honouring it
// would let a caller choose which file the check consults.
func pinGateAuthorityConfig(t *testing.T, value string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	body := "MUXCODE_GATE_AUTHORITY_ROLES=" + value + "\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	orig := gateAuthorityConfigPaths
	gateAuthorityConfigPaths = func() []string { return []string{path} }
	t.Cleanup(func() { gateAuthorityConfigPaths = orig })
}

// unsealGateAuthority drops any seal, in this test and after it. The seal is
// process-wide by design, so one test sealing it would otherwise decide what
// every later test reads.
func unsealGateAuthority(t *testing.T) {
	t.Helper()
	clear := func() {
		gateSealMu.Lock()
		sealedGateRoles, gateAuthoritySealed = nil, false
		gateSealMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// restoreProductionAuthorityPaths puts the shipped path resolution back, for a
// test whose subject is the paths themselves rather than the value on them.
func restoreProductionAuthorityPaths(t *testing.T) {
	t.Helper()
	orig := gateAuthorityConfigPaths
	gateAuthorityConfigPaths = defaultGateAuthorityConfigPaths
	t.Cleanup(func() { gateAuthorityConfigPaths = orig })
}

// TestSealedGateAuthorityIgnoresLaterWidening covers what the config-file read
// could not: the file is agent-writable, so an agent refused a gate can edit the
// list and try again. The daemon seals the list at startup and never re-reads it
// upward, so that edit buys nothing until someone restarts the daemon.
func TestSealedGateAuthorityIgnoresLaterWidening(t *testing.T) {
	pinCompiledAuthorities(t)

	SealGateAuthority()
	pinGateAuthorityConfig(t, "user,build")

	if deny := CheckGateApprovalAuthority("build", nil); deny == "" {
		t.Error("a config edited after the seal widened the authority — the seal must hold for the daemon's lifetime")
	}
	if deny := CheckGateApprovalAuthority(ActorUser, nil); deny != "" {
		t.Errorf("the sealed authority stopped admitting the user (%q) — sealing freezes the list, it does not empty it", deny)
	}

	// Negative control: the same widened config DOES admit build unsealed, so the
	// refusal above is the seal at work and not the file being ignored outright.
	unsealGateAuthority(t)
	if deny := CheckGateApprovalAuthority("build", nil); deny != "" {
		t.Errorf("unsealed, a config granting build refused it (%q)", deny)
	}
}

// TestSealedGateAuthorityHonorsLaterNarrowing pins the asymmetry. A seal that
// froze the list in both directions would make an emergency lockdown wait for a
// daemon restart, and shutting gates is never the move an agent wants.
func TestSealedGateAuthorityHonorsLaterNarrowing(t *testing.T) {
	pinCompiledAuthorities(t)
	pinGateAuthorityConfig(t, "user,build")
	SealGateAuthority()

	if deny := CheckGateApprovalAuthority("build", nil); deny != "" {
		t.Fatalf("the sealed list refused a role it sealed (%q) — the premise of the narrowing below", deny)
	}

	pinGateAuthorityConfig(t, "user")
	if deny := CheckGateApprovalAuthority("build", nil); deny == "" {
		t.Error("narrowing the config left build authorized — a lockdown must not wait for a daemon restart")
	}
	if deny := CheckGateApprovalAuthority(ActorUser, nil); deny != "" {
		t.Errorf("narrowing refused the user too (%q) — it must remove only what it removed", deny)
	}
}

// TestSealedGateAuthorityIgnoresHomeSelectedConfig pins the last
// caller-reachable input to the authority read, and that sealing is what closes
// it.
//
// gateAuthorityConfigPaths stopped honouring $MUXCODE_CONFIG in 31a2ca4, but its
// second entry sits under os.UserHomeDir — which is $HOME — so an UNSEALED read
// is still steered by `HOME=/tmp/mine muxcode graph approve …`, the same hole one
// variable over. The control half below asserts that; if a later change closes
// it, invert that half rather than deleting it.
func TestSealedGateAuthorityIgnoresHomeSelectedConfig(t *testing.T) {
	pinCompiledAuthorities(t)
	SealGateAuthority()
	restoreProductionAuthorityPaths(t)

	home := t.TempDir()
	dir := filepath.Join(home, ".config", "muxcode")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir planted config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte("MUXCODE_GATE_AUTHORITY_ROLES=user,build\n"), 0644); err != nil {
		t.Fatalf("write planted config: %v", err)
	}
	t.Setenv("HOME", home)

	if deny := CheckGateApprovalAuthority("build", nil); deny == "" {
		t.Error("a config under a planted $HOME widened a sealed authority — the seal must not re-read the caller's paths")
	}

	// Control: unsealed, the planted $HOME does reach the read. It records the
	// residual hole this seal exists to cover, and proves the assertion above is
	// about sealing rather than about the file never being consulted. It assumes
	// no ./.muxcode/config in the package directory shadows the $HOME entry.
	unsealGateAuthority(t)
	if deny := CheckGateApprovalAuthority("build", nil); deny != "" {
		t.Errorf("unsealed, a planted $HOME did not reach the authority read (%q) — that hole is closed; invert this control", deny)
	}
}

// armGate drives actorGateGraph's send node to success so its gate is waiting.
func armGate(t *testing.T, runID string) {
	t.Helper()
	step(t, runTestSession, runID)
	completeSendNode(t, runTestSession, runID, "a", OutcomeSuccess)
	step(t, runTestSession, runID)
	if s := nodeState(t, runTestSession, runID, "gate"); s != GraphNodeWaiting {
		t.Fatalf("gate state %q, want waiting before approval", s)
	}
}

// TestHarvestWaitHumanRefusesUnauthorizedApproval is the daemon-side half of the
// authority: ApproveGraphGate's refusal runs in the approver's own process, so a
// marker written straight to disk never meets it. The executor re-decides on the
// recorded approver, and refuses the release rather than trusting the file.
//
// The unattributed case is a deliberate departure from "approved_by is
// additive": a grant naming nobody cannot be checked against anyone, and the
// markers that predate the field live only in a session's /tmp bus directory.
func TestHarvestWaitHumanRefusesUnauthorizedApproval(t *testing.T) {
	pinCompiledAuthorities(t)
	pinActor(t, "")

	for _, tc := range []struct {
		name   string
		marker map[string]any
	}{
		{"an agent naming itself", map[string]any{"approved_at": time.Now().Unix(), "approved_by": "build"}},
		{"a forged user approval", map[string]any{"approved_at": time.Now().Unix(), "approved_by": ActorUser}},
		{"an unattributed grant", map[string]any{"approved_at": time.Now().Unix()}},
	} {
		run := createTestRun(t, actorGateGraph())
		armGate(t, run.ID)
		if err := atomicWriteJSON(graphApprovalPath(runTestSession, run.ID, "gate", "approved"), tc.marker); err != nil {
			t.Fatalf("%s: write marker: %v", tc.name, err)
		}

		step(t, runTestSession, run.ID)
		if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeWaiting {
			t.Errorf("%s: gate state %q, want waiting — the daemon honoured a grant it would have refused", tc.name, s)
		}
		if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodePending {
			t.Errorf("%s: commit node state %q, want pending", tc.name, s)
		}
		assertNoApproval(t, run.ID, "gate")
	}

	// Negative control: a person's grant opens the same gate, so the refusals
	// above are about the approver and not about gates that never open.
	run := createTestRun(t, actorGateGraph())
	armGate(t, run.ID)
	if err := ApproveGraphGate(runTestSession, run.ID, "gate"); err != nil {
		t.Fatalf("a person's approval was refused: %v", err)
	}
	step(t, runTestSession, run.ID)
	if s := nodeState(t, runTestSession, run.ID, "gate"); s != GraphNodeDone {
		t.Errorf("gate state %q after a person's approval, want done", s)
	}
	if s := nodeState(t, runTestSession, run.ID, "c"); s != GraphNodeRunning {
		t.Errorf("commit node state %q, want running", s)
	}
}

// The second half of the P1: closing the environment READ left the config
// PATH env-derived, so an agent could point the check at a file it wrote —
// `MUXCODE_CONFIG=/tmp/x muxcode graph approve …`. The authority read must
// ignore that variable entirely.
func TestGateAuthorityIgnoresConfigPathOverride(t *testing.T) {
	pinCompiledAuthorities(t)

	planted := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(planted, []byte("MUXCODE_GATE_AUTHORITY_ROLES=user,build\n"), 0644); err != nil {
		t.Fatalf("write planted config: %v", err)
	}
	t.Setenv("MUXCODE_CONFIG", planted)

	for _, actor := range GateApprovalAuthority() {
		if NormalizeBusRole(actor) == "build" {
			t.Fatal("a planted config reached the authority via $MUXCODE_CONFIG — the path must be fixed")
		}
	}

	// Negative control: the same file DOES grant when it is a path the reader
	// actually consults, so the test above is about the env var and not about
	// the file being ignored outright.
	pinGateAuthorityConfig(t, "user,build")
	found := false
	for _, actor := range GateApprovalAuthority() {
		if NormalizeBusRole(actor) == "build" {
			found = true
		}
	}
	if !found {
		t.Error("a config on a consulted path must still grant authority")
	}
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
