package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Characterization tests for MUX-130 Phase 1: today's phase-parsing behaviour,
// pinned green before any fix so Phase 3 and Phase 4 change it on purpose. Each
// test asserts what the parser does NOW — several of these are the defects —
// and names the phase that inverts it.

// MUX-031's shape: a 2-phase implementation and a verification-notes section
// that names the same phases again. Phase 1 pinned 4 phases document-wide
// (Defect B); scoped to `## Implementation` they are the 2 real ones.
func TestParseSpecPhases_OnlyImplementationHeadingsArePhases(t *testing.T) {
	spec := "## Implementation\n\n### Phase 1: A\n\n- [x] a\n\n### Phase 2: B\n\n- [x] b\n\n" +
		"## Verification notes\n\n### Phase 1 (2026-08-31) — 1/1 steps\n\nShipped.\n\n### Phase 2 (2026-09-01) — 1/1 steps\n\n- [x] verified\n"
	phases, unscoped := scanSpecPhases(spec)
	if len(phases) != 2 || phases[0].Title != "Phase 1: A" || phases[1].Title != "Phase 2: B" || unscoped {
		t.Fatalf("parsed %+v unscoped=%v, want the 2 implementation phases, scoped", phases, unscoped)
	}
}

// MUX-077's shape: boxes under a phase heading in `## Acceptance criteria`.
// Phase 1 pinned that ticking them made a commit ready on that heading alone;
// scoped, it is no phase, so nothing is newly complete. Negative control: the
// implementation phase closing is still committable.
func TestNewlyCompletedPhases_ANonImplementationPhaseHeadingIsNeverCommittable(t *testing.T) {
	spec := func(criterion, build string) string {
		return "## Acceptance criteria\n\n### Phase 3 criteria\n\n- [" + criterion + "] criterion\n\n" +
			"## Implementation\n\n### Phase 1: Build\n\n- [" + build + "] step\n"
	}
	if newly := newlyCompletedPhases(parseSpecPhases(spec("x", " ")), parseSpecPhases(spec(" ", " "))); len(newly) != 0 {
		t.Errorf("newly completed = %+v, want none — the acceptance-criteria heading is no phase", newly)
	}
	if newly := newlyCompletedPhases(parseSpecPhases(spec("x", "x")), parseSpecPhases(spec("x", " "))); len(newly) != 1 || newly[0].Number != 1 {
		t.Errorf("newly completed = %+v, want the implementation Phase 1", newly)
	}
}

// The section match is a prefix of the heading text: `## Implementation
// phases` opens the section, and a section merely mentioning implementation,
// or a verification-notes section after it, does not.
func TestParseSpecPhases_ImplementationSectionMatch(t *testing.T) {
	for _, tc := range []struct {
		heading string
		phases  int
	}{
		{"## Implementation", 1},
		{"## Implementation phases", 1},
		{"## Implementation plan (revised)", 1},
		{"## Notes on implementation", 0},
		{"## Verification notes", 0},
		{"# Appendix", 0},
	} {
		spec := "## Requirements\n\n- [ ] r\n\n" + tc.heading + "\n\n### Phase 1: A\n\n- [ ] a\n"
		if tc.phases == 0 {
			spec = "## Implementation\n\n### Phase 9: Real\n\n- [ ] real\n\n" + tc.heading + "\n\n### Phase 1: A\n\n- [ ] a\n"
		}
		phases := parseSpecPhases(spec)
		got := 0
		for _, p := range phases {
			if p.Number == 1 {
				got++
			}
		}
		if got != tc.phases {
			t.Errorf("%q: Phase 1 parsed %d times, want %d (%+v)", tc.heading, got, tc.phases, phases)
		}
	}
}

