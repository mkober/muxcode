package bus

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// MUX-136 Phase 3 sole-caller proof: Claude Code's `--agent` flag is emitted
// from exactly one place, bound to `--agents` on the same line, and no script,
// config, agent, or skill file launches a role by bare name. A spot check found
// the emitter once; this walks the tree so a second emitter cannot appear
// unnoticed.
//
// The allowlist names every file that may contain the literal `"--agent"`:
// the bound emitter, OpenCode's launcher (a different CLI whose `--agent
// <role>` names a config-file agent), and the probe that reads argv.
var claudeAgentFlagFiles = map[string]string{
	"bus/provider_claude.go":   "emitter",
	"bus/provider_opencode.go": "opencode CLI",
	"bus/definition.go":        "probe",
}

// repoRoots are the non-Go trees a launch could hide in; config/nvim is
// editor config and never launches an agent.
var repoRoots = []string{"scripts", "config", "agents", "skills"}

// bareLaunchAllowed names the non-Go files, by repo-relative path, that may
// match the bare-launch pattern, each with the invocation the matching line
// must itself carry. Only a non-Claude CLI belongs here: OpenCode's
// `--agent <name>` names a config-file agent and is not the Claude flag this
// test guards. The line must name the invocation, so a continuation line of
// a multi-line claude launch in the same file is still refused.
var bareLaunchAllowed = map[string]string{
	"scripts/test-role-write-guard.sh": "opencode run ", // MUX-157 live permissions probe
}

func bareLaunchExempt(rel, line string) bool {
	want, ok := bareLaunchAllowed[rel]
	return ok && strings.Contains(line, want) && !strings.Contains(line, "claude")
}

func TestBareLaunchExempt(t *testing.T) {
	const file = "scripts/test-role-write-guard.sh"
	cases := []struct {
		rel, line string
		want      bool
	}{
		{file, `  perl -e 'alarm shift; exec @ARGV' 300 opencode run --standalone --archived --agent "$AGENT" --format json "$PROMPT") \`, true},
		{file, `      --agent "$AGENT" --format json`, false},
		{file, `claude --agent test-runner -p hi`, false},
		{file, `opencode run --agent x; claude --agent y`, false},
		{"scripts/other.sh", `opencode run --agent "$AGENT"`, false},
	}
	for _, c := range cases {
		if got := bareLaunchExempt(c.rel, c.line); got != c.want {
			t.Errorf("bareLaunchExempt(%s, %q) = %v, want %v", c.rel, c.line, got, c.want)
		}
	}
}

func TestClaudeAgentFlagSoleEmitter(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	moduleRoot := filepath.Dir(filepath.Dir(thisFile))
	repoRoot := filepath.Dir(filepath.Dir(moduleRoot))

	seenEmitter := false
	err := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(moduleRoot, path)
		hits := linesContaining(t, path, `"--agent"`)
		if len(hits) == 0 {
			return nil
		}
		kind, allowed := claudeAgentFlagFiles[filepath.ToSlash(rel)]
		if !allowed {
			t.Errorf("new --agent emitter outside the allowlist: %s:%d", rel, hits[0].n)
			return nil
		}
		if kind != "emitter" {
			return nil
		}
		seenEmitter = true
		if len(hits) != 1 || !strings.Contains(hits[0].text, `"--agents"`) {
			t.Errorf("%s must emit --agent exactly once, bound to --agents on the same line; got %d hit(s): %+v", rel, len(hits), hits)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", moduleRoot, err)
	}
	if !seenEmitter {
		t.Fatalf("walk never reached the emitter — module root resolved to %s", moduleRoot)
	}

	bareLaunch := regexp.MustCompile(`--agent[ =]`)
	nonGo := 0
	for _, root := range repoRoots {
		_ = filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.Contains(path, string(filepath.Separator)+"nvim"+string(filepath.Separator)) {
				return nil
			}
			nonGo++
			rel, _ := filepath.Rel(repoRoot, path)
			for _, hit := range linesMatching(t, path, bareLaunch) {
				if bareLaunchExempt(filepath.ToSlash(rel), hit.text) {
					continue
				}
				t.Errorf("bare-name launch outside the launcher: %s:%d: %s", rel, hit.n, strings.TrimSpace(hit.text))
			}
			return nil
		})
	}
	for _, name := range []string{"install.sh", "build.sh", "test.sh", "Makefile"} {
		path := filepath.Join(repoRoot, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		nonGo++
		for _, hit := range linesMatching(t, path, bareLaunch) {
			t.Errorf("bare-name launch outside the launcher: %s:%d: %s", name, hit.n, strings.TrimSpace(hit.text))
		}
	}
	for rel := range bareLaunchAllowed {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		exempt := 0
		if _, err := os.Stat(path); err == nil {
			for _, hit := range linesMatching(t, path, bareLaunch) {
				if bareLaunchExempt(rel, hit.text) {
					exempt++
				}
			}
		}
		if exempt == 0 {
			t.Errorf("stale bareLaunchAllowed entry %s: no longer launches with --agent; remove it", rel)
		}
	}
	if nonGo < 20 {
		t.Fatalf("non-Go scan covered only %d files — repo root resolved to %s", nonGo, repoRoot)
	}
}

type lineHit struct {
	n    int
	text string
}

func linesContaining(t *testing.T, path, needle string) []lineHit {
	t.Helper()
	return linesMatching(t, path, regexp.MustCompile(regexp.QuoteMeta(needle)))
}

func linesMatching(t *testing.T, path string, re *regexp.Regexp) []lineHit {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var hits []lineHit
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		if re.MatchString(sc.Text()) {
			hits = append(hits, lineHit{n: n, text: sc.Text()})
		}
	}
	return hits
}
