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