// A level-1 heading after the implementation section ends it as a `##` does:
// a `### Phase 9` under `# Appendix` is no phase, so ticking its box ships
// nothing. Control: boxes under a `####` subheading inside a real phase still
// count for that phase.
func TestParseSpecPhases_ALevelOneHeadingEndsTheImplementationSection(t *testing.T) {
	spec := func(appendix string) string {
		return "## Implementation\n\n### Phase 1: Build\n\n#### Steps\n\n- [x] step\n\n" +
			"# Appendix\n\n### Phase 9: Notes\n\n- [" + appendix + "] note\n"
	}
	tree, head := parseSpecPhases(spec("x")), parseSpecPhases(spec(" "))
	if len(tree) != 1 || tree[0].Number != 1 || tree[0].Done != 1 {
		t.Fatalf("phases = %+v, want Phase 1 alone with its #### box", tree)
	}
	if newly := newlyCompletedPhases(tree, head); len(newly) != 0 {
		t.Errorf("newly completed = %+v, want none — the appendix heading is no phase", newly)
	}
}

// MUX-143's Defect A, landed here: a non-Phase `###` heading inside the
// implementation section ends the current phase, so boxes under `### Deferred`
// belong to no phase and never hold the loop — but SpecOpenItems still counts
// them, so the spec cannot close while one is open.
func TestParseSpecPhases_DeferredBoxesBelongToNoPhase(t *testing.T) {
	spec := "## Implementation\n\n### Phase 1: A\n\n- [x] a\n\n### Deferred\n\n- [ ] parked item\n\n### Phase 2: B\n\n- [x] b\n"
	phases := parseSpecPhases(spec)
	if len(phases) != 2 || !phases[0].Complete() || !phases[1].Complete() {
		t.Fatalf("phases = %+v, want Phases 1 and 2 complete with the deferred box in neither", phases)
	}
	if got, found := currentSpecPhase(phases); found {
		t.Errorf("current phase = %+v, want none — a deferred box holds no loop", got)
	}
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if n, _, err := SpecOpenItems(path); err != nil || n != 1 {
		t.Errorf("SpecOpenItems = %d %v, want the deferred box still open", n, err)
	}
}

// A spec with phase headings and no implementation section degrades
// explicitly: it is read document-wide, never as zero phases, and flagged
// unscoped so the gate text and guard can say so. Negative control: a spec
// with the section, or with no phases at all, is not flagged.
func TestScanSpecPhases_NoImplementationSectionDegradesExplicitly(t *testing.T) {
	phases, unscoped := scanSpecPhases("# S\n\n### Phase 1: A\n\n- [x] a\n\n### Phase 2: B\n\n- [ ] b\n")
	if len(phases) != 2 || !unscoped {
		t.Errorf("no implementation section: %d phases unscoped=%v, want both phases, flagged unscoped", len(phases), unscoped)
	}
	if _, unscoped := scanSpecPhases("## Implementation\n\n### Phase 1: A\n\n- [ ] a\n"); unscoped {
		t.Error("a scoped spec was flagged unscoped")
	}
	if _, unscoped := scanSpecPhases("# S\n\nNo phases here.\n"); unscoped {
		t.Error("a spec with no phase headings was flagged unscoped")
	}
	phases, unscoped = scanSpecPhases("## Implementation\n\nSee below.\n\n## Plan\n\n### Phase 1: A\n\n- [ ] a\n")
	if len(phases) != 1 || !unscoped {
		t.Errorf("an implementation section with no phase heading: %d phases unscoped=%v, want the phase read, flagged — never zero", len(phases), unscoped)
	}
}

// The warning reaches a dispatch naming a phase as a sentence of its own,
// never inside the phase name. Negative control: a scoped spec's dispatch, and
// a message naming no phase, carry none.
func TestInterpolateGraphMessage_UnscopedSpecCarriesTheWarning(t *testing.T) {
	g := &Graph{Name: "g", Start: "impl", Nodes: []Node{{ID: "impl", Type: NodeSend, Role: "build", Action: "build", Message: "Implement ${current_phase}"}}}
	run := createTestRun(t, g)

	writeSpecFixture(t, "# S\n### Phase 1: A\n- [ ] a\n")
	got := interpolateGraphMessage(runTestSession, run, "Implement ${current_phase}", "")
	if got != "Implement Phase 1: A "+SpecPhasesUnscopedWarning {
		t.Errorf("unscoped dispatch = %q, want the phase name then the warning sentence", got)
	}
	if got := interpolateGraphMessage(runTestSession, run, "Build it", ""); strings.Contains(got, "Warning") {
		t.Errorf("a message naming no phase carried the warning: %q", got)
	}

	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [ ] a\n")
	if got := interpolateGraphMessage(runTestSession, run, "Implement ${current_phase}", ""); got != "Implement Phase 1: A" {
		t.Errorf("scoped dispatch = %q, want no warning", got)
	}
}

