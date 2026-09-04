package bus

import (
	"errors"
	"strings"
	"testing"
)

// The banner as Claude Code 2.1.258 prints it on a resume whose agent cannot
// be resolved from disk — the 2026-09-01 plan pane, with the path elided.
const definitionlessBanner = "This session was running agent 'planner', which is no longer available (no agent by that name in /repo). " +
	"Continuing with the default tools and system prompt — the agent's tool restrictions no longer apply. " +
	"To restore it, re-create the agent, or resume with an explicit --agent <name>."

func TestPaneShowsDefinitionlessAgent_Detects(t *testing.T) {
	cases := []string{
		definitionlessBanner,
		"scrollback above\n" + definitionlessBanner + "\n❯ ",
		strings.ToUpper(definitionlessBanner),
	}
	for _, c := range cases {
		if !PaneShowsDefinitionlessAgent(c) {
			t.Errorf("expected definition-less detection for: %q", c)
		}
	}
}

func TestPaneShowsDefinitionlessAgent_CleanPaneIgnored(t *testing.T) {
	clean := []string{
		"",
		"❯ ready for next request",
		"Lower-priority mode is no longer available — it has ended",
		"The agent's tool restrictions are enforced by the definition",
		"Resumed the build after the definition landed; no agent by that name was ever needed",
	}
	for _, c := range clean {
		if PaneShowsDefinitionlessAgent(c) {
			t.Errorf("clean pane should not match: %q", c)
		}
	}
}

func TestArgsCarryDefinition(t *testing.T) {
	json := `{"planner":{"description":"Docs","prompt":"Maintain docs. Never pass --agent alone."}}`
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"launcher shape", []string{"claude", "--agent", "planner", "--agents", json, "--model", "x"}, true},
		{"bare resume", []string{"claude", "--resume", "abc"}, false},
		{"name only", []string{"claude", "--agent", "planner"}, false},
		{"json only", []string{"claude", "--agents", json}, false},
		{"flag words inside a value are not flags", []string{"claude", "--append-system-prompt", "use --agent and --agents"}, false},
	}
	for _, c := range cases {
		if got := ArgsCarryDefinition(c.args); got != c.want {
			t.Errorf("%s: ArgsCarryDefinition = %v, want %v", c.name, got, c.want)
		}
	}
}

// ps output for two panes: pane shell 100 runs a launcher-started claude via
// the pre-exec `muxcode agent launch` hop; pane shell 300 runs a bare resume.
// 400 mentions claude without being claude.
const fakePS = `
  100     1 -bash
  150   100 muxcode agent launch plan
  200   150 claude --agent planner --agents {"planner":{"description":"Docs","prompt":"Maintain docs"}} --model claude-opus-5
  300     1 -bash
  310   300 /Users/x/.nvm/versions/node/v24/bin/claude --resume 0f3a
  400   100 grep claude notes.txt
`

func TestClaudeArgsUnder(t *testing.T) {
	args, ok := claudeArgsUnder(fakePS, 100)
	if !ok || !ArgsCarryDefinition(args) {
		t.Fatalf("pane 100: want launcher claude through the pre-exec hop, got (%v, %v)", args, ok)
	}
	args, ok = claudeArgsUnder(fakePS, 300)
	if !ok || ArgsCarryDefinition(args) || args[1] != "--resume" {
		t.Fatalf("pane 300: want the bare resume, got (%v, %v)", args, ok)
	}
	if args, ok := claudeArgsUnder(fakePS, 999); ok {
		t.Fatalf("pane 999 has no claude, got %v", args)
	}
	if args, ok := claudeArgsUnder("  100 1 -bash\n  400 100 grep claude notes.txt\n", 100); ok {
		t.Fatalf("a process mentioning claude is not claude, got %v", args)
	}
}

// probeWith runs ProbeAgentDefinition against a fake pane pid and ps listing.
func probeWith(t *testing.T, panePID string, ps string, psErr error) DefinitionProbe {
	t.Helper()
	origTmux, origPS := tmuxOutputRunner, psListRunner
	tmuxOutputRunner = func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "display-message" {
			return panePID + "\n", nil
		}
		return "", errors.New("unexpected tmux call")
	}
	psListRunner = func() (string, error) { return ps, psErr }
	t.Cleanup(func() { tmuxOutputRunner, psListRunner = origTmux, origPS })
	return ProbeAgentDefinition("s", "plan")
}

func TestProbeAgentDefinition(t *testing.T) {
	if got := probeWith(t, "100", fakePS, nil); got != DefinitionPresent {
		t.Errorf("launcher pane: got %s, want present", got)
	}
	if got := probeWith(t, "300", fakePS, nil); got != DefinitionMissing {
		t.Errorf("bare-resume pane: got %s, want missing", got)
	}
	if got := probeWith(t, "999", fakePS, nil); got != DefinitionUnknown {
		t.Errorf("pane without claude: got %s, want unknown", got)
	}
	if got := probeWith(t, "100", "", errors.New("ps failed")); got != DefinitionUnknown {
		t.Errorf("ps failure: got %s, want unknown", got)
	}
	if got := probeWith(t, "not-a-pid", fakePS, nil); got != DefinitionUnknown {
		t.Errorf("bad pane pid: got %s, want unknown", got)
	}
}
