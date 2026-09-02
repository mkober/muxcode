package main

import "testing"

// TestRouteFor pins that a version request never reaches the launcher. The
// launcher treats any unknown first arg as a project path, so before this
// ordering existed `muxcode --version` started a tmux session for a
// directory named "--version" instead of printing a line.
func TestRouteFor(t *testing.T) {
	cases := []struct {
		base string
		args []string
		want route
	}{
		{"muxcode", []string{"--version"}, routeVersion},
		{"muxcode", []string{"-v"}, routeVersion},
		{"muxcode", []string{"--version", "extra"}, routeVersion},
		{"muxcode-agent-bus", []string{"--version"}, routeVersion},
		{"muxcode", []string{"version"}, routeSubcommand},
		{"muxcode", []string{"version", "--json"}, routeSubcommand},
		{"muxcode", []string{"status"}, routeSubcommand},
		{"muxcode-agent-bus", []string{"status"}, routeSubcommand},

		// Negative controls: the launcher paths that must keep working.
		{"muxcode", nil, routeLauncher},
		{"muxcode", []string{"/tmp/project"}, routeLauncher},
		{"muxcode", []string{"/tmp/project", "name"}, routeLauncher},
		{"muxcode-agent-bus", nil, routeUsage},
	}
	for _, c := range cases {
		if got := routeFor(c.base, c.args); got != c.want {
			t.Errorf("routeFor(%q, %v) = %d, want %d", c.base, c.args, got, c.want)
		}
	}
}

// A subcommand missing from knownSubcommands is silently a project path.
func TestVersionIsKnownSubcommand(t *testing.T) {
	if !knownSubcommands["version"] {
		t.Fatal(`"version" is not in knownSubcommands — "muxcode version" would route to the launcher`)
	}
}
