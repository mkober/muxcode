package bus

import (
	"strings"
	"testing"
)

// PRs #104 and #105 were titled after the phase their run started on.
// ${spec_title} keeps the key and the whole title, including a title that
// itself contains " — " (MUX-178's), and drops only the phase.
func TestSpecTitle(t *testing.T) {
	tests := []struct{ intent, want string }{
		{"MUX-142 Spawn Worker Delegates Into the Wrong Tree — Phase 1: Confirm the live shape",
			"MUX-142 Spawn Worker Delegates Into the Wrong Tree"},
		{"MUX-178 A Graph Spawn Node Cuts No Worktree — MUX-131 Has Regressed — Phase 1: Locate the regression",
			"MUX-178 A Graph Spawn Node Cuts No Worktree — MUX-131 Has Regressed"},
		{"MUX-9 Title Only", "MUX-9 Title Only"},
		{"104", "104"},
	}
	for _, tt := range tests {
		if got := specTitle(tt.intent); got != tt.want {
			t.Errorf("specTitle(%q) = %q, want %q", tt.intent, got, tt.want)
		}
	}
}

// push-pr names the PR from ${spec_title}; the phase the run launched on
// must not reach it, while ${spec} still carries it for the phase nodes.
func TestPushPRTitleOmitsLaunchPhase(t *testing.T) {
	g := mustTemplate(t, "50-spec-to-pr")
	run := &GraphRun{Intent: "MUX-142 Spawn Worker Delegates Into the Wrong Tree — Phase 1: Confirm the live shape"}

	msg := interpolateGraphMessage(runTestSession, run, g.node("push-pr").Message, "")
	if !strings.Contains(msg, `"MUX-142 Spawn Worker Delegates Into the Wrong Tree"`) {
		t.Errorf("push-pr must name the PR after the spec title: %q", msg)
	}
	if strings.Contains(msg, "Phase 1") {
		t.Errorf("push-pr must not carry the launch-time phase: %q", msg)
	}
	if got := interpolateGraphMessage(runTestSession, run, "${spec}", ""); got != run.Intent {
		t.Errorf("negative control: ${spec} must still expand to the full intent, got %q", got)
	}
}
