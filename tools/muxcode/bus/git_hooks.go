package bus

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// commitMsgHookMarker is embedded in the hook so we can detect whether our
// stripping logic is already installed.
const commitMsgHookMarker = "# muxcode: strip-attribution"

// commitMsgHookFull is a standalone commit-msg hook that strips Co-authored-by
// trailers. Installed when no existing commit-msg hook is present.
const commitMsgHookFull = `#!/bin/sh
# muxcode: strip-attribution
# Strips Co-authored-by trailers added by AI coding assistants.
# Installed by muxcode session launcher — safe to remove.

sed '/^[[:space:]]*[Cc]o-[Aa]uthored-[Bb]y:/d' "$1" > "$1.tmp" && mv "$1.tmp" "$1"
`

// commitMsgHookAppend is appended to an existing commit-msg hook that does
// not yet contain our marker. No shebang — it chains with the existing script.
const commitMsgHookAppend = `

# muxcode: strip-attribution
# Strips Co-authored-by trailers added by AI coding assistants.
sed '/^[[:space:]]*[Cc]o-[Aa]uthored-[Bb]y:/d' "$1" > "$1.tmp" && mv "$1.tmp" "$1"
`

// InstallCommitMsgHook ensures a git commit-msg hook exists in projectDir
// that strips Co-authored-by trailer lines from every commit message.
//
// Behavior:
//   - No .git directory → no-op (not a git repo).
//   - Respects core.hooksPath if configured.
//   - Hook already has our marker → no-op (idempotent).
//   - Existing hook without marker → appends stripping logic.
//   - No existing hook → creates a new standalone hook.
func InstallCommitMsgHook(projectDir string) error {
	return installGitHook(projectDir, "commit-msg", commitMsgHookMarker, commitMsgHookFull, commitMsgHookAppend)
}

// installGitHook installs (or extends) a git hook named `name` in projectDir.
//
// Behavior:
//   - No .git directory → no-op (not a git repo).
//   - Respects core.hooksPath if configured.
//   - Hook already contains marker → no-op (idempotent).
//   - Existing hook without marker → appends `appendBody` (chains, no shebang).
//   - No existing hook → writes `fullBody` as a fresh executable hook.
//
// A ReadFile error other than "not exists" (e.g. a permission error) is returned
// rather than swallowed, so a readable-but-unwritable hook is never silently
// overwritten.
func installGitHook(projectDir, name, marker, fullBody, appendBody string) error {
	hooksDir, err := resolveGitHooksDir(projectDir)
	if err != nil {
		return nil // not a git repo — silently skip
	}

	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	hookPath := filepath.Join(hooksDir, name)

	existing, err := os.ReadFile(hookPath)
	if err == nil {
		// Hook exists — check for our marker.
		if strings.Contains(string(existing), marker) {
			return nil // already installed
		}
		// Append our logic to the existing hook.
		f, err := os.OpenFile(hookPath, os.O_APPEND|os.O_WRONLY, 0755)
		if err != nil {
			return fmt.Errorf("open existing hook: %w", err)
		}
		defer f.Close()
		_, err = f.WriteString(appendBody)
		return err
	}
	if !os.IsNotExist(err) {
		// A real read error (e.g. permission) — do NOT fall through and clobber.
		return fmt.Errorf("read %s hook: %w", name, err)
	}

	// No existing hook — create a fresh one.
	return os.WriteFile(hookPath, []byte(fullBody), 0755)
}

// prepareCommitMsgHookMarker identifies our branch-time trailer logic.
const prepareCommitMsgHookMarker = "# muxcode: branch-time-trailer"

// prepareCommitMsgHookFull is a standalone prepare-commit-msg hook that appends
// a `Time-spent:` trailer reflecting the active time tracked for the current
// branch. Installed when no existing prepare-commit-msg hook is present.
//
// $2 is the message source; skip merge/squash/amend (source "merge", "squash",
// "commit") so those don't get a duplicate or misattributed trailer. The trailer
// value comes from `muxcode branch-time --trailer` (empty when disabled, not a
// repo, or no time yet). git interpret-trailers places it correctly and
// --if-exists doNothing keeps it idempotent.
const prepareCommitMsgHookFull = `#!/bin/sh
# muxcode: branch-time-trailer
# Appends a Time-spent: trailer from muxcode branch-time tracking.
# Installed by muxcode session launcher — safe to remove.

case "$2" in
  merge|squash|commit) exit 0 ;;
esac
tr=$(muxcode branch-time --trailer 2>/dev/null)
[ -n "$tr" ] || exit 0
if command -v git >/dev/null 2>&1; then
  git interpret-trailers --if-exists doNothing --trailer "$tr" "$1" > "$1.mux.tmp" 2>/dev/null && mv "$1.mux.tmp" "$1" || rm -f "$1.mux.tmp"
fi
`

// prepareCommitMsgHookAppend chains our logic onto an existing hook (no shebang).
const prepareCommitMsgHookAppend = `

# muxcode: branch-time-trailer
# Appends a Time-spent: trailer from muxcode branch-time tracking.
case "$2" in
  merge|squash|commit) : ;;
  *)
    tr=$(muxcode branch-time --trailer 2>/dev/null)
    if [ -n "$tr" ] && command -v git >/dev/null 2>&1; then
      git interpret-trailers --if-exists doNothing --trailer "$tr" "$1" > "$1.mux.tmp" 2>/dev/null && mv "$1.mux.tmp" "$1" || rm -f "$1.mux.tmp"
    fi
    ;;
esac
`

// InstallPrepareCommitMsgHook ensures a git prepare-commit-msg hook exists in
// projectDir that appends a `Time-spent: <duration>` trailer from the
// branch-time ledger. Mirrors InstallCommitMsgHook's behavior: no-op when not a
// git repo, respects core.hooksPath, idempotent via marker, appends to an
// existing hook or creates a fresh one.
func InstallPrepareCommitMsgHook(projectDir string) error {
	return installGitHook(projectDir, "prepare-commit-msg", prepareCommitMsgHookMarker,
		prepareCommitMsgHookFull, prepareCommitMsgHookAppend)
}

// resolveGitHooksDir returns the hooks directory for the git repo at
// projectDir. It checks core.hooksPath first, falling back to .git/hooks/.
// Returns an error if projectDir has no .git directory.
func resolveGitHooksDir(projectDir string) (string, error) {
	gitDir := filepath.Join(projectDir, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no .git directory")
	}

	// Check if core.hooksPath overrides the default location.
	out, err := exec.Command("git", "-C", projectDir, "config", "core.hooksPath").Output()
	if err == nil {
		hp := strings.TrimSpace(string(out))
		if hp != "" {
			if filepath.IsAbs(hp) {
				return hp, nil
			}
			return filepath.Join(projectDir, hp), nil
		}
	}

	return filepath.Join(gitDir, "hooks"), nil
}
