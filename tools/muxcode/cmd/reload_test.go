package cmd

import "testing"

// `reload --resume` is the operator restart: it needs --all, and refuses any
// flag that would change what runs an agent (provider is a filter, never a
// switch). Without --resume nothing is newly refused.
func TestValidateRestartFlags(t *testing.T) {
	cases := []struct {
		name           string
		resume, all    bool
		cli, model     string
		compact, valid bool
	}{
		{name: "restart all", resume: true, all: true, valid: true},
		{name: "restart without --all", resume: true},
		{name: "restart with --cli", resume: true, all: true, cli: "opencode"},
		{name: "restart with --model", resume: true, all: true, model: "claude-sonnet-5"},
		{name: "restart with --compact", resume: true, all: true, compact: true},
		{name: "plain reload --all --cli", all: true, cli: "opencode", valid: true},
		{name: "plain reload --compact", compact: true, valid: true},
	}
	for _, c := range cases {
		err := validateRestartFlags(c.resume, c.all, c.cli, c.model, c.compact)
		if (err == nil) != c.valid {
			t.Errorf("%s: err = %v, want valid=%v", c.name, err, c.valid)
		}
	}
}