// The real files the spec names: MUX-031 parses its 7 implementation phases,
// not 15. MUX-077 has no implementation section — its four phases sit under
// `## Acceptance criteria` — so it degrades explicitly: all four, flagged
// unscoped, never zero.
func TestSpecPhases_RealSpecsScoped(t *testing.T) {
	root := filepath.Join("..", "..", "..", "docs", "requirements", "completed")
	mux031 := filepath.Join(root, "MUX-031-graph-run-tui.md")
	if _, err := os.Stat(mux031); err != nil {
		t.Skipf("no checkout spec at %s", mux031)
	}
	if phases, err := SpecPhases(mux031); err != nil || len(phases) != 7 {
		t.Errorf("MUX-031: %d phases (%v), want 7", len(phases), err)
	}
	data, err := os.ReadFile(filepath.Join(root, "MUX-077-planner-agent.md"))
	if err != nil {
		t.Skip("no MUX-077 spec")
	}
	if phases, unscoped := scanSpecPhases(string(data)); len(phases) != 4 || !unscoped {
		t.Errorf("MUX-077: %d phases unscoped=%v, want its 4 acceptance-criteria phases read unscoped", len(phases), unscoped)
	}
}

// ${phase_hold} says why the loop holds (decision 4): the lowest open phase,
// its open items, and that an item left open on purpose holds it. With no open
// phase it never implies completeness while items remain outside the phases;
// control — a spec with nothing open says so honestly, with no hold wording.
func TestResolvePhaseHoldText(t *testing.T) {
	g := &Graph{Name: "g", Start: "gate", Nodes: []Node{{ID: "gate", Type: NodeWaitHuman, Message: "x"}}}
	createTestRun(t, g)

	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] keep open on purpose\n")
	got := resolvePhaseHoldText(runTestSession)
	for _, want := range []string{"Phase 2: B", "keep open on purpose", "left open on purpose holds the loop", "### Deferred"} {
		if !strings.Contains(got, want) {
			t.Errorf("hold text %q lacks %q", got, want)
		}
	}

	writeSpecFixture(t, "## Requirements\n- [ ] criterion outside\n## Implementation\n### Phase 1: A\n- [x] a\n")
	if got := resolvePhaseHoldText(runTestSession); !strings.Contains(got, "1 item(s) remain open outside the phases") || !strings.Contains(got, "criterion outside") {
		t.Errorf("no open phase with an open criterion: %q, want the items outside the phases named", got)
	}

	writeSpecFixture(t, "## Requirements\n- [x] criterion\n## Implementation\n### Phase 1: A\n- [x] a\n")
	if got := resolvePhaseHoldText(runTestSession); got != "Nothing in the spec is open." {
		t.Errorf("a finished spec = %q, want the honest completion text", got)
	}
}

// A node with message_no_open_phase dispatches it when the active spec has no
// open phase, chosen before substitution — never "(no open phase)" in a noun
// slot. Controls: with an open phase the ordinary message is used, and a node
// without the alternate keeps its message.
func TestNodeMessage_NoOpenPhaseSentence(t *testing.T) {
	n := &Node{ID: "impl", Type: NodeSpawn, Role: "edit", Message: "Implement ${current_phase}.", MessageNoOpenPhase: "Nothing to implement. ${phase_hold}"}
	g := &Graph{Name: "g", Start: "impl", Nodes: []Node{*n}}
	run := createTestRun(t, g)

	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [x] a\n")
	got := interpolateGraphMessage(runTestSession, run, nodeMessage(runTestSession, n), "")
	if got != "Nothing to implement. Nothing in the spec is open." || strings.Contains(got, "(no open phase)") {
		t.Errorf("no open phase: %q, want the alternate sentence", got)
	}

	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [ ] a\n")
	if got := interpolateGraphMessage(runTestSession, run, nodeMessage(runTestSession, n), ""); got != "Implement Phase 1: A." {
		t.Errorf("open phase: %q, want the ordinary message", got)
	}
	plain := &Node{ID: "p", Message: "Implement ${current_phase}."}
	if nodeMessage(runTestSession, plain) != plain.Message {
		t.Error("a node with no alternate changed its message")
	}
}

