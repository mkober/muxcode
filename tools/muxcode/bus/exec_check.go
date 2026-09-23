package bus

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrNotExecutable reports an agent CLI that is on PATH but that the kernel
// cannot exec directly — syscall.Exec would fail with "exec format error".
var ErrNotExecutable = errors.New("not an executable program")

// claudeNPMPackageDir is the path fragment of Claude Code's npm package.
const claudeNPMPackageDir = "/node_modules/@anthropic-ai/claude-code/"

// execMagics are the leading bytes the kernel accepts without a shell: a `#!`
// interpreter line, ELF, and Mach-O (thin, either byte order, and fat).
var execMagics = [][]byte{
	[]byte("#!"),
	{0x7f, 'E', 'L', 'F'},
	{0xcf, 0xfa, 0xed, 0xfe}, {0xce, 0xfa, 0xed, 0xfe},
	{0xfe, 0xed, 0xfa, 0xcf}, {0xfe, 0xed, 0xfa, 0xce},
	{0xca, 0xfe, 0xba, 0xbe}, {0xbe, 0xba, 0xfe, 0xca},
}

// CheckExecutable is a file-format precheck: it returns nil when path starts
// with a header the kernel execs directly, and an error wrapping
// ErrNotExecutable naming what was found otherwise. Passing is not proof the
// program runs — a truncated or wrong-architecture binary with a valid header
// still passes — it only rules out the shape that cannot run at all. An
// interactive shell runs a shebang-less text file anyway, so a CLI that works
// from a terminal can still fail every muxcode launch: on 2026-09-23 npm 12
// blocked Claude Code's postinstall, leaving its placeholder `claude` in place,
// and every agent pane died with "exec claude: exec format error". Windows has
// no such distinction and always passes.
func CheckExecutable(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 64)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	head = head[:n]
	for _, m := range execMagics {
		if bytes.HasPrefix(head, m) {
			return nil
		}
	}
	if n == 0 {
		return fmt.Errorf("%s is empty: %w", path, ErrNotExecutable)
	}
	if line, _, _ := bytes.Cut(head, []byte("\n")); isPrintableText(line) {
		return fmt.Errorf("%s is a text file with no #! line (starts %q): %w", path, string(line), ErrNotExecutable)
	}
	return fmt.Errorf("%s is not a script or a native binary for this machine: %w", path, ErrNotExecutable)
}

func isPrintableText(b []byte) bool {
	for _, c := range b {
		if c != '\t' && c != '\r' && (c < 0x20 || c > 0x7e) {
			return false
		}
	}
	return len(b) > 0
}

// ExecRemedy names the fix for a CLI that failed CheckExecutable. Claude
// Code's npm placeholder gets the skipped install step and the npm setting
// that stops the next update reinstating it; muxcode never runs that step
// itself, because it is the install script the user's npm policy blocked.
func ExecRemedy(binary, path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	if i := strings.Index(resolved, claudeNPMPackageDir); i >= 0 {
		pkg := resolved[:i+len(claudeNPMPackageDir)-1]
		return fmt.Sprintf("Claude Code's npm install script (%s/install.cjs) did not run — npm 12 blocks it by default. Fix: re-run muxcode's install.sh, which runs that step and adds @anthropic-ai/claude-code to npm's allow-scripts list without dropping existing entries, so the next update keeps the native binary",
			pkg)
	}
	return fmt.Sprintf("Reinstall %s, or put a working %s earlier on PATH", binary, binary)
}

// refuseUnexecutableCLI is the launcher check behind ErrNotExecutable: an
// agent whose CLI the kernel cannot exec is refused, logged as
// `launch-refused`, and reported to edit with the fix —
// instead of dying on a bare "exec format error" the daemon then relaunches
// into again.
func refuseUnexecutableCLI(session, role, binary, binPath string) error {
	err := CheckExecutable(binPath)
	if err == nil {
		return nil
	}
	detail := fmt.Sprintf("%s: cannot launch %s — %v", role, binary, err)
	remedy := ExecRemedy(binary, binPath)
	if session != "" {
		LogLifecycle(session, "error", "launch", "launch-refused", detail)
		m := NewMessage(NormalizeBusRole(role), "edit", "event", "agent-cli-unexecutable",
			detail+". "+remedy+", then relaunch: muxcode agent launch "+role, "")
		_ = SendNoCC(session, m)
	}
	return fmt.Errorf("%s\n%s", detail, remedy)
}
