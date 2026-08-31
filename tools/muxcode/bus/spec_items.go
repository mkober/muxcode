package bus

import (
	"fmt"
	"os"
	"regexp"
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
// number, title text, and the names of checkbox items still open inside
// it.
type SpecPhase struct {
	Number int
	Title  string // full heading text after "### ", e.g. "Phase 2: Attribution"
	Items  []string
}

// phaseHeadingRe extracts the number from a phase heading line.
var phaseHeadingRe = regexp.MustCompile(`^### (Phase ([0-9]+)\b.*)$`)

// SpecPhases scans a spec once and returns every phase section in file
// order with its open-item count. This is the single primitive behind
// stateless phase derivation (MUX-121 decision 1): the current phase is
// always recomputed from the spec, never stored, so it cannot drift and
// an already-complete phase can never be re-implemented — the failure
// observed three times on 2026-08-28.
func SpecPhases(path string) ([]SpecPhase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var phases []SpecPhase
	inFence := false
	cur := -1
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			cur = -1
			if m := phaseHeadingRe.FindStringSubmatch(trimmed); m != nil {
				n, _ := strconv.Atoi(m[2])
				phases = append(phases, SpecPhase{Number: n, Title: m[1]})
				cur = len(phases) - 1
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
		}
	}
	return phases, nil
}

// SpecCurrentPhase returns the lowest-numbered phase with open items, or
// the zero SpecPhase (Number 0) when every phase is complete or the spec
// has no phases.
func SpecCurrentPhase(path string) (SpecPhase, error) {
	phases, err := SpecPhases(path)
	if err != nil {
		return SpecPhase{}, err
	}
	best := SpecPhase{}
	for _, p := range phases {
		if len(p.Items) > 0 && (best.Number == 0 || p.Number < best.Number) {
			best = p
		}
	}
	return best, nil
}

// SpecJustCompletedPhase returns the completion frontier: the last phase
// in file order with zero open items before the first open one (or the
// last complete phase overall when none are open). This is the phase a
// per-phase commit ships — ${current_phase} at commit time already points
// at the NEXT phase, because update-spec closed this one before the
// commit dispatched (found live by test-multi-phase-graph.sh: every
// commit was labeled one phase ahead, the last "(no open phase)").
func SpecJustCompletedPhase(path string) (SpecPhase, error) {
	phases, err := SpecPhases(path)
	if err != nil {
		return SpecPhase{}, err
	}
	last := SpecPhase{}
	for _, p := range phases {
		if len(p.Items) > 0 {
			break
		}
		last = p
	}
	return last, nil
}

// SpecCompletedPhaseCount returns how many phases have zero open items —
// the progress signal MUX-121's stuck-phase gate compares against loop
// iterations (completed < iterations = the last loop closed nothing).
func SpecCompletedPhaseCount(path string) (int, error) {
	phases, err := SpecPhases(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range phases {
		if len(p.Items) == 0 {
			n++
		}
	}
	return n, nil
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