// The phase-progress decline names the lowest open phase, its open items and
// the deliberate hold (decision 4). Control: the guard still allows a
// legitimate phase-closing commit and writes no decline — a guard that always
// declines would pass every decline assertion.
func TestPhaseProgressDeclineNamesTheHold(t *testing.T) {
	isolateLifecycle(t)
	g := &Graph{Name: "g", Start: "commit", Nodes: []Node{{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "commit", Guard: GuardPhaseProgress}}}
	run := createTestRun(t, g)
	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [ ] left open\n")
	stubSpecAtHEAD(t, "## Implementation\n### Phase 1: A\n- [ ] left open\n")

	if phaseProgressGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Fatal("the guard allowed a commit with no newly complete phase")
	}
	rows := strings.Join(lifecycleDetails(t, "graph-guard-declined"), "\n")
	for _, want := range []string{"Phase 1: A", "left open", "left open on purpose holds the loop"} {
		if !strings.Contains(rows, want) {
			t.Errorf("decline %q lacks %q", rows, want)
		}
	}

	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [x] left open\n")
	if !phaseProgressGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Error("the guard declined a commit that closes Phase 1 in the tree while HEAD has it open")
	}
	if n := len(lifecycleDetails(t, "graph-guard-declined")); n != 1 {
		t.Errorf("decline rows = %d after an allowed commit, want still 1", n)
	}
}

// Duplicate numbers warn at dispatch — a line in the message and a
// spec-phase-numbers row — and are never refused. Control: a clean spec's
// dispatch carries no warning and writes no row.
func TestInterpolateGraphMessage_DuplicatePhaseNumbersWarn(t *testing.T) {
	isolateLifecycle(t)
	g := &Graph{Name: "g", Start: "impl", Nodes: []Node{{ID: "impl", Type: NodeSend, Role: "build", Action: "build", Message: "Implement ${current_phase}"}}}
	run := createTestRun(t, g)

	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [ ] a\n### Phase 1: Again\n- [ ] b\n")
	if got := interpolateGraphMessage(runTestSession, run, "Implement ${current_phase}", ""); !strings.Contains(got, "phase number(s) 1 repeat or are out of order") {
		t.Errorf("duplicate dispatch = %q, want the warning line", got)
	}
	if rows := lifecycleDetails(t, "spec-phase-numbers"); len(rows) != 1 {
		t.Errorf("spec-phase-numbers rows = %v, want one", rows)
	}

	writeSpecFixture(t, "## Implementation\n### Phase 1: A\n- [ ] a\n### Phase 2: B\n- [ ] b\n")
	if got := interpolateGraphMessage(runTestSession, run, "Implement ${current_phase}", ""); strings.Contains(got, "Warning") {
		t.Errorf("clean dispatch = %q, want no warning", got)
	}
	if rows := lifecycleDetails(t, "spec-phase-numbers"); len(rows) != 1 {
		t.Errorf("a clean dispatch wrote a row: %v", rows)
	}
}

// Decision 1 as it stands under HEAD anchoring: a phase complete in the tree
// behind an open earlier phase is committable. Recorded as today's behaviour,
// not a defect — declining it would be a new rule (spec Notes).
func TestNewlyCompletedPhases_APhaseBehindAnOpenEarlierPhaseIsCommittable(t *testing.T) {
	spec := func(box string) string {
		return "## Implementation\n\n### Phase 1: First\n\n- [ ] open\n\n### Phase 2: Second\n\n- [" + box + "] done\n"
	}
	newly := newlyCompletedPhases(parseSpecPhases(spec("x")), parseSpecPhases(spec(" ")))
	if len(newly) != 1 || newly[0].Number != 2 {
		t.Errorf("newly completed = %+v, want [Phase 2] committable behind open Phase 1", newly)
	}
}

