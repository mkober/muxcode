package main

import (
	"os"
	"regexp"
	"testing"
)

// TestRouteFor pins that nothing but a real launch request reaches the
// launcher. The launcher treats any unknown first arg as a project path and
// starts a full agent session, so each guard here stands for an invocation
// that once did that by accident: `muxcode --version` (a directory named
// "--version"), `muxcode --help` (which died on "not a directory" and taught
// an agent to guess the calling convention), and the four stray sessions that
// guess then produced on 2026-09-14.
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

		// Help must print usage, not look for a directory named "--help".
		{"muxcode", []string{"--help"}, routeHelp},
		{"muxcode", []string{"-h"}, routeHelp},
		{"muxcode", []string{"help"}, routeHelp},
		{"muxcode-agent-bus", []string{"--help"}, routeHelp},

		// Any other leading flag is a flag, never a project path.
		{"muxcode", []string{"--nope"}, routeUsage},
		{"muxcode", []string{"-x"}, routeUsage},

		// A launcher call that lost its subcommand. Each of these started a
		// real stray session on 2026-09-14; "test" is the one a
		// knownSubcommands check alone would miss, since it is not a
		// subcommand — only the arg count catches it.
		{"muxcode", []string{".", "memory"}, routeAmbiguousName},
		{"muxcode", []string{"/repo", "send", "test", "hello"}, routeAmbiguousName},
		{"muxcode", []string{"/repo", "test", "send", "hello"}, routeAmbiguousName},
		{"muxcode", []string{"/tmp/project", "my-session", "extra"}, routeAmbiguousName},

		// Negative controls: the launcher paths that must keep working.
		{"muxcode", nil, routeLauncher},
		{"muxcode", []string{"/tmp/project"}, routeLauncher},
		{"muxcode", []string{"/tmp/project", "name"}, routeLauncher},
		{"muxcode", []string{".", "my-session"}, routeLauncher},
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

// The refusal message names what the caller was reaching for, so a malformed
// call is self-correcting rather than just denied.
func TestFirstKnownSubcommand(t *testing.T) {
	if got := firstKnownSubcommand([]string{"/repo", "test", "send", "hi"}); got != "send" {
		t.Errorf("firstKnownSubcommand = %q, want \"send\"", got)
	}
	if got := firstKnownSubcommand([]string{"/repo", "my-session"}); got != "" {
		t.Errorf("firstKnownSubcommand = %q, want \"\" for a plain session name", got)
	}
}

// Every subcommand main() dispatches must be in knownSubcommands, or invoking
// it routes to the launcher and starts a session instead of running it. This
// is how "test" and "__help" became sessions: nothing kept the two lists
// together.
func TestEveryDispatchedSubcommandIsKnown(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\tcase "([a-z0-9-]+)":`)
	found := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		name := m[1]
		found++
		if !knownSubcommands[name] {
			t.Errorf("main() dispatches %q but it is not in knownSubcommands — "+
				"`muxcode %s` would route to the launcher", name, name)
		}
	}
	if found < 20 {
		t.Fatalf("only matched %d dispatch cases — the regex stopped matching main()'s switch", found)
	}
}
