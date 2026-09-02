package bus

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Definition presence (MUX-136). A Claude Code agent muxcode launched carries
// its definition on its own command line — `--agent <name> --agents <json>`,
// bound as one unit by ClaudeCodeProvider.BuildExecArgs. A claude process in
// an agent pane WITHOUT both flags is running default tools under the role's
// name: the shape a bare `claude --resume` typed into the pane produces, which
// is what left the plan agent unconstrained for ~37 minutes on 2026-09-01.
//
// ProbeAgentDefinition is a POSITIVE probe: it reads the live process's argv
// rather than looking for Claude's startup banner, so it holds for the whole
// life of the process instead of only until the banner scrolls away. The
// banner (PaneShowsDefinitionlessAgent) is the fallback for when no claude
// process can be attributed to the pane.

// DefinitionProbe is the verdict of ProbeAgentDefinition.
type DefinitionProbe int

const (
	// DefinitionUnknown — no claude process could be attributed to the pane:
	// launcher still pre-exec, a non-Claude provider, or a tmux/ps failure.
	DefinitionUnknown DefinitionProbe = iota
	// DefinitionPresent — the claude process carries --agent and --agents.
	DefinitionPresent
	// DefinitionMissing — a claude process is running without the flag pair.
	DefinitionMissing
)

func (p DefinitionProbe) String() string {
	switch p {
	case DefinitionPresent:
		return "present"
	case DefinitionMissing:
		return "missing"
	}
	return "unknown"
}

// definitionlessSignatures is Claude Code's resume-path banner, printed once
// when a resumed session's agent cannot be resolved from disk (2.1.258).
var definitionlessSignatures = []string{
	"which is no longer available (no agent by that name in",
	"the agent's tool restrictions no longer apply",
}

// PaneShowsDefinitionlessAgent reports whether captured pane content carries
// Claude Code's definition-less resume banner. Scanned scrollback-wide on
// purpose: the banner prints once at session start and never again, so a
// live-tail scope would miss it on every sweep but the first. Case-insensitive.
func PaneShowsDefinitionlessAgent(content string) bool {
	if content == "" {
		return false
	}
	lower := strings.ToLower(content)
	for _, sig := range definitionlessSignatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// ArgsCarryDefinition reports whether a claude argv carries the bound flag
// pair. Exact-token matching: the --agents JSON body may mention either flag
// in prose, but a definition-less launch has no JSON at all, so prose can only
// ever appear alongside the real flags.
func ArgsCarryDefinition(args []string) bool {
	hasAgent, hasAgents := false, false
	for _, a := range args {
		switch a {
		case "--agent":
			hasAgent = true
		case "--agents":
			hasAgents = true
		}
	}
	return hasAgent && hasAgents
}

// psListRunner lists every process as "<pid> <ppid> <command...>" lines.
// Tests replace it.
var psListRunner = func() (string, error) {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	return string(out), err
}

// ProbeAgentDefinition attributes a claude process to the role's pane and
// reads its argv. Descendants of the pane's shell are walked, so an
// intermediate `muxcode agent launch` (pre-exec) or a shell wrapper does not
// hide the agent.
func ProbeAgentDefinition(session, role string) DefinitionProbe {
	pidOut, err := TmuxOutput("display-message", "-p", "-t", PaneTarget(session, role), "#{pane_pid}")
	if err != nil {
		return DefinitionUnknown
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(pidOut))
	if err != nil || panePID <= 0 {
		return DefinitionUnknown
	}
	psOut, err := psListRunner()
	if err != nil {
		return DefinitionUnknown
	}
	args, ok := claudeArgsUnder(psOut, panePID)
	if !ok {
		return DefinitionUnknown
	}
	if ArgsCarryDefinition(args) {
		return DefinitionPresent
	}
	return DefinitionMissing
}

// claudeArgsUnder finds the first claude process among the descendants of
// panePID in ps output ("<pid> <ppid> <command...>" per line) and returns its
// argv. A process is claude when its argv[0] basename is "claude" — a process
// merely mentioning claude in its arguments (grep, an editor) is not.
func claudeArgsUnder(psOutput string, panePID int) ([]string, bool) {
	type proc struct {
		pid  int
		args []string
	}
	children := map[int][]proc{}
	for _, line := range strings.Split(psOutput, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], proc{pid: pid, args: f[2:]})
	}

	queue := []int{panePID}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[pid] {
			if filepath.Base(c.args[0]) == "claude" {
				return c.args, true
			}
			queue = append(queue, c.pid)
		}
	}
	return nil, false
}
