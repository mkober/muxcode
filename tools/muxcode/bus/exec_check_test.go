package bus

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// claudePlaceholder mirrors the shebang-less stub Claude Code's npm package
// ships until its postinstall swaps in the native binary.
const claudePlaceholder = "echo \"claude: native binary not installed\" >&2\nexit 1\n"

func writeExe(t *testing.T, path string, content []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckExecutable(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content []byte
		ok      bool
		detail  string
	}{
		{"shebang script", []byte("#!/bin/sh\necho hi\n"), true, ""},
		{"elf", []byte("\x7fELF\x02\x01\x01rest"), true, ""},
		{"mach-o 64 little-endian", []byte{0xcf, 0xfa, 0xed, 0xfe, 0x0c, 0x00}, true, ""},
		{"mach-o fat", []byte{0xca, 0xfe, 0xba, 0xbe, 0x00}, true, ""},
		{"npm placeholder", []byte(claudePlaceholder), false, "no #! line"},
		{"empty", nil, false, "is empty"},
		{"foreign binary", []byte("MZ\x90\x00\x03\x00"), false, "not a script or a native binary"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeExe(t, filepath.Join(dir, strings.ReplaceAll(c.name, " ", "-")), c.content)
			err := CheckExecutable(p)
			if c.ok {
				if err != nil {
					t.Fatalf("CheckExecutable = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrNotExecutable) {
				t.Fatalf("CheckExecutable = %v, want ErrNotExecutable", err)
			}
			if !strings.Contains(err.Error(), c.detail) {
				t.Errorf("error %q does not say %q", err, c.detail)
			}
		})
	}
}

func TestCheckExecutable_MissingFileIsNotAFormatVerdict(t *testing.T) {
	err := CheckExecutable(filepath.Join(t.TempDir(), "absent"))
	if err == nil || errors.Is(err, ErrNotExecutable) {
		t.Fatalf("CheckExecutable(missing) = %v, want an open error, not ErrNotExecutable", err)
	}
}

// The verdict must agree with the kernel for the two shapes that matter: the
// placeholder exec fails with ENOEXEC exactly as syscall.Exec did in the
// 2026-09-23 incident, and a #! script runs.
func TestCheckExecutable_AgreesWithKernel(t *testing.T) {
	dir := t.TempDir()
	placeholder := writeExe(t, filepath.Join(dir, "placeholder"), []byte(claudePlaceholder))
	script := writeExe(t, filepath.Join(dir, "script"), []byte("#!/bin/sh\nexit 0\n"))

	if err := exec.Command(placeholder).Run(); !errors.Is(err, syscall.ENOEXEC) {
		t.Fatalf("kernel exec of placeholder = %v, want ENOEXEC (fixture no longer reproduces the incident)", err)
	}
	if CheckExecutable(placeholder) == nil {
		t.Error("CheckExecutable passed a file the kernel refuses")
	}
	if err := exec.Command(script).Run(); err != nil {
		t.Fatalf("kernel exec of #! script = %v", err)
	}
	if err := CheckExecutable(script); err != nil {
		t.Errorf("CheckExecutable refused a file the kernel runs: %v", err)
	}
}

// npmClaudeLayout builds <tmp>/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe
// holding content, linked from <tmp>/bin/claude as npm does. Returns the link.
func npmClaudeLayout(t *testing.T, content []byte) string {
	t.Helper()
	root := t.TempDir()
	exe := writeExe(t, filepath.Join(root, "lib", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"), content)
	link := filepath.Join(root, "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestExecRemedy(t *testing.T) {
	link := npmClaudeLayout(t, []byte(claudePlaceholder))
	got := ExecRemedy("claude", link)
	for _, want := range []string{"/node_modules/@anthropic-ai/claude-code/install.cjs", "re-run muxcode's install.sh"} {
		if !strings.Contains(got, want) {
			t.Errorf("npm remedy %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "npm config set") {
		t.Errorf("npm remedy %q hands out a config set that would overwrite existing allow-scripts entries", got)
	}

	plain := writeExe(t, filepath.Join(t.TempDir(), "claude"), []byte(claudePlaceholder))
	got = ExecRemedy("claude", plain)
	if strings.Contains(got, "install.cjs") || !strings.Contains(got, "Reinstall claude") {
		t.Errorf("non-npm remedy = %q, want the generic reinstall advice", got)
	}
}

func agentCLIUnexecutableEvents(t *testing.T, session string) int {
	t.Helper()
	msgs, _ := Peek(session, "edit")
	n := 0
	for _, m := range msgs {
		if m.Action == "agent-cli-unexecutable" {
			n++
		}
	}
	return n
}

func TestRefuseUnexecutableCLI(t *testing.T) {
	session := "test-refuse-unexecutable"
	base := t.TempDir()
	SetBusDirBase(base)
	t.Cleanup(ResetBusDirBase)
	Init(session, base)

	healthy := npmClaudeLayout(t, []byte("#!/bin/sh\n"))
	if err := refuseUnexecutableCLI(session, "build", "claude", healthy); err != nil {
		t.Fatalf("healthy CLI refused: %v", err)
	}
	if n := agentCLIUnexecutableEvents(t, session); n != 0 {
		t.Fatalf("healthy CLI sent %d agent-cli-unexecutable events", n)
	}

	broken := npmClaudeLayout(t, []byte(claudePlaceholder))
	err := refuseUnexecutableCLI(session, "build", "claude", broken)
	if err == nil || !strings.Contains(err.Error(), "cannot launch claude") {
		t.Fatalf("placeholder CLI: err = %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "install.cjs") {
		t.Errorf("refusal %q does not carry the npm remedy", err)
	}
	if n := agentCLIUnexecutableEvents(t, session); n != 1 {
		t.Fatalf("placeholder CLI sent %d agent-cli-unexecutable events, want 1", n)
	}
}

// The provider selector must not offer a CLI the launcher would refuse; a
// runnable one on the same isolated PATH is offered (negative control).
func TestIsProviderInstalled_RefusesPlaceholder(t *testing.T) {
	for _, c := range []struct {
		name    string
		content string
		want    bool
	}{
		{"placeholder", claudePlaceholder, false},
		{"runnable", "#!/bin/sh\nexit 0\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin := t.TempDir()
			writeExe(t, filepath.Join(bin, "claude"), []byte(c.content))
			t.Setenv("PATH", bin)
			if got := isProviderInstalled("claude"); got != c.want {
				t.Errorf("isProviderInstalled(claude) = %v, want %v", got, c.want)
			}
		})
	}
	t.Run("absent", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if isProviderInstalled("claude") {
			t.Error("isProviderInstalled(claude) = true with no claude on PATH")
		}
	})
}

// End to end: a role whose CLI is a placeholder never reaches exec, and the
// same role with a runnable CLI does (negative control). A non-Claude CLI keeps
// the MUX-136 definition refusal out of the way.
func TestRunAgentLaunch_RefusesUnexecutableCLI(t *testing.T) {
	for _, c := range []struct {
		name    string
		content string
		refused bool
	}{
		{"placeholder", claudePlaceholder, true},
		{"runnable", "#!/bin/sh\nexit 0\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			session := "test-launch-unexecutable-" + c.name
			_, argv := launchSandbox(t, session)
			bin := t.TempDir()
			writeExe(t, filepath.Join(bin, "opencode"), []byte(c.content))
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("MUXCODE_BUILD_CLI", "opencode")

			err := RunAgentLaunch("build")
			if c.refused {
				if err == nil || !strings.Contains(err.Error(), "cannot launch opencode") {
					t.Fatalf("err = %v, want refusal", err)
				}
				if len(*argv) != 0 {
					t.Fatalf("exec ran for an unexecutable CLI: %v", *argv)
				}
				return
			}
			if err != nil {
				t.Fatalf("runnable CLI: err = %v", err)
			}
			if len(*argv) == 0 || (*argv)[0] != "opencode" {
				t.Fatalf("runnable CLI: argv = %v, want exec of opencode", *argv)
			}
		})
	}
}
