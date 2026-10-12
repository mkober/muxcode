package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// EnsureCodexProjectTrust records dir's project as trusted in Codex's user
// config ($CODEX_HOME/config.toml, default ~/.codex) so Codex launches
// without its directory-trust prompt. The project is the nearest ancestor
// holding .git, else dir itself — the root Codex keys trust on.
//
// A table defined twice is invalid TOML that stops every Codex agent from
// starting, and muxcode has no TOML parser, so the write is conservative:
// nothing is written when the config may already configure the project
// (codexConfigMentionsProject), whatever its trust_level — an explicit
// "untrusted" is the user's decision — and the trust-prompt road answers the
// prompt instead. Appends are serialized by a flock in $CODEX_HOME, because
// every Codex agent in a session launches at once. A symlinked config.toml
// (dotfile-managed) is updated at its target, so the link survives.
//
// It reports whether a table was added. MUXCODE_CODEX_AUTO_TRUST=0 opts out.
//
// Codex 0.162 reworded the prompt ("Trust this folder?", "esc quit"), which
// codexTrustPromptLive did not recognise: the pane read as idle, the wake's
// Escape preamble chose Quit, and on 2026-10-12 a new dotfiles session's
// Codex agents exited to the shell on every launch and restart.
func EnsureCodexProjectTrust(dir string) (bool, error) {
	if os.Getenv("MUXCODE_CODEX_AUTO_TRUST") == "0" {
		return false, nil
	}
	home := codexHome()
	if home == "" {
		return false, fmt.Errorf("cannot resolve the Codex home directory")
	}
	root := codexTrustRoot(dir)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return false, err
	}

	lock, err := os.OpenFile(filepath.Join(home, ".muxcode-trust.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return false, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	path, err := codexConfigTarget(filepath.Join(home, "config.toml"))
	if err != nil {
		return false, err
	}
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if codexConfigMentionsProject(string(content), root) {
		return false, nil
	}

	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	updated := string(content)
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	if updated != "" {
		updated += "\n"
	}
	updated += codexProjectHeader(root) + "\ntrust_level = \"trusted\"\n"

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.muxcode-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return false, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	return true, nil
}

// trustCodexLaunchDir trusts the working directory Codex is about to launch
// in. A failure is logged and the launch proceeds: the trust-prompt road in
// ClassifyPane still answers the prompt.
func trustCodexLaunchDir(session, role string) {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	added, err := EnsureCodexProjectTrust(dir)
	switch {
	case err != nil:
		LogLifecycle(session, "warn", "codex-trust", "codex-trust-failed",
			fmt.Sprintf("%s: could not trust %s in Codex config: %v", role, codexTrustRoot(dir), err))
	case added:
		LogLifecycle(session, "info", "codex-trust", "codex-trust-added",
			fmt.Sprintf("%s: trusted %s in %s", role, codexTrustRoot(dir), filepath.Join(codexHome(), "config.toml")))
	}
}

// codexTrustRoot returns the nearest ancestor of dir holding .git (a
// directory, or a worktree's file), else dir.
func codexTrustRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return abs
		}
	}
}

// codexConfigTarget returns the file a write to path must replace: path
// itself, or a symlink's resolved target so the link is kept.
func codexConfigTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	return filepath.EvalSymlinks(path)
}

func tomlBasicString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func codexProjectHeader(root string) string {
	return "[projects." + tomlBasicString(root) + "]"
}

// codexProjectsUnextendable matches a projects key that no appended
// [projects."…"] header may extend: an inline table assignment, in any key
// quoting, or an array of tables.
var codexProjectsUnextendable = regexp.MustCompile(`(?m)^\s*(\[\[\s*)?["']?projects["']?\s*(=|\]\]|\.)`)

// codexConfigMentionsProject reports whether appending a [projects."<root>"]
// table to content could be invalid TOML: root appears as a quoted string in
// either spelling (covering header whitespace, dotted keys and inline
// entries), any \u or \U escape could spell it some other way, or projects is
// an inline table or array of tables. It errs toward true; a false positive
// only leaves the prompt to the trust-prompt road.
func codexConfigMentionsProject(content, root string) bool {
	if strings.Contains(content, tomlBasicString(root)) || strings.Contains(content, "'"+root+"'") {
		return true
	}
	if strings.Contains(content, `\u`) || strings.Contains(content, `\U`) {
		return true
	}
	for _, m := range codexProjectsUnextendable.FindAllStringSubmatch(content, -1) {
		if m[1] != "" || m[2] == "=" {
			return true
		}
	}
	return false
}
