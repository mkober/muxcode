package bus

import (
	"fmt"
	"strings"
	"time"
)

// RenderSpawnConsole renders the left-pane view for a spawned worker
// window: the spawn's task and status, and — when the spawn belongs to a
// graph run — the run's per-node state, so the worker's place in the
// orchestration is visible beside it (user request 2026-08-28; the pane
// previously held an idle shell).
func RenderSpawnConsole(session, spawnRole string, width int) string {
	entry, found := findSpawnByRole(session, spawnRole)
	if !found {
		return Pad + ColorDim + "no spawn entry for " + spawnRole + ColorReset + "\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s%stask%s\n", Pad, ColorPurple, ColorReset)
	for _, ln := range wrapText(entry.Task, width-len(ContPad)-RightMargin) {
		fmt.Fprintf(&b, "%s%s\n", ContPad, ln)
	}
	b.WriteString("\n")

	statusColor := ColorCyan
	switch entry.Status {
	case "completed":
		statusColor = ColorGreen
	case "stopped":
		statusColor = ColorRed
	}
	elapsed := time.Since(time.Unix(entry.StartedAt, 0)).Round(time.Second)
	fmt.Fprintf(&b, "%s%sstatus%s   %s%s%s\n", Pad, ColorDim, ColorReset, statusColor, entry.Status, ColorReset)
	fmt.Fprintf(&b, "%s%sowner%s    %s\n", Pad, ColorDim, ColorReset, entry.Owner)
	fmt.Fprintf(&b, "%s%selapsed%s  %s\n", Pad, ColorDim, ColorReset, elapsed)
	if entry.Worktree != "" {
		fmt.Fprintf(&b, "%s%sworktree%s %s%s%s\n", Pad, ColorDim, ColorReset, ColorDim, entry.Worktree, ColorReset)
	}
	b.WriteString("\n")

	run, g, statuses, ok := owningGraphRun(session, entry)
	if !ok {
		b.WriteString(Pad + ColorDim + "no owning graph run (ad-hoc spawn)" + ColorReset + "\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%s%sgraph run%s\n", Pad, ColorPurple, ColorReset)
	b.WriteString(indentBlock(FormatGraphRunColored(run, g, statuses), Pad))
	return b.String()
}

// wrapText greedily wraps s into lines of at most w runes, breaking on
// spaces where one falls in the back half of a line.
func wrapText(s string, w int) []string {
	if w < 20 {
		w = 20
	}
	var out []string
	r := []rune(strings.TrimSpace(s))
	for len(r) > 0 {
		if len(r) <= w {
			out = append(out, string(r))
			break
		}
		cut := w
		for i := w; i > w/2; i-- {
			if r[i] == ' ' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimRight(string(r[:cut]), " "))
		r = r[cut:]
		for len(r) > 0 && r[0] == ' ' {
			r = r[1:]
		}
	}
	return out
}

// indentBlock prefixes every non-empty line with pad.
func indentBlock(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// findSpawnByRole resolves a spawn entry by its bus role / window name.
func findSpawnByRole(session, spawnRole string) (SpawnEntry, bool) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return SpawnEntry{}, false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].SpawnRole == spawnRole {
			return entries[i], true
		}
	}
	return SpawnEntry{}, false
}

// owningGraphRun finds the in-flight run holding a node whose task
// correlation names this spawn. Graph dispatch stores entry.SpawnRole
// (graphSpawnFn), so that is the primary key — matching entry.ID was the
// live "ad-hoc spawn" false negative; ID stays as a fallback for other
// correlation shapes. Map nodes store several ids in one TaskID, hence
// Contains.
func owningGraphRun(session string, entry SpawnEntry) (*GraphRun, *Graph, map[string]*GraphNodeStatus, bool) {
	for _, run := range ScanInFlightGraphRuns(session) {
		g, err := ReadGraphRunGraph(session, run.ID)
		if err != nil {
			continue
		}
		statuses, err := ReadAllNodeStatuses(session, run.ID)
		if err != nil {
			continue
		}
		for _, st := range statuses {
			if st == nil || st.TaskID == "" {
				continue
			}
			if strings.Contains(st.TaskID, entry.SpawnRole) || strings.Contains(st.TaskID, entry.ID) {
				r := run
				return &r, g, statuses, true
			}
		}
	}
	return nil, nil, nil, false
}