// Defect D, inverted by the found result: Phase 1 pinned an open Phase 0
// losing to a later open phase, Number 0 doubling as "none found". Control:
// a spec with nothing open still reports none — absent, not zero.
func TestCurrentSpecPhase_AnOpenPhaseZeroIsCurrent(t *testing.T) {
	phases := parseSpecPhases("## Implementation\n\n### Phase 0: Pin\n\n- [ ] open zero\n\n### Phase 1: Fix\n\n- [ ] open one\n")
	if got, found := currentSpecPhase(phases); !found || got.Number != 0 {
		t.Errorf("current phase = %d found=%v, want the open Phase 0", got.Number, found)
	}
	if got, found := currentSpecPhase(parseSpecPhases("## Implementation\n\n### Phase 0: Pin\n\n- [x] done zero\n")); found {
		t.Errorf("a spec with nothing open reported %+v as current", got)
	}
}

// Strict refusal (the user, 2026-11-27): a spec read unscoped — its phase
// headings all outside an implementation section — is never commit-ready,
// though a ticked acceptance-criteria phase would otherwise be newly complete.
// The guard declines with the unscoped warning as its reason, and the
// pre-gate condition, reading the same predicate, fails with it. Control: a
// scoped spec closing an implementation phase is still committable.
func TestPhaseCommitReady_AnUnscopedSpecIsNeverCommittable(t *testing.T) {
	isolateLifecycle(t)
	g := &Graph{Name: "g", Start: "commit", Nodes: []Node{{ID: "commit", Type: NodeSend, Role: "plan", Action: "update-docs", Message: "commit", Guard: GuardPhaseProgress}}}
	run := createTestRun(t, g)
	ctx := &ChainContext{Session: runTestSession, GraphRun: run, Graph: g}
	cond := map[string]any{"spec_phase_committable": "commit"}

	writeSpecFixture(t, "# S\n\n## Acceptance criteria\n\n### Phase 1: Criteria\n\n- [x] criterion\n")
	stubSpecAtHEAD(t, "# S\n\n## Acceptance criteria\n\n### Phase 1: Criteria\n\n- [ ] criterion\n")
	if v := phaseCommitReady(runTestSession); v.ready || !v.unscoped {
		t.Errorf("unscoped verdict ready=%v unscoped=%v, want not ready and flagged", v.ready, v.unscoped)
	}
	if ok, res := EvaluateConditions(cond, ctx); ok || !strings.Contains(res[0].Detail, SpecPhasesUnscopedWarning) {
		t.Errorf("spec_phase_committable on an unscoped spec = %v %+v, want false naming the warning", ok, res)
	}
	if phaseProgressGuardAllows(runTestSession, run, &g.Nodes[0]) {
		t.Fatal("the guard allowed a commit on an unscoped spec")
	}
	if rows := strings.Join(lifecycleDetails(t, "graph-guard-declined"), "\n"); !strings.Contains(rows, "never commit-ready") || !strings.Contains(rows, SpecPhasesUnscopedWarning) {
		t.Errorf("decline %q does not name why", rows)
	}

	writeSpecFixture(t, "# S\n\n## Implementation\n\n### Phase 1: Build\n\n- [x] step\n")
	stubSpecAtHEAD(t, "# S\n\n## Implementation\n\n### Phase 1: Build\n\n- [ ] step\n")
	if v := phaseCommitReady(runTestSession); !v.ready || v.phase.Number != 1 {
		t.Errorf("scoped verdict = %+v, want Phase 1 ready", v)
	}
}

