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
	hooksDir, err := resolveGitHooksDir(projectDir)
	if err != nil {
		return nil // not a git repo — silently skip
	}

	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	hookPath := filepath.Join(hooksDir, "commit-msg")

	existing, err := os.ReadFile(hookPath)
	if err == nil {
		// Hook exists — check for our marker.
		if strings.Contains(string(existing), commitMsgHookMarker) {
			return nil // already installed
		}
		// Append our logic to the existing hook.
		f, err := os.OpenFile(hookPath, os.O_APPEND|os.O_WRONLY, 0755)
		if err != nil {
			return fmt.Errorf("open existing hook: %w", err)
		}
		defer f.Close()
		_, err = f.WriteString(commitMsgHookAppend)
		return err
	}

	// No existing hook — create a fresh one.
	return os.WriteFile(hookPath, []byte(commitMsgHookFull), 0755)
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
