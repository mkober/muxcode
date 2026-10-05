package bus

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	resumeIDB = "0f3a9c2e-7d1b-4e5a-9b8c-1d2e3f4a5b6c"
	resumeIDC = "1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f"
)

// stubTmuxCapture serves pane as tmux would for `capture-pane -S -<n>`: only
// the last n lines, so a capture shallower than the hint's depth misses it.
func stubTmuxCapture(t *testing.T, pane string) {
	t.Helper()
	orig := tmuxOutputRunner
	t.Cleanup(func() { tmuxOutputRunner = orig })
	tmuxOutputRunner = func(args ...string) (string, error) {
		lines := strings.Split(pane, "\n")
		for i, a := range args {
			if a == "-S" && i+1 < len(args) {
				if n, err := strconv.Atoi(strings.TrimPrefix(args[i+1], "-")); err == nil && n < len(lines) {
					lines = lines[len(lines)-n:]
				}
			}
		}
		return strings.Join(lines, "\n"), nil
	}
}

// The banner sits eight lines above the bottom — the shell redrew its prompt
// several times after the exit. IsAgentAlive's -S -5 window cannot see it;
// the resume capture must reach deeper. Narrowing resumeCaptureLines to 5
// fails this test.
func TestCaptureResumeSessionID_HintAboveLastFiveLines(t *testing.T) {
	pane := "❯ \n\nResume this session with:\nclaude --resume " + resumeIDA + "\n" + strings.Repeat("$ \n", 7) + "$ "
	if tail := strings.Split(pane, "\n"); strings.Contains(strings.Join(tail[len(tail)-5:], "\n"), "Resume") {
		t.Fatal("fixture invalid: the banner must sit above the last five lines")
	}
	stubTmuxCapture(t, pane)
	if id, ok := CaptureResumeSessionID("s:edit.1"); !ok || id != resumeIDA {
		t.Errorf("CaptureResumeSessionID = (%q, %v), want (%q, true)", id, ok, resumeIDA)
	}
}

