package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecOpenItems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	content := "# T\n\n" +
		"- [x] done item\n" +
		"- [ ] open one\n" +
		"  - [ ] nested open\n" +
		"- [X] loudly done\n" +
		"prose mentioning - [ ] mid-line is not a checkbox\n" +
		"```markdown\n" +
		"- [ ] fenced example must not count\n" +
		"```\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	count, names, err := SpecOpenItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (checked and mid-line rows must not count)", count)
	}
	if len(names) != 2 || names[0] != "open one" || names[1] != "nested open" {
		t.Errorf("names = %v, want [open one, nested open]", names)
	}
}

func TestSpecOpenItemsFullyChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte("# T\n- [x] a\n- [x] b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	count, names, err := SpecOpenItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(names) != 0 {
		t.Errorf("fully-checked spec: count=%d names=%v, want 0/empty", count, names)
	}
}

func TestSpecOpenItemsBareCheckbox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte("- [ ]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	count, names, err := SpecOpenItems(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || names[0] != "(unnamed item)" {
		t.Errorf("bare checkbox: count=%d names=%v, want 1/[(unnamed item)]", count, names)
	}
}

func TestIntentPhase(t *testing.T) {
	cases := map[string]int{
		"MUX-115 Turn Budget — Phase 1: Turn trace": 1,
		"phase 12 cleanup":                          12,
		"no phase named here":                       0,
		"":                                          0,
	}
	for in, want := range cases {
		if got := IntentPhase(in); got != want {
			t.Errorf("IntentPhase(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSpecPhaseOpenItems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	content := "# T\n\n" +
		"### Phase 1: Done work\n- [x] finished\n\n" +
		"### Phase 2: Current\n- [ ] open in two\n- [x] done in two\n\n" +
		"### Phase 10: Far future\n- [ ] open in ten\n\n" +
		"## Status\n- [ ] outside any phase\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	count, names, err := SpecPhaseOpenItems(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || names[0] != "open in two" {
		t.Errorf("phase 2: count=%d names=%v, want 1/[open in two]", count, names)
	}

	// Digit boundary: Phase 1 must not absorb Phase 10's items.
	if count, _, _ := SpecPhaseOpenItems(path, 1); count != 0 {
		t.Errorf("phase 1 = %d open, want 0 — must not match Phase 10", count)
	}
	if count, _, _ := SpecPhaseOpenItems(path, 10); count != 1 {
		t.Errorf("phase 10 = %d open, want 1", count)
	}
	// A phase the spec lacks reports zero.
	if count, _, _ := SpecPhaseOpenItems(path, 7); count != 0 {
		t.Errorf("missing phase = %d open, want 0", count)
	}
}

func TestUnscopedPhaseGuardWarning(t *testing.T) {
	guarded := &Graph{Nodes: []Node{{ID: "commit", Guard: GuardPhaseComplete}}}
	if w := UnscopedPhaseGuardWarning(guarded, "free-text intent"); w == "" {
		t.Error("guarded graph with no phase in the intent must warn")
	}
	if w := UnscopedPhaseGuardWarning(guarded, "ship Phase 2: things"); w != "" {
		t.Errorf("phase-scoped intent must not warn: %s", w)
	}
	unguarded := &Graph{Nodes: []Node{{ID: "commit"}}}
	if w := UnscopedPhaseGuardWarning(unguarded, "free-text intent"); w != "" {
		t.Errorf("unguarded graph must not warn: %s", w)
	}
}

// TestSpecCurrentPhase pins stateless phase derivation (MUX-121): the
// current phase is the lowest-numbered one with open items, so a
// completed phase can never be derived again — the re-implementation
// failure observed three times on 2026-08-28.
func TestSpecCurrentPhase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	content := "# T\n\n" +
		"### Phase 1: Done\n- [x] finished\n\n" +
		"### Phase 3: Later\n- [ ] future work\n\n" +
		"### Phase 2: Next\n- [ ] open step\n- [x] done step\n\n" +
		"```markdown\n### Phase 9: fenced\n- [ ] not real\n```\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	phases, err := SpecPhases(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) != 3 {
		t.Fatalf("phases = %d, want 3 (fenced heading must not count): %+v", len(phases), phases)
	}

	cur, err := SpecCurrentPhase(path)
	if err != nil {
		t.Fatal(err)
	}
	// Lowest OPEN phase wins even when headings are out of file order.
	if cur.Number != 2 || !strings.Contains(cur.Title, "Phase 2: Next") {
		t.Errorf("current = %+v, want Phase 2 (never the completed Phase 1)", cur)
	}

	done, err := SpecCompletedPhaseCount(path)
	if err != nil {
		t.Fatal(err)
	}
	if done != 1 {
		t.Errorf("completed phases = %d, want 1", done)
	}

	// All phases complete derives the zero phase.
	if err := os.WriteFile(path, []byte("### Phase 1: X\n- [x] a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cur, err = SpecCurrentPhase(path)
	if err != nil || cur.Number != 0 {
		t.Errorf("fully-complete spec must derive phase 0, got %+v err %v", cur, err)
	}
}

// TestSpecJustCompletedPhase pins the completion frontier — the phase a
// per-phase commit ships (found live: ${current_phase} at commit time was
// one phase ahead).
func TestSpecJustCompletedPhase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.md")
	write := func(content string) {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("### Phase 1: A\n- [x] a\n### Phase 2: B\n- [ ] b\n### Phase 3: C\n- [ ] c\n")
	p, err := SpecJustCompletedPhase(path)
	if err != nil || p.Number != 1 {
		t.Errorf("mid-walk frontier = %+v, want Phase 1", p)
	}

	write("### Phase 1: A\n- [x] a\n### Phase 2: B\n- [x] b\n")
	p, _ = SpecJustCompletedPhase(path)
	if p.Number != 2 {
		t.Errorf("all-complete frontier = %+v, want Phase 2 (the last)", p)
	}

	write("### Phase 1: A\n- [ ] a\n")
	p, _ = SpecJustCompletedPhase(path)
	if p.Number != 0 {
		t.Errorf("nothing-complete frontier = %+v, want zero phase", p)
	}
}

func TestSpecOpenItemsMissingFile(t *testing.T) {
	if _, _, err := SpecOpenItems(filepath.Join(t.TempDir(), "absent.md")); err == nil {
		t.Error("missing spec file must return an error, not a zero count")
	}
}
