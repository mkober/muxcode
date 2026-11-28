package bus

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// openItemRe matches an unchecked markdown checkbox line (`- [ ] text`),
// including nested/indented items. The repo's docs convention requires
// checkboxes for every actionable item, so this count is the mechanical
// definition of "work still open" a spec close-out must respect (MUX-114).
var openItemRe = regexp.MustCompile(`^\s*- \[ \]\s*(.*)`)

// SpecOpenItems reads a requirements spec and returns how many checkbox
// items remain unchecked, along with their texts in file order. Lines
// inside fenced code blocks are skipped — specs quote checkbox examples in
// fences, and counting those would block a legitimate close-out.
func SpecOpenItems(path string) (int, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	var names []string
	inFence := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := openItemRe.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			if name == "" {
				name = "(unnamed item)"
			}
			names = append(names, name)
		}
	}
	return len(names), names, nil
}

// intentPhaseRe pulls a phase number out of a run intent ("… Phase 1:
// Turn trace" → 1).
var intentPhaseRe = regexp.MustCompile(`(?i)phase\s+([0-9]+)`)

// IntentPhase returns the phase number named in a run intent, or 0 when
// none is named.
func IntentPhase(intent string) int {
	m := intentPhaseRe.FindStringSubmatch(intent)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// SpecPhase is one `### Phase N` section of a requirements spec: its
// number, title text, the names of checkbox items still open inside it,
// and how many are checked.
type SpecPhase struct {
	Number int
	Title  string // full heading text after "### ", e.g. "Phase 2: Attribution"
	Items  []string
	Done   int
}

// Complete reports whether the phase has work and all of it is checked.
// A phase with no boxes at all — a stub, or a narrative "### Phase 1
// findings" heading — is empty, not done (MUX-183 defect 2).
func (p SpecPhase) Complete() bool {
	return p.Done > 0 && len(p.Items) == 0
}

// phaseHeadingRe extracts the number from a phase heading line.
var phaseHeadingRe = regexp.MustCompile(`^### (Phase ([0-9]+)\b.*)$`)

// doneItemRe matches a checked markdown checkbox line.
var doneItemRe = regexp.MustCompile(`^\s*- \[[xX]\]`)

// SpecPhases scans a spec once and returns every phase section in file
// order with its open and checked items. This is the single primitive
// behind stateless phase derivation (MUX-121 decision 1): the current phase
// is always recomputed from the spec, never stored, so it cannot drift and
// an already-complete phase can never be re-implemented — the failure
// observed three times on 2026-08-28.
func SpecPhases(path string) ([]SpecPhase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSpecPhases(string(data)), nil
}

// parseSpecPhases is SpecPhases over content already read. A `####` or
// deeper subheading stays inside the enclosing phase, so boxes under it
// count for that phase; any shallower heading ends it (MUX-183 defect 2).
//
// What a phase is: a `### Phase N` heading inside the spec's
// `## Implementation` section — any `##` heading whose text begins
// "Implementation", so `## Implementation phases` counts — and the boxes under
// it (MUX-130 decision 2a). A `### Phase N` in a `## Verification notes`,
// `## Design` or `## Acceptance criteria` section is no phase, boxes or not:
// ticking one made a commit ready on that heading alone. A non-Phase `###`
// heading such as `### Deferred` ends the current phase, so the boxes under it
// belong to no phase: they never hold the loop, and still count for
// SpecOpenItems, so the spec cannot close while they are open.
//
// A spec with phase headings but none inside an implementation section — no
// such section, or one holding no phase heading — is not read as having none:
// a zero-phase spec ends a run at once and gives the loop cap nothing to count.
// It degrades to every `### Phase N` in the file, flagged unscoped
// (scanSpecPhases), and the guard and gate text say so.
func parseSpecPhases(content string) []SpecPhase {
	phases, _ := scanSpecPhases(content)
	return phases
}

// SpecPhasesUnscopedWarning is the sentence a spec read without an
// implementation section carries into the guard and gate text (MUX-130).
const SpecPhasesUnscopedWarning = "Warning: no ### Phase heading in the spec sits inside a ## Implementation section, so every ### Phase heading in the file is read as a phase."

// SpecPhasesUnscoped reports whether the spec at path has phase headings but
// no implementation section — read document-wide, see parseSpecPhases.
func SpecPhasesUnscoped(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, unscoped := scanSpecPhases(string(data))
	return unscoped
}

// isImplementationSection reports whether a trimmed `##` heading line opens
// the implementation section.
func isImplementationSection(trimmed string) bool {
	return strings.HasPrefix(trimmed, "## Implementation")
}

// sectionBoundary reports whether a trimmed line is a level-1 or level-2
// heading — one that opens a new section — and whether that section is the
// implementation section. The one rule both phase scans use, so they cannot
// drift: a `# Appendix` after `## Implementation` ends it as a `##` does.
func sectionBoundary(trimmed string) (boundary, impl bool) {
	switch {
	case strings.HasPrefix(trimmed, "## "):
		return true, isImplementationSection(trimmed)
	case strings.HasPrefix(trimmed, "# "):
		return true, false
	}
	return false, false
}

// implementationHasPhases reports whether an implementation section of content
// holds a phase heading, outside any code fence — whether phases can be scoped
// to it without reading the spec as having none.
func implementationHasPhases(content string) bool {
	inFence, inImpl := false, false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		boundary, impl := sectionBoundary(trimmed)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inFence = !inFence
		case inFence:
		case boundary:
			inImpl = impl
		case inImpl && phaseHeadingRe.MatchString(trimmed):
			return true
		}
	}
	return false
}

