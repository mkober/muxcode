package bus

import "testing"

func TestOutputToken(t *testing.T) {
	cases := []struct {
		name, output string
		want         bool
	}{
		{"2026-11-27 watch red report", "CI RED (not CI-GREEN) PR 264 head 03d657c: check test FAILED 1m56s", false},
		{"upper-case negation", "test failed — NOT CI-GREEN. EXIT=0", false},
		{"no before token", "no CI-GREEN yet; CI-PENDING", false},
		{"hyphen-joined negation", "NOT-CI-GREEN", false},
		{"token inside a longer word", "XCI-GREEN CI-GREENISH", false},
		{"absent", "CI-RED: test failed", false},
		{"genuine terse reply", "CI-GREEN EXIT=0", true},
		{"genuine reply after sentence", "all 3 checks passed. CI-GREEN", true},
		{"dash before token", "test — pass. CI-GREEN\nEXIT=0", true},
		{"sentence punctuation is no negation", "Any failures? No. CI-GREEN", true},
		{"negated mention then genuine", "earlier run was not CI-GREEN; head abc passed: CI-GREEN", true},
		{"parenthesised genuine", "(CI-GREEN)", true},
		{"single-quoted genuine", "verdict: 'CI-GREEN'", true},
		{"single-quoted negated", "isn't 'CI-GREEN'", false},
	}
	for _, c := range cases {
		got, _ := EvaluateConditions(map[string]any{CondOutputToken: "CI-GREEN"}, &ChainContext{Output: c.output})
		if got != c.want {
			t.Errorf("%s: output_token CI-GREEN on %q = %v, want %v", c.name, c.output, got, c.want)
		}
	}
}

// The 55 loop's ci-green node against the reply that routed red PR #264 to
// merge on 2026-11-27, and the genuine reply as its negative control.
func TestDefectLoopCIGreenRejectsNegatedToken(t *testing.T) {
	g, err := ParseGraph([]byte(builtinGraphJSON["55-defect-backlog-loop"]))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	n := g.node("ci-green")
	if n == nil {
		t.Fatal("ci-green node missing")
	}
	red := "CI RED (not CI-GREEN) PR 264 head 03d657c: check test FAILED 1m56s run 45671603150."
	if ok, _ := EvaluateConditions(n.Conditions, &ChainContext{Output: red}); ok {
		t.Errorf("ci-green passed the red report %q — merge would be dispatched", red)
	}
	if ok, _ := EvaluateConditions(n.Conditions, &ChainContext{Output: "all checks passed on 03d657c. CI-GREEN EXIT=0"}); !ok {
		t.Error("ci-green rejected a genuine green report")
	}
}

// Every builtin condition that branches on a sentinel token must use the
// negation-aware output_token, never the substring output_contains.
func TestBuiltinSentinelConditionsUseOutputToken(t *testing.T) {
	for name, src := range builtinGraphJSON {
		g, err := ParseGraph([]byte(src))
		if err != nil {
			t.Fatalf("%s parse: %v", name, err)
		}
		for _, n := range g.Nodes {
			if _, bad := n.Conditions["output_contains"]; bad {
				t.Errorf("%s: node %s branches on output_contains %v; use %s", name, n.ID, n.Conditions["output_contains"], CondOutputToken)
			}
		}
	}
}
