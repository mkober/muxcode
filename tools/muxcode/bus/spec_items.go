package bus

import (
	"fmt"
	"os"
	"regexp"
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
	n := 0
	fmt.Sscanf(m[1], "%d", &n)
	return n
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
// section of a spec — the lines between `### Phase N` and the next
// heading. Fenced code blocks are skipped as in SpecOpenItems. A phase
// heading that does not exist reports zero items — an intent naming a
// phase the spec lacks is not the guard's problem to solve.
func SpecPhaseOpenItems(path string, phase int) (int, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	var names []string
	inFence, inPhase := false, false
	want := fmt.Sprintf("### Phase %d", phase)
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
			rest, matched := strings.CutPrefix(trimmed, want)
			// digit boundary: "### Phase 1" must not match "### Phase 10"
			inPhase = matched && (rest == "" || rest[0] < '0' || rest[0] > '9')
			continue
		}
		if !inPhase {
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