// spec_phases_remaining counts an open Phase 0 — before the found result a
// spec whose only open phase was Phase 0 read as nothing remaining and ended
// the loop. Control: with Phase 0 closed nothing remains.
func TestSpecPhasesRemainingCountsAnOpenPhaseZero(t *testing.T) {
	g := &Graph{Name: "g", Start: "impl", Nodes: []Node{{ID: "impl", Type: NodeSend, Role: "build", Action: "build", Message: "x"}}}
	run := createTestRun(t, g)
	ctx := &ChainContext{Session: runTestSession, GraphRun: run, Graph: g}
	cond := map[string]any{"spec_phases_remaining": true}

	writeSpecFixture(t, "## Implementation\n### Phase 0: Pin\n- [ ] open zero\n### Phase 1: Fix\n- [x] done one\n")
	if ok, res := EvaluateConditions(cond, ctx); !ok {
		t.Errorf("an open Phase 0 read as nothing remaining: %+v", res)
	}
	writeSpecFixture(t, "## Implementation\n### Phase 0: Pin\n- [x] done zero\n### Phase 1: Fix\n- [x] done one\n")
	if ok, res := EvaluateConditions(cond, ctx); ok {
		t.Errorf("a finished spec read as phases remaining: %+v", res)
	}
}

// Defect C: duplicate and non-monotonic numbers are accepted, as decision 3b
// asks — warned at dispatch (phaseNumberIssues), never refused — and these
// behaviours are stated in the doc comments of newlyCompletedPhases and
// SpecPhaseOpenItems. Control: a clean spec has no issues.
func TestSpecPhases_DuplicateAndNonMonotonicNumbers(t *testing.T) {
	spec := func(firstTwo string) string {
		return "## Implementation\n\n### Phase 2: Early\n\n- [" + firstTwo + "] early\n\n" +
			"### Phase 1: Then\n\n- [ ] one open\n\n### Phase 2: Again\n\n- [ ] two open\n"
	}
	tree, head := parseSpecPhases(spec("x")), parseSpecPhases(spec(" "))
	if got := []int{tree[0].Number, tree[1].Number, tree[2].Number}; got[0] != 2 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("file-order numbers = %v, want [2 1 2] accepted as written", got)
	}
	if got, _ := currentSpecPhase(tree); got.Number != 1 {
		t.Errorf("current phase = %d, want the lowest open number, 1", got.Number)
	}
	if got := phaseNumberIssues(tree); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("issues = %v, want [1 2]: 1 after a higher number, 2 repeated", got)
	}
	if got := phaseNumberIssues(parseSpecPhases("## Implementation\n\n### Phase 1: A\n\n- [ ] a\n\n### Phase 2: B\n\n- [ ] b\n")); len(got) != 0 {
		t.Errorf("a clean spec reported issues %v", got)
	}
	newly := newlyCompletedPhases(tree, head)
	if len(newly) != 1 || newly[0].Title != "Phase 2: Early" {
		t.Errorf("newly completed = %+v, want the first Phase 2 section alone, though another Phase 2 section is open", newly)
	}
	if again := newlyCompletedPhases(tree, tree); len(again) != 0 {
		t.Errorf("with the first Phase 2 complete at HEAD too, newly = %+v, want none: shipped is keyed by number", again)
	}

	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte(spec(" ")), 0644); err != nil {
		t.Fatal(err)
	}
	if n, items, err := SpecPhaseOpenItems(path, 2); err != nil || n != 2 {
		t.Errorf("SpecPhaseOpenItems(2) = %d %v %v, want both Phase 2 sections summed", n, items, err)
	}
}

// specPhaseHeading is one `### Phase N` heading found by surveyHeadings: its
// number, its enclosing `##` section and whether boxes sit under it.
type specPhaseHeading struct {
	number  int
	section string
	boxes   bool
}

// surveyHeadings walks a spec's headings independently of parseSpecPhases —
// fences, `##` sections and phase headings only — so the survey can check the
// Go parser against the heading rule rather than inherit its answer.
func surveyHeadings(content string) []specPhaseHeading {
	var out []specPhaseHeading
	inFence, section, inPhase := false, "", false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
		case inFence:
		case phaseHeadingRe.MatchString(trimmed):
			n, _ := strconv.Atoi(phaseHeadingRe.FindStringSubmatch(trimmed)[2])
			out = append(out, specPhaseHeading{number: n, section: section})
			inPhase = true
		case strings.HasPrefix(trimmed, "## "):
			section, inPhase = strings.TrimPrefix(trimmed, "## "), false
		case strings.HasPrefix(trimmed, "# "):
			section, inPhase = "", false
		case strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "####"):
			inPhase = false
		case inPhase && (openItemRe.MatchString(line) || doneItemRe.MatchString(line)):
			out[len(out)-1].boxes = true
		}
	}
	return out
}

