package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// The --check exit code is what a cron or a script polls. Exit 10 must mean
// "an upgrade is available and would run", so a newer release blocked by a
// missing tool is an error, while a missing tool with nothing to upgrade is
// not.
func TestUpgradeCheckExit(t *testing.T) {
	cases := []struct {
		name string
		c    bus.UpgradeCheck
		err  error
		want int
	}{
		{"newer", bus.UpgradeCheck{Verdict: bus.UpgradeNewer}, nil, 10},
		{"newer, tool missing", bus.UpgradeCheck{Verdict: bus.UpgradeNewer, MissingTools: []string{"go"}}, nil, 1},
		{"current", bus.UpgradeCheck{Verdict: bus.UpgradeCurrent}, nil, 0},
		{"current, tool missing", bus.UpgradeCheck{Verdict: bus.UpgradeCurrent, MissingTools: []string{"tar"}}, nil, 0},
		{"ahead", bus.UpgradeCheck{Verdict: bus.UpgradeAhead}, nil, 0},
		{"unknown", bus.UpgradeCheck{Verdict: bus.UpgradeUnknown}, nil, 1},
		{"lookup failed", bus.UpgradeCheck{}, errors.New("HTTP 403"), 1},
	}
	for _, c := range cases {
		if got := upgradeCheckExit(c.c, c.err); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}
}

// The pipeline prints one line per step, a failure with its cause, and a
// step's sub-rows indented beneath it.
func TestWriteStepResult(t *testing.T) {
	var out bytes.Buffer
	writeStepResult(&out, bus.StepResult{Name: "Download", Success: true, Note: "1.2 MB, sha256 abc"})
	writeStepResult(&out, bus.StepResult{Name: "Build", Error: "make build failed — log: /c/build.log"})
	writeStepResult(&out, bus.StepResult{Name: "Restart daemons", Error: "1 of 2 daemons failed to restart", Sub: []bus.StepResult{
		{Name: "alpha", Success: true, Note: "daemon v0.1.20 → installed v0.1.21 — daemon restarted"},
		{Name: "beta", Error: "kill 4242: no such process"},
	}})
	want := "Download: 1.2 MB, sha256 abc\n" +
		"Build: FAILED — make build failed — log: /c/build.log\n" +
		"Restart daemons: FAILED — 1 of 2 daemons failed to restart\n" +
		"  alpha: daemon v0.1.20 → installed v0.1.21 — daemon restarted\n" +
		"  beta: FAILED — kill 4242: no such process\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// upgraded is what a script reads to decide whether anything changed, so it
// is false both for a failed run and for one that stopped at Check.
func TestWriteUpgradeReport(t *testing.T) {
	check := bus.UpgradeCheck{Installed: bus.Info{Version: "v0.1.20"}, Latest: bus.Release{Tag: "v0.1.21"}, Verdict: bus.UpgradeNewer}
	cases := []struct {
		name     string
		state    bus.UpgradeState
		err      error
		upgraded bool
	}{
		{"upgraded", bus.UpgradeState{Check: check, Results: []bus.StepResult{{Name: "Check", Success: true}}}, nil, true},
		{"stopped at Check", bus.UpgradeState{Check: check, Stopped: true}, nil, false},
		{"failed", bus.UpgradeState{Check: check}, errors.New("Build: make build failed"), false},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		writeUpgradeReport(&out, &errOut, &c.state, c.err)
		var got struct {
			Installed string           `json:"installed"`
			Latest    string           `json:"latest"`
			Upgraded  bool             `json:"upgraded"`
			Steps     []map[string]any `json:"steps"`
			Error     string           `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%s: JSON %q: %v", c.name, out.String(), err)
		}
		if got.Upgraded != c.upgraded || got.Installed != "v0.1.20" || got.Latest != "v0.1.21" {
			t.Errorf("%s: report %s, want upgraded=%v with both versions", c.name, out.String(), c.upgraded)
		}
		if (c.err != nil) != (got.Error != "") {
			t.Errorf("%s: error field %q, want it set only on failure", c.name, got.Error)
		}
	}
}

// The JSON is a script-facing contract: a check carries verdict and latest
// tag, and a failed lookup carries the error with no empty release a script
// could read as a real one.
func TestWriteUpgradeCheckJSON(t *testing.T) {
	check := bus.UpgradeCheck{
		Installed: bus.Info{Version: "v0.1.19"},
		Latest:    bus.Release{Tag: "v0.1.20"},
		Verdict:   bus.UpgradeNewer,
	}
	var out, errOut bytes.Buffer
	writeUpgradeCheck(&out, &errOut, check, nil, true)
	var ok struct {
		Installed struct{ Version string } `json:"installed"`
		Latest    struct{ Tag string }     `json:"latest"`
		Verdict   string                   `json:"verdict"`
	}
	if err := json.Unmarshal(out.Bytes(), &ok); err != nil {
		t.Fatalf("success JSON %q: %v", out.String(), err)
	}
	if ok.Installed.Version != "v0.1.19" || ok.Latest.Tag != "v0.1.20" || ok.Verdict != "newer" {
		t.Errorf("success JSON = %s", out.String())
	}

	out.Reset()
	writeUpgradeCheck(&out, &errOut, bus.UpgradeCheck{Installed: bus.Info{Version: "v0.1.19"}}, errors.New("HTTP 403: rate limit"), true)
	var failed map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &failed); err != nil {
		t.Fatalf("failure JSON %q: %v", out.String(), err)
	}
	if !strings.Contains(string(failed["error"]), "HTTP 403") || failed["installed"] == nil {
		t.Errorf("failure JSON = %s, want installed and error", out.String())
	}
	if _, has := failed["latest"]; has {
		t.Errorf("failure JSON = %s, must not carry an empty latest release", out.String())
	}
}