// writeTranscript writes a Claude Code transcript whose head records agent;
// an empty agent writes a transcript recording none.
func writeTranscript(t *testing.T, dir, id, agent string) {
	t.Helper()
	body := `{"type":"permission-mode","permissionMode":"default","sessionId":"` + id + `"}` + "\n"
	if agent != "" {
		body = `{"type":"agent-setting","agentSetting":"` + agent + `","sessionId":"` + id + `"}` + "\n" + body
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The session and spawn role transcriptFixture registers as its cwd's owner.
const (
	resumeSession = "resume-id-test"
	resumeRole    = "spawn-1a15e8f0"
)

// transcriptFixture is transcriptFixtureOwned with cwd registered as
// resumeRole's own worktree, the one place the transcript fallback may answer.
func transcriptFixture(t *testing.T) (cwd, projectDir string) {
	t.Helper()
	return transcriptFixtureOwned(t, resumeRole)
}

// transcriptFixtureOwned points claudeProjectsDir at a scratch root and returns
// a real cwd plus its (empty, created) Claude project directory, with
// resumeSession's spawn registry recording each of owners as a worker whose
// worktree is cwd. No owners leaves cwd unowned, like the shared repo root.
func transcriptFixtureOwned(t *testing.T, owners ...string) (cwd, projectDir string) {
	t.Helper()
	cwd, projectDir = newTranscriptDirs(t)
	SetBusDirBase(t.TempDir())
	t.Cleanup(ResetBusDirBase)
	if err := os.MkdirAll(BusDir(resumeSession), 0o755); err != nil {
		t.Fatal(err)
	}
	var entries []SpawnEntry
	for _, role := range owners {
		entries = append(entries, SpawnEntry{ID: role, SpawnRole: role, Status: "running", Worktree: cwd})
	}
	if err := WriteSpawnEntries(resumeSession, entries); err != nil {
		t.Fatal(err)
	}
	return cwd, projectDir
}

func newTranscriptDirs(t *testing.T) (cwd, projectDir string) {
	t.Helper()
	root := t.TempDir()
	orig := claudeProjectsDir
	t.Cleanup(func() { claudeProjectsDir = orig })
	claudeProjectsDir = func() string { return filepath.Join(root, "projects") }

	cwd = filepath.Join(t.TempDir(), "spawn-1a15e8f0")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	name, err := claudeProjectDirName(cwd)
	if err != nil {
		t.Fatal(err)
	}
	projectDir = filepath.Join(root, "projects", name)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return cwd, projectDir
}

// Claude Code names a cwd's transcript directory by its symlink-resolved path
// with every non-alphanumeric byte a dash. macOS spawn dirs live under
// /var/folders, recorded by Claude as /private/var/folders: encoding the
// unresolved path names a directory that never exists.
func TestClaudeProjectDirName(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "muxcode-spawn.s_1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(resolved)

	for _, cwd := range []string{dir, link} {
		got, err := claudeProjectDirName(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("claudeProjectDirName(%s) = %q, want %q", cwd, got, want)
		}
	}
	if !strings.HasSuffix(want, "-muxcode-spawn-s-1") {
		t.Errorf("encoding %q does not dash the dot and underscore", want)
	}
	if strings.HasPrefix(base, "/var/") && !strings.HasPrefix(want, "-private-var-") {
		t.Errorf("macOS /var cwd encoded as %q, want the /private/var resolution", want)
	}
}

func TestTranscriptIDForCwd(t *testing.T) {
	t.Run("one transcript of the agent resolves", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, resumeIDA, "code-editor")
		writeTranscript(t, dir, resumeIDB, "git-manager")
		if err := os.Mkdir(filepath.Join(dir, resumeIDC), 0o755); err != nil {
			t.Fatal(err)
		}
		id, err := TranscriptIDForCwd(resumeSession, resumeRole, cwd, "code-editor")
		if err != nil || id != resumeIDA {
			t.Errorf("got (%q, %v), want (%q, nil)", id, err, resumeIDA)
		}
	})

	// Two sessions of one agent in its own worktree: a worker relaunched
	// fresh. Picking either could hand over the wrong conversation.
	t.Run("two transcripts of the agent decline as ambiguous", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, resumeIDA, "code-editor")
		writeTranscript(t, dir, resumeIDB, "code-editor")
		id, err := TranscriptIDForCwd(resumeSession, resumeRole, cwd, "code-editor")
		if !errors.Is(err, ErrTranscriptAmbiguous) || id != "" {
			t.Errorf("got (%q, %v), want ErrTranscriptAmbiguous", id, err)
		}
	})

	t.Run("a lone transcript of another agent declines", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, resumeIDA, "planner")
		if id, err := TranscriptIDForCwd(resumeSession, resumeRole, cwd, "git-manager"); !errors.Is(err, ErrTranscriptNone) || id != "" {
			t.Errorf("got (%q, %v), want ErrTranscriptNone", id, err)
		}
	})

	t.Run("a transcript recording no agent never qualifies", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, resumeIDA, "")
		if id, err := TranscriptIDForCwd(resumeSession, resumeRole, cwd, "code-editor"); !errors.Is(err, ErrTranscriptNone) || id != "" {
			t.Errorf("got (%q, %v), want ErrTranscriptNone", id, err)
		}
	})

	t.Run("a non-uuid transcript name is ignored", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, "not-a-session", "code-editor")
		if id, err := TranscriptIDForCwd(resumeSession, resumeRole, cwd, "code-editor"); !errors.Is(err, ErrTranscriptNone) || id != "" {
			t.Errorf("got (%q, %v), want ErrTranscriptNone", id, err)
		}
	})

	t.Run("no project directory declines", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		if id, err := TranscriptIDForCwd(resumeSession, resumeRole, cwd, "code-editor"); !errors.Is(err, ErrTranscriptNone) || id != "" {
			t.Errorf("got (%q, %v), want ErrTranscriptNone", id, err)
		}
	})

	t.Run("no agent name declines", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, resumeIDA, "")
		if id, err := TranscriptIDForCwd(resumeSession, resumeRole, cwd, ""); !errors.Is(err, ErrTranscriptNone) || id != "" {
			t.Errorf("got (%q, %v), want ErrTranscriptNone", id, err)
		}
	})
}

