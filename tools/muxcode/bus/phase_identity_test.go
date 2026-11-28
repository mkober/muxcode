package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MUX-143 Defect B: a run carries two phase identities — the frozen
// IntentPhase(run.Intent) and the live SpecCurrentPhase. Phase 1 pinned that
// nothing compared them; these are its characterizations inverted, with the
// negative controls. The `### Deferred` rule (Defect A) is pinned by MUX-130's
// TestParseSpecPhases_DeferredBoxesBelongToNoPhase.

const divergentSpec = "## Implementation\n### Phase 2: Config and CDK\n- [ ] open two\n### Phase 5: Ship\n- [x] done five\n"

func phaseCompleteGraph() *Graph {
	return &Graph{Name: "g", Start: "ship", Nodes: []Node{{ID: "ship", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "ship", Guard: GuardPhaseComplete}}}
}

// The 2026-09-03 launch: intent "Phase 5", lowest open phase 2 — Phase 1
// pinned it silent. It now warns naming both, and the unscoped-guard warning
// stays silent, so the launch gains one warning, not two. Controls: an
// agreeing intent is silent; an intent naming no phase keeps only the
// existing unscoped-guard warning.
func TestPhaseIdentityWarning_WarnsOnADivergentIntent(t *testing.T) {
	createTestRun(t, phaseCompleteGraph())
	writeSpecFixture(t, divergentSpec)

	w := PhaseIdentityWarning(runTestSession, "PBP1-1 — Phase 5: Ship")
	if !strings.Contains(w, "Phase 5") || !strings.Contains(w, "Phase 2: Config and CDK") {
		t.Errorf("divergent intent: warning %q, want both phases named", w)
	}
	if got := UnscopedPhaseGuardWarning(phaseCompleteGraph(), "PBP1-1 — Phase 5: Ship"); got != "" {
		t.Errorf("a phase-naming intent also drew the unscoped warning %q", got)
	}
	if got := PhaseIdentityWarning(runTestSession, "PBP1-1 — Phase 2: Config and CDK"); got != "" {
		t.Errorf("agreeing intent warned: %q", got)
	}
	if got := PhaseIdentityWarning(runTestSession, "PBP1-1 ship it"); got != "" {
		t.Errorf("no-phase intent drew the mismatch warning %q", got)
	}
	if got := UnscopedPhaseGuardWarning(phaseCompleteGraph(), "PBP1-1 ship it"); got == "" {
		t.Error("no-phase intent lost its existing unscoped-guard warning")
	}
}

// A named Phase 0 is a phase, not "none" (MUX-130), but the phase-complete
// guard passes it through unchecked — its policy, kept — and every
// diagnostic must say so truthfully. Through the real guard:
//   - diverging (derived Phase 2): the mismatch warning names both and says
//     the guard passes Phase 0 through unchecked, never that it checks it;
//     the launch row is written; the guard passes and logs the mismatch
//     without claiming an item check
//   - agreeing (an open Phase 0): no mismatch warning or row, the guard still
//     passes — and the unscoped warning still says Phase 0 is unchecked
func TestPhaseIdentityWarning_PhaseZeroIsReportedTruthfully(t *testing.T) {
	isolateLifecycle(t)
	g := phaseCompleteGraph()
	run := createTestRun(t, g)
	const intent = "PBP1-1 — Phase 0: Pin"
	mutateRunIntent(t, run.ID, intent)
	run, err := ReadGraphRun(runTestSession, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := UnscopedPhaseGuardWarning(g, intent); !strings.Contains(got, "names Phase 0, which the guard does not check") {
		t.Errorf("unscoped warning for a Phase 0 intent = %q, want the unchecked pass-through named", got)
	}

	writeSpecFixture(t, divergentSpec)
	w := PhaseIdentityWarning(runTestSession, intent)
	if !strings.Contains(w, "Phase 2: Config and CDK") || !strings.Contains(w, "passes Phase 0 through unchecked") || strings.Contains(w, "checks Phase 0") {
		t.Errorf("diverging Phase 0 warning = %q, want both phases and the unchecked pass-through, never a check", w)
	}
	if _, err := CreateGraphRun(runTestSession, g, "g", intent); err != nil {
		t.Fatal(err)
	}
	if rows := lifecycleDetails(t, "graph-phase-identity-mismatch"); len(rows) != 1 {
		t.Errorf("diverging Phase 0 launch rows = %v, want one", rows)
	}
	if !phaseCompleteGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Fatal("the guard declined a Phase 0 intent — pass-through is its policy")
	}
	rows := lifecycleDetails(t, "graph-guard-phase-mismatch")
	if len(rows) != 1 || !strings.Contains(rows[0], "Phase 0 through unchecked") || !strings.Contains(rows[0], "Phase 2: Config and CDK") {
		t.Errorf("guard mismatch rows = %v, want one naming the unchecked pass and the derived phase", rows)
	}

	writeSpecFixture(t, "## Implementation\n### Phase 0: Pin\n- [ ] open zero\n### Phase 1: Fix\n- [ ] open one\n")
	if got := PhaseIdentityWarning(runTestSession, intent); got != "" {
		t.Errorf("agreeing Phase 0 drew the mismatch warning %q", got)
	}
	if !phaseCompleteGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Fatal("the guard declined an agreeing Phase 0 intent")
	}
	if rows := lifecycleDetails(t, "graph-guard-phase-mismatch"); len(rows) != 1 {
		t.Errorf("an agreeing Phase 0 logged a mismatch: %v", rows)
	}
}