// scanSpecPhases is parseSpecPhases reporting, too, whether it read the spec
// unscoped: phase headings found and none inside an implementation section to
// scope them to — no section, or one holding no phase heading.
func scanSpecPhases(content string) (phases []SpecPhase, unscoped bool) {
	scoped := implementationHasPhases(content)
	inFence, inImpl := false, false
	cur := -1
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if boundary, impl := sectionBoundary(trimmed); boundary {
				inImpl = impl
			}
			if m := phaseHeadingRe.FindStringSubmatch(trimmed); m != nil && (inImpl || !scoped) {
				n, _ := strconv.Atoi(m[2])
				phases = append(phases, SpecPhase{Number: n, Title: m[1]})
				cur = len(phases) - 1
			} else if !strings.HasPrefix(trimmed, "####") {
				cur = -1
			}
			continue
		}
		if cur < 0 {
			continue
		}
		if m := openItemRe.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[1])
			if name == "" {
				name = "(unnamed item)"
			}
			phases[cur].Items = append(phases[cur].Items, name)
		} else if doneItemRe.MatchString(line) {
			phases[cur].Done++
		}
	}
	return phases, !scoped && len(phases) > 0
}

// newlyCompletedPhases returns, lowest number first, the phases complete in
// tree that were not complete in head — the phases a commit would ship.
// Anchoring on the committed copy is what stops a fresh run, a later lap or
// a retry from re-crediting a phase already shipped (MUX-183 defect 1): the
// answer depends only on the two files, never on per-run counters.
//
// Duplicate numbers (MUX-130 Defect C, warned at dispatch, not refused):
// shipped is keyed by number, so a number complete in any HEAD section is
// shipped for every section of it, and each complete tree section of an
// unshipped number is returned — though another section of it is open.
func newlyCompletedPhases(tree, head []SpecPhase) []SpecPhase {
	shipped := map[int]bool{}
	for _, p := range head {
		if p.Complete() {
			shipped[p.Number] = true
		}
	}
	var out []SpecPhase
	for _, p := range tree {
		if p.Complete() && !shipped[p.Number] {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// completedPhaseCount counts the complete phases in a parsed spec.
func completedPhaseCount(phases []SpecPhase) int {
	n := 0
	for _, p := range phases {
		if p.Complete() {
			n++
		}
	}
	return n
}

// SpecCurrentPhase returns the lowest-numbered phase with open items and
// found true, or found false when every phase is complete or the spec has
// no phases. found, never Number, says whether there is one: Number 0 is a
// real Phase 0, which before MUX-130 read as "none" and lost to any later
// open phase.
//
// It answers "which phase is the work on". An open box inside a phase holds
// the loop on that phase, on purpose, until it is ticked or moved under
// `### Deferred` (MUX-130 decision 4). It is the wrong question for "is a
// commit ready" — that is phaseCommitReady — and for "is anything open at
// all", which is SpecOpenItems: boxes outside every phase are invisible here.
func SpecCurrentPhase(path string) (phase SpecPhase, found bool, err error) {
	phases, err := SpecPhases(path)
	if err != nil {
		return SpecPhase{}, false, err
	}
	phase, found = currentSpecPhase(phases)
	return phase, found, nil
}

// currentSpecPhase is SpecCurrentPhase over phases already parsed.
func currentSpecPhase(phases []SpecPhase) (best SpecPhase, found bool) {
	for _, p := range phases {
		if len(p.Items) > 0 && (!found || p.Number < best.Number) {
			best, found = p, true
		}
	}
	return best, found
}

// phaseNumberIssues returns, ascending, each phase number that repeats or
// arrives after a higher one in file order — specs MUX-130 decision 3b warns
// on at dispatch rather than refuses.
func phaseNumberIssues(phases []SpecPhase) []int {
	seen, flagged := map[int]bool{}, map[int]bool{}
	highest := -1
	for _, p := range phases {
		if seen[p.Number] || p.Number < highest {
			flagged[p.Number] = true
		}
		seen[p.Number] = true
		highest = max(highest, p.Number)
	}
	out := make([]int, 0, len(flagged))
	for n := range flagged {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// UnscopedPhaseGuardWarning reports why a run's phase-complete guard will
// not engage: the graph carries the guard but the intent names no phase,
// so the commit ships unchecked. Empty when the guard is scoped or
// absent. Shared by both launch surfaces — CLI and TUI — so neither can
// drift (plan finding 2026-08-28: the CLI warned, the TUI did not).
func UnscopedPhaseGuardWarning(g *Graph, intent string) string {
	if IntentPhase(intent) != 0 {
		return ""
	}
	for _, n := range g.Nodes {
		if n.Guard == GuardPhaseComplete {
			return fmt.Sprintf("node %q has a phase-complete guard but the intent names no phase (\"Phase N\") — the guard will pass through and the commit ships unchecked", n.ID)
		}
	}
	return ""
}

// SpecPhaseOpenItems counts the unchecked checkbox items inside one phase
// section, derived from the single SpecPhases scan. A phase the spec
// lacks reports zero items — an intent naming a missing phase is not the
// guard's problem to solve. A spec with the same phase number twice sums
// the sections.
func SpecPhaseOpenItems(path string, phase int) (int, []string, error) {
	phases, err := SpecPhases(path)
	if err != nil {
		return 0, nil, err
	}
	var names []string
	for _, p := range phases {
		if p.Number == phase {
			names = append(names, p.Items...)
		}
	}
	return len(names), names, nil
}
