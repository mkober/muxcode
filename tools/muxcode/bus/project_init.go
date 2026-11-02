package bus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Project files session-start init creates (MUX-207 decision 1), relative to
// the project root.
const (
	initConfigPath   = ".muxcode/config"
	initSettingsPath = ".claude/settings.json"
	initClaudeMDPath = "CLAUDE.md"
)

// ProjectInitResult names what InitProjectFiles wrote and what it declined to
// write, both as project-relative paths.
type ProjectInitResult struct {
	Created []string
	Skipped []string // "<path>: <reason>"
}

// initPatternKeys are the classification variables the generated config sets,
// each with the built-in list it falls back to (see ClassifyCommand).
var initPatternKeys = []struct {
	env      string
	defaults []string
}{
	{"MUXCODE_BUILD_PATTERNS", DefaultBuildPatterns},
	{"MUXCODE_BUILD_PRECHECK_PATTERNS", DefaultBuildPrecheckPatterns},
	{"MUXCODE_TEST_PATTERNS", DefaultTestPatterns},
	{"MUXCODE_TEST_PRECHECK_PATTERNS", DefaultTestPrecheckPatterns},
}

// toolchainPermissions are the read-only toolchain queries the generated
// .claude/settings.json allows, per detected project type. Only edit consults
// Claude permissions — every other Claude role launches with
// --dangerously-skip-permissions — and edit's guard refuses builds and tests,
// xcodebuild included, so nothing here may be a command that guard blocks.
var toolchainPermissions = map[string][]string{
	"go":     {"Bash(go version*)", "Bash(go env*)", "Bash(go list *)", "Bash(go doc *)", "Bash(gofmt -l *)"},
	"nodejs": {"Bash(node --version*)", "Bash(npm ls*)", "Bash(pnpm list*)"},
	"python": {"Bash(python3 --version*)", "Bash(pip show *)", "Bash(pip list*)"},
	"rust":   {"Bash(cargo --version*)", "Bash(cargo metadata*)", "Bash(cargo tree*)"},
	"swift": {
		"Bash(swift --version*)", "Bash(swift package describe*)", "Bash(swift package dump-package*)",
		"Bash(xcrun simctl list*)", "Bash(xcodegen --version*)",
	},
}

// InitProjectFiles creates, from detection, whichever of .muxcode/config,
// .claude/settings.json and CLAUDE.md the project lacks. It never overwrites:
// each file is created with O_EXCL, so an existing file — or a symlink at that
// path — is left byte-identical, and CLAUDE.md counts as present when
// .claude/CLAUDE.md exists, since Claude reads either. It runs no git command,
// so what it creates stays untracked for the user to review (decision 2).
// Nothing is written when detection found nothing, and a file with nothing
// detected to put in it is not written.
//
// The config is withheld only when muxcode would ignore it
// (configIgnoredReason); a user config is layered under it, never hidden.
func InitProjectFiles(projectDir string, types []ProjectType) ProjectInitResult {
	var res ProjectInitResult
	if len(types) == 0 {
		return res
	}
	write := func(rel string, data []byte) {
		if data == nil {
			return
		}
		created, err := createExclusive(filepath.Join(projectDir, rel), data)
		switch {
		case err != nil:
			res.Skipped = append(res.Skipped, rel+": "+err.Error())
		case created:
			res.Created = append(res.Created, rel)
		}
	}

	if !pathPresent(filepath.Join(projectDir, initConfigPath)) {
		if reason := configIgnoredReason(); reason != "" {
			res.Skipped = append(res.Skipped, initConfigPath+": "+reason)
		} else {
			write(initConfigPath, projectConfigContent(types))
		}
	}
	write(initSettingsPath, projectSettingsContent(types))
	if !pathPresent(filepath.Join(projectDir, ".claude", "CLAUDE.md")) {
		write(initClaudeMDPath, projectClaudeMDContent(filepath.Base(projectDir), types))
	}
	return res
}

// configIgnoredReason reports why a project .muxcode/config would be ignored,
// or "" when muxcode reads it: GetShellConfig reads a MUXCODE_CONFIG file alone.
// A home config is no reason — it is layered under the project's, so its role
// CLI and model choices keep applying (MUX-207; before layering, a project file
// would have relaunched tibo's Codex build and test agents on the default
// provider).
func configIgnoredReason() string {
	if v := os.Getenv("MUXCODE_CONFIG"); v != "" && fileExists(v) {
		return "MUXCODE_CONFIG names " + v + ", which muxcode reads instead, so a project config would be ignored"
	}
	return ""
}