// TestSpecPhaseSurvey reproduces MUX-130's 2026-11-24 survey of
// docs/requirements/ with the Go parser, logging the table for the spec to
// record rather than adjust — and re-runnable, so a misparse cannot return
// silently. Its assertion: for every spec, the phase numbers parseSpecPhases
// returns, in order, are the `### Phase N` headings the independent walk
// finds inside `## Implementation…` sections, or all of them for a spec with
// no such section (the unscoped degradation). Skipped outside a checkout.
func TestSpecPhaseSurvey(t *testing.T) {
	root := filepath.Join("..", "..", "..", "docs", "requirements")
	files, _ := filepath.Glob(filepath.Join(root, "*", "*.md"))
	if len(files) == 0 {
		t.Skipf("no specs under %s", root)
	}
	sort.Strings(files)
	var carrying, outside, outsideBoxes, live, dupes, zero, unscopedSpecs, stranded []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(filepath.Base(filepath.Dir(f)), filepath.Base(f))
		heads, parsed := surveyHeadings(string(data)), parseSpecPhases(string(data))
		var want []specPhaseHeading
		for _, h := range heads {
			if strings.HasPrefix(h.section, "Implementation") {
				want = append(want, h)
			}
		}
		if len(want) == 0 {
			want = heads
		}
		if len(want) != len(parsed) {
			t.Errorf("%s: Go parsed %d phases, the heading walk expects %d", name, len(parsed), len(want))
			continue
		}
		for i := range want {
			if want[i].number != parsed[i].Number {
				t.Errorf("%s: phase %d is %d to Go, %d to the heading walk", name, i, parsed[i].Number, want[i].number)
			}
		}
		if _, unscoped := scanSpecPhases(string(data)); unscoped {
			unscopedSpecs = append(unscopedSpecs, name)
		}
		if open, found := currentSpecPhase(parsed); found && !strings.HasPrefix(name, "completed") {
			for _, p := range parsed {
				if p.Complete() && p.Number > open.Number {
					stranded = append(stranded, fmt.Sprintf("%s: Phase %d behind open Phase %d", name, p.Number, open.Number))
				}
			}
		}
		if len(heads) == 0 {
			continue
		}
		carrying = append(carrying, name)
		isOutside, hasBoxes, hasZero := false, false, false
		seen, last, nonMono := map[int]bool{}, -1, false
		for _, h := range heads {
			hasZero = hasZero || h.number == 0
			if !strings.HasPrefix(h.section, "Implementation") {
				isOutside = true
				hasBoxes = hasBoxes || h.boxes
				continue
			}
			nonMono = nonMono || seen[h.number] || h.number < last
			seen[h.number], last = true, h.number
		}
		if isOutside {
			outside = append(outside, name)
			if !strings.HasPrefix(name, "completed") {
				live = append(live, name)
			}
		}
		if hasBoxes {
			outsideBoxes = append(outsideBoxes, name)
		}
		if nonMono {
			dupes = append(dupes, name)
		}
		if hasZero {
			zero = append(zero, name)
		}
	}
	t.Logf("MUX-130 survey over %d spec files (Go parser):", len(files))
	t.Logf("  specs carrying ### Phase N: %d", len(carrying))
	t.Logf("  with one outside a ## Implementation… section (no longer parsed as phases): %d %v", len(outside), outside)
	t.Logf("  with phase headings and no ## Implementation… section (read unscoped, warned): %d %v", len(unscopedSpecs), unscopedSpecs)
	t.Logf("  of those, outside headings holding checkboxes: %d %v", len(outsideBoxes), outsideBoxes)
	t.Logf("  outside-section specs in backlog/ or drafts/: %d %v", len(live), live)
	t.Logf("  duplicate or non-monotonic numbers inside the implementation section: %d %v", len(dupes), dupes)
	t.Logf("  specs with a Phase 0: %d %v", len(zero), zero)
	t.Logf("  backlog/ and drafts/ phases complete behind an open earlier phase (scoped parser): %d %v", len(stranded), stranded)
}