// Matching the recorded agent does not prove whose conversation a transcript
// is: a spawn worker launched with the edit definition shares code-editor and
// the repo root with edit. Only a worktree the spawn registry assigns to the
// launching role alone lets the fallback answer (MUX-139 Decision 1); each
// decline below holds exactly one code-editor transcript, which the owned
// control resolves.
func TestTranscriptIDForCwd_OwnershipGate(t *testing.T) {
	cases := []struct {
		name   string
		owners []string
		role   string
		want   error
	}{
		{"the role's own worktree resolves", []string{resumeRole}, resumeRole, nil},
		{"a shared cwd no spawn owns declines", nil, resumeRole, ErrTranscriptSharedCwd},
		{"a persistent role in a shared cwd declines", nil, "edit", ErrTranscriptSharedCwd},
		{"another worker's worktree declines", []string{"spawn-0b0b0b0b"}, resumeRole, ErrTranscriptSharedCwd},
		{"a worktree two spawns claim declines", []string{resumeRole, "spawn-0b0b0b0b"}, resumeRole, ErrTranscriptSharedCwd},
		{"no agent role declines", []string{resumeRole}, "", ErrTranscriptSharedCwd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cwd, dir := transcriptFixtureOwned(t, tc.owners...)
			writeTranscript(t, dir, resumeIDA, "code-editor")
			id, err := TranscriptIDForCwd(resumeSession, tc.role, cwd, "code-editor")
			if tc.want == nil {
				if err != nil || id != resumeIDA {
					t.Errorf("got (%q, %v), want (%q, nil)", id, err, resumeIDA)
				}
				return
			}
			if !errors.Is(err, tc.want) || id != "" {
				t.Errorf("got (%q, %v), want %v", id, err, tc.want)
			}
		})
	}
}

// The banner names the session that died; a transcript only names one that
// ran here. So the pane wins whenever it answers, and the transcript is
// consulted only on a miss.
func TestFindResumeID(t *testing.T) {
	banner := "❯ \n\nResume this session with:\nclaude --resume " + resumeIDA + "\n$ "

	t.Run("pane answers first", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, resumeIDB, "code-editor")
		stubTmuxCapture(t, banner)
		id, source, _, ok := FindResumeID("s:edit.1", resumeSession, resumeRole, cwd, "code-editor")
		if !ok || id != resumeIDA || source != ResumeSourcePane {
			t.Errorf("got (%q, %q, %v), want (%q, pane, true)", id, source, ok, resumeIDA)
		}
	})

	t.Run("pane miss falls back to the transcript", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, resumeIDB, "code-editor")
		stubTmuxCapture(t, "❯ \n$ ")
		id, source, _, ok := FindResumeID("s:edit.1", resumeSession, resumeRole, cwd, "code-editor")
		if !ok || id != resumeIDB || source != ResumeSourceTranscript {
			t.Errorf("got (%q, %q, %v), want (%q, transcript, true)", id, source, ok, resumeIDB)
		}
	})

	t.Run("both declining names both reasons", func(t *testing.T) {
		cwd, dir := transcriptFixture(t)
		writeTranscript(t, dir, resumeIDB, "code-editor")
		writeTranscript(t, dir, resumeIDC, "code-editor")
		stubTmuxCapture(t, "❯ \n$ ")
		id, _, reason, ok := FindResumeID("s:edit.1", resumeSession, resumeRole, cwd, "code-editor")
		if ok || id != "" {
			t.Fatalf("got (%q, %v), want a decline", id, ok)
		}
		if !strings.Contains(reason, "no resume banner") || !strings.Contains(reason, ErrTranscriptAmbiguous.Error()) {
			t.Errorf("reason %q does not name both declines", reason)
		}
	})

	t.Run("a pane miss in a shared cwd launches fresh, naming why", func(t *testing.T) {
		cwd, dir := transcriptFixtureOwned(t)
		writeTranscript(t, dir, resumeIDB, "code-editor")
		stubTmuxCapture(t, "❯ \n$ ")
		id, _, reason, ok := FindResumeID("s:spawn-1a15e8f0.1", resumeSession, resumeRole, cwd, "code-editor")
		if ok || id != "" {
			t.Fatalf("got (%q, %v), want a decline", id, ok)
		}
		if !strings.Contains(reason, ErrTranscriptSharedCwd.Error()) {
			t.Errorf("reason %q does not name the shared cwd", reason)
		}
	})
}