// projectConfigContent renders the shell-sourceable config: the current
// classification patterns, as GetShellConfig parses them back. A list holding
// a single quote cannot be quoted for that parser and is left out.
//
// The header names detected types only, never their indicator paths: a path
// is repository data, and one holding a newline would write an active line —
// a MUXCODE_*_CLI assignment, or a command for the scripts that source this
// file — out of what was meant as a comment (review, 2026-11-02).
func projectConfigContent(types []ProjectType) []byte {
	var b strings.Builder
	b.WriteString("# muxcode project config — created at session start from project detection\n")
	b.WriteString("# (MUX-207). Review it, edit it, and commit it if you keep it; muxcode never\n")
	b.WriteString("# overwrites this file.\n#\n")
	names := make([]string, len(types))
	for i, pt := range types {
		names[i] = pt.Name
	}
	fmt.Fprintf(&b, "# Detected: %s\n", strings.Join(names, ", "))
	b.WriteString("#\n# Each *_PATTERNS value REPLACES muxcode's built-in list for this project, so a\n")
	b.WriteString("# pattern muxcode adds to its defaults later will not reach it. Delete a line to\n")
	b.WriteString("# go back to the built-in list.\n")
	for _, k := range initPatternKeys {
		v := strings.Join(envOrDefault(k.env, k.defaults), "|")
		if strings.Contains(v, "'") {
			continue
		}
		fmt.Fprintf(&b, "%s='%s'\n", k.env, v)
	}
	return []byte(b.String())
}

// projectSettingsContent renders .claude/settings.json allowing the detected
// toolchains' read-only queries, or nil when no detected type has any.
func projectSettingsContent(types []ProjectType) []byte {
	var allow []string
	seen := map[string]bool{}
	for _, pt := range types {
		for _, p := range toolchainPermissions[pt.Name] {
			if !seen[p] {
				seen[p] = true
				allow = append(allow, p)
			}
		}
	}
	if len(allow) == 0 {
		return nil
	}
	settings := map[string]any{"permissions": map[string]any{"allow": allow}}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil
	}
	return append(data, '\n')
}

// projectClaudeMDContent renders a CLAUDE.md stub from each detected type's
// convention text, which names its build and test commands, or nil when no
// detected type has any.
func projectClaudeMDContent(name string, types []ProjectType) []byte {
	var sections []string
	for _, pt := range types {
		if s := conventionText(pt); s != "" {
			sections = append(sections, s)
		}
	}
	if len(sections) == 0 {
		return nil
	}
	return []byte(fmt.Sprintf("# %s\n\n"+
		"Created by muxcode at session start from project detection (MUX-207). Review\n"+
		"it, correct what detection guessed wrong, and commit it if you keep it.\n\n%s\n",
		name, strings.Join(sections, "\n\n")))
}

// createExclusive writes data to a new file at p, creating its directory.
// It returns false with no error when p already exists, symlink included.
func createExclusive(p string, data []byte) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(p)
		return false, err
	}
	if err := f.Close(); err != nil {
		os.Remove(p)
		return false, err
	}
	return true, nil
}

// pathPresent reports whether anything, a dangling symlink included, is at p.
func pathPresent(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// announceProjectInit records an init in the lifecycle log and tells edit
// which files are new, so the user reviews and commits them. Edit is told only
// when something was created: a skip repeats on every launch and is logged,
// not sent.
func announceProjectInit(session string, res ProjectInitResult) {
	if len(res.Skipped) > 0 {
		LogLifecycle(session, "info", "launcher", "project-init-skipped", strings.Join(res.Skipped, "; "))
	}
	if len(res.Created) == 0 {
		return
	}
	files := strings.Join(res.Created, ", ")
	LogLifecycle(session, "info", "launcher", "project-init", "created "+files+" (untracked)")
	payload := "Session start created " + files + " from project detection (MUX-207). They are untracked: tell the user to review, edit and commit them — never commit them yourself."
	if len(res.Skipped) > 0 {
		payload += " Not created: " + strings.Join(res.Skipped, "; ") + "."
	}
	_ = SendNoCC(session, Message{
		ID:      NewMsgID("launcher"),
		TS:      time.Now().Unix(),
		From:    "launcher",
		To:      "edit",
		Type:    "request",
		Action:  "project-init",
		Payload: payload,
	})
}
