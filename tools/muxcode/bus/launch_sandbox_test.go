package bus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sandboxTestEnv isolates provider resolution and the sandbox's inputs: a
// scratch bus (so no live session's runtime overrides decide the provider) and
// a pinned lifecycle log dir. HOME stays real: the build role's `go env` writes
// Go telemetry under it after returning, which races a temp HOME's cleanup.
func sandboxTestEnv(t *testing.T) string {
	t.Helper()
	useTempBusDir(t)
	useEmptyShellConfig(t)
	session := testSession(t)
	t.Setenv("BUS_SESSION", session)
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	return session
}

// A non-Codex role fixes no sandbox at launch; a fingerprint for it would have
// the watchdog reloading Claude agents over a sandbox they never had.
func TestLaunchSandboxFingerprint_CodexOnly(t *testing.T) {
	sandboxTestEnv(t)
	t.Setenv("MUXCODE_REVIEW_CLI", "claude")
	t.Setenv("MUXCODE_BUILD_CLI", "codex")
	if fp := LaunchSandboxFingerprint("review"); fp != "" {
		t.Errorf("claude review fingerprint = %q, want empty", fp)
	}
	if LaunchSandboxFingerprint("build") == "" {
		t.Error("codex build fingerprint is empty")
	}
}

// The fingerprint must follow the sandbox flags, or an install that widens a
// grant goes unseen — and stay stable while they are unchanged, or every Codex
// agent reloads on every check.
func TestLaunchSandboxFingerprint_FollowsSandboxArgs(t *testing.T) {
	sandboxTestEnv(t)
	t.Setenv("MUXCODE_REVIEW_CLI", "codex")
	before := LaunchSandboxFingerprint("review")
	if again := LaunchSandboxFingerprint("review"); again != before {
		t.Fatalf("fingerprint unstable for unchanged flags: %s then %s", before, again)
	}
	t.Setenv("MUXCODE_LIFECYCLE_LOG_DIR", t.TempDir())
	if after := LaunchSandboxFingerprint("review"); after == before {
		t.Error("fingerprint unchanged after the sandbox's writable roots changed")
	}
}

// The watchdog reads what the launch stamped; a stamp it cannot read back marks
// every Codex agent stale.
func TestStampLaunchSandbox_RoundTrip(t *testing.T) {
	session := sandboxTestEnv(t)
	t.Setenv("MUXCODE_BUILD_CLI", "codex")
	if got := ReadLaunchSandbox(session, "build"); got != "" {
		t.Fatalf("unstamped read = %q, want empty", got)
	}
	StampLaunchSandbox(session, "build")
	if got, want := ReadLaunchSandbox(session, "build"), LaunchSandboxFingerprint("build"); got != want {
		t.Errorf("stamp read back %q, want %q", got, want)
	}
}

// fakeInstalledMuxcode puts a muxcode on PATH that fails until the returned
// file exists, then reports its contents as build's fingerprint. Rewriting the
// file changes the answer without changing the binary.
func fakeInstalledMuxcode(t *testing.T) (fpFile string) {
	t.Helper()
	dir := t.TempDir()
	fpFile = filepath.Join(dir, "fp")
	script := "#!/bin/sh\n[ -f '" + fpFile + "' ] || exit 1\nprintf 'build\\t%s\\n' \"$(cat '" + fpFile + "')\"\n"
	if err := os.WriteFile(filepath.Join(dir, "muxcode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")
	return fpFile
}

func writeFingerprint(t *testing.T, fpFile, fp string) {
	t.Helper()
	if err := os.WriteFile(fpFile, []byte(fp), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A transient probe failure must not disable the check until the next install:
// the unchanged binary is probed again once the retry interval has passed, and
// not before.
func TestSandboxFingerprinter_RetriesFailedProbe(t *testing.T) {
	cases := []struct {
		name   string
		retry  time.Duration
		wantFP string
	}{
		{"retry due", 0, "abc"},
		{"within backoff", time.Hour, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			session := sandboxTestEnv(t)
			fpFile := fakeInstalledMuxcode(t)
			probe := SandboxFingerprinter(c.retry)
			if _, err := probe(session, []string{"build"}); err == nil {
				t.Fatal("a failing probe reported no error")
			}
			writeFingerprint(t, fpFile, "abc")
			got, err := probe(session, []string{"build"})
			if err != nil || got["build"] != c.wantFP {
				t.Errorf("second probe = %v, %v; want build=%q", got, err, c.wantFP)
			}
		})
	}
}

// The sandbox follows inputs the binary key misses — a writable root's symlink
// target, the Go caches, the config file — so a successful answer from an
// unchanged binary is asked again once refresh has passed, and not before.
func TestSandboxFingerprinter_RefreshesSuccessfulProbe(t *testing.T) {
	cases := []struct {
		name    string
		refresh time.Duration
		wantFP  string
	}{
		{"refresh due", 0, "def"},
		{"within refresh", time.Hour, "abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			session := sandboxTestEnv(t)
			fpFile := fakeInstalledMuxcode(t)
			writeFingerprint(t, fpFile, "abc")
			probe := SandboxFingerprinter(c.refresh)
			if got, err := probe(session, []string{"build"}); err != nil || got["build"] != "abc" {
				t.Fatalf("first probe = %v, %v; want build=abc", got, err)
			}
			writeFingerprint(t, fpFile, "def")
			got, err := probe(session, []string{"build"})
			if err != nil || got["build"] != c.wantFP {
				t.Errorf("second probe = %v, %v; want build=%q", got, err, c.wantFP)
			}
		})
	}
}

// The daemon parses this output from the installed binary, which may be a
// different build than its own.
func TestParseSandboxFingerprints(t *testing.T) {
	got := ParseSandboxFingerprints("build\tabc123\nreview\t\nnot a row\n\n")
	if got["build"] != "abc123" || got["review"] != "" || len(got) != 2 {
		t.Errorf("parsed %v, want build=abc123 and an empty review only", got)
	}
}