// Defect A, blessed: an item parked under `### Deferred` inside
// `## Implementation` belongs to no phase, so derivation moves on to the next
// phase with open work. Negative control: the same item left inside its
// phase pins derivation there, as it always did. The answer is re-derived
// from the file on every read — moving the item back and forth flips it with
// no stored cursor (MUX-121 decision 1).
func TestDeferredItemLetsDerivationMoveOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	parked := "## Implementation\n### Phase 1: Config\n- [x] done\n### Deferred\n- [ ] piiAllowedRoles — on a user decision\n### Phase 2: Next\n- [ ] next work\n"
	inPhase := "## Implementation\n### Phase 1: Config\n- [x] done\n- [ ] piiAllowedRoles — on a user decision\n### Phase 2: Next\n- [ ] next work\n"
	current := func(content string) int {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		p, found, err := SpecCurrentPhase(path)
		if err != nil || !found {
			t.Fatalf("no current phase: %v", err)
		}
		return p.Number
	}
	if got := current(parked); got != 2 {
		t.Errorf("parked item: current phase %d, want 2 — the deferred box must not hold Phase 1", got)
	}
	if got := current(inPhase); got != 1 {
		t.Errorf("control, item inside Phase 1: current phase %d, want 1", got)
	}
	if got := current(parked); got != 2 {
		t.Errorf("parked again: current phase %d, want 2 — derivation must re-read, not remember", got)
	}
}

// A deferred item is parked, not done: the spec-complete guard still refuses
// to close the spec while it is open, naming it. Control: ticked, the guard
// allows.
func TestDeferredItemStillBlocksTheSpecCompleteGuard(t *testing.T) {
	isolateLifecycle(t)
	g := &Graph{Name: "g", Start: "close", Nodes: []Node{{ID: "close", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "close", Guard: GuardSpecComplete}}}
	run := createTestRun(t, g)

	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [x] a\n### Deferred\n- [ ] parked on a user decision\n")
	if specCompleteGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Fatal("the spec-complete guard allowed close-out with a deferred item open")
	}
	if rows := strings.Join(lifecycleDetails(t, "graph-guard-declined"), "\n"); !strings.Contains(rows, "parked on a user decision") {
		t.Errorf("decline %q does not name the deferred item", rows)
	}
	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [x] a\n### Deferred\n- [x] parked on a user decision\n")
	if !specCompleteGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Error("the spec-complete guard declined a spec with nothing open")
	}
}

// The warning is a lifecycle row on the run, so a run an agent launched leaves
// it on record. Control: an agreeing launch writes none.
func TestCreateGraphRun_LogsAPhaseIdentityMismatch(t *testing.T) {
	isolateLifecycle(t)
	createTestRun(t, phaseCompleteGraph())
	writeSpecFixture(t, divergentSpec)

	if _, err := CreateGraphRun(runTestSession, phaseCompleteGraph(), "g", "PBP1-1 — Phase 2: Config and CDK"); err != nil {
		t.Fatal(err)
	}
	if rows := lifecycleDetails(t, "graph-phase-identity-mismatch"); len(rows) != 0 {
		t.Errorf("agreeing launch wrote %v", rows)
	}
	run, err := CreateGraphRun(runTestSession, phaseCompleteGraph(), "g", "PBP1-1 — Phase 5: Ship")
	if err != nil {
		t.Fatal(err)
	}
	rows := lifecycleDetails(t, "graph-phase-identity-mismatch")
	if len(rows) != 1 || !strings.Contains(rows[0], run.ID) || !strings.Contains(rows[0], "Phase 2: Config and CDK") {
		t.Errorf("divergent launch rows = %v, want one naming the run and the derived phase", rows)
	}
}

// The vacuous pass, no longer silent: the guard still passes on the intent's
// complete Phase 5 (warn, not refuse) and logs that the worker was given
// Phase 2. Control: a spec with no open phase passes with no mismatch row.
func TestPhaseCompleteGuard_LogsThePassOnTheIntentPhase(t *testing.T) {
	isolateLifecycle(t)
	g := phaseCompleteGraph()
	run := createTestRun(t, g)
	mutateRunIntent(t, run.ID, "PBP1-1 — Phase 5: Ship")
	run, err := ReadGraphRun(runTestSession, run.ID)
	if err != nil {
		t.Fatal(err)
	}

	writeSpecFixture(t, divergentSpec)
	if !phaseCompleteGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Fatal("the guard declined — the decision is warn, not refuse")
	}
	rows := lifecycleDetails(t, "graph-guard-phase-mismatch")
	if len(rows) != 1 || !strings.Contains(rows[0], "Phase 5") || !strings.Contains(rows[0], "Phase 2: Config and CDK") {
		t.Errorf("mismatch rows = %v, want one naming both phases", rows)
	}

	writeSpecFixture(t, "## Implementation\n### Phase 2: Config and CDK\n- [x] two\n### Phase 5: Ship\n- [x] five\n")
	if !phaseCompleteGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Fatal("the guard declined a finished spec")
	}
	if rows := lifecycleDetails(t, "graph-guard-phase-mismatch"); len(rows) != 1 {
		t.Errorf("a finished spec logged a mismatch: %v", rows)
	}
}
