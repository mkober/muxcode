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

		// A bare misspelled subcommand, which the arg-count guard above misses.
		// "agents" is the one that ran on 2026-09-15: one edit from "agent",
		// and a real directory here, so it launched a 10-window fleet.
		{"muxcode", []string{"agents"}, routeNearMiss},
		{"muxcode", []string{"agents", "my-session"}, routeNearMiss},
		{"muxcode", []string{"skills"}, routeNearMiss},
		{"muxcode", []string{"specs"}, routeNearMiss},
		{"muxcode", []string{"graphs"}, routeNearMiss},
		{"muxcode", []string{"stats"}, routeNearMiss},

		// Negative controls: the launcher paths that must keep working.
		{"muxcode", nil, routeLauncher},
		{"muxcode", []string{"/tmp/project"}, routeLauncher},
		{"muxcode", []string{"/tmp/project", "name"}, routeLauncher},
		{"muxcode", []string{".", "my-session"}, routeLauncher},
		{"muxcode-agent-bus", nil, routeUsage},

		// Negative controls for the near-miss guard specifically: a path
		// spelling is the escape hatch for a directory that shadows a
		// subcommand, and an ordinary project name is not a misspelling.
		{"muxcode", []string{"./agents"}, routeLauncher},
		{"muxcode", []string{"/repos/agents"}, routeLauncher},
		{"muxcode", []string{"~/repos/agents"}, routeLauncher},
		{"muxcode", []string{"./agents", "my-session"}, routeLauncher},
		{"muxcode", []string{"muxcode"}, routeLauncher},
		{"muxcode", []string{"my-project"}, routeLauncher},

		// An exact subcommand still dispatches; the guard sits behind that check.
		{"muxcode", []string{"agent"}, routeSubcommand},
		{"muxcode", []string{"skill"}, routeSubcommand},
	}
	for _, c := range cases {
		if got := routeFor(c.base, c.args); got != c.want {
			t.Errorf("routeFor(%q, %v) = %d, want %d", c.base, c.args, got, c.want)
		}
	}
}

// The near-miss guard must never shadow a real subcommand: every name in the
// map has to reach its handler, or the guard has broken the CLI it protects.
func TestEveryKnownSubcommandStillDispatches(t *testing.T) {
	for sub := range knownSubcommands {
		if got := routeFor("muxcode", []string{sub}); got != routeSubcommand {
			t.Errorf("routeFor(muxcode, [%q]) = %d, want routeSubcommand", sub, got)
		}
	}
}

// Ties are resolved lexicographically, so the "did you mean" line a user reads
// does not change between runs with map iteration order.
func TestNearestSubcommandIsDeterministic(t *testing.T) {
	if got := nearestSubcommand("agents"); got != "agent" {
		t.Errorf("nearestSubcommand(\"agents\") = %q, want \"agent\"", got)
	}
	first := nearestSubcommand("logs")
	for i := 0; i < 50; i++ {
		if got := nearestSubcommand("logs"); got != first {
			t.Fatalf("nearestSubcommand(\"logs\") unstable: %q then %q", first, got)
		}
	}
	if got := nearestSubcommand("my-project"); got != "" {
		t.Errorf("nearestSubcommand(\"my-project\") = %q, want \"\"", got)
	}
}

func TestWithinEditDistance1(t *testing.T) {
	near := [][2]string{
		{"agent", "agents"}, // insertion
		{"agents", "agent"}, // deletion
		{"status", "stats"}, // deletion mid-word
		{"spec", "spac"},    // substitution
		{"agent", "agent"},  // identical
	}
	for _, p := range near {
		if !withinEditDistance1(p[0], p[1]) {
			t.Errorf("withinEditDistance1(%q, %q) = false, want true", p[0], p[1])
		}
	}
	far := [][2]string{
		{"agent", "agentss"},    // two insertions
		{"agent", "my-project"}, // unrelated
		{"spec", "spac3"},       // substitution plus insertion
		{"send", "dnes"},        // same letters, four edits
	}
	for _, p := range far {
		if withinEditDistance1(p[0], p[1]) {
			t.Errorf("withinEditDistance1(%q, %q) = true, want false", p[0], p[1])
		}
	}
}

// The escape hatch has to be real: a directory shadowing a subcommand is
// reachable by path spelling, or the guard has locked a user out of it.
func TestIsPathLike(t *testing.T) {
	for _, s := range []string{"./agents", "../agents", ".", "/repos/agents", "~/repos/agents"} {
		if !isPathLike(s) {
			t.Errorf("isPathLike(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"agents", "my-project", "muxcode"} {
		if isPathLike(s) {
			t.Errorf("isPathLike(%q) = true, want false", s)
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
